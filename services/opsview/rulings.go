// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"io"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/services/harness/session"
)

// The rulings panel: every operator ruling in force, with the gate that
// enforces it or the UNENFORCED flag, and how many a gate enforces. The
// harness keeps the ruling records in one file at the root of the state
// directory (session.RulingsFile) and the panel follows it the way the
// resident panel follows its series.
const (
	// RulingsRegion is the panel's region.
	RulingsRegion = "opsview.rulings"
	// EventRulings carries the ruling records as the file holds them, in the
	// rulings field. It is internal: the follow effect emits it and a browser
	// may not.
	EventRulings = "opsview.rulings"
	// FieldRulings is the field the ruling records travel in.
	FieldRulings = "rulings"

	rulingsTemplate = "rulings"
)

// RulingsEvent is the event the follow effect emits when the ruling records
// changed, addressed to the panel.
func RulingsEvent(content []byte) live.Event {
	return live.Event{Name: EventRulings, FragmentID: RulingsRegion, Fields: live.NewFields(map[string]string{FieldRulings: string(content)})}
}

// rulingsView is the panel's data.
type rulingsView struct {
	Region     string
	Rulings    []rulingRow
	Enforced   int
	Total      int
	Unenforced int
}

// rulingRow is one ruling as the panel shows it.
type rulingRow struct {
	session.Ruling
	Gate string
}

// rulingsViewOf is the panel's data for the rulings in force.
func rulingsViewOf(rulings []session.Ruling) rulingsView {
	coverage := session.CoverageOf(rulings)
	view := rulingsView{Region: RulingsRegion, Enforced: coverage.Enforced, Total: coverage.Total, Unenforced: coverage.Unenforced()}
	for _, ruling := range rulings {
		view.Rulings = append(view.Rulings, rulingRow{Ruling: ruling, Gate: ruling.Enforcement()})
	}
	return view
}

// renderRulings draws the panel: nothing inside it until a ruling is
// recorded.
func renderRulings(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		return views.ExecuteTemplate(writer, rulingsTemplate, rulingsViewOf(state.rulings))
	})
}

// rulingsChanged reports whether the panel's markup moved.
func rulingsChanged(previous viewState, next viewState) bool {
	return previous.rulingRecords != next.rulingRecords
}

// rulingsFragment is the panel as the live library mounts it.
func rulingsFragment() live.Fragment[viewState] {
	return live.Fragment[viewState]{ID: RulingsRegion, Render: renderRulings, Dirty: rulingsChanged}
}

// reduceRulings folds the records an event carries into the panel's
// rulings; records that cannot be read leave the panel as it was.
func reduceRulings(state viewState, event live.Event) viewState {
	content := event.Fields.Get(FieldRulings)
	rulings, err := session.InForce([]byte(content))
	if err != nil {
		return state
	}
	state.rulings, state.rulingRecords = rulings, content
	return state
}
