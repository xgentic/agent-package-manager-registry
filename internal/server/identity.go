package server

import (
	"fmt"
	"net/http"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
	"github.com/xgentic/agent-package-manager-registry/internal/service"
)

// MS-API §1.2 requires one resource at two path shapes:
//
//	/v1/packages/acme/web-skills/versions                     ← two segments
//	/v1/packages/gitlab.com%2Facme%2Fweb-skills/versions       ← one, encoded
//
// ServeMux matches against the **escaped** path, so `%2F` stays inside a single
// wildcard segment and the two shapes are two patterns of different lengths —
// no ambiguity, no manual ordering. r.PathValue then returns the **decoded**
// value.
//
// Two rules follow, both security-relevant (ADR-0007, FR-15):
//
//  1. Never read r.URL.Path. It is already decoded, so a percent-encoded
//     identity reads as six segments there and any routing or validation based
//     on it is wrong.
//  2. Validate after decoding. `/v1/packages/acme%2F..%2Fevil/versions` matches,
//     returns 200 from the mux, and hands the handler `acme/../evil`. The
//     traversal is only visible in the decoded value, so that is what
//     ParseIdentity has to see.

// scopeAction selects which grant a route requires.
type scopeAction string

const (
	readAction    scopeAction = domain.ActionRead
	publishAction scopeAction = domain.ActionPublish
)

// Whether a route's pattern carries a {version} segment.
const (
	withVersion    = true
	withoutVersion = false
)

// packageRequest is a resolved, authorised request against one package.
type packageRequest struct {
	Principal  Principal
	Repository service.Repository
	Package    domain.Identity
	Version    domain.Version
}

type packageHandler func(http.ResponseWriter, *http.Request, packageRequest)

// handlePackageRoutes registers one endpoint in **both** identity path forms,
// funnelling them into a single handler through a shared resolver. Registering
// them separately, or resolving identity per handler, is how the two forms drift
// into resolving to different packages.
func (s *Server) handlePackageRoutes(mux *http.ServeMux, method, suffix string, action scopeAction, needsVersion bool, h packageHandler) {
	handler := s.resolvePackage(action, needsVersion, h)

	mux.Handle(method+" "+packagePrefix+"/{owner}/{repo}"+suffix, handler)
	mux.Handle(method+" "+packagePrefix+"/{identity}"+suffix, handler)
}

// resolvePackage turns path values into domain values, resolves the repository,
// and applies the scope check — in that order, before the handler runs.
func (s *Server) resolvePackage(action scopeAction, needsVersion bool, h packageHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := packageRequest{Principal: principalFrom(r.Context())}

		// ADR-0009: the repository is resolved before anything else, so every
		// downstream store call is repository-scoped by construction.
		repo, err := s.repositories.Get(r.Context(), r.PathValue("repository"))
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		req.Repository = repo

		id, err := identityFrom(r)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		req.Package = id

		if needsVersion {
			version, err := domain.ParseVersion(r.PathValue("version"))
			if err != nil {
				s.writeError(w, r, err)
				return
			}
			req.Version = version
		}

		// The scope check runs here, on every package route, in MVP 1 — where
		// it always passes. See the seam note in middleware.go.
		if err := authorise(req.Principal, requiredScope(action, id)); err != nil {
			s.writeError(w, r, err)
			return
		}

		h(w, r, req)
	})
}

func requiredScope(action scopeAction, id domain.Identity) domain.Scope {
	if action == publishAction {
		return domain.PublishScope(id)
	}
	return domain.ReadScope(id)
}

// identityFrom reads whichever path form matched. PathValue returns decoded
// values, which is exactly what ParseIdentity must validate.
func identityFrom(r *http.Request) (domain.Identity, error) {
	if encoded := r.PathValue("identity"); encoded != "" {
		return domain.ParseIdentity(encoded)
	}

	owner, repo := r.PathValue("owner"), r.PathValue("repo")
	if owner == "" || repo == "" {
		return domain.Identity{}, fmt.Errorf("%w: no package identity in the request path", domain.ErrBadRequest)
	}
	return domain.ParseIdentityParts(owner, repo)
}
