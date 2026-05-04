package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ── YamlFileSource ─────────────────────────────────────────────────

// YamlFileSource reads a YAML file from disk, applies env
// interpolation to the raw text, then decodes the result with yaml.v3.
//
// Interpolation is done BEFORE parsing so that a typed env value
// becomes the matching YAML type: `port: ${PORT}` with PORT=5432
// decodes to int, not string. This matches the config-python
// reference binding.
type YamlFileSource struct {
	path   string
	id     string
	getenv Getenv
}

// NewYamlFileSource constructs a YamlFileSource reading the given
// path. The env resolver is os.LookupEnv; use NewYamlFileSourceWithEnv
// for custom resolution (tests, secret managers).
func NewYamlFileSource(path string) *YamlFileSource {
	return NewYamlFileSourceWithEnv(path, nil)
}

// NewYamlFileSourceWithEnv is the dependency-injection variant —
// passes a custom Getenv for deterministic testing.
func NewYamlFileSourceWithEnv(path string, getenv Getenv) *YamlFileSource {
	if getenv == nil {
		getenv = osLookupEnv
	}
	return &YamlFileSource{
		path:   path,
		id:     "yaml:" + path,
		getenv: getenv,
	}
}

// ID implements Source.
func (s *YamlFileSource) ID() string { return s.id }

// Interpolate implements Source — YamlFileSource applies its own
// raw-text interpolation in Load, so the loader-level pass is
// disabled (false).
func (s *YamlFileSource) Interpolate() bool { return false }

// Load reads the file, interpolates `${VAR}` tokens in the raw text,
// and decodes YAML 1.2. Returns *Error with ReasonSourceUnavailable /
// ReasonParseError / ReasonEnvUnresolved as applicable.
func (s *YamlFileSource) Load(ctx context.Context) (Tree, error) {
	if err := ctx.Err(); err != nil {
		return nil, &Error{
			Reason:   ReasonSourceUnavailable,
			Details:  "context cancelled before load",
			SourceID: s.id,
			Wrapped:  err,
		}
	}

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, &Error{
			Reason:   ReasonSourceUnavailable,
			Details:  fmt.Sprintf("read %s: %v", s.path, err),
			SourceID: s.id,
			Wrapped:  err,
		}
	}

	interpolated, err := interpolateString(string(raw), s.getenv)
	if err != nil {
		return nil, &Error{
			Reason:   ReasonEnvUnresolved,
			Details:  err.Error(),
			SourceID: s.id,
			Wrapped:  err,
		}
	}

	tree, err := decodeYAML(interpolated, s.id)
	if err != nil {
		return nil, err
	}
	walked, err := walkSecretRefs(tree, s.id)
	if err != nil {
		return nil, err
	}
	return walked.(Tree), nil
}

// ── JsonFileSource ─────────────────────────────────────────────────

// JsonFileSource reads a JSON file. Semantics mirror YamlFileSource:
// interpolation on raw text, then parse. Used when YAML parser is
// unavailable or the file is produced by another tool (terraform,
// CI-generated config).
type JsonFileSource struct {
	path   string
	id     string
	getenv Getenv
}

// NewJsonFileSource constructs a JsonFileSource with default
// os.LookupEnv resolver.
func NewJsonFileSource(path string) *JsonFileSource {
	return NewJsonFileSourceWithEnv(path, nil)
}

// NewJsonFileSourceWithEnv is the DI variant.
func NewJsonFileSourceWithEnv(path string, getenv Getenv) *JsonFileSource {
	if getenv == nil {
		getenv = osLookupEnv
	}
	return &JsonFileSource{
		path:   path,
		id:     "json:" + path,
		getenv: getenv,
	}
}

// ID implements Source.
func (s *JsonFileSource) ID() string { return s.id }

// Interpolate implements Source. Interpolation runs inside Load on
// the raw file text, so the loader must not re-apply it (returns false).
func (s *JsonFileSource) Interpolate() bool { return false }

// Load reads, interpolates, parses. Same error taxonomy as
// YamlFileSource.
func (s *JsonFileSource) Load(ctx context.Context) (Tree, error) {
	if err := ctx.Err(); err != nil {
		return nil, &Error{
			Reason:   ReasonSourceUnavailable,
			Details:  "context cancelled before load",
			SourceID: s.id,
			Wrapped:  err,
		}
	}

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, &Error{
			Reason:   ReasonSourceUnavailable,
			Details:  fmt.Sprintf("read %s: %v", s.path, err),
			SourceID: s.id,
			Wrapped:  err,
		}
	}

	interpolated, err := interpolateString(string(raw), s.getenv)
	if err != nil {
		return nil, &Error{
			Reason:   ReasonEnvUnresolved,
			Details:  err.Error(),
			SourceID: s.id,
			Wrapped:  err,
		}
	}

	tree, err := decodeJSON(interpolated, s.id)
	if err != nil {
		return nil, err
	}
	walked, err := walkSecretRefs(tree, s.id)
	if err != nil {
		return nil, err
	}
	return walked.(Tree), nil
}

// ── DictSource ─────────────────────────────────────────────────────

// DictSource wraps an in-memory tree — the programmatic counterpart
// to file sources. Primary uses: tests, bootstrap scenarios, and
// override layers composed in code.
//
// Interpolation is off by default (consumer passes pre-built tree);
// enable with WithInterpolation option if the tree contains literal
// ${VAR} strings.
type DictSource struct {
	tree        Tree
	id          string
	interpolate bool
}

// NewDictSource constructs a DictSource with a default id. A shallow
// copy of the tree is stored — the caller may mutate the original
// without affecting the source.
func NewDictSource(tree Tree) *DictSource {
	return &DictSource{
		tree: copyTree(tree),
		id:   "dict:in-memory",
	}
}

// WithID overrides the default id for diagnostics (useful when
// stacking multiple DictSources).
func (s *DictSource) WithID(id string) *DictSource {
	s.id = id
	return s
}

// WithInterpolation enables loader-level interpolation of ${VAR}
// tokens inside string leaves of the stored tree.
func (s *DictSource) WithInterpolation() *DictSource {
	s.interpolate = true
	return s
}

// ID implements Source.
func (s *DictSource) ID() string { return s.id }

// Interpolate implements Source.
func (s *DictSource) Interpolate() bool { return s.interpolate }

// Load returns a fresh copy of the stored tree.
func (s *DictSource) Load(ctx context.Context) (Tree, error) {
	if err := ctx.Err(); err != nil {
		return nil, &Error{
			Reason:   ReasonSourceUnavailable,
			Details:  "context cancelled before load",
			SourceID: s.id,
			Wrapped:  err,
		}
	}
	walked, err := walkSecretRefs(copyTree(s.tree), s.id)
	if err != nil {
		return nil, err
	}
	return walked.(Tree), nil
}

// ── Decoding helpers ───────────────────────────────────────────────

// decodeYAML parses YAML 1.2 text into a Tree. Empty input decodes
// to an empty tree. Non-mapping root (top-level scalar or sequence)
// is rejected with parse_error.
func decodeYAML(text, sourceID string) (Tree, error) {
	var root any
	if err := yaml.Unmarshal([]byte(text), &root); err != nil {
		return nil, &Error{
			Reason:   ReasonParseError,
			Details:  fmt.Sprintf("yaml: %v", err),
			SourceID: sourceID,
			Wrapped:  err,
		}
	}
	return normaliseRoot(root, sourceID)
}

// decodeJSON parses JSON text into a Tree. encoding/json decodes every
// numeric literal as float64, so whole-number values like `"port": 5432`
// become float64(5432) — `GetInt` then fails type_mismatch even though
// the YAML equivalent works. normaliseJSONIntegers walks the parsed
// tree and converts float64 values with no fractional part to int64,
// restoring parity with YamlFileSource.
func decodeJSON(text, sourceID string) (Tree, error) {
	text = trimLeadingWhitespace(text)
	if text == "" {
		return Tree{}, nil
	}
	var root any
	if err := json.Unmarshal([]byte(text), &root); err != nil {
		return nil, &Error{
			Reason:   ReasonParseError,
			Details:  fmt.Sprintf("json: %v", err),
			SourceID: sourceID,
			Wrapped:  err,
		}
	}
	root = normaliseJSONIntegers(root)
	return normaliseRoot(root, sourceID)
}

// normaliseJSONIntegers walks a decoded JSON tree and converts whole-
// number float64 values to int64 where the value fits and has no
// fractional part. Fractional floats and out-of-range integers are
// left as float64. The walker is recursive over map[string]any and
// []any — all other kinds pass through unchanged.
func normaliseJSONIntegers(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = normaliseJSONIntegers(vv)
		}
		return out
	case []any:
		for i, vv := range x {
			x[i] = normaliseJSONIntegers(vv)
		}
		return x
	case float64:
		if x == float64(int64(x)) && !isOutsideSafeRange(x) {
			return int64(x)
		}
		return x
	default:
		return v
	}
}

// isOutsideSafeRange reports whether |x| exceeds the i-JSON safe
// integer range (|x| > 2^53-1 = 9_007_199_254_740_991). Values beyond
// this bound cannot round-trip through IEEE-754 double-precision or
// ECMAScript Number without precision loss, so they stay float64 —
// consumers access them via GetNumber, not GetInt. Matches spec
// ADR-0001 §4.3 coercion rules + _meta/coercion.yaml safe_range_limit.
//
// Note: Go's native int64 range is 2^63-1 (far wider), but emitting
// values in (2^53, 2^63) as int would silently lose precision when
// the same config is read by a JS or Python consumer. The i-JSON
// bound keeps bindings interoperable.
func isOutsideSafeRange(x float64) bool {
	const iJSONSafeMax = float64(1<<53 - 1) // 9007199254740991
	return x > iJSONSafeMax || x < -iJSONSafeMax
}

// normaliseRoot converts yaml.v3 / encoding/json decode output into a
// Tree. yaml.v3 produces map[string]any; json produces the same for
// objects. Nil (empty document) → empty tree.
//
// Non-object roots (scalar, sequence) violate spec §1 and return
// parse_error — config root is always a map.
func normaliseRoot(v any, sourceID string) (Tree, error) {
	if v == nil {
		return Tree{}, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, &Error{
			Reason:   ReasonParseError,
			Details:  fmt.Sprintf("config root must be a mapping, got %T", v),
			SourceID: sourceID,
		}
	}
	return Tree(m), nil
}

// trimLeadingWhitespace strips BOM and whitespace-only JSON input;
// needed because json.Unmarshal treats "" and whitespace as errors.
func trimLeadingWhitespace(s string) string {
	for i, r := range s {
		switch r {
		case ' ', '\t', '\r', '\n', 0xFEFF:
			continue
		default:
			return s[i:]
		}
	}
	return ""
}

// ── Compile-time checks: all three sources satisfy Source ──────────

var (
	_ Source = (*YamlFileSource)(nil)
	_ Source = (*JsonFileSource)(nil)
	_ Source = (*DictSource)(nil)
)

// isNotExist reports whether err signals "file missing", distinguishing
// it from other source_unavailable causes.
func isNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

// IsFileNotFound reports whether err wraps a "file not found" OS
// error from a YamlFileSource or JsonFileSource. Exposed for callers
// (Config.Load auto-discovery) that want to skip missing sibling
// files silently.
func IsFileNotFound(err error) bool { return isNotExist(err) }
