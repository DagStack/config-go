package config

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// canonicalJSON serialises v as Canonical JSON per spec §9.1.1 and
// _meta/canonical_json.yaml. The output is UTF-8, no BOM, no trailing
// newline, sorted keys at every level, minimal whitespace, shortest
// round-trip floats, integers without decimal point.
//
// Supported input kinds: Tree, map[string]any, []any, string, bool,
// all signed and unsigned int types, float32, float64, and nil. Any
// other kind returns *Error with Reason=ReasonTypeMismatch. NaN and
// ±Infinity are rejected. Non-string map keys are rejected.
//
// The function assumes the input tree is acyclic — YAML 1.2 parsers
// (yaml.v3) reject anchor-cycles at parse time, so the normal load
// path cannot construct one. A manually-built cyclic map will stack-
// overflow here; do not feed one in.
//
// The output is deterministic: the same input produces bit-identical
// bytes across calls and platforms (verified by TestCanonicalJSONIdempotent).
//
// The function is unexported by design — application code should not
// call this directly (use the stdlib `encoding/json` package). The
// only legitimate public consumer is the cross-binding conformance
// runner (`config-spec/scripts/canonical_go`), which compares this
// binding's canonical bytes against `config-python` and
// `config-typescript` outputs for the same fixture. That runner
// reaches the function through the exported `CanonicalJSON` alias
// declared just below.
func canonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CanonicalJSON exposes the internal canonical-JSON serialiser to the
// cross-binding conformance runner (`dagstack/config-spec` workflow
// `cross-binding-roundtrip.yml`).
//
// **Application code MUST NOT call this** — the canonical form is
// wire-internal to the spec, and using it as a general-purpose JSON
// serialiser will produce surprising output (sorted keys, no
// indentation, shortest-roundtrip floats). Use `encoding/json` for
// regular serialisation.
//
// The function exists as an exported alias because Go does not have
// a build-tag-test-only export mechanism comparable to Python's
// `from .canonical_json import canonical_json_dumps as _internal`.
// The minimal-export approach keeps the wire-internal status legible
// at the call site (consumers see the `CanonicalJSON` doc comment
// every time they import).
func CanonicalJSON(v any) ([]byte, error) {
	return canonicalJSON(v)
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
		return nil
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil
	case string:
		return writeCanonicalString(buf, x)
	case int:
		buf.WriteString(strconv.FormatInt(int64(x), 10))
		return nil
	case int8:
		buf.WriteString(strconv.FormatInt(int64(x), 10))
		return nil
	case int16:
		buf.WriteString(strconv.FormatInt(int64(x), 10))
		return nil
	case int32:
		buf.WriteString(strconv.FormatInt(int64(x), 10))
		return nil
	case int64:
		buf.WriteString(strconv.FormatInt(x, 10))
		return nil
	case uint:
		buf.WriteString(strconv.FormatUint(uint64(x), 10))
		return nil
	case uint8:
		buf.WriteString(strconv.FormatUint(uint64(x), 10))
		return nil
	case uint16:
		buf.WriteString(strconv.FormatUint(uint64(x), 10))
		return nil
	case uint32:
		buf.WriteString(strconv.FormatUint(uint64(x), 10))
		return nil
	case uint64:
		buf.WriteString(strconv.FormatUint(x, 10))
		return nil
	case float32:
		return writeCanonicalFloat(buf, float64(x))
	case float64:
		return writeCanonicalFloat(buf, x)
	case []any:
		return writeCanonicalArray(buf, x)
	case Tree:
		return writeCanonicalObject(buf, x)
	case map[string]any:
		return writeCanonicalObject(buf, Tree(x))
	default:
		return &Error{
			Reason:  ReasonTypeMismatch,
			Details: fmt.Sprintf("canonicalJSON: unsupported type %T", v),
		}
	}
}

// writeCanonicalString emits a JSON string with minimal escaping.
// Per spec:
//   - required escapes: `\"`, `\\`, `\b`, `\f`, `\n`, `\r`, `\t`;
//   - control characters below 0x20 without a shortcut → `\u00XX`;
//   - everything else, including all non-ASCII UTF-8, passes through
//     as-is.
//
// Invalid UTF-8 sequences are rejected — canonical JSON requires
// well-formed UTF-8.
func writeCanonicalString(buf *bytes.Buffer, s string) error {
	if !utf8.ValidString(s) {
		return &Error{
			Reason:  ReasonTypeMismatch,
			Details: "canonicalJSON: invalid UTF-8 in string",
		}
	}
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
				continue
			}
			buf.WriteRune(r)
		}
	}
	buf.WriteByte('"')
	return nil
}

// writeCanonicalFloat emits a float using shortest round-trip
// representation (`strconv.FormatFloat(..., 'g', -1, 64)`), with
// negative-zero normalisation and rejection of NaN / ±Infinity.
//
// NOTE(phase-d-parity): Go emits `100` for `100.0`, matching RFC 8785
// ECMAScript ToString semantics. Python's reference binding currently
// emits `100.0` (json.dumps default). Until the spec resolves this
// ambiguity (issue to be filed on dagstack/config-spec), conformance
// fixtures that round-trip whole-number floats will diverge between
// bindings — pin expected outputs to non-integer floats in Phase D
// fixtures for now.
func writeCanonicalFloat(buf *bytes.Buffer, f float64) error {
	if math.IsNaN(f) {
		return &Error{Reason: ReasonTypeMismatch, Details: "canonicalJSON: NaN not allowed"}
	}
	if math.IsInf(f, 0) {
		return &Error{Reason: ReasonTypeMismatch, Details: "canonicalJSON: ±Infinity not allowed"}
	}
	// Negative-zero normalisation (spec §9.1.1, RFC 8785 §3.2.2.3).
	if f == 0 {
		f = 0
	}
	buf.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
	return nil
}

func writeCanonicalArray(buf *bytes.Buffer, arr []any) error {
	buf.WriteByte('[')
	for i, v := range arr {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := writeCanonical(buf, v); err != nil {
			return err
		}
	}
	buf.WriteByte(']')
	return nil
}

// sortKeysUTF16 sorts keys lexicographically by their UTF-16 code-unit
// sequence per RFC 8785 §3.2.3. This differs from sort.Strings (UTF-8 byte
// order) on supplementary-plane code points: a BMP private-use key such as
// U+E000 sorts after U+20000 because U+20000 is represented as the high
// surrogate D840 in UTF-16 and 0xD840 < 0xE000, whereas UTF-8 byte order
// would put U+E000 first (EE 80 80 < F0 A0 80 80). The cross-binding
// fixture spec/conformance/canonical_json/key_order_drift_witness.json
// pins the conformant outcome.
func sortKeysUTF16(keys []string) {
	sort.SliceStable(keys, func(i, j int) bool {
		a := utf16.Encode([]rune(keys[i]))
		b := utf16.Encode([]rune(keys[j]))
		minLen := len(a)
		if len(b) < minLen {
			minLen = len(b)
		}
		for k := 0; k < minLen; k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
}

// writeCanonicalObject serialises a map as a JSON object with keys
// sorted in lexicographic UTF-16 code-unit order per RFC 8785 §3.2.3.
//
// Non-string keys are structurally impossible here — Tree is
// map[string]any — so the `non_string_dict_keys: reject` rule from
// _meta/canonical_json.yaml is enforced by the type system rather
// than runtime validation.
func writeCanonicalObject(buf *bytes.Buffer, obj Tree) error {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sortKeysUTF16(keys)

	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := writeCanonicalString(buf, k); err != nil {
			return err
		}
		buf.WriteByte(':')
		if err := writeCanonical(buf, obj[k]); err != nil {
			return err
		}
	}
	buf.WriteByte('}')
	return nil
}
