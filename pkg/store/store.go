// Package store provides a generic data store interface and in-process
// synchronization primitives. IStore[D] places a pure data structure
// somewhere (memory, file, or database) and handles synchronization. The data
// structure itself has no locks or I/O; synchronization is the store's job.
package store

import (
	"context"
	"sync"
)

// IStore[D] is a generic store that places a data structure D somewhere and
// provides synchronized access. The backend determines the tier (in_process,
// kernel_io, host, network).
type IStore[D any] interface {
	// Load reads the current state.
	Load(ctx context.Context) (D, error)

	// Save writes state. The store owns synchronization; the caller does not
	// need to hold external locks.
	Save(ctx context.Context, data D) error
}

// MemoryStore[D] is an in-process, synchronized store that holds one value
// in memory. It is safe for concurrent access. The caller is responsible for
// cloning D on load if mutation isolation is required; the store itself
// performs no copying.
//
// CS-5 justification: The mutex is an honest leaf critical section guarding
// one store value. No coordinate state, no nested locks, no waiting on other
// components. Caller serializes all access through Load/Save methods.
type MemoryStore[D any] struct {
	mu    sync.Mutex
	value D
	saved bool
}

// NewMemoryStore creates a new empty in-process store.
func NewMemoryStore[D any]() *MemoryStore[D] {
	return &MemoryStore[D]{}
}

// Load returns a copy of the current state. If no value has been saved,
// it returns the zero value of D.
func (s *MemoryStore[D]) Load(ctx context.Context) (D, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value, nil
}

// Save atomically writes the value to the store. Concurrent loads see the
// new value after the call returns.
func (s *MemoryStore[D]) Save(ctx context.Context, data D) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = data
	s.saved = true
	return nil
}

// Saved reports whether Save has been called at least once.
func (s *MemoryStore[D]) Saved() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saved
}
