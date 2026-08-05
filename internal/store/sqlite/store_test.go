package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
	"github.com/xgentic/agent-package-manager-registry/internal/service"
	"github.com/xgentic/agent-package-manager-registry/internal/store/sqlite"
)

func newStore(t *testing.T) *sqlite.Store {
	t.Helper()

	store, err := sqlite.Open(filepath.Join(t.TempDir(), "registry.db"), service.RandomIDs{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return store
}

func newRepository(t *testing.T, store *sqlite.Store) service.Repository {
	t.Helper()

	repo, err := store.CreateRepository(t.Context(), "corp-main", service.VisibilityPrivate, 0)
	if err != nil {
		t.Fatalf("CreateRepository() error = %v", err)
	}
	return repo
}

func identity(t *testing.T, raw string) domain.Identity {
	t.Helper()

	id, err := domain.ParseIdentity(raw)
	if err != nil {
		t.Fatalf("ParseIdentity(%q) error = %v", raw, err)
	}
	return id
}

func newVersion(id domain.Identity, version string, digestHex string, at time.Time) service.NewVersion {
	return service.NewVersion{
		Package:     id,
		Version:     domain.Version(version),
		Digest:      domain.Digest("sha256:" + digestHex),
		SizeBytes:   1024,
		MediaType:   "application/zip",
		PublishedAt: at,
	}
}

func digestOf(n byte) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = "0123456789abcdef"[n%16]
	}
	return string(out)
}

// T1.3: running migrate twice must be a no-op, because `serve` runs it on every
// boot (FR-43).
func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatalf("second Migrate() error = %v, want nil", err)
	}
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatalf("third Migrate() error = %v, want nil", err)
	}
}

func TestRepositoryLifecycle(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	created := newRepository(t, store)

	got, err := store.GetRepository(t.Context(), "corp-main")
	if err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}
	if got.ID != created.ID || got.Name != "corp-main" {
		t.Errorf("GetRepository() = %+v, want the created repository", got)
	}

	if _, err := store.GetRepository(t.Context(), "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetRepository(unknown) error = %v, want ErrNotFound", err)
	}

	if _, err := store.CreateRepository(t.Context(), "corp-main", service.VisibilityPublic, 0); err == nil {
		t.Error("CreateRepository() with a duplicate name error = nil, want a rejection")
	}

	repos, err := store.ListRepositories(t.Context())
	if err != nil {
		t.Fatalf("ListRepositories() error = %v", err)
	}
	if len(repos) != 1 {
		t.Errorf("ListRepositories() = %d repositories, want 1", len(repos))
	}
}

func TestCreateAndReadVersions(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	repo := newRepository(t, store)
	id := identity(t, "acme/web-skills")
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	// The package row does not exist yet: publish creates it.
	if _, err := store.ListVersions(t.Context(), repo.ID, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ListVersions(unknown package) error = %v, want ErrNotFound", err)
	}

	first, err := store.CreateVersion(t.Context(), repo.ID, newVersion(id, "1.0.0", digestOf(1), now))
	if err != nil {
		t.Fatalf("CreateVersion() error = %v", err)
	}
	if _, err := store.CreateVersion(t.Context(), repo.ID, newVersion(id, "1.1.0", digestOf(2), now.Add(time.Hour))); err != nil {
		t.Fatalf("CreateVersion() error = %v", err)
	}

	versions, err := store.ListVersions(t.Context(), repo.ID, id)
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("ListVersions() = %d versions, want 2", len(versions))
	}
	// FR-18: publish-time descending.
	if versions[0].Version != "1.1.0" {
		t.Errorf("first listed version = %q, want the newest publish (1.1.0)", versions[0].Version)
	}

	got, err := store.GetVersion(t.Context(), repo.ID, id, "1.0.0")
	if err != nil {
		t.Fatalf("GetVersion() error = %v", err)
	}
	if got.Digest != first.Digest {
		t.Errorf("GetVersion().Digest = %q, want %q", got.Digest, first.Digest)
	}
	if !got.PublishedAt.Equal(now) {
		t.Errorf("GetVersion().PublishedAt = %v, want %v", got.PublishedAt, now)
	}

	if _, err := store.GetVersion(t.Context(), repo.ID, id, "9.9.9"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetVersion(unknown) error = %v, want ErrNotFound", err)
	}
}

// FR-11 / TR-08: a repeat tuple is a conflict whether or not the bytes differ,
// and the 409 body needs the previous publish's digest and time.
func TestRepeatPublishConflicts(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	repo := newRepository(t, store)
	id := identity(t, "acme/web-skills")
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	if _, err := store.CreateVersion(t.Context(), repo.ID, newVersion(id, "1.0.0", digestOf(1), now)); err != nil {
		t.Fatalf("CreateVersion() error = %v", err)
	}

	for _, tt := range []struct {
		name   string
		digest string
	}{
		{"identical bytes", digestOf(1)},
		{"different bytes", digestOf(3)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.CreateVersion(t.Context(), repo.ID, newVersion(id, "1.0.0", tt.digest, now.Add(time.Hour)))
			if !errors.Is(err, domain.ErrVersionConflict) {
				t.Fatalf("CreateVersion() error = %v, want ErrVersionConflict", err)
			}

			var conflict *domain.ConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("error type = %T, want *domain.ConflictError", err)
			}
			if conflict.PreviousDigest != domain.Digest("sha256:"+digestOf(1)) {
				t.Errorf("PreviousDigest = %q, want the first publish's digest", conflict.PreviousDigest)
			}
			if !conflict.PreviousPublish.Equal(now) {
				t.Errorf("PreviousPublish = %v, want %v", conflict.PreviousPublish, now)
			}
		})
	}
}

// FR-17: selectors are case-sensitive, which is why the column is BINARY. If
// this fails, `V1.0` and `v1.0` have merged and a legitimate publish is being
// rejected as a conflict.
func TestVersionSelectorsAreCaseSensitive(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	repo := newRepository(t, store)
	id := identity(t, "acme/web-skills")
	now := time.Now().UTC()

	if _, err := store.CreateVersion(t.Context(), repo.ID, newVersion(id, "v1.0", digestOf(1), now)); err != nil {
		t.Fatalf("CreateVersion(v1.0) error = %v", err)
	}
	if _, err := store.CreateVersion(t.Context(), repo.ID, newVersion(id, "V1.0", digestOf(2), now)); err != nil {
		t.Fatalf("CreateVersion(V1.0) error = %v, want it accepted as a distinct version", err)
	}

	if _, err := store.GetVersion(t.Context(), repo.ID, id, "V1.0"); err != nil {
		t.Errorf("GetVersion(V1.0) error = %v", err)
	}
}

// ADR-0009: repositories are independent namespaces, so the same identity and
// version may exist in two of them with different bytes.
func TestRepositoriesAreIndependentNamespaces(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	one := newRepository(t, store)
	two, err := store.CreateRepository(t.Context(), "sandbox", service.VisibilityPublic, 0)
	if err != nil {
		t.Fatalf("CreateRepository() error = %v", err)
	}

	id := identity(t, "acme/web-skills")
	now := time.Now().UTC()

	if _, err := store.CreateVersion(t.Context(), one.ID, newVersion(id, "1.0.0", digestOf(1), now)); err != nil {
		t.Fatalf("CreateVersion() error = %v", err)
	}
	if _, err := store.CreateVersion(t.Context(), two.ID, newVersion(id, "1.0.0", digestOf(2), now)); err != nil {
		t.Fatalf("CreateVersion() in a second repository error = %v, want it accepted", err)
	}

	got, err := store.GetVersion(t.Context(), two.ID, id, "1.0.0")
	if err != nil {
		t.Fatalf("GetVersion() error = %v", err)
	}
	if got.Digest != domain.Digest("sha256:"+digestOf(2)) {
		t.Errorf("GetVersion() returned %q; a repository must not see another's bytes", got.Digest)
	}
}

// TR-08: the conflict is a database constraint, so concurrency cannot produce
// two winners.
func TestConcurrentPublishesOfOneVersionProduceOneWinner(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	repo := newRepository(t, store)
	id := identity(t, "acme/web-skills")

	const attempts = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		conflicts int
		other     []error
	)

	wg.Add(attempts)
	for i := range attempts {
		go func(i int) {
			defer wg.Done()

			_, err := store.CreateVersion(context.Background(), repo.ID,
				newVersion(id, "1.0.0", digestOf(byte(i)), time.Now().UTC()))

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, domain.ErrVersionConflict):
				conflicts++
			default:
				other = append(other, err)
			}
		}(i)
	}
	wg.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected errors: %v", other)
	}
	if succeeded != 1 {
		t.Errorf("%d publishes succeeded, want exactly 1", succeeded)
	}
	if conflicts != attempts-1 {
		t.Errorf("%d conflicts, want %d", conflicts, attempts-1)
	}
}
