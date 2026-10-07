// Copyright 2026 Candace Labs

package cron_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grammar "github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/pkg/pgmem"
	cron "github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/cron/crontest"
)

// The store specs run CSF's real schema and csfpg's generated queries on
// pgmem, the in-process substitute for PostgreSQL. pgmem keeps timestamps
// at second precision, so every instant here is a whole second.
var anchor = time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)

const (
	leaseOwner = "runner"
	rollup     = "rollup"
)

func triggerDefinition(name string, schedule grammar.Schedule, catchUp grammar.CatchUpPolicy, overlap grammar.OverlapPolicy) grammar.TriggerDefinition {
	GinkgoHelper()
	definition, err := schedule.Definition()
	Expect(err).NotTo(HaveOccurred())
	return grammar.TriggerDefinition{Name: name, Schedule: definition, CatchUp: catchUp, Overlap: overlap}
}

func claim(triggerName string, scheduledAt, nextRunAt time.Time, token string, claimedAt time.Time, duration time.Duration) cron.ClaimRequest {
	return cron.ClaimRequest{
		OccurrenceID: grammar.OccurrenceID(triggerName, scheduledAt),
		TriggerName:  triggerName,
		ScheduledAt:  scheduledAt,
		NextRunAt:    nextRunAt,
		LeaseOwner:   leaseOwner,
		LeaseToken:   token,
		ClaimedAt:    claimedAt,
		LeaseUntil:   claimedAt.Add(duration),
	}
}

func nextAfter(schedule grammar.Schedule, after time.Time) time.Time {
	GinkgoHelper()
	next, err := schedule.Next(after)
	Expect(err).NotTo(HaveOccurred())
	return next
}

func snapshotOf(ctx context.Context, store cron.IStore) grammar.StoreSnapshot {
	GinkgoHelper()
	snapshot, err := store.Snapshot(ctx)
	Expect(err).NotTo(HaveOccurred())
	return snapshot
}

var _ = Describe("the cron store on CSF's schema", func() {
	var (
		ctx      context.Context
		store    *pgmem.PostgresStoreOnPgmem[*cron.Store]
		schedule grammar.Schedule
	)

	BeforeEach(func() {
		ctx = context.Background()
		store = crontest.OpenStore(GinkgoT())
		schedule = grammar.Spec(grammar.Every(time.Minute)).Anchor(anchor)
	})

	It("establishes an interval anchor exactly once and reconciles the declared triggers", func() {
		definition := triggerDefinition(rollup, grammar.Spec(grammar.Every(time.Minute)), grammar.CatchUpNone, grammar.OverlapSkip)

		states, err := store.Store.Reconcile(ctx, []grammar.TriggerDefinition{definition}, anchor)
		Expect(err).NotTo(HaveOccurred())
		Expect(states).To(HaveLen(1))
		Expect(states[0].Definition.Schedule.HasAnchor).To(BeTrue())
		Expect(states[0].Definition.Schedule.Anchor).To(Equal(anchor))
		Expect(states[0].NextRunAt).To(Equal(anchor.Add(time.Minute)))

		states, err = store.Store.Reconcile(ctx, []grammar.TriggerDefinition{definition}, anchor.Add(30*time.Second))
		Expect(err).NotTo(HaveOccurred())
		Expect(states[0].Definition.Schedule.Anchor).To(Equal(anchor), "a restart keeps the cadence")
		Expect(states[0].NextRunAt).To(Equal(anchor.Add(time.Minute)))

		policyOnly := definition
		policyOnly.CatchUp = grammar.CatchUpLatest
		policyOnly.Overlap = grammar.OverlapAllow
		states, err = store.Store.Reconcile(ctx, []grammar.TriggerDefinition{policyOnly}, anchor.Add(40*time.Second))
		Expect(err).NotTo(HaveOccurred())
		Expect(states[0].Definition.CatchUp).To(Equal(grammar.CatchUpLatest))
		Expect(states[0].Definition.Overlap).To(Equal(grammar.OverlapAllow))
		Expect(states[0].NextRunAt).To(Equal(anchor.Add(time.Minute)), "a policy change keeps the cursor")

		states, err = store.Store.Reconcile(ctx, nil, anchor.Add(time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(states).To(BeEmpty(), "a trigger no longer declared is disabled")

		reenabledAt := anchor.Add(2 * time.Minute)
		states, err = store.Store.Reconcile(ctx, []grammar.TriggerDefinition{definition}, reenabledAt)
		Expect(err).NotTo(HaveOccurred())
		Expect(states[0].Definition.Schedule.Anchor).To(Equal(reenabledAt))
		Expect(states[0].NextRunAt).To(Equal(reenabledAt.Add(time.Minute)))
	})

	It("rejects a duplicate declaration and an unknown trigger", func() {
		definition := triggerDefinition(rollup, schedule, grammar.CatchUpNone, grammar.OverlapSkip)
		_, err := store.Store.Reconcile(ctx, []grammar.TriggerDefinition{definition, definition}, anchor)
		Expect(err).To(MatchError(cron.ErrInvalidConfiguration))

		scheduledAt := anchor.Add(time.Minute)
		_, err = store.Store.Claim(ctx, claim("unknown", scheduledAt, scheduledAt.Add(time.Minute), "token", scheduledAt, time.Minute))
		Expect(err).To(MatchError(cron.ErrTriggerNotFound))
	})

	It("uses deterministic occurrence IDs and fenced renewable leases", func() {
		definition := triggerDefinition(rollup, schedule, grammar.CatchUpAll, grammar.OverlapSkip)
		states, err := store.Store.Reconcile(ctx, []grammar.TriggerDefinition{definition}, anchor)
		Expect(err).NotTo(HaveOccurred())

		scheduledAt := states[0].NextRunAt
		nextRunAt := nextAfter(schedule, scheduledAt)
		occurrenceID := grammar.OccurrenceID(rollup, scheduledAt)
		_, err = store.Store.Claim(ctx, claim(rollup, scheduledAt, nextRunAt.Add(time.Minute), "invalid", scheduledAt.Add(time.Second), time.Minute))
		Expect(err).To(MatchError(cron.ErrOccurrenceConflict), "the next run must follow the durable schedule")
		_, err = store.Store.Claim(ctx, claim(rollup, scheduledAt, nextRunAt, "too-short", scheduledAt.Add(time.Second), time.Nanosecond))
		Expect(err).To(MatchError(cron.ErrInvalidConfiguration))

		claimedAt := scheduledAt.Add(time.Second)
		first, err := store.Store.Claim(ctx, claim(rollup, scheduledAt, nextRunAt, "token_a", claimedAt, time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Disposition).To(Equal(cron.ClaimAcquired))
		Expect(first.Occurrence.ID).To(Equal(occurrenceID))
		Expect(first.Occurrence.Attempt).To(Equal(uint32(1)))
		Expect(store.Store.Renew(ctx, cron.LeaseRenewal{
			OccurrenceID: occurrenceID, LeaseToken: "token_a",
			RenewedAt: claimedAt.Add(time.Second), LeaseUntil: claimedAt.Add(time.Second + time.Nanosecond),
		})).To(MatchError(ContainSubstring("invalid lease renewal")))

		held, err := store.Store.Claim(ctx, claim(rollup, scheduledAt, nextRunAt, "token_b", claimedAt.Add(time.Second), 2*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(held.Disposition).To(Equal(cron.ClaimLeaseHeld))

		recoveryAt := claimedAt.Add(time.Minute + time.Second)
		states, err = store.Store.Reconcile(ctx, []grammar.TriggerDefinition{definition}, recoveryAt)
		Expect(err).NotTo(HaveOccurred())
		Expect(states[0].NextRunAt).To(Equal(nextRunAt), "a restart never rewinds the cursor")
		expired, err := store.Store.Expired(ctx, recoveryAt, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(expired).To(HaveLen(1))
		Expect(expired[0].ID).To(Equal(occurrenceID))

		recovered, err := store.Store.Claim(ctx, claim(rollup, scheduledAt, nextRunAt, "token_b", recoveryAt, time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(recovered.Disposition).To(Equal(cron.ClaimAcquired))
		Expect(recovered.Occurrence.Attempt).To(Equal(uint32(2)))

		Expect(store.Store.Complete(ctx, cron.Completion{
			OccurrenceID: occurrenceID, LeaseToken: "token_a", Status: grammar.OccurrenceSucceeded, FinishedAt: recoveryAt.Add(time.Second),
		})).To(MatchError(cron.ErrLeaseLost), "a stale token never completes an occurrence")
		Expect(store.Store.Complete(ctx, cron.Completion{
			OccurrenceID: occurrenceID, LeaseToken: "token_b", Status: grammar.OccurrenceSucceeded,
			FinishedAt: recoveryAt.Add(time.Second), Error: "successful runs cannot carry errors",
		})).To(MatchError(ContainSubstring("invalid completion")))
		Expect(store.Store.Complete(ctx, cron.Completion{
			OccurrenceID: occurrenceID, LeaseToken: "token_b", Status: grammar.OccurrenceSucceeded, FinishedAt: recoveryAt.Add(time.Second),
		})).To(Succeed())

		snapshot := snapshotOf(ctx, store.Store)
		Expect(snapshot.Occurrences).To(HaveLen(1))
		Expect(snapshot.Occurrences[0].Status).To(Equal(grammar.OccurrenceSucceeded))
		Expect(snapshot.Occurrences[0].Attempt).To(Equal(uint32(2)))
		Expect(snapshot.Occurrences[0].LeaseOwner).To(BeEmpty())
		Expect(snapshot.Occurrences[0].LeaseUntil.IsZero()).To(BeTrue())
	})

	It("renews only a live lease with its current fencing token", func() {
		definition := triggerDefinition("renewable", schedule, grammar.CatchUpAll, grammar.OverlapSkip)
		states, err := store.Store.Reconcile(ctx, []grammar.TriggerDefinition{definition}, anchor)
		Expect(err).NotTo(HaveOccurred())

		scheduledAt := states[0].NextRunAt
		claimedAt := scheduledAt.Add(time.Second)
		request := claim("renewable", scheduledAt, nextAfter(schedule, scheduledAt), "current-token", claimedAt, time.Minute)
		_, err = store.Store.Claim(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		renewedAt := claimedAt.Add(10 * time.Second)
		leaseUntil := claimedAt.Add(2 * time.Minute)
		Expect(store.Store.Renew(ctx, cron.LeaseRenewal{OccurrenceID: "missing", LeaseToken: request.LeaseToken, RenewedAt: renewedAt, LeaseUntil: leaseUntil})).To(MatchError(cron.ErrLeaseLost))
		Expect(store.Store.Renew(ctx, cron.LeaseRenewal{OccurrenceID: request.OccurrenceID, LeaseToken: "stale-token", RenewedAt: renewedAt, LeaseUntil: leaseUntil})).To(MatchError(cron.ErrLeaseLost))
		Expect(store.Store.Renew(ctx, cron.LeaseRenewal{OccurrenceID: request.OccurrenceID, LeaseToken: request.LeaseToken, RenewedAt: request.LeaseUntil, LeaseUntil: request.LeaseUntil.Add(time.Minute)})).To(MatchError(cron.ErrLeaseLost), "an expired lease is not renewed")
		Expect(store.Store.Renew(ctx, cron.LeaseRenewal{OccurrenceID: request.OccurrenceID, LeaseToken: request.LeaseToken, RenewedAt: renewedAt, LeaseUntil: leaseUntil})).To(Succeed())

		Expect(snapshotOf(ctx, store.Store).Occurrences).To(ContainElement(And(
			HaveField("ID", request.OccurrenceID),
			HaveField("LeaseUntil", leaseUntil),
		)))
	})

	It("records fresh and expired skips while fencing live work", func() {
		definitions := []grammar.TriggerDefinition{
			triggerDefinition("fresh-skip", schedule, grammar.CatchUpNone, grammar.OverlapSkip),
			triggerDefinition("expired-skip", schedule, grammar.CatchUpNone, grammar.OverlapSkip),
		}
		states, err := store.Store.Reconcile(ctx, definitions, anchor)
		Expect(err).NotTo(HaveOccurred())
		Expect(states).To(HaveLen(2))
		Expect(states[0].Definition.Name).To(Equal("expired-skip"), "states are ordered by name")

		freshAt := states[1].NextRunAt
		fresh := cron.SkipRequest{
			OccurrenceID: grammar.OccurrenceID("fresh-skip", freshAt), TriggerName: "fresh-skip",
			ScheduledAt: freshAt, NextRunAt: nextAfter(schedule, freshAt), SkippedAt: freshAt.Add(time.Second), Reason: "catch-up disabled",
		}
		invalid := fresh
		invalid.Reason = ""
		Expect(store.Store.Skip(ctx, invalid)).To(MatchError(ContainSubstring("invalid occurrence skip")))
		missing := fresh
		missing.TriggerName = "missing"
		missing.OccurrenceID = grammar.OccurrenceID(missing.TriggerName, missing.ScheduledAt)
		Expect(store.Store.Skip(ctx, missing)).To(MatchError(cron.ErrTriggerNotFound))
		Expect(store.Store.Skip(ctx, fresh)).To(Succeed())
		Expect(store.Store.Skip(ctx, fresh)).To(Succeed(), "a skip is idempotent")

		expiredAt := states[0].NextRunAt
		claimedAt := expiredAt.Add(time.Second)
		claimRequest := claim("expired-skip", expiredAt, nextAfter(schedule, expiredAt), "expiring", claimedAt, time.Minute)
		_, err = store.Store.Claim(ctx, claimRequest)
		Expect(err).NotTo(HaveOccurred())
		expired := cron.SkipRequest{
			OccurrenceID: claimRequest.OccurrenceID, TriggerName: claimRequest.TriggerName,
			ScheduledAt: claimRequest.ScheduledAt, NextRunAt: claimRequest.NextRunAt,
			SkippedAt: claimedAt.Add(30 * time.Second), Reason: "recovery disabled",
		}
		Expect(store.Store.Skip(ctx, expired)).To(MatchError(cron.ErrOccurrenceRunning), "a live lease is never skipped")
		expired.SkippedAt = claimRequest.LeaseUntil.Add(time.Second)
		Expect(store.Store.Skip(ctx, expired)).To(Succeed())

		Expect(snapshotOf(ctx, store.Store).Occurrences).To(ConsistOf(
			And(HaveField("ID", fresh.OccurrenceID), HaveField("Status", grammar.OccurrenceSkipped), HaveField("SkipReason", fresh.Reason)),
			And(HaveField("ID", expired.OccurrenceID), HaveField("Status", grammar.OccurrenceSkipped), HaveField("SkipReason", expired.Reason)),
		))
	})

	It("keeps terminal skip replays from touching an advanced cursor", func() {
		definition := triggerDefinition("stale-skip", schedule, grammar.CatchUpNone, grammar.OverlapSkip)
		states, err := store.Store.Reconcile(ctx, []grammar.TriggerDefinition{definition}, anchor)
		Expect(err).NotTo(HaveOccurred())

		firstAt := states[0].NextRunAt
		secondAt := nextAfter(schedule, firstAt)
		thirdAt := nextAfter(schedule, secondAt)
		first := cron.SkipRequest{
			OccurrenceID: grammar.OccurrenceID(definition.Name, firstAt), TriggerName: definition.Name,
			ScheduledAt: firstAt, NextRunAt: secondAt, SkippedAt: firstAt.Add(time.Second), Reason: "catch-up disabled",
		}
		second := cron.SkipRequest{
			OccurrenceID: grammar.OccurrenceID(definition.Name, secondAt), TriggerName: definition.Name,
			ScheduledAt: secondAt, NextRunAt: thirdAt, SkippedAt: secondAt.Add(time.Second), Reason: "catch-up disabled",
		}
		Expect(store.Store.Skip(ctx, first)).To(Succeed())
		Expect(store.Store.Skip(ctx, second)).To(Succeed())

		advanced := snapshotOf(ctx, store.Store)
		Expect(advanced.Triggers[0].NextRunAt).To(Equal(thirdAt))
		Expect(store.Store.Skip(ctx, first)).To(Succeed())
		Expect(snapshotOf(ctx, store.Store)).To(Equal(advanced))

		reconfigured := triggerDefinition(definition.Name, grammar.Spec(grammar.Daily(grammar.Noon())), grammar.CatchUpNone, grammar.OverlapSkip)
		_, err = store.Store.Reconcile(ctx, []grammar.TriggerDefinition{reconfigured}, second.SkippedAt.Add(time.Second))
		Expect(err).NotTo(HaveOccurred())
		beforeStaleReplay := snapshotOf(ctx, store.Store)
		Expect(store.Store.Skip(ctx, first)).To(Succeed())
		Expect(snapshotOf(ctx, store.Store)).To(Equal(beforeStaleReplay))
	})

	It("enforces no-overlap in the store, where every replica sees it", func() {
		definition := triggerDefinition(rollup, schedule, grammar.CatchUpAll, grammar.OverlapSkip)
		states, err := store.Store.Reconcile(ctx, []grammar.TriggerDefinition{definition}, anchor)
		Expect(err).NotTo(HaveOccurred())

		firstAt := states[0].NextRunAt
		secondAt := nextAfter(schedule, firstAt)
		thirdAt := nextAfter(schedule, secondAt)
		claimedAt := secondAt.Add(time.Second)
		_, err = store.Store.Claim(ctx, claim(rollup, firstAt, secondAt, "first", claimedAt, time.Minute))
		Expect(err).NotTo(HaveOccurred())

		second, err := store.Store.Claim(ctx, claim(rollup, secondAt, thirdAt, "second", claimedAt, time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Disposition).To(Equal(cron.ClaimSkippedOverlap))
		Expect(second.Occurrence.Status).To(Equal(grammar.OccurrenceSkipped))
		Expect(second.Occurrence.SkipReason).To(Equal("overlap"))
		Expect(snapshotOf(ctx, store.Store).Triggers[0].NextRunAt).To(Equal(thirdAt), "a skipped occurrence still advances the cursor")
	})

	It("leaves an expired occurrence recoverable while a different occurrence owns the no-overlap lease", func() {
		definition := triggerDefinition("serial", schedule, grammar.CatchUpAll, grammar.OverlapSkip)
		states, err := store.Store.Reconcile(ctx, []grammar.TriggerDefinition{definition}, anchor)
		Expect(err).NotTo(HaveOccurred())
		firstAt := states[0].NextRunAt
		secondAt := nextAfter(schedule, firstAt)
		thirdAt := nextAfter(schedule, secondAt)

		firstClaimedAt := firstAt.Add(time.Second)
		_, err = store.Store.Claim(ctx, claim("serial", firstAt, secondAt, "first", firstClaimedAt, time.Second))
		Expect(err).NotTo(HaveOccurred())
		secondClaimedAt := firstClaimedAt.Add(2 * time.Second)
		second, err := store.Store.Claim(ctx, claim("serial", secondAt, thirdAt, "second", secondClaimedAt, time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Disposition).To(Equal(cron.ClaimAcquired), "an expired lease does not block the next occurrence")

		recovery := claim("serial", firstAt, secondAt, "recovery", secondClaimedAt.Add(time.Second), time.Minute)
		result, err := store.Store.Claim(ctx, recovery)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Disposition).To(Equal(cron.ClaimLeaseHeld))
		expired, err := store.Store.Expired(ctx, recovery.ClaimedAt, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(expired).To(ContainElement(HaveField("ID", grammar.OccurrenceID("serial", firstAt))))
	})
})
