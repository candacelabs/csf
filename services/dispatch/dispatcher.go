// Copyright 2026 Candace Labs

package dispatch

import (
	"time"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

const (
	// TriggerDispatcher is the slice dispatcher's cron trigger: one pass
	// every DispatcherInterval.
	TriggerDispatcher = "dispatch.dispatcher"
	// DispatcherInterval is the cadence of the dispatcher's passes. A pass
	// costs one harness check, one ledger read, the newest session logs and
	// one pull request read per recorded pull request; a minute keeps a
	// freed machine idle for at most that long between merges the pass
	// detects itself.
	DispatcherInterval = time.Minute
	// SnapshotFile is where the binary writes every published snapshot,
	// under the harness state directory, for the Workbench and the ops view.
	SnapshotFile = "dispatch.json"

	maxReasonBytes = 4096
	listSeparator  = ", "

	waitingDepends  = "depends on %s"
	waitingContends = "contends with %s, which runs"
	waitingPaused   = "the dispatcher is paused: %s"
	waitingHeld     = "held: %s"
	waitingRate     = "the rate limit for provider %s admits no launches until its window resets"
	waitingCapacity = "the limits admit %d dispatched sessions and %d run"
	waitingNextPass = "launches at the next pass"
)

// ControlAction is one control of the slice dispatcher; its spelling is the
// action column of csf_dispatch_controls.
type ControlAction string

// The four controls.
const (
	ControlPause   ControlAction = "pause"
	ControlResume  ControlAction = "resume"
	ControlHold    ControlAction = "hold"
	ControlRelease ControlAction = "release"
)

// Control is one recorded control: the action, the slice a hold or release
// names, and the reason it was given.
type Control struct {
	Action  ControlAction
	SliceID string
	Reason  string
}

// names reports whether the action names one slice.
func (action ControlAction) names() bool {
	return action == ControlHold || action == ControlRelease
}

// DispatcherControlInput is a pause or a resume of the slice dispatcher.
type DispatcherControlInput struct {
	Reason string `json:"reason" jsonschema:"why, recorded with the control"`
}

// SliceControlInput is a hold or a release of one slice.
type SliceControlInput struct {
	SliceID string `json:"slice_id" jsonschema:"the slice to hold or release"`
	Reason  string `json:"reason" jsonschema:"why, recorded with the control"`
}

// pendingMerge is a slice whose recorded pull request has not been seen
// merged.
type pendingMerge struct {
	slice string
	url   string
}

// SnapshotInput asks for the dispatcher's snapshot; it has no fields.
type SnapshotInput struct{}

// Snapshot is what the slice dispatcher shows: whether it is paused, the
// admission and the limits it was derived from, the queue in dispatch order,
// what runs, what is held, and the next launch.
type Snapshot struct {
	At          time.Time `json:"at"`
	Paused      bool      `json:"paused"`
	PauseReason string    `json:"pause_reason,omitempty"`
	// Capacity is how many dispatched sessions the limits admit at once.
	Capacity   int         `json:"capacity"`
	Limits     []Limit     `json:"limits"`
	LimitsAt   time.Time   `json:"limits_at"`
	NextPassAt time.Time   `json:"next_pass_at"`
	Queue      []SliceView `json:"queue"`
	Running    []SliceView `json:"running"`
	Held       []SliceView `json:"held"`
	// Finished is every merged, failed or canceled slice, in enqueue order:
	// the rest of the slice graph, so a reader can draw all of it.
	Finished []SliceView `json:"finished"`
	// NextLaunch is the queued slice the dispatcher launches next, absent
	// when none is ready.
	NextLaunch *SliceView `json:"next_launch,omitempty"`
}

// SliceView is one slice as the snapshot shows it.
type SliceView struct {
	SliceID        string `json:"slice_id"`
	Title          string `json:"title"`
	TicketURL      string `json:"ticket_url,omitempty"`
	State          string `json:"state"`
	Rank           uint32 `json:"rank,omitempty"`
	CriticalPath   uint32 `json:"critical_path"`
	Urgency        string `json:"urgency"`
	Attempts       uint32 `json:"attempts"`
	AssignmentID   string `json:"assignment_id,omitempty"`
	PullRequestURL string `json:"pull_request_url,omitempty"`
	Held           string `json:"held,omitempty"`
	// Source is the slice's provenance: who added it and why.
	Source string `json:"source,omitempty"`
	// Ready is true for a slice in the frontier: every slice it depends on
	// has merged.
	Ready bool `json:"ready"`
	// Waiting says why a queued slice does not run yet.
	Waiting string `json:"waiting,omitempty"`
	// DependsOn is the slices that must merge before this one; Contends the
	// slices it never runs beside.
	DependsOn []string `json:"depends_on,omitempty"`
	Contends  []string `json:"contends,omitempty"`
	// CreatedAt is when the slice was enqueued and UpdatedAt when its state
	// last moved: time in stage, and for a merged slice its lead time.
	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// ReadySlice is one slice that may run elsewhere now, with the recipe its
// session runs.
type ReadySlice struct {
	SliceID   string
	TicketURL string
	Recipe    *pb.AgentAssignmentRecipe
}
