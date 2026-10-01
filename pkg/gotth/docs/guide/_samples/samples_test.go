package samples_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This suite is the reason the documentation can be trusted by somebody who
// never opens the library's source.
//
// It asserts two properties, and the second is the one that stops rot:
//
//  1. Every sample package compiles, against the working tree.
//  2. Every fenced Go or templ block in README.md, docs/quickstart.md and
//     docs/guide/*.md names the sample file it came from, that file exists, and
//     every significant line of the block is still in it.
//
// A code block that drifts from its sample fails here, in the same run that
// would have caught a sample that stopped compiling. A block with no marker
// fails too: an unmarked block is an unchecked block, which is exactly the
// state this suite exists to make unreachable.

// docDirs are the directories whose markdown this suite governs, and docPages
// the individual files, both relative to the module root.
//
// It is deliberately not the whole docs tree. The design documents — the PRD,
// the RFC, the protocol and instrumentation specifications — argue about code
// that does not exist yet and about code that is internal, and holding them to
// a compiled sample would be a category error. What is governed is exactly the
// set a reader is told to build from.
// The third page is the repository's own README — the front door, added after
// QA-1 found there was none (F-5). It carries code, so it is governed here for
// the same reason the quickstart is: the first block a reader ever sees is the
// one that must not have rotted.
var (
	docDirs  = []string{".."}
	docPages = []string{"../../quickstart.md", "../../README.md", "../../../README.md"}
)

// sampleMarker introduces a fenced block and names the file it was extracted
// from, as a path relative to this module's root:
//
//	<!-- sample: quickstart/main.go -->
//
// The one legal escape is a block that is deliberately not from this module —
// another project's API, a hypothetical, a shape being argued against:
//
//	<!-- sample: none — the OTel SDK's own setup, not gotth-live's -->
const sampleMarker = "<!-- sample:"

// noSample is the marker value that exempts a block from the file check. It
// still has to be written down, so an unchecked block is a decision somebody
// made rather than one nobody noticed.
const noSample = "none"

// elisions are the lines a documentation block may carry that no sample file
// contains.
var elisions = map[string]bool{
	"...":     true,
	"// ...":  true,
	"//  ...": true,
	"// …":    true,
	"…":       true,
}

var _ = Describe("the sample packages", func() {
	for _, pkg := range samplePackages() {
		It("builds "+pkg, func() {
			out := filepath.Join(GinkgoT().TempDir(), "out")
			cmd := exec.Command("go", "build", "-o", out, "./"+pkg)
			cmd.Dir = moduleRoot()
			combined, err := cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(),
				"sample package %q does not build, so the documentation extracted from it does not either:\n%s",
				pkg, combined)
		})

		It("vets "+pkg, func() {
			// go vet typechecks the _test.go files too, which go build does
			// not — and the testing guide's samples live in test files.
			cmd := exec.Command("go", "vet", "./"+pkg)
			cmd.Dir = moduleRoot()
			combined, err := cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), "go vet ./%s:\n%s", pkg, combined)
		})
	}

	It("has no sample package the documentation never shows", func() {
		cited := map[string]bool{}
		for _, block := range docBlocks() {
			if block.sample == noSample {
				continue
			}
			cited[strings.SplitN(block.sample, "/", 2)[0]] = true
		}

		var orphans []string
		for _, pkg := range samplePackages() {
			if !cited[pkg] {
				orphans = append(orphans, pkg)
			}
		}
		Expect(orphans).To(BeEmpty(),
			"these sample packages are cited by no documentation block: %v. "+
				"Either a page lost its block, or the sample outlived the page it was written for.",
			orphans)
	})
})

var _ = Describe("the documentation blocks", func() {
	blocks := docBlocks()

	It("finds blocks to check", func() {
		Expect(blocks).NotTo(BeEmpty(),
			"no fenced Go or templ block was found under %v or %v, which means this suite is checking nothing",
			docDirs, docPages)
	})

	for _, block := range blocks {
		block := block
		It(block.where()+" names its sample", func() {
			Expect(block.sample).NotTo(BeEmpty(),
				"this block has no %s marker. Put one immediately above the fence naming the file it "+
					"was extracted from, or %s none — <reason> if it is deliberately not from this module.",
				sampleMarker, sampleMarker)
		})

		if block.sample == "" || block.sample == noSample {
			continue
		}

		It(block.where()+" still matches "+block.sample, func() {
			path := filepath.Join(moduleRoot(), block.sample)
			source, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred(),
				"the block claims to come from %q, which does not exist", block.sample)

			have := map[string]bool{}
			for _, line := range strings.Split(string(source), "\n") {
				have[strings.TrimSpace(line)] = true
			}

			var missing []string
			for _, line := range strings.Split(block.body, "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || elisions[trimmed] {
					continue
				}
				if !have[trimmed] {
					missing = append(missing, trimmed)
				}
			}
			Expect(missing).To(BeEmpty(),
				"these lines are in the documentation and not in %s:\n  %s\n"+
					"The sample is the source of truth. Either the page was edited without the sample, "+
					"or the sample was edited without the page.",
				block.sample, strings.Join(missing, "\n  "))
		})
	}
})

// block is one fenced code block and the sample it names.
type block struct {
	file   string // markdown file, relative to the module root
	line   int    // 1-based line of the opening fence
	lang   string
	sample string
	body   string
}

func (b block) where() string {
	return b.file + ":" + itoa(b.line)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}

// docBlocks reads every governed markdown file and returns its Go and templ
// blocks. Blocks in other languages — shell, JSON, the wire protocol — are not
// this suite's business.
func docBlocks() []block {
	var out []block
	for _, path := range docFiles() {
		source, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(moduleRoot(), path)
		if err != nil {
			rel = path
		}

		lines := strings.Split(string(source), "\n")
		marker := ""
		for i := 0; i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])

			if strings.HasPrefix(trimmed, sampleMarker) {
				marker = strings.TrimSpace(strings.TrimSuffix(
					strings.TrimPrefix(trimmed, sampleMarker), "-->"))
				// "none — because ..." carries its reason with it.
				if fields := strings.Fields(marker); len(fields) > 0 {
					marker = fields[0]
				}
				continue
			}
			if !strings.HasPrefix(trimmed, "```") {
				if trimmed != "" {
					marker = ""
				}
				continue
			}

			lang := strings.TrimPrefix(trimmed, "```")
			var body []string
			start := i
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "```"; i++ {
				body = append(body, lines[i])
			}
			if lang == "go" || lang == "templ" {
				out = append(out, block{
					file:   rel,
					line:   start + 1,
					lang:   lang,
					sample: marker,
					body:   strings.Join(body, "\n"),
				})
			}
			marker = ""
		}
	}
	return out
}

// docFiles is every markdown file this suite governs: the guide beside it, the
// two pages one level up that a reader is sent to first, and the repository
// README a reader lands on before either.
func docFiles() []string {
	seen := map[string]bool{}
	var out []string

	add := func(path string) {
		clean := filepath.Clean(path)
		if seen[clean] {
			return
		}
		if _, err := os.Stat(clean); err != nil {
			return
		}
		seen[clean] = true
		out = append(out, clean)
	}

	for _, dir := range docDirs {
		entries, err := os.ReadDir(filepath.Join(moduleRoot(), dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			add(filepath.Join(moduleRoot(), dir, e.Name()))
		}
	}
	for _, page := range docPages {
		add(filepath.Join(moduleRoot(), page))
	}

	sort.Strings(out)
	return out
}

// samplePackages is every directory under the module root holding Go source.
//
// It is discovered rather than listed, because a list is a second place to
// forget a sample — and it is walked here rather than delegated to
// `go build ./...`, which would work, so that a broken sample fails a spec
// carrying its name instead of one carrying the whole module's compiler
// output.
func samplePackages() []string {
	entries, err := os.ReadDir(moduleRoot())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		files, err := os.ReadDir(filepath.Join(moduleRoot(), e.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".go") {
				out = append(out, e.Name())
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// moduleRoot is the directory holding go.mod. go test runs a package's binary
// with its working directory set to the package's source directory, and this
// package is the module root.
func moduleRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// ---------------------------------------------------------------------------
// FR-53's line budget.
//
// Everything from here to the end of the file is one check and the three
// declarations it needs. It is separable on purpose: the budget it asserts is
// PM-1's number, and if PM-1 withdraws it this section comes out whole.
//
// Why it exists. The block/sample pin above is a subset check — it builds a set
// of trimmed lines from the sample file and asserts each significant line of
// the documentation block is in it. That catches a doc block drifting away from
// its sample, which is what it was written for, and it does not catch a count
// moving. QA-1 mutation-tested it and published the result
// (docs/qa/phase-4-grading.md §10.4): a sample file that GAINS a counted line
// stays green, because the pin only reads the doc side; and a doc block that
// REPEATS a line it already carries stays green four times over, because a set
// membership test cannot see a duplicate. Both mutations move the number
// FR-53 is measured on.
//
// The margin is zero. Phase 4's exit box 2 went green on 2026-08-05 at exactly
// 31 counted lines against a budget of ≤31, after being open since v0.6 and
// missed by 16, then 9, then 8. The whole of that margin is one 84-column call
// in view.templ that no formatter in this repository will ever wrap and any
// human might. Before this check, nothing in the tree could fail if somebody
// wrapped it.

// fr53Budget is the line budget FR-53 sets, and it is not this file's to
// choose: "A developer following the quickstart from zero MUST reach a working,
// live counter in ≤15 minutes and ≤31 lines of application code"
// (docs/PRD.md §5.I). It read 30 from the day the requirement was written until
// 2026-08-05, when docs/PRD.md §9's v1.1 row 1 amended it to 31 — the smallest
// count this API can express — which §9 v1.2 records L9-1 countersigning. If it
// moves again it moves in §9 first and here second, and under trigger 1 as
// repaired at v1.2 it moves DOWN only.
const fr53Budget = 31

// fr53Page is the page FR-53 measures, relative to this module's root. The
// repository README carries excerpts under the same two sample markers and they
// are excerpts — a `var app` block with no `const` above it, a view with no
// imports. FR-53 counts the two complete files the quickstart prints.
const fr53Page = "../../quickstart.md"

// fr53Files are the two artifacts FR-53's scope ruling names. The rule "binds
// the total: every line of application code the developer authors, in every
// file, whatever its extension", and for the quickstart that is main.go plus
// view.templ. Markup is not exempt: the templ view is compiled Go, and
// exempting it would let the count be met by moving code across a file
// boundary.
var fr53Files = []string{"quickstart/main.go", "quickstart/view.templ"}

// fr53Counted applies FR-53's counting rule — fixed at docs/PRD.md §9 v0.6 by
// PM-1 ruling, and unchanged since — to one artifact's source, and returns the
// lines it counts rather than how many, so a failure can print them.
//
// Counted: every line that is not blank, not a comment, and not a `package` or
// `import` line. Not counted, by the rule's own carve-out: generated files the
// developer does not write (*_templ.go), go.mod, and shell commands — which is
// why neither this function nor its callers ever open one.
//
// The one reading the rule's text leaves open is whether the entries inside a
// parenthesised `import ( … )` block are "import lines". They are, and this
// function treats the whole declaration — the block and its closing paren — as
// the exclusion. That is not a preference. QA-1 ruled it on the record
// (docs/qa/phase-4-grading.md §10.5) on the ground that it is the only reading
// that reproduces this project's published history of the measurement: run over
// the same two blocks at the five commits the record states a figure for, it
// returns 46, 46, 39, 39, 31 — which is what the record says at every one of
// them — while the other reading returns 55, 55, 46, 46, 38, and 55 and 38
// appear nowhere. The other reading also makes the count depend on whether
// gofmt grouped the imports, which changes no program.
func fr53Counted(source string) []string {
	var counted []string
	var inImportBlock, inBlockComment bool

	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)

		switch {
		case inBlockComment:
			if strings.Contains(trimmed, "*/") {
				inBlockComment = false
			}
		case inImportBlock:
			if trimmed == ")" {
				inImportBlock = false
			}
		case trimmed == "":
		case strings.HasPrefix(trimmed, "//"):
		case strings.HasPrefix(trimmed, "/*"):
			if !strings.Contains(trimmed, "*/") {
				inBlockComment = true
			}
		case trimmed == "package" || strings.HasPrefix(trimmed, "package "):
		case strings.HasPrefix(trimmed, "import ("):
			inImportBlock = true
		case strings.HasPrefix(trimmed, "import "):
		default:
			counted = append(counted, trimmed)
		}
	}
	return counted
}

var _ = Describe("FR-53's line budget", func() {
	It("holds the quickstart application at or under it, on both counting paths", func() {
		// Path A: the fenced blocks on the page. These are the bytes a reader
		// copies, so they are what the requirement is about.
		blocks := map[string][]string{}
		for _, b := range docBlocks() {
			if b.file == fr53Page {
				blocks[b.sample] = append(blocks[b.sample], b.body)
			}
		}
		fromPage := map[string]string{}
		for _, name := range fr53Files {
			Expect(blocks[name]).To(HaveLen(1),
				"%s should carry exactly one block marked %s %s -->, and it carries %d. "+
					"FR-53 counts the two complete files that page prints; more than one block "+
					"under a marker, or none, and this check no longer knows which bytes it is counting.",
				fr53Page, sampleMarker, name, len(blocks[name]))
			fromPage[name] = blocks[name][0]
		}

		// Path B: the shipping sample files. The pin above makes these a
		// superset of path A's significant lines; it does not make them the
		// same size, which is the gap this closes.
		fromDisk := map[string]string{}
		for _, name := range fr53Files {
			source, err := os.ReadFile(filepath.Join(moduleRoot(), name))
			Expect(err).NotTo(HaveOccurred(),
				"FR-53 counts %s and it cannot be read", name)
			fromDisk[name] = string(source)
		}

		for _, path := range []struct {
			what    string
			sources map[string]string
		}{
			{fr53Page + "'s fenced blocks, which is what a reader copies", fromPage},
			{"docs/guide/_samples/quickstart, which is what this module compiles", fromDisk},
		} {
			total := 0
			var detail []string
			for _, name := range fr53Files {
				lines := fr53Counted(path.sources[name])
				total += len(lines)
				detail = append(detail,
					"  "+name+" — "+itoa(len(lines))+" counted:\n    "+strings.Join(lines, "\n    "))
			}

			Expect(total).To(BeNumerically("<=", fr53Budget),
				"the quickstart application counts %d lines of application code against FR-53's "+
					"budget of %d, measured on %s.\n\n"+
					"The rule is docs/PRD.md §5.I: every line that is not blank, not a comment, and "+
					"not a `package` or `import` line, across main.go plus view.templ, with an import "+
					"declaration excluded whole — parenthesised block and closing paren included.\n\n"+
					"%s\n\n"+
					"Phase 4's exit box 2 is green at exactly 31 with no margin at all, so one line "+
					"is the whole of it. Take the line back out, or move the work into the library "+
					"the way app.Document took eight lines of page shell in. Do NOT raise the "+
					"constant above: FR-53's budget moves in docs/PRD.md §9 or not at all, and under "+
					"trigger 1 as repaired at v1.2 it moves down only — a total above 31 withdraws "+
					"the amendment and reopens the box rather than re-baselining onto it.",
				total, fr53Budget, path.what, strings.Join(detail, "\n"))
		}
	})
})
