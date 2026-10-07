// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/csf/prod"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The golden metrics panel: the merge quality gate's verdict (csf/prod), read
// from golden_metrics.json under the state directory. The operator's ruling
// made the code metrics the quality gate — the seed files, the seed breaks, the
// chief violations per item, and the derived share, each compared between main
// and the merge result — so the panel shows the four numbers on each side and
// whether the gate refused the merge.
const (
	// GoldenRegion is the panel's region.
	GoldenRegion = "opsview.golden_metrics"
	// EventGolden carries the gate's verdict, as its JSON, in the
	// golden_metrics field. It is internal: the follow effect emits it, and a
	// browser may not.
	EventGolden = "opsview.golden_metrics"
	// FieldGolden is the field EventGolden carries the JSON in.
	FieldGolden = "golden_metrics"

	goldenTemplate = "golden_metrics"
)

// GoldenRow is one of the gate's four golden metrics, each number on both
// sides of the merge already rendered.
type GoldenRow struct {
	// Name is the metric: seed files, seed breaks, chief violations, or derived
	// share.
	Name string
	// Main is the value on main, already rendered.
	Main string
	// Merge is the value on the merge result, already rendered.
	Merge string
	// Refuses reports whether this metric alone refuses the merge.
	Refuses bool
}

// GoldenPanel is what the view shows of the merge quality gate's verdict: a
// pure projection of it, every number already rendered.
type GoldenPanel struct {
	Recorded      bool
	Gate          string
	MainRevision  string
	MergeRevision string
	Rows          []GoldenRow
	Refused       bool
	Reasons       []string
}

// equal compares two panels field by field.
func (panel GoldenPanel) equal(other GoldenPanel) bool {
	return panel.Recorded == other.Recorded && panel.Gate == other.Gate &&
		panel.MainRevision == other.MainRevision && panel.MergeRevision == other.MergeRevision &&
		slices.Equal(panel.Rows, other.Rows) && panel.Refused == other.Refused &&
		slices.Equal(panel.Reasons, other.Reasons)
}

// goldenPanelOf renders one snapshot.
func goldenPanelOf(snapshot prod.Snapshot) GoldenPanel {
	refused := make(map[prod.MergeGate]bool, len(snapshot.Reasons))
	for _, reason := range snapshot.Reasons {
		refused[reason] = true
	}
	panel := GoldenPanel{
		Recorded:      true,
		Gate:          snapshot.Gate,
		MainRevision:  snapshot.MainRevision,
		MergeRevision: snapshot.MergeRevision,
		Refused:       snapshot.Refused,
		Rows: []GoldenRow{
			{Name: "seed files", Main: fmt.Sprint(snapshot.SeedFilesMain), Merge: fmt.Sprint(snapshot.SeedFilesMerge), Refuses: refused[prod.MergeGateSeedFilesFall]},
			{Name: "seed breaks", Main: fmt.Sprint(0), Merge: fmt.Sprint(snapshot.SeedBreaks), Refuses: refused[prod.MergeGateSeedBreaks]},
			{Name: "chief violations", Main: fmt.Sprint(snapshot.ViolationsMain), Merge: fmt.Sprint(snapshot.ViolationsMerge), Refuses: refused[prod.MergeGateChiefViolationsRise]},
			{Name: "derived share", Main: fmt.Sprint(snapshot.DerivedShareMain), Merge: fmt.Sprint(snapshot.DerivedShareMerge), Refuses: refused[prod.MergeGateDerivedShareFalls]},
		},
	}
	for _, reason := range snapshot.Reasons {
		panel.Reasons = append(panel.Reasons, string(reason))
	}
	return panel
}

// decodeGolden reads a gate verdict off the wire. One that does not decode
// leaves the panel as it was.
func decodeGolden(text string) (GoldenPanel, bool) {
	var snapshot prod.Snapshot
	if json.Unmarshal([]byte(text), &snapshot) != nil {
		return GoldenPanel{}, false
	}
	return goldenPanelOf(snapshot), true
}

// GoldenEvent is the event the follow effect emits when the gate's verdict
// changed, addressed to the panel's own region.
func GoldenEvent(content []byte) live.Event {
	return live.Event{Name: EventGolden, FragmentID: GoldenRegion, Fields: live.NewFields(map[string]string{FieldGolden: string(content)})}
}

// goldenView is the panel region's data.
type goldenView struct {
	GoldenPanel
	Region string
}

// renderGolden draws the panel from its projection.
func renderGolden(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		return views.ExecuteTemplate(writer, goldenTemplate, goldenView{GoldenPanel: state.golden, Region: GoldenRegion})
	})
}

// goldenChanged reports whether the panel's rendered values moved.
func goldenChanged(previous viewState, next viewState) bool {
	return !previous.golden.equal(next.golden)
}
