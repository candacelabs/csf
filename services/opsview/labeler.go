// Copyright 2026 Candace Labs

package opsview

import (
	"fmt"
	"strconv"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/pkg/gotth/live"
	ouroborosv1 "github.com/candacelabs/csf/proto/candace/ouroboros/v1"
)

// The labeler panel's identity on the wire: one fragment above the board,
// fed by the run record the ouroboros labeler replaces under the state
// directory (services/ouroboros/labeler, RunFile).
const (
	LabelerRegion = "opsview.labeler"
	// EventLabeler carries the run record, as its JSON, in the run field. It
	// is internal: the follow effect emits it, and a browser may not.
	EventLabeler = "opsview.labeler"
	FieldRun     = "run"

	panelUnmeasured = "unmeasured"
	panelNone       = "none"
	panelTimeFormat = time.TimeOnly
	gigabyte        = 1e9
)

// LabelerPanel is what the view shows of the labeler: a pure projection of
// its run record, every number already rendered, so the fragment's dirty
// check is a comparison of strings.
type LabelerPanel struct {
	Recorded  bool
	Model     string
	Phase     string
	Current   string
	Tickets   string
	Labels    string
	Precision string
	Rate      string
	GPU       string
	KeepAlive string
	Yields    string
	Updated   string
}

// panelOf renders one run record.
func panelOf(run *ouroborosv1.LabelerRun) LabelerPanel {
	panel := LabelerPanel{
		Recorded:  true,
		Model:     run.GetModel(),
		Phase:     run.GetPhase(),
		Current:   panelNone,
		Tickets:   fmt.Sprintf("%d (%d with a real instance)", run.GetTickets(), run.GetTicketsWithInstance()),
		Labels:    fmt.Sprintf("%d proposed, %d accepted, %d rejected", run.GetProposed(), run.GetAccepted(), run.GetRejected()),
		Precision: panelUnmeasured,
		Rate:      strconv.FormatFloat(run.GetLabelsPerHour(), 'f', 1, 64) + " labels/h",
		GPU:       panelUnmeasured,
		KeepAlive: fmt.Sprintf("%d s (gap %d s, load %d s)", run.GetKeepAliveSeconds(), run.GetBatchGapSeconds(), run.GetLoadSeconds()),
		Yields:    strconv.FormatInt(run.GetYields(), 10),
		Updated:   time.Unix(run.GetUpdated(), 0).UTC().Format(panelTimeFormat),
	}
	if run.GetCurrent() > 0 {
		panel.Current = "#" + strconv.FormatInt(run.GetCurrent(), 10)
	}
	if run.GetHeldOut() > 0 {
		panel.Precision = fmt.Sprintf("%.0f%% of %d positives", run.GetPrecision()*100, run.GetHeldOut())
	}
	if run.GetGpuUtilization() >= 0 {
		panel.GPU = fmt.Sprintf("%.0f%% (%.1f GB)", run.GetGpuUtilization(), float64(run.GetGpuMemoryBytes())/gigabyte)
	}
	return panel
}

// decodePanel reads a run record off the wire. A record that does not decode
// leaves the panel as it was: the reducer is pure and reports nothing.
func decodePanel(text string) (LabelerPanel, bool) {
	var run ouroborosv1.LabelerRun
	if protojson.Unmarshal([]byte(text), &run) != nil {
		return LabelerPanel{}, false
	}
	return panelOf(&run), true
}

// LabelerEvent is the event the follow effect emits when the run record
// changed, addressed to the panel's own region.
func LabelerEvent(record []byte) live.Event {
	return live.Event{Name: EventLabeler, FragmentID: LabelerRegion, Fields: live.NewFields(map[string]string{FieldRun: string(record)})}
}
