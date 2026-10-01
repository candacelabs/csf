package grpcmux_test

import (
	"context"
	"sync"

	"github.com/candacelabs/csf/services/warden"
)

// fakeRPC is a warden.IRPCHandler that records the last decoded request and
// returns canned responses, so specs can assert both the decode (conversion)
// path and the response encoding. It can be told to panic to exercise the
// server's recovery -> Internal mapping. Guarded by a small mutex because
// handler goroutines and the test goroutine touch it concurrently; it is a test
// double, not the production single-owner state machine.
type fakeRPC struct {
	mu sync.Mutex

	voteResp  warden.VoteResponse
	hbResp    warden.HeartbeatResponse
	identResp warden.IdentifyResponse

	panicVote bool

	lastVote *warden.VoteRequest
	lastHB   *warden.HeartbeatRequest
}

func (f *fakeRPC) HandleVote(_ context.Context, req warden.VoteRequest) warden.VoteResponse {
	f.mu.Lock()
	f.lastVote = &req
	panicVote := f.panicVote
	resp := f.voteResp
	f.mu.Unlock()
	if panicVote {
		panic("fakeRPC: induced vote panic")
	}
	return resp
}

func (f *fakeRPC) HandleHeartbeat(_ context.Context, req warden.HeartbeatRequest) warden.HeartbeatResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastHB = &req
	return f.hbResp
}

func (f *fakeRPC) HandleIdentify(ctx context.Context) warden.IdentifyResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.identResp
}

func (f *fakeRPC) sawVote() *warden.VoteRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastVote
}

func (f *fakeRPC) sawHeartbeat() *warden.HeartbeatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastHB
}

// fakeViewSource is a controllable warden.IViewSource. It models the election
// manager's Subscribe contract: a fresh subscriber gets an immediate snapshot,
// delivery is best-effort (non-blocking send, dropped when the buffer is full),
// and cancel closes the channel. It additionally exposes the live subscription
// count so the teardown/leak spec can prove a stream released its subscription.
// A small mutex guards the shared maps — it is a test double.
type fakeViewSource struct {
	mu      sync.Mutex
	current warden.ClusterView
	subs    map[int]chan warden.ClusterView
	nextID  int
	active  int
}

func newFakeViewSource(initial warden.ClusterView) *fakeViewSource {
	return &fakeViewSource{current: initial, subs: map[int]chan warden.ClusterView{}}
}

func (f *fakeViewSource) View() warden.ClusterView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current
}

func (f *fakeViewSource) Subscribe(buf int) (<-chan warden.ClusterView, func()) {
	if buf < 0 {
		buf = 0
	}
	f.mu.Lock()
	id := f.nextID
	f.nextID++
	ch := make(chan warden.ClusterView, buf)
	f.subs[id] = ch
	f.active++
	// Immediate snapshot, mirroring Manager.onSubscribe.
	select {
	case ch <- f.current:
	default:
	}
	f.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			f.mu.Lock()
			if _, ok := f.subs[id]; ok {
				delete(f.subs, id)
				f.active--
				close(ch)
			}
			f.mu.Unlock()
		})
	}
	return ch, cancel
}

// setView advances the current snapshot without signalling subscribers, so a
// later signal delivers the latest state (used by the drop-to-latest spec).
func (f *fakeViewSource) setView(v warden.ClusterView) {
	f.mu.Lock()
	f.current = v
	f.mu.Unlock()
}

// signal pushes a bare change signal carrying payload to every subscriber
// (best-effort), WITHOUT changing the current view. Consumers re-read View(), so
// the payload is deliberately ignorable — sending a stale payload here proves
// the handler delivers current state, not the signal's payload.
func (f *fakeViewSource) signal(payload warden.ClusterView) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.subs {
		select {
		case ch <- payload:
		default:
		}
	}
}

// publish is the common case: advance current AND signal every subscriber.
func (f *fakeViewSource) publish(v warden.ClusterView) {
	f.setView(v)
	f.signal(v)
}

// closeAll models the election loop shutting down: every subscription channel
// is closed.
func (f *fakeViewSource) closeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, ch := range f.subs {
		delete(f.subs, id)
		f.active--
		close(ch)
	}
}

func (f *fakeViewSource) activeSubs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}
