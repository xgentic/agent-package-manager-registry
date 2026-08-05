package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/cli"
)

// run executes a command against a temp data directory and returns its exit
// code plus both streams.
func run(t *testing.T, dataDir string, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := cli.Run(t.Context(), cli.Env{
		Args:   args,
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: func(key string) string {
			if key == "APM_REGISTRY_DATA_DIR" {
				return dataDir
			}
			return ""
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return code, stdout.String(), stderr.String()
}

func TestHelpListsCommands(t *testing.T) {
	t.Parallel()

	code, stdout, _ := run(t, t.TempDir(), "help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, command := range []string{"serve", "migrate", "repo create", "repo list"} {
		if !strings.Contains(stdout, command) {
			t.Errorf("help output does not mention %q:\n%s", command, stdout)
		}
	}
}

func TestUnknownCommandExitsNonZero(t *testing.T) {
	t.Parallel()

	code, _, stderr := run(t, t.TempDir(), "frobnicate")
	if code == 0 {
		t.Error("exit code = 0 for an unknown command, want non-zero")
	}
	if !strings.Contains(stderr, "frobnicate") {
		t.Errorf("stderr = %q, want it to name the unknown command", stderr)
	}
}

// The installers run `apm-registry version` as their post-download smoke test,
// so all three spellings have to exit 0 and name the binary.
func TestVersionReportsTheBuild(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"version", "--version", "-v"} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()

			code, stdout, stderr := run(t, t.TempDir(), arg)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
			}
			if !strings.HasPrefix(stdout, "apm-registry ") {
				t.Errorf("stdout = %q, want it to start with the binary name", stdout)
			}
			// An unstamped test build must say so rather than claim a release.
			if !strings.Contains(stdout, "dev") {
				t.Errorf("stdout = %q, want an unstamped build to report \"dev\"", stdout)
			}
		})
	}
}

// FR-43: migrate is idempotent, so `serve` can run it on every boot.
func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()

	for i := range 2 {
		code, stdout, stderr := run(t, dataDir, "migrate")
		if code != 0 {
			t.Fatalf("run %d: exit code = %d, stderr = %s", i, code, stderr)
		}
		if !strings.Contains(stdout, "migrations applied") {
			t.Errorf("run %d: stdout = %q", i, stdout)
		}
	}
}

// T7.3 / ADR-0013: a fresh install can create a repository and see it listed,
// with no server running.
func TestRepoCreateAndList(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()

	code, stdout, stderr := run(t, dataDir, "repo", "create", "corp-main", "--private")
	if code != 0 {
		t.Fatalf("repo create exit code = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "corp-main") {
		t.Errorf("stdout = %q, want it to confirm the name", stdout)
	}

	code, stdout, stderr = run(t, dataDir, "repo", "list", "--json")
	if code != 0 {
		t.Fatalf("repo list exit code = %d, stderr = %s", code, stderr)
	}

	var repos []struct {
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
	}
	if err := json.Unmarshal([]byte(stdout), &repos); err != nil {
		t.Fatalf("repo list --json produced %q: %v", stdout, err)
	}
	if len(repos) != 1 || repos[0].Name != "corp-main" {
		t.Fatalf("repos = %+v, want the one created repository", repos)
	}
	if repos[0].Visibility != "private" {
		t.Errorf("visibility = %q, want private by default", repos[0].Visibility)
	}
}

func TestRepoCreateRejectsInvalidNames(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()

	for _, name := range []string{"Corp-Main", "-leading", "has space", "has/slash"} {
		code, _, stderr := run(t, dataDir, "repo", "create", name)
		if code == 0 {
			t.Errorf("repo create %q exit code = 0, want non-zero", name)
		}
		if stderr == "" {
			t.Errorf("repo create %q produced no explanation", name)
		}
	}
}

func TestRepoCreateRejectsContradictoryVisibility(t *testing.T) {
	t.Parallel()

	code, _, _ := run(t, t.TempDir(), "repo", "create", "corp-main", "--public", "--private")
	if code == 0 {
		t.Error("exit code = 0 for --public --private, want non-zero")
	}
}

func TestRepoListOnAFreshInstall(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := run(t, t.TempDir(), "repo", "list")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	// A fresh install has no repositories, and the output should say how to
	// get one rather than printing nothing.
	if !strings.Contains(stdout, "repo create") {
		t.Errorf("stdout = %q, want it to point at the bootstrap command", stdout)
	}
}

// TR-30: invalid configuration stops the process rather than starting it
// degraded.
func TestInvalidConfigurationExitsNonZero(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := cli.Run(t.Context(), cli.Env{
		Args:   []string{"migrate"},
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: func(key string) string {
			switch key {
			case "APM_REGISTRY_DATA_DIR":
				return filepath.Join(t.TempDir(), "data")
			case "PORT":
				return "not-a-port"
			default:
				return ""
			}
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	if code == 0 {
		t.Fatal("exit code = 0 with an invalid PORT, want non-zero")
	}
	if !strings.Contains(stderr.String(), "PORT") {
		t.Errorf("stderr = %q, want it to name the offending variable", stderr.String())
	}
}
