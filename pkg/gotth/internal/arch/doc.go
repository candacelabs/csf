// Package arch holds this module's architecture tests.
//
// Several of this library's structural claims are only claims until something
// checks them, and the two that would be quietly expensive to lose are both
// import-graph properties: that the core packages never reach the WebSocket
// transport, and that a production binary importing live never links testing.
// Both are asserted here by walking the real build graph with `go list -deps`,
// so they hold for transitive imports and not merely for the import block of
// the file someone happened to read.
//
// The package deliberately contains no non-test code. It exists so the
// assertions have an owner and a name, rather than living in whichever
// package last needed them.
package arch
