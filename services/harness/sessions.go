// Copyright 2026 Candace Labs

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/ipc/model/claudecode"
	"github.com/candacelabs/csf/pkg/mailbox"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/harness/session"
)

var (
	// ErrNoRunner reports a service built without the session runner.
	ErrNoRunner = errors.New("harness: a session runner is required")
	// ErrInvalidServiceOption reports a nil option or a value the service
	// cannot use.
	ErrInvalidServiceOption = errors.New("harness: invalid option")
	// ErrNotStarted reports an operation before the service was mounted and
	// started, or after it stopped.
	ErrNotStarted = errors.New("harness: the session service is not running")
	// ErrUnknownSession reports an assignment this harness holds no session
	// for.
	ErrUnknownSession = fmt.Errorf("%w: no session for this assignment", csf.ErrNotFound)
	// ErrSessionExists reports a second Submit of an assignment this harness
	// already runs.
	ErrSessionExists = fmt.Errorf("%w: the assignment already has a session", csf.ErrConflict)
	// ErrSessionFinished reports a Send to a session that has ended.
	ErrSessionFinished = fmt.Errorf("%w: the session has finished", csf.ErrConflict)
	// ErrAdmissionHeld reports a Submit refused while admission is held, as
	// housekeeping holds it while free disk is below its floor.
	ErrAdmissionHeld = fmt.Errorf("%w: admission is held", csf.ErrConflict)
	// ErrInterruptBudget is the cause a running turn is killed with when its
	// executor did not reach a safepoint within the interrupt budget.
	ErrInterruptBudget = errors.New("harness: the turn did not stop at a safepoint within the interrupt budget")
)

// The environment every session's turn executor inherits for Bazel: the
// shared, content-addressed disk cache under the state directory and an
// output base of its own under the run directory, removed when the session
// ends. The OCaml toolchain cache is also shared across sessions and built once.
const (
	bazelDiskCacheVariable      = "CANDACE_BAZEL_DISK_CACHE"
	bazelOutputVariable         = "CANDACE_BAZEL_CACHE"
	ocamlToolchainCacheVariable = "CANDACE_OCAML_TOOLCHAIN_CACHE"
	// BazelDiskCacheDirectory is the shared cache, under the state directory.
	BazelDiskCacheDirectory = "bazel-disk-cache"
	// BazelOutputDirectory is a session's output base, under its run directory.
	BazelOutputDirectory = "bazel"
	// OCamlToolchainCacheDirectory is the shared OCaml toolchain, under the state directory.
	OCamlToolchainCacheDirectory = "ocaml-toolchain"

	// DefaultInterruptBudget is how long a canceled turn may take to reach a
	// safepoint before its executor is killed.
	DefaultInterruptBudget = 60 * time.Second
	// DefaultCloseBudget bounds the close of one session's executor.
	DefaultCloseBudget = 30 * time.Second

	sessionScopePrefix = "session/"
	registryOwner      = "registry"
	stateDirectoryMode = 0o700
	stateFileMode      = 0o600

	// QueueFile is a run's queued messages and the text of the turn running,
	// under its run directory, so a restart neither loses nor skips one.
	QueueFile = "queue.json"
	// EndedFile marks a run that was canceled or failed: a restarted harness
	// does not reopen it.
	EndedFile = "ended"
	// RedeliveredPrefix opens a turn delivered again because the harness
	// restarted while it ran.
	RedeliveredPrefix = "(Delivered again: the harness restarted while this turn ran.)\n\n"
)

// AgentSessionServiceOption configures an [AgentSessionService].
type AgentSessionServiceOption func(service *AgentSessionService) error

// WithSessionRunner grants the runner that prepares and opens each session.
// Required.
func WithSessionRunner(runner *session.AgentSessionRunner) AgentSessionServiceOption {
	return func(service *AgentSessionService) error {
		if runner == nil {
			return ErrNoRunner
		}
		service.runner = runner
		return nil
	}
}

// WithHostMeasures replaces the host measures the launch check reads.
func WithHostMeasures(measures IHostMeasures) AgentSessionServiceOption {
	return func(service *AgentSessionService) error {
		if measures == nil {
			return fmt.Errorf("%w: nil host measures", ErrInvalidServiceOption)
		}
		service.measures = measures
		return nil
	}
}

// WithClock replaces the clock.
func WithClock(clock IClock) AgentSessionServiceOption {
	return func(service *AgentSessionService) error {
		if clock == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidServiceOption)
		}
		service.clock = clock
		return nil
	}
}

// WithServiceLogger receives the service's own records; sessions keep writing
// their events.jsonl.
func WithServiceLogger(logger *slog.Logger) AgentSessionServiceOption {
	return func(service *AgentSessionService) error {
		if logger == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidServiceOption)
		}
		service.logger = logger
		return nil
	}
}

// WithStopRequest grants the host's shutdown: StopHarness calls it. Without
// it StopHarness reports that the host cannot be stopped this way.
func WithStopRequest(stop func()) AgentSessionServiceOption {
	return func(service *AgentSessionService) error {
		if stop == nil {
			return fmt.Errorf("%w: nil stop request", ErrInvalidServiceOption)
		}
		service.stop = stop
		return nil
	}
}

// WithInterruptBudget bounds how long a canceled turn may run on after the
// interrupt before its executor is killed.
func WithInterruptBudget(budget time.Duration) AgentSessionServiceOption {
	return func(service *AgentSessionService) error {
		if budget <= 0 {
			return fmt.Errorf("%w: interrupt budget must be positive", ErrInvalidServiceOption)
		}
		service.interruptBudget = budget
		return nil
	}
}

// WithHostPID names the process every session runs in, for List.
func WithHostPID(pid int) AgentSessionServiceOption {
	return func(service *AgentSessionService) error {
		service.hostPID = pid
		return nil
	}
}

// SessionObserver receives a copy of a session's state after each change to
// it: the phase transitions, turn counts and pull request URL. It is called
// on the goroutine that made the change, never on the registry, so it may
// enqueue work but must return promptly.
type SessionObserver func(state *harnessv1.AgentSessionState)

// WithSessionObserver adds an observer of every session's state changes. It
// may be given more than once; observers are called in the order given.
func WithSessionObserver(observe SessionObserver) AgentSessionServiceOption {
	return func(service *AgentSessionService) error {
		if observe == nil {
			return fmt.Errorf("%w: nil session observer", ErrInvalidServiceOption)
		}
		service.observers = append(service.observers, observe)
		return nil
	}
}

// AgentSessionService runs agent sessions concurrently in one process. Each
// session is one owner goroutine on a child scope of the service's scope: it
// opens the session, runs its turns from a queue Send fills, and stops at a
// safepoint when canceled or when the service stops. The table of sessions is
// owned by one goroutine behind a mailbox, so no field needs a lock.
type AgentSessionService struct {
	runner          *session.AgentSessionRunner
	measures        IHostMeasures
	clock           IClock
	logger          *slog.Logger
	stop            func()
	interruptBudget time.Duration
	closeBudget     time.Duration
	hostPID         int
	observers       []SessionObserver
	registry        *mailbox.Mailbox[registry]
}

var _ runtime.IService = (*AgentSessionService)(nil)
var _ csf.IAgentSessions = (*AgentSessionService)(nil)

// registry is the session table and the service's lifecycle, owned by the
// mailbox goroutine.
type registry struct {
	scope      *runtime.Scope
	sessions   map[string]*sessionRecord
	order      []string
	largestRun uint64
	// held is why admission is held; empty while sessions are admitted.
	held string
}

// sessionRecord is one session as the registry sees it. The owner goroutine
// mutates it only through registry commands; the few fields it writes
// directly are published before opened closes and read after.
type sessionRecord struct {
	state   *harnessv1.AgentSessionState
	receipt *pb.AgentAssignmentReceipt
	queue   []queuedMessage
	// wake is poked when the queue or the cancel flag changes.
	wake chan struct{}
	// cancelRequested is canceled by Cancel; the owner observes it at its
	// safepoints and during a turn.
	cancelRequested context.Context
	requestCancel   context.CancelCauseFunc
	cancel          bool
	// opened closes once the owner has opened the session or failed to; open
	// and openErr are written before.
	opened  chan struct{}
	open    *session.OpenSession
	openErr error
	scope   *runtime.Scope
	// directory is the run directory; resume is set when a previous harness
	// process left the run open there, so it is reopened, not created.
	directory string
	resume    bool
	// inflight is the text of the turn running now, persisted with the queue
	// so a restart mid-turn delivers it again.
	inflight string
}

// persistedQueue is the queue as the run directory keeps it across restarts.
type persistedQueue struct {
	Inflight string   `json:"inflight,omitempty"`
	Queued   []string `json:"queued"`
}

// persistQueue writes the record's queue to its run directory. It runs on the
// registry, where the queue is owned.
func (service *AgentSessionService) persistQueue(record *sessionRecord) {
	saved := persistedQueue{Inflight: record.inflight, Queued: []string{}}
	for _, message := range record.queue {
		saved.Queued = append(saved.Queued, message.text)
	}
	content, err := json.Marshal(saved)
	if err == nil {
		path := filepath.Join(record.directory, QueueFile)
		err = os.WriteFile(path+".tmp", content, stateFileMode)
		if err == nil {
			err = os.Rename(path+".tmp", path)
		}
	}
	if err != nil {
		service.logger.Warn("harness: queue not persisted", "assignment", record.state.GetAssignmentId(), "error", err)
	}
}

// restoredQueue is the queue a previous process persisted, with the turn it
// was running delivered again first.
func restoredQueue(directory string) []string {
	content, err := os.ReadFile(filepath.Join(directory, QueueFile))
	if err != nil {
		return nil
	}
	var saved persistedQueue
	if json.Unmarshal(content, &saved) != nil {
		return nil
	}
	texts := []string{}
	if saved.Inflight != "" {
		texts = append(texts, RedeliveredPrefix+saved.Inflight)
	}
	return append(texts, saved.Queued...)
}

type queuedMessage struct {
	text string
	turn uint32
}

// NewAgentSessionService validates the whole option set before building the
// service. [WithSessionRunner] is required.
func NewAgentSessionService(options ...AgentSessionServiceOption) (*AgentSessionService, error) {
	service := &AgentSessionService{
		measures:        HostMeasures{},
		clock:           SystemClock{},
		logger:          slog.New(slog.DiscardHandler),
		interruptBudget: DefaultInterruptBudget,
		closeBudget:     DefaultCloseBudget,
		registry:        mailbox.New[registry](),
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidServiceOption)
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	if service.runner == nil {
		return nil, ErrNoRunner
	}
	return service, nil
}

// Start creates the shared Bazel disk cache, measures the existing runs for
// the disk floor and starts the registry owner on the scope. The registry
// retires when the scope is canceled; every session's child scope is canceled
// with it and joined by it.
func (service *AgentSessionService) Start(scope *runtime.Scope) error {
	if err := os.MkdirAll(service.bazelDiskCache(), stateDirectoryMode); err != nil {
		return fmt.Errorf("harness: create the Bazel disk cache: %w", err)
	}
	largest, err := measureRuns(scope.Context(), service.measures, service.runner.StateDirectory())
	if err != nil {
		service.logger.Warn("harness: runs not measured", "error", err)
	}
	table := &registry{scope: scope, sessions: map[string]*sessionRecord{}, largestRun: largest}
	if err := scope.GoOwner(registryOwner, func(ctx context.Context) error {
		stopRetire := context.AfterFunc(ctx, func() {
			_ = service.registry.Submit(func(_ *registry) bool { return true })
		})
		defer stopRetire()
		service.registry.Run(table)
		return nil
	}); err != nil {
		return err
	}
	return scope.Go(service.resumeOpenRuns)
}

// resumeOpenRuns reopens every run a previous process left open: a run
// directory with a recorded recipe and run record and no end. Each is
// submitted again under its assignment, which reopens it on its recorded
// conversation.
func (service *AgentSessionService) resumeOpenRuns(ctx context.Context) error {
	entries, err := os.ReadDir(service.runner.StateDirectory())
	if err != nil {
		service.logger.Warn("harness: open runs not listed", "error", err)
		return nil
	}
	for _, entry := range entries {
		directory := filepath.Join(service.runner.StateDirectory(), entry.Name())
		if !entry.IsDir() || fileExists(filepath.Join(directory, EndedFile)) || !fileExists(filepath.Join(directory, session.RunStateFile)) {
			continue
		}
		recipe, err := session.ReadRecipe(directory)
		if err != nil {
			continue
		}
		if _, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: recipe}); err != nil {
			service.logger.Warn("harness: open run not resumed", "assignment", entry.Name(), "error", err)
		}
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (service *AgentSessionService) bazelDiskCache() string {
	return filepath.Join(service.runner.StateDirectory(), BazelDiskCacheDirectory)
}

func (service *AgentSessionService) recordLargestRun(size uint64) {
	_ = service.registry.Submit(func(table *registry) bool {
		table.largestRun = max(table.largestRun, size)
		return false
	})
}

// command runs one registry command and waits for it; a stopped registry is
// reported as the service not running.
func (service *AgentSessionService) command(ctx context.Context, command func(table *registry) error) error {
	reply := make(chan error, 1)
	if !service.registry.SubmitContext(ctx, nil, func(table *registry) bool {
		reply <- command(table)
		return false
	}) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return ErrNotStarted
	}
	return <-reply
}

// Submit admits one recipe as a session: it is checked against the launch
// bounds, recorded, and opened by its owner goroutine on a child scope. It
// returns once the session is open, with its receipt, or with the failure
// that kept it from opening.
func (service *AgentSessionService) Submit(ctx context.Context, request *harnessv1.SubmitAgentSessionRequest) (*harnessv1.SubmitAgentSessionResponse, error) {
	recipe := request.GetRecipe()
	if recipe == nil {
		return nil, fmt.Errorf("%w: a recipe is required", csf.ErrInvalidRequest)
	}
	plan, err := csf.PrepareAgentAssignment(recipe)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	id := recipe.GetAssignmentId()
	directory := session.RunDirectory(service.runner.StateDirectory(), id)
	_, recorded := session.ReadRunState(directory)
	record := &sessionRecord{
		directory: directory,
		resume:    recorded == nil,
		state: &harnessv1.AgentSessionState{
			AssignmentId: id,
			AgentId:      recipe.GetAgent().GetId(),
			SessionId:    plan.GetSessionKey(),
			Phase:        harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_STARTING,
			StartedAt:    timestamppb.New(service.clock.Now()),
			UpdatedAt:    timestamppb.New(service.clock.Now()),
		},
		wake:   make(chan struct{}, 1),
		opened: make(chan struct{}),
	}
	record.cancelRequested, record.requestCancel = context.WithCancelCause(context.Background())
	var check *harnessv1.LaunchCheck
	if err := service.command(ctx, func(table *registry) error {
		if _, exists := table.sessions[id]; exists {
			return fmt.Errorf("%w: %s", ErrSessionExists, id)
		}
		if table.held != "" {
			return fmt.Errorf("%w: %s", ErrAdmissionHeld, table.held)
		}
		check = launchCheck(service.measures, service.runner.StateDirectory(), uint32(table.running()), table.largestRun)
		child, err := table.scope.Child(sessionScopePrefix + id)
		if err != nil {
			return err
		}
		if err := child.Go(func(ctx context.Context) error {
			defer child.Cancel()
			service.own(ctx, record, recipe)
			return nil
		}); err != nil {
			return errors.Join(err, child.Close())
		}
		record.scope = child
		table.sessions[id] = record
		table.order = append(table.order, id)
		return nil
	}); err != nil {
		record.requestCancel(err)
		return nil, err
	}
	service.logger.Info("harness: launch check", "assignment", id, "admitted", check.GetAdmitted(), "report_only", check.GetReportOnly(),
		"load", check.GetLoadOneMinute(), "cores", check.GetCores(), "free_bytes", check.GetFreeBytes(), "worker_cap", check.GetWorkerCap(),
		"disk_floor_bytes", check.GetDiskFloorBytes(), "findings", check.GetFindings())
	select {
	case <-record.opened:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	if record.openErr != nil {
		return nil, record.openErr
	}
	state, err := service.Get(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: id})
	if err != nil {
		return nil, err
	}
	return &harnessv1.SubmitAgentSessionResponse{Receipt: proto.Clone(record.receipt).(*pb.AgentAssignmentReceipt), Session: state.GetSession(), Check: check}, nil
}

// HoldAdmission refuses every Submit, with reason, until ReleaseAdmission.
// Sessions already running are not affected.
func (service *AgentSessionService) HoldAdmission(reason string) {
	_ = service.registry.Submit(func(table *registry) bool {
		table.held = reason
		return false
	})
}

// ReleaseAdmission admits sessions again.
func (service *AgentSessionService) ReleaseAdmission() {
	_ = service.registry.Submit(func(table *registry) bool {
		table.held = ""
		return false
	})
}

// running counts the sessions that have not ended.
func (table *registry) running() int {
	count := 0
	for _, record := range table.sessions {
		if !finished(record.state.GetPhase()) {
			count++
		}
	}
	return count
}

func finished(phase harnessv1.AgentSessionPhase) bool {
	switch phase {
	case harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED,
		harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED,
		harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CLOSED:
		return true
	}
	return false
}

// Send queues message for the session's next turn and acknowledges it with
// the turn's identifier.
func (service *AgentSessionService) Send(ctx context.Context, request *harnessv1.SendAgentSessionMessageRequest) (*harnessv1.SendAgentSessionMessageResponse, error) {
	if err := harnessv1.ValidateSendAgentSessionMessageRequest(request); err != nil {
		return nil, fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	response := &harnessv1.SendAgentSessionMessageResponse{}
	err := service.command(ctx, func(table *registry) error {
		record, err := table.lookup(request.GetAssignmentId())
		if err != nil {
			return err
		}
		if finished(record.state.GetPhase()) || record.cancel {
			return fmt.Errorf("%w: %s", ErrSessionFinished, request.GetAssignmentId())
		}
		turn := record.state.GetTurns() + uint32(len(record.queue)) + 1
		record.queue = append(record.queue, queuedMessage{text: request.GetMessage(), turn: turn})
		record.state.Queued = uint32(len(record.queue))
		service.persistQueue(record)
		record.state.UpdatedAt = timestamppb.New(service.clock.Now())
		record.poke()
		response.Session = proto.Clone(record.state).(*harnessv1.AgentSessionState)
		response.TurnId = fmt.Sprint(turn)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// List reports every session in submission order and the process they run in.
func (service *AgentSessionService) List(ctx context.Context, _ *harnessv1.ListAgentSessionsRequest) (*harnessv1.ListAgentSessionsResponse, error) {
	response := &harnessv1.ListAgentSessionsResponse{HostPid: int32(service.hostPID)}
	err := service.command(ctx, func(table *registry) error {
		for _, id := range table.order {
			response.Sessions = append(response.Sessions, proto.Clone(table.sessions[id].state).(*harnessv1.AgentSessionState))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// Get reports one session.
func (service *AgentSessionService) Get(ctx context.Context, request *harnessv1.GetAgentSessionRequest) (*harnessv1.GetAgentSessionResponse, error) {
	if err := harnessv1.ValidateGetAgentSessionRequest(request); err != nil {
		return nil, fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	response := &harnessv1.GetAgentSessionResponse{}
	err := service.command(ctx, func(table *registry) error {
		record, err := table.lookup(request.GetAssignmentId())
		if err != nil {
			return err
		}
		response.Session = proto.Clone(record.state).(*harnessv1.AgentSessionState)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// Cancel asks the session's owner to stop at its next safepoint. Between
// turns the owner stops at once; during a turn the executor is interrupted
// and the owner stops when the turn reports its result, or kills the executor
// after the interrupt budget. A finished session is reported as it is.
func (service *AgentSessionService) Cancel(ctx context.Context, request *harnessv1.CancelAgentSessionRequest) (*harnessv1.CancelAgentSessionResponse, error) {
	if err := harnessv1.ValidateCancelAgentSessionRequest(request); err != nil {
		return nil, fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	response := &harnessv1.CancelAgentSessionResponse{}
	changed := false
	err := service.command(ctx, func(table *registry) error {
		record, err := table.lookup(request.GetAssignmentId())
		if err != nil {
			return err
		}
		if !finished(record.state.GetPhase()) && !record.cancel {
			record.cancel = true
			record.state.Phase = harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELING
			record.state.UpdatedAt = timestamppb.New(service.clock.Now())
			record.requestCancel(context.Canceled)
			record.poke()
			changed = true
		}
		response.Session = proto.Clone(record.state).(*harnessv1.AgentSessionState)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if changed {
		service.observe(response.GetSession())
	}
	return response, nil
}

// observe hands one state copy to every observer.
func (service *AgentSessionService) observe(state *harnessv1.AgentSessionState) {
	for _, observer := range service.observers {
		observer(proto.Clone(state).(*harnessv1.AgentSessionState))
	}
}

// Stop asks the host to shut down and reports how many sessions were running.
func (service *AgentSessionService) Stop(ctx context.Context, _ *harnessv1.StopHarnessRequest) (*harnessv1.StopHarnessResponse, error) {
	if service.stop == nil {
		return nil, fmt.Errorf("%w: this host grants no stop request", csf.ErrInvalidRequest)
	}
	response := &harnessv1.StopHarnessResponse{}
	err := service.command(ctx, func(table *registry) error {
		response.SessionsRunning = uint32(table.running())
		return nil
	})
	if err != nil {
		return nil, err
	}
	service.stop()
	return response, nil
}

func (table *registry) lookup(id string) (*sessionRecord, error) {
	record, exists := table.sessions[id]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSession, id)
	}
	return record, nil
}

// poke wakes the owner without blocking: one pending wake is enough.
func (record *sessionRecord) poke() {
	select {
	case record.wake <- struct{}{}:
	default:
	}
}

// own is one session's owner goroutine, on the session's child scope. It
// opens the session, runs turns from the queue and stops at a safepoint when
// canceled or when its scope ends.
func (service *AgentSessionService) own(ctx context.Context, record *sessionRecord, recipe *pb.AgentAssignmentRecipe) {
	id := recipe.GetAssignmentId()
	runDirectory := session.RunDirectory(service.runner.StateDirectory(), id)
	outputBase := filepath.Join(runDirectory, BazelOutputDirectory)
	ocamlToolchainCache := filepath.Join(service.runner.StateDirectory(), OCamlToolchainCacheDirectory)
	openRun := service.runner.Open
	if record.resume {
		openRun = service.runner.Reopen
	}
	open, err := openRun(ctx, recipe,
		bazelDiskCacheVariable+"="+service.bazelDiskCache(),
		bazelOutputVariable+"="+outputBase,
		ocamlToolchainCacheVariable+"="+ocamlToolchainCache)
	if err != nil {
		record.openErr = err
		close(record.opened)
		service.finish(record, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED, err)
		return
	}
	state := open.State()
	record.receipt = &pb.AgentAssignmentReceipt{Plan: open.Plan(), SessionId: state.SessionID, WorktreeId: state.Worktree, Branch: state.Branch, TraceId: state.TraceID}
	record.open = open
	service.transition(record, func(current *harnessv1.AgentSessionState) {
		current.TraceId, current.Branch, current.Worktree = state.TraceID, state.Branch, state.Worktree
		current.Phase = harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN
	})
	pending := []string{}
	if state.Turns == 0 {
		pending = append(pending, open.FirstPrompt())
	}
	if record.resume {
		pending = append(pending, restoredQueue(runDirectory)...)
	}
	_ = service.command(context.WithoutCancel(ctx), func(table *registry) error {
		restored := []queuedMessage{}
		for index, text := range pending {
			restored = append(restored, queuedMessage{text: text, turn: uint32(state.Turns + index + 1)})
		}
		record.queue = append(restored, record.queue...)
		record.state.Queued = uint32(len(record.queue))
		record.state.Turns = uint32(state.Turns)
		service.persistQueue(record)
		return nil
	})
	close(record.opened)
	phase, err := service.turns(ctx, record, open)
	closeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.closeBudget)
	defer cancel()
	if closeErr := open.Close(closeContext); closeErr != nil {
		service.logger.Warn("harness: session closed with error", "assignment", id, "error", closeErr)
	}
	if removeErr := os.RemoveAll(outputBase); removeErr != nil {
		service.logger.Warn("harness: Bazel output base not removed", "assignment", id, "error", removeErr)
	}
	if size, measureErr := service.measures.DirectoryBytes(runDirectory); measureErr == nil {
		service.recordLargestRun(size)
	}
	service.finish(record, phase, err)
}

// turns runs the queue until a safepoint ends the session: a cancel observed
// between turns, the scope ending, or the executor failing. It returns the
// phase the session ends in.
func (service *AgentSessionService) turns(ctx context.Context, record *sessionRecord, open *session.OpenSession) (harnessv1.AgentSessionPhase, error) {
	for {
		next, stop := service.dequeue(ctx, record)
		switch {
		case ctx.Err() != nil:
			return harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CLOSED, nil
		case stop:
			return harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED, nil
		case next == nil:
			select {
			case <-record.wake:
				continue
			case <-ctx.Done():
				return harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CLOSED, nil
			}
		}
		service.transition(record, func(current *harnessv1.AgentSessionState) {
			// The turn counts from its start, so a Send during it is
			// acknowledged as the turn after this one.
			current.Turns++
			if current.Phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN {
				current.Phase = harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING
			}
		})
		receipt, err := service.turn(ctx, record, open, next.text)
		if ctx.Err() == nil {
			_ = service.command(context.WithoutCancel(ctx), func(_ *registry) error {
				record.inflight = ""
				service.persistQueue(record)
				return nil
			})
		}
		service.transition(record, func(current *harnessv1.AgentSessionState) {
			current.Turns = uint32(open.State().Turns)
			if url := receipt.GetPullRequestUrl(); url != "" {
				current.PullRequestUrl = url
			}
			if current.Phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING {
				current.Phase = harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN
			}
		})
		switch {
		case err == nil, errors.Is(err, session.ErrTurnFailed) && !executorGone(err):
			// A turn that failed on its own terms leaves the session open for
			// the next message; the record carries the failure.
			continue
		case ctx.Err() != nil:
			return harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CLOSED, nil
		case record.cancelRequested.Err() != nil:
			return harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED, nil
		default:
			return harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED, err
		}
	}
}

// executorGone reports a turn error after which no further turn can run on
// the open executor.
func executorGone(err error) bool {
	return errors.Is(err, claudecode.ErrSessionClosed) || errors.Is(err, claudecode.ErrTurnAbandoned) || errors.Is(err, claudecode.ErrTurnIncomplete)
}

// turn runs one turn and delivers a cancel that arrives during it: the
// executor is interrupted, and killed if it reaches no safepoint within the
// interrupt budget.
func (service *AgentSessionService) turn(ctx context.Context, record *sessionRecord, open *session.OpenSession, prompt string) (*pb.AgentAssignmentReceipt, error) {
	turnContext, endTurn := context.WithCancelCause(ctx)
	defer endTurn(nil)
	stopWatching := context.AfterFunc(record.cancelRequested, func() {
		if err := open.Interrupt(turnContext); err != nil {
			service.logger.Warn("harness: interrupt not delivered", "assignment", record.state.GetAssignmentId(), "error", err)
		}
		// The kill fires on the turn's context; after the turn ends it cancels
		// a context nothing reads any more.
		service.clock.AfterFunc(service.interruptBudget, func() { endTurn(ErrInterruptBudget) })
	})
	defer stopWatching()
	return open.Turn(turnContext, prompt)
}

// dequeue takes the next message at a safepoint, or reports that the session
// was canceled.
func (service *AgentSessionService) dequeue(ctx context.Context, record *sessionRecord) (*queuedMessage, bool) {
	var next *queuedMessage
	stop := false
	if err := service.command(context.WithoutCancel(ctx), func(table *registry) error {
		if record.cancel {
			stop = true
			return nil
		}
		if len(record.queue) == 0 {
			return nil
		}
		message := record.queue[0]
		record.queue = record.queue[1:]
		record.state.Queued = uint32(len(record.queue))
		record.inflight = message.text
		service.persistQueue(record)
		next = &message
		return nil
	}); err != nil {
		// The registry has retired: the service is stopping.
		return nil, true
	}
	return next, stop
}

// transition applies one change to the session's state on the registry and
// reports the result to the observers.
func (service *AgentSessionService) transition(record *sessionRecord, change func(state *harnessv1.AgentSessionState)) {
	var changed *harnessv1.AgentSessionState
	if err := service.command(context.Background(), func(_ *registry) error {
		change(record.state)
		record.state.UpdatedAt = timestamppb.New(service.clock.Now())
		changed = proto.Clone(record.state).(*harnessv1.AgentSessionState)
		return nil
	}); err != nil {
		return
	}
	service.observe(changed)
}

// finish records the session's end.
func (service *AgentSessionService) finish(record *sessionRecord, phase harnessv1.AgentSessionPhase, err error) {
	service.transition(record, func(state *harnessv1.AgentSessionState) {
		state.Phase = phase
		state.Queued = 0
		if err != nil {
			state.Error = err.Error()
		}
	})
	record.requestCancel(context.Canceled)
	if phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED || phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED {
		if writeErr := os.WriteFile(filepath.Join(record.directory, EndedFile), []byte(phase.String()+"\n"), stateFileMode); writeErr != nil {
			service.logger.Warn("harness: end not recorded", "assignment", record.state.GetAssignmentId(), "error", writeErr)
		}
	}
	service.logger.Info("harness: session finished", "assignment", record.state.GetAssignmentId(), "phase", phase.String(), "error", err)
}
