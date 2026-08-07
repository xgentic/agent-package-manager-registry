package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/xgentic/agent-package-manager-registry/internal/server"
	"github.com/xgentic/agent-package-manager-registry/internal/service"
)

// Server timeouts (TR-27). ReadHeaderTimeout bounds slow-header attacks;
// WriteTimeout is generous because publish and download both stream archives.
const (
	readHeaderTimeout = 5 * time.Second
	writeTimeout      = 10 * time.Minute
	idleTimeout       = 2 * time.Minute
	shutdownTimeout   = 30 * time.Second
)

func runServe(ctx context.Context, env Env, args []string) error {
	fs := newFlagSet(env, "serve")
	port := fs.String("port", "", "listen port, keeping the configured host (overrides PORT)")
	addr := fs.String("addr", "", "listen address as host:port (overrides APM_REGISTRY_ADDR)")
	noMigrate := fs.Bool("no-migrate", false, "do not apply pending migrations on boot")
	if err := fs.Parse(args); err != nil {
		return err
	}

	app, err := open(env.Getenv, env.Log)
	if err != nil {
		return err
	}
	defer func() { _ = app.close() }()

	// Resolved before any work with side effects: a bad address should stop the
	// process, not leave it having migrated and swept on the way to failing.
	listenAddr, err := resolveListenAddr(app.cfg.Addr, *addr, *port)
	if err != nil {
		return err
	}

	if !*noMigrate {
		if err := app.migrate(ctx); err != nil {
			return fmt.Errorf("applying migrations: %w", err)
		}
	}

	// TR-15: a process killed mid-upload leaves an archive-sized temp file
	// behind. Nothing can be in flight at boot, so this is the safe moment to
	// clear them.
	if removed, err := service.SweepTemp(app.cfg.TempDir()); err != nil {
		app.log.Warn("sweeping orphaned uploads", "error", err)
	} else if removed > 0 {
		app.log.Info("swept orphaned uploads", "count", removed)
	}

	handler := server.New(server.Deps{
		Log:          app.log,
		Config:       app.cfg,
		Publish:      app.client.publish,
		Query:        app.client.query,
		Repositories: app.client.repositories,
		Meta:         app.store,
		Blobs:        app.blobs,
	})

	return listenAndServe(ctx, app.log.Info, listenAddr, handler)
}

// listenAndServe binds, serves, and drains on signal.
func listenAndServe(ctx context.Context, info func(string, ...any), addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	// Bind before backgrounding, so a failure here is reported accurately and
	// exits non-zero rather than logging "listening" for a socket we never got.
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", srv.Addr, err)
	}
	info("listening", "addr", ln.Addr().String())

	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	info("shutting down")

	// Drain rather than cut: an in-flight archive transfer is minutes of a
	// user's time and, for publish, a partial upload (TR-27).
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown failed: %w", err)
	}
	return <-serveErr
}

func runMigrate(ctx context.Context, env Env, args []string) error {
	fs := newFlagSet(env, "migrate")
	if err := fs.Parse(args); err != nil {
		return err
	}

	app, err := open(env.Getenv, env.Log)
	if err != nil {
		return err
	}
	defer func() { _ = app.close() }()

	if err := app.migrate(ctx); err != nil {
		return err
	}
	fprintf(env.Stdout, "migrations applied\n")
	return nil
}
