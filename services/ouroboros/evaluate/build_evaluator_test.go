// Copyright 2026 Candace Labs

package evaluate_test

import (
	"context"
	"errors"
	"testing/fstest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/kernel/clock"
	evaluation "github.com/candacelabs/csf/pkg/evaluate"
	"github.com/candacelabs/csf/services/ouroboros/evaluate"
)

var _ = Describe("Evaluating a build on the suite", func() {
	const build = "dddddddddddd"
	var (
		ctx      context.Context
		state    fstest.MapFS
		suite    evaluate.Suite
		replayer *MockIReplayer
		records  *MockIRunRecords
		source   *clock.ManualClock
	)

	BeforeEach(func() {
		ctx = context.Background()
		controller := gomock.NewController(GinkgoT())
		replayer, records = NewMockIReplayer(controller), NewMockIRunRecords(controller)
		state = fstest.MapFS{"run-1/recipe.json": {Data: recipe(1)}, "run-2/recipe.json": {Data: recipe(2)}}
		first, second := ticket(1, 100, 1), ticket(2, 100, 1)
		first.Assignment, second.Assignment = "run-1", "run-2"
		suite = evaluate.Suite{Version: 5, Model: "claude-opus-5-5", Tickets: []evaluate.Ticket{first, second}}
		source = clock.NewManualClock(specStart)
	})

	newEvaluator := func(options ...evaluate.BuildEvaluatorOption) *evaluate.BuildEvaluator {
		evaluator, err := evaluate.NewBuildEvaluator(records, replayer, suite, state, "/eval/repository",
			append([]evaluate.BuildEvaluatorOption{evaluate.WithBuildClock(source)}, options...)...)
		Expect(err).NotTo(HaveOccurred())
		return evaluator
	}

	It("is an evaluator of builds by their score", func() {
		var _ evaluation.IEvaluator[string, evaluate.Score] = newEvaluator()
	})

	It("replays every suite ticket on this host, records the run and returns the build's score", func() {
		want := evaluate.Score{Build: build, SuiteVersion: 5, Replays: 2, Expected: 2}
		gomock.InOrder(
			replayer.EXPECT().Run(ctx, build, suite, evaluate.NodeHost, gomock.Len(2), gomock.Any()).
				DoAndReturn(func(context.Context, string, evaluate.Suite, string, []evaluate.Job, func(context.Context, evaluate.Replay) error) error {
					source.Advance(time.Hour)
					return nil
				}),
			records.EXPECT().RecordRun(ctx, evaluate.Run{Build: build, SuiteVersion: 5, StartedAt: specStart.UTC(), FinishedAt: specStart.Add(time.Hour).UTC()}),
			records.EXPECT().ScoreOn(ctx, build, suite).Return(want, nil),
		)
		Expect(newEvaluator().Evaluate(ctx, build)).To(Equal(want))
	})

	It("replays here only the jobs the shard leaves", func() {
		sharded := newEvaluator(evaluate.WithShard(func(_ context.Context, jobs []evaluate.Job) ([]evaluate.Job, error) {
			return jobs[:1], nil
		}))
		replayer.EXPECT().Run(ctx, build, suite, evaluate.NodeHost, gomock.Len(1), gomock.Any())
		records.EXPECT().RecordRun(ctx, gomock.Any())
		records.EXPECT().ScoreOn(ctx, build, suite)
		_, err := sharded.Evaluate(ctx, build)
		Expect(err).NotTo(HaveOccurred())
	})

	It("records no run when a replay fails", func() {
		failed := errors.New("host refused the replay")
		replayer.EXPECT().Run(ctx, build, suite, evaluate.NodeHost, gomock.Any(), gomock.Any()).Return(failed)
		_, err := newEvaluator().Evaluate(ctx, build)
		Expect(err).To(MatchError(failed))
	})

	It("replays nothing for a ticket whose original run left no recipe", func() {
		delete(state, "run-2/recipe.json")
		_, err := newEvaluator().Evaluate(ctx, build)
		Expect(err).To(MatchError(evaluate.ErrNoOriginal))
	})

	It("refuses to build without the parts every run needs", func() {
		_, err := evaluate.NewBuildEvaluator(nil, replayer, suite, state, "/eval/repository")
		Expect(err).To(MatchError(evaluate.ErrIncompleteBuildEvaluator))
		_, err = evaluate.NewBuildEvaluator(records, replayer, suite, state, "")
		Expect(err).To(MatchError(evaluate.ErrIncompleteBuildEvaluator))
	})
})
