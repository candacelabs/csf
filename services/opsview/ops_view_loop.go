// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"

	"github.com/a-h/templ"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/services/ouroboros"
)

// The loop panel on the wire: one internal event carrying the panel whole,
// read from the snapshot the mining loop projects into the state directory.
const (
	// EventLoop carries the panel as JSON in FieldLoop. Internal: the follow
	// effect emits it, a browser may not.
	EventLoop = "opsview.loop"
	FieldLoop = "loop"
	// LoopFile is the snapshot the loop writes, under the state directory.
	LoopFile = ouroboros.SnapshotFile

	loopTemplate          = "loop"
	noValue               = "–"
	rateFormat            = "%.1f"
	factorFormat          = "×%.3f"
	slopeFormat           = "%+.3f"
	perInterventionFormat = "%.4f"
	usdFormat             = "$%.2f"
	percentFormat         = "%.0f%%"
	percentScale          = 100
)

// LoopPanel is what the ops view shows of the mining loop: the loop's
// snapshot, present once the loop has written one.
type LoopPanel struct {
	Present  bool               `json:"present"`
	Snapshot ouroboros.Snapshot `json:"snapshot"`
}

// loopView is the panel's template data: every number already rendered as
// the text it shows, so the template holds no arithmetic.
type loopView struct {
	Section     sectionView
	Region      string
	Present     bool
	ComputedAt  string
	Day         string
	Rate        string
	Interval    string
	ToolCalls   int64
	Struggles   int64
	WeekStart   string
	WeekRate    string
	WeekRange   string
	WeekCalls   int64
	WeekHits    int64
	Factor      string
	FactorRange string
	FactorWeeks int
	Baseline    string
	BaseRange   string
	BaseRate    string
	BaseWeek    string
	DailyFactor string
	DailyRange  string
	DailyDays   int
	Structure   []structureView
	Exponent    string
	ExpRange    string
	ExpWeeks    int
	Proxy       string
	Generic     int
	Tenant      int
	Findings    int
	Today       int
	PerDay      string
	Sessions    int
	Running     int
	Ready       int
	Spend       string
	SpendToday  string
	Budget      string
	CostPerPull string
	Yield       string
	Launch      bool
	Merged      int
	Refused     int
	Queue       int
	Miners      []ouroboros.MinerStatus
	Days        []dayView
}

type dayView struct {
	Day       string
	ToolCalls int64
	Struggles int64
	Rate      string
}

type structureView struct {
	Week            string
	Gates           int
	Miners          int
	Terms           int
	Gained          int
	Interventions   int64
	PerIntervention string
}

// renderLoop draws the panel: a pure function of the panel state.
func renderLoop(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		view := loopViewOf(state.loop)
		view.Section = sectionOf(state.prefs, sectionLoop)
		return views.ExecuteTemplate(writer, loopTemplate, view)
	})
}

func loopViewOf(panel LoopPanel) loopView {
	snapshot := panel.Snapshot
	view := loopView{
		Region: LoopRegion, Present: panel.Present, Day: snapshot.Day,
		ToolCalls: snapshot.Struggle.ToolCalls, Struggles: snapshot.Struggle.Struggles,
		Rate: optional(snapshot.Struggle.PerK, rateFormat), Interval: interval(snapshot.Struggle.Low, snapshot.Struggle.High, rateFormat),
		WeekStart: snapshot.Week.Day, WeekRate: optional(snapshot.Week.PerK, rateFormat), WeekRange: interval(snapshot.Week.Low, snapshot.Week.High, rateFormat),
		WeekCalls: snapshot.Week.ToolCalls, WeekHits: snapshot.Week.Struggles,
		Factor: optional(snapshot.Compounding.Factor, factorFormat), FactorRange: interval(snapshot.Compounding.Low, snapshot.Compounding.High, factorFormat),
		FactorWeeks: snapshot.Compounding.Weeks,
		Baseline:    fmt.Sprintf(factorFormat, snapshot.Baseline.Factor), BaseRange: fmt.Sprintf(factorFormat, snapshot.Baseline.Low) + "–" + fmt.Sprintf(factorFormat, snapshot.Baseline.High),
		BaseRate: fmt.Sprintf(rateFormat, snapshot.Baseline.RatePerK), BaseWeek: snapshot.Baseline.Week,
		DailyFactor: optional(snapshot.CompoundingDaily.Factor, factorFormat), DailyRange: interval(snapshot.CompoundingDaily.Low, snapshot.CompoundingDaily.High, factorFormat),
		DailyDays: snapshot.CompoundingDaily.Weeks,
		Exponent:  optional(snapshot.StructureExponent.Slope, slopeFormat), ExpRange: interval(snapshot.StructureExponent.Low, snapshot.StructureExponent.High, slopeFormat),
		ExpWeeks: snapshot.StructureExponent.Weeks, Proxy: snapshot.StructureExponent.Proxy,
		Generic: snapshot.GenericMiners, Tenant: snapshot.TenantMiners,
		Findings: snapshot.Findings, Today: snapshot.FindingsToday, PerDay: optional(snapshot.FindingsPerDay, rateFormat),
		Sessions: snapshot.Fixers.Sessions, Running: snapshot.Fixers.Running, Ready: snapshot.Fixers.Ready,
		Spend: fmt.Sprintf(usdFormat, snapshot.Fixers.SpendUSD), SpendToday: fmt.Sprintf(usdFormat, snapshot.Fixers.SpendTodayUSD),
		Budget: fmt.Sprintf(usdFormat, snapshot.Fixers.BudgetUSD), CostPerPull: optional(snapshot.Fixers.CostPerReady, usdFormat),
		Launch: snapshot.Fixers.Launch, Merged: snapshot.Merges.Merged, Refused: snapshot.Merges.Refused, Queue: snapshot.Queue,
		Miners: snapshot.Miners,
	}
	if !snapshot.ComputedAt.IsZero() {
		view.ComputedAt = snapshot.ComputedAt.UTC().Format(recentTimeFormat)
	}
	if snapshot.Fixers.Yield != nil {
		view.Yield = fmt.Sprintf(percentFormat, *snapshot.Fixers.Yield*percentScale)
	} else {
		view.Yield = noValue
	}
	for _, day := range snapshot.Days {
		view.Days = append(view.Days, dayView{Day: day.Day, ToolCalls: day.ToolCalls, Struggles: day.Struggles, Rate: optional(day.PerK, rateFormat)})
	}
	for _, week := range snapshot.Structure {
		view.Structure = append(view.Structure, structureView{
			Week: week.Week, Gates: week.Counts.Gates, Miners: week.Counts.Miners, Terms: week.Counts.Terms, Gained: week.Gained,
			Interventions: week.Interventions, PerIntervention: optional(week.PerIntervention, perInterventionFormat),
		})
	}
	return view
}

func optional(value *float64, format string) string {
	if value == nil {
		return noValue
	}
	return fmt.Sprintf(format, *value)
}

func interval(low *float64, high *float64, format string) string {
	if low == nil || high == nil {
		return noValue
	}
	return fmt.Sprintf(format, *low) + "–" + fmt.Sprintf(format, *high)
}

// loopChanged reports whether the panel moved: its wire form differs.
func loopChanged(previous viewState, next viewState) bool {
	before, _ := json.Marshal(previous.loop)
	after, _ := json.Marshal(next.loop)
	return string(before) != string(after) || sectionChanged(previous, next, sectionLoop)
}

// LoopEvent is the event the follow effect emits when the loop's snapshot
// changes.
func LoopEvent(panel LoopPanel) (live.Event, error) {
	encoded, err := json.Marshal(panel)
	if err != nil {
		return live.Event{}, err
	}
	return live.Event{Name: EventLoop, FragmentID: LoopRegion, Fields: live.NewFields(map[string]string{FieldLoop: string(encoded)})}, nil
}

// readLoop reads the loop's snapshot from the state directory; no file is
// no panel, and a file being replaced reads again on its next change.
func readLoop(files iofs.IFiles) (LoopPanel, error) {
	content, err := files.ReadFile(LoopFile)
	if errors.Is(err, stdfs.ErrNotExist) {
		return LoopPanel{}, nil
	}
	if err != nil {
		return LoopPanel{}, err
	}
	var snapshot ouroboros.Snapshot
	if err := json.Unmarshal(content, &snapshot); err != nil {
		return LoopPanel{}, fmt.Errorf("ops view: decode %s: %w", LoopFile, err)
	}
	return LoopPanel{Present: true, Snapshot: snapshot}, nil
}

// deliverLoop reads the snapshot and emits the panel.
func (view *OpsView) deliverLoop(emit live.Emitter) error {
	panel, err := readLoop(view.files)
	if err != nil {
		view.logger.Warn("ops view: loop snapshot not read", "error", err)
		return nil
	}
	event, err := LoopEvent(panel)
	if err != nil {
		return err
	}
	return emit(event)
}
