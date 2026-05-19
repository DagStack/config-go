// Package docs_examples contains automated tests for the Go snippets in config-docs.
//
// Every *_test.go file mirrors one docs page and reproduces the Go snippet
// verbatim between the `// --- snippet start ---` / `// --- snippet end ---`
// markers, then asserts the expectations expressed in the snippet's comments.
//
// Run: `go test ./docs_examples/...`.
//
// When docs and binding drift (a snippet calls a non-existent method or
// returns a different value), the test leaves an NB comment plus an assertion
// against the real behaviour, and the drift is tracked as a separate issue.
package docs_examples
