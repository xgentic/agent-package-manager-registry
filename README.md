# apm-registry

A self-hosted package registry server — clients can `apm install` and
`apm publish` against it to distribute and consume packages.

Written in Go against the standard library.

---

## ⚠️ Alpha — not for production
## ⚠️ This build has no authentication

**Every endpoint is open. Anyone who can reach this server can publish to it.**

Run it on localhost, a VPN, or behind a reverse proxy with an IP
allowlist — never on an untrusted network. Published bytes are still immutable,
so the failure mode is unwanted packages and consumed storage, not corrupted
ones.

---

## Requirements

- Go 1.22+ (developed on 1.25.4)
- An `apm` client with registry support, to exercise the server with a real
  client - python

## Installation

Deployment is one static binary plus a data directory — there is no runtime
dependency to install alongside it.

### Homebrew (macOS and Linux)

```sh
brew install xgentic/tap/apm-registry
```

Homebrew can also run it for you, under launchd on macOS and systemd on Linux:

```sh
brew services start apm-registry     # starts now, and again at login
brew services stop apm-registry
```

The service listens on `127.0.0.1:3000` and keeps its data in
`$(brew --prefix)/var/apm-registry`, with logs beside it in `var/log`. The
loopback address is deliberate: this build has no authentication, and a
background service that binds every interface at login is exactly the mistake
the warning above is about. Run `apm-registry serve` yourself if you want it
reachable from elsewhere.

### macOS and Linux

```sh
curl -fsSL https://raw.githubusercontent.com/xgentic/agent-package-manager-registry/main/install.sh | sh
```

Detects your platform, verifies the download against the release's
`SHA256SUMS`, and installs to `/usr/local/bin` (with `sudo` if that directory
needs it).

### Windows

```powershell
irm https://raw.githubusercontent.com/xgentic/agent-package-manager-registry/main/install.ps1 | iex
```

Installs to `%LOCALAPPDATA%\Programs\apm-registry` and adds it to your user
`PATH`. Under Git Bash or WSL, use the `curl` line above instead.

### Options

Both installers read `VERSION` and `INSTALL_DIR`. Note the variables go on
`sh`, not on `curl` — a prefix on `curl` sets them for the wrong process:

```sh
curl -fsSL …/install.sh | VERSION=v1.0.0 sh
curl -fsSL …/install.sh | INSTALL_DIR="$HOME/.local/bin" sh
```

```powershell
$env:VERSION = 'v1.0.0'; irm …/install.ps1 | iex
```

### Published platforms

| OS | Architectures |
|---|---|
| macOS | `amd64` (Intel), `arm64` (Apple Silicon) |
| Linux | `amd64`, `arm64` |
| Windows | `amd64` (runs under emulation on arm64) |

### From source

```sh
go build .            # unstamped: reports version "dev"
make dist             # every platform above, stripped and stamped, into dist/
```

Verify what you have with `apm-registry version`.

## Running

A fresh install has no repositories. There is no HTTP route that creates one —
that is deliberate, so bootstrapping requires host access ([ADR-0013](docs/specs/adr/0013-operator-cli.md)):

```sh
go build .

./apm-registry repo create local --public   # creates ./data and the schema
./apm-registry serve                        # or: make run

curl localhost:3000/ready
# {"status":"ready"}
```

Point a client at it by adding the repository's base URL to `apm.yml`:

```yaml
registries:
  local:
    url: http://localhost:3000/api/agentpackages/local
```

Deployment is one static binary plus a data directory. Configuration is
environment variables, validated at startup — see [`.env.example`](.env.example)
for every variable and its default.

### Operator commands

```sh
apm-registry serve [--addr 127.0.0.1:3000] [--port 3000] [--no-migrate]
apm-registry migrate
apm-registry repo create <name> [--public|--private] [--quota <bytes>]
apm-registry repo list [--json]
```

## The check loop

```sh
make check              # build + vet + test — keep this green
make lint               # golangci-lint
make test-conformance   # contract suite, in-process
make test-e2e           # round trip with the real apm client
```

`make check` is the single command that verifies the codebase, and it is what
both humans and coding agents should run before considering a change done.

`make test-conformance BASE_URL=http://localhost:3000` runs the same
suite against a deployment.


## What works today

The core three endpoints, in both package-identity path forms:

```
GET  /api/agentpackages/{repository}/v1/packages/{owner}/{repo}/versions
GET  /api/agentpackages/{repository}/v1/packages/{owner}/{repo}/versions/{version}/download
PUT  /api/agentpackages/{repository}/v1/packages/{owner}/{repo}/versions/{version}
GET  /health
GET  /ready
```

plus RFC 7807 problem bodies on every error, sha256 content-addressed storage,
version immutability enforced by a database constraint, and the eight publish
validation rules run against the archive's entry table without ever extracting
it.

## Conventions

The ones a reviewer will stop on are listed in
[CONTRIBUTING.md](CONTRIBUTING.md). The two that bite hardest:

- **`snake_case` on the wire, with explicit `json` tags.** The reference client
  reads only `snake_case` and ignores unknown fields *without erroring*, so an
  untagged Go field is a broken install with no error anywhere.
- **Never route or validate from `r.URL.Path`.** It is already percent-decoded;
  use `r.PathValue()`, and run traversal checks on the decoded value.

## Licence

[MIT](LICENSE).
