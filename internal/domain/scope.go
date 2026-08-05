package domain

import (
	"fmt"
	"strings"
)

// Scope is an authorisation grant.
//
//	read                      read any package
//	read:acme/web-skills      read one package
//	publish                   publish any package
//	publish:acme/*            publish any package owned by acme
//	publish:acme/web-skills   publish one package
//
// Scope strings are server-side only and never appear in responses
// (docs/specs/04-api-contract.md §2).
//
// Nothing enforces scopes in MVP 1 — the auth middleware grants everything.
// This file exists anyway, fully tested, because it is what makes MVP 3 a
// change to one middleware body rather than a retrofit across every handler
// (roadmap T2.6, risk R-2).
type Scope string

// Actions.
const (
	ActionRead    = "read"
	ActionPublish = "publish"
)

// Scope constructors for the two required grants. Handlers ask for these; they
// never build scope strings by concatenation.
func ReadScope(id Identity) Scope    { return Scope(ActionRead + ":" + id.String()) }
func PublishScope(id Identity) Scope { return Scope(ActionPublish + ":" + id.String()) }

// Global grants.
const (
	ScopeReadAll    Scope = ActionRead
	ScopePublishAll Scope = ActionPublish
)

func (s Scope) String() string { return string(s) }

// ParseScope validates a scope string.
func ParseScope(s string) (Scope, error) {
	action, target, hasTarget := strings.Cut(s, ":")
	if action != ActionRead && action != ActionPublish {
		return "", invalidInput("scope", "unknown action %q", action)
	}
	if !hasTarget {
		return Scope(s), nil
	}

	if target == "" {
		return "", invalidInput("scope", "%q has an empty target", s)
	}
	// An owner wildcard is a target shape, not an identity, so it is validated
	// here rather than by ParseIdentity.
	if owner, ok := strings.CutSuffix(target, "/*"); ok {
		if owner == "" {
			return "", invalidInput("scope", "%q has an empty owner", s)
		}
		return Scope(s), nil
	}
	if _, err := ParseIdentity(target); err != nil {
		return "", invalidInput("scope", "%q has an invalid target: %v", s, err)
	}
	return Scope(s), nil
}

// Satisfies reports whether the grant s covers the concrete requirement.
//
// Required scopes are always concrete (`publish:acme/web-skills`); a wildcard
// on the required side would be a bug, and is treated as unsatisfiable.
func (s Scope) Satisfies(required Scope) bool {
	requiredAction, requiredTarget, requiredHasTarget := strings.Cut(string(required), ":")
	if !requiredHasTarget || requiredTarget == "" || strings.Contains(requiredTarget, "*") {
		return false
	}

	grantedAction, grantedTarget, grantedHasTarget := strings.Cut(string(s), ":")
	if grantedAction != requiredAction {
		return false
	}

	switch {
	case !grantedHasTarget:
		// `read` / `publish` with no target cover every package.
		return true
	case grantedTarget == requiredTarget:
		return true
	}

	if owner, ok := strings.CutSuffix(grantedTarget, "/*"); ok {
		// `publish:acme/*` covers `publish:acme/web-skills` but not
		// `publish:acme-corp/web-skills`, hence the separator in the prefix.
		return strings.HasPrefix(requiredTarget, owner+"/")
	}
	return false
}

// Scopes is a principal's full grant set.
type Scopes []Scope

// Satisfies reports whether any single grant covers the requirement.
func (ss Scopes) Satisfies(required Scope) bool {
	for _, s := range ss {
		if s.Satisfies(required) {
			return true
		}
	}
	return false
}

func (ss Scopes) String() string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = string(s)
	}
	return strings.Join(parts, " ")
}

// ParseScopes validates a whitespace-separated grant list.
func ParseScopes(raw string) (Scopes, error) {
	var out Scopes
	for _, field := range strings.Fields(raw) {
		s, err := ParseScope(field)
		if err != nil {
			return nil, fmt.Errorf("parsing scopes: %w", err)
		}
		out = append(out, s)
	}
	return out, nil
}
