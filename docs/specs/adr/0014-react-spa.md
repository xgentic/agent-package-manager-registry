# ADR-0014 — React SPA embedded in the Go binary

**Status** Accepted
**Date** 2026-08-05
**Supersedes** [ADR-0010](0010-web-ui.md) — Server-rendered web UI over the same domain services
**Relates to** [FR-33](../02-requirements.md#fr-33)–[FR-41](../02-requirements.md#fr-41), [TR-19](../02-requirements.md#tr-19)–[TR-21](../02-requirements.md#tr-21), [UC-12](../01-use-cases.md#uc-12-upload-an-artefact-through-the-web-ui)

## Context

[ADR-0010](0010-web-ui.md) chose a server-rendered UI using Hono's JSX renderer.
Two things invalidated it:

1. **The runtime moved to Go** ([ADR-0001](0001-runtime-go-net-http.md)). Hono's
   JSX renderer no longer exists in the stack. Go's `html/template` is the
   nearest equivalent, but it is a materially worse authoring experience for an
   interactive upload flow than what ADR-0010 assumed.
2. **React was chosen explicitly** as a project requirement for the web UI
   milestone.

What must survive the change is ADR-0010's actual load-bearing argument, which
had nothing to do with the rendering technology:

> If the web upload path re-implements publish, it will eventually disagree with
> the API about what a valid package is — a version publishable through one door
> and not the other. That divergence would be invisible in the conformance
> suite, which only exercises the API.

That risk is unchanged by React, and is the reason [FR-34](../02-requirements.md#fr-34)
exists. A naive SPA makes it *worse*, because the obvious implementation is to
give the browser its own upload endpoint with its own validation.

## Decision

**React + TypeScript, built by Vite, embedded into the Go binary with
`embed.FS`, served by the same process, talking to a `/ui/api` surface that is a
thin adapter over the same services as the registry API.**

### The constraint that carries over from ADR-0010

**`/ui/api` handlers are adapters, exactly like `/v1` handlers.** They call
`PublishService`, `QueryService`, `TokenService`, `RepositoryService` — never
their own logic, and never the registry API over loopback. The only difference
between the two front doors is presentation.

This is what preserves the guarantee: there is one publish implementation, so
the pre-flight report the browser shows is *by construction* a prediction of
what `PUT /v1/...` would do.

```
POST /ui/api/repos/{repo}/preflight   → PublishService.Inspect()   (validate + inventory, no commit)
POST /ui/api/repos/{repo}/publish     → PublishService.Publish()   (identical path to PUT /v1/...)
```

`Inspect()` is `Publish()` minus the commit step
([ADR-0011](0011-publish-pipeline.md)).

### Why a separate `/ui/api` rather than consuming `/v1`

The registry API is a **fixed external contract** shaped for the `apm` CLI. It
has no "list all packages", no search, no archive-entry listing, and it is
strictly `snake_case` because the CLI silently ignores anything else
([FR-04](../02-requirements.md#fr-04)).

Making the browser a first-class consumer of `/v1` would either bend that
contract toward UI needs or force the UI into awkward call patterns. Keeping
`/ui/api` separate means:

- `/v1` stays exactly what `MS-API` specifies — the conformance suite tests one
  surface with one set of rules.
- `/ui/api` is explicitly **internal and unversioned**; it may change freely with
  the UI, and is documented as not-the-registry-contract.
- Errors still use RFC 7807 ([ADR-0006](0006-rfc7807-errors.md)) so there is one
  error taxonomy, not two.

### Build and packaging

- `web/` holds React + TypeScript sources, built by Vite to `web/dist`.
- `internal/webui` embeds `web/dist` with `//go:embed`, serving assets and an
  SPA fallback to `index.html` for client-side routes.
- `CGO_ENABLED=0 go build` therefore produces **one static binary containing the
  registry API, the web UI and the operator CLI**. No separate static host, no
  second deployable, no CDN dependency.
- Dev loop: Vite dev server with HMR proxying `/ui/api` to the Go server.

### Security posture

The threats are the same as ADR-0010's; the mitigations move to the client
boundary.

- **Untrusted content stays server-sanitised.** Package file names, `apm.yml`
  fields and `SKILL.md` bodies are attacker-controlled
  ([TR-20](../02-requirements.md#tr-20)). Markdown is sanitised **server-side**
  before it reaches the browser — not with `dangerouslySetInnerHTML` on
  client-sanitised input.
- React escapes interpolated values by default, which is the same property that
  made JSX attractive in ADR-0010.
- CSP with no `unsafe-inline` ([TR-19](../02-requirements.md#tr-19)). Vite must
  therefore emit external assets with no inline bootstrap script; verify this in
  the built output, since it is easy to regress silently.
- Archive downloads keep `Content-Disposition: attachment` and a non-renderable
  content type so archive bytes never execute in the UI's origin
  ([TR-21](../02-requirements.md#tr-21)).
- Session auth stays a signed `HttpOnly` `Secure` `SameSite=Lax` cookie with CSRF
  on state-changing requests ([FR-41](../02-requirements.md#fr-41)). **`HttpOnly`
  means the SPA cannot read the session token — which is the point.** The UI must
  not fall back to storing an API bearer token in `localStorage`; that would turn
  any XSS into a publish credential.
- Listings stay scope-filtered, with "not permitted" indistinguishable from "not
  found" ([FR-40](../02-requirements.md#fr-40)).

## Consequences

**Positive**
- Still one publish implementation, so the divergence risk ADR-0010 existed to
  prevent remains closed.
- Genuinely better UX for the flows that need it: drag-and-drop upload, upload
  **progress** on large archives, and an interactive pre-flight report. Upload
  progress was an explicit negative in ADR-0010; here it is free.
- Still one deployable artefact — arguably a better story than ADR-0010's, since
  the Go binary embeds the UI rather than rendering it.
- The UI can evolve without touching the conformance-tested `/v1` surface.

**Negative**
- **A second toolchain.** Node + Vite are now required to build, which the Go-only
  story avoided. Mitigation: `web/dist` build is a distinct `make` target, and CI
  builds it before `go build`; contributors touching only the server do not need
  Node if a prebuilt `dist` is present.
- **A second API surface to keep honest.** `/ui/api` could drift into duplicating
  registry logic. The rule above is the guard, and it needs review enforcement —
  any `/ui/api` handler containing validation logic is a bug.
- **No-JS operation is lost.** ADR-0010 delivered a UI that worked without
  JavaScript, which had some value in locked-down enterprise browsers. Accepted
  as a real cost of the React requirement.
- Client/server type drift is possible. Mitigated by generating or hand-mirroring
  `/ui/api` types in `web/src/api/` so a shape change breaks the frontend build.

## Alternatives considered

**Go `html/template` server rendering.** The direct Go translation of ADR-0010.
Keeps the single toolchain and no-JS operation. Rejected: React was an explicit
requirement, and the upload flow — drag-drop, progress, interactive pre-flight —
is the part of this product that most benefits from a client-side framework.

**React consuming `/v1` directly.** Rejected: it would push UI-shaped concerns
(search, listing, pagination) into a fixed external contract, or force the UI to
work without them. Keeping `/v1` exactly as specified is worth one internal API.

**Separate frontend deployment (static host / CDN).** Rejected: two deployables,
CORS, and a versioning problem between UI and server for no benefit at this
scale. `embed.FS` makes co-deployment strictly easier.

**SPA calling the Go server's own HTTP API over loopback.** Rejected for the same
reason ADR-0010 rejected it: re-serialising through HTTP loses the session
principal at the boundary and makes error rendering a translation of a
translation. Share the service layer, not the HTTP layer.
