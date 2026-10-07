// Copyright 2026 Candace Labs

package ouroboros_test

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/cron/crontest"
	"github.com/candacelabs/csf/services/ouroboros"
)

var _ = Describe("The ledger on pgmem", func() {
	var (
		ctx    context.Context
		ledger *ouroboros.Ledger
		at     time.Time
	)

	BeforeEach(func() {
		ctx = context.Background()
		store := crontest.OpenStore(GinkgoT())
		var err error
		ledger, err = ouroboros.NewLedger(store.Database())
		Expect(err).NotTo(HaveOccurred())
		at = specStart
	})

	It("records a finding once, however often a grown item is mined", func() {
		finding := ouroboros.Finding{Miner: minerName, Rule: "invisible", Subject: runAssignment, Item: runLog, Severity: "SEVERITY_S3", Record: json.RawMessage(`{"rule":"invisible"}`), FoundAt: at}
		inserted, err := ledger.RecordFindings(ctx, []ouroboros.Finding{finding})
		Expect(err).NotTo(HaveOccurred())
		Expect(inserted).To(Equal(int64(1)))
		inserted, err = ledger.RecordFindings(ctx, []ouroboros.Finding{finding})
		Expect(err).NotTo(HaveOccurred())
		Expect(inserted).To(BeZero())
		findings, err := ledger.FindingsSince(ctx, time.Time{})
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(HaveLen(1))
		Expect(findings[0].Subject).To(Equal(runAssignment))
		Expect(string(findings[0].Record)).To(Equal(`{"rule":"invisible"}`))
	})

	It("records where a miner stands on an item", func() {
		_, known, err := ledger.Item(ctx, minerName, runLog)
		Expect(err).NotTo(HaveOccurred())
		Expect(known).To(BeFalse())
		Expect(ledger.RecordItem(ctx, ouroboros.Item{Miner: minerName, Item: runLog, ByteSize: 120, MinedAt: at})).To(Succeed())
		Expect(ledger.RecordItem(ctx, ouroboros.Item{Miner: minerName, Item: runLog, ByteSize: 240, MinedAt: at.Add(time.Minute)})).To(Succeed())
		item, known, err := ledger.Item(ctx, minerName, runLog)
		Expect(err).NotTo(HaveOccurred())
		Expect(known).To(BeTrue())
		Expect(item.ByteSize).To(Equal(int64(240)))
	})

	It("keeps the labeler's queue as the proposals that need labels", func() {
		Expect(ledger.RecordProposal(ctx, ouroboros.ProposalRecord{Ticket: 217, ID: "IC_1", Miner: "serve_drain_resume", Corpus: "jsonl", Instances: []string{"test-drain-ok-001"}, Verdict: ouroboros.VerdictNeedsLabels, Reason: "none", CheckedAt: at})).To(Succeed())
		Expect(ledger.RecordProposal(ctx, ouroboros.ProposalRecord{Ticket: 249, ID: "IC_2", Miner: "no_self_merge", Corpus: "pull_request", Instances: []string{"#226"}, Verdict: ouroboros.VerdictLaunch, Reason: "found", CheckedAt: at})).To(Succeed())
		queue, err := ledger.ProposalsNeedingLabels(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(queue).To(HaveLen(1))
		Expect(queue[0].Ticket).To(Equal(int64(217)))
		Expect(queue[0].Instances).To(Equal([]string{"test-drain-ok-001"}))
		record, judged, err := ledger.Proposal(ctx, 249, "IC_2")
		Expect(err).NotTo(HaveOccurred())
		Expect(judged).To(BeTrue())
		Expect(record.Verdict).To(Equal(ouroboros.VerdictLaunch))
		_, judged, err = ledger.Proposal(ctx, 249, "IC_9")
		Expect(err).NotTo(HaveOccurred())
		Expect(judged).To(BeFalse())
	})

	It("follows a fixer from launch to its outcome", func() {
		Expect(ledger.RecordProposal(ctx, ouroboros.ProposalRecord{Ticket: 249, ID: "IC_2", Miner: "no_self_merge", Corpus: "pull_request", Instances: []string{"#226"}, Verdict: ouroboros.VerdictLaunch, Reason: "found", CheckedAt: at})).To(Succeed())
		fixer := ouroboros.Fixer{AssignmentID: otherAssignment, Ticket: 249, ProposalID: "IC_2", Miner: "no_self_merge", Model: ouroboros.DefaultFixerModel, StartedAt: at, Outcome: ouroboros.OutcomeRunning}
		Expect(ledger.RecordFixer(ctx, fixer)).To(Succeed())
		running, err := ledger.RunningFixers(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(running).To(HaveLen(1))
		fixer.Outcome, fixer.FinishedAt, fixer.CostUSDMicros, fixer.PullRequestURL = ouroboros.OutcomeReady, at.Add(20*time.Minute), 1_125_090, "https://example.invalid/pull/307"
		Expect(ledger.UpdateFixer(ctx, fixer)).To(Succeed())
		running, err = ledger.RunningFixers(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(running).To(BeEmpty())
		today, err := ledger.FixersStartedSince(ctx, at.Add(-time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(today).To(HaveLen(1))
		Expect(today[0].CostUSDMicros).To(Equal(int64(1_125_090)))
		Expect(today[0].Outcome).To(Equal(ouroboros.OutcomeReady))
		forTicket, err := ledger.FixersForTicket(ctx, 249)
		Expect(err).NotTo(HaveOccurred())
		Expect(forTicket).To(HaveLen(1))
		Expect(ledger.UpdateFixer(ctx, ouroboros.Fixer{AssignmentID: runAssignment, Outcome: ouroboros.OutcomeFailed, FinishedAt: at})).To(HaveOccurred(), "a fixer never launched is not updated")
	})

	It("records one merge result per pull request head", func() {
		merge := ouroboros.Merge{PullRequest: 15, HeadSHA: "abc1234", AuthorSessions: []string{"a", "b"}, Merger: specMerger, Merged: false, ExitCode: 1, Reason: "REFUSED", RecordedAt: at}
		Expect(ledger.RecordMerge(ctx, merge)).To(Succeed())
		read, seen, err := ledger.Merge(ctx, 15, "abc1234")
		Expect(err).NotTo(HaveOccurred())
		Expect(seen).To(BeTrue())
		Expect(read.AuthorSessions).To(Equal([]string{"a", "b"}))
		_, seen, err = ledger.Merge(ctx, 15, "def5678")
		Expect(err).NotTo(HaveOccurred())
		Expect(seen).To(BeFalse())
		recent, err := ledger.RecentMerges(ctx, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(recent).To(HaveLen(1))
	})

	It("keeps one point per series and day", func() {
		value := 4.2
		Expect(ledger.RecordSeries(ctx, ouroboros.Point{Series: ouroboros.SeriesStruggleRate, Day: "2026-10-03", Value: &value, Numerator: 3, Denominator: 714, ComputedAt: at})).To(Succeed())
		Expect(ledger.RecordSeries(ctx, ouroboros.Point{Series: ouroboros.SeriesStruggleRate, Day: "2026-10-03", Numerator: 0, Denominator: 0, ComputedAt: at})).To(Succeed())
		points, err := ledger.Series(ctx, ouroboros.SeriesStruggleRate)
		Expect(err).NotTo(HaveOccurred())
		Expect(points).To(HaveLen(1))
		Expect(points[0].Value).To(BeNil(), "the day was replaced by a point with no rate")
	})
})
