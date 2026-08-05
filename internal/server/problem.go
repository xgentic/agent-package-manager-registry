package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
)

// problemContentType is RFC 7807's media type. Per docs/specs/00-glossary.md it
// is the only error shape this server emits, so every error path must go
// through writeProblem rather than http.Error or a bare JSON object.
const problemContentType = "application/problem+json; charset=utf-8"

// Problem `type` URIs. Stable identifiers a client can branch on without
// parsing prose (§4).
const (
	typeNotFound         = "https://docs.apm.dev/errors/not-found"
	typeBadRequest       = "https://docs.apm.dev/errors/bad-request"
	typeVersionConflict  = "https://docs.apm.dev/errors/version-conflict"
	typeValidationFailed = "https://docs.apm.dev/errors/validation-failed"
	typePayloadTooLarge  = "https://docs.apm.dev/errors/payload-too-large"
	typeUnsupportedMedia = "https://docs.apm.dev/errors/unsupported-media-type"
	typeUnauthenticated  = "https://docs.apm.dev/errors/unauthenticated"
	typeForbidden        = "https://docs.apm.dev/errors/insufficient-scope"
	typeInternal         = "https://docs.apm.dev/errors/internal"
)

// problemDetails is the RFC 7807 error envelope.
//
// Every field carries an explicit json tag. That is not decoration: an untagged
// Go field marshals as `Status`, and the reference client ignores unknown
// fields silently (TR-23, risk R-4).
type problemDetails struct {
	Type       string         `json:"type,omitempty"`
	Title      string         `json:"title"`
	Status     int            `json:"status"`
	Detail     string         `json:"detail,omitempty"`
	Instance   string         `json:"instance,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// writeProblem emits an RFC 7807 body. Every non-2xx response in this server
// goes through here (FR-26, ADR-0006).
func (s *Server) writeProblem(w http.ResponseWriter, r *http.Request, status int, problemType, title, detail string, ext map[string]any) {
	if ext == nil {
		ext = map[string]any{}
	}
	// A user quoting a request_id must be findable in the logs (TR-29, FR-27).
	if id := requestIDFrom(r.Context()); id != "" {
		ext["request_id"] = id
	}

	s.write(w, problemContentType, status, problemDetails{
		Type:   problemType,
		Title:  title,
		Status: status,
		Detail: detail,
		// EscapedPath, not Path: for a percent-encoded identity the decoded
		// path is a different, wrong-looking URL (ADR-0007).
		Instance:   r.URL.EscapedPath(),
		Extensions: ext,
	})
}

// writeError maps a domain error to its Problem response.
//
// This is the *only* place that turns an error into a status code. A handler
// that picks its own status is how two endpoints end up disagreeing about what
// "not found" means.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, problemType, title, detail, ext := problemFor(err)

	if status >= http.StatusInternalServerError {
		// Log the real cause; return none of it. Problem bodies never contain
		// stack traces, filesystem paths, SQL or token material (FR-28).
		s.log.Error("request failed",
			"error", err,
			"request_id", requestIDFrom(r.Context()),
			"method", r.Method,
			"path", r.URL.EscapedPath(),
		)
	}
	s.writeProblem(w, r, status, problemType, title, detail, ext)
}

//nolint:gocyclo // a flat mapping table; splitting it would hide the taxonomy
func problemFor(err error) (status int, problemType, title, detail string, ext map[string]any) {
	// Typed errors first: they carry the payload the contract requires in the
	// body, so matching them before the sentinels is what fills in
	// extensions.errors[] and extensions.previous_* (§3.3).
	var validation *domain.ValidationError
	if errors.As(err, &validation) {
		return http.StatusUnprocessableEntity, typeValidationFailed,
			"Package validation failed",
			pluralise(len(validation.Failures)),
			map[string]any{"errors": validation.Failures}
	}

	var conflict *domain.ConflictError
	if errors.As(err, &conflict) {
		return http.StatusConflict, typeVersionConflict,
			"Version already published",
			conflict.Error(),
			map[string]any{
				"previous_publish": conflict.PreviousPublish.UTC().Format(timestampLayout),
				"previous_digest":  conflict.PreviousDigest.String(),
			}
	}

	var scopeErr *domain.ScopeError
	if errors.As(err, &scopeErr) {
		return http.StatusForbidden, typeForbidden,
			"Insufficient scope",
			"The presented credentials do not grant " + scopeErr.Required.String() + ".",
			map[string]any{"required_scope": scopeErr.Required.String()}
	}

	switch {
	case errors.Is(err, domain.ErrNotFound):
		// FR-40: "not permitted" and "not found" are indistinguishable, so this
		// body says nothing about which one it was.
		return http.StatusNotFound, typeNotFound, "Not Found",
			trimSentinel(err, domain.ErrNotFound), nil

	case errors.Is(err, domain.ErrVersionConflict):
		return http.StatusConflict, typeVersionConflict, "Version already published",
			trimSentinel(err, domain.ErrVersionConflict), nil

	case errors.Is(err, domain.ErrPayloadTooLarge):
		return http.StatusRequestEntityTooLarge, typePayloadTooLarge, "Payload Too Large",
			trimSentinel(err, domain.ErrPayloadTooLarge), nil

	case errors.Is(err, domain.ErrUnsupportedMediaType):
		return http.StatusUnsupportedMediaType, typeUnsupportedMedia, "Unsupported Media Type",
			trimSentinel(err, domain.ErrUnsupportedMediaType), nil

	case errors.Is(err, domain.ErrArchiveInvalid):
		return http.StatusUnprocessableEntity, typeValidationFailed, "Package validation failed",
			trimSentinel(err, domain.ErrArchiveInvalid), nil

	case errors.Is(err, domain.ErrScopeDenied):
		return http.StatusForbidden, typeForbidden, "Insufficient scope",
			trimSentinel(err, domain.ErrScopeDenied), nil

	case errors.Is(err, domain.ErrUnauthenticated):
		return http.StatusUnauthorized, typeUnauthenticated, "Authentication required",
			trimSentinel(err, domain.ErrUnauthenticated), nil

	case errors.Is(err, domain.ErrBadRequest):
		return http.StatusBadRequest, typeBadRequest, "Bad Request",
			trimSentinel(err, domain.ErrBadRequest), nil

	default:
		// Anything unrecognised is a fault on our side and says so and nothing
		// more. The detail is a constant, so no wrapped message can leak.
		return http.StatusInternalServerError, typeInternal, "Internal Server Error",
			"The request could not be completed. Quote the request_id when reporting this.", nil
	}
}

// trimSentinel strips the taxonomy prefix so the detail reads as prose rather
// than as "not found: not found: package \"x\"".
func trimSentinel(err error, sentinel error) string {
	msg := err.Error()
	if trimmed, ok := strings.CutPrefix(msg, sentinel.Error()+": "); ok {
		return capitalise(trimmed)
	}
	return capitalise(msg)
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func pluralise(n int) string {
	if n == 1 {
		return "The uploaded archive failed 1 validation rule"
	}
	return fmt.Sprintf("The uploaded archive failed %d validation rules", n)
}
