// Copyright 2026 Candace Labs

package evaluate

import (
	"context"
	"errors"
	"fmt"

	"github.com/candacelabs/csf/io/kernel/clock"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
)

// ErrIncompleteBuildEvaluator reports a build evaluator built without one of
// the parts every run needs.
var ErrIncompleteBuildEvaluator = errors.New("evaluate: a build evaluator needs its records, replayer, corpus and repository")

// IReplayer runs a build's replay jobs on one node kind and records each
// replay as it finishes. [Replayer] is the host's.
type IReplayer interface {
	Run(ctx context.Context, build string, suite Suite, node string, jobs []Job, record func(ctx context.Context, replay Replay) error) error
}

// IRunRecords is what a build's evaluation writes and reads back: each
// replay, the run, and the score over the recorded replays. [SuiteStore] is
// the database's.
type IRunRecords interface {
	RecordReplay(ctx context.Context, replay Replay) error
	RecordRun(ctx context.Context, run Run) error
	ScoreOn(ctx context.Context, build string, suite Suite) (Score, error)
}

var (
	_ IReplayer   = (*Replayer)(nil)
	_ IRunRecords = (*SuiteStore)(nil)
)

// BuildEvaluator measures a csf build on one suite version: the suite is the
// held-out criteria and the build never chooses it. It replays the suite's
// tickets on the build, records the run and returns the build's score over
// every replay recorded for it.
type BuildEvaluator struct {
	records    IRunRecords
	replayer   IReplayer
	suite      Suite
	corpus     iofs.IFiles
	repository string
	clock      clock.IClock
	shard      func(ctx context.Context, jobs []Job) ([]Job, error)
}

// BuildEvaluatorOption configures a [BuildEvaluator].
type BuildEvaluatorOption func(evaluator *BuildEvaluator) error

// WithBuildClock reads the run's start and finish from source instead of the
// system clock.
func WithBuildClock(source clock.IClock) BuildEvaluatorOption {
	return func(evaluator *BuildEvaluator) error {
		if source == nil {
			return fmt.Errorf("evaluate: nil clock")
		}
		evaluator.clock = source
		return nil
	}
}

// WithShard hands the build's jobs to shard before any replays here: shard
// sends what it can elsewhere, such as a burst node whose replays are
// recorded on their own, and returns the jobs left for this host.
func WithShard(shard func(ctx context.Context, jobs []Job) ([]Job, error)) BuildEvaluatorOption {
	return func(evaluator *BuildEvaluator) error {
		if shard == nil {
			return fmt.Errorf("evaluate: nil shard")
		}
		evaluator.shard = shard
		return nil
	}
}

// NewBuildEvaluator builds the evaluator of builds on suite: replays read their
// tickets from corpus and branch from repository.
func NewBuildEvaluator(records IRunRecords, replayer IReplayer, suite Suite, corpus iofs.IFiles, repository string, options ...BuildEvaluatorOption) (*BuildEvaluator, error) {
	if records == nil || replayer == nil || corpus == nil || repository == "" {
		return nil, ErrIncompleteBuildEvaluator
	}
	evaluator := &BuildEvaluator{records: records, replayer: replayer, suite: suite, corpus: corpus, repository: repository,
		clock: clock.NewSystemClock(), shard: func(_ context.Context, jobs []Job) ([]Job, error) { return jobs, nil }}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("evaluate: nil build evaluator option")
		}
		if err := option(evaluator); err != nil {
			return nil, err
		}
	}
	return evaluator, nil
}

// Evaluate replays the suite on build, records the run and returns the build's
// score on the suite.
func (evaluator *BuildEvaluator) Evaluate(ctx context.Context, build string) (Score, error) {
	jobs, err := Jobs(evaluator.suite, evaluator.corpus, build, evaluator.repository)
	if err != nil {
		return Score{}, err
	}
	started := evaluator.clock.Now().UTC()
	local, err := evaluator.shard(ctx, jobs)
	if err != nil {
		return Score{}, err
	}
	if err := evaluator.replayer.Run(ctx, build, evaluator.suite, NodeHost, local, evaluator.records.RecordReplay); err != nil {
		return Score{}, err
	}
	run := Run{Build: build, SuiteVersion: evaluator.suite.Version, StartedAt: started, FinishedAt: evaluator.clock.Now().UTC()}
	if err := evaluator.records.RecordRun(ctx, run); err != nil {
		return Score{}, err
	}
	return evaluator.records.ScoreOn(ctx, build, evaluator.suite)
}
