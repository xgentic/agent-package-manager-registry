# Requirements

Functional (`FR-xx`) and technical / non-functional (`TR-xx`) requirements.
RFC 2119 keywords. Every requirement carries a **Source** and, where it exists,
the upstream requirement ID. Full mapping in [05-traceability.md](05-traceability.md).

**Priority** — `M` must (v1 blocker) · `S` should · `C` could · `W` won't (v1).

---

# Part A — Functional requirements

## A.1 Registry HTTP API — core contract

A conformant v1 server MUST implement the three endpoints, RFC 7807 errors,
bearer auth, digest accuracy and version immutability (`MS-API §5`). FR-01…FR-13
are the decomposition of that statement.

### FR-01
**M** · The server MUST expose `GET /v1/packages/{owner}/{repo}/versions`
returning `200` with a JSON object containing `package` (string) and `versions`
(array).
*Source: `MS-API §3.1`.*

### FR-02
**M** · Each entry in `versions[]` MUST carry `version` (string),
`digest` (`sha256:` + 64 lowercase hex) and `published_at` (ISO 8601 UTC).
`size_bytes` is optional but SHOULD be emitted.
*Source: `MS-API §3.1`.*

### FR-03
**M** · `versions` MUST be present even when empty (`[]`) and MUST NOT be
omitted. `package` MUST echo the requested identity in decoded canonical form.
*Source: `MS-API §3.1`.*

### FR-04
**M** · All JSON field names MUST be `snake_case`. The server MUST NOT emit
camelCase variants alongside or instead of canonical names.
*Source: `MS-API §1.3.1`.*
> The reference client reads only canonical names and **silently ignores**
> variants. A casing regression is therefore invisible in manual testing —
> [TR-22](#tr-22) makes it a test obligation.

### FR-05
**M** · The server MUST expose
`GET /v1/packages/{owner}/{repo}/versions/{version}/download`, streaming the
exact stored bytes with the stored `Content-Type`.
*Source: `MS-API §3.2`.*

### FR-06
**M** · The download response MUST set `Content-Type` to the media type recorded
at publish, and SHOULD set `Content-Length`, RFC 3230 `Digest`, and `ETag`
(`"sha256:<hex>"`).
*Source: `MS-API §3.2`.*

### FR-07
**M** · The server MUST NOT transcode, recompress or otherwise alter archive
bytes between publish and download. `sha256(served bytes)` MUST equal the
advertised digest, permanently.
*Source: `req-rg-001`; `MS-API §5`, §9.1.*

### FR-08
**M** · The server MUST expose
`PUT /v1/packages/{owner}/{repo}/versions/{version}` accepting a raw archive
body and returning `201` with `{package, version, digest, published_at, size_bytes}`.
*Source: `MS-API §3.3`.*

### FR-09
**M** · The server MUST accept `Content-Type: application/zip` and
`application/gzip` on publish, record which was used, and replay it on download.
Any other type MUST yield `415`.
*Source: `MS-API §3.2`, §3.3. See [ADR-0004](adr/0004-archive-format.md) for the
conflict with `req-sc-004`.*

### FR-10
**M** · On publish the server MUST validate, returning `422` with all failures
in `extensions.errors[]`:

| # | Rule |
|---|---|
| 1 | Version selector is non-empty after URL-decoding, contains no control characters, and is treated as an opaque key — never a filesystem path |
| 2 | Archive parses cleanly as the declared `Content-Type` |
| 3 | Archive contains `apm.yml` at the **root** of the extraction tree |
| 4 | `apm.yml` is valid YAML with `name` and `version` present |
| 5 | `apm.yml.version` is present |
| 6 | `apm.yml.name` matches the URL path identity (or its repo-name suffix) |
| 7 | No archive entry has an absolute path, `..` traversal, symlink or hardlink |
| 8 | Archive size is within the configured limit |

*Source: `MS-API §6`; `MS-SPEC §10.9` / `req-sc-002`.*

### FR-11
**M** · A `(repository, identity, version)` tuple that has been successfully
published MUST NOT be overwritten. A repeat `PUT` MUST return `409`, whether the
body is identical or different, with `extensions.previous_publish` and
`extensions.previous_digest`.
*Source: `MS-API §1.6`, §3.3, §9.2; `req-rg-001`. See [ADR-0008](adr/0008-version-immutability.md).*

### FR-12
**M** · Publish MUST return `400` on a body that does not parse as the declared
type, `413` when the body exceeds the size cap, and `415` on an unsupported
`Content-Type`.
*Source: `MS-API §3.3`.*

### FR-13
**M** · Validation MUST complete before persistence. A rejected publish MUST
leave no blob, no metadata row and no orphaned temporary file.
*Source: `MS-API §6` ("validate per §6 before persistence"), §8.*

## A.2 Identity and versions

### FR-14
**M** · The server MUST accept package identity both as two path segments
(`acme/web-skills`) and as one percent-encoded segment
(`gitlab.com%2Facme%2Fweb-skills`), and MUST decode percent-encoded segments
before lookup.
*Source: `MS-API §1.2`.*

### FR-15
**M** · Identity components MUST be validated **after** decoding. `..`, absolute
paths, empty components, control characters and path separators beyond the
identity structure MUST be rejected with `400`.
*Source: `MS-API §8` (path-traversal prevention); `MS-SPEC §10.9`.*

### FR-16
**M** · Owner and repo MUST be normalised to lowercase for storage and lookup,
so `Acme/Web-Skills` and `acme/web-skills` denote the same package. The
canonical (lowercase) form is what `package` echoes.
*Source: `apm publish` CLI reference ("Owner and repository are normalized to
lowercase"); `req-mf-009` (canonical normalisation).*

### FR-17
**M** · Version selectors MUST be treated as opaque, case-sensitive strings. The
server MUST NOT parse, normalise, reorder or filter them by semver.
*Source: `MS-API §1.3`.*

### FR-18
**S** · `GET /versions` SHOULD return versions in publish-time descending order.
*Source: `MS-API §3.1`.*

### FR-19
**M** · `GET /versions` MUST return the complete version set. It MUST NOT
paginate or truncate — a partial list silently produces an incorrect client-side
range resolution.
*Source: derived from `MS-API §1.3` + §1.6 ("MUST return the same set of
versions on identical inputs"); `MS-SPEC §7.2`.*

## A.3 Authentication and authorisation

### FR-20
**M** · Every endpoint MUST accept `Authorization: Bearer <token>`, treating the
token as an opaque byte string.
*Source: `MS-API §2.1`.*

### FR-21
**S** · The server SHOULD accept `Authorization: Basic <base64(user:pass)>` as
an alternative, and MUST grant it scopes identical to the equivalent bearer
token.
*Source: `MS-API §2.1.1`.*

### FR-22
**M** · The server MUST enforce scopes server-side: `read` (or
`read:{owner}/{repo}`) for the two `GET`s, `publish:{owner}/{repo}` or
`publish:{owner}/*` for `PUT`. Scope strings MUST NOT appear in responses.
*Source: `MS-API §2.2`.*

### FR-23
**M** · Missing credentials MUST yield `401`; present-but-insufficient
credentials MUST yield `403` with a Problem body naming the missing scope.
*Source: `MS-API §2.1`, §2.2.*

### FR-24
**S** · Repositories MUST have a visibility setting. `public` repositories SHOULD
permit anonymous `GET`; `private` repositories MUST return `401` to
unauthenticated reads. Publish MUST always require authentication.
*Source: `MS-API §2.1`, §9.5.*

### FR-25
**M** · `401`/`403` Problem bodies SHOULD include a remediation `detail` naming
the expected client env var, e.g. `APM_REGISTRY_TOKEN_CORP_MAIN`.
*Source: `MS-API §2.3`, §8 (client checklist).*

## A.4 Error model

### FR-26
**M** · Every `4xx`/`5xx` response MUST use `application/problem+json` with at
least `title` and `status`; `type`, `detail`, `instance` and `extensions`
SHOULD be populated.
*Source: `MS-API §4`, §9.6.*

### FR-27
**M** · Vendor-specific data MUST live under `extensions.*`.
*Source: `MS-API §4`.*

### FR-28
**M** · Problem bodies MUST NOT leak internal detail: no stack traces, no
filesystem paths, no SQL, no token material.
*Source: `MS-API §8`; standard practice.*

## A.5 Repositories and namespacing

### FR-29
**M** · The server MUST support multiple named repositories, each with its own
base URL `{origin}/api/agentpackages/{repository}` to which clients append
`/v1/...`. Base URLs MUST NOT have a trailing slash.
*See [ADR-0009](adr/0009-multi-repository-namespacing.md).*

### FR-30
**M** · Repository names MUST match `^[a-z0-9][a-z0-9._-]*$`. Packages in
different repositories are wholly independent, including identical identities.
*Registry naming convention.*

### FR-31
**S** · At publish the server SHOULD extract and store a **primitive inventory**
from `.apm/` — counts and names per type across `instructions`, `prompts`,
`agents`, `skills`, `commands`, `hooks`, `mcp` — plus parsed `apm.yml` metadata
(`description`, `author`, `license`, `type`, `targets`, `keywords`) for display
and search.
*Source: `MS-SPEC §8.1`; project requirement (UC-13, UC-14).*
> Computed once at publish. Version contents are immutable, so per-request
> re-derivation is pure waste.

### FR-32
**W** · Remote, virtual and smart repositories, and replication, are **not**
supported. Upstream excludes them for Agent Packages.
*Supported features.*

## A.6 Web interface

### FR-33
**M** · The web UI MUST let an authenticated user upload a `.zip` artefact to a
repository they hold publish scope for.
*Source: project requirement (UC-12).*

### FR-34
**M** · Web upload MUST execute the identical domain service and validation
pipeline as the HTTP API. No parallel implementation.
*Source: project requirement. See [ADR-0010](adr/0010-web-ui.md).*

### FR-35
**S** · Before committing an upload, the UI SHOULD present a pre-flight report:
detected identity and version from `apm.yml`, the eight validation results, and
the primitive inventory.
*Source: project requirement (UC-12).*

### FR-36
**M** · The UI MUST provide package browse, search and a version history showing
digest, size, publish time and publisher, plus a copy-pasteable
`apm install <identity>#<version>` snippet.
*Source: project requirement (UC-13).*

### FR-37
**S** · The UI SHOULD render a version's archive entries grouped by primitive
type, with size-capped, sanitised Markdown previews of text files.
*Source: project requirement (UC-14).*

### FR-38
**M** · The UI MUST provide token management: create with name/scopes/expiry,
display plaintext exactly once, list metadata, revoke immediately.
*Source: `MS-API §2.2`, §8; project requirement (UC-15).*

### FR-39
**M** · The UI MUST provide repository management for operators: create, set
visibility, set quota, delete with confirmation.
*Source: project requirement (UC-16).*

### FR-40
**M** · The UI MUST filter every listing and search result by the caller's read
scope. A user MUST NOT be able to infer the existence of a package they cannot
read — "not found" and "not permitted" MUST be indistinguishable.
*Source: derived from `MS-API §2.2`; standard practice.*

### FR-41
**M** · Web sessions MUST authenticate via a signed, `HttpOnly`, `Secure`,
`SameSite=Lax` cookie, distinct from API tokens, and all state-changing form
posts MUST carry CSRF protection.
*Source: project requirement. See [ADR-0010](adr/0010-web-ui.md).*

## A.7 Operator CLI

### FR-42
**M** · The server MUST ship a CLI providing at least: `serve`, `migrate`,
`repo create|list|delete`, `token create|list|revoke`, `version delete`,
`gc`, `verify`.
*Source: project requirement (UC-17). See [ADR-0013](adr/0013-operator-cli.md).*

### FR-43
**M** · `migrate` MUST be idempotent and safe to run on every boot.
*Source: project requirement.*

### FR-44
**S** · `verify` MUST re-read every stored blob and compare its SHA-256 against
recorded metadata, reporting mismatches non-zero. This makes `req-rg-001` a
provable property rather than an assumed one.
*Source: `req-rg-001`; `MS-API §8`.*

### FR-45
**S** · `gc` MUST identify and (with `--confirm`) remove blobs no live version
references, defaulting to `--dry-run`.
*Source: project requirement.*

## A.8 Audit, quotas and observability

### FR-46
**M** · Every successful `PUT` MUST be recorded in an append-only audit log with
token id, `owner/repo`, version, sha256 and timestamp. Failed authentication and
failed validation MUST also be recorded.
*Source: `MS-API §8` (audit-log checklist item).*

### FR-47
**M** · Audit records MUST NOT contain token plaintext. Tokens MUST NOT appear in
application logs, error bodies or traces.
*Source: `MS-API §8`.*

### FR-48
**S** · The server SHOULD enforce per-token and per-owner quotas on total stored
bytes and version count, rejecting with `403` and an explanatory Problem body.
*Source: `MS-API §8` (quota enforcement).*

### FR-49
**S** · The server SHOULD rate-limit and return `429` with `Retry-After` plus
`extensions.limit` and `extensions.remaining`.
*Source: `MS-API §7.2`.*

### FR-50
**M** · The server MUST expose unauthenticated `GET /health` (liveness) and
`GET /ready` (readiness: metadata store and blob store reachable), both excluded
from rate limiting.
*Source: project requirement (UC-17).*

## A.9 Vendor extensions and reserved surface

### FR-51
**C** · The server MAY expose `GET /v1/search?q=` for package search and to
back the web UI. It MUST be documented as a **vendor extension** and MAY paginate.
*See [ADR-0012](adr/0012-search-vendor-extension.md).*

### FR-52
**W** · Version yank/withdrawal is **not** implemented in v1. `410` is reserved
in the router and a nullable `yanked_at` column is reserved in the schema so
OpenAPM v0.2 lands as a migration.
*Source: `MS-SPEC §7.9`; `MS-API §3.2`.*

### FR-53
**W** · Package signing, SBOM/SLSA provenance and attestation verification are
not implemented. Upstream lists these as "not yet provided" / reserved for v0.2.
*Source: `MS-GUIDE`; `MS-SPEC §10.12`.*

### FR-54
**S** · The `/v1/` prefix MUST be a first-class routing concern so a future
`/v2/` can be served in parallel during migration.
*Source: `MS-API §1.5`.*

---

# Part B — Technical requirements

## B.1 Platform

### TR-01
**M** · Language and runtime: **Go** (≥ 1.22, developed on 1.25). HTTP layer: the
standard library's **`net/http`**, with no third-party router.
*Source: project decision; matches existing `go.mod`.
See [ADR-0001](adr/0001-runtime-go-net-http.md).*

### TR-02
**M** · The HTTP layer MUST be a plain `http.Handler` that knows nothing about
process startup or configuration, keeping the whole API testable in-process with
`httptest` without binding a port (as `internal/server/server_test.go` already
does).
*Source: existing code; [ADR-0001](adr/0001-runtime-go-net-http.md).*

### TR-03
**M** · Deployment target is a single container running one process. Horizontal
scaling requirements are stated in [TR-19](#tr-19).
*Source: project requirement.*

## B.2 Storage

### TR-04
**M** · Archive bytes MUST be stored **content-addressed** by SHA-256, in a
store that never mutates an existing object.
*Source: `req-rg-001`; [ADR-0003](adr/0003-content-addressed-storage.md).*

### TR-05
**M** · The blob store MUST be behind a port interface (`put`, `get`, `stat`,
`delete`) with a filesystem adapter for v1 and an S3-compatible adapter possible
later without touching domain code.
*Source: [ADR-0003](adr/0003-content-addressed-storage.md).*

### TR-06
**M** · Metadata MUST live in a transactional store. v1 uses SQLite via
`modernc.org/sqlite` (pure Go, cgo-free) on stdlib `database/sql` with WAL
enabled, accessed through a store interface so Postgres remains an option.
*Source: [ADR-0002](adr/0002-metadata-store.md).*

### TR-07
**M** · Publish MUST be atomic: blob committed first, metadata row second, in a
transaction that either fully succeeds or leaves no trace. A crash mid-publish
MUST NOT leave a listable version without bytes.
> Ordering is deliberate. A blob with no metadata row is invisible garbage that
> `gc` reclaims. A metadata row with no blob is a `404` on a version the client
> believes exists — and a broken lockfile.
*Source: `MS-API §6`, §1.6; [ADR-0011](adr/0011-publish-pipeline.md).*

### TR-08
**M** · Uniqueness MUST be enforced by a database constraint on
`(repository_id, package_id, version)`, not by a read-then-write check. The
constraint is the immutability guarantee; an application-level check is a race.
*Source: `MS-API §1.6`; [ADR-0008](adr/0008-version-immutability.md).*

## B.3 Publish pipeline

### TR-09
**M** · The upload body MUST be streamed to temporary storage with SHA-256
computed incrementally. The server MUST NOT buffer entire archives in memory.
*Source: `MS-API §6.8`; [ADR-0011](adr/0011-publish-pipeline.md).*

### TR-10
**M** · The size cap MUST be enforced **during** streaming and abort as soon as
it is exceeded, before the full body is read.
*Source: `MS-API §3.3` (413), §8 (quota enforcement).*

### TR-11
**M** · Archive inspection MUST read the entry table without extracting to disk.
Entry paths MUST be checked for absolute paths, `..` segments, symlinks and
hardlinks before any content is read.
*Source: `MS-API §6.7`; `req-sc-002`.*

### TR-12
**M** · ZIP inspection MUST read the **central directory** as the authoritative
entry list, not only local file headers, so a mismatch between the two cannot
smuggle an entry past validation.
*Source: derived from `MS-API §6.2`, §6.7; standard ZIP-parsing hardening.*

### TR-13
**M** · Decompression MUST be bounded: enforce an uncompressed-size cap and an
entry-count cap while expanding, aborting on breach (zip-bomb defence).
*Source: `req-sc-004`.*

### TR-14
**S** · Default limits: compressed archive **50 MB** (`MS-API §6.8`),
uncompressed **100 MB**, entry count **10,000** (`req-sc-004`). All configurable.
> The consumer-side caps are enforced at publish so we never accept an archive
> that no conformant consumer could extract.
*Source: `MS-API §6.8`; `req-sc-004`.*

### TR-15
**M** · Temporary upload files MUST be cleaned up on every path — success,
validation failure, client disconnect, and process restart (startup sweep).
*Source: `MS-API §6`; [ADR-0011](adr/0011-publish-pipeline.md).*

## B.4 Security

### TR-16
**M** · TLS MUST be required in production; plain HTTP is permitted only for
local development and MUST be gated behind an explicit config flag.
*Source: `MS-API §8`; `MS-SPEC §10.5`.*

### TR-17
**M** · Token secrets MUST be stored using a memory-hard one-way hash
(argon2id; bcrypt acceptable). Plaintext MUST NOT be stored anywhere.
*Source: `MS-API §8`.*

### TR-18
**M** · Digest and token comparisons MUST be constant-time.
*Source: `MS-API §8`.*

### TR-19
**M** · Security headers on all HTML responses: `Content-Security-Policy` with
no `unsafe-inline`, `X-Content-Type-Options: nosniff`, `Referrer-Policy`,
`X-Frame-Options: DENY`.
*Source: standard practice; [ADR-0010](adr/0010-web-ui.md).*

### TR-20
**M** · Archive-derived content (file names, Markdown, manifest fields) is
untrusted input. It MUST be escaped/sanitised before rendering and MUST NOT be
interpolated into HTML, SQL or shell.
*Source: `MS-SPEC §10` (threat model); standard practice.*

### TR-21
**M** · Downloads MUST set `Content-Disposition: attachment` and a
non-renderable content type so archive bytes can never execute in a browser
origin shared with the UI.
*Source: standard practice.*

## B.5 Correctness and testing

### TR-22
**M** · A **conformance test suite** MUST implement all six `MS-API §9` fixture
groups as executable tests:
| Group | Coverage |
|---|---|
| §9.1 | Round-trip publish → fetch; digest equality across `201`, `/download` and `/versions` |
| §9.2 | Immutability: second `PUT` → `409`, same body and different body |
| §9.3 | Format dispatch: publish zip → download returns `application/zip`, same bytes |
| §9.4 | Validation: missing `apm.yml`; version mismatch; absolute paths in tar; symlink in zip |
| §9.5 | Auth: anonymous public read; missing `read` scope → 403; missing `publish` scope → 403; no token → 401 |
| §9.6 | Error format: every 4xx is `application/problem+json` with `title` + `status` |
*Source: `MS-API §9`.*

### TR-23
**M** · The suite MUST assert exact `snake_case` field names on every response
body, since a casing regression is silently ignored by the reference client.
*Source: `MS-API §1.3.1`; see [FR-04](#fr-04).*

### TR-24
**S** · A property/fuzz test SHOULD assert the round-trip invariant
`sha256(download(publish(bytes))) == sha256(bytes)` over generated archives.
*Source: `req-rg-001`.*

### TR-25
**S** · Malicious-archive fixtures (zip slip, symlink, hardlink, absolute path,
zip bomb, central-directory/local-header mismatch, missing `apm.yml`, invalid
YAML) SHOULD be committed and asserted to be rejected.
*Source: `MS-API §9.4`; `req-sc-002`, `req-sc-004`.*

### TR-26
**S** · An end-to-end test SHOULD run the real `apm` CLI against the server
(publish then install) to catch divergence the fixture suite cannot.
*Source: derived — the CLI is the actual client of record.*

## B.6 Performance and operations

### TR-27
**S** · `GET /versions` SHOULD respond in < 50 ms p95 for packages with ≤ 1,000
versions; `GET /download` SHOULD stream with constant memory regardless of
archive size.
*Source: project requirement.*

### TR-28
**S** · Cache headers: `/versions` → `Cache-Control: max-age=60, public`;
`/download` → `Cache-Control: max-age=86400, immutable`. Conditional `GET` via
`If-None-Match` SHOULD return `304`.
*Source: `MS-API §5`, §7.1.*

### TR-29
**M** · Structured JSON logs with a per-request correlation id, echoed in
Problem bodies as `extensions.request_id` for support.
*Source: project requirement.*

### TR-30
**M** · Configuration via environment variables with startup validation. The
process MUST fail fast on invalid configuration rather than start degraded.
*Source: project requirement (UC-17).*

### TR-31
**S** · Multi-instance deployment SHOULD be possible with shared blob storage
and a shared metadata store. The SQLite default is explicitly single-instance;
scaling out requires the Postgres adapter.
> Stated so the limit is a known trade-off rather than a surprise.
*Source: [ADR-0002](adr/0002-metadata-store.md).*

---

## Deferred / explicitly out of scope

| Item | Reason | Source |
|---|---|---|
| Yank / withdrawal | Reserved for OpenAPM v0.2 | `MS-SPEC §7.9` |
| Package signing | "Not yet provided" upstream | `MS-GUIDE` |
| SBOM / SLSA provenance / attestations | Reserved for v0.2 | `MS-SPEC §10.12` |
| Hash-algorithm agility (beyond sha256) | "Not yet provided" upstream | `MS-GUIDE` |
| Vulnerability / malicious-instruction scanning | Explicitly out of scope for v1 | |
| License-text validation | Out of scope for v1 | `MS-API §6` |
| Remote / virtual / smart repositories, replication | Unsupported; local-only design | |
| Building the `apm` client | External dependency we satisfy, not build | project scope |
| Git-host proxying (`PROXY_REGISTRY_URL`) | Orthogonal mechanism | `MS-SPEC` registry-proxy guide |
