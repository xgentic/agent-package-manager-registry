package domain_test

import (
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
)

const validHex = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func TestNewDigestRoundTrips(t *testing.T) {
	t.Parallel()

	sum := sha256.Sum256([]byte("archive bytes"))
	d := domain.NewDigest(sum[:])

	parsed, err := domain.ParseDigest(d.String())
	if err != nil {
		t.Fatalf("ParseDigest(%q) error = %v, want nil", d, err)
	}
	if parsed != d {
		t.Errorf("round trip = %q, want %q", parsed, d)
	}
	if got := len(d.Bytes()); got != sha256.Size {
		t.Errorf("Bytes() length = %d, want %d", got, sha256.Size)
	}
	if strings.HasPrefix(d.Hex(), domain.DigestPrefix) {
		t.Errorf("Hex() = %q, want it without the algorithm prefix", d.Hex())
	}
}

func TestParseDigestRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"no prefix", validHex},
		{"wrong algorithm", "sha512:" + validHex},
		{"too short", "sha256:abc123"},
		{"too long", "sha256:" + validHex + "ff"},
		{"not hex", "sha256:" + strings.Repeat("z", 64)},
		{"uppercase hex", "sha256:" + strings.ToUpper(validHex)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := domain.ParseDigest(tt.in); err == nil {
				t.Fatalf("ParseDigest(%q) error = nil, want a rejection", tt.in)
			} else if !errors.Is(err, domain.ErrBadRequest) {
				t.Errorf("error = %v, want it to wrap ErrBadRequest", err)
			}
		})
	}
}

func TestDigestEqual(t *testing.T) {
	t.Parallel()

	a := domain.Digest("sha256:" + validHex)
	b := domain.Digest("sha256:" + validHex)
	c := domain.Digest("sha256:" + strings.Repeat("a", 64))

	if !a.Equal(b) {
		t.Error("Equal() = false for identical digests")
	}
	if a.Equal(c) {
		t.Error("Equal() = true for different digests")
	}
}
