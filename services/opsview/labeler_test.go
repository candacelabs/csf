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
	"github.com/candacelabs/csf/services/opsview"
	"github.com/candacelabs/csf/services/ouroboros/labeler"
)

// The labeler panel over the run record the labeler writes, in the proto
// JSON form the label verb emits (field names as records.proto spells them).
const (
	labelingRun = `{"model":"ollama/qwen3:8b","phase":"labeling","current":"105","tickets":"3","tickets_with_instance":"2","proposed":"12","accepted":"5","rejected":"2","batches":"6","labels_per_hour":48.5,"precision":0.714,"held_out":"7","gpu_utilization":41,"gpu_memory_bytes":"6926614528","keep_alive_seconds":"40","batch_gap_seconds":"20","load_seconds":"4","yields":"1","started":"1791504000","updated":"1791504900"}`
	finishedRun = `{"model":"ollama/qwen3:8b","phase":"finished","tickets":"85","tickets_with_instance":"40","proposed":"300","accepted":"120","rejected":"30","batches":"170","labels_per_hour":300,"precision":0.8,"held_out":"150","gpu_utilization":-1,"yields":"0","started":"1791504000","updated":"1791507600"}`

	ticketsMark = `data-opsview="labeler-tickets">`
)

func writeLabelerRun(directory string, content string) {
	GinkgoHelper()
	Expect(os.WriteFile(filepath.Join(directory, labeler.RunFile), []byte(content), 0o600)).To(Succeed())
}

func showsTickets(text string) func(html string) bool {
	return func(html string) bool { return strings.Contains(html, ticketsMark+text+"<") }
}

var _ = Describe("The labeler panel", func() {
	var (
		directory string
		watcher   *specWatcher
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		watcher = newSpecWatcher(gomock.NewController(GinkgoT()))
	})

	It("shows the run record and patches only the panel when it is replaced", func() {
		writeLabelerRun(directory, labelingRun)
		client := connect(mountView(directory, watcher.mock))
		client.Send(opsview.EventSection, opsview.LabelerRegion, map[string]string{opsview.FieldSection: "labeler"})
		panel := client.WaitFor(opsview.LabelerRegion, opened(showsTickets("3 (2 with a real instance)")))
		client.Ack(panel.Patch.ServerSeq)
		html, _ := panel.Patch.Fragment(opsview.LabelerRegion)
		Expect(html).To(ContainSubstring(`class="labeler labeling"`))
		Expect(html).To(ContainSubstring("ollama/qwen3:8b"))
		Expect(html).To(ContainSubstring(`data-opsview="labeler-current">#105<`))
		Expect(html).To(ContainSubstring("12 proposed, 5 accepted, 2 rejected"))
		Expect(html).To(ContainSubstring("71% of 7 positives"))
		Expect(html).To(ContainSubstring("48.5 labels/h"))
		Expect(html).To(ContainSubstring("41% (6.9 GB)"))
		Expect(html).To(ContainSubstring("40 s (gap 20 s, load 4 s)"))

		writeLabelerRun(directory, finishedRun)
		watcher.changes <- iofs.Change{Name: labeler.RunFile, Op: iofs.ChangeCreated}
		panel = client.WaitFor(opsview.LabelerRegion, showsTickets("85 (40 with a real instance)"))
		Expect(panel.Patch.FragmentIDs()).To(Equal([]string{opsview.LabelerRegion}), "the board did not re-render")
		html, _ = panel.Patch.Fragment(opsview.LabelerRegion)
		Expect(html).To(ContainSubstring(`class="labeler finished"`))
		Expect(html).To(ContainSubstring(`data-opsview="labeler-gpu">unmeasured<`))
		Expect(html).To(ContainSubstring(`data-opsview="labeler-current">none<`))
	})

	It("says so when no run is recorded", func() {
		client := connect(mountView(directory, watcher.mock))
		html, found := unfold(client, opsview.LabelerRegion, "labeler"), true
		Expect(found).To(BeTrue())
		Expect(html).To(ContainSubstring("No labeler run recorded"))
	})
})
