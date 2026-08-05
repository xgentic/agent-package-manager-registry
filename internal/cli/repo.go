package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xgentic/agent-package-manager-registry/internal/service"
)

// runRepo dispatches `repo create` and `repo list`.
//
// A fresh install has no repositories, and there is no HTTP route that creates
// one: publishing into a repository that does not exist is a 404 until an
// operator with host access creates it (ADR-0013).
func runRepo(ctx context.Context, env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("repo needs a subcommand: create or list")
	}

	switch args[0] {
	case "create":
		return runRepoCreate(ctx, env, args[1:])
	case "list":
		return runRepoList(ctx, env, args[1:])
	default:
		return fmt.Errorf("unknown repo subcommand %q; want create or list", args[0])
	}
}

func runRepoCreate(ctx context.Context, env Env, args []string) error {
	fs := newFlagSet(env, "repo create")
	public := fs.Bool("public", false, "allow anonymous reads (enforced from MVP 3)")
	private := fs.Bool("private", false, "require credentials to read (the default)")
	quota := fs.Int64("quota", 0, "storage quota in bytes, 0 for unlimited (enforced from MVP 4)")
	asJSON := fs.Bool("json", false, "emit the created repository as JSON")

	positional, err := parseFlagsAround(fs, args, 1)
	if err != nil {
		return fmt.Errorf("usage: apm-registry repo create <name> [--public|--private] [--quota <bytes>]")
	}
	if *public && *private {
		return fmt.Errorf("--public and --private are mutually exclusive")
	}

	visibility := service.VisibilityPrivate
	if *public {
		visibility = service.VisibilityPublic
	}

	app, err := open(env.Getenv, env.Log)
	if err != nil {
		return err
	}
	defer func() { _ = app.close() }()

	// The CLI works on a stopped registry, so it migrates before writing.
	if err := app.migrate(ctx); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}

	repo, err := app.client.repositories.Create(ctx, positional[0], visibility, *quota)
	if err != nil {
		return err
	}

	if *asJSON {
		return writeJSON(env, repositoryView(repo))
	}
	fprintf(env.Stdout, "created repository %q (%s)\n", repo.Name, repo.Visibility)
	fprintf(env.Stdout, "base URL: %s/api/agentpackages/%s\n", app.cfg.BaseURL, repo.Name)
	return nil
}

func runRepoList(ctx context.Context, env Env, args []string) error {
	fs := newFlagSet(env, "repo list")
	asJSON := fs.Bool("json", false, "emit the list as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	app, err := open(env.Getenv, env.Log)
	if err != nil {
		return err
	}
	defer func() { _ = app.close() }()

	if err := app.migrate(ctx); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}

	repos, err := app.client.repositories.List(ctx)
	if err != nil {
		return err
	}

	if *asJSON {
		views := make([]repositoryJSON, 0, len(repos))
		for _, r := range repos {
			views = append(views, repositoryView(r))
		}
		return writeJSON(env, views)
	}

	if len(repos) == 0 {
		fprintf(env.Stdout, "no repositories; create one with: apm-registry repo create <name>\n")
		return nil
	}
	for _, r := range repos {
		fprintf(env.Stdout, "%-24s %-8s quota=%d\n", r.Name, r.Visibility, r.QuotaBytes)
	}
	return nil
}

// repositoryJSON is the machine-readable shape. `--json` on every read command
// is what makes the CLI usable from scripts and health checks (ADR-0013).
type repositoryJSON struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	QuotaBytes int64  `json:"quota_bytes"`
	CreatedAt  string `json:"created_at"`
}

func repositoryView(r service.Repository) repositoryJSON {
	return repositoryJSON{
		Name:       r.Name,
		Visibility: string(r.Visibility),
		QuotaBytes: r.QuotaBytes,
		CreatedAt:  r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func writeJSON(env Env, v any) error {
	enc := json.NewEncoder(env.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
