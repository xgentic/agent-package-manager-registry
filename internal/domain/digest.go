package domain

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

// DigestPrefix is the only algorithm this registry speaks. Hash agility is
// explicitly deferred upstream, so a second prefix would be inventing contract.
const DigestPrefix = "sha256:"

// digestHexLength is sha256's hex width.
const digestHexLength = sha256.Size * 2

// Digest is `sha256:` followed by 64 lowercase hex characters (FR-02).
//
// It is the trust anchor for consumer lockfiles: whatever /download serves must
// hash to the digest /versions advertised, forever.
type Digest string

// ParseDigest validates the wire form.
func ParseDigest(s string) (Digest, error) {
	if !strings.HasPrefix(s, DigestPrefix) {
		return "", invalidInput("digest", "must start with %q", DigestPrefix)
	}

	hexPart := strings.TrimPrefix(s, DigestPrefix)
	if len(hexPart) != digestHexLength {
		return "", invalidInput("digest", "must be %d hex characters, got %d", digestHexLength, len(hexPart))
	}
	// Uppercase hex would parse but would not be byte-equal to what we emit,
	// and clients compare digest strings.
	if hexPart != strings.ToLower(hexPart) {
		return "", invalidInput("digest", "must be lowercase")
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return "", invalidInput("digest", "is not hexadecimal")
	}
	return Digest(s), nil
}

// NewDigest builds a Digest from a raw 32-byte sha256 sum, as returned by
// hash.Hash.Sum(nil).
func NewDigest(sum []byte) Digest { return Digest(DigestPrefix + hex.EncodeToString(sum)) }

func (d Digest) String() string { return string(d) }

// Hex is the digest without its algorithm prefix — the form the blob store
// uses for its directory layout.
func (d Digest) Hex() string { return strings.TrimPrefix(string(d), DigestPrefix) }

// Bytes is the raw digest, which the RFC 3230 `Digest` response header carries
// base64-encoded.
func (d Digest) Bytes() []byte {
	raw, err := hex.DecodeString(d.Hex())
	if err != nil {
		return nil
	}
	return raw
}

// Equal compares in constant time (TR-18). Digest comparison decides whether
// bytes are served, so it is not a place for an early-exit comparison.
func (d Digest) Equal(other Digest) bool {
	return subtle.ConstantTimeCompare([]byte(d), []byte(other)) == 1
}
