// Package cli is the operator CLI: `apm-registry serve`, `migrate` and
// `repo`.
//
// One binary runs the registry and administers it, over the same services the
// HTTP layer uses — never its own logic and never HTTP calls to itself
// (ADR-0013). That is what lets `migrate`, and repository bootstrap, work on a
// stopped registry: a fresh install has no repository and no server, and
// something has to be able to create one.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/xgentic/agent-package-manager-registry/internal/config"
	"github.com/xgentic/agent-package-manager-registry/internal/domain/archive"
	"github.com/xgentic/agent-package-manager-registry/internal/service"
	"github.com/xgentic/agent-package-manager-registry/internal/store/blob"
	"github.com/xgentic/agent-package-manager-registry/internal/store/sqlite"
)

// app is the wired registry: config, both stores and the services over them.
type app struct {
	cfg    config.Config
	log    *slog.Logger
	store  *sqlite.Store
	blobs  *blob.FS
	client struct {
		publish      *service.PublishService
		query        *service.QueryService
		repositories *service.RepositoryService
	}
}

// open loads configuration and connects both stores.
//
// Configuration is validated first and the process stops on anything invalid,
// rather than starting degraded (TR-30).
func open(getenv func(string) string, log *slog.Logger) (*app, error) {
	cfg, err := config.Load(getenv)
	if err != nil {
		return nil, err
	}

	store, err := sqlite.Open(cfg.SQLitePath(), service.RandomIDs{})
	if err != nil {
		return nil, err
	}

	blobs, err := blob.NewFS(cfg.BlobDir())
	if err != nil {
		_ = store.Close()
		return nil, err
	}

	a := &app{cfg: cfg, log: log, store: store, blobs: blobs}
	a.client.publish = service.NewPublishService(store, blobs, service.SystemClock{}, service.PublishConfig{
		TempDir:              cfg.TempDir(),
		MaxArchiveBytes:      cfg.MaxArchiveBytes,
		MaxUncompressedBytes: cfg.MaxUncompressedBytes,
		MaxArchiveEntries:    cfg.MaxArchiveEntries,
		AcceptedMediaTypes:   cfg.AcceptedMediaTypes,
		RequireVersionMatch:  cfg.RequireManifestVersionMatch,
	})
	a.client.query = service.NewQueryService(store, blobs)
	a.client.repositories = service.NewRepositoryService(store)

	// Guard against a policy that accepts a type no reader implements.
	for _, mt := range cfg.AcceptedMediaTypes {
		if mt != archive.MediaTypeZip && mt != archive.MediaTypeGzip {
			_ = a.close()
			return nil, fmt.Errorf("configuration accepts %q, which this build cannot read", mt)
		}
	}
	return a, nil
}

func (a *app) close() error { return a.store.Close() }

// migrate applies pending migrations. It is idempotent, so `serve` runs it on
// every boot (FR-43).
func (a *app) migrate(ctx context.Context) error { return a.store.Migrate(ctx) }

func fprintf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
