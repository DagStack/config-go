// Automated tests for `config-docs/site/docs/concepts/sources.mdx`
// (Go TabItem).

package docs_examples_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.dagstack.dev/config"
)

// ── "Explicit source enumeration" — DictSource as a test override ──

func TestConceptsSources_LoadFromWithDictOverride(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "app-config.yaml")
	err := os.WriteFile(yamlPath, []byte(`database:
  host: "localhost"
  port: 5432
  name: "orders"
  user: "app"
  password: "test-pw"
  pool_size: 20
`), 0o644)
	if err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// --- snippet start -----------------------------------------------
	cfg, err := config.LoadFrom(context.Background(), []config.Source{
		config.NewYamlFileSource(yamlPath),
		config.NewDictSource(config.Tree{"database": map[string]any{"pool_size": 5}}),
	})
	// --- snippet end -------------------------------------------------
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	// The second source (DictSource) has the highest priority: it overrides
	// pool_size. The other fields stay sourced from the YAML.
	pool, _ := cfg.GetInt("database.pool_size")
	if pool != 5 {
		t.Errorf("pool_size = %d, want 5 (overridden by DictSource)", pool)
	}
	host, _ := cfg.GetString("database.host")
	if host != "localhost" {
		t.Errorf("host = %q, want localhost (from YAML)", host)
	}
	user, _ := cfg.GetString("database.user")
	if user != "app" {
		t.Errorf("user = %q, want app (from YAML)", user)
	}
}
