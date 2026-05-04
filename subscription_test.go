package config_test

import (
	"testing"

	"go.dagstack.dev/config"
)

func TestSubscriptionUnsubscribeIsIdempotent(t *testing.T) {
	calls := 0
	sub := config.NewSubscription("x.y", func() { calls++ })

	sub.Unsubscribe()
	sub.Unsubscribe()
	sub.Unsubscribe()

	if calls != 1 {
		t.Errorf("unsubscribe called underlying %d times, want 1", calls)
	}
	if sub.Path != "x.y" {
		t.Errorf("Subscription.Path = %q, want %q", sub.Path, "x.y")
	}
	if !sub.Active {
		t.Error("NewSubscription should produce Active=true")
	}
}

func TestNewInactiveSubscription(t *testing.T) {
	sub := config.NewInactiveSubscription("db.host", "no watch-capable source registered")

	if sub.Active {
		t.Error("NewInactiveSubscription should produce Active=false")
	}
	if sub.InactiveReason != "no watch-capable source registered" {
		t.Errorf("InactiveReason = %q, want diagnostic text", sub.InactiveReason)
	}

	// Unsubscribe on an inactive subscription must not panic.
	sub.Unsubscribe()
}

func TestSubscriptionUnsubscribeNilSafe(t *testing.T) {
	// Zero-value / nil Subscription must not panic on Unsubscribe.
	var sub *config.Subscription
	sub.Unsubscribe() // should not panic

	zero := &config.Subscription{}
	zero.Unsubscribe() // should not panic
}

// Phase 1 check: OnChange returns an inactive subscription and never
// invokes the callback.
func TestConfigOnChangeReturnsInactiveInPhase1(t *testing.T) {
	cfg := &config.Config{}
	sub := cfg.OnChange("database.host", func(ev config.ChangeEvent) {
		t.Fatalf("callback fired in Phase 1 — should be inactive; event=%+v", ev)
	})
	if sub == nil {
		t.Fatal("OnChange returned nil Subscription")
	}
	if sub.Active {
		t.Error("Phase 1 Subscription should be Active=false")
	}
	if sub.InactiveReason == "" {
		t.Error("Phase 1 Subscription should populate InactiveReason")
	}
	if sub.Path != "database.host" {
		t.Errorf("Subscription.Path = %q, want %q", sub.Path, "database.host")
	}
}
