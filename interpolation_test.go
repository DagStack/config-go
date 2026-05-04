package config

import (
	"errors"
	"testing"
)

// These tests live in package `config` (internal), not `config_test` —
// interpolateTree / interpolateString are unexported implementation
// helpers. Public behaviour is exercised indirectly via Config.Load
// once Phase C lands.

func TestInterpolateStringBasic(t *testing.T) {
	getenv := func(k string) (string, bool) {
		switch k {
		case "FOO":
			return "bar", true
		case "EMPTY":
			return "", true
		}
		return "", false
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no placeholders", "hello world", "hello world"},
		{"simple var", "value=${FOO}", "value=bar"},
		{"multiple vars", "${FOO}_${FOO}", "bar_bar"},
		{"default used when unset", "${MISSING:-fallback}", "fallback"},
		{"default used when empty", "${EMPTY:-fallback}", "fallback"},
		{"env wins over default", "${FOO:-fallback}", "bar"},
		{"dollar-dollar escape", "price: $$100", "price: $100"},
		{"mixed escape and var", "$$ ${FOO}", "$ bar"},
		{"empty default", "${MISSING:-}", ""},
		{"default with spaces", "${MISSING:- a b }", " a b "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := interpolateString(c.in, getenv)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("interpolateString(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestInterpolateStringUnresolvedError(t *testing.T) {
	getenv := func(string) (string, bool) { return "", false }

	_, err := interpolateString("${MISSING}", getenv)
	if err == nil {
		t.Fatal("expected error for unresolved var, got nil")
	}
	if !contains(err.Error(), "MISSING") {
		t.Errorf("error message should mention variable name, got %q", err.Error())
	}
}

func TestInterpolateStringEmptySetVarReturnsEmpty(t *testing.T) {
	// Spec §2: bare `${VAR}` with VAR="" returns "". Only the
	// `${VAR:-default}` form treats empty-set as unset. Regression
	// guard — Phase B originally conflated the two.
	getenv := func(k string) (string, bool) {
		if k == "EMPTY" {
			return "", true
		}
		return "", false
	}

	got, err := interpolateString("prefix-${EMPTY}-suffix", getenv)
	if err != nil {
		t.Fatalf("empty-set var must not error: %v", err)
	}
	if got != "prefix--suffix" {
		t.Errorf("got %q, want %q", got, "prefix--suffix")
	}
}

func TestInterpolateStringNestedDefaultsNotExpanded(t *testing.T) {
	// spec §2: "Nested ${...} inside defaults are not interpolated —
	// kept literal to keep the parser simple."
	getenv := func(k string) (string, bool) {
		if k == "BAR" {
			return "actual", true
		}
		return "", false
	}

	got, err := interpolateString("${FOO:-${BAR}}", getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Regex default group `[^}]*` stops at the first `}`, so the
	// recognised default value is the literal "${BAR" (without the
	// trailing `}`). The outer substitution consumes `${FOO:-${BAR}`;
	// the remaining `}` passes through verbatim. BAR is never looked
	// up — proving nested ${...} is NOT re-expanded.
	if got != "${BAR}" {
		t.Errorf("got %q, want %q (nested ${...} in default is NOT re-expanded)", got, "${BAR}")
	}
}

func TestInterpolateStringLowercaseNotExpanded(t *testing.T) {
	// Portable regex only matches uppercase — lowercase is intentional
	// no-op (spec §2 rationale).
	getenv := func(k string) (string, bool) {
		if k == "foo" {
			return "SHOULD_NOT_APPEAR", true
		}
		return "", false
	}

	got, err := interpolateString("${foo}", getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "${foo}" {
		t.Errorf("lowercase var was expanded to %q, expected literal passthrough", got)
	}
}

func TestInterpolateTreeWalksMapsAndSlices(t *testing.T) {
	getenv := func(k string) (string, bool) {
		env := map[string]string{"HOST": "example.com", "PORT": "8080"}
		v, ok := env[k]
		return v, ok
	}

	in := Tree{
		"database": Tree{
			"host": "${HOST}",
			"port": "${PORT}",
			"name": "app",
		},
		"endpoints": []any{
			"https://${HOST}/a",
			"https://${HOST}/b",
		},
		"features": []any{true, 42, nil},
	}

	out, err := interpolateTree(in, getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	db := out["database"].(Tree)
	if db["host"] != "example.com" {
		t.Errorf("database.host = %v, want %q", db["host"], "example.com")
	}
	if db["port"] != "8080" {
		t.Errorf("database.port = %v, want %q", db["port"], "8080")
	}

	endpoints := out["endpoints"].([]any)
	if endpoints[0] != "https://example.com/a" {
		t.Errorf("endpoints[0] = %v, want %q", endpoints[0], "https://example.com/a")
	}

	// Non-string scalars pass through unchanged.
	features := out["features"].([]any)
	if features[0] != true || features[1] != 42 || features[2] != nil {
		t.Errorf("non-string scalars altered: %+v", features)
	}

	// Input tree must not have been mutated.
	if in["database"].(Tree)["host"] != "${HOST}" {
		t.Error("input tree was mutated — interpolateTree must not touch inputs")
	}
}

func TestInterpolateTreeKeysNotInterpolated(t *testing.T) {
	getenv := func(k string) (string, bool) {
		if k == "PREFIX" {
			return "expanded", true
		}
		return "", false
	}

	in := Tree{"${PREFIX}_host": "value"}
	out, err := interpolateTree(in, getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := out["${PREFIX}_host"]; !ok {
		t.Errorf("key was interpolated; got keys %v", keysOf(out))
	}
}

func TestInterpolateTreeReportsPathOnError(t *testing.T) {
	getenv := func(string) (string, bool) { return "", false }

	in := Tree{
		"database": Tree{
			"host": "${MISSING}",
		},
	}

	_, err := interpolateTree(in, getenv)
	if err == nil {
		t.Fatal("expected error for unresolved var")
	}

	var cfgErr *Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error is not *Error: %T", err)
	}
	if cfgErr.Reason != ReasonEnvUnresolved {
		t.Errorf("reason = %q, want %q", cfgErr.Reason, ReasonEnvUnresolved)
	}
	if cfgErr.Path != "database.host" {
		t.Errorf("path = %q, want %q", cfgErr.Path, "database.host")
	}
}

func TestInterpolateTreeSliceIndexInPath(t *testing.T) {
	getenv := func(string) (string, bool) { return "", false }

	in := Tree{
		"servers": []any{
			"ok",
			"${MISSING}",
			"ok",
		},
	}

	_, err := interpolateTree(in, getenv)
	var cfgErr *Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error is not *Error: %v", err)
	}
	if cfgErr.Path != "servers[1]" {
		t.Errorf("path = %q, want %q", cfgErr.Path, "servers[1]")
	}
}

func TestInterpolateTreeNilInputReturnsEmptyTree(t *testing.T) {
	out, err := interpolateTree(nil, nil)
	if err != nil {
		t.Fatalf("interpolateTree(nil, nil): %v", err)
	}
	if out == nil || len(out) != 0 {
		t.Errorf("got %v, want empty Tree", out)
	}
}

func TestInterpolateTreeUsesOsLookupWhenGetenvNil(t *testing.T) {
	// Default env resolver (os.LookupEnv) is exercised when the
	// caller passes nil. t.Setenv restores the variable automatically
	// at test end.
	t.Setenv("CONFIG_GO_TEST_VAR", "from-os")

	out, err := interpolateTree(Tree{"host": "${CONFIG_GO_TEST_VAR}"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["host"] != "from-os" {
		t.Errorf("host = %v, want %q (os.LookupEnv fallback broken)",
			out["host"], "from-os")
	}
}

// helpers

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0
}
func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
func keysOf(t Tree) []string {
	out := make([]string, 0, len(t))
	for k := range t {
		out = append(out, k)
	}
	return out
}
