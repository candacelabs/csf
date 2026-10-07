// Copyright 2026 Candace Labs

package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/candacelabs/csf/csf/prod"
)

// Measurer measures a repository into a prod.Reading. A real caller wires the
// checker commands; a fixture injects a scripted measurer.
type Measurer func(ctx context.Context, repo string) (prod.Reading, error)

// Fixer applies one candidate change to the working tree. It returns an error
// only when it cannot apply the change at all; a change the gates later refuse
// is a nil error and a rollback, not an error.
type Fixer func(ctx context.Context, run *Run) error

// NoFix is a Fixer that proposes no change. The mine and induce slices supply
// the repairing one; until they land, a loop built with NoFix measures a
// repository and emits its archive without altering the tree.
func NoFix(_ context.Context, _ *Run) error { return nil }

// Run is one iteration's state, handed to every stage.
type Run struct {
	Repo      string
	Before    prod.Reading
	After     prod.Reading
	Refusals  []prod.Refusal
	Kept      bool
	Iteration int
}

// DefaultCap bounds the loop's iterations when none is set.
const DefaultCap = 32

// ArchiveName is the archive the loop emits beside the repository.
const ArchiveName = "bootstrap.tar.gz"

// Loop runs the bootstrap stages to a fixed point.
type Loop struct {
	pipeline       []Entry
	fixer          Fixer
	measurer       Measurer
	emitter        Emitter
	archive        string
	sourceRevision string
	created        string
	cap            int
	logger         *slog.Logger
}

// NewLoop builds a loop from a measurer and a fixer, with its default pipeline,
// archive name and cap; the With* methods replace them.
func NewLoop(measurer Measurer, fixer Fixer) *Loop {
	loop := &Loop{
		fixer:    fixer,
		measurer: measurer,
		emitter:  TarGz,
		archive:  ArchiveName,
		cap:      DefaultCap,
		logger:   slog.New(slog.DiscardHandler),
	}
	loop.pipeline = loop.defaultStages()
	return loop
}

// WithPipeline replaces the loop's pipeline of stages.
func (l *Loop) WithPipeline(pipeline []Entry) *Loop { l.pipeline = pipeline; return l }

// WithFixer replaces the fixer stage's function.
func (l *Loop) WithFixer(fixer Fixer) *Loop { l.fixer = fixer; return l }

// WithMeasurer replaces the measurer.
func (l *Loop) WithMeasurer(measurer Measurer) *Loop { l.measurer = measurer; return l }

// WithEmitter replaces the archive emitter.
func (l *Loop) WithEmitter(emitter Emitter) *Loop { l.emitter = emitter; return l }

// WithArchive replaces the archive destination, relative to the repository's
// parent directory.
func (l *Loop) WithArchive(archive string) *Loop { l.archive = archive; return l }

// WithSourceRevision sets the revision the emitted archive's manifest records.
func (l *Loop) WithSourceRevision(revision string) *Loop {
	l.sourceRevision = revision
	return l
}

// WithCreated sets the timestamp the emitted archive's manifest records, as
// RFC 3339, or empty for none. It is part of the archive's bytes, so a caller
// that wants a reproducible archive fixes it.
func (l *Loop) WithCreated(created string) *Loop { l.created = created; return l }

// WithCap replaces the iteration cap.
func (l *Loop) WithCap(cap int) *Loop { l.cap = cap; return l }

// WithLogger replaces the refusal logger.
func (l *Loop) WithLogger(logger *slog.Logger) *Loop { l.logger = logger; return l }

// defaultStages builds the loop's own stages as function values capturing the
// loop, in stage order. It is the registry in data (CS-6): the pipeline is one
// slice of [Entry] values, and a sibling slice plugs a stage in by appending
// one — the loop never dispatches on a stage id.
func (l *Loop) defaultStages() []Entry {
	return []Entry{
		{ID: StageWork, Stage: l.work},
		{ID: StageFix, Stage: l.fix},
		{ID: StageScore, Stage: l.score},
		{ID: StageKeep, Stage: l.keep},
	}
}

// work turns csfc refusals into work. The mine slice supplies the refusal
// source; until then this stage has nothing to translate.
func (l *Loop) work(ctx context.Context, run *Run) error { return nil }

// fix applies the injected fixer's candidate change to the working tree.
func (l *Loop) fix(ctx context.Context, run *Run) error { return l.fixer(ctx, run) }

// score measures the repository again and records it as the run's after reading.
func (l *Loop) score(ctx context.Context, run *Run) error {
	after, err := l.measurer(ctx, run.Repo)
	if err != nil {
		return err
	}
	run.After = after
	return nil
}

// keep applies the two gates: a kept change grew the seed without a refusal.
func (l *Loop) keep(ctx context.Context, run *Run) error {
	run.Refusals = prod.Refuses(run.Before, run.After)
	run.Kept = prod.Grew(run.Before, run.After) && len(run.Refusals) == 0
	return nil
}

// Result reports how the loop finished. Kept holds the readings of the kept
// changes in order — every consecutive pair grew the seed — and Refusals holds
// every refusal, so every_kept_change_raised_seed and refusals_logged are both
// read off one run.
type Result struct {
	Done       bool
	Iterations int
	Archive    string
	Initial    prod.Reading
	Final      prod.Reading
	Kept       []prod.Reading
	Refusals   []prod.Refusal
}

// Run drives the loop to a fixed point and returns the emitted archive's path.
func (l *Loop) Run(ctx context.Context, repo string) (Result, error) {
	if l.measurer == nil {
		return Result{}, errors.New("bootstrap loop has no measurer")
	}
	if l.fixer == nil {
		return Result{}, errors.New("bootstrap loop has no fixer")
	}

	before, err := l.measurer(ctx, repo)
	if err != nil {
		return Result{}, fmt.Errorf("measure before: %w", err)
	}
	result := Result{Initial: before}

	for iteration := 1; iteration <= l.cap; iteration++ {
		result.Iterations = iteration
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if before.AllHold() {
			result.Done = true
			break
		}

		snapshot, err := l.snapshot(repo)
		if err != nil {
			return Result{}, fmt.Errorf("snapshot: %w", err)
		}

		run := &Run{Repo: repo, Before: before, Iteration: iteration}
		if err := l.runStages(ctx, run); err != nil {
			return Result{}, err
		}

		if prod.Equal(before, run.After) {
			// No candidate change moved the reading: the fixed point. The tree
			// already carries every kept change, so nothing is restored.
			if err := l.removeSnapshot(snapshot); err != nil {
				return Result{}, err
			}
			result.Done = run.After.AllHold()
			break
		}

		if run.Kept {
			if err := l.removeSnapshot(snapshot); err != nil {
				return Result{}, err
			}
			result.Kept = append(result.Kept, run.After)
			before = run.After
			continue
		}

		// A refused change is rolled back and logged; the loop repeats so a
		// later candidate can try again.
		if err := l.restore(repo, snapshot); err != nil {
			return Result{}, fmt.Errorf("restore: %w", err)
		}
		result.Refusals = append(result.Refusals, run.Refusals...)
		l.logRefusals(run)
	}
	result.Final = before

	archive, err := l.emitter(ctx, EmitRequest{
		Repo:           repo,
		Destination:    l.archive,
		SourceRevision: l.sourceRevision,
		Created:        l.created,
	})
	if err != nil {
		return Result{}, fmt.Errorf("emit archive: %w", err)
	}
	result.Archive = archive
	return result, nil
}

// runStages runs the pipeline in order, stopping at the first stage error.
func (l *Loop) runStages(ctx context.Context, run *Run) error {
	for _, entry := range l.pipeline {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := entry.Stage(ctx, run); err != nil {
			return fmt.Errorf("stage %s: %w", entry.ID, err)
		}
	}
	return nil
}

// logRefusals records each refused change so the loop's refusals are logged.
func (l *Loop) logRefusals(run *Run) {
	for _, refusal := range run.Refusals {
		l.logger.Warn("bootstrap change refused",
			"gate", string(refusal.Gate),
			"signal", refusal.Signal,
			"directory", refusal.Directory,
			"before", refusal.Before,
			"after", refusal.After,
		)
	}
}
