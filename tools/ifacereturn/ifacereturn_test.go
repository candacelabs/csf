// Ginkgo/Gomega, like every other behavior suite in this module.
//
// This file used to be stock analysistest with two plain `func TestXxx(t
// *testing.T)` declarations, above a header explaining that analysistest.Run
// takes a *testing.T and that candace/pkg/scripts/check-test-style.sh scoped
// the Ginkgo convention to candace/pkg, so this package sat outside it "by that
// script's own boundary rather than by an exemption written here".
//
// Both halves of that were wrong, and the second one was the expensive half.
// analysistest.Run does not take a *testing.T: it takes analysistest.Testing,
// which is `Errorf(format string, args ...any)` and nothing else, so a Ginkgo
// spec has satisfied it all along. And a gate's walk is not a statement about
// where a convention applies — it is a line, and a line teaches people to build
// on the far side of it. The walk grew to cover candace/tools on 2026-09-02;
// this suite is the reason it had to.
//
// GinkgoTB() rather than GinkgoT(): both satisfy analysistest.Testing, but
// Run() additionally type-asserts its harness to testing.TB, and when that
// succeeds it calls testenv.NeedsGoPackages and logs each loaded package's
// parse and type errors instead of discarding them. GinkgoT() fails that
// assertion — testing.TB has an unexported method — so passing it would compile
// and silently give up the diagnostics that explain a broken fixture.
// GinkgoTBWrapper embeds testing.TB and overrides every method of it, so the
// assertion succeeds and nothing reaches the nil embedded value.
//
// No gomock. The analyzer has no collaborator to double: Inspect takes
// []*ast.File and *types.Info, both produced by the real type-checker, and a
// fake go/types would be a test of the fake. The subject under test is a set of
// fixture packages, and analysistest is already the harness that drives them.
package ifacereturn_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/candacelabs/csf/tools/ifacereturn"
)

// fixtureSuffix is the guard extension the fixtures are stored under. They are
// materialized without it into a temporary GOPATH, so a tree full of
// deliberate CS-8 violations never joins the tracked Go corpus that the
// blocking gate, the reuse gate and the census all read.
const fixtureSuffix = ".txt"

var _ = Describe("the ifacereturn analyzer", Ordered, func() {
	// One load of the fixture corpus for the whole container: analysistest
	// shells out to `go` to type-check, which is seconds rather than
	// milliseconds, and every specification below reads the same result.
	var results []*analysistest.Result

	BeforeAll(func() {
		requireGoToolchain()
		results = analysistest.Run(GinkgoTB(), materializeFixtures(), ifacereturn.Analyzer, "subject")
	})

	It("reports exactly the set the fixtures mark with `// want`", func() {
		// analysistest.Run has already compared the reported set against every
		// `// want` comment in the fixture — a missing report and a spurious one
		// both fail it — so arriving here without a recorded failure is that
		// assertion. What Run never checks is that it analyzed anything at all:
		// it returns one Result per root package, and a pattern that matched an
		// empty corpus would produce a clean run over nothing.
		Expect(results).To(HaveLen(1), "one Result per analyzed root package; `subject` is the only one")
		Expect(results[0].Err).NotTo(HaveOccurred())
		Expect(results[0].Diagnostics).To(HaveLen(12))
	})

	It("renders each finding in source order, with the exact text Message produces", func() {
		// `// want` patterns are regular expressions, so the fixture's
		// `.Registry..Store` matches the receiver-qualified name the analyzer
		// actually renders and also several strings it must never render. The
		// literal comparison below is the one that pins the punctuation, the
		// `(result N of M)` clause and the order Inspect documents.
		Expect(messages(results[0].Diagnostics)).To(Equal([]string{
			"NewStore returns the interface subject.IStore",
			"OpenReader returns the interface io.Reader",
			"OpenSink returns the interface thirdparty.Sink",
			"Load returns the interface subject.IStore (result 1 of 2)",
			"Decode returns the interface any",
			"AllStores returns the interface subject.IStore",
			"NewView returns View.Store, which is the interface subject.IStore",
			"NewViews returns View.Store, which is the interface subject.IStore",
			"NewInner returns Inner.View.Store, which is the interface subject.IStore",
			"NewRecursive returns Recursive.Store, which is the interface subject.IStore",
			"(Registry).Store returns the interface subject.IStore",
			"(*Registry).Reader returns the interface io.Reader",
		}))
	})

	It("returns no analysis result value, because it is a reporter and not a fact producer", func() {
		// analysis.Analyzer.Run's own `any` result is this lane's first finding
		// and is left standing on purpose. Nothing consumes it, no other
		// analyzer depends on this one, and analysistest looks only at
		// diagnostics — so the day it starts carrying a value is a design
		// change that should fail here rather than pass unnoticed.
		Expect(results[0].Result).To(BeNil())
	})
})

var _ = Describe("Finding.Message", func() {
	// The `// want` patterns above would still match if the arity clause
	// silently disappeared from a multi-result signature, so the two message
	// shapes are pinned directly. These specifications need no Go toolchain,
	// which is what keeps the suite from being a completely vacuous pass under
	// Bazel's sandbox.

	It("names only the interface when the signature has one result", func() {
		finding := ifacereturn.Finding{
			Declaration: "NewStore",
			Position:    1,
			Arity:       1,
			Interface:   "subject.IStore",
		}
		Expect(finding.Message()).To(Equal("NewStore returns the interface subject.IStore"))
	})

	It("names which result it means when the signature has several", func() {
		finding := ifacereturn.Finding{
			Declaration: "(*Registry).Load",
			Position:    2,
			Arity:       3,
			Interface:   "io.Reader",
		}
		Expect(finding.Message()).To(Equal("(*Registry).Load returns the interface io.Reader (result 2 of 3)"))
	})

	It("names the path when the interface is reached through a struct's field", func() {
		// CS-8's 2026-09-03 amendment: a struct that is returned and carries an
		// interface returns that interface, and "somewhere inside this struct"
		// is not an actionable diagnostic. The path is the whole difference.
		finding := ifacereturn.Finding{
			Declaration: "NewView",
			Position:    1,
			Arity:       1,
			Interface:   "subject.IStore",
			Path:        "View.Store",
		}
		Expect(finding.Message()).To(
			Equal("NewView returns View.Store, which is the interface subject.IStore"))
	})

	It("names the path and the result position together", func() {
		finding := ifacereturn.Finding{
			Declaration: "NewView",
			Position:    1,
			Arity:       2,
			Interface:   "subject.IStore",
			Path:        "View.Inner.Store",
		}
		Expect(finding.Message()).To(Equal(
			"NewView returns View.Inner.Store, which is the interface subject.IStore (result 1 of 2)"))
	})

	It("renders positions past nine, which the package's hand-rolled itoa reaches by a different path", func() {
		// itoa exists so this package imports the analysis framework and go/*
		// and nothing else. Its single-digit path is exercised by every other
		// specification here; its prepend loop is not, and a digit-reversing
		// bug would be invisible below ten.
		finding := ifacereturn.Finding{
			Declaration: "Everything",
			Position:    10,
			Arity:       12,
			Interface:   "io.Reader",
		}
		Expect(finding.Message()).To(Equal("Everything returns the interface io.Reader (result 10 of 12)"))
	})
})

// requireGoToolchain skips the specification when there is no `go` on PATH.
//
// analysistest type-checks the fixtures by shelling out to `go`, so those
// specifications need a toolchain. Under `go test` — which is how CI runs them,
// in the pinned golang container — there always is one. Under Bazel's sandbox
// there is not, and the skip is explicit rather than silent for the reason this
// repository states about every gate: a suite that quietly evaporates is worse
// than one that says what it did not do. The Finding.Message specifications run
// in both, so the Bazel target is never a completely vacuous pass.
func requireGoToolchain() {
	GinkgoHelper()
	if _, err := exec.LookPath("go"); err != nil {
		Skip("analysistest needs the go tool to type-check its fixtures; run this suite with `go test` (Bazel's sandbox has no toolchain)")
	}
}

// messages renders a diagnostic list as the plain strings the analyzer
// reported, in the order it reported them.
func messages(diagnostics []analysis.Diagnostic) []string {
	rendered := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		rendered = append(rendered, diagnostic.Message)
	}
	return rendered
}

// materializeFixtures copies testdata/src into a temporary directory, dropping
// the .txt guard, and returns that directory for use as analysistest's GOPATH.
func materializeFixtures() string {
	GinkgoHelper()
	root := GinkgoTB().TempDir()
	source := filepath.Join("testdata", "src")
	copied := 0
	walk := func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(root, "src", strings.TrimSuffix(relative, fixtureSuffix))
		if info.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		if !strings.HasSuffix(path, fixtureSuffix) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		copied++
		return os.WriteFile(destination, content, 0o644)
	}
	Expect(filepath.Walk(source, walk)).To(Succeed(), "materializing fixtures")
	// A harness that silently found no fixtures would report a passing
	// analyzer over an empty corpus, which is the one result this suite must
	// never be able to produce.
	Expect(copied).To(BeNumerically(">", 0), "no *%s fixtures under %s", fixtureSuffix, source)
	return root
}
