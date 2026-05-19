package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.dagstack.dev/config"
)

// ── LoadFrom end-to-end ────────────────────────────────────────────

func TestLoadFromMergesSourcesInPriorityOrder(t *testing.T) {
	// Second source overrides first; third overrides both.
	base := config.NewDictSource(config.Tree{
		"db": config.Tree{
			"host": "base",
			"port": 5432,
			"pool": 10,
		},
	}).WithID("dict:base")

	overlay := config.NewDictSource(config.Tree{
		"db": config.Tree{
			"host": "overlay",
			"pool": 20,
		},
	}).WithID("dict:overlay")

	cfg, err := config.LoadFrom(context.Background(), []config.Source{base, overlay})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	host, _ := cfg.GetString("db.host")
	if host != "overlay" {
		t.Errorf("host = %q, want overlay", host)
	}
	port, _ := cfg.GetInt("db.port")
	if port != 5432 {
		t.Errorf("port = %d, want 5432 (preserved from base)", port)
	}
	pool, _ := cfg.GetInt("db.pool")
	if pool != 20 {
		t.Errorf("pool = %d, want 20 (overlay)", pool)
	}
}

func TestLoadFromAppliesInterpolationFlag(t *testing.T) {
	t.Setenv("HOST", "example.com")
	src := config.NewDictSource(config.Tree{"host": "${HOST}"}).WithInterpolation()

	cfg, err := config.LoadFrom(context.Background(), []config.Source{src})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	host, _ := cfg.GetString("host")
	if host != "example.com" {
		t.Errorf("host = %q, want example.com", host)
	}
}

func TestLoadFromRequiresAtLeastOneSource(t *testing.T) {
	_, err := config.LoadFrom(context.Background(), nil)
	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) || cfgErr.Reason != config.ReasonSourceUnavailable {
		t.Errorf("expected source_unavailable for empty sources, got %v", err)
	}
}

// ── Load (file-based shortcut) ─────────────────────────────────────

func TestLoadReadsBaseFile(t *testing.T) {
	path := writeTempFile(t, "app.yaml", "service: base\n")
	cfg, err := config.Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	svc, _ := cfg.GetString("service")
	if svc != "base" {
		t.Errorf("service = %q, want base", svc)
	}
}

func TestLoadDiscoversLocalOverride(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "app.yaml")
	local := filepath.Join(dir, "app.local.yaml")

	_ = os.WriteFile(base, []byte("service: base\nport: 8080\n"), 0o600)
	_ = os.WriteFile(local, []byte("port: 9090\n"), 0o600)

	cfg, err := config.Load(context.Background(), base)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	port, _ := cfg.GetInt("port")
	if port != 9090 {
		t.Errorf("port = %d, want 9090 (from local)", port)
	}
	svc, _ := cfg.GetString("service")
	if svc != "base" {
		t.Errorf("service = %q, want base (preserved from base)", svc)
	}
}

func TestLoadDiscoversEnvOverride(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "app.yaml")
	production := filepath.Join(dir, "app.production.yaml")

	_ = os.WriteFile(base, []byte("service: base\nport: 8080\n"), 0o600)
	_ = os.WriteFile(production, []byte("port: 443\n"), 0o600)

	t.Setenv("DAGSTACK_ENV", "production")
	cfg, err := config.Load(context.Background(), base)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	port, _ := cfg.GetInt("port")
	if port != 443 {
		t.Errorf("port = %d, want 443 (from production override)", port)
	}
}

func TestLoadMissingBaseFails(t *testing.T) {
	_, err := config.Load(context.Background(), "/does/not/exist.yaml")
	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if cfgErr.Reason != config.ReasonSourceUnavailable {
		t.Errorf("Reason = %q, want %q", cfgErr.Reason, config.ReasonSourceUnavailable)
	}
}

// ── Get* methods ───────────────────────────────────────────────────

func TestGetStringVariants(t *testing.T) {
	src := config.NewDictSource(config.Tree{"name": "foo", "number": 42})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	if s, err := cfg.GetString("name"); err != nil || s != "foo" {
		t.Errorf("GetString(name) = %q, %v", s, err)
	}
	if _, err := cfg.GetString("number"); !isReason(err, config.ReasonTypeMismatch) {
		t.Errorf("GetString on int must give type_mismatch, got %v", err)
	}
	if _, err := cfg.GetString("missing"); !isReason(err, config.ReasonMissing) {
		t.Errorf("GetString missing must give missing, got %v", err)
	}
	if s, err := cfg.GetStringDefault("missing", "fallback"); err != nil || s != "fallback" {
		t.Errorf("GetStringDefault fallback: got %q, %v", s, err)
	}
}

func TestGetIntCoercions(t *testing.T) {
	src := config.NewDictSource(config.Tree{
		"native": 42,
		"string": "777",
		"bad":    "abc",
		"float":  3.14,
	})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	if n, err := cfg.GetInt("native"); err != nil || n != 42 {
		t.Errorf("GetInt(native) = %d, %v", n, err)
	}
	if n, err := cfg.GetInt("string"); err != nil || n != 777 {
		t.Errorf("GetInt(string): %d, %v", n, err)
	}
	if _, err := cfg.GetInt("bad"); !isReason(err, config.ReasonTypeMismatch) {
		t.Errorf("GetInt(bad): want type_mismatch, got %v", err)
	}
	if _, err := cfg.GetInt("float"); !isReason(err, config.ReasonTypeMismatch) {
		t.Errorf("GetInt on float: want type_mismatch (§4.3 requires ^-?\\d+$), got %v", err)
	}
	if n, err := cfg.GetIntDefault("missing", 10); err != nil || n != 10 {
		t.Errorf("GetIntDefault fallback: %d, %v", n, err)
	}
}

func TestGetBoolCoercions(t *testing.T) {
	src := config.NewDictSource(config.Tree{
		"native":  true,
		"yes":     "yes",
		"no":      "NO", // case-insensitive
		"one":     "1",
		"invalid": "maybe",
	})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	for _, p := range []string{"native", "yes", "one"} {
		b, err := cfg.GetBool(p)
		if err != nil || !b {
			t.Errorf("GetBool(%s) = %v, %v", p, b, err)
		}
	}
	b, _ := cfg.GetBool("no")
	if b {
		t.Errorf("GetBool(no) = true, want false")
	}
	if _, err := cfg.GetBool("invalid"); !isReason(err, config.ReasonTypeMismatch) {
		t.Errorf("GetBool(invalid) must give type_mismatch, got %v", err)
	}
	if b, err := cfg.GetBoolDefault("missing", true); err != nil || !b {
		t.Errorf("GetBoolDefault fallback: %v, %v", b, err)
	}
}

func TestGetNumberAcceptsIntAndFloat(t *testing.T) {
	src := config.NewDictSource(config.Tree{"int": 7, "float": 1.5, "string": "2.5"})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	for _, p := range []string{"int", "float", "string"} {
		v, err := cfg.GetNumber(p)
		if err != nil {
			t.Errorf("GetNumber(%s): %v", p, err)
		}
		if v <= 0 {
			t.Errorf("GetNumber(%s) = %v, want positive", p, v)
		}
	}
	if n, err := cfg.GetNumberDefault("missing", 3.14); err != nil || n != 3.14 {
		t.Errorf("GetNumberDefault fallback: %v, %v", n, err)
	}
}

func TestGetList(t *testing.T) {
	src := config.NewDictSource(config.Tree{"items": []any{1, 2, 3}})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	list, err := cfg.GetList("items")
	if err != nil || len(list) != 3 {
		t.Errorf("GetList: %v, len=%d", err, len(list))
	}
}

func TestHas(t *testing.T) {
	src := config.NewDictSource(config.Tree{"present": "x", "nullish": nil})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	if !cfg.Has("present") {
		t.Error("Has(present) = false")
	}
	if cfg.Has("missing") {
		t.Error("Has(missing) = true")
	}
	// An explicit null counts as "present" — spec §4.3 and Python
	// config binding treat existence, not truthiness.
	if !cfg.Has("nullish") {
		t.Error("Has(nullish) = false; explicit-null should still be present")
	}
}

// ── GetSection (typed access) ──────────────────────────────────────

func TestGetSectionDecodesIntoStruct(t *testing.T) {
	path := writeTempFile(t, "app.yaml", `
database:
  host: db.example.com
  port: 6432
  pool_size: 50
`)
	cfg, err := config.Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var db struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		PoolSize int    `yaml:"pool_size"`
	}
	if err := cfg.GetSection("database", &db); err != nil {
		t.Fatalf("GetSection: %v", err)
	}

	if db.Host != "db.example.com" || db.Port != 6432 || db.PoolSize != 50 {
		t.Errorf("decoded section: %+v", db)
	}
}

func TestGetSectionValidationFailure(t *testing.T) {
	src := config.NewDictSource(config.Tree{
		"db": config.Tree{"host": []any{"not", "a", "string"}},
	})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	var db struct {
		Host string `yaml:"host"`
	}
	err := cfg.GetSection("db", &db)
	if !isReason(err, config.ReasonValidationFailed) {
		t.Errorf("type mismatch in GetSection must produce validation_failed, got %v", err)
	}
}

func TestGetSectionRejectsNonMapSubtree(t *testing.T) {
	// A list or scalar at the section root returns type_mismatch,
	// not validation_failed (matches config-python reference).
	src := config.NewDictSource(config.Tree{
		"list":   []any{"a", "b"},
		"scalar": 42,
	})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	for _, path := range []string{"list", "scalar"} {
		var target struct {
			X string `yaml:"x"`
		}
		err := cfg.GetSection(path, &target)
		if !isReason(err, config.ReasonTypeMismatch) {
			t.Errorf("GetSection(%q): want type_mismatch, got %v", path, err)
		}
	}
}

func TestGetSectionNilTarget(t *testing.T) {
	src := config.NewDictSource(config.Tree{"x": 1})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	if err := cfg.GetSection("x", nil); !isReason(err, config.ReasonTypeMismatch) {
		t.Errorf("nil target must return type_mismatch, got %v", err)
	}
}

// ── Close / Snapshot ───────────────────────────────────────────────

func TestSnapshotReturnsDeepCopy(t *testing.T) {
	src := config.NewDictSource(config.Tree{"x": config.Tree{"y": 1}})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	snap := cfg.Snapshot()
	snap["x"].(config.Tree)["y"] = 999

	val, _ := cfg.GetInt("x.y")
	if val != 1 {
		t.Errorf("Snapshot shares storage with Config: mutation leaked (got %d)", val)
	}
}

func TestCloseOnLoadedConfigIdempotent(t *testing.T) {
	src := config.NewDictSource(config.Tree{"x": 1})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	for i := 0; i < 3; i++ {
		if err := cfg.Close(); err != nil {
			t.Errorf("Close #%d: %v", i, err)
		}
	}
}

func TestParsePathTrailingDotIsError(t *testing.T) {
	// Phase C regression guard — `a.b.` parses as parse_error, not
	// as silent [a, b] (latent bug discovered in architect review).
	src := config.NewDictSource(config.Tree{"a": config.Tree{"b": 1}})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	_, err := cfg.Get("a.b.")
	if !isReason(err, config.ReasonParseError) {
		t.Errorf("Get(\"a.b.\"): want parse_error, got %v", err)
	}
}

func TestJsonFileSourceWholeNumbersCoerceToInt(t *testing.T) {
	// JsonFileSource normalises whole-number float64 to int64 so
	// GetInt works across YAML / JSON — regression guard.
	path := writeTempFile(t, "app.json", `{"api":{"port":8080},"ratio":0.5}`)
	cfg, err := config.LoadFrom(context.Background(),
		[]config.Source{config.NewJsonFileSource(path)})
	if err != nil {
		t.Fatalf("%v", err)
	}

	port, err := cfg.GetInt("api.port")
	if err != nil || port != 8080 {
		t.Errorf("GetInt(api.port) = %d, %v (whole-number float must become int)", port, err)
	}
	// Fractional floats stay float64 — GetNumber works, GetInt fails.
	ratio, err := cfg.GetNumber("ratio")
	if err != nil || ratio != 0.5 {
		t.Errorf("GetNumber(ratio) = %v, %v", ratio, err)
	}
	if _, err := cfg.GetInt("ratio"); !isReason(err, config.ReasonTypeMismatch) {
		t.Errorf("GetInt on fractional value must give type_mismatch, got %v", err)
	}
}

func TestJsonFileSourceIJSONSafeRange(t *testing.T) {
	// Spec ADR-0001 §4.3 + _meta/coercion.yaml:
	//   safe_range_limit = 2^53 - 1 = 9_007_199_254_740_991
	//
	// Values inside the i-JSON safe range normalise to int64.
	// Values beyond it stay float64 — they cannot round-trip through
	// JS / Python consumers without precision loss.
	path := writeTempFile(t, "app.json", `{
		"at_limit":      9007199254740991,
		"beyond_limit":  9007199254740993,
		"negative_lim": -9007199254740991
	}`)
	cfg, err := config.LoadFrom(context.Background(),
		[]config.Source{config.NewJsonFileSource(path)})
	if err != nil {
		t.Fatalf("%v", err)
	}

	atLimit, err := cfg.GetInt("at_limit")
	if err != nil || atLimit != 9007199254740991 {
		t.Errorf("GetInt(at_limit) = %d, %v (2^53-1 must normalise)", atLimit, err)
	}
	negLim, err := cfg.GetInt("negative_lim")
	if err != nil || negLim != -9007199254740991 {
		t.Errorf("GetInt(negative_lim) = %d, %v", negLim, err)
	}

	// 2^53+1 cannot round-trip as float64 (no unique representation).
	// Stays float64 after normalisation → GetInt must reject.
	if _, err := cfg.GetInt("beyond_limit"); !isReason(err, config.ReasonTypeMismatch) {
		t.Errorf("GetInt on value beyond 2^53 must give type_mismatch (spec i-JSON safe), got %v", err)
	}
}

func TestCoerceIntAcceptsAllIntKinds(t *testing.T) {
	// Coverage gap — `int32`, `int64`, `uint*` variants of GetInt.
	src := config.NewDictSource(config.Tree{
		"i":   int(1),
		"i32": int32(2),
		"i64": int64(3),
		"u":   uint(4),
		"u32": uint32(5),
		"u64": uint64(6),
	})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	for _, p := range []string{"i", "i32", "i64", "u", "u32", "u64"} {
		v, err := cfg.GetInt(p)
		if err != nil {
			t.Errorf("GetInt(%s): %v", p, err)
			continue
		}
		if v < 1 || v > 6 {
			t.Errorf("GetInt(%s) = %d, out of expected range", p, v)
		}
	}
}

func TestCoerceNumberAcceptsAllNumericKinds(t *testing.T) {
	src := config.NewDictSource(config.Tree{
		"i32": int32(2),
		"u64": uint64(3),
		"f32": float32(1.5),
	})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	for _, p := range []string{"i32", "u64", "f32"} {
		if _, err := cfg.GetNumber(p); err != nil {
			t.Errorf("GetNumber(%s): %v", p, err)
		}
	}
}

func TestOnSectionChangeReturnsInactive(t *testing.T) {
	src := config.NewDictSource(config.Tree{"x": 1})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})

	var target struct {
		X int `yaml:"x"`
	}
	sub := cfg.OnSectionChange("x", &target, func(old, new any) {
		t.Fatalf("Phase 1 must never invoke section-change callback")
	})
	if sub == nil || sub.Active {
		t.Errorf("Phase 1 OnSectionChange: want inactive Subscription, got %+v", sub)
	}
}

func TestReloadIsNoOpInPhaseOne(t *testing.T) {
	src := config.NewDictSource(config.Tree{"x": 1})
	cfg, _ := config.LoadFrom(context.Background(), []config.Source{src})
	if err := cfg.Reload(context.Background()); err != nil {
		t.Errorf("Phase 1 Reload must be no-op, got %v", err)
	}
}

func TestJsonFileSourceIDAndInterpolate(t *testing.T) {
	src := config.NewJsonFileSource("/some/path.json")
	if src.ID() != "json:/some/path.json" {
		t.Errorf("ID() = %q", src.ID())
	}
	if src.Interpolate() {
		t.Error("JsonFileSource.Interpolate() must be false")
	}
}

// ── helpers ────────────────────────────────────────────────────────

func isReason(err error, want config.ErrorReason) bool {
	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		return false
	}
	return cfgErr.Reason == want
}
