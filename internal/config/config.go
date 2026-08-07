// Package config loads and validates the server's configuration from the
// environment.
//
// The contract is TR-30: a process either starts with a fully valid
// configuration or does not start at all. Load therefore reports *every*
// invalid variable at once rather than failing on the first — an operator
// fixing a deployment should need one round-trip, not five.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Defaults, from docs/specs/03-architecture.md §10.
const (
	DefaultPort                 = "3000"
	DefaultDataDir              = "./data"
	DefaultMaxArchiveBytes      = 52_428_800  // 50 MB, MS-API §6.8
	DefaultMaxUncompressedBytes = 104_857_600 // 100 MB, req-sc-004
	DefaultMaxArchiveEntries    = 10_000      // req-sc-004
	DefaultAcceptedMediaTypes   = "application/zip,application/gzip"
	DefaultRequireVersionMatch  = true
	DefaultAllowInsecureHTTP    = false
	DefaultBlobBackend          = "fs"
	blobSubdir                  = "blobs"
	tempSubdir                  = "tmp"
	defaultDatabaseFileName     = "registry.db"
	sqliteDSNPrefix             = "sqlite://"
)

// Config is the validated configuration. Every field is usable as-is; nothing
// downstream re-parses an environment variable.
type Config struct {
	Port        string
	Addr        string
	BaseURL     string
	DataDir     string
	DatabaseDSN string
	BlobBackend string

	MaxArchiveBytes      int64
	MaxUncompressedBytes int64
	MaxArchiveEntries    int

	AcceptedMediaTypes []string

	RequireManifestVersionMatch bool
	AllowInsecureHTTP           bool
}

// BlobDir is where content-addressed archive bytes live.
func (c Config) BlobDir() string { return filepath.Join(c.DataDir, blobSubdir) }

// TempDir is where uploads are streamed before validation. It sits inside the
// data directory on purpose: BlobStore.Put renames rather than copies, and
// os.Rename only works within one filesystem (ADR-0011).
func (c Config) TempDir() string { return filepath.Join(c.DataDir, tempSubdir) }

// SQLitePath returns the database file path for a sqlite:// DSN.
func (c Config) SQLitePath() string { return strings.TrimPrefix(c.DatabaseDSN, sqliteDSNPrefix) }

// Load reads the configuration from the environment, applying defaults and
// validating as it goes. The returned error, if any, names every problem.
func Load(getenv func(string) string) (Config, error) {
	var problems []error
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf(format, args...))
	}

	cfg := Config{
		Port:        stringOr(getenv, "PORT", DefaultPort),
		BaseURL:     strings.TrimRight(getenv("APM_REGISTRY_BASE_URL"), "/"),
		DataDir:     stringOr(getenv, "APM_REGISTRY_DATA_DIR", DefaultDataDir),
		BlobBackend: stringOr(getenv, "APM_REGISTRY_BLOB_BACKEND", DefaultBlobBackend),
	}

	if port, err := strconv.Atoi(cfg.Port); err != nil || port < 1 || port > 65535 {
		fail("PORT: %q is not a port number between 1 and 65535", cfg.Port)
	}

	// The listen address is resolved before the base URL, because an unset
	// base URL is derived from the port the server actually binds.
	cfg.Addr = resolveAddr(getenv, &cfg.Port, fail)

	if cfg.BaseURL == "" {
		// A local default so `repo create` can print a usable base URL on a
		// fresh install. A deployment sets its real public origin.
		cfg.BaseURL = "http://localhost:" + cfg.Port
	}

	if cfg.BlobBackend != "fs" {
		fail("APM_REGISTRY_BLOB_BACKEND: %q is not supported; v1 implements only \"fs\"", cfg.BlobBackend)
	}

	// The DSN defaults to a file inside the data directory, so a bare
	// APM_REGISTRY_DATA_DIR is enough to run.
	cfg.DatabaseDSN = stringOr(getenv, "APM_REGISTRY_DB_URL",
		sqliteDSNPrefix+filepath.Join(cfg.DataDir, defaultDatabaseFileName))
	if !strings.HasPrefix(cfg.DatabaseDSN, sqliteDSNPrefix) {
		fail("APM_REGISTRY_DB_URL: %q is not a sqlite:// DSN; v1 implements only SQLite", cfg.DatabaseDSN)
	} else if cfg.SQLitePath() == "" {
		fail("APM_REGISTRY_DB_URL: %q has no database path", cfg.DatabaseDSN)
	}

	cfg.MaxArchiveBytes = positiveInt64(getenv, "APM_REGISTRY_MAX_ARCHIVE_BYTES", DefaultMaxArchiveBytes, fail)
	cfg.MaxUncompressedBytes = positiveInt64(getenv, "APM_REGISTRY_MAX_UNCOMPRESSED_BYTES", DefaultMaxUncompressedBytes, fail)
	cfg.MaxArchiveEntries = int(positiveInt64(getenv, "APM_REGISTRY_MAX_ARCHIVE_ENTRIES", DefaultMaxArchiveEntries, fail))

	// An uncompressed cap below the compressed cap would reject archives that
	// are within their stated limit — a configuration that cannot be satisfied.
	if cfg.MaxUncompressedBytes > 0 && cfg.MaxArchiveBytes > cfg.MaxUncompressedBytes {
		fail("APM_REGISTRY_MAX_UNCOMPRESSED_BYTES (%d) must be at least APM_REGISTRY_MAX_ARCHIVE_BYTES (%d)",
			cfg.MaxUncompressedBytes, cfg.MaxArchiveBytes)
	}

	cfg.AcceptedMediaTypes = parseMediaTypes(
		stringOr(getenv, "APM_REGISTRY_ACCEPTED_MEDIA_TYPES", DefaultAcceptedMediaTypes), fail)

	cfg.RequireManifestVersionMatch = boolOr(getenv, "APM_REGISTRY_REQUIRE_MANIFEST_VERSION_MATCH", DefaultRequireVersionMatch, fail)
	cfg.AllowInsecureHTTP = boolOr(getenv, "APM_REGISTRY_ALLOW_INSECURE_HTTP", DefaultAllowInsecureHTTP, fail)

	// TR-16: plain HTTP in production must be asked for explicitly. A loopback
	// origin is local development by definition, so it needs no flag — the
	// requirement is about not shipping an unencrypted *deployment* by
	// accident.
	if strings.HasPrefix(cfg.BaseURL, "http://") && !isLoopback(cfg.BaseURL) && !cfg.AllowInsecureHTTP {
		fail("APM_REGISTRY_BASE_URL: %q is plain HTTP; set APM_REGISTRY_ALLOW_INSECURE_HTTP=true to permit it (development only)", cfg.BaseURL)
	}

	if len(problems) > 0 {
		return Config{}, &InvalidError{Problems: problems}
	}
	return cfg, nil
}

// LoadFromEnv is Load against the process environment.
func LoadFromEnv() (Config, error) { return Load(os.Getenv) }

// InvalidError collects every configuration problem found in one pass, so the
// operator sees the whole list rather than the first item of it.
type InvalidError struct {
	Problems []error
}

func (e *InvalidError) Error() string {
	var b strings.Builder
	b.WriteString("invalid configuration:")
	for _, p := range e.Problems {
		b.WriteString("\n  - ")
		b.WriteString(p.Error())
	}
	return b.String()
}

func (e *InvalidError) Unwrap() []error { return e.Problems }

// isLoopback reports whether a base URL points at this machine.
func isLoopback(baseURL string) bool {
	host := strings.TrimPrefix(baseURL, "http://")
	if i := strings.IndexAny(host, "/:"); i >= 0 && !strings.HasPrefix(host, "[") {
		host = host[:i]
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")

	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// ValidatePort reports whether raw is a usable TCP port number. Exported so
// the CLI's flags are held to the same rule as the environment.
func ValidatePort(raw string) error {
	if n, err := strconv.Atoi(raw); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%q is not a port number between 1 and 65535", raw)
	}
	return nil
}

// ValidateAddr reports whether raw is a bindable host:port address. An empty
// host means every interface.
func ValidateAddr(raw string) error {
	_, port, err := net.SplitHostPort(raw)
	if err != nil {
		return fmt.Errorf("%q is not a host:port address (try \":3000\" or \"127.0.0.1:3000\")", raw)
	}
	if err := ValidatePort(port); err != nil {
		return fmt.Errorf("%q has an unusable port: %w", raw, err)
	}
	return nil
}

// resolveAddr determines the TCP address the server binds.
//
// Unset means every interface on PORT, which is what this server did before the
// variable existed — the default must not change under anyone. Set, it wins,
// and *port is updated to match so the default base URL still names the port
// that answers.
func resolveAddr(getenv func(string) string, port *string, fail func(string, ...any)) string {
	raw := strings.TrimSpace(getenv("APM_REGISTRY_ADDR"))
	if raw == "" {
		return ":" + *port
	}

	if err := ValidateAddr(raw); err != nil {
		fail("APM_REGISTRY_ADDR: %v", err)
		return ""
	}
	_, p, _ := net.SplitHostPort(raw)

	// Two variables that both name a port must not disagree silently: the
	// loser decides where an unauthenticated server listens.
	if explicit := strings.TrimSpace(getenv("PORT")); explicit != "" && explicit != p {
		fail("APM_REGISTRY_ADDR: %q and PORT (%q) disagree about the port; set one of them, not both", raw, explicit)
		return ""
	}

	*port = p
	return raw
}

func stringOr(getenv func(string) string, key, fallback string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return fallback
}

func positiveInt64(getenv func(string) string, key string, fallback int64, fail func(string, ...any)) int64 {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback
	}

	v, err := strconv.ParseInt(raw, 10, 64)
	switch {
	case err != nil:
		fail("%s: %q is not an integer", key, raw)
		return fallback
	case v <= 0:
		fail("%s: %d must be greater than zero", key, v)
		return fallback
	}
	return v
}

func boolOr(getenv func(string) string, key string, fallback bool, fail func(string, ...any)) bool {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback
	}

	v, err := strconv.ParseBool(raw)
	if err != nil {
		fail("%s: %q is not a boolean (true/false)", key, raw)
		return fallback
	}
	return v
}

// parseMediaTypes narrows the publish format policy (ADR-0004). Only the two
// types MS-API defines are selectable; anything else would be a type this
// server has no reader for.
func parseMediaTypes(raw string, fail func(string, ...any)) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		mt := strings.ToLower(strings.TrimSpace(part))
		if mt == "" {
			continue
		}
		if mt != "application/zip" && mt != "application/gzip" {
			fail("APM_REGISTRY_ACCEPTED_MEDIA_TYPES: %q is not a supported archive type (application/zip, application/gzip)", mt)
			continue
		}
		if !slices.Contains(out, mt) {
			out = append(out, mt)
		}
	}
	if len(out) == 0 {
		fail("APM_REGISTRY_ACCEPTED_MEDIA_TYPES: at least one media type must be accepted")
	}
	return out
}
