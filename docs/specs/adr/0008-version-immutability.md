# ADR-0008 — Version immutability: 409 over idempotent republish

**Status** Accepted
**Date** 2026-08-05
**Relates to** [FR-11](../02-requirements.md#fr-11), [TR-08](../02-requirements.md#tr-08), [UC-02](../01-use-cases.md#uc-02-reject-a-duplicate-version), [UC-21](../01-use-cases.md#uc-21-delete-a-version-break-glass)

## Context

Immutability is the single normative `Registry`-class requirement in OpenAPM
v0.1, and the whole consumer trust model rests on it: lockfiles record
`resolved_url` + `resolved_hash`, and clients re-fetch and re-verify on every
frozen install.

The two sources agree on the invariant but differ on the **response to a
repeat publish**.

**`MS-API §1.6`, §3.3 — always 409:**

> Versions are immutable. A successful `PUT .../versions/{version}` cannot be
> overwritten — subsequent PUTs return `409 Conflict`. This is a hard
> requirement.

Conformance fixture §9.2 nails it down:

> 1. `PUT .../versions/1.0.0` → `201`.
> 2. `PUT .../versions/1.0.0` (**same body**) → `409`.
> 3. `PUT .../versions/1.0.0` (**different body**) → `409`.

**`req-rg-001` — 409 *or* idempotent republish:**

> When a Registry receives a publish request for an `(name, version)` it has
> previously served, the Registry MUST either (a) reject the publish request
> with a diagnostic identifying the existing version, or **(b) accept it ONLY if
> the submitted archive bytes are byte-identical** to the previously-served
> bytes (idempotent republish). A Registry MUST NOT replace the bytes of a
> previously-served `(name, version)` under any circumstance.

Option (b) is superficially attractive: a CI job that retries after a network
timeout would get `201` instead of a confusing `409`.

## Decision

**Always return `409` on a repeat publish, regardless of whether the bytes are
identical.** We take option (a).

1. `409` for identical bytes and for different bytes alike.
2. The `409` body is RFC 7807 with `extensions.previous_publish` and
   `extensions.previous_digest`, so a client can tell "my retry already
   succeeded" (digests match) from "someone else published this version"
   (digests differ). That recovers the ergonomic benefit of option (b) without
   the conformance cost.
3. Conflict is detected by a **database unique constraint** on
   `(package_id, version)`, not a read-then-write check — see below.
4. Uniqueness is checked against `versions` **and** `version_tombstones`, so a
   break-glass deletion ([UC-21](../01-use-cases.md#uc-21-delete-a-version-break-glass))
   cannot reopen a tuple for different bytes.

### Why the DB constraint rather than an application check

A `SELECT` followed by an `INSERT` is a race: two concurrent publishes of the
same version can both see "not present" and both proceed. With
content-addressed blob storage the second write is a silent no-op at the blob
layer, so the failure would be invisible — two producers would each believe they
published, and one would be wrong about which bytes are live.

The unique constraint makes the database the arbiter. Exactly one transaction
commits; the other gets a constraint violation which maps to `409`. The
invariant is enforced by the storage engine, not by our control flow.

## Consequences

**Positive**
- Passes `MS-API` fixture §9.2 exactly, including the same-body case that option
  (b) would fail.
- One code path, no byte-comparison of a freshly uploaded archive against stored
  bytes — which for a 50 MB archive means no second full read.
- The invariant survives concurrency by construction.
- `409` is unambiguous to a human: the version exists, bump it. Option (b) can
  mask a genuine mistake — a producer who rebuilt identical bytes by accident
  gets a success they did not earn.

**Negative**
- A CI job whose publish succeeded but whose response was lost sees `409` on
  retry and must treat it as success. Mitigated by `extensions.previous_digest`:
  matching digest means the retry is a no-op. This must be called out in the
  operator docs, because it is the one place where the decision costs someone
  something.
- We forgo a permitted convenience. Accepted: `MS-API` is the contract the real
  client is written against, and `req-rg-001` permits (a) unconditionally.

**Neutral**
- Both options satisfy `req-rg-001`. This decision cannot make us
  non-conformant to OpenAPM; it only decides which conformant behaviour we pick.

## Alternatives considered

**Idempotent republish (option b).** Rejected: fails `MS-API` fixture §9.2 step
2, which requires `409` for an identical body. Conformance against the contract
the real client speaks outranks retry ergonomics — especially when
`extensions.previous_digest` recovers most of the ergonomics anyway.

**Read-then-write conflict check.** Rejected as racy; see above.

**`PUT` overwrite with versioned history.** Rejected outright. It violates the
core invariant: *"A Registry MUST NOT replace the bytes of a previously-served
`(name, version)` under any circumstance."* Every consumer lockfile that
recorded the old digest would fail closed on the next frozen install.
