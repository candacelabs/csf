package live_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/patience"
	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/trace"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
	"github.com/candacelabs/csf/pkg/gotth/internal/obstest"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

var connectionCountBudget = patience.Budget{Within: 5 * time.Second}

// The evidence for the two exit criteria that previously rested on no-op
// providers: the metric set flowing, and one trace spanning the path.
//
// Both drive a real interaction through the real handler over a real socket,
// with a recording provider installed through the same single Config field a
// consumer uses. The distinction that matters is the one a no-op provider
// cannot make: these assert on the name, the labels and the value of what was
// emitted, not on a method having been called.
var _ = Describe("Instrumentation", func() {
	var (
		metrics *obstest.Metrics
		traces  *obstest.Traces
		app     *mounted
	)

	BeforeEach(func() {
		metrics = obstest.NewMetrics()
		traces = obstest.NewTraces()
		app = mount(func(c *live.Config[counter, user]) {
			c.Metrics = metrics
			c.Tracer = traces
		})
		DeferCleanup(app.stop)
	})

	Describe("metrics", func() {
		It("counts connections independently of identity and releases each registration", func() {
			Expect(app.app.ActiveConnections()).To(Equal(1))
			second := app.again()
			DeferCleanup(func() { _ = second.conn.CloseNow() })
			Expect(app.app.ActiveConnections()).To(Equal(2))
			Expect(metrics.Total("gotthlive_sessions_active")).To(Equal(float64(2)))
			Expect(second.conn.CloseNow()).To(Succeed())
			patience.Await(GinkgoT(), "one remaining browser connection", connectionCountBudget,
				app.app.ActiveConnections, func(count int) bool { return count == 1 })
			Expect(metrics.Total("gotthlive_sessions_active")).To(Equal(float64(1)))
			Expect(app.conn.CloseNow()).To(Succeed())
			Expect(app.app.Close(app.ctx)).To(Succeed())
			Expect(app.app.ActiveConnections()).To(BeZero())
			Expect(metrics.Total("gotthlive_sessions_active")).To(BeZero())
			Expect(metrics.Total("gotthlive_connections_total")).To(Equal(float64(2)))
		})

		It("releases the registration when initialization fails", func() {
			cfg := validConfig()
			failedMetrics := obstest.NewMetrics()
			cfg.Metrics = failedMetrics
			cfg.Init = func(ctx context.Context, peer live.Session[user]) (counter, []live.Effect[user], error) {
				return counter{}, nil, errors.New("initialization failed")
			}
			failed, err := live.New(cfg)
			Expect(err).NotTo(HaveOccurred())
			server := httptest.NewServer(failed.Handler())
			DeferCleanup(server.Close)
			connection, _, err := websocket.Dial(app.ctx, "ws"+strings.TrimPrefix(server.URL, "http"),
				&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://app.example"}}, Subprotocols: []string{"gotth-live.v1"}})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = connection.CloseNow() })
			_, _, err = connection.Read(app.ctx)
			Expect(err).NotTo(HaveOccurred(), "initialization failure is reported before the close")
			Expect(connection.CloseNow()).To(Succeed())
			Expect(failed.Close(app.ctx)).To(Succeed())
			Expect(failed.ActiveConnections()).To(BeZero())
			Expect(failedMetrics.Total("gotthlive_sessions_active")).To(BeZero())
			Expect(failedMetrics.Total("gotthlive_connections_total")).To(Equal(float64(1)))
		})

		It("registers the whole catalogue from one Config field", func() {
			// An instrument that is never created cannot ever be emitted, and
			// that failure is silent at runtime — so the registration is
			// asserted separately from the emission below.
			Expect(metrics.Registered()).To(ContainElements(
				"gotthlive_connections_total",
				"gotthlive_connections_closed_total",
				"gotthlive_frames_received_total",
				"gotthlive_frames_sent_total",
				"gotthlive_frames_rejected_total",
				"gotthlive_events_received_total",
				"gotthlive_events_rejected_total",
				"gotthlive_transitions_total",
				"gotthlive_patches_sent_total",
				"gotthlive_patches_suppressed_total",
				"gotthlive_patches_coalesced_total",
				"gotthlive_wire_bytes_total",
				"gotthlive_effects_total",
				"gotthlive_panics_total",
				"gotthlive_sessions_active",
				"gotthlive_goroutines",
				"gotthlive_session_tracked_bytes",
				"gotthlive_reduce_duration_seconds",
				"gotthlive_render_duration_seconds",
				"gotthlive_encode_duration_seconds",
				"gotthlive_send_duration_seconds",
				"gotthlive_frame_bytes",
				"gotthlive_mailbox_depth",
				"gotthlive_outbound_window_depth",
				"gotthlive_resync_requests_total",
				"gotthlive_outbound_validation_failed_total",
			))
		})

		It("counts an interaction end to end, with the labels the catalogue names", func() {
			app.send("counter.increment", nil)
			Expect(app.nextPatch().GetUpdates()[0].GetHtml()).To(Equal("<b>hits 1</b>"))

			Eventually(func() float64 {
				return metrics.Total("gotthlive_events_received_total")
			}).Should(BeNumerically(">=", 1))

			By("the event, labelled by its registered name")
			received := metrics.Observations("gotthlive_events_received_total")
			Expect(received).NotTo(BeEmpty())
			Expect(received[0].Attr("event")).To(Equal("counter.increment"))

			By("the transition, labelled by its result")
			transitions := metrics.Observations("gotthlive_transitions_total")
			Expect(transitions).NotTo(BeEmpty())
			Expect(transitions[len(transitions)-1].Attr("result")).To(Equal("applied"))

			By("the fragment update, labelled by its operation")
			patched := metrics.Observations("gotthlive_patches_sent_total")
			Expect(patched).NotTo(BeEmpty())
			var morph float64
			for _, m := range patched {
				if m.Attr("op") == "morph" {
					morph += m.Value
				}
			}
			Expect(morph).To(BeNumerically(">=", 1))

			By("the frames, in both directions, labelled by kind")
			// nextPatch observed delivery on the client side; the sender
			// records its counters after the write returns, so the send-side
			// rows land a beat later and are polled, not asserted at an
			// instant.
			Eventually(func(g Gomega) {
				var sentPatch, receivedEvent bool
				for _, m := range metrics.Observations("gotthlive_frames_sent_total") {
					sentPatch = sentPatch || m.Attr("kind") == "patch"
				}
				for _, m := range metrics.Observations("gotthlive_frames_received_total") {
					receivedEvent = receivedEvent || m.Attr("kind") == "event"
				}
				g.Expect(sentPatch).To(BeTrue())
				g.Expect(receivedEvent).To(BeTrue())
			}).Should(Succeed())

			By("the bytes, labelled by direction")
			Eventually(func(g Gomega) {
				directions := map[string]bool{}
				for _, m := range metrics.Observations("gotthlive_wire_bytes_total") {
					directions[m.Attr("direction")] = true
					g.Expect(m.Value).To(BeNumerically(">", 0))
				}
				g.Expect(directions).To(HaveKey("in"))
				g.Expect(directions).To(HaveKey("out"))
			}).Should(Succeed())

			By("the durations, as real measurements rather than zeroes")
			for _, name := range []string{
				"gotthlive_reduce_duration_seconds",
				"gotthlive_render_duration_seconds",
				"gotthlive_encode_duration_seconds",
			} {
				Expect(metrics.Observations(name)).NotTo(BeEmpty(), "%s recorded nothing", name)
			}

			By("the session gauges")
			Expect(metrics.Total("gotthlive_sessions_active")).To(BeNumerically(">=", 1))
			Expect(metrics.Total("gotthlive_session_tracked_bytes")).To(BeNumerically(">", 0))
		})

		It("counts a refusal on the rejection path rather than only the happy one", func() {
			app.sendRaw("counter.unknown", nil)
			Expect(app.nextError().GetCode()).To(Equal(pb.ErrorCode_UNKNOWN_EVENT))

			Eventually(func() []obstest.Measurement {
				return metrics.Observations("gotthlive_events_rejected_total")
			}).ShouldNot(BeEmpty())

			Expect(metrics.Observations("gotthlive_events_rejected_total")[0].Attr("reason")).
				To(Equal("unknown_event"))
		})

		It("records no observation on any instrument when no provider is configured", func() {
			// The other half of the claim: one field turns the whole set on,
			// and leaving it nil costs a branch rather than a no-op call.
			quiet := obstest.NewMetrics()
			off := mount(nil)
			defer off.stop()

			off.send("counter.increment", nil)
			off.nextPatch()

			Expect(quiet.All()).To(BeEmpty())
			Expect(quiet.Registered()).To(BeEmpty())
		})
	})

	Describe("traces", func() {
		It("spans receive, authorize, transition, encode and the client's morph in one trace", func() {
			app.send("counter.increment", nil)
			patch := app.nextPatch()

			// The client reports how long it took to apply the patch, which is
			// the half of the path that arrives after the server span closed.
			app.sendTelemetry(patch.GetPatchId())

			// All eight phases FR-36 names, and the whole point of listing
			// them here is that five of them used to be absent: parse, reduce,
			// render, render.fragment and send were drawn in instrumentation
			// §3.1, declared (four of them) in internal/obs, and started
			// nowhere. A span set asserted at four names could not tell that
			// apart from a span set of four.
			Eventually(traces.Names).Should(ContainElements(
				obs.SpanParse,
				obs.SpanAuthorize,
				obs.SpanEvent,
				obs.SpanReduce,
				obs.SpanRender,
				obs.SpanRenderFragment,
				obs.SpanEncode,
				obs.SpanSend,
				obs.SpanClientMorph,
			), func() string { return "recorded spans:\n" + traces.Describe() })

			By("one trace, not four")
			ids := map[trace.TraceID]bool{}
			for _, s := range traces.Spans() {
				ids[s.TraceID] = true
			}
			Expect(ids).To(HaveLen(1), "the path is split across %d traces:\n%s", len(ids), traces.Describe())

			By("every span carrying the session")
			for _, s := range traces.Spans() {
				Expect(s.Attr(obs.AttrSessionID)).NotTo(BeEmpty(),
					"span %q carries no session id", s.Name)
			}

			By("the causal identifiers on the spans that own them")
			event := traces.Named(obs.SpanEvent)
			Expect(event).NotTo(BeEmpty())
			Expect(event[0].Attr(obs.AttrEventName)).To(Equal("counter.increment"))
			Expect(event[0].Attr(obs.AttrEventID)).NotTo(Equal("0"))
			Expect(event[0].Attr(obs.AttrTransitionID)).NotTo(BeEmpty())
			Expect(event[0].Attr(obs.AttrStateVersion)).NotTo(BeEmpty())

			encode := traces.Named(obs.SpanEncode)
			Expect(encode).NotTo(BeEmpty())
			var patchSpan obstest.Span
			for _, s := range encode {
				if s.Attr(obs.AttrPatchID) == itoa(int(patch.GetPatchId())) {
					patchSpan = s
				}
			}
			Expect(patchSpan.Name).To(Equal(obs.SpanEncode),
				"no encode span carries patch %d:\n%s", patch.GetPatchId(), traces.Describe())
			Expect(patchSpan.Attr(obs.AttrServerSeq)).To(Equal(itoa(int(patch.GetServerSeq()))))

			By("the transition descending from the authorization that admitted it")
			// This was a link until FR-36 clause 4, and the change is not
			// cosmetic: a link leaves the transition a sampler root, and at
			// instrumentation §3.5's own default that cost 0 of 300
			// interactions recording authorize and event together. obstest
			// cannot express a sampling decision (D-11), so what it can hold is
			// the edge that carries one — the parent pointer.
			byID := map[trace.SpanID]obstest.Span{}
			for _, s := range traces.Spans() {
				byID[s.SpanID] = s
			}
			parent, ok := byID[event[0].ParentID]
			Expect(ok).To(BeTrue(),
				"the transition span is a root, so it makes its own sampling decision "+
					"and FR-36 clause 4 is not met:\n%s", traces.Describe())
			Expect(parent.Name).To(Equal(obs.SpanAuthorize),
				"the transition span descends from %q rather than from the authorization "+
					"that admitted it:\n%s", parent.Name, traces.Describe())
			Expect(event[0].Links).To(BeEmpty(),
				"the transition still carries the link clause 4 replaced, so the "+
					"enumeration in instrumentation §3.1 is wrong by one site")

			By("the client's morph linked to the span that encoded the patch it applied")
			morph := traces.Named(obs.SpanClientMorph)
			Expect(morph).To(HaveLen(1))
			Expect(morph[0].Attr(obs.AttrTimingSource)).To(Equal("client_reported"))
			Expect(morph[0].Attr(obs.AttrPatchID)).To(Equal(itoa(int(patch.GetPatchId()))))
			Expect(morph[0].Links).To(HaveLen(1))
			Expect(morph[0].Links[0].SpanID()).To(Equal(patchSpan.SpanID),
				"the morph span links somewhere other than the span that encoded its patch")
		})

		It("gives a server-initiated transition an origin span naming its cause", func() {
			Eventually(traces.Names).Should(ContainElement(obs.SpanOrigin))

			origin := traces.Named(obs.SpanOrigin)
			Expect(origin[0].Attr(obs.AttrOriginKind)).To(Equal("MOUNT"))
			Expect(origin[0].Attr(obs.AttrOriginSource)).To(Equal("mount"))
		})

		It("closes every span it opens", func() {
			app.send("counter.increment", nil)
			app.nextPatch()

			Eventually(func() []string {
				var open []string
				for _, s := range traces.Spans() {
					if !s.Ended {
						open = append(open, s.Name)
					}
				}
				return open
			}, 2*time.Second).Should(BeEmpty())
		})
	})
})

// itoa keeps the attribute comparisons readable; span attributes are recorded
// as the strings OpenTelemetry emits them as.
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
