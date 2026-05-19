package config_test

// Phase 2 runtime API: RefreshSecrets / Snapshot semantics.
//
// Per ADR-0002 §3:
// - Config.RefreshSecrets() MUST re-resolve every reference under a
//   write-lock and atomically swap the resolved tree (manual rotation
//   hook). Go is eager-by-default, so resolution happens during the
//   call, not on next read.
// - Config.Snapshot() MUST replace every SecretRef with [MASKED] and
//   apply field-name suffix masking by default; with
//   WithIncludeSecrets() it resolves SecretRef placeholders and
//   applies field-name suffix masking (audit-mode opt-in).
//
// Go is eager-by-default — secrets resolved at LoadFrom time. The test
// fixtures construct an in-process SecretSource that records every
// Resolve() call and lets us flip the returned value to verify
// refresh semantics.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	config "go.dagstack.dev/config"
)

type countingSource struct {
	value     atomic.Pointer[string]
	expiresAt time.Time
	calls     atomic.Pointer[[]string]
}

func newCountingSource(value string, expiresAt time.Time) *countingSource {
	c := &countingSource{expiresAt: expiresAt}
	c.value.Store(&value)
	empty := []string{}
	c.calls.Store(&empty)
	return c
}

func (c *countingSource) Scheme() string { return "ctr" }
func (c *countingSource) ID() string     { return "ctr:test" }

func (c *countingSource) Resolve(_ context.Context, path string) (config.SecretValue, error) {
	for {
		old := c.calls.Load()
		next := append([]string(nil), *old...)
		next = append(next, path)
		if c.calls.CompareAndSwap(old, &next) {
			break
		}
	}
	return config.SecretValue{
		Value:     *c.value.Load(),
		SourceID:  c.ID(),
		ExpiresAt: c.expiresAt,
	}, nil
}

func (c *countingSource) Close() error { return nil }

func (c *countingSource) callsSnapshot() []string {
	return append([]string(nil), *c.calls.Load()...)
}

func (c *countingSource) setValue(v string) {
	c.value.Store(&v)
}

func writeYaml(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	return p
}

// ─── RefreshSecrets() ───────────────────────────────────────────────────

func TestRefreshSecretsReResolves(t *testing.T) {
	src := newCountingSource("v1", time.Time{})
	cfg, err := config.LoadFrom(
		context.Background(),
		[]config.Source{config.NewYamlFileSource(writeYaml(t, "k: ${secret:ctr:foo}\n"))},
		config.WithSecretSources(src),
	)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	got, _ := cfg.GetString("k")
	if got != "v1" {
		t.Fatalf("first read: got %q want v1", got)
	}

	src.setValue("v2")
	got, _ = cfg.GetString("k")
	if got != "v1" {
		t.Fatalf("cached read: got %q want v1 (cache should still serve old)", got)
	}

	if err := cfg.RefreshSecrets(context.Background()); err != nil {
		t.Fatalf("RefreshSecrets: %v", err)
	}
	got, _ = cfg.GetString("k")
	if got != "v2" {
		t.Fatalf("after refresh: got %q want v2", got)
	}
	if calls := src.callsSnapshot(); len(calls) != 2 {
		t.Fatalf("resolve calls: got %v want 2 (load+refresh)", len(calls))
	}
}

func TestGetAndRefreshSecretsRaceFree(t *testing.T) {
	src := newCountingSource("v1", time.Time{})
	cfg, err := config.LoadFrom(
		context.Background(),
		[]config.Source{config.NewYamlFileSource(writeYaml(t, "k: ${secret:ctr:foo}\n"))},
		config.WithSecretSources(src),
	)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	stop := make(chan struct{})
	done := make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = cfg.GetString("k")
					_ = cfg.Has("k")
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		if err := cfg.RefreshSecrets(context.Background()); err != nil {
			t.Fatalf("RefreshSecrets: %v", err)
		}
	}
	close(stop)
	for i := 0; i < 4; i++ {
		<-done
	}
}

func TestRefreshSecretsAtomicityOnFailure(t *testing.T) {
	value := "v1"
	shouldFail := atomic.Bool{}
	src := &flippingSource{
		valueRef:    &value,
		shouldFail:  &shouldFail,
		callPathSet: nil,
	}

	cfg, err := config.LoadFrom(
		context.Background(),
		[]config.Source{config.NewYamlFileSource(writeYaml(t, "k: ${secret:flip:foo}\n"))},
		config.WithSecretSources(src),
	)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got, _ := cfg.GetString("k"); got != "v1" {
		t.Fatalf("first read: got %q want v1", got)
	}

	value = "v2"
	shouldFail.Store(true)
	if err := cfg.RefreshSecrets(context.Background()); err == nil {
		t.Fatalf("expected refresh to fail when backend is down")
	}
	// Previously resolved tree must remain active.
	if got, _ := cfg.GetString("k"); got != "v1" {
		t.Fatalf("after failed refresh: got %q want v1 (atomicity)", got)
	}

	shouldFail.Store(false)
	if err := cfg.RefreshSecrets(context.Background()); err != nil {
		t.Fatalf("RefreshSecrets after recovery: %v", err)
	}
	if got, _ := cfg.GetString("k"); got != "v2" {
		t.Fatalf("after successful refresh: got %q want v2", got)
	}
}

type flippingSource struct {
	valueRef    *string
	shouldFail  *atomic.Bool
	callPathSet []string
}

func (f *flippingSource) Scheme() string { return "flip" }
func (f *flippingSource) ID() string     { return "flip:test" }

func (f *flippingSource) Resolve(_ context.Context, _ string) (config.SecretValue, error) {
	if f.shouldFail.Load() {
		return config.SecretValue{}, errors.New("backend down")
	}
	return config.SecretValue{Value: *f.valueRef, SourceID: f.ID()}, nil
}

func (f *flippingSource) Close() error { return nil }

// ─── ExpiresAt honoured ─────────────────────────────────────────────────

func TestExpiresAtHonouredInWalkCache(t *testing.T) {
	past := time.Now().Add(-time.Second)
	src := newCountingSource("v1", past)
	_, err := config.LoadFrom(
		context.Background(),
		[]config.Source{config.NewYamlFileSource(writeYaml(t,
			"a: ${secret:ctr:foo}\nb: ${secret:ctr:foo}\n"))},
		config.WithSecretSources(src),
	)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	calls := src.callsSnapshot()
	if len(calls) != 2 {
		t.Fatalf("expected 2 fresh calls (a + b, both stale-cached): got %d (%v)", len(calls), calls)
	}
}

func TestExpiresAtFutureDedupes(t *testing.T) {
	future := time.Now().Add(time.Hour)
	src := newCountingSource("v1", future)
	_, err := config.LoadFrom(
		context.Background(),
		[]config.Source{config.NewYamlFileSource(writeYaml(t,
			"a: ${secret:ctr:foo}\nb: ${secret:ctr:foo}\n"))},
		config.WithSecretSources(src),
	)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if calls := src.callsSnapshot(); len(calls) != 1 {
		t.Fatalf("expected 1 dedup'd call: got %d (%v)", len(calls), calls)
	}
}

func TestExpiresAtZeroDedupes(t *testing.T) {
	src := newCountingSource("v1", time.Time{})
	_, err := config.LoadFrom(
		context.Background(),
		[]config.Source{config.NewYamlFileSource(writeYaml(t,
			"a: ${secret:ctr:foo}\nb: ${secret:ctr:foo}\n"))},
		config.WithSecretSources(src),
	)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if calls := src.callsSnapshot(); len(calls) != 1 {
		t.Fatalf("expected 1 dedup'd call: got %d (%v)", len(calls), calls)
	}
}

// ─── Snapshot() ─────────────────────────────────────────────────────────

func TestSnapshotDefaultMasksSecretRefsWithoutResolving(t *testing.T) {
	src := newCountingSource("should-not-appear", time.Time{})
	cfg, err := config.LoadFrom(
		context.Background(),
		[]config.Source{config.NewYamlFileSource(writeYaml(t,
			"api_key: ${secret:ctr:foo}\nplain: hello\n"))},
		config.WithSecretSources(src),
	)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	priorCalls := len(src.callsSnapshot())

	snap := cfg.Snapshot()
	if v, _ := snap["api_key"].(string); v != config.MaskedPlaceholder {
		t.Fatalf("api_key should be masked: got %v", snap["api_key"])
	}
	if v, _ := snap["plain"].(string); v != "hello" {
		t.Fatalf("plain: got %q want hello", v)
	}
	// Snapshot must NOT trigger backend round-trips.
	if got := len(src.callsSnapshot()); got != priorCalls {
		t.Fatalf("snapshot triggered %d extra resolve calls", got-priorCalls)
	}
}

func TestSnapshotMasksPlainStringUnderSecretName(t *testing.T) {
	cfg, err := config.LoadFrom(
		context.Background(),
		[]config.Source{config.NewYamlFileSource(writeYaml(t,
			"password: hunter2\nuser: alice\n"))},
	)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	snap := cfg.Snapshot()
	if v, _ := snap["password"].(string); v != config.MaskedPlaceholder {
		t.Fatalf("password should be masked: got %v", snap["password"])
	}
	if v, _ := snap["user"].(string); v != "alice" {
		t.Fatalf("user: got %q want alice", v)
	}
}

func TestSnapshotIncludeSecretsResolvedThenFieldMasks(t *testing.T) {
	src := newCountingSource("resolved-secret", time.Time{})
	cfg, err := config.LoadFrom(
		context.Background(),
		[]config.Source{config.NewYamlFileSource(writeYaml(t,
			"api_key: ${secret:ctr:foo}\nendpoint: ${secret:ctr:bar}\n"))},
		config.WithSecretSources(src),
	)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	snap := cfg.Snapshot(config.WithIncludeSecrets())
	// api_key matches secret-name pattern → still masked.
	if v, _ := snap["api_key"].(string); v != config.MaskedPlaceholder {
		t.Fatalf("api_key: got %v want masked", snap["api_key"])
	}
	// endpoint does not match → resolved value visible.
	if v, _ := snap["endpoint"].(string); v != "resolved-secret" {
		t.Fatalf("endpoint: got %v want resolved-secret", snap["endpoint"])
	}
}

func TestSnapshotReturnsIndependentCopy(t *testing.T) {
	cfg, err := config.LoadFrom(
		context.Background(),
		[]config.Source{config.NewYamlFileSource(writeYaml(t, "x:\n  y: 1\n"))},
	)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	snap := cfg.Snapshot()
	if x, ok := snap["x"].(config.Tree); ok {
		x["y"] = 999
	}
	got, _ := cfg.GetInt("x.y")
	if got != 1 {
		t.Fatalf("config mutated by snapshot: got %d want 1", got)
	}
}
