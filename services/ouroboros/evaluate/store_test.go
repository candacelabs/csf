// Copyright 2026 Candace Labs

package evaluate_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/cron/crontest"
	"github.com/candacelabs/csf/services/ouroboros/evaluate"
)

var _ = Describe("The suite store on pgmem", func() {
	const live, candidate = "aaaaaaaaaaaa", "bbbbbbbbbbbb"
	var (
		ctx   context.Context
		store *evaluate.SuiteStore
		suite evaluate.Suite
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		store, err = evaluate.NewSuiteStore(crontest.OpenStore(GinkgoT()).Database())
		Expect(err).NotTo(HaveOccurred())
		suite = evaluate.Suite{Version: 1, SelectedAt: specStart, RotatesAt: specStart.Add(evaluate.RotationPeriod), Model: "claude-opus-5-5",
			Tickets: []evaluate.Ticket{ticket(1, 100, 1), ticket(2, 100, 1)}}
	})

	It("refuses a store with no database, and a suite read before any is selected, with the defined errors", func() {
		_, err := evaluate.NewSuiteStore(nil)
		Expect(err).To(MatchError(evaluate.ErrDatabaseRequired))
		_, err = store.LatestSuite(ctx)
		Expect(err).To(MatchError(evaluate.ErrNoSuite))
	})

	It("keeps every suite version, the latest current, and hides every ticket any version held", func() {
		Expect(store.RecordSuite(ctx, suite)).To(Succeed())
		next := suite
		next.Version, next.Tickets = 2, []evaluate.Ticket{ticket(3, 100, 1)}
		Expect(store.RecordSuite(ctx, next)).To(Succeed())
		latest, err := store.LatestSuite(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(latest.Version).To(Equal(2))
		Expect(latest.Tickets[0].Number).To(Equal(int64(3)))
		hidden, err := store.HiddenTickets(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(hidden).To(Equal(map[int64]bool{1: true, 2: true, 3: true}))
	})

	It("aggregates replays from every node into one score with its wall clock, and recomputes the citation", func() {
		Expect(store.RecordSuite(ctx, suite)).To(Succeed())
		for _, recorded := range []evaluate.Replay{replay(live, 1, 1, 100, 4), replay(live, 1, 2, 100, 4), replay(candidate, 1, 1, 100, 2)} {
			Expect(store.RecordReplay(ctx, recorded)).To(Succeed())
		}
		burst := replay(candidate, 1, 2, 100, 2)
		burst.Node = evaluate.NodeBurst
		Expect(store.RecordReplay(ctx, burst)).To(Succeed())
		Expect(store.RecordRun(ctx, evaluate.Run{Build: candidate, SuiteVersion: 1, StartedAt: specStart, FinishedAt: specStart.Add(time.Hour)})).To(Succeed())

		score, err := store.ScoreOn(ctx, candidate, suite)
		Expect(err).NotTo(HaveOccurred())
		Expect(score.Complete).To(BeTrue())
		Expect(score.WallSeconds).To(Equal(3600.0))
		Expect(score.Nodes).To(Equal(map[string]int{evaluate.NodeHost: 1, evaluate.NodeBurst: 1}))

		line, err := store.Recompute(ctx, candidate, 1, live)
		Expect(err).NotTo(HaveOccurred())
		Expect(evaluate.Citations(line)).To(HaveLen(1))
		_, err = store.Recompute(ctx, "cccccccccccc", 1, live)
		Expect(err).To(MatchError(evaluate.ErrUnscored))

		scores, err := store.Scores(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(scores).To(HaveLen(1))
		Expect(scores[0].Build).To(Equal(candidate))
	})

	It("replaces a replay recorded again for the same build, version and ticket", func() {
		Expect(store.RecordSuite(ctx, suite)).To(Succeed())
		Expect(store.RecordReplay(ctx, replay(live, 1, 1, 100, 4))).To(Succeed())
		Expect(store.RecordReplay(ctx, replay(live, 1, 1, 120, 1))).To(Succeed())
		replays, err := store.Replays(ctx, live, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(replays).To(HaveLen(1))
		Expect(replays[0].ToolCalls).To(Equal(int64(120)))
	})
})
