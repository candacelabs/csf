package obs_test

import (
	"context"
	"log/slog"
	"reflect"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
)

func TestObs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Observability Suite")
}

type capture struct {
	records []slog.Record
	attrs   []slog.Attr
	parent  *capture
}

func (c *capture) Enabled(ctx context.Context, level slog.Level) bool { return true }

func (c *capture) Handle(_ context.Context, r slog.Record) error {
	r.AddAttrs(c.attrs...)
	root := c
	for root.parent != nil {
		root = root.parent
	}
	root.records = append(root.records, r)
	return nil
}

func (c *capture) WithAttrs(a []slog.Attr) slog.Handler {
	return &capture{attrs: a, parent: c}
}

func (c *capture) WithGroup(name string) slog.Handler { return c }

func (c *capture) fields(i int) map[string]any {
	out := map[string]any{}
	c.records[i].Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value.Any()
		return true
	})
	return out
}

var _ = Describe("A disabled configuration", func() {
	// The requirement is that a disabled signal costs one predictable branch
	// and not an indirect call through a no-op implementation. The observable
	// consequence, and the one a spec can hold, is that the nil receiver is a
	// valid one on every method.
	It("is a nil receiver, and every call on it is correct", func() {
		var m *obs.Metrics
		var t *obs.Tracer
		var l *obs.Logger

		Expect(m.Enabled()).To(BeFalse())
		Expect(t.Enabled()).To(BeFalse())
		Expect(l.Enabled()).To(BeFalse())

		ctx := context.Background()
		Expect(func() {
			m.FrameReceived(ctx, "event", 10)
			m.FrameSent(ctx, "patch", 10)
			m.FrameRejected(ctx, "oversize")
			m.OutboundInvalid(ctx, "patch")
			m.EventReceived(ctx, "counter.increment")
			m.EventRejected(ctx, "unauthorized")
			m.Transition(ctx, "applied", 0.001, "counter.increment")
			m.RenderDuration(ctx, 0.001, "counter")
			m.EncodeDuration(ctx, 0.001)
			m.SendDuration(ctx, 0.001)
			m.PatchesSent(ctx, "morph", 1)
			m.PatchesSuppressed(ctx, 1)
			m.PatchCoalesced(ctx)
			m.SlowClientEvent(ctx)
			m.ResyncRequest(ctx, "snapshot", 100)
			m.Effect(ctx, "test", "ok")
			m.EffectAbandoned(ctx)
			m.Panic(ctx, "reduce")
			m.ConnectionOpened(ctx)
			m.ConnectionClosed(ctx, "normal")
			m.SessionsActive(ctx, 1)
			m.Goroutines(ctx, 1)
			m.TrackedBytes(ctx, 1024)
			m.MailboxDepth(ctx, 3)
			m.WindowDepth(ctx, 3)
			m.ClientTiming(ctx, 1, 1)
			m.ClientTelemetryDropped(ctx, "unknown_patch")
			Expect(m.SourceLabel(ctx, "test")).To(Equal("test"))

			_, span := t.Start(ctx, "gotthlive.event")
			span.SetAttributes()
			span.RecordError(nil)
			Expect(span.Ref().SpanContext().IsValid()).To(BeFalse())
			span.End()

			l.Debug(ctx, "x")
			l.Info(ctx, "x")
			l.Warn(ctx, "x")
			l.Error(ctx, "x")
			l.Provenance(ctx, obs.Provenance{})
		}).NotTo(Panic())
	})

	It("builds nothing from a nil provider, and calls that an enabled state", func() {
		m, err := obs.NewMetrics(nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Enabled()).To(BeFalse())
		Expect(obs.NewTracer(nil).Enabled()).To(BeFalse())
		Expect(obs.NewLogger(nil).Enabled()).To(BeFalse())
	})
})

var _ = Describe("The metric set", func() {
	It("creates every instrument in the catalogue without error", func() {
		m, err := obs.NewMetrics(metricnoop.NewMeterProvider())
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Enabled()).To(BeTrue())
	})

	Describe("the origin-source label", func() {
		// Nothing registers an effect, so this cardinality cannot be bounded
		// by registration the way event and fragment names are. It is bounded
		// here, and the collapse is counted rather than silent.
		It("caps distinct values and collapses the rest, rather than growing forever", func() {
			m, err := obs.NewMetrics(metricnoop.NewMeterProvider())
			Expect(err).NotTo(HaveOccurred())
			ctx := context.Background()

			for i := 0; i < obs.SourceLabelCap; i++ {
				Expect(m.SourceLabel(ctx, "effect:s"+itoa(i))).To(Equal("effect:s" + itoa(i)))
			}
			Expect(m.SourceLabel(ctx, "effect:one_too_many")).To(Equal(obs.SourceOverflowLabel))

			By("still recognising a value it admitted before the cap")
			Expect(m.SourceLabel(ctx, "effect:s0")).To(Equal("effect:s0"))
		})
	})
})

var _ = Describe("The span reference", func() {
	// The window holds one of these per slot, and the whole memory argument
	// for doing so rests on its width.
	It("is exactly thirty-two bytes", func() {
		Expect(int(reflect.TypeOf(obs.SpanRef{}).Size())).To(Equal(32))
	})

	It("reconstructs an invalid context from a zero value rather than a plausible one", func() {
		Expect(obs.SpanRef{}.SpanContext().IsValid()).To(BeFalse())
	})

	It("reconstructs the context it was built from, exactly", func() {
		ref := obs.SpanRef{
			TraceID: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
			SpanID:  [8]byte{1, 2, 3, 4, 5, 6, 7, 8},
			Flags:   1,
		}

		sc := ref.SpanContext()

		Expect([16]byte(sc.TraceID())).To(Equal(ref.TraceID))
		Expect([8]byte(sc.SpanID())).To(Equal(ref.SpanID))
		Expect(sc.IsSampled()).To(BeTrue())
		Expect(sc.IsRemote()).To(BeFalse())
		Expect(ref.SpanContext().IsValid()).To(BeTrue())
	})

	It("comes back empty from a disabled tracer, rather than looking real", func() {
		tr := obs.NewTracer(tracenoop.NewTracerProvider())
		_, span := tr.Start(context.Background(), "gotthlive.encode")
		defer span.End()

		Expect(span.Ref().SpanContext().IsValid()).To(BeFalse())
	})
})

var _ = Describe("The log boundary", func() {
	var (
		cap *capture
		l   *obs.Logger
	)

	BeforeEach(func() {
		cap = &capture{}
		l = obs.NewLogger(slog.New(cap))
	})

	It("carries typed fields and nothing else", func() {
		l.Warn(context.Background(), "gotth-live: something degraded",
			obs.Str("session_id", "abc"),
			obs.U64("event_id", 7),
			obs.Int("depth", 3),
			obs.Bool("fatal", false),
			obs.Dur("duration_ms", 1500*time.Millisecond))

		Expect(cap.records).To(HaveLen(1))
		f := cap.fields(0)
		Expect(f["session_id"]).To(Equal("abc"))
		Expect(f["event_id"]).To(Equal(uint64(7)))
		Expect(f["depth"]).To(Equal(int64(3)))
		Expect(f["fatal"]).To(Equal(false))
		Expect(f["duration_ms"]).To(Equal(1500.0))
	})

	It("puts a provenance record on its own logger name", func() {
		l.Provenance(context.Background(), obs.Provenance{
			SessionID:    "abc",
			EventID:      7,
			ClientRef:    3,
			TransitionID: 2,
			StateVersion: 2,
			PatchID:      2,
			ServerSeq:    2,
			OriginKind:   "CLIENT_EVENT",
			OriginSource: "event:counter.increment",
			FragmentIDs:  []string{"counter"},
		})

		Expect(cap.records).To(HaveLen(1))
		f := cap.fields(0)
		Expect(f["logger"]).To(Equal(obs.ProvenanceLogger))
		Expect(f["event_id"]).To(Equal(uint64(7)))
		Expect(f["fragment_ids"]).To(Equal([]string{"counter"}))
		Expect(f).NotTo(HaveKey("contributing_event_ids"),
			"an empty contributing list was emitted as a field")
		Expect(f).NotTo(HaveKey("superseded_from_seq"))
	})

	It("records a suppressed transition with a zero patch identifier", func() {
		l.Provenance(context.Background(), obs.Provenance{
			SessionID: "abc", TransitionID: 3, StateVersion: 2,
			OriginKind: "CLIENT_EVENT", OriginSource: "event:counter.noop",
		})

		Expect(cap.fields(0)["patch_id"]).To(BeZero())
	})

	It("does not sample the provenance stream, whatever the log volume", func() {
		for i := 0; i < 5000; i++ {
			l.Provenance(context.Background(), obs.Provenance{SessionID: "abc", TransitionID: uint64(i + 1)})
		}
		Expect(cap.records).To(HaveLen(5000))
	})

	It("samples Info once the volume passes the threshold, and says that it did", func() {
		for i := 0; i < obs.InfoSampleThreshold*20; i++ {
			l.Info(context.Background(), "gotth-live: session opened")
		}

		Expect(len(cap.records)).To(BeNumerically("<", obs.InfoSampleThreshold*20),
			"a high-volume Info stream was not sampled")

		var sampled int
		for i := range cap.records {
			if _, ok := cap.fields(i)["sampled_1_in"]; ok {
				sampled++
			}
		}
		Expect(sampled).To(BeNumerically(">", 0),
			"records were dropped without the survivors admitting the stream was sampled")
	})
})

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
