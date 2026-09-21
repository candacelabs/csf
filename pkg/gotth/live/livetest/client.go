package livetest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// ClientOptions configures a dial.
//
// Path and Origin are both required and both are the application's, not the
// library's: the handler is mounted wherever the application's router put it,
// and Config.Origins is an allowlist the application wrote. Guessing either one
// would turn a misconfiguration into a hang.
type ClientOptions struct {
	// Path is the request path the live handler is mounted at, as the
	// application's router spells it — "/dashboard/live", not "/".
	Path string

	// Origin is the value of the Origin header the handshake carries. It must
	// be one the application's Config.Origins admits; a rejected origin fails
	// the dial, which is the intended way to spec the refusal.
	Origin string

	// Header carries anything else the handshake needs — a session cookie for
	// an application that authenticates from one, most often. A browser cannot
	// set headers on a WebSocket handshake, so anything here that is not a
	// cookie is testing a path a browser does not have.
	Header http.Header

	// Timeout bounds the whole session, not one read. It defaults to 60s and
	// exists because coder/websocket closes a connection whose read context is
	// cancelled — a per-read deadline would make a slow spec look like a
	// server that hung up.
	Timeout time.Duration
}

// Client drives one session over the real protocol against an http.Handler: a
// real dial, a real upgrade, real frames in both directions.
//
// It is a browser as far as the server can tell, with one deliberate
// difference: it never acknowledges a patch on its own. "This client stopped
// acknowledging" is the condition the backpressure specs are built on, and a
// helpful auto-ack here would make the whole ladder unreachable — so Ack is
// explicit, always, including in WaitFor.
//
// Every retrieval method takes an explicit timeout rather than reading the
// spec's deadline, because "no frame arrived within 5s" and "the suite's 60s
// budget expired" are different failures and only the first one names what was
// being waited for.
//
// A Client's frames are decoded with the library's own generated types and
// handed back as the plain values below, so a spec asserts on the wire without
// the protocol's generated types reaching its import graph — the property
// api-surface.md §6 records, held here rather than by not decoding at all.
type Client struct {
	tb   testing.TB
	name string

	server *httptest.Server
	conn   *websocket.Conn

	ctx    context.Context
	cancel context.CancelFunc

	// snapshot is the mount snapshot, read before NewClient returns: a session
	// that has not received it is not yet a session.
	snapshot *Frame

	sessionID []byte

	incoming chan *Frame
	readErr  chan error
	done     chan struct{}

	closeOnce sync.Once
	closeErr  error

	mu     sync.Mutex
	frames []*Frame
	ref    uint64
	seq    uint64
}

// NewClient dials h and returns once the mount snapshot has arrived.
//
// It serves h from an httptest.Server rather than taking a URL, so a spec never
// has a server it forgot to close: the server, the connection and the read
// goroutine are all released through tb.Cleanup, in that order, whether the
// spec passed or failed.
//
// Returning only after the snapshot is not convenience. H-10 makes the first
// frame on a connection the snapshot, and a Client handed back before it
// arrived would have no session identifier, so its first Send would be a frame
// the server refuses for a reason that has nothing to do with the spec.
func NewClient(tb testing.TB, h http.Handler, opts ClientOptions) *Client {
	tb.Helper()

	switch {
	case h == nil:
		tb.Fatalf("livetest.NewClient: the handler is nil. Pass the http.Handler your " +
			"application serves — live.App.Handler(), or the router it is mounted in.")
		return nil
	case opts.Path == "":
		tb.Fatalf("livetest.NewClient: ClientOptions.Path is empty. It is the path your " +
			"router mounted the live handler at; there is no default because the library " +
			"does not choose it.")
		return nil
	case opts.Origin == "":
		tb.Fatalf("livetest.NewClient: ClientOptions.Origin is empty. The handshake checks " +
			"Origin against your Config.Origins allowlist, and an absent header is not the " +
			"same test as a rejected one.")
		return nil
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	server := httptest.NewServer(h)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	header := http.Header{}
	for k, vs := range opts.Header {
		for _, v := range vs {
			header.Add(k, v)
		}
	}
	header.Set("Origin", opts.Origin)

	conn, _, err := websocket.Dial(ctx,
		"ws"+strings.TrimPrefix(server.URL, "http")+opts.Path,
		&websocket.DialOptions{
			HTTPHeader:   header,
			Subprotocols: []string{protocol.Subprotocol},
		})
	if err != nil {
		cancel()
		server.Close()
		tb.Fatalf("livetest.NewClient: dialling %s%s: %v.\n"+
			"The upgrade was refused before a session existed, so check ClientOptions.Path against "+
			"the path your router mounted the handler at, and ClientOptions.Origin against "+
			"Config.Origins.", server.URL, opts.Path, err)
		return nil
	}

	c := &Client{
		tb:       tb,
		name:     tb.Name(),
		server:   server,
		conn:     conn,
		ctx:      ctx,
		cancel:   cancel,
		incoming: make(chan *Frame, 4096),
		readErr:  make(chan error, 1),
		done:     make(chan struct{}),
	}
	go c.pump()

	// The teardown a spec that never calls Close still gets. It is the same
	// release, reached without the close handshake — and it is guarded by the
	// same sync.Once, so a spec that did call Close does not pay for it twice.
	tb.Cleanup(func() { c.closeOnce.Do(c.release) })

	// NextErr rather than next: this is the one call made before a session
	// exists, so it is what makes where's unestablished arm reachable, and the
	// message below is written to compose with the prefix it already carries
	// rather than to repeat it.
	first, err := c.NextErr(30 * time.Second)
	if err != nil {
		tb.Fatalf("%v\n"+
			"That is livetest.NewClient waiting for the mount snapshot. The upgrade succeeded and "+
			"no first frame followed, so the mount transition is the suspect: Config.Init "+
			"returning an error, or a fragment whose first render fails, both of which the "+
			"server logs at Error.", err)
		return nil
	}
	if first.Kind != FrameSnapshot {
		tb.Fatalf("livetest.NewClient: the first frame on a connection is the Snapshot (H-10); "+
			"this one was %s", first)
		return nil
	}
	c.snapshot = first
	c.sessionID = first.SessionID
	return c
}

// pump reads for the life of the connection.
//
// It is the only caller of conn.Read, which is what stops a spec from killing
// its own session by timing out: coder/websocket closes the connection when a
// read's context is cancelled, because a half-consumed message cannot be
// abandoned safely, so a per-call read deadline would turn "this assertion
// waited too long" into "the transport failed".
func (c *Client) pump() {
	defer close(c.done)
	defer close(c.incoming)

	for {
		typ, data, err := c.conn.Read(c.ctx)
		if err != nil {
			c.failRead(err)
			return
		}
		if typ != websocket.MessageBinary {
			c.failRead(fmt.Errorf("a %v message arrived and every payload on this protocol is binary: "+
				"nothing in this library sends one, so look for a proxy or a middleware in the "+
				"handler under test that is writing to the socket", typ))
			return
		}
		f, err := decodeFrame(data)
		if err != nil {
			c.failRead(err)
			return
		}

		c.mu.Lock()
		c.frames = append(c.frames, f)
		c.mu.Unlock()
		c.incoming <- f
	}
}

func (c *Client) failRead(err error) {
	select {
	case c.readErr <- err:
	default:
	}
}

// SessionID is the identifier the server bound to this session at the
// handshake, as it appears in every frame in both directions.
func (c *Client) SessionID() []byte { return slices.Clone(c.sessionID) }

// where names this client and the session the server bound to it.
//
// It is the subject of every failure this file reports AND of every error
// NextErr returns, because that is the pair FR-58 asks a library-produced
// diagnostic to carry and a returned error is the one that leaves this package.
// The name is what a spec driving three clients uses to tell them apart; the
// session identifier is what joins a failing spec to the server's own log
// records for the same run, which are keyed on nothing else. Before this
// existed a spec with two clients failed with "livetest: b: no frame arrived
// within 2s" and the operator reading the server's records beside it had no way
// to say which of the two sessions in them was b.
//
// The unestablished arm is the one window in which no session exists: NewClient
// returns only after the mount snapshot, so it is reached from the snapshot
// wait, which is the one NextErr call made before a session exists.
func (c *Client) where() string {
	if len(c.sessionID) == 0 {
		return c.name + " (no session yet: the mount snapshot has not arrived)"
	}
	return fmt.Sprintf("%s (session %x)", c.name, c.sessionID)
}

// Snapshot is the mount snapshot this session opened with.
func (c *Client) Snapshot() *Frame { return c.snapshot }

// Seq is the highest server sequence this client has seen, which is what an
// event frame reports as its seen_server_seq and what a resync request would
// claim to hold.
func (c *Client) Seq() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seq
}

// NextErr returns the next frame that is not a heartbeat, or an error naming
// the session it was waiting on and what happened instead.
//
// Heartbeats are skipped because no assertion in any consumer of this package
// is about a heartbeat's position in the stream, and a suite that has to filter
// them by hand writes the filter in every await.
//
// The error carries where's prefix — "livetest: <name> (session <hex>): …" —
// and it is the same string Next fails the spec with. That is deliberate and it
// is the FR-58 clause: this is the path that hands the value to a caller, so it
// is the one where this package is not the last reader, and an error a spec
// stores, wraps or logs beside the server's own records has to say which
// session it is about. It did not until 2026-08-05: where was applied on the
// tb.Fatalf paths and nowhere else, so the five docs/error-audit.md §3.4 rows
// that graded these messages as carrying a session were true of Next and Await
// and false here. QA-1 caught it by driving it (phase-4-grading.md F-1).
func (c *Client) NextErr(timeout time.Duration) (*Frame, error) {
	f, err := c.next(timeout)
	if err != nil {
		return nil, fmt.Errorf("livetest: %s: %w", c.where(), err)
	}
	return f, nil
}

// next is NextErr's body without the prefix, so that the wrap happens exactly
// once no matter which arm produced the error — including an error this package
// did not author, which is the transport's read failure and is the arm most in
// need of a session identifier.
func (c *Client) next(timeout time.Duration) (*Frame, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case f, ok := <-c.incoming:
			if !ok {
				select {
				case err := <-c.readErr:
					return nil, err
				default:
					return nil, errors.New("the connection closed with no error of its own: " +
						"the server closed it, so read the close code in the server's records — " +
						"a session evicted or refused looks exactly like this from here")
				}
			}
			if f.Kind == FrameHeartbeat {
				continue
			}
			if f.Patch != nil {
				c.mu.Lock()
				if f.Patch.ServerSeq > c.seq {
					c.seq = f.Patch.ServerSeq
				}
				c.mu.Unlock()
			}
			return f, nil
		case <-timer.C:
			return nil, fmt.Errorf("no frame arrived within %s: the session is open and quiet, so "+
				"either the transition you expected produced no patch — an identical render is "+
				"suppressed — or the outbound window is full and nothing was acknowledged", timeout)
		}
	}
}

// Next is NextErr, failing the spec rather than returning the error.
func (c *Client) Next(timeout time.Duration) *Frame {
	c.tb.Helper()
	f, err := c.NextErr(timeout)
	if err != nil {
		// No prefix here: NextErr's value already carries it, and printing
		// where twice is how the two paths drift apart again.
		c.tb.Fatalf("%v", err)
		return nil
	}
	return f
}

// Await takes frames until one satisfies pred, and fails naming what it did
// see instead.
//
// The what argument is the failure message's subject — "a meters patch", "the
// resync snapshot" — and it is required because the alternative message is
// "the predicate never matched", which tells a reader nothing they can act on.
func (c *Client) Await(what string, timeout time.Duration, pred func(frame *Frame) bool) *Frame {
	c.tb.Helper()

	deadline := time.Now().Add(timeout)
	var seen []string
	for time.Now().Before(deadline) {
		f, err := c.NextErr(time.Until(deadline))
		if err != nil {
			break
		}
		if pred(f) {
			return f
		}
		seen = append(seen, f.String())
	}
	c.tb.Fatalf("livetest: %s never saw %s within %s; it saw %v.\n"+
		"If that list is empty the session sent nothing at all; if it is not, the predicate is\n"+
		"narrower than the frames this session actually produces.",
		c.where(), what, timeout, seen)
	return nil
}

// WaitFor blocks until a patch or snapshot makes one fragment's markup satisfy
// pred, and returns the frame that did it.
//
// It does not acknowledge what it consumed. A spec that wants to behave like a
// browser calls Ack on the returned frame's sequence; one that is building
// backpressure does not, and that difference is the point of leaving it out.
func (c *Client) WaitFor(fragmentID string, pred func(html string) bool) *Frame {
	c.tb.Helper()
	return c.Await(fmt.Sprintf("a patch making %q satisfy the predicate", fragmentID),
		10*time.Second, func(f *Frame) bool {
			if f.Patch == nil {
				return false
			}
			html, ok := f.Patch.Fragment(fragmentID)
			return ok && pred(html)
		})
}

// Settle drains whatever is in flight and returns once nothing has arrived for
// the idle period.
func (c *Client) Settle(idle time.Duration) []*Frame {
	var out []*Frame
	for {
		f, err := c.NextErr(idle)
		if err != nil {
			return out
		}
		out = append(out, f)
	}
}

// Received is every frame this client has decoded, heartbeats included, in
// arrival order. Unlike the retrieval methods it consumes nothing.
func (c *Client) Received() []*Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.frames)
}

// Closed reports whether the server hung up within the timeout. It is how a
// spec asserts that a fatal error closed the connection rather than merely
// arriving on it.
func (c *Client) Closed(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := c.NextErr(time.Until(deadline)); err != nil {
			return true
		}
	}
	return false
}

// Send writes one event frame and returns the client reference it used.
//
// The reference is the handle a provenance assertion resolves to a server-side
// event identifier, which is why it is returned rather than kept: correlating
// a log line to the interaction that caused it is the assertion, and a client
// that hides the correlator cannot make it.
//
// Fields are sent in sorted key order, so a spec that asserts on the encoded
// frame is asserting on a stable one.
func (c *Client) Send(name, fragmentID string, fields map[string]string) uint64 {
	c.tb.Helper()

	c.mu.Lock()
	c.ref++
	ref := c.ref
	seen := c.seq
	c.mu.Unlock()

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	ev := &pb.Event{ClientRef: ref, Name: name, FragmentId: fragmentID, SeenServerSeq: seen}
	for _, k := range keys {
		ev.Fields = append(ev.Fields, &pb.EventField{Key: k, Value: fields[k]})
	}

	c.write("event", &pb.Frame{
		ProtocolVersion: 1,
		SessionId:       c.sessionID,
		Payload:         &pb.Frame_Event{Event: ev},
	})
	return ref
}

// Ack acknowledges a patch, which is the whole of the window protocol's client
// half. Withholding it is how a spec makes a client slow without throttling a
// socket.
func (c *Client) Ack(serverSeq uint64) {
	c.tb.Helper()
	c.write("ack", &pb.Frame{
		ProtocolVersion: 1,
		SessionId:       c.sessionID,
		Payload:         &pb.Frame_Ack{Ack: &pb.Ack{ServerSeq: serverSeq}},
	})
}

// Resync asks for a snapshot, claiming to hold everything through lastApplied.
//
// A lastApplied already equal to the server's sequence is answered with an Ack
// rather than a Snapshot — the no-op short circuit — so a spec measuring what a
// resync costs passes a value behind the server's on purpose.
func (c *Client) Resync(lastApplied uint64, reason int32) {
	c.tb.Helper()
	c.write("resync request", &pb.Frame{
		ProtocolVersion: 1,
		SessionId:       c.sessionID,
		Payload: &pb.Frame_ResyncRequest{ResyncRequest: &pb.ResyncRequest{
			LastAppliedSeq: lastApplied,
			Reason:         pb.ResyncReason(reason),
		}},
	})
}

// WriteRaw sends bytes the client did not build, for the specs that are about
// what the server does with a frame no correct client would send.
//
// It returns the error rather than failing, because "the write was refused" is
// frequently the assertion.
func (c *Client) WriteRaw(payload []byte) error {
	return c.conn.Write(c.ctx, websocket.MessageBinary, payload)
}

func (c *Client) write(what string, f *pb.Frame) {
	c.tb.Helper()
	b, err := proto.Marshal(f)
	if err != nil {
		c.tb.Fatalf("livetest: %s: encoding an %s frame: %v: this frame was built by livetest, "+
			"so it is a library bug rather than anything the spec did", c.where(), what, err)
		return
	}
	if err := c.conn.Write(c.ctx, websocket.MessageBinary, b); err != nil {
		c.tb.Fatalf("livetest: %s: writing an %s frame: %v: the connection is gone — the server "+
			"closed it, and its close code says why", c.where(), what, err)
	}
}

// Close ends the session the way a browser closing a tab does and releases
// everything this Client owns: the connection, the read goroutine, and the
// server it dialled.
//
// Calling it is optional — NewClient registered the same release with
// tb.Cleanup — so it is **idempotent**, returning the first close's result to
// every later caller. Without that, every spec which closed explicitly would
// also fail in cleanup, which would make the method unusable for the one thing
// it exists for.
//
// Releasing the server here rather than only at cleanup is what makes a
// connect/disconnect loop a real one. A spec that opens twenty tabs to prove
// the library leaks neither a subscription nor a goroutine would otherwise be
// measuring twenty servers this package had not got round to closing, and
// would fail on the harness rather than on the library.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.conn.Close(websocket.StatusNormalClosure, "")
		c.release()
	})
	return c.closeErr
}

// release drops the connection, joins the pump and closes the server, in that
// order — the order is the point. CloseNow unblocks a pump parked in
// conn.Read, <-done joins it, and only then is it safe to drop the server the
// handler is still running under.
func (c *Client) release() {
	_ = c.conn.CloseNow()
	c.cancel()
	<-c.done
	c.server.Close()
}
