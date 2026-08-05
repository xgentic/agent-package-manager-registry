package cli

import "runtime"

// Build metadata, stamped by the linker in `make dist` and the release
// workflow (-X github.com/xgentic/agent-package-manager-registry/internal/cli.version=...).
//
// An unstamped build — `go build .`, `go run .` — reports "dev". That is the
// point: a bug report from a locally built binary must be distinguishable from
// one against a published release, and the installer's checksum only covers
// the latter.
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

// runVersion reports the build alongside the toolchain and target it was built
// for, so a single line is enough to reproduce an environment from a bug report.
func runVersion(env Env) {
	fprintf(env.Stdout, "apm-registry %s (commit %s, built %s, %s %s/%s)\n",
		version, commit, buildDate, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
