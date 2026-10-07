// Copyright 2026 Candace Labs

package opsview_test

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/net/model/jev"
	"github.com/candacelabs/csf/services/opsview"
)

// jevLedger is a decision record as `csf decide --ledger` writes it: the runs
// appended in order, the record stamped with the newest run's time.
func jevLedger(runs ...jev.DecisionRun) []byte {
	GinkgoHelper()
	ledger := jev.DecideLedger{}
	for _, run := range runs {
		ledger = ledger.Record(run)
	}
	content, err := ledger.Encode()
	Expect(err).NotTo(HaveOccurred())
	return content
}

func writeJevLedger(directory string, content []byte) {
	GinkgoHelper()
	Expect(os.WriteFile(filepath.Join(directory, jev.DecideLedgerFile), content, 0o600)).To(Succeed())
}

func showsJevTokens(tokens string) func(html string) bool {
	return func(html string) bool { return strings.Contains(html, `data-opsview="jev-tokens">`+tokens+`<`) }
}

var _ = Describe("The local decision model panel", func() {
	var (
		directory string
		watcher   *specWatcher
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		watcher = newSpecWatcher(gomock.NewController(GinkgoT()))
	})

	It("shows the newest day's tokens and decisions, and patches only itself when the record is replaced", func() {
		writeJevLedger(directory, jevLedger(
			jev.DecisionRun{At: time.Date(2026, 10, 5, 23, 0, 0, 0, time.UTC), Model: "jevk5_9b_q4", Decisions: 4, PromptTokens: 100, EvalTokens: 10},
			jev.DecisionRun{At: time.Date(2026, 10, 6, 1, 0, 0, 0, time.UTC), Model: "jevk5_9b_q4", Decisions: 3, PromptTokens: 700, EvalTokens: 115},
		))
		client := connect(mountView(directory, watcher.mock))
		client.Send(opsview.EventSection, opsview.JevRegion, map[string]string{opsview.FieldSection: "jev"})
		panel := client.WaitFor(opsview.JevRegion, opened(showsJevTokens("815")))
		client.Ack(panel.Patch.ServerSeq)
		html, _ := panel.Patch.Fragment(opsview.JevRegion)
		Expect(html).To(ContainSubstring(`class="jev"`))
		Expect(html).To(ContainSubstring(`<span class="status">jevk5_9b_q4</span>`))
		Expect(html).To(ContainSubstring("<dd>2026-10-06</dd>"))
		Expect(html).To(ContainSubstring(`data-opsview="jev-tokens">815<`), "the newest day's tokens, not the earlier day's")
		Expect(html).To(ContainSubstring(`data-opsview="jev-decisions">3<`))

		writeJevLedger(directory, jevLedger(jev.DecisionRun{At: time.Date(2026, 10, 6, 2, 0, 0, 0, time.UTC), Model: "jevk5_9b_q4", Decisions: 1, PromptTokens: 9, EvalTokens: 1}))
		watcher.changes <- iofs.Change{Name: jev.DecideLedgerFile, Op: iofs.ChangeCreated}
		panel = client.WaitFor(opsview.JevRegion, showsJevTokens("10"))
		Expect(panel.Patch.FragmentIDs()).To(Equal([]string{opsview.JevRegion}), "the board did not re-render")
	})

	It("says so when no record is written, and ignores one that does not decode", func() {
		writeJevLedger(directory, []byte("not a record"))
		client := connect(mountView(directory, watcher.mock))
		html := unfold(client, opsview.JevRegion, "jev")
		Expect(html).To(ContainSubstring("No decision run recorded"))
	})
})
