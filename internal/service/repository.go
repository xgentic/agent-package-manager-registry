package service

import (
	"context"
	"fmt"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
)

// RepositoryService manages the namespaces packages live in.
//
// Repositories are created only through this service, and only from the
// operator CLI in v1: a fresh install has no repository until someone runs
// `apm-registry repo create` (ADR-0013).
type RepositoryService struct {
	meta MetadataStore
}

func NewRepositoryService(meta MetadataStore) *RepositoryService {
	return &RepositoryService{meta: meta}
}

// Create validates the name against FR-30 before storing it.
func (s *RepositoryService) Create(ctx context.Context, name string, visibility Visibility, quotaBytes int64) (Repository, error) {
	validated, err := domain.ParseRepositoryName(name)
	if err != nil {
		return Repository{}, err
	}
	switch visibility {
	case VisibilityPublic, VisibilityPrivate:
	default:
		return Repository{}, fmt.Errorf("%w: visibility must be %q or %q",
			domain.ErrBadRequest, VisibilityPublic, VisibilityPrivate)
	}
	if quotaBytes < 0 {
		return Repository{}, fmt.Errorf("%w: quota must not be negative", domain.ErrBadRequest)
	}

	return s.meta.CreateRepository(ctx, validated.String(), visibility, quotaBytes)
}

// Get resolves the repository named in the base URL. An unknown name is
// domain.ErrNotFound, which the HTTP layer renders as a 404 (FR-29).
func (s *RepositoryService) Get(ctx context.Context, name string) (Repository, error) {
	// The name is untrusted path input, so it is validated before it reaches a
	// query — the same rule as identities.
	validated, err := domain.ParseRepositoryName(name)
	if err != nil {
		return Repository{}, fmt.Errorf("%w: no such repository", domain.ErrNotFound)
	}
	return s.meta.GetRepository(ctx, validated.String())
}

func (s *RepositoryService) List(ctx context.Context) ([]Repository, error) {
	return s.meta.ListRepositories(ctx)
}
