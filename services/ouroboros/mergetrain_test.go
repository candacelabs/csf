// Copyright 2026 Candace Labs

package ouroboros_test

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/ouroboros"
)

var readyPulls = []ouroboros.PullRequest{
	{Number: 9, Title: "LANG: terms", HeadSHA: "aaaaaaa", CreatedAt: specStart.Add(-3 * 60e9)},
	{Number: 10, Title: "SELF: mine", HeadSHA: "bbbbbbb", CreatedAt: specStart.Add(-2 * 60e9)},
	{Number: 11, Title: "MINER no_self_merge (#249)", HeadSHA: "ccccccc", CreatedAt: specStart.Add(-60e9)},
}

var _ = Describe("The merge train", func() {
	var (
		ctx  context.Context
		spec *fixture
	)

	BeforeEach(func() {
		ctx = context.Background()
		spec = newFixture()
		spec.tickets.EXPECT().ListReadyPullRequests(gomock.Any()).Return(readyPulls, nil).AnyTimes()
	})

	It("refuses a self-authored pull request, runs the path on the rest and retries only a new head", func() {
		spec.tickets.EXPECT().PullRequestSessions(gomock.Any(), int64(10)).Return([]string{specMerger}, nil)
		spec.tickets.EXPECT().Comment(gomock.Any(), int64(10), gomock.Any()).DoAndReturn(func(_ context.Context, _ int64, body string) error {
			Expect(body).To(ContainSubstring("NO-SELF-MERGE"))
			return nil
		})
		spec.tickets.EXPECT().PullRequestSessions(gomock.Any(), int64(11)).Return([]string{runAssignment}, nil)
		spec.launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, command proc.Command) (proc.Result, error) {
			switch command.Executable {
			case "bash":
				Expect(command.Arguments).To(Equal([]string{"tools/merge-pr.sh", "11"}))
				Expect(command.Directory).To(Equal("/repository"))
				Expect(command.ExtraEnvironment).To(ContainElement(harness.BazelOutputVariable + "=" + filepath.Join(spec.state, "ouroboros-bazel")))
				return proc.Result{ExitCode: 1, Stdout: []byte("check-merge: REFUSED: house-lint\nmerge-pr: pull request #11 is refused; see the report above\n")},
					&proc.ExitError{Executable: "bash", Code: 1}
			case "git":
				return proc.Result{Stdout: []byte("0d07c80\n")}, nil
			}
			return proc.Result{ExitCode: -1}, proc.ErrExecutableRequired
		}).Times(2)
		spec.tickets.EXPECT().Comment(gomock.Any(), int64(11), gomock.Any()).DoAndReturn(func(_ context.Context, _ int64, body string) error {
			Expect(body).To(ContainSubstring("`tools/merge-pr.sh 11` exit 1 on 0d07c80"))
			Expect(body).To(ContainSubstring("check-merge: REFUSED: house-lint"))
			return nil
		})
		Expect(spec.loop.MergeTrain(ctx)).To(MatchError(ouroboros.ErrSelfMerge))

		merges, err := spec.ledger.RecentMerges(ctx, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(merges).To(HaveLen(2))
		refused, seen, err := spec.ledger.Merge(ctx, 11, "ccccccc")
		Expect(err).NotTo(HaveOccurred())
		Expect(seen).To(BeTrue())
		Expect(refused.Merged).To(BeFalse())
		Expect(refused.ExitCode).To(Equal(1))
		Expect(refused.AuthorSessions).To(Equal([]string{runAssignment}))
		self, _, err := spec.ledger.Merge(ctx, 10, "bbbbbbb")
		Expect(err).NotTo(HaveOccurred())
		Expect(self.Reason).To(ContainSubstring("NO-SELF-MERGE"))

		Expect(spec.loop.MergeTrain(ctx)).To(Succeed(), "the same heads are not merged again")
	})

	It("records a merged pull request without commenting", func() {
		spec.tickets.EXPECT().PullRequestSessions(gomock.Any(), gomock.Any()).Return([]string{runAssignment}, nil).Times(2)
		spec.launcher.EXPECT().Run(gomock.Any(), gomock.Any()).Return(proc.Result{Stdout: []byte("check-merge: passed\n")}, nil).Times(2)
		Expect(spec.loop.MergeTrain(ctx)).To(Succeed())
		merged, seen, err := spec.ledger.Merge(ctx, 11, "ccccccc")
		Expect(err).NotTo(HaveOccurred())
		Expect(seen).To(BeTrue())
		Expect(merged.Merged).To(BeTrue())
		Expect(merged.Reason).To(Equal("merged"))
	})
})
