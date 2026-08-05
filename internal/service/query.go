package service

import (
	"context"
	"fmt"
	"io"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
)

// QueryService serves the two read endpoints.
type QueryService struct {
	meta  MetadataStore
	blobs BlobStore
}

func NewQueryService(meta MetadataStore, blobs BlobStore) *QueryService {
	return &QueryService{meta: meta, blobs: blobs}
}

// ListVersions returns the **complete** version set, newest publish first.
//
// There is no pagination and no truncation, deliberately: the client resolves
// semver ranges against this list, so a partial answer does not fail — it
// silently resolves to the wrong version (FR-19).
func (s *QueryService) ListVersions(ctx context.Context, repo Repository, id domain.Identity) ([]Version, error) {
	return s.meta.ListVersions(ctx, repo.ID, id)
}

// GetVersion returns one version's metadata.
func (s *QueryService) GetVersion(ctx context.Context, repo Repository, id domain.Identity, v domain.Version) (Version, error) {
	return s.meta.GetVersion(ctx, repo.ID, id, v)
}

// Download is an open archive, ready to stream.
type Download struct {
	Version Version
	Body    io.ReadSeekCloser
	Size    int64
}

// OpenDownload streams the stored bytes with the media type recorded at publish.
//
// No transcoding, ever: the bytes served must hash to the advertised digest, so
// converting a zip to a tarball here would break every lockfile that recorded
// it (FR-07, req-rg-001).
func (s *QueryService) OpenDownload(ctx context.Context, repo Repository, id domain.Identity, v domain.Version) (Download, error) {
	version, err := s.meta.GetVersion(ctx, repo.ID, id, v)
	if err != nil {
		return Download{}, err
	}

	body, size, err := s.blobs.Open(ctx, version.Digest)
	if err != nil {
		// A row without bytes is the failure mode publish ordering exists to
		// prevent (TR-07). If it happens anyway, it is an internal fault and
		// not a 404 — the version genuinely was published, and telling the
		// client "no such version" would send it looking for a bug in its own
		// lockfile.
		//
		// The blob error is deliberately *not* wrapped: it is a
		// domain.ErrNotFound, and wrapping it would make errors.Is match
		// ErrNotFound first and turn this into exactly the 404 the sentence
		// above rules out.
		//nolint:errorlint // %v, not %w — see above
		return Download{}, fmt.Errorf("%w: version %s#%s has metadata but no stored bytes: %v", domain.ErrInternal, id, v, err)
	}

	if size != version.SizeBytes {
		_ = body.Close()
		return Download{}, fmt.Errorf("%w: stored size %d does not match recorded size %d for %s#%s",
			domain.ErrInternal, size, version.SizeBytes, id, v)
	}
	return Download{Version: version, Body: body, Size: size}, nil
}
