package conformance_test

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
	"github.com/xgentic/agent-package-manager-registry/internal/domain/archive"
	"github.com/xgentic/agent-package-manager-registry/internal/fixtures"
)

// Each test publishes under its own identity, so the suite is re-runnable
// against a live deployment where versions are immutable and already taken.
func uniquePackage(t *testing.T) string {
	t.Helper()

	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("generating a unique package name: %v", err)
	}
	return "acme/conformance-" + hex.EncodeToString(buf[:])
}

func packagePath(identity string) string { return "/v1/packages/" + identity }

// --- §9.1 round-trip -------------------------------------------------------

// Publish, list, download; the digest must be the same in all three places.
// This is req-rg-001 end to end: it is what makes a lockfile's resolved_hash
// mean anything.
func TestGroup91RoundTrip(t *testing.T) {
	t.Parallel()

	reg := newRegistry(t)
	identity := uniquePackage(t)
	body := fixtures.ValidZipFor(identity, "1.0.0")

	published := reg.put(t, packagePath(identity)+"/versions/1.0.0", body, archive.MediaTypeZip)
	requireStatus(t, published, http.StatusCreated)
	requireFields(t, published.Decoded, "package", "version", "digest", "published_at", "size_bytes")

	publishDigest, _ := published.Decoded["digest"].(string)
	if publishDigest == "" {
		t.Fatalf("201 body has no digest: %s", published.Body)
	}
	if got := published.Decoded["package"]; got != identity {
		t.Errorf("package = %v, want the canonical %q", got, identity)
	}

	listed := reg.get(t, packagePath(identity)+"/versions")
	requireStatus(t, listed, http.StatusOK)
	requireFields(t, listed.Decoded, "package", "versions")

	versions, ok := listed.Decoded["versions"].([]any)
	if !ok || len(versions) != 1 {
		t.Fatalf("versions = %v, want exactly the published version", listed.Decoded["versions"])
	}
	entry, ok := versions[0].(map[string]any)
	if !ok {
		t.Fatalf("versions[0] = %v, want an object", versions[0])
	}
	requireFields(t, entry, "version", "digest", "published_at", "size_bytes")

	if entry["digest"] != publishDigest {
		t.Errorf("/versions digest = %v, want the 201's %q", entry["digest"], publishDigest)
	}

	downloaded := reg.get(t, packagePath(identity)+"/versions/1.0.0/download")
	requireStatus(t, downloaded, http.StatusOK)

	sum := sha256.Sum256(downloaded.Body)
	if got := domain.NewDigest(sum[:]).String(); got != publishDigest {
		t.Errorf("sha256(downloaded bytes) = %q, want the advertised %q", got, publishDigest)
	}
	if !equalBytes(downloaded.Body, body) {
		t.Error("downloaded bytes differ from the uploaded bytes")
	}
	if got := downloaded.Header.Get("ETag"); got == "" {
		t.Error("no ETag on /download")
	}
	if got := downloaded.Header.Get("Digest"); !strings.HasPrefix(got, "sha256=") {
		t.Errorf("Digest header = %q, want the RFC 3230 form", got)
	}
}

// --- §9.2 immutability -----------------------------------------------------

// A second PUT is a 409 for **both** identical and different bodies. The
// identical case is the one worth stating: a registry that accepted it would
// be making the tuple mutable in exactly the case nobody notices
// (ADR-0008).
func TestGroup92Immutability(t *testing.T) {
	t.Parallel()

	reg := newRegistry(t)
	identity := uniquePackage(t)
	original := fixtures.ValidZipFor(identity, "1.0.0")

	requireStatus(t, reg.put(t, packagePath(identity)+"/versions/1.0.0", original, archive.MediaTypeZip),
		http.StatusCreated)

	different := fixtures.Custom([]fixtures.Entry{
		{Name: "apm.yml", Body: fixtures.Manifest(identity, "1.0.0")},
		{Name: "README.md", Body: "# a different archive with the same version\n"},
	})

	for _, tt := range []struct {
		name string
		body []byte
	}{
		{"identical body", original},
		{"different body", different},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp := reg.put(t, packagePath(identity)+"/versions/1.0.0", tt.body, archive.MediaTypeZip)
			requireStatus(t, resp, http.StatusConflict)

			extensions, ok := resp.Decoded["extensions"].(map[string]any)
			if !ok {
				t.Fatalf("409 body has no extensions: %s", resp.Body)
			}
			requireFields(t, extensions, "previous_publish", "previous_digest")
		})
	}

	// The stored bytes must still be the first publish's.
	downloaded := reg.get(t, packagePath(identity)+"/versions/1.0.0/download")
	requireStatus(t, downloaded, http.StatusOK)
	if !equalBytes(downloaded.Body, original) {
		t.Error("a rejected republish changed the stored bytes")
	}
}

// --- §9.3 format dispatch --------------------------------------------------

// Publish as zip, get zip back; publish as gzip, get gzip back. No transcoding
// (FR-07, FR-09).
func TestGroup93FormatDispatch(t *testing.T) {
	t.Parallel()

	reg := newRegistry(t)

	tests := []struct {
		name      string
		mediaType string
		build     func(string, string) []byte
		extension string
	}{
		{"zip", archive.MediaTypeZip, fixtures.ValidZipFor, ".zip"},
		{"gzip", archive.MediaTypeGzip, fixtures.ValidTarGzFor, ".tar.gz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := uniquePackage(t)
			body := tt.build(identity, "1.0.0")

			requireStatus(t, reg.put(t, packagePath(identity)+"/versions/1.0.0", body, tt.mediaType),
				http.StatusCreated)

			downloaded := reg.get(t, packagePath(identity)+"/versions/1.0.0/download")
			requireStatus(t, downloaded, http.StatusOK)

			if got := downloaded.Header.Get("Content-Type"); got != tt.mediaType {
				t.Errorf("Content-Type = %q, want the type recorded at publish (%q)", got, tt.mediaType)
			}
			if !equalBytes(downloaded.Body, body) {
				t.Error("downloaded bytes differ from the uploaded bytes")
			}
			if got := downloaded.Header.Get("Content-Disposition"); !strings.Contains(got, tt.extension) {
				t.Errorf("Content-Disposition = %q, want a %s filename", got, tt.extension)
			}
		})
	}
}

// --- §9.4 validation -------------------------------------------------------

// The rejections MS-API §9.4 names: missing apm.yml, version mismatch,
// absolute paths in tar, symlink in zip.
func TestGroup94Validation(t *testing.T) {
	t.Parallel()

	reg := newRegistry(t)
	hostile := fixtures.Hostile()

	tests := []struct {
		name       string
		fixture    string
		mediaType  string
		wantStatus int
		wantRule   string
	}{
		{"missing apm.yml", "no-manifest.zip", archive.MediaTypeZip, http.StatusUnprocessableEntity, "manifest_present"},
		{"version mismatch", "manifest-wrong-version.zip", archive.MediaTypeZip, http.StatusUnprocessableEntity, "manifest_version_match"},
		{"absolute path in tar", "tar-absolute-path.tar.gz", archive.MediaTypeGzip, http.StatusUnprocessableEntity, "entry_safety"},
		{"symlink in zip", "zip-symlink.zip", archive.MediaTypeZip, http.StatusUnprocessableEntity, "entry_safety"},
		{"traversal in zip", "zip-slip.zip", archive.MediaTypeZip, http.StatusUnprocessableEntity, "entry_safety"},
		{"invalid manifest YAML", "bad-manifest-yaml.zip", archive.MediaTypeZip, http.StatusUnprocessableEntity, "manifest_yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, ok := hostile[tt.fixture]
			if !ok {
				t.Fatalf("fixture %q is missing", tt.fixture)
			}

			identity := uniquePackage(t)
			resp := reg.put(t, packagePath(identity)+"/versions/1.0.0", body, tt.mediaType)
			requireStatus(t, resp, tt.wantStatus)

			extensions, ok := resp.Decoded["extensions"].(map[string]any)
			if !ok {
				t.Fatalf("422 body has no extensions: %s", resp.Body)
			}
			errorsList, ok := extensions["errors"].([]any)
			if !ok || len(errorsList) == 0 {
				t.Fatalf("extensions.errors = %v, want the broken rules listed", extensions["errors"])
			}

			var found bool
			for _, item := range errorsList {
				failure, ok := item.(map[string]any)
				if !ok {
					continue
				}
				requireFields(t, failure, "rule", "message")
				if failure["rule"] == tt.wantRule {
					found = true
				}
			}
			if !found {
				t.Errorf("errors = %v, want rule %q among them", errorsList, tt.wantRule)
			}

			// FR-13: a rejected publish is not listable.
			listed := reg.get(t, packagePath(identity)+"/versions")
			if listed.Status != http.StatusNotFound {
				t.Errorf("after a rejected publish, /versions = %d, want 404", listed.Status)
			}
		})
	}
}

// Media type and body-format rejections, which are statuses rather than rule
// failures (§3.3).
func TestGroup94StatusCodes(t *testing.T) {
	t.Parallel()

	reg := newRegistry(t)
	identity := uniquePackage(t)

	if resp := reg.put(t, packagePath(identity)+"/versions/1.0.0",
		fixtures.ValidZipFor(identity, "1.0.0"), "application/x-7z-compressed"); resp.Status != http.StatusUnsupportedMediaType {
		t.Errorf("unsupported Content-Type = %d, want 415", resp.Status)
	}
	if resp := reg.put(t, packagePath(identity)+"/versions/1.0.0",
		fixtures.NotAnArchive(), archive.MediaTypeZip); resp.Status != http.StatusBadRequest {
		t.Errorf("unparseable body = %d, want 400", resp.Status)
	}
	if resp := reg.get(t, packagePath(identity)+"/versions"); resp.Status != http.StatusNotFound {
		t.Errorf("unknown package = %d, want 404", resp.Status)
	}
}

// --- §9.5 auth -------------------------------------------------------------

// Deliberately skipped, and *visibly* so.
//
// Bearer auth is a hard MS-API §5 requirement, so MVP 1 is knowingly
// non-conformant: this group is what makes that honest rather than invisible.
// MVP 3 (T12.5) deletes the skip, and the full suite going green is that
// milestone's gate.
func TestGroup95Auth(t *testing.T) {
	t.Skip("auth lands in MVP 3 (roadmap T12.5); until then this server has no authentication " +
		"and must not be exposed to an untrusted network (risk R-1)")
}

// --- §9.6 error format -----------------------------------------------------

// Every 4xx is application/problem+json with a title and a status. The sweep
// covers every route, because one endpoint answering with a bare JSON object is
// enough to break a client's error handling.
func TestGroup96ErrorFormat(t *testing.T) {
	t.Parallel()

	reg := newRegistry(t)
	identity := uniquePackage(t)

	requireStatus(t, reg.put(t, packagePath(identity)+"/versions/1.0.0",
		fixtures.ValidZipFor(identity, "1.0.0"), archive.MediaTypeZip), http.StatusCreated)

	cases := []struct {
		name        string
		method      string
		path        string
		body        []byte
		contentType string
	}{
		{"unknown package", http.MethodGet, packagePath("acme/never-published-at-all") + "/versions", nil, ""},
		{"unknown version", http.MethodGet, packagePath(identity) + "/versions/9.9.9/download", nil, ""},
		{"malformed identity", http.MethodGet, "/v1/packages/acme%2F..%2Fevil/versions", nil, ""},
		{"republish conflict", http.MethodPut, packagePath(identity) + "/versions/1.0.0", fixtures.ValidZipFor(identity, "1.0.0"), archive.MediaTypeZip},
		{"unsupported media type", http.MethodPut, packagePath(identity) + "/versions/2.0.0", fixtures.ValidZipFor(identity, "2.0.0"), "text/plain"},
		{"corrupt archive", http.MethodPut, packagePath(identity) + "/versions/2.0.0", fixtures.NotAnArchive(), archive.MediaTypeZip},
		{"validation failure", http.MethodPut, packagePath(identity) + "/versions/2.0.0", fixtures.Hostile()["no-manifest.zip"], archive.MediaTypeZip},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			resp := reg.request(t, tt.method, tt.path, tt.body, tt.contentType)

			if resp.Status < 400 || resp.Status >= 600 {
				t.Fatalf("status = %d, want a 4xx or 5xx", resp.Status)
			}
			if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/problem+json") {
				t.Fatalf("Content-Type = %q, want application/problem+json", got)
			}

			requireFields(t, resp.Decoded, "title", "status")
			if status, ok := resp.Decoded["status"].(float64); !ok || int(status) != resp.Status {
				t.Errorf("body status = %v, want it to match the response status %d", resp.Decoded["status"], resp.Status)
			}
			if title, ok := resp.Decoded["title"].(string); !ok || title == "" {
				t.Errorf("title = %v, want a non-empty string", resp.Decoded["title"])
			}
		})
	}
}

// --- identity routing ------------------------------------------------------

// MS-API §1.2: both path shapes are one resource.
func TestBothIdentityFormsAreOneResource(t *testing.T) {
	t.Parallel()

	reg := newRegistry(t)
	identity := uniquePackage(t)
	body := fixtures.ValidZipFor(identity, "1.0.0")

	requireStatus(t, reg.put(t, packagePath(identity)+"/versions/1.0.0", body, archive.MediaTypeZip),
		http.StatusCreated)

	// The same identity, percent-encoded into a single segment.
	encoded := strings.ReplaceAll(identity, "/", "%2F")
	listed := reg.get(t, packagePath(encoded)+"/versions")
	requireStatus(t, listed, http.StatusOK)

	if got := listed.Decoded["package"]; got != identity {
		t.Errorf("package = %v, want the canonical %q from the encoded form too", got, identity)
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
