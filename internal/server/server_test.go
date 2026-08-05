package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/config"
	"github.com/xgentic/agent-package-manager-registry/internal/domain/archive"
	"github.com/xgentic/agent-package-manager-registry/internal/server"
	"github.com/xgentic/agent-package-manager-registry/internal/service"
	"github.com/xgentic/agent-package-manager-registry/internal/store/blob"
	"github.com/xgentic/agent-package-manager-registry/internal/store/sqlite"
)

const testRepository = "corp-main"

// stack is a fully wired registry over a temp directory. Handler tests run
// against the real stores rather than fakes: the seams that matter here — the
// unique constraint, blob streaming — are exactly the ones a fake would
// paper over.
type stack struct {
	handler http.Handler
	store   *sqlite.Store
	blobs   *blob.FS
}

func newStack(t *testing.T) *stack {
	t.Helper()

	dataDir := t.TempDir()
	cfg, err := config.Load(func(key string) string {
		switch key {
		case "APM_REGISTRY_DATA_DIR":
			return dataDir
		case "APM_REGISTRY_MAX_ARCHIVE_BYTES":
			return "1048576"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}

	store, err := sqlite.Open(cfg.SQLitePath(), service.RandomIDs{})
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	blobs, err := blob.NewFS(cfg.BlobDir())
	if err != nil {
		t.Fatalf("blob.NewFS() error = %v", err)
	}

	repositories := service.NewRepositoryService(store)
	if _, err := repositories.Create(t.Context(), testRepository, service.VisibilityPrivate, 0); err != nil {
		t.Fatalf("creating repository: %v", err)
	}

	handler := server.New(server.Deps{
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: cfg,
		Publish: service.NewPublishService(store, blobs, service.SystemClock{}, service.PublishConfig{
			TempDir:              filepath.Join(dataDir, "tmp"),
			MaxArchiveBytes:      cfg.MaxArchiveBytes,
			MaxUncompressedBytes: cfg.MaxUncompressedBytes,
			MaxArchiveEntries:    cfg.MaxArchiveEntries,
			AcceptedMediaTypes:   cfg.AcceptedMediaTypes,
			RequireVersionMatch:  cfg.RequireManifestVersionMatch,
		}),
		Query:        service.NewQueryService(store, blobs),
		Repositories: repositories,
		Meta:         store,
		Blobs:        blobs,
	})

	return &stack{handler: handler, store: store, blobs: blobs}
}

// do sends a request built from a **raw** path. Building the URL with
// url.Parse rather than httptest.NewRequest's convenience form is deliberate:
// it preserves RawPath, which is the whole subject of the percent-encoding
// tests below.
func (s *stack) do(t *testing.T, method, rawPath string, body []byte, contentType string) *httptest.ResponseRecorder {
	t.Helper()

	target, err := url.Parse("http://registry.test" + rawPath)
	if err != nil {
		t.Fatalf("parsing %q: %v", rawPath, err)
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target.String(), reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec
}

func (s *stack) publish(t *testing.T, identityPath, version string, body []byte) *httptest.ResponseRecorder {
	t.Helper()

	return s.do(t, http.MethodPut,
		"/api/agentpackages/"+testRepository+"/v1/packages/"+identityPath+"/versions/"+version,
		body, archive.MediaTypeZip)
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body %q: %v", rec.Body.String(), err)
	}
	return body
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()

	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, want, rec.Body.String())
	}
}

func TestHealthReturnsOK(t *testing.T) {
	t.Parallel()

	rec := newStack(t).do(t, http.MethodGet, "/health", nil, "")

	assertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json; charset=utf-8")
	}
	if got := decode(t, rec)["status"]; got != "ok" {
		t.Errorf("status = %v, want %q", got, "ok")
	}
}

// FR-50: /ready reports on the stores, not just on the process.
func TestReadyChecksTheStores(t *testing.T) {
	t.Parallel()

	s := newStack(t)

	rec := s.do(t, http.MethodGet, "/ready", nil, "")
	assertStatus(t, rec, http.StatusOK)

	// A closed database is exactly the "live but not serving" state readiness
	// exists to report.
	if err := s.store.Close(); err != nil {
		t.Fatalf("closing store: %v", err)
	}

	rec = s.do(t, http.MethodGet, "/ready", nil, "")
	assertStatus(t, rec, http.StatusServiceUnavailable)
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want a Problem body", got)
	}
}

// docs/specs/00-glossary.md makes RFC 7807 the only error envelope this server
// emits, so the shape is asserted rather than just the status code.
func TestUnknownRouteReturnsProblemDetails(t *testing.T) {
	t.Parallel()

	rec := newStack(t).do(t, http.MethodGet, "/does-not-exist", nil, "")

	assertStatus(t, rec, http.StatusNotFound)
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", got, "application/problem+json; charset=utf-8")
	}

	body := decode(t, rec)
	if got, ok := body["title"].(string); !ok || got == "" {
		t.Errorf("title = %v, want a non-empty string (RFC 7807 requires it)", body["title"])
	}
	if got, ok := body["status"].(float64); !ok || int(got) != http.StatusNotFound {
		t.Errorf("status = %v, want %d", body["status"], http.StatusNotFound)
	}
}

func TestHealthRejectsWrongMethod(t *testing.T) {
	t.Parallel()

	rec := newStack(t).do(t, http.MethodPost, "/health", nil, "")

	if rec.Code == http.StatusOK {
		t.Errorf("POST /health returned 200, want a rejection")
	}
}

// FR-27: every Problem body carries a request id, and the response echoes it,
// so a user quoting one can be found in the logs.
func TestProblemBodiesCarryARequestID(t *testing.T) {
	t.Parallel()

	rec := newStack(t).do(t, http.MethodGet, "/nope", nil, "")

	extensions, ok := decode(t, rec)["extensions"].(map[string]any)
	if !ok {
		t.Fatalf("body has no extensions object: %s", rec.Body.String())
	}
	id, ok := extensions["request_id"].(string)
	if !ok || id == "" {
		t.Fatalf("extensions.request_id = %v, want a non-empty id", extensions["request_id"])
	}
	if got := rec.Header().Get("X-Request-Id"); got != id {
		t.Errorf("X-Request-Id header = %q, want it to match the body's %q", got, id)
	}
}
