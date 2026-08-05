# ADR-0003 — Content-addressed blob storage

**Status** Accepted
**Date** 2026-08-05
**Relates to** [TR-04](../02-requirements.md#tr-04), [TR-05](../02-requirements.md#tr-05), [FR-07](../02-requirements.md#fr-07), [FR-44](../02-requirements.md#fr-44)

## Context

`req-rg-001` — the only normative `Registry`-class requirement in OpenAPM v0.1 —
says the bytes served must hash to the advertised digest and must never change
once served. Consumer lockfiles record `resolved_url` + `resolved_hash` and
re-verify on every frozen install, so a byte-level change is not a degradation;
it is a hard failure across every consumer that ever installed that version.

We already compute SHA-256 of the archive during upload
([TR-09](../02-requirements.md#tr-09)) because the publish response must include
`digest`. So the digest exists before we choose where to put the bytes.

The alternative organising principle — store by
`{repository}/{owner}/{repo}/{version}.zip` — takes
attacker-influenced strings and turns them into filesystem paths.

## Decision

**Store archives content-addressed by their SHA-256. The digest is the key.**

Filesystem layout (fan-out to avoid oversized directories):

```
<data>/blobs/sha256/ab/cd/abcdef0123...   ← full 64-hex digest as filename
```

Rules:

1. **Write is create-if-absent.** Publishing bytes that already exist is a no-op
   at the blob layer; the version row still requires a unique
   `(package_id, version)` ([ADR-0008](0008-version-immutability.md)).
2. **Objects are never mutated.** There is no update path in the port interface —
   `put`, `get`, `stat`, `delete` only.
3. **Deletion is refcount-driven.** A blob is removable only when no live version
   references it, which is what `apm-registry gc` computes
   ([FR-45](../02-requirements.md#fr-45)).
4. **Behind a `BlobStore` port** ([TR-05](../02-requirements.md#tr-05)), with a
   filesystem adapter for v1. An S3-compatible adapter is a drop-in because the
   key is already a flat opaque string.
5. **`apm-registry verify`** re-reads every blob and compares its hash to the
   recorded digest ([FR-44](../02-requirements.md#fr-44)), turning `req-rg-001`
   from an assumption into a checkable property.

## Consequences

**Positive**
- **Immutability is structural.** The key *is* the content hash, so "changing a
  blob" is not an operation that exists. `req-rg-001` cannot be violated by an
  ordinary bug — only by deliberately writing to the wrong path.
- **No attacker-controlled path components.** Package identity and version
  selectors never touch the filesystem. This removes the entire class of
  traversal-via-identity and traversal-via-version bugs at the storage layer.
- **Automatic deduplication.** Republishing identical bytes under a different
  identity — common for forks and re-tags — costs nothing.
- **Corruption is detectable**, because the expected hash is the name.
- **Trivially cacheable and mirrorable** — a mirror may serve the bytes
  provided they still hash correctly.

**Negative**
- The storage layout is not human-browsable. An operator cannot `ls` their way to
  "all versions of `acme/web-skills`"; that question is answered by the metadata
  store or the CLI. Accepted — the CLI is the intended interface.
- Deletion requires refcounting rather than an `rm`. Contained in `gc`.
- Not compatible with path-based storage layouts. Migration between the two means
  copying through the API rather than moving files. Accepted: API-level
  migration preserves digests, which a file move would not verify.

## Alternatives considered

**Path-addressed storage** (`{repo}/{owner}/{name}/{name}-{version}.zip`). Rejected:
it converts three attacker-influenced strings into filesystem paths, and it makes
immutability a convention enforced by application code rather than a property of
the store. Human-browsability is not worth that.

**Blobs as BLOB columns in SQLite.** Rejected: puts multi-megabyte binaries in
the transactional store, bloats the backup path, and makes streaming downloads
awkward. Keeping the metadata store small is what makes a file-copy backup
reasonable.

**Object storage only, no local filesystem adapter.** Rejected for v1: it forces
an external dependency on a system that should run as a single container with a
volume. The port keeps it available later.
