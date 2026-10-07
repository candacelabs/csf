// Copyright 2026 Candace Labs

package costs_test

import (
	"strings"
	"testing/fstest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/harness/costs"
)

// One recorded run: a bind, a turn two hours later (past the one-hour
// lifetime), the executor closed and reopened, and a turn five minutes after
// the reopen. The per-model totals are cumulative within an executor
// process: 1.0 $ after the first turn, 1.5 $ after the second, then 0.25 $
// in the reopened process.
var recordedRun = strings.Join([]string{
	`{"time":"2026-10-05T00:00:00Z","event_type":"harness_run_started"}`,
	`{"time":"2026-10-05T00:00:01Z","event_type":"harness_turn_requested"}`,
	`{"time":"2026-10-05T00:00:03Z","event_type":"assistant","event":{"session_id":"r1","parent_tool_use_id":null,"message":{"model":"m","usage":{"input_tokens":2,"cache_creation_input_tokens":20000,"cache_read_input_tokens":25000,"output_tokens":5}}}}`,
	`not a record`,
	`{"time":"2026-10-05T00:00:20Z","event_type":"assistant","event":{"session_id":"r1","parent_tool_use_id":"tool","message":{"model":"h","usage":{"input_tokens":9,"cache_creation_input_tokens":9,"cache_read_input_tokens":9,"output_tokens":9}}}}`,
	`{"time":"2026-10-05T00:00:30Z","event_type":"assistant","event":{"session_id":"r1","parent_tool_use_id":null,"message":{"model":"m","usage":{"input_tokens":1,"cache_creation_input_tokens":1000,"cache_read_input_tokens":45000,"output_tokens":7}}}}`,
	`{"time":"2026-10-05T00:01:00Z","event_type":"result","event":{"session_id":"r1","ttft_ms":2000,"usage":{"input_tokens":3,"cache_creation_input_tokens":21000,"cache_read_input_tokens":70000,"output_tokens":500,"cache_creation":{"ephemeral_1h_input_tokens":21000,"ephemeral_5m_input_tokens":0}},"modelUsage":{"m":{"inputTokens":3,"outputTokens":500,"cacheReadInputTokens":70000,"cacheCreationInputTokens":21000,"costUSD":1.0}}}}`,
	`{"time":"2026-10-05T02:01:00Z","event_type":"harness_turn_requested"}`,
	`{"time":"2026-10-05T02:01:04Z","event_type":"assistant","event":{"session_id":"r1","parent_tool_use_id":null,"message":{"model":"m","usage":{"input_tokens":1,"cache_creation_input_tokens":22000,"cache_read_input_tokens":25000,"output_tokens":4}}}}`,
	`{"time":"2026-10-05T02:02:00Z","event_type":"result","event":{"session_id":"r1","ttft_ms":4000,"usage":{"input_tokens":1,"cache_creation_input_tokens":22000,"cache_read_input_tokens":25000,"output_tokens":100,"cache_creation":{"ephemeral_1h_input_tokens":22000,"ephemeral_5m_input_tokens":0}},"modelUsage":{"m":{"inputTokens":4,"outputTokens":600,"cacheReadInputTokens":95000,"cacheCreationInputTokens":43000,"costUSD":1.5}}}}`,
	`{"time":"2026-10-05T02:10:00Z","event_type":"harness_session_closed"}`,
	`{"time":"2026-10-05T02:14:00Z","event_type":"harness_run_resumed"}`,
	`{"time":"2026-10-05T02:15:00Z","event_type":"harness_turn_requested"}`,
	`{"time":"2026-10-05T02:15:03Z","event_type":"assistant","event":{"session_id":"r1","parent_tool_use_id":null,"message":{"model":"m","usage":{"input_tokens":1,"cache_creation_input_tokens":1500,"cache_read_input_tokens":47000,"output_tokens":3}}}}`,
	`{"time":"2026-10-05T02:16:00Z","event_type":"result","event":{"session_id":"r1","usage":{"input_tokens":1,"cache_creation_input_tokens":1500,"cache_read_input_tokens":47000,"output_tokens":50,"cache_creation":{"ephemeral_1h_input_tokens":1500,"ephemeral_5m_input_tokens":0}},"modelUsage":{"m":{"inputTokens":1,"outputTokens":50,"cacheReadInputTokens":47000,"cacheCreationInputTokens":1500,"costUSD":0.25}}}}`,
}, "\n")

func recordedState() fstest.MapFS {
	return fstest.MapFS{
		"a1/run.json":     {Data: []byte(`{"agent_id":"slice","model":"m","branch":"dev/x"}`)},
		"a1/events.jsonl": {Data: []byte(recordedRun)},
	}
}

var _ = Describe("deriving cost events from the record", func() {
	var runs []costs.Run

	BeforeEach(func() {
		var err error
		runs, err = costs.ReadRuns(recordedState())
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(1))
	})

	It("reads the run's turns, its close and the lifetime its writes used", func() {
		run := runs[0]
		Expect(run.Agent).To(Equal("slice"))
		Expect(run.Kind).To(Equal("dev"))
		Expect(run.Turns).To(HaveLen(3))
		Expect(run.Closes).To(HaveLen(1))
		By("differencing the cumulative totals within one executor process, and not across the reopen")
		Expect([]float64{run.Turns[0].USD, run.Turns[1].USD, run.Turns[2].USD}).To(Equal([]float64{1.0, 0.5, 0.25}))
		Expect(run.Turns[1].Tokens).To(Equal(costs.Tokens{Uncached: 1, Write: 22000, Read: 25000, Output: 100}))
		By("taking the first and last top-level calls, never a subagent's")
		Expect(run.Turns[0].First).To(Equal(costs.Tokens{Uncached: 2, Write: 20000, Read: 25000, Output: 5}))
		Expect(run.Turns[0].Last).To(Equal(costs.Tokens{Uncached: 1, Write: 1000, Read: 45000, Output: 7}))
		Expect(run.Turns[2].Reopened).To(BeTrue())
		lifetime, written := costs.CacheLifetime(runs)
		Expect(lifetime).To(Equal(time.Hour))
		Expect(written).To(Equal(int64(44500)))
	})

	It("assigns every operation and says which fields are inferred", func() {
		events := costs.Derive(runs, time.Hour, map[string]costs.Price{"m": {Write: 1.0 / 1024}}, 250_000_000)
		type row struct {
			Operation harnessv1.HypervisorOperation
			Write     int64
			USD       float64
			GapMs     int64
			TTFTMs    int64
			Inferred  []harnessv1.CostField
			Missing   []harnessv1.CostField
		}
		rows := []row{}
		for _, event := range events {
			rows = append(rows, row{event.GetOperation(), event.GetTokens().GetCacheWrite(), event.GetUsd(), event.GetGapMs(), event.GetTtftMs(), event.GetInferred(), event.GetMissing()})
		}
		inferredRebuild := []harnessv1.CostField{harnessv1.CostField_COST_FIELD_TOKENS, harnessv1.CostField_COST_FIELD_USD}
		inferredClass := []harnessv1.CostField{harnessv1.CostField_COST_FIELD_OPERATION}
		ttft := []harnessv1.CostField{harnessv1.CostField_COST_FIELD_TTFT}
		Expect(rows).To(Equal([]row{
			{harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_BIND, 20000, 20000.0 / 1024, 0, 2000, inferredRebuild, nil},
			{harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_TURN_COLD, 21000, 1.0, 0, 2000, inferredClass, nil},
			{harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_TURN_COLD, 22000, 0.5, 7_200_000, 4000, inferredClass, nil},
			{harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_IDLE_CLOSE, 0, 0, 0, 0, []harnessv1.CostField{harnessv1.CostField_COST_FIELD_RESIDENT}, ttft},
			{harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_REOPEN, 1500, 1500.0 / 1024, 780_000, 0, inferredRebuild, ttft},
			{harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_TURN_WARM, 1500, 0.25, 780_000, 0, inferredClass, ttft},
		}))
		Expect(events[3].GetResidentBytes()).To(Equal(int64(250_000_000)))
		Expect(events[5].GetPrefixTokens()).To(Equal(int64(22000 + 25000 + 1 + 4)))
	})

	It("builds the same report from the same record", func() {
		at := time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC)
		first := costs.Build(runs, []int64{250_000_000}, nil, at)
		second := costs.Build(runs, []int64{250_000_000}, nil, at)
		Expect(proto.Equal(first, second)).To(BeTrue())
		Expect(first.GetOperations()).To(HaveKeyWithValue(harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_REOPEN.String(), int64(1)))
		Expect(first.GetSeries()).To(HaveLen(1))
	})
})

var _ = Describe("reading a state directory", func() {
	It("reads no runs from an empty directory and builds an empty report", func() {
		runs, err := costs.ReadRuns(fstest.MapFS{})
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(BeEmpty())
		report := costs.Build(runs, nil, nil, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))
		Expect(report.GetRuns()).To(BeZero())
		Expect(report.GetSeries()).To(BeEmpty())
	})

	It("counts a router attach as a rebind", func() {
		state := recordedState()
		state["routing/decisions.jsonl"] = &fstest.MapFile{Data: []byte(`{"v":"a1","r":"r1","arm":"attach","reason":"key"}` + "\n")}
		runs, err := costs.ReadRuns(state)
		Expect(err).NotTo(HaveOccurred())
		events := costs.Derive(runs, time.Hour, nil, 0)
		Expect(events[0].GetOperation()).To(Equal(harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_REBIND))
	})
})

var _ = Describe("sampling turn executor memory", func() {
	It("reads the resident memory of every process with the executor's name", func() {
		proc := fstest.MapFS{
			"12/comm":   {Data: []byte("claude\n")},
			"12/status": {Data: []byte("Name:\tclaude\nVmRSS:\t  2048 kB\n")},
			"13/comm":   {Data: []byte("bash\n")},
			"13/status": {Data: []byte("VmRSS:\t  9999 kB\n")},
			"14/comm":   {Data: []byte("claude\n")},
			"self/comm": {Data: []byte("claude\n")},
		}
		Expect(costs.SampleResident(proc, costs.ExecutorCommand)).To(Equal([]int64{2048 * 1024}))
	})
})
