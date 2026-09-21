package sampling

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/pkg/gotth/internal/obstest"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/patience"
)

func TestSampling(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "FR-36 Clause 4 Sampling Suite")
}

const (
	fragmentCount = "sampling.count"
	eventIncr     = "sampling.increment"
	metricWindow  = "gotthlive_outbound_window_depth"
	testOrigin    = "https://sampling.example"
	subprotocol   = "gotth-live.v1"
)

var ackAppliedBudget = patience.Budget{Within: 5 * time.Second, Interval: time.Millisecond}

type state struct{ N int }

type user string

func (u user) Subject() string { return string(u) }

// newApp is one fragment and one event, and the event always changes state.
//
// That is load-bearing rather than minimal: a transition that changed nothing
// would render to identical bytes, be suppressed, and emit no patch — so its
// graph would legitimately lack the encode and send spans, and a spec that
// called that "partial" would be failing on the application rather than on the
// tracer.
func newApp(tp *sdktrace.TracerProvider, metrics *obstest.Metrics) *live.App[state, user] {
	GinkgoHelper()

	app, err := live.New(live.Config[state, user]{
		Init: func(ctx context.Context, session live.Session[user]) (state, []live.Effect[user], error) {
			return state{}, nil, nil
		},
		Reduce: func(s state, ev live.Event) (state, []live.Effect[user]) {
			if ev.Name == eventIncr {
				s.N++
			}
			return s, nil
		},
		Fragments: []live.Fragment[state]{{
			ID: fragmentCount,
			Render: func(s state) templ.Component {
				return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
					_, err := fmt.Fprintf(w, "<b>%d</b>", s.N)
					return err
				})
			},
			Dirty: func(prev, next state) bool { return prev != next },
		}},
		Events:       []string{eventIncr},
		Origins:      []string{testOrigin},
		Authenticate: func(request *http.Request) (user, error) { return user("sampler"), nil },
		Authorize:    live.AllowAll[user],
		CSRF:         live.NoCSRFCheck,
		Tracer:       tp,
		Metrics:      metrics,
		// The inbound event bucket is raised, and only it.
		//
		// Its default is a real production bound and this suite drives
		// hundreds of interactions as fast as a loopback socket allows, so at
		// the default the sampler would be measured over however many events
		// fit in the burst and the rest would be RATE_LIMITED — a spec that
		// asserts a rate over an unknown number of interactions. Raising the
		// bucket is the narrow change: nothing else about the path moves, and
		// the window, the coalesce ladder and the render suppression stay at
		// their shipped values because those DO change which spans an
		// interaction produces.
		Limits: live.Limits{MaxEventsPerSecond: 1e6, EventBurst: 1 << 16},
	})
	Expect(err).NotTo(HaveOccurred())
	return app
}

// recorder is a real SDK provider with a real sampler and an in-process span
// recorder. tracetest.SpanRecorder is a SpanProcessor, so a span is recorded
// the moment it ends and nothing needs flushing; what does need waiting for is
// the actor finishing, which stop() below does by draining the handler.
func recorder(rate float64) (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sr),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(rate))),
	)
	return tp, sr
}

// driven is one connected session.
type driven struct {
	app     *live.App[state, user]
	server  *httptest.Server
	conn    *websocket.Conn
	ctx     context.Context
	metrics *obstest.Metrics

	sessionID   []byte
	ref         uint64
	highest     uint64
	lastPatchID uint64

	// reportTiming makes each interaction send a ClientTelemetry frame for the
	// patch it just received, which is what produces a morph span.
	reportTiming bool

	incoming chan *pb.Frame
	stopOnce sync.Once
}

func dial(tp *sdktrace.TracerProvider) *driven {
	GinkgoHelper()

	metrics := obstest.NewMetrics()
	app := newApp(tp, metrics)
	ts := httptest.NewServer(app.Handler())

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	DeferCleanup(cancel)

	headers := http.Header{}
	headers.Set("Origin", testOrigin)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http"), &websocket.DialOptions{
		HTTPHeader:   headers,
		Subprotocols: []string{subprotocol},
	})
	Expect(err).NotTo(HaveOccurred())

	d := &driven{
		app: app, server: ts, conn: conn, ctx: ctx, metrics: metrics,
		incoming: make(chan *pb.Frame, 8192),
	}
	go d.pump()
	DeferCleanup(d.stop)

	first := d.read()
	Expect(first.GetSnapshot()).NotTo(BeNil(), "the first frame on a connection is the Snapshot")
	d.sessionID = first.GetSessionId()
	d.highest = first.GetSnapshot().GetServerSeq()
	return d
}

func (d *driven) pump() {
	defer close(d.incoming)
	for {
		typ, data, err := d.conn.Read(d.ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary {
			return
		}
		var f pb.Frame
		if err := proto.Unmarshal(data, &f); err != nil {
			return
		}
		select {
		case d.incoming <- &f:
		default:
		}
	}
}

func (d *driven) read() *pb.Frame {
	GinkgoHelper()
	select {
	case f, ok := <-d.incoming:
		Expect(ok).To(BeTrue(), "the connection closed while a frame was expected")
		return f
	case <-time.After(20 * time.Second):
		Fail("no frame arrived within twenty seconds")
		return nil
	}
}

// stop drains the server before a spec reads spans.
//
// It is not only cleanup. The client holds a patch as soon as the socket write
// returns, and the encode, transition and parse spans end after that, so a
// spec that counted spans the instant the last patch arrived would be racing
// the actor. Handler.Close waits for every session's actor to finish, which
// makes the recorded set complete rather than probably-complete.
func (d *driven) stop() {
	d.stopOnce.Do(func() {
		if d.conn != nil {
			_ = d.conn.CloseNow()
		}
		if d.app != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = d.app.Close(ctx)
		}
		if d.server != nil {
			d.server.Close()
		}
	})
}

// interact drives one whole interaction: an event in, its patch out, and the
// acknowledgement that re-opens the outbound window.
//
// Acknowledging is what keeps the window shallow. Without it the coalesce
// stage engages at half the window and transitions stop emitting a frame each,
// which would make some interactions legitimately produce no encode span — a
// second reason for an incomplete graph that has nothing to do with sampling.
func (d *driven) interact() {
	GinkgoHelper()

	d.ref++
	ev := &pb.Event{
		ClientRef:     d.ref,
		Name:          eventIncr,
		FragmentId:    fragmentCount,
		SeenServerSeq: d.highest,
	}
	Expect(d.write(&pb.Frame{
		ProtocolVersion: 1,
		SessionId:       d.sessionID,
		Payload:         &pb.Frame_Event{Event: ev},
	})).To(Succeed())

	for {
		f := d.read()
		if p := f.GetPatch(); p != nil {
			d.highest = p.GetServerSeq()
			d.lastPatchID = p.GetPatchId()

			// Acknowledging up to this patch retires its window slot, and the
			// slot is where the encode span's reference lives — so a timing
			// report for the patch just acknowledged is dropped as
			// unknown_patch and no morph span is ever created. When timing is
			// being reported the acknowledgement therefore lags by one
			// sequence: the window stays one deep, which is nowhere near the
			// coalesce threshold, and the slot the report names is still
			// there. This was found by the morph spec recording zero spans,
			// which is the failure that assertion exists for.
			ackTo := p.GetServerSeq()
			if d.reportTiming {
				d.reportTelemetry()
				ackTo--
			}
			d.ackAndAwait(ackTo, p.GetServerSeq())
			return
		}
		if e := f.GetError(); e != nil {
			Fail(fmt.Sprintf("the server refused an interaction: %s (%s)", e.GetMessage(), e.GetCode()))
		}
	}
}

// ackAndAwait keeps the sampling test about tracing rather than incidental
// backpressure. An Ack reaches the actor on a different channel from the next
// Event, so writing it is not proof the window slot has been retired yet.
func (d *driven) ackAndAwait(ackTo, emitted uint64) {
	GinkgoHelper()

	before := len(d.metrics.Observations(metricWindow))
	Expect(d.write(&pb.Frame{
		ProtocolVersion: 1,
		SessionId:       d.sessionID,
		Payload:         &pb.Frame_Ack{Ack: &pb.Ack{ServerSeq: ackTo}},
	})).To(Succeed())

	want := float64(emitted - ackTo)
	patience.Await(GinkgoTB(), "the actor to apply the sampling acknowledgement", ackAppliedBudget,
		func() []obstest.Measurement { return d.metrics.Observations(metricWindow) },
		func(observed []obstest.Measurement) bool {
			return len(observed) > before && observed[len(observed)-1].Value == want
		})
}

// reportTelemetry sends one client timing report for the last patch, which is
// what produces a gotthlive.client.morph span.
func (d *driven) reportTelemetry() {
	GinkgoHelper()
	Expect(d.write(&pb.Frame{
		ProtocolVersion: 1,
		SessionId:       d.sessionID,
		Payload: &pb.Frame_ClientTelemetry{ClientTelemetry: &pb.ClientTelemetry{
			PatchId: d.lastPatchID, MorphMicros: 1200, ApplyMicros: 1500,
		}},
	})).To(Succeed())
}

func (d *driven) write(f *pb.Frame) error {
	b, err := proto.Marshal(f)
	if err != nil {
		return err
	}
	return d.conn.Write(d.ctx, websocket.MessageBinary, b)
}
