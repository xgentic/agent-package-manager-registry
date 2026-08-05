package server_test

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
	"github.com/xgentic/agent-package-manager-registry/internal/domain/archive"
	"github.com/xgentic/agent-package-manager-registry/internal/fixtures"
)

const base = "/api/agentpackages/" + testRepository + "/v1/packages/"

// FR-14 / ADR-0007: the two path shapes are one resource. This is the test
// risk R-5 exists for — if it fails, percent-encoded identities are
// mis-routing and nothing else in the suite will say so.
func TestBothIdentityPathFormsResolveToTheSamePackage(t *testing.T) {
	t.Parallel()

	s := newStack(t)

	// Publish through the two-segment form...
	assertStatus(t, s.publish(t, "acme/web-skills", "1.0.0", fixtures.ValidZip()), http.StatusCreated)

	// ...and read it back through the percent-encoded single-segment form.
	rec := s.do(t, http.MethodGet, base+"acme%2Fweb-skills/versions", nil, "")
	assertStatus(t, rec, http.StatusOK)

	body := decode(t, rec)
	if got := body["package"]; got != "acme/web-skills" {
		t.Errorf("package = %v, want the canonical %q", got, "acme/web-skills")
	}
	versions, ok := body["versions"].([]any)
	if !ok || len(versions) != 1 {
		t.Fatalf("versions = %v, want the version published through the other path form", body["versions"])
	}
}

// A non-GitHub identity has no two-segment form at all: it arrives as one
// percent-encoded segment (MS-API §1.2).
func TestPercentEncodedMultiComponentIdentity(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	encoded := "gitlab.com%2Facme%2Fweb-skills"
	body := fixtures.ValidZipFor("gitlab.com/acme/web-skills", "1.0.0")

	assertStatus(t, s.publish(t, encoded, "1.0.0", body), http.StatusCreated)

	rec := s.do(t, http.MethodGet, base+encoded+"/versions", nil, "")
	assertStatus(t, rec, http.StatusOK)
	if got := decode(t, rec)["package"]; got != "gitlab.com/acme/web-skills" {
		t.Errorf("package = %v, want the decoded identity", got)
	}
}

// FR-15 / risk R-5: `acme%2F..%2Fevil` matches the single-segment route and
// ServeMux hands the handler the decoded `acme/../evil`. The traversal is only
// visible after decoding, so validating the raw segment would let it through.
func TestPercentEncodedTraversalIsRejected(t *testing.T) {
	t.Parallel()

	s := newStack(t)

	for _, path := range []string{
		base + "acme%2F..%2Fevil/versions",
		base + "..%2F..%2Fetc%2Fpasswd/versions",
		base + "%2Facme%2Fweb-skills/versions",
	} {
		rec := s.do(t, http.MethodGet, path, nil, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400; body = %s", path, rec.Code, rec.Body.String())
		}
	}
}

// FR-16: identity is canonicalised to lowercase, so the two spellings are one
// package rather than two.
func TestIdentityIsCanonicalisedToLowercase(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	assertStatus(t, s.publish(t, "Acme/Web-Skills", "1.0.0", fixtures.ValidZip()), http.StatusCreated)

	rec := s.do(t, http.MethodGet, base+"acme/web-skills/versions", nil, "")
	assertStatus(t, rec, http.StatusOK)
	if got := decode(t, rec)["package"]; got != "acme/web-skills" {
		t.Errorf("package = %v, want the canonical lowercase form", got)
	}
}

// FR-29 / ADR-0009: repositories are resolved from the base URL, and an unknown
// one is a 404 before any package lookup happens.
func TestUnknownRepositoryIs404(t *testing.T) {
	t.Parallel()

	s := newStack(t)

	rec := s.do(t, http.MethodGet,
		"/api/agentpackages/no-such-repo/v1/packages/acme/web-skills/versions", nil, "")
	assertStatus(t, rec, http.StatusNotFound)
}

func TestUnknownPackageIs404(t *testing.T) {
	t.Parallel()

	s := newStack(t)

	rec := s.do(t, http.MethodGet, base+"acme/never-published/versions", nil, "")
	assertStatus(t, rec, http.StatusNotFound)
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/problem+json") {
		t.Errorf("Content-Type = %q, want a Problem body", got)
	}
}

// FR-01 to FR-04: the exact response shape. Field names are asserted literally
// because a casing regression is ignored *silently* by the reference client
// (TR-23, risk R-4).
func TestListVersionsResponseShape(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	for _, v := range []string{"1.0.0", "1.1.0"} {
		assertStatus(t, s.publish(t, "acme/web-skills", v,
			fixtures.ValidZipFor(fixtures.DefaultIdentity, v)), http.StatusCreated)
	}

	rec := s.do(t, http.MethodGet, base+"acme/web-skills/versions", nil, "")
	assertStatus(t, rec, http.StatusOK)

	body := decode(t, rec)
	assertExactKeys(t, body, "package", "versions")

	versions, ok := body["versions"].([]any)
	if !ok || len(versions) != 2 {
		t.Fatalf("versions = %v, want 2 entries", body["versions"])
	}

	first, ok := versions[0].(map[string]any)
	if !ok {
		t.Fatalf("versions[0] = %v, want an object", versions[0])
	}
	assertExactKeys(t, first, "version", "digest", "published_at", "size_bytes")

	if digest, _ := first["digest"].(string); !strings.HasPrefix(digest, "sha256:") {
		t.Errorf("digest = %v, want a sha256: prefix", first["digest"])
	}
	if got := rec.Header().Get("Cache-Control"); got != "max-age=60, public" {
		t.Errorf("Cache-Control = %q, want %q", got, "max-age=60, public")
	}
}

// FR-03: `versions` is always present. A package with no versions cannot exist
// in this model — publish creates the package — so the guarantee is asserted at
// the encoder level instead.
func TestVersionsIsAnArrayNeverNull(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	assertStatus(t, s.publish(t, "acme/web-skills", "1.0.0", fixtures.ValidZip()), http.StatusCreated)

	rec := s.do(t, http.MethodGet, base+"acme/web-skills/versions", nil, "")
	if strings.Contains(rec.Body.String(), `"versions":null`) {
		t.Errorf("body = %s, want versions as an array", rec.Body.String())
	}
}

// FR-05 to FR-07, TR-21: the download response, headers included.
func TestDownloadHeadersAndBytes(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	body := fixtures.ValidZip()
	assertStatus(t, s.publish(t, "acme/web-skills", "1.0.0", body), http.StatusCreated)

	rec := s.do(t, http.MethodGet, base+"acme/web-skills/versions/1.0.0/download", nil, "")
	assertStatus(t, rec, http.StatusOK)

	// req-rg-001: byte-identical, and hashing to the advertised digest.
	if !bytesEqual(rec.Body.Bytes(), body) {
		t.Error("served bytes differ from the published bytes")
	}

	sum := sha256.Sum256(body)
	digest := domain.NewDigest(sum[:])

	if got := rec.Header().Get("Content-Type"); got != archive.MediaTypeZip {
		t.Errorf("Content-Type = %q, want the media type recorded at publish", got)
	}
	if got, want := rec.Header().Get("ETag"), `"`+digest.String()+`"`; got != want {
		t.Errorf("ETag = %q, want %q", got, want)
	}
	// RFC 3230 carries the raw digest base64-encoded, not the hex form.
	if got, want := rec.Header().Get("Digest"),
		"sha256="+base64.StdEncoding.EncodeToString(digest.Bytes()); got != want {
		t.Errorf("Digest = %q, want %q", got, want)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="web-skills-1.0.0.zip"` {
		t.Errorf("Content-Disposition = %q, want an attachment with a derived filename", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "max-age=86400, immutable" {
		t.Errorf("Cache-Control = %q, want %q", got, "max-age=86400, immutable")
	}
}

func TestDownloadUnknownVersionIs404(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	assertStatus(t, s.publish(t, "acme/web-skills", "1.0.0", fixtures.ValidZip()), http.StatusCreated)

	rec := s.do(t, http.MethodGet, base+"acme/web-skills/versions/9.9.9/download", nil, "")
	assertStatus(t, rec, http.StatusNotFound)
}

// §3.3: the 201 body, which the CLI prints `digest` and `published_at` from.
func TestPublishResponseShape(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	rec := s.publish(t, "acme/web-skills", "1.0.0", fixtures.ValidZip())
	assertStatus(t, rec, http.StatusCreated)

	body := decode(t, rec)
	assertExactKeys(t, body, "package", "version", "digest", "published_at", "size_bytes")

	if got := body["package"]; got != "acme/web-skills" {
		t.Errorf("package = %v, want the canonical identity", got)
	}
	if got := body["version"]; got != "1.0.0" {
		t.Errorf("version = %v, want %q", got, "1.0.0")
	}
}

// FR-11: republishing is a 409 whether the bytes match or not, and the body
// names the previous publish.
func TestRepublishReturns409(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	assertStatus(t, s.publish(t, "acme/web-skills", "1.0.0", fixtures.ValidZip()), http.StatusCreated)

	for _, tt := range []struct {
		name string
		body []byte
	}{
		{"identical bytes", fixtures.ValidZip()},
		{"different bytes", fixtures.Custom([]fixtures.Entry{
			{Name: "apm.yml", Body: fixtures.Manifest(fixtures.DefaultIdentity, "1.0.0")},
			{Name: "README.md", Body: "# different\n"},
		})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := s.publish(t, "acme/web-skills", "1.0.0", tt.body)
			assertStatus(t, rec, http.StatusConflict)

			extensions, ok := decode(t, rec)["extensions"].(map[string]any)
			if !ok {
				t.Fatalf("409 body has no extensions: %s", rec.Body.String())
			}
			for _, key := range []string{"previous_publish", "previous_digest"} {
				if v, ok := extensions[key].(string); !ok || v == "" {
					t.Errorf("extensions.%s = %v, want the previous publish's value", key, extensions[key])
				}
			}
		})
	}
}

// FR-09: an unacceptable Content-Type is a 415, decided before the body is read.
func TestPublishRejectsUnsupportedMediaType(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	rec := s.do(t, http.MethodPut, base+"acme/web-skills/versions/1.0.0",
		fixtures.ValidZip(), "application/x-7z-compressed")

	assertStatus(t, rec, http.StatusUnsupportedMediaType)
}

// §3.3: a body that does not parse as its declared type is a 400 — distinct
// from an archive that parses and breaks rules, which is a 422.
func TestPublishRejectsCorruptBodyWith400(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	rec := s.publish(t, "acme/web-skills", "1.0.0", fixtures.NotAnArchive())

	assertStatus(t, rec, http.StatusBadRequest)
}

// FR-12: over the cap is a 413.
func TestPublishRejectsOversizedBody(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	// The stack's cap is 1 MiB; this is comfortably past it.
	oversized := fixtures.Oversized(2 << 20)
	if len(oversized) <= 1<<20 {
		t.Fatalf("fixture is %d bytes, under the 1 MiB cap; the test would prove nothing", len(oversized))
	}

	rec := s.publish(t, "acme/web-skills", "1.0.0", oversized)
	assertStatus(t, rec, http.StatusRequestEntityTooLarge)
}

// FR-10: a 422 lists every broken rule, using the vocabulary of §4.
func TestPublishValidationFailureListsEveryRule(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	body := fixtures.Custom([]fixtures.Entry{
		{Name: "apm.yml", Body: fixtures.Manifest("wrong/name", "9.9.9")},
		{Name: "../../escape.txt", Body: "traversal\n"},
	})

	rec := s.publish(t, "acme/web-skills", "1.0.0", body)
	assertStatus(t, rec, http.StatusUnprocessableEntity)

	extensions, ok := decode(t, rec)["extensions"].(map[string]any)
	if !ok {
		t.Fatalf("422 body has no extensions: %s", rec.Body.String())
	}
	errorsList, ok := extensions["errors"].([]any)
	if !ok || len(errorsList) < 3 {
		t.Fatalf("extensions.errors = %v, want every broken rule listed", extensions["errors"])
	}

	seen := map[string]bool{}
	for _, item := range errorsList {
		failure, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("errors[] entry = %v, want an object", item)
		}
		rule, _ := failure["rule"].(string)
		seen[rule] = true
		if msg, _ := failure["message"].(string); msg == "" {
			t.Errorf("rule %q has no message", rule)
		}
	}
	for _, want := range []string{"entry_safety", "manifest_name_match", "manifest_version_match"} {
		if !seen[want] {
			t.Errorf("errors[] rules = %v, want %q among them", seen, want)
		}
	}
}

// TR-25: every hostile fixture is refused over HTTP, not merely in a unit test.
func TestHostileArchivesAreRejectedOverHTTP(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	version := 0

	for name, body := range fixtures.Hostile() {
		version++
		contentType := archive.MediaTypeZip
		if strings.HasSuffix(name, ".tar.gz") {
			contentType = archive.MediaTypeGzip
		}

		rec := s.do(t, http.MethodPut,
			base+"acme/web-skills/versions/1.0."+itoa(version), body, contentType)

		if rec.Code < 400 || rec.Code >= 500 {
			t.Errorf("PUT with %s = %d, want a 4xx rejection; body = %s", name, rec.Code, rec.Body.String())
		}
	}
}

// FR-17: selectors are opaque and case-sensitive, so these are two versions.
func TestVersionSelectorsAreCaseSensitiveOverHTTP(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	assertStatus(t, s.publish(t, "acme/web-skills", "v1.0",
		fixtures.ValidZipFor(fixtures.DefaultIdentity, "v1.0")), http.StatusCreated)
	assertStatus(t, s.publish(t, "acme/web-skills", "V1.0",
		fixtures.ValidZipFor(fixtures.DefaultIdentity, "V1.0")), http.StatusCreated)

	rec := s.do(t, http.MethodGet, base+"acme/web-skills/versions", nil, "")
	versions, _ := decode(t, rec)["versions"].([]any)
	if len(versions) != 2 {
		t.Errorf("versions = %v, want V1.0 and v1.0 as distinct versions", versions)
	}
}

// §9.6: every non-2xx this server can produce is application/problem+json with
// a title and a status. The sweep is what makes that a property rather than a
// habit.
func TestEveryErrorResponseIsProblemJSON(t *testing.T) {
	t.Parallel()

	s := newStack(t)
	assertStatus(t, s.publish(t, "acme/web-skills", "1.0.0", fixtures.ValidZip()), http.StatusCreated)

	cases := []struct {
		name        string
		method      string
		path        string
		body        []byte
		contentType string
	}{
		{"unrouted path", http.MethodGet, "/nope", nil, ""},
		{"unknown repository", http.MethodGet, "/api/agentpackages/nope/v1/packages/acme/web-skills/versions", nil, ""},
		{"unknown package", http.MethodGet, base + "acme/nothing/versions", nil, ""},
		{"unknown version", http.MethodGet, base + "acme/web-skills/versions/9.9.9/download", nil, ""},
		{"malformed identity", http.MethodGet, base + "acme%2F..%2Fevil/versions", nil, ""},
		{"republish conflict", http.MethodPut, base + "acme/web-skills/versions/1.0.0", fixtures.ValidZip(), archive.MediaTypeZip},
		{"bad media type", http.MethodPut, base + "acme/web-skills/versions/2.0.0", fixtures.ValidZip(), "text/plain"},
		{"corrupt archive", http.MethodPut, base + "acme/web-skills/versions/2.0.0", fixtures.NotAnArchive(), archive.MediaTypeZip},
		{"validation failure", http.MethodPut, base + "acme/web-skills/versions/2.0.0", fixtures.Hostile()["no-manifest.zip"], archive.MediaTypeZip},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rec := s.do(t, tt.method, tt.path, tt.body, tt.contentType)

			if rec.Code < 400 {
				t.Fatalf("status = %d, want a 4xx or 5xx", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/problem+json") {
				t.Fatalf("Content-Type = %q, want application/problem+json", got)
			}

			body := decode(t, rec)
			if title, ok := body["title"].(string); !ok || title == "" {
				t.Errorf("title = %v, want a non-empty string", body["title"])
			}
			if status, ok := body["status"].(float64); !ok || int(status) != rec.Code {
				t.Errorf("body status = %v, want it to match the response status %d", body["status"], rec.Code)
			}
			// FR-28: no internals in an error body.
			for _, leak := range []string{"goroutine", "/Users/", "SELECT ", "INSERT "} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Errorf("body leaks %q: %s", leak, rec.Body.String())
				}
			}
		})
	}
}

func assertExactKeys(t *testing.T, body map[string]any, want ...string) {
	t.Helper()

	for _, key := range want {
		if _, ok := body[key]; !ok {
			t.Errorf("response is missing %q; keys = %v", key, keysOf(body))
		}
	}
	if len(body) != len(want) {
		t.Errorf("response keys = %v, want exactly %v", keysOf(body), want)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func bytesEqual(a, b []byte) bool {
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

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
