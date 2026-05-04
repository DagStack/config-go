# ADR-0001 (config-go): VaultSource — SDK choice and sub-module layout

- **Status:** accepted
- **Revision:** 1.0 (2026-05-03)
- **Date:** 2026-05-03
- **Architect review:** ai-systems-architect (proposed round 2026-05-03)
- **Related:**
  [config-spec ADR-0002 §6](https://github.com/dagstack/config-spec/blob/main/adr/0002-secret-references-and-sources.md#6-pilot-adapter--vaultsource-hashicorp-vault-kv-v2),
  [hashicorp/vault/api](https://pkg.go.dev/github.com/hashicorp/vault/api).

## Context

ADR-0002 in `dagstack/config-spec` mandates a HashiCorp Vault adapter
for the Phase 2 SecretSource roll-out across the three bindings
(`config-python`, `config-typescript`, `config-go`). The cross-binding
spec leaves SDK choice, packaging strategy, and token renewal to
each binding.

This ADR records the choices for the Go binding.

## Decision

### 1. SDK — `github.com/hashicorp/vault/api v1.16.0`

The official Go client from HashiCorp. Considered alternatives:

- **Hand-rolled `net/http` client** — feasible for the narrow KV v2
  path, but loses auth-method helpers (`Logical().Write` for AppRole /
  Kubernetes login is implicit in the `Logical` interface) and
  shifts maintenance burden onto the binding.
- **Third-party wrappers** (`github.com/sethvargo/go-vault`) — thin
  wrappers around the official client; no value over importing
  `vault/api` directly.

`vault/api` is the de facto standard in Go ecosystem and ships with
its own typed interfaces for KV v2, AppRole, and Kubernetes auth.
Pin to `v1.16.0` (current minor at adoption time); bump in a
dedicated PR when a new minor lands.

### 2. Sub-module — `go.dagstack.dev/config/vault`

The Vault adapter lives in **a separate Go module** with its own
`go.mod` under `vault/`. Reasons:

1. `vault/api` pulls a fairly heavy dependency tree (`hcl`,
   `go-rootcerts`, several `hashicorp/go-*` libraries plus
   `golang.org/x/crypto`). Consumers that only use file sources
   should not pay this cost in their `go.sum` / binary size.
2. Go ecosystem convention — sub-modules are the idiomatic way to
   ship optional adapters separately from a core API package.

**Import:**

```go
import (
    "go.dagstack.dev/config"
    "go.dagstack.dev/config/vault"
)

src, err := vault.NewSource(
    "https://vault.example.com",
    vault.TokenAuth{Token: os.Getenv("VAULT_TOKEN")},
    vault.WithNamespace("dagstack/prod"),
)
```

`go get go.dagstack.dev/config/vault` resolves the sub-module
independently from the main module (after the parent ships a tagged
release and the sub-module follows with its own).

### 3. Stutter avoidance

The sub-package extracts secrets out of the main `config` package, so
the canonical type name is **`vault.Source`**, not
`vault.VaultSource` (per spec §4.5 and §8.2). Constructor is
`vault.NewSource(...)`. Auth types follow the same rule:
`vault.TokenAuth`, `vault.AppRoleAuth`, `vault.KubernetesAuth`.

The cross-binding spec name `VaultSource` appears in
`_meta/types.yaml` under the `spec_form` column; the Go binding's
column points at `Source` per the stutter-avoidance rule. The name
is not a wire contract — operators reading code see
`vault.Source`, which is what `_meta/types.yaml` documents.

### 4. Token renewal — Phase 2 boundary

Vault tokens carry a TTL. `vault.Source` does **not** spawn a renewal
background task in Phase 2 — token renewal lives in the same Phase 3
PR as `Config.RefreshSecrets()` and the rotation hook (consistent
across bindings).

Phase 2 patterns operators can use:

1. **Long TTL + restart** — Vault tokens issued with a TTL longer
   than the application's expected uptime; renewal handled by an
   init-container or sidecar at SIGTERM.
2. **AppRole** — `secret_id` is a credential, not a session;
   `vault.Source` performs `auth/approle/login` at construction time;
   the resulting token has a TTL the operator controls through Vault's
   role configuration. Restart re-logs-in.
3. **Kubernetes ServiceAccount** — kubelet renews the projected SA JWT
   on a ~60-minute cadence; re-login is cheap.

### 5. Test strategy

Phase 2 ships:

- **Unit tests** covering the path parser (parseVaultPath) and
  interface contracts. Resolve / auth path testing requires a vault
  API mock layer; those tests land alongside the integration suite.

Deferred to a follow-up PR alongside the conformance fixtures from
config-spec issue #18 slice 3:

- **Integration tests** with `testcontainers-go` against `vault:1.15`
  in dev mode, with a seed script populating known KV v2 paths.

This split keeps the Phase 2 PR fast (no Docker dependency in the unit
suite) and lets us land the cross-binding fixture set in lockstep with
Python and TypeScript bindings.

## Consequences

### Positive

- Zero dependency cost for consumers using only file sources —
  `go.dagstack.dev/config` `go.sum` stays small.
- First-class auth coverage — Token, AppRole, Kubernetes ServiceAccount.
- Maintained upstream — `vault/api` is HashiCorp's official Go client.
- Sub-module pattern is idiomatic Go and matches what
  `aws-sdk-go-v2/service/*` and similar libraries do.

### Negative

- Operators have to `go get` two modules instead of one. Mitigated by
  the import path being mnemonically obvious
  (`go.dagstack.dev/config/vault`).
- `vault/api` pulls many transitive dependencies; a future major may
  break the contract. Mitigated by pinning the minor and reviewing
  upgrades manually.
- Sub-module versioning — when the parent module ships v0.5.0, the
  sub-module needs a coordinated v0.5.0 release with `replace` removed.
  Documented in the release process.

### Neutral

- struct-tag-based `GetSection` in the parent module is unaffected —
  resolution happens before reflection.

## Out of scope

- KV v1 (Phase 3 if any operator requests it).
- Dynamic secrets with leases (`database/creds/...`) — requires
  background-renewal infrastructure that lands with the rotation hook.
- Vault Agent / Banzai Vault wrappers — deployment-time concerns.
- Token revocation on `Close()` — operators that want it call
  `client.Auth().Token().RevokeSelf("")` themselves before close.
