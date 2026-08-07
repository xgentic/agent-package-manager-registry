package config_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/config"
)

// env builds a getenv function over a map, so tests never touch the process
// environment and can run in parallel.
func env(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

func TestLoadAppliesDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(nil))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if cfg.Port != config.DefaultPort {
		t.Errorf("Port = %q, want %q", cfg.Port, config.DefaultPort)
	}
	if cfg.DataDir != config.DefaultDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, config.DefaultDataDir)
	}
	if want := "sqlite://" + filepath.Join(config.DefaultDataDir, "registry.db"); cfg.DatabaseDSN != want {
		t.Errorf("DatabaseDSN = %q, want %q", cfg.DatabaseDSN, want)
	}
	if cfg.MaxArchiveBytes != config.DefaultMaxArchiveBytes {
		t.Errorf("MaxArchiveBytes = %d, want %d", cfg.MaxArchiveBytes, config.DefaultMaxArchiveBytes)
	}
	if !cfg.RequireManifestVersionMatch {
		t.Error("RequireManifestVersionMatch = false, want true (MS-API §6.5 policy default)")
	}
	if len(cfg.AcceptedMediaTypes) != 2 {
		t.Errorf("AcceptedMediaTypes = %v, want zip and gzip", cfg.AcceptedMediaTypes)
	}
}

// The listen address is what keeps an unauthenticated build off the network,
// so its resolution is worth pinning down case by case.
func TestLoadAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		vars     map[string]string
		wantAddr string
		wantPort string
	}{
		{
			// Unset must stay byte-identical to the behaviour before this
			// variable existed: every interface, on the default port.
			"unset listens on every interface",
			nil,
			":" + config.DefaultPort,
			config.DefaultPort,
		},
		{
			"unset follows PORT",
			map[string]string{"PORT": "8080"},
			":8080",
			"8080",
		},
		{
			"host and port",
			map[string]string{"APM_REGISTRY_ADDR": "127.0.0.1:8080"},
			"127.0.0.1:8080",
			"8080",
		},
		{
			"empty host is every interface",
			map[string]string{"APM_REGISTRY_ADDR": ":8080"},
			":8080",
			"8080",
		},
		{
			"hostname",
			map[string]string{"APM_REGISTRY_ADDR": "localhost:3000"},
			"localhost:3000",
			"3000",
		},
		{
			"ipv6 host",
			map[string]string{"APM_REGISTRY_ADDR": "[::1]:3000"},
			"[::1]:3000",
			"3000",
		},
		{
			// Agreeing with PORT is not a conflict, so it must not fail.
			"agrees with PORT",
			map[string]string{"APM_REGISTRY_ADDR": "127.0.0.1:8080", "PORT": "8080"},
			"127.0.0.1:8080",
			"8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := config.Load(env(tt.vars))
			if err != nil {
				t.Fatalf("Load(%v) error = %v, want nil", tt.vars, err)
			}
			if cfg.Addr != tt.wantAddr {
				t.Errorf("Addr = %q, want %q", cfg.Addr, tt.wantAddr)
			}
			// Port stays the source of the default base URL, so it has to
			// follow the address rather than contradict it.
			if cfg.Port != tt.wantPort {
				t.Errorf("Port = %q, want %q", cfg.Port, tt.wantPort)
			}
		})
	}
}

// A base URL nobody set should describe the port the server actually listens
// on, or `repo create` prints one that does not answer.
func TestLoadAddrDrivesDefaultBaseURL(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(map[string]string{"APM_REGISTRY_ADDR": "127.0.0.1:8080"}))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if want := "http://localhost:8080"; cfg.BaseURL != want {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, want)
	}
}

func TestLoadDerivedPaths(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(map[string]string{"APM_REGISTRY_DATA_DIR": "/srv/registry"}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if want := "/srv/registry/blobs"; cfg.BlobDir() != want {
		t.Errorf("BlobDir() = %q, want %q", cfg.BlobDir(), want)
	}
	// ADR-0011: temp must share a filesystem with the blob store, so Put can
	// rename rather than copy.
	if want := "/srv/registry/tmp"; cfg.TempDir() != want {
		t.Errorf("TempDir() = %q, want %q", cfg.TempDir(), want)
	}
	if want := "/srv/registry/registry.db"; cfg.SQLitePath() != want {
		t.Errorf("SQLitePath() = %q, want %q", cfg.SQLitePath(), want)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		vars map[string]string
		want string
	}{
		{"port not a number", map[string]string{"PORT": "http"}, "PORT"},
		{"port out of range", map[string]string{"PORT": "70000"}, "PORT"},
		{"negative archive cap", map[string]string{"APM_REGISTRY_MAX_ARCHIVE_BYTES": "-1"}, "MAX_ARCHIVE_BYTES"},
		{"non-numeric entry cap", map[string]string{"APM_REGISTRY_MAX_ARCHIVE_ENTRIES": "many"}, "MAX_ARCHIVE_ENTRIES"},
		{"unknown media type", map[string]string{"APM_REGISTRY_ACCEPTED_MEDIA_TYPES": "application/x-7z"}, "ACCEPTED_MEDIA_TYPES"},
		{"empty media types", map[string]string{"APM_REGISTRY_ACCEPTED_MEDIA_TYPES": " , "}, "ACCEPTED_MEDIA_TYPES"},
		{"bad boolean", map[string]string{"APM_REGISTRY_REQUIRE_MANIFEST_VERSION_MATCH": "yes-please"}, "REQUIRE_MANIFEST_VERSION_MATCH"},
		{"unsupported blob backend", map[string]string{"APM_REGISTRY_BLOB_BACKEND": "s3"}, "BLOB_BACKEND"},
		{"non-sqlite dsn", map[string]string{"APM_REGISTRY_DB_URL": "postgres://localhost/db"}, "DB_URL"},
		{
			"uncompressed cap below compressed cap",
			map[string]string{
				"APM_REGISTRY_MAX_ARCHIVE_BYTES":      "200",
				"APM_REGISTRY_MAX_UNCOMPRESSED_BYTES": "100",
			},
			"MAX_UNCOMPRESSED_BYTES",
		},
		{
			// TR-16: plain HTTP has to be asked for explicitly.
			"plain http without the escape hatch",
			map[string]string{"APM_REGISTRY_BASE_URL": "http://registry.example.com"},
			"ALLOW_INSECURE_HTTP",
		},
		{"addr without a port", map[string]string{"APM_REGISTRY_ADDR": "127.0.0.1"}, "ADDR"},
		{"addr with no port after the colon", map[string]string{"APM_REGISTRY_ADDR": "127.0.0.1:"}, "ADDR"},
		{"addr port not a number", map[string]string{"APM_REGISTRY_ADDR": "127.0.0.1:http"}, "ADDR"},
		{"addr port out of range", map[string]string{"APM_REGISTRY_ADDR": "127.0.0.1:70000"}, "ADDR"},
		{
			// Silently preferring one of the two is how a server ends up
			// listening somewhere the operator did not intend.
			"addr and PORT disagree",
			map[string]string{"APM_REGISTRY_ADDR": "127.0.0.1:8080", "PORT": "3000"},
			"ADDR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Load(env(tt.vars))
			if err == nil {
				t.Fatalf("Load(%v) error = nil, want a validation failure", tt.vars)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load() error = %q, want it to name %q", err, tt.want)
			}
		})
	}
}

// TR-30: the operator should be able to fix a deployment in one round-trip, so
// the error names every problem rather than the first one found.
func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	t.Parallel()

	_, err := config.Load(env(map[string]string{
		"PORT":                             "nope",
		"APM_REGISTRY_MAX_ARCHIVE_BYTES":   "0",
		"APM_REGISTRY_BLOB_BACKEND":        "s3",
		"APM_REGISTRY_MAX_ARCHIVE_ENTRIES": "lots",
	}))
	if err == nil {
		t.Fatal("Load() error = nil, want failures")
	}

	var invalid *config.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("Load() error type = %T, want *config.InvalidError", err)
	}
	if len(invalid.Problems) != 4 {
		t.Errorf("reported %d problems, want 4:\n%v", len(invalid.Problems), err)
	}
}

func TestPlainHTTPAllowedWhenExplicit(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(map[string]string{
		"APM_REGISTRY_BASE_URL":            "http://localhost:3000/",
		"APM_REGISTRY_ALLOW_INSECURE_HTTP": "true",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	// MS-API §1.1: the base URL must not carry a trailing slash.
	if want := "http://localhost:3000"; cfg.BaseURL != want {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, want)
	}
}
