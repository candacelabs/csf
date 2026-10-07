// Copyright 2026 Candace Labs

package evaluate_test

import (
	"math"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/ouroboros/evaluate"
)

var _ = Describe("Scoring a build", func() {
	const live, candidate = "aaaaaaaaaaaa", "bbbbbbbbbbbb"
	suite := evaluate.Suite{Version: 3, Tickets: []evaluate.Ticket{ticket(1, 100, 1), ticket(2, 100, 1), ticket(3, 100, 1)}}

	It("reads a replay only up to the suite's budget of tool calls", func() {
		events := eventLog(30, 2, 25)
		calls, episodes := evaluate.Read(events, 20)
		Expect(calls).To(Equal(int64(20)))
		Expect(episodes).To(Equal(int64(1)))
		calls, episodes = evaluate.Read(events, 0)
		Expect(calls).To(Equal(int64(30)))
		Expect(episodes).To(Equal(int64(2)))
	})

	It("is the struggle rate over its replays with the Garwood interval, complete once every ticket is replayed", func() {
		replays := []evaluate.Replay{replay(live, 3, 1, 100, 2), replay(live, 3, 2, 100, 4)}
		partial := evaluate.ScoreOf(live, suite, replays, 0)
		Expect(partial.Complete).To(BeFalse())
		Expect(partial.Replays).To(Equal(2))
		replays = append(replays, replay(live, 3, 3, 100, 0))
		score := evaluate.ScoreOf(live, suite, replays, 90*time.Minute)
		Expect(score.Complete).To(BeTrue())
		Expect(*score.PerK).To(BeNumerically("~", 20.0, 1e-9))
		Expect(*score.Low).To(BeNumerically("<", 20.0))
		Expect(*score.High).To(BeNumerically(">", 20.0))
		Expect(score.CostUSD).To(BeNumerically("~", 6.0, 1e-9))
		Expect(score.WallSeconds).To(Equal(5400.0))
		Expect(score.Nodes).To(Equal(map[string]int{evaluate.NodeHost: 3}))
	})

	It("counts only the build's replays of this version's tickets", func() {
		replays := []evaluate.Replay{replay(live, 3, 1, 100, 2), replay(candidate, 3, 2, 100, 4), replay(live, 2, 2, 100, 4), replay(live, 3, 99, 100, 4)}
		Expect(evaluate.ScoreOf(live, suite, replays, 0).Replays).To(Equal(1))
	})

	It("bounds a comparison by the spread of the per-ticket differences", func() {
		liveReplays := []evaluate.Replay{replay(live, 3, 1, 100, 2), replay(live, 3, 2, 100, 4), replay(live, 3, 3, 100, 3)}
		worse := []evaluate.Replay{replay(candidate, 3, 1, 100, 6), replay(candidate, 3, 2, 100, 8), replay(candidate, 3, 3, 100, 7)}
		comparison := evaluate.Compare(worse, liveReplays)
		Expect(comparison.Paired).To(Equal(3))
		Expect(comparison.Delta).To(BeNumerically("~", 40.0, 1e-9))
		// Every difference is exactly 40 per 1k: no spread, so a zero bound.
		Expect(comparison.Bound).To(BeNumerically("~", 0, 1e-9))
		Expect(evaluate.Compare(worse[:1], liveReplays).Bound).To(Equal(math.Inf(1)))
	})

	It("admits a scored build no worse than live and refuses an unscored or regressed one", func() {
		liveReplays := []evaluate.Replay{replay(live, 3, 1, 100, 2), replay(live, 3, 2, 100, 4), replay(live, 3, 3, 100, 3)}
		better := []evaluate.Replay{replay(candidate, 3, 1, 100, 1), replay(candidate, 3, 2, 100, 3), replay(candidate, 3, 3, 100, 3)}
		worse := []evaluate.Replay{replay(candidate, 3, 1, 100, 6), replay(candidate, 3, 2, 100, 8), replay(candidate, 3, 3, 100, 7)}

		comparison, err := evaluate.Admit(suite, better, liveReplays, candidate, live)
		Expect(err).NotTo(HaveOccurred())
		Expect(comparison.Delta).To(BeNumerically("<", 0))

		_, err = evaluate.Admit(suite, worse, liveReplays, candidate, live)
		Expect(err).To(MatchError(evaluate.ErrRegressed))

		_, err = evaluate.Admit(suite, better[:2], liveReplays, candidate, live)
		Expect(err).To(MatchError(evaluate.ErrUnscored))
	})

	It("admits a scored build on its own score when live has none to compare with", func() {
		better := []evaluate.Replay{replay(candidate, 3, 1, 100, 1), replay(candidate, 3, 2, 100, 3), replay(candidate, 3, 3, 100, 3)}
		comparison, err := evaluate.Admit(suite, better, nil, candidate, live)
		Expect(err).NotTo(HaveOccurred())
		Expect(comparison).To(BeNil())
	})

	It("measures recall as the share of the reference's files the replay touched", func() {
		Expect(evaluate.Recall([]string{"a.go", "b.go"}, []string{"b.go", "c.go"})).To(Equal(0.5))
		Expect(evaluate.Recall(nil, []string{"b.go"})).To(BeZero())
	})
})
