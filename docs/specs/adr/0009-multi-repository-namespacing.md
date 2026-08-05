# ADR-0009 — Multiple named repositories in the base URL

**Status** Accepted
**Date** 2026-08-05
**Relates to** [FR-29](../02-requirements.md#fr-29), [FR-30](../02-requirements.md#fr-30), [FR-32](../02-requirements.md#fr-32), [UC-16](../01-use-cases.md#uc-16-manage-repositories)

## Context

`MS-API §1.1` leaves the base URL vendor-defined:

> The registry's **base URL** is vendor-defined — for example,
> `https://registry.example.com/apm`. Clients always append paths starting with
> `/v1/...` to it. The base URL MUST NOT contain a trailing slash.

The repository name can be part of the base URL for multi-tenancy, and the client
is configured per-repository:

```bash
apm config set registry.corp-main.url https://.../api/agentpackages/corp-main-local
```

The client already treats a registry as a *named, independently-credentialled
source*: `registries:` in `apm.yml`, `APM_REGISTRY_TOKEN_{NAME}` per name, and
policy that can require dependencies to route through a specific one. Multiple
named sources is the model the client is built around.

The realistic use for it is separation with different rules: `internal` (private,
restricted publish) versus `mirror` (public read) versus `sandbox` (permissive).

## Decision

**Support multiple named repositories, each with the repository name in its base
URL:**

```
https://<host>/api/agentpackages/<repository>          ← base URL, no trailing slash
https://<host>/api/agentpackages/<repository>/v1/...   ← what clients request
```

1. Repository names match `^[a-z0-9][a-z0-9._-]*$` — the character class the APM
   client already allows for registry names (lowercase, digits, `-`, `.`).
2. Repositories are **fully independent namespaces**. The same identity may exist
   in two repositories with different versions and different bytes.
3. Visibility (`public` | `private`) is per repository and drives anonymous read
   ([FR-24](../02-requirements.md#fr-24)).
4. Quotas are per repository ([FR-48](../02-requirements.md#fr-48)).
5. The path prefix is `/api/agentpackages/<name>`.
6. **Only local repositories.** Remote, virtual and smart repository types and
   replication are explicitly unsupported for Agent Packages upstream, and are
   out of scope here ([FR-32](../02-requirements.md#fr-32)).

Routing-wise the repository is resolved by a prefix middleware into the request
context before `/v1/` routes run, so every downstream query is repository-scoped
by construction — there is no code path that can query across repositories by
accident.

## Consequences

**Positive**
- One deployment serves several isolated registries with distinct access rules —
  the common enterprise need, without running several servers.
- Matches the client's existing mental model and configuration surface exactly.
- Repository-scoped URL structure is clear and predictable.
- Repository-scoped context makes cross-tenant leakage a structural
  impossibility rather than a review item.

**Negative**
- Every query carries a repository dimension; forgetting it in a new query is a
  cross-tenant bug. Mitigated by resolving the repository into request context
  and having the `MetadataStore` port take it as a required parameter — the type
  system refuses the unscoped call.
- Identity is only unique *within* a repository, so any UI or log line naming a
  package must also name its repository or become ambiguous.

**Neutral**
- Single-repository deployments simply configure one repository. The abstraction
  costs a path segment.

## Alternatives considered

**Single implicit registry, base URL = origin.** Simpler, and adequate for one
team. Rejected: no way to separate public from private without running multiple
servers.

**Repository as a query parameter** (`?repo=corp-main`). Rejected: clients append
`/v1/...` to a base URL, so the discriminator must be a path prefix. A query
parameter cannot survive that concatenation.

**Repository as a subdomain** (`corp-main.registry.example.com`). Rejected:
requires wildcard DNS and wildcard TLS, which is real operational burden for a
self-hosted deployment.
