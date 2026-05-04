package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the primary handle on loaded configuration — returned by
// Load / LoadFrom and the target of all Get*, GetSection, OnChange,
// Reload, Close calls.
//
// Config is safe for concurrent reads: Get* / GetSection run against
// an immutable merged tree. RefreshSecrets performs an atomic swap
// of the tree under a write-lock; concurrent readers in flight
// observe either the pre-swap or post-swap tree, never a torn
// intermediate. Snapshot returns a masked deep copy by default; pass
// WithIncludeSecrets() for audit-mode access to resolved values.
type Config struct {
	mu            sync.RWMutex
	tree          Tree
	originalTree  Tree
	sources       []Source
	secretSources map[string]SecretSource
}

// Load reads a YAML file and its auto-discovered sibling layers:
//
//   - <path>                        — base (required).
//   - <path-without-ext>.local.yaml — developer overrides (optional).
//   - <path-without-ext>.${DAGSTACK_ENV}.yaml — env-specific (optional,
//     only when DAGSTACK_ENV is set in the environment).
//
// Missing local / env siblings are NOT an error — the loader skips
// them silently. The base path is required; its absence returns
// *Error with Reason=ReasonSourceUnavailable.
//
// Env interpolation runs on each file's raw text before YAML decode
// (see YamlFileSource.Load). Deep-merge combines the layers in
// priority order base → local → env (spec §3).
func Load(ctx context.Context, path string) (*Config, error) {
	sources := []Source{NewYamlFileSource(path)}

	// app-config.yaml → app-config.local.yaml
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	if ext == "" {
		ext = ".yaml"
	}

	localPath := base + ".local" + ext
	if fileExists(localPath) {
		sources = append(sources, NewYamlFileSource(localPath))
	}

	if env := os.Getenv("DAGSTACK_ENV"); env != "" {
		envPath := base + "." + env + ext
		if fileExists(envPath) {
			sources = append(sources, NewYamlFileSource(envPath))
		}
	}

	return LoadFrom(ctx, sources)
}

// LoadOption configures LoadFrom behaviour. Used for Phase 2
// SecretSource registration via `WithSecretSources`.
type LoadOption func(*loadOptions)

type loadOptions struct {
	secretSources []SecretSource
}

// WithSecretSources registers one or more SecretSource adapters for
// `${secret:<scheme>:...}` resolution per ADR-0002 §4. Each SecretSource
// is keyed by its Scheme(); duplicate schemes return *Error with
// Reason=ReasonValidationFailed at LoadFrom time. If no SecretSource
// is passed, an EnvSecretSource is auto-registered for the `env`
// scheme.
func WithSecretSources(sources ...SecretSource) LoadOption {
	return func(o *loadOptions) {
		o.secretSources = append(o.secretSources, sources...)
	}
}

// LoadFrom loads configuration from an explicit list of sources,
// merging them in priority order: the first source is lowest
// priority, the last overrides everything before it. Deep-merge for
// maps, atomic replacement for slices (spec §3).
//
// Sources are loaded sequentially; the first failing source short-
// circuits the load and returns its error. A source that declares
// Interpolate()=true has its string leaves passed through
// interpolateTree before being merged — useful for DictSource
// bootstrap with env placeholders. File sources interpolate on the
// raw text themselves and return false.
//
// Phase 2 secrets (ADR-0002 §3, §4): pass SecretSource adapters via
// WithSecretSources. After merging, every `${secret:<scheme>:...}`
// reference is eagerly resolved at LoadFrom time — Vault round-trips
// happen before LoadFrom returns. This matches the recommended mode
// in ADR-0002 §3 for long-lived servers and lets Get* getters stay
// sync. References to unregistered schemes without a `:-default`
// fail fast at LoadFrom (per §4 rule 3 — operator typos surface at
// startup, not at first request).
func LoadFrom(ctx context.Context, sources []Source, opts ...LoadOption) (*Config, error) {
	if len(sources) == 0 {
		return nil, &Error{
			Reason:  ReasonSourceUnavailable,
			Details: "no sources provided to LoadFrom",
		}
	}

	options := &loadOptions{}
	for _, opt := range opts {
		opt(options)
	}

	secretSources := make(map[string]SecretSource)
	for _, src := range options.secretSources {
		if existing, dup := secretSources[src.Scheme()]; dup {
			return nil, &Error{
				Reason: ReasonValidationFailed,
				Details: fmt.Sprintf("duplicate SecretSource scheme: %q "+
					"(already registered: %q, now adding: %q)",
					src.Scheme(), existing.ID(), src.ID()),
			}
		}
		secretSources[src.Scheme()] = src
	}
	if _, hasEnv := secretSources["env"]; !hasEnv {
		secretSources["env"] = NewEnvSecretSource()
	}

	merged := Tree{}
	for _, src := range sources {
		tree, err := src.Load(ctx)
		if err != nil {
			return nil, err
		}
		if src.Interpolate() {
			tree, err = interpolateTree(tree, nil)
			if err != nil {
				return nil, err
			}
		}
		merged = deepMerge(merged, tree)
	}

	// We retain the original (pre-resolved) tree so RefreshSecrets can
	// re-walk it, and Snapshot (without WithIncludeSecrets) can mask
	// SecretRef placeholders without leaking resolved values.
	original := copyTree(merged)
	resolved, err := resolveSecretRefs(ctx, merged, secretSources)
	if err != nil {
		return nil, err
	}

	return &Config{
		tree:          resolved,
		originalTree:  original,
		sources:       sources,
		secretSources: secretSources,
	}, nil
}

// resolveSecretRefs walks the merged tree, replacing every SecretRef
// with the resolved string value via the registered SecretSource.
// Adapters that fetch a multi-key envelope from a backend may keep
// their own internal cache for two refs to different `#field`
// projections of the same key.
func resolveSecretRefs(
	ctx context.Context,
	tree Tree,
	secretSources map[string]SecretSource,
) (Tree, error) {
	cache := make(map[string]SecretValue)
	resolved, err := walkResolve(ctx, tree, secretSources, cache)
	if err != nil {
		return nil, err
	}
	return resolved.(Tree), nil
}

func walkResolve(
	ctx context.Context,
	value any,
	secretSources map[string]SecretSource,
	cache map[string]SecretValue,
) (any, error) {
	switch v := value.(type) {
	case Tree:
		out := make(Tree, len(v))
		for k, val := range v {
			r, err := walkResolve(ctx, val, secretSources, cache)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			r, err := walkResolve(ctx, val, secretSources, cache)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			r, err := walkResolve(ctx, val, secretSources, cache)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case SecretRef:
		return resolveOneRef(ctx, v, secretSources, cache)
	default:
		return value, nil
	}
}

func resolveOneRef(
	ctx context.Context,
	ref SecretRef,
	secretSources map[string]SecretSource,
	cache map[string]SecretValue,
) (any, error) {
	cacheKey := ref.Scheme + ":" + ref.Path
	// ADR-0002 §3 cache rule: a SecretValue with ExpiresAt in the past
	// is a cache miss. ExpiresAt zero-value (time.Time{}) means cache
	// for the resolution-walk lifetime.
	if cached, ok := cache[cacheKey]; ok && !isExpired(cached.ExpiresAt) {
		return cached.Value, nil
	}

	source, registered := secretSources[ref.Scheme]
	if !registered {
		if ref.Default != nil {
			return *ref.Default, nil
		}
		schemes := make([]string, 0, len(secretSources))
		for k := range secretSources {
			schemes = append(schemes, "'"+k+"'")
		}
		sortStrings(schemes)
		return nil, &Error{
			Reason: ReasonSecretUnresolved,
			Details: fmt.Sprintf("no SecretSource registered for scheme %q "+
				"(referenced from %q); available schemes: [%s]",
				ref.Scheme, ref.OriginSource, joinStrings(schemes, ", ")),
		}
	}

	resolved, err := source.Resolve(ctx, ref.Path)
	if err != nil {
		if ref.Default != nil {
			// Fall back to default on any ConfigError from the source.
			var cerr *Error
			if errorsAs(err, &cerr) {
				return *ref.Default, nil
			}
		}
		return nil, err
	}
	cache[cacheKey] = resolved
	return resolved.Value, nil
}

// errorsAs is a tiny wrapper around the stdlib errors.As to keep this
// file's dependency graph easy to read.
func errorsAs(err error, target **Error) bool {
	return errors.As(err, target)
}

// sortStrings is a tiny helper avoiding sort imports for one place.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// joinStrings is a tiny helper avoiding strings.Join for one place.
func joinStrings(s []string, sep string) string {
	if len(s) == 0 {
		return ""
	}
	out := s[0]
	for i := 1; i < len(s); i++ {
		out += sep + s[i]
	}
	return out
}

// SnapshotOption configures Snapshot behaviour. Default (no options):
// every SecretRef in the tree is replaced with MaskedPlaceholder, and
// every plain string value whose key matches a secret-name pattern
// (_meta/secret_patterns.yaml, e.g. api_key, password) is also masked.
//
// WithIncludeSecrets opts in to audit-mode: the resolved tree is
// returned with field-name suffix masking still applied (ADR-0002 §3
// trigger table).
type SnapshotOption func(*snapshotOptions)

type snapshotOptions struct {
	includeSecrets bool
}

// WithIncludeSecrets returns the resolved (post-secret-resolution)
// tree from Snapshot, masking only by field-name suffix pattern.
// Treat the returned tree as sensitive — operators have explicitly
// opted in to seeing resolved secret values for audit / debug.
func WithIncludeSecrets() SnapshotOption {
	return func(o *snapshotOptions) {
		o.includeSecrets = true
	}
}

// Snapshot returns a deep copy of the merged config tree with secret-
// aware masking (ADR-0002 §3 "Resolution timing" trigger table).
//
// Default behaviour (no options): every SecretRef placeholder in the
// original tree is replaced with MaskedPlaceholder, AND every plain
// string value whose key matches a secret pattern is also replaced.
// No backend round-trip is performed; resolved secret values are NOT
// exposed.
//
// With WithIncludeSecrets() — audit-mode opt-in. The fully-resolved
// tree is returned with field-name suffix masking still applied.
//
// The returned tree is independent — mutating it does not affect
// subsequent Get* calls.
func (c *Config) Snapshot(opts ...SnapshotOption) Tree {
	if c == nil {
		return Tree{}
	}
	options := &snapshotOptions{}
	for _, opt := range opts {
		opt(options)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	source := c.originalTree
	if options.includeSecrets {
		source = c.tree
	}
	return walkSnapshot(source, options.includeSecrets, "").(Tree)
}

// RefreshSecrets drops the cached resolved tree and re-resolves every
// ${secret:...} reference against its registered SecretSource
// (ADR-0002 §3 "Forced refresh"). Manual rotation hook for Phase 2;
// push-based rotation is deferred to Phase 3.
//
// SecretValue.ExpiresAt is honoured at refresh time — a cached
// resolution whose ExpiresAt has passed is skipped within the per-
// walk dedup cache, so each stale path takes a fresh backend round-
// trip even if it appears more than once in the tree.
//
// Atomic from the caller's perspective: on failure the previously
// resolved tree remains the active tree, so a caller may safely
// retry. The internal reference is only swapped after a successful
// full re-walk under a write-lock; concurrent readers in flight
// observe either the previous or the next tree, never a torn
// intermediate.
func (c *Config) RefreshSecrets(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	original := c.originalTree
	sources := c.secretSources
	c.mu.RUnlock()

	resolved, err := resolveSecretRefs(ctx, original, sources)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.tree = resolved
	c.mu.Unlock()
	return nil
}

// isExpired implements the ADR-0002 §3 cache rule: a SecretValue with
// ExpiresAt in the past is a cache miss. Zero-value time.Time means
// cache for the resolution-walk lifetime.
func isExpired(expiresAt time.Time) bool {
	if expiresAt.IsZero() {
		return false
	}
	return !time.Now().Before(expiresAt)
}

// walkSnapshot masks SecretRef placeholders (when not including
// secrets) and field-name-pattern matches (always). Mirrors
// ADR-0002 §3 trigger table semantics.
func walkSnapshot(value any, includeSecrets bool, key string) any {
	switch v := value.(type) {
	case Tree:
		out := make(Tree, len(v))
		for k, val := range v {
			out[k] = walkSnapshot(val, includeSecrets, k)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = walkSnapshot(val, includeSecrets, k)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = walkSnapshot(val, includeSecrets, key)
		}
		return out
	case SecretRef:
		return MaskedPlaceholder
	case string:
		if v != "" && IsSecretField(key) {
			return MaskedPlaceholder
		}
		return v
	default:
		return v
	}
}

// SourceIDs returns the IDs of sources from which this Config was built,
// in load order (priority-ordered).
//
// ADR-0001 v2.1 §4.1: extension method, OPTIONAL in Phase 1. Cross-binding
// parity with Python `source_ids()` and TypeScript `sourceIds()`. Returns a
// copy so callers cannot mutate the internal source list.
func (c *Config) SourceIDs() []string {
	if c == nil {
		return nil
	}
	ids := make([]string, len(c.sources))
	for i, s := range c.sources {
		ids[i] = s.ID()
	}
	return ids
}

// ── Primitive getters (spec §4.3) ──────────────────────────────────

// currentTree returns the active resolved tree under a read-lock so
// it is safe to navigate concurrently with RefreshSecrets writers.
// Tree maps and slices are treated as read-only after assignment, so
// the returned reference can be navigated without holding the lock —
// the swap is atomic and the new tree is independently allocated.
func (c *Config) currentTree() Tree {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tree
}

// Has reports whether the path exists in the merged tree. An explicit
// null value at the path counts as "present" — matches config-python
// reference binding and spec §4.3 ("true if key/index exists at path").
func (c *Config) Has(path string) bool {
	if c == nil {
		return false
	}
	_, err := navigate(c.currentTree(), path)
	return err == nil
}

// Get returns the raw value at path. Returns *Error with
// Reason=ReasonMissing when the path is absent, ReasonTypeMismatch
// when an intermediate segment is the wrong kind, ReasonParseError
// when the path syntax is invalid.
func (c *Config) Get(path string) (any, error) {
	if c == nil {
		return nil, &Error{Path: path, Reason: ReasonMissing, Details: "Config is nil"}
	}
	return navigate(c.currentTree(), path)
}

// GetString returns the string value at path. Reason=ReasonTypeMismatch
// if the value is present but not a string.
func (c *Config) GetString(path string) (string, error) {
	v, err := c.Get(path)
	if err != nil {
		return "", err
	}
	return coerceString(v, path)
}

// GetStringDefault returns the string value at path, or def if the
// path is absent. Reason=ReasonTypeMismatch on type mismatch.
func (c *Config) GetStringDefault(path, def string) (string, error) {
	v, err := c.Get(path)
	if err != nil {
		if isMissing(err) {
			return def, nil
		}
		return "", err
	}
	return coerceString(v, path)
}

// GetInt returns the int64 value at path. Accepts native integers and
// env-interpolated strings matching ^-?\d+$ (spec §4.3).
func (c *Config) GetInt(path string) (int64, error) {
	v, err := c.Get(path)
	if err != nil {
		return 0, err
	}
	return coerceInt(v, path)
}

// GetIntDefault returns the int64 value at path, or def if absent.
func (c *Config) GetIntDefault(path string, def int64) (int64, error) {
	v, err := c.Get(path)
	if err != nil {
		if isMissing(err) {
			return def, nil
		}
		return 0, err
	}
	return coerceInt(v, path)
}

// GetNumber returns the float64 value at path.
func (c *Config) GetNumber(path string) (float64, error) {
	v, err := c.Get(path)
	if err != nil {
		return 0, err
	}
	return coerceNumber(v, path)
}

// GetNumberDefault returns the float64 value at path, or def if absent.
func (c *Config) GetNumberDefault(path string, def float64) (float64, error) {
	v, err := c.Get(path)
	if err != nil {
		if isMissing(err) {
			return def, nil
		}
		return 0, err
	}
	return coerceNumber(v, path)
}

// GetBool returns the bool value at path. Accepts native booleans and
// env-interpolated strings matching true/false/yes/no/1/0 (case-insensitive).
func (c *Config) GetBool(path string) (bool, error) {
	v, err := c.Get(path)
	if err != nil {
		return false, err
	}
	return coerceBool(v, path)
}

// GetBoolDefault returns the bool value at path, or def if absent.
func (c *Config) GetBoolDefault(path string, def bool) (bool, error) {
	v, err := c.Get(path)
	if err != nil {
		if isMissing(err) {
			return def, nil
		}
		return false, err
	}
	return coerceBool(v, path)
}

// GetList returns the raw []any at path.
func (c *Config) GetList(path string) ([]any, error) {
	v, err := c.Get(path)
	if err != nil {
		return nil, err
	}
	list, ok := v.([]any)
	if !ok {
		return nil, &Error{
			Path:    path,
			Reason:  ReasonTypeMismatch,
			Details: fmt.Sprintf("expected list, got %T", v),
		}
	}
	return list, nil
}

// ── Typed section access (spec §4.4) ───────────────────────────────

// GetSection extracts the subtree at path, decodes it into target
// (a pointer to a struct with `yaml:"..."` tags), and validates it
// against any struct-level validation rules.
//
// ADR-0001 v2.1 §4.4 Typed section access: env-substituted numeric/bool
// strings are coerced to their native types against target's struct fields
// BEFORE yaml.Unmarshal, so `port: "${DB_PORT:-5432}"` with `Port int`
// validates successfully. Reverse case (native int/float/bool in
// string-typed field) returns *Error(ReasonTypeMismatch).
//
// §4.5 Path preservation: nested decode failures produce Path with full
// dot-notation (section + field).
func (c *Config) GetSection(path string, target any) error {
	if target == nil {
		return &Error{
			Path:    path,
			Reason:  ReasonTypeMismatch,
			Details: "GetSection target is nil",
		}
	}
	v, err := c.Get(path)
	if err != nil {
		return err
	}

	// Pre-check: section root must be a mapping. Leaves / lists /
	// scalars → ReasonTypeMismatch, not ReasonValidationFailed
	// (matches config-python; clearer diagnostic than letting yaml
	// unmarshal error bubble up).
	if _, ok := toMap(v); !ok {
		return &Error{
			Path:    path,
			Reason:  ReasonTypeMismatch,
			Details: fmt.Sprintf("expected object (map) for section, got %T", v),
		}
	}

	// §4.4 env-string coercion + reverse case (native→string → type_mismatch).
	coerced, err := coerceSectionForTarget(v, target, path)
	if err != nil {
		return err
	}

	// Round-trip through yaml.v3 — the decoder honours `yaml:"..."`
	// tags on target, including default / omitempty semantics.
	raw, err := yaml.Marshal(coerced)
	if err != nil {
		return &Error{
			Path:    path,
			Reason:  ReasonValidationFailed,
			Details: fmt.Sprintf("marshal subtree: %v", err),
			Wrapped: err,
		}
	}
	if err := yaml.Unmarshal(raw, target); err != nil {
		// §4.5: best-effort extract field name from yaml.v3 error to build
		// full path (section.field). yaml.v3 error form:
		//   "yaml: unmarshal errors: line N: cannot unmarshal !!X into Y"
		// has no explicit location — fallback: path remains at the `section` level.
		return &Error{
			Path:    path,
			Reason:  ReasonValidationFailed,
			Details: fmt.Sprintf("decode into target: %v", err),
			Wrapped: err,
		}
	}
	return nil
}

// ── Subscriptions (spec §7.2, Phase 1 returns inactive) ────────────

// OnChange registers a callback invoked when the path (or any key
// below it) changes. In Phase 1 the returned Subscription has
// Active=false — no Source implements Watcher yet.
func (c *Config) OnChange(path string, callback func(ChangeEvent)) *Subscription {
	return NewInactiveSubscription(path, "no watch-capable source registered")
}

// OnSectionChange registers a typed-section callback. Like OnChange,
// Phase 1 returns an inactive Subscription.
func (c *Config) OnSectionChange(path string, target any, callback func(old, new any)) *Subscription {
	return NewInactiveSubscription(path, "no watch-capable source registered")
}

// Reload explicitly re-reads all sources and swaps the merged tree
// atomically. Phase 1 is a no-op.
func (c *Config) Reload(ctx context.Context) error { return nil }

// Close releases all source resources (file handles, network
// connections). Idempotent — repeated calls return nil.
func (c *Config) Close() error {
	if c == nil {
		return nil
	}
	var errs []error
	for _, src := range c.sources {
		if closer, ok := src.(Closer); ok {
			if err := closer.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	c.sources = nil
	return errors.Join(errs...)
}

// ── Coercion helpers ───────────────────────────────────────────────

// isMissing returns true when err is a *Error with ReasonMissing.
// Used by Get*Default methods to distinguish "absent" from "wrong type".
func isMissing(err error) bool {
	var cfgErr *Error
	if !asError(err, &cfgErr) {
		return false
	}
	return cfgErr.Reason == ReasonMissing
}

func asError(err error, target **Error) bool {
	for e := err; e != nil; {
		if cast, ok := e.(*Error); ok {
			*target = cast
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

func coerceString(v any, path string) (string, error) {
	if v == nil {
		return "", typeMismatch(path, "string", v)
	}
	if s, ok := v.(string); ok {
		return s, nil
	}
	return "", typeMismatch(path, "string", v)
}

func coerceInt(v any, path string) (int64, error) {
	switch x := v.(type) {
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case int64:
		return x, nil
	case uint:
		return int64(x), nil
	case uint32:
		return int64(x), nil
	case uint64:
		return int64(x), nil
	case string:
		// Spec §4.3 coercion — only ^-?\d+$.
		n, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return 0, typeMismatch(path, "int", v)
		}
		return n, nil
	default:
		return 0, typeMismatch(path, "int", v)
	}
}

func coerceNumber(v any, path string) (float64, error) {
	switch x := v.(type) {
	case int:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case uint:
		return float64(x), nil
	case uint32:
		return float64(x), nil
	case uint64:
		return float64(x), nil
	case float32:
		return float64(x), nil
	case float64:
		return x, nil
	case string:
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return 0, typeMismatch(path, "number", v)
		}
		return f, nil
	default:
		return 0, typeMismatch(path, "number", v)
	}
}

func coerceBool(v any, path string) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		switch strings.ToLower(x) {
		case "true", "yes", "1":
			return true, nil
		case "false", "no", "0":
			return false, nil
		}
		return false, typeMismatch(path, "bool", v)
	default:
		return false, typeMismatch(path, "bool", v)
	}
}

func typeMismatch(path, want string, got any) error {
	return &Error{
		Path:    path,
		Reason:  ReasonTypeMismatch,
		Details: fmt.Sprintf("expected %s, got %T (%v)", want, got, got),
	}
}

// fileExists is a tiny helper used by Load auto-discovery. Distinguishes
// "file missing" (skip silently) from other stat errors like EACCES —
// the latter return true so the subsequent YamlFileSource.Load surfaces
// the real diagnostic instead of the auto-discovery hiding it.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	return true
}
