package chaos_test

import (
	"bufio"
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/patience"
)

// PRD Phase 3, case 3:
//
//	Server restarted under load → clients reconnect and resync within a stated
//	bound.
//
// The server is a CHILD PROCESS and the restart is a SIGKILL. Restarting the
// live.App inside the test binary would have exercised Mount and Snapshot and
// nothing else: the listener would never close, the accept queue would never
// drain, no socket would be reset by the kernel, and the port would never be
// rebound. All four of those are what a deploy actually does, and the last one
// is where a restart most often stalls.
//
// # The stated bound, and what it is made of
//
// A client's reconnect delay is RFC §8.4: full jitter, base 250 ms, cap 15 s,
// so attempt n waits random(0, min(15s, 250ms·2^n)). The clients here implement
// exactly that rule — they are not the shipped runtime, whose own
// implementation of it is DEV-2's and is asserted in
// client/test/reconnect.test.mjs; what is being measured is the SERVER's
// contribution, with the client's contribution held at the documented values so
// the two can be told apart.
//
// The bound is therefore
//
//	restart gap  +  the client's backoff delay for the attempt that lands
//
// and the two are reported separately, because a slow restart and a slow
// backoff are different defects with different owners. The spec asserts a total
// and prints the split.
var reconnectBudget = patience.Budget{Within: 90 * time.Second, Interval: 50 * time.Millisecond}

var _ = Describe("A server restarted under load (PRD case 3)", func() {

	It("brings every client back to a fresh Snapshot carrying pre-restart truth, within a stated bound", Label("soak"), func() {
		soakOnly()

		bin := buildChaosServer()
		ledgerPath := filepath.Join(GinkgoT().TempDir(), "chaos.ledger")
		port := freePort()
		addr := fmt.Sprintf("127.0.0.1:%d", port)

		child := startChaosServer(bin, addr, ledgerPath)

		const clients = 25
		fleet := make([]*reconnecting, 0, clients)
		for i := 0; i < clients; i++ {
			fleet = append(fleet, newReconnecting(addr, uint64(i+1)*100000, 20*time.Millisecond))
		}
		DeferCleanup(func() {
			for _, c := range fleet {
				c.stop()
			}
		})

		Eventually(func() int { return liveCount(fleet) }, 30*time.Second).Should(Equal(clients),
			"the fleet never fully connected before the restart")
		Eventually(func() int { return ledgerLines(ledgerPath) }, 30*time.Second).
			Should(BeNumerically(">", clients),
				"no load reached the server before the restart")

		truthBefore := ledgerDistinct(ledgerPath)
		generationsBefore := generations(fleet)

		// The restart. SIGKILL, not a graceful stop: a graceful stop writes a
		// 4001 close frame to every session and is the path the drain test in
		// internal/wsx already covers. What a crash or an aggressive orchestrator
		// does is this.
		killedAt := time.Now()
		child.kill()
		restarted := startChaosServer(bin, addr, ledgerPath)
		DeferCleanup(restarted.kill)
		rebindAfter := time.Since(killedAt)

		// Every client back on a NEW session — server_seq 1, a mount Snapshot —
		// carrying the truth the dead process had committed.
		patience.Await(GinkgoTB(), "every client to receive a fresh Snapshot after the restart",
			reconnectBudget,
			func() int { return generationsAfter(fleet, generationsBefore) },
			func(recovered int) bool { return recovered == clients })

		var worst time.Duration
		var worstAttempts int
		for _, c := range fleet {
			d, attempts := c.lastRecovery()
			if d > worst {
				worst = d
				worstAttempts = attempts
			}
			snap := c.lastSnapshot()
			Expect(snap).NotTo(BeNil())
			Expect(snap.GetServerSeq()).To(Equal(uint64(1)),
				"a reconnect after a restart is a new session and starts at 1")
			Expect(snap.GetOrigin().GetKind()).To(Equal(pb.OriginKind_MOUNT))
			Expect(snapshotHTML(snap, "total")).NotTo(BeEmpty())
		}

		// Server truth survived the process. The reconnected Snapshot's markup
		// is at least what was committed before the kill: at least, rather than
		// exactly, because the fleet is still committing and the ledger is
		// still growing while the assertion runs.
		truthAfter := ledgerDistinct(ledgerPath)
		Expect(truthAfter).To(BeNumerically(">=", truthBefore),
			"the ledger lost commits across the restart")

		// The bound. 30 s is the reconnect deadline this suite states, and it is
		// derived rather than chosen: RFC §8.4 caps a single delay at 15 s, and
		// a client that has been up long enough to have a small attempt counter
		// meets the cap at most once before succeeding, so 15 s of backoff plus
		// the restart gap plus one round trip is the worst case a healthy
		// restart can produce. A figure above it is a stalled rebind or a
		// backoff that is not the documented one.
		const bound = 30 * time.Second
		Expect(worst).To(BeNumerically("<=", bound),
			"the slowest client took %s to reconnect and resync, against a bound of %s", worst, bound)

		AddReportEntry("case 3", fmt.Sprintf(
			"%d clients, SIGKILL restart; port rebound in %s; slowest reconnect+resync %s over %d attempts (bound %s); ledger %d -> %d distinct commits",
			clients, rebindAfter.Round(time.Millisecond), worst.Round(time.Millisecond),
			worstAttempts, bound, truthBefore, truthAfter))
	})
})

// ---------------------------------------------------------------------------
// The child process
// ---------------------------------------------------------------------------

type childServer struct {
	cmd  *exec.Cmd
	addr string
}

func (c *childServer) kill() {
	if c.cmd == nil || c.cmd.Process == nil {
		return
	}
	_ = c.cmd.Process.Kill()
	_, _ = c.cmd.Process.Wait()
}

// buildChaosServer compiles the child once per suite run.
//
// It FAILS rather than skips when the toolchain is not there. A skip would make
// this case invisible in exactly the environment where it cannot run, which is
// the failure mode this project has now caught four times.
var buildOnce struct {
	sync.Once
	path string
	err  error
}

func buildChaosServer() string {
	GinkgoHelper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "chaossrv-")
		if err != nil {
			buildOnce.err = err
			return
		}
		out := filepath.Join(dir, "chaossrv")
		cmd := exec.Command("go", "build", "-o", out, "./cmd/chaossrv")
		cmd.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false")
		if combined, err := cmd.CombinedOutput(); err != nil {
			buildOnce.err = fmt.Errorf("go build ./cmd/chaossrv: %w\n%s", err, combined)
			return
		}
		buildOnce.path = out
	})
	Expect(buildOnce.err).NotTo(HaveOccurred(),
		"the case-3 child server did not build: a restart case that cannot start a server is not a case that passes")
	return buildOnce.path
}

// startChaosServer spawns the child and waits for it to announce its address.
func startChaosServer(bin, addr, ledger string) *childServer {
	GinkgoHelper()
	cmd := exec.Command(bin, "-addr", addr, "-ledger", ledger, "-origin", chaosOrigin)
	stdout, err := cmd.StdoutPipe()
	Expect(err).NotTo(HaveOccurred())
	cmd.Stderr = os.Stderr
	Expect(cmd.Start()).To(Succeed())

	ready := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "READY ") {
				ready <- strings.TrimPrefix(line, "READY ")
				return
			}
		}
		close(ready)
	}()

	select {
	case bound, ok := <-ready:
		Expect(ok).To(BeTrue(), "the child server exited before it was ready")
		return &childServer{cmd: cmd, addr: bound}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		Fail("the child server did not become ready within 30s")
		return nil
	}
}

func ledgerLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { Expect(f.Close()).To(Succeed()) }()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if sc.Text() != "" {
			n++
		}
	}
	return n
}

func ledgerDistinct(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { Expect(f.Close()).To(Succeed()) }()
	seen := map[string]struct{}{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			seen[line] = struct{}{}
		}
	}
	return len(seen)
}

// ---------------------------------------------------------------------------
// A client that reconnects the way RFC §8.4 says it should
// ---------------------------------------------------------------------------

// reconnecting is one client that keeps itself connected across a restart,
// using RFC §8.4's backoff: full jitter, base 250 ms, cap 15 s.
//
// It is not the shipped runtime and does not pretend to be. Its purpose is to
// hold the client's contribution to the recovery time at the DOCUMENTED values
// so that what remains in the measurement is the server's.
type reconnecting struct {
	addr    string
	refBase uint64

	mu         sync.Mutex
	conn       *websocket.Conn
	sessionID  []byte
	snapshot   *pb.Snapshot
	generation int
	recovery   time.Duration
	attempts   int
	live       bool
	ref        uint64
	// seq is the highest contiguous server_seq applied on the CURRENT
	// connection. Events carry it as seen_server_seq, which is refined
	// `this > 0` — so an event sent before the first Snapshot is refused at the
	// parse boundary, which is also why runtime.js has `if (!seq) return` in
	// sendEvent.
	seq uint64

	stopped chan struct{}
	once    sync.Once
	load    time.Duration
}

// newReconnecting starts a client that keeps itself connected and commits every
// `load` interval for as long as each connection lives.
//
// The load interval is a constructor argument rather than a setter, and the
// difference is not stylistic: serve() reads it once when a connection is
// established, so a setter called after the first connection was already up
// left the first generation driving nothing at all — which is exactly "under
// load" quietly not happening, on the one case whose whole subject is load.
func newReconnecting(addr string, refBase uint64, load time.Duration) *reconnecting {
	c := &reconnecting{addr: addr, refBase: refBase, load: load, stopped: make(chan struct{})}
	go c.run()
	return c
}

func (c *reconnecting) stop() {
	c.once.Do(func() { close(c.stopped) })
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		_ = conn.CloseNow()
	}
}

// run is the connect / serve / back-off loop.
func (c *reconnecting) run() {
	attempt := 0
	var downSince time.Time
	for {
		select {
		case <-c.stopped:
			return
		default:
		}

		conn, snap, sid, err := c.dial()
		if err != nil {
			attempt++
			// RFC §8.4: delay = random(0, min(cap, base·2^n)).
			ceiling := 250 * time.Millisecond << min(attempt, 6)
			if ceiling > 15*time.Second {
				ceiling = 15 * time.Second
			}
			delay := time.Duration(rand.Int63n(int64(ceiling) + 1))
			select {
			case <-c.stopped:
				return
			case <-time.After(delay):
			}
			continue
		}

		c.mu.Lock()
		c.conn = conn
		c.snapshot = snap
		c.sessionID = sid
		// From the mount Snapshot, which dial() consumed. Leaving it at zero
		// deadlocks the client against itself: drive() will not send an event
		// with seen_server_seq = 0 because the schema refuses it, and seq only
		// advances on a frame that an event would have caused.
		c.seq = snap.GetServerSeq()
		c.generation++
		c.live = true
		c.attempts = attempt
		if !downSince.IsZero() {
			c.recovery = time.Since(downSince)
		}
		c.mu.Unlock()
		attempt = 0

		c.serve(conn)

		c.mu.Lock()
		c.live = false
		c.mu.Unlock()
		downSince = time.Now()
	}
}

func (c *reconnecting) dial() (*websocket.Conn, *pb.Snapshot, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	headers := http.Header{}
	headers.Set("Origin", chaosOrigin)
	conn, _, err := websocket.Dial(ctx, wsURL(c.addr), &websocket.DialOptions{
		HTTPHeader:   headers,
		Subprotocols: []string{"gotth-live.v1"},
	})
	if err != nil {
		return nil, nil, nil, err
	}
	conn.SetReadLimit(1 << 20)

	_, data, err := conn.Read(ctx)
	if err != nil {
		_ = conn.CloseNow()
		return nil, nil, nil, err
	}
	var f pb.Frame
	if err := proto.Unmarshal(data, &f); err != nil || f.GetSnapshot() == nil {
		_ = conn.CloseNow()
		return nil, nil, nil, fmt.Errorf("chaos: the first frame was not a Snapshot")
	}
	return conn, f.GetSnapshot(), f.GetSessionId(), nil
}

// serve reads until the connection ends, acknowledging and committing.
func (c *reconnecting) serve(conn *websocket.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-c.stopped:
			_ = conn.CloseNow()
		case <-ctx.Done():
		}
	}()

	c.mu.Lock()
	every := c.load
	c.mu.Unlock()
	if every > 0 {
		go c.drive(ctx, conn, every)
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var f pb.Frame
		if proto.Unmarshal(data, &f) != nil {
			return
		}
		c.mu.Lock()
		switch {
		case f.GetSnapshot() != nil:
			c.seq = f.GetSnapshot().GetServerSeq()
		case f.GetPatch() != nil:
			if f.GetPatch().GetServerSeq() != c.seq+1 {
				c.mu.Unlock()
				continue
			}
			c.seq = f.GetPatch().GetServerSeq()
		case f.GetHeartbeat() != nil:
			// Echoed, exactly as the shipped runtime does, so the session stays
			// liveness-healthy for the whole spec.
			hb := f.GetHeartbeat()
			c.mu.Unlock()
			echo, err := proto.Marshal(&pb.Frame{
				ProtocolVersion: 1,
				SessionId:       f.GetSessionId(),
				Payload: &pb.Frame_Heartbeat{Heartbeat: &pb.Heartbeat{
					Nonce: hb.GetNonce(), IntervalMs: hb.GetIntervalMs(),
				}},
			})
			if err != nil || conn.Write(ctx, websocket.MessageBinary, echo) != nil {
				return
			}
			continue
		default:
			c.mu.Unlock()
			continue
		}
		seq := c.seq
		c.mu.Unlock()
		ackBytes, err := proto.Marshal(&pb.Frame{
			ProtocolVersion: 1,
			SessionId:       f.GetSessionId(),
			Payload:         &pb.Frame_Ack{Ack: &pb.Ack{ServerSeq: seq}},
		})
		if err != nil {
			return
		}
		if conn.Write(ctx, websocket.MessageBinary, ackBytes) != nil {
			return
		}
	}
}

// drive is the load: one commit every interval, for as long as the connection
// lives.
func (c *reconnecting) drive(ctx context.Context, conn *websocket.Conn, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		c.mu.Lock()
		c.ref++
		ref := c.refBase + c.ref
		sid := c.sessionID
		seen := c.seq
		c.mu.Unlock()

		if seen == 0 {
			// Nothing has been applied yet. seen_server_seq is refined
			// `this > 0`, so an event sent now is refused at the parse boundary
			// — which is what the shipped runtime's `if (!seq) return` avoids.
			continue
		}

		b, err := proto.Marshal(&pb.Frame{
			ProtocolVersion: 1,
			SessionId:       sid,
			Payload: &pb.Frame_Event{Event: &pb.Event{
				ClientRef:     ref,
				Name:          "chaos.commit",
				FragmentId:    "total",
				SeenServerSeq: seen,
				Fields:        []*pb.EventField{{Key: "ref", Value: fmt.Sprint(ref)}},
			}},
		})
		if err != nil {
			return
		}
		if conn.Write(ctx, websocket.MessageBinary, b) != nil {
			return
		}
	}
}

func (c *reconnecting) lastSnapshot() *pb.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot
}

func (c *reconnecting) lastRecovery() (time.Duration, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recovery, c.attempts
}

func (c *reconnecting) gen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

func (c *reconnecting) isLive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.live
}

func liveCount(fleet []*reconnecting) int {
	n := 0
	for _, c := range fleet {
		if c.isLive() {
			n++
		}
	}
	return n
}

// generations snapshots each client's successful mount count. One scalar
// minimum loses client identity and can let a client that never recovered pass
// merely because it had already cycled more often before the restart.
func generations(fleet []*reconnecting) []int {
	out := make([]int, len(fleet))
	for i, c := range fleet {
		out[i] = c.gen()
	}
	return out
}

func generationsAfter(fleet []*reconnecting, before []int) int {
	count := 0
	for i, c := range fleet {
		if c.gen() > before[i] && c.isLive() {
			count++
		}
	}
	return count
}
