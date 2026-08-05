// Package sqlite implements service.MetadataStore on SQLite, through stdlib
// database/sql and the pure-Go modernc.org/sqlite driver.
//
// Pure Go is the point: it keeps CGO_ENABLED=0, so the binary stays static and
// cross-compilable (ADR-0002). The trade is stated plainly in TR-31 — SQLite
// makes this deployment single-instance, and it needs a *local* volume.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
	"github.com/xgentic/agent-package-manager-registry/internal/service"

	// Imported for its side effect of registering the "sqlite" database/sql
	// driver, and for the error type isUniqueViolation inspects.
	sqlitedriver "modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"
)

// timeLayout is RFC 3339 in UTC at fixed width, so `ORDER BY published_at` is
// chronological as a string comparison.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

// Store is the SQLite-backed MetadataStore.
type Store struct {
	db  *sql.DB
	ids service.IDGenerator
}

var _ service.MetadataStore = (*Store)(nil)

// Open connects to the database at path, creating the parent directory if
// needed, and applies the pragmas the registry depends on.
//
// The caller still has to run Migrate; `serve` does it on boot, and the
// operator can do it on a stopped registry (ADR-0013).
func Open(path string, ids service.IDGenerator) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("creating database directory: %w", err)
		}
	}

	// WAL for concurrent readers alongside a writer; busy_timeout so a
	// concurrent publish waits instead of failing with SQLITE_BUSY; foreign
	// keys because SQLite leaves them off by default and the schema relies on
	// them.
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// One writer. SQLite serialises writes anyway; capping the pool turns
	// lock contention into queueing inside the process, which is cheaper and
	// far easier to reason about than SQLITE_BUSY retries.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	return &Store{db: db, ids: ids}, nil
}

// DB exposes the handle for the migration runner and for tests.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Migrate applies pending migrations (FR-43).
func (s *Store) Migrate(ctx context.Context) error { return Migrate(ctx, s.db) }

func (s *Store) CreateRepository(ctx context.Context, name string, visibility service.Visibility, quotaBytes int64) (service.Repository, error) {
	repo := service.Repository{
		ID:         s.ids.NewID(),
		Name:       name,
		Visibility: visibility,
		QuotaBytes: quotaBytes,
		CreatedAt:  time.Now().UTC(),
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO repositories (id, name, visibility, quota_bytes, created_at) VALUES (?, ?, ?, ?, ?)`,
		repo.ID, repo.Name, string(repo.Visibility), repo.QuotaBytes, formatTime(repo.CreatedAt))
	switch {
	case isUniqueViolation(err):
		return service.Repository{}, fmt.Errorf("%w: repository %q already exists", domain.ErrBadRequest, name)
	case err != nil:
		return service.Repository{}, fmt.Errorf("creating repository: %w", err)
	}
	return repo, nil
}

func (s *Store) GetRepository(ctx context.Context, name string) (service.Repository, error) {
	var (
		repo       service.Repository
		visibility string
		createdAt  string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, visibility, quota_bytes, created_at FROM repositories WHERE name = ?`, name).
		Scan(&repo.ID, &repo.Name, &visibility, &repo.QuotaBytes, &createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return service.Repository{}, fmt.Errorf("%w: repository %q", domain.ErrNotFound, name)
	case err != nil:
		return service.Repository{}, fmt.Errorf("reading repository: %w", err)
	}

	repo.Visibility = service.Visibility(visibility)
	repo.CreatedAt = parseTime(createdAt)
	return repo, nil
}

func (s *Store) ListRepositories(ctx context.Context) ([]service.Repository, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, visibility, quota_bytes, created_at FROM repositories ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing repositories: %w", err)
	}
	defer func() { _ = rows.Close() }()

	repos := []service.Repository{}
	for rows.Next() {
		var (
			repo       service.Repository
			visibility string
			createdAt  string
		)
		if err := rows.Scan(&repo.ID, &repo.Name, &visibility, &repo.QuotaBytes, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning repository: %w", err)
		}
		repo.Visibility = service.Visibility(visibility)
		repo.CreatedAt = parseTime(createdAt)
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

func (s *Store) ListVersions(ctx context.Context, repositoryID string, id domain.Identity) ([]service.Version, error) {
	packageID, err := s.packageID(ctx, repositoryID, id)
	if err != nil {
		return nil, err
	}

	// Publish-time descending (FR-18). rowid breaks ties so the order is
	// stable when two publishes share a timestamp.
	rows, err := s.db.QueryContext(ctx, `
		SELECT version, digest, size_bytes, media_type, published_at, manifest_json
		FROM versions
		WHERE package_id = ?
		ORDER BY published_at DESC, rowid DESC`, packageID)
	if err != nil {
		return nil, fmt.Errorf("listing versions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Never nil: the `versions` field is always emitted, as [] when empty
	// (FR-03).
	versions := []service.Version{}
	for rows.Next() {
		v, err := scanVersion(rows, id)
		if err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func (s *Store) GetVersion(ctx context.Context, repositoryID string, id domain.Identity, version domain.Version) (service.Version, error) {
	packageID, err := s.packageID(ctx, repositoryID, id)
	if err != nil {
		return service.Version{}, err
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT version, digest, size_bytes, media_type, published_at, manifest_json
		FROM versions
		WHERE package_id = ? AND version = ?`, packageID, version.String())

	v, err := scanVersion(row, id)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Version{}, fmt.Errorf("%w: %s#%s", domain.ErrNotFound, id, version)
	}
	return v, err
}

func (s *Store) CreateVersion(ctx context.Context, repositoryID string, in service.NewVersion) (service.Version, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return service.Version{}, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	packageID, err := s.upsertPackage(ctx, tx, repositoryID, in.Package, in.PublishedAt)
	if err != nil {
		return service.Version{}, err
	}

	// Content-addressed: several versions may legitimately share one blob.
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO blobs (digest, size_bytes, created_at) VALUES (?, ?, ?)`,
		in.Digest.String(), in.SizeBytes, formatTime(in.PublishedAt)); err != nil {
		return service.Version{}, fmt.Errorf("recording blob: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO versions (id, package_id, version, digest, size_bytes, media_type, published_at, manifest_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ids.NewID(), packageID, in.Version.String(), in.Digest.String(), in.SizeBytes,
		in.MediaType, formatTime(in.PublishedAt), string(in.ManifestJSON))
	if isUniqueViolation(err) {
		// The conflict comes from the constraint, never from a prior SELECT
		// (TR-08). Only once it has fired do we read the winning row, to fill
		// in the 409 body (FR-11).
		_ = tx.Rollback()
		return service.Version{}, s.conflictFor(ctx, repositoryID, in)
	}
	if err != nil {
		return service.Version{}, fmt.Errorf("recording version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return service.Version{}, fmt.Errorf("committing publish: %w", err)
	}

	return service.Version(in), nil
}

// conflictFor builds the 409 payload from whichever publish won.
func (s *Store) conflictFor(ctx context.Context, repositoryID string, in service.NewVersion) error {
	conflict := &domain.ConflictError{
		Package: in.Package.String(),
		Version: in.Version.String(),
	}
	if existing, err := s.GetVersion(ctx, repositoryID, in.Package, in.Version); err == nil {
		conflict.PreviousDigest = existing.Digest
		conflict.PreviousPublish = existing.PublishedAt
	}
	return conflict
}

func (s *Store) upsertPackage(ctx context.Context, tx *sql.Tx, repositoryID string, id domain.Identity, now time.Time) (string, error) {
	var packageID string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM packages WHERE repository_id = ? AND identity = ?`, repositoryID, id.String()).
		Scan(&packageID)
	if err == nil {
		return packageID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("reading package: %w", err)
	}

	// First publish of this identity in this repository creates the package.
	// There is no "create package" endpoint; publish is the only way in.
	packageID = s.ids.NewID()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO packages (id, repository_id, identity, owner, repo, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		packageID, repositoryID, id.String(), id.Owner(), id.Repo(), formatTime(now)); err != nil {
		return "", fmt.Errorf("creating package: %w", err)
	}
	return packageID, nil
}

func (s *Store) packageID(ctx context.Context, repositoryID string, id domain.Identity) (string, error) {
	var packageID string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM packages WHERE repository_id = ? AND identity = ?`, repositoryID, id.String()).
		Scan(&packageID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", fmt.Errorf("%w: package %q", domain.ErrNotFound, id)
	case err != nil:
		return "", fmt.Errorf("reading package: %w", err)
	}
	return packageID, nil
}

// scanner is the shared surface of *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanVersion(row scanner, id domain.Identity) (service.Version, error) {
	var (
		version      string
		digest       string
		publishedAt  string
		manifestJSON sql.NullString
		v            service.Version
	)
	if err := row.Scan(&version, &digest, &v.SizeBytes, &v.MediaType, &publishedAt, &manifestJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.Version{}, err
		}
		return service.Version{}, fmt.Errorf("scanning version: %w", err)
	}

	v.Package = id
	v.Version = domain.Version(version)
	v.Digest = domain.Digest(digest)
	v.PublishedAt = parseTime(publishedAt)
	if manifestJSON.Valid {
		v.ManifestJSON = []byte(manifestJSON.String)
	}
	return v, nil
}

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) time.Time {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		// Fall back to the looser form so a row written by an older layout, or
		// by hand, still reads rather than reading as the zero time.
		if t, err = time.Parse(time.RFC3339Nano, s); err != nil {
			return time.Time{}
		}
	}
	return t.UTC()
}

// isUniqueViolation recognises the constraint failure that *is* the
// immutability guarantee, so callers see a typed conflict rather than a driver
// error (TR-08).
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}

	var sqliteErr *sqlitedriver.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlitelib.SQLITE_CONSTRAINT_UNIQUE, sqlitelib.SQLITE_CONSTRAINT_PRIMARYKEY:
			return true
		}
	}
	// Belt and braces: the driver's error type is not part of its compatibility
	// promise, and misreading a conflict as a 500 would be a bad failure.
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
