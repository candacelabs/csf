// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/services/dispatch"
	"github.com/candacelabs/csf/services/dreamer"
)

// ISliceControls holds and releases one slice of the dispatcher's queue.
// *dispatch.DispatchService satisfies it.
type ISliceControls interface {
	Hold(ctx context.Context, input dispatch.SliceControlInput) (dispatch.Snapshot, error)
	Release(ctx context.Context, input dispatch.SliceControlInput) (dispatch.Snapshot, error)
}

// IDreamerControls pauses and resumes the dreamer. *dreamer.Dreamer
// satisfies it.
type IDreamerControls interface {
	Pause(ctx context.Context, input dreamer.ControlInput) (dreamer.Snapshot, error)
	Resume(ctx context.Context, input dreamer.ControlInput) (dreamer.Snapshot, error)
}

// The dreamer panel and the queue's controls on the wire.
const (
	// DreamerRegion is the dreamer's panel: its state, its strategy with the
	// held-out comparison, and its decision records.
	DreamerRegion = "opsview.dreamer"
	// DreamerFile is the snapshot the dreamer projects into the state
	// directory.
	DreamerFile = dreamer.SnapshotFile
	// EventDreamer carries the snapshot as JSON in FieldDreamer. Internal.
	EventDreamer = "opsview.dreamer"
	FieldDreamer = "dreamer"
	// EventControl is a queue or dreamer button: FieldAction names the
	// verified action it runs.
	EventControl = "opsview.control"
	// EventControlled carries the control's outcome in FieldNotice. Internal.
	EventControlled = "opsview.controlled"

	dreamerTemplate = "dreamer"
	actionSeparator = ":"
	queueHold       = "hold"
	queueRelease    = "release"
	actionPause     = "pause-dreamer"
	actionResume    = "resume-dreamer"
	controlReason   = "from the Workbench"
	buttonClass     = "act quiet"
	timeShown       = "2006-01-02 15:04"
	readOnlyControl = "This page is read-only: it was mounted without the queue controls."
)

// WithQueueControls gives the queue a hold or release button on every slice
// and the dreamer panel its pause and resume, each a verified action.
func WithQueueControls(slices ISliceControls, dreams IDreamerControls) Option {
	return func(view *OpsView) error {
		if slices == nil || dreams == nil {
			return ErrNoOperations
		}
		view.sliceControls, view.dreamerControls = slices, dreams
		return nil
	}
}

// controlRequest is one queue or dreamer control, as a verified action's
// request.
type controlRequest struct {
	Action  string
	SliceID string
}

// queueActions are the verified actions the page offers now: hold every
// queued slice that is not held, release every held one, and pause or
// resume the dreamer.
func queueActions(state viewState) []widget.VerifiedAction[controlRequest] {
	var actions []widget.VerifiedAction[controlRequest]
	if state.queue != nil {
		for _, view := range slices.Concat(state.queue.Queue, state.queue.Held) {
			action, label := queueHold, "Hold"
			if view.Held != "" {
				action, label = queueRelease, "Release"
			}
			actions = append(actions, widget.VerifiedAction[controlRequest]{ID: action + actionSeparator + view.SliceID, Label: label, Request: controlRequest{Action: action, SliceID: view.SliceID}})
		}
	}
	if state.dreamer != nil {
		action, label := actionPause, "Pause the dreamer"
		if state.dreamer.Paused {
			action, label = actionResume, "Resume the dreamer"
		}
		actions = append(actions, widget.VerifiedAction[controlRequest]{ID: action, Label: label, Request: controlRequest{Action: action}})
	}
	return actions
}

// checkControl is the dry run of a control: what it will do, or why the page
// cannot run it. The actions themselves come from the latest snapshots, so
// only what those snapshots allow is ever checked.
func (view *OpsView) checkControl(_ context.Context, request controlRequest) (widget.Expectation, error) {
	if view.sliceControls == nil {
		return widget.Expectation{}, errors.New(readOnlyControl)
	}
	switch request.Action {
	case queueHold:
		return widget.Expectation{Result: "Holds " + request.SliceID + ": the dispatcher does not launch it until it is released."}, nil
	case queueRelease:
		return widget.Expectation{Result: "Releases " + request.SliceID + ": the dispatcher may launch it within its admission."}, nil
	case actionPause:
		return widget.Expectation{Result: "Pauses the dreamer: its passes add and release nothing until it is resumed."}, nil
	case actionResume:
		return widget.Expectation{Result: "Resumes the dreamer: its next pass fills the frontier again."}, nil
	}
	return widget.Expectation{}, fmt.Errorf("unknown control %q", request.Action)
}

// verifiedControls checks every action the page offers now.
func (view *OpsView) verifiedControls(state viewState) []widget.Verified[controlRequest] {
	return widget.VerifyActions(context.Background(), queueActions(state), view.checkControl)
}

// reduceControl runs the verified action a button names, or says why not.
func (view *OpsView) reduceControl(state viewState, event live.Event) (viewState, []live.Effect[ViewerIdentity]) {
	action, found := widget.FindVerified(view.verifiedControls(state), event.Fields.Get(FieldAction))
	switch {
	case !found:
		state.controlNotice = "That control is no longer offered: the queue changed."
		return state, nil
	case !action.Enabled():
		state.controlNotice = action.Refusal
		return state, nil
	}
	state.controlNotice = action.Result
	request := action.Request
	return state, []live.Effect[ViewerIdentity]{{Source: EventControl, Run: func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
		notice, err := view.runControl(ctx, request)
		if err != nil {
			notice = request.Action + " failed: " + firstLine(err.Error())
		}
		return emit(live.Event{Name: EventControlled, FragmentID: QueueRegion, Fields: live.NewFields(map[string]string{FieldNotice: notice})})
	}}}
}

// runControl is the control's operation.
func (view *OpsView) runControl(ctx context.Context, request controlRequest) (string, error) {
	slice := dispatch.SliceControlInput{SliceID: request.SliceID, Reason: controlReason}
	dream := dreamer.ControlInput{Reason: controlReason}
	var err error
	switch request.Action {
	case queueHold:
		_, err = view.sliceControls.Hold(ctx, slice)
	case queueRelease:
		_, err = view.sliceControls.Release(ctx, slice)
	case actionPause:
		_, err = view.dreamerControls.Pause(ctx, dream)
	case actionResume:
		_, err = view.dreamerControls.Resume(ctx, dream)
	}
	return "Done: " + strings.ReplaceAll(request.Action, "-", " ") + " " + request.SliceID + ".", err
}

// controlButton draws the verified action named id, or nothing when the page
// does not offer it.
func controlButton(ctx context.Context, verified []widget.Verified[controlRequest], id string) (template.HTML, error) {
	action, found := widget.FindVerified(verified, id)
	if !found {
		return "", nil
	}
	rendered, err := renderComponent(ctx, widget.VerifiedButton(action, EventControl, FieldAction, buttonClass))
	// Only markup html/template already escaped crosses this boundary.
	return template.HTML(rendered), err
}

// DreamerEvent is the event the follow effect emits when the dreamer's
// snapshot changes; nil is no snapshot.
func DreamerEvent(snapshot *dreamer.Snapshot) (live.Event, error) {
	encoded := ""
	if snapshot != nil {
		content, err := json.Marshal(snapshot)
		if err != nil {
			return live.Event{}, err
		}
		encoded = string(content)
	}
	return live.Event{Name: EventDreamer, FragmentID: DreamerRegion, Fields: live.NewFields(map[string]string{FieldDreamer: encoded})}, nil
}

// deliverDreamer reads the dreamer's snapshot and emits the panel; a file
// that cannot be read is logged and the panel left as it was.
func (view *OpsView) deliverDreamer(emit live.Emitter) error {
	snapshot, readable := readSnapshot[dreamer.Snapshot](view, DreamerFile, "dreamer snapshot")
	if !readable {
		return nil
	}
	event, err := DreamerEvent(snapshot)
	if err != nil {
		return err
	}
	return emit(event)
}

// recordView is one decision record as the panel shows it.
type recordView struct {
	At      string
	Kind    dreamer.RecordKind
	Outcome dreamer.Outcome
	Reason  string
	Target  string
	Added   []dreamer.Added
	Ranked  []dreamer.Ranked
	Notes   []string
}

// dreamerView is the panel's data.
type dreamerView struct {
	Region      string
	Present     bool
	Paused      bool
	PauseReason string
	Button      template.HTML
	Explore     string
	Comparison  []string
	Generated   []string
	Records     []recordView
}

func (view *OpsView) renderDreamer(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		panel := dreamerView{Region: DreamerRegion, Present: state.dreamer != nil}
		if snapshot := state.dreamer; snapshot != nil {
			panel.Paused, panel.PauseReason, panel.Explore = snapshot.Paused, snapshot.PauseReason, fmt.Sprintf("%.0f", snapshot.Explore)
			id := actionPause
			if snapshot.Paused {
				id = actionResume
			}
			button, err := controlButton(ctx, view.verifiedControls(state), id)
			if err != nil {
				return err
			}
			panel.Button = button
			if strategy := snapshot.Strategy; strategy != nil {
				for _, partition := range []dreamer.Partition{strategy.All, strategy.InSample, strategy.HeldOut} {
					panel.Comparison = append(panel.Comparison, fmt.Sprintf("%s (%d runs): %s per week", partition.Name, partition.Runs, factorText(partition)))
				}
				panel.Comparison = append(panel.Comparison, strategy.ExploreDerivation)
			}
			for _, source := range slices.Sorted(maps.Keys(snapshot.Generated)) {
				panel.Generated = append(panel.Generated, fmt.Sprintf("%s %d", source, snapshot.Generated[source]))
			}
			for _, record := range snapshot.Decisions {
				panel.Records = append(panel.Records, recordOf(record))
			}
		}
		return views.ExecuteTemplate(writer, dreamerTemplate, panel)
	})
}

// factorText is a partition's weekly factor with its interval, or why there
// is none.
func factorText(partition dreamer.Partition) string {
	trend := partition.Factor
	if trend.Factor == nil {
		return fmt.Sprintf("no factor yet (%d active weeks of 3)", trend.Weeks)
	}
	return fmt.Sprintf("×%.3f (95%% %.3f–%.3f over %d weeks)", *trend.Factor, *trend.Low, *trend.High, trend.Weeks)
}

func recordOf(record dreamer.Record) recordView {
	entry := recordView{At: record.At.UTC().Format(timeShown), Kind: record.Kind}
	switch {
	case record.Decision != nil:
		decision := record.Decision
		entry.Outcome, entry.Reason, entry.Target = decision.Outcome, decision.Reason, decision.Target.Derivation
		entry.Added, entry.Ranked = decision.Added, decision.Ranked
		for _, released := range decision.Released {
			entry.Notes = append(entry.Notes, "released "+released)
		}
		for _, limit := range decision.Limits {
			entry.Notes = append(entry.Notes, string(limit.Name)+": "+limit.Derivation)
		}
		entry.Notes = append(entry.Notes, decision.Findings...)
	case record.Control != nil:
		entry.Reason = string(record.Control.Action) + ": " + record.Control.Reason
	}
	return entry
}

func dreamerChanged(previous viewState, next viewState) bool {
	before, _ := json.Marshal(previous.dreamer)
	after, _ := json.Marshal(next.dreamer)
	return string(before) != string(after)
}
