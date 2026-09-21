package arch_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// module is this module's path. It is spelled out rather than derived so that
// a rename shows up here as a deliberate edit.
const module = "github.com/candacelabs/csf/pkg/gotth"

// goList runs go list in dir — the caller's own directory when dir is empty —
// and returns its standard output, one trimmed non-empty line per element.
//
// Reading only standard output is the whole job of this function. go list
// writes advisory lines to standard error: "warning: ignoring symlink …" for
// every symlink it meets while walking a tree, and "go: downloading …" while
// the module cache warms. CombinedOutput merged those into the result, where
// each one arrived here looking exactly like a package path, and an npm
// node_modules under this module — which the Phase 5 comparison app lands by
// design — therefore failed the two-package cap below with a message accusing
// the author of adding a third exported package.
//
// Standard error is captured rather than discarded, and surfaced only when the
// command itself fails. That is the case where it is the diagnosis rather than
// noise; dropping it there would trade a misleading failure for an unreadable
// one.
func goList(dir string, args ...string) ([]string, error) {
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list %s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}

	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// deps returns the transitive import set of pkg as the Go build system sees
// it, including pkg itself. Walking the real build graph is the point: an
// import block tells you what one file reached for, and every property
// asserted below is about what the whole binary ends up linking.
func deps(pkg string) []string {
	GinkgoHelper()
	pkgs, err := goList("", "-deps", pkg)
	Expect(err).NotTo(HaveOccurred())
	Expect(pkgs).NotTo(BeEmpty(), "go list -deps %s returned nothing", pkg)
	return pkgs
}

// firstPartyImports returns, for every package of this module reachable from
// pkg, the packages it imports directly.
//
// Two of the properties below can only be stated over first-party code, and
// the reason is worth recording rather than rediscovering. The protobuf
// runtime links encoding/json for its own struct-tag handling, and templ's
// root package contains an http.Handler, so the transitive closure of anything
// that parses a frame or renders a component necessarily contains both.
// Asserting over the closure would assert something that is not true. What is
// actually wanted — no code in this library reaches for JSON to put bytes on
// the wire, and nothing on the render path reaches for a clock — is a property
// of the code in this module, and that is what is checked.
func firstPartyImports(pkg string) map[string][]string {
	GinkgoHelper()
	lines, err := goList("", "-deps", "-f", "{{.ImportPath}}\t{{join .Imports \" \"}}", pkg)
	Expect(err).NotTo(HaveOccurred())

	byPkg := map[string][]string{}
	for _, line := range lines {
		path, imports, _ := strings.Cut(line, "\t")
		if !strings.HasPrefix(path, module) {
			continue
		}
		byPkg[path] = strings.Fields(imports)
	}
	Expect(byPkg).NotTo(BeEmpty(), "no first-party packages reachable from %s", pkg)
	return byPkg
}

// moduleImports returns every package in this module and everything it imports
// directly, test files included.
//
// It is a different question from firstPartyImports, which walks outwards from
// one root: this walks the whole module, because the property below is about
// who imports a package rather than about what a package reaches. Test imports
// are counted too — a constructor reachable from a test file in a third package
// is reachable from a third package.
func moduleImports() map[string][]string {
	GinkgoHelper()
	lines, err := goList("",
		"-f", `{{.ImportPath}}	{{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}`,
		module+"/...")
	Expect(err).NotTo(HaveOccurred())

	byPkg := map[string][]string{}
	for _, line := range lines {
		path, imports, _ := strings.Cut(line, "\t")
		byPkg[path] = strings.Fields(imports)
	}
	Expect(byPkg).NotTo(BeEmpty(), "go list returned no packages for this module")
	return byPkg
}

// packages returns every package in this module, as the build system sees it.
//
// The pattern is the module path rather than ./... because a test runs with
// its own package directory as the working directory, and ./... would then
// enumerate only this package's subtree — which is empty, and would make the
// assertion below pass by finding nothing.
func packages() []string {
	GinkgoHelper()
	pkgs, err := goList("", module+"/...")
	Expect(err).NotTo(HaveOccurred())
	Expect(pkgs).NotTo(BeEmpty())
	return pkgs
}

var _ = Describe("The go list helper every property below is read through", func() {

	// D-13, held where it can fail rather than where it did.
	//
	// Every assertion in this file is a statement about a list of package
	// paths, so a line that is not a package path is not a smaller problem
	// than a wrong import — it is a failure that names the wrong culprit. The
	// original failure took ten lines of stderr from a bench directory's
	// node_modules and reported them as eleven extra exported packages, which
	// reads as "you added a third exported package" and is not remotely what
	// happened.
	//
	// The provocation is reproduced rather than described: a scratch module
	// with an npm workspace tree in it, whose node_modules entry is a symlink
	// to the workspace package's real directory. That is the layout npm writes
	// and it is what makes go list print "warning: ignoring symlink" — once
	// per walk, and it walks twice — on stderr.
	//
	// The pattern is the module path rather than ./..., which is not a detail:
	// go list only warns for the module-path form, and the module-path form is
	// what packages() uses and why.
	It("does not report a go list warning as a package path", func() {
		dir := GinkgoT().TempDir()

		Expect(os.WriteFile(filepath.Join(dir, "go.mod"),
			[]byte("module scratch.example\n\ngo 1.21\n"), 0o644)).To(Succeed())
		Expect(os.Mkdir(filepath.Join(dir, "alpha"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "alpha", "alpha.go"),
			[]byte("package alpha\n"), 0o644)).To(Succeed())

		Expect(os.MkdirAll(filepath.Join(dir, "bench", "apps", "next"), 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(dir, "bench", "node_modules"), 0o755)).To(Succeed())
		Expect(os.Symlink(filepath.Join(dir, "bench", "apps", "next"),
			filepath.Join(dir, "bench", "node_modules", "next"))).To(Succeed())

		pkgs, err := goList(dir, "scratch.example/...")

		Expect(err).NotTo(HaveOccurred())
		Expect(pkgs).To(ConsistOf("scratch.example/alpha"),
			"go list writes its warnings to stderr; a helper that merges them into stdout "+
				"turns each one into a package path that nobody wrote")
	})

	// The other half of the same decision. Demoting stderr is only safe if the
	// one case where stderr is the answer still gets it, and that case is a
	// go list that actually failed: the exit status says nothing at all about
	// why.
	It("puts the command's stderr in the error when the command fails", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "go.mod"),
			[]byte("this is not a go.mod\n"), 0o644)).To(Succeed())

		_, err := goList(dir, "./...")

		Expect(err).To(MatchError(ContainSubstring("unknown directive")),
			"the reason go list failed is on its stderr and nowhere else")
	})
})

// notLibrary reports whether rel names a package that is not this library.
//
// The three trees it names — the benchmark applications, the suites that need
// a dependency the library refuses, and the contributor tools — were separate
// modules until this library joined the single candace module, and `go list`
// on the library's own module path could not reach them at all. Folding the
// modules together did not make any of them the library: a benchmark
// application, a three-router mount suite and a minifier are still not
// something a consumer imports, and the cap below is a statement about what a
// consumer can import.
//
// So the boundary the module split used to draw is drawn here by name. That is
// a real loss of rigour — a fourth tree arriving under this root is inside the
// cap until somebody adds it to this list, where a fourth module was outside it
// by construction — and it is stated rather than hidden because the cap is what
// notices a third exported package.
func notLibrary(rel string) bool {
	for _, tree := range []string{"bench/", "test/", "tools/", "docs/"} {
		if strings.HasPrefix(rel, tree) {
			return true
		}
	}
	return false
}

var _ = Describe("The module's exported surface", func() {

	// The structural form of the two-package cap. The argument for shipping a
	// second exported package was narrow and specific — production code must
	// not link testing — and it does not generalise to a third arriving on
	// convenience grounds. A cap that lives only in prose is a cap until
	// somebody adds a directory.
	//
	// It also catches the case that is easy to reach by accident: the embedded
	// client artifact lives under live/clientjs, and a stray .go file there
	// would make it a third exported package without anybody deciding to.
	It("is exactly the two packages that were ruled on", func() {
		var exported []string
		for _, pkg := range packages() {
			rel := strings.TrimPrefix(pkg, module+"/")
			if strings.HasPrefix(rel, "internal/") || strings.Contains(rel, "/internal/") {
				continue
			}
			if notLibrary(rel) {
				continue
			}
			exported = append(exported, rel)
		}

		Expect(exported).To(ConsistOf("live", "live/livetest"),
			"a third exported package needs a ruling, not a directory. Test scaffolding belongs in "+
				"live/livetest and everything else belongs under internal/")
	})
})

var _ = Describe("The module's import graph", func() {

	// The condition attached to shipping a second exported package. livetest
	// exists precisely so a consumer's production binary does not link
	// testing — and, with it, flag, regexp, runtime/pprof and runtime/trace —
	// merely because it imports live. That is the whole argument for the
	// split, so it is checked rather than asserted.
	Describe("the public package live", func() {
		It("does not link the testing machinery into consumers' binaries", func() {
			for _, forbidden := range []string{
				"testing",
				"flag",
				"runtime/pprof",
				"runtime/trace",
			} {
				Expect(deps(module+"/live")).NotTo(ContainElement(forbidden),
					"package live transitively imports %q; test-only code belongs in live/livetest",
					forbidden)
			}
		})
	})

	// The isolation property the transport requirement was amended to
	// require. The core packages reach the connection through channels and a
	// framer function value, so a second transport stays possible without a
	// one-implementation interface existing to prove it.
	Describe("the core packages", func() {
		cores := []string{"internal/session", "internal/render", "internal/protocol"}

		It("do not reach the transport package or any WebSocket library", func() {
			for _, core := range cores {
				for _, forbidden := range []string{
					module + "/internal/wsx",
					"github.com/coder/websocket",
					"nhooyr.io/websocket",
					"github.com/gorilla/websocket",
				} {
					Expect(deps(module+"/"+core)).NotTo(ContainElement(forbidden),
						"package %s transitively imports %s", core, forbidden)
				}
			}
		})
	})

	// The wire-format ban, held where it would otherwise rot. Every payload in
	// both directions is one encoded protobuf frame; there is no JSON, no text
	// framing, and no debug escape hatch.
	Describe("the wire path", func() {
		It("contains no first-party code that reaches for JSON", func() {
			for _, entry := range []string{"internal/protocol", "internal/wsx", "live"} {
				for pkg, imports := range firstPartyImports(module + "/" + entry) {
					Expect(imports).NotTo(ContainElement("encoding/json"),
						"package %s imports encoding/json: every payload on the wire is an encoded Frame", pkg)
				}
			}
		})
	})

	// The condition attached to admitting livetest.NewSession (L9-1 C-25).
	//
	// live.Session's fields are unexported so that nothing downstream of the
	// handshake can mint an identity, and livetest needs to build one anyway.
	// The mechanism is a var in internal/livebridge that live assigns at init
	// and livetest reads, and its safety rests on a claim about who can import
	// it: gotth-live/internal/... is reachable from inside this module and
	// nowhere else, so the only route a consumer has is through livetest, where
	// the first parameter is a testing.TB.
	//
	// That is a claim about the import graph, and this is the assertion that
	// keeps it true. A third importer inside the module would not let a
	// consumer forge a Session, but it would mean the constructor is reachable
	// from code nobody decided should reach it — which is how the argument
	// above stops being the argument.
	Describe("the session-constructor bridge", func() {
		It("is imported by exactly live and live/livetest", func() {
			bridge := module + "/internal/livebridge"

			var importers []string
			for pkg, imports := range moduleImports() {
				if slices.Contains(imports, bridge) {
					importers = append(importers, strings.TrimPrefix(pkg, module+"/"))
				}
			}

			Expect(importers).To(ConsistOf("live", "live/livetest"),
				"internal/livebridge holds a constructor for a value live keeps unconstructable on "+
					"purpose. Its containment argument is that only these two packages reach it, "+
					"and a new importer is a decision, not an import")
		})
	})

	// The functional core's import allowlist. A reducer and a render are pure
	// functions of their inputs, and purity is structural here rather than a
	// review note: the render path may not reach for a clock, a random source,
	// the network, the filesystem, or a logger.
	Describe("the functional core", func() {
		banned := []string{
			"net",
			"net/http",
			"os",
			"os/exec",
			"math/rand",
			"math/rand/v2",
			"crypto/rand",
			"log",
			"log/slog",
			"database/sql",
			"encoding/json",
			"time",
		}

		It("does not reach for a clock, a random source, or the outside world", func() {
			for pkg, imports := range firstPartyImports(module + "/internal/render") {
				for _, forbidden := range banned {
					Expect(imports).NotTo(ContainElement(forbidden),
						"package %s imports %q: rendering is a pure function of state, and "+
							"time and identifiers enter a transition stamped at the actor boundary",
						pkg, forbidden)
				}
			}
		})
	})
})
