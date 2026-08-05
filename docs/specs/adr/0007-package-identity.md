# ADR-0007 — Dual-form package identity routing

**Status** Accepted
**Date** 2026-08-05
**Relates to** [FR-14](../02-requirements.md#fr-14), [FR-15](../02-requirements.md#fr-15), [FR-16](../02-requirements.md#fr-16), [UC-11](../01-use-cases.md#uc-11-publish-a-non-github-origin-package)

## Context

`MS-API §1.2` requires one resource to be addressable through **two different
path shapes**:

| Origin | Identity | Path segment(s) |
|---|---|---|
| GitHub | `acme/web-skills` | `acme/web-skills` — **two** segments |
| GitLab | `gitlab.com/acme/web-skills` | `gitlab.com%2Facme%2Fweb-skills` — **one** encoded segment |
| Azure DevOps | `dev.azure.com/org/proj/repo` | `dev.azure.com%2Forg%2Fproj%2Frepo` — **one** encoded segment |

> Servers MUST decode percent-encoded path segments before lookup.

A single route pattern cannot express "either two segments or one encoded
segment". Worse, whether a router *can* even see the difference depends on
whether the runtime normalises `%2F` before matching — many frameworks decode
the path first, at which point `gitlab.com%2Facme%2Fweb-skills` becomes
indistinguishable from three real segments and the distinction is lost
irrecoverably.

The `apm publish` CLI reference adds a normalisation rule: *"Owner and repository
are normalized to lowercase."*

## Decision

**Register two routes per endpoint, both funnelling into one handler through a
shared identity resolver. Decode first, then validate, then canonicalise.**

```
GET /v1/packages/{owner}/{repo}/versions   →  identity = owner + "/" + repo
GET /v1/packages/{identity}/versions       →  identity = decoded {identity}
```

The two-segment pattern is more specific, so `ServeMux` prefers it — a plain
GitHub identity never falls through to the single-segment handler.

### Verified runtime behaviour

Measured on **Go 1.25.4**, `net/http.ServeMux` with Go 1.22+ method-and-wildcard
patterns, before committing to this design:

| Request path | Matched route | `r.PathValue(...)` | `r.URL.Path` (decoded) |
|---|---|---|---|
| `/v1/packages/acme/web-skills/versions` | `{owner}/{repo}` | `acme`, `web-skills` | 4 segments |
| `/v1/packages/gitlab.com%2Facme%2Fweb-skills/versions` | `{identity}` | `gitlab.com/acme/web-skills` | **6 segments** |
| `/v1/packages/dev.azure.com%2Forg%2Fproj%2Frepo/versions` | `{identity}` | `dev.azure.com/org/proj/repo` | 7 segments |
| `/v1/packages/acme%2F..%2Fevil/versions` | `{identity}` → **200** | `acme/../evil` | traversal visible |

Three properties, all confirmed rather than assumed:

1. **`ServeMux` matches against the *escaped* path.** `%2F` is not treated as a
   separator during matching, so the encoded identity matches as a **single**
   wildcard segment. `r.URL.RawPath` retains the original encoding whenever it
   differs from `r.URL.Path`.
2. **`r.PathValue()` returns the *decoded* value**, so the handler receives
   `gitlab.com/acme/web-skills` directly.
3. **`r.URL.Path` is already decoded** and would show six segments for the
   GitLab case. Anything that routes or validates from `r.URL.Path` is wrong.

### Security rule: validate *after* decoding

Properties 2 and 3 are convenient and dangerous. Row 4 above is the proof:
`acme%2F..%2Fevil` arrives as one opaque segment, matches the single-segment
route, returns **200**, and `PathValue` hands the handler `acme/../evil`.
**Traversal checks must therefore run on the decoded output**; validating the
raw segment would pass the attempt straight through. `MS-API §8` requires
exactly this:

> Reject `..` segments in `{owner}` and `{repo}` path params before any storage
> lookup.

Order in `resolveIdentity()` is therefore fixed and non-negotiable:

```
raw segment(s)  →  decode  →  validate (no '..', no absolute, no control chars,
                              no empty components)  →  lowercase canonicalise
                              →  lookup
```

### Canonical storage form

Identities are stored **decoded and lowercased**. Consequences:
`acme/web-skills`, `acme%2Fweb-skills` and `Acme/Web-Skills` all resolve to the
same package, and `package` in responses echoes the canonical form.

Version selectors are explicitly **not** part of this normalisation — they stay
opaque and case-sensitive ([FR-17](../02-requirements.md#fr-17)).

## Consequences

**Positive**
- Both addressing forms work, as the spec requires, with one handler and one
  validation path.
- Traversal is blocked at exactly one chokepoint, on the decoded value.
- Behaviour is pinned by tests derived from a measurement, not from an
  assumption about framework internals.

**Negative**
- Every package endpoint registers twice — three endpoints become six routes.
  Contained by a helper (`internal/httpapi/identity.go`) that registers both
  forms from a single declaration.
- We depend on `ServeMux` matching the escaped path and on `PathValue` decoding.
  Both are stable Go 1.22+ behaviour covered by the Go 1 compatibility promise,
  but a conformance test asserts both forms resolve identically and that
  `acme%2F..%2Fevil` is rejected — so a regression fails loudly rather than
  silently routing to `404` or, worse, silently accepting a traversal.
- `r.URL.Path` is a live footgun: it is decoded, so it reads as six segments for
  a percent-encoded identity. Handlers must use `PathValue`. This is worth a
  lint rule or a review checklist item, not just a comment.

## Alternatives considered

**Wildcard route with manual splitting** (`/v1/packages/*`). Rejected: it hands
us a partially-decoded path and forces us to re-implement segment parsing, which
is where traversal bugs live. It also loses the framework's own guarantees about
what a "segment" is.

**Query-parameter identity** (`?package=acme/web-skills`). Rejected: not the
specified contract. The real client builds path URLs.

**Reject percent-encoded identities; support GitHub only.** Rejected: `MS-API
§1.2` requires decoding, and GitLab / Azure DevOps origins are first-class in
the identity table. It would also make us fail any conformance suite that
exercises non-GitHub identity.
