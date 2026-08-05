package domain

import (
	"errors"
	"fmt"

	"github.com/xgentic/agent-package-manager-registry/internal/domain/archive"
)

// ValidationInput is everything the eight publish rules judge.
type ValidationInput struct {
	Package Identity
	Version Version

	// Inspection is what the archive turned out to contain. The parser reports
	// facts; this file decides what they mean on the wire.
	Inspection archive.Inspection

	// RequireVersionMatch enforces `apm.yml.version` == URL version. MS-API
	// §6.5 leaves this to registry policy; ours defaults to strict
	// (APM_REGISTRY_REQUIRE_MANIFEST_VERSION_MATCH).
	RequireVersionMatch bool
}

// ValidationResult is the outcome of the pipeline: every failure, plus the
// parsed manifest when there was one.
type ValidationResult struct {
	Manifest Manifest
	Failures []RuleFailure
}

// OK reports whether the publish may proceed.
func (r ValidationResult) OK() bool { return len(r.Failures) == 0 }

// Validate applies the eight rules of MS-API §6 (FR-10).
//
// **No rule short-circuits.** Every one runs and every failure is collected, so
// a 422 tells a producer everything that is wrong in one round-trip rather than
// one thing per attempt. Returning early on the first failure would be the
// natural Go shape here and would be wrong.
func Validate(in ValidationInput) ValidationResult {
	var result ValidationResult
	fail := func(rule RuleName, entry, format string, args ...any) {
		result.Failures = append(result.Failures, RuleFailure{
			Rule:    rule,
			Message: fmt.Sprintf(format, args...),
			Entry:   entry,
		})
	}

	// Rule 1 — version selector. The handler has already parsed it; this
	// re-states the rule so the pipeline is complete on its own terms and a
	// non-HTTP caller (the CLI, a future import) cannot skip it.
	if _, err := ParseVersion(in.Version.String()); err != nil {
		fail(RuleVersionSelector, "", "version selector %q is not usable: %v", in.Version, err)
	}

	// Rule 2 — the archive parsed. A body that would not open at all never
	// reaches here (that is a 400); these are members that would not
	// decompress.
	for _, f := range in.Inspection.ParseFailures {
		fail(RuleArchiveParse, f.Name, "entry %q could not be read: %s", f.Name, f.Reason)
	}

	// Rule 7 — entry safety. Reported before the manifest rules because a
	// traversal entry is the more serious finding.
	for _, u := range in.Inspection.Unsafe {
		fail(RuleEntrySafety, u.Name, "entry %q %s", u.Name, u.Reason)
	}

	// Rule 8 — limits.
	switch in.Inspection.LimitExceeded {
	case archive.LimitEntries:
		fail(RuleArchiveLimits, "", "archive contains more than the permitted number of entries")
	case archive.LimitUncompressed:
		fail(RuleArchiveLimits, "", "archive expands beyond the permitted uncompressed size")
	}

	// Rules 3–6 — the manifest.
	if !in.Inspection.ManifestFound {
		fail(RuleManifestPresent, "", "no %s found at the archive root", archive.ManifestFileName)
		return result
	}

	manifest, err := ParseManifest(in.Inspection.Manifest)
	if err != nil {
		var me *ManifestError
		if errors.As(err, &me) {
			fail(me.Rule, archive.ManifestFileName, "%s", me.Message)
		} else {
			fail(RuleManifestYAML, archive.ManifestFileName, "%s could not be parsed", archive.ManifestFileName)
		}
		return result
	}
	result.Manifest = manifest

	if !manifest.MatchesIdentity(in.Package) {
		fail(RuleManifestNameMatch, archive.ManifestFileName,
			"%s declares name %q, which does not match the published identity %q",
			archive.ManifestFileName, manifest.Name, in.Package)
	}
	if in.RequireVersionMatch && manifest.Version != in.Version.String() {
		fail(RuleManifestVersionMatch, archive.ManifestFileName,
			"%s declares version %q, which does not match the published version %q",
			archive.ManifestFileName, manifest.Version, in.Version)
	}
	return result
}
