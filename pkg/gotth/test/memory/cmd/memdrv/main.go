// Command memdrv is equivalence-spec §3.6's synthetic session driver for
// gotth-live: it opens N real sessions against a memsrv, holds them IDLE, and
// keeps them alive for as long as the harness needs them.
//
// § 3.6 defines the driver as one that "speaks each stack's actual protocol —
// gotth-live: liquid proto over the ADR-001 transport, real handshake, real
// events — and consumes and discards pushed payloads at the rate a browser
// would (no artificial backpressure)". Three things follow and each is done
// here rather than approximated:
//
//   - The handshake is the real one: the gotth-live.v1 subprotocol is offered,
//     an Origin the server's allowlist contains is sent, and a session that is
//     refused is counted as refused rather than retried into existence.
//   - Every Snapshot and Patch is ACKNOWLEDGED as soon as it is read. Not
//     acknowledging would leave patches occupying the server's unacked window,
//     which is per-session memory the server would be holding because of the
//     driver — the artificial backpressure §3.6 forbids, in the direction that
//     inflates the number being measured.
//   - Every server Heartbeat is echoed. Liveness in this protocol is the
//     Heartbeat FRAME, not an RFC 6455 ping (protocol.md §3.4), so a driver
//     that only read would be evicted at HeartbeatTimeout and the run would
//     measure a server whose sessions were dying.
//
// Idle means idle: after the mount snapshot, this sends nothing except those
// acknowledgements and heartbeat echoes. That is the Idle workload of §3.4.
//
// Usage:
//
//	memdrv -url ws://127.0.0.1:18080/live -origin http://127.0.0.1:18080 \
//	       -n 1000 -status 127.0.0.1:19080
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// counters is what /status publishes. Every field is a fact the manifest needs:
// a run in which sessions died is not a run at N, and a run that never reached
// N is not one either.
type counters struct {
	Target     int64 `json:"target"`
	Dialed     int64 `json:"dialed"`
	Mounted    int64 `json:"mounted"`
	Live       int64 `json:"live"`
	Closed     int64 `json:"closed"`
	DialErrors int64 `json:"dial_errors"`
	ReadErrors int64 `json:"read_errors"`
	Acks       int64 `json:"acks"`
	Heartbeats int64 `json:"heartbeats"`
	Patches    int64 `json:"patches"`
	Snapshots  int64 `json:"snapshots"`
	Errors     int64 `json:"error_frames"`
}

type driver struct {
	target                                      int64
	dialed, mounted, live, closed               atomic.Int64
	dialErrors, readErrors                      atomic.Int64
	acks, heartbeats, patches, snaps, errFrames atomic.Int64
	url, origin                                 string
	// echo is the Idle workload's acknowledge-and-echo behaviour. False is the
	// diagnostic read-only mode described on the -echo flag.
	echo bool
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "memdrv:", err)
		os.Exit(1)
	}
}

func run() error {
	url := flag.String("url", "ws://127.0.0.1:8080/live", "the live endpoint")
	origin := flag.String("origin", "http://127.0.0.1:8080", "the Origin header to send")
	n := flag.Int("n", 1000, "number of idle sessions to establish and hold")
	batch := flag.Int("dial-batch", 50, "how many sessions to dial concurrently")
	pause := flag.Duration("dial-pause", 25*time.Millisecond, "pause between dial batches")
	status := flag.String("status", "127.0.0.1:9090", "address for the /status endpoint")
	echo := flag.Bool("echo", true,
		"acknowledge snapshots and patches and echo heartbeats, which is the Idle "+
			"workload of §3.4 and what every measured run uses. -echo=false is a "+
			"DIAGNOSTIC: it reads and discards but sends nothing, so the server's "+
			"read pump never runs its instrumented parse path. It is how the "+
			"goroutine-stack line of g2-baseline.md §5.1 is attributed to one of "+
			"the two per-session goroutines. Sessions die at HeartbeatTimeout, so "+
			"it is only valid for a window shorter than that and is never a "+
			"measured cell")
	flag.Parse()

	d := &driver{target: int64(*n), url: *url, origin: *origin, echo: *echo}

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(d.snapshot())
	})
	statusSrv := &http.Server{Addr: *status, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := statusSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "memdrv: status server:", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	for i := 0; i < *n; i += *batch {
		if ctx.Err() != nil {
			break
		}
		for j := i; j < i+*batch && j < *n; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				d.session(ctx)
			}()
		}
		select {
		case <-ctx.Done():
		case <-time.After(*pause):
		}
	}

	fmt.Printf("memdrv: dialing %d sessions at %s\n", *n, *url)
	<-ctx.Done()

	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = statusSrv.Shutdown(shutdown)
	wg.Wait()
	return nil
}

func (d *driver) snapshot() counters {
	return counters{
		Target:     d.target,
		Dialed:     d.dialed.Load(),
		Mounted:    d.mounted.Load(),
		Live:       d.live.Load(),
		Closed:     d.closed.Load(),
		DialErrors: d.dialErrors.Load(),
		ReadErrors: d.readErrors.Load(),
		Acks:       d.acks.Load(),
		Heartbeats: d.heartbeats.Load(),
		Patches:    d.patches.Load(),
		Snapshots:  d.snaps.Load(),
		Errors:     d.errFrames.Load(),
	}
}

// session holds exactly one idle session for the life of ctx.
//
// It does not reconnect. A session that drops is a fact the run manifest needs
// — /status reports live below target and the harness aborts the cell — and a
// driver that quietly re-dialled would turn "the server evicted 40 sessions"
// into "the number was a bit noisy".
func (d *driver) session(ctx context.Context) {
	header := http.Header{}
	header.Set("Origin", d.origin)

	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	conn, _, err := websocket.Dial(dialCtx, d.url, &websocket.DialOptions{
		HTTPHeader:   header,
		Subprotocols: []string{protocol.Subprotocol},
	})
	cancel()
	if err != nil {
		d.dialErrors.Add(1)
		return
	}
	d.dialed.Add(1)
	d.live.Add(1)
	defer func() {
		d.live.Add(-1)
		d.closed.Add(1)
		conn.CloseNow() //nolint:errcheck // the run is over; the socket is going away either way
	}()

	// A browser's runtime reads whatever the server sends. The library's own
	// inbound cap is MaxInboundFrameBytes (65536 by default); this is the
	// client side of the same order, generous enough that a snapshot of a real
	// page is never the thing that ends a session.
	conn.SetReadLimit(1 << 20)

	mounted := false
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				d.readErrors.Add(1)
			}
			return
		}
		if typ != websocket.MessageBinary {
			d.readErrors.Add(1)
			return
		}

		var f pb.Frame
		if err := proto.Unmarshal(data, &f); err != nil {
			d.readErrors.Add(1)
			return
		}
		id, ok := sessionID(&f)
		if !ok {
			d.readErrors.Add(1)
			return
		}

		switch {
		case f.GetSnapshot() != nil:
			d.snaps.Add(1)
			if !mounted {
				mounted = true
				d.mounted.Add(1)
			}
			if !d.echo {
				continue
			}
			if !d.send(ctx, conn, protocol.NewAck(id, f.GetSnapshot().GetServerSeq())) {
				return
			}
			d.acks.Add(1)
		case f.GetPatch() != nil:
			d.patches.Add(1)
			if !d.echo {
				continue
			}
			if !d.send(ctx, conn, protocol.NewAck(id, f.GetPatch().GetServerSeq())) {
				return
			}
			d.acks.Add(1)
		case f.GetHeartbeat() != nil:
			hb := f.GetHeartbeat()
			if !d.echo {
				continue
			}
			// The client echoes interval_ms verbatim (protocol.md §3.4): the
			// predicate forbids zero, and the echo doubles as an
			// acknowledgement that the client honoured the interval.
			if !d.send(ctx, conn, protocol.NewHeartbeat(id, hb.GetNonce(), hb.GetIntervalMs())) {
				return
			}
			d.heartbeats.Add(1)
		case f.GetError() != nil:
			d.errFrames.Add(1)
			if f.GetError().GetFatal() {
				return
			}
		}
	}
}

func (d *driver) send(ctx context.Context, conn *websocket.Conn, frame *pb.Frame) bool {
	b, err := proto.Marshal(frame)
	if err != nil {
		d.readErrors.Add(1)
		return false
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := conn.Write(writeCtx, websocket.MessageBinary, b); err != nil {
		if ctx.Err() == nil {
			d.readErrors.Add(1)
		}
		return false
	}
	return true
}

func sessionID(f *pb.Frame) ([16]byte, bool) {
	raw := f.GetSessionId()
	if len(raw) != 16 {
		return [16]byte{}, false
	}
	return [16]byte(raw), true
}
