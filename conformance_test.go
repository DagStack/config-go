package config_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"go.dagstack.dev/config"
)

// TestConformance walks spec/conformance/manifest.yaml and runs every
// fixture against the Go binding. The runner obeys the contract in
// spec/conformance/runner.md. Fails if any fixture produces non-
// byte-identical output (happy path) or wrong Reason / Path (error
// case).
//
// Skips if spec/ submodule is not initialised — useful for local
// `go test` runs without `git submodule update`.
func TestConformance(t *testing.T) {
	specDir := "spec/conformance"
	if _, err := os.Stat(filepath.Join(specDir, "manifest.yaml")); errors.Is(err, os.ErrNotExist) {
		t.Skip("spec/ submodule not initialised; run `git submodule update --init`")
	}

	m := loadManifest(t, specDir)
	for _, tc := range m.Tests {
		t.Run(tc.ID, func(t *testing.T) {
			// v2.1 fixtures tagged runner_extension_required exercise
			// getter/getSection-level semantics, which runner v1.0 does
			// not model (only load-level). Binding covers those cases
			// in native unit tests (config_test.go, docs_examples/*).
			for _, tag := range tc.Tags {
				if tag == "runner_extension_required" {
					t.Skip("getter/getSection-level fixture — covered by binding-native unit tests until runner extension")
				}
				// ADR-0002 phase2_secrets_vault — gated on a live Vault dev server
				// (see spec/conformance/vault/docker-compose.yml + seed.sh).
				if tag == "phase2_secrets_vault" && os.Getenv("DAGSTACK_CONFORMANCE_VAULT_ADDR") == "" {
					t.Skip("phase2_secrets_vault fixtures require DAGSTACK_CONFORMANCE_VAULT_ADDR")
				}
			}
			runConformanceCase(t, specDir, tc)
		})
	}
}

// ── manifest + per-case runner ────────────────────────────────────

type conformanceManifest struct {
	Version string            `yaml:"version"`
	Tests   []conformanceTest `yaml:"tests"`
}

type conformanceTest struct {
	ID            string         `yaml:"id"`
	Description   string         `yaml:"description"`
	Tags          []string       `yaml:"tags"`
	Inputs        []string       `yaml:"inputs"`
	Env           *string        `yaml:"env"`
	Expected      string         `yaml:"expected"`
	ExpectedError *expectedError `yaml:"expected_error"`
}

type expectedError struct {
	Reason          string `yaml:"reason"`
	Path            string `yaml:"path"`
	SourceIDPattern string `yaml:"source_id_pattern"`
}

func loadManifest(t *testing.T, specDir string) *conformanceManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(specDir, "manifest.yaml"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m conformanceManifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if m.Version != "1.0" {
		t.Fatalf("unsupported manifest version %q (want 1.0)", m.Version)
	}
	return &m
}

func runConformanceCase(t *testing.T, specDir string, tc conformanceTest) {
	t.Helper()
	env := loadEnvFile(t, specDir, tc.Env)

	sources := make([]config.Source, 0, len(tc.Inputs))
	for _, rel := range tc.Inputs {
		path := filepath.Join(specDir, rel)
		sources = append(sources, config.NewYamlFileSourceWithEnv(path, env))
	}

	// ADR-0002 phase2_secrets — feed the EnvSecretSource (the `env`
	// scheme) from the fixture's env vector, NOT from os.environ.
	// phase2_secrets_vault — connect to dev-mode Vault seeded by
	// spec/conformance/vault/seed.sh.
	var loadOpts []config.LoadOption
	var secretSrcs []config.SecretSource
	for _, tag := range tc.Tags {
		switch tag {
		case "phase2_secrets":
			secretSrcs = append(secretSrcs, &config.EnvSecretSource{Lookup: env})
		case "phase2_secrets_vault":
			vs, vsErr := buildVaultSource(t)
			if vsErr != nil {
				t.Fatalf("Vault source build failed: %v", vsErr)
			}
			secretSrcs = append(secretSrcs, vs)
		}
	}
	if len(secretSrcs) > 0 {
		loadOpts = append(loadOpts, config.WithSecretSources(secretSrcs...))
	}

	cfg, loadErr := config.LoadFrom(context.Background(), sources, loadOpts...)

	if tc.ExpectedError != nil {
		assertExpectedError(t, loadErr, tc.ExpectedError)
		return
	}

	if loadErr != nil {
		t.Fatalf("happy-path fixture failed to load: %v", loadErr)
	}

	actual, err := config.CanonicalJSON(map[string]any(config.ResolvedTreeForTest(cfg)))
	if err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}

	expected, err := os.ReadFile(filepath.Join(specDir, tc.Expected))
	if err != nil {
		t.Fatalf("read expected: %v", err)
	}
	// Expected fixtures follow spec §9.1.1: no trailing newline.
	// Some editors add \n on save — trim if present for tolerance
	// with human-authored files (canonical JSON itself has none).
	expected = bytes.TrimRight(expected, "\n")

	if !bytes.Equal(actual, expected) {
		t.Errorf("canonical JSON mismatch\n  want: %s\n  got:  %s", expected, actual)
	}
}

// assertExpectedError verifies the loadErr matches the error case
// declared in manifest — reason is exact-match, path is exact-match
// (empty string means "error did not identify a path"), source_id_pattern
// is optional regex-like substring check (v1.0 simplification).
func assertExpectedError(t *testing.T, got error, want *expectedError) {
	t.Helper()
	if got == nil {
		t.Fatalf("expected error with reason=%q, got nil", want.Reason)
	}

	var cfgErr *config.Error
	if !errors.As(got, &cfgErr) {
		t.Fatalf("expected *config.Error, got %T: %v", got, got)
	}

	if string(cfgErr.Reason) != want.Reason {
		t.Errorf("reason mismatch: got %q, want %q (details: %s)",
			cfgErr.Reason, want.Reason, cfgErr.Details)
	}
	if cfgErr.Path != want.Path {
		t.Errorf("path mismatch: got %q, want %q", cfgErr.Path, want.Path)
	}
	if want.SourceIDPattern != "" && !strings.Contains(cfgErr.SourceID, want.SourceIDPattern) {
		t.Errorf("source_id %q does not contain pattern %q",
			cfgErr.SourceID, want.SourceIDPattern)
	}
}

// ── env file parsing ──────────────────────────────────────────────

// loadEnvFile reads an env file in KEY=value format and returns a
// Getenv closure. Comments (`#`-prefix) and blank lines are skipped.
// Returns nil when envPath is nil — which tells
// NewYamlFileSourceWithEnv to fall back to os.LookupEnv. Tests that
// expect "variable not set" must pass an empty-map closure, not nil.
func loadEnvFile(t *testing.T, specDir string, envPath *string) config.Getenv {
	t.Helper()
	if envPath == nil {
		// Fixture explicitly says "no env file". The runner contract
		// (runner.md) requires us to NOT pass the process environment
		// — otherwise developer's local $HOME / $PATH leak into
		// fixture interpolation. Return an empty-map closure.
		return emptyGetenv
	}

	raw, err := os.ReadFile(filepath.Join(specDir, *envPath))
	if err != nil {
		t.Fatalf("read env file %s: %v", *envPath, err)
	}

	env := make(map[string]string)
	for i, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.IndexByte(trimmed, '=')
		if eq < 0 {
			t.Fatalf("env file %s line %d: no '=' separator", *envPath, i+1)
		}
		key := strings.TrimSpace(trimmed[:eq])
		val := trimmed[eq+1:]
		env[key] = val
	}

	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

func emptyGetenv(string) (string, bool) { return "", false }

// ensure the dummy reference keeps compiler happy when nil-env branch
// is taken; also documents the contract.
var _ config.Getenv = emptyGetenv

// buildVaultSource constructs a Vault SecretSource for fixtures tagged
// phase2_secrets_vault. Spec ships docker-compose.yml + seed.sh under
// conformance/vault/ that operators run before invoking `go test`.
//
// The Vault adapter lives in the sub-module go.dagstack.dev/config/vault;
// to avoid pulling its dependencies into the main module's tests, the
// runner adapts via the SecretSource interface — the function builds a
// minimal vaultapi-free SecretSource that delegates resolution to the
// dev-mode HTTP API directly. For simpler local dev (and to avoid an
// import cycle with the sub-module), this lives in a separate test file
// in the binding.
//
// NOTE: production code should import go.dagstack.dev/config/vault and
// use vault.NewSource(...). The runner avoids that import to keep the
// main module's go.mod free of vault SDK transitive deps.
func buildVaultSource(t *testing.T) (config.SecretSource, error) {
	t.Helper()
	addr := os.Getenv("DAGSTACK_CONFORMANCE_VAULT_ADDR")
	if addr == "" {
		return nil, errors.New("DAGSTACK_CONFORMANCE_VAULT_ADDR not set")
	}
	token := os.Getenv("DAGSTACK_CONFORMANCE_VAULT_TOKEN")
	if token == "" {
		token = "conformance-root-token"
	}
	return &devVaultSource{addr: addr, token: token}, nil
}
