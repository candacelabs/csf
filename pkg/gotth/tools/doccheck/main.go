// Command doccheck holds every exported symbol in the tree to a doc comment,
// and every godoc example to an output the test runner actually checks.
//
// FR-66 and FR-68 both name CI as the thing that enforces them and, until this
// existed, nothing did. That is the same failure ci.sh's header describes: a
// requirement whose gate is a tool nobody runs is a requirement in name only.
// Both requirements are about drift rather than about a moment — a doc comment
// is written when a symbol lands and is forgotten when the next one does, and
// an example that nothing compiles stops describing the API the day after the
// API moves — so the check has to run on every change or it checks nothing.
//
// # The three rules, exactly as enforced
//
// 1. Every exported symbol carries a doc comment. Package-level types, funcs,
// methods, consts and vars; the exported fields of exported structs; and the
// methods of exported interfaces. A symbol is documented when it carries a
// comment of its own, when it carries a trailing line comment (fields, consts
// and vars only — `Foo int // the foo` is a doc comment as far as go doc is
// concerned), or when it is one name inside a parenthesized group whose
// declaration carries one. The group case is the local idiom and is better
// documentation than the alternative, not weaker: internal/protocol.Kind's
// eight iota values are described once, as a set, which is what they are.
//
// 2. Every package carries a package comment, and it opens with "Package
// <name>" or, for a command, "Command <name>". That is the convention go doc's
// synopsis depends on, and a package overview that does not name the package
// is not an overview.
//
// 3. A consumer-reachable package's overview is RUNNABLE, which is defined
// here as: the package declares a package-level `func Example()` carrying an
// `// Output:` comment. That is godoc's own package example — the block a
// reader sees directly under the overview — and the Output comment is what
// makes `go test` execute it and compare. An Example without one compiles and
// is never run, so it can assert a behaviour the library no longer has.
// Rule 4 holds every OTHER example to the same standard.
//
// 4. Every Example* function in the tree, in every module, has an `// Output:`
// comment. An empty one counts: `// Output:` with nothing after it tells go
// test to expect no output, and go test then runs the function. What does not
// count is no comment at all, which is FR-68's exact failure — documentation
// that compiles, never runs, and drifts silently.
//
// # Scope, and the argument for it
//
// Every package in the tree is MEASURED, in every module. Rules 1 and 2 are
// ENFORCED on the packages of the published library — live, live/livetest and
// all of internal/**. Rule 3 is enforced
// on the consumer-reachable subset of those. Rule 4 is enforced everywhere,
// because an example that never runs costs the same wherever it sits.
//
// The scope line was the module boundary, and it is now the same line drawn by
// path. FR-66 is a requirement about the library's documentation, and until the
// single-module fold the library was its own module: docs/guide/_samples,
// test/routers, test/sampling, test/memory, bench/apps/*/gotth and tools/ each
// had their own go.mod so that what they need could not reach a consumer's
// build list, and the go.mod walk below told them apart from the library with
// no path in it. They are all in one module now, and none of them became the
// library by being folded into it, so notLibrary names them. The same boundary
// decides what tools/apisurface calls surface.
//
// The examples moved out of this tree altogether — they are at
// examples/gotth/, a sibling of pkg/ — and for one landing that put them
// outside -root and outside this gate entirely, which is how rule 4 stopped
// covering them: an example application whose Example function lost its Output
// comment would have compiled, never run, and passed.
//
// They are walked again through -reported-root, which is repeatable and names
// a tree that is MEASURED with rule 4 enforced and rules 1 to 3 not. That is
// the scope the three examples had before they moved, arrived at the same way:
// each carried its own go.mod, the walk below answered "not the published
// module", and they landed in scopeReported. It is not a relaxation to fit
// them — it is the second reason above, which is at its strongest here. The
// dashboard example's wire types are JSON payload structs whose fields are
// capitalised because encoding/json will not marshal them otherwise, and a doc
// comment on WireUpdate.HTML is a sentence nobody will read.
//
// The distinction is in the flag rather than in a path list because a root is
// not under the tree root the way bench/ and tools/ are; notLibraryTrees
// cannot name something that is not below it.
//
// There is a second reason, specific to those modules and worth stating because
// it is the one that would make the wider rule actively bad. Most of their
// exported identifiers are exported by the COMPILER's demand rather than by an
// author's decision: a field is capitalised because encoding/json will not
// marshal it otherwise, a type because templ generates a call to it. When this
// gate was first run, against the tree at 452e1e74, 359 of the 410 undocumented
// symbols it found were struct fields, and the bulk of them were JSON payload
// fields in a benchmark fixture — that measurement is the argument's evidence
// and is dated on purpose, because the live figure is the one the run prints
// below, not the one a comment remembers. A doc comment on
// ChatEvent.Body is a sentence nobody will read, in a file nobody imports, and
// writing 180 of them is how a doc gate teaches a team that doc comments are
// noise.
//
// So the out-of-scope packages are printed with their counts on every run,
// under the scope label "reported", rather than being dropped from the walk. The
// number is in the CI log, the decision is auditable, and widening the rule is a
// one-line change to enforcedScope rather than an archaeology exercise. Hiding
// them would be the defect ci.sh's ci_modules_unrun comment refuses in those
// words; printing them unenforced is the smallest thing that is not that.
//
// Rule 3 narrows once more, to the packages of the published module with no
// internal element in their path — today live and live/livetest, derived rather
// than listed, so a third one is covered the day it appears. FR-66's overview
// clause says "exported package", and a package under internal/ is precisely the
// package that is not exported: no consumer can import it and godoc publishes no
// page for it, so the overview a package example sits under is a page nobody
// outside this module can reach.
//
// Two exclusions are forced rather than chosen:
//
//   - Generated files. A doc comment written into a file that carries "Code
//     generated … DO NOT EDIT." is deleted by the next gen.sh run, and FR-7's
//     byte-reproducibility gate fails on the attempt. The generator is where
//     such a comment would have to come from, so a violation here is a finding
//     against the generator, which is not this tool's subject.
//   - Methods on unexported types. go doc drops them entirely, so a doc comment
//     there is documentation with no reader. tools/apisurface makes the same
//     call for the same reason — "a method on an unexported type is not
//     reachable by a consumer, so it is not surface" — and the two gates
//     agreeing on what the surface is matters more than either answer.
//
// # Output
//
// The report lists every package with its scope, its symbol count and its
// examples, and every enforced violation with file:line, because a gate that
// reports the first failure makes the fix a sequence of CI rounds. Paths are
// relative to the module root.
//
// Usage:
//
//	go run ./doccheck                             # report the coverage and check it
//	go run ./doccheck -report                     # report only, exit 0
//	go run ./doccheck -reported-root ../../x      # also walk a measured-only tree
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// nonRunnableExamples names the Example functions that deliberately check no
// output, keyed by "<package path>.<function name>", with the reason.
//
// Deliberately empty. An entry here says an example ships in the documentation
// and never runs, which is exactly what FR-68 exists to stop, so it has to be
// argued in place, beside the entry, the way ci.sh's ci_modules_unrun demands.
// The usual honest reason — an example that starts a server or prints a time —
// is usually a reason to make the example assert something else instead.
//
// A stale entry is an error: an exception naming a function that no longer
// exists is a permission nobody is using and nobody will notice has expired.
var nonRunnableExamples = map[string]string{}

// generatedMarker is the standard "generated code" line, as cmd/go defines it:
// it must appear before the first non-comment text in the file.
var generatedMarker = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// scope is how much of a package this gate enforces, and the label the report
// prints for it.
type scope string

const (
	// scopeOverview is a package of the published module that a consumer can
	// import: rules 1, 2, 3 and 4.
	scopeOverview scope = "overview"
	// scopeLibrary is a package of the published module that a consumer cannot
	// import — internal/** and the commands: rules 1, 2 and 4.
	scopeLibrary scope = "library"
	// scopeReported is a package of any other module. Measured and printed;
	// only rule 4 is enforced. The argument is in this command's doc comment.
	scopeReported scope = "reported"
)

// enforced reports whether the doc-comment rules apply to a package in this
// scope, as opposed to its counts merely being printed.
func (s scope) enforced() bool { return s != scopeReported }

// kind names what a finding is about, for the report.
type kind string

const (
	kindPackage   kind = "package"
	kindType      kind = "type"
	kindFunc      kind = "func"
	kindMethod    kind = "method"
	kindConst     kind = "const"
	kindVar       kind = "var"
	kindField     kind = "field"
	kindInterface kind = "interface method"
	kindExample   kind = "example"
)

// finding is one violation, with the position that makes it actionable.
type finding struct {
	Pkg  string
	Pos  token.Position
	Kind kind
	Name string
	Why  string
}

func (f finding) String() string {
	where := f.Pos.Filename
	switch {
	case where == "":
		where = "doccheck"
	case f.Pos.Line > 0:
		where = fmt.Sprintf("%s:%d", f.Pos.Filename, f.Pos.Line)
	}
	return fmt.Sprintf("%s: %s %s: %s", where, f.Kind, f.Name, f.Why)
}

// pkgReport is one package's measured coverage.
type pkgReport struct {
	Path             string // relative to the module root
	Name             string
	Scope            scope
	Symbols          int // exported symbols the doc rule applies to, plus the package itself
	Undocumented     int // of those, the ones with no doc comment
	Generated        int // generated files skipped
	Examples         int
	RunnableExamples int
}

// report is the whole tree's coverage, the violations that fail the build, and
// the violations outside the enforced scope, which are printed and do not.
type report struct {
	Packages []pkgReport
	Findings []finding
	Reported []finding
}

// treeRoot is one root this gate walks, and whether the packages it finds are the
// published library or a tree that is measured and printed only.
type treeRoot struct {
	// Path is the -root or -reported-root value, as given.
	Path string
	// Reported forces every package under this root to scopeReported: rule 4
	// is enforced, rules 1 to 3 are not.
	Reported bool
}

// roots collects a repeatable path flag.
type roots []string

func (r *roots) String() string { return strings.Join(*r, ",") }

func (r *roots) Set(v string) error {
	if v == "" {
		return fmt.Errorf("a root path may not be empty")
	}
	*r = append(*r, v)
	return nil
}

func main() {
	reportOnly := flag.Bool("report", false, "print the coverage and exit 0")
	var libraryRoots, reportedRoots roots
	flag.Var(&libraryRoots, "root", "path to a library tree root; repeat to walk more than one")
	flag.Var(&reportedRoots, "reported-root",
		"path to a tree measured with rule 4 enforced and rules 1-3 not; repeat as needed")
	flag.Parse()

	if len(libraryRoots) == 0 {
		libraryRoots = roots{".."}
	}
	var trees []treeRoot
	for _, r := range libraryRoots {
		trees = append(trees, treeRoot{Path: r})
	}
	for _, r := range reportedRoots {
		trees = append(trees, treeRoot{Path: r, Reported: true})
	}

	rep, err := runAll(trees)
	if err != nil {
		fail("%v", err)
	}

	fmt.Println("godoc coverage (FR-66, FR-68)")
	fmt.Printf("  %-9s %-38s %8s %8s %8s %8s\n", "scope", "package", "symbols", "undoc", "examples", "run")
	var enforced, reported pkgReport
	for _, p := range rep.Packages {
		fmt.Printf("  %-9s %-38s %8d %8d %8d %8d\n",
			p.Scope, p.Path, p.Symbols, p.Undocumented, p.Examples, p.RunnableExamples)
		into := &enforced
		if !p.Scope.enforced() {
			into = &reported
		}
		into.Symbols += p.Symbols
		into.Undocumented += p.Undocumented
		into.Examples += p.Examples
		into.RunnableExamples += p.RunnableExamples
		into.Generated += p.Generated
	}
	fmt.Printf("  %-9s %-38s %8d %8d %8d %8d\n", "", "enforced total",
		enforced.Symbols, enforced.Undocumented, enforced.Examples, enforced.RunnableExamples)
	fmt.Printf("  %-9s %-38s %8d %8d %8d %8d\n", "", "reported total (rule 4 only)",
		reported.Symbols, reported.Undocumented, reported.Examples, reported.RunnableExamples)
	fmt.Printf("  %d generated files skipped; the scope argument is in doccheck's own doc comment\n",
		enforced.Generated+reported.Generated)

	// The unenforced findings are counted here rather than listed. Listing them
	// all would bury the enforced ones this step exists to surface, and the
	// -report flag prints the lot when somebody wants to widen the scope. The
	// count is deliberately not written down in prose anywhere: it moved from
	// 268 to 269 between two commits of one landing, because a sample package
	// arrived while the scope argument was being written, and PM-1's v0.8
	// ratification had to spend a condition on which of three spellings was the
	// tree's. This line is the tree's.
	if reported.Undocumented > 0 {
		fmt.Printf("  NOTE: %d undocumented symbols sit outside the enforced scope, in modules "+
			"nothing imports.\n        Run with -report to list them.\n", reported.Undocumented)
	}

	if *reportOnly {
		for _, f := range rep.Reported {
			fmt.Fprintf(os.Stderr, "  reported (not enforced): %s\n", f)
		}
	}

	if len(rep.Findings) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d exported symbols, packages or examples fail FR-66/FR-68:\n\n", len(rep.Findings))
		for _, f := range rep.Findings {
			fmt.Fprintf(os.Stderr, "  %s\n", f)
		}
		fmt.Fprintf(os.Stderr, "\n"+
			"  A doc comment says what the symbol does and, where the behaviour is not\n"+
			"  obvious, what it guarantees (FR-66). A comment written to silence this\n"+
			"  check is worse than the failure it hides.\n"+
			"  An Example without an // Output: comment compiles and never runs, so it\n"+
			"  can assert a behaviour the library no longer has (FR-68).\n")
	}

	if *reportOnly {
		return
	}
	if len(rep.Findings) > 0 {
		os.Exit(1)
	}
	fmt.Println("  every exported symbol in the library carries a doc comment; every example checks its output")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "doccheck: "+format+"\n", args...)
	os.Exit(2)
}

// run walks one library tree and applies the four rules. It is runAll for a
// single root, and the shape the tests use.
func run(root string) (*report, error) { return runAll([]treeRoot{{Path: root}}) }

// runAll walks every tree in roots and applies the four rules across the union.
//
// It parses rather than builds, so it needs no toolchain beyond the parser and
// works across every module in the trees without resolving any of their
// requirements — which is what lets one invocation cover eleven go.mod files.
//
// The library assertions at the bottom are made against the union of the
// library roots. A reported root is asserted separately and differently: it
// must hold at least one package, because a reported root that resolves to
// nothing is this tool's own recurring failure — a walk that finds nothing,
// prints an empty table and exits 0 — and it is the failure that let the
// examples go unchecked for a landing.
func runAll(trees []treeRoot) (*report, error) {
	rep := &report{}
	fset := token.NewFileSet()
	seenExamples := map[string]bool{}

	for _, t := range trees {
		// One root keeps the paths it always printed, relative to that root.
		// More than one has to say which tree a package came from, or two
		// packages called "counter" are one line in the report.
		prefix := ""
		if len(trees) > 1 {
			prefix = filepath.ToSlash(filepath.Clean(t.Path))
		}

		dirs, err := sourceDirs(t.Path)
		if err != nil {
			return nil, err
		}

		found := 0
		for _, dir := range dirs {
			p, err := parseDir(fset, t.Path, prefix, dir)
			if err != nil {
				return nil, err
			}
			if p == nil {
				continue
			}
			found++
			if t.Reported {
				p.Scope = scopeReported
			}
			pr, violations, keys := p.check(fset)
			for name := range keys {
				seenExamples[name] = true
			}
			rep.Packages = append(rep.Packages, pr)
			for _, f := range violations {
				// Rule 4 is enforced everywhere; rules 1, 2 and 3 only inside
				// the published module.
				if p.Scope.enforced() || f.Kind == kindExample {
					rep.Findings = append(rep.Findings, f)
				} else {
					rep.Reported = append(rep.Reported, f)
				}
			}
		}
		if t.Reported && found == 0 {
			return nil, fmt.Errorf(
				"%s holds no Go package at all: a reported root that resolves to nothing is a "+
					"tree this gate silently stops covering, which is how the examples went "+
					"unchecked",
				t.Path)
		}
	}

	// A stale exception is a finding of its own: a permission nobody is using
	// reads, to the next person, as a rule that was relaxed for a reason.
	for name, reason := range nonRunnableExamples {
		if !seenExamples[name] {
			rep.Findings = append(rep.Findings, finding{
				Kind: kindExample,
				Name: name,
				Why: fmt.Sprintf(
					"is listed in doccheck's nonRunnableExamples (%q) and does not exist; drop the entry", reason),
			})
		}
	}

	// A check that cannot fail is indistinguishable from a check that passes,
	// and this one has an obvious way to become that: a walk that finds nothing
	// prints an empty table and exits 0. It would happen from the wrong
	// working directory, or a -root that no longer names the module — the same
	// paste-from-the-wrong-directory shape that let the client bundle suite
	// report `tests 0` and pass. So the walk asserts it found the thing it
	// exists to check, before believing its own silence.
	var overview, enforced int
	for _, p := range rep.Packages {
		if p.Scope == scopeOverview {
			overview++
		}
		if p.Scope.enforced() {
			enforced++
		}
	}
	var library []string
	for _, t := range trees {
		if !t.Reported {
			library = append(library, t.Path)
		}
	}
	where := strings.Join(library, ", ")
	switch {
	case len(rep.Packages) == 0:
		return nil, fmt.Errorf("%s holds no Go package at all: this check would pass over an empty tree", where)
	case enforced == 0:
		return nil, fmt.Errorf(
			"%s holds no package of the published library: check -root.\n"+
				"  Every package found belongs to a nested module or to a tree this gate only "+
				"reports on, so the doc rules would apply to nothing",
			where)
	case overview == 0:
		return nil, fmt.Errorf(
			"%s holds no consumer-reachable package: every package of the published module is "+
				"under internal/ or is a command.\n"+
				"  The runnable-overview rule would then apply to nothing, which is a gate that "+
				"passes because it looked at nothing rather than because the tree is clean",
			where)
	}

	sort.Slice(rep.Packages, func(i, j int) bool { return rep.Packages[i].Path < rep.Packages[j].Path })
	byPosition(rep.Findings)
	byPosition(rep.Reported)
	return rep, nil
}

// byPosition orders findings the way a reader works through them: file, then
// line.
func byPosition(found []finding) {
	sort.Slice(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.Pos.Filename != b.Pos.Filename {
			return a.Pos.Filename < b.Pos.Filename
		}
		return a.Pos.Line < b.Pos.Line
	})
}

// sourceDirs returns every directory under root holding at least one .go file,
// relative to root and sorted.
//
// node_modules is excluded the way ci.sh's module walk excludes it, and
// testdata because the go tool does not build it. Directories beginning with a
// dot are skipped; directories beginning with an underscore are NOT — the go
// tool ignores those, which is exactly why docs/guide/_samples was invisible to
// every check in this repository until ci.sh grew a step naming it, and this
// tool is not going to reproduce that hole.
func sourceDirs(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if path != root && (name == "node_modules" || name == "testdata" || strings.HasPrefix(name, ".")) {
			return filepath.SkipDir
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				out = append(out, filepath.ToSlash(rel))
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// pkg is one directory's parsed source, split the way the rules need it.
type pkg struct {
	Rel       string // path relative to the module root, in slash form
	Name      string
	Files     []*ast.File // non-test, non-generated
	Tests     []*ast.File // _test.go, whatever package clause they carry
	Generated int
	Scope     scope
}

// parseDir parses one directory. It returns nil when the directory holds only
// generated code or only files the rules do not read.
//
// rel is relative to root and is what scopeOf reads; prefix is prepended to it
// for display only, so a multi-root run names the tree a finding came from
// without changing which rules apply to it.
func parseDir(fset *token.FileSet, root, prefix, rel string) (*pkg, error) {
	dir := filepath.Join(root, filepath.FromSlash(rel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	p := &pkg{Rel: rel}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", filepath.ToSlash(filepath.Join(rel, name)), err)
		}
		switch {
		case strings.HasSuffix(name, "_test.go"):
			p.Tests = append(p.Tests, file)
		case isGenerated(file):
			p.Generated++
		default:
			p.Name = file.Name.Name
			p.Files = append(p.Files, file)
		}
	}
	if len(p.Files) == 0 && len(p.Tests) == 0 && p.Generated == 0 {
		return nil, nil
	}
	p.Scope, err = scopeOf(root, rel, p.Name)
	if err != nil {
		return nil, err
	}
	if prefix != "" {
		p.Rel = path.Join(prefix, rel)
	}
	return p, nil
}

// isGenerated reports whether a file carries the standard generated-code
// marker before its package clause.
func isGenerated(file *ast.File) bool {
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			return false
		}
		for _, c := range group.List {
			if generatedMarker.MatchString(c.Text) {
				return true
			}
		}
	}
	return false
}

// notLibraryTrees names the directories under the tree root that the published
// library does not occupy.
//
// Each of them carried its own go.mod until the single-module fold, so the walk
// below answered for them and this list did not exist. A benchmark application,
// a suite that needs a router the library refuses, a minifier and the guide's
// samples are no more the library now than they were then — but the walk can no
// longer say so, because there is one go.mod and it is three directories above
// this tree.
//
// The cost of the change is worth naming where the list is: a tree added under
// this root is enforced as library documentation until somebody adds it here,
// where a tree with its own go.mod was out of scope by construction.
var notLibraryTrees = []string{"bench", "test", "tools", "docs"}

// scopeOf decides how much of a package this gate enforces.
//
// A nested module is still found by walking up for the nearest go.mod, so a
// package that moves into one changes scope without anybody remembering to say
// so. What that walk can no longer answer — which trees of the enclosing module
// are the library — is answered by notLibraryTrees.
func scopeOf(root, rel, name string) (scope, error) {
	if top, _, _ := strings.Cut(rel, "/"); slices.Contains(notLibraryTrees, top) {
		return scopeReported, nil
	}
	published, err := inRootModule(root, rel)
	if err != nil {
		return "", err
	}
	if !published {
		return scopeReported, nil
	}
	if name == "" || name == "main" {
		return scopeLibrary, nil
	}
	for _, element := range strings.Split(rel, "/") {
		if element == "internal" || strings.HasPrefix(element, "_") {
			return scopeLibrary, nil
		}
	}
	return scopeOverview, nil
}

// inRootModule reports whether rel belongs to the module the tree root belongs
// to, rather than to a module nested inside the tree.
//
// The walk stops at root. A go.mod found below root is a nested module and the
// answer is no; a go.mod at root is the tree's own and the answer is yes; and
// reaching root with neither is the case the single-module fold created — this
// tree has no go.mod of its own any more, because it is a subdirectory of the
// candace module — and the answer is yes there too, because the enclosing
// module is the tree's module.
func inRootModule(root, rel string) (bool, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	dir := filepath.Join(root, filepath.FromSlash(rel))
	for {
		_, err := os.Stat(filepath.Join(dir, "go.mod"))
		switch {
		case err == nil:
			abs, err := filepath.Abs(dir)
			if err != nil {
				return false, err
			}
			return abs == absRoot, nil
		case !os.IsNotExist(err):
			return false, err
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return false, err
		}
		if abs == absRoot {
			return true, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Walked out of the filesystem without meeting root: not a tree
			// this gate can say anything about.
			return false, nil
		}
		dir = parent
	}
}

// check applies the four rules to one package. The returned set names every
// Example this package holds, which is what the stale-exception check reads.
func (p *pkg) check(fset *token.FileSet) (pkgReport, []finding, map[string]bool) {
	rep := pkgReport{Path: p.Rel, Name: p.Name, Scope: p.Scope, Generated: p.Generated}
	var found []finding

	// Rule 1.
	for _, file := range p.Files {
		for _, decl := range file.Decls {
			symbols, missing := p.checkDecl(fset, decl)
			rep.Symbols += symbols
			found = append(found, missing...)
		}
	}
	rep.Undocumented = len(found)

	// Rule 2. A directory with no non-test files is a test-only package: there
	// is no package for a consumer to import and nothing to write an overview
	// about, so the rule does not apply rather than being waived.
	if len(p.Files) > 0 {
		rep.Symbols++
		if f, ok := p.checkPackageComment(fset); !ok {
			rep.Undocumented++
			found = append(found, f)
		}
	}

	examples := doc.Examples(p.Tests...)
	keys := make(map[string]bool, len(examples))
	rep.Examples = len(examples)
	var hasOverview bool
	for _, ex := range examples {
		name := "Example" + ex.Name
		keys[p.Rel+"."+name] = true
		// An empty // Output: counts: it tells go test to expect no output,
		// and go test then runs the function, which is the whole of FR-68's
		// demand. What does not count is no comment at all.
		runs := ex.Output != "" || ex.EmptyOutput
		if runs {
			rep.RunnableExamples++
		}
		if name == "Example" && runs {
			hasOverview = true
		}
		// Rule 4.
		if _, excepted := nonRunnableExamples[p.Rel+"."+name]; excepted {
			continue
		}
		if !runs {
			found = append(found, finding{
				Pkg:  p.Rel,
				Pos:  p.position(fset, ex.Code),
				Kind: kindExample,
				Name: name,
				Why: "has no // Output: comment, so go test compiles it and never runs it. " +
					"Give it one (an empty // Output: counts) or delete it (FR-68)",
			})
		}
	}

	// Rule 3.
	if p.Scope == scopeOverview && !hasOverview {
		pos := token.Position{Filename: p.Rel}
		if len(p.Files) > 0 {
			pos = fset.Position(p.Files[0].Package)
			pos.Filename = p.relPath(pos.Filename)
			pos.Line = 0
		}
		found = append(found, finding{
			Pkg:  p.Rel,
			Pos:  pos,
			Kind: kindPackage,
			Name: p.Name,
			Why: "is consumer-reachable and its overview is not runnable: it needs a " +
				"package-level func Example() with an // Output: comment, which is the " +
				"block godoc renders under the overview and the only kind go test executes (FR-66)",
		})
	}

	return rep, found, keys
}

// checkPackageComment applies rule 2.
func (p *pkg) checkPackageComment(fset *token.FileSet) (finding, bool) {
	want := "Package " + p.Name
	if p.Name == "main" {
		want = "Command "
	}
	var text string
	var pos token.Position
	for _, file := range p.Files {
		if file.Doc == nil {
			continue
		}
		if t := strings.TrimSpace(file.Doc.Text()); t != "" {
			text = t
			pos = fset.Position(file.Doc.Pos())
			pos.Filename = p.relPath(pos.Filename)
			break
		}
	}
	if text == "" {
		pos = fset.Position(p.Files[0].Package)
		pos.Filename = p.relPath(pos.Filename)
		return finding{
			Pkg:  p.Rel,
			Pos:  pos,
			Kind: kindPackage,
			Name: p.Name,
			Why: "has no package comment. FR-66 requires a package overview: what the " +
				"package is for, and what a reader has to know before using it",
		}, false
	}
	// "Command x" and "Package main" are both conventional for a main package;
	// go doc's synopsis reads the first sentence either way.
	if p.Name == "main" && (strings.HasPrefix(text, "Command ") || strings.HasPrefix(text, "Package main")) {
		return finding{}, true
	}
	if p.Name != "main" && strings.HasPrefix(text, want) {
		return finding{}, true
	}
	return finding{
		Pkg:  p.Rel,
		Pos:  pos,
		Kind: kindPackage,
		Name: p.Name,
		Why: fmt.Sprintf("has a package comment that does not open with %q: go doc's synopsis "+
			"is the first sentence of this comment, and an overview that does not name "+
			"the package is not an overview. It opens %q", want, firstWords(text)),
	}, false
}

// firstWords is the head of a comment, for an error message that shows what it
// actually says rather than only that it is wrong.
func firstWords(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	if len(line) > 48 {
		line = line[:48] + "…"
	}
	return line
}

// checkDecl applies rule 1 to one top-level declaration, returning the number
// of exported symbols it holds and the findings against them.
func (p *pkg) checkDecl(fset *token.FileSet, decl ast.Decl) (symbols int, found []finding) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if !d.Name.IsExported() {
			return 0, nil
		}
		what, name := kindFunc, d.Name.Name
		if d.Recv != nil {
			receiver, exported := receiverName(d.Recv)
			if !exported {
				return 0, nil
			}
			what, name = kindMethod, receiver+"."+d.Name.Name
		}
		symbols++
		if !documented(d.Doc) {
			found = append(found, p.missing(fset, d.Name.Pos(), what, name))
		}
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				if !s.Name.IsExported() {
					continue
				}
				symbols++
				if !documented(s.Doc) && !documented(d.Doc) {
					found = append(found, p.missing(fset, s.Name.Pos(), kindType, s.Name.Name))
				}
				n, more := p.checkTypeMembers(fset, s)
				symbols += n
				found = append(found, more...)
			case *ast.ValueSpec:
				what := kindVar
				if d.Tok == token.CONST {
					what = kindConst
				}
				for _, ident := range s.Names {
					if !ident.IsExported() {
						continue
					}
					symbols++
					if !documented(s.Doc) && !documented(s.Comment) && !documented(d.Doc) {
						found = append(found, p.missing(fset, ident.Pos(), what, ident.Name))
					}
				}
			}
		}
	}
	return symbols, found
}

// checkTypeMembers applies rule 1 to the exported fields of an exported struct
// and to the methods of an exported interface.
//
// An embedded field has no name of its own: go doc renders the embedded type's
// documentation in its place, so there is nothing here to document. An embedded
// interface is the same case.
func (p *pkg) checkTypeMembers(fset *token.FileSet, s *ast.TypeSpec) (symbols int, found []finding) {
	switch t := s.Type.(type) {
	case *ast.StructType:
		for _, field := range t.Fields.List {
			for _, ident := range field.Names {
				if !ident.IsExported() {
					continue
				}
				symbols++
				if !documented(field.Doc) && !documented(field.Comment) {
					found = append(found, p.missing(fset, ident.Pos(), kindField, s.Name.Name+"."+ident.Name))
				}
			}
		}
	case *ast.InterfaceType:
		for _, method := range t.Methods.List {
			for _, ident := range method.Names {
				if !ident.IsExported() {
					continue
				}
				symbols++
				if !documented(method.Doc) && !documented(method.Comment) {
					found = append(found, p.missing(fset, ident.Pos(), kindInterface, s.Name.Name+"."+ident.Name))
				}
			}
		}
	}
	return symbols, found
}

func (p *pkg) missing(fset *token.FileSet, pos token.Pos, what kind, name string) finding {
	at := fset.Position(pos)
	at.Filename = p.relPath(at.Filename)
	return finding{
		Pkg:  p.Rel,
		Pos:  at,
		Kind: what,
		Name: name,
		Why:  "is exported and has no doc comment (FR-66)",
	}
}

// position resolves an example's position, tolerating the whole-file example
// form where Code is the file rather than a block.
func (p *pkg) position(fset *token.FileSet, node ast.Node) token.Position {
	if node == nil {
		return token.Position{Filename: p.Rel}
	}
	at := fset.Position(node.Pos())
	at.Filename = p.relPath(at.Filename)
	return at
}

// relPath renders an absolute source path as a module-root-relative one, so a
// failure names the file the way a reader would open it.
func (p *pkg) relPath(path string) string {
	return filepath.ToSlash(filepath.Join(p.Rel, filepath.Base(path)))
}

// documented reports whether a comment group says anything. A group holding
// only a build constraint, a lint directive or blank lines is not a doc
// comment, and go doc renders it as none: CommentGroup.Text drops directives
// for exactly that reason, which is why this asks it rather than reading the
// comment's own bytes.
func documented(group *ast.CommentGroup) bool {
	return group != nil && strings.TrimSpace(group.Text()) != ""
}

// receiverName returns a method receiver's type name and whether it is
// exported.
func receiverName(recv *ast.FieldList) (string, bool) {
	if len(recv.List) == 0 {
		return "", false
	}
	expr := recv.List[0].Type
	if star, isPointer := expr.(*ast.StarExpr); isPointer {
		expr = star.X
	}
	// A generic receiver is written App[S], so the name is under the index.
	if index, generic := expr.(*ast.IndexExpr); generic {
		expr = index.X
	}
	if index, generic := expr.(*ast.IndexListExpr); generic {
		expr = index.X
	}
	ident, named := expr.(*ast.Ident)
	if !named {
		return "", false
	}
	return ident.Name, ident.IsExported()
}
