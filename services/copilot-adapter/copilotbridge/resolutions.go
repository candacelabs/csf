package copilotbridge

import (
	"context"
	"errors"
	"sync"

	"github.com/github/copilot-sdk/go/rpc"
	"github.com/google/uuid"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
)

var errResolutionAbandoned = errors.New("copilot bridge: the request was abandoned before the operator decision was acknowledged")

var errResolutionNotApplied = errors.New("copilot bridge: the SDK did not apply the permission decision")

var errResolutionConflict = errors.New("copilot bridge: request already received a different resolution")

var errResolutionMissing = errors.New("copilot bridge: no pending request with that id")

var errResolutionHandlerUnavailable = errors.New("copilot bridge: permission resolution is not connected to the SDK session")

type permissionResolutionHandler func(ctx context.Context, request *rpc.PermissionDecisionRequest) (*rpc.PermissionRequestResult, error)

type resolutionAttempt struct {
	resolution copilotadapter.BridgeResolution
	done       chan struct{}
	err        error
}

type resolutionEntry struct {
	sdkRequestID       string
	delivered          *copilotadapter.BridgeResolution
	attempt            *resolutionAttempt
	completionObserved bool
	acknowledged       bool
	abandoned          bool
}

// resolutionRegistry owns the exact adapter-request to SDK-request mapping.
// It serializes concurrent retries around the one synchronous SDK RPC and
// retains a successful result until the database commit is acknowledged.
type resolutionRegistry struct {
	mutex    sync.Mutex
	entries  map[uuid.UUID]*resolutionEntry
	bySDK    map[string]uuid.UUID
	handle   permissionResolutionHandler
	stopped  bool
	stopOnce sync.Once
}

func newResolutionRegistry() *resolutionRegistry {
	return &resolutionRegistry{
		entries: map[uuid.UUID]*resolutionEntry{},
		bySDK:   map[string]uuid.UUID{},
	}
}

func (registry *resolutionRegistry) bind(handler permissionResolutionHandler) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.stopped {
		return
	}
	registry.handle = handler
}

// open registers one SDK permission event occurrence. Returning false means
// the exact occurrence was already observed; callers must not emit it twice.
func (registry *resolutionRegistry) open(identifier uuid.UUID, sdkRequestID string) (bool, error) {
	if identifier == uuid.Nil || sdkRequestID == "" {
		return false, errResolutionMissing
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.stopped {
		return false, errResolutionAbandoned
	}
	if existing, found := registry.entries[identifier]; found {
		if existing.sdkRequestID != sdkRequestID {
			return false, errResolutionConflict
		}
		return false, nil
	}
	if existing, found := registry.bySDK[sdkRequestID]; found && existing != identifier {
		return false, errResolutionConflict
	}
	registry.entries[identifier] = &resolutionEntry{sdkRequestID: sdkRequestID}
	registry.bySDK[sdkRequestID] = identifier
	return true, nil
}

// resolve applies an operator decision to the exact pending SDK request. A
// false Success result is a failed delivery and deliberately remains retryable.
func (registry *resolutionRegistry) resolve(ctx context.Context, resolution copilotadapter.BridgeResolution) error {
	if resolution.Decision != "approve" && resolution.Decision != "deny" {
		return errResolutionConflict
	}
	for {
		registry.mutex.Lock()
		if registry.stopped {
			registry.mutex.Unlock()
			return errResolutionAbandoned
		}
		entry, found := registry.entries[resolution.RequestID]
		if !found || entry.abandoned {
			registry.mutex.Unlock()
			return errResolutionMissing
		}
		if entry.delivered != nil {
			conflict := entry.delivered.Decision != resolution.Decision
			registry.mutex.Unlock()
			if conflict {
				return errResolutionConflict
			}
			return nil
		}
		if entry.attempt != nil {
			attempt := entry.attempt
			if attempt.resolution.Decision != resolution.Decision {
				registry.mutex.Unlock()
				return errResolutionConflict
			}
			registry.mutex.Unlock()
			select {
			case <-attempt.done:
				return attempt.err
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if registry.handle == nil {
			registry.mutex.Unlock()
			return errResolutionHandlerUnavailable
		}
		attempt := &resolutionAttempt{resolution: resolution, done: make(chan struct{})}
		entry.attempt = attempt
		handler := registry.handle
		sdkRequestID := entry.sdkRequestID
		registry.mutex.Unlock()

		result, err := handler(ctx, permissionDecisionRequest(sdkRequestID, resolution.Decision))
		if err == nil && (result == nil || !result.Success) {
			err = errResolutionNotApplied
		}

		registry.mutex.Lock()
		current, stillOpen := registry.entries[resolution.RequestID]
		if !stillOpen || current != entry || entry.abandoned || registry.stopped {
			err = errResolutionAbandoned
		} else if entry.delivered != nil {
			// PermissionCompleted is the SDK's authoritative outcome and may
			// arrive before the direct RPC response. A matching completion proves
			// delivery even when that response is false or transport-ambiguous.
			if entry.delivered.Decision != resolution.Decision {
				err = errResolutionConflict
			}
			if entry.delivered.Decision == resolution.Decision {
				err = nil
			}
		} else if err == nil {
			applied := resolution
			entry.delivered = &applied
		}
		entry.attempt = nil
		attempt.err = err
		close(attempt.done)
		if err != nil && !entry.abandoned {
			// The mapping remains pending, so the same durable delivery may retry.
			entry.acknowledged = false
		}
		if entry.abandoned || (entry.acknowledged && entry.delivered != nil) {
			registry.deleteLocked(resolution.RequestID, entry)
		}
		registry.mutex.Unlock()
		return err
	}
}

func permissionDecisionRequest(sdkRequestID string, decision string) *rpc.PermissionDecisionRequest {
	decisionContext := &rpc.PermissionDecisionContext{
		Outcome: rpc.PermissionDecisionOutcomePromptedUser,
		Source:  rpc.PermissionDecisionSourceHumanResponse,
		Surface: rpc.PermissionDecisionSurfaceSDK,
	}
	var result rpc.PermissionDecision = &rpc.PermissionDecisionReject{}
	if decision == "approve" {
		result = &rpc.PermissionDecisionApproveOnce{ApprovedInteractively: boolPointer(true)}
	}
	return &rpc.PermissionDecisionRequest{RequestID: sdkRequestID, Result: result, DecisionContext: decisionContext}
}

func boolPointer(value bool) *bool {
	return &value
}

// complete records a permission.completed event from this or another client.
// The adapter projects the returned request before acknowledging this state.
func (registry *resolutionRegistry) complete(sdkRequestID string, decision string) (uuid.UUID, bool) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.stopped {
		return uuid.Nil, false
	}
	identifier, found := registry.bySDK[sdkRequestID]
	if !found {
		return uuid.Nil, false
	}
	entry, found := registry.entries[identifier]
	if !found || entry.abandoned || entry.completionObserved {
		return uuid.Nil, false
	}
	entry.completionObserved = true
	if entry.delivered == nil {
		resolution := copilotadapter.BridgeResolution{RequestID: identifier, Decision: decision}
		entry.delivered = &resolution
	}
	return identifier, true
}

// acknowledge forgets retry state only after the adapter committed the final
// delivered row. An in-flight RPC retains the entry until its caller observes
// the SDK result, preventing completion-event ordering from creating a false
// "no pending request" failure.
func (registry *resolutionRegistry) acknowledge(identifier uuid.UUID) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	entry, found := registry.entries[identifier]
	if !found {
		return
	}
	if entry.attempt != nil {
		entry.acknowledged = true
		return
	}
	registry.deleteLocked(identifier, entry)
}

func (registry *resolutionRegistry) abandon(identifier uuid.UUID) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	entry, found := registry.entries[identifier]
	if !found {
		return
	}
	entry.abandoned = true
	if current, mapped := registry.bySDK[entry.sdkRequestID]; mapped && current == identifier {
		delete(registry.bySDK, entry.sdkRequestID)
	}
	if entry.attempt == nil {
		delete(registry.entries, identifier)
	}
}

func (registry *resolutionRegistry) deleteLocked(identifier uuid.UUID, entry *resolutionEntry) {
	delete(registry.entries, identifier)
	if current, found := registry.bySDK[entry.sdkRequestID]; found && current == identifier {
		delete(registry.bySDK, entry.sdkRequestID)
	}
}

func (registry *resolutionRegistry) stop() {
	registry.stopOnce.Do(func() {
		registry.mutex.Lock()
		defer registry.mutex.Unlock()
		registry.stopped = true
		registry.entries = map[uuid.UUID]*resolutionEntry{}
		registry.bySDK = map[string]uuid.UUID{}
		registry.handle = nil
	})
}
