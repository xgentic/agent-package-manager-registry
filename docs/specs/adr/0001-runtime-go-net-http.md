# ADR-0001 — Runtime and HTTP framework: Go + net/http

**Status** Accepted
**Date** 2026-08-05
**Relates to** [TR-01](../02-requirements.md#tr-01), [TR-02](../02-requirements.md#tr-02)

## Context

An earlier revision of this ADR recorded **Bun + Hono + TypeScript**, on the basis
that the runtime was a project constraint and the team's language is TypeScript.
That revision named Go as "genuinely better fit for a byte-pushing registry" and
rejected it only on those two organisational grounds.

**The constraint has since been lifted by explicit decision.** With it gone, the
reasoning that rejected Go no longer holds, and this ADR is rewritten rather than
superseded because the Bun decision was never committed or shipped — there is no
history to preserve.

The workload is narrow and unusual for a web app: three API endpoints, one of which
streams multi-megabyte binaries in and out, plus a small server-rendered UI and an
operator CLI ([UC-17](../01-use-cases.md#uc-17-run-and-operate-the-server)). The
dominant costs are byte movement, hashing and hostile-archive parsing — not JSON
serialisation or template rendering.

Most of the code will be written with AI coding agents, which makes the speed and
precision of the check-and-fix loop a first-class selection criterion.

## Decision

**Go (≥ 1.22) as runtime and language, `net/http` as the HTTP layer, no
third-party router.**

Properties we deliberately depend on:

1. **The compiler is the feedback loop.** `go build` and `go vet` turn shape
   mistakes into precise, located errors before anything runs. For agent-written
   code this is the single strongest correctness lever, and it is the reason the
   verification command is one command ([`make check`](../../../Makefile)).
2. **The hostile-input surface is standard library.** `archive/zip` reads the
   **central directory** as the authoritative entry list, which is exactly what
   [TR-12](../02-requirements.md#tr-12) requires; `archive/tar` + `compress/gzip`
   cover [ADR-0004](0004-archive-format.md); `io.LimitedReader` bounds
   decompression for [TR-13](../02-requirements.md#tr-13). The previous revision
   listed this as its main *negative* — needing vetted libraries or hand-rolled
   parsers. In Go it is the strongest positive.
3. **Streaming hashing is stdlib and idiomatic.** `crypto/sha256` is an
   `io.Writer`, so `io.TeeReader` computes the digest during upload without
   buffering ([TR-09](../02-requirements.md#tr-09)).
4. **`crypto/subtle.ConstantTimeCompare`** satisfies
   [TR-18](../02-requirements.md#tr-18) with no dependency.
5. **Method-aware routing since Go 1.22.** `GET /v1/packages/{owner}/{repo}/versions`
   is expressible in `ServeMux` directly, so no third-party router is needed.
6. **`http.Handler` is the testable seam.** Every route is exercised through
   `httptest` in-process without binding a port
   ([TR-02](../02-requirements.md#tr-02)), the same property the previous revision
   valued in Hono's `app.fetch`.
7. **Single static binary.** One artefact serves the API, the web UI and the
   operator CLI ([ADR-0013](0013-operator-cli.md)), and containers can be
   `FROM scratch`.
8. **The Go 1 compatibility promise.** Training data from years ago still
   compiles, which is the single biggest suppressant of hallucinated APIs in
   agent-written code.

Constraints we impose on ourselves in exchange:

- **`net/http` types are confined to the HTTP layer.** `domain/`, `services/` and
  `ports/` take and return plain values and never see `http.ResponseWriter` or
  `*http.Request`; they raise typed errors that the HTTP layer maps to RFC 7807
  ([ADR-0006](0006-rfc7807-errors.md)).
- **Errors go through one envelope.** Every error path calls `writeProblem`.
  `http.Error` and bare JSON error objects are not acceptable.

## Consequences

**Positive**

- The archive-parsing and crypto surface — the highest-risk part of the system
  ([TR-11](../02-requirements.md#tr-11)–[TR-13](../02-requirements.md#tr-13),
  [TR-17](../02-requirements.md#tr-17)–[TR-18](../02-requirements.md#tr-18)) — is
  almost entirely standard library, so the hostile-input path carries close to no
  third-party dependency risk.
- Deploys as one static binary; no runtime to install, no build step, no lockfile
  drift.
- `go vet` and the race detector (`go test -race`) are built in.

**Negative**

- **This is not the team's primary language.** The previous revision rejected Go
  partly on this ground and it remains true; the decision accepts a real ramp-up
  cost in exchange for the properties above. Recorded plainly so nobody later
  reads this ADR as claiming the cost away.
- Argon2id ([TR-17](../02-requirements.md#tr-17)) is not in the standard library
  and needs `golang.org/x/crypto/argon2`.
- SQLite ([ADR-0002](0002-metadata-store.md)) is not in the standard library.
  `modernc.org/sqlite` (pure Go, cgo-free) preserves the static-binary property;
  a cgo driver would forfeit it.
- More ceremony than TypeScript for JSON shapes — explicit structs and tags rather
  than inferred types. For a contract specified externally this is an acceptable
  trade, but it means request decoding is written by hand with no Zod-style
  inference to lean on.

**Neutral**

- The web UI ([ADR-0010](0010-web-ui.md)) renders server-side with `html/template`.
  It was already specified as server-rendered over the same domain services, so
  the change of language does not change that decision — only its implementation.
- Version selectors stay opaque strings; no semver library is required in the
  server at all, because range resolution is entirely client-side
  ([UC-06](../01-use-cases.md#uc-06-resolve-a-semver-range)).

## Alternatives considered

**Bun + Hono + TypeScript.** The previous decision, and a genuinely good one: Hono
gives real end-to-end type inference, Bun consolidates runtime, package manager,
test runner, SQLite and argon2 into one binary, and web-standard
`Request`/`Response` makes the API testable in-process. It loses here because its
main advantages — ecosystem reach and shared types with a browser client — are
worth little to an upload-and-browse admin UI that must reuse the same domain
services as the API ([UC-12](../01-use-cases.md#uc-12-upload-an-artefact-through-the-web-ui)),
while its main weakness is precisely this project's highest-risk surface: archive
parsing.

**Node + Fastify.** Real types via JSON-schema type providers and the easiest
institutional sell of the JavaScript options, but adds a build step, an external
SQLite native module and an argon2 dependency, for no benefit to a byte-moving
workload.

**Rust.** Better still on raw byte-pushing and memory safety, and `zip`/`sha2` are
mature. Rejected on iteration speed: the compile-and-fix loop is materially slower,
which is the loop this project optimises for.

**A third-party Go router (chi, echo, gin).** Unnecessary since Go 1.22.
`ServeMux` handles method-and-path patterns, including the dual-form identity
routing in [ADR-0007](0007-package-identity.md) — to be verified against the
percent-encoded single-segment form, not assumed.
