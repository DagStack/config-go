package config

import (
	"errors"
	"math"
	"testing"
)

func TestCanonicalJSONPrimitives(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "null"},
		{true, "true"},
		{false, "false"},
		{"hello", `"hello"`},
		{"", `""`},
		{42, "42"},
		{-42, "-42"},
		{int64(9007199254740991), "9007199254740991"}, // I-JSON max safe
		{uint64(42), "42"},
		{3.14, "3.14"},
		{0.0, "0"},
		{-0.0, "0"}, // negative-zero normalisation
	}
	for _, c := range cases {
		got, err := canonicalJSON(c.in)
		if err != nil {
			t.Errorf("canonicalJSON(%v): %v", c.in, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("canonicalJSON(%v) = %q, want %q", c.in, string(got), c.want)
		}
	}
}

func TestCanonicalJSONFloatShortestRoundTrip(t *testing.T) {
	// Go's strconv.FormatFloat(v, 'g', -1, 64) matches ECMAScript
	// shortest round-trip. Spot-check a few typical values.
	got, err := canonicalJSON(1.5)
	if err != nil || string(got) != "1.5" {
		t.Errorf("1.5 → %q (err %v)", got, err)
	}
	got, _ = canonicalJSON(100.0)
	if string(got) != "100" {
		t.Errorf("100.0 → %q, want 100", got)
	}
	got, _ = canonicalJSON(1e20)
	if string(got) != "1e+20" {
		t.Errorf("1e20 → %q", got)
	}
}

func TestCanonicalJSONRejectsNaNAndInfinity(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := canonicalJSON(v)
		if err == nil {
			t.Errorf("canonicalJSON(%v): expected error, got nil", v)
		}
		var cfgErr *Error
		if !errors.As(err, &cfgErr) {
			t.Errorf("error is not *Error: %T", err)
		}
	}
}

func TestCanonicalJSONStringEscaping(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`simple`, `"simple"`},
		{`has "quote"`, `"has \"quote\""`},
		{`back\slash`, `"back\\slash"`},
		{"tab\there", `"tab\there"`},
		{"line\nfeed", `"line\nfeed"`},
		{"car\rret", `"car\rret"`},
		{"bell\bform\ffeed", `"bell\bform\ffeed"`},
		{"ctrl\x01char", `"ctrl\u0001char"`}, // control char below 0x20
		{"unicode Ω Я 中", `"unicode Ω Я 中"`}, // pass through non-ASCII
		{"emoji 🎉", `"emoji 🎉"`},             // 4-byte UTF-8 pass through
	}
	for _, c := range cases {
		got, err := canonicalJSON(c.in)
		if err != nil {
			t.Errorf("canonicalJSON(%q): %v", c.in, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("canonicalJSON(%q) = %q, want %q", c.in, string(got), c.want)
		}
	}
}

func TestCanonicalJSONObjectKeysSorted(t *testing.T) {
	in := Tree{
		"zebra": 1,
		"alpha": 2,
		"mike":  3,
	}
	got, err := canonicalJSON(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{"alpha":2,"mike":3,"zebra":1}`
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCanonicalJSONObjectKeysUnicodeOrdering(t *testing.T) {
	// UTF-8 byte order == code-point order for valid UTF-8.
	in := Tree{
		"z": 1,
		"А": 2, // Cyrillic A (U+0410)
		"あ": 3, // Hiragana (U+3042)
		"a": 4,
	}
	got, err := canonicalJSON(in)
	if err != nil {
		t.Fatalf("%v", err)
	}
	// ASCII < Cyrillic < Hiragana in code-point order.
	want := `{"a":4,"z":1,"А":2,"あ":3}`
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCanonicalJSONNested(t *testing.T) {
	in := Tree{
		"server": Tree{
			"port": 8080,
			"host": "example.com",
		},
		"tags": []any{"a", "b", "c"},
		"flag": true,
	}
	got, err := canonicalJSON(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{"flag":true,"server":{"host":"example.com","port":8080},"tags":["a","b","c"]}`
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCanonicalJSONArrayPreservesOrder(t *testing.T) {
	// Array order is semantic and MUST be preserved.
	got, err := canonicalJSON([]any{3, 1, 2})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if string(got) != "[3,1,2]" {
		t.Errorf("got %q, want [3,1,2]", got)
	}
}

func TestCanonicalJSONEmptyContainers(t *testing.T) {
	got, _ := canonicalJSON(Tree{})
	if string(got) != "{}" {
		t.Errorf("empty Tree → %q, want {}", got)
	}
	got, _ = canonicalJSON([]any{})
	if string(got) != "[]" {
		t.Errorf("empty slice → %q, want []", got)
	}
}

func TestCanonicalJSONRejectsUnsupportedType(t *testing.T) {
	type unsupported struct{ X int }
	_, err := canonicalJSON(unsupported{X: 1})
	if err == nil {
		t.Fatal("expected error on unsupported type")
	}
	var cfgErr *Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error is not *Error: %T", err)
	}
	if cfgErr.Reason != ReasonTypeMismatch {
		t.Errorf("reason = %q, want %q", cfgErr.Reason, ReasonTypeMismatch)
	}
}

func TestCanonicalJSONIdempotent(t *testing.T) {
	// Bit-identical output for same input, called multiple times.
	in := Tree{"a": 1, "b": []any{"x", "y"}, "c": Tree{"k": "v"}}

	first, err := canonicalJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		next, err := canonicalJSON(in)
		if err != nil {
			t.Fatal(err)
		}
		if string(first) != string(next) {
			t.Fatalf("non-deterministic: iter %d differs from first\nfirst: %s\ndiff:  %s",
				i, first, next)
		}
	}
}
