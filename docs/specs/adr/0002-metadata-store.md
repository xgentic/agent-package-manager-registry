# ADR-0002 — Metadata store: SQLite behind a port

**Status** Accepted
**Date** 2026-08-05
**Relates to** [TR-06](../02-requirements.md#tr-06), [TR-07](../02-requirements.md#tr-07), [TR-08](../02-requirements.md#tr-08), [TR-31](../02-requirements.md#tr-31)

## Context

Metadata — repositories, packages, versions, tokens, primitives, audit — needs a
transactional store. Two requirements dominate the choice:

1. **[TR-08](../02-requirements.md#tr-08)** — version uniqueness must be enforced
   by a **database constraint**, not application logic. A read-then-write check
   races, and with content-addressed blobs the losing write is a silent no-op,
   so the race is invisible ([ADR-0008](0008-version-immutability.md)).
2. **[TR-07](../02-requirements.md#tr-07)** — publish must be atomic across the
   version row, the primitive inventory rows and the audit row.

Both are ordinary relational-database features. Expected scale is modest:
thousands of packages, tens of thousands of versions, low write rate (publishes
are human- or CI-paced), read-dominated. The heavy data — archive bytes — is not
in this store at all ([ADR-0003](0003-content-addressed-storage.md)).

## Decision

**SQLite via `modernc.org/sqlite` on stdlib `database/sql`, with WAL, accessed
exclusively through a `MetadataStore` port.**

- **`modernc.org/sqlite` (pure Go, cgo-free)** rather than `mattn/go-sqlite3`.
  This keeps `CGO_ENABLED=0`, which is what preserves the static single binary
  and trivial cross-compilation that [ADR-0001](0001-runtime-go-net-http.md)
  depends on. The cgo driver is faster, but a registry at this write rate is not
  driver-bound, and losing static linking would cost more than the speed gains.
- WAL mode so readers do not block the writer.
- `FOREIGN KEYS` on; `busy_timeout` set.
- A **single writer connection** (`db.SetMaxOpenConns(1)` on the write path, or a
  dedicated write handle). SQLite permits one writer; letting `database/sql`
  open an unbounded pool against it converts contention into `SQLITE_BUSY`
  errors under concurrent publishes.
- `version` column uses **case-sensitive (`BINARY`) collation** — selectors are
  opaque and case-sensitive ([FR-17](../02-requirements.md#fr-17)), so a
  case-insensitive default would both merge distinct versions and spuriously
  trip the unique constraint.
- Schema migrations are numbered, forward-only, and applied by
  `apm-registry migrate`, which is idempotent and safe on every boot
  ([FR-43](../02-requirements.md#fr-43)).
- **No SQL outside `internal/store/sqlite/`.** Services depend on the interface.

The port exists specifically so Postgres can be added without touching domain or
service code — the file is written now, the adapter later.

## Consequences

**Positive**
- Zero operational surface: no database server, no connection pooling, no
  separate backup path. A registry is a container plus two volumes
  ([03-architecture §11](../03-architecture.md#11-deployment)).
- Real transactions and real unique constraints — everything
  [TR-07](../02-requirements.md#tr-07) and [TR-08](../02-requirements.md#tr-08)
  need.
- Backup and restore is a file copy, and the metadata store is small because
  blobs live elsewhere.
- Tests run against an in-memory database, so the suite is fast and hermetic.

**Negative — stated plainly**
- **Single-instance only.** SQLite on a single file with one writer means the
  registry cannot be horizontally scaled as designed
  ([TR-31](../02-requirements.md#tr-31)). This is a real limit, accepted for v1,
  not hand-waved: the mitigation is the port, and the trigger for exercising it
  is any requirement for multi-instance deployment or HA.
- Network filesystems (NFS, some container volume drivers) have unreliable
  SQLite locking. The deployment must use a local volume — an operational
  constraint that has to be documented, not just known.
- Concurrent-writer behaviour differs from Postgres, so the Postgres adapter
  will need its own tests rather than inheriting confidence from these.

## Alternatives considered

**Postgres from the start.** Rejected for v1: it imposes a database server,
migrations tooling, connection management and backup strategy on a system whose
expected write rate is a handful of publishes per hour. Kept as the designed-for
upgrade path rather than the default.

**JSON/YAML files on disk as metadata.** Rejected: no transactions and no unique
constraints, so [TR-08](../02-requirements.md#tr-08) would fall back to
application-level checking — exactly the race
[ADR-0008](0008-version-immutability.md) rules out.

**Metadata derived from the blob store at read time** (scan directories, parse
manifests). Rejected: read cost grows with package count, tokens and audit have
no natural home, and there is no way to enforce uniqueness atomically.
