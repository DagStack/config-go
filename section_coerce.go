package config

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// ADR-0001 v2.1 §4.4: env-substituted strings are coerced to schema fields
// before yaml.Unmarshal. The walker traverses the merged subtree paired with
// the reflect.Type of the target, recognises expected numeric/bool fields,
// and converts matching strings using the regexes from `_meta/coercion.yaml`.
//
// Reverse case (§4.4 M1): a native int/float/bool in a string-typed field
// returns *Error(ReasonTypeMismatch) with the full dot-notation path (§4.5).

var (
	intStringRe    = regexp.MustCompile(`^-?\d+$`)
	numberStringRe = regexp.MustCompile(`^-?\d+(\.\d+)?([eE][-+]?\d+)?$`)
)

// coerceSectionForTarget walks a subtree (expected to be map[string]any)
// alongside the reflect.Type of target's struct and produces a new tree
// where env-substituted numeric/bool strings are converted to their
// native types.
//
// Returns *Error(ReasonTypeMismatch) on native-scalar-into-string-field.
// basePath is the user-visible section prefix used for path preservation.
func coerceSectionForTarget(subtree any, target any, basePath string) (any, error) {
	tv := reflect.ValueOf(target)
	if tv.Kind() != reflect.Ptr || tv.IsNil() {
		return subtree, nil
	}
	return coerceForType(subtree, tv.Elem().Type(), basePath)
}

func coerceForType(v any, t reflect.Type, path string) (any, error) {
	// Unwrap pointer types. `Elem()` on a `Ptr` returns the target type
	// safely even when the reflect.Value is zero — we operate on Type,
	// not Value, so a nil-panic is not possible. An explicit
	// t.Kind() != nil check is unnecessary: reflect.Type is never nil
	// for Go values.
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Struct:
		m := asStringMap(v)
		if m == nil {
			return v, nil
		}
		return coerceStruct(m, t, path)

	case reflect.Map:
		// Map with arbitrary keys — walk values against map's value type.
		m := asStringMap(v)
		if m == nil {
			return v, nil
		}
		out := make(map[string]any, len(m))
		vt := t.Elem()
		for k, mv := range m {
			childPath := appendPath(path, k)
			cv, err := coerceForType(mv, vt, childPath)
			if err != nil {
				return nil, err
			}
			out[k] = cv
		}
		return out, nil

	case reflect.Slice, reflect.Array:
		arr, ok := v.([]any)
		if !ok {
			return v, nil
		}
		elemType := t.Elem()
		out := make([]any, len(arr))
		for i, av := range arr {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			cv, err := coerceForType(av, elemType, childPath)
			if err != nil {
				return nil, err
			}
			out[i] = cv
		}
		return out, nil

	case reflect.Interface:
		// any / interface{} — no coercion, decoder accepts anything.
		return v, nil

	default:
		return coerceScalar(v, t, path)
	}
}

func coerceStruct(m map[string]any, t reflect.Type, path string) (any, error) {
	out := make(map[string]any, len(m))
	// Copy values not covered by struct fields verbatim; for matched keys,
	// walk their value against the field type.
	yamlToFieldType := structYamlIndex(t)
	for k, v := range m {
		fieldType, matched := yamlToFieldType[k]
		childPath := appendPath(path, k)
		if !matched {
			out[k] = v
			continue
		}
		cv, err := coerceForType(v, fieldType, childPath)
		if err != nil {
			return nil, err
		}
		out[k] = cv
	}
	return out, nil
}

// structYamlIndex builds map[yaml_key]reflect.Type for all tagged fields of t.
// Fields without yaml tag fall back to lowercase field name.
func structYamlIndex(t reflect.Type) map[string]reflect.Type {
	out := make(map[string]reflect.Type, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		key := yamlKeyFromField(f)
		if key == "" || key == "-" {
			continue
		}
		out[key] = f.Type
	}
	return out
}

func yamlKeyFromField(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("yaml")
	if !ok {
		return strings.ToLower(f.Name)
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return strings.ToLower(f.Name)
	}
	return name
}

// coerceScalar applies env-string coercion or reverse-case rejection for
// a single scalar value v against target field type t.
func coerceScalar(v any, t reflect.Type, path string) (any, error) {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if s, ok := v.(string); ok {
			if intStringRe.MatchString(s) {
				if n, err := strconv.ParseInt(s, 10, 64); err == nil {
					return n, nil
				}
			}
		}
		return v, nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if s, ok := v.(string); ok {
			if intStringRe.MatchString(s) && !strings.HasPrefix(s, "-") {
				if n, err := strconv.ParseUint(s, 10, 64); err == nil {
					return n, nil
				}
			}
		}
		return v, nil

	case reflect.Float32, reflect.Float64:
		if s, ok := v.(string); ok {
			if numberStringRe.MatchString(s) {
				if f, err := strconv.ParseFloat(s, 64); err == nil {
					return f, nil
				}
			}
		}
		return v, nil

	case reflect.Bool:
		if s, ok := v.(string); ok {
			switch strings.ToLower(s) {
			case "true", "yes", "1":
				return true, nil
			case "false", "no", "0":
				return false, nil
			}
		}
		return v, nil

	case reflect.String:
		// Reverse case (§4.4 M1): a native int/float/bool in a string-typed
		// field → type_mismatch. Guards against silent `dimension: 768` → `"768"`.
		switch v.(type) {
		case int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64,
			float32, float64, bool:
			return nil, &Error{
				Path:    path,
				Reason:  ReasonTypeMismatch,
				Details: fmt.Sprintf("expected string at %q, got %T (native non-string in string field — §4.4 reverse coerce)", path, v),
			}
		}
		return v, nil

	default:
		return v, nil
	}
}

func appendPath(base, segment string) string {
	if base == "" {
		return segment
	}
	return base + "." + segment
}

// asStringMap accepts either map[string]any or config.Tree (named type over
// the same underlying type) and returns an untyped map[string]any.
// Returns nil if v is not a string-keyed map at all.
func asStringMap(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	case Tree:
		return map[string]any(m)
	default:
		return nil
	}
}
