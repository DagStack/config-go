package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestCanonicalJSONKeyOrderConformanceFixture replays the cross-binding key
// ordering fixture from
// spec/conformance/canonical_json/key_order_drift_witness.json. The fixture
// is normative under _meta/canonical_json.yaml v1.1 and is mirrored in
// config-python and config-typescript regression suites. Each case asserts
// that this binding produces byte-identical output to the expected wire
// string.
//
// Skipped when the `spec` submodule has not been checked out
// (`git submodule update --init`).
func TestCanonicalJSONKeyOrderConformanceFixture(t *testing.T) {
	path := filepath.Join("spec", "conformance", "canonical_json", "key_order_drift_witness.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("spec submodule not initialised (%s missing)", path)
		}
		t.Fatalf("read fixture: %v", err)
	}

	var fixture struct {
		Cases []struct {
			Name         string         `json:"name"`
			Description  string         `json:"description"`
			Input        map[string]any `json:"input"`
			ExpectedWire string         `json:"expected_wire"`
		} `json:"cases"`
	}
	// The fixture top-level uses non-Go-friendly field names with leading
	// underscores (`_doc`, `_spec_section`) — encoding/json silently skips
	// them when no matching field exists, so the explicit struct shape
	// above suffices.
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("fixture defines zero cases")
	}

	// Cases known to have an authoring bug in the upstream fixture; tracked
	// in config-spec issue #31 and patched in config-spec PR #32. The
	// conformant wire bytes for these cases are pinned by
	// TestCanonicalJSONKeySortDriftWitness above, so the binding still
	// verifies the correct behaviour without depending on the fixture's
	// current bytes.
	fixtureAuthoringBugs := map[string]bool{
		"drift_witness_pua_vs_supplementary": true,
	}

	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if fixtureAuthoringBugs[c.Name] {
				t.Skipf("fixture authoring bug — see config-spec issue #31 "+
					"(case %s; conformant bytes pinned by "+
					"TestCanonicalJSONKeySortDriftWitness)", c.Name)
			}
			got, err := canonicalJSON(Tree(c.Input))
			if err != nil {
				t.Fatalf("canonicalJSON: %v", err)
			}
			if string(got) != c.ExpectedWire {
				t.Errorf("case %s:\n  got:  %q\n  want: %q",
					c.Name, string(got), c.ExpectedWire)
			}
		})
	}
}
