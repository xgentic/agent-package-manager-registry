# AGENTS.md

Orientation for coding agents working in this repository. Human-facing setup and
usage live in [README.md](README.md); this file is about **where the knowledge
is** and **which invariants must not be broken**.

## The check loop

```sh
make check      # build + vet + test — keep this green
```

One command verifies the codebase. Run it before considering any change done.

## What this is

A self-hosted package registry server. Clients speak a registry HTTP API to
`apm install` and `apm publish` packages. We do not build the client — it is a
fixed implementation we must satisfy.

Written in Go against the standard library, no third-party router.

## Documentation map

Read in this order when you need context. Everything is under [docs/](docs/).

| Doc | Read it when |
|---|---|
| [docs/ROADMAP.md](docs/ROADMAP.md) | **Start here for "what should I build next".** MVP ladder, modules, tasks with objective *Done when* criteria, risk register, progress log. |
| [docs/specs/00-glossary.md](docs/specs/00-glossary.md) | Before using any domain term. Precise meanings; note the `repo` overload (identity component vs. registry namespace). |
| [docs/specs/01-use-cases.md](docs/specs/01-use-cases.md) | You need the behaviour of a flow end to end (`UC-xx`). |
| [docs/specs/02-requirements.md](docs/specs/02-requirements.md) | You need the exact obligation. Functional `FR-xx`, technical `TR-xx`, each with its upstream source. |
| [docs/specs/03-architecture.md](docs/specs/03-architecture.md) | You need layering, module layout, data model, or a request flow. |
| [docs/specs/04-api-contract.md](docs/specs/04-api-contract.md) | **You are touching an HTTP handler.** Exact wire shapes, status codes, headers. |
| [docs/specs/05-traceability.md](docs/specs/05-traceability.md) | You want to know *why* a requirement exists, or where upstream sources conflict. |
| [docs/specs/adr/](docs/specs/adr/) | You are about to make or revisit a design decision. |

## Non-negotiable invariants

These are the ways this project breaks **silently**. Each traces to a spec
requirement; none is stylistic.

### 1. JSON field names are `snake_case`

Go marshals untagged fields as `PublishedAt`. The reference client reads only
`published_at` and **ignores unknown fields without erroring** — so a missing
struct tag produces a broken `apm install` with no error anywhere.

```go
PublishedAt time.Time `json:"published_at"`   // always tag it
```
[FR-04](docs/specs/02-requirements.md#fr-04), [TR-23](docs/specs/02-requirements.md#tr-23)

### 2. Every error goes through `writeProblem`

RFC 7807 `application/problem+json` on **all** 4xx/5xx. Never `http.Error` (it
writes `text/plain` and fails the contract outright), never a bare JSON object.
[FR-26](docs/specs/02-requirements.md#fr-26), [ADR-0006](docs/specs/adr/0006-rfc7807-errors.md)

### 3. Read path params with `PathValue`, never `r.URL.Path`

`r.URL.Path` is already **decoded**, so a percent-encoded package identity reads
as six segments instead of one. `ServeMux` matches on the *escaped* path.
[ADR-0007](docs/specs/adr/0007-package-identity.md)

### 4. Validate identity *after* decoding

`acme%2F..%2Fevil` matches a route, returns 200, and `PathValue` yields
`acme/../evil`. Traversal checks must run on the **decoded** value — validating
the raw segment lets it straight through.
[FR-15](docs/specs/02-requirements.md#fr-15)

### 5. Archive bytes are immutable and never transcoded

`sha256(bytes served)` must equal the advertised digest, forever. Consumer
lockfiles record it and re-verify on every frozen install. Never re-zip,
recompress or normalise. Storage is content-addressed so this is structural.
`req-rg-001`, [FR-07](docs/specs/02-requirements.md#fr-07), [ADR-0003](docs/specs/adr/0003-content-addressed-storage.md)

### 6. Version conflicts come from the DB constraint

`UNIQUE (package_id, version)` — not a `SELECT` then `INSERT`. That is a race,
and with content-addressed blobs the losing write is a silent no-op.
[TR-08](docs/specs/02-requirements.md#tr-08), [ADR-0008](docs/specs/adr/0008-version-immutability.md)

### 7. `GET /versions` never paginates

Semver range resolution happens client-side over the returned list. A truncated
list produces a silently *wrong* resolution rather than an error. (`/v1/search`
may paginate — it is a vendor extension.)
[FR-19](docs/specs/02-requirements.md#fr-19)

### 8. Version selectors are opaque and case-sensitive

No semver parsing, normalising, reordering or filtering server-side. `stable`,
`main` and `v1.2.3` are all legitimate versions. `V1.0` ≠ `v1.0`.
[FR-17](docs/specs/02-requirements.md#fr-17)

### 9. `internal/domain` imports no `net/http` and no `database/sql`

Validation rules are pure functions over parsed input. If the domain needs an
HTTP or SQL type, the design is wrong.
[docs/specs/03-architecture.md §3](docs/specs/03-architecture.md#3-layering)

### 10. All eight publish rules run — none short-circuits

`422` bodies list **every** failure in `extensions.errors[]` so a producer fixes
everything in one round-trip.
[FR-10](docs/specs/02-requirements.md#fr-10)

### 11. Do not remove the auth seam

Handlers resolve a `Principal` and call `Scope.Satisfies()` **now**, backed by a
no-op all-scopes middleware. It looks like dead code. It is what makes MVP 3 a
middleware change instead of a rewrite — deleting it is how the auth retrofit
becomes a rewrite. Marked `TODO(MVP3)`.
[ROADMAP risk R-2](docs/ROADMAP.md#risk-register)

## Where code goes

```
main.go                  server lifecycle; becomes the CLI dispatcher
internal/config/         env parsing, fail-fast validation
internal/domain/         pure: identity, version, digest, scope, manifest, validation
internal/domain/archive/ hostile-input surface — wraps archive/zip, archive/tar
internal/service/        PublishService, QueryService (+ the interfaces they consume)
internal/store/sqlite/   MetadataStore implementation + migrations
internal/store/blob/     content-addressed filesystem BlobStore
internal/server/         HTTP layer: routing, handlers, middleware, writeProblem
internal/conformance/    contract compliance tests
internal/cli/            operator CLI: serve, migrate, repo
internal/fixtures/       valid and malicious archives, generated
web/                     React + Vite source (MVP 2)
```

Go idiom: **interfaces are declared in the consuming package**, not a shared
`ports` package. Tests sit beside code as `*_test.go`; hostile archive fixtures
live in `testdata/` so the Go tool ignores them.

## Conventions

- **Archives use the standard library.** `archive/zip` reads the central
  directory by construction — do not hand-roll a parser on the attack surface.
- **Bound everything from an upload.** `http.MaxBytesReader` for the request,
  `io.LimitedReader` during decompression. Declared sizes are attacker-controlled.
- **Stream, don't buffer.** `io.TeeReader` into `sha256.New()` while writing a
  temp file; memory must not scale with archive size.
- **Clean up temp files with `defer`** on every path, including client disconnect.
- **Decode request bodies explicitly** into a struct and validate field by field.
- Prefer table-driven tests.

## Current status

**Phase 0 and [MVP 1](docs/ROADMAP.md#mvp-1--a-working-registry) are complete.**
The three core endpoints work in both identity path forms, over SQLite
metadata and content-addressed blob storage, behind the operator CLI
(`serve`, `migrate`, `repo`). The contract compliance test suite passes.
`make test-e2e` round-trips through the real `apm` client (v0.27.0).

Next is [MVP 2](docs/ROADMAP.md#mvp-2--react-web-ui) (web UI) or
[MVP 3](docs/ROADMAP.md#mvp-3--authentication-and-authorisation) (auth) — the
ladder allows either.

Two things to know before changing anything here:

- **Hostile archive fixtures are generated, not committed as blobs.** They live
  in `internal/fixtures` as code so each attack is legible; `go test
  ./internal/fixtures` also writes them to `testdata/archives/` for manual
  replay.
- **The auth seam is live and always passes.** `authenticate` in
  `internal/server/middleware.go` returns an all-scopes principal behind one
  `TODO(MVP3)`. Every package route already resolves a `Principal` and calls
  `Scope.Satisfies`.

⚠️ **MVP 1 and MVP 2 ship without authentication by design.** Auth is
[MVP 3](docs/ROADMAP.md#mvp-3--authentication-and-authorisation). Until then the
server must not be exposed to an untrusted network — every endpoint is open.

## When specs and code disagree

The specs are the baseline; the code is the implementation. If you find a real
conflict, say so rather than silently picking one — several requirements exist
because upstream sources **contradict each other**, and those resolutions are
recorded deliberately in
[05-traceability.md §3](docs/specs/05-traceability.md#3-conflicts-between-sources).
Changing one is an ADR, not an edit.
