// Command apm-registry serves the APM Registry HTTP API and administers it.
//
// The binary is both the server and the operator CLI (ADR-0013): `serve` is the
// default subcommand, so running it with no arguments starts the registry.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/xgentic/agent-package-manager-registry/internal/cli"
)

func main() {
	// Shut down on signal rather than dropping in-flight archive transfers.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Run(ctx, cli.Env{
		Args:   os.Args[1:],
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Getenv: os.Getenv,
		Log:    slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}))
}
