// Package service holds the application services — publish, query and
// repository management — plus the interfaces they consume.
//
// The interfaces live here, beside their consumer, rather than in a `ports`
// package (Go idiom; docs/specs/03-architecture.md §3). Implementations are in
// internal/store/.
package service

import (
	"context"
	"io"
	"time"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
)

// Visibility drives anonymous read. MVP 1 enforces nothing — every endpoint is
// open — but the column and the value ship now so MVP 3 adds enforcement rather
// than a migration plus enforcement (FR-24).
type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
)

// Repository is one independent namespace, named in the base URL
// (`/api/agentpackages/{repository}`, ADR-0009).
type Repository struct {
	ID         string
	Name       string
	Visibility Visibility
	QuotaBytes int64 // 0 means unlimited; quotas are enforced in MVP 4
	CreatedAt  time.Time
}

// Version is a published version as stored. It is the source for both the
// /versions list and the 201 publish response.
type Version struct {
	Package      domain.Identity
	Version      domain.Version
	Digest       domain.Digest
	SizeBytes    int64
	MediaType    string
	PublishedAt  time.Time
	ManifestJSON []byte
}

// NewVersion is the row a successful publish commits.
type NewVersion struct {
	Package      domain.Identity
	Version      domain.Version
	Digest       domain.Digest
	SizeBytes    int64
	MediaType    string
	PublishedAt  time.Time
	ManifestJSON []byte
}

// MetadataStore is the transactional metadata side of the registry.
//
// Every package-scoped method takes a repository, so there is no way to spell a
// cross-repository query: the signature refuses it rather than review catching
// it (ADR-0009).
//
// Implementations translate driver errors into the domain taxonomy before
// returning — a caller sees domain.ErrVersionConflict, never a driver's
// UNIQUE-constraint string.
type MetadataStore interface {
	// CreateRepository fails with domain.ErrVersionConflict semantics if the
	// name is taken; the operator CLI reports that as a plain message.
	CreateRepository(ctx context.Context, name string, visibility Visibility, quotaBytes int64) (Repository, error)
	GetRepository(ctx context.Context, name string) (Repository, error)
	ListRepositories(ctx context.Context) ([]Repository, error)

	// ListVersions returns every version of a package, newest publish first.
	// An unknown package is domain.ErrNotFound; a known package always has at
	// least one version, because package rows are created by publish.
	ListVersions(ctx context.Context, repositoryID string, id domain.Identity) ([]Version, error)
	GetVersion(ctx context.Context, repositoryID string, id domain.Identity, v domain.Version) (Version, error)

	// CreateVersion commits the package row (creating it on first publish), the
	// blob row and the version row in one transaction. A repeat tuple returns
	// an error satisfying errors.Is(err, domain.ErrVersionConflict), raised by
	// the unique constraint rather than a prior SELECT (TR-08).
	CreateVersion(ctx context.Context, repositoryID string, v NewVersion) (Version, error)

	// Ping backs GET /ready.
	Ping(ctx context.Context) error
}

// BlobStore holds archive bytes, content-addressed by digest (TR-04, TR-05).
// It never mutates an existing object: a Put of a digest that is already stored
// is a no-op, which is what makes publish idempotent at the bytes layer.
type BlobStore interface {
	Put(ctx context.Context, d domain.Digest, src io.Reader) error
	Open(ctx context.Context, d domain.Digest) (io.ReadSeekCloser, int64, error)
	Stat(ctx context.Context, d domain.Digest) (int64, error)
	Delete(ctx context.Context, d domain.Digest) error
	Ping(ctx context.Context) error
}

// Clock exists so published_at is deterministic in tests — publish assertions
// compare exact response bodies.
type Clock interface {
	Now() time.Time
}

// IDGenerator exists for the same reason as Clock.
type IDGenerator interface {
	NewID() string
}
