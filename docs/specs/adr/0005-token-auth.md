# ADR-0005 — Opaque hashed bearer tokens with a scope grammar

**Status** Accepted
**Date** 2026-08-05
**Relates to** [FR-20](../02-requirements.md#fr-20)–[FR-25](../02-requirements.md#fr-25), [TR-17](../02-requirements.md#tr-17), [TR-18](../02-requirements.md#tr-18)

## Context

`MS-API §2` fixes most of this contract, so the decision space is narrower than
it looks:

- Tokens are **opaque** — *"the client treats them as bytes (no parsing, no
  inspection)"*. We therefore cannot require a JWT: the client will not parse it,
  and any structure we add is invisible to it.
- Scopes are **server-side only** — *"Clients never see scope strings"*.
- Basic auth is a first-class alternative and MUST yield identical scope grants.
- `MS-API §8` requires *"a one-way hash (bcrypt/argon2) for stored bearer tokens;
  never store plaintext"* and constant-time comparison.
- `401` when credentials are absent, `403` when present but insufficient, so the
  client can distinguish the two.

The client resolves credentials from `APM_REGISTRY_TOKEN_{NAME}` or
`~/.apm/config.json`, where `{NAME}` is the registry name uppercased with `-`
and `.` mapped to `_`.

## Decision

**Opaque high-entropy random tokens, stored argon2id-hashed, carrying a scope
set. Basic auth resolves to the same principal type.**

### Token format

```
apmr_<base62(32 random bytes)>
```

The `apmr_` prefix is for **humans and secret scanners**, not for us — it makes a
leaked token recognisable in a log or a paste. We do not parse it for
authorisation; the token is looked up as a whole.

### Storage and lookup

- Only `argon2id(secret)` is stored ([TR-17](../02-requirements.md#tr-17)).
  Plaintext is displayed once at creation and never recoverable.
- A short non-secret `token_id` prefix is stored alongside so lookup is an
  indexed fetch of one row followed by one constant-time verify — not a hash
  comparison against every token in the table.
- Comparison is constant-time ([TR-18](../02-requirements.md#tr-18)).
- `last_used_at` is updated on use so stale credentials are findable
  ([FR-38](../02-requirements.md#fr-38)).

### Scope grammar

```
read
read:{owner}/{repo}
publish:{owner}/{repo}
publish:{owner}/*
```

`Scope.satisfies(required, granted)` is a pure domain function, used identically
by API middleware, web routes and the CLI. Wildcards expand only in the `repo`
position — there is no `publish:*/*`; an operator granting broad publish rights
grants it per owner, deliberately.

### Basic auth

`Authorization: Basic <b64(user:pass)>` resolves a user, then loads that user's
effective scopes. The resulting `Principal` is structurally identical to a
bearer principal, satisfying *"MUST treat both forms as semantically
identical for scope evaluation"* ([FR-21](../02-requirements.md#fr-21)).

### Escalation guard

A token may only be granted scopes its creator already holds
([UC-15](../01-use-cases.md#uc-15-manage-api-tokens)). Without this, any
publish-scoped token could mint an operator token.

## Consequences

**Positive**
- Matches `MS-API §2` exactly, including the `401`/`403` distinction that clients
  rely on for remediation messaging.
- Revocation is immediate — a database row, not a signature that stays valid
  until expiry. For a registry where a leaked publish token means supply-chain
  compromise, immediate revocation outweighs statelessness.
- A leaked database gives an attacker argon2id hashes, not usable tokens.
- Prefix makes leaked tokens greppable and scanner-detectable.

**Negative**
- Every authenticated request costs one indexed lookup plus one argon2id verify.
  argon2id is deliberately slow — that is the point for stored secrets, but it is
  a per-request cost on a read-heavy path. Mitigation if it becomes measurable: a
  short-TTL in-process cache keyed by the token id, holding the verified
  principal, with revocation invalidating the entry. Not built in v1; noted so
  the fix is known before the problem is felt.
- Stateful tokens mean the auth path needs the metadata store, so auth failures
  and database outages are correlated. Acceptable — every endpoint needs the
  store anyway.

## Alternatives considered

**JWTs.** Rejected: the client explicitly treats tokens as opaque bytes, so
structure buys nothing on the wire, and stateless validation makes immediate
revocation impossible. For publish credentials, revocation latency is the wrong
thing to trade away.

**Plain `SHA-256(token)` instead of argon2id.** Tempting — tokens are
high-entropy, so brute force is infeasible regardless, and it removes the
per-request cost. Rejected because `MS-API §8` names bcrypt/argon2 explicitly,
and being defensible against that checklist is worth more than the latency.
Revisit only with measurements.

**mTLS.** Rejected: not in the client's credential model, and operationally
heavy for the target deployment.
