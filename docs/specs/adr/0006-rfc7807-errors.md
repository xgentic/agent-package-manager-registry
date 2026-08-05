# ADR-0006 — RFC 7807 as the sole error envelope

**Status** Accepted
**Date** 2026-08-05
**Relates to** [FR-26](../02-requirements.md#fr-26)–[FR-28](../02-requirements.md#fr-28), [TR-29](../02-requirements.md#tr-29)

## Context

`MS-API §4` mandates RFC 7807 Problem Details on **all** `4xx` and `5xx`
responses, with `application/problem+json`, required `title` and `status`, and
vendor data under `extensions.*`. Conformance fixture §9.6 asserts it:

> 1. Any `4xx` response has `Content-Type: application/problem+json`.
> 2. Body is valid JSON with at least `title` and `status`.

"All" is the hard part. Runtime-generated errors — `ServeMux`'s plain-text 404
on an unmatched route, a 405, a 413 from `http.MaxBytesReader`, a 500 from a
recovered panic — do not naturally take our shape. `net/http`'s defaults are
plain text, and `http.Error` is the path of least resistance everywhere in Go,
so drift is the default outcome unless it is designed against.

`internal/server` already registers a catch-all `"/"` handler for exactly this
reason, so unrouted requests get a problem body instead of `net/http`'s
`404 page not found`.

The web UI complicates it further: the same failures must be renderable as
field-level form errors, not just a status line.

## Decision

**One typed error taxonomy in the domain, rendered at the edge through a single
`writeProblem`.**

### Taxonomy

`internal/domain/errors.go` defines the closed set, each carrying its status and
the data its Problem body needs. Errors are matched with `errors.Is` / `errors.As`,
so wrapping with `fmt.Errorf("%w", ...)` preserves the mapping:

| Error | Status | Extensions |
|---|---|---|
| `ErrNotFound` | 404 | — |
| `ErrUnauthenticated` | 401 | `remediation` (the env var to set) |
| `ErrScopeDenied` | 403 | `required_scope` |
| `ErrVersionConflict` | 409 | `previous_publish`, `previous_digest` |
| `ErrArchiveInvalid` | 422 | `errors[]` (all failures, never just the first) |
| `ErrPayloadTooLarge` | 413 | `limit_bytes` |
| `ErrUnsupportedMediaType` | 415 | `accepted[]` |
| `ErrMalformedArchive` | 400 | `detail` from the parser |
| `ErrRateLimited` | 429 | `limit`, `remaining` (+ `Retry-After` header) |

### One renderer

`writeProblem(w, status, title, detail, ext)` in `internal/server` is the only
function that writes an error body — it already exists and already marshals
before touching the `ResponseWriter`, so an encoding failure becomes a 500
rather than a truncated 200.

`/v1` and `/ui/api` share it. The React client renders field-level messages from
`extensions.errors[]` rather than the server producing a second error shape
([ADR-0014](0014-react-spa.md)). One taxonomy, one envelope, two presentations.

### Catch-alls

Three, so nothing escapes:

1. **A `recover()` middleware** maps a panic to a generic `500` Problem — logging
   the real cause with the request id but **never** putting it in the body
   ([FR-28](../02-requirements.md#fr-28)). Without it, a panic yields `net/http`'s
   empty connection close, which is not a Problem body at all.
2. **The catch-all `"/"` route** returns a Problem `404`, since `ServeMux`'s
   default is plain text. Already implemented in `internal/server`.
3. **A response-shape assertion in the conformance suite** checks every non-2xx
   across every route, so a new handler cannot quietly return a bare JSON error
   or reach for `http.Error` ([TR-22](../02-requirements.md#tr-22)).

### Fields we always populate

`type` (stable documentation URL per error class), `title`, `status`,
`detail`, `instance` (the request path), and
`extensions.request_id` — the correlation id from
[TR-29](../02-requirements.md#tr-29), which is what makes a user-reported error
findable in the logs.

## Consequences

**Positive**
- Fixture §9.6 passes by construction rather than by inspection.
- `422` bodies list **every** validation failure in `extensions.errors[]`, so a
  producer fixes everything in one round-trip instead of discovering the eight
  rules one publish at a time ([FR-10](../02-requirements.md#fr-10)).
- `extensions.request_id` connects a user's screenshot to a log line.
- Because the taxonomy is closed and lives in the domain, adding an error forces
  a decision about its status and extensions at definition time.

**Negative**
- Every failure path must return a domain error rather than a bare
  `errors.New`, and every handler must call `writeProblem` rather than
  `http.Error`. That is a discipline the codebase has to hold, and Go makes the
  wrong thing easy — `http.Error` is one import away. The conformance assertion
  is the enforcement mechanism; a lint rule banning `http.Error` outside
  `problem.go` would be better.
- RFC 7807 is verbose for trivial errors. Irrelevant — errors are not the hot
  path.

**Neutral**
- `type` URIs currently point at `docs.apm.dev/errors/...`, matching the
  examples in `MS-API §4`. If that namespace is not ours to use, they become
  URIs under our own documentation host; the spec requires only that `type` be a
  URI, not that it resolve.

## Alternatives considered

**`http.Error` and ad-hoc JSON shapes** (`{"error":"not_found"}`). Rejected:
non-conformant, fails fixture §9.6, and gives clients nothing structured to
branch on. `http.Error` additionally writes `text/plain`, which fails the
content-type half of §9.6 outright.

**Problem Details for the API, arbitrary errors internally.** Rejected: it puts
the mapping at every return site instead of one edge, which is how the "all 4xx"
requirement gets quietly broken by the next new handler.

**A distinct error shape for `/ui/api`.** Rejected: two taxonomies drift, and the
React client is perfectly able to render `extensions.errors[]` into field-level
messages. The shared thing is the *taxonomy and the envelope*; only the
presentation differs.
