package copilotadapter

import "sync"

type sessionMutation struct {
	mutex      sync.Mutex
	references int
}

// mutationRegistry serializes one resource mutation. Entries are reference
// counted so completed resources do not accumulate process-local locks for the
// lifetime of the server.
type mutationRegistry[K comparable] struct {
	mutex   sync.Mutex
	entries map[K]*sessionMutation
}

func newMutationRegistry[K comparable]() *mutationRegistry[K] {
	return &mutationRegistry[K]{entries: make(map[K]*sessionMutation)}
}

func (registry *mutationRegistry[K]) lock(identifier K) func() {
	registry.mutex.Lock()
	mutation := registry.entries[identifier]
	if mutation == nil {
		mutation = &sessionMutation{}
		registry.entries[identifier] = mutation
	}
	mutation.references++
	registry.mutex.Unlock()

	mutation.mutex.Lock()
	return func() {
		mutation.mutex.Unlock()
		registry.mutex.Lock()
		defer registry.mutex.Unlock()
		mutation.references--
		if mutation.references == 0 {
			delete(registry.entries, identifier)
		}
	}
}
