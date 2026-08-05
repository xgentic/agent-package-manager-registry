# Use cases

Terms are per [00-glossary.md](00-glossary.md).

## Actors

| Actor | Type | Description |
|---|---|---|
| **Producer** | Human | Authors a package, publishes versions. Works through `apm publish` or the web UI. |
| **Consumer** | Human | Declares dependencies and runs `apm install`. Never talks to the registry directly — the CLI does. |
| **`apm` CLI** | System (external) | The client we must satisfy. Calls `/versions`, `/download`, `PUT /versions/{v}`. We do not build it. |
| **CI pipeline** | System | Publishes on tag, or installs with `--frozen`. Uses a scoped token from a secret store. |
| **Operator** | Human | Runs the registry. Manages repositories, tokens, quotas, storage; reads the audit log. |
| **Governance owner** | Human | Sets `apm-policy.yml` org-wide to force traffic through this registry. Read-only consumer of our audit surface. |

## Use case index

| ID | Title | Actor | Priority |
|---|---|---|---|
| [UC-01](#uc-01-publish-a-package-version) | Publish a package version | `apm` CLI / CI | Must |
| [UC-02](#uc-02-reject-a-duplicate-version) | Reject a duplicate version | `apm` CLI | Must |
| [UC-03](#uc-03-reject-an-invalid-archive) | Reject an invalid archive | `apm` CLI | Must |
| [UC-04](#uc-04-list-versions-of-a-package) | List versions of a package | `apm` CLI | Must |
| [UC-05](#uc-05-download-a-package-archive) | Download a package archive | `apm` CLI | Must |
| [UC-06](#uc-06-resolve-a-semver-range) | Resolve a semver range | Consumer | Must |
| [UC-07](#uc-07-frozen-reinstall-from-a-lockfile) | Frozen reinstall from a lockfile | CI pipeline | Must |
| [UC-08](#uc-08-authenticate-with-a-bearer-token) | Authenticate with a bearer token | all | Must |
| [UC-09](#uc-09-authenticate-with-http-basic) | Authenticate with HTTP Basic | `apm` CLI | Should |
| [UC-10](#uc-10-anonymous-read-of-a-public-repository) | Anonymous read of a public repository | `apm` CLI | Should |
| [UC-11](#uc-11-publish-a-non-github-origin-package) | Publish a non-GitHub-origin package | Producer | Must |
| [UC-12](#uc-12-upload-an-artefact-through-the-web-ui) | Upload an artefact through the web UI | Producer | Must |
| [UC-13](#uc-13-browse-and-search-the-registry) | Browse and search the registry | Producer / Consumer | Must |
| [UC-14](#uc-14-inspect-a-published-versions-contents) | Inspect a published version's contents | Producer / Consumer | Should |
| [UC-15](#uc-15-manage-api-tokens) | Manage API tokens | Operator / Producer | Must |
| [UC-16](#uc-16-manage-repositories) | Manage repositories | Operator | Must |
| [UC-17](#uc-17-run-and-operate-the-server) | Run and operate the server | Operator | Must |
| [UC-18](#uc-18-review-the-audit-log) | Review the audit log | Operator / Governance | Must |
| [UC-19](#uc-19-enforce-quotas-and-rate-limits) | Enforce quotas and rate limits | Operator | Should |
| [UC-20](#uc-20-search-via-the-api-vendor-extension) | Search via the API (vendor extension) | `apm` CLI / tooling | Could |
| [UC-21](#uc-21-delete-a-version-break-glass) | Delete a version (break-glass) | Operator | Should |
| [UC-22](#uc-22-yank-a-version) | Yank a version | Operator | Won't (v1) |

---

## UC-01 Publish a package version

**Actor** `apm` CLI (`apm publish --package acme/my-skill --registry corp-main`), or CI.

**Preconditions**
- The producer holds a token with `publish:acme/my-skill` or `publish:acme/*`.
- The target repository exists.
- The CLI has auto-packed a flat archive `{name}-{version}.zip` containing
  `apm.yml` and `.apm/` at the archive root.

**Main flow**
1. Client sends `PUT /v1/packages/acme/my-skill/versions/1.2.0` with
   `Content-Type: application/zip` and the archive as the raw body.
2. Server authenticates the token and checks the `publish` scope.
3. Server streams the body to temporary storage, computing SHA-256 as it goes,
   enforcing the size cap.
4. Server validates the archive against the eight publish rules — see [FR-10](02-requirements.md#fr-10).
5. Server checks `(repository, identity, version)` does not already exist.
6. Server commits blob + metadata atomically, recording the uploaded
   `Content-Type` for later replay.
7. Server writes an audit entry: token id, identity, version, digest, timestamp.
8. Server responds `201` with `{package, version, digest, published_at, size_bytes}`.

**Postconditions**
- The version is immutable and immediately listable and downloadable.
- `sha256(bytes served at /download) == digest returned here` — forever.

**Alternate flows** → [UC-02](#uc-02-reject-a-duplicate-version) (409),
[UC-03](#uc-03-reject-an-invalid-archive) (422/400/415/413), missing scope → 403,
missing token → 401.

**Note** Some registries return `201` with an empty body and the CLI still
treats it as success. We always return the full body — it is what the CLI prints
(`digest`, `published_at`).

---

## UC-02 Reject a duplicate version

**Actor** `apm` CLI. 
**Main flow**
1. Client `PUT`s a version that already exists.
2. Server responds `409 Conflict` with `application/problem+json`, `detail`
   naming the existing publish time, and `extensions.previous_publish` /
   `extensions.previous_digest`.

**Rules**
- Applies whether the body is **identical or different** — both cases result in 409.
- We accept byte-identical republish as a conflict (409); see
  [ADR-0008](adr/0008-version-immutability.md).
- The server MUST NOT replace the bytes of a previously served version under any
  circumstance.

**Producer remedy** Bump `version:` in `apm.yml` and republish.

---

## UC-03 Reject an invalid archive

**Actor** `apm` CLI. 
**Trigger** Any publish that fails validation.

| Condition | Status |
|---|---|
| `Content-Type` not `application/zip` / `application/gzip` | `415` |
| Body exceeds the size cap | `413` |
| Body does not parse as the declared type (corrupt zip/gzip) | `400` |
| No `apm.yml` at archive root | `422` |
| `apm.yml` is not valid YAML, or lacks `name` / `version` | `422` |
| `apm.yml.version` ≠ URL version (when lockstep enabled) | `422` |
| `apm.yml.name` does not match the URL identity | `422` |
| Entry with absolute path, `..` traversal, symlink or hardlink | `422` |
| Entry count over cap | `422` |

**Rules**
- `422` bodies list every failure in `extensions.errors[]` — not just the first.
  Producers should be able to fix everything in one round-trip.
- Validation happens **before** persistence. A rejected publish leaves no blob,
  no metadata row, and no partially written temp file.
- The version selector itself is validated after URL-decoding: non-empty, no
  control characters, and never treated as a filesystem path.

---

## UC-04 List versions of a package

**Actor** `apm` CLI.

**Main flow**
1. `GET /v1/packages/acme/web-skills/versions`.
2. Server authenticates (or allows anonymous read on a public repository).
3. Server returns `200` with `{package, versions[]}` where each entry has
   `version`, `digest`, `published_at`, and optionally `size_bytes`.

**Rules**
- `package` echoes the requested identity — the client may have used the
  percent-encoded form and needs the canonical value back.
- `versions` MUST be present even when empty (`[]`), never omitted.
- Ordering SHOULD be publish-time descending; clients MUST NOT rely on it.
- Field names are `snake_case`. Emitting `publishedAt` makes us non-conformant
  and the reference client silently ignores it — a **silent** failure, so we
  test for it.
- Unknown package → `404`.
- Idempotent: identical inputs return the same set.

---

## UC-05 Download a package archive

**Actor** `apm` CLI.

**Main flow**
1. `GET /v1/packages/acme/web-skills/versions/1.2.0/download`, `Accept:
   application/gzip, application/zip`.
2. Server authenticates / permits anonymous read.
3. Server streams the stored bytes with the **stored** `Content-Type`,
   `Content-Length`, RFC 3230 `Digest`, and `ETag`.
4. Client re-hashes the body and compares to `versions[].digest` or the
   lockfile's `resolved_hash`; a mismatch aborts before extraction.

**Rules**
- The bytes are byte-identical to what was uploaded. **No transcoding** —
  format conversion is explicitly not a server responsibility.
- `Accept` MAY be ignored; we serve what was published.
- Unknown tuple → `404`. Yanked → `410` (reserved for future use, [UC-22](#uc-22-yank-a-version)).
- `Cache-Control: max-age=86400, immutable`.

---

## UC-06 Resolve a semver range

**Actor** Consumer, via `apm install acme/foo#^1.2.0`.

**Main flow**
1. CLI calls `GET .../versions`.
2. CLI filters semver-parseable versions, applies the range, picks the highest
   match, then downloads it.

**Server obligation** Exactly one: return the complete, accurate version list.
The registry performs **no** range logic. Consequences we must honour:

- Version strings stay opaque and case-sensitive on our side.
- We do not normalise `v1.2.3` → `1.2.3`, reorder, or drop non-semver entries —
  `stable`, `main` and commit pins are legitimate versions matched exactly.
- We must not paginate `/versions` in a way that truncates the list; a partial
  list silently produces a wrong resolution. See
  [ADR-0012](adr/0012-search-vendor-extension.md) for where pagination *is*
  acceptable.

---

## UC-07 Frozen reinstall from a lockfile

**Actor** CI pipeline (`apm install --frozen`).

**Main flow**
1. CI reads `apm.lock.yaml`, which holds `resolved_url` and `resolved_hash`.
2. CI `GET`s the `resolved_url` directly — possibly skipping `/versions`.
3. CI verifies SHA-256 against `resolved_hash` and fails closed on mismatch.

**Server obligations**
- `resolved_url` must stay valid for the life of the version. Download URLs are
  therefore **stable and permanent**, not signed or expiring.
- Byte-for-byte stability is absolute. This is `req-rg-001` and the reason
  storage is content-addressed ([ADR-0003](adr/0003-content-addressed-storage.md)).
- A mirror may serve the bytes, but they must still hash to the recorded value —
  so we must never re-compress, re-zip or normalise stored archives.

---

## UC-08 Authenticate with a bearer token

**Actor** all API clients.

**Main flow**
1. Client sends `Authorization: Bearer <token>`.
2. Server hashes the presented token and looks it up in constant time.
3. Server checks expiry, revocation and required scope for the route.
4. Request proceeds, and the token id is attached to the audit context.

**Rules**
- Missing credentials → `401` (not `403`), so the client can distinguish
  "auth required" from "authenticated but not authorised".
- Wrong/insufficient scope → `403`, with a Problem body naming the missing scope.
- Tokens are stored one-way hashed (argon2id); plaintext is shown once at
  creation and never again.
- Never log or echo a token value — including in audit entries, which record the
  token **id**.

**Client-side context (informational)** The CLI resolves credentials as:
`APM_REGISTRY_TOKEN_{NAME}` → `~/.apm/config.json` → anonymous. `{NAME}` is the
registry name uppercased with `-` and `.` mapped to `_`, so `corp-main` →
`APM_REGISTRY_TOKEN_CORP_MAIN`. Our `401`/`403` messages should name that exact
variable so the remediation is copy-pasteable.

---

## UC-09 Authenticate with HTTP Basic

**Actor** `apm` CLI with `APM_REGISTRY_USER_{NAME}` + `APM_REGISTRY_PASS_{NAME}`.

**Main flow**
1. Client sends `Authorization: Basic <base64(user:pass)>`.
2. Server resolves it to the same principal and scope set as the equivalent bearer token.

**Rule** Basic and Bearer MUST produce **identical scope grants** — a
Basic-authed `admin:password` and its Bearer equivalent are indistinguishable to
authorisation. When a client sets both, it sends Bearer.

---

## UC-10 Anonymous read of a public repository

**Actor** `apm` CLI with no configured credentials.

**Main flow**
1. Client sends `GET /versions` with no `Authorization` header (the CLI tries
   anonymous first when no env var matches the URL).
2. If the repository's visibility is `public`, the server responds `200`.
3. If `private`, the server responds `401` with a remediation `detail`.

**Rule** Visibility is a per-repository setting ([UC-16](#uc-16-manage-repositories)).
Publishing always requires authentication, regardless of visibility.

---

## UC-11 Publish a non-GitHub-origin package

**Actor** Producer with a GitLab / Azure DevOps / self-hosted origin.

**Main flow**
1. Client percent-encodes the full identity into one segment:
   `PUT /v1/packages/gitlab.com%2Facme%2Fweb-skills/versions/2.0.0`.
2. Server decodes the segment, canonicalises the identity, and proceeds as UC-01.
3. `/versions` echoes the decoded identity in `package`.

**Rules**
- Servers MUST decode percent-encoded segments before lookup.
- Decoding happens **before** validation, never after: `acme%2F..%2Fevil`
  decodes to `acme/../evil` and must be rejected. Traversal checks run on the
  decoded value.
- `acme/web-skills` (two segments) and `acme%2Fweb-skills` (one segment) denote
  the **same** package and must resolve identically.

See [ADR-0007](adr/0007-package-identity.md) for the routing mechanics.

---

## UC-12 Upload an artefact through the web UI

**Actor** Producer, in a browser.

**Main flow**
1. Producer signs in and picks a target repository.
2. Producer drops a `.zip` onto the upload form.
3. The UI reads `apm.yml` from the archive server-side and pre-fills identity and
   version, so the producer confirms rather than retypes them.
4. The UI shows a **pre-flight validation report**: the same eight publish checks
   as UC-01, plus a preview of the primitives found under `.apm/`.
5. Producer confirms; the server runs the identical publish path as UC-01.
6. The UI shows the resulting digest and a copy-pasteable
   `apm install <identity>#<version>` line.

**Rules**
- The web upload **MUST** go through the same domain service and validation
  pipeline as the API — no second, divergent code path. This is the single most
  important constraint on the web tier ([ADR-0010](adr/0010-web-ui.md)).
- Web sessions authenticate with a cookie, not an API token, and are
  CSRF-protected. Session identity maps to the same scope model.
- Immutability applies identically: re-uploading an existing version is refused
  with the same 409 semantics, surfaced as a form error rather than JSON.

---

## UC-13 Browse and search the registry

**Actor** Producer / Consumer.

**Main flow**
1. User opens the web UI and sees repositories they can read.
2. User searches by identity substring, owner, or description text.
3. User opens a package page: description, latest version, full version history
   with digest, size, publish time and publisher.
4. User copies the install snippet.

**Rules**
- Search results are filtered by the caller's read scope — a user must never
  learn a private package exists. Absence and denial look identical.
- Version listings in the UI may paginate; the **API** `/versions` may not
  ([UC-06](#uc-06-resolve-a-semver-range)).

---

## UC-14 Inspect a published version's contents

**Actor** Producer / Consumer.

**Main flow**
1. User opens a version page.
2. The UI lists the archive entries and groups the primitives found under
   `.apm/` by type (`skills`, `agents`, `prompts`, `instructions`, `hooks`,
   `commands`, `mcp`).
3. User previews a text file (`SKILL.md`, `README.md`) rendered as Markdown.

**Rules**
- Primitive inventory is computed **once at publish time** and stored as
  metadata. Version contents are immutable, so re-deriving them per request is
  wasted work.
- Previews are size-capped and rendered as sanitised Markdown. Package content is
  attacker-controlled text — it is never injected as raw HTML, and never executed.

---

## UC-15 Manage API tokens

**Actor** Operator (any token) / Producer (own tokens).

**Main flow**
1. User opens *Tokens*, creates one with a name, scope set and optional expiry.
2. Server generates a high-entropy secret, stores only its argon2id hash, and
   displays the plaintext **once**.
3. User copies it into `APM_REGISTRY_TOKEN_{NAME}` or a CI secret.
4. User can list (name, scopes, created, last used, expiry) and revoke.

**Rules**
- Revocation takes effect immediately.
- A token can only be granted scopes its creator holds — no privilege escalation.
- `last_used_at` is recorded to make stale-credential cleanup possible.

---

## UC-16 Manage repositories

**Actor** Operator. (See [ADR-0009](adr/0009-multi-repository-namespacing.md).)

**Main flow**
1. Operator creates a repository with a name, visibility (`public` | `private`)
   and optional quota.
2. The registry base URL for it becomes
   `https://<host>/api/agentpackages/<name>`.
3. Operator hands that URL to producers/consumers for
   `apm config set registry.<name>.url <url>`.

**Rules**
- Repository names match `^[a-z0-9][a-z0-9._-]*$` — same character class the
  APM client allows for registry names (lowercase letters, digits, `-`, `.`).
- Deleting a non-empty repository requires explicit confirmation and is audited.
- Only **local** repositories exist. Remote, virtual, smart repositories and
  replication are unsupported for Agent Packages upstream and out of scope here.

---

## UC-17 Run and operate the server

**Actor** Operator.

**Main flow**
```
apm-registry serve --port 3000            # run the HTTP server
apm-registry migrate                      # apply schema migrations
apm-registry repo create corp-main --private
apm-registry token create --name ci --scope 'publish:acme/*'
apm-registry gc --dry-run                 # find unreferenced blobs
apm-registry verify                       # re-hash blobs against metadata
```

**Rules**
- `serve` fails fast and loudly on invalid config rather than starting degraded.
- `migrate` is idempotent and safe to run on every boot.
- `verify` exists because `req-rg-001` is an invariant we should be able to
  *prove*, not just assume — it re-reads every blob and compares to the recorded
  digest.
- `GET /health` (liveness) and `GET /ready` (readiness: DB + blob store
  reachable) are unauthenticated and excluded from rate limits.

---

## UC-18 Review the audit log

**Actor** Operator / Governance owner.

**Main flow**
1. Operator filters the audit log by repository, identity, actor or time range.
2. Each publish entry shows: token id, `owner/repo`, version, sha256, timestamp,
   client IP, outcome.

**Rules**
- **Every** successful `PUT` is recorded — this is a required checklist item.
- Failed auth and failed validation are recorded too; they are the interesting
  signal for an operator.
- Audit entries are append-only and never contain token plaintext.

---

## UC-19 Enforce quotas and rate limits

**Actor** Operator.

**Main flow**
- Per-archive size cap (default 50 MB) → `413`.
- Per-token / per-owner storage and version-count quotas → `403` with an
  explanatory Problem body.
- Rate limits → `429` with `Retry-After` (seconds) and `extensions.limit` /
  `extensions.remaining`.

**Note on the size cap.** We enforce compressed (50 MB), uncompressed (100 MB), and
entry count (10,000) limits, since accepting an archive that cannot be extracted
is a bad trade ([TR-14](02-requirements.md#tr-14)).

---

## UC-20 Search via the API (vendor extension)

**Actor** Tooling / `apm` CLI.

**Main flow** `GET /v1/search?q=<term>` returns matching packages, scope-filtered.

**Rules**
- **Vendor extension.** Implemented to support search and the web UI, and documented
  as optional so it is clear what is standard and what is not
  ([ADR-0012](adr/0012-search-vendor-extension.md)).
- No conformant client depends on it, so it may paginate freely.

---

## UC-21 Delete a version (break-glass)

**Actor** Operator.

**Trigger** Leaked secret or unlawful content in a published archive.

**Main flow**
1. Operator runs `apm-registry version delete <repo> <identity> <version> --confirm`.
2. Server removes metadata and blob, and writes a **tombstone** so the tuple can
   never be reused.
3. Subsequent requests return `404`.

**Rules**
- Not exposed over the HTTP API. CLI-only, with explicit confirmation.
- A tombstone ensures deleted versions cannot be republished with different bytes,
  which would break frozen installs. Deletion makes a version *disappear*; it never
  makes it *change*.
- Deletion is loudly audited. It breaks frozen installs by design — that is the
  cost of a break-glass action, and the operator must see that stated.

---

## UC-22 Yank a version

**Status** **Won't implement in v1.** Withdrawal semantics (`yanked: true`,
`superseded_by`, `refuse_yanked`) are reserved for future versions, and
`410 Gone` is reserved for the same reason.

**v1 posture** Reserve the status code and leave a nullable `yanked_at` column
in the schema for future versions. Producers needing withdrawal today rely on
out-of-band advisories.
