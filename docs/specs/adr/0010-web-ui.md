# ADR-0010 — Server-rendered web UI over the same domain services

**Status** **Superseded by [ADR-0014](0014-react-spa.md)** — the runtime moved to
Go (so Hono's JSX renderer no longer exists in the stack) and React was chosen
explicitly for the web UI. The load-bearing argument below — one publish
implementation, no second validation path — carries over unchanged and is
restated in ADR-0014.
**Date** 2026-08-05
**Relates to** [FR-33](../02-requirements.md#fr-33)–[FR-41](../02-requirements.md#fr-41), [TR-19](../02-requirements.md#tr-19)–[TR-21](../02-requirements.md#tr-21), [UC-12](../01-use-cases.md#uc-12-upload-an-artefact-through-the-web-ui)

## Context

The project requires a web interface to upload and manage artefacts. Nothing
upstream specifies it — this is entirely our design space, which makes the risk
different from the API's: not *"will it conform?"* but *"will it drift?"*

The danger is concrete. If the web upload path re-implements publish, it will
eventually disagree with the API about what a valid package is — a version
publishable through one door and not the other. That divergence would be
invisible in the conformance suite, which only exercises the API.

The UI is also read-mostly and small: browse packages, view versions, upload,
manage tokens and repositories, read the audit log. There is no rich client
state.

## Decision

**Server-rendered HTML using Hono's JSX renderer, calling the same application
services as the API. Progressive enhancement only where it earns its keep.**

### The load-bearing constraint

**Web routes are adapters, exactly like API routes.** They call
`PublishService`, `QueryService`, `TokenService`, `RepositoryService` — never
their own logic, never the HTTP API over the loopback. The only difference
between the two front doors is rendering: `toProblemJson` versus `toFormErrors`
([ADR-0006](0006-rfc7807-errors.md)).

This is [FR-34](../02-requirements.md#fr-34) and it is non-negotiable. It is also
why upload gets a two-step flow:

```
POST /repos/:repo/upload          → PublishService.inspect()  (validate + inventory, no commit)
                                  → render pre-flight report
POST /repos/:repo/upload/confirm  → PublishService.publish()  (identical to API path)
```

`inspect()` is `publish()` minus the commit. One pipeline, two entry points, so
the pre-flight report is guaranteed to predict the publish outcome
([FR-35](../02-requirements.md#fr-35)).

### Session authentication

Distinct from API tokens ([FR-41](../02-requirements.md#fr-41)):
signed session cookie, `HttpOnly`, `Secure`, `SameSite=Lax`, with CSRF tokens on
every state-changing form. A browser session is not a bearer token and must not
be usable as one — otherwise XSS anywhere in the UI becomes a publish
credential.

Both credential kinds resolve to the same `Principal`, so authorisation is the
one `Scope.satisfies` predicate everywhere ([ADR-0005](0005-token-auth.md)).

### Rendering untrusted content

Package content is attacker-controlled: file names, `apm.yml` fields, `SKILL.md`
bodies. Therefore ([TR-20](../02-requirements.md#tr-20)):

- Markdown previews are sanitised and size-capped; raw HTML in Markdown is
  stripped, never passed through.
- All interpolation is escaped by default — JSX gives us this, which is a real
  reason to prefer it over string templates.
- CSP with no `unsafe-inline` ([TR-19](../02-requirements.md#tr-19)); scripts are
  external files with no inline handlers.
- Archive downloads set `Content-Disposition: attachment` and a non-renderable
  content type so archive bytes never execute in the UI's origin
  ([TR-21](../02-requirements.md#tr-21)).

### Scope-filtered listings

Every listing and search result is filtered by the caller's read scope, and
"not permitted" is rendered identically to "not found"
([FR-40](../02-requirements.md#fr-40)). A user must not be able to discover that
a private package exists by probing names.

## Consequences

**Positive**
- **No divergence is structurally possible**, because there is only one publish
  implementation. This is the whole point.
- No separate frontend build, bundler, or dependency tree; one process, one
  deployable.
- JSX escapes by default, which matters more than usual given the input is
  hostile.
- The UI works without JavaScript, so it stays usable in locked-down enterprise
  browsers — plausible for the target audience.

**Negative**
- Upload progress for large archives needs client-side JS to be pleasant. A
  small enhancement layer over a plain `<form>` is planned; without JS the upload
  still works, just without a progress bar.
- Full page loads for navigation. Acceptable for an admin tool at this scale.
- The two-step upload holds a temp file between requests, keyed by digest and
  swept on expiry ([TR-15](../02-requirements.md#tr-15)) — extra state that a
  single-step upload would avoid. Worth it for the pre-flight report.

## Alternatives considered

**SPA (React/Vue) against the public API.** Rejected: it would consume the
registry API from the browser, which means either exposing API tokens to
JavaScript or building a parallel session-authenticated API — the second front
door this ADR exists to prevent. It also adds a build pipeline and a bundle for a
handful of admin screens.

**Separate service for the UI.** Rejected: two deployables, shared database
coupling, and the divergence risk returns in a worse form — now across a network
boundary.

**Web UI calling our own HTTP API over loopback.** Rejected: it re-serialises
everything, loses the session principal at the boundary, and makes error
rendering a translation of a translation. Sharing the service layer is strictly
better than sharing the HTTP layer.
