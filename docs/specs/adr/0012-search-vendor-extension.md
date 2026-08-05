# ADR-0012 — Search as a labelled vendor extension

**Status** Accepted
**Date** 2026-08-05
**Relates to** [FR-51](../02-requirements.md#fr-51), [FR-19](../02-requirements.md#fr-19), [FR-40](../02-requirements.md#fr-40), [UC-13](../01-use-cases.md#uc-13-browse-and-search-the-registry), [UC-20](../01-use-cases.md#uc-20-search-via-the-api-vendor-extension)

## Context

Search is not part of the core API contract, yet the web UI needs it
([UC-13](../01-use-cases.md#uc-13-browse-and-search-the-registry)) and it is
useful for clients to discover packages.

So the question is not *whether* to build search, but how to expose it without
implying it is part of the standard contract — and without letting it
contaminate the endpoints that are.

## Decision

**Implement `GET /v1/search` as an optional vendor extension.**

1. It is documented in a separate **"Vendor extensions"** section of
   [04-api-contract.md](../04-api-contract.md#7-vendor-extensions), never in the
   core API contract.
2. Our response schema is defined here and versioned with our documentation:

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

3. **It may paginate** (`limit`, `offset`) — precisely because no conformant
   client depends on it. This is the sharp contrast with `/versions`, which
   **must not** paginate: a truncated version list silently corrupts client-side
   semver resolution ([FR-19](../02-requirements.md#fr-19)). Recording the
   contrast here is half the point of this ADR.
4. Results are filtered by the caller's read scope, and unreadable packages are
   omitted rather than denied ([FR-40](../02-requirements.md#fr-40)).
5. It is **excluded from the conformance suite**, so a conformance test failure
   always reflects the core API contract.
6. It is backed by the same `QueryService` the web UI uses — search is one
   implementation with two presentations, not two search engines.

## Consequences

**Positive**
- The web UI's search is a thin adapter over an already-needed service.
- The extension boundary is explicit, so it is clear what is standard and what is
  optional.
- Pagination freedom where it is safe (search), and pagination prohibition where
  it is not (versions), are documented as a pair.

**Negative**
- Search clients written against this implementation may break if the wire contract
  changes. Mitigated by `/v2/` versioning being a first-class routing concern
  ([FR-54](../02-requirements.md#fr-54)).

**Neutral**
- Marked `Could` priority ([FR-51](../02-requirements.md#fr-51)). If it slips,
  the web UI still needs the underlying service, so the cost of deferring the
  *endpoint* is near zero.

## Alternatives considered

**No search endpoint; UI-internal search only.** Cleanest from a conformance
standpoint. Rejected: the implementation cost is small and clients benefit from
search.

**Search under a clearly non-standard prefix** (`/x/search`, `/api/ui/search`).
More honest, but reduces discoverability.

**Adopt an external standard response shape.** Risky — would require reverse-engineering
an unpublished implementation.
