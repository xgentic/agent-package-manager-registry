# API contract

The wire contract **this server implements**. It specifies concrete choices for
vendor-defined aspects and includes clearly-labelled vendor extensions.

## 1. Conventions

| Aspect | Value |
|---|---|
| Base URL | `https://<host>/api/agentpackages/<repository>` — no trailing slash |
| Client behaviour | Clients append paths starting `/v1/...` |
| Transport | HTTPS. Plain HTTP only for local dev, behind an explicit flag |
| Field naming | **`snake_case` only.** camelCase variants MUST NOT be emitted |
| JSON media type | `application/json; charset=utf-8` |
| Error media type | `application/problem+json; charset=utf-8` |
| Archive media types | `application/zip`, `application/gzip` |
| API versioning | `/v1/` path segment; `/v2/` reserved for breaking changes |

### 1.1 Package identity

`{owner}/{repo}` for GitHub-origin packages. All other origins percent-encode
the full identity into **one** segment.

| Origin | Identity | Path |
|---|---|---|
| GitHub | `acme/web-skills` | `/v1/packages/acme/web-skills/versions` |
| GitLab | `gitlab.com/acme/web-skills` | `/v1/packages/gitlab.com%2Facme%2Fweb-skills/versions` |
| Azure DevOps | `dev.azure.com/org/proj/repo` | `/v1/packages/dev.azure.com%2Forg%2Fproj%2Frepo/versions` |

Server rules:
- Percent-encoded segments are decoded **before** lookup.
- Traversal/validation checks run on the **decoded** value.
- Owner and repo are lowercased canonically; both path forms resolve to the same
  package.

### 1.2 Versions

Version selectors are **opaque and case-sensitive**. The server performs no
semver parsing, normalisation, filtering or reordering. Semver range resolution
(`^1.2.3`, `>=1.2.0 <2.0.0`) happens entirely client-side against the list
returned by `/versions`.

### 1.3 Immutability

A successfully published `(repository, identity, version)` can never change its
bytes. `sha256(bytes served at /download)` always equals the advertised
`digest`. This is `req-rg-001` — the trust anchor for consumer lockfiles.

---

## 2. Authentication

```
Authorization: Bearer <opaque-token>
Authorization: Basic  <base64(username:password)>
```

Both forms resolve to the same principal and **must** yield identical scope
grants. When a client has both configured, it sends Bearer.

### Scopes

| Scope | Required for |
|---|---|
| `read` | `GET /versions`, `GET /download` |
| `read:{owner}/{repo}` | Same, package-scoped |
| `publish:{owner}/{repo}` | `PUT /versions/{version}` |
| `publish:{owner}/*` | Publish to any repo under an owner |

Scope strings are server-side only and never appear in responses.

### 401 vs 403

- **No credentials presented** → `401`, so the client can distinguish
  "authenticate" from "not permitted".
- **Credentials presented, scope insufficient** → `403`, with the missing scope
  named in the Problem body.
- Anonymous `GET` succeeds only on repositories whose visibility is `public`.

`401`/`403` bodies name the client env var to set:

```
APM_REGISTRY_TOKEN_{NAME}         →  Bearer
APM_REGISTRY_USER_{NAME} + _PASS_ →  Basic
```

`{NAME}` is the registry name uppercased with `-` and `.` mapped to `_`
(`corp-main` → `CORP_MAIN`).

---

## 3. Endpoints

### 3.1 `GET /v1/packages/{owner}/{repo}/versions`

List every published version of a package.

**Request**
```http
GET /api/agentpackages/corp-main/v1/packages/acme/web-skills/versions HTTP/1.1
Host: registry.example.com
Authorization: Bearer <token>
Accept: application/json
```

**Response `200`**
```json
{
  "package": "acme/web-skills",
  "versions": [
    {
      "version": "1.2.0",
      "digest": "sha256:abc123...",
      "published_at": "2026-03-01T12:00:00Z",
      "size_bytes": 24576
    },
    {
      "version": "1.1.0",
      "digest": "sha256:def456...",
      "published_at": "2026-02-14T08:00:00Z",
      "size_bytes": 23000
    }
  ]
}
```

| Field | Required | Notes |
|---|---|---|
| `package` | yes | Echoes the requested identity, decoded and canonicalised |
| `versions[]` | yes | May be `[]`; **never omitted** |
| `versions[].version` | yes | Opaque selector |
| `versions[].digest` | yes | `sha256:` + 64 lowercase hex |
| `versions[].published_at` | yes | ISO 8601 UTC |
| `versions[].size_bytes` | optional | Emitted by this server |

**Rules**
- Ordering is publish-time descending; clients must not depend on it.
- **No pagination.** The complete set is returned — truncation would silently
  corrupt client-side range resolution.
- Idempotent: identical inputs return the same set.

**Headers** `Cache-Control: max-age=60, public`

**Errors** `401` (missing/invalid credentials, or anonymous read disabled) ·
`403` (no `read` scope) · `404` (unknown package)

---

### 3.2 `GET /v1/packages/{owner}/{repo}/versions/{version}/download`

Stream the immutable archive. Named `/download`, not `/tarball`, because both
zip and gzip are valid — `Content-Type` discriminates.

**Request**
```http
GET /api/agentpackages/corp-main/v1/packages/acme/web-skills/versions/1.2.0/download HTTP/1.1
Authorization: Bearer <token>
Accept: application/gzip, application/zip
```

**Response `200`**
```http
HTTP/1.1 200 OK
Content-Type: application/zip
Content-Length: 24576
Digest: sha256=<base64-of-binary-digest>
ETag: "sha256:abc123..."
Cache-Control: max-age=86400, immutable
Content-Disposition: attachment; filename="web-skills-1.2.0.zip"

<binary archive body>
```

| Header | Status | Notes |
|---|---|---|
| `Content-Type` | required | The media type recorded at publish |
| `Content-Length` | emitted | Clients buffer to memory if absent |
| `Digest` | emitted | RFC 3230. Clients verify against `/versions`, not this header |
| `ETag` | emitted | Enables `If-None-Match` → `304` |
| `Content-Disposition` | emitted | Our addition; keeps archives from rendering in the UI origin |

**Rules**
- Bytes are byte-identical to what was uploaded. **No transcoding** — format
  conversion is not a server responsibility.
- `Accept` may be ignored; we serve what was published.
- Download URLs are **stable and permanent** — they are recorded as
  `resolved_url` in consumer lockfiles. No signed or expiring URLs.

**Errors** `401`/`403` (as §3.1) · `404` (unknown tuple) · `410` (reserved for
yank; not emitted in v1)

---

### 3.3 `PUT /v1/packages/{owner}/{repo}/versions/{version}`

Publish a version. Immutable — republishing returns `409`.

**Request**
```http
PUT /api/agentpackages/corp-main/v1/packages/acme/web-skills/versions/1.2.0 HTTP/1.1
Authorization: Bearer <publish-token>
Content-Type: application/zip
Content-Length: 24576

<binary archive body>
```

**Expected archive layout** (flat registry archive, as produced by `apm publish`):
```
{name}-{version}.zip
├── apm.yml          ← required, at root, must declare name and version
├── .apm/            ← primitives
├── README.md        ← if present
├── CHANGELOG.md     ← if present
└── LICENSE          ← case-insensitive; symlinks excluded
```

> Not the `apm pack` plugin-bundle layout (`{name}-{version}/plugin.json`).
> That layout is rejected — there is no `apm.yml` at the archive root.

**Response `201`**
```json
{
  "package": "acme/web-skills",
  "version": "1.2.0",
  "digest": "sha256:abc123...",
  "published_at": "2026-03-01T12:00:00Z",
  "size_bytes": 24576
}
```

> Some registries return `201` with an empty body and the CLI still treats it as
> success. We always return the full body — the CLI prints `digest` and
> `published_at` from it.

**Validation** (all applied; `422` lists every failure in `extensions.errors[]`)

| # | Rule |
|---|---|
| 1 | Version selector is non-empty after decoding, has no control characters, and is treated as an opaque key — never a path |
| 2 | Archive parses cleanly as the declared `Content-Type` |
| 3 | `apm.yml` exists at the root of the extraction tree |
| 4 | `apm.yml` is valid YAML with `name` and `version` |
| 5 | `apm.yml.version` is present |
| 6 | `apm.yml.name` matches the URL identity (or its repo-name suffix) |
| 7 | No entry has an absolute path, `..` segment, symlink or hardlink |
| 8 | Archive size within limits |

Rule 5 is strengthened by config: with
`APM_REGISTRY_REQUIRE_MANIFEST_VERSION_MATCH=true` (default) the manifest
version must equal the URL version. `MS-API §6.5` explicitly leaves this to
registry policy.

**Errors**

| Status | Reason |
|---|---|
| `400` | Body does not parse as the declared type (corrupt gzip, invalid zip directory) |
| `401` | No credentials |
| `403` | Token lacks `publish` scope for this package |
| `409` | Version already published |
| `413` | Body exceeds the configured archive size limit |
| `415` | `Content-Type` is neither `application/gzip` nor `application/zip` |
| `422` | Validation failed; see `extensions.errors[]` |

**409 body**
```json
{
  "type": "https://docs.apm.dev/errors/version-conflict",
  "title": "Version already published",
  "status": 409,
  "detail": "Version 1.2.0 of acme/web-skills was already published at 2026-02-14T08:00:00Z",
  "instance": "/v1/packages/acme/web-skills/versions/1.2.0",
  "extensions": {
    "previous_publish": "2026-02-14T08:00:00Z",
    "previous_digest": "sha256:def456..."
  }
}
```

`409` is returned whether or not the submitted bytes are identical to the stored
bytes — see [ADR-0008](adr/0008-version-immutability.md).

**422 body**
```json
{
  "type": "https://docs.apm.dev/errors/validation-failed",
  "title": "Package validation failed",
  "status": 422,
  "detail": "The uploaded archive failed 2 validation rules",
  "instance": "/v1/packages/acme/web-skills/versions/1.2.0",
  "extensions": {
    "errors": [
      { "rule": "manifest_present", "message": "no apm.yml found at archive root" },
      { "rule": "entry_safety",     "message": "entry '../../etc/passwd' escapes the archive root", "entry": "../../etc/passwd" }
    ],
    "request_id": "01JQ..."
  }
}
```

---

## 4. Error model

Every `4xx`/`5xx` is RFC 7807 `application/problem+json`.

**Required** `title`, `status`. **Recommended and emitted by us** `type`,
`detail`, `instance`, `extensions.request_id`.

Vendor data lives under `extensions.*`; clients must ignore unknown extensions.
Problem bodies never contain stack traces, filesystem paths, SQL or token
material.

| `rule` value in `extensions.errors[]` | Meaning |
|---|---|
| `version_selector` | Empty / control characters in the decoded selector |
| `archive_parse` | Not a valid archive of the declared type |
| `manifest_present` | No `apm.yml` at archive root |
| `manifest_yaml` | `apm.yml` is not valid YAML |
| `manifest_fields` | `name` or `version` missing |
| `manifest_version_match` | `apm.yml.version` ≠ URL version |
| `manifest_name_match` | `apm.yml.name` ≠ URL identity |
| `entry_safety` | Absolute path, `..`, symlink or hardlink |
| `archive_limits` | Entry count or uncompressed size cap exceeded |

---

## 5. Conformance

**Required of a conformant v1 server** — all implemented:

- [x] `GET /v1/packages/{owner}/{repo}/versions`
- [x] `GET /v1/packages/{owner}/{repo}/versions/{version}/download`
- [x] `PUT /v1/packages/{owner}/{repo}/versions/{version}`
- [x] RFC 7807 bodies on all 4xx/5xx
- [x] Bearer auth on all endpoints (anonymous reads optional)
- [x] sha256 digest accuracy
- [x] Version immutability

**SHOULD, for a fully-featured server** — all implemented:

- [x] `Cache-Control` and `ETag` on read endpoints
- [x] Conditional `GET` (`If-None-Match` → `304`)
- [x] Per-version `size_bytes`

**OpenAPM v0.1 `Registry` class** — `req-rg-001` only, and it is satisfied:
digest-accurate bytes, and previously published `(name, version)` bytes are
never mutated.

The executable form of this section is `test/conformance/`, implementing all six
`MS-API §9` fixture groups ([TR-22](02-requirements.md#tr-22)).

---

## 6. Rate limiting

`429 Too Many Requests` with `Retry-After` in seconds:

```json
{
  "title": "Rate limit exceeded",
  "status": 429,
  "extensions": { "limit": 100, "remaining": 0 }
}
```

`/health` and `/ready` are never rate-limited.

---

## 7. Vendor extensions

Extensions beyond the core API. They are optional and no conformant client
depends on them; they exist to support search and the web UI.

### 7.1 `GET /v1/search`

An optional search endpoint for package discovery. The response shape is
defined here.

```http
GET /api/agentpackages/corp-main/v1/search?q=review&limit=20&offset=0
```
```json
{
  "query": "review",
  "total": 2,
  "results": [
    {
      "package": "acme/code-review-prompts",
      "description": "Code review prompt library",
      "latest_version": "2.1.0",
      "published_at": "2026-04-02T09:30:00Z"
    }
  ]
}
```

Results are filtered by the caller's read scope. Unlike `/versions`, this
endpoint **may** paginate.

### 7.2 Operational endpoints

| Endpoint | Auth | Purpose |
|---|---|---|
| `GET /health` | none | Liveness. `{"status":"ok"}` |
| `GET /ready` | none | Readiness: metadata store + blob store reachable |

---

## 8. Reserved surface

| Surface | Status |
|---|---|
| `410 Gone` on download | Reserved for yank; never emitted in v1 |
| `DELETE` on any package route | Not exposed over HTTP; break-glass deletion is CLI-only |
| Signing / attestation headers | Reserved for OpenAPM v0.2 |
| `/v2/` prefix | Reserved; router supports parallel versions |
