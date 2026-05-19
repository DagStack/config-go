package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// interpolationRegex matches ${VAR} and ${VAR:-default} in a single
// string, capturing the variable name and optional default value.
//
// Regex breakdown:
//
//	\$\{                         — literal "${"
//	([A-Z_][A-Z0-9_]*)           — capture 1: POSIX-compatible env name
//	(?::-([^}]*))?               — capture 2: optional default after :-
//	\}                           — literal "}"
//
// The `[A-Z_][A-Z0-9_]*` shape pins names to uppercase ASCII — the
// portable convention from POSIX. Lowercase / Unicode env names are
// left unexpanded on purpose (they are not guaranteed portable across
// shells and CI systems).
var interpolationRegex = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)(?::-([^}]*))?\}`)

// Getenv resolves an env variable by name, returning the value and
// whether it is set. It mirrors os.LookupEnv, letting tests inject
// deterministic replacements and third-party wrappers plug in custom
// env providers — standard os.Getenv cannot distinguish "unset" from
// "empty", which spec §2 requires for `${VAR:-default}`.
type Getenv func(key string) (value string, ok bool)

// osLookupEnv is the default resolver — wraps os.LookupEnv.
func osLookupEnv(key string) (string, bool) { return os.LookupEnv(key) }

// interpolateString resolves all ${VAR} / ${VAR:-default} tokens in s
// using the supplied Getenv. The `$$` escape is processed before
// pattern substitution so that `$$` in the input always produces a
// literal `$` in the output, even in contexts without `${...}`.
//
// On an unresolved variable, returns a plain error naming the variable;
// the caller (interpolateNode) wraps it into *Error with the dot-notation
// path of the offending value.
func interpolateString(s string, getenv Getenv) (string, error) {
	// Step 1 — mask `$$` with a sentinel that cannot appear in valid
	// input. The sentinel contains a byte value (0x01) that is a
	// control character disallowed by YAML content anyway; we also
	// unmask after substitution, so even if the user had 0x01
	// somewhere we do not leak it.
	const sentinel = "\x01\x02"
	masked := strings.ReplaceAll(s, "$$", sentinel)

	// Step 2 — scan for interpolation tokens. We cannot use
	// ReplaceAllStringFunc because it swallows errors; emulate it
	// explicitly to keep the env_unresolved reason precise.
	matches := interpolationRegex.FindAllStringSubmatchIndex(masked, -1)
	if len(matches) == 0 {
		return strings.ReplaceAll(masked, sentinel, "$"), nil
	}

	var b strings.Builder
	b.Grow(len(masked))
	cursor := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		nameStart, nameEnd := m[2], m[3]
		hasDefault := m[4] != -1
		defaultStart, defaultEnd := m[4], m[5]

		b.WriteString(masked[cursor:start])

		name := masked[nameStart:nameEnd]
		val, ok := getenv(name)
		switch {
		case ok && val != "":
			b.WriteString(val)
		case ok && hasDefault:
			// Spec §2: empty env value with `:-` falls back to default.
			b.WriteString(masked[defaultStart:defaultEnd])
		case ok:
			// Spec §2: bare `${VAR}` with VAR="" returns "". Only the
			// `:-default` form treats empty as unset.
			// (no write — empty string is the resolved value)
		case hasDefault:
			// VAR not set, fallback to default.
			b.WriteString(masked[defaultStart:defaultEnd])
		default:
			return "", fmt.Errorf("env variable %q is not set", name)
		}

		cursor = end
	}
	b.WriteString(masked[cursor:])

	// Step 3 — unmask `$$` sentinel back to literal `$`.
	return strings.ReplaceAll(b.String(), sentinel, "$"), nil
}

// interpolateTree walks the config tree and applies string-level
// interpolation to every string leaf. Keys are NOT interpolated
// (per spec §2: "object keys are not interpolated").
//
// Returns a new tree — the input is not mutated. Errors surface with
// the dot-notation path of the offending value. A nil input yields an
// empty tree (matches deepMerge's nil handling convention).
func interpolateTree(tree Tree, getenv Getenv) (Tree, error) {
	if getenv == nil {
		getenv = osLookupEnv
	}
	if tree == nil {
		return Tree{}, nil
	}
	out, err := interpolateNode(tree, "", getenv)
	if err != nil {
		return nil, err
	}
	return out.(Tree), nil
}

// interpolateNode is the recursive helper. It dispatches on the value
// kind (map / slice / string / other) and returns a new node of the
// same shape.
func interpolateNode(v any, path string, getenv Getenv) (any, error) {
	switch x := v.(type) {
	case Tree:
		return interpolateMap(x, path, getenv)
	case map[string]any:
		return interpolateMap(Tree(x), path, getenv)
	case []any:
		out := make([]any, len(x))
		for i, elem := range x {
			itemPath := fmt.Sprintf("%s[%d]", path, i)
			r, err := interpolateNode(elem, itemPath, getenv)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case string:
		result, err := interpolateString(x, getenv)
		if err != nil {
			return nil, &Error{
				Path:    path,
				Reason:  ReasonEnvUnresolved,
				Details: err.Error(),
			}
		}
		return result, nil
	default:
		// Non-string scalar (int, float, bool, nil) — pass through.
		return v, nil
	}
}

func interpolateMap(m Tree, path string, getenv Getenv) (Tree, error) {
	out := make(Tree, len(m))
	for k, v := range m {
		childPath := k
		if path != "" {
			childPath = path + "." + k
		}
		r, err := interpolateNode(v, childPath, getenv)
		if err != nil {
			return nil, err
		}
		out[k] = r
	}
	return out, nil
}
