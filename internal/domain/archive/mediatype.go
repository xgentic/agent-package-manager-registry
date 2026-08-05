package archive

import (
	"fmt"
	"mime"
	"slices"
	"strings"
)

// The two media types MS-API defines for publish (§3.2, §3.3).
//
// The pair is a recorded conflict, not an oversight: `MS-API` allows both while
// OpenAPM's req-sc-004 leans towards tar.gz alone. Policy is isolated here so
// narrowing it is a config change rather than a code change (ADR-0004, FR-09).
const (
	MediaTypeZip  = "application/zip"
	MediaTypeGzip = "application/gzip"
)

// NormaliseMediaType strips parameters and lowercases, so
// `application/zip; charset=binary` is recognised as `application/zip`.
func NormaliseMediaType(header string) string {
	mt, _, err := mime.ParseMediaType(strings.TrimSpace(header))
	if err != nil {
		// A Content-Type we cannot parse is not one we accept; returning the
		// raw value keeps it out of the accepted set and out of the response.
		return strings.ToLower(strings.TrimSpace(header))
	}
	return strings.ToLower(mt)
}

// AcceptMediaType checks a request's Content-Type against the configured
// policy, returning the normalised type. Anything else is a 415 (FR-09).
func AcceptMediaType(accepted []string, header string) (string, error) {
	mt := NormaliseMediaType(header)
	if mt == "" {
		return "", fmt.Errorf("%w: a Content-Type is required", ErrUnsupportedMediaType)
	}
	if !slices.Contains(accepted, mt) {
		return "", fmt.Errorf("%w: %q; this registry accepts %s",
			ErrUnsupportedMediaType, mt, strings.Join(accepted, ", "))
	}
	return mt, nil
}
