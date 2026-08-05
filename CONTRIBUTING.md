# Contributing

## Start here

| You want to know | Read |
|---|---|
| What we are building and why | [docs/specs/README.md](docs/specs/README.md) |
| What the server must do | [docs/specs/02-requirements.md](docs/specs/02-requirements.md) — every requirement has an ID (`FR-xx`, `TR-xx`) |
| How it is put together | [docs/specs/03-architecture.md](docs/specs/03-architecture.md) |
| The exact wire contract | [docs/specs/04-api-contract.md](docs/specs/04-api-contract.md) |
| Why a decision was made | [docs/specs/adr/](docs/specs/adr/) |
| **What to work on next** | [docs/ROADMAP.md](docs/ROADMAP.md) |

The roadmap is the working document: tasks have IDs and a **Done when** column.
Tick the box and append to the progress log as you finish.

## The gate

```bash
make check      # build vet test — must stay green
make lint       # golangci-lint
```

`make check` is the single command that decides whether the tree is healthy. A
change that reddens it is not finished.

```bash
make run                                   # run the server locally
make test-conformance                      # contract compliance tests, in-process
make test-conformance BASE_URL=https://... # ...against a deployment
make test-e2e                              # round-trip with the real `apm` CLI
```

`make test-e2e` needs the `apm` client installed and its `registries`
experimental feature enabled. It is deliberately outside `make check` and CI.

## Ground rules

These are the ones a reviewer will actually stop on. Each traces to a spec.

1. **`snake_case` on the wire, always.** Every JSON struct field carries an
   explicit `json:"..."` tag. An untagged Go field marshals as `PublishedAt`,
   and the reference client ignores unknown fields *silently* — a casing slip is
   a broken install with no error anywhere ([TR-23](docs/specs/02-requirements.md#tr-23)).
2. **Every error response goes through `writeProblem`.** RFC 7807 is the only
   error envelope this server emits; `http.Error` and bare JSON error objects
   are not acceptable ([FR-26](docs/specs/02-requirements.md#fr-26), [ADR-0006](docs/specs/adr/0006-rfc7807-errors.md)).
3. **Never route or validate from `r.URL.Path`.** It is already percent-decoded.
   Use `r.PathValue()`, and run traversal checks on the decoded value
   ([FR-15](docs/specs/02-requirements.md#fr-15), [ADR-0007](docs/specs/adr/0007-package-identity.md)).
4. **`internal/domain` imports no `net/http` and no `database/sql`.** The
   validation rules must be unit-testable without a server or a database.
5. **Archive input is hostile.** Read entry tables, never extract. Enforce caps
   while expanding, never from declared sizes
   ([TR-11](docs/specs/02-requirements.md#tr-11)–[TR-14](docs/specs/02-requirements.md#tr-14)).
6. **Interfaces are declared in the consuming package**, Go-style. There is no
   `ports` package.

## Tests

Tests live beside the code as `*_test.go`. Two exceptions, both deliberate:

- `internal/conformance/` is its own package so it can run against an in-process
  `httptest.Server` *or* a remote base URL.
- `testdata/` holds malicious archive fixtures; the Go tool ignores `testdata`
  directories, so hostile bytes are never compiled or vetted.

Prefer table-driven tests. Domain code should need no I/O at all; services use
the in-memory fakes in `internal/service`.
