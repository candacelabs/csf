// Copyright 2026 Candace Labs

package ouroboros_test

import (
	"context"
	"testing/fstest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/ouroboros"
)

var _ = Describe("The detector", func() {
	var (
		ctx  context.Context
		spec *fixture
	)

	BeforeEach(func() {
		ctx = context.Background()
		spec = newFixture()
	})

	It("lists the miners under the repository with whether they are built", func() {
		spec.repository["services/ouroboros/miners/unbuilt/rules.dl"] = &fstest.MapFile{Data: []byte("x(R) :- y(R).\n")}
		spec.repository["services/ouroboros/miners/notes/README.md"] = &fstest.MapFile{Data: []byte("no rules here\n")}
		spec.expectMiner(0)
		miners, err := spec.loop.Miners(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(miners).To(HaveLen(2))
		Expect(miners[0].Name).To(Equal(minerName))
		Expect(miners[0].Built).To(BeTrue())
		Expect(miners[0].Scope).To(Equal(ouroboros.ScopeGeneric))
		Expect(miners[1].Name).To(Equal("unbuilt"))
		Expect(miners[1].Built).To(BeFalse())
		Expect(miners[1].Scope).To(BeEmpty())
	})

	It("mines a log once at its size, and again only when it grows", func() {
		spec.expectMiner(2)
		Expect(spec.loop.Detect(ctx, nil)).To(Succeed())
		findings, err := spec.ledger.FindingsSince(ctx, time.Time{})
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(HaveLen(1))
		Expect(findings[0].Miner).To(Equal(minerName))
		Expect(findings[0].Subject).To(Equal(runAssignment))
		Expect(findings[0].Item).To(Equal(runLog))
		Expect(findings[0].Scope).To(Equal(ouroboros.ScopeGeneric))
		item, known, err := spec.ledger.Item(ctx, minerName, runLog)
		Expect(err).NotTo(HaveOccurred())
		Expect(known).To(BeTrue())
		Expect(item.ByteSize).To(Equal(int64(len(logLine))))

		Expect(spec.loop.Detect(ctx, nil)).To(Succeed(), "an unchanged log is not mined again")

		spec.corpus[runLog] = &fstest.MapFile{Data: []byte(logLine + logLine)}
		spec.expectMiner(2)
		Expect(spec.loop.Detect(ctx, []string{runAssignment})).To(Succeed())
		item, _, err = spec.ledger.Item(ctx, minerName, runLog)
		Expect(err).NotTo(HaveOccurred())
		Expect(item.ByteSize).To(Equal(int64(2 * len(logLine))))
		findings, err = spec.ledger.FindingsSince(ctx, time.Time{})
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(HaveLen(1), "the same finding is recorded once")
	})

	It("mines a changed log from the watch once the change has settled", func() {
		changes := make(chan iofs.Change)
		added := make(chan string, 8)
		spec.watcher.EXPECT().Watch(gomock.Any()).DoAndReturn(func(ctx context.Context) (*iofs.Watch, error) {
			forward := make(chan iofs.Change)
			go func() {
				defer close(forward)
				for {
					select {
					case <-ctx.Done():
						return
					case change := <-changes:
						select {
						case forward <- change:
						case <-ctx.Done():
							return
						}
					}
				}
			}()
			return &iofs.Watch{Changes: forward, Add: func(name string) error { added <- name; return nil }}, nil
		})
		// The start's own sweep mines the log and its measure fits the knee;
		// the watch mines the grown log again.
		spec.expectMiner(5)
		spec.expectStructure()
		scope := runtime.NewScope(context.Background(), "detector-spec")
		Expect(spec.loop.Start(scope)).To(Succeed())
		DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })
		Eventually(added, ledgerBudget.Within).Should(Receive(Equal(".")), "the corpus is watched for new run directories")
		Eventually(added, ledgerBudget.Within).Should(Receive(Equal(runAssignment)), "every run directory is watched for its log")

		eventually.Await(GinkgoT(), "the first sweep mined the log", ledgerBudget, func() int64 {
			item, _, err := spec.ledger.Item(ctx, minerName, runLog)
			Expect(err).NotTo(HaveOccurred())
			return item.ByteSize
		}, func(size int64) bool { return size == int64(len(logLine)) })
		spec.corpus[runLog] = &fstest.MapFile{Data: []byte(logLine + logLine)}
		changes <- iofs.Change{Name: runLog, Op: iofs.ChangeWritten}
		changes <- iofs.Change{Name: runLog, Op: iofs.ChangeWritten}
		eventually.Await(GinkgoT(), "the settle wait is armed", ledgerBudget, spec.clock.Waiting, func(waiting int) bool { return waiting == 1 })
		spec.clock.Advance(11 * time.Second)
		eventually.Await(GinkgoT(), "the grown log is mined", ledgerBudget, func() int64 {
			item, _, err := spec.ledger.Item(ctx, minerName, runLog)
			Expect(err).NotTo(HaveOccurred())
			return item.ByteSize
		}, func(size int64) bool { return size == int64(2*len(logLine)) })
	})
})
