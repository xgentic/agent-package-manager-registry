# Usage

How to run the server and drive it from the CLI. Everything else is in
[README.md](../README.md).

> ⚠️ This build has **no authentication** — every endpoint is open. Run it on
> localhost or behind a trusted network only.

## 1. Build

```sh
go build .          # produces ./apm-registry
```

## 2. Create a repository

A fresh install has no repositories, and there is no HTTP route that creates
one — bootstrapping needs host access. This also creates `./data` and the schema:

```sh
./apm-registry repo create local --public
# created repository "local" (public)
# base URL: http://localhost:3000/api/agentpackages/local
```

## 3. Serve

```sh
./apm-registry serve            # or: make run
curl localhost:3000/ready       # {"status":"ready"}
```

## 4. Point a client at it

Enable the client's registry feature once, then add the base URL from step 2 to
`apm.yml`:

```sh
apm experimental enable registries
```

```yaml
registries:
  local:
    url: http://localhost:3000/api/agentpackages/local
```

Then `apm publish` and `apm install` work against it.

## Commands

```sh
apm-registry serve [--port 3000] [--no-migrate]   # default when no command given
apm-registry migrate                              # apply migrations, then exit
apm-registry repo create <name> [--public|--private] [--quota <bytes>] [--json]
apm-registry repo list [--json]
apm-registry help
```

`serve` migrates on boot unless `--no-migrate`. `repo` subcommands migrate
before writing, so they work on a stopped registry.

## Configuration

Environment variables only, validated at startup — invalid values stop the
process rather than starting it degraded. See [`.env.example`](../.env.example)
for every variable and its default. The ones you are most likely to set:

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `3000` | Listen port (`--port` overrides). |
| `APM_REGISTRY_BASE_URL` | `http://localhost:3000` | Public origin, no trailing slash. |
| `APM_REGISTRY_DATA_DIR` | `./data` | Database, blobs and upload temp files. Must be a local volume. |
| `APM_REGISTRY_ALLOW_INSECURE_HTTP` | `true` | Set `false` in production to refuse plain HTTP. |
| `APM_REGISTRY_MAX_ARCHIVE_BYTES` | `52428800` | Compressed upload cap; exceeding it aborts with `413`. |

## Endpoints

```
GET  /api/agentpackages/{repository}/v1/packages/{owner}/{repo}/versions
GET  /api/agentpackages/{repository}/v1/packages/{owner}/{repo}/versions/{version}/download
PUT  /api/agentpackages/{repository}/v1/packages/{owner}/{repo}/versions/{version}
GET  /health
GET  /ready
```

Errors are always RFC 7807 `application/problem+json`. Wire shapes are documented in the specs.
