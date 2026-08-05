package domain_test

import (
	"errors"
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
)

func TestParseManifest(t *testing.T) {
	t.Parallel()

	m, err := domain.ParseManifest([]byte(`
name: acme/web-skills
version: 1.2.0
description: A skill library
`))
	if err != nil {
		t.Fatalf("ParseManifest() error = %v, want nil", err)
	}
	if m.Name != "acme/web-skills" {
		t.Errorf("Name = %q, want %q", m.Name, "acme/web-skills")
	}
	if m.Version != "1.2.0" {
		t.Errorf("Version = %q, want %q", m.Version, "1.2.0")
	}
	if got := m.Fields["description"]; got != "A skill library" {
		t.Errorf("Fields[description] = %v, want the raw value preserved", got)
	}
}

// `version: 1.0` is a YAML float and `version: 2` an int. Treating either as a
// missing field would reject a manifest a producer reasonably wrote.
func TestParseManifestAcceptsNonStringScalars(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"version: 1.0\nname: acme/skills":   "1.0",
		"version: 2\nname: acme/skills":     "2",
		"version: '1.0'\nname: acme/skills": "1.0",
	}

	for src, want := range tests {
		m, err := domain.ParseManifest([]byte(src))
		if err != nil {
			t.Fatalf("ParseManifest(%q) error = %v", src, err)
		}
		if m.Version != want {
			t.Errorf("ParseManifest(%q).Version = %q, want %q", src, m.Version, want)
		}
	}
}

func TestParseManifestRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		src      string
		wantRule domain.RuleName
	}{
		{"not YAML", "name: [unclosed\n  : :", domain.RuleManifestYAML},
		{"a scalar, not a mapping", "just a string", domain.RuleManifestYAML},
		{"empty document", "", domain.RuleManifestFields},
		{"missing version", "name: acme/skills", domain.RuleManifestFields},
		{"missing name", "version: 1.0.0", domain.RuleManifestFields},
		{"blank name", "name: '   '\nversion: 1.0.0", domain.RuleManifestFields},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := domain.ParseManifest([]byte(tt.src))
			if err == nil {
				t.Fatalf("ParseManifest(%q) error = nil, want a rejection", tt.src)
			}
			if !errors.Is(err, domain.ErrArchiveInvalid) {
				t.Errorf("error = %v, want it to wrap ErrArchiveInvalid", err)
			}

			var me *domain.ManifestError
			if !errors.As(err, &me) {
				t.Fatalf("error type = %T, want *domain.ManifestError", err)
			}
			if me.Rule != tt.wantRule {
				t.Errorf("Rule = %q, want %q", me.Rule, tt.wantRule)
			}
		})
	}
}

// Validation rule 6 accepts the full identity or the bare repo name; real
// manifests use both (risk R-9).
func TestManifestMatchesIdentity(t *testing.T) {
	t.Parallel()

	id, err := domain.ParseIdentity("acme/web-skills")
	if err != nil {
		t.Fatalf("ParseIdentity() error = %v", err)
	}

	tests := []struct {
		name string
		want bool
	}{
		{"acme/web-skills", true},
		{"web-skills", true},
		{"Acme/Web-Skills", true},
		{"acme/other", false},
		{"other", false},
		{"", false},
	}

	for _, tt := range tests {
		m := domain.Manifest{Name: tt.name}
		if got := m.MatchesIdentity(id); got != tt.want {
			t.Errorf("Manifest{Name:%q}.MatchesIdentity(%q) = %v, want %v", tt.name, id, got, tt.want)
		}
	}
}
