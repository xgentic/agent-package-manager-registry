// Package domain holds the registry's pure rules: identity, versions, digests,
// scopes, manifests and the publish validation pipeline.
//
// It imports nothing of ours and, deliberately, neither net/http nor
// database/sql. Every rule here is testable without a server or a database —
// see docs/specs/03-architecture.md §3.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// The closed set of registry error kinds. Adapters map these to HTTP statuses
// in exactly one place (internal/server/problem.go); nothing else decides a
// status code (FR-26, ADR-0006).
var (
	// ErrNotFound covers an unknown repository, package or version. The API
	// never distinguishes "absent" from "not permitted" (FR-40).
	ErrNotFound = errors.New("not found")

	// ErrBadRequest is a malformed request: an unparseable identity, a version
	// selector with control characters, a corrupt archive body.
	ErrBadRequest = errors.New("bad request")

	// ErrUnauthenticated means no credentials were presented (401). MVP 1 never
	// returns it — the auth seam grants everything — but the taxonomy is
	// complete so MVP 3 adds no new kinds (FR-23).
	ErrUnauthenticated = errors.New("unauthenticated")

	// ErrVersionConflict is a repeat publish of an existing tuple (409). It is
	// raised by the database's unique constraint, never by a prior SELECT
	// (TR-08).
	ErrVersionConflict = errors.New("version already published")

	// ErrPayloadTooLarge means the upload exceeded the configured cap (413).
	ErrPayloadTooLarge = errors.New("payload too large")

	// ErrUnsupportedMediaType is a Content-Type outside the accepted set (415).
	ErrUnsupportedMediaType = errors.New("unsupported media type")

	// ErrArchiveInvalid is a validation failure carrying every broken rule
	// (422). It never carries just the first (FR-10).
	ErrArchiveInvalid = errors.New("package validation failed")

	// ErrScopeDenied means credentials were presented but lack the required
	// scope (403).
	ErrScopeDenied = errors.New("insufficient scope")

	// ErrInternal is the catch-all. Problem bodies for it say nothing about
	// what happened (FR-28).
	ErrInternal = errors.New("internal error")
)

// RuleFailure is one broken publish rule. The Rule field uses the exact
// vocabulary of docs/specs/04-api-contract.md §4 — it is part of the wire
// contract, not a log string.
type RuleFailure struct {
	Rule    RuleName `json:"rule"`
	Message string   `json:"message"`
	Entry   string   `json:"entry,omitempty"`
}

// RuleName is the closed vocabulary for RuleFailure.Rule.
type RuleName string

const (
	RuleVersionSelector      RuleName = "version_selector"
	RuleArchiveParse         RuleName = "archive_parse"
	RuleManifestPresent      RuleName = "manifest_present"
	RuleManifestYAML         RuleName = "manifest_yaml"
	RuleManifestFields       RuleName = "manifest_fields"
	RuleManifestVersionMatch RuleName = "manifest_version_match"
	RuleManifestNameMatch    RuleName = "manifest_name_match"
	RuleEntrySafety          RuleName = "entry_safety"
	RuleArchiveLimits        RuleName = "archive_limits"
)

// ValidationError reports every rule a publish broke.
type ValidationError struct {
	Failures []RuleFailure
}

func (e *ValidationError) Error() string {
	names := make([]string, 0, len(e.Failures))
	for _, f := range e.Failures {
		names = append(names, string(f.Rule))
	}
	return fmt.Sprintf("%s: %s", ErrArchiveInvalid, strings.Join(names, ", "))
}

func (e *ValidationError) Is(target error) bool { return target == ErrArchiveInvalid }

// NewValidationError returns nil when nothing failed, so callers can write
// `if err := NewValidationError(failures); err != nil`.
func NewValidationError(failures []RuleFailure) error {
	if len(failures) == 0 {
		return nil
	}
	return &ValidationError{Failures: failures}
}

// ConflictError carries what the 409 body must report about the publish that
// already holds the tuple (FR-11).
type ConflictError struct {
	Package         string
	Version         string
	PreviousDigest  Digest
	PreviousPublish time.Time
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("version %s of %s was already published at %s",
		e.Version, e.Package, e.PreviousPublish.UTC().Format(time.RFC3339))
}

func (e *ConflictError) Is(target error) bool { return target == ErrVersionConflict }

// ScopeError names the scope the caller would have needed, which the 403 body
// reports so the remediation is actionable (FR-25).
type ScopeError struct {
	Required Scope
}

func (e *ScopeError) Error() string {
	return fmt.Sprintf("%s: %s required", ErrScopeDenied, e.Required)
}

func (e *ScopeError) Is(target error) bool { return target == ErrScopeDenied }

// InvalidInputError is a malformed request value — an identity, a version
// selector, a repository name. Field names the offending input.
type InvalidInputError struct {
	Field  string
	Detail string
}

func (e *InvalidInputError) Error() string {
	return fmt.Sprintf("%s: %s: %s", ErrBadRequest, e.Field, e.Detail)
}

func (e *InvalidInputError) Is(target error) bool { return target == ErrBadRequest }

func invalidInput(field, format string, args ...any) error {
	return &InvalidInputError{Field: field, Detail: fmt.Sprintf(format, args...)}
}
