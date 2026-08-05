package cli

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
)

// Env is everything Run touches outside itself, so the whole CLI is testable
// without a process.
type Env struct {
	Args   []string // arguments after the program name
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	Log    *slog.Logger
}

// Run dispatches a subcommand and returns the process exit code.
//
// `serve` is the default: running the binary with no arguments starts the
// server, which is what a container image expects.
func Run(ctx context.Context, env Env) int {
	if env.Getenv == nil {
		env.Getenv = os.Getenv
	}
	if env.Log == nil {
		env.Log = slog.New(slog.NewJSONHandler(env.Stdout, nil))
	}

	command, args := "serve", []string(nil)
	if len(env.Args) > 0 {
		command, args = env.Args[0], env.Args[1:]
	}

	var err error
	switch command {
	case "serve":
		err = runServe(ctx, env, args)
	case "migrate":
		err = runMigrate(ctx, env, args)
	case "repo":
		err = runRepo(ctx, env, args)
	case "help", "-h", "--help":
		usage(env.Stdout)
		return 0
	case "version", "-v", "--version":
		runVersion(env)
		return 0
	default:
		fprintf(env.Stderr, "unknown command %q\n\n", command)
		usage(env.Stderr)
		return 2
	}

	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	default:
		fprintf(env.Stderr, "apm-registry: %v\n", err)
		return 1
	}
}

func usage(w io.Writer) {
	fprintf(w, `apm-registry — the APM package registry server and its operator CLI.

Usage:
  apm-registry [command] [flags]

Commands:
  serve                 Run the HTTP server (the default when no command is given)
  migrate               Apply pending database migrations, then exit
  repo create <name>    Create a repository
  repo list             List repositories
  version               Print the build version, commit and toolchain
  help                  Show this message

Run "apm-registry <command> --help" for the flags of a command.

Configuration is read from the environment; see .env.example. Invalid
configuration stops the process rather than starting it degraded.
`)
}

// newFlagSet builds a flag set that reports errors through the CLI's own
// streams rather than os.Stderr.
func newFlagSet(env Env, name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	return fs
}

// parseFlagsAround parses flags that appear on either side of the positional
// arguments.
//
// stdlib flag stops at the first non-flag argument, so
// `repo create corp-main --private` would silently ignore --private. Operators
// write it that way, and a silently ignored visibility flag is a repository
// with the wrong access rules.
func parseFlagsAround(fs *flag.FlagSet, args []string, positionals int) ([]string, error) {
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	rest := fs.Args()
	if len(rest) < positionals {
		return nil, errTooFewArguments
	}

	found := rest[:positionals]
	if err := fs.Parse(rest[positionals:]); err != nil {
		return nil, err
	}
	if fs.NArg() != 0 {
		return nil, errTooManyArguments
	}
	return found, nil
}

var (
	errTooFewArguments  = errors.New("too few arguments")
	errTooManyArguments = errors.New("too many arguments")
)
