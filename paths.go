package config

import (
	"fmt"
	"strconv"
	"strings"
)

// pathSegment is a single step in a dot-notation path — either a map
// key (str) or a slice index (int). Spec §4.2 defines the grammar;
// this file is the canonical Go parser + navigator.
type pathSegment struct {
	key   string // non-empty for map access
	index int    // valid only when key == ""
}

func (s pathSegment) isIndex() bool { return s.key == "" }

// parsePath splits a dot-notation path into segments, handling
// name / name[N] / [N] forms (spec §4.2).
//
// Examples:
//
//	parsePath("a.b.c")              → [a, b, c]
//	parsePath("cache.region.host")  → [cache, region, host]
//	parsePath("plugins[0].name")    → [plugins, [0], name]
//	parsePath("matrix[0][1]")       → [matrix, [0], [1]]
//
// Empty path or malformed syntax returns a *Error with Reason=ReasonParseError.
// Backslash-escaped dots in keys (spec §4.2 open question) are not
// supported in Phase C — see reference/path-syntax for scope.
func parsePath(path string) ([]pathSegment, error) {
	if path == "" {
		return nil, &Error{
			Path:    path,
			Reason:  ReasonParseError,
			Details: "path is empty",
		}
	}

	var segments []pathSegment
	i := 0
	// expectSeparator tracks whether the current position may accept a
	// '.' separator. A config path never starts with '.', and
	// consecutive '.' chars imply an empty segment — both rejected as
	// parse_error.
	expectSeparator := false
	for i < len(path) {
		switch ch := path[i]; {
		case ch == '.':
			if !expectSeparator {
				return nil, &Error{
					Path:    path,
					Reason:  ReasonParseError,
					Details: fmt.Sprintf("empty segment at offset %d", i),
				}
			}
			expectSeparator = false
			i++
		case ch == '[':
			end := strings.IndexByte(path[i:], ']')
			if end < 0 {
				return nil, &Error{
					Path:    path,
					Reason:  ReasonParseError,
					Details: fmt.Sprintf("unclosed bracket at offset %d", i),
				}
			}
			idxStr := path[i+1 : i+end]
			idx, err := strconv.Atoi(idxStr)
			if err != nil {
				return nil, &Error{
					Path:    path,
					Reason:  ReasonParseError,
					Details: fmt.Sprintf("invalid array index %q", idxStr),
				}
			}
			if idx < 0 {
				return nil, &Error{
					Path:    path,
					Reason:  ReasonParseError,
					Details: fmt.Sprintf("negative array index %d (spec §4.2 forbids)", idx),
				}
			}
			segments = append(segments, pathSegment{index: idx})
			i += end + 1
			expectSeparator = true
		default:
			// Key — everything up to next '.' or '['.
			j := i
			for j < len(path) && path[j] != '.' && path[j] != '[' {
				j++
			}
			if j == i {
				return nil, &Error{
					Path:    path,
					Reason:  ReasonParseError,
					Details: fmt.Sprintf("empty key at offset %d", i),
				}
			}
			segments = append(segments, pathSegment{key: path[i:j]})
			i = j
			expectSeparator = true
		}
	}

	if len(segments) == 0 {
		return nil, &Error{
			Path:    path,
			Reason:  ReasonParseError,
			Details: "path produced no segments",
		}
	}
	// Path must not end with an unterminated separator (`a.b.`).
	if !expectSeparator {
		return nil, &Error{
			Path:    path,
			Reason:  ReasonParseError,
			Details: "path ends with '.', no trailing segment",
		}
	}
	return segments, nil
}

// navigate walks the tree by path, returning the value at that location.
//
// Returns *Error:
//   - Reason=ReasonMissing when a segment points to a key/index that
//     does not exist in the traversed subtree.
//   - Reason=ReasonTypeMismatch when an intermediate segment expects
//     one kind (map vs. slice) and gets the other.
//   - Reason=ReasonParseError when the path itself is malformed.
//
// The returned error has its Path populated with the partial path
// that was successfully traversed plus the failing segment — the
// caller sees where in the path the lookup broke.
func navigate(tree any, path string) (any, error) {
	segments, err := parsePath(path)
	if err != nil {
		return nil, err
	}

	var current any = tree
	traversed := make([]pathSegment, 0, len(segments))
	for _, seg := range segments {
		traversed = append(traversed, seg)
		currentPath := formatTraversed(traversed)

		if seg.isIndex() {
			list, ok := toSlice(current)
			if !ok {
				return nil, &Error{
					Path:    currentPath,
					Reason:  ReasonTypeMismatch,
					Details: fmt.Sprintf("expected slice to index by position, got %T", current),
				}
			}
			if seg.index >= len(list) {
				return nil, &Error{
					Path:    currentPath,
					Reason:  ReasonMissing,
					Details: fmt.Sprintf("index %d out of range (length %d)", seg.index, len(list)),
				}
			}
			current = list[seg.index]
			continue
		}

		m, ok := toMap(current)
		if !ok {
			return nil, &Error{
				Path:    currentPath,
				Reason:  ReasonTypeMismatch,
				Details: fmt.Sprintf("expected map to index by key, got %T", current),
			}
		}
		next, exists := m[seg.key]
		if !exists {
			return nil, &Error{
				Path:    currentPath,
				Reason:  ReasonMissing,
				Details: fmt.Sprintf("key %q not found in map", seg.key),
			}
		}
		current = next
	}

	return current, nil
}

// toSlice normalises []any (the yaml.v3 decode target for sequences).
// Returns (nil, false) for any other type.
func toSlice(v any) ([]any, bool) {
	if s, ok := v.([]any); ok {
		return s, true
	}
	return nil, false
}

// formatTraversed renders a segment chain as human-readable
// dot-notation for inclusion in error.Path.
//
//	[{key: "a"}, {index: 0}, {key: "b"}] → "a[0].b"
func formatTraversed(segments []pathSegment) string {
	var b strings.Builder
	for i, seg := range segments {
		if seg.isIndex() {
			fmt.Fprintf(&b, "[%d]", seg.index)
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(seg.key)
	}
	return b.String()
}
