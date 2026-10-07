// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"io"
	"strconv"
	"time"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/io/net/model/jev"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The JEV panel: the local decision model's runs, as `csf decide --ledger`
// records them. It follows the record replaced whole under the state
// directory (jev.DecideLedgerFile), and shows the day's decisions and tokens,
// the two numbers the ask names.
const (
	JevRegion = "opsview.jev"
	// EventJev carries the record, as its JSON, in FieldLedger. It is
	// internal: the follow effect emits it, and a browser may not.
	EventJev = "opsview.jev"

	jevTemplate = "jev"
)

// JevPanel is what the view shows of the decision record: a pure projection,
// every number already rendered, so the fragment's dirty check is a
// comparison of strings.
type JevPanel struct {
	Recorded        bool
	Day             string
	Model           string
	Updated         string
	Runs            string
	TokensPerDay    string
	DecisionsPerDay string
}

// jevPanelOf renders one decision record. The day it counts is the record's
// own last update, so equal records render equal markup.
func jevPanelOf(ledger jev.DecideLedger) JevPanel {
	if len(ledger.Runs) == 0 {
		return JevPanel{}
	}
	day := ledger.UpdatedAt
	panel := JevPanel{
		Recorded:        true,
		Day:             day.UTC().Format(time.DateOnly),
		Model:           ledger.Runs[len(ledger.Runs)-1].Model,
		Updated:         day.UTC().Format(panelTimeFormat),
		Runs:            strconv.Itoa(len(ledger.Runs)),
		TokensPerDay:    strconv.FormatInt(ledger.TokensOn(day), 10),
		DecisionsPerDay: strconv.FormatInt(ledger.DecisionsOn(day), 10),
	}
	return panel
}

// decodeJev reads a record off the wire. A record that holds no runs leaves
// the panel unrecorded: the reducer is pure and reports nothing.
func decodeJev(text string) (JevPanel, bool) {
	if text == "" {
		return JevPanel{}, false
	}
	return jevPanelOf(jev.ReadDecideLedger([]byte(text))), true
}

// JevEvent is the event the follow effect emits when the record changed,
// addressed to the panel's own region.
func JevEvent(record []byte) live.Event {
	return live.Event{Name: EventJev, FragmentID: JevRegion, Fields: live.NewFields(map[string]string{FieldLedger: string(record)})}
}

// jevView is the panel region's data.
type jevView struct {
	JevPanel
	Region string
}

// renderJev draws the panel from its projection.
func renderJev(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		return views.ExecuteTemplate(writer, jevTemplate, jevView{JevPanel: state.jev, Region: JevRegion})
	})
}

// jevChanged reports whether the panel's rendered values moved.
func jevChanged(previous viewState, next viewState) bool {
	return previous.jev != next.jev
}
