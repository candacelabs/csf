// Copyright 2026 Candace Labs

package ouroboros_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/runtime"
	cronservice "github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/cron/crontest"
	"github.com/candacelabs/csf/services/ouroboros"
)

var _ = Describe("Building the loop", func() {
	It("refuses a loop without its capabilities", func() {
		_, err := ouroboros.NewLoop()
		Expect(err).To(MatchError(ouroboros.ErrMissingCapability))
	})

	It("refuses a nil option and values it cannot use", func() {
		_, err := ouroboros.NewLoop(nil)
		Expect(err).To(MatchError(ouroboros.ErrInvalidOption))
		_, err = ouroboros.NewLoop(ouroboros.WithLedger(nil))
		Expect(err).To(MatchError(ouroboros.ErrInvalidOption))
		_, err = ouroboros.NewLoop(ouroboros.WithDailyBudget(0))
		Expect(err).To(MatchError(ouroboros.ErrInvalidOption))
		_, err = ouroboros.NewLoop(ouroboros.WithMergerIdentity(" "))
		Expect(err).To(MatchError(ouroboros.ErrInvalidOption))
		_, err = ouroboros.NewLoop(ouroboros.WithCorpus("", nil, nil))
		Expect(err).To(MatchError(ouroboros.ErrInvalidOption))
	})

	It("reads the repository and number a pull request URL names, and whether it merged", func() {
		slug, number, err := ouroboros.PullRequestOf("https://github.com/candacelabs/csf_staging/pull/334")
		Expect(err).NotTo(HaveOccurred())
		Expect(slug).To(Equal("candacelabs/csf_staging"))
		Expect(number).To(Equal(int64(334)))
		_, _, err = ouroboros.PullRequestOf("https://github.com/candacelabs/csf_staging/issues/329")
		Expect(err).To(MatchError(ouroboros.ErrNotPullRequestURL))
		Expect(ouroboros.PullRequest{State: "MERGED"}.Merged()).To(BeTrue())
		Expect(ouroboros.PullRequest{State: "OPEN"}.Merged()).To(BeFalse())
	})

	It("refuses a ledger without a database", func() {
		_, err := ouroboros.NewLedger(nil)
		Expect(err).To(MatchError(ouroboros.ErrDatabaseRequired))
	})

	It("declares its four triggers for the cron service", func() {
		spec := newFixture()
		store := crontest.OpenStore(GinkgoT())
		scheduler, err := cronservice.NewScheduler(append([]cronservice.Option{cronservice.WithStore(store.Store)}, spec.loop.Triggers()...)...)
		Expect(err).NotTo(HaveOccurred())
		Expect(scheduler).NotTo(BeNil())
	})

	It("starts its watch once, sweeps and measures once, and refuses a second start", func() {
		spec := newFixture()
		// The start's sweep mines the log; its measure fits the knee again.
		spec.expectMiner(3)
		spec.expectStructure()
		spec.watcher.EXPECT().Watch(gomock.Any()).DoAndReturn(func(ctx context.Context) (*iofs.Watch, error) {
			changes := make(chan iofs.Change)
			go func() {
				<-ctx.Done()
				close(changes)
			}()
			return &iofs.Watch{Changes: changes, Add: func(name string) error { return nil }}, nil
		})
		scope := runtime.NewScope(context.Background(), "loop-spec")
		Expect(spec.loop.Start(scope)).To(Succeed())
		Expect(spec.loop.Start(scope)).To(MatchError(ouroboros.ErrAlreadyStarted))
		eventually.Await(GinkgoT(), "the first measure is published", ledgerBudget, spec.readSnapshot, func(content []byte) bool { return len(content) > 0 })
		Expect(scope.Close()).To(Succeed())
	})
})
