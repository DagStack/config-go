// Internal parser for `${secret:<scheme>:<path>[?query][#field][:-default]}`.
//
// Implements the grammar from ADR-0002 v1.1 §1 + `_meta/secret_ref_grammar.yaml`.
// The single public-API entry point is `parseSecretRef` — given the
// inner content of one `${secret:...}` token (the bytes between
// `${secret:` and `}`), it returns a SecretRef placeholder.
//
// The outer-token regex (`secretRefOuter`) is exposed for the YAML
// interpolator: a YAML string with multiple references is scanned with
// this pattern, and each match's group(1) is fed to parseSecretRef.
//
// Escape rules per ADR-0002 v1.1 §1:
//   - `##`  → literal `#` inside path
//   - `??`  → literal `?` inside path
//   - `::-` → literal `:-` inside path
//   - query_value uses RFC 3986 percent-encoding (url.QueryUnescape)

package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// secretRefOuter matches the WHOLE token shell. Group 1 is the inner
// content. Pattern matches `_meta/secret_ref_grammar.yaml` field
// `regex_outer.go` byte-for-byte.
var secretRefOuter = regexp.MustCompile(`\$\{secret:([^}]*)\}`)

// schemeRE — the lowercase ASCII scheme grammar from ADR-0002 §1.
var schemeRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Sentinel bytes for two-pass path-unescape. Using control bytes
// keeps the second pass from re-substituting characters legitimately
// present in the path.
const (
	sentQuery     = "\x00"
	sentHash      = "\x01"
	sentColonDash = "\x02"
)

// parseSecretRef parses the inner content of one `${secret:...}`
// token. `inner` is the string between `${secret:` and `}` (no
// braces). `originSource` is the diagnostic id of the source that
// emitted the token (typically a Source.ID()).
func parseSecretRef(inner, originSource string) (SecretRef, error) {
	// Step 1 — split off the optional `:-default` tail. Honour the
	// `::-` escape: literal `:-` inside path is written as `::-`.
	pathWithQueryField, defaultValue, hasDefault := splitDefault(inner)

	// Step 2 — split scheme from the rest. The first ":" terminates
	// scheme; ":" inside path is allowed and escape-free.
	schemeEnd := strings.IndexByte(pathWithQueryField, ':')
	if schemeEnd < 0 {
		return SecretRef{}, &Error{
			Reason:  ReasonParseError,
			Details: fmt.Sprintf("secret reference missing ':' between scheme and path: '${secret:%s}'", inner),
		}
	}
	scheme := pathWithQueryField[:schemeEnd]
	pathPart := pathWithQueryField[schemeEnd+1:]

	// Step 3 — validate scheme grammar.
	if !schemeRE.MatchString(scheme) {
		return SecretRef{}, &Error{
			Reason: ReasonParseError,
			Details: fmt.Sprintf("secret reference scheme %q does not match [a-z][a-z0-9_]*: '${secret:%s}'",
				scheme, inner),
		}
	}

	// Step 4 — split off the optional `#field` projection. Honour `##`.
	pathWithQuery, fieldProj, hasField := splitField(pathPart)

	// Step 5 — split off the optional `?query`. Honour `??`.
	pathOnly, query, hasQuery := splitQuery(pathWithQuery)

	// Step 6 — unescape the path: `??` → `?`, `##` → `#`, `::-` → `:-`.
	pathUnescaped := strings.ReplaceAll(pathOnly, "??", sentQuery)
	pathUnescaped = strings.ReplaceAll(pathUnescaped, "##", sentHash)
	pathUnescaped = strings.ReplaceAll(pathUnescaped, "::-", sentColonDash)
	if strings.ContainsAny(pathUnescaped, "?#") {
		bad := "?"
		if strings.ContainsRune(pathUnescaped, '#') {
			bad = "#"
		}
		return SecretRef{}, &Error{
			Reason: ReasonParseError,
			Details: fmt.Sprintf("unescaped %q in secret reference path "+
				"(use %q for a literal %q): '${secret:%s}'",
				bad, bad+bad, bad, inner),
		}
	}
	pathUnescaped = strings.ReplaceAll(pathUnescaped, sentQuery, "?")
	pathUnescaped = strings.ReplaceAll(pathUnescaped, sentHash, "#")
	pathUnescaped = strings.ReplaceAll(pathUnescaped, sentColonDash, ":-")

	// Compose the canonical full path: <unescaped-path>[?query][#field].
	fullPath := pathUnescaped
	if hasQuery {
		decoded, err := decodeQuery(query)
		if err != nil {
			return SecretRef{}, err
		}
		fullPath += "?" + decoded
	}
	if hasField {
		fullPath += "#" + strings.ReplaceAll(fieldProj, "##", "#")
	}

	ref := SecretRef{
		Scheme:       scheme,
		Path:         fullPath,
		OriginSource: originSource,
	}
	if hasDefault {
		v := defaultValue
		ref.Default = &v
	}
	return ref, nil
}

// splitDefault splits “...:-default“ honouring the “::-“ escape.
// Returns (head, default, hasDefault).
func splitDefault(s string) (string, string, bool) {
	i := 0
	n := len(s)
	for i < n-1 {
		if s[i] == ':' && s[i+1] == '-' {
			// ":-" preceded by ":" → "::-" escape. Consume past.
			if i > 0 && s[i-1] == ':' {
				i += 2
				continue
			}
			return s[:i], s[i+2:], true
		}
		i++
	}
	return s, "", false
}

// splitField splits “...#field“ honouring “##“. Returns (head,
// field-with-##-intact, hasField).
func splitField(s string) (string, string, bool) {
	i := 0
	n := len(s)
	for i < n {
		if s[i] == '#' {
			if i+1 < n && s[i+1] == '#' {
				i += 2
				continue
			}
			return s[:i], s[i+1:], true
		}
		i++
	}
	return s, "", false
}

// splitQuery splits “...?query“ honouring “??“. Returns (head,
// query-raw-still-percent-encoded, hasQuery).
func splitQuery(s string) (string, string, bool) {
	i := 0
	n := len(s)
	for i < n {
		if s[i] == '?' {
			if i+1 < n && s[i+1] == '?' {
				i += 2
				continue
			}
			return s[:i], s[i+1:], true
		}
		i++
	}
	return s, "", false
}

// decodeQuery decodes a percent-encoded query string per RFC 3986.
// Returns the canonical "key=value&key=value" form with values
// un-percent-encoded. Adapters parse keys/values themselves.
func decodeQuery(query string) (string, error) {
	parts := strings.Split(query, "&")
	out := make([]string, 0, len(parts))
	for _, kv := range parts {
		eqIdx := strings.IndexByte(kv, '=')
		if eqIdx < 0 {
			return "", &Error{
				Reason: ReasonParseError,
				Details: fmt.Sprintf("secret reference query parameter %q is missing '=' "+
					"(grammar: query_kv := query_key '=' query_value)", kv),
			}
		}
		key := kv[:eqIdx]
		value, err := url.QueryUnescape(kv[eqIdx+1:])
		if err != nil {
			return "", &Error{
				Reason: ReasonParseError,
				Details: fmt.Sprintf("secret reference query value for %q has malformed "+
					"percent-encoding: %v", key, err),
			}
		}
		out = append(out, key+"="+value)
	}
	return strings.Join(out, "&"), nil
}

// walkSecretRefs walks a freshly-loaded tree, converting `${secret:...}`
// strings to SecretRef placeholders.
//
// Called by each Source immediately after YAML/JSON parse. The Phase 1
// raw-text interpolator already left `${secret:...}` tokens intact
// (interpolation.go re-emits them verbatim), so this walker sees them
// as plain strings in scalar leaves.
//
// Behaviour:
//   - String leaf containing exactly one “${secret:...}“ token and
//     nothing else → replaced with a SecretRef.
//   - String leaf containing the token alongside other text → returns
//     ConfigError(ParseError) — splicing a secret into surrounding
//     text is ambiguous and not supported in Phase 2.
//   - String leaf with no token → unchanged.
//   - Mappings and slices → recursed.
func walkSecretRefs(tree any, sourceID string) (any, error) {
	switch v := tree.(type) {
	case Tree:
		out := make(Tree, len(v))
		for k, val := range v {
			converted, err := walkSecretRefs(val, sourceID)
			if err != nil {
				return nil, err
			}
			out[k] = converted
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			converted, err := walkSecretRefs(val, sourceID)
			if err != nil {
				return nil, err
			}
			out[k] = converted
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			converted, err := walkSecretRefs(val, sourceID)
			if err != nil {
				return nil, err
			}
			out[i] = converted
		}
		return out, nil
	case string:
		return convertString(v, sourceID)
	default:
		return tree, nil
	}
}

func convertString(s, sourceID string) (any, error) {
	matches := secretRefOuter.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s, nil
	}
	if len(matches) == 1 {
		m := matches[0]
		// m = [matchStart, matchEnd, group1Start, group1End]
		if m[0] == 0 && m[1] == len(s) {
			return parseSecretRef(s[m[2]:m[3]], sourceID)
		}
	}
	return nil, &Error{
		Reason: ReasonParseError,
		Details: fmt.Sprintf("a ${secret:...} reference must occupy the whole scalar value; "+
			"mixing it with other text is not supported (compose secrets at the "+
			"application level instead): %q", s),
		SourceID: sourceID,
	}
}
