# Architecture Decision Records

One decision per file, immutable once `Accepted`. Superseding a decision means a
**new** ADR that references the old one; the old file stays as written so the
reasoning at the time survives.

**Format** — Status · Context · Decision · Consequences · Alternatives considered.

**Status values** — `Proposed` · `Accepted` · `Superseded by ADR-xxxx` · `Deprecated`.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-runtime-go-net-http.md) | Runtime and HTTP framework: Go + net/http | Accepted |
| [0002](0002-metadata-store.md) | Metadata store: SQLite behind a port | Accepted |
| [0003](0003-content-addressed-storage.md) | Content-addressed blob storage | Accepted |
| [0004](0004-archive-format.md) | Accept both ZIP and tar.gz on publish | Accepted |
| [0005](0005-token-auth.md) | Opaque hashed bearer tokens with a scope grammar | Accepted |
| [0006](0006-rfc7807-errors.md) | RFC 7807 as the sole error envelope | Accepted |
| [0007](0007-package-identity.md) | Dual-form package identity routing | Accepted |
| [0008](0008-version-immutability.md) | Version immutability: 409 over idempotent republish | Accepted |
| [0009](0009-multi-repository-namespacing.md) | Multiple named repositories in the base URL | Accepted |
| [0010](0010-web-ui.md) | Server-rendered web UI over the same domain services | Superseded by ADR-0014 |
| [0011](0011-publish-pipeline.md) | Streaming validate-then-commit publish pipeline | Accepted |
| [0012](0012-search-vendor-extension.md) | Search as a labelled vendor extension | Accepted |
| [0013](0013-operator-cli.md) | Operator CLI as the administrative surface | Accepted |
| [0014](0014-react-spa.md) | React SPA embedded in the Go binary | Accepted |

## Decisions driven by upstream conflict

Two ADRs exist because the upstream sources **contradict each other**. They are
the highest-risk decisions in the project and the first to revisit when OpenAPM
v0.2 lands:

- **[ADR-0004](0004-archive-format.md)** — `MS-API` says accept zip *and* gzip
  and the CLI sends zip by default; OpenAPM `req-sc-004` says consumers MUST
  reject `application/zip` and accept only tar.gz.
- **[ADR-0008](0008-version-immutability.md)** — `MS-API §3.3`/§9.2 requires
  `409` on any republish; `req-rg-001` also permits accepting a byte-identical
  republish idempotently.
