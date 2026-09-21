package operator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ApprovalDecision is the terminal outcome recorded for an approval.
type ApprovalDecision string

const (
	// DecisionApprove and DecisionReject are operator choices; DecisionExpired is
	// the fail-closed outcome the queue applies when neither arrives in time.
	DecisionApprove ApprovalDecision = "approve"
	DecisionReject  ApprovalDecision = "reject"
	DecisionExpired ApprovalDecision = "expired"

	approvalActorOperatorKey    = "operator"
	approvalActorRuntimeKey     = "runtime"
	approvalActorTimeoutKey     = "timeout"
	approvalActorAbortKey       = "abort"
	approvalActorWebOperatorKey = "web-operator"
)

// ApprovalActorKeys names the canonical actors recorded in approval history.
type ApprovalActorKeys struct {
	Operator    string
	Runtime     string
	Timeout     string
	Abort       string
	WebOperator string
}

// ApprovalActorState returns the actor keys used in persisted approval history.
func ApprovalActorState() ApprovalActorKeys {
	return ApprovalActorKeys{
		Operator:    approvalActorOperatorKey,
		Runtime:     approvalActorRuntimeKey,
		Timeout:     approvalActorTimeoutKey,
		Abort:       approvalActorAbortKey,
		WebOperator: approvalActorWebOperatorKey,
	}
}

var (
	ErrApprovalRecording = errors.New("approval request is still being recorded")
	ErrApprovalResolving = errors.New("approval resolution is already in progress")
	ErrApprovalExpired   = errors.New("approval expired")
)

// ApprovalNotPendingError identifies an approval that cannot be resolved by
// the in-memory queue. The durable approval record owns its terminal history.
type ApprovalNotPendingError struct {
	ID string
}

// Error implements error.
func (err *ApprovalNotPendingError) Error() string {
	return fmt.Sprintf("approval %q is not pending", err.ID)
}

// ApprovalRequest is what a harness or tool asks the operator to authorize.
// Payload is hashed, not stored, so the queue never retains tool arguments.
type ApprovalRequest struct {
	RunID               string
	ToolCallID          string
	Kind                string
	Title               string
	Detail              string
	Risk                string
	Payload             any
	RequiresFleetQuorum bool
}

// Approval is the operator-visible form of a pending request, carrying the
// payload digest rather than the payload itself.
type Approval struct {
	ID                  string    `json:"id"`
	RunID               string    `json:"run_id,omitempty"`
	ToolCallID          string    `json:"tool_call_id,omitempty"`
	Kind                string    `json:"kind"`
	Title               string    `json:"title"`
	Detail              string    `json:"detail"`
	Risk                string    `json:"risk"`
	PayloadSHA256       string    `json:"payload_sha256"`
	RequiresFleetQuorum bool      `json:"requires_fleet_quorum,omitempty"`
	RequestedAt         time.Time `json:"requested_at"`
	ExpiresAt           time.Time `json:"expires_at"`
}

// ApprovalResolution is one approval's terminal outcome together with the
// actor that caused it; Actor is one of the keys ApprovalActorState names.
type ApprovalResolution struct {
	Approval Approval
	Decision ApprovalDecision
	Actor    string
	At       time.Time
}

type pendingApproval struct {
	approval      Approval
	result        chan approvalResult
	resolving     bool
	invalidatedBy string
}

type approvalResult struct {
	resolution ApprovalResolution
	err        error
}

type approvalFinishState uint8

const (
	approvalFinishMissing approvalFinishState = iota
	approvalFinishRecording
	approvalFinishResolving
	approvalFinishApplied
)

type approvalFinishResult struct {
	state  approvalFinishState
	result approvalResult
}

// ApprovalQueue serializes pending approvals for one Controller. It is safe
// for concurrent use. OnRequested and OnResolved are durability hooks the
// containing core installs; a hook returning an error fails the approval
// closed rather than admitting work that was not recorded.
type ApprovalQueue struct {
	timeout time.Duration
	now     func() time.Time

	mu        sync.Mutex
	recording map[string]*pendingApproval
	pending   map[string]*pendingApproval

	OnRequested func(approval Approval) error
	OnResolved  func(resolution ApprovalResolution) error
	onPublished func()
}

// NewApprovalQueue constructs an empty queue whose requests expire after
// timeout.
func NewApprovalQueue(timeout time.Duration) *ApprovalQueue {
	return &ApprovalQueue{
		timeout:   timeout,
		now:       time.Now,
		recording: make(map[string]*pendingApproval),
		pending:   make(map[string]*pendingApproval),
	}
}

// Request blocks until the approval is resolved, expires, or ctx is done, and
// resolves each request exactly once.
func (q *ApprovalQueue) Request(ctx context.Context, request ApprovalRequest) (ApprovalResolution, error) {
	if request.Kind == "" || request.Title == "" || request.Detail == "" {
		return ApprovalResolution{}, errors.New("approval kind, title, and detail are required")
	}
	if request.Risk != "low" && request.Risk != "medium" && request.Risk != "high" {
		return ApprovalResolution{}, errors.New("approval risk must be low, medium, or high")
	}
	payload, err := json.Marshal(request.Payload)
	if err != nil {
		return ApprovalResolution{}, err
	}
	digest := sha256.Sum256(payload)
	now := q.now().UTC()
	expiresAt := now.Add(q.timeout)
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline && deadline.Before(expiresAt) {
		expiresAt = deadline.UTC()
	}
	pending := &pendingApproval{
		approval: Approval{
			ID: uuid.NewString(), RunID: request.RunID, ToolCallID: request.ToolCallID,
			Kind: request.Kind, Title: request.Title, Detail: request.Detail, Risk: request.Risk,
			PayloadSHA256: hex.EncodeToString(digest[:]), RequiresFleetQuorum: request.RequiresFleetQuorum,
			RequestedAt: now, ExpiresAt: expiresAt,
		},
		result: make(chan approvalResult, 1),
	}

	q.mu.Lock()
	q.recording[pending.approval.ID] = pending
	q.mu.Unlock()
	if q.OnRequested != nil {
		if err := q.OnRequested(pending.approval); err != nil {
			q.mu.Lock()
			delete(q.recording, pending.approval.ID)
			q.mu.Unlock()
			return ApprovalResolution{}, fmt.Errorf("recording approval request: %w", err)
		}
	}
	q.mu.Lock()
	delete(q.recording, pending.approval.ID)
	invalidatedBy := pending.invalidatedBy
	contextErr := ctx.Err()
	if invalidatedBy == "" && contextErr == nil {
		q.pending[pending.approval.ID] = pending
	}
	q.mu.Unlock()
	if invalidatedBy != "" || contextErr != nil {
		if invalidatedBy == "" {
			invalidatedBy = ApprovalActorState().Runtime
		}
		resolution := ApprovalResolution{
			Approval: pending.approval, Decision: DecisionExpired,
			Actor: invalidatedBy, At: q.now().UTC(),
		}
		var resolutionErr error
		if q.OnResolved != nil {
			resolutionErr = q.OnResolved(resolution)
		}
		if contextErr != nil {
			return ApprovalResolution{}, errors.Join(contextErr, resolutionErr)
		}
		return resolution, resolutionErr
	}
	if q.onPublished != nil {
		q.onPublished()
	}

	timeRemaining := time.Until(expiresAt)
	if timeRemaining < 0 {
		timeRemaining = 0
	}
	timer := time.NewTimer(timeRemaining)
	defer timer.Stop()
	select {
	case result := <-pending.result:
		return result.resolution, result.err
	case <-ctx.Done():
		finished := q.finish(pending.approval.ID, DecisionExpired, ApprovalActorState().Runtime, q.now().UTC())
		return ApprovalResolution{}, errors.Join(ctx.Err(), finished.result.err)
	case <-timer.C:
		finished := q.finish(pending.approval.ID, DecisionExpired, ApprovalActorState().Timeout, q.now().UTC())
		if finished.state != approvalFinishApplied {
			result := <-pending.result
			return result.resolution, result.err
		}
		return finished.result.resolution, finished.result.err
	}
}

// Resolve applies an operator decision to one pending approval. An empty actor
// defaults to the operator key. It reports ApprovalNotPendingError for an
// unknown approval and ErrApprovalRecording or ErrApprovalResolving while
// another writer holds the same one.
func (q *ApprovalQueue) Resolve(id string, decision ApprovalDecision, actor string) (ApprovalResolution, error) {
	if decision != DecisionApprove && decision != DecisionReject {
		return ApprovalResolution{}, errors.New("decision must be approve or reject")
	}
	if actor == "" {
		actor = ApprovalActorState().Operator
	}
	finished := q.finish(id, decision, actor, q.now().UTC())
	switch finished.state {
	case approvalFinishMissing:
		return ApprovalResolution{}, &ApprovalNotPendingError{ID: id}
	case approvalFinishRecording:
		return ApprovalResolution{}, fmt.Errorf("%w: %q", ErrApprovalRecording, id)
	case approvalFinishResolving:
		return ApprovalResolution{}, fmt.Errorf("%w: %q", ErrApprovalResolving, id)
	case approvalFinishApplied:
		resolution := finished.result.resolution
		if resolution.Decision == DecisionExpired {
			return resolution, errors.Join(fmt.Errorf(
				"%w: %q reached its %s deadline before %q could submit %q",
				ErrApprovalExpired, id, resolution.Approval.ExpiresAt.Format(time.RFC3339Nano), actor, decision,
			), finished.result.err)
		}
		return resolution, finished.result.err
	default:
		panic("unreachable approval finish state")
	}
}

// Pending lists the approvals awaiting a decision, oldest request first.
func (q *ApprovalQueue) Pending() []Approval {
	q.mu.Lock()
	defer q.mu.Unlock()
	result := make([]Approval, 0, len(q.pending))
	for _, item := range q.pending {
		result = append(result, item.approval)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].RequestedAt.Before(result[j].RequestedAt)
	})
	return result
}

// Get returns one pending approval without claiming or resolving it.
func (q *ApprovalQueue) Get(id string) (Approval, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	pending, ok := q.pending[id]
	if !ok || pending.resolving {
		return Approval{}, false
	}
	return pending.approval, true
}

// ExpireRun fails closed every pending permission associated with one run.
// It is used after aborting an agent turn so no background approval waiter or
// actionable UI card survives the turn that requested it.
func (q *ApprovalQueue) ExpireRun(runID, actor string) error {
	if runID == "" {
		return nil
	}
	if actor == "" {
		actor = ApprovalActorState().Abort
	}
	q.mu.Lock()
	for _, recording := range q.recording {
		if recording.approval.RunID == runID && recording.invalidatedBy == "" {
			recording.invalidatedBy = actor
		}
	}
	ids := make([]string, 0)
	for id, pending := range q.pending {
		if pending.approval.RunID == runID {
			ids = append(ids, id)
		}
	}
	q.mu.Unlock()
	sort.Strings(ids)
	var expirationErrors []error
	for _, id := range ids {
		finished := q.finish(id, DecisionExpired, actor, q.now().UTC())
		switch {
		case finished.result.err != nil:
			expirationErrors = append(expirationErrors, fmt.Errorf("expiring approval %q: %w", id, finished.result.err))
		case finished.state == approvalFinishResolving:
			expirationErrors = append(expirationErrors, fmt.Errorf("expiring approval %q: resolution already in progress", id))
		case finished.state != approvalFinishApplied:
			expirationErrors = append(expirationErrors, fmt.Errorf("expiring approval %q: approval is no longer pending", id))
		}
	}
	return errors.Join(expirationErrors...)
}

func (q *ApprovalQueue) finish(
	id string,
	decision ApprovalDecision,
	actor string,
	at time.Time,
) approvalFinishResult {
	q.mu.Lock()
	if _, ok := q.recording[id]; ok {
		q.mu.Unlock()
		return approvalFinishResult{state: approvalFinishRecording}
	}
	pending, ok := q.pending[id]
	if ok && pending.resolving {
		q.mu.Unlock()
		return approvalFinishResult{state: approvalFinishResolving}
	}
	if ok {
		if decision != DecisionExpired && !at.Before(pending.approval.ExpiresAt) {
			decision = DecisionExpired
			actor = ApprovalActorState().Timeout
		}
		pending.resolving = true
	}
	q.mu.Unlock()
	if !ok {
		return approvalFinishResult{state: approvalFinishMissing}
	}
	resolution := ApprovalResolution{
		Approval: pending.approval,
		Decision: decision,
		Actor:    actor,
		At:       at,
	}
	var callbackErr error
	if q.OnResolved != nil {
		callbackErr = q.OnResolved(resolution)
	}
	result := approvalResult{resolution: resolution, err: callbackErr}
	q.mu.Lock()
	delete(q.pending, id)
	q.mu.Unlock()
	pending.result <- result
	return approvalFinishResult{state: approvalFinishApplied, result: result}
}
