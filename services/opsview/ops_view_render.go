// Copyright 2026 Candace Labs

package opsview

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/a-h/templ"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/services/harness/session"
)

// The page, the board region and one card. Every render is a pure function
// of state: equal states render byte-identical markup, which is what lets
// the library suppress a patch nobody needs.
//
//go:embed view.html
var pageSource string

// The Workbench's design tokens and its stylesheet, one file: color, type
// scale, spacing, radius and motion as custom properties, light and dark. An
// operator's workbench-theme.css is applied after it and overrides tokens.
//
//go:embed workbench.css
var workbenchStyle string

// Stylesheet is the Workbench's token set and stylesheet, for the other pages
// CSF serves beside it — the harness chat — so they share one design system.
func Stylesheet() string { return workbenchStyle }

// ChatPathPrefix is where csf serve mounts a session's chat: the prefix
// followed by the assignment. The chat service names its route from it, and
// every Workbench card links there.
const ChatPathPrefix = "/chat/"

var views = template.Must(template.New("opsview").Funcs(template.FuncMap{"pullNumber": pullRequestLabel}).Parse(pageSource))

const (
	pageTemplate    = "page"
	boardTemplate   = "board"
	cardTemplate    = "card"
	launchTemplate  = "launch"
	labelerTemplate = "labeler"
)

// pageView is the document's data: every region already rendered, in the
// order the first screen shows them, and the runtime tag.
type pageView struct {
	Regions []template.HTML
	Script  template.HTML
	// Style is the token set and the page's stylesheet, Theme the operator's
	// overrides, and WidgetStyle the widget SDK's stylesheet that installed
	// widgets read.
	Style       template.CSS
	Theme       template.CSS
	WidgetStyle template.CSS
}

// labelerView is the labeler panel region's data: its region and the panel.
type labelerView struct {
	LabelerPanel
	Region string
}

// The folded panels' headers.
const (
	hostTitle        = "Host: load, memory, disk and containers"
	endpointsTitle   = "Endpoints"
	labelerTitle     = "Ouroboros labeler"
	costsTitle       = "Session costs"
	jevTitle         = "Local decision model"
	certificateTitle = "Choice certificate"
	goldenTitle      = "Golden metrics"
)

// The groups the board shows sessions in, in the order it shows them.
const (
	groupNeeds   = "needs"
	groupRunning = "running"
	groupOther   = "other"
)

// boardView is the board region's data: each group's rows, already
// rendered, the merges of the day, the idle list's controls, and the notice.
type boardView struct {
	Region  string
	Notice  string
	Needs   []template.HTML
	Running []template.HTML
	Others  []template.HTML
	// OtherCount is every session in the idle group, Matching those the
	// filter lets through.
	OtherCount int
	Matching   int
	Prefs      ViewPrefs
	Statuses   []Status
	Merges     []MergedPullRequest
	// MergedCount is every merge of the day; Merges shows the latest few
	// until the viewer unfolds the list.
	MergedCount int
	MergesOpen  bool
	MoreMerges  bool
	OnMerges    template.HTMLAttr
	OnOthers    template.HTMLAttr
	OnFilter    template.HTMLAttr
	Preserve    template.HTMLAttr
	Transition  template.HTMLAttr
	Loaded      bool
}

// cardView is one card's data: its state, its region, the recent events it
// shows, how many it holds back, which actions it offers, and each control's
// attributes.
type cardView struct {
	SessionCard
	Region     string
	Name       string
	Summary    string
	Cost       string
	Stage      string
	Steps      string
	ChatURL    string
	Shown      []RecentEvent
	Hidden     int
	CanSend    bool
	CanReady   bool
	CanMerge   bool
	CanCancel  bool
	Preserve   template.HTMLAttr
	OnExpand   template.HTMLAttr
	OnSend     template.HTMLAttr
	OnCancel   template.HTMLAttr
	OnReady    template.HTMLAttr
	OnMerge    template.HTMLAttr
	OnCompose  template.HTMLAttr
	OnInternal template.HTMLAttr
	OnAskMerge template.HTMLAttr
	OnAskStop  template.HTMLAttr
	OnKeep     template.HTMLAttr
}

// The DOM events the page binds.
const (
	domClick  = "click"
	domSubmit = "submit"
	domInput  = "input"
)

// filterDebounce is how long the idle list's filter waits after the last
// keystroke before it asks the server: one request per pause, not per key.
const filterDebounce = 200 * time.Millisecond

// cardBindings are the card's controls and the event each raises.
var cardBindings = []struct {
	binding templ.Attributes
	into    func(view *cardView) *template.HTMLAttr
}{
	{live.On(domClick, EventExpand), func(view *cardView) *template.HTMLAttr { return &view.OnExpand }},
	{live.On(domSubmit, EventSend), func(view *cardView) *template.HTMLAttr { return &view.OnSend }},
	{live.On(domClick, EventCancel), func(view *cardView) *template.HTMLAttr { return &view.OnCancel }},
	{live.On(domClick, EventReady), func(view *cardView) *template.HTMLAttr { return &view.OnReady }},
	{live.On(domClick, EventMerge), func(view *cardView) *template.HTMLAttr { return &view.OnMerge }},
	{live.On(domClick, EventCompose), func(view *cardView) *template.HTMLAttr { return &view.OnCompose }},
	{live.On(domClick, EventInternals), func(view *cardView) *template.HTMLAttr { return &view.OnInternal }},
	{live.OnWith(domClick, EventConfirm, live.Bind{Fields: map[string]string{FieldAction: EventMerge}}), func(view *cardView) *template.HTMLAttr { return &view.OnAskMerge }},
	{live.OnWith(domClick, EventConfirm, live.Bind{Fields: map[string]string{FieldAction: EventCancel}}), func(view *cardView) *template.HTMLAttr { return &view.OnAskStop }},
	{live.OnWith(domClick, EventConfirm, live.Bind{Fields: map[string]string{FieldAction: ""}}), func(view *cardView) *template.HTMLAttr { return &view.OnKeep }},
	{live.Preserve(), func(view *cardView) *template.HTMLAttr { return &view.Preserve }},
}

// stageLabels are a pull request's stages as a row shows them.
var stageLabels = map[PullRequestStage]string{
	"":                 "open",
	PullRequestReady:   "ready",
	PullRequestMerging: "merging",
	PullRequestMerged:  "merged",
}

// executorChoices are the executors the page offers, as recipes spell them.
var executorChoices = []string{string(session.ExecutorClaudeCode), string(session.ExecutorCopilot)}

func renderCard(region string, state SessionCard, operable bool) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		shown := state.Recent
		if !state.Expanded && len(shown) > recentShown {
			shown = shown[len(shown)-recentShown:]
		}
		open := operable && state.Status != StatusClosed
		merged := state.PullRequest == PullRequestMerged || state.Landed
		view := cardView{
			SessionCard: state, Region: region, Name: nameOf(state), Summary: summaryLine(state), Cost: costLabel(state), Steps: counted(state.ToolCalls, "step"), ChatURL: ChatPathPrefix + state.Assignment,
			Shown: shown, Hidden: len(state.Recent) - len(shown), CanSend: open, CanCancel: open,
			CanReady: operable && state.PullRequestURL != "" && state.PullRequest == "" && !merged,
			CanMerge: operable && state.PullRequestURL != "" && !merged && state.PullRequest != PullRequestMerging,
		}
		if state.PullRequestURL != "" {
			view.Stage = stageLabels[state.PullRequest]
			if merged {
				view.Stage = stageLabels[PullRequestMerged]
			}
		}
		for _, binding := range cardBindings {
			rendered, err := attributes(binding.binding)
			if err != nil {
				return err
			}
			*binding.into(&view) = template.HTMLAttr(rendered)
		}
		return views.ExecuteTemplate(writer, cardTemplate, view)
	})
}

// nameOf is what a row is called: the agent, which names the slice.
func nameOf(card SessionCard) string {
	if card.Agent != "" {
		return card.Agent
	}
	return "new session"
}

// summaryLine is the one line under a row's name: what it is doing while it
// runs, what went wrong when it failed, and otherwise what it last said.
func summaryLine(card SessionCard) string {
	switch {
	case card.Status == StatusFailed && card.Problem != "":
		return card.Problem
	case card.Status == StatusRunning && card.Activity != "":
		return card.Activity
	case card.Status == StatusStalled:
		return "Stalled: the host running it stopped mid-turn"
	case card.Said != "":
		return card.Said
	case card.Activity != "":
		return card.Activity
	}
	return "Waiting for its first event"
}

// costLabel is the session's spend so far, empty when only the end of its log
// was read and the total is not known.
func costLabel(card SessionCard) string {
	if card.Partial {
		return ""
	}
	total := 0.0
	for _, spent := range card.CostByDay {
		total += spent
	}
	if total == 0 {
		return ""
	}
	return fmt.Sprintf(usdFormat, total)
}

// pullRequestLabel is #N for a pull request URL.
func pullRequestLabel(url string) string {
	if number := path.Base(url); number != "" && number != "." && strings.Contains(url, pullRequestPathMark) {
		return "#" + number
	}
	return "pull request"
}

// groupOf is the board group a session shows in. A session needs the
// operator when, today, it failed, asked a question, or finished its turn
// with a pull request not yet merged, and whenever a merge it started is in
// flight; one that runs shows as running; everything else is idle or
// finished, behind one count.
func groupOf(card SessionCard, today string) string {
	merged := card.PullRequest == PullRequestMerged || card.Landed
	if card.PullRequest == PullRequestMerging {
		return groupNeeds
	}
	if card.Status == StatusRunning {
		return groupRunning
	}
	if card.LastDay == today && card.Status != StatusClosed &&
		(card.Status == StatusFailed || card.Question || card.PullRequestURL != "" && !merged) {
		return groupNeeds
	}
	return groupOther
}

// matches is whether a card in the idle group passes the viewer's filter.
func matches(card SessionCard, prefs ViewPrefs) bool {
	if prefs.Show != "" && card.Status != prefs.Show {
		return false
	}
	if prefs.Filter == "" {
		return true
	}
	needle := strings.ToLower(prefs.Filter)
	for _, haystack := range []string{card.Agent, card.Branch, card.TicketURL, card.PullRequestURL, card.Said, card.Activity, card.Assignment} {
		if strings.Contains(strings.ToLower(haystack), needle) {
			return true
		}
	}
	return false
}

// layout is the board's rows by group, in display order: the groups' sets and
// orders are what the board's own markup depends on.
type layout struct {
	needs, running, others []string
	otherCount             int
}

func layoutOf(state viewState) layout {
	today := dayOf(state)
	var board layout
	for _, key := range displayOrder(state.cards) {
		card, _ := state.cards.Get(key)
		switch groupOf(card, today) {
		case groupNeeds:
			board.needs = append(board.needs, key)
		case groupRunning:
			board.running = append(board.running, key)
		default:
			board.otherCount++
			if state.prefs.OthersOpen && matches(card, state.prefs) {
				board.others = append(board.others, key)
			}
		}
	}
	return board
}

// render draws the board: each group's rows through the collection, so the
// markup is the member's own render, and the merges of the day.
func (view *OpsView) render(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		board := boardView{Region: BoardRegion, Notice: state.notice, Prefs: state.prefs, Statuses: otherStatuses, Merges: state.merges.Pulls, Loaded: state.loaded}
		board.MergedCount, board.MergesOpen = len(board.Merges), slices.Contains(state.prefs.Opened, sectionMerges)
		board.MoreMerges = board.MergedCount > mergesShown
		if !board.MergesOpen && len(board.Merges) > mergesShown {
			board.Merges = board.Merges[:mergesShown]
		}
		onMerges, err := attributes(live.OnWith(domClick, EventSection, live.Bind{Fields: map[string]string{FieldSection: sectionMerges}}))
		if err != nil {
			return err
		}
		board.OnMerges = template.HTMLAttr(onMerges)
		rows := layoutOf(state)
		board.OtherCount, board.Matching = rows.otherCount, len(rows.others)
		for _, group := range []struct {
			keys []string
			into *[]template.HTML
		}{{rows.needs, &board.Needs}, {rows.running, &board.Running}, {rows.others, &board.Others}} {
			for _, key := range group.keys {
				var content bytes.Buffer
				if err := view.cards.RenderItem(state.cards, key).Render(ctx, &content); err != nil {
					return err
				}
				// Only markup html/template already escaped crosses this boundary.
				*group.into = append(*group.into, template.HTML(content.String()))
			}
		}
		for _, binding := range []struct {
			binding templ.Attributes
			into    *template.HTMLAttr
		}{
			{live.On(domClick, EventOthers), &board.OnOthers},
			{live.OnAll(live.OnWith(domInput, EventFilter, live.Bind{Debounce: filterDebounce}), live.On(domSubmit, EventFilter)), &board.OnFilter},
			{live.Preserve(), &board.Preserve},
		} {
			rendered, err := attributes(binding.binding)
			if err != nil {
				return err
			}
			*binding.into = template.HTMLAttr(rendered)
		}
		// Rows that move between groups or reorder animate, once the first
		// read is done: the first read's arrivals are not changes.
		if state.loaded {
			transition, err := attributes(live.Transition())
			if err != nil {
				return err
			}
			board.Transition = template.HTMLAttr(transition)
		}
		return views.ExecuteTemplate(writer, boardTemplate, board)
	})
}

// launchView is the launch form's data.
type launchView struct {
	launchState
	Section  sectionView
	Region   string
	Operable bool
	Checked  bool
	OnCheck  template.HTMLAttr
	OnLaunch template.HTMLAttr
	// OnExecutor and Executors are the default executor form's wiring and
	// choices.
	OnExecutor template.HTMLAttr
	Executors  []string
}

func (view *OpsView) renderLaunch(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		onCheck, err := attributes(live.On(domSubmit, EventCheck))
		if err != nil {
			return err
		}
		onLaunch, err := attributes(live.On(domClick, EventLaunch))
		if err != nil {
			return err
		}
		onExecutor, err := attributes(live.On(domSubmit, EventExecutor))
		if err != nil {
			return err
		}
		section := sectionOf(state.prefs, sectionLaunch)
		checked := state.launch.Recipe != ""
		// A check, a launch in flight or its outcome keeps the form open.
		section.Open = section.Open || checked || state.launch.Pending || state.launch.Notice != ""
		return views.ExecuteTemplate(writer, launchTemplate, launchView{
			launchState: state.launch, Section: section, Region: LaunchRegion, Operable: view.operations != nil && view.templates != nil,
			Checked: checked, OnCheck: template.HTMLAttr(onCheck), OnLaunch: template.HTMLAttr(onLaunch),
			OnExecutor: template.HTMLAttr(onExecutor), Executors: executorChoices,
		})
	})
}

func launchChanged(previous viewState, next viewState) bool {
	before, after := previous.launch, next.launch
	return !proto.Equal(before.Check, after.Check) || !slices.Equal(before.Templates, after.Templates) ||
		before.Ticket != after.Ticket || before.Template != after.Template || before.Recipe != after.Recipe ||
		before.Held != after.Held || before.Pending != after.Pending || before.Notice != after.Notice ||
		sectionChanged(previous, next, sectionLaunch)
}

// attributes renders templ attributes as the text an element carries, so the
// template writes the library's own spelling of its wiring.
func attributes(attributes templ.Attributes) (string, error) {
	var buffer bytes.Buffer
	if err := templ.RenderAttributes(context.Background(), &buffer, attributes); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

// renderLabeler draws the labeler panel from its projection.
func (view *OpsView) renderLabeler(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		return views.ExecuteTemplate(writer, labelerTemplate, labelerView{LabelerPanel: state.labeler, Region: LabelerRegion})
	})
}

// labelerChanged reports whether the labeler panel's rendered values moved.
func labelerChanged(previous viewState, next viewState) bool {
	return previous.labeler != next.labeler
}

// renderPage draws the document: the tiles, the board, the host panel, the
// endpoints, the launch form, the loop, resident executors, the operator's
// rulings, the dreamer, the queue, the miners, the labeler, the cost model, the
// local decision model and the widgets.
func (view *OpsView) renderPage(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		page := pageView{Style: template.CSS(workbenchStyle), Theme: template.CSS(state.theme), WidgetStyle: template.CSS(widget.Stylesheet(widget.FieldStation()))}
		for _, region := range []templ.Component{renderSummary(state), view.renderLine(state), view.render(state), view.cloudPanel(state), githubPanel(state), foldedPanel(HostRegion, sectionHost, hostTitle, view.renderHost)(state), foldedPanel(EndpointsRegion, sectionEndpoints, endpointsTitle, view.renderEndpoints)(state), view.renderLaunch(state), renderLoop(state), renderResident(state), renderRulings(state), view.renderDreamer(state), view.renderQueue(state), renderMiners(state), foldedPanel(LabelerRegion, sectionLabeler, labelerTitle, view.renderLabeler)(state), foldedPanel(CostsRegion, sectionCosts, costsTitle, renderCosts)(state), foldedPanel(JevRegion, sectionJev, jevTitle, renderJev)(state), foldedPanel(CertificateRegion, sectionCertificate, certificateTitle, renderCertificate)(state), foldedPanel(GoldenRegion, sectionGolden, goldenTitle, renderGolden)(state), renderWidgets(state)} {
			rendered, err := renderComponent(ctx, region)
			if err != nil {
				return err
			}
			// Only markup html/template already escaped crosses this boundary.
			page.Regions = append(page.Regions, template.HTML(rendered))
		}
		script, err := renderComponent(ctx, live.Script(LivePath))
		if err != nil {
			return err
		}
		page.Script = template.HTML(script)
		return views.ExecuteTemplate(writer, pageTemplate, page)
	})
}

// displayOrder puts running sessions first, then the most recently active.
func displayOrder(cards widget.KeyedState[SessionCard]) []string {
	items := cards.Items()
	slices.SortStableFunc(items, func(a, b widget.KeyedItem[SessionCard]) int {
		if rank := statusRank(a.State.Status) - statusRank(b.State.Status); rank != 0 {
			return rank
		}
		if order := strings.Compare(b.State.LastDay, a.State.LastDay); order != 0 {
			return order
		}
		return b.State.StartedAt.Compare(a.State.StartedAt)
	})
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
	}
	return keys
}

func statusRank(status Status) int {
	switch status {
	case StatusRunning:
		return 0
	case StatusFailed:
		return 1
	case StatusWaiting:
		return 2
	case StatusFinished:
		return 3
	case StatusSuspended:
		return 4
	case StatusStalled:
		return 5
	}
	return 6
}

// boardChanged reports whether the board's own markup moved: its notice, the
// groups' rows and order, the idle list's controls, or the merges. A card's
// own change is its child's to report.
func boardChanged(previous viewState, next viewState) bool {
	before, after := layoutOf(previous), layoutOf(next)
	return previous.notice != next.notice || before.otherCount != after.otherCount ||
		!slices.Equal(before.needs, after.needs) || !slices.Equal(before.running, after.running) || !slices.Equal(before.others, after.others) ||
		previous.prefs.OthersOpen != next.prefs.OthersOpen || sectionChanged(previous, next, sectionMerges) || previous.prefs.Filter != next.prefs.Filter || previous.prefs.Show != next.prefs.Show ||
		!slices.Equal(previous.merges.Pulls, next.merges.Pulls) || previous.loaded != next.loaded
}

func renderComponent(ctx context.Context, component templ.Component) (string, error) {
	var buffer bytes.Buffer
	if err := component.Render(ctx, &buffer); err != nil {
		return "", err
	}
	return buffer.String(), nil
}
