package cron_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	cron "github.com/candacelabs/csf/pkg/cron"
)

var _ = Describe("Service store interactions", func() {
	It("projects the static job definition and propagates reconciliation failures", func() {
		controller := gomock.NewController(GinkgoT())
		store := NewMockIStore(controller)
		schedule := cron.Spec(cron.Daily(cron.At(3).PM()))
		definition := jobDefinition("daily-rollup", schedule, cron.CatchUpAll, cron.OverlapAllow)
		storeError := errors.New("reconciliation unavailable")

		store.EXPECT().
			Reconcile(gomock.Any(), gomock.Eq([]cron.JobDefinition{definition}), gomock.Any()).
			DoAndReturn(func(ctx context.Context, definitions []cron.JobDefinition, startedAt time.Time) ([]cron.JobState, error) {
				Expect(ctx.Err()).NotTo(HaveOccurred())
				Expect(definitions).To(Equal([]cron.JobDefinition{definition}))
				Expect(startedAt.IsZero()).To(BeFalse())
				return nil, storeError
			})

		service, err := cron.New(
			cron.WithStore(store),
			cron.WithJob(
				definition.Name,
				schedule,
				func(ctx context.Context, invocation cron.Invocation) error { return nil },
				cron.WithCatchUp(definition.CatchUp),
				cron.WithOverlap(definition.Overlap),
			),
		)
		Expect(err).NotTo(HaveOccurred())

		err = service.Run(context.Background())
		Expect(err).To(MatchError(ContainSubstring("reconcile static jobs")))
		Expect(errors.Is(err, storeError)).To(BeTrue())
	})

	It("claims a due occurrence and records the handler failure without failing the scheduler", func() {
		controller := gomock.NewController(GinkgoT())
		store := NewMockIStore(controller)
		anchor := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour - time.Minute)
		scheduledAt := anchor.Add(time.Hour)
		nextRunAt := scheduledAt.Add(time.Hour)
		schedule := cron.Spec(cron.Every(time.Hour)).Anchor(anchor)
		definition := jobDefinition("hourly-rollup", schedule, cron.CatchUpAll, cron.OverlapSkip)
		jobError := errors.New("rollup failed")
		var claimed cron.ClaimRequest
		completed := make(chan cron.Completion, 1)
		invoked := make(chan cron.Invocation, 1)
		runContext, cancelRun := context.WithCancel(context.Background())
		defer cancelRun()

		store.EXPECT().
			Reconcile(gomock.Any(), gomock.Eq([]cron.JobDefinition{definition}), gomock.Any()).
			Return([]cron.JobState{{Definition: definition, NextRunAt: scheduledAt}}, nil)
		store.EXPECT().Expired(gomock.Any(), gomock.Any(), 1000).Return(nil, nil)
		store.EXPECT().
			Claim(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, request cron.ClaimRequest) (cron.ClaimResult, error) {
				claimed = request
				return cron.ClaimResult{
					Disposition: cron.ClaimAcquired,
					Occurrence: cron.OccurrenceRecord{
						ID:          request.OccurrenceID,
						JobName:     request.JobName,
						ScheduledAt: request.ScheduledAt,
						StartedAt:   request.ClaimedAt,
						Attempt:     1,
					},
				}, nil
			})
		store.EXPECT().
			Complete(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, completion cron.Completion) error {
				completed <- completion
				cancelRun()
				return nil
			})

		service, err := cron.New(
			cron.WithStore(store),
			cron.WithLeaseOwner("mock-runner"),
			cron.WithLeaseDuration(time.Minute),
			cron.WithJob(definition.Name, schedule, func(_ context.Context, invocation cron.Invocation) error {
				invoked <- invocation
				return jobError
			}, cron.WithCatchUp(definition.CatchUp), cron.WithOverlap(definition.Overlap)),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(service.Run(runContext)).To(Succeed())

		Expect(claimed.OccurrenceID).To(Equal(cron.OccurrenceID(definition.Name, scheduledAt)))
		Expect(claimed.JobName).To(Equal(definition.Name))
		Expect(claimed.ScheduledAt).To(Equal(scheduledAt))
		Expect(claimed.NextRunAt).To(Equal(nextRunAt))
		Expect(claimed.LeaseOwner).To(Equal("mock-runner"))
		Expect(claimed.LeaseToken).NotTo(BeEmpty())
		Expect(claimed.LeaseUntil.Sub(claimed.ClaimedAt)).To(Equal(time.Minute))

		var invocation cron.Invocation
		Expect(invoked).To(Receive(&invocation))
		Expect(invocation).To(Equal(cron.Invocation{
			ID:          claimed.OccurrenceID,
			JobName:     claimed.JobName,
			ScheduledAt: claimed.ScheduledAt,
			StartedAt:   claimed.ClaimedAt,
			Attempt:     1,
		}))
		var completion cron.Completion
		Expect(completed).To(Receive(&completion))
		Expect(completion.OccurrenceID).To(Equal(claimed.OccurrenceID))
		Expect(completion.LeaseToken).To(Equal(claimed.LeaseToken))
		Expect(completion.Status).To(Equal(cron.OccurrenceFailed))
		Expect(completion.Error).To(Equal(jobError.Error()))
		Expect(completion.FinishedAt.IsZero()).To(BeFalse())
	})

	It("cancels the handler, records cancellation, and surfaces a lease renewal failure", func() {
		controller := gomock.NewController(GinkgoT())
		store := NewMockIStore(controller)
		anchor := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour - time.Minute)
		scheduledAt := anchor.Add(time.Hour)
		nextRunAt := scheduledAt.Add(time.Hour)
		schedule := cron.Spec(cron.Every(time.Hour)).Anchor(anchor)
		definition := jobDefinition("lease-sensitive", schedule, cron.CatchUpAll, cron.OverlapSkip)
		leaseError := errors.New("lease backend unavailable")
		var claimed cron.ClaimRequest
		renewed := make(chan cron.LeaseRenewal, 1)
		completed := make(chan cron.Completion, 1)
		handlerCanceled := make(chan error, 1)
		runContext, cancelRun := context.WithCancel(context.Background())
		defer cancelRun()

		store.EXPECT().
			Reconcile(gomock.Any(), gomock.Eq([]cron.JobDefinition{definition}), gomock.Any()).
			Return([]cron.JobState{{Definition: definition, NextRunAt: scheduledAt}}, nil)
		store.EXPECT().Expired(gomock.Any(), gomock.Any(), 1000).Return(nil, nil).MinTimes(1)
		store.EXPECT().
			Claim(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, request cron.ClaimRequest) (cron.ClaimResult, error) {
				claimed = request
				return cron.ClaimResult{
					Disposition: cron.ClaimAcquired,
					Occurrence: cron.OccurrenceRecord{
						ID:          request.OccurrenceID,
						JobName:     request.JobName,
						ScheduledAt: request.ScheduledAt,
						StartedAt:   request.ClaimedAt,
						Attempt:     1,
					},
				}, nil
			})
		store.EXPECT().
			Renew(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, renewal cron.LeaseRenewal) error {
				renewed <- renewal
				return leaseError
			})
		store.EXPECT().
			Complete(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, completion cron.Completion) error {
				completed <- completion
				return nil
			})

		service, err := cron.New(
			cron.WithStore(store),
			cron.WithLeaseOwner("mock-runner"),
			cron.WithLeaseDuration(30*time.Millisecond),
			cron.WithJob(definition.Name, schedule, func(ctx context.Context, _ cron.Invocation) error {
				<-ctx.Done()
				handlerCanceled <- ctx.Err()
				return ctx.Err()
			}, cron.WithCatchUp(definition.CatchUp), cron.WithOverlap(definition.Overlap)),
		)
		Expect(err).NotTo(HaveOccurred())

		runDone := make(chan error, 1)
		go func() { runDone <- service.Run(runContext) }()
		var runError error
		Eventually(runDone, time.Second).Should(Receive(&runError))
		Expect(errors.Is(runError, leaseError)).To(BeTrue())
		Expect(runError).To(MatchError(ContainSubstring("renew lease")))
		Expect(handlerCanceled).To(Receive(MatchError(context.Canceled)))
		Expect(claimed.NextRunAt).To(Equal(nextRunAt))

		var renewal cron.LeaseRenewal
		Expect(renewed).To(Receive(&renewal))
		Expect(renewal.OccurrenceID).To(Equal(claimed.OccurrenceID))
		Expect(renewal.LeaseToken).To(Equal(claimed.LeaseToken))
		Expect(renewal.LeaseUntil.Sub(renewal.RenewedAt)).To(Equal(30 * time.Millisecond))

		var completion cron.Completion
		Expect(completed).To(Receive(&completion))
		Expect(completion.OccurrenceID).To(Equal(claimed.OccurrenceID))
		Expect(completion.LeaseToken).To(Equal(claimed.LeaseToken))
		Expect(completion.Status).To(Equal(cron.OccurrenceCanceled))
		Expect(completion.Error).To(Equal(context.Canceled.Error()))
	})
})
