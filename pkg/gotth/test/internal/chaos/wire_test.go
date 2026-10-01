package chaos_test

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// The server under test
// ---------------------------------------------------------------------------

// standing is one live application behind a real HTTP server on a real
// loopback socket.
type standing struct {
	app    *live.App[board, chaosUser]
	http   *httptest.Server
	ledger *ledger
}

// serve mounts chaosConfig, optionally mutated, on a real listener.
func serve(mutate func(cfg *live.Config[board, chaosUser])) *standing {
	GinkgoHelper()

	led := newLedger()
	cfg := chaosConfig(led)
	cfg.Logger = discardLogger()
	if mutate != nil {
		mutate(&cfg)
	}

	app, err := live.New(cfg)
	Expect(err).NotTo(HaveOccurred())

	ts := httptest.NewServer(app.Handler())
	s := &standing{app: app, http: ts, ledger: led}
	DeferCleanup(s.stop)
	return s
}

func (s *standing) stop() {
	if s.app != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.app.Close(ctx)
	}
	if s.http != nil {
		s.http.Close()
	}
}

// addr is the host:port a relay dials.
func (s *standing) addr() string { return strings.TrimPrefix(s.http.URL, "http://") }

func wsURL(hostPort string) string { return "ws://" + hostPort }

// ---------------------------------------------------------------------------
// The wire client
// ---------------------------------------------------------------------------

// ackMode is what a client does with the patches it receives.
type ackMode int

const (
	// ackAuto acknowledges every applied frame immediately: a well-behaved
	// client, and the default.
	ackAuto ackMode = iota
	// ackNever acknowledges nothing after the first snapshot, which is how a
	// spec drives the outbound window to its bound without touching TCP.
	ackNever
	// ackDelayed acknowledges after a fixed pause, standing in for a client on
	// a high-latency link.
	ackDelayed
)

type wireOpts struct {
	acks ackMode
	// ackAfter is the pause ackDelayed waits.
	ackAfter time.Duration
	// resyncOnGap makes the client behave as FR-11 requires of one: stop
	// applying and ask for a snapshot. Off by default so that a spec can watch
	// a gap go undetected on purpose.
	//
	// On its own it models the runtime as it was BEFORE D-29 was closed: a
	// request per gap, latched until the answering Snapshot, and nothing at all
	// on a refusal. That is deliberately still reachable, because it is the
	// pre-fix cost D-29's characterisation spec is about.
	resyncOnGap bool
	// resyncRetryOnRefusal adds the D-29 re-arm the shipped runtime gained in
	// c3a91af8, so that a spec can put the FIXED client's behaviour in front of
	// the real server:
	//
	//	* Error{RATE_LIMITED} re-arms the REQUEST and not the detector: the gap
	//	  latch stays set, and one retry is armed.
	//	* the schedule is equal jitter — bound/2 + random(0, bound/2) over
	//	  bound = min(15 s, 1000 ms x 2^n) — with at most one retry in flight
	//	  per gap and n the count of consecutive refusals for THIS gap.
	//	* a patch discarded because of the gap is still acknowledged, at the
	//	  sequence the client actually holds.
	//
	// It is a Go transcription of client/runtime.js's refused()/ask() and of
	// its patch branch, and it is not a second opinion about them: the shipped
	// runtime's own behaviour is DEV-2's and client/test/resync.test.mjs holds
	// fourteen specs for it. What this flag buys is the half no JS harness can
	// answer — whether the REAL server's resync bucket, its denial counter and
	// its slow-client eviction admit that schedule, or whether the retry is
	// served late, refused into 4008, or evicted anyway.
	resyncRetryOnRefusal bool
	// dropPatchAt makes the client discard the Nth patch it receives WITHOUT
	// acking or applying it, which injects a sequence gap on the client side —
	// the FR-11 trigger, produced by loss rather than by a forged frame.
	dropPatchAt int
	// lossPercent drops that percentage of inbound sequenced frames, using a
	// deterministic counter rather than a random source so a measurement is
	// reproducible.
	lossPercent int
	// silent stops the client echoing the server's heartbeats. It is the
	// opt-out from looking alive, for the specs that need a peer the server
	// must conclude is dead. Default OFF, because the shipped runtime echoes
	// (client/runtime.js: `send({ heartbeat: {...} })`) and a harness that did
	// not would have every session closed with 4010 one heartbeat timeout after
	// its last patch — which is exactly what this suite hit before the echo
	// landed here.
	silent bool
	// dropHeartbeatEchoAt makes the client skip the echo of the Nth heartbeat
	// it is sent, 1-based, and echo every other one. Zero means echo them all.
	//
	// It is the "one lost solicitation" D-30's fix is sized for: RFC-0001's
	// liveness is the Heartbeat frame and not an RFC 6455 ping, so a quiet
	// session's only inbound frame is that echo, and a HeartbeatTimeout of one
	// interval is satisfiable exactly while nothing is ever lost. Distinct from
	// silent, which drops every echo and is how a spec builds a peer the server
	// must conclude is dead.
	dropHeartbeatEchoAt int
	// noCapture stops the client retaining every frame it decodes. The flood
	// specs need it: the client and the server share one process, so a
	// harness that kept a hundred thousand Error frames would be the thing the
	// heap measurement measured.
	noCapture bool
}

// wire is a hand-written protocol client: one connection, a read pump, and the
// bookkeeping FR-11 requires of a client (contiguous sequence, gap detection,
// cumulative acknowledgement).
//
// It is deliberately NOT the shipped runtime. The shipped runtime's own
// behaviour is DEV-2's and is covered by client/test/reconnect.test.mjs; what
// this type exists for is to hold one end of the wire steady while the server
// is put under a fault, and to be able to misbehave in ways the real client
// never would.
type wire struct {
	conn *websocket.Conn
	ctx  context.Context
	opts wireOpts

	sessionID []byte
	snapshot  *pb.Snapshot

	incoming chan *pb.Frame
	closed   chan struct{}

	mu     sync.Mutex
	frames []*pb.Frame
	raw    [][]byte
	seq    uint64 // highest contiguous server_seq applied
	gaps   int
	// gapPending latches a requested resync until the answering Snapshot
	// arrives, which is what the shipped runtime does (runtime.js resync():
	// "if (gap || !seq) return"). Without the latch every frame after a gap
	// would produce another request, and a measurement of how often a
	// LEGITIMATE client is rate-limited would be measuring an illegitimate one.
	gapPending bool
	resyncs    int
	dropped    int
	patchSeen  int
	ref        uint64

	// The D-29 re-arm's state, live only under resyncRetryOnRefusal. gapTries
	// is the n in the schedule and is reset where runtime.js resets it — when
	// resync() latches the NEXT gap, not when a Snapshot lands, so that a
	// Snapshot arriving while a retry is armed disarms the retry without also
	// forgiving the refusals that produced it. gapTimer is the one armed retry,
	// nil when none: at most one request is in flight per gap, ever.
	gapTries int
	gapTimer *time.Timer
	retries  int

	// hbSeen counts the heartbeats the server has sent, for
	// dropHeartbeatEchoAt.
	hbSeen int

	// Counted rather than retained, so the flood specs have their numbers
	// without their capture.
	errorsSeen  atomic.Int64
	rateLimited atomic.Int64
	framesIn    atomic.Int64
	bytesIn     atomic.Int64

	closeCode atomic.Int64
	readErr   atomic.Pointer[error]
}

// dialWire connects to addr and consumes the first Snapshot.
func dialWire(addr string, opts wireOpts) *wire {
	GinkgoHelper()
	w, err := dialWireErr(addr, opts)
	Expect(err).NotTo(HaveOccurred())
	return w
}

func dialWireErr(addr string, opts wireOpts) (*wire, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	DeferCleanup(cancel)

	headers := http.Header{}
	headers.Set("Origin", chaosOrigin)
	conn, _, err := websocket.Dial(ctx, wsURL(addr), &websocket.DialOptions{
		HTTPHeader:   headers,
		Subprotocols: []string{"gotth-live.v1"},
	})
	if err != nil {
		return nil, err
	}
	// The read limit has to be generous: a resync Snapshot carries every
	// fragment, and the flood specs deliberately push large frames.
	conn.SetReadLimit(8 << 20)

	w := &wire{
		conn:     conn,
		ctx:      ctx,
		opts:     opts,
		incoming: make(chan *pb.Frame, 8192),
		closed:   make(chan struct{}),
	}
	w.closeCode.Store(-1)
	go w.pump()
	DeferCleanup(w.stop)

	f, err := w.next(10 * time.Second)
	if err != nil {
		return nil, err
	}
	if f.GetSnapshot() == nil {
		if e := f.GetError(); e != nil {
			return nil, fmt.Errorf("chaos: the first frame was an Error rather than a Snapshot (H-10): %s: %s",
				e.GetCode(), e.GetMessage())
		}
		return nil, fmt.Errorf("chaos: the first frame was not a Snapshot (H-10): %T", f.GetPayload())
	}
	w.snapshot = f.GetSnapshot()
	return w, nil
}

func (w *wire) stop() {
	// An armed D-29 retry outlives the socket otherwise, and would fire into a
	// closed connection after the spec that owned it had finished — which is
	// the harness's own version of the leak case 7 is about.
	w.mu.Lock()
	if w.gapTimer != nil {
		w.gapTimer.Stop()
		w.gapTimer = nil
	}
	w.gapPending = false
	w.mu.Unlock()
	if w.conn != nil {
		_ = w.conn.CloseNow()
	}
}

// pump reads for the life of the connection. It is the only caller of Read, so
// no spec can kill the socket by timing out on one.
func (w *wire) pump() {
	defer close(w.incoming)
	defer close(w.closed)
	for {
		typ, data, err := w.conn.Read(w.ctx)
		if err != nil {
			w.closeCode.Store(int64(websocket.CloseStatus(err)))
			w.readErr.Store(&err)
			return
		}
		if typ != websocket.MessageBinary {
			err := fmt.Errorf("chaos: a non-binary message arrived: %v", typ)
			w.readErr.Store(&err)
			return
		}

		var f pb.Frame
		if err := proto.Unmarshal(data, &f); err != nil {
			err = fmt.Errorf("chaos: a captured payload is not a Frame: %w", err)
			w.readErr.Store(&err)
			return
		}

		w.framesIn.Add(1)
		w.bytesIn.Add(int64(len(data)))
		if e := f.GetError(); e != nil {
			w.errorsSeen.Add(1)
			if e.GetCode() == pb.ErrorCode_RATE_LIMITED {
				w.rateLimited.Add(1)
				if w.opts.resyncRetryOnRefusal {
					w.refused()
				}
			}
		}

		w.mu.Lock()
		if w.sessionID == nil {
			// Learned from the first frame rather than assigned by the dialler,
			// because handle() answers that frame with an acknowledgement on
			// THIS goroutine — before the dialler has returned. Setting it
			// afterwards made every session's first Ack carry an empty
			// session_id, which is an H-3 violation and closed the connection
			// with 4002 before any spec had sent anything.
			w.sessionID = f.GetSessionId()
		}
		if !w.opts.noCapture {
			w.raw = append(w.raw, data)
			w.frames = append(w.frames, &f)
		}
		w.mu.Unlock()

		w.handle(&f)

		select {
		case w.incoming <- &f:
		default:
			// The channel is a spec's view, not the protocol's. Dropping here
			// keeps a flood spec from being bounded by its own buffer; the
			// captured slice above still holds everything.
		}
	}
}

// handle applies the sequenced-frame bookkeeping FR-11 puts on a client, and
// the heartbeat echo that keeps the session's liveness check satisfied.
func (w *wire) handle(f *pb.Frame) {
	if hb := f.GetHeartbeat(); hb != nil {
		w.mu.Lock()
		w.hbSeen++
		n := w.hbSeen
		w.mu.Unlock()
		if w.opts.silent || (w.opts.dropHeartbeatEchoAt > 0 && n == w.opts.dropHeartbeatEchoAt) {
			return
		}
		_ = w.send(w.envelope(&pb.Heartbeat{
			Nonce: hb.GetNonce(), IntervalMs: hb.GetIntervalMs(),
		}))
		return
	}

	var seq uint64
	switch {
	case f.GetPatch() != nil:
		seq = f.GetPatch().GetServerSeq()
		w.mu.Lock()
		w.patchSeen++
		n := w.patchSeen
		w.mu.Unlock()

		if w.opts.dropPatchAt > 0 && n == w.opts.dropPatchAt {
			w.mu.Lock()
			w.dropped++
			w.mu.Unlock()
			return
		}
		if w.opts.lossPercent > 0 && (n*w.opts.lossPercent)/100 > ((n-1)*w.opts.lossPercent)/100 {
			w.mu.Lock()
			w.dropped++
			w.mu.Unlock()
			return
		}
	case f.GetSnapshot() != nil:
		seq = f.GetSnapshot().GetServerSeq()
	default:
		return
	}

	w.mu.Lock()
	expected := w.seq + 1
	if f.GetSnapshot() != nil {
		// A Snapshot re-establishes the sequence: on a new connection it starts
		// at 1, and a resync Snapshot supersedes whatever was missed. It also
		// re-arms the gap detector.
		w.seq = seq
		w.gapPending = false
		// runtime.js applied(): a Snapshot that lands while a retry is armed
		// disarms it, or the request would go out for a gap that has just been
		// closed. gapTries is deliberately NOT reset here — requestResync does
		// it when it latches the next gap.
		if w.gapTimer != nil {
			w.gapTimer.Stop()
			w.gapTimer = nil
		}
		w.mu.Unlock()
		w.sendAck(seq)
		return
	}
	if seq != expected {
		w.gaps++
		held := w.seq
		w.mu.Unlock()
		if w.opts.resyncOnGap {
			w.requestResync()
		}
		// The D-29 fix's second half: the discarded patch is still
		// acknowledged, at the sequence the client actually holds rather than
		// the one it just refused to apply. It stops a latched client being
		// indistinguishable from a dead one, and it is legal under H-7 — never
		// backwards, never a patch that was not sent.
		if w.opts.resyncRetryOnRefusal {
			w.sendAck(held)
		}
		return
	}
	w.seq = seq
	w.mu.Unlock()
	w.sendAck(seq)
}

func (w *wire) sendAck(seq uint64) {
	switch w.opts.acks {
	case ackNever:
		return
	case ackDelayed:
		go func() {
			select {
			case <-time.After(w.opts.ackAfter):
			case <-w.closed:
				return
			}
			_ = w.send(w.envelope(&pb.Ack{ServerSeq: seq}))
		}()
	default:
		_ = w.send(w.envelope(&pb.Ack{ServerSeq: seq}))
	}
}

// requestResync is the FR-11 recovery a client owes: stop applying, ask for a
// snapshot naming the last sequence it did apply.
func (w *wire) requestResync() {
	w.mu.Lock()
	if w.gapPending {
		w.mu.Unlock()
		return
	}
	w.gapPending = true
	// The schedule is per gap: latching a new one starts at the base again,
	// which is where runtime.js zeroes it (resync(): `gapTries = 0`).
	w.gapTries = 0
	last := w.seq
	w.resyncs++
	w.mu.Unlock()
	_ = w.send(w.envelope(&pb.ResyncRequest{LastAppliedSeq: last, Reason: pb.ResyncReason_GAP}))
}

// resyncRetryBase and resyncRetryCap are runtime.js's RESYNC_BASE and its
// shared CAP, transcribed. The base is one second because RFC §7.6's default
// MinResyncInterval is one second; the cap is the reconnect schedule's, shared
// rather than copied.
const (
	resyncRetryBase = time.Second
	resyncRetryCap  = 15 * time.Second
)

// refused is runtime.js's refused(): Error{RATE_LIMITED} re-arms the request
// rather than the detector.
//
// The guard is the client's own gap state and not the error's causal
// identifiers, because a refused resync's Error carries the event id of the
// request it refused and a client that is not latched has no request to retry.
// So an event flood cannot be turned into a resync flood: with no gap
// outstanding this is a no-op.
//
// EQUAL jitter, deliberately not RFC §8.4's full jitter, and this is the one
// place the client has two schedules that disagree. Full jitter draws from zero
// to spread a herd; a refused resync has no herd, the bucket is per session,
// and a delay near zero is precisely the request the server has just declined.
func (w *wire) refused() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.gapPending || w.gapTimer != nil {
		return
	}
	bound := resyncRetryBase << w.gapTries
	if bound > resyncRetryCap || bound <= 0 {
		bound = resyncRetryCap
	}
	w.gapTries++
	half := bound / 2
	w.gapTimer = time.AfterFunc(half+time.Duration(rand.Int63n(int64(half))), w.ask)
}

// ask is runtime.js's ask(): the armed retry firing. It names the sequence the
// client really holds at the moment it asks, not the one it held when the gap
// opened, so a Snapshot that arrived in between cannot make the request stale.
func (w *wire) ask() {
	w.mu.Lock()
	w.gapTimer = nil
	if !w.gapPending {
		w.mu.Unlock()
		return
	}
	last := w.seq
	w.resyncs++
	w.retries++
	w.mu.Unlock()
	_ = w.send(w.envelope(&pb.ResyncRequest{LastAppliedSeq: last, Reason: pb.ResyncReason_GAP}))
}

// retryCount is how many of the requests in resyncCount were armed retries
// rather than first asks.
func (w *wire) retryCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.retries
}

// ---------------------------------------------------------------------------
// Sending
// ---------------------------------------------------------------------------

func (w *wire) send(f *pb.Frame) error {
	b, err := proto.Marshal(f)
	if err != nil {
		return err
	}
	return w.sendBytes(b)
}

func (w *wire) sendBytes(b []byte) error {
	ctx, cancel := context.WithTimeout(w.ctx, 10*time.Second)
	defer cancel()
	return w.conn.Write(ctx, websocket.MessageBinary, b)
}

// sid returns the session identifier learned from the wire. It is read under
// the mutex because the read pump is what learns it.
func (w *wire) sid() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sessionID
}

func (w *wire) envelope(payload any) *pb.Frame {
	f := &pb.Frame{ProtocolVersion: 1, SessionId: w.sid()}
	switch p := payload.(type) {
	case *pb.Event:
		f.Payload = &pb.Frame_Event{Event: p}
	case *pb.Ack:
		f.Payload = &pb.Frame_Ack{Ack: p}
	case *pb.Heartbeat:
		f.Payload = &pb.Frame_Heartbeat{Heartbeat: p}
	case *pb.ResyncRequest:
		f.Payload = &pb.Frame_ResyncRequest{ResyncRequest: p}
	case *pb.ClientTelemetry:
		f.Payload = &pb.Frame_ClientTelemetry{ClientTelemetry: p}
	default:
		panic(fmt.Sprintf("chaos: envelope does not carry %T", payload))
	}
	return f
}

// commit sends one chaos.commit event and returns the client_ref it used, which
// is also the ledger key the resulting effect writes.
func (w *wire) commit(delay time.Duration) uint64 {
	GinkgoHelper()
	w.mu.Lock()
	w.ref++
	ref := w.ref
	seen := w.seq
	w.mu.Unlock()

	ev := &pb.Event{
		ClientRef:     ref,
		Name:          "chaos.commit",
		FragmentId:    "total",
		SeenServerSeq: seen,
		Fields: []*pb.EventField{
			{Key: "ref", Value: strconv.FormatUint(ref, 10)},
			{Key: "delay", Value: delay.String()},
		},
	}
	Expect(w.send(w.envelope(ev))).To(Succeed())
	return ref
}

// commitBytes returns the exact bytes of a commit event without sending it, so
// a spec can send the same bytes twice.
func (w *wire) commitBytes(ref uint64) []byte {
	GinkgoHelper()
	w.mu.Lock()
	seen := w.seq
	w.mu.Unlock()
	f := w.envelope(&pb.Event{
		ClientRef:     ref,
		Name:          "chaos.commit",
		FragmentId:    "total",
		SeenServerSeq: seen,
		Fields: []*pb.EventField{
			{Key: "ref", Value: strconv.FormatUint(ref, 10)},
			{Key: "delay", Value: "0s"},
		},
	})
	b, err := proto.Marshal(f)
	Expect(err).NotTo(HaveOccurred())
	return b
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

func (w *wire) next(timeout time.Duration) (*pb.Frame, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case f, ok := <-w.incoming:
		if !ok {
			return nil, fmt.Errorf("chaos: the connection closed")
		}
		return f, nil
	case <-timer.C:
		return nil, fmt.Errorf("chaos: no frame within %s", timeout)
	}
}

// await reads until pick matches or the timeout expires.
func (w *wire) await(timeout time.Duration, pick func(frame *pb.Frame) bool) (*pb.Frame, error) {
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("chaos: nothing matched within %s", timeout)
		}
		f, err := w.next(remaining)
		if err != nil {
			return nil, err
		}
		if pick(f) {
			return f, nil
		}
	}
}

func (w *wire) captured() []*pb.Frame {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*pb.Frame, len(w.frames))
	copy(out, w.frames)
	return out
}

func (w *wire) patches() []*pb.Patch {
	var out []*pb.Patch
	for _, f := range w.captured() {
		if p := f.GetPatch(); p != nil {
			out = append(out, p)
		}
	}
	return out
}

func (w *wire) snapshots() []*pb.Snapshot {
	var out []*pb.Snapshot
	for _, f := range w.captured() {
		if s := f.GetSnapshot(); s != nil {
			out = append(out, s)
		}
	}
	return out
}

func (w *wire) errors() []*pb.Error {
	var out []*pb.Error
	for _, f := range w.captured() {
		if e := f.GetError(); e != nil {
			out = append(out, e)
		}
	}
	return out
}

// counters returns what the read pump tallied without retaining: frames in,
// bytes in, error frames, and the rate-limited subset of them.
func (w *wire) counters() (frames, bytes, errs, limited int64) {
	return w.framesIn.Load(), w.bytesIn.Load(), w.errorsSeen.Load(), w.rateLimited.Load()
}

func (w *wire) gapCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.gaps
}

func (w *wire) resyncCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.resyncs
}

func (w *wire) appliedSeq() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.seq
}

// isClosed reports whether the read pump has ended, and with which code.
func (w *wire) isClosed() bool {
	select {
	case <-w.closed:
		return true
	default:
		return false
	}
}

func (w *wire) code() websocket.StatusCode {
	return websocket.StatusCode(w.closeCode.Load())
}

// fragmentHTML returns the markup a snapshot carries for one fragment.
func snapshotHTML(s *pb.Snapshot, fragmentID string) string {
	for _, u := range s.GetUpdates() {
		if u.GetFragmentId() == fragmentID {
			return u.GetHtml()
		}
	}
	return ""
}

// patchHTML returns the markup a patch carries for one fragment, or "" if it
// does not carry that fragment.
func patchHTML(p *pb.Patch, fragmentID string) string {
	for _, u := range p.GetUpdates() {
		if u.GetFragmentId() == fragmentID {
			return u.GetHtml()
		}
	}
	return ""
}

// freePort reserves and releases a loopback port, so a child process can be
// restarted on the same address.
func freePort() int {
	GinkgoHelper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	port := ln.Addr().(*net.TCPAddr).Port
	Expect(ln.Close()).To(Succeed())
	return port
}
