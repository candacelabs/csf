// Copyright 2026 Candace Labs

package cron_test

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/ipc/clock"
	"github.com/candacelabs/csf/ipc/db/csfpg"
	grammar "github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/runtime"
	cron "github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/cron/crontest"
)

// The scheduler specs mount the service into a host runtime, grant it a
// manual clock and the store on pgmem, and move time themselves: no spec
// sleeps, and every wait is a bounded await on observable state.
var (
	start     = time.Date(2026, time.October, 2, 9, 0, 0, 0, time.UTC)
	runBudget = eventually.Budget{Within: 10 * time.Second, Interval: 10 * time.Millisecond}
	// longLease outlives every advance a spec makes, so an occurrence in
	// flight is never mistaken for an abandoned one.
	longLease = 10 * time.Minute
)

// host runs one scheduler in a host runtime of its own. shutdown cancels
// the root context and returns what Run returned once every service joined;
// the first call does the work and every later one repeats its answer.
type host struct {
	done chan error
	stop context.CancelFunc
	once sync.Once
	err  error
}

func mountScheduler(scheduler *cron.Scheduler) *host {
	GinkgoHelper()
	hostRuntime, err := runtime.NewHostRuntime(runtime.WithHostName("cron-spec"))
	Expect(err).NotTo(HaveOccurred())
	Expect(hostRuntime.Mount("cron", scheduler)).To(Succeed())
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- hostRuntime.Run(ctx) }()
	mounted := &host{done: done, stop: stop}
	DeferCleanup(func() { Expect(mounted.shutdown()).To(Succeed()) })
	return mounted
}

func (mounted *host) shutdown() error {
	GinkgoHelper()
	mounted.once.Do(func() {
		mounted.stop()
		Eventually(mounted.done).WithTimeout(runBudget.Within).Should(Receive(&mounted.err))
	})
	return mounted.err
}

func awaitWaits(manual *clock.ManualClock, count int) {
	GinkgoHelper()
	eventually.Await(GinkgoTB(), "the scheduler to arm its waits", runBudget, manual.Waiting, func(waiting int) bool { return waiting == count })
}

func awaitOccurrence(store cron.IStore, occurrenceID string, status grammar.OccurrenceStatus) grammar.OccurrenceRecord {
	GinkgoHelper()
	return eventually.Await(GinkgoTB(), "occurrence "+occurrenceID+" to be "+string(status), runBudget,
		func() grammar.OccurrenceRecord {
			snapshot, err := store.Snapshot(context.Background())
			if err != nil {
				return grammar.OccurrenceRecord{}
			}
			for _, occurrence := range snapshot.Occurrences {
				if occurrence.ID == occurrenceID {
					return occurrence
				}
			}
			return grammar.OccurrenceRecord{}
		},
		func(occurrence grammar.OccurrenceRecord) bool { return occurrence.Status == status })
}

func occurrenceStatuses(ctx context.Context, store cron.IStore) map[grammar.OccurrenceStatus]int {
	GinkgoHelper()
	counts := map[grammar.OccurrenceStatus]int{}
	for _, occurrence := range snapshotOf(ctx, store).Occurrences {
		counts[occurrence.Status]++
	}
	return counts
}

var _ = Describe("the cron service in a host runtime", func() {
	var (
		ctx    context.Context
		store  *crontest.MemoryStore
		manual *clock.ManualClock
		every  grammar.Schedule
	)

	BeforeEach(func() {
		ctx = context.Background()
		store = crontest.OpenStore(GinkgoT())
		manual = clock.NewManualClock(start)
		every = grammar.Spec(grammar.Every(time.Minute)).Anchor(start)
	})

	It("fires the declared trigger on its schedule and records the occurrence through csfpg", func() {
		fired := make(chan cron.Occurrence, 1)
		scheduler, err := cron.NewScheduler(
			cron.WithStore(store), cron.WithClock(manual), cron.WithLeaseDuration(longLease),
			cron.WithTrigger(rollup, every, func(_ context.Context, occurrence cron.Occurrence) error {
				fired <- occurrence
				return nil
			}),
		)
		Expect(err).NotTo(HaveOccurred())
		mounted := mountScheduler(scheduler)

		awaitWaits(manual, 1)
		Consistently(fired).ShouldNot(Receive(), "nothing fires before its instant")
		Expect(occurrenceStatuses(ctx, store)).To(BeEmpty())

		manual.Advance(time.Minute)
		var occurrence cron.Occurrence
		Eventually(fired).WithTimeout(runBudget.Within).Should(Receive(&occurrence))
		Expect(occurrence.TriggerName).To(Equal(rollup))
		Expect(occurrence.ScheduledAt).To(Equal(start.Add(time.Minute)))
		Expect(occurrence.StartedAt).To(Equal(start.Add(time.Minute)))
		Expect(occurrence.Attempt).To(Equal(uint32(1)))
		Expect(occurrence.ID).To(Equal(grammar.OccurrenceID(rollup, start.Add(time.Minute))))

		recorded := awaitOccurrence(store, occurrence.ID, grammar.OccurrenceSucceeded)
		Expect(recorded.FinishedAt).To(Equal(start.Add(time.Minute)))
		row, err := csfpg.New(store.Database()).GetCronOccurrence(ctx, occurrence.ID)
		Expect(err).NotTo(HaveOccurred(), "the record is a row of csf_cron_occurrences")
		Expect(row.Status).To(Equal(string(grammar.OccurrenceSucceeded)))
		Expect(row.LeaseToken).To(BeNil(), "a finished occurrence holds no lease")
		Expect(snapshotOf(ctx, store).Triggers[0].NextRunAt).To(Equal(start.Add(2 * time.Minute)))

		Expect(mounted.shutdown()).To(Succeed())
	})

	It("rejects a duplicate trigger name, an invalid schedule and a missing store before mounting", func() {
		operation := func(_ context.Context, _ cron.Occurrence) error { return nil }
		_, err := cron.NewScheduler(cron.WithStore(store),
			cron.WithTrigger(rollup, every, operation), cron.WithTrigger(rollup, every, operation))
		Expect(err).To(MatchError(cron.ErrInvalidConfiguration))
		Expect(err).To(MatchError(ContainSubstring("duplicate trigger")))

		_, err = cron.NewScheduler(cron.WithStore(store),
			cron.WithTrigger(rollup, grammar.Spec(grammar.Daily(grammar.At(13).AM())), operation))
		Expect(err).To(MatchError(cron.ErrInvalidConfiguration))
		Expect(err).To(MatchError(ContainSubstring("schedule")))

		_, err = cron.NewScheduler(cron.WithStore(store), cron.WithTrigger("Rollup", every, operation))
		Expect(err).To(MatchError(grammar.ErrInvalidTrigger))

		_, err = cron.NewScheduler(cron.WithTrigger(rollup, every, operation))
		Expect(err).To(MatchError(cron.ErrStoreRequired))
		_, err = cron.NewScheduler(cron.WithStore(store))
		Expect(err).To(MatchError(cron.ErrNoTriggers))
	})

	It("skips the next occurrence while one still runs under OverlapSkip", func() {
		release := make(chan struct{})
		started := make(chan cron.Occurrence, 2)
		scheduler, err := cron.NewScheduler(
			cron.WithStore(store), cron.WithClock(manual), cron.WithLeaseDuration(longLease),
			cron.WithTrigger(rollup, every, func(ctx context.Context, occurrence cron.Occurrence) error {
				started <- occurrence
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}, cron.WithOverlap(grammar.OverlapSkip)),
		)
		Expect(err).NotTo(HaveOccurred())
		mounted := mountScheduler(scheduler)

		awaitWaits(manual, 1)
		manual.Advance(time.Minute)
		var first cron.Occurrence
		Eventually(started).WithTimeout(runBudget.Within).Should(Receive(&first))
		awaitOccurrence(store, first.ID, grammar.OccurrenceRunning)
		awaitWaits(manual, 2) // the next occurrence's wait and the lease renewal

		manual.Advance(time.Minute)
		secondID := grammar.OccurrenceID(rollup, start.Add(2*time.Minute))
		skipped := awaitOccurrence(store, secondID, grammar.OccurrenceSkipped)
		Expect(skipped.SkipReason).To(Equal("overlap"))
		Consistently(started).ShouldNot(Receive(), "the skipped occurrence never invokes the operation")

		close(release)
		awaitOccurrence(store, first.ID, grammar.OccurrenceSucceeded)
		Expect(mounted.shutdown()).To(Succeed())
	})

	DescribeTable("catches up after downtime by the trigger's policy",
		func(policy grammar.CatchUpPolicy, wantInvoked int, wantSkipped int) {
			// The store already holds the trigger from an earlier run whose
			// cursor stopped at the first occurrence; three are overdue when
			// the service starts again.
			_, err := store.Reconcile(ctx, []grammar.TriggerDefinition{
				triggerDefinition(rollup, every, policy, grammar.OverlapAllow),
			}, start)
			Expect(err).NotTo(HaveOccurred())
			manual.Advance(3*time.Minute + 30*time.Second)

			invoked := make(chan cron.Occurrence, 3)
			scheduler, err := cron.NewScheduler(
				cron.WithStore(store), cron.WithClock(manual), cron.WithLeaseDuration(longLease),
				cron.WithTrigger(rollup, every, func(_ context.Context, occurrence cron.Occurrence) error {
					invoked <- occurrence
					return nil
				}, cron.WithCatchUp(policy), cron.WithOverlap(grammar.OverlapAllow)),
			)
			Expect(err).NotTo(HaveOccurred())
			mounted := mountScheduler(scheduler)

			eventually.Await(GinkgoTB(), "every overdue occurrence to be recorded", runBudget,
				func() int {
					counts := occurrenceStatuses(ctx, store)
					return counts[grammar.OccurrenceSucceeded] + counts[grammar.OccurrenceSkipped]
				},
				func(recorded int) bool { return recorded == 3 })
			counts := occurrenceStatuses(ctx, store)
			Expect(counts[grammar.OccurrenceSucceeded]).To(Equal(wantInvoked))
			Expect(counts[grammar.OccurrenceSkipped]).To(Equal(wantSkipped))
			Expect(invoked).To(HaveLen(wantInvoked))
			if policy == grammar.CatchUpLatest {
				Expect(<-invoked).To(HaveField("ScheduledAt", start.Add(3*time.Minute)), "only the latest ran")
			}
			Expect(snapshotOf(ctx, store).Triggers[0].NextRunAt).To(Equal(start.Add(4 * time.Minute)))
			Expect(mounted.shutdown()).To(Succeed())
		},
		Entry("none skips every missed occurrence", grammar.CatchUpNone, 0, 3),
		Entry("latest runs the most recent one", grammar.CatchUpLatest, 1, 2),
		Entry("all runs every one", grammar.CatchUpAll, 3, 0),
	)

	It("joins the occurrence in flight on shutdown and records it canceled", func() {
		started := make(chan cron.Occurrence, 1)
		observedCancel := make(chan struct{})
		scheduler, err := cron.NewScheduler(
			cron.WithStore(store), cron.WithClock(manual), cron.WithLeaseDuration(longLease),
			cron.WithTrigger(rollup, every, func(ctx context.Context, occurrence cron.Occurrence) error {
				started <- occurrence
				<-ctx.Done()
				close(observedCancel)
				return ctx.Err()
			}),
		)
		Expect(err).NotTo(HaveOccurred())
		mounted := mountScheduler(scheduler)

		awaitWaits(manual, 1)
		manual.Advance(time.Minute)
		var occurrence cron.Occurrence
		Eventually(started).WithTimeout(runBudget.Within).Should(Receive(&occurrence))
		awaitOccurrence(store, occurrence.ID, grammar.OccurrenceRunning)

		Expect(mounted.shutdown()).To(Succeed())
		Expect(observedCancel).To(BeClosed(), "Run returned only after the operation stopped")
		snapshot := snapshotOf(ctx, store)
		Expect(snapshot.Occurrences).To(HaveLen(1))
		Expect(snapshot.Occurrences[0].Status).To(Equal(grammar.OccurrenceCanceled), "the end was recorded before the join completed")
		Expect(snapshot.Occurrences[0].LeaseOwner).To(BeEmpty())
	})

	It("records an operation's failure and panic without failing the service", func() {
		failure := errors.New("rollup refused")
		scheduler, err := cron.NewScheduler(
			cron.WithStore(store), cron.WithClock(manual), cron.WithLeaseDuration(longLease),
			cron.WithTrigger("failing", every, func(_ context.Context, _ cron.Occurrence) error { return failure }),
			cron.WithTrigger("panicking", every, func(_ context.Context, _ cron.Occurrence) error { panic("boom") }),
		)
		Expect(err).NotTo(HaveOccurred())
		mounted := mountScheduler(scheduler)

		awaitWaits(manual, 1)
		manual.Advance(time.Minute)
		failed := awaitOccurrence(store, grammar.OccurrenceID("failing", start.Add(time.Minute)), grammar.OccurrenceFailed)
		Expect(failed.Error).To(Equal(failure.Error()))
		panicked := awaitOccurrence(store, grammar.OccurrenceID("panicking", start.Add(time.Minute)), grammar.OccurrenceFailed)
		Expect(panicked.Error).To(ContainSubstring("operation panic: boom"))
		Consistently(mounted.done).ShouldNot(Receive(), "an operation's failure never stops the service")

		Expect(mounted.shutdown()).To(Succeed())
	})
})
