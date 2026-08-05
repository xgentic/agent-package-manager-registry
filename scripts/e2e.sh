#!/usr/bin/env bash
#
# End-to-end round trip against the **real** `apm` client (TR-26, roadmap T6.6).
#
# The conformance suite asserts our reading of the contract. This script asserts
# the client's reading of it, which is the one that decides whether a publish is
# usable — the client is external and unpatchable, so a disagreement is our bug
# by definition.
#
#   build → repo create → serve → apm publish → apm install → verify the lockfile
#
# Deliberately outside `make check` and CI: it needs the apm binary, and coupling
# the build to another tool's release cadence would make our gate flaky for
# reasons that are not about us.
#
# Usage: make test-e2e

set -euo pipefail

readonly REPOSITORY="local"
readonly PACKAGE="acme/demo"
readonly VERSION="1.0.0"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workdir="$(mktemp -d)"
server_pid=""

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31mFAIL:\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
  local status=$?
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if [[ $status -ne 0 && -f "$workdir/server.log" ]]; then
    echo "--- server log ---" >&2
    cat "$workdir/server.log" >&2
  fi
  rm -rf "$workdir"
  exit $status
}
trap cleanup EXIT

command -v apm >/dev/null 2>&1 || fail "the apm client is not on PATH; see https://microsoft.github.io/apm"

log "apm client: $(apm --version)"

# ---------------------------------------------------------------------------
# Build and bootstrap. A fresh install has no repository: publishing into one
# that does not exist is a 404 until an operator creates it (ADR-0013).
# ---------------------------------------------------------------------------

log "building apm-registry"
( cd "$repo_root" && CGO_ENABLED=0 go build -o "$workdir/apm-registry" . )

export APM_REGISTRY_DATA_DIR="$workdir/data"

log "creating repository '$REPOSITORY'"
"$workdir/apm-registry" repo create "$REPOSITORY" --public >/dev/null

# An ephemeral port, so a developer's own registry on 3000 is left alone.
port="$(
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"
readonly base_url="http://localhost:${port}/api/agentpackages/${REPOSITORY}"

log "starting the registry on port $port"
PORT="$port" "$workdir/apm-registry" serve >"$workdir/server.log" 2>&1 &
server_pid=$!

for _ in $(seq 1 50); do
  if curl -fsS "http://localhost:${port}/ready" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
curl -fsS "http://localhost:${port}/ready" >/dev/null || fail "the registry did not become ready"

# Registries are still an experimental client feature; publish refuses without
# the flag.
log "enabling the client's 'registries' feature"
apm experimental enable registries >/dev/null 2>&1 || true

# ---------------------------------------------------------------------------
# Publish
# ---------------------------------------------------------------------------

producer="$workdir/producer"
mkdir -p "$producer/.apm/skills"
cat >"$producer/apm.yml" <<EOF
# The manifest declares the bare repo name while the URL identity is
# acme/demo. Validation rule 6 accepts both, and this is the shape the client
# actually produces — see risk R-9.
name: demo
version: ${VERSION}
description: End-to-end round-trip package
license: MIT
registries:
  ${REPOSITORY}:
    url: ${base_url}
dependencies:
  apm: []
  mcp: []
EOF
echo "# probe skill" >"$producer/.apm/skills/probe.md"

log "apm publish --package $PACKAGE"
publish_output="$(cd "$producer" && apm publish --package "$PACKAGE" --registry "$REPOSITORY" 2>&1)" \
  || { echo "$publish_output" >&2; fail "apm publish exited non-zero"; }
echo "$publish_output"

digest="$(printf '%s' "$publish_output" | tr -d '\n ' | sed -n 's/.*digest:\(sha256:[0-9a-f]\{64\}\).*/\1/p')"
[[ -n "$digest" ]] || fail "apm publish printed no digest"
log "published digest: $digest"

# ---------------------------------------------------------------------------
# Install into a fresh project
# ---------------------------------------------------------------------------

consumer="$workdir/consumer"
mkdir -p "$consumer"
cat >"$consumer/apm.yml" <<EOF
name: consumer
version: 0.1.0
targets:
  - claude
registries:
  ${REPOSITORY}:
    url: ${base_url}
dependencies:
  apm:
    - id: ${PACKAGE}
      version: ${VERSION}
      registry: ${REPOSITORY}
  mcp: []
EOF

log "apm install ${PACKAGE}#${VERSION}"
install_output="$(cd "$consumer" && apm install 2>&1)" \
  || { echo "$install_output" >&2; fail "apm install exited non-zero"; }
echo "$install_output"

# ---------------------------------------------------------------------------
# Verify what the client ended up with
# ---------------------------------------------------------------------------

[[ -f "$consumer/apm_modules/${PACKAGE}/apm.yml" ]] \
  || fail "the package was not extracted into apm_modules/"
[[ -f "$consumer/apm_modules/${PACKAGE}/.apm/skills/probe.md" ]] \
  || fail "the package's primitives were not installed"

lockfile="$consumer/apm.lock.yaml"
[[ -f "$lockfile" ]] || fail "no apm.lock.yaml was written"

# req-rg-001: the lockfile records a permanent resolved_url and the digest we
# advertised. If either drifts, every frozen reinstall of this package breaks.
grep -q "resolved_url: ${base_url}/v1/packages/${PACKAGE}/versions/${VERSION}/download" "$lockfile" \
  || fail "the lockfile's resolved_url does not point at this registry:
$(grep resolved_url "$lockfile" || true)"
grep -q "resolved_hash: ${digest}" "$lockfile" \
  || fail "the lockfile's resolved_hash does not match the published digest:
$(grep resolved_hash "$lockfile" || true)"

log "lockfile records:"
grep -E 'resolved_url|resolved_hash|source' "$lockfile" | sed 's/^/    /'

printf '\n\033[1;32mPASS\033[0m  publish and install both round-tripped through the real apm client.\n'
