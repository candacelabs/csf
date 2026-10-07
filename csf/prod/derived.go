// Copyright 2026 Candace Labs

package prod

import (
	"path"
	"slices"
	"strings"
)

// The derived share is the fraction of the tree a person or agent never
// authors. A file is authored when it is neither source nor derived, so the
// authored set is the distance to the goal — every derivation slice shrinks it.
//
// A file is derived by reproduction, never by the generated header its text
// carries: it is reproduced by a declared generator, or written by a declared
// run and never hand-edited. The header a file shows is prose a generator may
// or may not emit (the bench data below has none), while the declaration names
// the program that owns every file matching its output, so the two measures
// disagree exactly where the header is silent. The declarations mirror the
// repository's own generators; the generated-drift gate (tools/check-generated.sh)
// holds each generator's output byte-equal to its source, so a matching file is
// reproduced in fact, not merely asserted here.
//
//   source(F)   :- ext(F, [csf, dl]).
//   derived(F)  :- declared_generator(G, F), reproduces(G, F, byte_equal).
//   derived(F)  :- declared_output(Run, F), machine_written(Run, F), \+ hand_edited(F).
//   authored(F) :- \+ source(F), \+ derived(F).
//   derived_share = (count(source) + count(derived)) / count(files).

// SourceExts are the extensions that make a file source rather than authored:
// the CSF and Datalog the tooling reads as its own.
var SourceExts = []string{".csf", ".dl"}

// Generator is a declared generator: a tool whose run reproduces every tracked
// file matching one of its Outputs, byte for byte. Authored lists paths that
// match an Output but a person wrote by hand (reproduces_or_listed_as_authored).
type Generator struct {
	// ID is the generator's name.
	ID string
	// Command is the run that reproduces its outputs.
	Command string
	// Outputs are the path globs the run reproduces.
	Outputs []string
	// Authored are matching paths a person edits by hand.
	Authored []string
}

// Output is a declared machine-written output: a declared run that writes every
// tracked file matching one of its Outputs, which no hand edits. HandEdited
// lists matching paths that were in fact edited by hand.
type Output struct {
	// ID is the run's name.
	ID string
	// Run is the command that writes its outputs.
	Run string
	// Outputs are the path globs the run writes.
	Outputs []string
	// HandEdited are matching paths a person edited by hand.
	HandEdited []string
}

// Generators are the declared generators. Each names the run that reproduces
// its outputs and the globs those outputs match, so a file is derived by the
// program that owns it, regardless of the header its text carries.
var Generators = []Generator{
	{ID: "gazelle", Command: "bazel run //:gazelle", Outputs: []string{"**/BUILD.bazel"}},
	{ID: "protoc", Command: "proto/generate.sh", Outputs: []string{"proto/**/*.pb.go"}},
	{ID: "sqlc", Command: "sqlc generate", Outputs: []string{"**/*.sql.go"}},
	{ID: "mockgen", Command: "go generate ./csf/...", Outputs: []string{"**/*.gen.go"}},
	{ID: "templ", Command: "pkg/widget/gen.sh", Outputs: []string{"**/*_templ.go"}},
	{ID: "csfc", Command: "csfc emit", Outputs: []string{"csf/architecture/generated/**"}},
	{ID: "language_generator", Command: "generate --root . write", Outputs: []string{
		"csf/docs/generated/**",
		"csf/compiler/language/**/*.ebnf",
		"csf/compiler/metric_compat/*_cgen.py",
		"services/ouroboros/codes/*_cgen.go",
		"docs/GLOSSARY.md",
	}},
	{ID: "api_codegen", Command: "csf/tools/codegen", Outputs: []string{
		"csf/*_cgen.go",
		"csf/*_gen.go",
		"csf/tools/codegen/generated/**",
	}},
}

// Outputs are the declared machine-written outputs. Each names the run that
// writes its outputs and the globs those outputs match.
var Outputs = []Output{
	{ID: "gotth_bench_driver", Run: "pkg/gotth/test/memory/measure.sh", Outputs: []string{"pkg/gotth/docs/bench/data/**"}},
}

// Classification is the tree split three ways: the source, the derived, and the
// authored — the last being what a person or agent still writes.
type Classification struct {
	Source   []string
	Derived  []string
	Authored []string
}

// Family is a directory and how many authored files it holds: one entry of the
// report the next derivation slices read.
type Family struct {
	// Directory is the authored file's directory, repository-relative.
	Directory string `json:"directory"`
	// Files is how many authored files the directory holds.
	Files int `json:"files"`
}

// IsSource reports whether a path is source: a .csf or .dl file.
func IsSource(path string) bool {
	return slices.Contains(SourceExts, pathExt(path))
}

// pathExt returns a path's extension, lowercased, including the dot.
func pathExt(name string) string { return strings.ToLower(path.Ext(name)) }

// Classify splits files into source, derived and authored by the rules at the
// top of this file: a source file is source; a file reproduced by a declared
// generator, or written by a declared run and not hand-edited, is derived; the
// rest is authored.
func Classify(files []string) Classification {
	var classified Classification
	for _, file := range files {
		switch {
		case IsSource(file):
			classified.Source = append(classified.Source, file)
		case reproduced(file) || machineWritten(file):
			classified.Derived = append(classified.Derived, file)
		default:
			classified.Authored = append(classified.Authored, file)
		}
	}
	return classified
}

// reproduced reports whether a declared generator reproduces a file: the file
// matches one of a generator's outputs and is not one of its authored
// exceptions.
func reproduced(file string) bool {
	for _, generator := range Generators {
		if matchesAny(generator.Outputs, file) && !slices.Contains(generator.Authored, file) {
			return true
		}
	}
	return false
}

// machineWritten reports whether a declared run writes a file and no hand
// edited it: the file matches one of a run's outputs and is not one of its
// hand-edited exceptions.
func machineWritten(file string) bool {
	for _, output := range Outputs {
		if matchesAny(output.Outputs, file) && !slices.Contains(output.HandEdited, file) {
			return true
		}
	}
	return false
}

// matchesAny reports whether any glob matches the file.
func matchesAny(globs []string, file string) bool {
	for _, glob := range globs {
		if matchGlob(glob, file) {
			return true
		}
	}
	return false
}

// matchGlob reports whether a repository-relative slash path matches a pattern
// where ** matches any sequence of path segments (including none) and * matches
// within one segment. It is the glob the generator and output declarations
// use.
func matchGlob(pattern, name string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

// matchSegments matches split patterns and names segment by segment.
func matchSegments(pattern, name []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			// ** matches zero segments here, or consumes one and tries again.
			if matchSegments(pattern[1:], name) {
				return true
			}
			if len(name) == 0 {
				return false
			}
			name = name[1:]
			continue
		}
		if len(name) == 0 {
			return false
		}
		matched, err := path.Match(pattern[0], name[0])
		if err != nil || !matched {
			return false
		}
		pattern, name = pattern[1:], name[1:]
	}
	return len(name) == 0
}

// DerivedShare is the fraction of the tree that is source or derived, over
// every classified file. A tree with no files is wholly derived, vacuously.
func (c Classification) DerivedShare() float64 {
	total := len(c.Source) + len(c.Derived) + len(c.Authored)
	if total == 0 {
		return 1
	}
	return float64(len(c.Source)+len(c.Derived)) / float64(total)
}

// AuthoredFamilies groups the authored files by directory, most files first and
// ties by directory name: the report(authored, by(family), descending) the next
// derivation slices read.
func (c Classification) AuthoredFamilies() []Family {
	counts := map[string]int{}
	for _, file := range c.Authored {
		counts[path.Dir(file)]++
	}
	families := make([]Family, 0, len(counts))
	for directory, files := range counts {
		families = append(families, Family{Directory: directory, Files: files})
	}
	slices.SortFunc(families, func(a, b Family) int {
		if a.Files != b.Files {
			return b.Files - a.Files
		}
		return strings.Compare(a.Directory, b.Directory)
	})
	return families
}

// TopFamilies is the first n authored families, all of them when there are
// fewer than n.
func (c Classification) TopFamilies(n int) []Family {
	families := c.AuthoredFamilies()
	if n < len(families) {
		return families[:n]
	}
	return families
}
