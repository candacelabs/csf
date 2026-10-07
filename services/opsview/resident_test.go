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
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/opsview"
)

// Two samples as the harness writes them a minute apart: the second after a
// suspend took one executor down and a resume brought it back.
const (
	firstSample  = `{"at":"2026-10-04T12:00:00Z","harness_rss_bytes":58720256,"executors_rss_bytes":1258291200,"open_sessions":5,"executors_alive":4,"resumes":0,"resume_ttft_ms":0,"idle_bound_s":661,"idle_bound_quantile":0.829,"gaps":486}` + "\n"
	secondSample = `{"at":"2026-10-04T12:01:00Z","harness_rss_bytes":58720256,"executors_rss_bytes":1006632960,"open_sessions":5,"executors_alive":3,"resumes":1,"resume_ttft_ms":3497,"idle_bound_s":661,"idle_bound_quantile":0.829,"gaps":486}` + "\n"
	aliveMark    = `data-opsview="executors-alive">`
)

func showsAlive(count string) func(html string) bool {
	return func(html string) bool { return strings.Contains(html, aliveMark+count+"<") }
}

var _ = Describe("Following the resident series", func() {
	var (
		directory string
		watcher   *specWatcher
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		writeSession(directory, firstAssignment, strings.ReplaceAll(startedLine, "%s", firstAssignment), requestedLine, oneToolLine)
		watcher = newSpecWatcher(gomock.NewController(GinkgoT()))
	})

	It("shows an empty panel until a sample exists", func() {
		client := connect(mountView(directory, watcher.mock))
		board := client.WaitFor(opsview.BoardRegion, showsToolCalls("1"))
		client.Ack(board.Patch.ServerSeq)
		html, present := client.Snapshot().Patch.Fragment(opsview.ResidentRegion)
		Expect(present).To(BeTrue(), "the panel's region is on the page from the first paint")
		Expect(html).NotTo(ContainSubstring("<article"))
		Expect(board.Patch.FragmentIDs()).NotTo(ContainElement(opsview.ResidentRegion), "no series means no panel patch")
	})

	It("shows the latest sample and patches only the panel when the harness rewrites the file", func() {
		Expect(os.WriteFile(filepath.Join(directory, harness.ResidentFile), []byte(firstSample), 0o600)).To(Succeed())
		client := connect(mountView(directory, watcher.mock))
		panel := client.WaitFor(opsview.ResidentRegion, showsAlive("4"))
		client.Ack(panel.Patch.ServerSeq)
		html, _ := panel.Patch.Fragment(opsview.ResidentRegion)
		Expect(html).To(ContainSubstring(`data-opsview="executors-rss">harness 56 MB · executors 1200 MB<`))
		Expect(html).To(ContainSubstring(`data-opsview="open-sessions">5<`))
		Expect(html).To(ContainSubstring(`data-opsview="idle-bound">11m1s (q0.83 of 486 gaps)<`))
		Expect(html).To(ContainSubstring(`data-opsview="resumes">0<`))

		Expect(os.WriteFile(filepath.Join(directory, harness.ResidentFile), []byte(firstSample+secondSample), 0o600)).To(Succeed())
		watcher.changes <- iofs.Change{Name: harness.ResidentFile, Op: iofs.ChangeWritten}
		panel = client.WaitFor(opsview.ResidentRegion, showsAlive("3"))
		Expect(panel.Patch.FragmentIDs()).To(Equal([]string{opsview.ResidentRegion}), "the board did not re-render")
		client.Ack(panel.Patch.ServerSeq)
		html, _ = panel.Patch.Fragment(opsview.ResidentRegion)
		Expect(html).To(ContainSubstring(`data-opsview="resumes">1<`))
		Expect(html).To(ContainSubstring(`data-opsview="resume-ttft">3.5s<`))
		Expect(strings.Count(html, `<li><span class="time">`)).To(Equal(2), "both samples are in the series")
	})
})
