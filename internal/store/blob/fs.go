// Package blob implements service.BlobStore over a local filesystem, addressing
// objects by their SHA-256 digest (TR-04, ADR-0003).
//
// The layout fans out on the first two byte-pairs of the digest:
//
//	<root>/sha256/ab/cd/abcd…<64 hex>
//
// so no single directory accumulates every archive in the registry.
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
	"github.com/xgentic/agent-package-manager-registry/internal/service"
)

// FS is a content-addressed blob store rooted at a directory.
type FS struct {
	root string
}

var _ service.BlobStore = (*FS)(nil)

// NewFS creates the root directory if it does not exist.
func NewFS(root string) (*FS, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("creating blob root: %w", err)
	}
	return &FS{root: root}, nil
}

// Root is where the store writes. Exposed for the readiness check and for
// operator tooling.
func (f *FS) Root() string { return f.root }

// Put stores src under d. It is create-if-absent: an object already present is
// left exactly as it is, which is what makes the store non-mutating (TR-04).
//
// The write goes to a temp file in the same directory and is committed with
// os.Rename, so a crash mid-write can never leave a partial object at a digest
// path — a reader either sees the whole object or no object.
func (f *FS) Put(ctx context.Context, d domain.Digest, src io.Reader) error {
	final, err := f.path(d)
	if err != nil {
		return err
	}

	if _, err := os.Stat(final); err == nil {
		return nil // already stored; digests are immutable, so nothing to do
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		return fmt.Errorf("creating blob directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(final), ".incoming-*")
	if err != nil {
		return fmt.Errorf("creating blob temp file: %w", err)
	}
	tmpName := tmp.Name()
	// Removing a name that Rename already consumed is a no-op error we ignore;
	// what matters is that a failure path never leaves the temp file behind.
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if _, err := io.Copy(tmp, src); err != nil {
		return fmt.Errorf("writing blob: %w", err)
	}
	// fsync before rename: the rename is only a commit if the bytes are
	// already durable.
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("syncing blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing blob: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("setting blob permissions: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return fmt.Errorf("committing blob: %w", err)
	}

	_ = ctx // the filesystem operations above are not cancellable
	return nil
}

// Open returns a stream over the stored bytes and their size.
func (f *FS) Open(_ context.Context, d domain.Digest) (io.ReadSeekCloser, int64, error) {
	path, err := f.path(d)
	if err != nil {
		return nil, 0, err
	}

	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, fmt.Errorf("%w: blob %s", domain.ErrNotFound, d)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("opening blob: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("stat blob: %w", err)
	}
	return file, info.Size(), nil
}

// Stat reports the stored size without opening a stream.
func (f *FS) Stat(_ context.Context, d domain.Digest) (int64, error) {
	path, err := f.path(d)
	if err != nil {
		return 0, err
	}

	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("%w: blob %s", domain.ErrNotFound, d)
	}
	if err != nil {
		return 0, fmt.Errorf("stat blob: %w", err)
	}
	return info.Size(), nil
}

// Delete removes an object. Nothing in MVP 1 calls it — deletion is refcount
// driven and arrives with `gc` (FR-45) — but the port is complete so the
// adapter is too.
func (f *FS) Delete(_ context.Context, d domain.Digest) error {
	path, err := f.path(d)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("deleting blob: %w", err)
	}
	return nil
}

// Ping backs GET /ready: the blob root must exist and be a directory.
func (f *FS) Ping(context.Context) error {
	info, err := os.Stat(f.root)
	if err != nil {
		return fmt.Errorf("blob store unreachable: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("blob root %q is not a directory", f.root)
	}
	return nil
}

// path maps a digest to its location. It parses the digest rather than trusting
// it: the hex alphabet is what keeps a digest from ever becoming a traversal.
func (f *FS) path(d domain.Digest) (string, error) {
	parsed, err := domain.ParseDigest(d.String())
	if err != nil {
		return "", err
	}
	hex := parsed.Hex()
	return filepath.Join(f.root, "sha256", hex[0:2], hex[2:4], hex), nil
}
