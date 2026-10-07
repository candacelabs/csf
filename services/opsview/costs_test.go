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
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/harness/costs"
	"github.com/candacelabs/csf/services/opsview"
)

// costReport is a cost model of one day, as the costs verb writes it.
func costReport(day string, hFleet float64) []byte {
	GinkgoHelper()
	report := &harnessv1.CostReport{At: timestamppb.New(time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC)), Runs: 2, Days: 1,
		Series: []*harnessv1.CostDay{{Day: day, SessionHours: 10, HFleet: hFleet, SavingUsd: 1.5, UsdPerSessionHour: map[string]float64{
			harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_TURN_WARM.String():  0.25,
			harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_IDLE_CLOSE.String(): 0.125,
		}}},
		Policies: []*harnessv1.PolicyOutcome{
			{Decision: harnessv1.HypervisorDecision_HYPERVISOR_DECISION_KEEP_ALIVE, Policy: "never", Today: true, Chosen: true,
				UsdPerDay: 18.5, UsdPerDayInterval: &harnessv1.Interval{Low: 7.25, High: 32}},
			{Decision: harnessv1.HypervisorDecision_HYPERVISOR_DECISION_KEEP_ALIVE, Policy: "fixed", UsdPerDay: 17},
		},
		Parameters: []*harnessv1.HypervisorParameter{{Name: costs.ParameterCacheLifetime, Value: 3600, Unit: "s", Chosen: true}},
	}
	content, err := protojson.Marshal(report)
	Expect(err).NotTo(HaveOccurred())
	return content
}

func writeCostReport(directory string, content []byte) {
	GinkgoHelper()
	Expect(os.WriteFile(filepath.Join(directory, costs.ReportFile), content, 0o600)).To(Succeed())
}

func showsCostDay(day string) func(html string) bool {
	return func(html string) bool { return strings.Contains(html, `data-opsview-cost-day="`+day+`"`) }
}

var _ = Describe("The cost panel", func() {
	var (
		directory string
		watcher   *specWatcher
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		watcher = newSpecWatcher(gomock.NewController(GinkgoT()))
	})

	It("shows the series, the chosen policies and the parameters, and patches only itself when replaced", func() {
		writeCostReport(directory, costReport("2026-10-04", 0.9881))
		client := connect(mountView(directory, watcher.mock))
		client.Send(opsview.EventSection, opsview.CostsRegion, map[string]string{opsview.FieldSection: "costs"})
		panel := client.WaitFor(opsview.CostsRegion, opened(showsCostDay("2026-10-04")))
		client.Ack(panel.Patch.ServerSeq)
		html, _ := panel.Patch.Fragment(opsview.CostsRegion)
		Expect(html).To(ContainSubstring("2 runs over 1 days, built 2026-10-05 03:00:00"))
		Expect(html).To(ContainSubstring("<td>10</td><td>0.250</td><td>0.000</td><td>0.000</td><td>0.000</td><td>0.125</td><td>0.9881</td><td>1.50</td>"))
		Expect(html).To(ContainSubstring("<td>keep_alive</td><td>never</td><td>18.50 [7.25, 32.00]</td>"))
		Expect(html).NotTo(ContainSubstring("<td>fixed</td>"))
		Expect(html).To(ContainSubstring("<td>cache_lifetime_seconds</td><td>3600 s</td><td>yes</td>"))

		writeCostReport(directory, costReport("2026-10-05", 0.99))
		watcher.changes <- iofs.Change{Name: costs.ReportFile, Op: iofs.ChangeCreated}
		panel = client.WaitFor(opsview.CostsRegion, showsCostDay("2026-10-05"))
		Expect(panel.Patch.FragmentIDs()).To(Equal([]string{opsview.CostsRegion}), "the board did not re-render")
	})

	It("says so when no report is written, and ignores one that does not decode", func() {
		writeCostReport(directory, []byte("not a report"))
		client := connect(mountView(directory, watcher.mock))
		html, found := unfold(client, opsview.CostsRegion, "costs"), true
		Expect(found).To(BeTrue())
		Expect(html).To(ContainSubstring("No cost model yet"))
	})
})
