package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	// ErrStaleFence means a request came from an older leader term.
	ErrStaleFence = errors.New("stale leader fence")
	// ErrFenceConflict means two leader IDs claimed the same term.
	ErrFenceConflict = errors.New("conflicting leader fence")
	// ErrPersistence means durable state could not be read or written.
	ErrPersistence = errors.New("agent state persistence failed")
	// ErrExecution means a validated Compose plan failed to execute.
	ErrExecution = errors.New("compose execution failed")
)

// Reconciler serializes node mutations, fences leaders, and persists state.
type Reconciler struct {
	operationMu sync.Mutex
	stateMu     sync.RWMutex
	store       IStore
	executor    IExecutor
	now         func() time.Time
	state       Snapshot
}

// NewReconciler restores durable state before accepting requests.
func NewReconciler(store IStore, executor IExecutor) (*Reconciler, error) {
	state, ok, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("%w: loading reconciliation state: %v", ErrPersistence, err)
	}
	if !ok {
		state = Snapshot{}
	}
	return &Reconciler{
		store:    store,
		executor: executor,
		now:      func() time.Time { return time.Now().UTC() },
		state:    cloneSnapshot(state),
	}, nil
}

// Reconcile validates and applies one request. The highest new fence is saved
// before any Docker command is allowed to run.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	request *candaceosv1.ReconcileRequest,
) (*candaceosv1.ReconcileResponse, error) {
	ownedRequest, err := validatedOwnedRequest(request)
	if err != nil {
		return nil, err
	}
	fence := ownedRequest.GetFence()
	assignment := ownedRequest.GetAssignment()

	// Serialize fencing, immutable source materialization, and Compose. A stale
	// or conflicting request must be rejected before Plan can touch the cache.
	// stateMu remains independently available so status requests do not block
	// behind a long image pull or container start.
	r.operationMu.Lock()
	defer r.operationMu.Unlock()
	if err := r.acceptFence(fence); err != nil {
		return nil, err
	}
	plan, err := r.executor.Plan(ctx, cloneAssignment(assignment))
	if err != nil {
		return nil, err
	}

	candidate := Snapshot{
		Fence: fence, Assignment: assignment, Commands: cloneCommands(plan.Commands),
	}
	dryRun := r.executor.DryRun()
	result := responseFromSnapshot(candidate, dryRun)
	if err := r.executor.Execute(ctx, plan); err != nil {
		return result, fmt.Errorf("%w: %v", ErrExecution, err)
	}

	candidate.UpdatedAt = r.now()
	r.stateMu.Lock()
	err = r.persistStateLocked(candidate)
	r.stateMu.Unlock()
	if err != nil {
		return result, fmt.Errorf("%w: persisting reconciled assignment: %v", ErrPersistence, err)
	}
	return responseFromSnapshot(candidate, dryRun), nil
}

// validatedOwnedRequest establishes the Reconciler's ownership boundary.
// Generated protobuf messages are mutable, so retaining caller-owned fields
// would let later request reuse rewrite already validated or persisted state.
func validatedOwnedRequest(request *candaceosv1.ReconcileRequest) (*candaceosv1.ReconcileRequest, error) {
	if request == nil {
		return nil, fmt.Errorf("fence message is required")
	}
	owned := proto.Clone(request).(*candaceosv1.ReconcileRequest)
	if owned.GetFence() == nil {
		return nil, fmt.Errorf("fence message is required")
	}
	if owned.GetAssignment() == nil {
		return nil, fmt.Errorf("assignment message is required")
	}
	if err := candaceosv1.ValidateFence(owned.GetFence()); err != nil {
		return nil, err
	}
	if err := candaceosv1.ValidateAssignment(owned.GetAssignment()); err != nil {
		return nil, err
	}
	return owned, nil
}

func (r *Reconciler) acceptFence(fence *candaceosv1.Fence) error {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	current := r.state.Fence
	if current != nil && fence.GetTerm() < current.GetTerm() {
		return fmt.Errorf("%w: got term %d, highest is %d", ErrStaleFence, fence.GetTerm(), current.GetTerm())
	}
	if current != nil && fence.GetTerm() == current.GetTerm() && fence.GetLeaderId() != current.GetLeaderId() {
		return fmt.Errorf("%w: term %d belongs to %q, not %q", ErrFenceConflict, fence.GetTerm(), current.GetLeaderId(), fence.GetLeaderId())
	}
	if current != nil && fence.GetTerm() == current.GetTerm() {
		return nil
	}
	candidate := r.state
	candidate.Fence = fence
	if err := r.persistStateLocked(candidate); err != nil {
		return fmt.Errorf("%w: persisting accepted fence: %v", ErrPersistence, err)
	}
	return nil
}

// persistStateLocked publishes a candidate only after its durable write. The
// store and Reconciler receive independent copies because Snapshot contains
// mutable protobuf messages and slices.
func (r *Reconciler) persistStateLocked(candidate Snapshot) error {
	owned := cloneSnapshot(candidate)
	if err := r.store.Save(cloneSnapshot(owned)); err != nil {
		return err
	}
	r.state = owned
	return nil
}

func responseFromSnapshot(snapshot Snapshot, dryRun bool) *candaceosv1.ReconcileResponse {
	response := &candaceosv1.ReconcileResponse{
		Fence:      cloneFence(snapshot.Fence),
		Assignment: cloneAssignment(snapshot.Assignment),
		DryRun:     dryRun,
		Commands:   CommandsToProto(snapshot.Commands),
	}
	if !snapshot.UpdatedAt.IsZero() {
		response.UpdatedAt = timestamppb.New(snapshot.UpdatedAt)
	}
	return response
}

// CommandsToProto returns isolated wire commands for a plan or snapshot.
func CommandsToProto(commands []Command) []*candaceosv1.Command {
	result := make([]*candaceosv1.Command, len(commands))
	for index, command := range commands {
		result[index] = &candaceosv1.Command{Argv: append([]string(nil), command.Argv...)}
	}
	return result
}

// Snapshot returns an isolated copy of current in-memory state.
func (r *Reconciler) Snapshot() Snapshot {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return cloneSnapshot(r.state)
}

// DryRun reports the executor mode.
func (r *Reconciler) DryRun() bool { return r.executor.DryRun() }

// Workspace returns the executor's canonical workspace.
func (r *Reconciler) Workspace() string { return r.executor.Workspace() }
