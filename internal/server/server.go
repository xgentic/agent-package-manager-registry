// Package server wires the registry's HTTP routes.
//
// It deliberately holds no knowledge of how the process is started or
// configured; main owns that. Everything here is reachable from a test with
// httptest, without binding a port (TR-02).
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/xgentic/agent-package-manager-registry/internal/config"
	"github.com/xgentic/agent-package-manager-registry/internal/service"
)

// routePrefix is the base URL shape (ADR-0009).
const routePrefix = "/api/agentpackages/{repository}/v1"

// packagePrefix is where both identity path forms hang off (ADR-0007).
const packagePrefix = routePrefix + "/packages"

// Deps are what the handlers need. Everything is an interface or a value; the
// server constructs nothing itself, which is what keeps it testable in-process.
type Deps struct {
	Log          *slog.Logger
	Config       config.Config
	Publish      *service.PublishService
	Query        *service.QueryService
	Repositories *service.RepositoryService
	Meta         service.MetadataStore
	Blobs        service.BlobStore
}

// Server carries the dependencies shared by the handlers.
type Server struct {
	log          *slog.Logger
	cfg          config.Config
	publish      *service.PublishService
	query        *service.QueryService
	repositories *service.RepositoryService
	meta         service.MetadataStore
	blobs        service.BlobStore
}

// New returns the registry's HTTP handler.
func New(deps Deps) http.Handler {
	s := &Server{
		log:          deps.Log,
		cfg:          deps.Config,
		publish:      deps.Publish,
		query:        deps.Query,
		repositories: deps.Repositories,
		meta:         deps.Meta,
		blobs:        deps.Blobs,
	}
	if s.log == nil {
		s.log = slog.Default()
	}

	mux := http.NewServeMux()

	// Operational endpoints. Unauthenticated by design, and never rate-limited
	// (§7.2, §6).
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /ready", s.handleReady)

	// The three endpoints of MS-API §3, each registered in both identity path
	// forms. See identity.go for why that is two registrations rather than one
	// clever pattern.
	s.handlePackageRoutes(mux, http.MethodGet, "/versions", readAction, withoutVersion, s.handleListVersions)
	s.handlePackageRoutes(mux, http.MethodGet, "/versions/{version}/download", readAction, withVersion, s.handleDownload)
	s.handlePackageRoutes(mux, http.MethodPut, "/versions/{version}", publishAction, withVersion, s.handlePublish)

	// "/" is ServeMux's catch-all, so every unrouted request lands here and
	// gets a Problem body rather than net/http's plain-text 404.
	mux.HandleFunc("/", s.handleNotFound)

	// Outermost first: a panic in any of the inner layers still becomes a
	// Problem body, and the request id exists before anything can log or fail.
	return recoverPanics(s.log)(
		requestID(
			s.logRequests(
				s.authenticate(mux))))
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.writeProblem(w, r, http.StatusNotFound, typeNotFound, "Not Found", "No such route.", nil)
}

// writeJSON emits a success body. Errors never come through here — they go
// through writeProblem (FR-26).
func (s *Server) writeJSON(w http.ResponseWriter, status int, body any) {
	s.write(w, "application/json; charset=utf-8", status, body)
}

// write marshals before touching the ResponseWriter, so an encoding failure can
// still become a 500 instead of a truncated 200.
func (s *Server) write(w http.ResponseWriter, contentType string, status int, body any) {
	buf, err := json.Marshal(body)
	if err != nil {
		s.log.Error("encoding response", "error", err)
		w.Header().Set("Content-Type", problemContentType)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"title":"Internal Server Error","status":500}`))
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if _, err := w.Write(buf); err != nil {
		s.log.Error("writing response", "error", err)
	}
}
