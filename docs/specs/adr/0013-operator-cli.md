# ADR-0013 — Operator CLI as the administrative surface

**Status** Accepted
**Date** 2026-08-05
**Relates to** [FR-42](../02-requirements.md#fr-42)–[FR-45](../02-requirements.md#fr-45), [UC-17](../01-use-cases.md#uc-17-run-and-operate-the-server), [UC-21](../01-use-cases.md#uc-21-delete-a-version-break-glass)

## Context

The project brief calls for "a CLI server and a web interface". Two readings are
possible, and they lead to different work:

**(a)** a server that is *operated* via a CLI, or
**(b)** a separate admin CLI *plus* a server.

These converge in practice — the operator-side commands are the same either way —
so this ADR covers both and the distinction costs nothing. What it must **not**
be confused with is the `apm` CLI: that is the *client*, it is external, it
already exists, and we do not build it ([README, Scope note](../README.md#scope-note)).

Three needs push toward a CLI rather than web-only administration:

1. **Bootstrapping.** A fresh deployment has no users and no tokens. Something
   must create the first operator without being authenticated.
2. **Break-glass operations.** Version deletion
   ([UC-21](../01-use-cases.md#uc-21-delete-a-version-break-glass)) breaks frozen
   installs by design. It should require shell access to the host, not a
   misclick in a browser.
3. **Verification and maintenance.** `verify` and `gc` are long-running batch
   jobs that belong in a terminal or a cron job, not an HTTP request.

## Decision

**Ship `apm-registry`: a single binary that both runs the server and administers
it, operating on the metadata and blob stores directly through the same services
as the HTTP layer.**

```bash
apm-registry serve   [--port 3000]
apm-registry migrate

apm-registry repo create <name> [--public|--private] [--quota <bytes>]
apm-registry repo list
apm-registry repo delete <name> --confirm

apm-registry token create --name <n> --scope <s> [--scope <s>...] [--expires <dur>]
apm-registry token list
apm-registry token revoke <id>

apm-registry version delete <repo> <identity> <version> --confirm
apm-registry gc      [--dry-run]        # default: --dry-run
apm-registry verify                     # re-hash every blob vs recorded digest
```

Design rules:

1. **Same services as HTTP.** The CLI is an inbound adapter over
   `RepositoryService`, `TokenService`, `PublishService`, `QueryService` — never
   its own logic and never HTTP calls to itself. Same rationale as the web UI
   ([ADR-0010](0010-web-ui.md)).
2. **Direct store access, no running server needed.** `migrate`, `gc`, `verify`
   and bootstrap work on a stopped registry.
3. **Destructive commands require `--confirm`**, and `gc` defaults to
   `--dry-run`. Deletion is loudly audited.
4. **`version delete` is CLI-only** and never exposed over HTTP
   ([04-api-contract §8](../04-api-contract.md#8-reserved-surface)). Requiring
   host access is the access control.
5. **`verify` exists to make `req-rg-001` provable.** It re-reads every blob and
   compares to recorded metadata, exiting non-zero on mismatch
   ([FR-44](../02-requirements.md#fr-44)) — the invariant becomes something we
   check on a schedule rather than assume.
6. **`migrate` is idempotent** and safe to run on every boot
   ([FR-43](../02-requirements.md#fr-43)); `serve` runs it automatically unless
   `--no-migrate`.
7. **`serve` fails fast on invalid config** rather than starting degraded
   ([TR-30](../02-requirements.md#tr-30)).
8. Machine-readable output via `--json` on every read command, so the CLI is
   usable from scripts and health checks.

## Consequences

**Positive**
- A registry can be bootstrapped from empty with no chicken-and-egg auth problem.
- Break-glass deletion requires host access, which is a meaningfully higher bar
  than a browser session.
- Batch jobs (`verify`, `gc`) fit naturally into cron and CI without an HTTP
  surface that would need its own authentication and timeout story.
- One binary, one deployable, one dependency tree.

**Negative**
- CLI administration needs filesystem access to the data directory, so it must
  run on the host or in a container sharing the volume — not from an
  administrator's laptop. That is a deliberate constraint, but it has to be
  documented for operators.
- Some operations exist in two places (repositories and tokens are also in the
  web UI). Contained by both being thin adapters over one service; the risk is
  duplicated *presentation*, not duplicated *rules*.

**Neutral**
- Resolves the "cli server" ambiguity by covering both readings. If the intent
  was only (a), the extra subcommands are still the operations an operator needs.

## Alternatives considered

**Web-UI-only administration.** Rejected: no bootstrap path for the first
operator, and it puts break-glass deletion one misclick away in a browser.

**Separate `apm-registry-admin` binary.** Rejected: two artefacts to build,
version and ship, with a shared dependency on the same internals — all cost, no
isolation benefit.

**Administration through the HTTP API with an admin scope.** Partly adopted —
repositories and tokens *are* manageable through the web UI, which uses the same
services. Rejected for `serve`, `migrate`, `gc`, `verify` and `version delete`:
those either precede the server's existence or should require host access.
