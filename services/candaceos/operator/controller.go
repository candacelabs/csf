// Package operator owns CandaceOS Core's agent turn: policy, approvals, and
// run state.
//
// Controller is the boundary between an agent harness and the rest of Core. It
// owns session and run identity, the fencing rules that decide whether an
// incoming message steers the active run or starts a new one, the approval
// gate in front of every reconciliation, and the operator-visible timeline. An
// agent runtime supplies only the turn lifecycle, behind a deliberately
// unexported interface; applying desired state belongs to the exported
// IReconciler interface. Controller passes owned values across both boundaries
// and retains nothing a caller handed it.
//
// Callers may rely on ErrSessionConflict, ErrRunConflict, and ErrRunNotActive
// distinguishing a stale session from a stale run from a run that cannot be
// steered, on ApprovalQueue resolving each request exactly once — by operator
// decision, timeout, or abort — and on ApprovalActorState naming the actor
// keys written into persisted approval history. Nothing here is durable:
// persistence and receipts belong to store, installed by control.
package operator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/config"
	"github.com/candacelabs/csf/services/candaceos/fleet"
	harnesssdk "github.com/candacelabs/csf/services/candaceos/harness"
)

// TimelineEntry is one operator-visible item in a run: a message, a tool call,
// or a status change. Entries are append-only within a run.
type TimelineEntry struct {
	ID     string    `json:"id"`
	Kind   string    `json:"kind"`
	Role   string    `json:"role,omitempty"`
	Name   string    `json:"name,omitempty"`
	Text   string    `json:"text,omitempty"`
	Detail string    `json:"detail,omitempty"`
	Status string    `json:"status,omitempty"`
	At     time.Time `json:"at"`
}

// RunState is the complete operator-visible state of the current run,
// including its timeline. It is a value copy: mutating it does not affect the
// Controller.
type RunState struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	Title     string          `json:"title"`
	Prompt    string          `json:"prompt,omitempty"`
	Status    string          `json:"status"`
	StartedAt time.Time       `json:"started_at"`
	CanAbort  bool            `json:"can_abort"`
	Entries   []TimelineEntry `json:"entries"`
}

// RunStarted is the durability hook's view of a newly admitted run, delivered
// before the run produces any output.
type RunStarted struct {
	RunID     string
	SessionID string
	Title     string
	Prompt    string
	At        time.Time
}

var (
	// ErrSessionConflict reports that an action targeted a different harness
	// session than the one currently owned by this Controller.
	ErrSessionConflict = errors.New("agent session changed")
	// ErrRunConflict reports that an action targeted an older or otherwise
	// different run in the current session.
	ErrRunConflict = errors.New("agent run changed")
	// ErrRunNotActive reports that a targeted run cannot currently be steered.
	ErrRunNotActive = errors.New("agent run is not active")
)

// reconcileToolInput is the provider JSON-schema adapter. ReconcileIntent is
// the canonical contract after this provider boundary.
type reconcileToolInput struct {
	App           string            `json:"app" jsonschema:"Compose service name to reconcile"`
	Project       string            `json:"project" jsonschema:"stable Docker Compose project name"`
	Path          string            `json:"path" jsonschema:"workspace-relative directory containing the Compose file"`
	DesiredState  string            `json:"desired_state" jsonschema:"desired state: running or stopped"`
	PlacementMode string            `json:"placement_mode" jsonschema:"placement mode: exact_node, leader, or labels"`
	NodeID        string            `json:"node_id,omitempty" jsonschema:"node ID required for exact_node placement"`
	Labels        map[string]string `json:"labels,omitempty" jsonschema:"exact node labels required for labels placement"`
	Replicas      int32             `json:"replicas,omitempty" jsonschema:"replica count for labels placement"`
	Stateful      bool              `json:"stateful,omitempty" jsonschema:"whether the app retains node-local state"`
}

// IReconciler resolves and applies canonical desired state. Implementations may
// retain neither input: Controller passes owned snapshots and snapshots both
// returned messages before exposing or storing them.
type IReconciler interface {
	Prepare(
		ctx context.Context,
		intent *candaceosv1.ReconcileIntent,
	) (*candaceosv1.ReconcileRevision, error)
	ReconcileApproved(
		ctx context.Context,
		intent *candaceosv1.ReconcileIntent,
		revision *candaceosv1.ReconcileRevision,
	) (*candaceosv1.ReconcileEvidence, error)
}

type reconcileApprovalPayload struct {
	Input    *candaceosv1.ReconcileIntent
	Revision *candaceosv1.ReconcileRevision
}

func (payload reconcileApprovalPayload) MarshalJSON() ([]byte, error) {
	input, err := json.Marshal(reconcileToolInputFromIntent(payload.Input))
	if err != nil {
		return nil, fmt.Errorf("encoding approved reconcile intent: %w", err)
	}
	marshal := protojson.MarshalOptions{UseProtoNames: true}
	revision, err := marshal.Marshal(payload.Revision)
	if err != nil {
		return nil, fmt.Errorf("encoding approved reconcile revision: %w", err)
	}
	return json.Marshal(struct {
		Input    json.RawMessage `json:"input"`
		Revision json.RawMessage `json:"revision"`
	}{Input: input, Revision: revision})
}

type approvedReconcile struct {
	inputSHA256 string
	revision    *candaceosv1.ReconcileRevision
	expiresAt   time.Time
}

type eventKind string

const (
	eventKindSessionStart     eventKind = "session.start"
	eventKindSessionIdle      eventKind = "session.idle"
	eventKindSessionError     eventKind = "session.error"
	eventKindUserMessage      eventKind = "user.message"
	eventKindAssistantDelta   eventKind = "assistant.message_delta"
	eventKindAssistantMessage eventKind = "assistant.message"
	// eventKindAssistantIdle reports that one agentic loop finished. It is
	// deliberately not terminal: a provider emits it between queued follow-ups
	// and while background agents or attached shell commands are still running,
	// so only eventKindSessionIdle concludes a run.
	eventKindAssistantIdle         eventKind = "assistant.idle"
	eventKindToolExecutionStart    eventKind = "tool.execution_start"
	eventKindToolExecutionComplete eventKind = "tool.execution_complete"
	// The kinds below report conditions the operator needs to see but that no
	// message or tool entry describes: the runtime ending, a policy or
	// subscription warning, and context the provider dropped to keep working.
	eventKindSessionShutdown           eventKind = "session.shutdown"
	eventKindSessionWarning            eventKind = "session.warning"
	eventKindSessionCompactionComplete eventKind = "session.compaction_complete"
	eventKindSessionTruncation         eventKind = "session.truncation"
)

// endsRun reports whether an event kind concludes the active run as a failure.
// A runtime that shuts down mid-turn abandons that turn even when it shuts down
// routinely, so the run must not be left rendering as live work.
func (kind eventKind) endsRun() bool {
	return kind == eventKindSessionError || kind == eventKindSessionShutdown
}

type eventRecord struct {
	ID        string
	ParentID  string
	Type      eventKind
	Timestamp time.Time
	Data      map[string]any
	Ephemeral bool
}

// harnessPermission is the runtime-neutral policy input produced by an agent
// harness. SDK request types stay in their adapter.
type harnessPermission struct {
	kind                    string
	toolCallID              string
	title                   string
	risk                    string
	payload                 any
	requiresFleetQuorum     bool
	requiresManagedApproval bool
	requestSandboxBypass    bool
	path                    string
	commandSegments         []string
	possiblePaths           []string
	hasWriteFileRedirection bool
	hasPossibleURLs         bool
	reconcileArgs           any
}

type harnessPermissionDecision string

const (
	harnessPermissionApprove     harnessPermissionDecision = "approve"
	harnessPermissionReject      harnessPermissionDecision = "reject"
	harnessPermissionUnavailable harnessPermissionDecision = "unavailable"
)

// Controller owns one agent harness session: run admission and fencing,
// approvals, the operator timeline, and change notification. The exported
// On* fields are durability hooks the containing core installs before Start;
// a hook returning an error fails the corresponding operation closed.
type Controller struct {
	config     *candaceosv1.CoreConfig
	identity   *candaceosv1.HarnessRuntimeIdentity
	fleet      *fleet.Client
	queue      *ApprovalQueue
	reconciler IReconciler

	mu          sync.RWMutex
	harness     iHarnessImplementation
	sessionID   string
	status      controllerPhase
	runPhase    runPhase
	startedAt   time.Time
	currentRun  RunState
	events      map[string]eventRecord
	eventOrder  []string
	streams     map[string]string
	subscribers map[chan struct{}]struct{}
	approved    map[string]approvedReconcile
	lifecycle   context.Context
	cancel      context.CancelFunc

	sendMu sync.Mutex

	OnRunStarted        func(event RunStarted) error
	OnRunStatus         func(runID, status string, at time.Time)
	OnApprovalRequested func(approval Approval) error
	OnApprovalResolved  func(resolution ApprovalResolution) error
}

// NewController constructs the operator lifecycle for one configured harness and optional reconciler.
func NewController(cfg *candaceosv1.CoreConfig, fleetClient *fleet.Client, reconciler IReconciler) (*Controller, error) {
	return NewControllerWithHarness(cfg, fleetClient, reconciler, nil)
}

// NewControllerWithHarness constructs the operator lifecycle with an optional
// compiled-in harness. A nil factory preserves configuration-selected built-ins.
func NewControllerWithHarness(
	cfg *candaceosv1.CoreConfig,
	fleetClient *fleet.Client,
	reconciler IReconciler,
	factory harnesssdk.IFactory,
) (*Controller, error) {
	controller := &Controller{
		config:      cfg,
		fleet:       fleetClient,
		queue:       NewApprovalQueue(config.ApprovalTimeout(cfg)),
		reconciler:  reconciler,
		status:      controllerStarting,
		events:      make(map[string]eventRecord),
		streams:     make(map[string]string),
		subscribers: make(map[chan struct{}]struct{}),
		approved:    make(map[string]approvedReconcile),
	}
	controller.queue.OnRequested = func(approval Approval) error {
		if controller.OnApprovalRequested != nil {
			if err := controller.OnApprovalRequested(approval); err != nil {
				return err
			}
		}
		return nil
	}
	controller.queue.onPublished = controller.notify
	controller.queue.OnResolved = func(resolution ApprovalResolution) error {
		if controller.OnApprovalResolved != nil {
			if err := controller.OnApprovalResolved(resolution); err != nil {
				return err
			}
		}
		controller.notify()
		return nil
	}
	harness, err := configureHarness(cfg, controller, factory)
	if err != nil {
		return nil, err
	}
	controller.harness = harness.runtime
	controller.identity = harness.identity
	return controller, nil
}

func harnessRuntimeIdentity(cfg *candaceosv1.CoreConfig) (*candaceosv1.HarnessRuntimeIdentity, error) {
	if cfg == nil {
		return nil, errors.New("agent harness configuration is required")
	}
	identity := &candaceosv1.HarnessRuntimeIdentity{Backend: cfg.GetHarnessBackend()}
	switch cfg.GetHarnessBackend() {
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI:
		identity.Implementation = "copilot-cli"
		identity.Model = cfg.GetCopilotModel()
		identity.Capabilities = []candaceosv1.HarnessCapability{
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_WORKSPACE_WRITE,
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_RECONCILE,
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_ACTIVE_TURN_STEERING,
		}
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO:
		identity.Implementation = "demo"
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA:
		identity.Implementation = "ollama"
		identity.Capabilities = []candaceosv1.HarnessCapability{
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_RECONCILE,
		}
		if cfg.GetOllama() == nil {
			return nil, errors.New("Ollama harness configuration is required")
		}
		if err := candaceosv1.ValidateOllamaConfig(cfg.GetOllama()); err != nil {
			return nil, fmt.Errorf("Ollama harness configuration: %w", err)
		}
		identity.Model = cfg.GetOllama().GetModel()
		identity.ModelDigest = cfg.GetOllama().GetModelDigest()
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_OPENCODE:
		identity.Implementation = "opencode"
		identity.Capabilities = []candaceosv1.HarnessCapability{
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_WORKSPACE_WRITE,
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_ACTIVE_TURN_STEERING,
		}
	default:
		return nil, fmt.Errorf("unsupported agent harness backend %q", cfg.GetHarnessBackend())
	}
	if err := candaceosv1.ValidateHarnessRuntimeIdentity(identity); err != nil {
		return nil, fmt.Errorf("agent harness identity: %w", err)
	}
	return identity, nil
}

// ApprovalQueue exposes the queue so the transport can list and resolve
// pending approvals.
func (c *Controller) ApprovalQueue() *ApprovalQueue {
	return c.queue
}

// Start opens the harness session and binds this Controller's lifecycle to
// ctx. A harness that fails to start or activate is closed before the error
// is returned, so a failed Start leaves nothing running.
func (c *Controller) Start(ctx context.Context) error {
	lifecycle, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.lifecycle = lifecycle
	c.cancel = cancel
	clear(c.approved)
	c.mu.Unlock()
	started, err := c.harness.Start(lifecycle)
	if err != nil {
		return c.closeRejectedHarness(fmt.Errorf("starting agent harness: %w", err))
	}
	c.mu.Lock()
	c.sessionID = started.SessionID
	c.status = controllerIdle
	c.startedAt = time.Now().UTC()
	c.mu.Unlock()
	if started.Activate != nil {
		if err := started.Activate(); err != nil {
			return c.closeRejectedHarness(fmt.Errorf("activating agent harness: %w", err))
		}
	}
	c.notify()
	return nil
}

func (c *Controller) closeRejectedHarness(rejection error) error {
	if err := c.Close(); err != nil {
		return errors.Join(rejection, fmt.Errorf("closing rejected agent harness: %w", err))
	}
	return rejection
}

func (c *Controller) configuredFleetStatus() (fleet.ConfiguredSnapshot, error) {
	if c.fleet == nil {
		return fleet.ConfiguredSnapshot{}, errors.New("fleet status is unavailable")
	}
	return fleet.WithConfiguration(c.fleet.Snapshot(), config.NodeLabels(c.config)), nil
}

// Send admits one operator prompt, starting a new run or joining the active
// one according to delivery. Concurrent calls are serialized.
func (c *Controller) Send(
	ctx context.Context,
	prompt string,
	delivery candaceosv1.HarnessDelivery,
) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", errors.New("prompt is required")
	}
	if err := validateHarnessDelivery(delivery); err != nil {
		return "", err
	}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.startRunLocked(ctx, prompt, delivery)
}

// SendToSession atomically steers the active run or starts an ordinary new run
// after an idle turn. Both paths are fenced to the session and run rendered by
// the caller, so a stale browser cannot affect newer work.
func (c *Controller) SendToSession(
	ctx context.Context,
	expectedSessionID, expectedRunID, prompt string,
	delivery candaceosv1.HarnessDelivery,
) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", errors.New("prompt is required")
	}
	if err := validateHarnessDelivery(delivery); err != nil {
		return "", err
	}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := c.requireTargets(expectedSessionID, expectedRunID); err != nil {
		return "", err
	}
	c.mu.RLock()
	controllerStatus := c.status
	runStatus := c.runPhase
	runID := c.currentRun.ID
	c.mu.RUnlock()
	if controllerStatus == controllerRunning && runStatus == runRunning {
		if err := c.steerLocked(ctx, prompt, delivery); err != nil {
			return "", err
		}
		return runID, nil
	}
	if controllerStatus != controllerIdle || runStatus == runRunning || runStatus == runAborting {
		return "", ErrRunNotActive
	}
	return c.startRunLocked(ctx, prompt, delivery)
}

// Steer delivers another message to the exact active execution without
// creating a run, resetting its transcript, or invoking durable run-start
// callbacks.
func (c *Controller) Steer(
	ctx context.Context,
	expectedSessionID, expectedRunID, prompt string,
	delivery candaceosv1.HarnessDelivery,
) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return errors.New("prompt is required")
	}
	if err := validateHarnessDelivery(delivery); err != nil {
		return err
	}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := c.requireTargets(expectedSessionID, expectedRunID); err != nil {
		return err
	}
	c.mu.RLock()
	active := c.status == controllerRunning && c.runPhase == runRunning
	c.mu.RUnlock()
	if !active {
		return ErrRunNotActive
	}
	return c.steerLocked(ctx, prompt, delivery)
}

func (c *Controller) startRunLocked(
	ctx context.Context,
	prompt string,
	delivery candaceosv1.HarnessDelivery,
) (string, error) {
	c.mu.RLock()
	controllerStatus := c.status
	runStatus := c.runPhase
	c.mu.RUnlock()
	if controllerStatus != controllerIdle || runStatus == runRunning || runStatus == runAborting {
		return "", errors.New("the agent is already running or stopping a turn")
	}
	runID := uuid.NewString()
	now := time.Now().UTC()
	sessionID := c.SessionID()
	c.mu.Lock()
	c.streams = make(map[string]string)
	c.currentRun = RunState{
		ID: runID, SessionID: sessionID, Title: summarizePrompt(prompt), Prompt: prompt,
		Status: runRunning.String(), StartedAt: now, CanAbort: true,
	}
	c.runPhase = runRunning
	c.status = controllerRunning
	c.mu.Unlock()
	if c.OnRunStarted != nil {
		if err := c.OnRunStarted(RunStarted{RunID: runID, SessionID: sessionID, Title: summarizePrompt(prompt), Prompt: prompt, At: now}); err != nil {
			c.mu.Lock()
			c.setRunPhaseLocked(runFailed)
			c.currentRun.CanAbort = false
			c.status = controllerIdle
			c.mu.Unlock()
			c.notify()
			return "", fmt.Errorf("recording agent run: %w", err)
		}
	}
	c.notify()

	request := &candaceosv1.HarnessPrompt{RunId: runID, Content: prompt, Delivery: delivery}
	if err := candaceosv1.ValidateHarnessPrompt(request); err != nil {
		c.finishRun(runFailed)
		return "", fmt.Errorf("agent harness prompt: %w", err)
	}
	if err := c.harness.Send(ctx, request); err != nil {
		c.finishRun(runFailed)
		return "", fmt.Errorf("sending agent prompt: %w", err)
	}
	return runID, nil
}

func (c *Controller) steerLocked(
	ctx context.Context,
	prompt string,
	delivery candaceosv1.HarnessDelivery,
) error {
	request := &candaceosv1.HarnessPrompt{
		RunId: c.currentRunID(), Content: prompt, Delivery: delivery,
	}
	if err := candaceosv1.ValidateHarnessPrompt(request); err != nil {
		return fmt.Errorf("agent harness prompt: %w", err)
	}
	if err := c.harness.Send(ctx, request); err != nil {
		return fmt.Errorf("steering agent run: %w", err)
	}
	return nil
}

func (c *Controller) requireTargets(expectedSessionID, expectedRunID string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if expectedSessionID == "" || expectedSessionID != c.sessionID {
		return ErrSessionConflict
	}
	if expectedRunID == "" || expectedRunID != c.currentRun.ID {
		return ErrRunConflict
	}
	return nil
}

func validateHarnessDelivery(delivery candaceosv1.HarnessDelivery) error {
	switch delivery {
	case candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE:
	case candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE:
	default:
		return errors.New("delivery must be immediate or enqueue")
	}
	return nil
}

// Abort stops the active run, whichever it is. Use AbortRun to stop only the
// run a caller has actually rendered.
func (c *Controller) Abort(ctx context.Context) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.abortLocked(ctx)
}

// AbortRun stops only the exact execution rendered by the caller.
func (c *Controller) AbortRun(ctx context.Context, expectedSessionID, expectedRunID string) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := c.requireTargets(expectedSessionID, expectedRunID); err != nil {
		return err
	}
	return c.abortLocked(ctx)
}

func (c *Controller) abortLocked(ctx context.Context) error {
	c.mu.Lock()
	if c.currentRun.ID == "" || c.runPhase != runRunning {
		c.mu.Unlock()
		return errors.New("no active agent run")
	}
	runID := c.currentRun.ID
	c.setRunPhaseLocked(runAborting)
	c.currentRun.CanAbort = false
	c.status = controllerAborting
	clear(c.approved)
	c.mu.Unlock()
	c.notify()

	if err := c.harness.Abort(ctx); err != nil {
		c.restoreRunningAfterAbortFailure()
		return fmt.Errorf("aborting agent run: %w", err)
	}
	expirationErr := c.queue.ExpireRun(runID, ApprovalActorState().Abort)
	c.finishAbort()
	return expirationErr
}

func (c *Controller) finishAbort() {
	c.mu.Lock()
	if c.runPhase != runAborting {
		c.mu.Unlock()
		return
	}
	runID := c.currentRun.ID
	c.setRunPhaseLocked(runAborted)
	c.currentRun.CanAbort = false
	// Keep "aborting" until the SDK's idle event arrives. That prevents a late
	// idle event from the aborted turn from completing a newly submitted run.
	c.mu.Unlock()
	if runID != "" && c.OnRunStatus != nil {
		c.OnRunStatus(runID, runAborted.String(), time.Now().UTC())
	}
	c.notify()
}

func (c *Controller) restoreRunningAfterAbortFailure() {
	var completedRunID string
	c.mu.Lock()
	if c.runPhase == runAborting {
		switch c.status {
		case controllerIdle:
			// The turn reached idle while the abort RPC was in flight. Do not
			// resurrect completed work merely because that RPC returned an error.
			c.setRunPhaseLocked(runSucceeded)
			c.currentRun.CanAbort = false
			completedRunID = c.currentRun.ID
			c.status = controllerPersisting
		case controllerStopped:
			// Close owns the terminal controller state.
		default:
			c.setRunPhaseLocked(runRunning)
			c.currentRun.CanAbort = true
			c.status = controllerRunning
		}
	}
	c.mu.Unlock()
	if completedRunID != "" {
		if c.OnRunStatus != nil {
			c.OnRunStatus(completedRunID, runSucceeded.String(), time.Now().UTC())
		}
		c.mu.Lock()
		if c.currentRun.ID == completedRunID && c.status == controllerPersisting {
			c.status = controllerIdle
		}
		c.mu.Unlock()
	}
	c.notify()
}

// Close cancels the lifecycle, drops approved reconciliations, and closes the
// harness. It is safe to call on a Controller that never started.
func (c *Controller) Close() error {
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.status = controllerStopped
	clear(c.approved)
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return c.harness.Close()
}

// SessionID is the current harness session identifier, empty before Start.
// Callers fence their actions against it.
func (c *Controller) SessionID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionID
}

// Status is the controller phase rendered for the operator.
func (c *Controller) Status() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status.String()
}

// HarnessBackend returns the canonical configured adapter enum.
func (c *Controller) HarnessBackend() candaceosv1.HarnessBackend { return c.identity.GetBackend() }

// HarnessBackendName returns the configured adapter's environment spelling.
func (c *Controller) HarnessBackendName() string {
	return config.HarnessBackendName(c.HarnessBackend())
}

// HarnessIdentity returns the immutable backend and model artifact selected
// when this controller was constructed.
func (c *Controller) HarnessIdentity() *candaceosv1.HarnessRuntimeIdentity {
	return proto.Clone(c.identity).(*candaceosv1.HarnessRuntimeIdentity)
}

// Run returns a snapshot of the current run and its timeline.
func (c *Controller) Run() RunState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	run := c.currentRun
	run.Entries = c.timelineLocked()
	return run
}

// Subscribe returns a coalescing change signal and the function that
// unsubscribes it. The channel carries no payload: a reader re-reads state
// through Run, Status, or the snapshot projection.
func (c *Controller) Subscribe() (<-chan struct{}, func()) {
	updates := make(chan struct{}, 1)
	c.mu.Lock()
	c.subscribers[updates] = struct{}{}
	c.mu.Unlock()
	cancel := func() {
		c.mu.Lock()
		if _, ok := c.subscribers[updates]; ok {
			delete(c.subscribers, updates)
			close(updates)
		}
		c.mu.Unlock()
	}
	return updates, cancel
}

func (c *Controller) notify() {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for subscriber := range c.subscribers {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}

func (c *Controller) publishHarnessEvent(event *candaceosv1.HarnessEvent) error {
	c.mu.Lock()
	if event.GetSessionStarted() == nil &&
		(event.GetRunId() == "" || event.GetRunId() != c.currentRun.ID) {
		c.mu.Unlock()
		return errors.New("publishing harness event: run_id is not the active run")
	}
	completedRunID, completedRunStatus := c.ingestLocked(harnessEventRecord(event))
	c.mu.Unlock()
	c.finishPublishedEvent(completedRunID, completedRunStatus)
	return nil
}

func (c *Controller) ingest(event eventRecord) {
	c.mu.Lock()
	completedRunID, completedRunStatus := c.ingestLocked(event)
	c.mu.Unlock()
	c.finishPublishedEvent(completedRunID, completedRunStatus)
}

// ingestLocked applies one normalized event while c.mu is held. Keeping the
// run fence and mutation in the same critical section makes Host.Publish
// linearizable without acquiring sendMu, which a Runtime may already be
// calling through synchronously from Send.
func (c *Controller) ingestLocked(event eventRecord) (string, string) {
	var completedRunID string
	var completedRunStatus string
	if c.status == controllerStopped {
		return "", ""
	}
	if event.Ephemeral && event.Type == eventKindAssistantDelta {
		messageID := text(event.Data["messageId"])
		if messageID == "" {
			messageID = "active"
		}
		c.streams[messageID] += text(event.Data["deltaContent"])
	} else if !event.Ephemeral {
		if _, exists := c.events[event.ID]; !exists {
			c.events[event.ID] = event
			c.eventOrder = append(c.eventOrder, event.ID)
		}
		if event.Type == eventKindAssistantMessage {
			delete(c.streams, text(event.Data["messageId"]))
		}
	}
	if event.Type == eventKindSessionIdle {
		if c.currentRun.ID != "" && c.runPhase == runRunning {
			c.setRunPhaseLocked(runSucceeded)
			c.currentRun.CanAbort = false
			completedRunID = c.currentRun.ID
			completedRunStatus = runSucceeded.String()
			c.status = controllerPersisting
		} else {
			c.status = controllerIdle
		}
	} else if event.Type.endsRun() && c.currentRun.ID != "" &&
		(c.runPhase == runRunning || c.runPhase == runAborting) {
		c.setRunPhaseLocked(runFailed)
		c.currentRun.CanAbort = false
		completedRunID = c.currentRun.ID
		completedRunStatus = runFailed.String()
		c.status = controllerPersisting
	}
	return completedRunID, completedRunStatus
}

func (c *Controller) finishPublishedEvent(completedRunID, completedRunStatus string) {
	if completedRunID != "" {
		if c.OnRunStatus != nil {
			c.OnRunStatus(completedRunID, completedRunStatus, time.Now().UTC())
		}
		c.mu.Lock()
		if c.currentRun.ID == completedRunID && c.status == controllerPersisting {
			c.status = controllerIdle
		}
		c.mu.Unlock()
	}
	c.notify()
}

func (c *Controller) finishRun(status runPhase) {
	c.mu.Lock()
	runID := c.currentRun.ID
	c.setRunPhaseLocked(status)
	c.currentRun.CanAbort = false
	c.status = controllerIdle
	c.mu.Unlock()
	if runID != "" && c.OnRunStatus != nil {
		c.OnRunStatus(runID, status.String(), time.Now().UTC())
	}
	c.notify()
}

func (c *Controller) setRunPhaseLocked(status runPhase) {
	c.runPhase = status
	c.currentRun.Status = status.String()
}

func (c *Controller) timelineLocked() []TimelineEntry {
	ordered := append([]string(nil), c.eventOrder...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return c.events[ordered[i]].Timestamp.Before(c.events[ordered[j]].Timestamp)
	})
	entries := make([]TimelineEntry, 0, len(ordered)+len(c.streams))
	tools := make(map[string]int)
	for _, id := range ordered {
		event := c.events[id]
		switch event.Type {
		case eventKindUserMessage:
			entries = append(entries, TimelineEntry{
				ID: id, Kind: "message", Role: "user", Name: "You",
				Text:   text(event.Data["content"]),
				Status: promptDelivery(event.Data["delivery"]),
				At:     event.Timestamp,
			})
		case eventKindAssistantMessage:
			entries = append(entries, TimelineEntry{ID: id, Kind: "message", Role: "assistant", Name: "Claw", Text: text(event.Data["content"]), At: event.Timestamp})
		case eventKindToolExecutionStart:
			toolCallID := text(event.Data["toolCallId"])
			tools[toolCallID] = len(entries)
			entries = append(entries, TimelineEntry{ID: id, Kind: "tool", Name: text(event.Data["toolName"]), Detail: compactJSON(event.Data["arguments"]), Status: "running", At: event.Timestamp})
		case eventKindToolExecutionComplete:
			if index, ok := tools[text(event.Data["toolCallId"])]; ok {
				if text(event.Data["error"]) != "" {
					entries[index].Status = "failed"
				} else {
					entries[index].Status = "complete"
				}
				entries[index].Detail = compactJSON(event.Data)
			}
		case eventKindSessionError:
			entries = append(entries, TimelineEntry{
				ID: id, Kind: "error", Name: "Claw", Text: text(event.Data["message"]), Status: "failed", At: event.Timestamp,
			})
		case eventKindSessionShutdown:
			entries = append(entries, notice(id, event, "stopped", shutdownReason(event)))
		case eventKindSessionWarning:
			entries = append(entries, notice(id, event, "warning", text(event.Data["message"])))
		case eventKindSessionCompactionComplete:
			entries = append(entries, notice(id, event, "compacted",
				"Compacted the conversation to stay inside the model context window."))
		case eventKindSessionTruncation:
			entries = append(entries, notice(id, event, "truncated",
				"Dropped earlier conversation to stay inside the model context window."))
		}
	}
	for messageID, content := range c.streams {
		entries = append(entries, TimelineEntry{ID: "stream-" + messageID, Kind: "message", Role: "assistant", Name: "Claw", Text: content, Status: "streaming", At: time.Now().UTC()})
	}
	return entries
}

// promptDelivery names how the provider admitted one operator message relative
// to the running turn, so the transcript distinguishes guidance that interrupted
// active work from guidance that waited behind it. An empty result means the
// message opened its own turn and needs no annotation.
func promptDelivery(value any) string {
	switch text(value) {
	case "steering", "queued":
		return text(value)
	default:
		return ""
	}
}

// notice renders one provider condition that no message or tool entry
// describes, such as the runtime ending or context being dropped.
func notice(id string, event eventRecord, status, message string) TimelineEntry {
	return TimelineEntry{
		ID: id, Kind: "notice", Name: "Claw",
		Status: status, Text: message, At: event.Timestamp,
	}
}

// shutdownReason describes why the agent runtime ended. A crash carries its own
// explanation; a routine shutdown has none to give.
func shutdownReason(event eventRecord) string {
	if reason := text(event.Data["errorReason"]); reason != "" {
		return "The agent runtime stopped: " + reason
	}
	return "The agent runtime stopped."
}

func (c *Controller) handlePermission(request harnessPermission) (harnessPermissionDecision, error) {
	return c.handlePermissionContext(nil, request)
}

func (c *Controller) handlePermissionContext(ctx context.Context, request harnessPermission) (harnessPermissionDecision, error) {
	if c.safeToApprove(request) {
		return harnessPermissionApprove, nil
	}
	c.mu.RLock()
	lifecycle := c.lifecycle
	c.mu.RUnlock()
	if ctx == nil {
		ctx = lifecycle
	}
	if ctx == nil {
		ctx = context.Background()
	}
	payload := request.payload
	var prepared *approvedReconcile
	if request.requiresFleetQuorum {
		var err error
		payload, prepared, err = c.prepareReconcileApproval(ctx, request.toolCallID, request.reconcileArgs)
		if err != nil {
			return harnessPermissionUnavailable, err
		}
	}
	detail, _ := json.Marshal(payload)
	resolution, err := c.queue.Request(ctx, ApprovalRequest{
		RunID:               c.currentRunID(),
		ToolCallID:          request.toolCallID,
		Kind:                request.kind,
		Title:               request.title,
		Detail:              string(detail),
		Risk:                request.risk,
		Payload:             payload,
		RequiresFleetQuorum: request.requiresFleetQuorum,
	})
	if err != nil || resolution.Decision == DecisionExpired {
		return harnessPermissionUnavailable, err
	}
	if resolution.Decision == DecisionApprove {
		if prepared != nil {
			prepared.expiresAt = resolution.Approval.ExpiresAt
			c.mu.Lock()
			if lifecycle == nil || c.lifecycle != lifecycle || lifecycle.Err() != nil || ctx.Err() != nil || c.status == controllerAborting || c.status == controllerStopped {
				c.mu.Unlock()
				lifecycleErr := ctx.Err()
				if lifecycleErr == nil && lifecycle != nil {
					lifecycleErr = lifecycle.Err()
				}
				if lifecycleErr == nil {
					lifecycleErr = errors.New("reconcile approval was invalidated before dispatch")
				}
				return harnessPermissionUnavailable, lifecycleErr
			}
			c.approved[resolution.Approval.ToolCallID] = *prepared
			c.mu.Unlock()
		}
		return harnessPermissionApprove, nil
	}
	return harnessPermissionReject, nil
}

func (c *Controller) prepareReconcileApproval(
	ctx context.Context,
	toolCallID string,
	args any,
) (reconcileApprovalPayload, *approvedReconcile, error) {
	if toolCallID == "" {
		return reconcileApprovalPayload{}, nil, errors.New("reconcile approval requires a tool call ID")
	}
	if c.reconciler == nil {
		return reconcileApprovalPayload{}, nil, errors.New("reconciler is unavailable")
	}
	c.mu.Lock()
	delete(c.approved, toolCallID)
	c.mu.Unlock()
	input, err := reconcileIntent(args)
	if err != nil {
		return reconcileApprovalPayload{}, nil, fmt.Errorf("decoding reconcile approval input: %w", err)
	}
	revision, err := c.reconciler.Prepare(
		ctx,
		proto.Clone(input).(*candaceosv1.ReconcileIntent),
	)
	if err != nil {
		return reconcileApprovalPayload{}, nil, fmt.Errorf("preparing immutable app revision for approval: %w", err)
	}
	var ownedRevision *candaceosv1.ReconcileRevision
	if revision != nil {
		ownedRevision = proto.Clone(revision).(*candaceosv1.ReconcileRevision)
	}
	if err := candaceosv1.ValidateReconcileRevision(ownedRevision); err != nil {
		return reconcileApprovalPayload{}, nil, fmt.Errorf("prepared app revision: %w", err)
	}
	inputSHA256, err := reconcileInputDigest(input)
	if err != nil {
		return reconcileApprovalPayload{}, nil, err
	}
	payload := reconcileApprovalPayload{
		Input:    proto.Clone(input).(*candaceosv1.ReconcileIntent),
		Revision: proto.Clone(ownedRevision).(*candaceosv1.ReconcileRevision),
	}
	return payload, &approvedReconcile{
		inputSHA256: inputSHA256,
		revision:    proto.Clone(ownedRevision).(*candaceosv1.ReconcileRevision),
	}, nil
}

func (c *Controller) reconcileApproved(
	ctx context.Context,
	input *candaceosv1.ReconcileIntent,
	toolCallID string,
) (*candaceosv1.ReconcileEvidence, error) {
	if toolCallID == "" {
		return nil, errors.New("reconcile dispatch requires a tool call ID")
	}
	var ownedInput *candaceosv1.ReconcileIntent
	if input != nil {
		ownedInput = proto.Clone(input).(*candaceosv1.ReconcileIntent)
	}
	if err := candaceosv1.ValidateReconcileIntent(ownedInput); err != nil {
		return nil, fmt.Errorf("reconcile dispatch input: %w", err)
	}
	c.mu.Lock()
	approved, ok := c.approved[toolCallID]
	delete(c.approved, toolCallID)
	c.mu.Unlock()
	if !ok {
		return nil, errors.New("reconcile dispatch has no matching one-time approval")
	}
	if time.Now().UTC().After(approved.expiresAt) {
		return nil, errors.New("reconcile approval expired before dispatch")
	}
	inputSHA256, err := reconcileInputDigest(ownedInput)
	if err != nil {
		return nil, err
	}
	if inputSHA256 != approved.inputSHA256 {
		return nil, errors.New("reconcile input changed after approval")
	}
	result, reconcileErr := c.reconciler.ReconcileApproved(
		ctx,
		proto.Clone(ownedInput).(*candaceosv1.ReconcileIntent),
		proto.Clone(approved.revision).(*candaceosv1.ReconcileRevision),
	)
	if result == nil {
		if reconcileErr != nil {
			return nil, reconcileErr
		}
		return nil, errors.New("reconciler returned no evidence")
	}
	ownedResult := proto.Clone(result).(*candaceosv1.ReconcileEvidence)
	if err := candaceosv1.ValidateReconcileEvidence(ownedResult); err != nil {
		validationErr := fmt.Errorf("reconcile evidence: %w", err)
		if reconcileErr != nil {
			return nil, errors.Join(reconcileErr, validationErr)
		}
		return nil, validationErr
	}
	return ownedResult, reconcileErr
}

func reconcileIntent(value any) (*candaceosv1.ReconcileIntent, error) {
	if input, ok := value.(*candaceosv1.ReconcileIntent); ok {
		var ownedInput *candaceosv1.ReconcileIntent
		if input != nil {
			ownedInput = proto.Clone(input).(*candaceosv1.ReconcileIntent)
		}
		if err := candaceosv1.ValidateReconcileIntent(ownedInput); err != nil {
			return nil, err
		}
		return ownedInput, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var input reconcileToolInput
	if err := json.Unmarshal(encoded, &input); err != nil {
		return nil, err
	}
	desiredState, err := reconcileDesiredState(input.DesiredState)
	if err != nil {
		return nil, err
	}
	placementMode, err := reconcilePlacementMode(input.PlacementMode)
	if err != nil {
		return nil, err
	}
	intent := &candaceosv1.ReconcileIntent{
		App: input.App, Project: input.Project, Path: input.Path,
		DesiredState: desiredState, PlacementMode: placementMode,
		NodeId: input.NodeID, Labels: input.Labels, Replicas: input.Replicas,
		Stateful: input.Stateful,
	}
	if err := candaceosv1.ValidateReconcileIntent(intent); err != nil {
		return nil, err
	}
	return intent, nil
}

func reconcileToolInputFromIntent(intent *candaceosv1.ReconcileIntent) reconcileToolInput {
	return reconcileToolInput{
		App: intent.GetApp(), Project: intent.GetProject(), Path: intent.GetPath(),
		DesiredState:  reconcileDesiredStateName(intent.GetDesiredState()),
		PlacementMode: reconcilePlacementModeName(intent.GetPlacementMode()),
		NodeID:        intent.GetNodeId(), Labels: intent.GetLabels(), Replicas: intent.GetReplicas(),
		Stateful: intent.GetStateful(),
	}
}

func reconcileDesiredState(value string) (candaceosv1.DesiredState, error) {
	switch value {
	case "running":
		return candaceosv1.DesiredState_DESIRED_STATE_RUNNING, nil
	case "stopped":
		return candaceosv1.DesiredState_DESIRED_STATE_STOPPED, nil
	default:
		return candaceosv1.DesiredState_DESIRED_STATE_UNSPECIFIED, fmt.Errorf("desired_state must be running or stopped")
	}
}

func reconcilePlacementMode(value string) (candaceosv1.PlacementMode, error) {
	switch value {
	case "exact_node":
		return candaceosv1.PlacementMode_PLACEMENT_MODE_EXACT_NODE, nil
	case "leader":
		return candaceosv1.PlacementMode_PLACEMENT_MODE_LEADER, nil
	case "labels":
		return candaceosv1.PlacementMode_PLACEMENT_MODE_LABELS, nil
	default:
		return candaceosv1.PlacementMode_PLACEMENT_MODE_UNSPECIFIED, fmt.Errorf("placement_mode must be exact_node, leader, or labels")
	}
}

func reconcileDesiredStateName(value candaceosv1.DesiredState) string {
	switch value {
	case candaceosv1.DesiredState_DESIRED_STATE_RUNNING:
		return "running"
	case candaceosv1.DesiredState_DESIRED_STATE_STOPPED:
		return "stopped"
	default:
		return ""
	}
}

func reconcilePlacementModeName(value candaceosv1.PlacementMode) string {
	switch value {
	case candaceosv1.PlacementMode_PLACEMENT_MODE_EXACT_NODE:
		return "exact_node"
	case candaceosv1.PlacementMode_PLACEMENT_MODE_LEADER:
		return "leader"
	case candaceosv1.PlacementMode_PLACEMENT_MODE_LABELS:
		return "labels"
	default:
		return ""
	}
}

func reconcileInputDigest(input *candaceosv1.ReconcileIntent) (string, error) {
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encoding reconcile input: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (c *Controller) safeToApprove(request harnessPermission) bool {
	if request.requiresManagedApproval {
		return false
	}
	switch request.kind {
	case "read", "write":
		return !request.requestSandboxBypass && c.inWorkspace(request.path)
	default:
		return false
	}
}

func (c *Controller) inWorkspace(path string) bool {
	if path == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.config.Workspace, path)
	}
	clean, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	root, err := filepath.Abs(c.config.Workspace)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, clean)
	if err != nil || !filepath.IsLocal(relative) {
		return false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolvedPath, err := resolveExistingAncestor(clean)
	if err != nil {
		return false
	}
	resolvedRelative, err := filepath.Rel(resolvedRoot, resolvedPath)
	return err == nil && filepath.IsLocal(resolvedRelative)
}

func resolveExistingAncestor(path string) (string, error) {
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(path)
			if readErr != nil {
				return "", readErr
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}
			resolved, resolveErr := resolveExistingAncestor(target)
			if resolveErr != nil {
				return "", resolveErr
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		missing = append(missing, filepath.Base(path))
		path = parent
	}
}

func (c *Controller) currentRunID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentRun.ID
}

func summarizePrompt(prompt string) string {
	const maximum = 72
	first := strings.TrimSpace(strings.SplitN(prompt, "\n", 2)[0])
	if len(first) <= maximum {
		return first
	}
	return first[:maximum-1] + "…"
}

func text(value any) string {
	if result, ok := value.(string); ok {
		return result
	}
	return ""
}

func compactJSON(value any) string {
	if value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}
