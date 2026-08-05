package server

import (
	"net/http"
	"time"

	"github.com/xgentic/agent-package-manager-registry/internal/service"
)

// timestampLayout is ISO 8601 in UTC, as MS-API §3.1 specifies for
// `published_at`.
const timestampLayout = time.RFC3339

// versionsResponse is `GET /versions`.
//
// Every field is tagged. An untagged `Versions` would marshal as "Versions",
// the reference client would ignore it without erroring, and the install would
// simply resolve nothing (FR-04, TR-23).
type versionsResponse struct {
	Package  string        `json:"package"`
	Versions []versionItem `json:"versions"`
}

type versionItem struct {
	Version     string `json:"version"`
	Digest      string `json:"digest"`
	PublishedAt string `json:"published_at"`
	SizeBytes   int64  `json:"size_bytes"`
}

func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request, req packageRequest) {
	versions, err := s.query.ListVersions(r.Context(), req.Repository, req.Package)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	// The complete set, in publish-time descending order. No pagination: a
	// truncated list does not fail a client, it silently resolves its semver
	// range against the wrong data (FR-19).
	items := make([]versionItem, 0, len(versions))
	for _, v := range versions {
		items = append(items, toVersionItem(v))
	}

	w.Header().Set("Cache-Control", "max-age=60, public")
	s.writeJSON(w, http.StatusOK, versionsResponse{
		// The canonical identity, so both path forms echo the same string
		// (FR-16).
		Package: req.Package.String(),
		// `items` is built with make(), never nil, so this marshals as [] and
		// never as null — the field is required and must not be omitted
		// (FR-03).
		Versions: items,
	})
}

func toVersionItem(v service.Version) versionItem {
	return versionItem{
		Version:     v.Version.String(),
		Digest:      v.Digest.String(),
		PublishedAt: v.PublishedAt.UTC().Format(timestampLayout),
		SizeBytes:   v.SizeBytes,
	}
}
