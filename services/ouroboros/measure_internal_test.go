// Copyright 2026 Candace Labs

package ouroboros

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Event log lines in the turn executor's shape, one per kind the struggle
// measure reads, each stamped by the spec.
const (
	callLineFormat   = `{"time":"%s","event_type":"assistant","direction":"out","event":{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"%s"}}]}}}`
	resultLineFormat = `{"time":"%s","event_type":"user","direction":"out","event":{"type":"user","message":{"content":[{"type":"tool_result","content":"out","is_error":%t}]}}}`
	denyLineFormat   = `{"time":"%s","event_type":"session_gate_decision","gate":"wait","decision":"%s"}`
	deniedLineFormat = `{"time":"%s","event_type":"system","direction":"out","event":{"type":"system","subtype":"permission_denied","tool_name":"Bash"}}`
)

func logLines(day string, lines ...string) string {
	var out []string
	for index, line := range lines {
		at := fmt.Sprintf("%sT12:%02d:00Z", day, index)
		out = append(out, strings.Replace(line, "%s", at, 1))
	}
	return strings.Join(out, "\n") + "\n"
}

func call(command string) string  { return fmt.Sprintf(callLineFormat, "%s", command) }
func result(isError bool) string  { return fmt.Sprintf(resultLineFormat, "%s", isError) }
func gate(decision string) string { return fmt.Sprintf(denyLineFormat, "%s", decision) }
func denied() string              { return fmt.Sprintf(deniedLineFormat, "%s") }
func ptr(value float64) *float64  { return &value }

var _ = Describe("Reading struggles from an event log", func() {
	It("counts tool calls per day and merges nearby hits into one episode", func() {
		lines := logLines("2026-10-03",
			call("ls"), result(true), // tool_error: episode 1
			call("ls"), result(false), // retry of the same call, 2 events later: episode 1
			call("cat a"), result(false),
			call("cat b"), result(false),
			call("cat c"), result(false),
			call("cat d"), result(false),
			call("cat e"), result(false), // 10 events since the last hit
			gate("deny"), // permission_denial: episode 2
			gate("allow"),
			denied(), // 2 events after the gate denial: episode 2
		)
		reading := Struggles([]byte(lines), time.UTC)
		Expect(reading.Calls).To(Equal(map[string]int64{"2026-10-03": 7}))
		Expect(reading.Episodes).To(Equal(map[string]int64{"2026-10-03": 2}))
		Expect(reading.Interventions).To(BeEmpty())
	})

	It("dates a day in the measurement's zone", func() {
		zone, err := time.LoadLocation(ScheduleLocation)
		Expect(err).NotTo(HaveOccurred())
		lines := strings.Replace(call("ls"), "%s", "2026-10-04T03:00:00Z", 1) + "\n"
		reading := Struggles([]byte(lines), zone)
		Expect(reading.Calls).To(Equal(map[string]int64{"2026-10-03": 1}), "03:00 UTC is the evening before in Los Angeles")
	})

	It("counts every prompt after a run's first turn as an operator intervention", func() {
		lines := `{"time":"2026-10-03T12:00:00Z","event_type":"user","direction":"in","turn":1,"event":{"type":"user","message":{"content":"the brief"}}}` + "\n" +
			`{"time":"2026-10-03T13:00:00Z","event_type":"user","direction":"in","turn":2,"event":{"type":"user","message":{"content":"fix it"}}}` + "\n" +
			`{"time":"2026-10-03T14:00:00Z","event_type":"user","direction":"out","turn":2,"event":{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}}` + "\n"
		reading := Struggles([]byte(lines), time.UTC)
		Expect(reading.Interventions).To(Equal(map[string]int64{"2026-10-03": 1}))
	})

	It("reads an empty log as nothing", func() {
		reading := Struggles(nil, time.UTC)
		Expect(reading.Calls).To(BeEmpty())
		Expect(reading.Episodes).To(BeEmpty())
		Expect(reading.Interventions).To(BeEmpty())
	})
})

var _ = Describe("The struggle rate and its interval", func() {
	It("is the Garwood Poisson interval measure.py computes", func() {
		rate, low, high := PoissonRate(5, 1000)
		Expect(*rate).To(Equal(5.0))
		Expect(*low).To(BeNumerically("~", 1.6113, 0.0005))
		Expect(*high).To(BeNumerically("~", 11.6682, 0.0005))
		rate, low, high = PoissonRate(0, 400)
		Expect(*rate).To(Equal(0.0))
		Expect(*low).To(Equal(0.0))
		Expect(*high).To(BeNumerically("~", 9.1700, 0.0005))
	})

	It("is no rate without exposure", func() {
		rate, low, high := PoissonRate(1, 0)
		Expect(rate).To(BeNil())
		Expect(low).To(BeNil())
		Expect(high).To(BeNil())
	})
})

var _ = Describe("The compounding rate", func() {
	It("is the per-day factor of the weighted log-linear fit over active days", func() {
		trend := Compounding([]Rate{
			{Day: "2026-10-01", ToolCalls: 1000, Struggles: 5},
			{Day: "2026-10-02", ToolCalls: 1000, Struggles: 6},
			{Day: "2026-10-03", ToolCalls: 50, Struggles: 9}, // below activeCalls: not fitted
			{Day: "2026-10-04", ToolCalls: 1000, Struggles: 8},
		}, PerDay)
		Expect(trend.Weeks).To(Equal(3))
		Expect(*trend.Factor).To(BeNumerically("~", 1.1662, 0.0005))
		Expect(*trend.Low).To(BeNumerically("~", 0.8184, 0.0005))
		Expect(*trend.High).To(BeNumerically("~", 1.6618, 0.0005))
	})

	It("is the same fit per week over weekly rates: measure.py's trend", func() {
		trend := Compounding([]Rate{
			{Day: "2026-09-07", ToolCalls: 1000, Struggles: 5},
			{Day: "2026-09-14", ToolCalls: 1000, Struggles: 6},
			{Day: "2026-09-28", ToolCalls: 1000, Struggles: 8},
		}, PerWeek)
		Expect(trend.Weeks).To(Equal(3))
		Expect(*trend.Factor).To(BeNumerically("~", 1.1662, 0.0005))
	})

	It("is not estimable under three active periods", func() {
		trend := Compounding([]Rate{{Day: "2026-10-01", ToolCalls: 1000, Struggles: 5}, {Day: "2026-10-02", ToolCalls: 1000, Struggles: 6}}, PerDay)
		Expect(trend.Weeks).To(Equal(2))
		Expect(trend.Factor).To(BeNil())
	})
})

var _ = Describe("Weeks", func() {
	It("start on Monday and fold the days into one rate", func() {
		Expect(WeekOf("2026-10-04")).To(Equal("2026-09-28"), "a Sunday belongs to the Monday before")
		Expect(WeekOf("2026-10-05")).To(Equal("2026-10-05"), "a Monday is its own week")
		weeks := Weekly([]Rate{{Day: "2026-10-01", ToolCalls: 600, Struggles: 2}, {Day: "2026-10-03", ToolCalls: 400, Struggles: 3}, {Day: "2026-10-06", ToolCalls: 100, Struggles: 1}})
		Expect(weeks).To(HaveLen(2))
		Expect(weeks[0].Day).To(Equal("2026-09-28"))
		Expect(weeks[0].ToolCalls).To(Equal(int64(1000)))
		Expect(weeks[0].Struggles).To(Equal(int64(5)))
		Expect(*weeks[0].PerK).To(Equal(5.0))
		Expect(weeks[1].Day).To(Equal("2026-10-05"))
	})
})

var _ = Describe("The internal check's exponent", func() {
	It("is the least-squares slope of log(structure per intervention) on the week, with its interval", func() {
		exponent := StructureExponent([]StructureWeek{
			{Week: "2026-09-07", PerIntervention: ptr(0.10)},
			{Week: "2026-09-14", PerIntervention: ptr(0.12)},
			{Week: "2026-09-21", PerIntervention: ptr(0.15)},
			{Week: "2026-09-28", PerIntervention: ptr(0.17)},
		})
		Expect(exponent.Weeks).To(Equal(4))
		Expect(*exponent.Slope).To(BeNumerically("~", 0.1815, 0.0005))
		Expect(*exponent.Low).To(BeNumerically("~", 0.1554, 0.0005))
		Expect(*exponent.High).To(BeNumerically("~", 0.2077, 0.0005))
		Expect(exponent.Proxy).To(Equal(StructureProxy))
	})

	It("is not estimable under three weeks", func() {
		exponent := StructureExponent([]StructureWeek{{Week: "2026-09-28", PerIntervention: ptr(0.1)}})
		Expect(exponent.Weeks).To(Equal(1))
		Expect(exponent.Slope).To(BeNil())
	})
})
