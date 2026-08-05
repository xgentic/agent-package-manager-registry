package server

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
	"github.com/xgentic/agent-package-manager-registry/internal/domain/archive"
)

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, req packageRequest) {
	download, err := s.query.OpenDownload(r.Context(), req.Repository, req.Package, req.Version)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	defer func() { _ = download.Body.Close() }()

	version := download.Version

	// The media type recorded at publish, replayed. No transcoding, ever:
	// converting the bytes would break the digest every lockfile recorded
	// (FR-07).
	w.Header().Set("Content-Type", version.MediaType)
	w.Header().Set("Content-Length", strconv.FormatInt(download.Size, 10))

	// RFC 3230: the *raw* digest, base64-encoded — not the hex form that goes
	// in `digest` fields. Clients verify against /versions, not this header.
	w.Header().Set("Digest", "sha256="+base64.StdEncoding.EncodeToString(version.Digest.Bytes()))
	w.Header().Set("ETag", `"`+version.Digest.String()+`"`)

	// Immutable by construction: this tuple's bytes can never change (FR-11).
	w.Header().Set("Cache-Control", "max-age=86400, immutable")

	// TR-21: archives must never render in the browser origin the UI shares.
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", downloadFileName(req.Package, req.Version, version.MediaType)))
	w.Header().Set("X-Content-Type-Options", "nosniff")

	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, download.Body); err != nil {
		// The status line is already sent, so this cannot become a Problem
		// response; a truncated body is all the client can be told.
		s.log.Warn("download interrupted",
			"error", err,
			"request_id", requestIDFrom(r.Context()),
			"package", req.Package.String(),
			"version", req.Version.String(),
		)
	}
}

// downloadFileName builds `<repo>-<version>.<ext>`.
//
// Both components are archive-adjacent, attacker-influenced strings heading
// into a response header, so anything outside a conservative alphabet is
// replaced rather than escaped (TR-20).
func downloadFileName(id domain.Identity, version domain.Version, mediaType string) string {
	extension := ".zip"
	if mediaType == archive.MediaTypeGzip {
		extension = ".tar.gz"
	}
	return safeFileNamePart(id.Repo()) + "-" + safeFileNamePart(version.String()) + extension
}

func safeFileNamePart(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "package"
	}
	return b.String()
}
