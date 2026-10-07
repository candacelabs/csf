// Copyright 2026 Candace Labs

// Package opsview is the ops view: a gotth-live page that shows every
// harness session on this machine as a live card, read from the run
// directories under the harness state directory. Each card is a widget SDK
// widget in a keyed collection; cards update the moment a line lands in a
// session's event log, because the page follows the files through the
// kernel's change notification rather than polling them.
//
// It is a service: it owns no listener and no process. The harness host app
// grants it the file and watch capabilities over the state directory, mounts
// it into the host runtime and binds the listener.
package opsview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/io/inproc"
	"github.com/candacelabs/csf/io/kernel/clock"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/cloud"
	"github.com/candacelabs/csf/services/dispatch"
	"github.com/candacelabs/csf/services/dreamer"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/harness/endpoint"
	"github.com/candacelabs/csf/services/harness/session"
)

// Routes: the page and the live mount under it. The live handler serves its
// own runtime script beneath the mount.
const (
	PagePath      = "/"
	LivePath      = "/live"
	liveAssetPath = LivePath + "/*asset"

	// BoardRegion is the parent region every card lives in: the keyed
	// collection's region, so a card's region is BoardRegion:<assignment>.
	BoardRegion = "opsview.sessions"
	// LoopRegion is the mining loop's panel: the compounding number and the
	// loop's yields, read from the snapshot the loop projects into the state
	// directory.
	LoopRegion = "opsview.loop"
	// LaunchRegion is the launch form: a ticket and a recipe template in, the
	// admission check shown, then the launch.
	LaunchRegion = "opsview.launch"
)

var (
	// ErrNoFiles reports a view built without the file capability.
	ErrNoFiles = errors.New("ops view: the file capability over the state directory is required")
	// ErrNoWatcher reports a view built without the watch capability.
	ErrNoWatcher = errors.New("ops view: the watch capability over the state directory is required")
)

// viewState is one connection's view: the cards by assignment, the mining
// loop's numbers when the loop has projected them, every miner's mutation
// score series, the labeler panel, the cost model, the installed widgets, the queue, the
// launch form, and a notice when the follow effect failed.
type viewState struct {
	cards widget.KeyedState[SessionCard]
	// prefs is the viewer's view; today is the UTC day by the view's clock,
	// and merges what the forge reported merged into main that day.
	prefs ViewPrefs
	// theme is the operator's stylesheet, read when the page is drawn.
	theme  string
	today  string
	merges mergesState
	// loaded is set once the follow effect's first pass has read every
	// session: rows that move after it animate, the first read's do not.
	loaded bool
	// sessions is the session service's count of both kinds of session.
	sessions SessionCounts
	loop     LoopPanel
	miners   []MinerSeries
	labeler  LabelerPanel
	costs    CostsPanel
	jev      JevPanel
	widgets  []InstalledWidget
	queue    *dispatch.Snapshot
	dreamer  *dreamer.Snapshot
	resident []harness.ResidentSample
	// certificate is the choice-verification certificate the bootstrap's
	// archive carries, when one has landed in the state directory.
	certificate CertificatePanel
	// golden is the merge quality gate's verdict on the latest merge, when one
	// has landed in the state directory.
	golden GoldenPanel
	// rulings is the rulings in force; rulingRecords the records they were
	// folded from, which the panel compares.
	rulings       []session.Ruling
	rulingRecords string
	launch        launchState
	host          hostState
	cloud         cloudState
	github        GitHubPanel
	notice        string
	// controlNotice is what the last queue or dreamer control said.
	controlNotice string
}

// OpsView is the live application and its routes.
type OpsView struct {
	files      iofs.IFiles
	watcher    iofs.IWatcher
	operations IWorkbenchOperations
	templates  iofs.IFiles
	// sliceControls and dreamerControls are the queue's buttons, when
	// granted.
	sliceControls   ISliceControls
	dreamerControls IDreamerControls
	// definitions and definitionWatcher are the widgets directory, when
	// granted.
	definitions       iofs.IFiles
	definitionWatcher iofs.IWatcher
	sources           widget.Sources
	// endpoints is the endpoint registry the endpoints panel lists.
	endpoints endpoint.Registry
	// host is the host panel's operations, when granted.
	host *HostOperations
	// cloudStop is the cloud service's Stop, when granted.
	cloudStop func(ctx context.Context, input cloud.StopCloudJobInput) (cloud.CloudJob, error)
	// github starts gh for the merges on main; nil leaves the tile without
	// a source.
	github proc.ILauncher
	clock  clock.IClock
	// hostStart is when the host serving the page started; zero reads no
	// session as stalled.
	hostStart time.Time
	// build is the build the host serving the page runs, for the line's
	// live stage.
	build   Build
	viewers *inproc.Recent[string, ViewPrefs]
	cards   *widget.KeyedCollection[SessionCard, ViewerIdentity, *sessionCard]
	live    *live.App[viewState, ViewerIdentity]
	logger  *slog.Logger
}

var _ runtime.IService = (*OpsView)(nil)

// NewOpsView builds the view over the harness state directory: files reads
// it and watcher reports its changes. origins is the browser Origin
// allowlist, one per address the host serves; nothing else is accepted.
// [WithOperations] makes it the Workbench, the page that acts.
func NewOpsView(files iofs.IFiles, watcher iofs.IWatcher, origins []string, logger *slog.Logger, options ...Option) (*OpsView, error) {
	if files == nil {
		return nil, ErrNoFiles
	}
	if watcher == nil {
		return nil, ErrNoWatcher
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	view := &OpsView{files: files, watcher: watcher, logger: logger, sources: WidgetSources(), clock: clock.NewSystemClock(), viewers: newViewers()}
	for _, option := range options {
		if option == nil {
			return nil, ErrNoOperations
		}
		if err := option(view); err != nil {
			return nil, err
		}
	}
	cards, err := widget.NewKeyedCollection[SessionCard, ViewerIdentity](BoardRegion, sessionCardsOver(view.operations))
	if err != nil {
		return nil, err
	}
	view.cards = cards
	board := widget.KeyedFragment(cards, func(state viewState) widget.KeyedState[SessionCard] { return state.cards })
	board.Render, board.Dirty = view.render, boardChanged
	fragments := []live.Fragment[viewState]{
		{ID: SummaryRegion, Render: renderSummary, Dirty: summaryChanged},
		{ID: LineRegion, Render: view.renderLine, Dirty: view.lineChanged},
		{ID: HostRegion, Render: foldedPanel(HostRegion, sectionHost, hostTitle, view.renderHost), Dirty: foldedDirty(sectionHost, hostChanged)},
		{ID: EndpointsRegion, Render: foldedPanel(EndpointsRegion, sectionEndpoints, endpointsTitle, view.renderEndpoints), Dirty: foldedDirty(sectionEndpoints, endpointsUnchanged)},
		{ID: LaunchRegion, Render: view.renderLaunch, Dirty: launchChanged},
		view.cloudFragment(),
		githubFragment(),
		board,
		{ID: LoopRegion, Render: renderLoop, Dirty: loopChanged},
		residentFragment(),
		rulingsFragment(),
		minersFragment(),
		{ID: LabelerRegion, Render: foldedPanel(LabelerRegion, sectionLabeler, labelerTitle, view.renderLabeler), Dirty: foldedDirty(sectionLabeler, labelerChanged)},
		{ID: CostsRegion, Render: foldedPanel(CostsRegion, sectionCosts, costsTitle, renderCosts), Dirty: foldedDirty(sectionCosts, costsChanged)},
		{ID: JevRegion, Render: foldedPanel(JevRegion, sectionJev, jevTitle, renderJev), Dirty: foldedDirty(sectionJev, jevChanged)},
		{ID: CertificateRegion, Render: foldedPanel(CertificateRegion, sectionCertificate, certificateTitle, renderCertificate), Dirty: foldedDirty(sectionCertificate, certificateChanged)},
		{ID: GoldenRegion, Render: foldedPanel(GoldenRegion, sectionGolden, goldenTitle, renderGolden), Dirty: foldedDirty(sectionGolden, goldenChanged)},
		widgetsFragment(),
		{ID: DreamerRegion, Render: view.renderDreamer, Dirty: dreamerChanged},
		{ID: QueueRegion, Render: view.renderQueue, Dirty: queueChanged},
	}
	view.live, err = live.New(live.Config[viewState, ViewerIdentity]{
		Init:         view.initialize,
		Reduce:       view.reduce,
		Fragments:    fragments,
		Events:       append(append(cards.Events(), EventCheck, EventLaunch, EventExecutor, EventOthers, EventFilter, EventSection, EventDefine, EventCloudStop, EventControl), hostEvents...),
		Origins:      origins,
		Authenticate: authenticateViewer,
		Authorize:    live.AllowAll[ViewerIdentity],
		CSRF:         live.NoCSRFCheck,
		Logger:       logger,
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// Start starts the live application's connection scope.
func (view *OpsView) Start(scope *runtime.Scope) error { return view.live.Start(scope) }

// Register mounts the page and the live routes on the caller's router.
func (view *OpsView) Register(router gin.IRouter) {
	router.GET(PagePath, withViewerCookie, gin.WrapH(view.live.PageHandler(view.renderPage)))
	router.GET(LivePath, gin.WrapH(view.live.Handler()))
	router.GET(liveAssetPath, gin.WrapH(view.live.Handler()))
}

// PageURL is the view's page under base, the host's address.
func PageURL(base string) string { return strings.TrimRight(base, "/") + PagePath }

// reduce is the pure transition. A session event for a card not yet on the
// board adds the member first, so the collection can route it; a mutation
// event replaces the miners panel's series, a resident event the resident
// panel's and a labeler event the labeler panel; a failed effect becomes the
// board's notice; everything else, the browser's expand included, is the
// collection's to route to the member it names.
func (view *OpsView) reduce(state viewState, event live.Event) (viewState, []live.Effect[ViewerIdentity]) {
	if isHostEvent(event.Name) {
		var effects []live.Effect[ViewerIdentity]
		state.host, effects = view.reduceHost(state.host, event)
		return state, effects
	}
	if next, effects, handled := view.reduceView(state, event); handled {
		return next, effects
	}
	switch event.Name {
	case EventToday, EventRefreshMerges, EventMerges, EventPulls:
		return view.reduceMerges(state, event)
	case EventRefreshSessions, EventSessionCounts:
		return view.reduceSessions(state, event)
	case EventLoaded:
		state.loaded = true
		return state, nil
	case EventMutation:
		var series []MinerSeries
		if json.Unmarshal([]byte(event.Fields.Get(FieldSeries)), &series) == nil {
			state.miners = series
		}
		return state, nil
	case EventLabeler:
		if panel, decoded := decodePanel(event.Fields.Get(FieldRun)); decoded {
			state.labeler = panel
		}
		return state, nil
	case EventResident:
		var samples []harness.ResidentSample
		if json.Unmarshal([]byte(event.Fields.Get(FieldSeries)), &samples) == nil {
			state.resident = samples
		}
		return state, nil
	case EventRulings:
		return reduceRulings(state, event), nil
	case EventCosts:
		if panel, decoded := decodeCosts(event.Fields.Get(FieldReport)); decoded {
			state.costs = panel
		}
		return state, nil
	case EventJev:
		if panel, decoded := decodeJev(event.Fields.Get(FieldLedger)); decoded {
			state.jev = panel
		}
		return state, nil
	case EventCertificate:
		if panel, decoded := decodeCertificate(event.Fields.Get(FieldCertificate)); decoded {
			state.certificate = panel
		}
		return state, nil
	case EventGolden:
		if panel, decoded := decodeGolden(event.Fields.Get(FieldGolden)); decoded {
			state.golden = panel
		}
		return state, nil
	case EventSession:
		key, _, exists := view.cards.Lookup(state.cards, event.FragmentID)
		if !exists {
			// A card the viewer had open opens again: the view outlives the
			// connection, the cards are read anew.
			added, err := state.cards.Upsert(key, SessionCard{
				Expanded:  slices.Contains(state.prefs.Expanded, key),
				Internals: slices.Contains(state.prefs.Internals, key),
				Composing: slices.Contains(state.prefs.Composing, key),
			})
			if err != nil {
				return state, nil
			}
			state.cards = added
		}
	case EventWidgets:
		var definitions []DefinitionFiles
		if json.Unmarshal([]byte(event.Fields.Get(FieldWidgets)), &definitions) == nil {
			state.widgets = InstallWidgets(state.widgets, definitions, view.sources)
		}
		return state, nil
	case EventLoop:
		var panel LoopPanel
		if json.Unmarshal([]byte(event.Fields.Get(FieldLoop)), &panel) == nil {
			state.loop = panel
		}
		return state, nil
	case EventQueue:
		state.queue = nil
		if encoded := event.Fields.Get(FieldQueue); encoded != "" {
			snapshot := &dispatch.Snapshot{}
			if json.Unmarshal([]byte(encoded), snapshot) == nil {
				state.queue = snapshot
			}
		}
		return state, nil
	case EventDreamer:
		state.dreamer = nil
		if encoded := event.Fields.Get(FieldDreamer); encoded != "" {
			snapshot := &dreamer.Snapshot{}
			if json.Unmarshal([]byte(encoded), snapshot) == nil {
				state.dreamer = snapshot
			}
		}
		return state, nil
	case EventControl:
		return view.reduceControl(state, event)
	case EventControlled:
		state.controlNotice = event.Fields.Get(FieldNotice)
		return state, nil
	case EventTemplates, EventCheck, EventChecked, EventLaunch, EventLaunched, EventRefused, EventExecutor, EventExecutorSet:
		var effects []live.Effect[ViewerIdentity]
		state.launch, effects = view.reduceLaunch(state.launch, event)
		return state, effects
	case EventGitHub:
		var panel GitHubPanel
		if json.Unmarshal([]byte(event.Fields.Get(FieldPanel)), &panel) == nil {
			state.github = panel
		}
		return state, nil
	case EventCloud, EventCloudStop, EventCloudStopped:
		var effects []live.Effect[ViewerIdentity]
		state.cloud, effects = view.reduceCloud(state.cloud, event)
		return state, effects
	case live.EffectFailedEvent:
		state.notice = event.Fields.Get(live.EffectFailedSourceField) + " failed: " + event.Fields.Get(live.EffectFailedErrorField)
		return state, nil
	}
	var effects []live.Effect[ViewerIdentity]
	state.cards, effects = view.cards.Reduce(state.cards, event)
	return state, effects
}

// reduceLaunch is the launch form's transition.
func (view *OpsView) reduceLaunch(state launchState, event live.Event) (launchState, []live.Effect[ViewerIdentity]) {
	switch event.Name {
	case EventTemplates:
		state.Templates = strings.Split(event.Fields.Get(FieldTemplates), templateSeparator)
		if len(state.Templates) == 1 && state.Templates[0] == "" {
			state.Templates = nil
		}
	case EventCheck:
		state.Ticket, state.Template = strings.TrimSpace(event.Fields.Get(FieldTicket)), event.Fields.Get(FieldTemplate)
		state.Recipe, state.Check, state.Held = "", nil, ""
		if view.operations == nil || view.templates == nil {
			state.Notice = "This page is read-only: it was mounted without the control plane."
			return state, nil
		}
		state.Pending, state.Notice = true, "Checking admission…"
		return state, []live.Effect[ViewerIdentity]{{Source: EventCheck, Run: view.check(state.Ticket, state.Template)}}
	case EventChecked:
		var check harnessv1.LaunchCheck
		if protojson.Unmarshal([]byte(event.Fields.Get(FieldCheck)), &check) == nil {
			state.Check = &check
		}
		state.Recipe, state.Held, state.Pending, state.Notice = event.Fields.Get(FieldRecipe), event.Fields.Get(FieldHeld), false, ""
	case EventLaunch:
		if state.Recipe == "" || view.operations == nil {
			state.Notice = "Check a ticket and template first."
			return state, nil
		}
		state.Pending, state.Notice = true, "Launching: the worktree and the turn executor are being prepared…"
		return state, []live.Effect[ViewerIdentity]{{Source: EventLaunch, Run: view.launch(state.Recipe)}}
	case EventLaunched:
		state = launchState{Templates: state.Templates, Notice: "Launched " + event.Fields.Get(FieldAssigned) + "; its card is on the board."}
	case EventRefused, EventExecutorSet:
		state.Pending, state.Notice = false, event.Fields.Get(FieldNotice)
	case EventExecutor:
		if view.operations == nil {
			state.Notice = "This page is read-only: it was mounted without the control plane."
			return state, nil
		}
		state.Pending, state.Notice = true, "Switching the default executor…"
		return state, []live.Effect[ViewerIdentity]{{Source: EventExecutor, Run: view.switchExecutor(event.Fields.Get(FieldExecutor), strings.TrimSpace(event.Fields.Get(FieldModel)))}}
	}
	return state, nil
}

// switchExecutor switches the default executor and model every session
// submitted from now on runs on when its recipe names none.
func (view *OpsView) switchExecutor(executor string, model string) func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
	return func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
		response, err := view.operations.SetAgentExecutorDefault(ctx, &harnessv1.SetAgentExecutorDefaultRequest{
			ExecutorDefault: &harnessv1.AgentExecutorDefault{Executor: executor, Model: model}})
		if err != nil {
			return emitRefusal(emit, err)
		}
		return emit(live.Event{Name: EventExecutorSet, FragmentID: LaunchRegion, Fields: live.NewFields(map[string]string{
			FieldNotice: "New sessions run on " + executorLabel(response.GetExecutorDefault()) + "; was " + executorLabel(response.GetPrevious()) + ".",
		})})
	}
}

// check prepares the recipe from the template and the ticket, and asks the
// control plane for its admission check.
func (view *OpsView) check(ticket string, template string) func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
	return func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
		recipe, err := RecipeFromTemplate(view.templates, template, ticket, uuid.NewString())
		if err != nil {
			return emitRefusal(emit, err)
		}
		response, err := view.operations.CheckAgentSessionAdmission(ctx, &harnessv1.CheckAgentSessionAdmissionRequest{Recipe: recipe})
		if err != nil {
			return emitRefusal(emit, err)
		}
		encodedRecipe, err := protojson.Marshal(recipe)
		if err != nil {
			return err
		}
		encodedCheck, err := protojson.Marshal(response.GetCheck())
		if err != nil {
			return err
		}
		return emit(live.Event{Name: EventChecked, FragmentID: LaunchRegion, Fields: live.NewFields(map[string]string{
			FieldRecipe: string(encodedRecipe), FieldCheck: string(encodedCheck), FieldHeld: response.GetHeld(),
		})})
	}
}

// launch submits the checked recipe.
func (view *OpsView) launch(encoded string) func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
	return func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
		recipe := &pb.AgentAssignmentRecipe{}
		if err := protojson.Unmarshal([]byte(encoded), recipe); err != nil {
			return emitRefusal(emit, err)
		}
		response, err := view.operations.SubmitAgentSession(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: recipe})
		if err != nil {
			return emitRefusal(emit, err)
		}
		return emit(live.Event{Name: EventLaunched, FragmentID: LaunchRegion, Fields: live.NewFields(map[string]string{FieldAssigned: response.GetSession().GetAssignmentId()})})
	}
}

// followHost reads the host and delivers the verified panel at once, which
// is when the buttons are installed, and again every refresh.
func (view *OpsView) followHost(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
	for {
		event, err := HostEvent(view.host.Panel(ctx))
		if err != nil {
			return err
		}
		if err := emit(event); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-view.host.clock.After(hostRefresh):
		}
	}
}

// reduceHost is the panel's transition. A press of a refused button says
// why; a press of one that asks for a confirm shows its diff and names; a
// confirm runs exactly the action whose diff was shown.
func (view *OpsView) reduceHost(state hostState, event live.Event) (hostState, []live.Effect[ViewerIdentity]) {
	switch event.Name {
	case EventHost:
		var panel HostPanel
		if encoded := event.Fields.Get(FieldHost); json.Unmarshal([]byte(encoded), &panel) == nil {
			state.Panel, state.encoded = panel, encoded
		}
	case EventHostDone:
		state.Pending, state.Notice = false, event.Fields.Get(FieldNotice)
	case EventHostDismiss:
		state.Confirming = ""
	case EventHostPress, EventHostConfirm:
		id := event.Fields.Get(FieldAction)
		action, found := widget.FindVerified(state.Panel.Actions, id)
		switch {
		case view.host == nil:
			state.Notice = "This page is read-only: it was mounted without the host operations."
		case !found:
			state.Notice = "That button is no longer on the panel."
		case !action.Enabled():
			state.Notice = action.Label + " is disabled: " + action.Refusal
		case state.Pending:
			state.Notice = "Wait for the last action to finish."
		case event.Name == EventHostPress && len(action.Confirm) > 0:
			state.Confirming, state.Notice = id, ""
		case event.Name == EventHostConfirm && state.Confirming != id:
			state.Notice = "Press the button again: the confirm was for another action."
		default:
			state.Confirming, state.Pending, state.Notice = "", true, action.Label+"…"
			return state, []live.Effect[ViewerIdentity]{{Source: sourceHostPress, Run: view.press(action)}}
		}
	case EventHostProfile:
		if view.host == nil {
			state.Notice = "This page is read-only: it was mounted without the host operations."
			return state, nil
		}
		input := PutHostProfileInput{Profile: HostProfile{
			Name: event.Fields.Get(FieldProfileName), Keep: splitNames(event.Fields.Get(FieldProfileKeep)),
			KeepCSFWork: event.Fields.Get(FieldKeepCSF) == checkboxOn, StopOthers: event.Fields.Get(FieldStopOthers) == checkboxOn,
		}, Remove: event.Fields.Get(FieldRemove) == checkboxOn}
		state.Pending, state.Notice = true, "Saving the profile…"
		return state, []live.Effect[ViewerIdentity]{{Source: sourceHostPress, Run: view.report(func(ctx context.Context) (string, error) {
			_, err := view.host.PutProfile(ctx, input)
			return "Saved the profile " + strings.TrimSpace(input.Profile.Name) + ".", err
		})}}
	}
	return state, nil
}

// press runs one verified action with the names its confirm showed: the
// operation plans again against the live host and refuses if that moved.
func (view *OpsView) press(action widget.Verified[HostRequest]) func(ctx context.Context, session live.Session[ViewerIdentity], emit live.Emitter) error {
	return view.report(func(ctx context.Context) (string, error) {
		return view.host.Press(ctx, action.Request, action.Confirm)
	})
}

// report runs call, then reports its outcome and a fresh panel.
func (view *OpsView) report(call func(ctx context.Context) (string, error)) func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
	return func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
		notice, err := call(ctx)
		if err != nil {
			notice = "Refused: " + firstLine(err.Error())
		}
		if err := emit(live.Event{Name: EventHostDone, FragmentID: HostRegion, Fields: live.NewFields(map[string]string{FieldNotice: notice})}); err != nil {
			return err
		}
		event, err := HostEvent(view.host.Panel(ctx))
		if err != nil {
			return err
		}
		return emit(event)
	}
}

// renderHost draws the panel: the gauges and the load guard, the confirm
// when one waits, the profiles, then each group's running containers in
// full and its stopped ones folded away.
func (view *OpsView) renderHost(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		host := state.host
		panel := host.Panel
		data := hostView{Region: HostRegion, Operable: view.host != nil, Present: panel.Report.Containers != nil || panel.Error != "",
			Error: panel.Error, Notice: host.Notice, Pending: host.Pending}
		button := func(id string, class string) (template.HTML, error) {
			action, found := widget.FindVerified(panel.Actions, id)
			if !found {
				return "", nil
			}
			rendered, err := renderComponent(ctx, widget.VerifiedButton(action, EventHostPress, FieldAction, class))
			// Only markup html/template already escaped crosses this boundary.
			return template.HTML(rendered), err
		}
		gauges := panel.Report.Gauges
		data.Load = fmt.Sprintf("%.1f / %d cores", gauges.LoadOneMinute, gauges.Cores)
		data.Memory = fmt.Sprintf("%.1f of %.1f GiB free", float64(gauges.MemoryAvailableBytes)/gibibyte, float64(gauges.MemoryTotalBytes)/gibibyte)
		data.Disk = fmt.Sprintf("%.0f of %.0f GiB free", float64(gauges.DiskFreeBytes)/gibibyte, float64(gauges.DiskTotalBytes)/gibibyte)
		data.Pressure = fmt.Sprintf("cpu %s · memory %s · io %s", pressureText(gauges.PressureCPU), pressureText(gauges.PressureMemory), pressureText(gauges.PressureIO))
		data.Resumes = panel.Report.Resumes
		data.Overloaded, data.Findings = gauges.Overloaded, gauges.Findings
		if gauges.Overloaded {
			for _, id := range []string{string(OperationProfile) + idSeparator + OnlyCSFWork, actionHold} {
				rendered, err := button(id, "act")
				if err != nil {
					return err
				}
				data.Guard = append(data.Guard, rendered)
			}
		}
		if action, found := widget.FindVerified(panel.Actions, host.Confirming); found && host.Confirming != "" {
			onConfirm, err := attributes(live.OnWith(domClick, EventHostConfirm, live.Bind{Fields: map[string]string{FieldAction: action.ID}}))
			if err != nil {
				return err
			}
			onDismiss, err := attributes(live.On(domClick, EventHostDismiss))
			if err != nil {
				return err
			}
			data.Confirm = &hostConfirmView{ID: action.ID, Label: action.Label, Result: action.Result, Names: action.Confirm,
				OnConfirm: template.HTMLAttr(onConfirm), OnDismiss: template.HTMLAttr(onDismiss)}
		}
		for _, profile := range panel.Report.Settings.Profiles {
			rendered, err := button(string(OperationProfile)+idSeparator+profile.Name, "act quiet")
			if err != nil {
				return err
			}
			data.Profiles = append(data.Profiles, hostProfileView{HostProfile: profile, KeepText: strings.Join(profile.Keep, ", "), Button: rendered})
		}
		var err error
		if data.Undo, err = button(actionUndo, "act quiet"); err != nil {
			return err
		}
		if undoable := panel.Report.Undoable; undoable != nil {
			data.Undoable = fmt.Sprintf("%s, applied %s UTC: %s", undoable.Profile, undoable.At.Format(time.DateTime), describe(undoable.Changes))
		}
		for _, group := range HostGroups {
			groupData := hostGroupView{Group: group, Title: groupTitle(group)}
			for _, action := range []ContainerAction{ActionStop, ActionPause, ActionUnpause, ActionStart} {
				rendered, err := button(groupActionID(group, action), "act quiet")
				if err != nil {
					return err
				}
				groupData.Buttons = append(groupData.Buttons, rendered)
			}
			for _, found := range panel.Report.Containers {
				if found.Group != group {
					continue
				}
				row := hostRowView{HostContainer: found, CPU: "–", Memory: "–"}
				if found.CPUPercent >= 0 {
					row.CPU = fmt.Sprintf("%.0f%%", found.CPUPercent)
				}
				if found.MemoryBytes > 0 {
					row.Memory = fmt.Sprintf("%.2f GiB", float64(found.MemoryBytes)/gibibyte)
				}
				verbs := []string{}
				for _, action := range containerActions(found.State) {
					verbs = append(verbs, string(action))
				}
				verbs = append(verbs, map[bool]string{false: string(OperationProtect), true: "un" + string(OperationProtect)}[found.Protected])
				for _, verb := range verbs {
					class := "act quiet"
					if verb == string(ActionStop) || verb == string(ActionPause) {
						class = "act danger"
					}
					rendered, err := button(containerActionID(verb, found.Name), class)
					if err != nil {
						return err
					}
					row.Buttons = append(row.Buttons, rendered)
				}
				if liveRank(found.State) < 2 {
					groupData.Live = append(groupData.Live, row)
				} else {
					groupData.Stopped = append(groupData.Stopped, row)
				}
			}
			data.Groups = append(data.Groups, groupData)
		}
		onProfile, err := attributes(live.On(domSubmit, EventHostProfile))
		if err != nil {
			return err
		}
		data.OnProfile = template.HTMLAttr(onProfile)
		return views.ExecuteTemplate(writer, hostTemplate, data)
	})
}
