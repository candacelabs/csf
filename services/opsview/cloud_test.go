// Copyright 2026 Candace Labs

package opsview_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/net/hfjobs"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
	"github.com/candacelabs/csf/services/cloud"
	"github.com/candacelabs/csf/services/opsview"
)

// The Cloud panel over the record the cloud service writes: one running job,
// one the operator stopped.
const (
	runningCloudJob = "dryrun0002"
	stoppedCloudJob = "dryrun0001"
)

func writeCloudLedger(directory string) {
	GinkgoHelper()
	at := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	finished := at.Add(-time.Minute)
	ledger := cloud.Ledger{
		DailyCapUSD: 100, UpdatedAt: at.Add(5 * time.Minute),
		Jobs: []cloud.CloudJob{
			{ID: stoppedCloudJob, Provider: cloud.ProviderDryRun, Flavor: "cpu-basic", Purpose: "first probe", CreatedAt: at.Add(-10 * time.Minute), FinishedAt: &finished,
				Stage: hfjobs.StageCanceled, CapUSD: 0.01, SpendUSD: 0.0015, StopReason: cloud.StopOperator, PullRequests: -1},
			{ID: runningCloudJob, Provider: "huggingface", Flavor: "cpu-xl", Purpose: "burst-1: S1, S2", URL: "https://hub.example/jobs/operator/" + runningCloudJob,
				CreatedAt: at, Stage: hfjobs.StageRunning, CapUSD: 1, SpendUSD: 0.0833, PullRequests: -1},
		},
		Notices: []cloud.Notice{{At: at, Kind: cloud.NoticeStart, JobID: runningCloudJob, Text: runningCloudJob + " started"}},
	}
	content, err := json.Marshal(ledger)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(directory, cloud.LedgerFile), content, 0o600)).To(Succeed())
}

// cloudTotals is the folded panel's one line for the record above.
const cloudTotals = "Cloud: $0.0848 today of $100 cap, 1 running"

// openCloud waits for the record's totals on the folded panel, then unfolds
// it the way a viewer does and returns its markup.
func openCloud(client *livetest.Client) string {
	GinkgoHelper()
	client.WaitFor(opsview.CloudRegion, contains(cloudTotals))
	return unfold(client, opsview.CloudRegion, "cloud")
}

var _ = Describe("The Cloud panel", func() {
	var (
		directory string
		watcher   *specWatcher
		stopped   chan string
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		watcher = newSpecWatcher(gomock.NewController(GinkgoT()))
		stopped = make(chan string, 1)
		writeCloudLedger(directory)
	})

	stop := func(_ context.Context, input cloud.StopCloudJobInput) (cloud.CloudJob, error) {
		stopped <- input.ID
		return cloud.CloudJob{ID: input.ID, Stage: hfjobs.StageCanceled}, nil
	}

	It("shows the day's spend against its cap, the jobs running, the constraint, and a Stop on each running job only", func() {
		client := connect(mountView(directory, watcher.mock, opsview.WithCloudStop(stop)))
		html := openCloud(client)
		Expect(html).To(ContainSubstring(`data-opsview="cloud-spend-today">$0.0848<small>of $100 cap`), "both jobs were created today")
		Expect(html).To(ContainSubstring("The usage limit follows the model credential, not the machine"))
		Expect(html).To(ContainSubstring(`href="https://hub.example/jobs/operator/` + runningCloudJob + `"`))
		Expect(html).To(ContainSubstring("$0.0833 of $1"))
		Expect(html).To(ContainSubstring("5m0s"), "elapsed to the record's update")
		Expect(html).To(ContainSubstring(`data-verified="` + runningCloudJob + `"`))
		Expect(html).NotTo(ContainSubstring(`data-verified="` + stoppedCloudJob + `"`))
		Expect(html).To(ContainSubstring("CANCELED · operator"))
		Expect(strings.Index(html, runningCloudJob)).To(BeNumerically("<", strings.Index(html, stoppedCloudJob)), "running jobs first")
	})

	It("stops a running job through the service's Stop and shows what the provider's list says", func() {
		client := connect(mountView(directory, watcher.mock, opsview.WithCloudStop(stop)))
		openCloud(client)
		client.Send(opsview.EventCloudStop, opsview.CloudRegion, map[string]string{opsview.FieldJob: runningCloudJob})
		Expect(<-stopped).To(Equal(runningCloudJob))
		client.WaitFor(opsview.CloudRegion, func(html string) bool {
			return strings.Contains(html, "Stopped "+runningCloudJob+": the provider&#39;s job list shows it CANCELED.")
		})
	})

	It("refuses a Stop of a job that is not running, without calling the service", func() {
		client := connect(mountView(directory, watcher.mock, opsview.WithCloudStop(stop)))
		openCloud(client)
		client.Send(opsview.EventCloudStop, opsview.CloudRegion, map[string]string{opsview.FieldJob: stoppedCloudJob})
		client.WaitFor(opsview.CloudRegion, func(html string) bool {
			return strings.Contains(html, "Stop refused: "+stoppedCloudJob+" is CANCELED already")
		})
		Expect(stopped).To(BeEmpty())
	})

	It("draws Stop disabled with the reason on a page without the cloud service", func() {
		client := connect(mountView(directory, watcher.mock))
		html := openCloud(client)
		Expect(html).To(ContainSubstring("disabled"))
		Expect(html).To(ContainSubstring("read-only: the page was mounted without the cloud service"))
	})
})
