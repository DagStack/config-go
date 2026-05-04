package vault

import (
	"context"
	"errors"
	"testing"

	"go.dagstack.dev/config"
)

// ── Path parser ───────────────────────────────────────────────────────

func TestParseVaultPathMinimal(t *testing.T) {
	p, err := parseVaultPath("secret/db")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.MountPoint != "secret" || p.KeyPath != "db" {
		t.Fatalf("unexpected: %+v", p)
	}
}

func TestParseVaultPathSubkey(t *testing.T) {
	p, err := parseVaultPath("secret/db#password")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Field != "password" {
		t.Fatalf("expected field 'password', got %q", p.Field)
	}
}

func TestParseVaultPathVersion(t *testing.T) {
	p, err := parseVaultPath("secret/db?version=3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Version != 3 {
		t.Fatalf("expected version 3, got %d", p.Version)
	}
}

func TestParseVaultPathMissingMount(t *testing.T) {
	_, err := parseVaultPath("just-a-path")
	var cerr *config.Error
	if !errors.As(err, &cerr) || cerr.Reason != config.ReasonSecretUnresolved {
		t.Fatalf("expected SecretUnresolved, got %v", err)
	}
}

func TestParseVaultPathInvalidVersion(t *testing.T) {
	_, err := parseVaultPath("secret/db?version=latest")
	var cerr *config.Error
	if !errors.As(err, &cerr) || cerr.Reason != config.ReasonSecretUnresolved {
		t.Fatalf("expected SecretUnresolved, got %v", err)
	}
}

func TestParseVaultPathUnknownQueryKey(t *testing.T) {
	_, err := parseVaultPath("secret/db?colour=red")
	var cerr *config.Error
	if !errors.As(err, &cerr) || cerr.Reason != config.ReasonSecretUnresolved {
		t.Fatalf("expected SecretUnresolved, got %v", err)
	}
}

func TestParseVaultPathVersionAndField(t *testing.T) {
	p, err := parseVaultPath("secret/dagstack/prod/db?version=5#password")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.MountPoint != "secret" || p.KeyPath != "dagstack/prod/db" ||
		p.Version != 5 || p.Field != "password" {
		t.Fatalf("unexpected: %+v", p)
	}
}

// ── Source.Scheme + ID ────────────────────────────────────────────────

func TestSourceSchemeAndID(t *testing.T) {
	src := &Source{addr: "https://vault.example.com", id: "vault:https://vault.example.com"}
	if src.Scheme() != "vault" {
		t.Fatalf("expected scheme 'vault', got %q", src.Scheme())
	}
	if src.ID() != "vault:https://vault.example.com" {
		t.Fatalf("unexpected id: %q", src.ID())
	}
}

func TestSourceImplementsConfigSecretSource(t *testing.T) {
	var _ config.SecretSource = (*Source)(nil)
}

// ── Auth descriptors ──────────────────────────────────────────────────

func TestAuthDescriptorSatisfiesAuth(t *testing.T) {
	var _ Auth = TokenAuth{Token: "x"}
	var _ Auth = AppRoleAuth{RoleID: "r", SecretID: "s"}
	var _ Auth = KubernetesAuth{Role: "r"}
}

// Note: integration tests against a real Vault dev server land in a
// follow-up PR alongside the conformance fixtures from
// config-spec issue #18 slice 3 (Vault dev-mode docker-compose).
// Unit-level coverage of Resolve/auth-handlers requires a vault
// API mock layer that's heavier than the test value at this stage —
// the per-binding ADR documents this trade-off.

// Touch context to silence the unused import lint when only path
// tests are present.
var _ = context.Background
