// Copyright 2026 Candace Labs

package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/io/inproc"
	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	grammar "github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/pkg/listsched"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	cronservice "github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/harness"
)

// ISessionHost is what the service needs of the agent harness: to open a
// session for a slice, to steer a running one, to ask one to stop at its
// next safepoint, and the launch check the dispatcher's admission starts
// from. *harness.AgentSessionService satisfies it.
//
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=dispatch.go -destination=mocks/mock_sessions.go -package=mocks
type ISessionHost interface {
	Submit(ctx context.Context, request *harnessv1.SubmitAgentSessionRequest) (*harnessv1.SubmitAgentSessionResponse, error)
	Send(ctx context.Context, request *harnessv1.SendAgentSessionMessageRequest) (*harnessv1.SendAgentSessionMessageResponse, error)
	Cancel(ctx context.Context, request *harnessv1.CancelAgentSessionRequest) (*harnessv1.CancelAgentSessionResponse, error)
	Check(ctx context.Context) (*harnessv1.LaunchCheck, error)
}

// MergeCheck reports whether the pull request at url has merged: the
// dispatcher asks it about every slice that recorded one.
type MergeCheck func(ctx context.Context, url string) (bool, error)

// SnapshotSink receives every snapshot the dispatcher publishes, on the
// service's own goroutine: the binary writes it to the state directory.
type SnapshotSink func(snapshot Snapshot) error

const (
	ownerName = "dispatch"
	// resumeBranchFormat names the branch of a slice's later attempt: the
	// earlier worktree still holds the first branch.
	resumeBranchFormat = "%s-r%d"
	// resumeTaskFormat appends the checkpoint to a re-enqueued slice's task.
	resumeTaskFormat = "%s\n\nResume from the checkpoint of the previous attempt: %s"
	// checkpointFormat is what a preempted slice remembers of its session.
	checkpointFormat = "attempt %d stopped at a safepoint after %d turns on branch %s; pull request: %s"
	noPullRequest    = "none"

	// Findings Route reports for the checks it cannot make yet.
	findingGateCheck   = "already-done check for gates is not implemented: a gate that already enforces the intent is not detected"
	findingNoCoverage  = "no slice's touch-set covers the terms; give new_slice to create one"
	findingNoOntology  = "no ontology source is configured, so term existence was not checked"
	findingMissingTerm = "term not defined in the ontology: %s"
	findingNoMessage   = "no message was given, so nothing was sent into the running session"
	findingNotQueued   = "the covering slice is %s; the intent is attached but the slice is not in the frontier"
)

// Option configures a [DispatchService].
type Option func(service *DispatchService) error

// WithSessions grants the agent harness the frontier is dispatched onto.
// Required.
func WithSessions(sessions ISessionHost) Option {
	return func(service *DispatchService) error {
		if sessions == nil {
			return ErrNoSessions
		}
		service.sessions = sessions
		return nil
	}
}

// WithDatabase grants the csfpg-backed persistence every change is written
// through to and the graph is restored from. Without it the graph lives only
// in this process.
func WithDatabase(database IDispatchDatabase) Option {
	return func(service *DispatchService) error {
		if database == nil {
			return fmt.Errorf("%w: nil database", ErrInvalidOption)
		}
		service.database = database
		return nil
	}
}

// WithLimit adds one measure the dispatcher's admission is the minimum of,
// beside the harness's launch check, which is always one: [DailyBudgetLimit]
// and [RateLimit] are the binary's.
func WithLimit(source LimitSource) Option {
	return func(service *DispatchService) error {
		if source == nil {
			return fmt.Errorf("%w: nil limit", ErrInvalidOption)
		}
		service.limits = append(service.limits, source)
		return nil
	}
}

// WithMergeCheck grants the check a dispatcher pass asks about every recorded
// pull request, so a slice whose pull request merged anywhere — by the merge
// train, its own session or the operator — releases what depends on it.
// Without it only MarkSliceMerged records a merge.
func WithMergeCheck(check MergeCheck) Option {
	return func(service *DispatchService) error {
		if check == nil {
			return fmt.Errorf("%w: nil merge check", ErrInvalidOption)
		}
		service.mergeCheck = check
		return nil
	}
}

// WithSnapshotSink receives every published snapshot.
func WithSnapshotSink(sink SnapshotSink) Option {
	return func(service *DispatchService) error {
		if sink == nil {
			return fmt.Errorf("%w: nil snapshot sink", ErrInvalidOption)
		}
		service.sink = sink
		return nil
	}
}

// WithClock replaces the clock.
func WithClock(clock harness.IClock) Option {
	return func(service *DispatchService) error {
		if clock == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidOption)
		}
		service.clock = clock
		return nil
	}
}

// WithLogger receives the service's records.
func WithLogger(logger *slog.Logger) Option {
	return func(service *DispatchService) error {
		if logger == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidOption)
		}
		service.logger = logger
		return nil
	}
}

// WithOntologySource names the architecture.csf whose terms Route checks an
// ontology-scoped intent against.
func WithOntologySource(path string) Option {
	return func(service *DispatchService) error {
		if path == "" {
			return fmt.Errorf("%w: empty ontology source", ErrInvalidOption)
		}
		service.ontology = path
		return nil
	}
}

// command is one unit of work on the graph, run by its owner goroutine.
type command func(ctx context.Context, store *graph)

// DispatchService is the slice graph and its dispatch onto harness sessions.
// One goroutine owns the graph; every operation and every session event is
// a command it pops from one queue.
type DispatchService struct {
	sessions   ISessionHost
	database   IDispatchDatabase
	limits     []LimitSource
	mergeCheck MergeCheck
	sink       SnapshotSink
	clock      harness.IClock
	logger     *slog.Logger
	ontology   string
	commands   *inproc.Queue[command]
	stopped    chan struct{}
	scope      *runtime.Scope
}

var _ runtime.IService = (*DispatchService)(nil)
var _ csf.IDispatch = (*DispatchService)(nil)

// NewDispatchService validates the whole option set before building the
// service. [WithSessions] is required.
func NewDispatchService(options ...Option) (*DispatchService, error) {
	service := &DispatchService{
		clock:    harness.SystemClock{},
		logger:   slog.New(slog.DiscardHandler),
		commands: inproc.NewQueue[command](),
		stopped:  make(chan struct{}),
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	if service.sessions == nil {
		return nil, ErrNoSessions
	}
	service.limits = append([]LimitSource{harnessLimit(service.sessions)}, service.limits...)
	return service, nil
}

// Start restores the graph and the controls from the database, when one was
// granted, starts the owner goroutine on the scope and runs the first
// dispatcher pass. Slices the previous process was running return to the
// frontier and are dispatched by that pass.
func (service *DispatchService) Start(scope *runtime.Scope) error {
	store := newGraph()
	if service.database != nil {
		if err := service.restore(scope.Context(), store); err != nil {
			return err
		}
	}
	service.scope = scope
	if err := scope.GoOwner(ownerName, func(ctx context.Context) error {
		defer close(service.stopped)
		for {
			next, err := service.commands.Pop(ctx)
			if err != nil {
				service.commands.Close()
				service.drain(store)
				return nil
			}
			next(ctx, store)
		}
	}); err != nil {
		return err
	}
	return scope.Go(func(ctx context.Context) error {
		if _, err := service.Dispatch(ctx); err != nil && ctx.Err() == nil {
			service.logger.Warn("dispatch: first pass incomplete", "error", err)
		}
		return nil
	})
}

// drain runs every command still queued after the scope ended, under a
// canceled context, so no caller waits on a stopped service.
func (service *DispatchService) drain(store *graph) {
	canceled, cancel := context.WithCancelCause(context.Background())
	cancel(ErrNotStarted)
	for {
		next, err := service.commands.Pop(canceled)
		if err != nil {
			return
		}
		next(canceled, store)
	}
}

// command runs one unit of work on the owner and waits for its result.
func (service *DispatchService) command(ctx context.Context, work func(ctx context.Context, store *graph) error) error {
	reply := make(chan error, 1)
	if err := service.commands.Push(func(ctx context.Context, store *graph) { reply <- work(ctx, store) }); err != nil {
		return ErrNotStarted
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-service.stopped:
		select {
		case err := <-reply:
			return err
		default:
			return ErrNotStarted
		}
	}
}

// push queues work the owner runs without a caller waiting: session events
// and launch results. A stopped service drops them.
func (service *DispatchService) push(work command) {
	if err := service.commands.Push(work); err != nil {
		service.logger.Warn("dispatch: event dropped after stop")
	}
}

// ObserveSession is the harness session observer: the host wires it with
// harness.WithSessionObserver. Session events reach the graph in order.
func (service *DispatchService) ObserveSession(state *harnessv1.AgentSessionState) {
	service.push(func(ctx context.Context, store *graph) { service.observe(ctx, store, state) })
}

func (service *DispatchService) now() *timestamppb.Timestamp {
	return timestamppb.New(service.clock.Now())
}

// Enqueue admits one slice with its edges, touch-set and provenance. Every
// slice its edges name must be enqueued already; a depends_on relation that
// would close a cycle is rejected.
func (service *DispatchService) Enqueue(ctx context.Context, request *dispatchv1.EnqueueSliceRequest) (*dispatchv1.EnqueueSliceResponse, error) {
	if err := validateEnqueue(request); err != nil {
		return nil, err
	}
	response := &dispatchv1.EnqueueSliceResponse{}
	err := service.command(ctx, func(ctx context.Context, store *graph) error {
		admitted, err := service.enqueue(ctx, store, request)
		if err != nil {
			return err
		}
		service.schedule(ctx, store)
		response.Node = store.views([]string{admitted})[0]
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// validateEnqueue checks the request's typed parts and that the recipe is
// one the harness can run.
func validateEnqueue(request *dispatchv1.EnqueueSliceRequest) error {
	slice := request.GetSlice()
	if slice == nil {
		return fmt.Errorf("%w: a slice is required", ErrInvalidSlice)
	}
	if err := dispatchv1.ValidateSlice(slice); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSlice, err)
	}
	if request.GetProvenance() != nil {
		if err := dispatchv1.ValidateProvenance(request.GetProvenance()); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidSlice, err)
		}
	}
	if slice.GetRecipe().GetWorkspace() == nil {
		return fmt.Errorf("%w: the recipe needs a workspace for the harness to run it", ErrInvalidSlice)
	}
	if _, err := csf.PrepareAgentAssignment(slice.GetRecipe()); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSlice, err)
	}
	return nil
}

// enqueue admits the slice on the owner: it checks the edges, persists the
// node, then places it in the graph.
func (service *DispatchService) enqueue(ctx context.Context, store *graph, request *dispatchv1.EnqueueSliceRequest) (string, error) {
	id := request.GetSlice().GetSliceId()
	if store.has(id) {
		return "", fmt.Errorf("%w: %s", ErrSliceExists, id)
	}
	edges := request.GetEdges()
	for _, other := range slices.Concat(edges.GetDependsOn(), edges.GetRequiredBy(), edges.GetContends()) {
		if other == id {
			return "", fmt.Errorf("%w: %s names itself", ErrCycle, id)
		}
		if !store.has(other) {
			return "", fmt.Errorf("%w: %s", ErrUnknownSlice, other)
		}
	}
	for _, successor := range edges.GetRequiredBy() {
		for _, predecessor := range edges.GetDependsOn() {
			if successor == predecessor || store.edges.Reaches(RelationDependsOn, successor, predecessor) {
				return "", fmt.Errorf("%w: %s -> %s -> %s", ErrCycle, predecessor, id, successor)
			}
		}
	}
	record := &dispatchv1.SliceNode{
		Slice:      proto.Clone(request.GetSlice()).(*dispatchv1.Slice),
		Edges:      proto.Clone(edges).(*dispatchv1.SliceEdges),
		TouchSet:   proto.Clone(request.GetTouchSet()).(*dispatchv1.TouchSet),
		Provenance: proto.Clone(request.GetProvenance()).(*dispatchv1.Provenance),
		State:      dispatchv1.SliceState_SLICE_STATE_QUEUED,
		CreatedAt:  service.now(),
		UpdatedAt:  service.now(),
	}
	if record.Edges == nil {
		record.Edges = &dispatchv1.SliceEdges{}
	}
	if record.TouchSet == nil {
		record.TouchSet = &dispatchv1.TouchSet{}
	}
	if record.Provenance == nil {
		record.Provenance = &dispatchv1.Provenance{}
	}
	sequence := store.sequence + 1
	if err := service.persistNew(ctx, record, sequence); err != nil {
		return "", err
	}
	if err := store.add(record, sequence); err != nil {
		return "", err
	}
	for _, predecessor := range edges.GetDependsOn() {
		if err := store.precede(predecessor, id); err != nil {
			return "", err
		}
	}
	for _, successor := range edges.GetRequiredBy() {
		if err := store.precede(id, successor); err != nil {
			return "", err
		}
	}
	for _, other := range edges.GetContends() {
		store.exclude(id, other)
	}
	service.logger.Info("dispatch: slice enqueued", "slice", id, "sequence", sequence)
	return id, nil
}

// DeclareIntent records an intent and attaches it to the slices its scope or
// terms route it to. An URGENT intent preempts a lower-ranked running session
// standing between its slice and the cap.
func (service *DispatchService) DeclareIntent(ctx context.Context, request *dispatchv1.DeclareIntentRequest) (*dispatchv1.DeclareIntentResponse, error) {
	if err := validateIntent(request.GetIntent()); err != nil {
		return nil, err
	}
	response := &dispatchv1.DeclareIntentResponse{}
	err := service.command(ctx, func(ctx context.Context, store *graph) error {
		attached, err := service.declare(ctx, store, request.GetIntent())
		if err != nil {
			return err
		}
		response.Nodes = store.views(attached)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// Reprioritize re-declares an existing intent: a changed urgency, deadline
// or supersedes takes effect on the slices it attaches to.
func (service *DispatchService) Reprioritize(ctx context.Context, request *dispatchv1.ReprioritizeRequest) (*dispatchv1.ReprioritizeResponse, error) {
	if err := validateIntent(request.GetIntent()); err != nil {
		return nil, err
	}
	response := &dispatchv1.ReprioritizeResponse{}
	err := service.command(ctx, func(ctx context.Context, store *graph) error {
		if _, known := store.intents[request.GetIntent().GetIntentId()]; !known {
			return fmt.Errorf("%w: %s", ErrUnknownIntent, request.GetIntent().GetIntentId())
		}
		attached, err := service.declare(ctx, store, request.GetIntent())
		if err != nil {
			return err
		}
		response.Nodes = store.views(attached)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func validateIntent(intent *dispatchv1.Intent) error {
	if intent == nil {
		return fmt.Errorf("%w: an intent is required", ErrInvalidIntent)
	}
	if err := dispatchv1.ValidateIntent(intent); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidIntent, err)
	}
	return nil
}

// declare attaches the intent on the owner and reschedules. It returns the
// slices the intent attached to.
func (service *DispatchService) declare(ctx context.Context, store *graph, intent *dispatchv1.Intent) ([]string, error) {
	id := intent.GetIntentId()
	store.intents[id] = proto.Clone(intent).(*dispatchv1.Intent)
	if superseded := intent.GetSupersedes(); superseded != "" {
		if previous, known := store.intents[superseded]; known {
			store.detach(superseded)
			delete(store.intents, superseded)
			if err := service.persistIntent(ctx, previous, ""); err != nil {
				return nil, err
			}
		}
	}
	var attached []string
	for _, target := range store.targets(intent) {
		if terminal(store.state(target)) {
			continue
		}
		store.attach(intent, target)
		store.nodes[target].proto.UpdatedAt = service.now()
		if err := service.persistIntent(ctx, intent, target); err != nil {
			return nil, err
		}
		attached = append(attached, target)
	}
	if len(attached) == 0 {
		if err := service.persistIntent(ctx, intent, ""); err != nil {
			return nil, err
		}
	}
	if intent.GetUrgency() == dispatchv1.Urgency_URGENCY_URGENT {
		for _, target := range attached {
			service.preempt(ctx, store, target)
		}
	}
	service.schedule(ctx, store)
	return attached, nil
}

// Route sends an operator message to the slice whose touch-set covers the
// intent's terms: into its running session (steer), onto the queued slice
// (queue), as a new slice when none covers the terms (new), or back as
// already done.
func (service *DispatchService) Route(ctx context.Context, request *dispatchv1.RouteMessageRequest) (*dispatchv1.RouteMessageResponse, error) {
	if err := dispatchv1.ValidateRouteMessageRequest(request); err != nil {
		return nil, fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	if err := validateIntent(request.GetIntent()); err != nil {
		return nil, err
	}
	if request.GetNewSlice() != nil {
		if err := validateEnqueue(request.GetNewSlice()); err != nil {
			return nil, err
		}
	}
	response := &dispatchv1.RouteMessageResponse{}
	err := service.command(ctx, func(ctx context.Context, store *graph) error {
		return service.route(ctx, store, request, response)
	})
	if err != nil {
		return nil, err
	}
	if response.GetDecision() == dispatchv1.RouteDecision_ROUTE_DECISION_STEER {
		if request.GetMessage() == "" {
			response.Findings = append(response.Findings, findingNoMessage)
			return response, nil
		}
		sent, err := service.sessions.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: response.GetAssignmentId(), Message: request.GetMessage()})
		if err != nil {
			return nil, err
		}
		response.TurnId = sent.GetTurnId()
	}
	return response, nil
}

// route decides on the owner; the steer itself happens outside it.
func (service *DispatchService) route(ctx context.Context, store *graph, request *dispatchv1.RouteMessageRequest, response *dispatchv1.RouteMessageResponse) error {
	intent := request.GetIntent()
	response.Findings = append(response.Findings, findingGateCheck)
	if targets := store.targets(intent); len(targets) > 0 {
		target := targets[0]
		record := store.nodes[target]
		response.SliceId = target
		switch record.proto.GetState() {
		case dispatchv1.SliceState_SLICE_STATE_MERGED:
			response.Decision = dispatchv1.RouteDecision_ROUTE_DECISION_ALREADY_DONE
			response.Reference = record.proto.GetPullRequestUrl()
		case dispatchv1.SliceState_SLICE_STATE_RUNNING:
			response.Decision = dispatchv1.RouteDecision_ROUTE_DECISION_STEER
			response.AssignmentId = record.proto.GetAssignmentId()
			store.intents[intent.GetIntentId()] = proto.Clone(intent).(*dispatchv1.Intent)
			store.attach(intent, target)
			if err := service.persistIntent(ctx, intent, target); err != nil {
				return err
			}
		default:
			response.Decision = dispatchv1.RouteDecision_ROUTE_DECISION_QUEUE
			if _, err := service.declare(ctx, store, intent); err != nil {
				return err
			}
			if state := record.proto.GetState(); state != dispatchv1.SliceState_SLICE_STATE_QUEUED {
				response.Findings = append(response.Findings, fmt.Sprintf(findingNotQueued, state.String()))
			}
		}
		response.Node = store.views([]string{target})[0]
		return nil
	}
	if intent.GetScope() == ScopeOntology {
		done, findings, err := service.termsExist(intent.GetTerms())
		if err != nil {
			return err
		}
		response.Findings = append(response.Findings, findings...)
		if done {
			response.Decision = dispatchv1.RouteDecision_ROUTE_DECISION_ALREADY_DONE
			response.Reference = service.ontology
			return nil
		}
	}
	response.Decision = dispatchv1.RouteDecision_ROUTE_DECISION_NEW
	if request.GetNewSlice() == nil {
		response.Findings = append(response.Findings, findingNoCoverage)
		return nil
	}
	created, err := service.enqueue(ctx, store, request.GetNewSlice())
	if err != nil {
		return err
	}
	response.SliceId = created
	store.intents[intent.GetIntentId()] = proto.Clone(intent).(*dispatchv1.Intent)
	store.attach(intent, created)
	if err := service.persistIntent(ctx, intent, created); err != nil {
		return err
	}
	service.schedule(ctx, store)
	response.Node = store.views([]string{created})[0]
	return nil
}

// termsExist reports whether every term is defined in the ontology source,
// with a finding per missing term; without a source it reports not done and
// says why.
func (service *DispatchService) termsExist(terms []string) (bool, []string, error) {
	if service.ontology == "" {
		return false, []string{findingNoOntology}, nil
	}
	defined, err := ontologyTerms(service.ontology)
	if err != nil {
		return false, nil, err
	}
	missing := missingTerms(defined, terms)
	findings := make([]string, 0, len(missing))
	for _, term := range missing {
		findings = append(findings, fmt.Sprintf(findingMissingTerm, term))
	}
	return len(terms) > 0 && len(missing) == 0, findings, nil
}

// Frontier reports the slices whose predecessors have merged, in dispatch
// order with their priority breakdown.
func (service *DispatchService) Frontier(ctx context.Context, _ *dispatchv1.GetFrontierRequest) (*dispatchv1.GetFrontierResponse, error) {
	response := &dispatchv1.GetFrontierResponse{}
	err := service.command(ctx, func(_ context.Context, store *graph) error {
		response.Nodes = store.views(store.frontier(store.ranks()))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// List reports every slice in enqueue order with its priority breakdown, the
// admission the latest dispatcher pass derived and how many sessions run
// under it.
func (service *DispatchService) List(ctx context.Context, _ *dispatchv1.ListSlicesRequest) (*dispatchv1.ListSlicesResponse, error) {
	response := &dispatchv1.ListSlicesResponse{Persisted: service.database != nil}
	err := service.command(ctx, func(_ context.Context, store *graph) error {
		response.Nodes = store.views(store.order())
		response.ConcurrencyCap = uint32(store.capacity)
		response.Running = uint32(len(store.runningIDs()))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// MarkMerged records that a slice's pull request merged: its session, if one
// still runs, is asked to stop, and every slice that depended on it may
// enter the frontier.
func (service *DispatchService) MarkMerged(ctx context.Context, request *dispatchv1.MarkSliceMergedRequest) (*dispatchv1.MarkSliceMergedResponse, error) {
	if err := dispatchv1.ValidateMarkSliceMergedRequest(request); err != nil {
		return nil, fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	response := &dispatchv1.MarkSliceMergedResponse{}
	err := service.command(ctx, func(ctx context.Context, store *graph) error {
		id := request.GetSliceId()
		if !store.has(id) {
			return fmt.Errorf("%w: %s", ErrUnknownSlice, id)
		}
		record := store.nodes[id]
		switch record.proto.GetState() {
		case dispatchv1.SliceState_SLICE_STATE_MERGED:
			response.Nodes = store.views([]string{id})
			return nil
		case dispatchv1.SliceState_SLICE_STATE_CANCELED:
			return fmt.Errorf("%w: %s", ErrSliceFinished, id)
		}
		before := store.frontier(store.ranks())
		if assignment := record.proto.GetAssignmentId(); assignment != "" {
			delete(store.running, assignment)
			service.cancelSession(assignment)
		}
		record.proto.State = dispatchv1.SliceState_SLICE_STATE_MERGED
		record.proto.AssignmentId = ""
		if url := request.GetPullRequestUrl(); url != "" {
			record.proto.PullRequestUrl = url
		}
		if err := service.persistState(ctx, record); err != nil {
			return err
		}
		released := slices.DeleteFunc(store.frontier(store.ranks()), func(candidate string) bool { return slices.Contains(before, candidate) })
		service.schedule(ctx, store)
		response.Nodes = store.views(append([]string{id}, released...))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// schedule is one list-scheduling pass: the frontier in rank order, less the
// held slices and the slices whose provider's rate limit admits no launches,
// onto the admission the latest dispatcher pass derived, skipping what
// contends with a running slice. A paused dispatcher launches nothing.
func (service *DispatchService) schedule(ctx context.Context, store *graph) {
	if store.paused != "" {
		return
	}
	ranks := store.ranks()
	ready := slices.DeleteFunc(store.launchable(ranks), func(id string) bool { return store.throttle(id) != "" })
	for _, id := range listsched.Pick(ready, store.runningIDs(), store.capacity, store.conflicts, store.less(ranks)) {
		service.launch(ctx, store, id)
	}
}

// launch marks the slice running and opens its session off the owner; the
// result returns as a command.
func (service *DispatchService) launch(ctx context.Context, store *graph, id string) {
	record := store.nodes[id]
	record.proto.Attempts++
	assignment := assignmentFor(record.proto)
	record.proto.State = dispatchv1.SliceState_SLICE_STATE_RUNNING
	record.proto.AssignmentId = assignment
	record.proto.Error = ""
	store.running[assignment] = id
	if err := service.persistState(ctx, record); err != nil {
		service.logger.Error("dispatch: launch not recorded", "slice", id, "error", err)
		delete(store.running, assignment)
		record.proto.Attempts--
		record.proto.State = dispatchv1.SliceState_SLICE_STATE_QUEUED
		record.proto.AssignmentId = ""
		return
	}
	recipe := resumeRecipe(record.proto, assignment)
	service.logger.Info("dispatch: slice launched", "slice", id, "assignment", assignment, "attempt", record.proto.GetAttempts())
	if err := service.scope.Go(func(ctx context.Context) error {
		response, err := service.sessions.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: recipe})
		service.push(func(ctx context.Context, store *graph) { service.launched(ctx, store, id, assignment, response, err) })
		return nil
	}); err != nil {
		service.launched(ctx, store, id, assignment, nil, err)
	}
}

// launched records a session's opening or the failure that kept it closed.
func (service *DispatchService) launched(ctx context.Context, store *graph, id string, assignment string, response *harnessv1.SubmitAgentSessionResponse, err error) {
	record := store.nodes[id]
	if record.proto.GetAssignmentId() != assignment {
		return
	}
	if err != nil {
		service.logger.Warn("dispatch: session not opened", "slice", id, "assignment", assignment, "error", err)
		delete(store.running, assignment)
		record.proto.State = dispatchv1.SliceState_SLICE_STATE_FAILED
		record.proto.AssignmentId = ""
		record.proto.Error = err.Error()
		if errors.Is(err, harness.ErrAdmissionHeld) {
			// The harness held admission after the pass measured it: the
			// slice waits for the next pass rather than failing.
			record.proto.State = dispatchv1.SliceState_SLICE_STATE_QUEUED
			record.proto.Attempts--
			store.capacity = len(store.runningIDs())
		}
		if persistErr := service.persistState(ctx, record); persistErr != nil {
			service.logger.Error("dispatch: failure not recorded", "slice", id, "error", persistErr)
		}
		service.schedule(ctx, store)
		return
	}
	service.logger.Info("dispatch: session open", "slice", id, "assignment", assignment, "worker_cap", response.GetCheck().GetWorkerCap())
}

// assignmentFor is the assignment of the slice's current attempt: the
// recipe's own for the first, one derived from the slice and attempt after.
func assignmentFor(record *dispatchv1.SliceNode) string {
	if record.GetAttempts() <= 1 {
		return record.GetSlice().GetRecipe().GetAssignmentId()
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "%s#%d", record.GetSlice().GetSliceId(), record.GetAttempts())).String()
}

// resumeRecipe is the recipe a session runs for the slice's current attempt:
// a later attempt works on a branch of its own and starts from the
// checkpoint.
func resumeRecipe(record *dispatchv1.SliceNode, assignment string) *pb.AgentAssignmentRecipe {
	recipe := proto.Clone(record.GetSlice().GetRecipe()).(*pb.AgentAssignmentRecipe)
	recipe.AssignmentId = assignment
	if record.GetAttempts() > 1 {
		if workspace := recipe.GetWorkspace(); workspace != nil {
			workspace.Branch = fmt.Sprintf(resumeBranchFormat, workspace.GetBranch(), record.GetAttempts())
		}
		if record.GetCheckpoint() != "" {
			recipe.Task = fmt.Sprintf(resumeTaskFormat, recipe.GetTask(), record.GetCheckpoint())
		}
	}
	return recipe
}

// preempt makes room for a queued slice an urgent intent attached to: when
// the cap or a contending session keeps it from starting, the lowest-ranked
// running session that ranks below it is asked to stop at its next
// safepoint. The preempted slice is checkpointed and re-enqueued when its
// session reports CANCELED.
func (service *DispatchService) preempt(ctx context.Context, store *graph, target string) {
	if store.state(target) != dispatchv1.SliceState_SLICE_STATE_QUEUED || store.paused != "" {
		return
	}
	ranks := store.ranks()
	if !slices.Contains(store.launchable(ranks), target) {
		return
	}
	running := store.runningIDs()
	full := len(running) >= store.capacity
	var blockers []string
	for _, id := range running {
		if store.state(id) == dispatchv1.SliceState_SLICE_STATE_RUNNING && (full || store.conflicts(target, id)) {
			blockers = append(blockers, id)
		}
	}
	if len(blockers) == 0 {
		return
	}
	less := store.less(ranks)
	victim := blockers[0]
	for _, candidate := range blockers[1:] {
		if less(victim, candidate) {
			victim = candidate
		}
	}
	if !less(target, victim) {
		return
	}
	record := store.nodes[victim]
	record.proto.State = dispatchv1.SliceState_SLICE_STATE_PREEMPTED
	if err := service.persistState(ctx, record); err != nil {
		service.logger.Error("dispatch: preemption not recorded", "slice", victim, "error", err)
		record.proto.State = dispatchv1.SliceState_SLICE_STATE_RUNNING
		return
	}
	service.logger.Info("dispatch: preempting", "slice", victim, "for", target, "assignment", record.proto.GetAssignmentId())
	service.cancelSession(record.proto.GetAssignmentId())
}

// cancelSession asks the harness to stop a session at its next safepoint,
// off the owner.
func (service *DispatchService) cancelSession(assignment string) {
	if err := service.scope.Go(func(ctx context.Context) error {
		if _, err := service.sessions.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: assignment}); err != nil {
			service.logger.Warn("dispatch: cancel not delivered", "assignment", assignment, "error", err)
		}
		return nil
	}); err != nil {
		service.logger.Warn("dispatch: cancel not started", "assignment", assignment, "error", err)
	}
}

// observe applies one session state change to the slice it runs.
func (service *DispatchService) observe(ctx context.Context, store *graph, state *harnessv1.AgentSessionState) {
	id, owned := store.running[state.GetAssignmentId()]
	if !owned {
		return
	}
	record := store.nodes[id]
	changed := false
	if url := state.GetPullRequestUrl(); url != "" && url != record.proto.GetPullRequestUrl() {
		record.proto.PullRequestUrl = url
		changed = true
	}
	switch state.GetPhase() {
	case harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED:
		delete(store.running, state.GetAssignmentId())
		if record.proto.GetState() == dispatchv1.SliceState_SLICE_STATE_PREEMPTED {
			service.requeue(record, state)
		} else {
			record.proto.State = dispatchv1.SliceState_SLICE_STATE_CANCELED
			record.proto.AssignmentId = ""
		}
		changed = true
	case harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED:
		delete(store.running, state.GetAssignmentId())
		record.proto.State = dispatchv1.SliceState_SLICE_STATE_FAILED
		record.proto.AssignmentId = ""
		record.proto.Error = state.GetError()
		changed = true
	case harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CLOSED:
		delete(store.running, state.GetAssignmentId())
		service.requeue(record, state)
		changed = true
	}
	if !changed {
		return
	}
	if err := service.persistState(ctx, record); err != nil {
		service.logger.Error("dispatch: session change not recorded", "slice", id, "error", err)
	}
	service.logger.Info("dispatch: session observed", "slice", id, "phase", state.GetPhase().String(), "state", record.proto.GetState().String())
	service.schedule(ctx, store)
}

// requeue checkpoints a slice whose session stopped at a safepoint and puts
// it back in the queue.
func (service *DispatchService) requeue(record *node, state *harnessv1.AgentSessionState) {
	pullRequest := state.GetPullRequestUrl()
	if pullRequest == "" {
		pullRequest = noPullRequest
	}
	record.proto.Checkpoint = fmt.Sprintf(checkpointFormat, record.proto.GetAttempts(), state.GetTurns(), state.GetBranch(), pullRequest)
	record.proto.State = dispatchv1.SliceState_SLICE_STATE_QUEUED
	record.proto.AssignmentId = ""
}

// persistNew writes a node and its edges in one transaction.
func (service *DispatchService) persistNew(ctx context.Context, record *dispatchv1.SliceNode, sequence uint64) error {
	if service.database == nil {
		return nil
	}
	params, err := insertSliceParams(record, sequence, service.clock.Now())
	if err != nil {
		return err
	}
	return service.database.Transact(ctx, func(queries IDispatchQueries) error {
		if _, err := queries.InsertSlice(ctx, params); err != nil {
			return fmt.Errorf("dispatch: record slice: %w", err)
		}
		for _, edge := range edgeRows(record) {
			if err := queries.InsertSliceEdge(ctx, edge.params()); err != nil {
				return fmt.Errorf("dispatch: record edge: %w", err)
			}
		}
		return nil
	})
}

// persistState writes a node's changed state.
func (service *DispatchService) persistState(ctx context.Context, record *node) error {
	record.proto.UpdatedAt = service.now()
	if service.database == nil {
		return nil
	}
	if _, err := service.database.UpdateSliceState(ctx, updateSliceParams(record.proto, service.clock.Now())); err != nil {
		return fmt.Errorf("dispatch: record slice state: %w", err)
	}
	return nil
}

// persistIntent writes an intent attached to slice, or to none.
func (service *DispatchService) persistIntent(ctx context.Context, intent *dispatchv1.Intent, slice string) error {
	if service.database == nil {
		return nil
	}
	params, err := insertIntentParams(intent, slice, service.clock.Now())
	if err != nil {
		return err
	}
	if _, err := service.database.InsertIntent(ctx, params); err != nil {
		return fmt.Errorf("dispatch: record intent: %w", err)
	}
	return nil
}

// restore reads the whole graph back. Slices that held a session in the
// previous process return to the queue: their sessions died with it.
func (service *DispatchService) restore(ctx context.Context, store *graph) error {
	rows, err := service.database.ListSlices(ctx)
	if err != nil {
		return fmt.Errorf("dispatch: restore slices: %w", err)
	}
	for _, row := range rows {
		record, err := nodeFromRow(row)
		if err != nil {
			return err
		}
		if err := store.add(record, uint64(row.Sequence)); err != nil {
			return err
		}
	}
	edges, err := service.database.ListSliceEdges(ctx)
	if err != nil {
		return fmt.Errorf("dispatch: restore edges: %w", err)
	}
	for _, edge := range edges {
		if !store.has(edge.FromSliceID) || !store.has(edge.ToSliceID) {
			return fmt.Errorf("%w: edge %s -> %s", ErrUnknownSlice, edge.FromSliceID, edge.ToSliceID)
		}
		switch Relation(edge.Relation) {
		case RelationDependsOn:
			if err := store.precede(edge.FromSliceID, edge.ToSliceID); err != nil {
				return err
			}
			to := store.nodes[edge.ToSliceID].proto
			to.Edges.DependsOn = append(to.Edges.DependsOn, edge.FromSliceID)
		case RelationContends:
			store.exclude(edge.FromSliceID, edge.ToSliceID)
			from := store.nodes[edge.FromSliceID].proto
			from.Edges.Contends = append(from.Edges.Contends, edge.ToSliceID)
		}
	}
	intents, err := service.database.ListIntents(ctx)
	if err != nil {
		return fmt.Errorf("dispatch: restore intents: %w", err)
	}
	for _, row := range intents {
		intent := &dispatchv1.Intent{}
		if err := decode("intent", row.Intent, intent); err != nil {
			return err
		}
		store.intents[intent.GetIntentId()] = intent
		if row.SliceID != nil && store.has(*row.SliceID) {
			store.attach(intent, *row.SliceID)
		}
	}
	for _, id := range store.order() {
		record := store.nodes[id]
		if occupied(record.proto.GetState()) {
			service.requeue(record, &harnessv1.AgentSessionState{Branch: record.proto.GetSlice().GetRecipe().GetWorkspace().GetBranch(), PullRequestUrl: record.proto.GetPullRequestUrl()})
			if err := service.persistState(ctx, record); err != nil {
				return err
			}
		}
	}
	if err := service.restoreControls(ctx, store); err != nil {
		return err
	}
	service.logger.Info("dispatch: graph restored", "slices", store.edges.Len(), "intents", len(store.intents), "controls", store.controls, "paused", store.paused != "")
	return nil
}

// restoreControls replays every recorded control in order, so a pause or a
// hold outlives the process that was given it.
func (service *DispatchService) restoreControls(ctx context.Context, store *graph) error {
	controls, err := service.database.ListDispatchControls(ctx)
	if err != nil {
		return fmt.Errorf("dispatch: restore controls: %w", err)
	}
	for _, row := range controls {
		control := Control{Action: ControlAction(row.Action), Reason: row.Reason}
		if row.SliceID != nil {
			control.SliceID = *row.SliceID
		}
		store.apply(control)
		store.controls = uint64(row.Sequence)
	}
	return nil
}

// Pause stops every launch until Resume; running sessions are not touched.
func (service *DispatchService) Pause(ctx context.Context, input DispatcherControlInput) (Snapshot, error) {
	return service.control(ctx, Control{Action: ControlPause, Reason: input.Reason})
}

// Resume lets the dispatcher launch again, at once within the latest
// admission.
func (service *DispatchService) Resume(ctx context.Context, input DispatcherControlInput) (Snapshot, error) {
	return service.control(ctx, Control{Action: ControlResume, Reason: input.Reason})
}

// Hold keeps one slice from being launched until Release; a held slice that
// runs keeps running.
func (service *DispatchService) Hold(ctx context.Context, input SliceControlInput) (Snapshot, error) {
	return service.control(ctx, Control{Action: ControlHold, SliceID: input.SliceID, Reason: input.Reason})
}

// Release lets a held slice be launched again.
func (service *DispatchService) Release(ctx context.Context, input SliceControlInput) (Snapshot, error) {
	return service.control(ctx, Control{Action: ControlRelease, SliceID: input.SliceID, Reason: input.Reason})
}

// control validates, records and applies one control, then schedules and
// publishes the snapshot it leaves.
func (service *DispatchService) control(ctx context.Context, control Control) (Snapshot, error) {
	reason := strings.TrimSpace(control.Reason)
	if reason == "" || len(reason) > maxReasonBytes {
		return Snapshot{}, fmt.Errorf("%w: a reason of 1 to %d bytes is required", ErrInvalidControl, maxReasonBytes)
	}
	control.Reason = reason
	if control.Action.names() != (control.SliceID != "") {
		return Snapshot{}, fmt.Errorf("%w: hold and release name one slice; pause and resume name none", ErrInvalidControl)
	}
	var snapshot Snapshot
	err := service.command(ctx, func(ctx context.Context, store *graph) error {
		if control.Action.names() {
			if !store.has(control.SliceID) {
				return fmt.Errorf("%w: %s", ErrUnknownSlice, control.SliceID)
			}
			if control.Action == ControlHold && terminal(store.state(control.SliceID)) {
				return fmt.Errorf("%w: %s", ErrSliceFinished, control.SliceID)
			}
		}
		if err := service.persistControl(ctx, store, control); err != nil {
			return err
		}
		store.apply(control)
		service.logger.Info("dispatch: control", "action", control.Action, "slice", control.SliceID, "reason", control.Reason)
		service.schedule(ctx, store)
		snapshot = service.publish(store)
		return nil
	})
	return snapshot, err
}

// persistControl records a control under the next sequence.
func (service *DispatchService) persistControl(ctx context.Context, store *graph, control Control) error {
	sequence := store.controls + 1
	if service.database != nil {
		params := csfpg.InsertDispatchControlParams{
			Sequence: int64(sequence), Action: string(control.Action), Reason: control.Reason, RecordedAt: timestamp(service.clock.Now()),
		}
		if control.SliceID != "" {
			params.SliceID = &control.SliceID
		}
		if _, err := service.database.InsertDispatchControl(ctx, params); err != nil {
			return fmt.Errorf("dispatch: record control: %w", err)
		}
	}
	store.controls = sequence
	return nil
}

// Trigger declares the dispatcher's pass for the cron service.
func (service *DispatchService) Trigger() cronservice.Option {
	return cronservice.WithTrigger(TriggerDispatcher, grammar.Spec(grammar.Every(DispatcherInterval)), func(ctx context.Context, _ cronservice.Occurrence) error {
		_, err := service.Dispatch(ctx)
		return err
	})
}

// Dispatch is one pass of the slice dispatcher. It marks merged every slice
// whose recorded pull request merged, measures every limit, and launches the
// ready frontier in rank order up to the admission they derive: the minimum
// of the limits' launches over the dispatched sessions running. Between
// passes, merges and session ends launch within that admission at once. It
// returns the snapshot it published.
func (service *DispatchService) Dispatch(ctx context.Context) (Snapshot, error) {
	if service.mergeCheck != nil {
		if err := service.detectMerges(ctx); err != nil {
			return Snapshot{}, err
		}
	}
	running := 0
	if err := service.command(ctx, func(_ context.Context, store *graph) error {
		running = len(store.runningIDs())
		return nil
	}); err != nil {
		return Snapshot{}, err
	}
	limits := make([]Limit, 0, len(service.limits))
	for _, source := range service.limits {
		limits = append(limits, source(ctx, running))
	}
	var snapshot Snapshot
	err := service.command(ctx, func(ctx context.Context, store *graph) error {
		store.limits = limits
		// The limits' launches were measured against running sessions, so the
		// admission adds them to that count: another pass may have launched
		// since, and adding to the count read now would admit its launch twice.
		store.capacity = admission(limits, running)
		store.passAt = service.clock.Now()
		service.schedule(ctx, store)
		snapshot = service.publish(store)
		return nil
	})
	return snapshot, err
}

// detectMerges asks the merge check about every recorded pull request of an
// unfinished slice and marks merged each one that did. A pull request it
// cannot read is asked about again at the next pass.
func (service *DispatchService) detectMerges(ctx context.Context) error {
	pending, err := service.pendingMerges(ctx)
	if err != nil {
		return err
	}
	for _, candidate := range pending {
		merged, err := service.mergeCheck(ctx, candidate.url)
		if err != nil {
			service.logger.Warn("dispatch: pull request not read", "slice", candidate.slice, "url", candidate.url, "error", err)
			continue
		}
		if !merged {
			continue
		}
		if _, err := service.MarkMerged(ctx, &dispatchv1.MarkSliceMergedRequest{SliceId: candidate.slice, PullRequestUrl: candidate.url}); err != nil {
			return err
		}
		service.logger.Info("dispatch: merge detected", "slice", candidate.slice, "url", candidate.url)
	}
	return nil
}

// MarkPullRequestMerged marks merged every unfinished slice that recorded the
// pull request at url: a merge GitHub reported, so the frontier moves without
// waiting for the next pass to ask. It returns the slices it marked, none
// when no slice recorded that pull request.
func (service *DispatchService) MarkPullRequestMerged(ctx context.Context, url string) ([]string, error) {
	pending, err := service.pendingMerges(ctx)
	if err != nil {
		return nil, err
	}
	var marked []string
	for _, candidate := range pending {
		if candidate.url != url {
			continue
		}
		if _, err := service.MarkMerged(ctx, &dispatchv1.MarkSliceMergedRequest{SliceId: candidate.slice, PullRequestUrl: url}); err != nil {
			return marked, err
		}
		service.logger.Info("dispatch: merge reported", "slice", candidate.slice, "url", url)
		marked = append(marked, candidate.slice)
	}
	return marked, nil
}

func (service *DispatchService) pendingMerges(ctx context.Context) ([]pendingMerge, error) {
	var pending []pendingMerge
	err := service.command(ctx, func(_ context.Context, store *graph) error {
		pending = store.awaitingMerge()
		return nil
	})
	return pending, err
}

// HeldBranch reports whether the slice whose branch is branch is held, and the
// reason it is: a merge of a pull request from that branch is refused while
// the hold stands. It is the guard the GitHub tools ask before merging, so a
// held slice's branch cannot reach main.
func (service *DispatchService) HeldBranch(ctx context.Context, branch string) (string, bool, error) {
	var reason string
	var held bool
	err := service.command(ctx, func(_ context.Context, store *graph) error {
		reason, held = store.heldBranch(branch)
		return nil
	})
	return reason, held, err
}

// CurrentSnapshot reports the dispatcher's state now, under the latest
// pass's limits.
func (service *DispatchService) CurrentSnapshot(ctx context.Context, _ SnapshotInput) (Snapshot, error) {
	var snapshot Snapshot
	err := service.command(ctx, func(_ context.Context, store *graph) error {
		snapshot = service.snapshot(store)
		return nil
	})
	return snapshot, err
}

// publish builds the snapshot and hands it to the sink, on the owner.
func (service *DispatchService) publish(store *graph) Snapshot {
	snapshot := service.snapshot(store)
	if service.sink != nil {
		if err := service.sink(snapshot); err != nil {
			service.logger.Warn("dispatch: snapshot not published", "error", err)
		}
	}
	return snapshot
}

// snapshot is the dispatcher's state, read on the owner.
func (service *DispatchService) snapshot(store *graph) Snapshot {
	snapshot := Snapshot{
		At: service.clock.Now(), Paused: store.paused != "", PauseReason: store.paused, Capacity: store.capacity,
		Limits: slices.Clone(store.limits), LimitsAt: store.passAt, NextPassAt: store.passAt.Add(DispatcherInterval),
		Queue: []SliceView{}, Running: []SliceView{}, Held: []SliceView{}, Finished: []SliceView{},
	}
	if snapshot.Limits == nil {
		snapshot.Limits = []Limit{}
	}
	ranks := store.ranks()
	frontier := store.frontier(ranks)
	running := store.runningIDs()
	queued := slices.Clone(frontier)
	for _, id := range store.order() {
		if store.state(id) == dispatchv1.SliceState_SLICE_STATE_QUEUED && !slices.Contains(queued, id) {
			queued = append(queued, id)
		}
	}
	for _, id := range queued {
		view := store.sliceView(id, ranks, frontier)
		view.Waiting = store.waiting(id, running)
		if view.Held != "" {
			snapshot.Held = append(snapshot.Held, view)
			continue
		}
		snapshot.Queue = append(snapshot.Queue, view)
		if snapshot.NextLaunch == nil && slices.Contains(frontier, id) && !store.contendsRunning(id, running) {
			next := view
			snapshot.NextLaunch = &next
		}
	}
	for _, id := range running {
		view := store.sliceView(id, ranks, frontier)
		snapshot.Running = append(snapshot.Running, view)
		if view.Held != "" {
			snapshot.Held = append(snapshot.Held, view)
		}
	}
	for _, id := range store.order() {
		if state := store.state(id); terminal(state) || state == dispatchv1.SliceState_SLICE_STATE_FAILED {
			snapshot.Finished = append(snapshot.Finished, store.sliceView(id, ranks, frontier))
		}
	}
	return snapshot
}

// ReadySlices is at most limit slices that may run away from this host now,
// in dispatch order: on the frontier, not held, contending with nothing that
// runs here and with none of the others picked. The pause and the admission
// govern this host's launches, not another machine's, so they do not apply.
func (service *DispatchService) ReadySlices(ctx context.Context, limit int) ([]ReadySlice, error) {
	var ready []ReadySlice
	err := service.command(ctx, func(_ context.Context, store *graph) error {
		ranks := store.ranks()
		running := store.runningIDs()
		var picked []string
		for _, id := range store.launchable(ranks) {
			if len(picked) >= limit {
				break
			}
			if store.contendsRunning(id, running) || store.contendsRunning(id, picked) {
				continue
			}
			picked = append(picked, id)
			recipe := proto.Clone(store.nodes[id].proto.GetSlice().GetRecipe()).(*pb.AgentAssignmentRecipe)
			ready = append(ready, ReadySlice{SliceID: id, TicketURL: store.sliceView(id, ranks, nil).TicketURL, Recipe: recipe})
		}
		return nil
	})
	return ready, err
}
