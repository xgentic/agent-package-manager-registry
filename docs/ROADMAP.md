# apm-registry — delivery roadmap

Working document. Update the status columns as you go; this is the file to open
when you sit down to continue.

**Stack:** Go, standard library `net/http` ([ADR-0001](specs/adr/0001-runtime-go-net-http.md)).
**Specs:** [docs/specs/](specs/) · **Requirements:** [02-requirements.md](specs/02-requirements.md) ·
**Decisions:** [docs/specs/adr/](specs/adr/)

---

## How to use this document

- Work is grouped into **MVPs** (shippable milestones), each containing
  **modules** (`M1`, `M2`…), each containing **tasks** (`T1.1`, `T1.2`…).
- Every task has a **Done when** column — a verifiable condition, not a feeling.
  If you can't check it objectively, the task is written wrong; rewrite it.
- Status: ` ` todo · `~` in progress · `x` done · `-` deliberately skipped.
- **Tasks within a module are ordered; modules with no shared dependency can run
  in parallel.** Dependencies are noted per module.

---

## Stack

Chosen to keep the hostile-input surface in the standard library and ship one
static binary.

| Concern | Choice | Why |
|---|---|---|
| HTTP | stdlib `net/http`, Go 1.22+ `ServeMux` patterns | Method+wildcard routing without a framework ([ADR-0001](specs/adr/0001-runtime-go-net-http.md)); see [routing note](#the-identity-routing-finding) |
| Archives | stdlib `archive/zip`, `archive/tar`, `compress/gzip` | **The main reason Go fits here.** `zip.NewReader` reads the central directory by construction, which is exactly [TR-12](specs/02-requirements.md#tr-12). No third-party parser on the attack surface ([ADR-0004](specs/adr/0004-archive-format.md)) |
| Hashing | stdlib `crypto/sha256`, `crypto/subtle` | Streaming digest + constant-time compare ([TR-18](specs/02-requirements.md#tr-18)) |
| SQLite | `modernc.org/sqlite` (pure Go, cgo-free) on `database/sql` | Keeps `CGO_ENABLED=0`, so the binary stays static and cross-compilable ([ADR-0002](specs/adr/0002-metadata-store.md)) |
| YAML | `gopkg.in/yaml.v3` | Parsing `apm.yml` |
| Password/token hashing | `golang.org/x/crypto/argon2` | argon2id ([ADR-0005](specs/adr/0005-token-auth.md), [TR-17](specs/02-requirements.md#tr-17)) |
| CLI | stdlib `flag` with subcommands | Add `cobra` only if the surface outgrows it ([ADR-0013](specs/adr/0013-operator-cli.md)) |
| Web UI | React + Vite, embedded via `embed.FS` | One binary contains the API *and* the UI — no separate static host ([ADR-0014](specs/adr/0014-react-spa.md)) |

**Deployment consequence worth stating early:** `CGO_ENABLED=0 go build` produces
a single static binary with the React UI embedded. Deployment is that binary plus
a data directory. Keep it that way — it's most of the operational simplicity.

### The identity routing finding

`MS-API §1.2` requires one resource at two path shapes: `acme/web-skills` (two
segments) and `gitlab.com%2Facme%2Fweb-skills` (one percent-encoded segment).
Measured on **Go 1.25.4**, `net/http.ServeMux` — full reasoning in
[ADR-0007](specs/adr/0007-package-identity.md):

| Request path | Route matched | `r.PathValue(...)` | `r.URL.Path` |
|---|---|---|---|
| `/v1/packages/acme/web-skills/versions` | `{owner}/{repo}` | `acme`, `web-skills` | 4 segments |
| `/v1/packages/gitlab.com%2Facme%2Fweb-skills/versions` | `{identity}` | `gitlab.com/acme/web-skills` | **6 segments** |
| `/v1/packages/acme%2F..%2Fevil/versions` | `{identity}` → **200** | `acme/../evil` | traversal visible |

Two facts to build on, and one trap:

1. `ServeMux` matches against the **escaped** path, so `%2F` stays a single
   segment and dual-route registration works.
2. `r.PathValue()` returns the **decoded** value.
3. **The trap:** `r.URL.Path` is already decoded and would show six segments.
   Any routing or validation that reads `r.URL.Path` instead of `RawPath` /
   `PathValue` is wrong. And since row 3 returns `200` with
   `identity="acme/../evil"`, **traversal validation must run on the decoded
   value** — validating the raw segment would pass it ([FR-15](specs/02-requirements.md#fr-15)).

---

## The MVP ladder

| MVP | Delivers | Demo that proves it |
|---|---|---|
| ✅ **[Phase 0](#phase-0--go-project-setup)** | Go project setup | `make test` and `make run` work from a clean clone |
| ✅ **[MVP 1](#mvp-1--a-working-registry)** | A working registry: publish, list, download | Real `apm publish` uploads a package; real `apm install` installs it in a fresh project |
| **[MVP 2](#mvp-2--react-web-ui)** | React web UI to browse, upload and manage packages | Drag a `.zip` into the browser, see the pre-flight report, publish, see it listed |
| **[MVP 3](#mvp-3--authentication-and-authorisation)** | Auth: tokens, scopes, repository visibility | Scoped token publishes; wrong scope → 403; private repo, no token → 401 |
| **[MVP 4](#mvp-4--operability-and-hardening)** | Audit, quotas, rate limits, gc/verify, search, caching | Operator finds every publish in the audit log; `verify` re-hashes all blobs clean |
| **[Later](#later--backlog)** | Postgres, S3, yank, attestations | — |

### What each MVP deliberately does *not* have

| MVP | Missing | Consequence |
|---|---|---|
| 1 | **All authentication.** Every endpoint is open. | **Do not expose MVP 1 to an untrusted network.** Local, or behind a VPN / reverse-proxy allowlist, only |
| 1 | Web UI, audit, quotas, rate limiting, search | Operated by CLI + the `apm` client only |
| 2 | Still no auth — the UI is open too | Same network constraint as MVP 1 |
| 3 | Audit log, quotas, rate limiting | Auth works; forensics and abuse-control don't |
| 4 | Yank, signing, attestations | Reserved for OpenAPM v0.2 upstream ([FR-52](specs/02-requirements.md#fr-52), [FR-53](specs/02-requirements.md#fr-53)) |

### Conformance status by MVP

Honest tracking against `MS-API §5`. The answer to "can we call it conformant
yet?" is **no until MVP 3**.

| Conformance requirement | MVP 1 | MVP 2 | MVP 3 | MVP 4 |
|---|:--:|:--:|:--:|:--:|
| `GET /versions` | ✅ | ✅ | ✅ | ✅ |
| `GET /download` | ✅ | ✅ | ✅ | ✅ |
| `PUT /versions/{version}` | ✅ | ✅ | ✅ | ✅ |
| RFC 7807 on all 4xx/5xx | ✅ | ✅ | ✅ | ✅ |
| sha256 digest accuracy | ✅ | ✅ | ✅ | ✅ |
| Version immutability | ✅ | ✅ | ✅ | ✅ |
| **Bearer auth on all endpoints** | ❌ | ❌ | ✅ | ✅ |
| *SHOULD:* Cache-Control + ETag + 304 | ❌ | ❌ | ❌ | ✅ |
| *SHOULD:* `size_bytes` | ✅ | ✅ | ✅ | ✅ |
| Fixture group §9.5 (auth) | ⏭ skipped | ⏭ skipped | ✅ | ✅ |

> **The one rule that makes this ordering safe:** the auth *seam* ships in MVP 1
> even though auth doesn't. Every handler resolves a `Principal` through
> `authMiddleware` and checks `Scope.Satisfies(...)`; in MVP 1 the middleware
> returns a hard-coded all-scopes principal. MVP 3 replaces one function body —
> it does not retrofit authorisation into handlers that never had it. See
> [T5.2](#m5--http-api-layer).

---

## Phase 0 — Go project setup

**Done.** ✅

| ✓ | ID | Task | Done when |
|:-:|---|---|---|
| ☒ | T0.1 | Remove the TypeScript scaffold | Gone; `.gitignore` is Go-shaped |
| ☒ | T0.2 | `go mod init` + skeleton | `go.mod` (`github.com/xgentic/agent-package-manager-registry`, Go 1.25.4), `main.go`, `internal/server/` |
| ☒ | T0.3 | `Makefile` | `check` = `build vet test` — **the one command that must stay green** |
| ☒ | T0.4 | `golangci-lint` config + CI workflow | CI runs `make check` + lint on every push |
| ☒ | T0.5 | `.env.example` documenting every config variable | Every variable the config loader reads appears in it |
| ☒ | T0.6 | `CONTRIBUTING.md` pointing at [docs/specs/](specs/) and this roadmap | A new contributor finds the specs without asking |
| ☒ | T0.7 | Add `test-conformance` target to the `Makefile` | `make test-conformance` runs the [M6](#m6--verification) suite |

### Already implemented (do not rebuild)

| Exists | Covers |
|---|---|
| `main.go` — bind-before-background, `SIGTERM` drain, `ReadHeaderTimeout`, `slog` JSON | most of [T5.9](#m5--http-api-layer), part of [T18.1](#mvp-4--operability-and-hardening) |
| `internal/server` — `ServeMux`, `GET /health`, catch-all JSON 404 | part of [T5.8](#m5--http-api-layer) |
| `writeProblem` / `problemDetails` — RFC 7807 envelope, marshal-before-write | the core of [T5.1](#m5--http-api-layer) |

`server.go` already states the rule T5.1 has to hold: *"every error path must go
through `writeProblem` rather than `http.Error` or a bare JSON object."* The
remaining T5.1 work is the request-ID extension, the panic-recovery catch-all,
and the typed-error → problem mapping.

### Target layout

Extends the existing `internal/server` package rather than replacing it.
Interfaces are declared in the **consuming** package (Go idiom), not in a
separate `ports` package.

```
main.go                           # exists — server lifecycle
internal/
  server/                         # exists — HTTP layer, grows into:
    server.go                     #   routing + wiring
    problem.go                    #   RFC 7807 mapping (writeProblem lives here)
    identity.go                   #   dual-form identity route registration
    versions.go download.go publish.go
    middleware.go                 #   auth seam, request id, rate limit
  config/                         # env parsing, validation, fail-fast
  domain/                         # pure: no I/O, no net/http
    errors.go identity.go version.go digest.go scope.go manifest.go validation.go
    archive/ mediatype.go zip.go targz.go entryrules.go inventory.go
  service/                        # publish, query, token, repository (+ interfaces)
  store/
    sqlite/                       # migrations + MetadataStore impl
    blob/                         # content-addressed filesystem BlobStore
  webui/                          # go:embed of web/dist
web/                              # React + Vite source
```

> The operator CLI ([M7](#m7--minimal-operator-cli)) turns `main.go` into a
> subcommand dispatcher (`serve` becomes the default). Keep the lifecycle code
> that is already there — it is the `serve` body.

---

## MVP 1 — A working registry

**Goal:** the real `apm` CLI can publish to and install from this server.
**Not in scope:** auth, web UI, audit, quotas, search.

**Definition of done — all four met:**
1. ☒ `apm publish --package acme/demo --registry local` returns `201` with a digest.
2. ☒ `apm install acme/demo#1.0.0` in a fresh project installs and verifies.
   Both are asserted by `make test-e2e` against `apm` v0.27.0.
3. ☒ Conformance groups §9.1, §9.2, §9.3, §9.4, §9.6 pass; §9.5 skipped with a
   recorded reason visible in test output.
4. ☒ Every malicious-archive fixture is rejected — in the domain tests, and
   again over HTTP.

### M1 — Foundations

No dependencies. Start here.

| ✓ | ID | Task | Delivers | Done when | Reqs |
|:-:|---|---|---|---|---|
| ☒ | T1.1 | Config loader | `internal/config` — env parsing, validation, fail-fast | Invalid config exits non-zero with a readable message | [TR-30](specs/02-requirements.md#tr-30) |
| ☒ | T1.2 | Store interfaces | `MetadataStore`, `BlobStore`, `Clock`, `IDGenerator` declared in `internal/service` | Compiles; no implementation yet | [TR-05](specs/02-requirements.md#tr-05), [TR-06](specs/02-requirements.md#tr-06) |
| ☒ | T1.3 | SQLite schema + migrations | `internal/store/sqlite/migrations/` — `repositories`, `packages`, `versions`, `blobs` | `migrate` twice is a no-op; `versions` has UNIQUE `(package_id, version)` and **case-sensitive (BINARY) collation** on `version` | [TR-06](specs/02-requirements.md#tr-06), [TR-08](specs/02-requirements.md#tr-08) |
| ☒ | T1.4 | SQLite MetadataStore | `internal/store/sqlite` on `database/sql` + `modernc.org/sqlite` | WAL on, `busy_timeout` set, foreign keys on; unique violation maps to a typed `ErrVersionConflict`, not a raw driver error | [TR-06](specs/02-requirements.md#tr-06) |
| ☒ | T1.5 | Filesystem BlobStore | `internal/store/blob` — `sha256/ab/cd/<hex>` | `Put` is create-if-absent and atomic (temp file + `os.Rename`); `Get` returns a stream | [TR-04](specs/02-requirements.md#tr-04), [ADR-0003](specs/adr/0003-content-addressed-storage.md) |
| ☒ | T1.6 | In-memory fakes | `internal/service` test doubles for both stores | Service tests run with zero I/O | — |

### M2 — Domain core

Depends only on T1.2. **Can run in parallel with M1.**

| ✓ | ID | Task | Delivers | Done when | Reqs |
|:-:|---|---|---|---|---|
| ☒ | T2.1 | Error taxonomy | `internal/domain/errors.go` — closed set of registry errors | All 9 kinds defined with status + extension payloads; `errors.Is`/`errors.As` friendly | [FR-26](specs/02-requirements.md#fr-26) |
| ☒ | T2.2 | Package identity | `identity.go` — parse, validate, canonicalise | Table tests cover 2-segment, percent-encoded, `Acme/Repo` → lowercase, and **`acme%2F..%2Fevil` rejected after decoding** | [FR-14](specs/02-requirements.md#fr-14)–[FR-16](specs/02-requirements.md#fr-16) |
| ☒ | T2.3 | Version selector | `version.go` — opaque, case-sensitive | `V1.0` ≠ `v1.0`; no semver parsing anywhere; empty/control chars rejected | [FR-17](specs/02-requirements.md#fr-17) |
| ☒ | T2.4 | Digest | `digest.go` — `sha256:<64 hex>`, `subtle.ConstantTimeCompare` | Malformed digests rejected | [FR-02](specs/02-requirements.md#fr-02), [TR-18](specs/02-requirements.md#tr-18) |
| ☒ | T2.5 | Manifest parser | `manifest.go` — `apm.yml`, require `name` + `version` | Missing field and bad YAML each produce a typed error | [FR-10](specs/02-requirements.md#fr-10) rules 4–5 |
| ☒ | T2.6 | Scope grammar *(unused until MVP 3)* | `scope.go` — `Scope.Satisfies()` | Implemented and table-tested **now**, even though nothing enforces it yet | [FR-22](specs/02-requirements.md#fr-22) |

> T2.6 looks premature. It isn't — it's what makes MVP 3 a middleware change
> instead of a rewrite. Write it, test it, leave it unused.

### M3 — Archive handling

Depends on M2. **The hostile-input surface — take the time.**

| ✓ | ID | Task | Delivers | Done when | Reqs |
|:-:|---|---|---|---|---|
| ☒ | T3.1 | Media-type policy | `archive/mediatype.go` | `application/zip` + `application/gzip` accepted, all else → 415; configurable | [FR-09](specs/02-requirements.md#fr-09), [ADR-0004](specs/adr/0004-archive-format.md) |
| ☒ | T3.2 | ZIP entry reader | `archive/zip.go` on stdlib `archive/zip` | Uses `zip.NewReader(ra, size)`, which reads the **central directory** by construction — satisfies [TR-12](specs/02-requirements.md#tr-12) without a custom parser | [TR-12](specs/02-requirements.md#tr-12) |
| ☒ | T3.3 | tar.gz entry reader | `archive/targz.go` on `compress/gzip` + `archive/tar` | Entry list exposes `Typeflag`, so `TypeSymlink` / `TypeLink` are detectable | [TR-11](specs/02-requirements.md#tr-11) |
| ☒ | T3.4 | Entry safety rules | `archive/entryrules.go` | Rejects absolute paths, `..` (after `path.Clean`), symlinks (`fs.ModeSymlink` in zip, `TypeSymlink`/`TypeLink` in tar), entry-count > cap, expansion > cap — **caps enforced with `io.LimitReader` during expansion, never from declared sizes** | [TR-11](specs/02-requirements.md#tr-11)–[TR-14](specs/02-requirements.md#tr-14) |
| ☒ | T3.5 | The eight validation rules | `domain/validation.go` | All 8 implemented; **none short-circuit** — result carries every failure | [FR-10](specs/02-requirements.md#fr-10) |
| ☒ | T3.6 | Malicious fixtures | `testdata/archives/` | ≥8 hostile archives committed: zip slip, symlink, hardlink, absolute path, zip bomb, CD/local-header mismatch, no `apm.yml`, bad YAML — all asserted rejected | [TR-25](specs/02-requirements.md#tr-25) |

### M4 — Application services

Depends on M1 + M3.

| ✓ | ID | Task | Delivers | Done when | Reqs |
|:-:|---|---|---|---|---|
| ☒ | T4.1 | Publish — streaming ingest | `io.TeeReader` into `sha256.New()` while writing `os.CreateTemp` | `http.MaxBytesReader` trips the cap **mid-stream** → 413; memory flat regardless of archive size | [TR-09](specs/02-requirements.md#tr-09), [TR-10](specs/02-requirements.md#tr-10) |
| ☒ | T4.2 | Publish — `Inspect()` | Validate + report, no commit | Returns identity, version and **every** validation failure | [FR-10](specs/02-requirements.md#fr-10) |
| ☒ | T4.3 | Publish — `Publish()` | Blob → metadata → cleanup | Blob written before metadata; metadata in one transaction; temp file removed via `defer` on **every** path incl. error and client disconnect | [TR-07](specs/02-requirements.md#tr-07), [TR-15](specs/02-requirements.md#tr-15), [ADR-0011](specs/adr/0011-publish-pipeline.md) |
| ☒ | T4.4 | Conflict handling | Unique violation → `ErrVersionConflict` | Conflict comes **from the DB constraint**, not a prior `SELECT`; a concurrency test proves exactly one of N parallel publishes wins | [FR-11](specs/02-requirements.md#fr-11), [TR-08](specs/02-requirements.md#tr-08) |
| ☒ | T4.5 | Query service | `ListVersions`, `GetVersion`, `OpenDownload` | Complete version list, **no pagination**; download streams stored bytes with stored media type | [FR-01](specs/02-requirements.md#fr-01), [FR-19](specs/02-requirements.md#fr-19), [FR-05](specs/02-requirements.md#fr-05) |
| ☒ | T4.6 | Startup temp sweep | Orphaned temp files removed on boot | Killing the process mid-upload leaves no permanent temp file after restart | [TR-15](specs/02-requirements.md#tr-15) |

### M5 — HTTP API layer

Depends on M4.

| ✓ | ID | Task | Delivers | Done when | Reqs |
|:-:|---|---|---|---|---|
| ☒ | T5.1 | Problem Details + request ID | `httpapi/problem.go`, `middleware/requestid.go` | **Every** non-2xx is `application/problem+json` with `title`+`status` and `extensions.request_id`; a catch-all recovers panics into a generic 500 that leaks nothing | [FR-26](specs/02-requirements.md#fr-26)–[FR-28](specs/02-requirements.md#fr-28), [ADR-0006](specs/adr/0006-rfc7807-errors.md) |
| ☒ | T5.2 | **Auth seam (no-op)** | `middleware/auth.go` returning an all-scopes `Principal` in the request context | Every handler resolves a `Principal` and calls `Scope.Satisfies()`; one documented `TODO(MVP3)` marks the body to replace | [ADR-0005](specs/adr/0005-token-auth.md) |
| ☒ | T5.3 | Repository prefix | `/api/agentpackages/{repository}/...` resolved into context | Unknown repository → 404; every store call is repository-scoped by signature | [FR-29](specs/02-requirements.md#fr-29), [ADR-0009](specs/adr/0009-multi-repository-namespacing.md) |
| ☒ | T5.4 | Dual identity routing | `httpapi/identity.go` registering both path forms per endpoint | Both forms resolve to the same package — asserted by test; **validation reads `PathValue`, never `r.URL.Path`** (see [routing note](#the-identity-routing-finding)) | [FR-14](specs/02-requirements.md#fr-14), [FR-15](specs/02-requirements.md#fr-15), [ADR-0007](specs/adr/0007-package-identity.md) |
| ☒ | T5.5 | `GET .../versions` | List endpoint | Exact `snake_case` JSON; `versions: []` when empty, **never omitted**; `package` echoes canonical identity | [FR-01](specs/02-requirements.md#fr-01)–[FR-04](specs/02-requirements.md#fr-04) |
| ☒ | T5.6 | `GET .../versions/{v}/download` | Download endpoint | Streams stored bytes; stored `Content-Type` replayed; `Digest`, `ETag`, `Content-Disposition: attachment` set | [FR-05](specs/02-requirements.md#fr-05)–[FR-07](specs/02-requirements.md#fr-07), [TR-21](specs/02-requirements.md#tr-21) |
| ☒ | T5.7 | `PUT .../versions/{v}` | Publish endpoint | 201/400/409/413/415/422 all correct; scope + media type checked **before** reading the body | [FR-08](specs/02-requirements.md#fr-08)–[FR-13](specs/02-requirements.md#fr-13) |
| ☒ | T5.8 | `GET /health`, `GET /ready` | Ops endpoints | `/ready` fails when DB or blob store is unreachable | [FR-50](specs/02-requirements.md#fr-50) |
| ☒ | T5.9 | Server lifecycle | Timeouts + graceful shutdown | `ReadHeaderTimeout`, `WriteTimeout`, `IdleTimeout` set; `SIGTERM` drains in-flight requests | [TR-27](specs/02-requirements.md#tr-27) |

### M6 — Verification

Depends on M5. **This module is what makes MVP 1 "done" rather than "probably working".**

| ✓ | ID | Task | Delivers | Done when | Reqs |
|:-:|---|---|---|---|---|
| ☒ | T6.1 | Conformance harness | `internal/conformance/` runnable via `httptest.Server` **or** a remote base URL | `make test-conformance BASE_URL=...` works against a deployment | [TR-22](specs/02-requirements.md#tr-22) |
| ☒ | T6.2 | §9.1 round-trip · §9.2 immutability · §9.3 format dispatch | Three fixture groups | §9.2 asserts `409` for **both** identical and different bodies | [TR-22](specs/02-requirements.md#tr-22) |
| ☒ | T6.3 | §9.4 validation · §9.6 error format | Two fixture groups | §9.6 sweeps **every** route's non-2xx responses | [TR-22](specs/02-requirements.md#tr-22) |
| ☒ | T6.4 | §9.5 auth — **skip with recorded reason** | `t.Skip("auth lands in MVP 3")` | Skip is visible in test output, not silently absent | see [conformance table](#conformance-status-by-mvp) |
| ☒ | T6.5 | Field-name assertions | Exact `snake_case` checks on every response | A `publishedAt` regression fails a test — the real client would ignore it **silently** | [TR-23](specs/02-requirements.md#tr-23) |
| ☒ | T6.6 | E2E with the real `apm` CLI | Script: install `apm`, `publish`, then `install` in a temp project | Both commands exit 0 against a running server | [TR-26](specs/02-requirements.md#tr-26) |

### M7 — Minimal operator CLI

Depends on M1. **Can run in parallel with M3–M6.**

| ✓ | ID | Task | Delivers | Done when | Reqs |
|:-:|---|---|---|---|---|
| ☒ | T7.1 | CLI entrypoint + subcommand dispatch | `cmd/apm-registry` | `apm-registry --help` lists commands | [FR-42](specs/02-requirements.md#fr-42) |
| ☒ | T7.2 | `serve`, `migrate` | Run server; apply migrations | `serve` auto-migrates unless `--no-migrate`; invalid config exits non-zero | [FR-43](specs/02-requirements.md#fr-43) |
| ☒ | T7.3 | `repo create` / `repo list` | Repository bootstrap | A fresh install can create a repository and publish into it | [FR-39](specs/02-requirements.md#fr-39) |

---

## MVP 2 — React web UI

**Goal:** manage and upload packages from a browser.
**Decision:** [ADR-0014](specs/adr/0014-react-spa.md) — React + Vite embedded in
the Go binary via `embed.FS`, superseding [ADR-0010](specs/adr/0010-web-ui.md).
**Still no auth** — the UI is as open as the API.

**Definition of done:**
1. Browse repositories and packages, search, open a package, see version history.
2. Drag a `.zip` in, see the pre-flight report (identity, version, validation
   results, primitive inventory), confirm, see the new version listed.
3. Copy a working `apm install <identity>#<version>` snippet.

### M8 — UI-facing API

Depends on MVP 1. The registry API has no "list all packages" — the UI needs its own.

| ✓ | ID | Task | Delivers | Done when | Reqs |
|:-:|---|---|---|---|---|
| ☐ | T8.1 | Primitive inventory extraction | `archive/inventory.go` — scan `.apm/`, group by the 7 primitive types | Inventory computed **once at publish** and stored | [FR-31](specs/02-requirements.md#fr-31) |
| ☐ | T8.2 | Migration: `primitives` + manifest metadata columns | Schema for description/author/license/targets | Applies cleanly to an MVP-1 database with existing rows | [FR-31](specs/02-requirements.md#fr-31) |
| ☐ | T8.3 | `/ui/api` read endpoints | List repos, list/search packages, package detail, version detail + inventory | Plain JSON; documented as internal, **not** the registry contract (errors are still RFC 7807) | [FR-36](specs/02-requirements.md#fr-36) |
| ☐ | T8.4 | `/ui/api` upload endpoints | `POST .../preflight` → `Inspect()`, `POST .../publish` → `Publish()` | Both call **the same service** as the registry API — no second validation path; any validation logic inside a `/ui/api` handler is a bug | [FR-34](specs/02-requirements.md#fr-34), [ADR-0014](specs/adr/0014-react-spa.md) |
| ☐ | T8.5 | Entry listing + file preview | Entries for a version; size-capped text preview | Markdown sanitised server-side; raw HTML stripped; never executed | [FR-37](specs/02-requirements.md#fr-37), [TR-20](specs/02-requirements.md#tr-20) |

### M9 — React application

Depends on M8.

| ✓ | ID | Task | Delivers | Done when | Reqs |
|:-:|---|---|---|---|---|
| ☐ | T9.1 | Frontend toolchain | `web/` React + TypeScript + Vite | `npm run dev` proxies to the Go server with HMR; `npm run build` emits `web/dist` | — |
| ☐ | T9.2 | Embed + serve | `internal/webui` with `//go:embed dist`, SPA fallback to `index.html` | `CGO_ENABLED=0 go build` yields **one binary** serving API + UI | — |
| ☐ | T9.3 | Typed API client | `web/src/api/` types mirroring the `/ui/api` payloads | A response-shape change breaks the frontend build | — |
| ☐ | T9.4 | App shell + routing | Layout, nav, repository switcher, error boundary | Unknown route renders a 404 view, not a blank page | — |
| ☐ | T9.5 | Security headers | CSP without `unsafe-inline`, `nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy` | Headers present on the HTML document response | [TR-19](specs/02-requirements.md#tr-19) |

### M10 — Screens

Depends on M9.

| ✓ | ID | Task | Delivers | Done when | Reqs |
|:-:|---|---|---|---|---|
| ☐ | T10.1 | Package list + search | Browse and filter | Search across identity and description | [FR-36](specs/02-requirements.md#fr-36) |
| ☐ | T10.2 | Package detail | Metadata, latest version, version history with digest/size/time | Copy-pasteable install snippet | [FR-36](specs/02-requirements.md#fr-36) |
| ☐ | T10.3 | Version detail | Entries grouped by primitive type; file preview | Inventory rendered from stored metadata, not re-derived | [FR-37](specs/02-requirements.md#fr-37) |
| ☐ | T10.4 | Upload — pre-flight step | Drop zone → `preflight` → report | Report shows detected identity/version, per-rule pass/fail, primitive inventory | [FR-35](specs/02-requirements.md#fr-35) |
| ☐ | T10.5 | Upload — confirm step | Confirm → `publish` → result | Success shows digest + install snippet; `409` renders as a readable "version exists, bump it" | [FR-33](specs/02-requirements.md#fr-33) |
| ☐ | T10.6 | Upload progress | Client-side progress for large archives | A 40 MB upload shows progress rather than appearing frozen | — |
| ☐ | T10.7 | Repository management | Create, list, set quota | An operator can create a repository from the UI | [FR-39](specs/02-requirements.md#fr-39) |

---

## MVP 3 — Authentication and authorisation

**Goal:** the registry becomes safe to expose.
**This is the MVP that makes us `MS-API` conformant.**

**Definition of done:**
1. Publish with a correctly-scoped token → `201`; wrong scope → `403`; no token → `401`.
2. Anonymous read succeeds on a `public` repository, `401` on a `private` one.
3. Conformance group §9.5 **unskipped and passing** — the full suite is green.
4. UI requires sign-in; tokens creatable and revocable from the UI.

### M11 — Token domain and storage

| ✓ | ID | Task | Delivers | Done when | Reqs |
|:-:|---|---|---|---|---|
| ☐ | T11.1 | Migration: `users`, `tokens`, `sessions`; `repositories.visibility` | Schema | Applies cleanly to an MVP-2 database | — |
| ☐ | T11.2 | Token generation + hashing | `apmr_<base62(32B)>` from `crypto/rand`; argon2id via `x/crypto/argon2` | Plaintext shown once, never stored; verify is constant-time | [TR-17](specs/02-requirements.md#tr-17), [TR-18](specs/02-requirements.md#tr-18) |
| ☐ | T11.3 | Indexed lookup by token-id prefix | One row fetch + one verify | Lookup does **not** argon2-compare against every token row | — |
| ☐ | T11.4 | Token service | create / list / revoke / `last_used_at` | Revocation takes effect on the next request | [FR-38](specs/02-requirements.md#fr-38) |
| ☐ | T11.5 | Escalation guard | Creator can only grant scopes they hold | A publish-scoped token cannot mint an operator token | [UC-15](specs/01-use-cases.md#uc-15-manage-api-tokens) |

### M12 — API enforcement

| ✓ | ID | Task | Delivers | Done when | Reqs |
|:-:|---|---|---|---|---|
| ☐ | T12.1 | **Replace the T5.2 no-op middleware body** | Real bearer resolution → `Principal` | The `TODO(MVP3)` is gone and **no handler file changed** — if any handler needed editing, the MVP-1 seam was wrong | [FR-20](specs/02-requirements.md#fr-20) |
| ☐ | T12.2 | 401 vs 403 semantics | Absent credentials → 401; insufficient → 403 | Distinction driven by credential *presence*, not outcome | [FR-23](specs/02-requirements.md#fr-23) |
| ☐ | T12.3 | Remediation messages | `401`/`403` bodies name `APM_REGISTRY_TOKEN_{NAME}` | Message is copy-pasteable for the actual registry name | [FR-25](specs/02-requirements.md#fr-25) |
| ☐ | T12.4 | Repository visibility | Anonymous `GET` on `public`, `401` on `private`; publish always authenticated | Both paths covered by tests | [FR-24](specs/02-requirements.md#fr-24) |
| ☐ | T12.5 | **Unskip conformance §9.5** | Auth fixture group live | Full `MS-API §9` suite green — the MVP-3 gate | [TR-22](specs/02-requirements.md#tr-22) |

### M13 — UI authentication

| ✓ | ID | Task | Delivers | Done when | Reqs |
|:-:|---|---|---|---|---|
| ☐ | T13.1 | Session cookies | Signed, `HttpOnly`, `Secure`, `SameSite=Lax` | A browser session is **not** usable as an API bearer token | [FR-41](specs/02-requirements.md#fr-41) |
| ☐ | T13.2 | CSRF protection | Token on every state-changing request | Upload/confirm without a CSRF token is rejected | [FR-41](specs/02-requirements.md#fr-41) |
| ☐ | T13.3 | Sign-in / sign-out | Auth UI | Unauthenticated users are redirected | — |
| ☐ | T13.4 | Token management screens | Create (plaintext shown once), list, revoke | Plaintext unrecoverable after leaving the page | [FR-38](specs/02-requirements.md#fr-38) |
| ☐ | T13.5 | Scope-filtered listings | Every list/search filtered by read scope | "not permitted" is **indistinguishable** from "not found" | [FR-40](specs/02-requirements.md#fr-40) |
| ☐ | T13.6 | First-operator bootstrap | `apm-registry user create --operator` | Fresh install creates its first operator with no auth chicken-and-egg | [FR-42](specs/02-requirements.md#fr-42) |

---

## MVP 4 — Operability and hardening

| ✓ | ID | Module / Task | Done when | Reqs |
|:-:|---|---|---|---|
| ☐ | T14.1 | **M14** Audit log: table + writes on every publish / auth failure / validation failure | Every successful `PUT` findable; no token plaintext anywhere | [FR-46](specs/02-requirements.md#fr-46), [FR-47](specs/02-requirements.md#fr-47) |
| ☐ | T14.2 | Audit UI: filter by repository, identity, actor, time | Operator answers "who published this?" in one screen | [UC-18](specs/01-use-cases.md#uc-18-review-the-audit-log) |
| ☐ | T15.1 | **M15** Quotas: per-repository and per-owner bytes + version count | Over-quota publish → `403` with an explanatory body | [FR-48](specs/02-requirements.md#fr-48) |
| ☐ | T15.2 | Rate limiting → `429` + `Retry-After` + `extensions.limit`/`remaining` | `/health` and `/ready` exempt | [FR-49](specs/02-requirements.md#fr-49) |
| ☐ | T16.1 | **M16** `apm-registry verify` — re-hash every blob vs metadata | Exits non-zero on any mismatch; makes the immutability invariant provable | [FR-44](specs/02-requirements.md#fr-44) |
| ☐ | T16.2 | `apm-registry gc` — refcount unreferenced blobs, `--dry-run` default | Dry run reports; `--confirm` deletes | [FR-45](specs/02-requirements.md#fr-45) |
| ☐ | T16.3 | `apm-registry version delete --confirm` + tombstones | A deleted tuple cannot be republished with different bytes | [UC-21](specs/01-use-cases.md#uc-21-delete-a-version-break-glass) |
| ☐ | T17.1 | **M17** Caching: `Cache-Control` on both reads, `ETag`, `If-None-Match` → `304` | Conditional GET returns 304 | [TR-28](specs/02-requirements.md#tr-28) |
| ☐ | T17.2 | `GET /v1/search` vendor extension | Documented as an extension; **excluded from the conformance suite** | [FR-51](specs/02-requirements.md#fr-51), [ADR-0012](specs/adr/0012-search-vendor-extension.md) |
| ☐ | T17.3 | HTTP Basic auth | Identical scope grants to the equivalent bearer token | [FR-21](specs/02-requirements.md#fr-21) |
| ☐ | T18.1 | **M18** Structured logging (`log/slog`) with request-id correlation | A user-reported `request_id` finds the log line | [TR-29](specs/02-requirements.md#tr-29) |
| ☐ | T18.2 | Fuzz + property tests | `go test -fuzz` on archive parsing; round-trip `sha256(download(publish(x))) == sha256(x)` | Fuzz corpus committed; no crashes | [TR-24](specs/02-requirements.md#tr-24) |
| ☐ | T18.3 | Container image + deployment docs (volumes, TLS, temp-space sizing) | A fresh operator can deploy from the README | [TR-16](specs/02-requirements.md#tr-16) |

---

## Later — backlog

| Item | Trigger | Reqs |
|---|---|---|
| Postgres adapter | Multi-instance or HA requirement | [TR-31](specs/02-requirements.md#tr-31) |
| S3 blob adapter | Storage outgrows a volume | [TR-05](specs/02-requirements.md#tr-05) |
| Principal cache for argon2id | Auth latency becomes measurable | — |
| Yank / withdrawal | OpenAPM v0.2 ships | [FR-52](specs/02-requirements.md#fr-52) |
| Signing / SLSA attestations | OpenAPM v0.2 ships | [FR-53](specs/02-requirements.md#fr-53) |
| tar.gz-only mode | OpenAPM v0.2 makes it normative for registries | [FR-09](specs/02-requirements.md#fr-09) |
| Vulnerability scanning at publish | Governance demand | — |
| Federated repositories | Multi-site deployment | [Q-4](specs/05-traceability.md#4-open-questions) |

---

## Risk register

| # | Risk | Impact | Mitigation | Phase |
|---|---|---|---|---|
| R-1 | MVP 1 and 2 have **no authentication** | Anyone reachable can publish; storage fills (immutability still holds) | Network isolation mandatory until MVP 3 — state it in the README, not just here | MVP 1 |
| R-2 | Auth retrofit turns into a rewrite | MVP 3 balloons | The T5.2 seam: handlers call `Scope.Satisfies()` from day 1. **T12.1 must change no handler files** — if it does, the seam failed | MVP 1 → 3 |
| R-3 | Archive parsing is the hostile surface | Zip-slip / bomb reaches disk | Stdlib `archive/zip` reads the central directory by construction; T3.6 commits ≥8 hostile fixtures; T18.2 fuzzes | MVP 1 |
| R-4 | `snake_case` regression is **silently ignored** by the real client | Broken install with no error | T6.5 asserts exact field names. Watch Go's default JSON tags — an untagged struct field marshals as `PublishedAt` | MVP 1 |
| R-5 | Reading `r.URL.Path` instead of `PathValue`/`RawPath` | Percent-encoded identities silently mis-route; traversal slips through | [Routing note](#the-identity-routing-finding) + T5.4 test asserting both forms and rejecting `acme%2F..%2Fevil` | MVP 1 |
| R-6 | Archive format decision invalidated by OpenAPM v0.2 | Published zips unconsumable by strict v0.2 clients; cannot re-pack without breaking digests | Format policy isolated to T3.1; config flag to narrow | MVP 1 |
| R-7 | Web upload path drifts from API validation | A package publishable one way and not the other | T8.4 calls the same service; no second pipeline | MVP 2 |
| R-8 | SQLite on a network volume corrupts | Data loss | Document local-volume requirement in T18.3 | MVP 4 |
| R-9 | Q-2 unresolved: how `apm.yml.name` "matches" the URL identity | Legitimate publishes rejected | T3.5 accepts full identity **or** bare repo name; revisit with real manifests | MVP 1 |

---

## Progress log

Append as you finish phases — cheap continuity for future sessions.

| Date | Phase / tasks | Notes |
|---|---|---|
| 2026-08-05 | Specs written | [docs/specs/](specs/) — requirements extracted from upstream sources |
| 2026-08-05 | Roadmap written | This file. Stack pivoted to Go + stdlib `net/http`; `%2F` routing re-verified on Go 1.25.4 |
| 2026-08-05 | ADRs aligned to Go | 0007 re-measured on Go (was Hono/Bun data); 0002 → `modernc.org/sqlite` on `database/sql`; [ADR-0014](specs/adr/0014-react-spa.md) added for the React SPA, superseding 0010 |
| 2026-08-05 | **Phase 0 complete** | `.golangci.yml` + GitHub Actions running `make check` and lint; `.env.example`; `CONTRIBUTING.md`; `make lint`, `test-conformance`, `test-e2e` |
| 2026-08-05 | **MVP 1 complete** (M1–M7) | The real `apm` CLI publishes to and installs from this server. Conformance §9.1–§9.4, §9.6 green; §9.5 skipped with a visible reason |
| | ↳ M1–M2 | `internal/config` (fail-fast, reports every problem at once); SQLite store with `COLLATE BINARY` on `version` and the `UNIQUE (package_id, version)` constraint; content-addressed FS blob store; the domain core incl. `Scope.Satisfies` (written unused, per T2.6) |
| | ↳ M3 | `internal/domain/archive` reads entry tables and never extracts. Hostile fixtures are **generated** in `internal/fixtures` rather than committed as opaque blobs, so each attack is legible as code, and also written to `testdata/archives/` for manual replay |
| | ↳ M4–M5 | Streaming publish (temp file + incremental sha256, cap trips mid-body); dual identity routing verified against `acme%2F..%2Fevil`; the T5.2 auth seam ships with one `TODO(MVP3)` |
| | ↳ M6–M7 | `internal/conformance` runs in-process or against `BASE_URL`; `scripts/e2e.sh` round-trips through `apm` v0.27.0 and asserts the lockfile's `resolved_url` and `resolved_hash` |
| 2026-08-05 | R-9 resolved | The real client's `apm publish` writes the **bare repo name** into `apm.yml` while publishing as `acme/demo`. Rule 6 accepting both is load-bearing, not lenience — see `scripts/e2e.sh` |
| | | |
