# Traceability

Mapping of requirements to internal design decisions (ADRs) and implementation coverage.

---

## 1. Requirements → design decisions

| Requirement ID | Covered by |
|---|---|
| [FR-01](02-requirements.md#fr-01)–[FR-13](02-requirements.md#fr-13) | Core API endpoints |
| [FR-14](02-requirements.md#fr-14)–[FR-19](02-requirements.md#fr-19) | Identity and versioning ([ADR-0007](adr/0007-package-identity.md)) |
| [FR-20](02-requirements.md#fr-20)–[FR-25](02-requirements.md#fr-25) | Authentication and authorization ([ADR-0005](adr/0005-token-auth.md)) |
| [FR-26](02-requirements.md#fr-26)–[FR-28](02-requirements.md#fr-28) | Error handling ([ADR-0006](adr/0006-rfc7807-errors.md)) |
| [FR-29](02-requirements.md#fr-29)–[FR-32](02-requirements.md#fr-32) | Repositories and storage ([ADR-0009](adr/0009-multi-repository-namespacing.md), [ADR-0003](adr/0003-content-addressed-storage.md)) |
| [FR-33](02-requirements.md#fr-33)–[FR-50](02-requirements.md#fr-50) | Web UI and operations |
| [FR-51](02-requirements.md#fr-51) | Search extension ([ADR-0012](adr/0012-search-vendor-extension.md)) |
| [TR-01](02-requirements.md#tr-01)–[TR-31](02-requirements.md#tr-31) | Technical requirements (data integrity, security, scalability) |

---

## 2. Design decisions (ADRs)

| ADR | Requirement coverage |
|---|---|
| [ADR-0001](adr/0001-runtime-go-net-http.md) | Go + stdlib choice |
| [ADR-0002](adr/0002-metadata-store.md) | SQLite metadata storage |
| [ADR-0003](adr/0003-content-addressed-storage.md) | Content-addressed blob storage ([FR-07](02-requirements.md#fr-07), [TR-04](02-requirements.md#tr-04)) |
| [ADR-0004](adr/0004-archive-format.md) | Support both zip and tar.gz ([FR-09](02-requirements.md#fr-09)) |
| [ADR-0005](adr/0005-token-auth.md) | Bearer token authentication |
| [ADR-0006](adr/0006-rfc7807-errors.md) | RFC 7807 problem responses |
| [ADR-0007](adr/0007-package-identity.md) | Package identity path forms ([FR-15](02-requirements.md#fr-15)) |
| [ADR-0008](adr/0008-version-immutability.md) | Version immutability enforcement ([FR-11](02-requirements.md#fr-11), [TR-08](02-requirements.md#tr-08)) |
| [ADR-0009](adr/0009-multi-repository-namespacing.md) | Multiple repositories with namespacing |
| [ADR-0010](adr/0010-web-ui.md) | Web UI design |
| [ADR-0011](adr/0011-publish-pipeline.md) | Publish validation pipeline |
| [ADR-0012](adr/0012-search-vendor-extension.md) | Search endpoint as vendor extension |
| [ADR-0013](adr/0013-operator-cli.md) | Operator CLI for server management |

---

## 3. Key invariants

These four requirements are non-negotiable and traced through the codebase:

| ID | Statement | Implementation |
|---|---|---|
| [FR-07](02-requirements.md#fr-07) | Served bytes must hash to advertised digest; never mutate | [ADR-0003](adr/0003-content-addressed-storage.md), [ADR-0008](adr/0008-version-immutability.md) |
| [FR-11](02-requirements.md#fr-11) | Version immutability enforced by database constraint | [ADR-0008](adr/0008-version-immutability.md) |
| [FR-15](02-requirements.md#fr-15) | Traversal checks run on decoded package identity | [ADR-0007](adr/0007-package-identity.md) |
| [FR-26](02-requirements.md#fr-26) | All 4xx/5xx responses are RFC 7807 problem bodies | [ADR-0006](adr/0006-rfc7807-errors.md) |

---

## 4. Open questions

None blocks implementation.

| # | Question | Current assumption |
|---|---|---|
| Q-1 | Should `apm.yml.version` equal the URL version? | Yes, default on, config-flaggable |
| Q-2 | How exactly does `apm.yml.name` "match" the URL identity? | Accept either full identity or bare repo name |
| Q-3 | Problem `type` URI namespace | Use `docs.apm.dev/errors/...`; need not resolve |
| Q-4 | Federated repository support? | No — local-only design for v1 |
| Q-5 | Require at least one primitive in `.apm/`? | No — accept empty packages |
