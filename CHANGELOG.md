# Changelog

All notable changes to `go.dagstack.dev/config` are recorded in this file.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versioning — [SemVer](https://semver.org/spec/v2.0.0.html).

## [0.5.0] - 2026-05-19

Canonical JSON key sort order aligned with RFC 8785 §3.2.3 — keys
are now ordered by their UTF-16 code-unit sequence (config-spec
ADR-0001 v2.3).

### Fixed

- `canonicalJSON` previously sorted object keys with `sort.Strings`
  (UTF-8 byte order). Per RFC 8785 §3.2.3 the canonical sort order
  is UTF-16 code units — the two orderings coincide for any key
  inside the Basic Multilingual Plane (BMP), but diverge whenever
  one of the keys lies in a supplementary plane (U+10000+). A new
  internal helper `sortKeysUTF16` encodes the keys via
  `utf16.Encode([]rune(s))` and compares the resulting `[]uint16`
  slices lexicographically.

### Breaking

- Wire bytes change on edge-case key shapes — any object that mixes
  BMP-PUA (U+E000+) keys with supplementary-plane (U+10000+) keys
  will serialize with the two keys in the opposite order. Internal
  Nexus consumers do not produce such keys, so no rollout impact
  observed.

### Spec submodule

- `spec`: 97640b3 → c180592 (config-spec ADR-0001 v2.3 +
  conformance fixture `key_order_drift_witness.json`).

## [0.4.0] - 2026-05-04

Phase 2 secrets — `${secret:<scheme>:<path>}` reference syntax with
pluggable `SecretSource` adapters. Pilot adapter for HashiCorp Vault
KV v2 ships in the sub-module `go.dagstack.dev/config/vault`. Spec:
ADR-0002.

### Added

- `SecretSource` interface (`Scheme()`, `ID()`, `Resolve(ctx, path)`,
  `Close()`) — adapter contract for secret backends.
- `SecretRef` and `SecretValue` value types for references and
  resolution. The spec's `ResolveContext` is encoded as
  `context.Context` in Go (cancellation + deadline already covered;
  `attempt` not exposed in Phase 2 per ADR-0002 §Open question 4).
- `EnvSecretSource` (constructor `NewEnvSecretSource`) — mandatory
  in-process adapter for the `env` scheme. `${secret:env:VAR}` is
  semantically identical to `${VAR}` from ADR-0001 §2 (backwards-
  compat).
- `vault.Source` (sub-module `go.dagstack.dev/config/vault`) — pilot
  Vault adapter. Separate Go module, isolated `go.sum`, depends on
  `github.com/hashicorp/vault/api`. KV v2 only; Token + AppRole +
  Kubernetes ServiceAccount auth; namespace support; `?version=N`
  query; `#field` projection.
- Three new error reasons in `errors.go`:
  `ReasonSecretUnresolved`, `ReasonSecretBackendUnavailable`,
  `ReasonSecretPermissionDenied` — for operator-actionable dispatch.
- `LoadFrom(ctx, sources, ...LoadOption)` accepts SecretSources via
  `WithSecretSources(...)`. The loader auto-registers
  `EnvSecretSource` if no `SecretSource` is passed; eager scan at
  load time fails fast on unknown schemes per ADR-0002 §4 rule 3.
- **Eager resolution** (Go choice per ADR-0002 §3): every `SecretRef`
  is resolved during `LoadFrom`. Vault round-trips happen before
  `LoadFrom` returns. This trades startup latency for the guarantee
  that `Get*` calls on the resulting `Config` are synchronous and
  free of resolution failures.
- `Config.RefreshSecrets(ctx) error` — drops the cached resolved
  tree and re-resolves every `${secret:...}` reference against its
  registered `SecretSource`, then atomically swaps the internal
  reference under a write-lock (ADR-0002 §3 "Forced refresh"). On
  failure the previously resolved tree remains active. Manual
  rotation hook for Phase 2; push-based rotation is deferred to
  Phase 3.
- `Config.Snapshot(opts ...SnapshotOption)` — default masks every
  `SecretRef` placeholder to `MaskedPlaceholder` and applies field-
  name suffix masking from `_meta/secret_patterns.yaml`. No backend
  round-trip. With `WithIncludeSecrets()` returns the resolved tree
  with field-name suffix masking still applied (audit-mode opt-in
  per ADR-0002 §3 trigger table).
- Resolution-walk cache honours `SecretValue.ExpiresAt` — an entry
  past its expiry is treated as a cache miss and forces a fresh
  backend round-trip even if the same path appears more than once
  in the tree. Closes the ADR-0002 §3 cache MUST-clause.
- New per-binding `adr/0001-vault-source.md` documenting the
  `hashicorp/vault/api` SDK choice, sub-module packaging, and the
  Phase 2 vs Phase 3 token-renewal boundary.

### Backwards compatibility

`${VAR}` Phase 1 syntax keeps working unchanged. `${secret:env:VAR}`
is semantically identical, so migration is a mechanical sed (no
breaking change for any existing consumer).

`Config.Snapshot()` keeps its zero-arg call shape, but the default
return is now masked. The previous unmasked behaviour is exposed via
`Snapshot(WithIncludeSecrets())` (which still applies field-name
suffix masking). Audit-mode consumers must update their call sites;
non-audit consumers should review whether they were relying on raw
values.

### Refs

- ADR-0002 §1 grammar, §2 SecretSource contract, §3 SecretRef +
  caching, §4 loader integration, §5 error reasons, §6 VaultSource.
- per-binding `adr/0001-vault-source.md`.

## [0.3.1] - 2026-04-27

First stable release tagged after the rc.1 soak. Cumulative changes since 0.3.0:

- Translate comments and godoc to English across `*.go` and `docs_examples/*.go`.
- Sync the `intro_test.go` YAML fixture with the now-English `intro.mdx` (`tagline`).

Non-functional relative to 0.3.0 — public API, semantics, and exported identifiers
unchanged. The corresponding documentation site (config.dagstack.dev) is also
English-first.

## [0.3.1-rc.1] - 2026-04-26

Translate Russian comments and godoc to English across `*.go` and
`docs_examples/*.go`. Non-functional change relative to 0.3.0 — public API,
semantics, and exported identifiers unchanged. Motivation: lower the barrier
for international adopters (godoc shown on pkg.go.dev, visible on the github
mirror).

## [0.3.0] - 2026-04-23

Release tracking config-spec ADR v2.2 (pre-release quality hardening).
No breaking API changes.

### New

- **`IsSecretField(name string) bool`** + **`MaskValue(name, value) any`** +
  the **`MaskedPlaceholder = "[MASKED]"`** constant — implement ADR v2.2 §6:
  source-of-truth suffix / prefix / exact patterns from
  `_meta/secret_patterns.yaml`. Bindings can use these helpers
  for custom diagnostics.

### Observable behaviour changes

- **`Config.SourceIDs()`**, `GetSection` — the v2.1 walker invariant is
  now spelled out in the spec explicitly. Behavior unchanged.
- **`Snapshot()`**, `GetSection` — secret-aware diagnostics: for fields
  matching `_meta/secret_patterns.yaml`, the value in the output
  (`ConfigError.details`) is replaced with `[MASKED]`.

### Conformance

- Submodule spec: `8cf2715` → `7ff2707` (ADR v2.2 merge).
- Load-level fixtures pass: `ijson_safe_boundary`,
  `yaml_1_2_bool_literals`, `getter_raw_vs_section_view`.
  (YAML 1.2 strict mode is the default in yaml.v3 — no patch needed.)
- Getter-level fixtures are skipped; covered by unit tests in
  `secrets_mask_test.go`.

## [0.2.0] - 2026-04-23

Release tracking config-spec ADR v2.1 (cross-binding conformance tightening).
Brings the Go binding into line with the spec on three points. The binding is
not published to the Go vanity URL — breaking change without shims.

### New

- **`Config.SourceIDs() []string`** — public method returning source IDs
  in load order (ADR v2.1 §4.1). Cross-binding parity with
  Python `source_ids()` and TS `sourceIds()`. Returns a copy — the caller
  cannot mutate internal state.

### Breaking changes

- **`GetSection`: env-string coercion** (ADR v2.1 §4.4). The walker
  `section_coerce.go` traverses the merged subtree using the target's
  `reflect.Type` and converts env-substituted strings to `int` / `float` / `bool`
  per `_meta/coercion.yaml` regexes **before** `yaml.Unmarshal`.
  Result: `port: "${DB_PORT:-5432}"` in YAML with a `Port int` field on the
  struct now parses correctly (previously `yaml.v3` rejected the string
  `"5432"` in an int field with validation_failed).
- **`GetSection`: reverse-coerce rejection** (ADR v2.1 §4.4 M1). A native
  `int` / `float` / `bool` in a field of type `string` → `*Error(ReasonTypeMismatch)`
  with the full dot-notation path `section.field` (§4.5). Guards against silent
  `dimension: 768` → `"768"`.

### Conformance

- Submodule spec: `09badaf` → `8cf2715` (ADR v2.1 merge).
- `TestConformance` skips fixtures tagged `runner_extension_required`
  (v2.1 introduced 3 fixtures for getter/getSection level, runner v1.0
  supports load level only). The binding covers these cases via native
  unit tests (`section_coerce_test.go` — 6 tests).

## [Unreleased]

## [0.1.0] - 2026-04-23

First public release — a Go YAML config reader with env interpolation
and multi-layer merge. Parity with
[`config-python`](https://github.com/dagstack/config-python) across 8
conformance fixtures of the [`dagstack/config-spec`](https://github.com/dagstack/config-spec)
v2.1 specification; getters in Go are strict (see "Known divergences").

### Highlights

- YAML configuration with env interpolation (`${VAR}`, `${VAR:-default}`,
  `$$` escape, UPPERCASE-ASCII names).
- Deep-merge of layers: `app-config.yaml` → `app-config.local.yaml` →
  `app-config.${DAGSTACK_ENV}.yaml` (atomic slice replacement, no
  mutation of inputs).
- Typed access: `GetString` / `GetInt` / `GetNumber` /
  `GetBool` / `GetList` plus `*Default` variants. `GetString` does no
  implicit coercion; `GetInt` accepts whole-number floats inside the
  i-JSON safe range (`±2^53-1`) — parity with ADR v2.1 §4.3.
- Canonical JSON (RFC 8785 ES ToString, sorted UTF-8 keys) for
  bit-identical serialization; `GetSection` via yaml round-trip.
- Three sources: `YamlFileSource`, `JsonFileSource`, `DictSource`
  (builder API: `WithID` / `WithInterpolation`).
- The conformance runner runs all 8 fixtures from
  `spec/conformance/manifest.yaml` (parity with config-python).
- Coverage 86.5%, Go 1.22 / 1.23 / 1.24, CI ≈27 s on the prebuilt
  `dagstack-runner-go`.
- Minimum Go version is 1.22 (`go 1.22` directive in `go.mod`).

### Known divergences from config-python

- `GetString` is strict (does not coerce `int` / `float` / `bool` to string).
  The Python binding up to v0.2.0 performs coercion; in Go we go straight
  to spec v2.1 §4.3. Migrating from Python requires explicit conversions.

### Not in v0.1.0

- `Watcher` / `OnChange` — the interfaces are declared, but sources
  return `ErrNotSupported` (Phase 2).
- Concurrent reload: multiple readers after `Load` are safe (the tree
  is immutable), but concurrent `Load` / `LoadFrom` alongside readers
  is not. v0.2 will move to `atomic.Pointer[Tree]`.
- Runner manifest v1.1 (`expected_has`, `expected_getter` assertions)
  — waiting on spec changes.

### Added — Phase D (conformance runner)

- `conformance_test.go` — a data-driven Go test that runs the
  fixtures from `spec/conformance/manifest.yaml` against the binding.
  Covers all 8 spec fixtures: happy path (basic_interpolation,
  layered, interpolation_coerces_numeric, whole_number_floats,
  null_parsing) + error cases (env_unresolved, parse_error_yaml,
  non_mapping_root).
- `export_test.go` — a `CanonicalJSON` alias accessible only from
  internal tests, for the unexported serializer (architect SF-6 from Phase B).
- `Makefile` — the `conformance` target now runs `TestConformance`.
- An empty `Getenv` closure (`emptyGetenv`) for fixtures without env —
  isolates interpolation from the developer's process env. The
  `runner.md` contract: process env never leaks into a fixture.

### Fixed — binding follow-up for config-spec PR #4 (ADR v2.1)

- `isHugeFloat` → `isOutsideSafeRange` with the bound `2^53-1` (i-JSON safe)
  instead of `2^63-1` (Go native int64). Brings `getInt` coercion and
  JSON-source normalization into line with the normative
  `_meta/coercion.yaml` and `_meta/canonical_json.yaml`.
- `TestJsonFileSourceIJSONSafeRange` — regression guard on the boundary.
- The `spec/` submodule is bumped to `09badaf` (ADR v2.1, new fixtures).

### Added — Phase C (sources + Load + getters + GetSection)

- `paths.go` — dot-notation parser (`a.b.c`, `a[N]`, `a[N].b`,
  `a[N][M]`) + `navigate` walker. Rejects negative indices,
  double dots, trailing dot, unclosed bracket, non-numeric index —
  all with `parse_error`.
- `sources.go` — three `Source` implementations:
  - `YamlFileSource` / `JsonFileSource` — interpolate raw text
    before parsing (parity with config-python), so YAML typing is
    preserved for `${INT_VAR}`.
  - `DictSource` — programmatic source with a builder API
    (`WithID`, `WithInterpolation`).
  - `JsonFileSource` normalizes whole-number `float64` → `int64`
    so that `GetInt` works uniformly with YAML.
- `config.go` — full replacement of the Phase A stubs:
  - `Load(ctx, path)` auto-discovers `<path>.local.yaml` +
    `<path>.${DAGSTACK_ENV}.yaml` (skipped silently if missing).
  - `LoadFrom(ctx, sources)` — deep-merge in priority order,
    applies the `Interpolate()` hint.
  - `Has` / `Get` / `GetString` / `GetInt` / `GetNumber` /
    `GetBool` / `GetList` + `*Default` variants — real bodies
    with coercion per spec §4.3.
  - `GetSection` via yaml round-trip; pre-checks that the subtree is a map,
    otherwise returns `type_mismatch` (parity with Python).
  - `Close` aggregates errors via `errors.Join` (ready for
    Phase 2 `Closer` implementations).
  - `Snapshot` — deep copy of the merged tree.
- 23 new unit tests — coverage 86.4%.

### Known issues / follow-ups

- `GetString` does not coerce int/float/bool (unlike config-python) —
  Go-strict, requires a spec clarification in §4.3.
- `has(path)` on an explicit-null now returns `true` (parity with
  config-python); ADR §4.3 doesn't formally clarify this behavior.
- yaml.v3 round-trip in `GetSection` — pragmatic for v0.1.0;
  migrating to `mapstructure` is a v0.2 follow-up.
- Thread safety for Phase 2: `tree` will become `atomic.Pointer[Tree]`,
  `sources` will be mutex-protected. See the comment in config.go.

### Added — Phase B (core primitives)

- `interpolation.go` — env interpolation `${VAR}` / `${VAR:-default}` with
  `$$` escape. The regex `[A-Z_][A-Z0-9_]*` locks down uppercase ASCII
  (POSIX-compatible). Nested `${...}` inside a default is not expanded.
  An empty value `${VAR}` when `VAR=""` returns `""` — parity with
  config-python. A nil input correctly returns an empty tree.
  The `interpolateTree` walker tracks the dot-notation path with indices
  `servers[1]` for diagnostics.
- `merge.go` — `deepMerge` with recursive merge for maps, atomic
  replacement for slices, deep copy to avoid shared backing
  storage. No mutation of inputs; nil-safe.
- `canonical_json.go` — the `canonicalJSON` serializer: sorted keys
  (UTF-8 code-point order), shortest round-trip floats, negative-zero
  normalization, rejects NaN / ±Infinity / invalid UTF-8 / non-Tree
  types. Deterministic bit-identical output.
- 23 new unit tests — coverage raised to 83%.

### Known issues (Phase B)

- Whole-number floats: Go emits `100` for `100.0` (RFC 8785 ES
  ToString), Python emits `100.0` (json.dumps default). Awaiting spec
  clarification in `dagstack/config-spec` before Phase D conformance.

### Added — Phase A (skeleton)

- Initialized the Go module `go.dagstack.dev/config`.
- Public types: `Config`, `Error`, `ErrorReason`, `Source`, `Watcher`,
  `Closer`, `Subscription`, `ChangeEvent`, `Tree`.
- Constants `ReasonMissing`, `ReasonTypeMismatch`, `ReasonEnvUnresolved`,
  `ReasonValidationFailed`, `ReasonParseError`, `ReasonSourceUnavailable`,
  `ReasonReloadRejected` — wire-stable, matching
  `spec/_meta/error_reasons.yaml`.
- Baseline unit tests: correctness of `Error.Error()`, idempotency of
  `Subscription.Unsubscribe`, the Phase 1 invariant for `OnChange`.
- CI configuration (Gitea, dagstack-runner, Go 1.22).
- Makefile with targets `test` / `vet` / `lint` / `fmt` / `tidy` /
  `conformance` / `clean`.

[Unreleased]: https://github.com/dagstack/config-go/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/dagstack/config-go/releases/tag/v0.4.0
[0.3.1]: https://github.com/dagstack/config-go/releases/tag/v0.3.1
[0.3.0]: https://github.com/dagstack/config-go/releases/tag/v0.3.0
[0.2.0]: https://github.com/dagstack/config-go/releases/tag/v0.2.0
[0.1.0]: https://github.com/dagstack/config-go/releases/tag/v0.1.0
