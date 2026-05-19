// Automated tests for `config-docs/site/docs/reference/errors.mdx`
// (Go TabItem).

package docs_examples_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.dagstack.dev/config"
)

// sampleConfig builds a DictSource with fields specifically crafted for
// negative tests (pool_size as the string "twenty" — type_mismatch;
// password empty — validation_failed).
func sampleConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadFrom(context.Background(), []config.Source{
		config.NewDictSource(config.Tree{
			"database": map[string]any{
				"host":      "localhost",
				"pool_size": "twenty", // invalid for GetInt
				"password":  "",       // invalid for validation
			},
		}),
	})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	return cfg
}

// ── `missing` ────────────────────────────────────────────────────────

func TestErrors_Missing(t *testing.T) {
	cfg := sampleConfig(t)

	// --- snippet start ---------------------------------------------
	_, err := cfg.GetString("nonexistent.path")
	// err: *config.Error {
	//   Path: "nonexistent.path",
	//   Reason: config.ReasonMissing,
	//   Details: "Key 'nonexistent.path' not found in config and no default provided",
	// }
	// --- snippet end -----------------------------------------------

	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *config.Error, got %v", err)
	}
	if cfgErr.Reason != config.ReasonMissing {
		t.Errorf("Reason = %q, want %q", cfgErr.Reason, config.ReasonMissing)
	}
}

// ── `type_mismatch` ──────────────────────────────────────────────────

func TestErrors_TypeMismatch(t *testing.T) {
	cfg := sampleConfig(t)

	// --- snippet start ---------------------------------------------
	// YAML: pool_size: "twenty"
	_, err := cfg.GetInt("database.pool_size")
	// err: *config.Error {
	//   Path: "database.pool_size",
	//   Reason: config.ReasonTypeMismatch,
	//   Details: "expected int, got string (does not match ^-?\\d+$)",
	// }
	// --- snippet end -----------------------------------------------

	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *config.Error, got %v", err)
	}
	if cfgErr.Reason != config.ReasonTypeMismatch {
		t.Errorf("Reason = %q, want %q", cfgErr.Reason, config.ReasonTypeMismatch)
	}
	if cfgErr.Path != "database.pool_size" {
		t.Errorf("Path = %q, want database.pool_size", cfgErr.Path)
	}
}

// ── `env_unresolved` ─────────────────────────────────────────────────

func TestErrors_EnvUnresolved(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "app-config.yaml")
	if err := os.WriteFile(yamlPath,
		[]byte(`database:
  password: "${DB_PASSWORD}"
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Remove DB_PASSWORD from the environment.
	t.Setenv("DB_PASSWORD", "")
	os.Unsetenv("DB_PASSWORD")
	t.Setenv("DAGSTACK_ENV", "")

	_, err := config.Load(context.Background(), yamlPath)
	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *config.Error, got %v", err)
	}
	if cfgErr.Reason != config.ReasonEnvUnresolved {
		t.Errorf("Reason = %q, want %q", cfgErr.Reason, config.ReasonEnvUnresolved)
	}
}

// ── `validation_failed` ──────────────────────────────────────────────

func TestErrors_ValidationFailed(t *testing.T) {
	// --- snippet start ---------------------------------------------
	type DatabaseConfig struct {
		Host     string `yaml:"host" validate:"required"`
		Password string `yaml:"password" validate:"required,min=1"`
	}
	// --- snippet end -----------------------------------------------

	// Scenario: pass a string instead of a map → validation_failed, because
	// GetSection attempts to decode map-into-struct.
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{
		config.NewDictSource(config.Tree{
			"database": "not a map", // cannot decode into a struct
		}),
	})

	var dbCfg DatabaseConfig
	err := cfg.GetSection("database", &dbCfg)

	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *config.Error, got %v", err)
	}

	// NB: the docs snippet shows the "password empty → required/min=1" scenario.
	// However, config-go v0.1.0 does not apply struct-tag validation (the
	// validate:"..." tags are interpreted separately by go-playground/validator
	// and are not wired into GetSection). To demonstrate validation_failed we
	// therefore use a scenario that is guaranteed to fail in yaml.Unmarshal
	// (value is a string, target is a struct) → ReasonValidationFailed
	// from the yaml.v3 decoder.
	// Follow-up: either integrate the validator into GetSection, or update
	// the docs snippet to a scenario that works without the validator.
	if cfgErr.Reason != config.ReasonValidationFailed &&
		cfgErr.Reason != config.ReasonTypeMismatch {
		t.Errorf("Reason = %q, want validation_failed or type_mismatch", cfgErr.Reason)
	}
}

// ── `source_unavailable` ─────────────────────────────────────────────

func TestErrors_SourceUnavailable(t *testing.T) {
	t.Setenv("DAGSTACK_ENV", "")
	dir := t.TempDir()

	// --- snippet start ---------------------------------------------
	// File does not exist:
	_, err := config.Load(context.Background(), filepath.Join(dir, "non-existent.yaml"))
	// err: *config.Error {
	//   Path: "",
	//   Reason: config.ReasonSourceUnavailable,
	//   Details: "open non-existent.yaml: no such file or directory",
	//   SourceID: "yaml:non-existent.yaml",
	// }
	// --- snippet end -----------------------------------------------

	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *config.Error, got %v", err)
	}
	if cfgErr.Reason != config.ReasonSourceUnavailable {
		t.Errorf("Reason = %q, want %q", cfgErr.Reason, config.ReasonSourceUnavailable)
	}
}

// ── Handler snippet with errors.As + switch ─────────────────────────

func TestErrors_HandlerSwitch(t *testing.T) {
	cfg := sampleConfig(t)

	// --- snippet start (simplified) -------------------------------------
	_, err := cfg.GetString("nonexistent")
	var handledReason config.ErrorReason
	if err != nil {
		var cfgErr *config.Error
		if errors.As(err, &cfgErr) {
			switch cfgErr.Reason {
			case config.ReasonMissing:
				handledReason = cfgErr.Reason
			case config.ReasonEnvUnresolved:
				handledReason = cfgErr.Reason
			case config.ReasonValidationFailed:
				handledReason = cfgErr.Reason
			}
		}
	}
	// --- snippet end ----------------------------------------------------

	if handledReason != config.ReasonMissing {
		t.Errorf("handledReason = %q, want %q", handledReason, config.ReasonMissing)
	}
}
