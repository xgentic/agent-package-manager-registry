package domain_test

import (
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
)

func TestScopeSatisfies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		granted  domain.Scope
		required domain.Scope
		want     bool
	}{
		{"global read covers a package read", "read", "read:acme/web-skills", true},
		{"global read does not cover publish", "read", "publish:acme/web-skills", false},
		{"exact package read", "read:acme/web-skills", "read:acme/web-skills", true},
		{"read of another package", "read:acme/other", "read:acme/web-skills", false},
		{"exact publish", "publish:acme/web-skills", "publish:acme/web-skills", true},
		{"publish does not grant read", "publish:acme/web-skills", "read:acme/web-skills", false},
		{"owner wildcard covers its repos", "publish:acme/*", "publish:acme/web-skills", true},
		// The separator in the prefix is what stops acme/* leaking into acme-corp.
		{"owner wildcard does not cover a similarly named owner", "publish:acme/*", "publish:acme-corp/web-skills", false},
		{"owner wildcard does not cover another owner", "publish:acme/*", "publish:other/web-skills", false},
		{"multi-component owner wildcard", "publish:gitlab.com/acme/*", "publish:gitlab.com/acme/web-skills", true},
		{"global publish covers everything", "publish", "publish:gitlab.com/acme/web-skills", true},
		// A wildcard on the required side would be a caller bug; unsatisfiable
		// is the safe reading.
		{"wildcard requirement is never satisfied", "publish", "publish:acme/*", false},
		{"targetless requirement is never satisfied", "publish", "publish", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.granted.Satisfies(tt.required); got != tt.want {
				t.Errorf("Scope(%q).Satisfies(%q) = %v, want %v", tt.granted, tt.required, got, tt.want)
			}
		})
	}
}

func TestScopesSatisfiesAcrossGrants(t *testing.T) {
	t.Parallel()

	granted := domain.Scopes{"read", "publish:acme/*"}

	id, err := domain.ParseIdentity("acme/web-skills")
	if err != nil {
		t.Fatalf("ParseIdentity() error = %v", err)
	}
	other, err := domain.ParseIdentity("other/thing")
	if err != nil {
		t.Fatalf("ParseIdentity() error = %v", err)
	}

	if !granted.Satisfies(domain.ReadScope(id)) {
		t.Errorf("%v does not satisfy %q, want it to", granted, domain.ReadScope(id))
	}
	if !granted.Satisfies(domain.PublishScope(id)) {
		t.Errorf("%v does not satisfy %q, want it to", granted, domain.PublishScope(id))
	}
	if granted.Satisfies(domain.PublishScope(other)) {
		t.Errorf("%v satisfies %q, want it not to", granted, domain.PublishScope(other))
	}
}

func TestParseScope(t *testing.T) {
	t.Parallel()

	valid := []string{"read", "publish", "read:acme/web-skills", "publish:acme/*", "publish:gitlab.com/acme/web-skills"}
	for _, in := range valid {
		if _, err := domain.ParseScope(in); err != nil {
			t.Errorf("ParseScope(%q) error = %v, want nil", in, err)
		}
	}

	invalid := []string{"", "admin", "read:", "publish:/*", "read:acme", "publish:acme/../evil"}
	for _, in := range invalid {
		if _, err := domain.ParseScope(in); err == nil {
			t.Errorf("ParseScope(%q) error = nil, want a rejection", in)
		}
	}
}

func TestParseScopes(t *testing.T) {
	t.Parallel()

	got, err := domain.ParseScopes("read publish:acme/*")
	if err != nil {
		t.Fatalf("ParseScopes() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ParseScopes() = %v, want 2 scopes", got)
	}
	if want := "read publish:acme/*"; got.String() != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	if _, err := domain.ParseScopes("read nonsense"); err == nil {
		t.Error("ParseScopes() error = nil for an invalid grant, want a rejection")
	}
}
