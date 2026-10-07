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
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/candacelabs/csf/csf"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/atomicfile"
	"github.com/candacelabs/csf/pkg/mailbox"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/harness/routing"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/verify"
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
// ends.
const (
	// BazelDiskCacheVariable, BazelOutputVariable and
	// OCamlToolchainCacheVariable name the three caches in the environment
	// of every program the harness, or a service beside it, runs Bazel in.
	BazelDiskCacheVariable      = "CANDACE_BAZEL_DISK_CACHE"
	BazelOutputVariable         = "CANDACE_BAZEL_CACHE"
	OCamlToolchainCacheVariable = "CANDACE_OCAML_TOOLCHAIN_CACHE"
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

// WithHostPID names the process every session runs in, for List and for
// finding each session's executor among its children.
func WithHostPID(pid int) AgentSessionServiceOption {
	return func(service *AgentSessionService) error {
		service.hostPID = pid
		return nil
	}
}

// WithProcessTable grants the host's process table, as /proc. With it the
// service closes idle executors, never one with a live background child, and
// samples the resident memory of the harness and its executors into the
// resident series. Without it executors stay open and nothing is sampled.
func WithProcessTable(processes iofs.IFiles) AgentSessionServiceOption {
	return func(service *AgentSessionService) error {
		if processes == nil {
			return fmt.Errorf("%w: nil process table", ErrInvalidServiceOption)
		}
		service.processes = processes
		return nil
	}
}

// WithResidentSampleInterval sets how often the resident series is sampled.
func WithResidentSampleInterval(interval time.Duration) AgentSessionServiceOption {
	return func(service *AgentSessionService) error {
		if interval <= 0 {
			return fmt.Errorf("%w: resident sample interval must be positive", ErrInvalidServiceOption)
		}
		service.residentInterval = interval
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
	launcher        proc.ILauncher
	registry        *mailbox.Mailbox[registry]
	// processes is the process table, when granted; idleBound is derived at
	// Start from the state directory's run records.
	processes        iofs.IFiles
	residentInterval time.Duration
	idleBound        IdleBoundReport
	// executorDefault is the default the host starts with when no switch has
	// been recorded under the state directory.
	executorDefault *harnessv1.AgentExecutorDefault
	// resumed is closed once Start's first pass over the open runs is done.
	resumed chan struct{}
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
	// resumes counts sessions resumed after a suspend, and the latest resumed
	// turn's time to first token is kept for the resident series.
	resumes                  int
	resumeTimeToFirstTokenMs float64
	// executorDefault is what a recipe naming no executor runs on.
	executorDefault *harnessv1.AgentExecutorDefault
}

// sessionRecord is one session as the registry sees it. The owner goroutine
// mutates it only through registry commands; the few fields it writes
// directly are published before opened closes and read after.
type sessionRecord struct {
	state   *harnessv1.AgentSessionState
	receipt *pb.AgentAssignmentReceipt
	queue   []queuedMessage
	// nextSeq is the next sequence number to assign to a queued message.
	nextSeq uint64
	// wake is poked when the queue or the cancel flag changes; idle when the
	// idle bound has passed with the session between turns, carrying the
	// generation of the wait that armed it so a late timer is ignored.
	wake           chan struct{}
	idle           chan uint64
	idleGeneration uint64
	// idleSince is when the session was suspended; zero while its executor is open.
	idleSince time.Time
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
	// Activity counters: updated as events are recorded.
	lastEventAt   time.Time
	eventsLast10m uint32
	lastCommand   string
	lastTool      string
	// deliveredMessages maps receipt IDs to their delivery times for read receipts.
	deliveredMessages map[string]time.Time
	// runningTurnMessage is the message currently running, or nil between turns.
	// Set by turns() when starting a turn, cleared when turn completes.
	runningTurnMessage *queuedMessage
	// interruptSignal is sent to when an interrupt-class message arrives during a running turn.
	interruptSignal chan *queuedMessage
	// directory is the run directory; resume is set when a previous harness
	// process left the run open there, so it is reopened, not created.
	directory string
	resume    bool
	// inflight is the message of the turn running now, persisted with the
	// queue so a restart mid-turn delivers it again; nil between turns.
	inflight *queuedMessage
}

// persistedQueue is the queue as the run directory keeps it across restarts.
type persistedQueue struct {
	Inflight *persistedMessage  `json:"inflight,omitempty"`
	Queued   []persistedMessage `json:"queued"`
}

// persistedMessage is one queued message as the run directory keeps it. A
// queue written before messages carried an author holds bare strings; one
// reads back as a message that is not the operator's.
type persistedMessage struct {
	Text     string `json:"text"`
	Operator bool   `json:"operator,omitempty"`
}

func (message *persistedMessage) UnmarshalJSON(data []byte) error {
	var text string
	if json.Unmarshal(data, &text) == nil {
		*message = persistedMessage{Text: text}
		return nil
	}
	type plain persistedMessage
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*message = persistedMessage(decoded)
	return nil
}

func persisted(message *queuedMessage) *persistedMessage {
	if message == nil {
		return nil
	}
	return &persistedMessage{Text: message.text, Operator: message.operator}
}

// persistQueue writes the record's queue to its run directory. It runs on the
// registry, where the queue is owned.
func (service *AgentSessionService) persistQueue(record *sessionRecord) {
	saved := persistedQueue{Inflight: persisted(record.inflight), Queued: []persistedMessage{}}
	for _, message := range record.queue {
		saved.Queued = append(saved.Queued, *persisted(&message))
	}
	content, err := json.Marshal(saved)
	if err == nil {
		path := filepath.Join(record.directory, QueueFile)
		err = atomicfile.WriteFile(path, content, stateFileMode)
	}
	if err != nil {
		service.logger.Warn("harness: queue not persisted", "assignment", record.state.GetAssignmentId(), "error", err)
	}
}

// restoredQueue is the queue a previous process persisted, with the turn it
// was running delivered again first. The turns are the caller's to number.
func restoredQueue(directory string) []queuedMessage {
	content, err := os.ReadFile(filepath.Join(directory, QueueFile))
	if err != nil {
		return nil
	}
	var saved persistedQueue
	if json.Unmarshal(content, &saved) != nil {
		return nil
	}
	messages := []queuedMessage{}
	if saved.Inflight != nil && saved.Inflight.Text != "" {
		messages = append(messages, queuedMessage{text: RedeliveredPrefix + saved.Inflight.Text, operator: saved.Inflight.Operator})
	}
	for _, message := range saved.Queued {
		messages = append(messages, queuedMessage{text: message.Text, operator: message.Operator})
	}
	return messages
}

// queuedMessage is one message waiting for its turn: its text, the turn it
// was acknowledged as, and whether it is the operator's own words.
type queuedMessage struct {
	text          string
	turn          uint32
	receipt       string
	priorityClass harnessv1.MessagePriorityClass
	operator      bool
	sequence      uint64
	timestamp     *timestamppb.Timestamp
}

// turnOptions are the session turn options the message carries.
func (message *queuedMessage) turnOptions() []session.TurnOption {
	if message.operator {
		return []session.TurnOption{session.OperatorAuthored()}
	}
	return nil
}

// NewAgentSessionService validates the whole option set before building the
// service. [WithSessionRunner] is required.
func NewAgentSessionService(options ...AgentSessionServiceOption) (*AgentSessionService, error) {
	service := &AgentSessionService{
		measures:         HostMeasures{},
		clock:            SystemClock{},
		logger:           slog.New(slog.DiscardHandler),
		interruptBudget:  DefaultInterruptBudget,
		closeBudget:      DefaultCloseBudget,
		residentInterval: DefaultResidentSampleInterval,
		registry:         mailbox.New[registry](),
		executorDefault:  &harnessv1.AgentExecutorDefault{Executor: string(session.ExecutorClaudeCode)},
		resumed:          make(chan struct{}),
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
// the disk floor and the idle bound, and starts the registry owner on the
// scope. The registry retires when the scope is canceled; every session's
// child scope is canceled with it and joined by it. With a process table
// granted, the resident series sampler runs on the scope too.
func (service *AgentSessionService) Start(scope *runtime.Scope) error {
	if err := os.MkdirAll(service.bazelDiskCache(), stateDirectoryMode); err != nil {
		return fmt.Errorf("harness: create the Bazel disk cache: %w", err)
	}
	if err := service.seedModelPolicy(); err != nil {
		return err
	}
	largest, err := measureRuns(scope.Context(), service.measures, service.runner.StateDirectory())
	if err != nil {
		service.logger.Warn("harness: runs not measured", "error", err)
	}
	service.idleBound = service.deriveIdleBound()
	table := &registry{scope: scope, sessions: map[string]*sessionRecord{}, largestRun: largest, executorDefault: service.recordedExecutorDefault()}
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
	if service.processes != nil {
		if err := scope.GoOwner(residentOwner, service.sampleResident); err != nil {
			return err
		}
	}
	return scope.Go(service.resumeOpenRuns)
}

// deriveIdleBound measures the gaps between turns in the state directory's
// run records and derives the idle bound from them; the prompt cache lifetime
// is the fallback before two gaps exist. The derivation is logged beside the
// bound.
func (service *AgentSessionService) deriveIdleBound() IdleBoundReport {
	gaps, err := TurnGaps(service.runner.StateDirectory())
	if err != nil {
		service.logger.Warn("harness: turn gaps not measured", "error", err)
	}
	report := IdleBound(gaps, routing.DefaultTTL)
	service.logger.Info("harness: idle bound derived", "derivation", report.String(), "bound", report.Bound, "quantile", report.Quantile, "gaps", report.Gaps, "fallback", report.Fallback)
	return report
}

// IdleBound is the derived idle bound with its derivation, after Start.
func (service *AgentSessionService) IdleBound() IdleBoundReport { return service.idleBound }

// Resumed is closed once Start's first resume pass has run: every open run a previous process
// left is resumed or recorded as held in ResumeQueueFile, so a host that waits on it is ready only with them back.
func (service *AgentSessionService) Resumed() <-chan struct{} { return service.resumed }

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
	return service.submit(ctx, request, false)
}

// submit is Submit; a resume after a restart is held to the launch check,
// which only reports on a new launch: a resume it does not admit is refused
// with ErrResumeHeld and nothing is recorded.
func (service *AgentSessionService) submit(ctx context.Context, request *harnessv1.SubmitAgentSessionRequest, resuming bool) (*harnessv1.SubmitAgentSessionResponse, error) {
	// A new run whose recipe names no executor takes the host's default; a
	// run already recorded reopens on what it recorded.
	recipe := request.GetRecipe()
	if recipe == nil {
		return nil, fmt.Errorf("%w: a recipe is required", csf.ErrInvalidRequest)
	}
	id := recipe.GetAssignmentId()
	directory := session.RunDirectory(service.runner.StateDirectory(), id)
	_, recorded := session.ReadRunState(directory)
	if recorded != nil && recipe.GetExecutor() == "" {
		resolved, err := service.withExecutorDefault(ctx, recipe)
		if err != nil {
			return nil, err
		}
		recipe = resolved
	}
	plan, err := csf.PrepareAgentAssignment(recipe)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	// A recorded run is held to the policy too: it never resumes on a model
	// the host no longer allows.
	if err := service.admitModel(plan.GetRecipe().GetModel()); err != nil {
		return nil, err
	}
	record := &sessionRecord{
		state: &harnessv1.AgentSessionState{
			AssignmentId: id,
			AgentId:      recipe.GetAgent().GetId(),
			SessionId:    plan.GetSessionKey(),
			Phase:        harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_STARTING,
			StartedAt:    timestamppb.New(service.clock.Now()),
			UpdatedAt:    timestamppb.New(service.clock.Now()),
		},
		directory:         directory,
		resume:            recorded == nil,
		wake:              make(chan struct{}, 1),
		idle:              make(chan uint64, 1),
		opened:            make(chan struct{}),
		deliveredMessages: make(map[string]time.Time),
		interruptSignal:   make(chan *queuedMessage, 1),
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
		// A launch the host cannot fit is refused even while the check is
		// report-only: no report makes room for it. The refusal is skipped
		// when the memory could not be read, which is a finding, not a bound.
		if !check.GetMemoryFits() && check.GetMemoryAvailableBytes() > 0 {
			return fmt.Errorf("%w: %s", ErrAdmissionHeld, strings.Join(check.GetFindings(), "; "))
		}
		if resuming {
			check.ReportOnly = false
			if !check.GetAdmitted() {
				return fmt.Errorf("%w: %s", ErrResumeHeld, strings.Join(check.GetFindings(), "; "))
			}
		}
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

// Check is the launch check a Submit made now would get, without submitting:
// the worker cap from the cores and the one-minute load, and the disk floor
// from the largest run directory measured so far. While admission is held it
// reports ErrAdmissionHeld with the reason instead.
func (service *AgentSessionService) Check(ctx context.Context) (*harnessv1.LaunchCheck, error) {
	var check *harnessv1.LaunchCheck
	if err := service.command(ctx, func(table *registry) error {
		if table.held != "" {
			return fmt.Errorf("%w: %s", ErrAdmissionHeld, table.held)
		}
		check = launchCheck(service.measures, service.runner.StateDirectory(), uint32(table.running()), table.largestRun)
		return nil
	}); err != nil {
		return nil, err
	}
	return check, nil
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
// a receipt containing the message state.
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
		receipt := uuid.New().String()
		now := timestamppb.New(service.clock.Now())
		message := queuedMessage{
			text:          request.GetMessage(),
			turn:          turn,
			receipt:       receipt,
			priorityClass: request.GetPriorityClass(),
			operator:      request.GetOperatorAuthored(),
			sequence:      record.nextSeq,
			timestamp:     now,
		}
		record.nextSeq++
		// Interrupt classes (PREEMPT, INTERRUPT) are inserted at head of their class;
		// QUEUE class appended normally. Within a class, sequence order is stable.
		if request.GetPriorityClass() == harnessv1.MessagePriorityClass_MESSAGE_PRIORITY_CLASS_PREEMPT ||
			request.GetPriorityClass() == harnessv1.MessagePriorityClass_MESSAGE_PRIORITY_CLASS_INTERRUPT {
			// Find insertion point: after last message of the same class
			index := 0
			for i := 0; i < len(record.queue); i++ {
				if record.queue[i].priorityClass == request.GetPriorityClass() {
					index = i + 1
				}
			}
			record.queue = append(record.queue[:index], append([]queuedMessage{message}, record.queue[index:]...)...)
		} else {
			record.queue = append(record.queue, message)
		}
		response.TurnId = fmt.Sprint(turn)
		service.persistQueue(record)
		record.state.Queued = uint32(len(record.queue))
		record.state.UpdatedAt = now
		record.poke()

		// If this is an interrupt-class message and a turn is running, signal the interrupt
		if (request.GetPriorityClass() == harnessv1.MessagePriorityClass_MESSAGE_PRIORITY_CLASS_INTERRUPT ||
			request.GetPriorityClass() == harnessv1.MessagePriorityClass_MESSAGE_PRIORITY_CLASS_PREEMPT) &&
			record.runningTurnMessage != nil {
			select {
			case record.interruptSignal <- &message:
			default:
				// Already signaled, ignore
			}
		}

		response.Session = proto.Clone(record.state).(*harnessv1.AgentSessionState)
		response.Receipt = &harnessv1.InboxMessage{
			ReceiptId:     receipt,
			Text:          request.GetMessage(),
			State:         harnessv1.MessageState_MESSAGE_STATE_QUEUED,
			SentAt:        now,
			PriorityClass: request.GetPriorityClass(),
			Sequence:      record.nextSeq - 1,
		}
		return nil
	})
	service.recordAction(ctx, request.GetAssignmentId(), session.ActionSend, err,
		slog.Bool(session.KeyOperatorAuthored, request.GetOperatorAuthored()), slog.String(session.KeyTurnID, response.GetTurnId()),
		slog.Bool(session.KeyQuestionWanted, request.GetQuestionWanted()))
	if err != nil {
		return nil, err
	}
	return response, nil
}

// Propose applies a proposed patch to a session's worktree.
func (service *AgentSessionService) Propose(ctx context.Context, request *harnessv1.ProposeProposalRequest) (*harnessv1.ProposeProposalResponse, error) {
	if err := harnessv1.ValidateProposal(request.GetProposal()); err != nil {
		return nil, fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	proposal := request.GetProposal()
	response := &harnessv1.ProposeProposalResponse{}
	err := service.command(ctx, func(table *registry) error {
		record, err := table.lookup(proposal.GetAssignmentId())
		if err != nil {
			return err
		}
		// Wait for the session to be opened so we have the worktree path.
		select {
		case <-record.opened:
			if record.openErr != nil {
				response.Rejection = &harnessv1.Rejection{
					Code:    harnessv1.Rejection_CODE_APPLY_FAILED,
					Details: "session failed to open",
				}
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
		// Check if the workspace is in patch mode.
		workspace := record.receipt.GetPlan().GetRecipe().GetWorkspace()
		if workspace.GetMode() != "patch" {
			response.Rejection = &harnessv1.Rejection{
				Code:    harnessv1.Rejection_CODE_MODE_UNSUPPORTED,
				Details: "workspace mode is not 'patch'",
			}
			return nil
		}
		// Apply the patch using the verifier.
		verifier := verify.NewPatchVerifier(record.open.State().Worktree, service.launcher)
		rejection := verifier.Apply(ctx, proposal)
		if rejection != nil {
			response.Rejection = rejection
			return nil
		}
		// Commit the changes.
		if err := verifier.Commit(ctx, proposal.GetMessage()); err != nil {
			response.Rejection = &harnessv1.Rejection{
				Code:    harnessv1.Rejection_CODE_APPLY_FAILED,
				Details: fmt.Sprintf("failed to commit: %v", err),
			}
			return nil
		}
		// Return the updated session state.
		record.state.UpdatedAt = timestamppb.New(service.clock.Now())
		response.Session = proto.Clone(record.state).(*harnessv1.AgentSessionState)
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

// ListInbox reports every message in a session's inbox with its state.
func (service *AgentSessionService) ListInbox(ctx context.Context, request *harnessv1.ListInboxRequest) (*harnessv1.ListInboxResponse, error) {
	if err := harnessv1.ValidateListInboxRequest(request); err != nil {
		return nil, fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	response := &harnessv1.ListInboxResponse{}
	err := service.command(ctx, func(table *registry) error {
		record, err := table.lookup(request.GetAssignmentId())
		if err != nil {
			return err
		}
		for _, queued := range record.queue {
			response.Messages = append(response.Messages, &harnessv1.InboxMessage{
				ReceiptId:     queued.receipt,
				Text:          queued.text,
				State:         harnessv1.MessageState_MESSAGE_STATE_QUEUED,
				SentAt:        queued.timestamp,
				PriorityClass: queued.priorityClass,
				Sequence:      queued.sequence,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// MoveInboxMessage reorders a message to a specific index in the queue.
func (service *AgentSessionService) MoveInboxMessage(ctx context.Context, request *harnessv1.MoveInboxMessageRequest) (*harnessv1.MoveInboxMessageResponse, error) {
	if request == nil || request.GetAssignmentId() == "" || request.GetReceiptId() == "" {
		return nil, fmt.Errorf("%w: invalid request", csf.ErrInvalidRequest)
	}
	response := &harnessv1.MoveInboxMessageResponse{}
	err := service.command(ctx, func(table *registry) error {
		record, err := table.lookup(request.GetAssignmentId())
		if err != nil {
			return err
		}
		index := -1
		for i, msg := range record.queue {
			if msg.receipt == request.GetReceiptId() {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("%w: receipt %s not found", csf.ErrNotFound, request.GetReceiptId())
		}
		msg := record.queue[index]
		record.queue = append(record.queue[:index], record.queue[index+1:]...)
		targetIndex := int(request.GetIndex())
		if targetIndex > len(record.queue) {
			targetIndex = len(record.queue)
		}
		record.queue = append(record.queue[:targetIndex], append([]queuedMessage{msg}, record.queue[targetIndex:]...)...)
		record.state.UpdatedAt = timestamppb.New(service.clock.Now())
		record.poke()
		for _, queued := range record.queue {
			response.Messages = append(response.Messages, &harnessv1.InboxMessage{
				ReceiptId:     queued.receipt,
				Text:          queued.text,
				State:         harnessv1.MessageState_MESSAGE_STATE_QUEUED,
				SentAt:        queued.timestamp,
				PriorityClass: queued.priorityClass,
				Sequence:      queued.sequence,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// TopInboxMessage promotes a message to the front of the queue.
func (service *AgentSessionService) TopInboxMessage(ctx context.Context, request *harnessv1.TopInboxMessageRequest) (*harnessv1.TopInboxMessageResponse, error) {
	if request == nil || request.GetAssignmentId() == "" || request.GetReceiptId() == "" {
		return nil, fmt.Errorf("%w: invalid request", csf.ErrInvalidRequest)
	}
	response := &harnessv1.TopInboxMessageResponse{}
	err := service.command(ctx, func(table *registry) error {
		record, err := table.lookup(request.GetAssignmentId())
		if err != nil {
			return err
		}
		index := -1
		for i, msg := range record.queue {
			if msg.receipt == request.GetReceiptId() {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("%w: receipt %s not found", csf.ErrNotFound, request.GetReceiptId())
		}
		if index > 0 {
			msg := record.queue[index]
			record.queue = append(record.queue[:index], record.queue[index+1:]...)
			record.queue = append([]queuedMessage{msg}, record.queue...)
			record.state.UpdatedAt = timestamppb.New(service.clock.Now())
			record.poke()
		}
		for _, queued := range record.queue {
			response.Messages = append(response.Messages, &harnessv1.InboxMessage{
				ReceiptId:     queued.receipt,
				Text:          queued.text,
				State:         harnessv1.MessageState_MESSAGE_STATE_QUEUED,
				SentAt:        queued.timestamp,
				PriorityClass: queued.priorityClass,
				Sequence:      queued.sequence,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// DropInboxMessage removes a message from the queue.
func (service *AgentSessionService) DropInboxMessage(ctx context.Context, request *harnessv1.DropInboxMessageRequest) (*harnessv1.DropInboxMessageResponse, error) {
	if request == nil || request.GetAssignmentId() == "" || request.GetReceiptId() == "" {
		return nil, fmt.Errorf("%w: invalid request", csf.ErrInvalidRequest)
	}
	response := &harnessv1.DropInboxMessageResponse{}
	err := service.command(ctx, func(table *registry) error {
		record, err := table.lookup(request.GetAssignmentId())
		if err != nil {
			return err
		}
		index := -1
		for i, msg := range record.queue {
			if msg.receipt == request.GetReceiptId() {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("%w: receipt %s not found", csf.ErrNotFound, request.GetReceiptId())
		}
		record.queue = append(record.queue[:index], record.queue[index+1:]...)
		record.state.Queued = uint32(len(record.queue))
		record.state.UpdatedAt = timestamppb.New(service.clock.Now())
		record.poke()
		for _, queued := range record.queue {
			response.Messages = append(response.Messages, &harnessv1.InboxMessage{
				ReceiptId:     queued.receipt,
				Text:          queued.text,
				State:         harnessv1.MessageState_MESSAGE_STATE_QUEUED,
				SentAt:        queued.timestamp,
				PriorityClass: queued.priorityClass,
				Sequence:      queued.sequence,
			})
		}
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
	service.recordAction(ctx, request.GetAssignmentId(), session.ActionCancel, err)
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

// pokeIdle tells the owner the idle bound passed for the wait of generation,
// replacing a poke it has not read yet, without blocking.
func (record *sessionRecord) pokeIdle(generation uint64) {
	select {
	case <-record.idle:
	default:
	}
	select {
	case record.idle <- generation:
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
		BazelDiskCacheVariable+"="+service.bazelDiskCache(),
		BazelOutputVariable+"="+outputBase,
		OCamlToolchainCacheVariable+"="+ocamlToolchainCache)
	if err != nil {
		record.openErr = err
		close(record.opened)
		service.finish(record, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED, err)
		return
	}
	state := open.State()
	record.receipt = &pb.AgentAssignmentReceipt{Plan: open.Plan(), SessionId: state.SessionID, WorktreeId: state.Worktree, Branch: state.Branch, TraceId: state.TraceID, Executor: string(state.TurnExecutor())}
	record.open = open
	service.transition(record, func(current *harnessv1.AgentSessionState) {
		current.TraceId, current.Branch, current.Worktree = state.TraceID, state.Branch, state.Worktree
		current.Phase = harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN
	})
	pending := []queuedMessage{}
	if state.Turns == 0 {
		pending = append(pending, queuedMessage{text: open.FirstPrompt()})
	}
	if record.resume {
		pending = append(pending, restoredQueue(runDirectory)...)
	}
	_ = service.command(context.WithoutCancel(ctx), func(table *registry) error {
		restored := []queuedMessage{}
		for index, message := range pending {
			message.turn = uint32(state.Turns + index + 1)
			restored = append(restored, message)
		}
		record.queue = append(restored, record.queue...)
		record.state.Queued = uint32(len(record.queue))
		record.state.Turns = uint32(state.Turns)
		service.persistQueue(record)
		return nil
	})
	// The admission is recorded before the session is reported open, so it
	// precedes the first turn's records in the log.
	service.recordAction(ctx, id, session.ActionSubmit, nil)
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
// phase the session ends in. Between turns the executor is closed once the
// idle bound passes, and resumed for the next message.
func (service *AgentSessionService) turns(ctx context.Context, record *sessionRecord, open *session.OpenSession) (harnessv1.AgentSessionPhase, error) {
	for {
		// A background result already waiting is the turn the executor is
		// running now, so it is counted before a queued message's turn.
		select {
		case notification := <-open.BackgroundResults():
			service.backgroundTurn(ctx, record, open, notification)
			continue
		default:
		}
		next, stop := service.dequeue(ctx, record)
		switch {
		case ctx.Err() != nil:
			return harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CLOSED, nil
		case stop:
			return harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED, nil
		case next == nil:
			if !service.await(ctx, record, open) {
				return harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CLOSED, nil
			}
			continue
		}
		if open.Suspended() {
			if err := service.resume(ctx, record, open); err != nil {
				return harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED, err
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
		receipt, err := service.turn(ctx, record, open, next)
		if ctx.Err() == nil {
			_ = service.command(context.WithoutCancel(ctx), func(table *registry) error {
				record.inflight = nil
				service.persistQueue(record)
				if resumed := receipt.GetResumed(); resumed != nil {
					table.resumeTimeToFirstTokenMs = resumed.GetTimeToFirstTokenMs()
				}
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

// await waits between turns for the next message, the cancel, or the idle
// bound. The bound is armed only with a process table granted and the
// executor open: when it passes with nothing queued, no cancel and no live
// background child, the session is suspended: its executor closes and it
// stays open on its conversation.
// It reports false when the scope ended.
func (service *AgentSessionService) await(ctx context.Context, record *sessionRecord, open *session.OpenSession) bool {
	record.idleGeneration++
	generation := record.idleGeneration
	if service.processes != nil && !open.Suspended() {
		stop := service.clock.AfterFunc(service.idleBound.Bound, func() { record.pokeIdle(generation) })
		defer stop()
	}
	for {
		select {
		case <-record.wake:
			return true
		case notification := <-open.BackgroundResults():
			service.backgroundTurn(ctx, record, open, notification)
			return true
		case fired := <-record.idle:
			if fired != generation {
				continue
			}
			service.suspend(ctx, record, open)
			return true
		case <-ctx.Done():
			return false
		}
	}
}

// suspend ends the executor when the session is still between turns
// with nothing queued and no cancel, and its executor has no live child in
// its process group; otherwise the executor stays and the next wait arms the
// bound again. An executor the process table does not show is kept, since
// its children cannot be seen either.
func (service *AgentSessionService) suspend(ctx context.Context, record *sessionRecord, open *session.OpenSession) {
	id := record.state.GetAssignmentId()
	quiet := false
	_ = service.command(context.WithoutCancel(ctx), func(_ *registry) error {
		quiet = len(record.queue) == 0 && !record.cancel
		return nil
	})
	if !quiet {
		return
	}
	children, found := service.liveChildren(open.State().Worktree)
	switch {
	case !found:
		service.logger.Info("harness: executor kept open: not found in the process table", "assignment", id)
		return
	case children > 0:
		service.logger.Info("harness: executor kept open: live background children", "assignment", id, "children", children)
		return
	}
	closeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.closeBudget)
	defer cancel()
	if err := open.Suspend(closeContext, service.idleBound.Bound); err != nil {
		service.logger.Warn("harness: session suspended with error", "assignment", id, "error", err)
	}
	record.idleSince = service.clock.Now()
	service.transition(record, func(state *harnessv1.AgentSessionState) { state.Suspended = true })
	service.logger.Info("harness: session suspended", "assignment", id, "idle_bound", service.idleBound.Bound)
}

// resume opens a suspended session's executor again on its recorded conversation
// for the next turn, and counts it.
func (service *AgentSessionService) resume(ctx context.Context, record *sessionRecord, open *session.OpenSession) error {
	closed := service.clock.Now().Sub(record.idleSince)
	if err := open.Resume(ctx, closed); err != nil {
		return err
	}
	record.idleSince = time.Time{}
	_ = service.command(context.WithoutCancel(ctx), func(table *registry) error {
		table.resumes++
		return nil
	})
	service.transition(record, func(state *harnessv1.AgentSessionState) { state.Suspended = false })
	service.logger.Info("harness: session resumed", "assignment", record.state.GetAssignmentId(), "suspended", closed)
	return nil
}

// backgroundTurn counts the turn the executor started on a background
// completion and records its typed result; the session's turn count follows.
func (service *AgentSessionService) backgroundTurn(ctx context.Context, record *sessionRecord, open *session.OpenSession, notification claudecode.TaskNotification) {
	if err := open.BackgroundResult(ctx, notification); err != nil {
		service.logger.Warn("harness: background result not recorded", "assignment", record.state.GetAssignmentId(), "error", err)
		return
	}
	service.transition(record, func(current *harnessv1.AgentSessionState) {
		current.Turns = uint32(open.State().Turns)
	})
}

// executorGone reports a turn error after which no further turn can run on
// the open executor.
func executorGone(err error) bool {
	return errors.Is(err, claudecode.ErrSessionClosed) || errors.Is(err, claudecode.ErrTurnAbandoned) || errors.Is(err, claudecode.ErrTurnIncomplete)
}

// turn runs one turn on message and delivers a cancel that arrives during
// it: the executor is interrupted, and killed if it reaches no safepoint
// within the interrupt budget. A priority message arriving during the turn
// interrupts it at a tool boundary, and the turn interrupted is queued again.
func (service *AgentSessionService) turn(ctx context.Context, record *sessionRecord, open *session.OpenSession, message *queuedMessage) (*pb.AgentAssignmentReceipt, error) {
	turnContext, endTurn := context.WithCancelCause(ctx)
	defer endTurn(nil)

	var priorityInterrupt *queuedMessage
	stopWatching := context.AfterFunc(record.cancelRequested, func() {
		if err := open.Interrupt(turnContext); err != nil {
			service.logger.Warn("harness: interrupt not delivered", "assignment", record.state.GetAssignmentId(), "error", err)
		}
		// The kill fires on the turn's context; after the turn ends it cancels
		// a context nothing reads any more.
		service.clock.AfterFunc(service.interruptBudget, func() { endTurn(ErrInterruptBudget) })
	})
	defer stopWatching()

	// Watch for priority interrupts arriving during the turn
	go func() {
		select {
		case msg := <-record.interruptSignal:
			priorityInterrupt = msg
			if err := open.Interrupt(turnContext); err != nil {
				service.logger.Warn("harness: priority interrupt not delivered", "assignment", record.state.GetAssignmentId(), "error", err)
			}
		case <-turnContext.Done():
		}
	}()

	receipt, err := open.Turn(turnContext, message.text, message.turnOptions()...)

	// If a priority message interrupted this turn, re-queue the running message at its class head
	if priorityInterrupt != nil && record.runningTurnMessage != nil {
		service.command(context.WithoutCancel(ctx), func(table *registry) error {
			// Re-insert the interrupted message at the head of its priority class
			class := record.runningTurnMessage.priorityClass
			index := 0
			for i := 0; i < len(record.queue); i++ {
				if record.queue[i].priorityClass == class {
					index = i + 1
					break
				}
			}
			record.queue = append(record.queue[:index], append([]queuedMessage{*record.runningTurnMessage}, record.queue[index:]...)...)
			record.state.Queued = uint32(len(record.queue))
			return nil
		})
	}

	record.runningTurnMessage = nil
	return receipt, err
}

// dequeue takes the next message at a safepoint, checking for interrupt-class
// messages first with non-blocking select. If one arrives while a turn is
// running, the turn is interrupted and the interrupt message runs next.
func (service *AgentSessionService) dequeue(ctx context.Context, record *sessionRecord) (*queuedMessage, bool) {
	var next *queuedMessage
	stop := false
	// Drain interrupt signal non-blocking if a turn just completed
	select {
	case <-record.interruptSignal:
	default:
	}
	if err := service.command(context.WithoutCancel(ctx), func(table *registry) error {
		if record.cancel {
			stop = true
			return nil
		}
		if len(record.queue) == 0 {
			return nil
		}
		// Find the first message by priority class order: PREEMPT, INTERRUPT, QUEUE
		index := -1
		for _, class := range []harnessv1.MessagePriorityClass{
			harnessv1.MessagePriorityClass_MESSAGE_PRIORITY_CLASS_PREEMPT,
			harnessv1.MessagePriorityClass_MESSAGE_PRIORITY_CLASS_INTERRUPT,
			harnessv1.MessagePriorityClass_MESSAGE_PRIORITY_CLASS_QUEUE,
		} {
			for i, msg := range record.queue {
				// A message that names no class (the session's first prompt, a
				// restored message, a Send that set none) is the QUEUE class.
				unnamed := msg.priorityClass == harnessv1.MessagePriorityClass_MESSAGE_PRIORITY_CLASS_UNSPECIFIED
				if msg.priorityClass == class || (unnamed && class == harnessv1.MessagePriorityClass_MESSAGE_PRIORITY_CLASS_QUEUE) {
					index = i
					break
				}
			}
			if index >= 0 {
				break
			}
		}
		if index < 0 {
			return nil
		}
		message := record.queue[index]
		record.queue = append(record.queue[:index], record.queue[index+1:]...)
		record.state.Queued = uint32(len(record.queue))
		record.runningTurnMessage = &message
		record.inflight = &message
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
		now := service.clock.Now()
		record.state.UpdatedAt = timestamppb.New(now)
		record.updateActivityCounters(now)
		changed = proto.Clone(record.state).(*harnessv1.AgentSessionState)
		return nil
	}); err != nil {
		return
	}
	service.observe(changed)
}

// updateActivityCounters refreshes the session's activity metrics.
func (record *sessionRecord) updateActivityCounters(now time.Time) {
	record.lastEventAt = now
	record.state.LastEventAt = timestamppb.New(now)
	record.state.InboxDepth = uint32(len(record.queue))
	record.state.EventsLast_10M = record.eventsLast10m
	record.state.LastCommand = record.lastCommand
	record.state.LastTool = record.lastTool
}

// finish records the session's end.
func (service *AgentSessionService) finish(record *sessionRecord, phase harnessv1.AgentSessionPhase, err error) {
	// The end is on disk before the phase says so, so whoever reads the
	// phase can rely on a restart not reopening the run.
	if phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED || phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED {
		if writeErr := os.WriteFile(filepath.Join(record.directory, EndedFile), []byte(phase.String()+"\n"), stateFileMode); writeErr != nil {
			service.logger.Warn("harness: end not recorded", "assignment", record.state.GetAssignmentId(), "error", writeErr)
		}
	}
	service.transition(record, func(state *harnessv1.AgentSessionState) {
		state.Phase = phase
		state.Queued = 0
		if err != nil {
			state.Error = err.Error()
		}
	})
	record.requestCancel(context.Canceled)
	service.logger.Info("harness: session finished", "assignment", record.state.GetAssignmentId(), "phase", phase.String(), "error", err)
}
