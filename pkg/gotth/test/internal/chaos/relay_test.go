package chaos_test

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// relay is a byte-level TCP proxy that sits between a client and the server and
// can be made to misbehave.
//
// It is byte-level rather than WebSocket-message-level on purpose. Two of the
// eight cases are about what TCP does rather than about what a frame says: a
// slow client is slow because it does not READ, which is a socket property that
// a message-level relay would absorb into its own buffers and hide; and a
// half-open connection is one where the peer is gone and the kernel has not
// noticed, which cannot be expressed at all above the socket.
//
// Three faults, each the smallest thing that produces the failure mode:
//
//   - cut: close both sockets immediately, with no WebSocket close frame. This
//     is a dropped connection, not a graceful one — the distinction matters
//     because the graceful path is the one the library is best at.
//   - throttle: rate-limit the server→client direction to a stated number of
//     bytes per second, which is PRD case 4's "throttled to a stated bandwidth".
//   - blackhole: keep both sockets open and keep READING from both, forwarding
//     nothing. This is the half-open connection: every write either side makes
//     succeeds, nothing arrives, and no error is ever reported. It is the case
//     a heartbeat exists for and the only one a TCP-level error cannot cover.
type relay struct {
	ln     net.Listener
	target string

	// bytesPerSecond throttles server→client. Zero is unthrottled.
	bytesPerSecond atomic.Int64
	// blackholed stops forwarding in both directions while still draining both
	// sockets, so neither peer sees an error.
	blackholed atomic.Bool

	mu    sync.Mutex
	conns []net.Conn
	// toClient counts bytes actually delivered downstream, which is what a
	// throttled measurement is stated against.
	toClient atomic.Int64
	toServer atomic.Int64
}

// newRelay starts a relay in front of target and returns it.
func newRelay(target string) *relay {
	GinkgoHelper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())

	r := &relay{ln: ln, target: target}
	go r.accept()
	DeferCleanup(r.stop)
	return r
}

func (r *relay) addr() string { return r.ln.Addr().String() }

func (r *relay) accept() {
	for {
		down, err := r.ln.Accept()
		if err != nil {
			return
		}
		up, err := net.Dial("tcp", r.target)
		if err != nil {
			_ = down.Close()
			continue
		}
		r.mu.Lock()
		r.conns = append(r.conns, down, up)
		r.mu.Unlock()

		go r.copy(down, up, &r.toClient, true)  // server -> client
		go r.copy(up, down, &r.toServer, false) // client -> server
	}
}

// copy moves bytes from src to dst, honouring the injected faults.
func (r *relay) copy(dst, src net.Conn, counter *atomic.Int64, throttled bool) {
	defer func() {
		_ = dst.Close()
		_ = src.Close()
	}()

	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			// The blackhole drains rather than stalls. A relay that simply
			// stopped reading would apply TCP backpressure to the writer, which
			// the server would eventually see as a stalled write — a DIFFERENT
			// failure from the one case 6 is about, and one the write deadline
			// already covers. Reading and discarding is what makes the peer
			// look alive to the kernel and dead to the protocol.
			if !r.blackholed.Load() {
				if throttled {
					if rate := r.bytesPerSecond.Load(); rate > 0 {
						// A token bucket would let a burst through; this is a
						// flat pacing of each chunk, which is the conservative
						// reading of "throttled to X bytes per second" and the
						// one a spec can state a bound against.
						//
						// CS-9 keep: this sleep is the slow link. It is the
						// subject of case 4, not a wait for one.
						time.Sleep(time.Duration(float64(n) / float64(rate) * float64(time.Second)))
					}
				}
				if _, werr := dst.Write(buf[:n]); werr != nil {
					return
				}
				counter.Add(int64(n))
			}
		}
		if err != nil {
			if err != io.EOF {
				return
			}
			return
		}
	}
}

// cut aborts every connection through the relay, with no close handshake. This
// is what a dropped connection looks like to both ends.
func (r *relay) cut() {
	r.mu.Lock()
	conns := r.conns
	r.conns = nil
	r.mu.Unlock()
	for _, c := range conns {
		// SetLinger(0) makes the close an RST rather than a FIN where the
		// platform allows it, so the peer sees a reset connection and not an
		// orderly shutdown. That is the difference between "the network died"
		// and "the peer said goodbye", and the library's behaviour is allowed
		// to differ between them.
		if tcp, ok := c.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = c.Close()
	}
}

func (r *relay) stop() {
	_ = r.ln.Close()
	r.cut()
}

// throttleTo sets the server→client bandwidth in bytes per second.
func (r *relay) throttleTo(bytesPerSecond int64) { r.bytesPerSecond.Store(bytesPerSecond) }

// partition starts the half-open condition.
func (r *relay) partition() { r.blackholed.Store(true) }

// heal ends it.
func (r *relay) heal() { r.blackholed.Store(false) }

// deliveredToClient is how many bytes actually reached the client.
func (r *relay) deliveredToClient() int64 { return r.toClient.Load() }
