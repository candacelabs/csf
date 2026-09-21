package copilotadapter

import (
	"context"
	"sync"
)

// WorkspaceSubscription coalesces committed changes into a request to reload
// current state. Subscribe before reading the initial snapshot so a commit
// during that read remains pending. Close releases the subscription; it starts
// no goroutine and is safe to call repeatedly.
type WorkspaceSubscription struct {
	Changed <-chan struct{}
	Close   func()
}

// workspaceChanges guards only the subscriber set. Each channel retains at
// most one invalidation; slow browsers cannot block a committing transaction.
// The notification carries no state, so coalescing cannot discard a newer value.
type workspaceChanges struct {
	mu          sync.Mutex
	subscribers map[chan struct{}]struct{}
}

func (changes *workspaceChanges) subscribe() WorkspaceSubscription {
	channel := make(chan struct{}, 1)
	changes.mu.Lock()
	if changes.subscribers == nil {
		changes.subscribers = make(map[chan struct{}]struct{})
	}
	changes.subscribers[channel] = struct{}{}
	changes.mu.Unlock()
	return WorkspaceSubscription{Changed: channel, Close: func() {
		changes.mu.Lock()
		defer changes.mu.Unlock()
		delete(changes.subscribers, channel)
	}}
}

func (changes *workspaceChanges) publish() {
	changes.mu.Lock()
	defer changes.mu.Unlock()
	for subscriber := range changes.subscribers {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}

// notifyingStore preserves the SQLC contract and adds one post-commit signal.
// Notifications describe writes made through this adapter; direct SQL writers
// must call InvalidateWorkspace after their transaction commits.
type notifyingStore struct {
	IStore
	changes *workspaceChanges
}

func (store *notifyingStore) Transact(ctx context.Context, operation StoreTransaction) error {
	if err := store.IStore.Transact(ctx, operation); err != nil {
		return err
	}
	store.changes.publish()
	return nil
}

// SubscribeWorkspace observes committed adapter writes in this process.
// The caller owns the returned subscription and must close it on cancellation.
func (adapter *CopilotAdapter) SubscribeWorkspace() WorkspaceSubscription {
	return adapter.changes.subscribe()
}

// InvalidateWorkspace invalidates snapshots after an external source is ingested.
// It does not itself fetch GitHub state or infer task completion.
func (adapter *CopilotAdapter) InvalidateWorkspace() {
	adapter.changes.publish()
}
