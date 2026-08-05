#!/bin/sh
# Install apm-registry from GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/xgentic/agent-package-manager-registry/main/install.sh | sh
#
# Environment:
#   VERSION      release tag to install, or "latest" (default: latest)
#   INSTALL_DIR  destination directory (default: /usr/local/bin)
#
# POSIX sh on purpose: this is documented as `| sh`, and on Debian and macOS
# /bin/sh is not bash. No `echo -e`, no arrays, no [[ ]].

set -eu

REPO="xgentic/agent-package-manager-registry"
BINARY="apm-registry"
VERSION="${VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Colour only when stdout is a terminal. Piped through `sh`, stdin is the
# script, so stdout is still the user's tty; when redirected to a file it is
# not, and escape codes there are noise.
if [ -t 1 ]; then
	red='\033[0;31m'; green='\033[0;32m'; yellow='\033[1;33m'; reset='\033[0m'
else
	red=''; green=''; yellow=''; reset=''
fi

info()  { printf '%s==>%s %s\n' "$yellow" "$reset" "$1"; }
ok()    { printf '%s  ok%s %s\n' "$green" "$reset" "$1"; }
fail()  { printf '%serror%s %s\n' "$red" "$reset" "$1" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"; }
need curl
need uname
need mktemp

# --- platform -----------------------------------------------------------------

ext=''
case "$(uname -s)" in
	Darwin)                 os='darwin'  ;;
	Linux)                  os='linux'   ;;
	MINGW*|MSYS*|CYGWIN*)   os='windows'; ext='.exe' ;;
	*) fail "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
	x86_64|amd64)   arch='amd64' ;;
	arm64|aarch64)  arch='arm64' ;;
	*) fail "unsupported architecture: $(uname -m)" ;;
esac

# One place builds the release asset name. The Makefile and the release
# workflow must produce exactly this; a second concatenation elsewhere is how
# the .exe suffix got appended twice.
asset="${BINARY}-${os}-${arch}${ext}"

if [ "$VERSION" = latest ]; then
	base="https://github.com/${REPO}/releases/latest/download"
else
	base="https://github.com/${REPO}/releases/download/${VERSION}"
fi

# --- download -----------------------------------------------------------------

tmp="$(mktemp -d)"
# Single-quoted so $tmp expands when the trap fires, not when it is set — and
# it fires on interrupt too, not just a clean exit.
trap 'rm -rf "$tmp"' EXIT INT TERM

info "installing ${BINARY} ${VERSION} for ${os}/${arch}"

curl -fsSL --proto '=https' --tlsv1.2 -o "${tmp}/${asset}" "${base}/${asset}" \
	|| fail "no release asset ${asset} at ${base} — check that ${VERSION} exists and publishes your platform"

# --- verify -------------------------------------------------------------------

# A checksum that ships from the same release is not a defence against a
# compromised release; it catches truncated and corrupted downloads, which is
# the failure this script can actually see. Anything stronger needs signing.
if curl -fsSL --proto '=https' --tlsv1.2 -o "${tmp}/SHA256SUMS" "${base}/SHA256SUMS" 2>/dev/null; then
	if command -v sha256sum >/dev/null 2>&1; then
		verify() { sha256sum -c "$1" >/dev/null 2>&1; }
	elif command -v shasum >/dev/null 2>&1; then
		verify() { shasum -a 256 -c "$1" >/dev/null 2>&1; }
	else
		verify() { return 2; }
	fi

	# -c wants only the line for the file we actually downloaded; the others
	# refer to assets that are not on disk and would report as missing.
	grep " \{1,2\}${asset}\$" "${tmp}/SHA256SUMS" > "${tmp}/expected" 2>/dev/null \
		|| fail "SHA256SUMS has no entry for ${asset}"

	# `|| rc=$?` keeps this a condition, so `set -e` does not exit before the
	# case below can tell "no sha256 tool" apart from "checksum mismatch".
	rc=0
	( cd "$tmp" && verify expected ) || rc=$?
	case $rc in
		0) ok "checksum verified" ;;
		2) info "no sha256 tool available — skipping checksum verification" ;;
		*) fail "checksum mismatch for ${asset} — refusing to install" ;;
	esac
else
	info "release publishes no SHA256SUMS — skipping checksum verification"
fi

chmod +x "${tmp}/${asset}"

# Fail before installing rather than leaving a half-working binary on PATH.
"${tmp}/${asset}" version >/dev/null 2>&1 \
	|| fail "downloaded binary does not run on this machine"

# --- install ------------------------------------------------------------------

target="${INSTALL_DIR}/${BINARY}${ext}"

# Create the destination as the user when we can. Falling straight through to
# the sudo branch would put a root-owned directory inside $HOME for the
# INSTALL_DIR=$HOME/.local/bin case this script recommends on failure.
# Attempting the mkdir is what tells us whether the user can create it, at any
# depth — `~/.local/bin` usually needs `~/.local` created too. Failure just
# falls through to the sudo branch.
[ -d "$INSTALL_DIR" ] || mkdir -p "$INSTALL_DIR" 2>/dev/null || true

if [ -d "$INSTALL_DIR" ] && [ -w "$INSTALL_DIR" ]; then
	mv "${tmp}/${asset}" "$target"
elif command -v sudo >/dev/null 2>&1; then
	info "elevating with sudo to write to ${INSTALL_DIR}"
	sudo mkdir -p "$INSTALL_DIR"
	sudo mv "${tmp}/${asset}" "$target"
else
	fail "cannot write to ${INSTALL_DIR} and sudo is unavailable — retry with INSTALL_DIR=\$HOME/.local/bin"
fi

ok "installed $("$target" version)"
printf '     %s\n' "$target"

# `command -v` consults this shell's PATH, which is the user's PATH.
if ! command -v "$BINARY" >/dev/null 2>&1; then
	printf '\n%swarning%s %s is not on your PATH. Add it:\n\n    export PATH="%s:$PATH"\n\n' \
		"$yellow" "$reset" "$INSTALL_DIR" "$INSTALL_DIR"
fi

printf '\nNext:\n    %s repo create local --public\n    %s serve\n' "$BINARY" "$BINARY"
