package server

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
	"github.com/xgentic/agent-package-manager-registry/internal/domain/archive"
	"github.com/xgentic/agent-package-manager-registry/internal/service"
)

// publishResponse is the 201 body. Some registries return 201 with an empty
// body and the CLI copes; we always return the full body, because the CLI
// prints `digest` and `published_at` from it (§3.3).
type publishResponse struct {
	Package     string `json:"package"`
	Version     string `json:"version"`
	Digest      string `json:"digest"`
	PublishedAt string `json:"published_at"`
	SizeBytes   int64  `json:"size_bytes"`
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request, req packageRequest) {
	// Media type before the body, as scope was checked before this.
	//
	// Both cheap checks precede any read of the body on purpose: a caller who
	// is not allowed to publish, or is sending a format we do not accept,
	// should not be able to make us ingest 50 MB first (ADR-0011 steps 1–2).
	mediaType, err := archive.AcceptMediaType(s.cfg.AcceptedMediaTypes, r.Header.Get("Content-Type"))
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: %w", domain.ErrUnsupportedMediaType, err))
		return
	}

	// MaxBytesReader stops the read at the cap and, unlike a plain limit, also
	// tells the client. The service enforces the same cap, so a non-HTTP caller
	// is bounded too (TR-10).
	body := http.MaxBytesReader(w, r.Body, s.cfg.MaxArchiveBytes+1)
	defer func() { _ = body.Close() }()

	published, err := s.publish.Publish(r.Context(), service.PublishRequest{
		Repository: req.Repository,
		Package:    req.Package,
		Version:    req.Version,
		MediaType:  mediaType,
		Body:       body,
	})
	if err != nil {
		s.writeError(w, r, translateUploadError(err))
		return
	}

	s.writeJSON(w, http.StatusCreated, publishResponse{
		Package:     published.Package.String(),
		Version:     published.Version.String(),
		Digest:      published.Digest.String(),
		PublishedAt: published.PublishedAt.UTC().Format(timestampLayout),
		SizeBytes:   published.SizeBytes,
	})
}

// translateUploadError maps the one transport-level failure the service cannot
// classify: MaxBytesReader's error, which reaches it as an ordinary read
// failure.
func translateUploadError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return fmt.Errorf("%w: archive exceeds the %d byte limit", domain.ErrPayloadTooLarge, tooLarge.Limit-1)
	}
	return err
}
