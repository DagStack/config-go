package config

import (
	"errors"
	"fmt"
)

// ErrorReason is the stable enum of structural error reasons from
// spec ADR-0001 §4.5 / _meta/error_reasons.yaml.
//
// Values are wire-stable — bindings in any language emit the same
// strings. Source of truth is spec/_meta/error_reasons.yaml;
// this file is hand-written in Phase A, an emitter will generate
// it in Phase D.
type ErrorReason string

const (
	// ReasonMissing — the requested path is absent from the merged
	// config tree and no default was supplied.
	ReasonMissing ErrorReason = "missing"

	// ReasonTypeMismatch — the value at the requested path cannot be
	// coerced to the requested type (e.g. GetInt on "abc").
	ReasonTypeMismatch ErrorReason = "type_mismatch"

	// ReasonEnvUnresolved — `${VAR}` without default encountered during
	// interpolation, VAR is not set in the environment.
	ReasonEnvUnresolved ErrorReason = "env_unresolved"

	// ReasonValidationFailed — schema validation failed for GetSection
	// (struct tag validation rules rejected a field).
	ReasonValidationFailed ErrorReason = "validation_failed"

	// ReasonParseError — YAML/JSON parse error when loading a source.
	ReasonParseError ErrorReason = "parse_error"

	// ReasonSourceUnavailable — the source could not be read (file
	// not found, remote endpoint down, etc.).
	ReasonSourceUnavailable ErrorReason = "source_unavailable"

	// ReasonReloadRejected — candidate tree failed validation during
	// hot-reload, swap was aborted (Phase 2+).
	ReasonReloadRejected ErrorReason = "reload_rejected"

	// ── ADR-0002 (Phase 2 — secret resolution errors) ────────────────

	// ReasonSecretUnresolved — a `${secret:<scheme>:...}` reference
	// cannot be resolved (no SecretSource registered, key missing,
	// requested ?version=N destroyed/deleted, or #field projection
	// asked for a sub-key absent in the resolved secret).
	ReasonSecretUnresolved ErrorReason = "secret_unresolved"

	// ReasonSecretBackendUnavailable — secret backend unreachable
	// (network error, auth failure, sealed Vault, timeout).
	ReasonSecretBackendUnavailable ErrorReason = "secret_backend_unavailable"

	// ReasonSecretPermissionDenied — backend rejected the read with
	// an authorisation error (Vault 403, AWS-SM AccessDenied, K8s RBAC).
	// Distinct from ReasonSecretUnresolved so operators dispatch on
	// reason: this one points at the backend's policy, not the
	// reference spelling.
	ReasonSecretPermissionDenied ErrorReason = "secret_permission_denied"
)

// Error is the structural error type from spec ADR-0001 §4.5.
//
// Fields (Path, Reason, Details, SourceID) are mandatory across all
// bindings and preserve identical values for the same error event —
// verified via conformance fixtures in spec/conformance/errors/.
//
// Go idiom: implements the error interface; callers use errors.As to
// retrieve the structural fields.
type Error struct {
	// Path is the dot-notation path where the error occurred (may be
	// empty for source-level errors).
	Path string

	// Reason is the stable enum value.
	Reason ErrorReason

	// Details is a human-readable message; not part of the wire contract
	// (bindings may phrase it idiomatically for their language).
	Details string

	// SourceID is the ID of the ConfigSource that raised the error,
	// if applicable; empty for post-load errors.
	SourceID string

	// Wrapped is an optional underlying error (file open failure,
	// yaml parse error, etc.) — retrievable via errors.Unwrap.
	Wrapped error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("config: %s at %q: %s", e.Reason, e.Path, e.Details)
	}
	return fmt.Sprintf("config: %s: %s", e.Reason, e.Details)
}

// Unwrap returns the wrapped underlying error for errors.Is / errors.As.
func (e *Error) Unwrap() error { return e.Wrapped }

// ErrNotImplemented is a sentinel returned from the Wrapped field of
// Phase A stub errors. Callers compare via errors.Is, not via equality.
//
// Phase B-D replaces stubs with real behaviour; consumers checking for
// this sentinel explicitly will then get the real error reasons.
var ErrNotImplemented = errors.New("config: not implemented in Phase A skeleton")

// notImplemented builds a fresh *Error for each stub call — no shared
// mutable singleton. Reason=source_unavailable carries Phase A semantics
// (see spec/_meta/error_reasons.yaml); Wrapped enables errors.Is check
// against ErrNotImplemented.
//
// TODO(phase-b): replace with real behaviour when method bodies land.
func notImplemented(path string) *Error {
	return &Error{
		Path:    path,
		Reason:  ReasonSourceUnavailable,
		Details: "not implemented in Phase A skeleton",
		Wrapped: ErrNotImplemented,
	}
}
