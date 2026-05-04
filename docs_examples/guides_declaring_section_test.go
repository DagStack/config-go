// Automated tests for `config-docs/site/docs/guides/declaring-section.mdx`
// (Go TabItem).

package docs_examples_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.dagstack.dev/config"
)

// ── Step 2. Write the schema ────────────────────────────────────────

// --- snippet start (schema from Step 2) -----------------------------
// DatabaseConfig is the schema for the database section.
type DatabaseConfigSchema struct {
	Host     string `yaml:"host" validate:"required,ne=0.0.0.0"`
	Port     int    `yaml:"port" validate:"min=1,max=65535"`
	Name     string `yaml:"name" validate:"required"`
	User     string `yaml:"user" validate:"required"`
	Password string `yaml:"password" validate:"required,min=1"`
	PoolSize int    `yaml:"pool_size" validate:"min=1,max=1000"`
	SSL      bool   `yaml:"ssl"`
}

func defaultDatabaseConfig() DatabaseConfigSchema {
	return DatabaseConfigSchema{
		Port:     5432,
		PoolSize: 20,
		SSL:      false,
	}
}

// CacheConfig is a minimal schema for the Step 4 Isolation test.
type CacheConfigSchema struct {
	URL    string `yaml:"url"`
	TTLMin int    `yaml:"ttl_min"`
}

// --- snippet end (schema from Step 2) -------------------------------

func writeFullConfigForDeclaring(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "app-config.yaml")
	mustWrite(t, path, `database:
  host: localhost
  port: 5432
  name: orders
  user: app
  password: test-pw
  pool_size: 20

cache:
  url: redis://localhost:6379/0
  ttl_min: 15
`)
	return path
}

// ── Step 3. Read the section ────────────────────────────────────────

func TestDeclaringSection_GetSection(t *testing.T) {
	cfgPath := writeFullConfigForDeclaring(t)

	// --- snippet start -----------------------------------------------
	cfg, _ := config.Load(context.Background(), cfgPath)
	dbCfg := defaultDatabaseConfig()
	if err := cfg.GetSection("database", &dbCfg); err != nil {
		t.Fatalf("GetSection: %v", err)
	}
	// --- snippet end -------------------------------------------------

	if dbCfg.Host != "localhost" {
		t.Errorf("Host = %q, want localhost", dbCfg.Host)
	}
	if dbCfg.PoolSize != 20 {
		t.Errorf("PoolSize = %d, want 20", dbCfg.PoolSize)
	}
}

// ── Step 4. Isolation ───────────────────────────────────────────────

func TestDeclaringSection_Isolation(t *testing.T) {
	cfgPath := writeFullConfigForDeclaring(t)
	cfg, _ := config.Load(context.Background(), cfgPath)

	// --- snippet start -----------------------------------------------
	// Correct — inside the DB service:
	var dbCfg DatabaseConfigSchema
	_ = cfg.GetSection("database", &dbCfg)

	// Incorrect — the DB service reads someone else's section:
	var cacheCfg CacheConfigSchema
	_ = cfg.GetSection("cache", &cacheCfg)
	// The DB service now depends on the structure of cache.
	// --- snippet end -------------------------------------------------

	// Both calls work — the docs only warn against cross-section reads,
	// they do not forbid them at the API level.
	if dbCfg.Host != "localhost" {
		t.Errorf("dbCfg.Host = %q, want localhost", dbCfg.Host)
	}
	if cacheCfg.URL != "redis://localhost:6379/0" {
		t.Errorf("cacheCfg.URL = %q, want redis://localhost:6379/0", cacheCfg.URL)
	}
}

// ── Step 5. Defaults in the schema ──────────────────────────────────

func TestDeclaringSection_DefaultsInSchema(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "app-config.yaml")
	// YAML contains only the required fields.
	if err := os.WriteFile(cfgPath, []byte(`database:
  host: localhost
  user: app
  password: pw
  name: test
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// --- snippet start -----------------------------------------------
	// In Go defaults are set by populating the zero-value struct BEFORE GetSection.
	// GetSection only fills fields present in the YAML — the rest keep
	// their initial values.
	dbCfg := DatabaseConfigSchema{
		Port:     5432,
		PoolSize: 20,
		SSL:      false,
	}

	cfg, _ := config.Load(context.Background(), cfgPath)
	_ = cfg.GetSection("database", &dbCfg)
	// host/user/password are required (validated externally or via
	// go-playground/validator struct tags).
	// --- snippet end -------------------------------------------------

	// Defaults applied (fields absent from YAML):
	if dbCfg.Port != 5432 {
		t.Errorf("Port = %d, want 5432 (default)", dbCfg.Port)
	}
	if dbCfg.PoolSize != 20 {
		t.Errorf("PoolSize = %d, want 20 (default)", dbCfg.PoolSize)
	}
	if dbCfg.SSL != false {
		t.Errorf("SSL = %v, want false (default)", dbCfg.SSL)
	}
	// Required fields — from YAML:
	if dbCfg.Host != "localhost" {
		t.Errorf("Host = %q, want localhost", dbCfg.Host)
	}
}
