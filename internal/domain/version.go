package domain

import (
	"strings"
	"unicode"
)

// Version is a version selector: an **opaque, case-sensitive** string.
//
// The server performs no semver parsing, normalisation, reordering or filtering
// (FR-17). `V1.0` and `v1.0` are different versions, and this package contains
// no semver import by design — range resolution is entirely client-side.
type Version string

const maxVersionLength = 256

// ParseVersion validates a **decoded** version selector (validation rule 1).
//
// The selector is a key, never a filesystem path, so the separators that would
// make it one are rejected outright rather than sanitised.
func ParseVersion(decoded string) (Version, error) {
	switch {
	case decoded == "":
		return "", invalidInput("version", "must not be empty")
	case len(decoded) > maxVersionLength:
		return "", invalidInput("version", "must be at most %d characters", maxVersionLength)
	case decoded == "." || decoded == "..":
		return "", invalidInput("version", "must not be %q", decoded)
	case strings.ContainsAny(decoded, `/\`):
		return "", invalidInput("version", "must not contain a path separator")
	}

	for _, r := range decoded {
		if unicode.IsControl(r) {
			return "", invalidInput("version", "must not contain control characters")
		}
	}
	return Version(decoded), nil
}

func (v Version) String() string { return string(v) }
