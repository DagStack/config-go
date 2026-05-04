package config

import (
	"reflect"
	"testing"
)

func TestDeepMergeScalarsOverrideWins(t *testing.T) {
	base := Tree{"port": 5432}
	override := Tree{"port": 6432}
	got := deepMerge(base, override)

	if got["port"] != 6432 {
		t.Errorf("port = %v, want 6432", got["port"])
	}
	if base["port"] != 5432 {
		t.Error("base was mutated")
	}
	if override["port"] != 6432 {
		t.Error("override was mutated")
	}
}

func TestDeepMergeMapsRecursive(t *testing.T) {
	base := Tree{
		"database": Tree{
			"host": "localhost",
			"port": 5432,
			"pool": Tree{
				"size": 20,
				"idle": 5,
			},
		},
	}
	override := Tree{
		"database": Tree{
			"host": "prod.example.com",
			"pool": Tree{
				"size": 100,
			},
		},
	}

	got := deepMerge(base, override)

	db := got["database"].(Tree)
	if db["host"] != "prod.example.com" {
		t.Errorf("host not overridden: %v", db["host"])
	}
	if db["port"] != 5432 {
		t.Errorf("port lost from base: %v", db["port"])
	}
	pool := db["pool"].(Tree)
	if pool["size"] != 100 {
		t.Errorf("pool.size not overridden: %v", pool["size"])
	}
	if pool["idle"] != 5 {
		t.Errorf("pool.idle lost from base: %v", pool["idle"])
	}
}

func TestDeepMergeSlicesReplaceAtomically(t *testing.T) {
	// Spec §3: arrays are replaced wholesale, not concatenated.
	base := Tree{"tags": []any{"a", "b", "c"}}
	override := Tree{"tags": []any{"x"}}

	got := deepMerge(base, override)
	tags := got["tags"].([]any)
	if !reflect.DeepEqual(tags, []any{"x"}) {
		t.Errorf("tags = %v, want [x] (atomic replacement)", tags)
	}
}

func TestDeepMergeMapReplacesScalar(t *testing.T) {
	base := Tree{"x": "string"}
	override := Tree{"x": Tree{"nested": true}}

	got := deepMerge(base, override)
	nested, ok := got["x"].(Tree)
	if !ok {
		t.Fatalf("x = %T, want Tree", got["x"])
	}
	if nested["nested"] != true {
		t.Errorf("nested.nested = %v", nested["nested"])
	}
}

func TestDeepMergeScalarReplacesMap(t *testing.T) {
	base := Tree{"x": Tree{"a": 1}}
	override := Tree{"x": "now a string"}

	got := deepMerge(base, override)
	if got["x"] != "now a string" {
		t.Errorf("x = %v, want string", got["x"])
	}
}

func TestDeepMergeNilInputs(t *testing.T) {
	// nil base → copy of override
	got := deepMerge(nil, Tree{"a": 1})
	if got["a"] != 1 {
		t.Errorf("merge(nil, {a:1}) = %v", got)
	}

	// nil override → copy of base
	got = deepMerge(Tree{"b": 2}, nil)
	if got["b"] != 2 {
		t.Errorf("merge({b:2}, nil) = %v", got)
	}

	// both nil → empty tree, not nil
	got = deepMerge(nil, nil)
	if got == nil {
		t.Error("merge(nil, nil) should return non-nil empty tree")
	}
	if len(got) != 0 {
		t.Errorf("merge(nil, nil) not empty: %v", got)
	}
}

func TestDeepMergeDeepCopy(t *testing.T) {
	// Result must not share backing storage with either input, so
	// subsequent mutations to inputs do not leak into result.
	base := Tree{"db": Tree{"pool": []any{"a", "b"}}}
	override := Tree{"other": "value"}

	got := deepMerge(base, override)

	// Mutate base AFTER merge.
	base["db"].(Tree)["pool"].([]any)[0] = "MUTATED"

	gotPool := got["db"].(Tree)["pool"].([]any)
	if gotPool[0] != "a" {
		t.Errorf("result shares slice with base; mutation leaked: %v", gotPool)
	}
}

func TestCopyTreeHandlesNil(t *testing.T) {
	out := copyTree(nil)
	if out == nil {
		t.Fatal("copyTree(nil) returned nil; expected empty Tree")
	}
	if len(out) != 0 {
		t.Errorf("copyTree(nil) = %v; want empty", out)
	}
}

func TestDeepMergeAcceptsMapStringAny(t *testing.T) {
	// yaml.v3 decodes into map[string]any, not Tree. Merge should
	// normalise transparently.
	base := map[string]any{"a": map[string]any{"x": 1}}
	override := map[string]any{"a": map[string]any{"y": 2}}

	got := deepMerge(Tree(base), Tree(override))
	a := got["a"].(Tree)
	if a["x"] != 1 || a["y"] != 2 {
		t.Errorf("normalisation of map[string]any failed: %+v", a)
	}
}
