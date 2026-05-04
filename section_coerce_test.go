// Native unit tests for §4.4 env-string coercion (v2.1) + §4.5 Path preservation.
// These scenarios cover v2.1 conformance fixtures
// (runner_extension_required) that TestConformance skips until the
// runner protocol is extended.

package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.dagstack.dev/config"
)

// ── §4.4 env-string coercion in int/number/bool fields ─────────────

func TestGetSection_EnvStringsCoerceToInt(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(yamlPath, []byte(`database:
  host: "${DB_HOST:-localhost}"
  port: "${DB_PORT:-5432}"
  pool_size: "${DB_POOL:-20}"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DAGSTACK_ENV", "")
	cfg, err := config.Load(context.Background(), yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	type Database struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		PoolSize int    `yaml:"pool_size"`
	}

	var db Database
	if err := cfg.GetSection("database", &db); err != nil {
		t.Fatalf("GetSection: %v (env-strings must coerce to int per §4.4)", err)
	}
	if db.Host != "localhost" || db.Port != 5432 || db.PoolSize != 20 {
		t.Errorf("decoded fields mismatch: %+v", db)
	}
}

func TestGetSection_EnvStringsCoerceToFloat(t *testing.T) {
	cfg, err := config.LoadFrom(context.Background(), []config.Source{
		config.NewDictSource(config.Tree{
			"cache": map[string]any{
				"max_size_mb": "64",
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	type Cache struct {
		MaxSizeMB int `yaml:"max_size_mb"`
	}

	var cache Cache
	if err := cfg.GetSection("cache", &cache); err != nil {
		t.Fatalf("GetSection: %v", err)
	}
	if cache.MaxSizeMB != 64 {
		t.Errorf("MaxSizeMB = %v, want 64", cache.MaxSizeMB)
	}
}

func TestGetSection_EnvStringsCoerceToBool(t *testing.T) {
	cfg, err := config.LoadFrom(context.Background(), []config.Source{
		config.NewDictSource(config.Tree{
			"feature": map[string]any{
				"enabled": "true",
				"beta":    "yes",
				"legacy":  "0",
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	type Feature struct {
		Enabled bool `yaml:"enabled"`
		Beta    bool `yaml:"beta"`
		Legacy  bool `yaml:"legacy"`
	}

	var f Feature
	if err := cfg.GetSection("feature", &f); err != nil {
		t.Fatalf("GetSection: %v", err)
	}
	if !f.Enabled || !f.Beta || f.Legacy {
		t.Errorf("Feature = %+v", f)
	}
}

// ── §4.4 M1 Reverse case: native int/float/bool → string field = type_mismatch

func TestGetSection_NativeIntIntoStringField_TypeMismatch(t *testing.T) {
	cfg, err := config.LoadFrom(context.Background(), []config.Source{
		config.NewDictSource(config.Tree{
			"database": map[string]any{
				"name": 42, // native int → must be rejected against `string` field
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	type Database struct {
		Name string `yaml:"name"`
	}

	var db Database
	err = cfg.GetSection("database", &db)
	if err == nil {
		t.Fatal("expected error for native int into string field")
	}

	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *config.Error, got %T: %v", err, err)
	}
	if cfgErr.Reason != config.ReasonTypeMismatch {
		t.Errorf("Reason = %q, want %q (reverse coerce per §4.4 M1)",
			cfgErr.Reason, config.ReasonTypeMismatch)
	}
	// §4.5 Path preservation: full dot-notation section.field.
	if cfgErr.Path != "database.name" {
		t.Errorf("Path = %q, want %q (§4.5 Path preservation)", cfgErr.Path, "database.name")
	}
}

func TestGetSection_NativeBoolIntoStringField_TypeMismatch(t *testing.T) {
	cfg, err := config.LoadFrom(context.Background(), []config.Source{
		config.NewDictSource(config.Tree{
			"api": map[string]any{
				"mode": true,
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	type API struct {
		Mode string `yaml:"mode"`
	}

	var api API
	err = cfg.GetSection("api", &api)
	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) || cfgErr.Reason != config.ReasonTypeMismatch {
		t.Fatalf("expected TypeMismatch for bool→string, got %v", err)
	}
}

// ── §4.1 SourceIDs() method ─────────────────────────────────────────

func TestSourceIDs_ReturnsIdsInLoadOrder(t *testing.T) {
	s1 := config.NewDictSource(config.Tree{"a": 1}).WithID("dict:s1")
	s2 := config.NewDictSource(config.Tree{"b": 2}).WithID("dict:s2")

	cfg, err := config.LoadFrom(context.Background(), []config.Source{s1, s2})
	if err != nil {
		t.Fatal(err)
	}

	got := cfg.SourceIDs()
	want := []string{"dict:s1", "dict:s2"}
	if len(got) != len(want) {
		t.Fatalf("len(SourceIDs) = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("SourceIDs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSourceIDs_ReturnsCopy(t *testing.T) {
	cfg, err := config.LoadFrom(context.Background(), []config.Source{
		config.NewDictSource(config.Tree{}).WithID("x"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := cfg.SourceIDs()
	ids[0] = "mutation"
	if got := cfg.SourceIDs(); got[0] != "x" {
		t.Errorf("SourceIDs mutable — got %q after external mutation, want %q", got[0], "x")
	}
}
