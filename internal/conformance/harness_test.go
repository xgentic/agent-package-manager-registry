// Package conformance is the executable form of MS-API §9.
//
// It is a package of its own, and not a _test.go file beside the handlers,
// because it must run two ways (TR-22):
//
//	go test ./internal/conformance/                       # in-process
//	BASE_URL=https://registry.example.com go test ./...   # against a deployment
//
// The point of the separation is what a failure *means*. A failure here reads
// as "we are not conformant", not as "a handler test broke" — the suite is the
// contract's regression net, and the contract is what an external, unpatchable
// client depends on.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xgentic/agent-package-manager-registry/internal/config"
	"github.com/xgentic/agent-package-manager-registry/internal/server"
	"github.com/xgentic/agent-package-manager-registry/internal/service"
	"github.com/xgentic/agent-package-manager-registry/internal/store/blob"
	"github.com/xgentic/agent-package-manager-registry/internal/store/sqlite"
)

// repositoryName is the repository the suite publishes into. Against a remote
// deployment it must already exist — `apm-registry repo create` is the only way
// to make one (ADR-0013).
const repositoryName = "conformance"

// registry is the system under test, whether in-process or remote.
type registry struct {
	baseURL string
	client  *http.Client
	remote  bool
}

// newRegistry returns the target: a deployment if BASE_URL is set, otherwise an
// httptest.Server over a freshly migrated temp data directory.
func newRegistry(t *testing.T) *registry {
	t.Helper()

	if base := os.Getenv("BASE_URL"); base != "" {
		t.Logf("running against the deployment at %s", base)
		return &registry{
			baseURL: trimTrailingSlash(base),
			client:  &http.Client{Timeout: 60 * time.Second},
			remote:  true,
		}
	}

	dataDir := t.TempDir()
	cfg, err := config.Load(func(key string) string {
		if key == "APM_REGISTRY_DATA_DIR" {
			return dataDir
		}
		return ""
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
	if _, err := repositories.Create(t.Context(), repositoryName, service.VisibilityPrivate, 0); err != nil {
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

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &registry{baseURL: srv.URL, client: srv.Client()}
}

// base is the vendor-defined base URL clients are configured with; they append
// paths starting `/v1/...` to it (§1.1).
func (r *registry) base() string {
	return r.baseURL + "/api/agentpackages/" + repositoryName
}

type response struct {
	Status  int
	Header  http.Header
	Body    []byte
	Decoded map[string]any
}

func (r *registry) request(t *testing.T, method, path string, body []byte, contentType string) response {
	t.Helper()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, r.base()+path, reader)
	if err != nil {
		t.Fatalf("building request %s %s: %v", method, path, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s %s: %v", method, path, err)
	}

	out := response{Status: resp.StatusCode, Header: resp.Header, Body: raw}
	// Binary downloads are not JSON; decoding is best-effort.
	_ = json.Unmarshal(raw, &out.Decoded)
	return out
}

func (r *registry) get(t *testing.T, path string) response {
	t.Helper()
	return r.request(t, http.MethodGet, path, nil, "")
}

func (r *registry) put(t *testing.T, path string, body []byte, contentType string) response {
	t.Helper()
	return r.request(t, http.MethodPut, path, body, contentType)
}

func requireStatus(t *testing.T, resp response, want int) {
	t.Helper()

	if resp.Status != want {
		t.Fatalf("status = %d, want %d; body = %s", resp.Status, want, resp.Body)
	}
}

// requireFields asserts exact snake_case field names (TR-23).
//
// This is the assertion risk R-4 exists for: the reference client reads only
// snake_case and ignores unknown fields *without erroring*, so an untagged Go
// field marshalling as `PublishedAt` produces a broken install with no error
// anywhere. Nothing else in the system would notice.
func requireFields(t *testing.T, object map[string]any, want ...string) {
	t.Helper()

	for _, field := range want {
		if _, ok := object[field]; !ok {
			t.Errorf("missing field %q; present fields are %v", field, fieldNames(object))
		}
	}
}

func fieldNames(object map[string]any) []string {
	out := make([]string, 0, len(object))
	for k := range object {
		out = append(out, k)
	}
	return out
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
