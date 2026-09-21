// Package samples is the compiled twin of the gotth-live documentation.
//
// Every Go and templ block in docs/quickstart.md and docs/guide/*.md is
// extracted from a file under this directory, and the suite in samples_test.go
// asserts two things: that each sample package builds, and that every fenced
// block in the docs still matches the file it claims to come from. The docs
// therefore cannot rot into code that no longer compiles without a red test.
//
// It is a SEPARATE module, on the precedent of examples/*, for the same
// reason: nothing a documentation sample reaches for should be able to arrive
// in a consumer's go.mod. The replace directive in go.mod is what a consumer
// does not have — it points at the checkout so the samples build against the
// working tree — and the leading underscore keeps the directory out of the
// library's own ./... patterns twice over, since a nested module is skipped by
// the parent anyway.
//
// From inside this module the ordinary patterns work: go build ./..., go vet
// ./... and go test ./... all see every sample. The suite nonetheless
// enumerates the packages and builds each by name, because that is what puts
// the name of the sample that broke in the failure rather than in a wall of
// compiler output.
package samples
