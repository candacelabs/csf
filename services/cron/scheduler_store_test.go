// Copyright 2026 Candace Labs

package cron_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/kernel/clock"
	grammar "github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/runtime"
	cron "github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/cron/mocks"
)

// A store that refuses is a failure of the service: the mount fails, or the
// scope fails and the runtime stops with the store's error as the cause.
var _ = Describe("the cron service with a store that refuses", func() {
	var (
		store    *mocks.MockIStore
		manual   *clock.ManualClock
		schedule grammar.Schedule
	)

	BeforeEach(func() {
		store = mocks.NewMockIStore(gomock.NewController(GinkgoT()))
		manual = clock.NewManualClock(start)
		schedule = grammar.Spec(grammar.Every(time.Hour)).Anchor(start.Add(-time.Hour))
	})

	It("fails the mount when the declared triggers cannot be reconciled", func() {
		definition := triggerDefinition(rollup, schedule, grammar.CatchUpAll, grammar.OverlapAllow)
		refused := errors.New("reconciliation unavailable")
		store.EXPECT().
			Reconcile(gomock.Any(), gomock.Eq([]grammar.TriggerDefinition{definition}), gomock.Eq(start)).
			Return(nil, refused)
		scheduler, err := cron.NewScheduler(cron.WithStore(store), cron.WithClock(manual),
			cron.WithTrigger(rollup, schedule, func(_ context.Context, _ cron.Occurrence) error { return nil },
				cron.WithCatchUp(definition.CatchUp), cron.WithOverlap(definition.Overlap)))
		Expect(err).NotTo(HaveOccurred())

		host, err := runtime.NewHostRuntime()
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Mount("cron", scheduler)).To(Succeed())
		err = host.Run(context.Background())
		Expect(err).To(MatchError(refused))
		Expect(err).To(MatchError(ContainSubstring("reconcile declared triggers")))
		Expect(scheduler.Start(runtime.NewScope(context.Background(), "again"))).To(MatchError(cron.ErrAlreadyStarted))
	})

	It("fails the service when an occurrence's end cannot be recorded", func() {
		definition := triggerDefinition(rollup, schedule, grammar.CatchUpAll, grammar.OverlapAllow)
		scheduledAt := start
		state := grammar.TriggerState{Definition: definition, NextRunAt: scheduledAt, CreatedAt: start, UpdatedAt: start}
		refused := errors.New("completion unavailable")
		invoked := make(chan cron.Occurrence, 1)
		store.EXPECT().Reconcile(gomock.Any(), gomock.Any(), gomock.Any()).Return([]grammar.TriggerState{state}, nil)
		store.EXPECT().Expired(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		store.EXPECT().Claim(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, request cron.ClaimRequest) (cron.ClaimResult, error) {
			Expect(request.TriggerName).To(Equal(rollup))
			Expect(request.ScheduledAt).To(Equal(scheduledAt))
			Expect(request.NextRunAt).To(Equal(scheduledAt.Add(time.Hour)))
			return cron.ClaimResult{Disposition: cron.ClaimAcquired, Occurrence: grammar.OccurrenceRecord{
				ID: request.OccurrenceID, TriggerName: rollup, ScheduledAt: scheduledAt, Status: grammar.OccurrenceRunning,
				Attempt: 1, StartedAt: request.ClaimedAt, LeaseOwner: request.LeaseOwner, LeaseToken: request.LeaseToken, LeaseUntil: request.LeaseUntil,
			}}, nil
		})
		store.EXPECT().Complete(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, completion cron.Completion) error {
			Expect(completion.Status).To(Equal(grammar.OccurrenceSucceeded))
			Expect(completion.OccurrenceID).To(Equal(grammar.OccurrenceID(rollup, scheduledAt)))
			return refused
		})
		scheduler, err := cron.NewScheduler(cron.WithStore(store), cron.WithClock(manual),
			cron.WithTrigger(rollup, schedule, func(_ context.Context, occurrence cron.Occurrence) error {
				invoked <- occurrence
				return nil
			}, cron.WithCatchUp(definition.CatchUp), cron.WithOverlap(definition.Overlap)))
		Expect(err).NotTo(HaveOccurred())

		host, err := runtime.NewHostRuntime()
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Mount("cron", scheduler)).To(Succeed())
		err = host.Run(context.Background())
		Expect(err).To(MatchError(refused))
		Expect(invoked).To(Receive(HaveField("Attempt", uint32(1))))
	})
})
