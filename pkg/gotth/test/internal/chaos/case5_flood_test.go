package chaos_test

import (
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/obstest"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/patience"
)

// PRD Phase 3, case 5:
//
//	Event flood from a hostile client → rate limit engages, typed error,
//	defined close, no unbounded allocation (FR-51).
//
// The first two clauses are already asserted in
// test/internal/conformance/limits_test.go. What is here is the two nothing
// measured:
//
//   - "no unbounded allocation" — a rate limiter that refuses correctly and
//     allocates per refused frame is still an allocation vector, and the
//     library emits an Error frame per refusal, so this is not a hypothetical.
//   - "defined close" — which turns out to be REACHABLE ONLY ABOVE A RATE, and
//     that rate is 300× the configured events-per-second. That is D-24, and the
//     two specs below are written so that the boundary is measured from both
//     sides rather than asserted from one.
var _ = Describe("An event flood from a hostile client (PRD case 5, FR-51)", func() {

	It("reaches the defined close when the flood outruns the bucket's refill", func() {
		// Session.ingressEvent closes after consecutiveDenialsBeforeClose ×
		// EventBurst = 3 × EventBurst CONSECUTIVE denials, and one allowed
		// event resets the run to zero. Tokens arrive at MaxEventsPerSecond, so
		// the close needs 3 × EventBurst frames inside one refill interval —
		// i.e. a flood rate above 3 × EventBurst × MaxEventsPerSecond.
		//
		// At 5/s and a burst of 5 that threshold is 75 frames/s, which a local
		// socket clears trivially. At the DEFAULTS it is 15,000 frames/s, which
		// is the point of the next spec.
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Logger = nil
			cfg.Limits.MaxEventsPerSecond = 5
			cfg.Limits.EventBurst = 5
		})
		w := dialWire(s.addr(), wireOpts{acks: ackAuto, noCapture: true})
		frame := w.commitBytes(1)

		sent := 0
		for i := 0; i < 20000 && !w.isClosed(); i++ {
			if err := w.sendBytes(frame); err != nil {
				break
			}
			sent++
		}

		Eventually(w.isClosed, 30*time.Second).Should(BeTrue(),
			"a flood above the close threshold was never closed")
		Expect(w.code()).To(Equal(websocket.StatusCode(4008)),
			"the close code for a sustained inbound flood is RATE_LIMITED")

		_, _, _, limited := w.counters()
		Expect(limited).To(BeNumerically(">", 0), "the flood produced no typed RATE_LIMITED error")

		AddReportEntry("case 5 — close reachable", fmt.Sprintf(
			"MaxEventsPerSecond=5, EventBurst=5 (threshold 3×5×5 = 75 frames/s): %d frames sent, %d RATE_LIMITED errors, close %d",
			sent, limited, w.code()))
	})

	// D-24. Below the threshold above, the session is NEVER closed: the server
	// answers every refused event with its own Error frame, indefinitely, and
	// the connection stays open. FR-51 says exceeding a limit "MUST produce a
	// typed error and a defined close"; what a client flooding at 100× the
	// configured rate gets is the typed error and no close at all, plus an
	// outbound byte stream larger than the inbound one that provoked it.
	//
	// The spec asserts the CURRENT behaviour, so it goes red the day the close
	// becomes reachable here — which is the signal that D-24 has been acted on.
	It("does not reach a defined close below that rate, and answers every refused frame instead (D-24)", func() {
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Logger = nil
			cfg.Limits.MaxEventsPerSecond = 50 // the documented default
			cfg.Limits.EventBurst = 100        // the documented default
		})
		w := dialWire(s.addr(), wireOpts{acks: ackAuto, noCapture: true})

		// One frame, built once and sent from a slice, so the client's own
		// allocation does not appear in the process heap the server shares.
		frame := w.commitBytes(1)
		baseline := liveHeap()

		// PACED, and the pacing is the whole claim rather than a convenience.
		// The close needs 3 x EventBurst CONSECUTIVE denials and one allowed
		// event resets the run, so at the defaults it needs 300 denials inside
		// one 20 ms refill interval. This caps sending at 3,000 frames/s —
		// sixty times the configured 50/s limit and a fifth of the close
		// threshold — which is the rate a hostile client would pick if it wanted
		// the session to survive. Unpaced, the same loop measured 10,124
		// frames/s on a loaded host and crossed the threshold on an idle one, so
		// the spec's result was the machine's rather than the library's.
		const rate = 3000
		const duration = 4 * time.Second
		const batch = 30
		batchDrainBudget := patience.Budget{Within: 10 * time.Second, Interval: time.Millisecond}
		start := time.Now()
		sent := 0
		bytesOut := 0
		for time.Since(start) < duration {
			for i := 0; i < batch; i++ {
				if err := w.sendBytes(frame); err != nil {
					break
				}
				sent++
				bytesOut += len(frame)
			}
			if w.isClosed() {
				break
			}
			// Sender pacing alone does not bound server ingress: scheduling can
			// bunch several writes in the socket buffer. Drain each small batch
			// through the real reducer/error stream before pacing the next one.
			// This also proves every sent frame was committed or explicitly refused.
			patience.Await(GinkgoTB(), "flood batch committed or refused", batchDrainBudget,
				func() int {
					_, _, errorsReceived, _ := w.counters()
					return s.ledger.callCount() + int(errorsReceived)
				}, func(accounted int) bool { return accounted == sent })
			// CS-9 keep: this sleep IS the flood's rate. It is the load
			// generator's throttle, which is the independent variable of the
			// whole case; replacing it with a wait would delete the experiment.
			time.Sleep(time.Duration(float64(batch) / rate * float64(time.Second)))
		}
		elapsed := time.Since(start)

		// Every batch has drained, so no arbitrary post-send sleep is needed.
		_, bytesIn, errs, limited := w.counters()

		Expect(limited).To(BeNumerically(">", 0),
			"the flood produced no typed RATE_LIMITED error at all")
		Expect(float64(sent)/elapsed.Seconds()).To(BeNumerically("<", 15000),
			"the sender outran the close threshold, so this run says nothing about the rate below it")
		Expect(w.isClosed()).To(BeFalse(),
			"the flood DID reach a defined close at %.0f frames/s, below the 15,000 frames/s threshold: "+
				"D-24 has been fixed and this spec should now be the FR-51 assertion rather than the "+
				"characterisation of its absence", float64(sent)/elapsed.Seconds())

		// The allocation claim, which holds either way. 4 MiB is roughly 180
		// times the 22,239 B of live heap the G2 baseline measures for one idle
		// session, so a few bytes retained per refused frame fails this and
		// ordinary allocator noise does not.
		const budget = 4 << 20
		var retained int64
		Eventually(func() int64 {
			retained = liveHeap() - baseline
			return retained
		}, 60*time.Second, 2*time.Second).Should(BeNumerically("<=", int64(budget)),
			"%d refused frames retained %d bytes of live heap", sent, retained)

		AddReportEntry("case 5 — D-24", fmt.Sprintf(
			"defaults (50/s, burst 100; close threshold 3x100x50 = 15,000 frames/s): %d frames in %s "+
				"(%.0f frames/s, feedback-paced ceiling 3000/s), %d error frames back (%d RATE_LIMITED), connection STILL OPEN, "+
				"%d B sent / %d B received back, live heap retained %d B of a %d B budget",
			sent, elapsed.Round(time.Millisecond), float64(sent)/elapsed.Seconds(),
			errs, limited, bytesOut, bytesIn, retained, budget))
	})

	// D-28 rides along with this one. protocol.md §8.3 enumerates 4007
	// FRAME_TOO_LARGE for H-5, and FR-8 says "closed for unknown reason is a
	// bug" — but the read limit is enforced by the WebSocket library, which
	// closes with its own RFC 6455 status 1009 before any gotth-live code sees
	// the frame. conn.noteReadError then returns early because
	// websocket.CloseStatus(err) is not -1, finalCode() falls back to
	// CloseNormal, and gotthlive_connections_closed_total records the close as
	// `normal`. So 4007 is unreachable, the client is told 1009, and the
	// operator's dashboard is told "normal".
	It("refuses an oversize frame at the transport without allocating its payload, and closes with 1009 rather than 4007 (FR-13, H-5, D-28)", func() {
		rec := obstest.NewMetrics()
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Logger = nil
			cfg.Metrics = rec
			cfg.Limits.MaxInboundFrameBytes = 4096
		})
		w := dialWire(s.addr(), wireOpts{acks: ackAuto, noCapture: true})

		baseline := liveHeap()

		// One megabyte, sixteen times, against a four-kilobyte limit. If the
		// payload were buffered before the check, the retention would show.
		payload := make([]byte, 1<<20)
		for i := range payload {
			payload[i] = byte(i)
		}
		for i := 0; i < 16 && !w.isClosed(); i++ {
			if err := w.sendBytes(payload); err != nil {
				break
			}
			// CS-9 keep: pacing the sender, not waiting on it. Sixteen
			// megabytes shoved down the socket at once would measure the
			// kernel's buffer rather than whether the server retains a frame
			// it refused.
			time.Sleep(20 * time.Millisecond)
		}

		Eventually(w.isClosed, 20*time.Second).Should(BeTrue(),
			"an oversize frame did not end the connection")

		const budget = 4 << 20
		var retained int64
		Eventually(func() int64 {
			retained = liveHeap() - baseline
			return retained
		}, 60*time.Second, 2*time.Second).Should(BeNumerically("<=", int64(budget)),
			"oversize frames retained %d bytes", retained)

		// D-28, asserted as measured so it goes red when the enumerated code is
		// used.
		Expect(w.code()).To(Equal(websocket.StatusCode(1009)),
			"the client was told %d rather than the transport's 1009. If it is now 4007, D-28 is fixed "+
				"and this spec should assert the enumerated code", w.code())

		var labels []string
		for _, m := range rec.Observations("gotthlive_connections_closed_total") {
			labels = append(labels, m.Attr("code"))
		}
		Expect(labels).NotTo(ContainElement("frame_too_large"),
			"gotthlive_connections_closed_total now records frame_too_large: D-28 is fixed")

		AddReportEntry("case 5 — oversize frames (D-28)", fmt.Sprintf(
			"16 × 1 MiB against a 4 KiB limit: client told close %d (protocol.md §8.3 enumerates 4007 for this), "+
				"server recorded gotthlive_connections_closed_total{code} = %v, live heap retained %d B",
			w.code(), labels, retained))
	})

	It("bounds the mailbox rather than queueing, and says so with a typed error", func() {
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Logger = nil
			cfg.Limits.MailboxDepth = 4
			// The bucket must not be the thing that refuses, or the mailbox
			// bound is never reached and this spec asserts the previous one.
			cfg.Limits.MaxEventsPerSecond = 100000
			cfg.Limits.EventBurst = 100000
		})
		w := dialWire(s.addr(), wireOpts{acks: ackAuto, noCapture: true})

		// Each commit schedules a slow effect, so the actor goroutine is busy
		// and the mailbox fills behind it.
		for i := 0; i < 400 && !w.isClosed(); i++ {
			w.commit(50 * time.Millisecond)
		}

		Eventually(func() int64 {
			_, _, _, limited := w.counters()
			return limited
		}, 20*time.Second).Should(BeNumerically(">", 0),
			"a saturated mailbox neither blocked nor refused: it must refuse")

		// Never the process, and never the connection either: a full mailbox is
		// a refusal, not a close.
		Expect(w.isClosed()).To(BeFalse())
	})

	// D-22 previously let refused handshakes decrement the active gauge.
	// Regression: only registry membership changes the active gauge.
	It("keeps gotthlive_sessions_active at zero after rejected handshakes (D-22, FR-34)", func() {
		rec := obstest.NewMetrics()
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Logger = nil
			cfg.Metrics = rec
		})

		client := &http.Client{}
		const attempts = 50
		for i := 0; i < attempts; i++ {
			req, err := http.NewRequest(http.MethodGet, s.http.URL+"/live", nil)
			Expect(err).NotTo(HaveOccurred())
			req.Header.Set("Origin", "https://not-allowed.example")
			resp, err := client.Do(req)
			Expect(err).NotTo(HaveOccurred())
			_ = resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
		}

		active := rec.Total("gotthlive_sessions_active")
		closed := rec.Total("gotthlive_connections_closed_total")
		opened := rec.Total("gotthlive_connections_total")

		AddReportEntry("case 5 — D-22", fmt.Sprintf(
			"%d rejected upgrades: gotthlive_sessions_active = %.0f, gotthlive_connections_total = %.0f, gotthlive_connections_closed_total = %.0f",
			attempts, active, opened, closed))

		Expect(active).To(BeZero(), "rejected handshakes never enter the live registry")
		Expect(opened).To(BeZero(),
			"no session was opened")
	})

	// D-23. live.Limits.validate() checks only for negative values and
	// CoalesceFlushAt's ceiling, but three more fields are copied verbatim into
	// the mount Snapshot's refined session parameters:
	//
	//	heartbeat_interval_ms    this >= 1000 && this <= 300000
	//	max_inbound_frame_bytes  this >= 1024 && this <= 1048576
	//	ack_window               this >= 1 && this <= 256
	//
	// A value outside any of those was accepted by live.New and then made the
	// mount Snapshot unencodable, so EVERY session on that configuration died at
	// establishment with Error{INTERNAL} "the server could not encode an
	// update" and a log line telling the operator it is a library bug. It was
	// exactly the D-14 defect class the same function was extended to close,
	// three fields wider.
	//
	// CLOSED. live.Limits.validate now checks all three against the ranges
	// named in internal/protocol, so the failure this table measured is
	// unreachable from live.New — which is what the entries assert now, per the
	// instruction the earlier version of this table carried. The wire half of
	// the closure lives in live's own suite, which mounts a real session at each
	// end of each range and reads the parameter back off the Snapshot.
	DescribeTable("refuses at construction a limit that would kill every session at mount (D-23)",
		func(mutate func(limits *live.Limits), field string) {
			cfg := chaosConfig(newLedger())
			cfg.Logger = nil
			mutate(&cfg.Limits)

			_, err := live.New(cfg)

			var cfgErr *live.ConfigError
			Expect(errors.As(err, &cfgErr)).To(BeTrue(),
				"%s outside the protocol's refined range was accepted by live.New, so it reaches a "+
					"session and the mount Snapshot becomes a frame the library refuses to send: "+
					"D-23 has regressed; got %v", field, err)
			Expect(cfgErr.Field).To(Equal("Limits."+field),
				"the rejection named %q, so the operator is sent to the wrong field", cfgErr.Field)
		},
		Entry("HeartbeatInterval below the 1 s floor",
			func(l *live.Limits) { l.HeartbeatInterval = 300 * time.Millisecond }, "HeartbeatInterval"),
		Entry("HeartbeatInterval above the 300 s ceiling",
			func(l *live.Limits) { l.HeartbeatInterval = 301 * time.Second }, "HeartbeatInterval"),
		Entry("MaxInboundFrameBytes below the 1024 B floor",
			func(l *live.Limits) { l.MaxInboundFrameBytes = 512 }, "MaxInboundFrameBytes"),
		Entry("MaxInboundFrameBytes above the 1 MiB ceiling",
			func(l *live.Limits) { l.MaxInboundFrameBytes = (1 << 20) + 1 }, "MaxInboundFrameBytes"),
		Entry("AckWindow above the 256 ceiling",
			func(l *live.Limits) { l.AckWindow = 257 }, "AckWindow"),
	)
})

// liveHeap is what the last garbage collection found reachable, after the
// runtime has been made to collect and release synchronously.
//
// Same instrument as the D-10 churn check in internal/wsx, and for the same
// reason: it is blind to the race detector's shadow and to allocator arena
// growth, both of which dominate RSS in a `go test -race` process and neither
// of which is memory this library allocated.
func liveHeap() int64 {
	for i := 0; i < 3; i++ {
		runtime.GC()
		debug.FreeOSMemory()
	}
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(sample)
	return int64(sample[0].Value.Uint64())
}
