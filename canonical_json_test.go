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
	// All keys live in the BMP, where UTF-16 code-unit order, UTF-32
	// code-point order, and UTF-8 byte order all coincide.
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
	want := `{"a":4,"z":1,"А":2,"あ":3}`
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestCanonicalJSONKeySortDriftWitness pins the single fixture case that
// distinguishes the three flavours of sort order on a single pair of keys:
//
//	U+E000  — BMP private-use area, single UTF-16 code unit 0xE000.
//	U+20000 — CJK Ext B, supplementary; UTF-16 surrogate pair 0xD840 0xDC00.
//
// UTF-16 code-unit order (RFC 8785 §3.2.3): 0xD840 < 0xE000
//
//	→ supplementary key first.
//
// UTF-32 code-point order (Python sorted()): 0xE000 < 0x20000
//
//	→ BMP-PUA key first (would be a regression).
//
// UTF-8 byte order (Go sort.Strings — the pre-fix behaviour):
//
//	EE 80 80 < F0 A0 80 80 → BMP-PUA key first (would be a regression).
//
// Mirrors spec/conformance/canonical_json/key_order_drift_witness.json
// → drift_witness_pua_vs_supplementary.
func TestCanonicalJSONKeySortDriftWitness(t *testing.T) {
	in := Tree{
		"":          1,
		"\U00020000": 2,
	}
	got, err := canonicalJSON(in)
	if err != nil {
		t.Fatalf("%v", err)
	}
	want := "{\"\U00020000\":2,\"\":1}"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSortKeysUTF16(t *testing.T) {
	// Direct unit coverage of the comparator.
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "ascii ordering",
			in:   []string{"b", "a", "c"},
			want: []string{"a", "b", "c"},
		},
		{
			name: "bmp pua before supplementary",
			in:   []string{"", "\U00020000"},
			want: []string{"\U00020000", ""},
		},
		{
			name: "prefix sorts before longer",
			in:   []string{"abc", "ab"},
			want: []string{"ab", "abc"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := append([]string(nil), c.in...)
			sortKeysUTF16(got)
			if len(got) != len(c.want) {
				t.Fatalf("length mismatch: got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], c.want[i])
				}
			}
		})
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
