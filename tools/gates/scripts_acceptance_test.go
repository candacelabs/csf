//go:build acceptance

package gates_test

import (
	"encoding/json"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/ipc/proc"
)

const (
	alignmentSchema = "candace.ontology.alignment/v1"
	// A cold run builds three OCaml binaries and the toolchain they need.
	acceptanceTimeout = 45 * time.Minute
)

// The default mode end to end: it builds the pinned checkers in the Bazel
// container and prints one record for HEAD. It needs Docker, so it is the
// labelled acceptance suite: go test -tags acceptance ./tools/gates.
var _ = Describe("ontology-score.sh in the default score mode", func() {
	It("prints one alignment record for HEAD", func(ctx SpecContext) {
		result := runScript(ctx, ontologyScore)
		Expect(result.ExitCode).To(Equal(0), string(result.Stderr))
		var record struct {
			Schema   string `json:"schema"`
			Revision string `json:"revision"`
		}
		Expect(json.Unmarshal(result.Stdout, &record)).To(Succeed())
		Expect(record.Schema).To(Equal(alignmentSchema))
		Expect(record.Revision).NotTo(BeEmpty())
	}, SpecTimeout(acceptanceTimeout))
})

// The gate escape the generated check closes: at this revision main carried
// csf/docs/generated/architecture.csf and grammar.ebnf, copies no generator
// wrote, and the first had drifted from the ontology source unseen.
const orphanRevision = "fbf8d6c"

var _ = Describe("check-generated.sh", func() {
	It("fails on the revision that carried orphan copies of the ontology", func(ctx SpecContext) {
		launcher, err := proc.NewHostLauncher()
		Expect(err).NotTo(HaveOccurred())
		tree := filepath.Join(GinkgoT().TempDir(), "orphans")
		_, err = launcher.Run(ctx, proc.Command{Executable: "git", Directory: repositoryRoot(),
			Arguments: []string{"worktree", "add", "--detach", tree, orphanRevision}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) {
			_, err := launcher.Run(ctx, proc.Command{Executable: "git", Directory: repositoryRoot(),
				Arguments: []string{"worktree", "remove", "--force", tree}})
			Expect(err).NotTo(HaveOccurred())
		})
		result := runScript(ctx, "tools/check-generated.sh", "--root", tree)
		Expect(result.ExitCode).To(Equal(1), string(result.Stderr))
		Expect(string(result.Stdout)).To(And(
			ContainSubstring("orphan: no generator writes csf/docs/generated/architecture.csf"),
			ContainSubstring("orphan: no generator writes csf/docs/generated/grammar.ebnf"),
			ContainSubstring("declares ontology terms outside the source")))
	}, SpecTimeout(acceptanceTimeout))
})
