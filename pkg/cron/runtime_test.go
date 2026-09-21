package cron_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cron "github.com/candacelabs/csf/pkg/cron"
)

var _ = Describe("Durable cron runtime", func() {
	Describe("construction", func() {
		It("requires an explicit store and at least one static job", func() {
			schedule := cron.Spec(cron.Every(time.Hour))
			handler := func(ctx context.Context, invocation cron.Invocation) error { return nil }

			_, err := cron.New(cron.WithJob("hourly", schedule, handler))
			Expect(err).To(MatchError(cron.ErrStoreRequired))

			_, err = cron.New(cron.WithStore(cron.NewMemoryStore()))
			Expect(err).To(MatchError(cron.ErrNoJobs))
		})

		It("rejects names outside the portable store contract and duplicates", func() {
			schedule := cron.Spec(cron.Every(time.Hour))
			handler := func(ctx context.Context, invocation cron.Invocation) error { return nil }

			for _, name := range []string{"Hourly", "hourly:rollup", "with space", ""} {
				_, err := cron.New(
					cron.WithStore(cron.NewMemoryStore()),
					cron.WithJob(name, schedule, handler),
				)
				Expect(err).To(MatchError(ContainSubstring("must match")), name)
			}

			_, err := cron.New(
				cron.WithStore(cron.NewMemoryStore()),
				cron.WithJob("hourly", schedule, handler),
				cron.WithJob("hourly", schedule, handler),
			)
			Expect(err).To(MatchError(ContainSubstring("duplicate job")))
		})

		It("rejects options that cannot be represented by the durable store contract", func() {
			handler := func(ctx context.Context, invocation cron.Invocation) error { return nil }
			base := []cron.Option{
				cron.WithStore(cron.NewMemoryStore()),
				cron.WithJob("hourly", cron.Spec(cron.Every(time.Hour)), handler),
			}

			_, err := cron.New(append(base, cron.WithLeaseDuration(time.Nanosecond))...)
			Expect(err).To(MatchError(ContainSubstring("whole number of microseconds")))
			_, err = cron.New(append(base, cron.WithLeaseDuration(time.Microsecond+time.Nanosecond))...)
			Expect(err).To(MatchError(ContainSubstring("whole number of microseconds")))
			_, err = cron.New(append(base, cron.WithCatchUpLimit(1<<31))...)
			Expect(err).To(MatchError(ContainSubstring("catch-up limit")))
		})
	})

	Describe("MemoryStore", func() {
		It("establishes an interval anchor exactly once and reconciles static jobs", func(ctx SpecContext) {
			store := cron.NewMemoryStore()
			startedAt := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
			definition := jobDefinition("rollup", cron.Spec(cron.Every(time.Minute)), cron.CatchUpNone, cron.OverlapSkip)

			states, err := store.Reconcile(ctx, []cron.JobDefinition{definition}, startedAt)
			Expect(err).NotTo(HaveOccurred())
			Expect(states).To(HaveLen(1))
			Expect(states[0].Definition.Schedule.HasAnchor).To(BeTrue())
			Expect(states[0].Definition.Schedule.Anchor).To(Equal(startedAt))
			Expect(states[0].NextRunAt).To(Equal(startedAt.Add(time.Minute)))

			states, err = store.Reconcile(ctx, []cron.JobDefinition{definition}, startedAt.Add(30*time.Second))
			Expect(err).NotTo(HaveOccurred())
			Expect(states[0].Definition.Schedule.Anchor).To(Equal(startedAt))
			Expect(states[0].NextRunAt).To(Equal(startedAt.Add(time.Minute)))

			policyOnly := definition
			policyOnly.CatchUp = cron.CatchUpLatest
			policyOnly.Overlap = cron.OverlapAllow
			states, err = store.Reconcile(ctx, []cron.JobDefinition{policyOnly}, startedAt.Add(40*time.Second))
			Expect(err).NotTo(HaveOccurred())
			Expect(states[0].Definition.CatchUp).To(Equal(cron.CatchUpLatest))
			Expect(states[0].Definition.Overlap).To(Equal(cron.OverlapAllow))
			Expect(states[0].NextRunAt).To(Equal(startedAt.Add(time.Minute)))

			states, err = store.Reconcile(ctx, nil, startedAt.Add(time.Minute))
			Expect(err).NotTo(HaveOccurred())
			Expect(states).To(BeEmpty())

			reenabledAt := startedAt.Add(2 * time.Minute)
			states, err = store.Reconcile(ctx, []cron.JobDefinition{definition}, reenabledAt)
			Expect(err).NotTo(HaveOccurred())
			Expect(states[0].Definition.Schedule.Anchor).To(Equal(reenabledAt))
			Expect(states[0].NextRunAt).To(Equal(reenabledAt.Add(time.Minute)))
		})

		It("uses deterministic occurrence IDs and fenced renewable leases", func(ctx SpecContext) {
			store := cron.NewMemoryStore()
			anchor := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
			schedule := cron.Spec(cron.Every(time.Minute)).Anchor(anchor)
			definition := jobDefinition("rollup", schedule, cron.CatchUpAll, cron.OverlapSkip)
			states, err := store.Reconcile(ctx, []cron.JobDefinition{definition}, anchor)
			Expect(err).NotTo(HaveOccurred())

			scheduledAt := states[0].NextRunAt
			nextRunAt, err := schedule.Next(scheduledAt)
			Expect(err).NotTo(HaveOccurred())
			occurrenceID := cron.OccurrenceID("rollup", scheduledAt)
			Expect(occurrenceID).To(Equal(cron.OccurrenceID("rollup", scheduledAt.In(time.FixedZone("elsewhere", 3600)))))
			Expect(occurrenceID).NotTo(Equal(cron.OccurrenceID("rollup", nextRunAt)))
			invalid := claim("rollup", scheduledAt, nextRunAt.Add(time.Minute), "invalid", scheduledAt.Add(time.Second), time.Minute)
			_, err = store.Claim(ctx, invalid)
			Expect(errors.Is(err, cron.ErrOccurrenceConflict)).To(BeTrue())
			subMicrosecond := claim("rollup", scheduledAt, nextRunAt, "too-short", scheduledAt.Add(time.Second), time.Nanosecond)
			_, err = store.Claim(ctx, subMicrosecond)
			Expect(errors.Is(err, cron.ErrInvalidConfiguration)).To(BeTrue())

			claimedAt := scheduledAt.Add(time.Second)
			first, err := store.Claim(ctx, cron.ClaimRequest{
				OccurrenceID: occurrenceID,
				JobName:      "rollup",
				ScheduledAt:  scheduledAt,
				NextRunAt:    nextRunAt,
				LeaseOwner:   "runner_a",
				LeaseToken:   "token_a",
				ClaimedAt:    claimedAt,
				LeaseUntil:   claimedAt.Add(time.Minute),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(first.Disposition).To(Equal(cron.ClaimAcquired))
			Expect(first.Occurrence.Attempt).To(Equal(uint32(1)))
			Expect(store.Renew(ctx, cron.LeaseRenewal{
				OccurrenceID: occurrenceID,
				LeaseToken:   "token_a",
				RenewedAt:    claimedAt.Add(time.Second),
				LeaseUntil:   claimedAt.Add(time.Second + time.Nanosecond),
			})).To(MatchError(ContainSubstring("invalid lease renewal")))

			held, err := store.Claim(ctx, cron.ClaimRequest{
				OccurrenceID: occurrenceID,
				JobName:      "rollup",
				ScheduledAt:  scheduledAt,
				NextRunAt:    nextRunAt,
				LeaseOwner:   "runner_b",
				LeaseToken:   "token_b",
				ClaimedAt:    claimedAt.Add(time.Second),
				LeaseUntil:   claimedAt.Add(2 * time.Minute),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(held.Disposition).To(Equal(cron.ClaimLeaseHeld))

			recoveryAt := claimedAt.Add(time.Minute + time.Second)
			states, err = store.Reconcile(ctx, []cron.JobDefinition{definition}, recoveryAt)
			Expect(err).NotTo(HaveOccurred())
			Expect(states[0].NextRunAt).To(Equal(nextRunAt))
			expired, err := store.Expired(ctx, recoveryAt, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(expired).To(HaveLen(1))
			Expect(expired[0].ID).To(Equal(occurrenceID))

			recovered, err := store.Claim(ctx, cron.ClaimRequest{
				OccurrenceID: occurrenceID,
				JobName:      "rollup",
				ScheduledAt:  scheduledAt,
				NextRunAt:    nextRunAt,
				LeaseOwner:   "runner_b",
				LeaseToken:   "token_b",
				ClaimedAt:    recoveryAt,
				LeaseUntil:   recoveryAt.Add(time.Minute),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(recovered.Disposition).To(Equal(cron.ClaimAcquired))
			Expect(recovered.Occurrence.Attempt).To(Equal(uint32(2)))

			Expect(store.Complete(ctx, cron.Completion{
				OccurrenceID: occurrenceID,
				LeaseToken:   "token_a",
				Status:       cron.OccurrenceSucceeded,
				FinishedAt:   recoveryAt.Add(time.Second),
			})).To(MatchError(cron.ErrLeaseLost))
			Expect(store.Complete(ctx, cron.Completion{
				OccurrenceID: occurrenceID,
				LeaseToken:   "token_b",
				Status:       cron.OccurrenceSucceeded,
				FinishedAt:   recoveryAt.Add(time.Second),
				Error:        "successful runs cannot carry errors",
			})).To(MatchError(ContainSubstring("invalid completion")))
			Expect(store.Complete(ctx, cron.Completion{
				OccurrenceID: occurrenceID,
				LeaseToken:   "token_b",
				Status:       cron.OccurrenceSucceeded,
				FinishedAt:   recoveryAt.Add(time.Second),
			})).To(Succeed())

			snapshot, err := store.Snapshot(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(snapshot.Occurrences).To(HaveLen(1))
			Expect(snapshot.Occurrences[0].Status).To(Equal(cron.OccurrenceSucceeded))
			Expect(snapshot.Occurrences[0].LeaseOwner).To(BeEmpty())
			Expect(snapshot.Occurrences[0].LeaseUntil.IsZero()).To(BeTrue())
		})

		It("renews only a live MemoryStore lease with its current fencing token", func(ctx SpecContext) {
			store := cron.NewMemoryStore()
			anchor := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
			schedule := cron.Spec(cron.Every(time.Minute)).Anchor(anchor)
			definition := jobDefinition("renewable", schedule, cron.CatchUpAll, cron.OverlapSkip)
			states, err := store.Reconcile(ctx, []cron.JobDefinition{definition}, anchor)
			Expect(err).NotTo(HaveOccurred())

			scheduledAt := states[0].NextRunAt
			nextRunAt, err := schedule.Next(scheduledAt)
			Expect(err).NotTo(HaveOccurred())
			claimedAt := scheduledAt.Add(time.Second)
			request := claim("renewable", scheduledAt, nextRunAt, "current-token", claimedAt, time.Minute)
			_, err = store.Claim(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			renewedAt := claimedAt.Add(10 * time.Second)
			leaseUntil := claimedAt.Add(2 * time.Minute)
			Expect(store.Renew(ctx, cron.LeaseRenewal{
				OccurrenceID: "missing",
				LeaseToken:   request.LeaseToken,
				RenewedAt:    renewedAt,
				LeaseUntil:   leaseUntil,
			})).To(MatchError(cron.ErrLeaseLost))
			Expect(store.Renew(ctx, cron.LeaseRenewal{
				OccurrenceID: request.OccurrenceID,
				LeaseToken:   "stale-token",
				RenewedAt:    renewedAt,
				LeaseUntil:   leaseUntil,
			})).To(MatchError(cron.ErrLeaseLost))
			Expect(store.Renew(ctx, cron.LeaseRenewal{
				OccurrenceID: request.OccurrenceID,
				LeaseToken:   request.LeaseToken,
				RenewedAt:    request.LeaseUntil,
				LeaseUntil:   request.LeaseUntil.Add(time.Minute),
			})).To(MatchError(cron.ErrLeaseLost))
			Expect(store.Renew(ctx, cron.LeaseRenewal{
				OccurrenceID: request.OccurrenceID,
				LeaseToken:   request.LeaseToken,
				RenewedAt:    renewedAt,
				LeaseUntil:   leaseUntil,
			})).To(Succeed())

			snapshot, err := store.Snapshot(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(snapshot.Occurrences).To(ContainElement(And(
				HaveField("ID", request.OccurrenceID),
				HaveField("LeaseUntil", leaseUntil),
			)))
		})

		It("records fresh and expired MemoryStore skips while fencing live work", func(ctx SpecContext) {
			store := cron.NewMemoryStore()
			anchor := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
			schedule := cron.Spec(cron.Every(time.Minute)).Anchor(anchor)
			definitions := []cron.JobDefinition{
				jobDefinition("fresh-skip", schedule, cron.CatchUpNone, cron.OverlapSkip),
				jobDefinition("expired-skip", schedule, cron.CatchUpNone, cron.OverlapSkip),
			}
			states, err := store.Reconcile(ctx, definitions, anchor)
			Expect(err).NotTo(HaveOccurred())
			Expect(states).To(HaveLen(2))

			freshAt := states[1].NextRunAt
			freshNext, err := schedule.Next(freshAt)
			Expect(err).NotTo(HaveOccurred())
			fresh := cron.SkipRequest{
				OccurrenceID: cron.OccurrenceID("fresh-skip", freshAt),
				JobName:      "fresh-skip",
				ScheduledAt:  freshAt,
				NextRunAt:    freshNext,
				SkippedAt:    freshAt.Add(time.Second),
				Reason:       "catch-up disabled",
			}
			invalid := fresh
			invalid.Reason = ""
			Expect(store.Skip(ctx, invalid)).To(MatchError(ContainSubstring("invalid occurrence skip")))
			missing := fresh
			missing.JobName = "missing"
			missing.OccurrenceID = cron.OccurrenceID(missing.JobName, missing.ScheduledAt)
			Expect(store.Skip(ctx, missing)).To(MatchError(ContainSubstring("job not found")))
			Expect(store.Skip(ctx, fresh)).To(Succeed())
			Expect(store.Skip(ctx, fresh)).To(Succeed())

			expiredState := states[0]
			expiredAt := expiredState.NextRunAt
			expiredNext, err := schedule.Next(expiredAt)
			Expect(err).NotTo(HaveOccurred())
			claimedAt := expiredAt.Add(time.Second)
			claimRequest := claim("expired-skip", expiredAt, expiredNext, "expiring", claimedAt, time.Minute)
			_, err = store.Claim(ctx, claimRequest)
			Expect(err).NotTo(HaveOccurred())
			expired := cron.SkipRequest{
				OccurrenceID: claimRequest.OccurrenceID,
				JobName:      claimRequest.JobName,
				ScheduledAt:  claimRequest.ScheduledAt,
				NextRunAt:    claimRequest.NextRunAt,
				SkippedAt:    claimedAt.Add(30 * time.Second),
				Reason:       "recovery disabled",
			}
			Expect(store.Skip(ctx, expired)).To(MatchError(cron.ErrOccurrenceRunning))
			expired.SkippedAt = claimRequest.LeaseUntil.Add(time.Second)
			Expect(store.Skip(ctx, expired)).To(Succeed())

			snapshot, err := store.Snapshot(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(snapshot.Occurrences).To(ConsistOf(
				And(HaveField("ID", fresh.OccurrenceID), HaveField("Status", cron.OccurrenceSkipped), HaveField("SkipReason", fresh.Reason)),
				And(HaveField("ID", expired.OccurrenceID), HaveField("Status", cron.OccurrenceSkipped), HaveField("SkipReason", expired.Reason)),
			))
		})

		It("keeps terminal MemoryStore skip replays from touching an advanced cursor", func(ctx SpecContext) {
			store := cron.NewMemoryStore()
			anchor := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
			schedule := cron.Spec(cron.Every(time.Minute)).Anchor(anchor)
			definition := jobDefinition("stale-skip", schedule, cron.CatchUpNone, cron.OverlapSkip)
			states, err := store.Reconcile(ctx, []cron.JobDefinition{definition}, anchor)
			Expect(err).NotTo(HaveOccurred())

			firstAt := states[0].NextRunAt
			secondAt, err := schedule.Next(firstAt)
			Expect(err).NotTo(HaveOccurred())
			thirdAt, err := schedule.Next(secondAt)
			Expect(err).NotTo(HaveOccurred())
			first := cron.SkipRequest{
				OccurrenceID: cron.OccurrenceID(definition.Name, firstAt),
				JobName:      definition.Name,
				ScheduledAt:  firstAt,
				NextRunAt:    secondAt,
				SkippedAt:    firstAt.Add(time.Second),
				Reason:       "catch-up disabled",
			}
			second := cron.SkipRequest{
				OccurrenceID: cron.OccurrenceID(definition.Name, secondAt),
				JobName:      definition.Name,
				ScheduledAt:  secondAt,
				NextRunAt:    thirdAt,
				SkippedAt:    secondAt.Add(time.Second),
				Reason:       "catch-up disabled",
			}
			Expect(store.Skip(ctx, first)).To(Succeed())
			Expect(store.Skip(ctx, second)).To(Succeed())

			advanced, err := store.Snapshot(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(store.Skip(ctx, first)).To(Succeed())
			replayed, err := store.Snapshot(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(replayed).To(Equal(advanced))

			reconfigured := jobDefinition(
				definition.Name,
				cron.Spec(cron.Daily(cron.Noon())),
				cron.CatchUpNone,
				cron.OverlapSkip,
			)
			_, err = store.Reconcile(ctx, []cron.JobDefinition{reconfigured}, second.SkippedAt.Add(time.Second))
			Expect(err).NotTo(HaveOccurred())
			beforeStaleReplay, err := store.Snapshot(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(store.Skip(ctx, first)).To(Succeed())
			afterStaleReplay, err := store.Snapshot(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(afterStaleReplay).To(Equal(beforeStaleReplay))
		})

		It("enforces no-overlap in the store shared by scheduler replicas", func(ctx SpecContext) {
			store := cron.NewMemoryStore()
			anchor := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
			schedule := cron.Spec(cron.Every(time.Minute)).Anchor(anchor)
			definition := jobDefinition("rollup", schedule, cron.CatchUpAll, cron.OverlapSkip)
			states, err := store.Reconcile(ctx, []cron.JobDefinition{definition}, anchor)
			Expect(err).NotTo(HaveOccurred())

			firstAt := states[0].NextRunAt
			secondAt, err := schedule.Next(firstAt)
			Expect(err).NotTo(HaveOccurred())
			thirdAt, err := schedule.Next(secondAt)
			Expect(err).NotTo(HaveOccurred())
			claimedAt := secondAt.Add(time.Second)
			_, err = store.Claim(ctx, claim("rollup", firstAt, secondAt, "first", claimedAt, time.Minute))
			Expect(err).NotTo(HaveOccurred())

			second, err := store.Claim(ctx, claim("rollup", secondAt, thirdAt, "second", claimedAt, time.Minute))
			Expect(err).NotTo(HaveOccurred())
			Expect(second.Disposition).To(Equal(cron.ClaimSkippedOverlap))
			Expect(second.Occurrence.Status).To(Equal(cron.OccurrenceSkipped))
			Expect(second.Occurrence.SkipReason).To(Equal("overlap"))
		})

		It("leaves an expired occurrence recoverable while a different occurrence owns the no-overlap lease", func(ctx SpecContext) {
			store := cron.NewMemoryStore()
			anchor := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
			schedule := cron.Spec(cron.Every(time.Minute)).Anchor(anchor)
			definition := jobDefinition("serial", schedule, cron.CatchUpAll, cron.OverlapSkip)
			states, err := store.Reconcile(ctx, []cron.JobDefinition{definition}, anchor)
			Expect(err).NotTo(HaveOccurred())
			firstAt := states[0].NextRunAt
			secondAt, err := schedule.Next(firstAt)
			Expect(err).NotTo(HaveOccurred())
			thirdAt, err := schedule.Next(secondAt)
			Expect(err).NotTo(HaveOccurred())

			firstClaimedAt := firstAt.Add(time.Second)
			_, err = store.Claim(ctx, claim("serial", firstAt, secondAt, "first", firstClaimedAt, time.Second))
			Expect(err).NotTo(HaveOccurred())
			secondClaimedAt := firstClaimedAt.Add(2 * time.Second)
			second, err := store.Claim(ctx, claim("serial", secondAt, thirdAt, "second", secondClaimedAt, time.Minute))
			Expect(err).NotTo(HaveOccurred())
			Expect(second.Disposition).To(Equal(cron.ClaimAcquired))

			recovery := claim("serial", firstAt, secondAt, "recovery", secondClaimedAt.Add(time.Second), time.Minute)
			result, err := store.Claim(ctx, recovery)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Disposition).To(Equal(cron.ClaimLeaseHeld))
			expired, err := store.Expired(ctx, recovery.ClaimedAt, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(expired).To(ContainElement(HaveField("ID", cron.OccurrenceID("serial", firstAt))))
		})
	})

	Describe("service lifecycle", func() {
		It("runs jobs, records job errors without failing Run, and shuts down cleanly", func() {
			store := cron.NewMemoryStore()
			invoked := make(chan cron.Invocation, 4)
			service, err := cron.New(
				cron.WithStore(store),
				cron.WithLeaseDuration(300*time.Millisecond),
				cron.WithJob("failing", cron.Spec(cron.Every(20*time.Millisecond)), func(_ context.Context, invocation cron.Invocation) error {
					select {
					case invoked <- invocation:
					default:
					}
					return errors.New("expected job failure")
				}),
			)
			Expect(err).NotTo(HaveOccurred())

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- service.Run(ctx) }()

			var invocation cron.Invocation
			Eventually(invoked).Should(Receive(&invocation))
			Eventually(func() cron.OccurrenceStatus {
				return occurrenceStatus(store, invocation.ID)
			}).Should(Equal(cron.OccurrenceFailed))
			Consistently(done, 20*time.Millisecond).ShouldNot(Receive())

			cancel()
			Eventually(done).Should(Receive(BeNil()))
		})

		It("recovers a panic as a failed occurrence", func() {
			store := cron.NewMemoryStore()
			invoked := make(chan cron.Invocation, 4)
			service, err := cron.New(
				cron.WithStore(store),
				cron.WithJob("panicking", cron.Spec(cron.Every(20*time.Millisecond)), func(_ context.Context, invocation cron.Invocation) error {
					select {
					case invoked <- invocation:
					default:
					}
					panic("boom")
				}),
			)
			Expect(err).NotTo(HaveOccurred())

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- service.Run(ctx) }()
			var invocation cron.Invocation
			Eventually(invoked).Should(Receive(&invocation))
			Eventually(func() string {
				return occurrenceError(store, invocation.ID)
			}).Should(ContainSubstring("job panic: boom"))

			cancel()
			Eventually(done).Should(Receive(BeNil()))
		})

		It("cancels and drains an in-flight handler before Run returns", func() {
			store := cron.NewMemoryStore()
			started := make(chan cron.Invocation, 1)
			exited := make(chan struct{})
			service, err := cron.New(
				cron.WithStore(store),
				cron.WithJob("blocking", cron.Spec(cron.Every(20*time.Millisecond)), func(ctx context.Context, invocation cron.Invocation) error {
					started <- invocation
					<-ctx.Done()
					close(exited)
					return ctx.Err()
				}),
			)
			Expect(err).NotTo(HaveOccurred())

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- service.Run(ctx) }()
			var invocation cron.Invocation
			Eventually(started).Should(Receive(&invocation))
			cancel()
			Eventually(exited).Should(BeClosed())
			Eventually(done).Should(Receive(BeNil()))
			Expect(occurrenceStatus(store, invocation.ID)).To(Equal(cron.OccurrenceCanceled))
		})

		It("does not fail Run when handler completion races a lease-renewal tick", func() {
			memory := cron.NewMemoryStore()
			anchor := time.Now().UTC().Add(-time.Hour - 20*time.Millisecond)
			schedule := cron.Spec(cron.Every(time.Hour)).Anchor(anchor)
			definition := jobDefinition("renew-race", schedule, cron.CatchUpLatest, cron.OverlapSkip)
			_, err := memory.Reconcile(context.Background(), []cron.JobDefinition{definition}, anchor)
			Expect(err).NotTo(HaveOccurred())

			store := &cancelingRenewStore{IStore: memory, started: make(chan struct{})}
			invoked := make(chan cron.Invocation, 1)
			service, err := cron.New(
				cron.WithStore(store),
				cron.WithLeaseDuration(300*time.Millisecond),
				cron.WithJob("renew-race", schedule, func(_ context.Context, invocation cron.Invocation) error {
					invoked <- invocation
					<-store.started
					return nil
				}, cron.WithCatchUp(cron.CatchUpLatest)),
			)
			Expect(err).NotTo(HaveOccurred())

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- service.Run(ctx) }()
			var invocation cron.Invocation
			Eventually(invoked).Should(Receive(&invocation))
			Eventually(func() cron.OccurrenceStatus {
				return occurrenceStatus(memory, invocation.ID)
			}).Should(Equal(cron.OccurrenceSucceeded))
			Consistently(done, 50*time.Millisecond).ShouldNot(Receive())

			cancel()
			Eventually(done).Should(Receive(BeNil()))
		})

		It("reclaims another replica's expired occurrence without restarting", func() {
			store := cron.NewMemoryStore()
			anchor := time.Now().UTC().Add(-time.Hour - 100*time.Millisecond)
			schedule := cron.Spec(cron.Every(time.Hour)).Anchor(anchor)
			definition := jobDefinition("survivor", schedule, cron.CatchUpNone, cron.OverlapSkip)
			states, err := store.Reconcile(context.Background(), []cron.JobDefinition{definition}, anchor)
			Expect(err).NotTo(HaveOccurred())
			scheduledAt := states[0].NextRunAt
			nextRunAt, err := schedule.Next(scheduledAt)
			Expect(err).NotTo(HaveOccurred())
			claimedAt := time.Now().UTC()
			first, err := store.Claim(context.Background(), claim("survivor", scheduledAt, nextRunAt, "replica_a", claimedAt, 70*time.Millisecond))
			Expect(err).NotTo(HaveOccurred())
			Expect(first.Disposition).To(Equal(cron.ClaimAcquired))

			invoked := make(chan cron.Invocation, 1)
			service, err := cron.New(
				cron.WithStore(store),
				cron.WithLeaseDuration(90*time.Millisecond),
				cron.WithLeaseOwner("replica_b"),
				cron.WithJob("survivor", schedule, func(_ context.Context, invocation cron.Invocation) error {
					invoked <- invocation
					return nil
				}),
			)
			Expect(err).NotTo(HaveOccurred())
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- service.Run(ctx) }()

			var recovered cron.Invocation
			Eventually(invoked).Should(Receive(&recovered))
			Expect(recovered.ID).To(Equal(first.Occurrence.ID))
			Expect(recovered.Attempt).To(Equal(uint32(2)))
			cancel()
			Eventually(done).Should(Receive(BeNil()))
			Expect(occurrenceStatus(store, recovered.ID)).To(Equal(cron.OccurrenceSucceeded))
		})

		It("applies startup catch-up policies to durable overdue cursors", func() {
			for _, testCase := range []struct {
				name        string
				policy      cron.CatchUpPolicy
				overlap     cron.OverlapPolicy
				wantInvoked int32
			}{
				{name: "none", policy: cron.CatchUpNone, overlap: cron.OverlapSkip, wantInvoked: 0},
				{name: "latest", policy: cron.CatchUpLatest, overlap: cron.OverlapSkip, wantInvoked: 1},
				{name: "all", policy: cron.CatchUpAll, overlap: cron.OverlapAllow, wantInvoked: 3},
			} {
				By(testCase.name)
				store := cron.NewMemoryStore()
				anchor := time.Now().UTC().Add(-3*time.Hour - 500*time.Millisecond)
				schedule := cron.Spec(cron.Every(time.Hour)).Anchor(anchor)
				definition := jobDefinition("catchup", schedule, testCase.policy, testCase.overlap)
				_, err := store.Reconcile(context.Background(), []cron.JobDefinition{definition}, anchor)
				Expect(err).NotTo(HaveOccurred())

				var invoked atomic.Int32
				service, err := cron.New(
					cron.WithStore(store),
					cron.WithJob("catchup", schedule, func(ctx context.Context, invocation cron.Invocation) error {
						invoked.Add(1)
						return nil
					}, cron.WithCatchUp(testCase.policy), cron.WithOverlap(testCase.overlap)),
				)
				Expect(err).NotTo(HaveOccurred())
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() { done <- service.Run(ctx) }()
				if testCase.wantInvoked == 0 {
					Consistently(invoked.Load, 50*time.Millisecond).Should(Equal(int32(0)))
				} else {
					Eventually(invoked.Load).Should(Equal(testCase.wantInvoked))
				}
				cancel()
				Eventually(done).Should(Receive(BeNil()))
			}
		})

		It("fast-forwards more than one catch-up batch without running a disabled startup occurrence", func() {
			store := cron.NewMemoryStore()
			anchor := time.Now().UTC().Add(-1001*time.Minute - 30*time.Second)
			schedule := cron.Spec(cron.Every(time.Minute)).Anchor(anchor)
			definition := jobDefinition("large_backlog", schedule, cron.CatchUpNone, cron.OverlapSkip)
			_, err := store.Reconcile(context.Background(), []cron.JobDefinition{definition}, anchor)
			Expect(err).NotTo(HaveOccurred())

			var invoked atomic.Int32
			service, err := cron.New(
				cron.WithStore(store),
				cron.WithJob("large_backlog", schedule, func(ctx context.Context, invocation cron.Invocation) error {
					invoked.Add(1)
					return nil
				}, cron.WithCatchUp(cron.CatchUpNone)),
			)
			Expect(err).NotTo(HaveOccurred())
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- service.Run(ctx) }()

			Eventually(func() bool {
				snapshot, snapshotErr := store.Snapshot(context.Background())
				Expect(snapshotErr).NotTo(HaveOccurred())
				return snapshot.Jobs[0].NextRunAt.After(time.Now())
			}, 3*time.Second).Should(BeTrue())
			snapshot, err := store.Snapshot(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(snapshot.Occurrences).To(HaveLen(cron.SnapshotOccurrenceLimit))
			Expect(invoked.Load()).To(Equal(int32(0)))

			cancel()
			Eventually(done).Should(Receive(BeNil()))
		})
	})

	It("mounts only a read-only Gin snapshot route", func(ctx SpecContext) {
		gin.SetMode(gin.TestMode)
		store := cron.NewMemoryStore()
		service, err := cron.New(
			cron.WithStore(store),
			cron.WithJob("status", cron.Spec(cron.Every(time.Hour)), func(ctx context.Context, invocation cron.Invocation) error { return nil }),
		)
		Expect(err).NotTo(HaveOccurred())
		router := gin.New()
		service.Register(router.Group("/internal"))

		response := httptest.NewRecorder()
		request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/internal/cron", nil)
		router.ServeHTTP(response, request)
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Header().Get("Cache-Control")).To(Equal("no-store"))
		Expect(response.Body.String()).To(ContainSubstring(`"running":false`))

		response = httptest.NewRecorder()
		request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/internal/cron", nil)
		router.ServeHTTP(response, request)
		Expect(response.Code).To(Equal(http.StatusNotFound))
	})
})

func jobDefinition(
	name string,
	schedule cron.Schedule,
	catchUp cron.CatchUpPolicy,
	overlap cron.OverlapPolicy,
) cron.JobDefinition {
	definition, err := schedule.Definition()
	Expect(err).NotTo(HaveOccurred())
	return cron.JobDefinition{Name: name, Schedule: definition, CatchUp: catchUp, Overlap: overlap}
}

func claim(
	jobName string,
	scheduledAt time.Time,
	nextRunAt time.Time,
	token string,
	claimedAt time.Time,
	duration time.Duration,
) cron.ClaimRequest {
	return cron.ClaimRequest{
		OccurrenceID: cron.OccurrenceID(jobName, scheduledAt),
		JobName:      jobName,
		ScheduledAt:  scheduledAt,
		NextRunAt:    nextRunAt,
		LeaseOwner:   "runner",
		LeaseToken:   token,
		ClaimedAt:    claimedAt,
		LeaseUntil:   claimedAt.Add(duration),
	}
}

func occurrenceStatus(store *cron.MemoryStore, occurrenceID string) cron.OccurrenceStatus {
	snapshot, err := store.Snapshot(context.Background())
	Expect(err).NotTo(HaveOccurred())
	for _, occurrence := range snapshot.Occurrences {
		if occurrence.ID == occurrenceID {
			return occurrence.Status
		}
	}
	return ""
}

func occurrenceError(store *cron.MemoryStore, occurrenceID string) string {
	snapshot, err := store.Snapshot(context.Background())
	Expect(err).NotTo(HaveOccurred())
	for _, occurrence := range snapshot.Occurrences {
		if occurrence.ID == occurrenceID {
			return occurrence.Error
		}
	}
	return ""
}

type cancelingRenewStore struct {
	cron.IStore
	started chan struct{}
	once    sync.Once
}

func (store *cancelingRenewStore) Renew(ctx context.Context, _ cron.LeaseRenewal) error {
	store.once.Do(func() { close(store.started) })
	<-ctx.Done()
	return ctx.Err()
}
