// Copyright 2026 Candace Labs

package opsview

import (
	"bytes"
	"context"
	"runtime/debug"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/dispatch"
	"github.com/candacelabs/csf/services/ouroboros"
)

// stagesOfLine is every board card's stage by key.
func stagesOfLine(line Line) map[string]dispatch.Stage {
	stages := map[string]dispatch.Stage{}
	for _, card := range line.Cards {
		stages[card.Key] = card.Stage
	}
	return stages
}

var _ = Describe("the line", func() {
	built := time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC)
	build := Build{Revision: "9d092e0a1b2c3d4e5f60718293a4b5c6d7e8f901", Time: built}
	inBuild, afterBuild := built.Add(-time.Hour), built.Add(time.Hour)
	const (
		mergedPull = "https://github.com/candacelabs/repo/pull/7"
		laterPull  = "https://github.com/candacelabs/repo/pull/9"
		openPull   = "https://github.com/candacelabs/repo/pull/8"
		today      = "2026-10-05"
	)

	snapshot := func() *dispatch.Snapshot {
		return &dispatch.Snapshot{
			Capacity: 2,
			Limits: []dispatch.Limit{
				{Name: dispatch.LimitHarness, Bounded: true, Launches: 5},
				{Name: dispatch.LimitRate, Bounded: true, Launches: 2},
				{Name: dispatch.LimitBudget, Bounded: false},
			},
			Queue: []dispatch.SliceView{
				{SliceID: "ready", Title: "Ready slice", Rank: 1, CriticalPath: 2, Urgency: "URGENCY_HIGH"},
				{SliceID: "contended", Title: "Contended slice", Rank: 2, Contends: []string{"with-pr"}, Waiting: "contends with with-pr, which runs"},
				{SliceID: "blocked", Title: "Blocked slice", DependsOn: []string{"merged"}, Waiting: "depends on merged"},
			},
			Running: []dispatch.SliceView{
				{SliceID: "with-pr", Title: "Slice with a pull request", AssignmentID: "a1", Contends: []string{"contended"}},
				{SliceID: "running", Title: "Running slice", AssignmentID: "a2"},
			},
			Finished: []dispatch.SliceView{
				{SliceID: "merged", Title: "Merged slice", State: "SLICE_STATE_MERGED", PullRequestURL: mergedPull},
				{SliceID: "failed", Title: "Failed slice", State: "SLICE_STATE_FAILED"},
			},
		}
	}

	state := func() viewState {
		cards, err := newCards(
			cardOf("a1", SessionCard{Agent: "pr-owner", Status: StatusFinished, PullRequestURL: openPull, PullRequest: PullRequestReady, Model: "claude-opus-5-5", Executor: "claude", CostByDay: map[string]float64{today: 1.5, "2026-10-04": 2}}),
			cardOf("a2", SessionCard{Agent: "runner", Status: StatusRunning, Activity: "Bash: go test ./...", Elapsed: "12m", Recent: []RecentEvent{{Kind: KindGate}}}),
			cardOf("a3", SessionCard{Agent: "loose", Status: StatusWaiting, TicketURL: "https://github.com/candacelabs/repo/issues/3"}),
			cardOf("a4", SessionCard{Agent: "gone", Status: StatusClosed}),
			cardOf("a5", SessionCard{Agent: "old-merge", Status: StatusFinished, PullRequest: PullRequestMerged, MergedOn: "2026-10-03"}),
			cardOf("a6", SessionCard{Agent: "sleeper", Status: StatusSuspended}),
		)
		Expect(err).NotTo(HaveOccurred())
		return viewState{
			cards: cards, queue: snapshot(), today: today,
			merges: mergesState{Pulls: []MergedPullRequest{
				{Number: 7, Title: "Merged slice", URL: mergedPull, MergedAt: inBuild},
				{Number: 9, Title: "Merged after the build", URL: laterPull, MergedAt: afterBuild},
			}},
		}
	}

	It("puts every slice on the board in its stage: the dispatcher's, refined by the sessions, the merges and the build", func() {
		line := lineOf(state(), build)
		Expect(stagesOfLine(line)).To(Equal(map[string]dispatch.Stage{
			"line-ready": dispatch.StageReady, "line-contended": dispatch.StageContended, "line-blocked": dispatch.StageBlocked,
			"line-with-pr": StagePR, "line-running": dispatch.StageRunning,
			"line-merged": StageLive, "line-a3": dispatch.StageRunning, "line--9": dispatch.StageMerged,
			"line-a6": dispatch.StageRunning,
		}))
		Expect(line.Graph).To(HaveLen(9), "the graph holds the failed slice and not the merge no slice or session carried")
	})

	It("says why each waiting slice stands where it does and who carries each running one", func() {
		cards := map[string]LineCard{}
		for _, card := range lineOf(state(), build).Cards {
			cards[card.Key] = card
		}
		Expect(cards["line-ready"].Why).To(Equal("#1: critical path 2, urgency high"))
		Expect(cards["line-ready"].Frontier).To(BeTrue())
		Expect(cards["line-contended"].Why).To(Equal("contends with with-pr, which runs"))
		Expect(cards["line-blocked"].Why).To(Equal("depends on merged"))
		Expect(cards["line-blocked"].DependsOn).To(Equal([]string{"line-merged"}))
		withPR := cards["line-with-pr"]
		Expect([]string{withPR.Owner, withPR.Model, withPR.Executor, withPR.Cost, withPR.PullRequestURL, string(withPR.PullRequest)}).To(
			Equal([]string{"pr-owner", "claude-opus-5-5", "claude", "$3.50", openPull, string(PullRequestReady)}))
		Expect(cards["line-running"].Activity).To(Equal("Bash: go test ./..."))
		Expect(cards["line-running"].Elapsed).To(Equal("12m"))
	})

	It("shows nothing live when the build is unknown", func() {
		stages := stagesOfLine(lineOf(state(), Build{}))
		Expect(stages["line-merged"]).To(Equal(dispatch.StageMerged))
		Expect(stages["line--9"]).To(Equal(dispatch.StageMerged))
	})

	It("gives each real session a slot and says whether the admission waits on quota or the machine", func() {
		line := lineOf(state(), build)
		states := map[string]string{}
		for _, worker := range line.Workers {
			states[worker.Owner] = worker.State
		}
		Expect(states).To(Equal(map[string]string{"runner": workerGate, "pr-owner": workerIdle, "loose": workerWaiting, "old-merge": workerIdle}),
			"a finished session keeps its real session open whatever its slice did; a suspended one has none and a closed one is gone")
		Expect(line.Running).To(Equal(1))
		Expect(line.Capacity).To(Equal(2))
		Expect(line.Binding).To(Equal(string(dispatch.LimitRate)))
		Expect(line.Quota).To(BeTrue())

		machine, quota := binding([]dispatch.Limit{{Name: dispatch.LimitHarness, Bounded: true, Launches: 1}, {Name: dispatch.LimitBudget, Bounded: true, Launches: 4}})
		Expect(machine).To(Equal(string(dispatch.LimitHarness)))
		Expect(quota).To(BeFalse())
	})

	It("draws the loop as a ring with the hop that piles up marked and the unrecorded hops counted as nothing", func() {
		panel := LoopPanel{Present: true, Snapshot: ouroboros.Snapshot{
			Findings: 40, Queue: 12, Miners: []ouroboros.MinerStatus{{Built: true}, {Built: false}, {Built: true}},
			Fixers: ouroboros.FixerSummary{Sessions: 3}, Merges: ouroboros.MergeSummary{Merged: 1},
		}}
		hops := loopHops(panel)
		Expect(hops).To(HaveLen(7))
		counts := map[string]int{}
		var piles []string
		for _, hop := range hops {
			if hop.Recorded {
				counts[hop.Name] = *hop.Count
			}
			if hop.PilesUp {
				piles = append(piles, hop.Name)
			}
		}
		Expect(counts).To(Equal(map[string]int{"mining findings": 40, "tickets waiting for labels": 12, "accepted miners": 2, "fixer slices": 3, "merged": 1}))
		Expect(piles).To(Equal([]string{"mining findings"}))
		Expect(hops[0].Recorded).To(BeFalse())
		Expect(hops[6].Recorded).To(BeFalse())
		Expect(loopHops(LoopPanel{})[1].Recorded).To(BeFalse(), "no snapshot, no counts")
	})

	It("lays out every slice with an edge once, depends_on as arrows and contends as dashed pairs, and lists the rest loose", func() {
		graph := layoutGraph(lineOf(state(), build).Graph)
		placed := map[string]graphNode{}
		for _, node := range graph.Nodes {
			Expect(placed).NotTo(HaveKey(node.Key))
			placed[node.Key] = node
		}
		Expect(placed).To(HaveLen(4), "merged → blocked, and the contending pair")
		loose := []string{}
		for _, node := range graph.Loose {
			loose = append(loose, node.Key)
		}
		Expect(loose).To(ConsistOf("line-ready", "line-running", "line-failed", "line-a3", "line-a6"))
		Expect(graph.Edges).To(HaveLen(1), "merged → blocked is the one depends_on edge")
		Expect(graph.Contends).To(HaveLen(1), "contended and with-pr, drawn once")
		Expect(placed["line-merged"].X).To(BeNumerically("<", placed["line-blocked"].X), "dependencies read left to right")
		Expect(graph.Width).To(BeNumerically(">", 0))
		Expect(layoutGraph(lineOf(state(), build).Graph)).To(Equal(graph), "the layout is deterministic, so equal states render equal markup")
		Expect(layoutGraph(nil)).To(Equal(graphView{}))
	})

	It("tells the operator to run csf upgrade when merged work is not in the running build", func() {
		view := &OpsView{build: build}
		next := state()
		next.prefs.Opened = []string{sectionLine}
		var page bytes.Buffer
		Expect(view.renderLine(next).Render(context.Background(), &page)).To(Succeed())
		Expect(page.String()).To(ContainSubstring("1 merged pull request is not in the build this host runs (9d092e0a1b2c, 2026-10-05 06:00:00 UTC). Run <code>csf upgrade</code> to take it live."))
		Expect(page.String()).To(ContainSubstring(`data-opsview-node="line-blocked"`))
		Expect(page.String()).To(ContainSubstring(`style="view-transition-name: line-with-pr"`))

		page.Reset()
		Expect((&OpsView{}).renderLine(next).Render(context.Background(), &page)).To(Succeed())
		Expect(page.String()).To(ContainSubstring("This host's build is unknown, so no merge shows as live."))
	})

	It("marks the panel dirty only when what it shows moved", func() {
		view := &OpsView{build: build}
		before := state()
		Expect(view.lineChanged(before, state())).To(BeFalse())
		after := state()
		card, _ := after.cards.Get("a2")
		card.Activity = "Edit: line.go"
		cards, err := after.cards.Upsert("a2", card)
		Expect(err).NotTo(HaveOccurred())
		after.cards = cards
		Expect(view.lineChanged(before, after)).To(BeTrue(), "the running card's current tool row moved")
	})

	It("reads the build go build stamped", func() {
		info := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}, {Key: "vcs.time", Value: "2026-10-05T06:00:00Z"}}}
		Expect(BuildOf(info, true)).To(Equal(Build{Revision: "abc123", Time: built}))
		Expect(BuildOf(nil, false)).To(Equal(Build{}))
		Expect(BuildOf(&debug.BuildInfo{}, true)).To(Equal(Build{}))
	})
})
