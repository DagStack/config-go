# dagstack-config-go

Go implementation of [`dagstack/config-spec`](https://github.com/dagstack/config-spec) — YAML configuration with env interpolation, layered deep-merge, typed access via struct tags, and secret references with pluggable backends.

**Status:** Phase 1 (`0.3.x`) is published on the Go module proxy. Phase 2 secrets (`0.4.x`) ship in this release, adding `${secret:...}` references with HashiCorp Vault as the pilot adapter (sub-module `go.dagstack.dev/config/vault`).

## Installation

```bash
go get go.dagstack.dev/config@v0.4.0
# Vault adapter is a separate sub-module — no transitive deps unless installed:
go get go.dagstack.dev/config/vault@v0.4.0
```

(The Go vanity URL `go.dagstack.dev/config` resolves to `github.com/dagstack/config-go`.)

## Secrets (Phase 2 — `0.4.0+`)

Per [ADR-0002](https://github.com/dagstack/config-spec/blob/main/adr/0002-secret-references-and-sources.md), Phase 2 adds the `${secret:<scheme>:<path>}` interpolation token alongside Phase 1's `${VAR}`. Pluggable `SecretSource` adapters resolve the references at load time. Go is eager-by-default — `LoadFrom` walks the merged tree and resolves every placeholder before returning, so `Get*` methods stay synchronous and never see resolution failures.

The `env` scheme is auto-registered and behaves identically to Phase 1's `${VAR}`:

```yaml
# app-config.yaml
llm:
  api_key: ${secret:env:OPENAI_API_KEY}        # ≡ ${OPENAI_API_KEY}
  fallback: ${secret:env:OPENAI_API_KEY:-sk-dev-placeholder}
```

The pilot HashiCorp Vault adapter ships in the sub-module:

```go
package main

import (
    "context"
    "log"
    "os"

    "go.dagstack.dev/config"
    "go.dagstack.dev/config/vault"
)

func main() {
    ctx := context.Background()
    src, err := vault.NewSource(
        "https://vault.example.com",
        vault.TokenAuth{Token: os.Getenv("VAULT_TOKEN")},
        vault.WithNamespace("dagstack/prod"),
    )
    if err != nil {
        log.Fatal(err)
    }

    cfg, err := config.LoadFrom(ctx,
        []config.Source{config.NewYamlFileSource("app-config.yaml")},
        config.WithSecretSources(src),
    )
    if err != nil {
        log.Fatal(err)
    }

    apiKey, _ := cfg.GetString("llm.api_key")
    // ${secret:vault:secret/dagstack/prod/openai#api_key}
    _ = apiKey
}
```

`?version=N` selects a specific KV v2 version; `#field` plucks a sub-key from a multi-key secret. AppRole and Kubernetes ServiceAccount auth are supported alongside `TokenAuth` — see [`adr/0001-vault-source.md`](https://github.com/dagstack/config-go/blob/main/adr/0001-vault-source.md) for details.

## Runtime API

- **`Config.RefreshSecrets(ctx) error`** — drops the cached resolved tree and re-resolves every `${secret:...}` reference against its registered `SecretSource`, then atomically swaps the internal reference under a write-lock. `SecretValue.ExpiresAt` is respected at this call only — schedule a `time.Ticker` (e.g. `for range time.Tick(5*time.Minute) { _ = cfg.RefreshSecrets(ctx) }`) to honour Vault TTL or rotation cadence. On failure the previously resolved tree remains active. Manual rotation hook for Phase 2; push-based rotation is deferred to Phase 3.
- **`Config.Snapshot()`** (default) — returns a deep copy of the merged tree with every `SecretRef` placeholder masked to `MaskedPlaceholder` and every plain string under a secret-named key (`api_key`, `password`, …) also masked. No backend round-trip.
- **`Config.Snapshot(WithIncludeSecrets())`** — audit-mode opt-in: returns the resolved tree with field-name suffix masking still applied. Treat the result as sensitive.

## Roadmap

- **Phase 1 (`0.3.x`)** — base spec MVP: file sources, env interpolation, deep-merge layering, struct-tag typed sections, canonical JSON.
- **Phase 2 (`0.4.x`)** — secret references + pluggable `SecretSource` adapters (per ADR-0002). Vault sub-module pilot.
- **Phase 3+** — push-based rotation events, AWS / GCP / K8s secret-manager adapters, watch + push-reload of file sources.

## Usage example (Phase 1)

```go
import "go.dagstack.dev/config"

cfg, err := config.Load(ctx, "app-config.yaml")
if err != nil {
    return err
}

host, err := cfg.GetString("database.host")
ttl, _  := cfg.GetIntDefault("cache.ttl_min", 10)

type DatabaseConfig struct {
    Host     string `yaml:"host"`
    Password string `yaml:"password"`
}
var db DatabaseConfig
if err := cfg.GetSection("database", &db); err != nil {
    return err
}
```

## Thread-safety

`Config` is a value with read-mostly semantics. The contract below holds for the
v0.x line; Phase 2 adds atomic-swap secret refresh (`RefreshSecrets`) without
weakening any of these guarantees. Watch / subscriber fan-out is reserved for
Phase 3+.

- **Concurrent reads — safe, no caller-side locking required.**
  `Get`, `GetString`, `GetInt`, `GetNumber`, `GetBool`, `GetList`, `Has`, and
  `GetSection` operate on the immutable merged tree built once by
  `Load` / `LoadFrom`. The tree is not mutated after construction, so any number
  of goroutines may read concurrently. The repository's `make test` target runs
  `go test -race ./...` and is race-clean.
- **`Snapshot` / `SourceIDs` — return copies.** Both methods materialise fresh
  values (`copyTree`, fresh `[]string`) so callers cannot reach into the
  internal state and mutating the result has no effect on `Config`.
- **`RefreshSecrets` — atomic swap under write-lock.** Re-walks the original
  pre-resolved tree against the registered SecretSources and replaces the
  internal resolved tree under `sync.RWMutex.Lock`. Concurrent readers in
  flight observe either the previous or the next tree, never a torn
  intermediate. On backend failure the previously resolved tree remains
  active — callers may safely retry. `Reload` (Phase 1) is a no-op for
  ConfigSource-derived data; push-capable file sources land in Phase 3.
- **`OnChange` / `OnSectionChange` — deferred to Phase 3+.** Both calls return an
  inactive `Subscription` (`Active == false`, `InactiveReason` set) and the
  callback never fires through Phase 2. When watch lands, registration will be
  safe to call from any goroutine; callbacks dispatch on a dedicated goroutine
  without an ordering guarantee between distinct subscribers.
- **`Close` — idempotent, terminal.** Repeated calls return `nil`. `Close`
  releases source-side resources (where a `Source` implements `Closer`),
  aggregates per-source errors with `errors.Join`, and clears the internal
  source list. Phase 1 has no watcher resources to release; Phase 2+ source
  adapters (etcd, Consul, HTTP) will tear down their network connections here.
  `Close` is the one mutating operation on `Config`; callers must not race
  `Close` against `SourceIDs` or any `Get*` — the standard pattern is "load
  once, use many, close on shutdown".
- **No package-level singleton.** `Load` / `LoadFrom` return a fresh `*Config`
  on every call; the binding deliberately ships no global handle. If an
  application wants a shared instance it owns the package-level variable or DI
  registration — the spec is silent on lifecycle ownership.


## Specification

The `spec/` submodule points to [`dagstack/config-spec`](https://github.com/dagstack/config-spec). Normative decisions are recorded in `spec/adr/0001-yaml-configuration.md`.

## Local development

```bash
git clone --recurse-submodules git@github.com:dagstack/config-go.git
cd config-go
make test           # go test -race ./...
make vet            # go vet
make lint           # golangci-lint (optional)
make conformance    # conformance fixtures (Phase D+)
```

## Related

- [`dagstack/config-spec`](https://github.com/dagstack/config-spec) — specification (source of truth).
- [`dagstack/config-python`](https://github.com/dagstack/config-python) — reference Python implementation.
- [`dagstack/config-docs`](https://github.com/dagstack/config-docs) — documentation and guides ([config.dagstack.dev](https://config.dagstack.dev)).

## License

Apache-2.0 — see `LICENSE`.
