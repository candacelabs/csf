package csf

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync/atomic"
	"time"

	"github.com/a-h/templ"
	"github.com/candacelabs/csf/pkg/gotth/live"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/gin-gonic/gin"
)

const (
	farmRegion           = "farm"
	farmSync             = "farm.sync"
	farmMoveEvent        = "farm.move"
	farmStateField       = "state"
	farmIDField          = "id"
	farmBeforeField      = "before"
	farmMount            = "/live"
	farmStylePath        = "/farm.css"
	farmStatePath        = "/api/work"
	farmOrderFile        = "priority-order.json"
	farmOrderReceiptFile = "priority-events.jsonl"
	farmPageTemplate     = "page"
	farmSampleInterval   = 2 * time.Second
	farmStaleAfter       = 120 * time.Second
	farmUnknownState     = "unknown"
	farmContentType      = "Content-Type"
	farmJSONType         = "application/json"
	farmHTMLType         = "text/html; charset=utf-8"
	farmCSSType          = "text/css; charset=utf-8"
)

//go:embed farm.html
var farmHTML string

//go:embed farm.css
var farmCSS []byte

// These are the board's presentation inputs, not a second domain contract.
// Runtime/program/knowledge contracts remain in their generated protobufs.
type FarmAgent struct {
	Name, Role, State, Task, Note, ObservedAt, SessionID, Worktree, TicketURL string
	Model, StartedAt, FinishedAt, Duration, DurationLabel                     string
	Tokens                                                                    *api.UsageObservation
	PremiumRequests                                                           *float64
}
type FarmTask struct{ ID, Title, Status, Detail, TicketURL string }
type FarmEvent struct{ At, Agent, Message, Evidence, Kind string }
type FarmScore struct{ Label, Value, Scope string }
type FarmLink struct{ Name, URL string }
type FarmCheck struct{ ID, Title, Status, Evidence string }
type FarmCheckpoint struct {
	Title                string
	Items                []FarmCheck
	Done, Total, Percent int
}
type FarmView struct {
	BrowserConnections                                                               int
	Title, Phase, UpdatedAt, Now, Uptime, Elapsed, StartedAt, Branch, TicketURL      string
	RuntimeCount, Goroutines, GoMaxProcs, HeapMiB, Containers, WorktreeCount         int
	ContainersObservedAt, WorktreesObservedAt, SourceError, AgentSourceError, Notice string
	Agents                                                                           []FarmAgent
	Tasks                                                                            []FarmTask
	Feed                                                                             []FarmEvent
	Scores                                                                           []FarmScore
	Operations                                                                       []HumanOperation
	DashboardLinks                                                                   []FarmLink
	Checkpoint                                                                       FarmCheckpoint
}

type farmMove struct {
	id, before string
	reply      chan error
}

// FarmDashboard mounts into an existing process. Observe owns publication and
// priority writes; sessions only consume immutable published views.
type FarmDashboard struct {
	agentSource FarmAgentSource
	path        string
	started     time.Time
	view        atomic.Pointer[FarmView]
	moves       chan farmMove
	app         *live.App[FarmView, live.AnonymousIdentity]
	template    *template.Template
}

// FarmAgentSource supplies observed workers without owning another durable store.
type FarmAgentSource func(now time.Time) ([]FarmAgent, error)

func WithFarmAgentSource(source FarmAgentSource) FarmOption {
	return func(dashboard *FarmDashboard) { dashboard.agentSource = source }
}

type FarmOption func(dashboard *FarmDashboard)

func WithFarmWorkPath(path string) FarmOption {
	return func(dashboard *FarmDashboard) { dashboard.path = path }
}

func NewFarmDashboard(origins []string, options ...FarmOption) (*FarmDashboard, error) {
	dashboard := &FarmDashboard{started: time.Now(), moves: make(chan farmMove, 16)}
	for _, option := range options {
		if option != nil {
			option(dashboard)
		}
	}
	if dashboard.path == "" {
		return nil, fmt.Errorf("work-state path is required")
	}
	parsed, err := template.New(farmPageTemplate).Parse(farmHTML)
	if err != nil {
		return nil, err
	}
	dashboard.template = parsed
	dashboard.publish(time.Now())
	app, err := live.New(live.Config[FarmView, live.AnonymousIdentity]{
		Init: func(ctx context.Context, session live.Session[live.AnonymousIdentity]) (FarmView, []live.Effect[live.AnonymousIdentity], error) {
			return *dashboard.view.Load(), []live.Effect[live.AnonymousIdentity]{dashboard.watch()}, nil
		},
		Reduce: func(state FarmView, event live.Event) (FarmView, []live.Effect[live.AnonymousIdentity]) {
			switch event.Name {
			case farmSync:
				var next FarmView
				if err := json.Unmarshal([]byte(event.Fields.Get(farmStateField)), &next); err == nil {
					return next, nil
				}
			case farmMoveEvent:
				return state, []live.Effect[live.AnonymousIdentity]{dashboard.moveEffect(event.Fields.Get(farmIDField), event.Fields.Get(farmBeforeField))}
			case live.EffectFailedEvent:
				state.Notice = "An update failed; the last observed state is retained."
			}
			return state, nil
		},
		Fragments: []live.Fragment[FarmView]{{ID: farmRegion, Render: func(state FarmView) templ.Component { return dashboard.component(farmRegion, state) }}},
		Events:    []string{farmMoveEvent}, Origins: origins,
		Authenticate: live.Anonymous, Authorize: live.AllowAll[live.AnonymousIdentity], CSRF: live.NoCSRFCheck,
	})
	if err != nil {
		return nil, err
	}
	dashboard.app = app
	return dashboard, nil
}

// Observe runs under the caller's process context; it owns no listener.
func (dashboard *FarmDashboard) Observe(ctx context.Context) {
	ticker := time.NewTicker(farmSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			dashboard.publish(now)
		case request := <-dashboard.moves:
			err := dashboard.move(request.id, request.before)
			dashboard.publish(time.Now())
			request.reply <- err
		}
	}
}

func (dashboard *FarmDashboard) Register(router gin.IRouter) {
	router.GET("/", gin.WrapH(dashboard.app.PageHandler(func(state FarmView) templ.Component { return dashboard.component(farmPageTemplate, state) })))
	router.Any(farmMount, gin.WrapH(dashboard.app.Handler()))
	router.Any(farmMount+"/*path", gin.WrapH(dashboard.app.Handler()))
	router.GET(farmStylePath, func(ctx *gin.Context) { ctx.Data(http.StatusOK, farmCSSType, farmCSS) })
	router.GET(farmStatePath, func(ctx *gin.Context) {
		ctx.Header("Cache-Control", "no-store")
		ctx.JSON(http.StatusOK, dashboard.view.Load())
	})
}

func (dashboard *FarmDashboard) Close(ctx context.Context) error { return dashboard.app.Close(ctx) }

// Snapshot returns the latest immutable view for sibling in-process mounts.
func (dashboard *FarmDashboard) Snapshot() FarmView { return *dashboard.view.Load() }

// ActiveConnections counts live board WebSockets, including connection cleanup.
// Workbench chat streams and terminal sockets have different owners and are excluded.
func (dashboard *FarmDashboard) ActiveConnections() int {
	if dashboard.app == nil {
		return 0
	}
	return dashboard.app.ActiveConnections()
}

func (dashboard *FarmDashboard) component(name string, state FarmView) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		return dashboard.template.ExecuteTemplate(writer, name, state)
	})
}

func (dashboard *FarmDashboard) publish(now time.Time) {
	view := FarmView{Title: "CSF — The Cerebrospinal Fluid", Phase: "Awaiting work report", Elapsed: "Awaiting work report"}
	file, err := os.Open(dashboard.path)
	if err != nil {
		view.SourceError = "Work report unavailable"
	} else {
		decoder := json.NewDecoder(io.LimitReader(file, maxAPIBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&view); err != nil {
			view.SourceError = "Work report cannot be decoded: " + err.Error()
		}
		_ = file.Close()
	}
	if dashboard.agentSource != nil {
		agents, err := dashboard.agentSource(now)
		if err != nil {
			view.AgentSourceError = "Workbench session observations unavailable"
		} else {
			seen := make(map[string]bool, len(agents))
			for _, agent := range agents {
				seen[agent.SessionID] = true
			}
			for _, agent := range view.Agents {
				if agent.SessionID == "" || !seen[agent.SessionID] {
					agents = append(agents, agent)
				}
			}
			view.Agents = agents
		}
	}
	if len(view.Tasks) > 128 {
		view.Tasks = view.Tasks[:128]
	}
	sort.SliceStable(view.Agents, func(left int, right int) bool {
		priority := func(state string) bool { return state == "running" || state == "starting" }
		return priority(view.Agents[left].State) && !priority(view.Agents[right].State)
	})
	if len(view.Agents) > 32 {
		view.Agents = view.Agents[:32]
	}
	if len(view.Feed) > 80 {
		view.Feed = view.Feed[:80]
	}
	view.Checkpoint.Total = len(view.Checkpoint.Items)
	view.Checkpoint.Done = 0
	for _, item := range view.Checkpoint.Items {
		if item.Status == "passed" && item.Evidence != "" {
			view.Checkpoint.Done++
		}
	}
	if view.Checkpoint.Total > 0 {
		view.Checkpoint.Percent = view.Checkpoint.Done * 100 / view.Checkpoint.Total
	}
	order, err := dashboard.readOrder()
	if err != nil {
		view.SourceError = "Priority order is invalid: " + err.Error()
	} else {
		view.Tasks = orderedTasks(view.Tasks, order)
	}
	for index := range view.Agents {
		observed, err := time.Parse(time.RFC3339, view.Agents[index].ObservedAt)
		if err != nil || now.Sub(observed) > farmStaleAfter || observed.After(now.Add(time.Second)) {
			view.Agents[index].State = farmUnknownState
		}
	}
	view.Now = now.UTC().Format(time.RFC3339)
	view.Operations = HumanOperations()
	view.Uptime = now.Sub(dashboard.started).Truncate(time.Second).String()
	if started, err := time.Parse(time.RFC3339, view.StartedAt); err == nil && !started.After(now) {
		view.Elapsed = now.Sub(started).Truncate(time.Second).String()
	}
	view.RuntimeCount = 1
	view.BrowserConnections = dashboard.ActiveConnections()
	view.Goroutines = runtime.NumGoroutine()
	view.GoMaxProcs = runtime.GOMAXPROCS(0)
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	view.HeapMiB = int(memory.HeapAlloc / (1024 * 1024))
	dashboard.view.Store(&view)
}

func orderedTasks(tasks []FarmTask, order []string) []FarmTask {
	result := make([]FarmTask, 0, len(tasks))
	seen := make(map[string]bool, len(tasks))
	for _, id := range order {
		for _, task := range tasks {
			if task.ID == id && !seen[id] {
				result = append(result, task)
				seen[id] = true
			}
		}
	}
	for _, task := range tasks {
		if !seen[task.ID] {
			result = append(result, task)
			seen[task.ID] = true
		}
	}
	return result
}

func (dashboard *FarmDashboard) readOrder() ([]string, error) {
	content, err := os.ReadFile(filepath.Join(filepath.Dir(dashboard.path), farmOrderFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(content) > 16*1024 {
		return nil, fmt.Errorf("priority order exceeds limit")
	}
	var order []string
	err = json.Unmarshal(content, &order)
	return order, err
}

func (dashboard *FarmDashboard) move(id, before string) error {
	tasks := dashboard.view.Load().Tasks
	order := make([]string, 0, len(tasks))
	found := false
	target := before == ""
	if id == before {
		return fmt.Errorf("task cannot precede itself")
	}
	for _, task := range tasks {
		if task.ID == id {
			found = true
		} else {
			order = append(order, task.ID)
		}
		if task.ID == before {
			target = true
		}
	}
	if !found || !target {
		return fmt.Errorf("task identity is not in this board")
	}
	position := len(order)
	for index, value := range order {
		if value == before {
			position = index
			break
		}
	}
	order = append(order, "")
	copy(order[position+1:], order[position:])
	order[position] = id
	content, err := json.Marshal(order)
	if err != nil {
		return err
	}
	directory := filepath.Dir(dashboard.path)
	temporary, err := os.CreateTemp(directory, ".priority-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	_, err = temporary.Write(content)
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(temporary.Name(), filepath.Join(directory, farmOrderFile)); err != nil {
		return err
	}
	// Receipts describe this operator ordering action, never permission to run it.
	receipt, err := os.OpenFile(filepath.Join(directory, farmOrderReceiptFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = receipt.Close() }()
	if err := json.NewEncoder(receipt).Encode(struct {
		At, Task, Before string
		Order            []string
	}{time.Now().UTC().Format(time.RFC3339), id, before, order}); err != nil {
		return err
	}
	return receipt.Sync()
}

func (dashboard *FarmDashboard) watch() live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{Source: farmSync, Run: func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
		ticker := time.NewTicker(farmSampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				if err := dashboard.emit(emit); err != nil {
					return err
				}
			}
		}
	}}
}

func (dashboard *FarmDashboard) emit(emit live.Emitter) error {
	content, err := json.Marshal(dashboard.view.Load())
	if err != nil {
		return err
	}
	return emit(live.Event{Name: farmSync, Fields: live.NewFields(map[string]string{farmStateField: string(content)})})
}

func (dashboard *FarmDashboard) moveEffect(id, before string) live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{Source: farmMoveEvent, Run: func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
		request := farmMove{id: id, before: before, reply: make(chan error, 1)}
		select {
		case dashboard.moves <- request:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-request.reply:
			if err != nil {
				return err
			}
			return dashboard.emit(emit)
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
}

// RenderFarm is useful for deterministic page inspection without opening a listener.
func (dashboard *FarmDashboard) RenderFarm() ([]byte, error) {
	var output bytes.Buffer
	err := dashboard.template.ExecuteTemplate(&output, farmPageTemplate, *dashboard.view.Load())
	return output.Bytes(), err
}
