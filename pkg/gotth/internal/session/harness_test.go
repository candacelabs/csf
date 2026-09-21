package session_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
	"github.com/candacelabs/csf/pkg/gotth/internal/obstest"
	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/internal/render"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
)

func TestSession(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Session Suite")
}

// counterState is the state the specs reduce over. It is comparable, which is
// the case the actor's change detection takes its fast path on.
type counterState struct {
	N     int
	Label string
}

type subject string

func (s subject) Subject() string { return string(s) }

// testEffect is what a spec's reducer decides to do, as data: a source and the
// reply the execute hook echoes back.
//
// It is not itself the effect any more. Since live.Effect became a concrete
// struct carrying its own Run, a spec builds one through [testApp.effect],
// which is where the harness records what the actor ran.
type testEffect struct {
	Source string
	Reply  string
}

// testApp is a stateful fake rather than a generated mock, because most of
// these specs are about behaviour over a sequence of transitions. The
// expectation-based mock is used where the assertion really is "this was
// called", in the authorization specs.
type testApp struct {
	reg    *render.Registry
	events map[string]bool

	initState   any
	initEffects []session.Effect[subject]
	initErr     error

	// pointerState makes StateComparable answer the way live's adapter answers
	// for a reference state type.
	pointerState bool

	reduce    func(state any, ev session.Event) (any, []session.Effect[subject])
	authorize func(ctx context.Context, p session.Peer[subject], ev session.Event) error
	execute   func(ctx context.Context, p session.Peer[subject], e testEffect, emit session.Emit) error

	mu          sync.Mutex
	authorized  []string
	reduced     []string
	tornDown    bool
	finalState  any
	executeSeen []string
	// executeScheduledBy records the causal identifier the actor handed each
	// Execute call, in the same order as executeSeen. It exists because that
	// identifier is what FR-58 makes the adapter's emission errors name, and a
	// parameter nothing observes is a parameter that can quietly become wrong.
	executeScheduledBy []uint64
}

func newTestApp(frags ...render.Fragment) *testApp {
	if len(frags) == 0 {
		frags = []render.Fragment{counterFragment()}
	}
	reg, err := render.NewRegistry(frags)
	Expect(err).NotTo(HaveOccurred())

	return &testApp{
		reg:       reg,
		events:    map[string]bool{"counter.increment": true, "counter.relabel": true, "counter.noop": true},
		initState: counterState{Label: "hits"},
		reduce: func(state any, ev session.Event) (any, []session.Effect[subject]) {
			s := state.(counterState)
			switch ev.Name {
			case "counter.increment":
				s.N++
			case "counter.relabel":
				s.Label = ev.Fields[0].Value
			}
			return s, nil
		},
	}
}

func counterFragment() render.Fragment {
	return render.Fragment{
		ID: "counter",
		Render: func(_ context.Context, state any, w io.Writer) error {
			s := state.(counterState)
			_, err := fmt.Fprintf(w, "<b>%s %d</b>", s.Label, s.N)
			return err
		},
	}
}

func (t *testApp) Init(ctx context.Context, peer session.Peer[subject]) (any, []session.Effect[subject], error) {
	return t.initState, t.initEffects, t.initErr
}

func (t *testApp) Authorize(ctx context.Context, p session.Peer[subject], ev session.Event) error {
	t.mu.Lock()
	t.authorized = append(t.authorized, ev.Name)
	t.mu.Unlock()
	if t.authorize != nil {
		return t.authorize(ctx, p, ev)
	}
	return nil
}

func (t *testApp) Reduce(state any, ev session.Event) (any, []session.Effect[subject]) {
	t.mu.Lock()
	t.reduced = append(t.reduced, ev.Name)
	t.mu.Unlock()
	if t.reduce == nil {
		return state, nil
	}
	return t.reduce(state, ev)
}

// effect builds the session.Effect[subject] a spec's reducer returns, and is where the
// harness observes what the actor ran.
//
// It replaced the IApp[subject].Execute hook when the effect became a concrete struct
// carrying its own Run: there is no executor method left to instrument, so the
// instrumentation moved onto the effect the spec constructs. scheduledBy is
// recorded here for the same reason it was recorded there — it is what FR-58
// makes the adapter's emission errors name, and a parameter nothing observes is
// a parameter that can quietly become wrong.
func (t *testApp) effect(e testEffect) session.Effect[subject] {
	return session.Effect[subject]{
		Source: e.Source,
		Run: func(ctx context.Context, p session.Peer[subject], scheduledBy uint64, emit session.Emit) error {
			t.mu.Lock()
			t.executeSeen = append(t.executeSeen, e.Source)
			t.executeScheduledBy = append(t.executeScheduledBy, scheduledBy)
			t.mu.Unlock()
			if t.execute == nil {
				return nil
			}
			return t.execute(ctx, p, e, emit)
		},
	}
}

func (t *testApp) Teardown(_ context.Context, _ session.Peer[subject], state any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tornDown = true
	t.finalState = state
}

func (t *testApp) Registry() *render.Registry { return t.reg }

func (t *testApp) Registered(name string) bool { return t.events[name] }

// StateComparable is what live's adapter answers for a struct state type, which
// counterState is. The pointer-state specs override it, because the whole point
// of BR-7 is that a pointer is comparable in Go's sense and must not take that
// path.
func (t *testApp) StateComparable() bool { return !t.pointerState }

func (t *testApp) authorizedNames() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.authorized...)
}

func (t *testApp) reducedNames() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.reduced...)
}

// clock is the actor's only source of time, so a spec can drive a thirty
// minute idle timeout without waiting thirty minutes.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func newClock() *clock {
	return &clock{at: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// sink captures the wire, which is the only place these specs look for what
// the server said. Asserting on frames rather than on internal state is
// deliberate: the frames are what a client and an auditor both see.
type sink struct {
	mu     sync.Mutex
	frames []*pb.Frame
	fail   error
	block  chan struct{}
}

func newSink() *sink { return &sink{} }

func (s *sink) write(ctx context.Context, b []byte) error {
	s.mu.Lock()
	fail, block := s.fail, s.block
	s.mu.Unlock()

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if fail != nil {
		return fail
	}

	var f pb.Frame
	if err := proto.Unmarshal(b, &f); err != nil {
		return err
	}
	s.mu.Lock()
	s.frames = append(s.frames, &f)
	s.mu.Unlock()
	return nil
}

func (s *sink) all() []*pb.Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*pb.Frame(nil), s.frames...)
}

func (s *sink) patches() []*pb.Patch {
	var out []*pb.Patch
	for _, f := range s.all() {
		if p := f.GetPatch(); p != nil {
			out = append(out, p)
		}
	}
	return out
}

func (s *sink) snapshots() []*pb.Snapshot {
	var out []*pb.Snapshot
	for _, f := range s.all() {
		if p := f.GetSnapshot(); p != nil {
			out = append(out, p)
		}
	}
	return out
}

func (s *sink) errors() []*pb.Error {
	var out []*pb.Error
	for _, f := range s.all() {
		if e := f.GetError(); e != nil {
			out = append(out, e)
		}
	}
	return out
}

func (s *sink) failWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = err
}

// closeRecord is what the transport was told to do.
type closeRecord struct {
	Code   protocol.CloseCode
	Reason string
}

// records captures the library's log stream, which is where the provenance
// row for every transition lands. It is a handler rather than a buffer of
// formatted text so a spec can assert on fields.
type records struct {
	mu  sync.Mutex
	all []map[string]any
}

func (r *records) Enabled(ctx context.Context, level slog.Level) bool { return true }

func (r *records) Handle(_ context.Context, rec slog.Record) error {
	m := map[string]any{"msg": rec.Message, "level": rec.Level.String()}
	rec.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.Any()
		return true
	})
	r.mu.Lock()
	r.all = append(r.all, m)
	r.mu.Unlock()
	return nil
}

func (r *records) WithAttrs(attrs []slog.Attr) slog.Handler {
	// The library adds exactly one attribute this way, the provenance logger
	// name, so carrying it forward into a child handler is enough.
	return &prefixed{parent: r, attrs: attrs}
}

func (r *records) WithGroup(name string) slog.Handler { return r }

type prefixed struct {
	parent *records
	attrs  []slog.Attr
}

func (p *prefixed) Enabled(ctx context.Context, level slog.Level) bool { return true }

func (p *prefixed) Handle(ctx context.Context, rec slog.Record) error {
	rec.AddAttrs(p.attrs...)
	return p.parent.Handle(ctx, rec)
}

func (p *prefixed) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &prefixed{parent: p.parent, attrs: append(append([]slog.Attr(nil), p.attrs...), attrs...)}
}

func (p *prefixed) WithGroup(name string) slog.Handler { return p }

// provenance returns the transition rows, in emission order.
func (r *records) provenance() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, m := range r.all {
		if m["logger"] == obs.ProvenanceLogger {
			out = append(out, m)
		}
	}
	return out
}

// renderFailures returns one record per recovered render or dirty panic. The
// frame is coalesced per pass and the log is not, so this is where a spec goes
// to count the failures themselves.
func (r *records) renderFailures() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, m := range r.all {
		if site, ok := m["site"].(string); ok && (site == "render" || site == "dirty") {
			out = append(out, m)
		}
	}
	return out
}

// warnings returns the message of every record at warning level or above. It
// is how a spec asserts that the library did NOT accuse anybody of anything:
// a metric says a report was dropped, and the log is where the accusation is.
func (r *records) warnings() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, m := range r.all {
		switch m["level"] {
		case "WARN", "ERROR":
			if msg, ok := m["msg"].(string); ok {
				out = append(out, msg)
			}
		}
	}
	return out
}

// bySite returns every record the library tagged with one panic site. It is
// renderFailures' general form, and exists because FR-58 is a property of the
// record's fields rather than of the frame the panic produced: a spec that
// asserts a log names its causal identifier has to read the log.
func (r *records) bySite(site string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, m := range r.all {
		if got, ok := m["site"].(string); ok && got == site {
			out = append(out, m)
		}
	}
	return out
}

// harness wires an actor to a fake transport and a fake clock.
type harness struct {
	app     *testApp
	sink    *sink
	clock   *clock
	ticks   chan time.Time
	logs    *records
	metrics *obstest.Metrics
	actor   *session.Actor[subject]

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	closes  []closeRecord
	invalid []error
	nextRef uint64
}

// invalidFrames returns every frame the library built and then refused to
// send. It must stay empty on every input an application can produce.
func (h *harness) invalidFrames() []error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]error(nil), h.invalid...)
}

// newHarness wires a production-mode actor, which is what almost every spec
// wants: production is the mode whose bytes are a contract.
func newHarness(app *testApp, lim session.Limits) *harness {
	return newHarnessIn(app, lim, false)
}

// newDevHarness wires an actor with Options[subject].Dev set. It exists so that FR-23's
// two directions are asserted by two specs over one code path, rather than by
// one spec and a hope about the other.
func newDevHarness(app *testApp, lim session.Limits) *harness {
	return newHarnessIn(app, lim, true)
}

func newHarnessIn(app *testApp, lim session.Limits, dev bool) *harness {
	GinkgoHelper()

	h := &harness{
		app:     app,
		sink:    newSink(),
		clock:   newClock(),
		ticks:   make(chan time.Time, 1),
		logs:    &records{},
		metrics: obstest.NewMetrics(),
		done:    make(chan struct{}),
	}
	h.ctx, h.cancel = context.WithCancel(context.Background())

	framer := protocol.NewFramer(h.sink.write)
	// Every frame this library builds and then refuses to send. Nothing an
	// application supplies may reach here: a value that fails the outbound
	// boundary is dropped on the actor goroutine, layers away from whoever
	// supplied it, as an INTERNAL error the application never hears about.
	// Recording it lets a spec state that as a property rather than as an
	// assertion about one field (BR-2).
	framer.OnInvalid = func(_ protocol.Kind, err error) {
		h.mu.Lock()
		h.invalid = append(h.invalid, err)
		h.mu.Unlock()
	}
	metrics, err := obs.NewMetrics(h.metrics)
	Expect(err).NotTo(HaveOccurred())

	h.actor = session.New[subject](session.Options[subject]{
		Peer:   session.Peer[subject]{ID: testSessionID(), Identity: subject("tester")},
		App:    app,
		Limits: lim,
		Framer: framer,
		Close: func(code protocol.CloseCode, reason string) {
			h.mu.Lock()
			h.closes = append(h.closes, closeRecord{code, reason})
			h.mu.Unlock()
			h.cancel()
		},
		// Telemetry is live on every spec rather than lazy: every instrument
		// and every span in the catalogue runs, so a nil dereference or a
		// malformed instrument name fails here instead of on the first consumer
		// who turns telemetry on. The metric provider records as well as runs,
		// because several invariants — H-11's drop counter, H-14's budget — are
		// stated about a counter and are unassertable against a no-op.
		Metrics: metrics,
		Tracer:  obs.NewTracer(tracenoop.NewTracerProvider()),
		Logger:  obs.NewLogger(slog.New(h.logs)),
		Dev:     dev,
		Now:     h.clock.Now,
		Ticks:   h.ticks,
	})
	return h
}

func (h *harness) start() {
	GinkgoHelper()
	go func() {
		defer close(h.done)
		h.actor.Run(h.ctx)
	}()
	Expect(h.actor.Ready(h.ctx)).To(Succeed())
}

func (h *harness) stop() {
	h.cancel()
	Eventually(h.done, time.Second).Should(BeClosed())
}

func (h *harness) closeRecords() []closeRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]closeRecord(nil), h.closes...)
}

func (h *harness) ref() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextRef++
	return h.nextRef
}

// send builds a real frame, encodes it, parses it through the real inbound
// boundary and hands the result to the ingress. Nothing is short-circuited:
// a spec that injects an event exercises the same path a browser does.
func (h *harness) send(f *pb.Frame) {
	GinkgoHelper()
	b, err := proto.Marshal(f)
	Expect(err).NotTo(HaveOccurred())

	in, err := protocol.ParseInbound(b, protocol.DefaultLimits())
	Expect(err).NotTo(HaveOccurred())
	Expect(protocol.CheckSessionID(in, testSessionID())).To(Succeed())

	h.actor.Ingress(h.ctx, in)
}

func (h *harness) sendEvent(name string, fields ...*pb.EventField) {
	GinkgoHelper()
	h.send(&pb.Frame{
		ProtocolVersion: protocol.Version,
		SessionId:       sessionIDBytes(),
		Payload: &pb.Frame_Event{Event: &pb.Event{
			ClientRef:     h.ref(),
			Name:          name,
			FragmentId:    "counter",
			SeenServerSeq: 1,
			Fields:        fields,
		}},
	})
}

func (h *harness) sendAck(seq uint64) {
	GinkgoHelper()
	h.send(&pb.Frame{
		ProtocolVersion: protocol.Version,
		SessionId:       sessionIDBytes(),
		Payload:         &pb.Frame_Ack{Ack: &pb.Ack{ServerSeq: seq}},
	})
}

func (h *harness) sendResync(lastApplied uint64) {
	GinkgoHelper()
	h.send(&pb.Frame{
		ProtocolVersion: protocol.Version,
		SessionId:       sessionIDBytes(),
		Payload: &pb.Frame_ResyncRequest{ResyncRequest: &pb.ResyncRequest{
			LastAppliedSeq: lastApplied,
			Reason:         pb.ResyncReason_GAP,
		}},
	})
}

func (h *harness) sendTelemetry(patchID uint64) {
	GinkgoHelper()
	h.send(&pb.Frame{
		ProtocolVersion: protocol.Version,
		SessionId:       sessionIDBytes(),
		Payload: &pb.Frame_ClientTelemetry{ClientTelemetry: &pb.ClientTelemetry{
			PatchId: patchID, MorphMicros: 1200, ApplyMicros: 800,
		}},
	})
}

func (h *harness) tick() {
	select {
	case h.ticks <- h.clock.Now():
	case <-time.After(time.Second):
		Fail("the actor did not consume a tick")
	}
}

// fragmentIDsOf names the fragments a patch or snapshot carried.
func fragmentIDsOf(us []*pb.FragmentUpdate) []string {
	out := make([]string, 0, len(us))
	for _, u := range us {
		out = append(out, u.GetFragmentId())
	}
	return out
}

func testSessionID() session.ID {
	var id session.ID
	copy(id[:], "0123456789abcdef")
	return id
}

func sessionIDBytes() []byte {
	id := testSessionID()
	return id[:]
}
