package gates_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	houseLintRatchet = "tools/house-lint-ratchet.sh"
	// A house lint summary as the runner writes it: mandatory rules with
	// native counts and a specialist, a per-owner rule, and advisory rules.
	ratchetSummary = `# House lint

| Rule | Policy | Native findings / specialist result | Coverage |
|---|---|---|---|
| CS-1 | mandatory | 0 | I-prefixed interfaces |
| CS-2 | mandatory | 6 | Named input parameters |
| CS-3 | mandatory | reuse: findings | Duplicate primitives (dupl) |
| CS-5 | advisory | 115 | Mutex declarations |
| CS-16 | advisory; mandatory in services/copilot-adapter/, pkg/clean/ | 43 | Crossings |
`
	ratchetNative = `services/copilot-adapter/store.go:12: CS-16: net.Dial outside ipc/
services/copilot-adapter/client_test.go:40: CS-16: net.Listen outside ipc/
services/deploy/fleet.go:8: CS-16: net.Dial outside ipc/
pkg/clean/clean.go:3: CS-16: os/exec import outside ipc/proc
pkg/cleanly/x.go:3: CS-17: os.Getenv outside the config capability
`
)

// The counts the ratchet compares: only findings that block, rule by rule.
// The full base-versus-head run needs the checker build and is proven on the
// pull request itself.
var _ = Describe("house-lint-ratchet.sh --count", func() {
	write := func(name string, content string) string {
		path := filepath.Join(GinkgoT().TempDir(), name)
		Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
		return path
	}

	It("counts every mandatory finding and only a per-owner rule's findings inside its owners", func(ctx SpecContext) {
		result := runScript(ctx, houseLintRatchet, "--count", write("summary.md", ratchetSummary), write("native.log", ratchetNative))
		Expect(result.ExitCode).To(Equal(0), string(result.Stderr))
		Expect(string(result.Stdout)).To(Equal("CS-1 0\nCS-2 6\nCS-3 1\nCS-16 3\n"))
	})

	It("refuses a report whose mandatory specialist did not complete", func(ctx SpecContext) {
		summary := write("summary.md", "| CS-3 | mandatory | reuse: SCAN ERROR | Duplicate primitives (dupl) |\n")
		result := runScript(ctx, houseLintRatchet, "--count", summary, write("native.log", ""))
		Expect(result.ExitCode).To(Equal(usageExit))
		Expect(string(result.Stderr)).To(ContainSubstring("CS-3 did not run"))
	})
})
