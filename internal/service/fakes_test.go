package service_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
	"github.com/xgentic/agent-package-manager-registry/internal/service"
)

// In-memory doubles for both stores (T1.6), so service tests run with zero I/O
// beyond the upload temp file that the pipeline is defined in terms of.
//
// The fakes reproduce the two behaviours the services actually depend on: the
// unique-constraint conflict, and create-if-absent blob storage. A fake that
// let a duplicate through would make the tests agree with each other and
// disagree with SQLite.

type fakeMeta struct {
	mu           sync.Mutex
	repositories map[string]service.Repository
	// versions is keyed repositoryID → identity → version.
	versions map[string]map[string]map[domain.Version]service.Version
	order    map[string][]domain.Version // insertion order per repo+identity

	failPing error
}

func newFakeMeta() *fakeMeta {
	return &fakeMeta{
		repositories: map[string]service.Repository{},
		versions:     map[string]map[string]map[domain.Version]service.Version{},
		order:        map[string][]domain.Version{},
	}
}

var _ service.MetadataStore = (*fakeMeta)(nil)

func (f *fakeMeta) CreateRepository(_ context.Context, name string, visibility service.Visibility, quota int64) (service.Repository, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.repositories[name]; exists {
		return service.Repository{}, fmt.Errorf("%w: repository %q already exists", domain.ErrBadRequest, name)
	}
	repo := service.Repository{
		ID:         "repo-" + name,
		Name:       name,
		Visibility: visibility,
		QuotaBytes: quota,
		CreatedAt:  time.Unix(0, 0).UTC(),
	}
	f.repositories[name] = repo
	return repo, nil
}

func (f *fakeMeta) GetRepository(_ context.Context, name string) (service.Repository, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	repo, ok := f.repositories[name]
	if !ok {
		return service.Repository{}, fmt.Errorf("%w: repository %q", domain.ErrNotFound, name)
	}
	return repo, nil
}

func (f *fakeMeta) ListRepositories(context.Context) ([]service.Repository, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]service.Repository, 0, len(f.repositories))
	for _, r := range f.repositories {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeMeta) ListVersions(_ context.Context, repositoryID string, id domain.Identity) ([]service.Version, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	byIdentity, ok := f.versions[repositoryID]
	if !ok {
		return nil, fmt.Errorf("%w: package %q", domain.ErrNotFound, id)
	}
	stored, ok := byIdentity[id.String()]
	if !ok {
		return nil, fmt.Errorf("%w: package %q", domain.ErrNotFound, id)
	}

	// Newest publish first, matching the SQL ordering (FR-18).
	key := repositoryID + "\x00" + id.String()
	out := []service.Version{}
	for i := len(f.order[key]) - 1; i >= 0; i-- {
		out = append(out, stored[f.order[key][i]])
	}
	return out, nil
}

func (f *fakeMeta) GetVersion(_ context.Context, repositoryID string, id domain.Identity, v domain.Version) (service.Version, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	stored, ok := f.versions[repositoryID][id.String()][v]
	if !ok {
		return service.Version{}, fmt.Errorf("%w: %s#%s", domain.ErrNotFound, id, v)
	}
	return stored, nil
}

func (f *fakeMeta) CreateVersion(_ context.Context, repositoryID string, in service.NewVersion) (service.Version, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.versions[repositoryID] == nil {
		f.versions[repositoryID] = map[string]map[domain.Version]service.Version{}
	}
	if f.versions[repositoryID][in.Package.String()] == nil {
		f.versions[repositoryID][in.Package.String()] = map[domain.Version]service.Version{}
	}

	// The unique constraint, reproduced: a repeat tuple conflicts whether or
	// not the bytes match (FR-11).
	if existing, exists := f.versions[repositoryID][in.Package.String()][in.Version]; exists {
		return service.Version{}, &domain.ConflictError{
			Package:         in.Package.String(),
			Version:         in.Version.String(),
			PreviousDigest:  existing.Digest,
			PreviousPublish: existing.PublishedAt,
		}
	}

	stored := service.Version(in)
	f.versions[repositoryID][in.Package.String()][in.Version] = stored

	key := repositoryID + "\x00" + in.Package.String()
	f.order[key] = append(f.order[key], in.Version)
	return stored, nil
}

func (f *fakeMeta) Ping(context.Context) error { return f.failPing }

type fakeBlobs struct {
	mu       sync.Mutex
	objects  map[domain.Digest][]byte
	failPing error
	failPut  error
}

func newFakeBlobs() *fakeBlobs {
	return &fakeBlobs{objects: map[domain.Digest][]byte{}}
}

var _ service.BlobStore = (*fakeBlobs)(nil)

func (f *fakeBlobs) Put(_ context.Context, d domain.Digest, src io.Reader) error {
	if f.failPut != nil {
		return f.failPut
	}

	body, err := io.ReadAll(src)
	if err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// Create-if-absent: an existing object is never overwritten (TR-04).
	if _, exists := f.objects[d]; exists {
		return nil
	}
	f.objects[d] = body
	return nil
}

func (f *fakeBlobs) Open(_ context.Context, d domain.Digest) (io.ReadSeekCloser, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	body, ok := f.objects[d]
	if !ok {
		return nil, 0, fmt.Errorf("%w: blob %s", domain.ErrNotFound, d)
	}
	return nopSeekCloser{bytes.NewReader(body)}, int64(len(body)), nil
}

func (f *fakeBlobs) Stat(_ context.Context, d domain.Digest) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	body, ok := f.objects[d]
	if !ok {
		return 0, fmt.Errorf("%w: blob %s", domain.ErrNotFound, d)
	}
	return int64(len(body)), nil
}

func (f *fakeBlobs) Delete(_ context.Context, d domain.Digest) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.objects, d)
	return nil
}

func (f *fakeBlobs) Ping(context.Context) error { return f.failPing }

func (f *fakeBlobs) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.objects)
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }
