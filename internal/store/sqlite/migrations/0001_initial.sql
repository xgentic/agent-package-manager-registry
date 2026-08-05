-- Initial schema: repositories, packages, blobs, versions.
-- Data model reference: docs/specs/03-architecture.md §5.

CREATE TABLE repositories (
    id          TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    visibility  TEXT    NOT NULL DEFAULT 'private',
    quota_bytes INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT    NOT NULL
);

CREATE TABLE packages (
    id            TEXT NOT NULL PRIMARY KEY,
    repository_id TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    -- Stored decoded and lowercased: the canonical form both path shapes
    -- resolve to (FR-16).
    identity      TEXT NOT NULL COLLATE BINARY,
    owner         TEXT NOT NULL,
    repo          TEXT NOT NULL,
    description   TEXT,
    created_at    TEXT NOT NULL,
    -- Identity is unique only *within* a repository; repositories are fully
    -- independent namespaces (ADR-0009).
    UNIQUE (repository_id, identity)
);

CREATE TABLE blobs (
    digest     TEXT    PRIMARY KEY,
    size_bytes INTEGER NOT NULL,
    created_at TEXT    NOT NULL
);

CREATE TABLE versions (
    id         TEXT NOT NULL PRIMARY KEY,
    package_id TEXT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    -- COLLATE BINARY is load-bearing, not decoration: selectors are opaque and
    -- case-sensitive (FR-17), so a case-insensitive collation would merge
    -- `V1.0` with `v1.0` and reject a legitimate publish as a conflict.
    version    TEXT NOT NULL COLLATE BINARY,
    digest     TEXT NOT NULL REFERENCES blobs(digest),
    size_bytes INTEGER NOT NULL,
    media_type TEXT NOT NULL,
    -- RFC 3339, UTC, fixed width, so lexicographic order is chronological.
    published_at TEXT NOT NULL,

    -- Reserved. Populated from MVP 3 (tokens) and MVP 2 (manifest metadata);
    -- present now so those milestones are enforcement, not schema churn.
    published_by_token_id TEXT,
    manifest_json         TEXT,
    -- Reserved for yank, which lands with OpenAPM v0.2 (FR-52). Never set in v1.
    yanked_at             TEXT,

    -- THIS CONSTRAINT IS THE IMMUTABILITY GUARANTEE (TR-08, req-rg-001).
    -- Conflict is detected here, never by a prior SELECT, so two concurrent
    -- publishes of the same version cannot both win.
    UNIQUE (package_id, version)
);

CREATE INDEX versions_by_publish_time ON versions (package_id, published_at DESC);
CREATE INDEX packages_by_repository ON packages (repository_id);
