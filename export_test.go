package config

// export_test.go exposes package-internal helpers to the `config_test`
// external test package. These are NOT part of the public API — they
// exist solely for integration with spec/conformance fixtures and
// other test-only tooling.

// ResolvedTreeForTest returns the raw, fully-resolved merged tree
// without field-name suffix masking. The conformance runner uses this
// because Snapshot() (per ADR-0002 §3 trigger table) applies field-
// name masking that would mangle the verbatim canonical JSON
// comparison against expected/*.json fixtures.
//
// Test-internal use only — external consumers must call Snapshot() or
// Snapshot(WithIncludeSecrets()) and accept the masking semantics
// that those methods document.
func ResolvedTreeForTest(c *Config) Tree {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return copyTree(c.tree)
}
