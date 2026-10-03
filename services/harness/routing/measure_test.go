// Copyright 2026 Candace Labs

package routing_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/candacelabs/csf/services/harness/routing"
	"github.com/candacelabs/csf/services/harness/session"
)

// result is one Claude Code result event as the session runner logs it.
func result(real string, read int64, creation int64, input int64) string {
	return fmt.Sprintf(`{"level":"INFO","event_type":"result","event":{"type":"result","session_id":%q,"usage":{"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"input_tokens":%d}}}`,
		real, read, creation, input)
}

func writeRun(state string, virtual string, lines ...string) {
	directory := filepath.Join(state, virtual)
	Expect(os.MkdirAll(directory, 0o700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(directory, "events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600)).To(Succeed())
}

var _ = Describe("Measure", func() {
	var state string

	BeforeEach(func() {
		state = GinkgoT().TempDir()
		// v1 started cold and ran two turns; v2 forked a warm prefix.
		writeRun(state, "v1",
			`{"level":"INFO","event_type":"harness_run_started"}`,
			result("r1", 0, 1000, 10),
			result("r1", 1000, 100, 10))
		writeRun(state, "v2", result("r2", 900, 50, 10))
	})

	It("reports the fleet hit ratio, each (v, r) row and the largest offenders", func() {
		report, err := routing.Measure(state)
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Virtual).To(Equal(2))
		Expect(report.Real).To(Equal(2))
		Expect(report.Turns).To(Equal(3))
		Expect(report.Usage).To(Equal(routing.Usage{Read: 1900, Creation: 1150, Input: 30}))
		Expect(report.HitRatio).To(BeNumerically("~", 1900.0/3080, 1e-12))
		Expect(report.FirstTurnCreationShare).To(BeNumerically("~", 1050.0/1150, 1e-12))
		Expect(report.Offenders).To(HaveLen(2))
		Expect(report.Offenders[0].Virtual).To(Equal("v1"))
		Expect(report.Offenders[0].Turns).To(Equal(2))
		Expect(report.Offenders[0].FirstTurn).To(Equal(routing.Usage{Creation: 1000, Input: 10}))
		Expect(report.Offenders[1].HitRatio).To(BeNumerically("~", 900.0/960, 1e-12))
	})

	It("labels each row with the run's agent and model, counts decisions, and exports no path", func() {
		Expect(session.WriteRunState(filepath.Join(state, "v1"), &session.RunState{
			AssignmentID: "v1", AgentID: "miner-wait-gate", Model: "claude-haiku-4-5-20251001",
			Worktree: filepath.Join(state, "v1", "worktree"),
		})).To(Succeed())
		router, err := routing.NewRouter(routing.WithStateDirectory(state))
		Expect(err).NotTo(HaveOccurred())
		_, err = router.Route(context.Background(), session.RouteRequest{Virtual: "v3", Session: "conversation-v3", Model: "m", Key: "k", Summary: "s"})
		Expect(err).NotTo(HaveOccurred())

		registry := prometheus.NewRegistry()
		registry.MustRegister(routing.NewCollector(state))
		families, err := registry.Gather()
		Expect(err).NotTo(HaveOccurred())
		decisions := map[string]float64{}
		labeled := false
		for _, family := range families {
			for _, metric := range family.GetMetric() {
				labels := map[string]string{}
				for _, label := range metric.GetLabel() {
					Expect(label.GetValue()).NotTo(ContainSubstring("/"))
					labels[label.GetName()] = label.GetValue()
				}
				if family.GetName() == "csf_harness_routing_decisions" {
					decisions[labels["arm"]+"/"+labels["reason"]] = metric.GetCounter().GetValue()
				}
				if labels["assignment"] == "v1" && family.GetName() == "csf_harness_cache_hit_ratio" {
					labeled = true
					Expect(labels).To(HaveKeyWithValue("agent", "miner-wait-gate"))
					Expect(labels).To(HaveKeyWithValue("model", "claude-haiku-4-5-20251001"))
					Expect(labels).To(HaveKeyWithValue("real_session", "r1"))
				}
			}
		}
		Expect(labeled).To(BeTrue())
		Expect(decisions).To(Equal(map[string]float64{"new/no-fit": 1}))
	})

	It("exports the fleet hit ratio as a gauge measured at the scrape", func() {
		registry := prometheus.NewRegistry()
		registry.MustRegister(routing.NewCollector(state))
		families, err := registry.Gather()
		Expect(err).NotTo(HaveOccurred())
		fleet := map[string]float64{}
		for _, family := range families {
			for _, metric := range family.GetMetric() {
				for _, label := range metric.GetLabel() {
					if label.GetName() == "scope" && label.GetValue() == "fleet" {
						fleet[family.GetName()] = metric.GetGauge().GetValue()
					}
				}
			}
		}
		Expect(fleet).To(HaveKeyWithValue("csf_harness_cache_hit_ratio", BeNumerically("~", 1900.0/3080, 1e-12)))
		Expect(fleet).To(HaveKeyWithValue("csf_harness_cache_creation_tokens", 1150.0))
	})
})
