// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"encoding/json"
	"html/template"
	"io"
	"slices"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
	"github.com/candacelabs/csf/services/dispatch"
)

// The dispatch queue panel and the summary strip on the wire.
const (
	// QueueRegion is the dispatch queue's panel.
	QueueRegion = "opsview.queue"
	// QueueFile is the snapshot the slice dispatcher projects into the state
	// directory. The panel is empty until the file exists.
	QueueFile = dispatch.SnapshotFile
	// EventQueue carries the snapshot as JSON in FieldQueue, empty for none.
	// Internal.
	EventQueue = "opsview.queue"
	FieldQueue = "queue"
	// SummaryRegion is the first screen's tiles: running sessions against
	// capacity, merges on main today and spend today against the cap.
	SummaryRegion = "opsview.summary"

	queueTemplate   = "queue"
	summaryTemplate = "summary"
	usdMicrosPerUSD = 1e6
)

// queueView is the queue panel's data: the dispatcher's snapshot, present
// once the dispatcher has written one, with running slices first, then the
// queue in dispatch order, then the held ones.
type queueView struct {
	dispatch.Snapshot
	Region  string
	Present bool
	Slices  []queueRow
	Section sectionView
	Notice  string
}

// queueRow is one slice with its control: hold for a queued slice, release
// for a held one, none for a running one.
type queueRow struct {
	dispatch.SliceView
	Button template.HTML
}

func (view *OpsView) renderQueue(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		panel := queueView{Region: QueueRegion, Present: state.queue != nil, Notice: state.controlNotice, Section: sectionOf(state.prefs, sectionQueue)}
		if state.queue != nil {
			panel.Snapshot = *state.queue
			verified := view.verifiedControls(state)
			held := map[string]bool{}
			for _, slice := range state.queue.Held {
				held[slice.SliceID] = true
			}
			for _, slice := range slices.Concat(state.queue.Running, state.queue.Queue, state.queue.Held) {
				row := queueRow{SliceView: slice}
				if slice.State != dispatchv1.SliceState_SLICE_STATE_RUNNING.String() {
					action := queueHold
					if held[slice.SliceID] {
						action = queueRelease
					}
					button, err := controlButton(ctx, verified, action+actionSeparator+slice.SliceID)
					if err != nil {
						return err
					}
					row.Button = button
				}
				panel.Slices = append(panel.Slices, row)
			}
		}
		return views.ExecuteTemplate(writer, queueTemplate, panel)
	})
}

func queueChanged(previous viewState, next viewState) bool {
	before, _ := json.Marshal(previous.queue)
	after, _ := json.Marshal(next.queue)
	return string(before) != string(after) || sectionChanged(previous, next, sectionQueue) || previous.controlNotice != next.controlNotice
}

// QueueEvent is the event the follow effect emits when the queue snapshot
// changes; nil is no snapshot.
func QueueEvent(snapshot *dispatch.Snapshot) (live.Event, error) {
	encoded := ""
	if snapshot != nil {
		content, err := json.Marshal(snapshot)
		if err != nil {
			return live.Event{}, err
		}
		encoded = string(content)
	}
	return live.Event{Name: EventQueue, FragmentID: QueueRegion, Fields: live.NewFields(map[string]string{FieldQueue: encoded})}, nil
}

// deliverQueue reads the queue snapshot and emits the panel; a file that
// cannot be read is logged and the panel left as it was.
func (view *OpsView) deliverQueue(emit live.Emitter) error {
	snapshot, readable := readSnapshot[dispatch.Snapshot](view, QueueFile, "dispatch queue")
	if !readable {
		return nil
	}
	event, err := QueueEvent(snapshot)
	if err != nil {
		return err
	}
	return emit(event)
}
