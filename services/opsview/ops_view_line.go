// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/nulab/autog"
	autograph "github.com/nulab/autog/graph"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/services/dispatch"
)

// The line panel: the value stream as a board, the slice graph, the workers
// against capacity and the mining loop as a ring, one region drawn from the
// state the page already holds.
const (
	// LineRegion is the line panel's region.
	LineRegion = "opsview.line"
	// StagePR and StageLive are the board's two stages past the
	// dispatcher's: a running slice whose session opened its pull request,
	// and a merged slice the build serving this page contains.
	StagePR   dispatch.Stage = "pr"
	StageLive dispatch.Stage = "live"

	lineTemplate = "line"
	urgencyLabel = "URGENCY_"
	readyFormat  = "#%d: critical path %d, urgency %s"
	revisionShow = 12
	gistTitle    = 28
	// columnShown is how many cards a board column lists before it counts
	// the rest: tonight's merged and live columns ran past forty each.
	columnShown = 8

	// The graph's node box and spacing, in SVG units before scaling.
	nodeWidth    = 196.0
	nodeHeight   = 46.0
	layerSpacing = 64.0
	nodeSpacing  = 18.0
	graphMargin  = 8.0
)

// boardStages are the board's columns, in the order a slice moves.
var boardStages = []dispatch.Stage{dispatch.StageReady, dispatch.StageBlocked, dispatch.StageContended, dispatch.StageRunning, StagePR, dispatch.StageMerged, StageLive}

var lineStageLabels = map[dispatch.Stage]string{
	dispatch.StageReady: "Ready", dispatch.StageBlocked: "Blocked", dispatch.StageContended: "Contended",
	dispatch.StageRunning: "Running", StagePR: "Pull request", dispatch.StageMerged: "Merged, not live", StageLive: "Live",
	dispatch.StageFailed: "Failed", dispatch.StageCanceled: "Canceled",
}

// quotaLimits are the dispatcher limits measured against the provider or the
// budget rather than the machine: work waiting on one waits on quota.
var quotaLimits = []dispatch.LimitName{dispatch.LimitRate, dispatch.LimitBudget}

// The worker states a slot shows.
const (
	workerRunning = "running"
	workerGate    = "at a gate"
	workerWaiting = "waiting on background work"
	workerIdle    = "idle"
)

var keyUnsafe = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// Build is the source revision the host serving the page was built from and
// that commit's time: a pull request merged at or before Time is in it.
type Build struct {
	Revision string    `json:"revision"`
	Time     time.Time `json:"time"`
}

// The build settings go build records from version control.
const (
	buildRevisionSetting = "vcs.revision"
	buildTimeSetting     = "vcs.time"
)

// BuildOf is the build go build stamped into a binary: its revision and that
// commit's time. A binary built with no version control information, or
// with no build information at all, is an unknown build.
func BuildOf(info *debug.BuildInfo, known bool) Build {
	var build Build
	if !known || info == nil {
		return build
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case buildRevisionSetting:
			build.Revision = setting.Value
		case buildTimeSetting:
			build.Time, _ = time.Parse(time.RFC3339, setting.Value)
		}
	}
	return build
}

// WithBuild tells the line which build the host runs, so merged work shows
// as live or as waiting for csf upgrade. Without it nothing is shown live.
func WithBuild(build Build) Option {
	return func(view *OpsView) error {
		view.build = build
		return nil
	}
}

// LineCard is one slice on the board: the dispatcher's slice, or the virtual
// session carrying one, or a pull request merged today.
type LineCard struct {
	Key            string         `json:"key"`
	Title          string         `json:"title"`
	Stage          dispatch.Stage `json:"stage"`
	TicketURL      string         `json:"ticket_url,omitempty"`
	PullRequestURL string         `json:"pull_request_url,omitempty"`
	// PullRequest is how far the control plane moved the pull request.
	PullRequest PullRequestStage `json:"pull_request,omitempty"`
	// Why is why the slice stands where it does: its rank on the frontier,
	// what it waits on, what it contends with.
	Why string `json:"why,omitempty"`
	// Owner is the virtual session carrying the slice; Model and Executor its
	// real session's, Activity its current tool row.
	Owner    string `json:"owner,omitempty"`
	Model    string `json:"model,omitempty"`
	Executor string `json:"executor,omitempty"`
	Activity string `json:"activity,omitempty"`
	Elapsed  string `json:"elapsed,omitempty"`
	Cost     string `json:"cost,omitempty"`
	Frontier bool   `json:"frontier,omitempty"`
	// DependsOn and Contends are the slice's edges in the slice graph.
	DependsOn []string `json:"depends_on,omitempty"`
	Contends  []string `json:"contends,omitempty"`
}

// Worker is one real session's slot: the virtual session it carries and
// whether it runs, waits or idles.
type Worker struct {
	Key      string `json:"key"`
	Owner    string `json:"owner"`
	State    string `json:"state"`
	On       string `json:"on"`
	Model    string `json:"model"`
	Executor string `json:"executor"`
	Elapsed  string `json:"elapsed"`
}

// LoopHop is one hop of the mining loop's ring with its count, absent when
// nothing records it yet.
type LoopHop struct {
	Name     string `json:"name"`
	Count    *int   `json:"count,omitempty"`
	PilesUp  bool   `json:"piles_up,omitempty"`
	Recorded bool   `json:"recorded"`
}

// Line is the panel's model: a pure function of the page's state and the
// build, compared whole to decide whether the panel moved.
type Line struct {
	Cards []LineCard `json:"cards"`
	// Graph is every node of the slice graph, the board's and the finished
	// ones the board leaves out.
	Graph   []LineCard `json:"graph"`
	Workers []Worker   `json:"workers"`
	// Running is how many real sessions run a turn; Capacity the dispatcher's
	// admission, Binding the limit that sets it and Quota whether that limit
	// is the provider's or the budget's rather than the machine's.
	Running  int              `json:"running"`
	Capacity int              `json:"capacity"`
	Limits   []dispatch.Limit `json:"limits,omitempty"`
	Binding  string           `json:"binding,omitempty"`
	Quota    bool             `json:"quota"`
	Snapshot bool             `json:"snapshot"`
	Loop     []LoopHop        `json:"loop"`
	Build    Build            `json:"build"`
}

// lineOf is the line the page's state shows under build.
func lineOf(state viewState, build Build) Line {
	line := Line{Build: build, Loop: loopHops(state.loop)}
	merged := map[string]time.Time{}
	for _, pull := range state.merges.Pulls {
		merged[pull.URL] = pull.MergedAt
	}
	live := func(url string, fallback time.Time) dispatch.Stage {
		at, known := merged[url]
		if !known {
			at = fallback
		}
		if build.Time.IsZero() || at.IsZero() || at.After(build.Time) {
			return dispatch.StageMerged
		}
		return StageLive
	}
	cards := map[string]SessionCard{}
	for _, item := range state.cards.Items() {
		cards[item.Key] = item.State
	}
	covered := map[string]bool{}
	if state.queue != nil {
		line.Snapshot, line.Capacity, line.Limits = true, state.queue.Capacity, state.queue.Limits
		line.Binding, line.Quota = binding(state.queue.Limits)
		for _, staged := range state.queue.Staged() {
			slice := staged.Slice
			card := LineCard{
				Key: lineKey(slice.SliceID), Title: slice.Title, Stage: staged.Stage, TicketURL: slice.TicketURL, PullRequestURL: slice.PullRequestURL,
				Frontier: slice.Rank > 0, DependsOn: keys(slice.DependsOn), Contends: keys(slice.Contends),
			}
			switch staged.Stage {
			case dispatch.StageReady:
				card.Why = fmt.Sprintf(readyFormat, slice.Rank, slice.CriticalPath, strings.ToLower(strings.TrimPrefix(slice.Urgency, urgencyLabel)))
			case dispatch.StageBlocked, dispatch.StageContended:
				card.Why = slice.Waiting
			case dispatch.StageMerged:
				card.Stage = live(slice.PullRequestURL, slice.UpdatedAt)
			}
			if session, owned := cards[slice.AssignmentID]; owned && slice.AssignmentID != "" {
				covered[slice.AssignmentID] = true
				card = withSession(card, session)
			}
			if card.Stage == dispatch.StageRunning && card.PullRequestURL != "" {
				card.Stage = StagePR
			}
			line.Graph = append(line.Graph, card)
			if staged.Stage != dispatch.StageFailed && staged.Stage != dispatch.StageCanceled {
				line.Cards = append(line.Cards, card)
			}
		}
	}
	for _, key := range displayOrder(state.cards) {
		session := cards[key]
		if covered[key] {
			continue
		}
		card := withSession(LineCard{Key: lineKey(key), Title: session.Agent, TicketURL: session.TicketURL, PullRequestURL: session.PullRequestURL}, session)
		switch {
		case session.PullRequest == PullRequestMerged || session.Landed:
			if session.MergedOn != state.today && !session.Landed {
				continue
			}
			card.Stage = live(session.PullRequestURL, time.Time{})
		case slices.Contains([]Status{StatusClosed, StatusFailed, StatusStalled}, session.Status):
			continue
		case session.PullRequestURL != "":
			card.Stage = StagePR
		default:
			card.Stage = dispatch.StageRunning
		}
		covered[key] = true
		line.Cards = append(line.Cards, card)
		line.Graph = append(line.Graph, card)
	}
	for _, pull := range state.merges.Pulls {
		if slices.ContainsFunc(line.Cards, func(card LineCard) bool { return card.PullRequestURL == pull.URL }) {
			continue
		}
		card := LineCard{Key: lineKey(pullRequestLabel(pull.URL)), Title: pull.Title, PullRequestURL: pull.URL, PullRequest: PullRequestMerged, Stage: live(pull.URL, pull.MergedAt)}
		line.Cards = append(line.Cards, card)
	}
	line.Workers, line.Running = workersOf(state.cards, line.Cards)
	return line
}

// withSession puts the virtual session carrying a slice on its card.
func withSession(card LineCard, session SessionCard) LineCard {
	card.Owner, card.Model, card.Executor, card.Activity, card.Elapsed = session.Agent, session.Model, session.Executor, session.Activity, session.Elapsed
	card.PullRequest = session.PullRequest
	if card.PullRequestURL == "" {
		card.PullRequestURL = session.PullRequestURL
	}
	if card.TicketURL == "" {
		card.TicketURL = session.TicketURL
	}
	var spent float64
	for _, usd := range session.CostByDay {
		spent += usd
	}
	if spent > 0 {
		card.Cost = fmt.Sprintf(usdFormat, spent)
	}
	return card
}

// workersOf is one slot per real session alive, running first, and how many
// run a turn now. A suspended session has no real session; a closed one is
// gone.
func workersOf(state widget.KeyedState[SessionCard], cards []LineCard) ([]Worker, int) {
	running := 0
	workers := []Worker{}
	for _, key := range displayOrder(state) {
		session, _ := state.Get(key)
		var status string
		switch session.Status {
		case StatusRunning:
			running++
			status = workerRunning
			if count := len(session.Recent); count > 0 && session.Recent[count-1].Kind == KindGate {
				status = workerGate
			}
		case StatusWaiting:
			status = workerWaiting
		case StatusFinished:
			status = workerIdle
		default:
			continue
		}
		on := session.Branch
		if index := slices.IndexFunc(cards, func(card LineCard) bool { return card.Key == lineKey(key) }); index >= 0 {
			on = cards[index].Title
		}
		workers = append(workers, Worker{Key: lineKey(key), Owner: session.Agent, State: status, On: on, Model: session.Model, Executor: session.Executor, Elapsed: session.Elapsed})
	}
	return workers, running
}

// binding is the bounded limit admitting the fewest launches, and whether it
// is a quota rather than the machine.
func binding(limits []dispatch.Limit) (string, bool) {
	var tightest *dispatch.Limit
	for index := range limits {
		limit := &limits[index]
		if limit.Bounded && (tightest == nil || limit.Launches < tightest.Launches) {
			tightest = limit
		}
	}
	if tightest == nil {
		return "", false
	}
	return string(tightest.Name), slices.Contains(quotaLimits, tightest.Name)
}

// loopHops is the mining loop's ring, from the loop's snapshot: findings,
// tickets waiting for labels, accepted miners, fixer sessions, merges. What
// the loop does not record yet — complaints filed, fixes live — is a hop
// with no count. The hop holding the most more than the next piles up.
func loopHops(panel LoopPanel) []LoopHop {
	snapshot := panel.Snapshot
	built := 0
	for _, miner := range snapshot.Miners {
		if miner.Built {
			built++
		}
	}
	counts := []*int{nil, &snapshot.Findings, &snapshot.Queue, &built, &snapshot.Fixers.Sessions, &snapshot.Merges.Merged, nil}
	names := []string{"complaints", "mining findings", "tickets waiting for labels", "accepted miners", "fixer slices", "merged", "live"}
	hops := make([]LoopHop, 0, len(names))
	pile, most := -1, 0
	for index, name := range names {
		hop := LoopHop{Name: name, Recorded: panel.Present && counts[index] != nil}
		if hop.Recorded {
			count := *counts[index]
			hop.Count = &count
			if next := index + 1; next < len(counts) && counts[next] != nil && count-*counts[next] > most {
				pile, most = index, count-*counts[next]
			}
		}
		hops = append(hops, hop)
	}
	if pile >= 0 {
		hops[pile].PilesUp = true
	}
	return hops
}

// lineKey is a slice or session identifier as a key the page can use in an
// attribute and a view-transition name.
func lineKey(identifier string) string { return "line-" + keyUnsafe.ReplaceAllString(identifier, "-") }

func keys(identifiers []string) []string {
	converted := make([]string, 0, len(identifiers))
	for _, identifier := range identifiers {
		converted = append(converted, lineKey(identifier))
	}
	return converted
}

// lineView is the panel's template data.
type lineView struct {
	Line
	Region     string
	Section    sectionView
	Transition template.HTMLAttr
	Columns    []lineColumn
	Graph      graphView
	NotLive    int
	Revision   string
	BuiltAt    string
}

type lineColumn struct {
	Stage dispatch.Stage
	Label string
	Cards []LineCard
	// More is how many cards past columnShown the column holds.
	More int
}

// graphView is the laid-out slice graph, in SVG units.
type graphView struct {
	Width, Height float64
	Nodes         []graphNode
	Edges         []string
	Contends      [][4]float64
	// Loose is every slice with no edge, listed rather than drawn: tonight
	// most sessions carry a slice no other depends on or contends with.
	Loose []LineCard
}

type graphNode struct {
	LineCard
	X, Y  float64
	Label string
	Short string
}

func (view *OpsView) renderLine(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		line := lineOf(state, view.build)
		data := lineView{Line: line, Region: LineRegion, Section: sectionOf(state.prefs, sectionLine)}
		for _, stage := range boardStages {
			column := lineColumn{Stage: stage, Label: lineStageLabels[stage]}
			for _, card := range line.Cards {
				if card.Stage == stage {
					column.Cards = append(column.Cards, card)
				}
			}
			if stage == dispatch.StageMerged {
				data.NotLive = len(column.Cards)
			}
			if len(column.Cards) > columnShown {
				column.More, column.Cards = len(column.Cards)-columnShown, column.Cards[:columnShown]
			}
			data.Columns = append(data.Columns, column)
		}
		data.Revision = view.build.Revision[:min(len(view.build.Revision), revisionShow)]
		if !view.build.Time.IsZero() {
			data.BuiltAt = view.build.Time.UTC().Format(time.DateTime)
		}
		if data.Section.Open {
			data.Graph = layoutGraph(line.Graph)
		}
		if state.loaded {
			transition, err := attributes(live.Transition())
			if err != nil {
				return err
			}
			data.Transition = template.HTMLAttr(transition)
		}
		return views.ExecuteTemplate(writer, lineTemplate, data)
	})
}

// layoutGraph lays the slice graph out with autog: depends_on edges are the
// layers, and a node joined only by contends is laid out on its own. A node
// with no edge at all is listed loose instead. autog lays layers top to
// bottom and places disconnected parts side by side; the drawing swaps the
// axes, so dependencies read left to right and parts stack down a phone.
func layoutGraph(nodes []LineCard) graphView {
	graph := graphView{}
	var edges autograph.EdgeSlice
	present := map[string]bool{}
	for _, node := range nodes {
		present[node.Key] = true
	}
	joined := func(node LineCard) (bool, bool) {
		depends := slices.ContainsFunc(node.DependsOn, func(key string) bool { return present[key] }) ||
			slices.ContainsFunc(nodes, func(other LineCard) bool { return slices.Contains(other.DependsOn, node.Key) })
		return depends, slices.ContainsFunc(node.Contends, func(key string) bool { return present[key] })
	}
	for _, node := range nodes {
		depends, contends := joined(node)
		switch {
		case depends:
			for _, predecessor := range node.DependsOn {
				if present[predecessor] {
					edges = append(edges, []string{predecessor, node.Key})
				}
			}
		case contends:
			// autog reads nodes from edges; a self-loop places a node with no
			// depends_on edge and is dropped from the drawing.
			edges = append(edges, []string{node.Key, node.Key})
		default:
			graph.Loose = append(graph.Loose, node)
		}
	}
	if len(edges) == 0 {
		return graph
	}
	// The sizes are swapped with the axes.
	layout := autog.Layout(edges, autog.WithNodeFixedSize(nodeHeight, nodeWidth), autog.WithLayerSpacing(layerSpacing), autog.WithNodeSpacing(nodeSpacing))
	centers := map[string][2]float64{}
	for _, placed := range layout.Nodes {
		index := slices.IndexFunc(nodes, func(node LineCard) bool { return node.Key == placed.ID })
		x, y := placed.Y+graphMargin, placed.X+graphMargin
		centers[placed.ID] = [2]float64{x + nodeWidth/2, y + nodeHeight/2}
		graph.Width, graph.Height = max(graph.Width, x+nodeWidth+graphMargin), max(graph.Height, y+nodeHeight+graphMargin)
		node := graphNode{LineCard: nodes[index], X: x, Y: y, Label: lineStageLabels[nodes[index].Stage], Short: shorten(nodes[index].Title)}
		graph.Nodes = append(graph.Nodes, node)
	}
	for _, edge := range layout.Edges {
		if edge.FromID == edge.ToID {
			continue
		}
		points := make([]string, 0, len(edge.Points))
		for _, point := range edge.Points {
			points = append(points, fmt.Sprintf("%.1f,%.1f", point[1]+graphMargin, point[0]+graphMargin))
		}
		graph.Edges = append(graph.Edges, strings.Join(points, " "))
	}
	for _, node := range nodes {
		for _, other := range node.Contends {
			from, fromPlaced := centers[node.Key]
			to, toPlaced := centers[other]
			if fromPlaced && toPlaced && node.Key < other {
				graph.Contends = append(graph.Contends, [4]float64{from[0], from[1], to[0], to[1]})
			}
		}
	}
	slices.SortFunc(graph.Nodes, func(a, b graphNode) int { return strings.Compare(a.Key, b.Key) })
	return graph
}

// shorten fits a title in a graph node.
func shorten(title string) string {
	runes := []rune(title)
	if len(runes) <= gistTitle {
		return title
	}
	return string(runes[:gistTitle-1]) + "…"
}

// lineChanged reports whether the line moved: its model, its fold, or the
// first read finishing, which turns animation on.
func (view *OpsView) lineChanged(previous viewState, next viewState) bool {
	before, _ := json.Marshal(lineOf(previous, view.build))
	after, _ := json.Marshal(lineOf(next, view.build))
	return string(before) != string(after) || sectionChanged(previous, next, sectionLine) || previous.loaded != next.loaded
}
