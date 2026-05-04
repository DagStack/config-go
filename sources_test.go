package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.dagstack.dev/config"
)

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

// ── YamlFileSource ─────────────────────────────────────────────────

func TestYamlFileSourceLoadsPlainYAML(t *testing.T) {
	path := writeTempFile(t, "app.yaml", `
database:
  host: localhost
  port: 5432
flags:
  - a
  - b
`)
	src := config.NewYamlFileSource(path)

	if src.ID() != "yaml:"+path {
		t.Errorf("ID() = %q, want %q", src.ID(), "yaml:"+path)
	}
	if src.Interpolate() {
		t.Error("YamlFileSource.Interpolate() must be false — interpolation is on raw text")
	}

	tree, err := src.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	db, ok := tree["database"].(map[string]any)
	if !ok {
		t.Fatalf("database node has wrong type %T", tree["database"])
	}
	if db["host"] != "localhost" {
		t.Errorf("host = %v", db["host"])
	}
	if db["port"] != 5432 {
		t.Errorf("port = %v (%T), want int 5432", db["port"], db["port"])
	}
}

func TestYamlFileSourceInterpolatesRawText(t *testing.T) {
	// Key property: interpolation runs BEFORE YAML decode — so
	// `${PORT}` with PORT=5432 becomes int, not string.
	path := writeTempFile(t, "app.yaml", `
database:
  host: ${DB_HOST:-localhost}
  port: ${DB_PORT:-5432}
`)
	src := config.NewYamlFileSourceWithEnv(path, func(k string) (string, bool) {
		if k == "DB_HOST" {
			return "example.com", true
		}
		return "", false
	})

	tree, err := src.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	db := tree["database"].(map[string]any)
	if db["host"] != "example.com" {
		t.Errorf("host = %v", db["host"])
	}
	if db["port"] != 5432 {
		t.Errorf("port = %v (%T), want int 5432 — raw-text interpolation must preserve YAML typing",
			db["port"], db["port"])
	}
}

func TestYamlFileSourceMissingFile(t *testing.T) {
	src := config.NewYamlFileSource("/nonexistent/path/app.yaml")
	_, err := src.Load(context.Background())

	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if cfgErr.Reason != config.ReasonSourceUnavailable {
		t.Errorf("Reason = %q, want %q", cfgErr.Reason, config.ReasonSourceUnavailable)
	}
	if !config.IsFileNotFound(err) {
		t.Error("IsFileNotFound must recognise the error")
	}
}

func TestYamlFileSourceInvalidYAML(t *testing.T) {
	path := writeTempFile(t, "broken.yaml", "foo: [not closed\n")
	src := config.NewYamlFileSource(path)
	_, err := src.Load(context.Background())

	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if cfgErr.Reason != config.ReasonParseError {
		t.Errorf("Reason = %q, want %q", cfgErr.Reason, config.ReasonParseError)
	}
}

func TestYamlFileSourceUnresolvedEnv(t *testing.T) {
	path := writeTempFile(t, "app.yaml", "host: ${MISSING_VAR_XYZ}\n")
	src := config.NewYamlFileSourceWithEnv(path, func(string) (string, bool) {
		return "", false
	})
	_, err := src.Load(context.Background())

	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *Error")
	}
	if cfgErr.Reason != config.ReasonEnvUnresolved {
		t.Errorf("Reason = %q, want %q", cfgErr.Reason, config.ReasonEnvUnresolved)
	}
}

func TestYamlFileSourceRejectsNonMappingRoot(t *testing.T) {
	path := writeTempFile(t, "list-root.yaml", "- item1\n- item2\n")
	src := config.NewYamlFileSource(path)
	_, err := src.Load(context.Background())

	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *Error")
	}
	if cfgErr.Reason != config.ReasonParseError {
		t.Errorf("Reason = %q, want %q", cfgErr.Reason, config.ReasonParseError)
	}
}

func TestYamlFileSourceEmptyFile(t *testing.T) {
	path := writeTempFile(t, "empty.yaml", "")
	src := config.NewYamlFileSource(path)
	tree, err := src.Load(context.Background())
	if err != nil {
		t.Fatalf("empty file must decode to empty tree, got error: %v", err)
	}
	if len(tree) != 0 {
		t.Errorf("tree = %v, want empty", tree)
	}
}

// ── JsonFileSource ─────────────────────────────────────────────────

func TestJsonFileSourceLoadsPlainJSON(t *testing.T) {
	path := writeTempFile(t, "app.json", `{"database":{"host":"localhost","port":5432}}`)
	src := config.NewJsonFileSource(path)

	tree, err := src.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	db := tree["database"].(map[string]any)
	if db["host"] != "localhost" {
		t.Errorf("host = %v", db["host"])
	}
	// JsonFileSource normalises whole-number float64 to int64 so
	// GetInt works uniformly across YAML / JSON.
	if db["port"] != int64(5432) {
		t.Errorf("port = %v (%T), want int64(5432)", db["port"], db["port"])
	}
}

func TestJsonFileSourceEmpty(t *testing.T) {
	path := writeTempFile(t, "empty.json", "  \n \t ")
	src := config.NewJsonFileSource(path)
	tree, err := src.Load(context.Background())
	if err != nil {
		t.Fatalf("whitespace-only JSON must decode to empty tree, got: %v", err)
	}
	if len(tree) != 0 {
		t.Errorf("tree = %v, want empty", tree)
	}
}

// ── DictSource ─────────────────────────────────────────────────────

func TestDictSourceReturnsCopyOfTree(t *testing.T) {
	original := config.Tree{"db": config.Tree{"host": "localhost"}}
	src := config.NewDictSource(original)

	tree, err := src.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Mutating the returned tree must not affect the stored one.
	tree["db"].(config.Tree)["host"] = "MUTATED"

	tree2, _ := src.Load(context.Background())
	if tree2["db"].(config.Tree)["host"] != "localhost" {
		t.Error("DictSource shared backing storage with caller")
	}
}

func TestDictSourceWithInterpolation(t *testing.T) {
	src := config.NewDictSource(config.Tree{"host": "${MY_HOST:-localhost}"}).
		WithInterpolation().
		WithID("dict:test")

	if !src.Interpolate() {
		t.Error("WithInterpolation should set interpolate=true")
	}
	if src.ID() != "dict:test" {
		t.Errorf("ID = %q, want %q", src.ID(), "dict:test")
	}
}

func TestDictSourceContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	src := config.NewDictSource(config.Tree{"x": 1})
	_, err := src.Load(ctx)

	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *Error on cancelled context")
	}
	if cfgErr.Reason != config.ReasonSourceUnavailable {
		t.Errorf("Reason = %q, want %q", cfgErr.Reason, config.ReasonSourceUnavailable)
	}
}
