# Glossary — ubiquitous language

Terms are used with exactly these meanings throughout the specs, the code and
the API. Where a term is defined upstream, the upstream source is cited.

## Core domain

**APM package** — A versioned unit of AI-agent configuration. On disk: a
directory containing an `apm.yml` manifest and a `.apm/` source tree.
(`MS-SPEC §8.1`, package-anatomy.)

**Primitive** — A typed unit of agent configuration inside a package. OpenAPM
v0.1 recognises **seven** types: `instructions`, `prompts`, `agents`, `skills`,
`commands`, `hooks`, `mcp`. (`MS-SPEC §8.1`.)

> Note: the `.apm/` directory layout additionally shows a `context/` folder in
> the authoring docs; it is a source convention, not a distinct primitive type in
> the v0.1 type system. The registry does not need to distinguish it — see
> [FR-31](02-requirements.md#fr-31).

**Manifest** (`apm.yml`) — The package descriptor. Required fields: `name`,
`version`. `version` MUST match the semver 2.0 pattern. (`MS-SPEC §4.1`,
`manifest-v0.1.schema.json`.)

**Lockfile** (`apm.lock.yaml`) — Consumer-side resolution record. Not stored or
produced by the registry, but its `resolved_url` / `resolved_hash` fields are
what make the registry a trust anchor. (`MS-SPEC §5`.)

**Archive** — The published artefact: a flat ZIP (or tar.gz) whose **root**
contains `apm.yml`, `.apm/`, and optionally `README.md`, `CHANGELOG.md`,
`LICENSE`/`LICENCE`. Symlinks excluded. (`MS-GUIDE`, `apm publish` reference.)

> Distinct from the **plugin bundle** layout produced by `apm pack`, which nests
> everything under `{name}-{version}/` with a `plugin.json`. The registry
> accepts the flat layout only.

**Digest** — `sha256:<64 lowercase hex>` over the exact archive bytes. The
identity of the artefact and the trust anchor for consumers. (`MS-API §3.1`.)

## Identity and addressing

**Package identity** — `{owner}/{repo}` for GitHub-origin packages. For
non-GitHub origins the whole identity (including host) is percent-encoded into a
**single** path segment. (`MS-API §1.2`.)

| Origin | Identity | Path segment(s) |
|---|---|---|
| GitHub | `acme/web-skills` | `acme/web-skills` (two segments) |
| GitLab | `gitlab.com/acme/web-skills` | `gitlab.com%2Facme%2Fweb-skills` (one segment) |
| Azure DevOps | `dev.azure.com/org/proj/repo` | `dev.azure.com%2Forg%2Fproj%2Frepo` (one segment) |

**Version selector** — An **opaque, case-sensitive** string. Semver strings
enable client-side range matching (`^1.2.3`); non-semver strings (`stable`,
`main`, `v1.4.2`) are matched exactly. The registry never parses semver — range
resolution is entirely client-side. (`MS-API §1.3`.)

**Repository** (registry sense) — A named namespace within one registry server
that holds packages, has its own visibility and access rules, and appears in the
base URL. (See [ADR-0009](adr/0009-multi-repository-namespacing.md).)

> ⚠️ **Overloaded term.** `repo` in `{owner}/{repo}` means the *source-control*
> repository name and is part of the package identity. A **registry repository**
> is the namespace/tenant. In code we use `Repository` for the namespace and
> `PackageIdentity.repo` for the identity component. Never abbreviate the
> namespace to `repo` in an API field name.

**Base URL** — Vendor-defined prefix, no trailing slash, to which clients append
`/v1/...`. Ours: `https://<host>/api/agentpackages/<repository>`.

## Security and access

**Bearer token** — Opaque string in `Authorization: Bearer <token>`. The client
never parses it. Stored one-way hashed. (`MS-API §2.1`, §8.)

**Scope** — Server-enforced authority attached to a token. Clients never see
scope strings. (`MS-API §2.2`.)

| Scope | Grants |
|---|---|
| `read` | `GET /versions`, `GET /download` (coarse) |
| `read:{owner}/{repo}` | Same, package-scoped |
| `publish:{owner}/{repo}` | `PUT .../versions/{version}` |
| `publish:{owner}/*` | Publish to all repos under an owner |

**Immutability** — A successfully published `(repository, identity, version)`
tuple can never have its bytes changed. This is the invariant the consumer
lockfile trust model depends on. (`req-rg-001`, `MS-API §1.6`.)

**Zip slip / symlink escape** — Archive attack where an entry path contains
`..`, is absolute, or is a link, causing extraction outside the root. The
registry rejects such archives at publish time. (`MS-SPEC §10.9`, `req-sc-002`.)

**Problem Details** — [RFC 7807](https://www.rfc-editor.org/rfc/rfc7807)
`application/problem+json` error body. The only error envelope this server
emits. Required fields: `title`, `status`. Vendor data goes under `extensions.*`.
(`MS-API §4`.)

## Actors

**Producer** — Publishes packages. Runs `apm publish` or uploads via the web UI.

**Consumer** — Installs packages. Runs `apm install`; the `apm` CLI calls
`/versions` and `/download`.

**Operator** — Runs the registry: deployment, repositories, tokens, quotas,
audit. Uses the `apm-registry` CLI and the web UI admin area.

**Governance owner** — Sets org policy (`.github/apm-policy.yml`) that forces
dependencies through this registry. Consumes the registry's audit surface but
does not administer it. (`MS-SPEC §6`.)

## Out-of-scope terms (defined so we can say "not this")

**Yank / withdrawal** — Marking a version unselectable. Explicitly **reserved
for v0.2** upstream (`MS-SPEC §7.9`); `410 Gone` is reserved in `MS-API §3.2`.
See [FR-24](02-requirements.md#fr-24).

**Attestation / provenance** — in-toto / SLSA publisher binding. Reserved for
OpenAPM v0.2 (`MS-SPEC §10.12`). Not implemented.

**Package signing** — Cryptographic signing of archives. `MS-GUIDE` lists it
explicitly under "not yet provided". Not implemented.

**Virtual / remote / smart repository** — Repository proxying and federation concepts.
Out of scope for v1; local-only design.

**Registry proxy** — A *different* mechanism (`PROXY_REGISTRY_URL`) that fronts
an upstream **Git host**. Orthogonal to a dedicated registry. Not this project.
