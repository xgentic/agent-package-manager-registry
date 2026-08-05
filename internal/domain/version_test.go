package domain_test

import (
	"errors"
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
)

func TestParseVersionAcceptsOpaqueSelectors(t *testing.T) {
	t.Parallel()

	// FR-17: opaque means we accept whatever a producer publishes, including
	// things no semver parser would like.
	accepted := []string{
		"1.0.0",
		"v1.0.0",
		"1.0.0-rc.1+build.5",
		"2026.08.05",
		"latest",
		"1.0.0_final",
	}

	for _, in := range accepted {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			v, err := domain.ParseVersion(in)
			if err != nil {
				t.Fatalf("ParseVersion(%q) error = %v, want nil", in, err)
			}
			if v.String() != in {
				t.Errorf("ParseVersion(%q) = %q; the selector must survive unchanged", in, v)
			}
		})
	}
}

func TestParseVersionRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"control character", "1.0.0\x00"},
		{"newline", "1.0\n0"},
		{"tab", "1.0\t0"},
		// Rule 1: a selector is a key, never a path.
		{"forward slash", "1.0/0"},
		{"backslash", `1.0\0`},
		{"dot", "."},
		{"double dot", ".."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := domain.ParseVersion(tt.in); err == nil {
				t.Fatalf("ParseVersion(%q) error = nil, want a rejection", tt.in)
			} else if !errors.Is(err, domain.ErrBadRequest) {
				t.Errorf("error = %v, want it to wrap ErrBadRequest", err)
			}
		})
	}
}

// FR-17: case-sensitive. This is also why the versions table uses a BINARY
// collation — a case-insensitive one would merge these two and reject a
// legitimate publish as a conflict.
func TestVersionsAreCaseSensitive(t *testing.T) {
	t.Parallel()

	lower, err := domain.ParseVersion("v1.0")
	if err != nil {
		t.Fatalf("ParseVersion() error = %v", err)
	}
	upper, err := domain.ParseVersion("V1.0")
	if err != nil {
		t.Fatalf("ParseVersion() error = %v", err)
	}

	if lower == upper {
		t.Errorf("%q and %q compared equal; version selectors are case-sensitive", lower, upper)
	}
}
