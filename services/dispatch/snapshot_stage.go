// Copyright 2026 Candace Labs

package dispatch

import (
	"slices"
	"time"

	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
)

// Stage is where a slice stands on its way from the queue to main, as the
// snapshot shows it: the three ways a queued slice waits, running, and the
// three ways a slice finishes.
type Stage string

const (
	// StageReady is a queued slice on the frontier that nothing running
	// contends with: it launches when the admission allows.
	StageReady Stage = "ready"
	// StageBlocked is a queued slice with a depends_on predecessor not yet
	// merged, or one held back.
	StageBlocked Stage = "blocked"
	// StageContended is a queued slice on the frontier that a running slice
	// contends with.
	StageContended Stage = "contended"
	StageRunning   Stage = "running"
	StageMerged    Stage = "merged"
	StageFailed    Stage = "failed"
	StageCanceled  Stage = "canceled"
)

// Stages is every stage in the order a slice moves through them.
var Stages = []Stage{StageReady, StageBlocked, StageContended, StageRunning, StageMerged, StageFailed, StageCanceled}

// StagedSlice is one slice of the snapshot with its stage.
type StagedSlice struct {
	Stage Stage
	Slice SliceView
}

// Staged is every slice of the snapshot with its stage: the queue in dispatch
// order, then what runs, then what finished. A held running slice is listed
// once, as running.
func (snapshot Snapshot) Staged() []StagedSlice {
	running := make([]string, 0, len(snapshot.Running))
	for _, slice := range snapshot.Running {
		running = append(running, slice.SliceID)
	}
	var staged []StagedSlice
	for _, slice := range slices.Concat(snapshot.Queue, snapshot.Held) {
		if slices.Contains(running, slice.SliceID) {
			continue
		}
		stage := StageBlocked
		if slice.Held == "" && slice.Rank > 0 {
			stage = StageReady
			if slices.ContainsFunc(slice.Contends, func(other string) bool { return slices.Contains(running, other) }) {
				stage = StageContended
			}
		}
		staged = append(staged, StagedSlice{Stage: stage, Slice: slice})
	}
	for _, slice := range snapshot.Running {
		staged = append(staged, StagedSlice{Stage: StageRunning, Slice: slice})
	}
	for _, slice := range snapshot.Finished {
		stage := StageCanceled
		switch slice.State {
		case dispatchv1.SliceState_SLICE_STATE_MERGED.String():
			stage = StageMerged
		case dispatchv1.SliceState_SLICE_STATE_FAILED.String():
			stage = StageFailed
		}
		staged = append(staged, StagedSlice{Stage: stage, Slice: slice})
	}
	return staged
}

// Flow is the snapshot's slices as a flow: how many stand in each stage, how
// long the oldest of them has stood there, and the mean lead time from
// enqueue to merge of the slices merged within the window before the
// snapshot. LeadTime is nil when none merged in the window.
type Flow struct {
	Slices   map[Stage]int
	Oldest   map[Stage]time.Duration
	LeadTime *time.Duration
}

// Flow measures the snapshot at its own instant.
func (snapshot Snapshot) Flow(window time.Duration) Flow {
	flow := Flow{Slices: map[Stage]int{}, Oldest: map[Stage]time.Duration{}}
	var lead time.Duration
	merged := 0
	for _, staged := range snapshot.Staged() {
		flow.Slices[staged.Stage]++
		flow.Oldest[staged.Stage] = max(flow.Oldest[staged.Stage], snapshot.At.Sub(staged.Slice.UpdatedAt))
		if staged.Stage == StageMerged && snapshot.At.Sub(staged.Slice.UpdatedAt) <= window {
			lead += staged.Slice.UpdatedAt.Sub(staged.Slice.CreatedAt)
			merged++
		}
	}
	if merged > 0 {
		mean := lead / time.Duration(merged)
		flow.LeadTime = &mean
	}
	return flow
}
