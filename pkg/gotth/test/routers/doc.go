// Package routers holds the FR-33 three-router mount suite and nothing else.
//
// FR-33 says the library "MUST expose ordinary http.Handler values and MUST
// NOT require a specific router or framework", verified by mounting under
// net/http, chi and gin. This package is that verification, and every line of
// it is in a _test.go file: the package itself is deliberately empty so that
// chi and gin are imported by tests only, and so that `go build ./...` has
// something to build.
//
// The suite mounts one live application under three routers at three
// DISTINCT prefixes — /live, /app/live and /ui/gotth — because mounting all
// three at /live would satisfy FR-33's wording literally while testing
// nothing about prefixes, and a prefix that is not /live is what would have
// caught the Script() hardcoded-mount defect before review did (L9-1
// condition C-23).
package routers
