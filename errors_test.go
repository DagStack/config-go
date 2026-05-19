package config_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.dagstack.dev/config"
)

func TestErrorStringIncludesReasonAndPath(t *testing.T) {
	e := &config.Error{
		Path:    "database.password",
		Reason:  config.ReasonValidationFailed,
		Details: "value too short",
	}
	got := e.Error()
	for _, want := range []string{"validation_failed", "database.password", "too short"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q, want contains %q", got, want)
		}
	}
}

func TestErrorStringOmitsPathWhenEmpty(t *testing.T) {
	e := &config.Error{
		Reason:  config.ReasonSourceUnavailable,
		Details: "file not found",
	}
	got := e.Error()
	if strings.Contains(got, `""`) {
		t.Errorf("Error() = %q, should not render empty-path quotes", got)
	}
}

func TestErrorUnwrap(t *testing.T) {
	inner := errors.New("yaml syntax error at line 3")
	e := &config.Error{
		Reason:  config.ReasonParseError,
		Details: "failed to parse",
		Wrapped: inner,
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is failed to traverse Wrapped")
	}
}

func TestErrNotImplementedIsSentinel(t *testing.T) {
	// ErrNotImplemented is a plain sentinel (errors.New), not an
	// *Error. It remains as a hook for third-party code that needs
	// to check whether a given error originated from a Phase-A stub
	// during incremental binding development.
	var target *config.Error
	if errors.As(config.ErrNotImplemented, &target) {
		t.Error("ErrNotImplemented is a plain sentinel; errors.As(*Error) must not extract")
	}
}

func TestZeroConfigReturnsMissing(t *testing.T) {
	// On a zero-value Config (no sources loaded), lookup of any
	// path returns *Error with Reason=ReasonMissing. This replaces
	// the Phase-A "not implemented" stub behaviour — Phase C
	// actually navigates and finds the absent key.
	cfg := &config.Config{}
	_, err := cfg.GetString("any.path")

	var target *config.Error
	if !errors.As(err, &target) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if target.Reason != config.ReasonMissing {
		t.Errorf("Reason = %q, want %q", target.Reason, config.ReasonMissing)
	}
}

func TestErrorAsThroughPercentWChain(t *testing.T) {
	// Consumers wrap config errors via fmt.Errorf("...: %w", cfgErr)
	// and must still be able to extract *Error via errors.As.
	// Regression guard for Go wire-contract.
	orig := &config.Error{
		Reason:  config.ReasonMissing,
		Path:    "database.password",
		Details: "key not set",
	}
	wrapped := fmt.Errorf("loading database config: %w", orig)

	var target *config.Error
	if !errors.As(wrapped, &target) {
		t.Fatal("errors.As failed to extract *Error through %w chain")
	}
	if target.Reason != config.ReasonMissing {
		t.Errorf("target.Reason = %q, want %q", target.Reason, config.ReasonMissing)
	}
	if target.Path != "database.password" {
		t.Errorf("target.Path = %q, want %q", target.Path, "database.password")
	}
}

func TestConfigCloseIdempotent(t *testing.T) {
	cfg := &config.Config{}
	for i := 0; i < 3; i++ {
		if err := cfg.Close(); err != nil {
			t.Errorf("close call %d: %v", i, err)
		}
	}
	// Nil-receiver Close must not panic (Phase A invariant).
	var nilCfg *config.Config
	_ = nilCfg // we deliberately do not call nil-receiver Close here — once
	//  Phase B introduces real state, that check should be added.
}

func TestReasonWireValues(t *testing.T) {
	// Spec ADR-0001 §4.5 / _meta/error_reasons.yaml fixes the wire
	// strings. A regression here breaks cross-binding compatibility.
	cases := map[config.ErrorReason]string{
		config.ReasonMissing:           "missing",
		config.ReasonTypeMismatch:      "type_mismatch",
		config.ReasonEnvUnresolved:     "env_unresolved",
		config.ReasonValidationFailed:  "validation_failed",
		config.ReasonParseError:        "parse_error",
		config.ReasonSourceUnavailable: "source_unavailable",
		config.ReasonReloadRejected:    "reload_rejected",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("ErrorReason wire value: got %q, want %q", got, want)
		}
	}
}
