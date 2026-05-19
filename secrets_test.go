package config

import (
	"context"
	"errors"
	"testing"
)

// ── parseSecretRef ────────────────────────────────────────────────────

func TestParseSecretRefMinimalEnv(t *testing.T) {
	ref, err := parseSecretRef("env:OPENAI_API_KEY", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Scheme != "env" || ref.Path != "OPENAI_API_KEY" {
		t.Fatalf("unexpected ref: %+v", ref)
	}
	if ref.Default != nil {
		t.Fatalf("expected no default, got %v", *ref.Default)
	}
}

func TestParseSecretRefWithDefault(t *testing.T) {
	ref, err := parseSecretRef("env:VAR:-fallback", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Default == nil || *ref.Default != "fallback" {
		t.Fatalf("unexpected default: %v", ref.Default)
	}
}

func TestParseSecretRefFieldProjection(t *testing.T) {
	ref, err := parseSecretRef("vault:secret/db#password", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Path != "secret/db#password" {
		t.Fatalf("expected 'secret/db#password', got %q", ref.Path)
	}
}

func TestParseSecretRefQueryAndField(t *testing.T) {
	ref, err := parseSecretRef("vault:secret/db?version=3#password", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Path != "secret/db?version=3#password" {
		t.Fatalf("unexpected path: %q", ref.Path)
	}
}

func TestParseSecretRefDoubledHash(t *testing.T) {
	ref, err := parseSecretRef("vault:tag##v2/db", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Path != "tag#v2/db" {
		t.Fatalf("expected 'tag#v2/db', got %q", ref.Path)
	}
}

func TestParseSecretRefDoubledQuery(t *testing.T) {
	ref, err := parseSecretRef("vault:where??name=foo", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Path != "where?name=foo" {
		t.Fatalf("expected 'where?name=foo', got %q", ref.Path)
	}
}

func TestParseSecretRefDoubledColonDash(t *testing.T) {
	ref, err := parseSecretRef("vault:foo::-bar:-default", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Path != "foo:-bar" {
		t.Fatalf("expected 'foo:-bar', got %q", ref.Path)
	}
	if ref.Default == nil || *ref.Default != "default" {
		t.Fatalf("unexpected default: %v", ref.Default)
	}
}

func TestParseSecretRefPercentEncoded(t *testing.T) {
	ref, err := parseSecretRef("vault:secret/db?token=val%26with%3Dchars", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Path != "secret/db?token=val&with=chars" {
		t.Fatalf("expected percent-decoded path, got %q", ref.Path)
	}
}

func TestParseSecretRefUppercaseSchemeRejected(t *testing.T) {
	_, err := parseSecretRef("Vault:path", "")
	var cerr *Error
	if !errors.As(err, &cerr) || cerr.Reason != ReasonParseError {
		t.Fatalf("expected ParseError, got %v", err)
	}
}

func TestParseSecretRefMissingSchemeSeparator(t *testing.T) {
	_, err := parseSecretRef("envOPENAI_KEY", "")
	var cerr *Error
	if !errors.As(err, &cerr) || cerr.Reason != ReasonParseError {
		t.Fatalf("expected ParseError, got %v", err)
	}
}

// ── walkSecretRefs ────────────────────────────────────────────────────

func TestWalkSecretRefsConvertsScalar(t *testing.T) {
	out, err := walkSecretRefs(Tree{"k": "${secret:env:V}"}, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tree := out.(Tree)
	ref, ok := tree["k"].(SecretRef)
	if !ok {
		t.Fatalf("expected SecretRef, got %T", tree["k"])
	}
	if ref.Scheme != "env" || ref.Path != "V" {
		t.Fatalf("unexpected ref: %+v", ref)
	}
}

func TestWalkSecretRefsPlainStringPassthrough(t *testing.T) {
	out, err := walkSecretRefs(Tree{"k": "literal"}, "t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.(Tree)["k"] != "literal" {
		t.Fatalf("expected 'literal', got %v", out.(Tree)["k"])
	}
}

func TestWalkSecretRefsNestedRecurses(t *testing.T) {
	out, err := walkSecretRefs(
		Tree{"db": Tree{"pw": "${secret:env:PW}"}},
		"t",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tree := out.(Tree)["db"].(Tree)
	if _, ok := tree["pw"].(SecretRef); !ok {
		t.Fatalf("expected SecretRef in nested map, got %T", tree["pw"])
	}
}

func TestWalkSecretRefsTokenMixedRejected(t *testing.T) {
	_, err := walkSecretRefs(
		Tree{"k": "prefix ${secret:env:V} suffix"},
		"t",
	)
	var cerr *Error
	if !errors.As(err, &cerr) || cerr.Reason != ReasonParseError {
		t.Fatalf("expected ParseError, got %v", err)
	}
}

// ── EnvSecretSource ───────────────────────────────────────────────────

func TestEnvSecretSourceResolves(t *testing.T) {
	src := &EnvSecretSource{
		Lookup: func(name string) (string, bool) {
			if name == "K" {
				return "v", true
			}
			return "", false
		},
	}
	val, err := src.Resolve(context.Background(), "K")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val.Value != "v" {
		t.Fatalf("expected 'v', got %q", val.Value)
	}
}

func TestEnvSecretSourceMissingRaises(t *testing.T) {
	src := &EnvSecretSource{Lookup: func(string) (string, bool) { return "", false }}
	_, err := src.Resolve(context.Background(), "MISSING")
	var cerr *Error
	if !errors.As(err, &cerr) || cerr.Reason != ReasonSecretUnresolved {
		t.Fatalf("expected SecretUnresolved, got %v", err)
	}
}

func TestEnvSecretSourceRejectsField(t *testing.T) {
	src := &EnvSecretSource{Lookup: func(string) (string, bool) { return "v", true }}
	_, err := src.Resolve(context.Background(), "K#sub")
	var cerr *Error
	if !errors.As(err, &cerr) || cerr.Reason != ReasonSecretUnresolved {
		t.Fatalf("expected SecretUnresolved, got %v", err)
	}
}

func TestEnvSecretSourceRejectsQuery(t *testing.T) {
	src := &EnvSecretSource{Lookup: func(string) (string, bool) { return "v", true }}
	_, err := src.Resolve(context.Background(), "K?version=1")
	var cerr *Error
	if !errors.As(err, &cerr) || cerr.Reason != ReasonSecretUnresolved {
		t.Fatalf("expected SecretUnresolved, got %v", err)
	}
}

// ── End-to-end via LoadFrom ───────────────────────────────────────────

func TestLoadFromResolvesEnvScheme(t *testing.T) {
	src := NewDictSource(Tree{"k": "${secret:env:V}"})
	env := &EnvSecretSource{
		Lookup: func(name string) (string, bool) {
			if name == "V" {
				return "value", true
			}
			return "", false
		},
	}
	cfg, err := LoadFrom(
		context.Background(),
		[]Source{src},
		WithSecretSources(env),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	val, err := cfg.GetString("k")
	if err != nil {
		t.Fatalf("GetString error: %v", err)
	}
	if val != "value" {
		t.Fatalf("expected 'value', got %q", val)
	}
}

func TestLoadFromUsesDefaultWhenEnvMissing(t *testing.T) {
	src := NewDictSource(Tree{"k": "${secret:env:NO_SUCH:-fb}"})
	env := &EnvSecretSource{Lookup: func(string) (string, bool) { return "", false }}
	cfg, err := LoadFrom(
		context.Background(),
		[]Source{src},
		WithSecretSources(env),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	val, _ := cfg.GetString("k")
	if val != "fb" {
		t.Fatalf("expected 'fb', got %q", val)
	}
}

func TestLoadFromUnknownSchemeWithoutDefaultRaises(t *testing.T) {
	src := NewDictSource(Tree{"k": "${secret:vault:secret/db#pw}"})
	_, err := LoadFrom(context.Background(), []Source{src})
	var cerr *Error
	if !errors.As(err, &cerr) || cerr.Reason != ReasonSecretUnresolved {
		t.Fatalf("expected SecretUnresolved, got %v", err)
	}
}

func TestLoadFromUnknownSchemeWithDefaultLoads(t *testing.T) {
	src := NewDictSource(Tree{"k": "${secret:vault:secret/db#pw:-fb}"})
	cfg, err := LoadFrom(context.Background(), []Source{src})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	val, _ := cfg.GetString("k")
	if val != "fb" {
		t.Fatalf("expected 'fb', got %q", val)
	}
}

func TestLoadFromAutoRegistersEnv(t *testing.T) {
	src := NewDictSource(Tree{"k": "${secret:env:DEFINITELY_NOT_SET_XXX}"})
	_, err := LoadFrom(context.Background(), []Source{src})
	var cerr *Error
	if !errors.As(err, &cerr) || cerr.Reason != ReasonSecretUnresolved {
		t.Fatalf("expected SecretUnresolved, got %v", err)
	}
	if cerr.Details == "" || !contains(cerr.Details, "not set in the process environment") {
		t.Fatalf("expected env-not-set message, got %q", cerr.Details)
	}
}

func TestLoadFromDuplicateSchemeRaises(t *testing.T) {
	src := NewDictSource(Tree{})
	env1 := NewEnvSecretSource()
	env2 := NewEnvSecretSource()
	_, err := LoadFrom(
		context.Background(),
		[]Source{src},
		WithSecretSources(env1, env2),
	)
	var cerr *Error
	if !errors.As(err, &cerr) || cerr.Reason != ReasonValidationFailed {
		t.Fatalf("expected ValidationFailed, got %v", err)
	}
}

// ── Cache ─────────────────────────────────────────────────────────────

type countingEnv struct {
	calls []string
}

func (*countingEnv) Scheme() string { return "env" }
func (*countingEnv) ID() string     { return "test:counting" }
func (c *countingEnv) Resolve(_ context.Context, path string) (SecretValue, error) {
	c.calls = append(c.calls, path)
	return SecretValue{Value: "v-" + path, SourceID: c.ID()}, nil
}
func (*countingEnv) Close() error { return nil }

func TestLoadFromCacheHitsOnePerPath(t *testing.T) {
	src := NewDictSource(Tree{
		"a": "${secret:env:K}",
		"b": "${secret:env:K}",
	})
	env := &countingEnv{}
	cfg, err := LoadFrom(
		context.Background(),
		[]Source{src},
		WithSecretSources(env),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a, _ := cfg.GetString("a")
	b, _ := cfg.GetString("b")
	if a != "v-K" || b != "v-K" {
		t.Fatalf("unexpected resolved values: a=%q b=%q", a, b)
	}
	if len(env.calls) != 1 {
		t.Fatalf("expected 1 resolve call, got %d (%v)", len(env.calls), env.calls)
	}
}
