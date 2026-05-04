package config

// deepMerge returns a new Tree that is the deep merge of base and
// override. Maps are merged recursively; slices are replaced wholly
// (atomic replacement — spec §3); scalars from override win.
//
// Neither input is mutated. The output is a fresh tree — consumers
// may safely modify it without affecting base or override.
//
// Nil inputs are treated as empty trees. Merging an empty tree with
// a non-empty one returns a copy of the non-empty one.
func deepMerge(base, override Tree) Tree {
	if base == nil && override == nil {
		return Tree{}
	}
	if base == nil {
		return copyTree(override)
	}
	if override == nil {
		return copyTree(base)
	}

	out := copyTree(base)
	for k, v := range override {
		if existing, ok := out[k]; ok {
			out[k] = mergeValues(existing, v)
			continue
		}
		out[k] = copyValue(v)
	}
	return out
}

// mergeValues picks the merge strategy based on the types of the two
// values. The rule matrix:
//
//	base \ override  | map         | slice       | scalar
//	map              | recurse     | replace     | replace
//	slice            | replace     | replace     | replace
//	scalar           | replace     | replace     | replace
//
// "replace" means: override wins wholesale. This mirrors spec §3 —
// sequences are not concatenated, primitives overwrite, objects merge.
func mergeValues(base, override any) any {
	bm, baseIsMap := toMap(base)
	om, overrideIsMap := toMap(override)
	if baseIsMap && overrideIsMap {
		return deepMerge(bm, om)
	}
	return copyValue(override)
}

// toMap normalises either a Tree or a map[string]any to Tree. Returns
// (nil, false) for any other type so callers can fall back to
// replacement strategy.
func toMap(v any) (Tree, bool) {
	switch x := v.(type) {
	case Tree:
		return x, true
	case map[string]any:
		return Tree(x), true
	default:
		return nil, false
	}
}

// copyTree deep-copies a tree so merge results do not share backing
// storage with inputs. Required because []any and Tree are reference
// types — a shallow copy would let consumers mutate one tree through
// another.
func copyTree(t Tree) Tree {
	if t == nil {
		return Tree{}
	}
	out := make(Tree, len(t))
	for k, v := range t {
		out[k] = copyValue(v)
	}
	return out
}

// copyValue deep-copies an arbitrary value. Scalars pass through;
// maps and slices get new backing storage.
func copyValue(v any) any {
	switch x := v.(type) {
	case Tree:
		return copyTree(x)
	case map[string]any:
		return copyTree(Tree(x))
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = copyValue(e)
		}
		return out
	default:
		return v
	}
}
