// Package noteboard is this repository's own service.
//
// It is the shape an extending product actually ships, rather than a fixture:
// it owns a bounded ledger with rules of its own, it joins Core's bring-up
// graph as a component that requires another of this repository's components,
// and it serves the operator page the sidebar entry points at. Core constructs
// none of it, reads none of its configuration, and hands it no Core state —
// Core owns the order it is brought up in and the engine its page is mounted
// on, and nothing else.
//
// The board reads the steering inputs the harness observed and keeps them as
// field notes. That crossing is the point: the input arrives at a service this
// repository composed, through an interface this repository declared, in an
// order Core resolved from a dependency this repository named.
package noteboard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/candacelabs/csf/services/candaceos/component"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

// Capacity bounds the notes the board retains. The oldest is discarded first;
// sequence numbers keep counting past a discarded note, so the page can say
// which note it is showing rather than only how many are left.
const Capacity = 24

// ComponentName is the name this service registers under. Core namespaces the
// component's diagnostics with it, and the resolver reports it by name when an
// order cannot be produced.
const ComponentName = "noteboard"

var (
	// ErrNoSteering reports a board constructed without a steering source.
	ErrNoSteering = errors.New("noteboard: a steering source is required")
	// ErrUnassembled reports a start attempted before Core assembled the board.
	ErrUnassembled = errors.New("noteboard: the board is not assembled")
)

// ISteering is the part of a steering service this board reads. It is declared
// here rather than imported so the ledger's rules can be exercised without a
// component graph, and so the service that satisfies it can be replaced without
// this package knowing.
type ISteering interface {
	// Observed returns every steering input recorded so far, oldest first.
	Observed() []string
}

// Note is one line of the ledger: what was steered, and which note it is.
type Note struct {
	// Sequence is the note's position in everything the board has ever
	// recorded, starting at 1. It keeps counting past a discarded note.
	Sequence int
	// Text is the steering input with its surrounding whitespace removed.
	Text string
}

// Board is the service. It is safe for concurrent use: Core starts it on one
// goroutine and the engine serves its page on others.
type Board struct {
	mutex    sync.Mutex
	steering ISteering
	brand    webui.Brand
	capacity int
	// considered counts the steering inputs already folded in, so re-reading
	// the steering service cannot record one of them twice.
	considered int
	recorded   int
	notes      []Note
	running    bool
}

// New returns the board this repository composes.
//
// The brand is the same value the composition root hands Core, resolved once
// here so the page cannot render a different identity than the shell beside it.
// An invalid brand fails here rather than at the first request.
func New(source ISteering, brand webui.Brand) (*Board, error) {
	if source == nil {
		return nil, ErrNoSteering
	}
	if err := brand.Validate(); err != nil {
		return nil, fmt.Errorf("noteboard: %w", err)
	}
	return &Board{steering: source, brand: brand.Resolved()}, nil
}

// Component returns the definition that brings the board up. Assembly
// establishes the whole ledger — its bound and its counters — so a board Core
// assembles is one that has recorded nothing yet.
//
// It requires the steering service by pointer identity, so Core assembles and
// starts the steering store and the steering service before this board and
// stops it before either of them. That edge is the whole reason the board can
// read steering inputs at all: an input observed before the store exists is
// dropped by the service rather than half-recorded here.
func (board *Board) Component(steeringComponent *component.Definition) (*component.Definition, error) {
	return component.New(
		ComponentName,
		component.WithRequires(steeringComponent),
		component.WithAssemble(func(ctx context.Context, capabilities component.ICapabilities) error {
			board.mutex.Lock()
			board.capacity = Capacity
			board.notes = make([]Note, 0, Capacity)
			board.considered, board.recorded = 0, 0
			board.mutex.Unlock()
			return capabilities.Log(
				ctx,
				"assembled",
				fmt.Sprintf("field notes retain %d entries", Capacity),
			)
		}),
		component.WithStart(func(ctx context.Context) error {
			board.mutex.Lock()
			defer board.mutex.Unlock()
			if board.capacity == 0 {
				return ErrUnassembled
			}
			board.running = true
			return ctx.Err()
		}),
		component.WithStop(func(ctx context.Context) error {
			board.mutex.Lock()
			defer board.mutex.Unlock()
			board.running = false
			return nil
		}),
	)
}

// Ledger is one internally consistent read of the board.
type Ledger struct {
	// Running reports whether Core has started the board. A page renders its
	// standing-by state rather than an empty list when it has not: those are
	// different facts, and an operator looking at a blank page deserves to know
	// which one they are looking at.
	Running bool

	// Notes are the retained notes, oldest first.
	Notes []Note
}

// Read folds in every steering input the board has not considered yet and
// returns one view of the result.
//
// It is one method rather than two accessors because the page renders both
// halves: a reader that asked whether the board was running and then asked for
// its notes could be told about a running board and handed a stopped one's
// ledger.
//
// Two rules of this product's own live here. An input identical to the newest
// retained note is a retry rather than a new note, so it is counted as
// considered and dropped. And a board Core has not started folds nothing: a
// half-composed product shows no ledger at all rather than one that starts
// mid-history.
func (board *Board) Read() Ledger {
	board.mutex.Lock()
	defer board.mutex.Unlock()
	if !board.running {
		return Ledger{}
	}
	board.fold(board.steering.Observed())
	return Ledger{Running: true, Notes: append([]Note(nil), board.notes...)}
}

// fold records the steering inputs past the ones already considered. The caller
// holds the lock.
func (board *Board) fold(observed []string) {
	if len(observed) <= board.considered {
		// The steering service is bounded too, so its window can shrink out
		// from under a board that read it. Nothing here can be recovered from
		// that, and re-reading the whole window would duplicate every note, so
		// the count is clamped and the board carries on.
		board.considered = len(observed)
		return
	}
	for _, input := range observed[board.considered:] {
		board.considered++
		text := strings.TrimSpace(input)
		if text == "" {
			continue
		}
		if len(board.notes) > 0 && board.notes[len(board.notes)-1].Text == text {
			continue
		}
		board.recorded++
		board.notes = append(board.notes, Note{Sequence: board.recorded, Text: text})
		if len(board.notes) > board.capacity {
			board.notes = board.notes[len(board.notes)-board.capacity:]
		}
	}
}
