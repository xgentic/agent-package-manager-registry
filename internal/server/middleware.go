package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	principalKey contextKey = "principal"
)

// requestHeaderName lets a reverse proxy's correlation id flow through rather
// than being replaced, so one id spans the whole hop chain.
const requestHeaderName = "X-Request-Id"

// requestID puts a correlation id on every request, echoes it in the response,
// and makes it available to logs and Problem bodies (TR-29, FR-27).
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitiseRequestID(r.Header.Get(requestHeaderName))
		if id == "" {
			id = newRequestID()
		}

		w.Header().Set(requestHeaderName, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func newRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// A missing entropy source must not cost us the request; the id is for
		// correlation, not for security.
		return "unavailable"
	}
	return hex.EncodeToString(buf[:])
}

// sanitiseRequestID accepts an inbound id only if it is short and printable.
// It is echoed into a response header and a JSON body, so it is untrusted input
// like any other (TR-20).
func sanitiseRequestID(raw string) string {
	if len(raw) == 0 || len(raw) > 64 {
		return ""
	}
	for _, r := range raw {
		isSafe := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.'
		if !isSafe {
			return ""
		}
	}
	return raw
}

// recoverPanics turns a panic in any handler into a generic 500 that leaks
// nothing (FR-28). Without it, net/http closes the connection and the client
// sees a transport error rather than a Problem body — and the §9.6 sweep would
// have a hole in it.
func recoverPanics(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}

				log.Error("panic recovered",
					"panic", recovered,
					"request_id", requestIDFrom(r.Context()),
					"method", r.Method,
					"path", r.URL.EscapedPath(),
				)

				// The response may be partly written already; the header write
				// is a no-op then, and there is nothing better to do than stop.
				w.Header().Set("Content-Type", problemContentType)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"type":"` + typeInternal +
					`","title":"Internal Server Error","status":500,` +
					`"detail":"The request could not be completed. Quote the request_id when reporting this.",` +
					`"extensions":{"request_id":"` + requestIDFrom(r.Context()) + `"}}`))
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// Principal is who the request is acting as. It is resolved once, into the
// request context, and read by every handler.
type Principal struct {
	Subject       string
	Scopes        domain.Scopes
	Authenticated bool
}

// authenticate resolves the request's Principal.
//
// # THE AUTH SEAM (T5.2, risk R-2)
//
// MVP 1 ships no authentication: this body returns an all-scopes principal and
// every request is permitted. What it does ship is the *shape* — handlers
// resolve a Principal from the context and call Scope.Satisfies before acting,
// exactly as they will when tokens exist.
//
// That ordering is the whole point. MVP 3 replaces this function body with real
// bearer resolution and changes **no handler file**. If T12.1 finds itself
// editing handlers, this seam was wrong.
//
// Until then: MVP 1 is not safe on an untrusted network (risk R-1).
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO(MVP3): resolve `Authorization: Bearer <token>` to a stored
		// token, verify it with argon2id, and build the Principal from its
		// scopes. Anonymous requests keep an unauthenticated Principal with no
		// scopes, so 401 and 403 stay distinguishable by credential presence
		// (FR-23). Replace only this block.
		principal := Principal{
			Subject:       "anonymous",
			Scopes:        domain.Scopes{domain.ScopeReadAll, domain.ScopePublishAll},
			Authenticated: true,
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, principal)))
	})
}

func principalFrom(ctx context.Context) Principal {
	principal, _ := ctx.Value(principalKey).(Principal)
	return principal
}

// authorise checks a required scope against the request's principal.
//
// The 401/403 split is decided by credential *presence*, not by outcome, so a
// client can tell "authenticate" from "not permitted" (FR-23). In MVP 1 the
// seam grants everything, so this never denies — but it runs on every request,
// which is what makes MVP 3 a middleware change.
func authorise(p Principal, required domain.Scope) error {
	if !p.Authenticated {
		return domain.ErrUnauthenticated
	}
	if !p.Scopes.Satisfies(required) {
		return &domain.ScopeError{Required: required}
	}
	return nil
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		s.log.Info("request",
			"method", r.Method,
			// EscapedPath, never Path: a percent-encoded identity logs as six
			// segments otherwise, and the log stops matching the request.
			"path", r.URL.EscapedPath(),
			"status", rec.status,
			"request_id", requestIDFrom(r.Context()),
			"duration_ns", time.Since(start).Nanoseconds(),
		)
	})
}

// statusRecorder captures the status code on its way through, so the log line
// can report what was actually sent.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
