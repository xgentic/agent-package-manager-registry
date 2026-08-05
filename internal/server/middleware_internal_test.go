package server

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
	"github.com/xgentic/agent-package-manager-registry/internal/domain/archive"
)

// FR-28: a panic must become a Problem body that leaks nothing, not a dropped
// connection. Without this, the §9.6 sweep would have a hole exactly where the
// unexpected failures live.
func TestRecoverPanicsProducesAProblemBody(t *testing.T) {
	t.Parallel()

	handler := recoverPanics(slog.New(slog.NewTextHandler(io.Discard, nil)))(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("a secret path /Users/someone/data/registry.db and a stack")
		}))

	rec := httptest.NewRecorder()
	requestID(handler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != problemContentType {
		t.Errorf("Content-Type = %q, want %q", got, problemContentType)
	}
	if strings.Contains(rec.Body.String(), "/Users/") {
		t.Errorf("body leaks the panic value: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"request_id"`) {
		t.Errorf("body = %s, want a request_id to quote", rec.Body.String())
	}
}

// The inbound id is echoed into a header and a JSON body, so it is untrusted
// input like any other (TR-20).
func TestSanitiseRequestID(t *testing.T) {
	t.Parallel()

	accepted := []string{"abc123", "01JQ-XYZ_9.8", strings.Repeat("a", 64)}
	for _, in := range accepted {
		if got := sanitiseRequestID(in); got != in {
			t.Errorf("sanitiseRequestID(%q) = %q, want it kept", in, got)
		}
	}

	rejected := []string{"", strings.Repeat("a", 65), `"injected"`, "with space", "new\nline", "<script>"}
	for _, in := range rejected {
		if got := sanitiseRequestID(in); got != "" {
			t.Errorf("sanitiseRequestID(%q) = %q, want it discarded", in, got)
		}
	}
}

// FR-23: 401 vs 403 is decided by credential presence, not by outcome. MVP 1
// never produces either, but the rule is what MVP 3 switches on.
func TestAuthoriseDistinguishesAuthenticationFromPermission(t *testing.T) {
	t.Parallel()

	id, err := domain.ParseIdentity("acme/web-skills")
	if err != nil {
		t.Fatalf("ParseIdentity() error = %v", err)
	}
	required := domain.PublishScope(id)

	anonymous := Principal{Authenticated: false}
	if err := authorise(anonymous, required); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Errorf("anonymous authorise() = %v, want ErrUnauthenticated (401)", err)
	}

	wrongScope := Principal{Authenticated: true, Scopes: domain.Scopes{domain.ScopeReadAll}}
	if err := authorise(wrongScope, required); !errors.Is(err, domain.ErrScopeDenied) {
		t.Errorf("insufficient-scope authorise() = %v, want ErrScopeDenied (403)", err)
	}

	granted := Principal{Authenticated: true, Scopes: domain.Scopes{domain.ScopePublishAll}}
	if err := authorise(granted, required); err != nil {
		t.Errorf("granted authorise() = %v, want nil", err)
	}
}

func TestProblemForMapsTheTaxonomy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"not found", domain.ErrNotFound, http.StatusNotFound},
		{"bad request", domain.ErrBadRequest, http.StatusBadRequest},
		{"unauthenticated", domain.ErrUnauthenticated, http.StatusUnauthorized},
		{"scope denied", &domain.ScopeError{Required: "publish:acme/web-skills"}, http.StatusForbidden},
		{"conflict", &domain.ConflictError{Package: "acme/x", Version: "1.0.0"}, http.StatusConflict},
		{"too large", domain.ErrPayloadTooLarge, http.StatusRequestEntityTooLarge},
		{"unsupported media type", domain.ErrUnsupportedMediaType, http.StatusUnsupportedMediaType},
		{"validation", domain.NewValidationError([]domain.RuleFailure{{Rule: domain.RuleEntrySafety}}), http.StatusUnprocessableEntity},
		{"unknown", errors.New("something internal went wrong in /var/lib"), http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, problemType, title, detail, _ := problemFor(tt.err)
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if title == "" || problemType == "" {
				t.Errorf("type = %q, title = %q; both are required", problemType, title)
			}
			// FR-28: an unrecognised error says nothing about itself.
			if tt.wantStatus == http.StatusInternalServerError && strings.Contains(detail, "/var/lib") {
				t.Errorf("detail = %q, want it to leak nothing from the wrapped error", detail)
			}
		})
	}
}

// TR-20: the filename lands in a response header and is built from
// archive-adjacent, attacker-influenced values.
func TestDownloadFileNameIsSanitised(t *testing.T) {
	t.Parallel()

	id, err := domain.ParseIdentity("acme/web-skills")
	if err != nil {
		t.Fatalf("ParseIdentity() error = %v", err)
	}

	if got := downloadFileName(id, "1.0.0", archive.MediaTypeZip); got != "web-skills-1.0.0.zip" {
		t.Errorf("downloadFileName() = %q, want %q", got, "web-skills-1.0.0.zip")
	}
	if got := downloadFileName(id, "1.0.0", archive.MediaTypeGzip); got != "web-skills-1.0.0.tar.gz" {
		t.Errorf("downloadFileName() = %q, want %q", got, "web-skills-1.0.0.tar.gz")
	}

	hostile := domain.Version(`1.0"; rm -rf /`)
	got := downloadFileName(id, hostile, archive.MediaTypeZip)
	if strings.ContainsAny(got, `"; /`) {
		t.Errorf("downloadFileName(%q) = %q, want the header-breaking characters replaced", hostile, got)
	}
}
