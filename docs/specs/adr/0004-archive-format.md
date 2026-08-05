# ADR-0004 — Accept both ZIP and tar.gz on publish

**Status** Accepted
**Date** 2026-08-05
**Relates to** [FR-09](../02-requirements.md#fr-09), [FR-07](../02-requirements.md#fr-07), [UC-01](../01-use-cases.md#uc-01-publish-a-package-version)

## Context

The `apm` client sends `.zip` by default, but some legacy clients or manual
uploads may use `.tar.gz`. Servers should store and replay whatever was uploaded
without format conversion, since transcoding changes bytes and therefore digests.

## Decision

**Accept both `application/zip` and `application/gzip` on publish. Store the
media type. Replay it verbatim on download. Never transcode.**

1. `Content-Type` is authoritative; anything else → `415`.
2. The declared type is *verified* by parsing — a body that does not parse as the
   declared container is `400`, not a silent sniff-and-accept.
3. The stored media type is replayed on `GET /download`. No conversion in either
   direction — transcoding would change the bytes and therefore the digest.
4. Format policy lives in exactly one module, `internal/domain/archive/mediatype.go`, so
   a v0.2 restriction is a one-file change plus a config flag.
5. A config flag `APM_REGISTRY_ACCEPTED_MEDIA_TYPES` lets an operator narrow to
   `application/gzip` alone if their organisation wants to pre-comply with the
   v0.2 direction.

## Consequences

**Positive**
- The `apm` CLI works out of the box — zip is what it sends by default.
- Both zip and tar.gz archives are handled uniformly without conversion.

**Negative**
- Two container parsers to write, harden and fuzz. ZIP is the more hostile
  format (central directory vs local headers — see [TR-12](../02-requirements.md#tr-12)),
  and we take that cost deliberately.
- If OpenAPM v0.2 makes tar.gz-only normative for registries, zip-published
  versions become unconsumable by strict v0.2 clients. We cannot fix that by
  re-packing — that would change digests and break every lockfile. Mitigation is
  the config flag plus an operator advisory, not a migration.

**Risk accepted** This is the decision most likely to be invalidated by OpenAPM
v0.2. It is isolated to one module precisely because we expect to revisit it.

## Alternatives considered

**tar.gz only.** Rejected: it breaks the default `apm publish` invocation.

**Zip only.** Rejected: would break legacy clients unnecessarily.

**Accept both, normalise to one on ingest.** Rejected outright — it violates
`req-rg-001`. Re-packing changes the bytes, changes the digest, and breaks every
consumer lockfile that recorded the original. Non-negotiable.
