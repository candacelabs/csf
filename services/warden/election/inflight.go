package election

import "sync"

// inflightTracker counts in-flight outbound RPC worker goroutines. It backs
// two things: graceful shutdown (Run waits for the count to reach zero before
// returning, so no worker outlives the loop) and the deterministic test
// harness (which waits for outbound RPCs to quiesce). A condition variable is
// used rather than a sync.WaitGroup because callers wait for the count to
// reach zero repeatedly, mid-life, while new work may still be added.
type inflightTracker struct {
	mu   sync.Mutex
	cond *sync.Cond
	n    int
}

func newInflightTracker() *inflightTracker {
	t := &inflightTracker{}
	t.cond = sync.NewCond(&t.mu)
	return t
}

func (t *inflightTracker) add(delta int) {
	t.mu.Lock()
	t.n += delta
	t.mu.Unlock()
}

func (t *inflightTracker) done() {
	t.mu.Lock()
	t.n--
	if t.n == 0 {
		t.cond.Broadcast()
	}
	t.mu.Unlock()
}

// wait blocks until the in-flight count reaches zero.
func (t *inflightTracker) wait() {
	t.mu.Lock()
	for t.n > 0 {
		t.cond.Wait()
	}
	t.mu.Unlock()
}

// count returns the current in-flight count.
func (t *inflightTracker) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.n
}
