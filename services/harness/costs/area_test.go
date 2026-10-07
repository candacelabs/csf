// Copyright 2026 Candace Labs

package costs_test

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/costs"
)

// Two runs of one model. A's real session reads pkg/x.go and goes idle at
// 00:10, its run open until open; B binds at 00:20, names the file and the
// harness, and reads the same file. A read carries 2^20 tokens, so one call
// over A's context reads 1 $ and the avoided read would have written 19 $.
func areaRuns(open time.Time, agentA string, agentB string) []costs.Run {
	at := func(minutes int) time.Time { return time.Date(2026, 10, 5, 0, minutes, 0, 0, time.UTC) }
	a := costs.Run{Virtual: "a", Agent: agentA, Model: model, Worktree: "/w/a", Title: "cost model for sessions",
		Open:  []costs.Span{{From: at(0), To: open}},
		Turns: []costs.Turn{{Real: "ra", Model: model, Start: at(0), End: at(10), Last: costs.Tokens{Read: prefix}}},
		Area:  costs.RunArea{Calls: 4, Accesses: []costs.Access{{At: at(1), Real: "ra", Path: "pkg/x.go", Kind: costs.AccessRead, Bytes: prefix}}}}
	b := costs.Run{Virtual: "b", Agent: agentB, Model: model, Worktree: "/w/b", Title: "cost model for sessions",
		Open:  []costs.Span{{From: at(20), To: at(40)}},
		Turns: []costs.Turn{{Real: "rb", Model: model, Start: at(20), End: at(30), Last: costs.Tokens{Read: 10}}},
		Area: costs.RunArea{Calls: 1,
			Prompts:  []costs.Prompt{{At: at(20), Text: "Fix /w/b/pkg/x.go in the agent harness."}},
			Accesses: []costs.Access{{At: at(21), Real: "rb", Path: "pkg/x.go", Kind: costs.AccessRead, Bytes: prefix}}}}
	return []costs.Run{a, b}
}

var terms = []costs.Term{{ID: "harness", Forms: []string{"harness", "agent harness"}}, {ID: "pkg", Forms: []string{"pkg"}}}

var _ = Describe("area affinity", func() {
	stillOpen := time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC)
	closedEarly := time.Date(2026, 10, 5, 0, 15, 0, 0, time.UTC)

	It("counts a read of a file another live real session had read", func() {
		rereads, read := costs.Rereads(areaRuns(stillOpen, "x", "y"))
		Expect(rereads).To(Equal([]costs.Reread{{Run: 1, Day: "2026-10-05", Model: model, Bytes: prefix}}))
		Expect(read).To(Equal(int64(2 * prefix)))
		By("not counting it once the other session's run has closed")
		rereads, _ = costs.Rereads(areaRuns(closedEarly, "x", "y"))
		Expect(rereads).To(BeEmpty())
	})

	It("reads a request's area from the paths and terms its prompt names", func() {
		runs := areaRuns(stillOpen, "x", "y")
		area := costs.RequestArea(runs[1].Area.Prompts[0].Text, runs[1].Worktree, costs.KnownPaths(runs), terms)
		Expect(area).To(Equal([]string{"pkg/x.go", "term:harness", "term:pkg"}))
		Expect(costs.RequestArea("nothing named here", "/w/b", costs.KnownPaths(runs), terms)).To(BeEmpty())
	})

	It("routes to the warm session covering the area and prices the attach by hand", func() {
		replay := &costs.AreaReplay{Runs: areaRuns(stillOpen, "x", "y"), Terms: terms, Lifetime: lifetime,
			Prices: map[string]costs.Price{model: price}, TokensPerByte: 1}
		routes := replay.Affinity(-1)
		Expect(routes).To(HaveLen(2))
		By("opening a new session for A, which had no warm candidate")
		Expect(routes[0].Attached).To(BeFalse())
		By("attaching B, whose file and the pkg term A's signature holds and the harness term it does not: one call reads A's 2^20 tokens (1 $) and the avoided read saves 19 $")
		Expect(routes[1]).To(Equal(costs.AreaRoute{Day: "2026-10-05", Attached: true, Coverage: 2.0 / 3, Carried: prefix,
			Credit: prefix, Delta: requestUSD - rebuildUSD, BreakEven: rebuildUSD * prefix}))
		By("refusing the attach when A carries more than the bound")
		Expect(replay.Affinity(prefix - 1)[1].Attached).To(BeFalse())
	})

	It("replays today's router only onto a closed session of the same agent", func() {
		replay := &costs.AreaReplay{Runs: areaRuns(stillOpen, "x", "x"), Terms: terms, Lifetime: lifetime,
			Prices: map[string]costs.Price{model: price}, TokensPerByte: 1}
		Expect(replay.Router()[1].Attached).To(BeFalse())
		replay.Runs = areaRuns(closedEarly, "x", "x")
		Expect(replay.Router()[1].Attached).To(BeTrue())
		replay.Runs = areaRuns(closedEarly, "x", "y")
		Expect(replay.Router()[1].Attached).To(BeFalse())
	})

	It("reads terms, names and forms from an ontology source", func() {
		source := strings.Join([]string{
			`term harness "Agent harness" "The service that runs agent sessions.";`,
			`term turn_executor "Turn executor" "The loop that carries out one turn."`,
			`  forms "turn executor" "turn executors";`,
			`not a term`,
		}, "\n")
		read, err := costs.ReadTerms(strings.NewReader(source))
		Expect(err).NotTo(HaveOccurred())
		Expect(read).To(Equal([]costs.Term{
			{ID: "harness", Forms: []string{"harness", "agent harness"}},
			{ID: "turn_executor", Forms: []string{"turn executor", "turn executor", "turn executor", "turn executors"}},
		}))
	})
})
