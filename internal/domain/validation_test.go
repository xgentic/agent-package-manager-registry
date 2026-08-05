package domain_test

import (
	"bytes"
	"slices"
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
	"github.com/xgentic/agent-package-manager-registry/internal/domain/archive"
	"github.com/xgentic/agent-package-manager-registry/internal/fixtures"
)

func validate(t *testing.T, mediaType string, body []byte, version string) domain.ValidationResult {
	t.Helper()

	in, err := archive.Inspect(mediaType, bytes.NewReader(body), int64(len(body)),
		archive.Limits{MaxEntries: 100, MaxUncompressedBytes: 1 << 20})
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	id, err := domain.ParseIdentity(fixtures.DefaultIdentity)
	if err != nil {
		t.Fatalf("ParseIdentity() error = %v", err)
	}

	return domain.Validate(domain.ValidationInput{
		Package:             id,
		Version:             domain.Version(version),
		Inspection:          in,
		RequireVersionMatch: true,
	})
}

func rules(result domain.ValidationResult) []domain.RuleName {
	out := make([]domain.RuleName, 0, len(result.Failures))
	for _, f := range result.Failures {
		out = append(out, f.Rule)
	}
	return out
}

func TestValidateAcceptsAValidArchive(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		mediaType string
		body      []byte
	}{
		{"zip", archive.MediaTypeZip, fixtures.ValidZip()},
		{"tar.gz", archive.MediaTypeGzip, fixtures.ValidTarGz()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := validate(t, tt.mediaType, tt.body, fixtures.DefaultVersion)
			if !result.OK() {
				t.Fatalf("Validate() failures = %v, want none", result.Failures)
			}
			if result.Manifest.Name != fixtures.DefaultIdentity {
				t.Errorf("Manifest.Name = %q, want %q", result.Manifest.Name, fixtures.DefaultIdentity)
			}
		})
	}
}

// Every hostile fixture must be rejected, and by the rule the wire vocabulary
// names for it (§4).
func TestValidateRejectsHostileArchives(t *testing.T) {
	t.Parallel()

	hostile := fixtures.Hostile()

	tests := []struct {
		fixture   string
		mediaType string
		wantRule  domain.RuleName
	}{
		{"zip-slip.zip", archive.MediaTypeZip, domain.RuleEntrySafety},
		{"zip-slip-disguised.zip", archive.MediaTypeZip, domain.RuleEntrySafety},
		{"zip-symlink.zip", archive.MediaTypeZip, domain.RuleEntrySafety},
		{"backslash-traversal.zip", archive.MediaTypeZip, domain.RuleEntrySafety},
		{"tar-symlink.tar.gz", archive.MediaTypeGzip, domain.RuleEntrySafety},
		{"tar-hardlink.tar.gz", archive.MediaTypeGzip, domain.RuleEntrySafety},
		{"tar-absolute-path.tar.gz", archive.MediaTypeGzip, domain.RuleEntrySafety},
		{"zip-bomb.zip", archive.MediaTypeZip, domain.RuleArchiveLimits},
		{"no-manifest.zip", archive.MediaTypeZip, domain.RuleManifestPresent},
		{"bad-manifest-yaml.zip", archive.MediaTypeZip, domain.RuleManifestYAML},
		{"manifest-missing-fields.zip", archive.MediaTypeZip, domain.RuleManifestFields},
		{"manifest-wrong-name.zip", archive.MediaTypeZip, domain.RuleManifestNameMatch},
		{"manifest-wrong-version.zip", archive.MediaTypeZip, domain.RuleManifestVersionMatch},
	}

	if len(tests) < 8 {
		t.Fatalf("only %d hostile fixtures asserted; TR-25 asks for at least 8", len(tests))
	}

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			t.Parallel()

			body, ok := hostile[tt.fixture]
			if !ok {
				t.Fatalf("fixture %q is missing", tt.fixture)
			}

			result := validate(t, tt.mediaType, body, fixtures.DefaultVersion)
			if result.OK() {
				t.Fatalf("Validate() accepted %s, want it rejected", tt.fixture)
			}
			if got := rules(result); !slices.Contains(got, tt.wantRule) {
				t.Errorf("failures = %v, want one of them to be %q", got, tt.wantRule)
			}
			for _, f := range result.Failures {
				if f.Message == "" {
					t.Errorf("failure %q has an empty message; a producer needs to know what to fix", f.Rule)
				}
			}
		})
	}
}

// FR-10: no rule short-circuits. An archive that breaks several rules reports
// all of them, so a producer fixes everything in one round-trip.
func TestValidateReportsEveryFailure(t *testing.T) {
	t.Parallel()

	// This archive is simultaneously: traversing, symlinked, and declaring the
	// wrong name and version.
	body := buildBadZip(t)

	result := validate(t, archive.MediaTypeZip, body, "1.0.0")
	if result.OK() {
		t.Fatal("Validate() accepted a thoroughly broken archive")
	}

	got := rules(result)
	for _, want := range []domain.RuleName{
		domain.RuleEntrySafety,
		domain.RuleManifestNameMatch,
		domain.RuleManifestVersionMatch,
	} {
		if !slices.Contains(got, want) {
			t.Errorf("failures = %v, want %q among them — validation must not short-circuit", got, want)
		}
	}
}

// The version-match rule is registry policy, not a MS-API requirement
// (§6.5), so it must be switchable.
func TestVersionMatchIsPolicy(t *testing.T) {
	t.Parallel()

	body := fixtures.Hostile()["manifest-wrong-version.zip"]
	in, err := archive.Inspect(archive.MediaTypeZip, bytes.NewReader(body), int64(len(body)),
		archive.Limits{MaxEntries: 100, MaxUncompressedBytes: 1 << 20})
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	id, err := domain.ParseIdentity(fixtures.DefaultIdentity)
	if err != nil {
		t.Fatalf("ParseIdentity() error = %v", err)
	}

	strict := domain.Validate(domain.ValidationInput{
		Package: id, Version: "1.0.0", Inspection: in, RequireVersionMatch: true,
	})
	if slices.Contains(rules(strict), domain.RuleManifestVersionMatch) == false {
		t.Errorf("strict policy failures = %v, want a version mismatch", rules(strict))
	}

	lenient := domain.Validate(domain.ValidationInput{
		Package: id, Version: "1.0.0", Inspection: in, RequireVersionMatch: false,
	})
	if !lenient.OK() {
		t.Errorf("lenient policy failures = %v, want none", lenient.Failures)
	}
}

// Rule 6 accepts the bare repo name as well as the full identity (risk R-9).
func TestManifestMayDeclareTheBareRepoName(t *testing.T) {
	t.Parallel()

	body := fixtures.ValidZipFor("web-skills", "1.0.0")

	result := validate(t, archive.MediaTypeZip, body, "1.0.0")
	if !result.OK() {
		t.Errorf("Validate() failures = %v, want the bare repo name accepted", result.Failures)
	}
}

func TestValidateRejectsAnUnusableVersionSelector(t *testing.T) {
	t.Parallel()

	result := validate(t, archive.MediaTypeZip, fixtures.ValidZip(), "")
	if !slices.Contains(rules(result), domain.RuleVersionSelector) {
		t.Errorf("failures = %v, want %q", rules(result), domain.RuleVersionSelector)
	}
}

// buildBadZip is deliberately local: it exists only to break several rules at
// once, which is not a shape any other suite needs.
func buildBadZip(t *testing.T) []byte {
	t.Helper()

	return fixtures.Custom([]fixtures.Entry{
		{Name: "apm.yml", Body: fixtures.Manifest("wrong/name", "9.9.9")},
		{Name: "../../escape.txt", Body: "traversal\n"},
	})
}
