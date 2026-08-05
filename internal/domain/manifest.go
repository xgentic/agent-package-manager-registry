package domain

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/xgentic/agent-package-manager-registry/internal/domain/archive"
)

// ManifestFileName is the manifest a registry archive must carry at its root.
// This is the flat registry-archive layout produced by `apm publish`, not the
// `apm pack` plugin-bundle layout (docs/specs/04-api-contract.md §3.3).
//
// Defined once, in the package that has to recognise it while walking an
// archive.
const ManifestFileName = archive.ManifestFileName

// maxManifestBytes bounds the YAML the parser will look at. A manifest is a
// handful of scalar fields; anything larger is either a mistake or an attempt
// to make the parser the expensive part of a publish.
const maxManifestBytes = 1 << 20 // 1 MiB

// Manifest is apm.yml reduced to what the registry needs. Everything else is
// preserved in Fields so it can be stored verbatim without this type having to
// track the client's schema.
type Manifest struct {
	Name    string
	Version string
	Fields  map[string]any
}

// ManifestError distinguishes "not YAML" from "YAML without the required
// fields", because they are separate rules on the wire (validation rules 4 and
// 5, and the `manifest_yaml` / `manifest_fields` values of §4).
type ManifestError struct {
	Rule    RuleName
	Message string
}

func (e *ManifestError) Error() string { return fmt.Sprintf("%s: %s", e.Rule, e.Message) }

func (e *ManifestError) Is(target error) bool { return target == ErrArchiveInvalid }

// ParseManifest reads apm.yml and requires `name` and `version` (FR-10 rules
// 4–5). The content is untrusted archive input, so nothing here is interpolated
// anywhere (TR-20) and the document size is bounded.
func ParseManifest(data []byte) (Manifest, error) {
	if len(data) > maxManifestBytes {
		return Manifest{}, &ManifestError{
			Rule:    RuleManifestYAML,
			Message: fmt.Sprintf("%s is larger than %d bytes", ManifestFileName, maxManifestBytes),
		}
	}

	// Decoding through yaml.Node rather than map[string]any keeps scalars as
	// their source text. `version: 1.0` decodes to the float 1 through a
	// generic map, and a version selector is opaque — it must survive exactly
	// as written (FR-17).
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Manifest{}, &ManifestError{
			Rule: RuleManifestYAML,
			// yaml's message names a line and column, which is useful to a
			// producer and reveals nothing about the server.
			Message: fmt.Sprintf("%s is not valid YAML: %s", ManifestFileName, firstLine(err.Error())),
		}
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return Manifest{}, &ManifestError{
			Rule:    RuleManifestFields,
			Message: fmt.Sprintf("%s is empty", ManifestFileName),
		}
	}

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return Manifest{}, &ManifestError{
			Rule:    RuleManifestYAML,
			Message: fmt.Sprintf("%s must be a YAML mapping", ManifestFileName),
		}
	}

	fields := map[string]any{}
	if err := root.Decode(&fields); err != nil {
		return Manifest{}, &ManifestError{
			Rule:    RuleManifestYAML,
			Message: fmt.Sprintf("%s is not a mapping of fields: %s", ManifestFileName, firstLine(err.Error())),
		}
	}

	m := Manifest{
		Name:    scalarText(root, "name"),
		Version: scalarText(root, "version"),
		Fields:  fields,
	}

	var missing []string
	if m.Name == "" {
		missing = append(missing, "name")
	}
	if m.Version == "" {
		missing = append(missing, "version")
	}
	if len(missing) > 0 {
		return Manifest{}, &ManifestError{
			Rule:    RuleManifestFields,
			Message: fmt.Sprintf("%s is missing required field(s): %s", ManifestFileName, strings.Join(missing, ", ")),
		}
	}
	return m, nil
}

// MatchesIdentity reports whether the manifest's declared name names the
// package being published. Validation rule 6 accepts the full identity or its
// bare repo-name suffix — real manifests use both, and rejecting the short form
// would fail legitimate publishes (risk R-9).
func (m Manifest) MatchesIdentity(id Identity) bool {
	name := strings.ToLower(strings.TrimSpace(m.Name))
	return name == id.String() || name == id.Repo()
}

// scalarText returns a top-level scalar field as its source text. Non-scalar
// values (a list, a nested mapping) read as absent, which is the same outcome
// as omitting the field: a rule-5 failure rather than a confusing type error.
func scalarText(mapping *yaml.Node, key string) string {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k, v := mapping.Content[i], mapping.Content[i+1]
		if k.Value != key {
			continue
		}
		if v.Kind != yaml.ScalarNode || v.Tag == "!!null" {
			return ""
		}
		return strings.TrimSpace(v.Value)
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
