// Automated tests for `config-docs/site/docs/concepts/layers.mdx`
// (Go TabItem).

package docs_examples_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.dagstack.dev/config"
)

// ── "Explicit layer enumeration" — LoadFrom with three YamlFileSources ───

func TestConceptsLayers_ExplicitPaths(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	if err := os.Mkdir(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, filepath.Join(cfgDir, "base.yaml"),
		"database:\n  host: base-host\n  pool_size: 10\n")
	mustWrite(t, filepath.Join(cfgDir, "integration-test.yaml"),
		"database:\n  pool_size: 3\n")
	mustWrite(t, filepath.Join(cfgDir, "secrets-ci.yaml"),
		"database:\n  password: ci-secret\n")

	// --- snippet start -----------------------------------------------
	cfg, err := config.LoadFrom(context.Background(), []config.Source{
		config.NewYamlFileSource(filepath.Join(cfgDir, "base.yaml")),
		config.NewYamlFileSource(filepath.Join(cfgDir, "integration-test.yaml")),
		config.NewYamlFileSource(filepath.Join(cfgDir, "secrets-ci.yaml")),
	})
	// Order determines priority; DAGSTACK_ENV is not applied.
	// --- snippet end -------------------------------------------------
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	host, _ := cfg.GetString("database.host")
	if host != "base-host" {
		t.Errorf("host = %q, want base-host (from base)", host)
	}
	pool, _ := cfg.GetInt("database.pool_size")
	if pool != 3 {
		t.Errorf("pool_size = %d, want 3 (from integration-test)", pool)
	}
	password, _ := cfg.GetString("database.password")
	if password != "ci-secret" {
		t.Errorf("password = %q, want ci-secret (from secrets-ci)", password)
	}
}

// ── "How to discover which layers were applied" — SourceIDs() ─────

func TestConceptsLayers_SourceIDsDiagnostic(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "app-config.yaml")
	mustWrite(t, yamlPath, "only: me\n")

	// Disable the env layer so SourceIDs contains only the base.
	t.Setenv("DAGSTACK_ENV", "")

	cfg, err := config.Load(context.Background(), yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// --- snippet start -----------------------------------------------
	// fmt.Println(cfg.SourceIDs())
	// → [yaml:app-config.yaml yaml:app-config.local.yaml
	//    yaml:app-config.production.yaml]
	// --- snippet end -------------------------------------------------

	// This test uses only base, without local/production.
	ids := cfg.SourceIDs()
	if len(ids) != 1 {
		t.Fatalf("SourceIDs len = %d, want 1", len(ids))
	}
	if ids[0] == "" {
		t.Errorf("SourceIDs[0] is empty")
	}
}

// mustWrite is a tiny helper for the write+fatal pattern.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
