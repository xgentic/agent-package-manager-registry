package domain

import (
	"strings"
	"unicode"
)

// Identity is a package identity in its canonical form: decoded, lowercased,
// and split into components (FR-16).
//
//	acme/web-skills                → 2 components
//	gitlab.com/acme/web-skills     → 3 components
//	dev.azure.com/org/proj/repo    → 4 components
//
// The zero value is not a valid Identity; use ParseIdentity.
type Identity struct {
	components []string
}

// Bounds. Generous enough for every origin MS-API §1.2 lists, tight enough that
// a pathological identity cannot become a storage or logging problem.
const (
	maxIdentityComponents = 8
	maxIdentityLength     = 255
)

// ParseIdentity validates and canonicalises a **decoded** identity.
//
// The input must already be percent-decoded — r.PathValue() returns decoded
// values, and validating the raw segment instead would let `acme%2F..%2Fevil`
// through (FR-15, ADR-0007). That case is exactly what the traversal check
// below exists for.
func ParseIdentity(decoded string) (Identity, error) {
	if decoded == "" {
		return Identity{}, invalidInput("package", "must not be empty")
	}
	if len(decoded) > maxIdentityLength {
		return Identity{}, invalidInput("package", "must be at most %d characters", maxIdentityLength)
	}
	// A backslash is a separator on Windows, so it is a traversal vector even
	// though path.Clean would not treat it as one.
	if strings.ContainsRune(decoded, '\\') {
		return Identity{}, invalidInput("package", "must not contain a backslash")
	}
	if strings.HasPrefix(decoded, "/") {
		return Identity{}, invalidInput("package", "must not be an absolute path")
	}

	components := strings.Split(strings.ToLower(decoded), "/")
	if len(components) < 2 {
		return Identity{}, invalidInput("package", "must have at least an owner and a repository")
	}
	if len(components) > maxIdentityComponents {
		return Identity{}, invalidInput("package", "must have at most %d components", maxIdentityComponents)
	}

	for _, c := range components {
		if err := validateIdentityComponent(c); err != nil {
			return Identity{}, err
		}
	}
	return Identity{components: components}, nil
}

// ParseIdentityParts builds an Identity from the two-segment path form
// (`/v1/packages/{owner}/{repo}/...`). Both forms canonicalise to the same
// value, which is what makes them the same package (FR-14, FR-16).
func ParseIdentityParts(owner, repo string) (Identity, error) {
	return ParseIdentity(owner + "/" + repo)
}

func validateIdentityComponent(c string) error {
	switch c {
	case "":
		return invalidInput("package", "must not contain an empty component")
	case ".", "..":
		return invalidInput("package", "must not contain a %q component", c)
	}

	hasAlphanumeric := false
	for _, r := range c {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			hasAlphanumeric = true
		case r == '.' || r == '-' || r == '_':
		case unicode.IsControl(r):
			return invalidInput("package", "must not contain control characters")
		default:
			return invalidInput("package", "component %q contains an unsupported character %q", c, r)
		}
	}
	if !hasAlphanumeric {
		return invalidInput("package", "component %q must contain a letter or digit", c)
	}
	return nil
}

// String is the canonical identity: what is stored, what lookups use, and what
// the `package` field echoes (FR-16).
func (i Identity) String() string { return strings.Join(i.components, "/") }

// Owner is every component but the last. For `gitlab.com/acme/web-skills` that
// is `gitlab.com/acme` — the unit that `publish:{owner}/*` grants over.
func (i Identity) Owner() string { return strings.Join(i.components[:len(i.components)-1], "/") }

// Repo is the last component: the bare package name, which is what a manifest
// is allowed to declare instead of the full identity (validation rule 6).
func (i Identity) Repo() string { return i.components[len(i.components)-1] }

// IsZero reports whether the Identity was never parsed.
func (i Identity) IsZero() bool { return len(i.components) == 0 }
