// Automated tests for `config-docs/site/docs/guides/testing.mdx`
// (Go TabItem).

package docs_examples_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.dagstack.dev/config"
)

// DatabaseConfigForTesting mirrors the schema from guides/declaring-section.
// In the docs snippet it is imported from src/database.
type DatabaseConfigForTesting struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Name     string `yaml:"name"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	PoolSize int    `yaml:"pool_size"`
}

// DatabasePool is a minimal implementation. Only Size is needed for assertions.
type DatabasePool struct {
	Size int
}

func NewDatabasePool(cfg DatabaseConfigForTesting) *DatabasePool {
	return &DatabasePool{Size: cfg.PoolSize}
}

// ── "Unit tests — inline config via DictSource" ────────────────────

func TestPoolUsesConfiguredSize(t *testing.T) {
	// --- snippet start -----------------------------------------------
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{
		config.NewDictSource(config.Tree{
			"database": map[string]any{
				"host":      "localhost",
				"port":      5432,
				"name":      "test",
				"user":      "app",
				"password":  "test-pw",
				"pool_size": 42,
			},
		}),
	})
	var dbCfg DatabaseConfigForTesting
	_ = cfg.GetSection("database", &dbCfg)

	pool := NewDatabasePool(dbCfg)
	if pool.Size != 42 {
		t.Fatalf("expected size 42, got %v", pool.Size)
	}
	// --- snippet end -------------------------------------------------
}

// ── "File-based test in a temporary directory" — env interpolation ──

func TestEnvInterpolation(t *testing.T) {
	// --- snippet start -----------------------------------------------
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "app-config.yaml")
	if err := os.WriteFile(yamlPath, []byte(`database:
  host: "${DB_HOST:-localhost}"
  password: "${DB_PASSWORD}"
  name: "test_db"
  user: "app"
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Setenv("DB_PASSWORD", "test-pw")
	cfg, _ := config.Load(context.Background(), yamlPath)
	got, _ := cfg.GetString("database.password")
	if got != "test-pw" {
		t.Fatal(got)
	}
	// --- snippet end -------------------------------------------------

	// Additional check for the default value.
	host, _ := cfg.GetString("database.host")
	if host != "localhost" {
		t.Errorf("host = %q, want localhost (default)", host)
	}
}

// ── "Integration tests with DAGSTACK_ENV" ──────────────────────────

func TestProductionOverridesBase(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "app-config.yaml"), `
database:
  pool_size: 20
  host: "localhost"
  name: "test"
  user: "app"
  password: "pw"
`)
	mustWrite(t, filepath.Join(dir, "app-config.production.yaml"), `
database:
  pool_size: 100
`)

	// --- snippet start -----------------------------------------------
	t.Setenv("DAGSTACK_ENV", "production")
	cfg, _ := config.Load(context.Background(), filepath.Join(dir, "app-config.yaml"))
	got, _ := cfg.GetInt("database.pool_size")
	if got != 100 {
		t.Fatalf("expected 100, got %d", got)
	}
	// --- snippet end -------------------------------------------------
}
