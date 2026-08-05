# apm-registry — Specifications

This folder is the requirements baseline for **apm-registry**: a self-hosted
package registry implemented in Go on the standard library's `net/http`
(see [ADR-0001](adr/0001-runtime-go-net-http.md)), exposing:

1. the **Registry HTTP API** consumed by the `apm` CLI,
2. a **web interface** for uploading, browsing and managing artefacts,
3. an **operator CLI** for running and administering the server.

## Reading order

| Doc | Purpose |
|---|---|
| [00-glossary.md](00-glossary.md) | Ubiquitous language. Read first — every other doc uses these terms precisely. |
| [01-use-cases.md](01-use-cases.md) | Actors and use cases (`UC-xx`), with main and alternate flows. |
| [02-requirements.md](02-requirements.md) | Functional (`FR-xx`) and technical/non-functional (`TR-xx`) requirements. |
| [03-architecture.md](03-architecture.md) | Component, data and deployment architecture; request flows; module layout. |
| [04-api-contract.md](04-api-contract.md) | The normative wire contract this server implements, incl. vendor extensions. |
| [05-traceability.md](05-traceability.md) | Every requirement mapped back to its upstream source, and upstream spec IDs mapped forward. |
| [adr/](adr/) | Architecture Decision Records (`ADR-0001`…). |

## Sources

Requirements came from the contract with the `apm` client and the digest/immutability
specification. `05-traceability.md` records which requirement came from where.

## Scope note

The user-facing goal is a registry server plus a web interface for uploading and
managing artefacts. The `apm` CLI is an **external client** we must satisfy — we
do not build it. The CLI we *do* build (`apm-registry`) is the operator-side
tool: run the server, run migrations, mint tokens, manage repositories. See
[ADR-0013](adr/0013-operator-cli.md).

## Conventions

- **MUST / SHOULD / MAY** are used per RFC 2119 convention.
- Our own IDs are stable: `UC-xx`, `FR-xx`, `TR-xx`, `ADR-xxxx`. Retired IDs are
  never reused.
