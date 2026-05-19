// Automated tests for the code snippets in `config-docs/site/docs/intro.mdx`
// (Go TabItem).

package docs_examples_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.dagstack.dev/config"
)

// Fixture YAML — an exact copy of the example in intro.mdx, the "Installation"
// section (the `app-config.yaml` block). ADR-0001 v2.1 §4.4: env-substituted
// strings are coerced to schema fields via the section walker before
// yaml.Unmarshal, so `port: "${DB_PORT:-5432}"` parses correctly into an
// `int` field.
const appConfigYAML = `app:
  name: "order-service"
  tagline: "Order processor"

database:
  host: "${DB_HOST:-localhost}"
  port: "${DB_PORT:-5432}"
  name: "${DB_NAME:-orders}"
  user: "${DB_USER}"
  password: "${DB_PASSWORD}"
  pool_size: 20

cache:
  url: "${REDIS_URL:-redis://localhost:6379/0}"
  ttl_min: 15

api:
  host: "0.0.0.0"
  port: 8080
  request_timeout_s: 30
`

func writeAppConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "app-config.yaml")
	if err := os.WriteFile(path, []byte(appConfigYAML), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// ── Section "Loading and reading" ───────────────────────────────────

func TestIntro_LoadAndRead(t *testing.T) {
	// Set env for DB_USER / DB_PASSWORD (no defaults in YAML).
	t.Setenv("DB_USER", "app")
	t.Setenv("DB_PASSWORD", "test-pw")
	t.Setenv("DAGSTACK_ENV", "") // disable the env layer

	cfgPath := writeAppConfig(t)

	// --- snippet start -----------------------------------------------
	// import (
	//     "context"
	//     "go.dagstack.dev/config"
	// )

	cfg, err := config.Load(context.Background(), cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	name, _ := cfg.GetString("app.name")        // "order-service"
	pool, _ := cfg.GetInt("database.pool_size") // 20
	port, _ := cfg.GetInt("api.port")           // 8080

	maxBody, _ := cfg.GetIntDefault("api.max_body_mb", 10) // 10
	// --- snippet end -------------------------------------------------

	if name != "order-service" {
		t.Errorf("app.name = %q, want order-service", name)
	}
	if pool != 20 {
		t.Errorf("database.pool_size = %d, want 20", pool)
	}
	if port != 8080 {
		t.Errorf("api.port = %d, want 8080", port)
	}
	if maxBody != 10 {
		t.Errorf("api.max_body_mb (default) = %d, want 10", maxBody)
	}
}

// ── Section "Typed access" ──────────────────────────────────────────

// DatabaseConfig is the schema shared across the Python/TS/Go TabItems.
type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Name     string `yaml:"name"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	PoolSize int    `yaml:"pool_size"`
}

func TestIntro_TypedSection(t *testing.T) {
	t.Setenv("DB_USER", "app")
	t.Setenv("DB_PASSWORD", "test-pw")
	t.Setenv("DAGSTACK_ENV", "")

	cfgPath := writeAppConfig(t)

	// --- snippet start -----------------------------------------------
	// type DatabaseConfig struct {
	//     Host     string `yaml:"host"`
	//     Port     int    `yaml:"port" validate:"min=1,max=65535"`
	//     Name     string `yaml:"name"`
	//     User     string `yaml:"user"`
	//     Password string `yaml:"password" validate:"required"`
	//     PoolSize int    `yaml:"pool_size" validate:"min=1,max=1000"`
	// }

	cfg, _ := config.Load(context.Background(), cfgPath)
	var db DatabaseConfig
	if err := cfg.GetSection("database", &db); err != nil {
		t.Fatalf("GetSection: %v", err)
	}
	// pool := createPool(db.Host, db.Port, db.PoolSize)
	//   ^^ createPool is a user-defined function in the snippet; commented out.
	// --- snippet end -------------------------------------------------

	// NB: go-playground/validator `validate` tags are NOT applied in v0.1 —
	// config-go performs yaml.Marshal+Unmarshal without a validation step.
	// This is a known drift that needs follow-up work in the binding.
	if db.Host != "localhost" {
		t.Errorf("db.Host = %q, want localhost", db.Host)
	}
	if db.Port != 5432 {
		t.Errorf("db.Port = %d, want 5432", db.Port)
	}
	if db.PoolSize != 20 {
		t.Errorf("db.PoolSize = %d, want 20", db.PoolSize)
	}
	if db.Password != "test-pw" {
		t.Errorf("db.Password = %q, want test-pw", db.Password)
	}
}
