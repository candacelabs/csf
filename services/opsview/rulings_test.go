// Copyright 2026 Candace Labs

package opsview_test

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/opsview"
)

// Two ruling records as the harness writes them: one a gate enforces, one
// whose gate is still pending.
const (
	gatedRuling   = `{"ruling_id":"opus-only","statement":"Every real session runs claude-opus-5-5.","excludes":["fable"],"quote":"DON'T USE FABLE JUST USE OPUS","ruled_on":"2026-10-05","scope":"every real session","enforced_by":"allowed-models refusal","recorded_at":"2026-10-05T08:00:00Z"}` + "\n"
	pendingRuling = `{"ruling_id":"no-native-tools","statement":"No executor-native tools.","excludes":null,"quote":"dont ever use your native tools again","ruled_on":"2026-10-05","pending_gate":"native-tool refusal","recorded_at":"2026-10-05T08:01:00Z"}` + "\n"
	coverageMark  = `data-opsview="rulings-coverage">`
)

func showsCoverage(coverage string) func(html string) bool {
	return func(html string) bool { return strings.Contains(html, coverageMark+coverage+"<") }
}

var _ = Describe("Following the ruling records", func() {
	var (
		directory string
		watcher   *specWatcher
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		writeSession(directory, firstAssignment, strings.ReplaceAll(startedLine, "%s", firstAssignment), requestedLine, oneToolLine)
		watcher = newSpecWatcher(gomock.NewController(GinkgoT()))
	})

	It("shows an empty panel until a ruling is recorded", func() {
		client := connect(mountView(directory, watcher.mock))
		board := client.WaitFor(opsview.BoardRegion, showsToolCalls("1"))
		client.Ack(board.Patch.ServerSeq)
		html, present := client.Snapshot().Patch.Fragment(opsview.RulingsRegion)
		Expect(present).To(BeTrue(), "the panel's region is on the page from the first paint")
		Expect(html).NotTo(ContainSubstring("<article"))
	})

	It("lists each ruling with its gate, flags the unenforced one, and patches only the panel when a ruling is recorded", func() {
		Expect(os.WriteFile(filepath.Join(directory, session.RulingsFile), []byte(gatedRuling), 0o600)).To(Succeed())
		client := connect(mountView(directory, watcher.mock))
		panel := client.WaitFor(opsview.RulingsRegion, showsCoverage("1 of 1 enforced · 0 UNENFORCED"))
		client.Ack(panel.Patch.ServerSeq)
		html, _ := panel.Patch.Fragment(opsview.RulingsRegion)
		Expect(html).To(ContainSubstring(`<li class="enforced" data-opsview-ruling="opus-only">`))
		Expect(html).To(ContainSubstring(`data-opsview="ruling-gate">enforced by allowed-models refusal<`))
		Expect(html).To(ContainSubstring(`<blockquote>DON&#39;T USE FABLE JUST USE OPUS</blockquote>`))

		Expect(os.WriteFile(filepath.Join(directory, session.RulingsFile), []byte(gatedRuling+pendingRuling), 0o600)).To(Succeed())
		watcher.changes <- iofs.Change{Name: session.RulingsFile, Op: iofs.ChangeWritten}
		panel = client.WaitFor(opsview.RulingsRegion, showsCoverage("1 of 2 enforced · 1 UNENFORCED"))
		Expect(panel.Patch.FragmentIDs()).To(Equal([]string{opsview.RulingsRegion}), "the board did not re-render")
		html, _ = panel.Patch.Fragment(opsview.RulingsRegion)
		Expect(html).To(ContainSubstring(`<li class="unenforced" data-opsview-ruling="no-native-tools">`))
		Expect(html).To(ContainSubstring(`data-opsview="ruling-gate">UNENFORCED (pending native-tool refusal)<`))
	})
})
