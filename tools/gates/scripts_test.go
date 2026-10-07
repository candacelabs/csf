package gates_test

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/ipc/proc"
)

const (
	bashExecutable   = "bash"
	ontologyScore    = "tools/ontology-score.sh"
	scoreModeRefusal = "ontology-score: --base and --accept-regression need --ratchet or --pr-spec"
	usageExit        = 2
)

// runScript starts one of the gate scripts through the process capability and
// returns its result; a non-zero exit is a result, not a failure.
func runScript(ctx context.Context, script string, arguments ...string) proc.Result {
	GinkgoHelper()
	launcher, err := proc.NewHostLauncher()
	Expect(err).NotTo(HaveOccurred())
	result, err := launcher.Run(ctx, proc.Command{
		Executable: bashExecutable,
		Arguments:  append([]string{filepath.Join(repositoryRoot(), script)}, arguments...),
		Directory:  repositoryRoot(),
	})
	if err != nil {
		var exitError *proc.ExitError
		Expect(err).To(BeAssignableToTypeOf(exitError))
	}
	return result
}

// The default score mode validates its arguments before it builds anything,
// so these specs reach that check without Docker or Bazel. The empty
// accept-regression array once failed it under `set -u` ("accept: unbound
// variable"); the full default-mode run is the acceptance spec beside this.
var _ = Describe("ontology-score.sh argument checks in the default score mode", func() {
	DescribeTable("refuses ratchet-only options with its own message",
		func(ctx SpecContext, arguments []string) {
			result := runScript(ctx, ontologyScore, arguments...)
			Expect(result.ExitCode).To(Equal(usageExit))
			Expect(string(result.Stderr)).To(ContainSubstring(scoreModeRefusal))
			Expect(string(result.Stderr)).NotTo(ContainSubstring("unbound variable"))
		},
		Entry("a base revision", []string{"--base", "origin/main"}),
		Entry("an accepted regression", []string{"--accept-regression"}),
	)
})
