package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ---------------------------------------------------------------------------
// FR-58's anti-rot guard: the error census.
//
// # Why this is a count and not a walk that grades
//
// FR-58 requires every library-produced error to name the session, the causal
// identifier where one exists, and the actionable next step. The first two
// clauses are checkable by machine at the sites where they apply, and
// live/fr58_test.go and internal/session/emission_internal_test.go check them
// there, on constructed errors, through the paths that build them.
//
// The third is not. "The actionable next step" is a judgement about whether a
// sentence tells a reader what to do, and every automatic proxy for it —
// count the colons, look for an imperative verb, require a minimum length —
// is a rule a bad message passes and a good one fails. Writing one would
// produce a gate that is green because it is weak, which is worse than no gate
// because it looks like a gate. So the grading lives in docs/error-audit.md,
// where a person did it and signed their name to the method.
//
// What rots about a document like that is not its judgements. It is its
// COVERAGE: somebody adds an error next quarter, the document says nothing
// about it, and nothing anywhere notices that it has stopped being an
// enumeration of the whole set. That is what this asserts. When it fails, the
// message says what to do, because a gate that fails without saying is the
// same defect one directory over.
//
// # The rule
//
// A site is one place a library error message is authored:
//
//   - errors.New(…) and fmt.Errorf(…) — the two the standard library gives.
//   - A composite literal of a type whose name ends in "Error" — ConfigError,
//     RejectError, InvalidFrameError, DenyError, FatalDenyError,
//     RetryableError. Each carries a message the type's Error method renders,
//     so each literal authors one.
//   - A call to protocol's reject helper, which is the only function in this
//     module that takes a message and returns an error built elsewhere. Every
//     inbound rejection an operator ever reads is written at one of those call
//     sites and nowhere else, so leaving them out would leave the whole
//     client-facing half of the surface uncounted.
//
// It deliberately does NOT try to be a general "returns an error" detector.
// The census is over places a HUMAN WROTE A SENTENCE, because those are the
// places FR-58 has an opinion about; a function that returns another
// function's error verbatim has no message of its own to grade.
// ---------------------------------------------------------------------------

// errorCensus is the number of error-authoring sites in each package of the
// published module that the Phase 4 audit enumerated and graded.
//
// Produced by this walk at the commit that landed docs/error-audit.md, and
// reproduced by running this spec. A number without its method is what this
// project does not ship, so the method is the file you are reading.
var errorCensus = map[string]int{
	"internal/cmd/gotth-live-dev": 3,
	"internal/obs":                4,
	"internal/protocol":           40,
	"internal/render":             13, // namespace overlap plus four distinct child contract refusals; audit §3.7
	"internal/session":            8,
	"internal/wsx":                10,
	// 37 at the walk, then 38 at revision 2: Config.Init's missing-hook
	// ConfigError is gone, because FR-53 made the field optional, and
	// live/page.go authors two. docs/error-audit.md §3.3.1 grades the two;
	// §3.1's struck row is the one that left.
	//
	// Then 39 at revision 4: (*App).Document refuses a document with an empty
	// title, because a title is application content with no defensible
	// default and an empty one renders as a blank tab rather than as an
	// error. docs/error-audit.md §3.3.2 grades it. Document's mount-path
	// refusals are NOT new sites — it calls normalizeMountFor, which Script
	// and Mux already call, and §3.3 grades those six clauses once.
	//
	// Then 40 at revision 5: Script refuses to render inside Document's head
	// content, where a second runtime tag would land above the inspector's and
	// silently blind it. That is L9-1's PS-1 (docs/reviews/page-shell.md §3.2)
	// repaired in code rather than in prose; docs/error-audit.md §3.3.2's
	// second row grades it.
	//
	// Then back to 39 at revision 6, which is the first time this census fired
	// on a REMOVAL: Config.Execute was deleted when live.Effect became a
	// concrete struct carrying its own Run (operator ruling, 2026-09-03), and
	// the one error that hook authored — "a reducer returned an effect but
	// Config.Execute is nil" — went with it. The census is symmetric, which is
	// what makes it a census rather than a floor. The failure that error
	// described moved to internal/session, which refuses an effect whose Run is
	// nil; that site was already enumerated.
	//
	// Then 37 at revision 7, on the same day and for the same kind of reason.
	// live.Session became generic in the identity type, so Config.Authenticate
	// returns the APPLICATION's own type and "returned no identity and no
	// error" stopped being a result it can produce. Both errors that answered
	// that shape are gone: the adapter's, and page.go's errNoIdentity. A type
	// parameter deleting two error messages is the strongest form of the
	// argument for it — the failure is not handled better, it is unreachable.
	"live": 37,
	// 7 at the walk, then 8 at revision 3: Client.NextErr now wraps whatever
	// ended the wait with the client's name and its session, so the value a
	// caller holds carries FR-58's session clause and not only the tb.Fatalf
	// paths. docs/error-audit.md §3.4 grades the wrap.
	"live/livetest": 8,
}

// outOfScope names the packages the audit deliberately does not cover, with
// the reason each is out. It is asserted as an exact set rather than used as a
// skip list, so a new directory cannot appear inside internal/ and quietly
// avoid being graded: an unknown package fails this spec whichever side of the
// line it belongs on.
var outOfScope = map[string]string{
	"internal/arch": "test-only package; it contains no non-test code, so it produces no " +
		"library error a consumer can reach",
	"internal/clientcodec": "build-time code generator, run by gen.sh. Its errors are read by " +
		"whoever regenerates the client codec, never linked into a consumer's binary",
	"internal/cmd/gen-clientcodec": "the generator's main package, out for the same reason as " +
		"internal/clientcodec",
	"internal/livebridge": "declares one variable and one interface and constructs no error",
	"internal/obstest":    "test support; not linked into a consumer's binary",
	"internal/protocol/gotthlivepb": "generated by protoc and protoc-gen-liquidproto, marked DO NOT " +
		"EDIT. Its nil-receiver messages are wrapped by RejectError inbound and " +
		"InvalidFrameError outbound, and those wrappers are what the audit grades",
}

var _ = Describe("FR-58's error census", func() {
	It("finds exactly the error-authoring sites docs/error-audit.md enumerates", func() {
		root := libraryRoot()
		counted := countErrorSites(root)

		// Every package the walk found is on exactly one of the two lists.
		var unclassified []string
		for pkg := range counted {
			_, graded := errorCensus[pkg]
			_, excused := outOfScope[pkg]
			if !graded && !excused {
				unclassified = append(unclassified, pkg)
			}
		}
		sort.Strings(unclassified)
		Expect(unclassified).To(BeEmpty(),
			"these packages of the published module author error messages and appear on neither "+
				"list in internal/arch/errors_test.go: %v.\n"+
				"Grade each one against FR-58 — does the message name the session, the causal "+
				"identifier where one exists, and the actionable next step? — add a row per error "+
				"to docs/error-audit.md, and add the package to errorCensus. If the package is "+
				"genuinely unreachable by a consumer, add it to outOfScope WITH THE REASON "+
				"instead; an exclusion without one is how a whole package stopped being audited.",
			unclassified)

		// And every package the audit graded still has the number of sites it
		// graded. This is the arm that fires on an ordinary edit, so its
		// message is written for somebody who did not know this spec existed.
		for pkg, want := range errorCensus {
			Expect(counted).To(HaveKeyWithValue(pkg, want),
				"package %s authors %d error messages and docs/error-audit.md enumerates %d.\n"+
					"An error was added, removed or moved. FR-58 is graded per error, so the "+
					"audit is only worth what its coverage is worth: grade the new one against "+
					"the three clauses, give it a row in docs/error-audit.md §3, and update this "+
					"number in the same commit.",
				pkg, counted[pkg], want)
		}

		// The excusals are asserted too. A package that stops authoring errors
		// is fine; one that is listed as out of scope and was deleted is a
		// stale exclusion, and a stale exclusion is indistinguishable from a
		// real one at a glance.
		for pkg := range outOfScope {
			Expect(packageExists(root, pkg)).To(BeTrue(),
				"outOfScope names %s and no such package is in the tree: delete the entry rather "+
					"than leaving an exclusion nobody can evaluate", pkg)
		}
	})
})

// libraryRoot is the root of this library's tree, found by walking up from
// this test's own directory rather than by counting "..", so moving this
// package does not silently point the walk at the wrong tree.
//
// The marker was go.mod until this library joined the single candace module.
// go.mod now sits three directories higher, over a tree this census says
// nothing about, so the marker is the generator that owns the tree's
// generated code and the package directory the census is mostly about — two
// rather than one, because either alone names something a parent directory
// could plausibly also hold.
func libraryRoot() string {
	GinkgoHelper()
	dir, err := os.Getwd()
	Expect(err).NotTo(HaveOccurred())
	for {
		_, genErr := os.Stat(filepath.Join(dir, "gen.sh"))
		_, liveErr := os.Stat(filepath.Join(dir, "live"))
		if genErr == nil && liveErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		Expect(parent).NotTo(Equal(dir), "no gen.sh beside a live/ above %s", dir)
		dir = parent
	}
}

func packageExists(root, pkg string) bool {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(pkg)))
	return err == nil && info.IsDir()
}

// countErrorSites walks the published module's non-test sources and returns
// the number of error-authoring sites per package, keyed by slash-separated
// path relative to the module root.
//
// It covers live/… and internal/… and nothing else: test/, bench/, tools/ and
// docs/guide/_samples/ are separate trees, examples/ sits outside this one
// entirely, and none of them is library code a consumer links. Each of those
// trees carried its own go.mod until the single-module fold; the sentence
// above is what that separation was saying, and it is still the rule.
func countErrorSites(root string) map[string]int {
	GinkgoHelper()

	counts := map[string]int{}
	for _, top := range []string{"live", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, filepath.Dir(path))
			Expect(relErr).NotTo(HaveOccurred())
			pkg := filepath.ToSlash(rel)
			if _, seen := counts[pkg]; !seen {
				counts[pkg] = 0
			}
			counts[pkg] += errorSitesInFile(path)
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
	}
	return counts
}

func errorSitesInFile(path string) int {
	GinkgoHelper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	Expect(err).NotTo(HaveOccurred(), "parsing %s", path)

	n := 0
	ast.Inspect(file, func(node ast.Node) bool {
		switch v := node.(type) {
		case *ast.CallExpr:
			switch fn := v.Fun.(type) {
			case *ast.SelectorExpr:
				pkg, ok := fn.X.(*ast.Ident)
				if !ok {
					return true
				}
				if (pkg.Name == "errors" && fn.Sel.Name == "New") ||
					(pkg.Name == "fmt" && fn.Sel.Name == "Errorf") {
					n++
				}
			case *ast.Ident:
				// protocol's reject helper. Named rather than pattern-matched
				// because it is one function in one package and a pattern that
				// caught "any call whose name suggests refusal" would catch
				// unrelated code in a year.
				if fn.Name == "reject" {
					n++
				}
			}
		case *ast.CompositeLit:
			if ident, ok := v.Type.(*ast.Ident); ok && strings.HasSuffix(ident.Name, "Error") {
				n++
			}
			if sel, ok := v.Type.(*ast.SelectorExpr); ok && strings.HasSuffix(sel.Sel.Name, "Error") {
				n++
			}
		}
		return true
	})
	return n
}
