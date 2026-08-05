package server

import (
	"net/http"
)

type healthResponse struct {
	Status string `json:"status"`
}

// handleHealth is liveness: the process is up and serving (§7.2).
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

// handleReady is readiness: both stores are reachable (FR-50).
//
// The distinction matters to an orchestrator — a live-but-not-ready process
// should stop receiving traffic without being restarted.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	checks := []struct {
		name  string
		check func() error
	}{
		{"metadata store", func() error { return s.meta.Ping(r.Context()) }},
		{"blob store", func() error { return s.blobs.Ping(r.Context()) }},
	}

	for _, c := range checks {
		if err := c.check(); err != nil {
			s.log.Error("readiness check failed", "component", c.name, "error", err)
			// The component is named; the error is not. An unreachable store's
			// message carries paths and DSNs (FR-28).
			s.writeProblem(w, r, http.StatusServiceUnavailable, typeInternal,
				"Service Unavailable", "The "+c.name+" is not reachable.", nil)
			return
		}
	}

	s.writeJSON(w, http.StatusOK, healthResponse{Status: "ready"})
}
