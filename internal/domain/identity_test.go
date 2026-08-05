package domain_test

import (
	"errors"
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
)

func TestParseIdentityAccepts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		in        string
		want      string
		wantOwner string
		wantRepo  string
	}{
		{"github two segments", "acme/web-skills", "acme/web-skills", "acme", "web-skills"},
		{"gitlab percent-decoded", "gitlab.com/acme/web-skills", "gitlab.com/acme/web-skills", "gitlab.com/acme", "web-skills"},
		{"azure devops", "dev.azure.com/org/proj/repo", "dev.azure.com/org/proj/repo", "dev.azure.com/org/proj", "repo"},
		// FR-16: the canonical form is lowercase, so both spellings are one package.
		{"uppercase canonicalised", "Acme/Web-Skills", "acme/web-skills", "acme", "web-skills"},
		{"underscores and dots", "acme_corp/web.skills", "acme_corp/web.skills", "acme_corp", "web.skills"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			id, err := domain.ParseIdentity(tt.in)
			if err != nil {
				t.Fatalf("ParseIdentity(%q) error = %v, want nil", tt.in, err)
			}
			if got := id.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
			if got := id.Owner(); got != tt.wantOwner {
				t.Errorf("Owner() = %q, want %q", got, tt.wantOwner)
			}
			if got := id.Repo(); got != tt.wantRepo {
				t.Errorf("Repo() = %q, want %q", got, tt.wantRepo)
			}
		})
	}
}

func TestParseIdentityRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"single component", "acme"},
		{"trailing slash leaves an empty component", "acme/"},
		{"leading slash", "/acme/web-skills"},
		{"absolute path", "/etc/passwd"},
		// The decisive case: `acme%2F..%2Fevil` matches the single-segment route
		// and PathValue hands the handler this decoded string (ADR-0007, FR-15).
		{"traversal after decoding", "acme/../evil"},
		{"dot component", "acme/./web-skills"},
		{"double dot at the end", "acme/.."},
		{"backslash separator", `acme\web-skills`},
		{"control character", "acme/web\x00skills"},
		{"newline", "acme/web\nskills"},
		{"space", "acme/web skills"},
		{"colon", "acme/web:skills"},
		{"punctuation-only component", "acme/---"},
		{"too many components", "a/b/c/d/e/f/g/h/i"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := domain.ParseIdentity(tt.in); err == nil {
				t.Fatalf("ParseIdentity(%q) error = nil, want a rejection", tt.in)
			} else if !errors.Is(err, domain.ErrBadRequest) {
				t.Errorf("ParseIdentity(%q) error = %v, want it to wrap ErrBadRequest", tt.in, err)
			}
		})
	}
}

// FR-14: the two path shapes are the same resource, so they must produce the
// same canonical identity.
func TestBothPathFormsCanonicaliseIdentically(t *testing.T) {
	t.Parallel()

	fromParts, err := domain.ParseIdentityParts("Acme", "Web-Skills")
	if err != nil {
		t.Fatalf("ParseIdentityParts() error = %v", err)
	}
	fromEncoded, err := domain.ParseIdentity("Acme/Web-Skills") // as PathValue decodes %2F
	if err != nil {
		t.Fatalf("ParseIdentity() error = %v", err)
	}

	if fromParts.String() != fromEncoded.String() {
		t.Errorf("two-segment form = %q, percent-encoded form = %q; want identical",
			fromParts, fromEncoded)
	}
}
