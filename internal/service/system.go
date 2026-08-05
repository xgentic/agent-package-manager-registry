package service

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// SystemClock is the production Clock.
type SystemClock struct{}

// Now returns UTC. Everything the registry stores or emits is UTC — MS-API
// timestamps are ISO 8601 UTC, and a local-time row would be a silent bug.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// FixedClock is a Clock that does not move, for tests that assert exact
// response bodies.
type FixedClock struct{ Time time.Time }

func (c FixedClock) Now() time.Time { return c.Time.UTC() }

// RandomIDs generates opaque row identifiers.
//
// They are internal surrogate keys — never a digest, never anything a client
// sees — so the only requirements are uniqueness and unguessability.
type RandomIDs struct{}

func (RandomIDs) NewID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failing means the system has no entropy source, which is
		// not a condition a registry can paper over with a fallback.
		panic("service: cannot read random bytes for an id: " + err.Error())
	}
	return hex.EncodeToString(buf[:])
}
