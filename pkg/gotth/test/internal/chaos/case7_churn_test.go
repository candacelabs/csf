package chaos_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// commitFrameFor builds one well-formed commit event for a session identifier
// read off the wire, so a churn cycle can put a transition in flight without
// needing the wire helper's bookkeeping.
func commitFrameFor(sessionID []byte, ref uint64) []byte {
	GinkgoHelper()
	b, err := proto.Marshal(&pb.Frame{
		ProtocolVersion: 1,
		SessionId:       sessionID,
		Payload: &pb.Frame_Event{Event: &pb.Event{
			ClientRef:  ref,
			Name:       "chaos.commit",
			FragmentId: "total",
			Fields: []*pb.EventField{
				{Key: "ref", Value: fmt.Sprint(ref)},
				{Key: "delay", Value: "0s"},
			},
		}},
	})
	Expect(err).NotTo(HaveOccurred())
	return b
}

// PRD Phase 3, case 7:
//
//	Rapid connect/disconnect churn (10k cycles) → no goroutine, timer, or heap
//	leak (FR-22).
//
// internal/wsx already runs 10,000 cycles of CLEAN disconnects and asserts
// goroutines, live heap and RSS against derived budgets (D-10, closed by
// 6f241373 and re-verified in docs/qa/checkpoint-3-chaos.md §3). Repeating that
// here would be a second copy of one claim.
//
// What this file churns instead is the ABNORMAL disconnect, which is the one a
// chaos suite owes: the socket is aborted with no WebSocket close handshake, so
// the server's read fails rather than returning a close frame, and — critically
// — half the cycles abort while a transition is in flight, so the actor is
// mid-render when its connection dies. Those are different exit paths through
// Actor.shutdown than a clean close takes, and a resource that leaks on one of
// them leaks nowhere the existing soak looks.
//
// The timer half of FR-22's sentence is asserted the only way Go permits.
// runtime/metrics exports no timer count, so a leaked *time.Ticker is invisible
// as a timer; what it is not invisible as is retained heap, because the runtime
// timer holds the ticker and the ticker holds its channel. The live-heap budget
// below is therefore what stands behind "no timer leak", and this comment is
// here so the next reader knows that rather than assuming a check exists that
// does not.
var _ = Describe("Rapid abnormal connect/disconnect churn (PRD case 7, FR-22)", func() {

	churn := func(addr string, n int, midFlight bool) {
		GinkgoHelper()
		headers := http.Header{}
		headers.Set("Origin", chaosOrigin)

		for i := 0; i < n; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			conn, _, err := websocket.Dial(ctx, wsURL(addr), &websocket.DialOptions{
				HTTPHeader:   headers,
				Subprotocols: []string{"gotth-live.v1"},
			})
			if err != nil {
				cancel()
				Expect(err).NotTo(HaveOccurred(), "cycle %d could not connect", i)
			}
			// Read the mount Snapshot, so every cycle allocates a full session
			// rather than dying inside the handshake.
			_, first, err := conn.Read(ctx)
			Expect(err).NotTo(HaveOccurred(), "cycle %d never received its Snapshot", i)

			if midFlight && i%2 == 0 {
				// An event whose transition will be in progress when the socket
				// dies: the actor is rendering and about to write into a
				// connection that has just been reset.
				var snap pb.Frame
				Expect(proto.Unmarshal(first, &snap)).To(Succeed())
				_ = conn.Write(ctx, websocket.MessageBinary, commitFrameFor(snap.GetSessionId(), uint64(i+1)))
			}

			// CloseNow aborts: no close frame, no handshake, the server's read
			// returns an error rather than a status.
			_ = conn.CloseNow()
			cancel()
		}
	}

	measure := func(cycles int, midFlight bool) {
		GinkgoHelper()
		s := serve(func(cfg *live.Config[board, chaosUser]) { cfg.Logger = nil })

		// A warm-up before either baseline, for the same reason
		// equivalence-spec §3.6 warms up before M(0): a baseline on a process
		// that has never served a connection charges the measured window with
		// every one-time cost of serving the first one.
		churn(s.addr(), 500, midFlight)

		Eventually(s.ledger.liveSessions, 60*time.Second).Should(BeZero())
		baselineGoroutines := settledGoroutines()
		baselineHeap := liveHeap()

		start := time.Now()
		churn(s.addr(), cycles, midFlight)
		elapsed := time.Since(start)

		Eventually(s.ledger.liveSessions, 120*time.Second).Should(BeZero(),
			"sessions outlived the connections that owned them")
		Eventually(settledGoroutines, 60*time.Second, 500*time.Millisecond).
			Should(BeNumerically("<=", baselineGoroutines+4),
				"goroutines outlived the aborted connections")

		// The same instrument and the same per-cycle budget internal/wsx
		// derives for D-10: 64 B/cycle is half the 128 B fragment-hash line,
		// the smallest per-session line RFC-0001 §6.2 sizes, so a cycle
		// retaining even that smallest line fails here. The fixed allowance is
		// charged once, not per cycle.
		const perCycle = 64
		const fixed = 256 << 10
		budget := int64(fixed) + int64(cycles)*perCycle

		var retained int64
		Eventually(func() int64 {
			retained = liveHeap() - baselineHeap
			return retained
		}, 120*time.Second, 2*time.Second).Should(BeNumerically("<=", budget),
			"%d aborted cycles retained %d B of live heap against a budget of %d B", cycles, retained, budget)

		AddReportEntry(fmt.Sprintf("case 7 — %d abrupt cycles (mid-flight=%v)", cycles, midFlight),
			fmt.Sprintf("%s wall; goroutines %d -> %d; live heap retained %d B (%.1f B/cycle) against %d B",
				elapsed.Round(time.Millisecond), baselineGoroutines, settledGoroutines(),
				retained, float64(retained)/float64(cycles), budget))
	}

	It("returns goroutines and live heap to baseline across three hundred aborted cycles", func() {
		measure(300, true)
	})

	It("returns goroutines and live heap to baseline across ten thousand aborted cycles", Label("soak"), func() {
		soakOnly()
		measure(10000, true)
	})

	It("survives a connection aborted before the handshake completes", func() {
		s := serve(func(cfg *live.Config[board, chaosUser]) { cfg.Logger = nil })
		baseline := settledGoroutines()

		// A raw TCP connection that sends a partial upgrade request and then
		// resets. Nothing here is a WebSocket, which is the point: the failure
		// path is inside net/http rather than inside the library, and a session
		// that got allocated anyway would show as a goroutine that never exits.
		for i := 0; i < 300; i++ {
			c, err := net.DialTimeout("tcp", s.addr(), 5*time.Second)
			Expect(err).NotTo(HaveOccurred())
			_, _ = c.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\n"))
			if tcp, ok := c.(*net.TCPConn); ok {
				_ = tcp.SetLinger(0)
			}
			_ = c.Close()
		}

		Eventually(settledGoroutines, 60*time.Second, 500*time.Millisecond).
			Should(BeNumerically("<=", baseline+4),
				"aborted half-handshakes left goroutines behind")
		Expect(s.ledger.mounts.Load()).To(BeZero(),
			"a session was mounted for a connection that never upgraded")

		// The process is still serving.
		w := dialWire(s.addr(), wireOpts{acks: ackAuto})
		Expect(w.snapshot.GetServerSeq()).To(Equal(uint64(1)))
	})
})
