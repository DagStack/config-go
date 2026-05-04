// Phase 2 secret references and SecretSource adapters (ADR-0002 §2/§3/§4).
//
// SecretSource is the contract every secret backend implements; the
// loader dispatches `${secret:<scheme>:<path>}` references to the
// registered source by `Scheme()`. Phase 2 normative implementations:
//
//   - EnvSecretSource (mandatory in-process adapter for the `env`
//     scheme), declared in this file.
//   - vault.Source (optional opt-in sub-module
//     `go.dagstack.dev/config/vault`, see that module's README and
//     `adr/0001-vault-source.md` for details on the
//     hashicorp/vault/api SDK choice and packaging).
//
// Stutter-avoidance note (per spec _meta/types.yaml row): in Phase 2
// the Go binding does not extract secrets to a sub-package, so
// `config.SecretSource` does not stutter (the package is `config`,
// the prefix is `Secret`). If a future binding extracts secrets to
// `config/secret`, the type rename in `_meta/types.yaml` will
// surface as `secret.Source` per §4.5 stutter-avoidance and §8.2
// idiom guidance.

package config

import (
	"context"
	"os"
	"time"
)

// SecretSource is the contract for secret backends per ADR-0002 §2.
//
// Distinct from `Source` (the ConfigSource contract): secrets resolve
// lazily by key, not eagerly as a tree. Phase 2 normative
// implementations: EnvSecretSource (mandatory) and VaultSource
// (optional sub-module).
type SecretSource interface {
	// Scheme returns the short scheme name; matches the leading token
	// in `${secret:<scheme>:...}`. Lowercase ASCII; the loader uses it
	// as a registry key.
	Scheme() string

	// ID returns a human-readable identifier (URI-style by convention,
	// e.g. "vault:https://vault.example.com"). Carried in
	// SecretValue.SourceID and Error.SourceID for diagnostics.
	ID() string

	// Resolve returns the SecretValue for the given path. Adapters
	// own parsing of any `?query` and `#field` projection inside
	// path. Errors MUST use ConfigError with one of the three
	// secret-related Reasons (Unresolved / BackendUnavailable /
	// PermissionDenied).
	Resolve(ctx context.Context, path string) (SecretValue, error)

	// Close releases resources (HTTP pool, lease renewal goroutine,
	// background tickers). The loader calls Close() on every
	// registered SecretSource when Config.Close() is called.
	Close() error
}

// AsyncSecretSource is provided as a type alias to SecretSource for
// cross-binding parity (per spec _meta/types.yaml). Go's
// `context.Context` already encodes async semantics through
// cancellation; the single SecretSource interface covers both sync
// and async use-cases. Re-exported as an alias so cross-binding
// documentation can reference a single name.
type AsyncSecretSource = SecretSource

// SecretRef is an opaque placeholder for an unresolved
// `${secret:...}` reference. Lives in the merged config tree mixed
// with regular scalars after `LoadFrom(...)`. Resolved transparently
// at load time (eager) per the Go binding's design choice; see
// LoadFrom for details.
type SecretRef struct {
	Scheme       string
	Path         string
	Default      *string // nil if no `:-default` was specified
	OriginSource string  // ConfigSource.ID where the token came from
}

// SecretValue is the result of SecretSource.Resolve.
//
// Value is always a string at the wire level — type coercion happens
// at the Get* call site. Bindings MUST NOT JSON-parse the value into
// a sub-tree; sub-key projection is handled by the adapter via the
// `#field` syntax in the input path (ADR-0002 §1.2).
type SecretValue struct {
	Value     string
	SourceID  string
	Version   string    // optional; empty if backend does not version
	ExpiresAt time.Time // zero value if backend does not expire
}

// EnvSecretSource is the mandatory in-process SecretSource for the
// `env` scheme.
//
// `${secret:env:VAR}` is semantically identical to `${VAR}` from
// ADR-0001 §2 — the env scheme is a degenerate case of secret
// resolution. Auto-registered by the loader if the consumer does not
// pass one explicitly (ADR-0002 §4 rule 2).
//
// The env scheme operates on env-var names; it does not support the
// `?query` or `#field` projection (env values are opaque single-value
// strings). If an operator tries to use them, the source raises
// secret_unresolved with an actionable hint pointing at structured
// backends (such as VaultSource).
type EnvSecretSource struct {
	// Lookup is the function used to read env vars. nil → os.Getenv.
	// Tests may inject a deterministic table.
	Lookup func(name string) (string, bool)
}

// NewEnvSecretSource returns an EnvSecretSource backed by os.LookupEnv.
func NewEnvSecretSource() *EnvSecretSource {
	return &EnvSecretSource{}
}

// Scheme returns "env".
func (*EnvSecretSource) Scheme() string { return "env" }

// ID returns "env:os.environ".
func (*EnvSecretSource) ID() string { return "env:os.environ" }

// Resolve looks up `path` in the environment.
func (s *EnvSecretSource) Resolve(ctx context.Context, path string) (SecretValue, error) {
	if err := ctx.Err(); err != nil {
		return SecretValue{}, &Error{
			Reason:   ReasonSecretBackendUnavailable,
			Details:  "context cancelled before env lookup: " + err.Error(),
			SourceID: s.ID(),
			Wrapped:  err,
		}
	}
	for _, sep := range []struct{ ch, hint string }{
		{"?", "query parameters"},
		{"#", "sub-key projection"},
	} {
		if containsRune(path, sep.ch) {
			return SecretValue{}, &Error{
				Reason: ReasonSecretUnresolved,
				Details: "env scheme does not support " + sep.hint +
					" ('" + sep.ch + "' in " + quote(path) + "); " +
					"env values are opaque single-value strings — switch to " +
					"a backend with structured secrets (e.g. VaultSource for " +
					"HashiCorp Vault KV v2) if you need them.",
				SourceID: s.ID(),
			}
		}
	}

	lookup := s.Lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	value, ok := lookup(path)
	if !ok {
		return SecretValue{}, &Error{
			Reason: ReasonSecretUnresolved,
			Details: "env:" + path + " is not set in the process environment " +
				"and the reference has no default",
			SourceID: s.ID(),
		}
	}
	return SecretValue{Value: value, SourceID: s.ID()}, nil
}

// Close is a no-op — env source holds no resources.
func (*EnvSecretSource) Close() error { return nil }

// containsRune is a tiny helper avoiding strings.Contains for a single
// byte — keeps this file dependency-light.
func containsRune(s, ch string) bool {
	for i := 0; i < len(s); i++ {
		if string(s[i]) == ch {
			return true
		}
	}
	return false
}

// quote produces a Go-idiomatic %q-style representation suitable for
// error messages without pulling fmt for a one-line dependency.
func quote(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' || c == '\\' {
			out = append(out, '\\', c)
			continue
		}
		out = append(out, c)
	}
	out = append(out, '"')
	return string(out)
}
