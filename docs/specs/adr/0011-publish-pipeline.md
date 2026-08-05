# ADR-0011 — Streaming validate-then-commit publish pipeline

**Status** Accepted
**Date** 2026-08-05
**Relates to** [FR-10](../02-requirements.md#fr-10), [FR-13](../02-requirements.md#fr-13), [TR-07](../02-requirements.md#tr-07), [TR-09](../02-requirements.md#tr-09)–[TR-15](../02-requirements.md#tr-15)

## Context

Publish is the only endpoint that ingests attacker-controlled bytes at scale, and
it has to satisfy several requirements that pull against each other:

- Validate eight rules before persistence (`MS-API §6`) — *"Validate per §6
  before persistence; reject zip slip / symlink attacks"*.
- Compute SHA-256 of the exact bytes for the `201` response.
- Enforce a size cap with `413` (`MS-API §3.3`).
- Reject archives exceeding consumer-side caps: 100 MB uncompressed, 10,000
  entries (`req-sc-004`).
- Never buffer whole archives in memory.
- Leave nothing behind on failure ([FR-13](../02-requirements.md#fr-13)).
- Be atomic across version, primitive-inventory and audit rows
  ([TR-07](../02-requirements.md#tr-07)).

The tension: hashing needs every byte, validation needs random access into the
archive (the ZIP central directory is at the *end*), and neither should hold the
archive in memory.

## Decision

**Stream to a temp file while hashing, inspect from the temp file, then commit
blob-before-metadata.**

```
1. authorise            check publish scope        → 403 before reading a byte
2. media type           zip | gzip                 → 415 before reading a byte
3. stream               body → temp file,
                        SHA-256 incrementally,
                        abort the moment the cap trips  → 413
4. inspect              read entry table from temp file (no extraction)
                        ├─ zip:   central directory
                        └─ gzip:  tar entry headers
5. entry rules          absolute / '..' / symlink / hardlink / count / expansion → 422
6. manifest             apm.yml at root, valid YAML, name + version,
                        name matches identity, version matches URL          → 422
7. inventory            scan .apm/ for primitives
8. blob                 BlobStore.put(digest, tempFile)   (create-if-absent)
9. metadata             BEGIN
                          INSERT version   (UNIQUE package_id, version) → 409
                          INSERT primitives
                          INSERT audit
                        COMMIT
10. cleanup             remove temp file (on every path, including failure)
```

### Why this order

- **Steps 1–2 precede any body read.** A caller without publish scope should not
  be able to make us ingest 50 MB. Cheap checks first is a denial-of-service
  concern, not a style preference.
- **Hash during the stream (step 3)**, so the archive is read once and never
  fully resident ([TR-09](../02-requirements.md#tr-09)).
- **Cap enforced *during* streaming**, aborting mid-body rather than after a full
  read ([TR-10](../02-requirements.md#tr-10)).
- **Temp file, not memory.** ZIP's central directory lives at the end of the
  file, so validation needs random access. A temp file gives that with bounded
  memory; buffering would give it with unbounded memory.
- **All of steps 5–6 run before persistence** ([FR-13](../02-requirements.md#fr-13)),
  and none of them short-circuit: `422` reports every failure so a producer fixes
  everything in one round-trip ([FR-10](../02-requirements.md#fr-10)).
- **Blob before metadata (steps 8–9).** A blob with no row is invisible garbage
  that `gc` reclaims. A row with no blob is a `404` on a version the client
  believes exists — and a broken lockfile. The asymmetry decides the order.
- **Conflict comes from the unique constraint**, not a prior `SELECT`
  ([ADR-0008](0008-version-immutability.md)). Concurrent publishes of the same
  version cannot both win.

### Hardening details

- **ZIP entries come from the central directory**, the authoritative table
  ([TR-12](../02-requirements.md#tr-12)). Trusting local file headers lets a
  crafted archive show one entry list to the validator and another to an
  extractor.
- **Expansion is bounded while decompressing**, not from declared sizes — a
  declared size is attacker-controlled. Abort on breach
  ([TR-13](../02-requirements.md#tr-13)); this is the zip-bomb defence.
- **Consumer-side caps enforced at publish** ([TR-14](../02-requirements.md#tr-14)):
  we refuse to accept an archive no conformant consumer could extract.
- **Temp files are swept on every exit path** — success, validation failure,
  client disconnect — plus a startup sweep for files orphaned by a crash
  ([TR-15](../02-requirements.md#tr-15)).

### Shared with the web UI

`inspect()` is steps 1–7; `publish()` is 1–10. The web pre-flight report calls
`inspect()`, so it is guaranteed to predict the publish outcome
([ADR-0010](0010-web-ui.md)).

## Consequences

**Positive**
- Memory is bounded by the temp-file buffer, not archive size, so a 50 MB
  publish and a 1 MB publish cost the same in RAM.
- Single pass over the body for hashing.
- Rejected publishes leave no trace.
- Atomicity comes from a real transaction plus a unique constraint, not from
  careful ordering of application checks.
- The failure asymmetry (garbage blob vs dangling row) is decided explicitly
  rather than by accident.

**Negative**
- Requires writable temp space sized for concurrent uploads: roughly
  `max_archive_bytes × max_concurrent_publishes`. That is a deployment
  requirement, and it must be documented, not discovered.
- Disk I/O for archives that would have fit in memory. Accepted: uniform
  behaviour beats a size-dependent fast path, and publishes are infrequent.
- Two archive parsers to harden ([ADR-0004](0004-archive-format.md)).

## Alternatives considered

**Buffer in memory and validate there.** Simpler, and fine at 1 MB. Rejected:
memory scales with archive size × concurrency, which turns the 50 MB cap into a
denial-of-service knob.

**Persist first, validate asynchronously.** Rejected: `MS-API §6` requires
validation before persistence, and it would mean briefly serving unvalidated
archives — precisely the zip-slip payloads the rules exist to stop.

**Extract to a directory to validate.** Rejected: extraction is the operation
zip-slip attacks, so validating by extracting means executing the attack to
detect it. We read the entry table and never extract.

**Metadata before blob.** Rejected: it makes the bad failure mode the likely one
— a listable version whose bytes are missing breaks frozen installs, whereas an
orphan blob is silent and reclaimable.
