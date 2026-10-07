// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"slices"
	"strings"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/services/ouroboros"
	viewservice "github.com/candacelabs/csf/services/views"
)

// The tiles, by the name EventDefine carries.
const (
	tileSessions = "sessions"
	tileRunning  = "running"
	tileMerges   = "merges"
	tileSpend    = "spend"
)

var tileNames = []string{tileSessions, tileRunning, tileMerges, tileSpend}

// tileView is one tile: its number, the line under it, and what it means.
type tileView struct {
	Name       string
	Label      string
	Value      string
	Detail     string
	Definition string
	// Panel is the tile's history: the metric's panel under the views
	// service.
	Panel    string
	Warn     bool
	Open     bool
	OnDefine template.HTMLAttr
}

// summaryView is the tiles region's data.
type summaryView struct {
	Region  string
	Tiles   []tileView
	Defined *tileView
}

// renderSummary draws the tiles: a pure function of the cards, the merges,
// the queue and the loop.
func renderSummary(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		summary, err := summaryOf(state)
		if err != nil {
			return err
		}
		return views.ExecuteTemplate(writer, summaryTemplate, summary)
	})
}

// dayOf is the day the tiles count: today by the view's clock, else the
// newest day the page has read anything for.
func dayOf(state viewState) string {
	if state.today != "" {
		return state.today
	}
	day := state.loop.Snapshot.Day
	for _, item := range state.cards.Items() {
		day = max(day, item.State.LastDay)
	}
	return day
}

func summaryOf(state viewState) (summaryView, error) {
	day := dayOf(state)
	running, sessionSpend, fixerSpend := 0, 0.0, 0.0
	for _, item := range state.cards.Items() {
		if item.State.Status == StatusRunning {
			running++
		}
		if isFixer(item.State) {
			fixerSpend += item.State.CostByDay[day]
		} else {
			sessionSpend += item.State.CostByDay[day]
		}
	}
	budget := state.loop.Snapshot.Fixers.BudgetUSD
	if !state.loop.Present {
		budget = float64(ouroboros.DefaultDailyBudgetUSDMicros) / usdMicrosPerUSD
	}

	sessionsTile := tileView{Name: tileSessions, Label: "Virtual / real", Value: "–", Panel: viewservice.PanelPath + "csf_sessions",
		Definition: "A virtual session is one harness session, one assignment, as the session service holds it. A real session is the executor process a virtual session runs its turns on; one is alive for each virtual session that is starting, running, open or canceling and not suspended (an idle session closes its executor and opens a new one on its next message). The ratio is virtual sessions per real session alive: the multiplexing factor."}
	if counts := state.sessions; counts.Present {
		running = counts.Running
		sessionsTile.Value = fmt.Sprintf("%d / %d", counts.Virtual, counts.Real)
		sessionsTile.Detail = fmt.Sprintf("%d open · %s", counts.Open, ratioLabel(counts))
		sessionsTile.Definition += fmt.Sprintf(" Now: %d virtual sessions, %d running a turn, %d open between turns (%d of them suspended), %d starting, %d canceled, %d failed, %d closed; %d real sessions alive.",
			counts.Virtual, counts.Running, counts.Open, counts.Suspended, counts.Starting, counts.Canceled, counts.Failed, counts.Closed, counts.Real)
	} else {
		sessionsTile.Detail = "not read"
		sessionsTile.Definition += " This page was mounted without the session service, so it has no count."
	}

	runningTile := tileView{Name: tileRunning, Label: "Running", Value: fmt.Sprint(running), Panel: viewservice.PanelPath + "csf_sessions",
		Definition: "Virtual sessions running a turn now. One whose log still says running from before this host started is stalled, not running."}
	if state.queue != nil {
		runningTile.Detail = fmt.Sprintf("of %d slots", state.queue.Capacity)
		runningTile.Definition += fmt.Sprintf(" Slots are how many dispatched virtual sessions the dispatcher's limits admit at once: %d now.", state.queue.Capacity)
		runningTile.Warn = running > state.queue.Capacity
	} else {
		runningTile.Detail = "no dispatcher"
		runningTile.Definition += " No dispatcher has written its snapshot, so there is no capacity to compare against."
	}

	mergesTile := tileView{Name: tileMerges, Label: "Merged", Value: "–", Detail: "today, to main", Panel: viewservice.PanelPath + "csf_merges_total"}
	switch {
	case state.merges.Present:
		mergesTile.Value = fmt.Sprint(len(state.merges.Pulls))
		mergesTile.Definition = "Pull requests merged into main on " + state.merges.Day + " (UTC), as GitHub lists them, in every repository a virtual session's pull request lives in. Every merge counts, whoever made it."
	default:
		mergesTile.Detail = "not read"
		mergesTile.Definition = "Pull requests merged into main today (UTC), as GitHub lists them. The page reads them when it connects and whenever a virtual session ends a turn; it has not read them yet, or was mounted without GitHub access."
	}

	spendTile := tileView{Name: tileSpend, Label: "Spend today", Value: fmt.Sprintf(usdFormat, sessionSpend+fixerSpend), Panel: viewservice.PanelPath + "csf_cost_usd_total",
		Detail: fmt.Sprintf("fixers "+usdFormat+" / "+usdFormat, fixerSpend, budget), Warn: fixerSpend >= budget,
		Definition: fmt.Sprintf("Model spend on %s (UTC), read from every virtual session's result records: what each one's reported cost rose by that day. "+
			"Virtual sessions other than fixers "+usdFormat+"; fixers "+usdFormat+". Fixers are the virtual sessions the mining loop launches, and the "+usdFormat+" cap is the loop's daily limit on their spend, which the loop enforces.",
			day, sessionSpend, fixerSpend, budget)}

	summary := summaryView{Region: SummaryRegion, Tiles: []tileView{sessionsTile, runningTile, mergesTile, spendTile}}
	for index := range summary.Tiles {
		tile := &summary.Tiles[index]
		onDefine, err := attributes(live.OnWith(domClick, EventDefine, live.Bind{Fields: map[string]string{FieldTile: tile.Name}}))
		if err != nil {
			return summaryView{}, err
		}
		tile.OnDefine = template.HTMLAttr(onDefine)
		if tile.Name == state.prefs.Defined {
			tile.Open = true
			summary.Defined = tile
		}
	}
	return summary, nil
}

func summaryChanged(previous viewState, next viewState) bool {
	before, errBefore := summaryOf(previous)
	after, errAfter := summaryOf(next)
	return errBefore != nil || errAfter != nil || !slices.Equal(before.Tiles, after.Tiles)
}

// The lower sections a viewer unfolds, by the name EventSection carries.
const (
	sectionLaunch      = "launch"
	sectionLoop        = "loop"
	sectionQueue       = "queue"
	sectionMerges      = "merges"
	sectionHost        = "host"
	sectionEndpoints   = "endpoints"
	sectionLabeler     = "labeler"
	sectionCosts       = "costs"
	sectionJev         = "jev"
	sectionCertificate = "certificate"
	sectionGolden      = "golden_metrics"
	sectionCloud       = "cloud"
	sectionLine        = "line"
	sectionGitHub      = "github"
)

// mergesShown is how many of the day's merges the board lists before the
// viewer unfolds the rest: on 2026-10-05 a host merged 19 pull requests by
// 02:30 UTC, which listed whole outgrew a phone screen on their own.
const mergesShown = 3

var foldableSections = []string{sectionLaunch, sectionLoop, sectionQueue, sectionMerges, sectionHost, sectionEndpoints, sectionLabeler, sectionCosts, sectionJev, sectionCertificate, sectionGolden, sectionCloud, sectionLine, sectionGitHub}

// sectionView is a foldable section's header: whether it is open and the
// binding that toggles it.
type sectionView struct {
	Open     bool
	OnToggle template.HTMLAttr
}

func sectionOf(prefs ViewPrefs, section string) sectionView {
	toggle, _ := attributes(live.OnWith(domClick, EventSection, live.Bind{Fields: map[string]string{FieldSection: section}}))
	return sectionView{Open: slices.Contains(prefs.Opened, section), OnToggle: template.HTMLAttr(toggle)}
}

func sectionChanged(previous viewState, next viewState, section string) bool {
	return slices.Contains(previous.prefs.Opened, section) != slices.Contains(next.prefs.Opened, section)
}

// ratioLabel is virtual sessions per real session alive, ×1.0.
func ratioLabel(counts SessionCounts) string {
	if counts.Real == 0 {
		return "no real alive"
	}
	return fmt.Sprintf("×%.1f", float64(counts.Virtual)/float64(counts.Real))
}

// foldedPanel wraps a panel that renders its own region so it shows as one
// line until the viewer unfolds it: folded, the region holds only its header;
// unfolded, the panel's own markup with the header first inside its root.
func foldedPanel(region string, section string, title string, render func(state viewState) templ.Component) func(state viewState) templ.Component {
	return func(state viewState) templ.Component {
		return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
			fold := sectionOf(state.prefs, section)
			head := fmt.Sprintf(`<button type="button" class="fold-head" aria-expanded="%t" data-opsview="%s-toggle" %s><span>%s</span><span class="chevron" aria-hidden="true"></span></button>`,
				fold.Open, section, fold.OnToggle, template.HTMLEscapeString(title))
			if !fold.Open {
				_, err := fmt.Fprintf(writer, `<section id="%s" data-gotth-region="%s" class="fold" aria-label="%s">%s</section>`,
					template.HTMLEscapeString(region), template.HTMLEscapeString(region), template.HTMLEscapeString(title), head)
				return err
			}
			inner, err := renderComponent(ctx, render(state))
			if err != nil {
				return err
			}
			opening := strings.Index(inner, ">") + 1
			_, err = io.WriteString(writer, inner[:opening]+head+inner[opening:])
			return err
		})
	}
}

// foldedDirty is a folded panel's dirty check: its own, or its fold moving.
func foldedDirty(section string, dirty func(previous viewState, next viewState) bool) func(previous viewState, next viewState) bool {
	return func(previous viewState, next viewState) bool {
		return dirty(previous, next) || sectionChanged(previous, next, section)
	}
}
