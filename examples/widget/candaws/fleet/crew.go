package fleet

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
)

// ErrAlreadyRunning is a second call to an engine's Run.
//
// One engine runs once: its goroutines are started against the context Run was
// given, so a second call would start a second set against a second context and
// leave two of everything writing into one set of channels.
var ErrAlreadyRunning = errors.New("candaws: this engine is already running")

// Once is a start token that can be taken exactly once.
//
// It is a channel holding one value rather than a boolean behind a lock, because
// a token that can be taken once is the whole of what "run once" means and a
// channel already is one.
type Once struct {
	token chan struct{}
}

// NewOnce mints an untaken token.
func NewOnce() Once {
	token := make(chan struct{}, 1)
	token <- struct{}{}
	return Once{token: token}
}

// Take reports whether this caller is the one that got the token.
func (once Once) Take() bool {
	select {
	case <-once.token:
		return true
	default:
		return false
	}
}

// Crew is a set of goroutines that live and die with one context.
//
// The [sync.WaitGroup] is the one in this package and it is a leaf counter: it
// counts goroutines that have returned and guards no protocol state. Everything
// those goroutines say to each other, they say on channels.
type Crew struct {
	running sync.WaitGroup
}

// Go starts one goroutine against the crew's context.
func (crew *Crew) Go(ctx context.Context, goroutine func(ctx context.Context)) {
	crew.running.Add(1)
	go func() {
		defer crew.running.Done()
		goroutine(ctx)
	}()
}

// Wait returns when every goroutine the crew started has returned.
//
// An engine's Run blocks on it, so a caller that waits for Run knows the
// goroutines are actually gone rather than merely asked to go — which is what
// makes a specification's cleanup a real join instead of a hope.
func (crew *Crew) Wait() { crew.running.Wait() }

// Jitter is one goroutine's own random stream, seeded from an engine's seed and
// that goroutine's index.
//
// Per-goroutine rather than shared: a package-level source would make two
// engines in one test binary perturb each other, and a source behind a lock
// would be the one piece of shared mutable state in a package whose whole claim
// is that it has none. Equal seeds give equal sequences; they do not give an
// equal scheduler, so an engine is reproducible in its delays and not in its
// interleavings.
func Jitter(seed int64, index uint64) *rand.Rand {
	return rand.New(rand.NewPCG(uint64(seed), index))
}
