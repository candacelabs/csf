package chaos_test

import (
	"fmt"
	"runtime"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/obstest"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// PRD Phase 3, case 6:
//
//	Network partition and half-open connection → heartbeat detects within the
//	configured bound, resources reclaimed (FR-12).
//
// The partition is a relay that keeps both sockets open, keeps READING from
// both, and forwards nothing. That is the only construction that produces a
// genuinely half-open connection: every write either side makes succeeds into a
// socket that is still alive, nothing arrives at the other end, and no error is
// reported to anybody. A relay that stopped reading instead would apply TCP
// backpressure and the server would eventually see a stalled write — a
// different failure, covered by the write deadline in case 4, and one that
// would let this spec pass without the heartbeat ever running.
//
// # The bound, in three terms rather than one
//
// The library's detection is in Actor.onTick: it compares now against the last
// inbound frame's timestamp and closes when the difference exceeds
// HeartbeatTimeout. onTick runs on a ticker at HeartbeatInterval, so detection
// costs at most HeartbeatTimeout + HeartbeatInterval.
//
// RECLAMATION costs one term more, and the measurement is what found it.
// Actor.Close calls the transport's close, which is coder/websocket's GRACEFUL
// close: it writes a close frame and waits for the peer to answer. The peer of
// a half-open connection is by definition never going to answer, so that wait
// runs to the library's own five-second close-handshake timeout, and only then
// does the read pump return, the actor's context cancel, and Teardown run. So
//
//	reclamation <= HeartbeatTimeout + HeartbeatInterval + closeHandshake(5 s)
//
// Measured at 2.5 s / 1 s: reclaimed after 7.98 s, against 3.5 s of detection.
// At the defaults that is 50 + 20 + 5 = 75 s rather than the 70 s the first two
// terms alone imply. That is D-27, and it is a five-second hold on a session's
// goroutines, mailbox and window after the library has already concluded the
// peer is dead.
var _ = Describe("A network partition and a half-open connection (PRD case 6, FR-12)", func() {

	const (
		// One second is the FLOOR, not a choice: protocol.md refines
		// Snapshot.heartbeat_interval_ms to [1000, 300000], so a shorter
		// interval makes the mount Snapshot unencodable and every session dies
		// at establishment. That is D-23, and it is what this file hit first
		// when it tried 300 ms.
		hbInterval = 1 * time.Second
		hbTimeout  = 2500 * time.Millisecond
		// The three terms, named separately so a failure says which one it
		// blew rather than only that the total was too large.
		detectionBound = hbTimeout + hbInterval
		// coder/websocket's close-handshake timeout, which a peer behind a
		// partition can never satisfy. It is not configurable through this
		// library (D-27).
		closeHandshake   = 5 * time.Second
		reclamationBound = detectionBound + closeHandshake
		margin           = 4 * time.Second

		// roundTrip is the one wall-clock term in this file that is not a
		// measurement, and it is sized for the slowest runner the project
		// supports rather than for the host that wrote it.
		//
		// It bounds "the client sent an event and the server answered it" on a
		// loopback socket — single-digit milliseconds of actual work here, and
		// nothing asserts it was fast, only that it happened. The runner that
		// has to satisfy it is GitHub's 2-vCPU shared ubuntu-24.04, where a
		// whole-process stall of a second or two under a noisy neighbour is
		// ordinary; ten seconds leaves room for several of them in a row. A
		// failure of an Eventually with this timeout therefore means the
		// answer never came, not that it came late.
		roundTrip = 10 * time.Second
	)

	It("detects the dead peer within HeartbeatTimeout + HeartbeatInterval and reclaims the session", func() {
		rec := obstest.NewMetrics()
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Logger = nil
			cfg.Metrics = rec
			cfg.Limits.HeartbeatInterval = hbInterval
			cfg.Limits.HeartbeatTimeout = hbTimeout
			// Long, so that the close this spec observes can only be the
			// heartbeat's: an idle timeout or a slow-client eviction landing
			// first would make the assertion pass for the wrong reason.
			cfg.Limits.IdleTimeout = 10 * time.Minute
			cfg.Limits.SlowClientGrace = 10 * time.Minute
			cfg.Limits.WriteDeadline = 10 * time.Minute
		})

		r := newRelay(s.addr())
		w := dialWire(r.addr(), wireOpts{acks: ackAuto})

		// A live, healthy session first, so that what is measured afterwards is
		// a transition from alive to detected-dead. 1 is the mount Snapshot's
		// server_seq, so this waits for the commit's Patch on top of it — a
		// constant known before the event is sent, which is what keeps the
		// bound out of the race the bystander spec below documents.
		w.commit(0)
		Eventually(w.appliedSeq, roundTrip).Should(BeNumerically(">", 1))

		goroutinesBefore := settledGoroutines()
		Expect(s.ledger.liveSessions()).To(Equal(1))

		partitionedAt := time.Now()
		r.partition()

		// The server must notice. The client will not: it is on the other side
		// of the partition and sees a socket that is still open, which is
		// exactly the condition FR-12 exists for.
		Eventually(s.ledger.liveSessions, reclamationBound+margin, 20*time.Millisecond).
			Should(BeZero(),
				"the server did not reclaim a half-open session within %s", reclamationBound+margin)
		reclaimed := time.Since(partitionedAt)

		Expect(reclaimed).To(BeNumerically("<=", reclamationBound+margin),
			"reclamation took %s against a bound of %s = detection %s + close handshake %s (+%s margin)",
			reclaimed, reclamationBound, detectionBound, closeHandshake, margin)

		// D-27, held where it can fail: reclamation is measurably LATER than
		// detection, by about the close-handshake timeout. If this ever comes in
		// under the detection bound, the graceful close has been replaced and
		// D-27 is closed.
		Expect(reclaimed).To(BeNumerically(">", detectionBound),
			"reclamation at %s came in under the detection bound of %s: the close against an "+
				"unresponsive peer is no longer waiting out the handshake, so D-27 is fixed and this "+
				"spec should assert the tighter bound", reclaimed, detectionBound)

		// Resources reclaimed: goroutines back to where they were before the
		// partition, once the session is gone.
		Eventually(settledGoroutines, 30*time.Second, 200*time.Millisecond).
			Should(BeNumerically("<=", goroutinesBefore+2),
				"goroutines outlived the half-open session")

		// The enumerated reason, read from the SERVER rather than from the
		// client. It cannot be read from the client here and the reason is the
		// fault itself: a partition drops the close frame along with everything
		// else, so the client's socket ends with no WebSocket status at all.
		// gotthlive_connections_closed_total carries the label instead, which is
		// also the signal an operator would use.
		var codes []string
		for _, m := range rec.Observations("gotthlive_connections_closed_total") {
			codes = append(codes, m.Attr("code"))
		}
		Expect(codes).To(ContainElement("heartbeat_timeout"),
			"the close was not recorded as HEARTBEAT_TIMEOUT; codes seen: %v", codes)

		// And the client does learn the connection is over, once the partition
		// heals and its own socket ends.
		r.heal()
		Eventually(w.isClosed, 20*time.Second).Should(BeTrue(),
			"the client never noticed the connection had gone")

		AddReportEntry("case 6", fmt.Sprintf(
			"half-open partition: session RECLAIMED after %s, against a bound of %s "+
				"(detection %s = timeout %s + interval %s, plus a %s close handshake against a peer that "+
				"cannot answer — D-27); close %d; goroutines %d -> %d",
			reclaimed.Round(time.Millisecond), reclamationBound, detectionBound, hbTimeout, hbInterval,
			closeHandshake, w.code(), goroutinesBefore, settledGoroutines()))
	})

	It("leaves other sessions serving while one is partitioned", func() {
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Logger = nil
			cfg.Limits.HeartbeatInterval = hbInterval
			cfg.Limits.HeartbeatTimeout = hbTimeout
			cfg.Limits.IdleTimeout = 10 * time.Minute
		})

		r := newRelay(s.addr())
		partitioned := dialWire(r.addr(), wireOpts{acks: ackAuto})
		healthy := dialWire(s.addr(), wireOpts{acks: ackAuto})

		partitioned.commit(0)
		healthy.commit(0)
		// Again 1 is the mount Snapshot; this is the commit's Patch landing on
		// top of it, and it also settles the wire so that the sample taken
		// below has nothing in flight behind it.
		Eventually(healthy.appliedSeq, roundTrip).Should(BeNumerically(">", 1))

		r.partition()
		Eventually(s.ledger.liveSessions, reclamationBound+margin, 20*time.Millisecond).
			Should(Equal(1), "the partitioned session was not reclaimed, or the healthy one was")

		// The bound is a COUNTED protocol event, and the ORDER of these two
		// lines is the whole of the fix.
		//
		// This read used to sit after the commit — commit, sample, wait for the
		// sequence to pass the sample — which made the spec's outcome depend on
		// whether that commit's Patch had already been applied when the sample
		// landed. On a 2-vCPU shared runner it had: `before` came back 3, the
		// Patch it was then waiting to overtake was the one already counted in
		// that 3, no further event was ever sent, and the Eventually ran its
		// window out at 10.001 s (run 30942082684, job 92102786176). Widening
		// the window would have bought nothing; a bound sampled out of a race
		// is not a bound, and this one was unreachable at any timeout.
		//
		// Sampled first, the arithmetic is the protocol's rather than the
		// scheduler's: the session has applied `before` sequenced frames,
		// exactly one state-changing event is sent, the server answers it with
		// exactly one Patch, so the applied sequence lands on before+1 and
		// stops there. Equal rather than ">" because "one event, one patch" is
		// the stronger statement and it holds identically on every machine.
		before := healthy.appliedSeq()
		healthy.commit(0)
		Eventually(healthy.appliedSeq, roundTrip).Should(Equal(before+1),
			"the bystander session applied %d frames before its neighbour was partitioned and "+
				"never applied the one patch its own next event asks for", before)
		Expect(healthy.isClosed()).To(BeFalse())
	})

	It("evicts an idle session on the configured bound rather than holding it forever (FR-22)", func() {
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Logger = nil
			cfg.Limits.HeartbeatInterval = time.Second
			// Long enough that a client sending nothing but heartbeats stays
			// liveness-healthy, which is what makes the idle timeout the thing
			// under test rather than the heartbeat.
			cfg.Limits.HeartbeatTimeout = 60 * time.Second
			cfg.Limits.IdleTimeout = 3 * time.Second
		})

		// The client echoes the server's heartbeats, so it stays
		// liveness-healthy for the whole spec. That is what makes the idle
		// timeout the thing under test: a heartbeat is a frame, so it resets the
		// LIVENESS clock, and it is not an event, so it does not reset the IDLE
		// clock. A session with a healthy peer and no interactions is exactly
		// what FR-22's eviction is for.
		w := dialWire(s.addr(), wireOpts{acks: ackAuto})

		Eventually(w.isClosed, 20*time.Second).Should(BeTrue(),
			"an idle session was never evicted")
		Expect(w.code()).To(Equal(websocket.StatusCode(4011)),
			"the close code for an idle eviction is SESSION_EVICTED")
	})
})

// settledGoroutines counts goroutines after letting the scheduler quiesce, so
// the figure is the settled one rather than whatever was mid-exit.
//
// CS-9 keep: a best-effort quiesce is not an await. Both callers wrap this in
// an Eventually that polls it, so it must return whatever it last read rather
// than failing — a fatal failure inside a poll aborts the retry that is doing
// the waiting.
func settledGoroutines() int {
	var last int
	for i := 0; i < 25; i++ {
		runtime.Gosched()
		time.Sleep(20 * time.Millisecond)
		n := runtime.NumGoroutine()
		if n == last {
			return n
		}
		last = n
	}
	return last
}
