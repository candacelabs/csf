// Copyright 2026 Candace Labs

package bootstrap

import "context"

// StageID names one stage of the loop, spelled as the brief names it.
type StageID string

const (
	// StageWork turns csfc refusals into work items.
	StageWork StageID = "csfc_refusals_to_work"
	// StageFix applies mechanical, jev-picked and coder-session fixes.
	StageFix StageID = "fix"
	// StageScore measures the score after the fixes.
	StageScore StageID = "build_test_score"
	// StageKeep decides keep_if(seed_grows, nothing_breaks).
	StageKeep StageID = "keep_if"
)

// Stage is one function value of the pipeline, mutating the working tree or
// the run's state. The loop measures before and after the stages and owns the
// keep/refuse decision, so a stage never decides whether to keep.
type Stage func(ctx context.Context, run *Run) error

// Entry is one pipeline row: a stage under its id, in run order.
type Entry struct {
	ID    StageID
	Stage Stage
}
