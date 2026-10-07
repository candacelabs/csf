// Copyright 2026 Candace Labs

package inproc

import (
	"errors"
	"slices"
	"sync"
)

// ErrNoCapacity reports a Recent built to hold nothing.
var ErrNoCapacity = errors.New("inproc: a recent map needs a capacity of at least one")

// Recent is a bounded map of the values most recently stored, shared by any
// number of goroutines: storing a key past its capacity forgets the key
// stored longest ago. It is for state a process keeps for a while on behalf
// of callers it cannot enumerate, such as one value per browser, where an
// unbounded map would grow with every caller that ever came.
//
// The lock guards one datum, the entries and their order; every section under
// it is a single step and nobody waits on it, so it is a leaf (house rule
// CS-5).
type Recent[K comparable, V any] struct {
	guard    sync.Mutex
	capacity int
	values   map[K]V
	// order is the keys from stored longest ago to most recently.
	order []K
}

// NewRecent returns an empty map holding at most capacity keys.
func NewRecent[K comparable, V any](capacity int) (*Recent[K, V], error) {
	if capacity < 1 {
		return nil, ErrNoCapacity
	}
	return &Recent[K, V]{capacity: capacity, values: make(map[K]V, capacity)}, nil
}

// Load returns the value stored under key and whether there is one.
func (recent *Recent[K, V]) Load(key K) (V, bool) {
	recent.guard.Lock()
	defer recent.guard.Unlock()
	value, found := recent.values[key]
	return value, found
}

// Store sets key's value and makes it the most recently stored, forgetting
// the key stored longest ago when the map is full.
func (recent *Recent[K, V]) Store(key K, value V) {
	recent.guard.Lock()
	defer recent.guard.Unlock()
	if _, found := recent.values[key]; found {
		recent.order = slices.DeleteFunc(recent.order, func(stored K) bool { return stored == key })
	} else if len(recent.order) == recent.capacity {
		delete(recent.values, recent.order[0])
		recent.order = recent.order[1:]
	}
	recent.values[key] = value
	recent.order = append(recent.order, key)
}

// Len is how many keys the map holds.
func (recent *Recent[K, V]) Len() int {
	recent.guard.Lock()
	defer recent.guard.Unlock()
	return len(recent.order)
}
