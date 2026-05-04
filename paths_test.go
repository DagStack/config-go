package config

import (
	"errors"
	"reflect"
	"testing"
)

func TestParsePathBasic(t *testing.T) {
	cases := []struct {
		in   string
		want []pathSegment
	}{
		{"a", []pathSegment{{key: "a"}}},
		{"a.b.c", []pathSegment{{key: "a"}, {key: "b"}, {key: "c"}}},
		{"cache.region.host", []pathSegment{{key: "cache"}, {key: "region"}, {key: "host"}}},
		{"plugins[0]", []pathSegment{{key: "plugins"}, {index: 0}}},
		{"plugins[0].name", []pathSegment{{key: "plugins"}, {index: 0}, {key: "name"}}},
		{"matrix[0][1]", []pathSegment{{key: "matrix"}, {index: 0}, {index: 1}}},
		{"a.b[2].c[3]", []pathSegment{{key: "a"}, {key: "b"}, {index: 2}, {key: "c"}, {index: 3}}},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := parsePath(c.in)
			if err != nil {
				t.Fatalf("parsePath(%q): %v", c.in, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestParsePathErrors(t *testing.T) {
	cases := []string{
		"",       // empty
		"a[",     // unclosed bracket
		"a[abc]", // non-numeric index
		"a[-1]",  // negative index
		"a..b",   // double dot — empty key at offset 2 (after first dot consumption)
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := parsePath(in)
			if err == nil {
				t.Fatalf("parsePath(%q) expected error, got nil", in)
			}
			var cfgErr *Error
			if !errors.As(err, &cfgErr) {
				t.Errorf("error is not *Error: %T", err)
			}
			if cfgErr.Reason != ReasonParseError {
				t.Errorf("Reason = %q, want %q", cfgErr.Reason, ReasonParseError)
			}
		})
	}
}

func TestNavigateSimpleMap(t *testing.T) {
	tree := Tree{
		"database": Tree{
			"host": "localhost",
			"port": 5432,
		},
		"flags": []any{"a", "b", "c"},
	}

	cases := []struct {
		path string
		want any
	}{
		{"database.host", "localhost"},
		{"database.port", 5432},
		{"flags[0]", "a"},
		{"flags[2]", "c"},
	}
	for _, c := range cases {
		got, err := navigate(tree, c.path)
		if err != nil {
			t.Errorf("navigate(%q): %v", c.path, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("navigate(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestNavigateMissingKey(t *testing.T) {
	tree := Tree{"a": Tree{"b": 1}}
	_, err := navigate(tree, "a.missing")

	var cfgErr *Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if cfgErr.Reason != ReasonMissing {
		t.Errorf("Reason = %q, want %q", cfgErr.Reason, ReasonMissing)
	}
	if cfgErr.Path != "a.missing" {
		t.Errorf("Path = %q, want a.missing", cfgErr.Path)
	}
}

func TestNavigateIndexOutOfRange(t *testing.T) {
	tree := Tree{"list": []any{"a", "b"}}
	_, err := navigate(tree, "list[5]")

	var cfgErr *Error
	errors.As(err, &cfgErr)
	if cfgErr.Reason != ReasonMissing {
		t.Errorf("Reason = %q, want %q", cfgErr.Reason, ReasonMissing)
	}
	if cfgErr.Path != "list[5]" {
		t.Errorf("Path = %q, want list[5]", cfgErr.Path)
	}
}

func TestNavigateTypeMismatchOnKeyIntoSlice(t *testing.T) {
	tree := Tree{"list": []any{"a"}}
	_, err := navigate(tree, "list.notAnIndex")

	var cfgErr *Error
	errors.As(err, &cfgErr)
	if cfgErr.Reason != ReasonTypeMismatch {
		t.Errorf("Reason = %q, want %q", cfgErr.Reason, ReasonTypeMismatch)
	}
}

func TestNavigateTypeMismatchOnIndexIntoMap(t *testing.T) {
	tree := Tree{"obj": Tree{"k": "v"}}
	_, err := navigate(tree, "obj[0]")

	var cfgErr *Error
	errors.As(err, &cfgErr)
	if cfgErr.Reason != ReasonTypeMismatch {
		t.Errorf("Reason = %q, want %q", cfgErr.Reason, ReasonTypeMismatch)
	}
}

func TestFormatTraversed(t *testing.T) {
	cases := []struct {
		in   []pathSegment
		want string
	}{
		{[]pathSegment{{key: "a"}}, "a"},
		{[]pathSegment{{key: "a"}, {key: "b"}}, "a.b"},
		{[]pathSegment{{key: "a"}, {index: 0}}, "a[0]"},
		{[]pathSegment{{key: "a"}, {index: 0}, {key: "b"}}, "a[0].b"},
		{[]pathSegment{{key: "m"}, {index: 1}, {index: 2}}, "m[1][2]"},
	}
	for _, c := range cases {
		got := formatTraversed(c.in)
		if got != c.want {
			t.Errorf("formatTraversed(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}
