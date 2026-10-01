package conformance_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// allowedOrigin is the one origin the harness's applications accept. Every
// other value, including the empty one, is a rejection this suite asserts.
const allowedOrigin = "https://qa.example"

// ---------------------------------------------------------------------------
// The application under test
// ---------------------------------------------------------------------------

// tally is a deliberately minimal state: two fields, comparable, so that the
// actor's own no-change detection is exercised rather than bypassed.
type tally struct {
	N     int
	Label string
}

type qaUser string

func (u qaUser) Subject() string { return string(u) }

func text(format string, args ...any) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := fmt.Fprintf(w, format, args...)
		return err
	})
}

// qaConfig is the application every spec in this suite drives unless it says
// otherwise. Two fragments rather than one, because a single-fragment
// application cannot exhibit the partial-update behaviour the provenance
// properties are about.
func qaConfig() live.Config[tally, qaUser] {
	return live.Config[tally, qaUser]{
		Init: func(ctx context.Context, session live.Session[qaUser]) (tally, []live.Effect[qaUser], error) {
			return tally{Label: "hits"}, nil, nil
		},
		Reduce: func(state tally, ev live.Event) (tally, []live.Effect[qaUser]) {
			switch ev.Name {
			case "qa.increment":
				state.N++
			case "qa.relabel":
				state.Label = ev.Fields.Get("label")
			case "qa.noop":
				// Deliberately changes nothing: the state version must not move.
			}
			return state, nil
		},
		Fragments: []live.Fragment[tally]{
			{
				ID:     "count",
				Render: func(s tally) templ.Component { return text("<b>%d</b>", s.N) },
				Dirty:  func(prev, next tally) bool { return prev.N != next.N },
			},
			{
				ID:     "label",
				Render: func(s tally) templ.Component { return text("<i>%s</i>", s.Label) },
				Dirty:  func(prev, next tally) bool { return prev.Label != next.Label },
			},
		},
		Events:       []string{"qa.increment", "qa.relabel", "qa.noop"},
		Origins:      []string{allowedOrigin},
		Authenticate: func(request *http.Request) (qaUser, error) { return qaUser("qa"), nil },
		Authorize:    live.AllowAll[qaUser],
		CSRF:         live.NoCSRFCheck,
	}
}

// ---------------------------------------------------------------------------
// The provenance log capture
// ---------------------------------------------------------------------------

// provRecord is one row of the server-side provenance index, decoded back out
// of the structured log. Joining the wire capture against this is the whole
// mechanism behind protocol.md §7: the frames are written by the framer and
// the records by the actor in step, so agreement between them is evidence and
// not tautology.
type provRecord struct {
	SessionID    string
	EventID      uint64
	ClientRef    uint64
	TransitionID uint64
	StateVersion uint64
	PatchID      uint64
	ServerSeq    uint64
	OriginKind   string
	OriginSource string
	FragmentIDs  []string
	Contributing []uint64
	FromSeq      uint64
	ThroughSeq   uint64
}

// logSink captures every record the library emits, keeping the attributes
// attached by With so provenance rows can be told apart from ordinary ones.
type logSink struct {
	mu      *sync.Mutex
	records *[]capturedRecord
	attrs   []slog.Attr
}

type capturedRecord struct {
	level  slog.Level
	msg    string
	fields map[string]any
}

func newLogSink() *logSink {
	return &logSink{mu: &sync.Mutex{}, records: &[]capturedRecord{}}
}

func (s *logSink) Enabled(ctx context.Context, level slog.Level) bool { return true }

func (s *logSink) Handle(_ context.Context, r slog.Record) error {
	fields := map[string]any{}
	for _, a := range s.attrs {
		fields[a.Key] = a.Value.Any()
	}
	r.Attrs(func(a slog.Attr) bool {
		fields[a.Key] = a.Value.Any()
		return true
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	*s.records = append(*s.records, capturedRecord{level: r.Level, msg: r.Message, fields: fields})
	return nil
}

func (s *logSink) WithAttrs(a []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(s.attrs)+len(a))
	merged = append(merged, s.attrs...)
	merged = append(merged, a...)
	return &logSink{mu: s.mu, records: s.records, attrs: merged}
}

func (s *logSink) WithGroup(name string) slog.Handler { return s }

// provenance returns every provenance row captured so far, in emission order.
func (s *logSink) provenance() []provRecord {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []provRecord
	for _, r := range *s.records {
		if r.fields["logger"] != obs.ProvenanceLogger {
			continue
		}
		out = append(out, provRecord{
			SessionID:    str(r.fields["session_id"]),
			EventID:      u64(r.fields["event_id"]),
			ClientRef:    u64(r.fields["client_ref"]),
			TransitionID: u64(r.fields["transition_id"]),
			StateVersion: u64(r.fields["state_version"]),
			PatchID:      u64(r.fields["patch_id"]),
			ServerSeq:    u64(r.fields["server_seq"]),
			OriginKind:   str(r.fields["origin_kind"]),
			OriginSource: str(r.fields["origin_source"]),
			FragmentIDs:  strs(r.fields["fragment_ids"]),
			Contributing: u64s(r.fields["contributing_event_ids"]),
			FromSeq:      u64(r.fields["superseded_from_seq"]),
			ThroughSeq:   u64(r.fields["superseded_through_seq"]),
		})
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func u64(v any) uint64 {
	switch n := v.(type) {
	case uint64:
		return n
	case int64:
		return uint64(n)
	case int:
		return uint64(n)
	}
	return 0
}

func strs(v any) []string {
	s, _ := v.([]string)
	return s
}

func u64s(v any) []uint64 {
	s, _ := v.([]uint64)
	return s
}

// ---------------------------------------------------------------------------
// The driven session
// ---------------------------------------------------------------------------

// driven is one live application behind a real HTTP server, reached over a
// real dialled WebSocket, with every received frame retained as bytes.
//
// Frames are captured as bytes and decoded per spec rather than kept only as
// messages, because two of the properties below are about the bytes: that they
// re-encode identically, and that nothing arrived that is not a Frame.
//
// Reading happens on a background goroutine feeding a channel, and not
// on demand with a per-read deadline. That is forced by the transport rather
// than chosen: coder/websocket closes the connection when the context passed
// to Read is cancelled, because a half-consumed message cannot be abandoned
// safely. A spec that "waits 300ms for more frames" by cancelling a read would
// therefore be killing the session it is measuring — which is exactly the
// false failure this harness was rewritten to remove.
type driven struct {
	app    *live.App[tally, qaUser]
	server *httptest.Server
	conn   *websocket.Conn
	ctx    context.Context
	logs   *logSink

	sessionID []byte
	snapshot  *pb.Snapshot
	ref       uint64

	incoming chan *pb.Frame
	readErrC chan error

	mu     sync.Mutex
	raw    [][]byte
	frames []*pb.Frame
}

// dial mounts qaConfig, optionally mutated, and connects to it.
func dial(mutate func(cfg *live.Config[tally, qaUser])) *driven {
	GinkgoHelper()

	cfg := qaConfig()
	sink := newLogSink()
	cfg.Logger = slog.New(sink)
	if mutate != nil {
		mutate(&cfg)
	}

	app, err := live.New(cfg)
	Expect(err).NotTo(HaveOccurred())

	ts := httptest.NewServer(app.Handler())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	DeferCleanup(cancel)

	headers := http.Header{}
	headers.Set("Origin", allowedOrigin)
	conn, _, err := websocket.Dial(ctx, wsURL(ts.URL), &websocket.DialOptions{
		HTTPHeader:   headers,
		Subprotocols: []string{"gotth-live.v1"},
	})
	Expect(err).NotTo(HaveOccurred())

	d := &driven{
		app: app, server: ts, conn: conn, ctx: ctx, logs: sink,
		incoming: make(chan *pb.Frame, 4096),
		readErrC: make(chan error, 1),
	}
	go d.pump()
	DeferCleanup(d.stop)

	first := d.read()
	Expect(first.GetSnapshot()).NotTo(BeNil(), "the first frame on a connection is the Snapshot (H-10)")
	d.snapshot = first.GetSnapshot()
	d.sessionID = first.GetSessionId()
	return d
}

func wsURL(httpURL string) string { return "ws" + strings.TrimPrefix(httpURL, "http") }

func (d *driven) stop() {
	if d.conn != nil {
		_ = d.conn.CloseNow()
	}
	if d.app != nil {
		_ = d.app.Close(context.Background())
	}
	if d.server != nil {
		d.server.Close()
	}
}

// pump reads for the life of the connection, capturing every message. It is
// the only caller of conn.Read, so no spec can close the socket by timing out.
func (d *driven) pump() {
	defer close(d.incoming)
	for {
		typ, data, err := d.conn.Read(d.ctx)
		if err != nil {
			select {
			case d.readErrC <- err:
			default:
			}
			return
		}
		if typ != websocket.MessageBinary {
			select {
			case d.readErrC <- fmt.Errorf("conformance: a non-binary message arrived, type %v", typ):
			default:
			}
			return
		}

		var f pb.Frame
		if err := proto.Unmarshal(data, &f); err != nil {
			select {
			case d.readErrC <- fmt.Errorf("conformance: a captured payload is not a Frame: %w", err):
			default:
			}
			return
		}

		d.mu.Lock()
		d.raw = append(d.raw, data)
		d.frames = append(d.frames, &f)
		d.mu.Unlock()

		d.incoming <- &f
	}
}

// read takes the next frame, failing the spec if none arrives.
func (d *driven) read() *pb.Frame {
	GinkgoHelper()
	f, err := d.readErr(5 * time.Second)
	Expect(err).NotTo(HaveOccurred())
	return f
}

// readErr takes the next frame within the timeout, returning an error rather
// than failing, so a spec can assert on a close or on silence.
func (d *driven) readErr(timeout time.Duration) (*pb.Frame, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case f, ok := <-d.incoming:
		if !ok {
			select {
			case err := <-d.readErrC:
				return nil, err
			default:
				return nil, fmt.Errorf("conformance: the connection closed")
			}
		}
		return f, nil
	case <-timer.C:
		return nil, fmt.Errorf("conformance: no frame within %s", timeout)
	}
}

// drainUntilQuiet consumes frames until none arrives for the idle period. It
// does not disturb the connection: silence is measured on the channel, not by
// cancelling a read.
func (d *driven) drainUntilQuiet(idle time.Duration) {
	for {
		if _, err := d.readErr(idle); err != nil {
			return
		}
	}
}

// closed reports whether the connection has ended, waiting up to timeout.
func (d *driven) closed(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-d.incoming:
			if !ok {
				return true
			}
		case <-timer.C:
			return false
		}
	}
}

// captured returns copies of everything received so far.
func (d *driven) captured() ([]*pb.Frame, [][]byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	frames := make([]*pb.Frame, len(d.frames))
	copy(frames, d.frames)
	raw := make([][]byte, len(d.raw))
	copy(raw, d.raw)
	return frames, raw
}

// ack acknowledges up to seq, which is what re-opens the outbound window.
func (d *driven) ack(seq uint64) {
	GinkgoHelper()
	Expect(d.writeFrame(d.envelope(&pb.Ack{ServerSeq: seq}))).To(Succeed())
}

// until reads frames until pick returns true, returning that frame.
func (d *driven) until(pick func(frame *pb.Frame) bool) *pb.Frame {
	GinkgoHelper()
	for {
		f := d.read()
		if pick(f) {
			return f
		}
	}
}

func (d *driven) nextPatch() *pb.Patch {
	GinkgoHelper()
	return d.until(func(f *pb.Frame) bool { return f.GetPatch() != nil }).GetPatch()
}

func (d *driven) nextError() *pb.Error {
	GinkgoHelper()
	return d.until(func(f *pb.Frame) bool { return f.GetError() != nil }).GetError()
}

func (d *driven) nextSnapshot() *pb.Snapshot {
	GinkgoHelper()
	return d.until(func(f *pb.Frame) bool { return f.GetSnapshot() != nil }).GetSnapshot()
}

// patches returns every Patch captured so far.
func (d *driven) patches() []*pb.Patch {
	frames, _ := d.captured()
	var out []*pb.Patch
	for _, f := range frames {
		if p := f.GetPatch(); p != nil {
			out = append(out, p)
		}
	}
	return out
}

// snapshots returns every Snapshot captured so far.
func (d *driven) snapshots() []*pb.Snapshot {
	frames, _ := d.captured()
	var out []*pb.Snapshot
	for _, f := range frames {
		if s := f.GetSnapshot(); s != nil {
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Sending
// ---------------------------------------------------------------------------

// writeBytes puts arbitrary bytes on the wire as a binary message. It is the
// hostile-data entry point: nothing here constructs a valid frame for you.
func (d *driven) writeBytes(b []byte) error {
	return d.conn.Write(d.ctx, websocket.MessageBinary, b)
}

// writeText puts a text message on the wire, which the protocol forbids.
func (d *driven) writeText(s string) error {
	return d.conn.Write(d.ctx, websocket.MessageText, []byte(s))
}

func (d *driven) writeFrame(f *pb.Frame) error {
	b, err := proto.Marshal(f)
	if err != nil {
		return err
	}
	return d.writeBytes(b)
}

// envelope wraps a payload in a well-formed envelope for this session.
func (d *driven) envelope(payload any) *pb.Frame {
	f := &pb.Frame{ProtocolVersion: 1, SessionId: d.sessionID}
	switch p := payload.(type) {
	case *pb.Event:
		f.Payload = &pb.Frame_Event{Event: p}
	case *pb.Ack:
		f.Payload = &pb.Frame_Ack{Ack: p}
	case *pb.Heartbeat:
		f.Payload = &pb.Frame_Heartbeat{Heartbeat: p}
	case *pb.ResyncRequest:
		f.Payload = &pb.Frame_ResyncRequest{ResyncRequest: p}
	case *pb.ClientTelemetry:
		f.Payload = &pb.Frame_ClientTelemetry{ClientTelemetry: p}
	default:
		panic(fmt.Sprintf("conformance: envelope does not carry %T", payload))
	}
	return f
}

// event sends one well-formed event and returns the client_ref it used, which
// is the handle a spec joins the resulting patch back to.
func (d *driven) event(name string, seenSeq uint64, fields ...[2]string) uint64 {
	GinkgoHelper()
	d.ref++
	ev := &pb.Event{
		ClientRef:     d.ref,
		Name:          name,
		FragmentId:    "count",
		SeenServerSeq: seenSeq,
	}
	for _, kv := range fields {
		ev.Fields = append(ev.Fields, &pb.EventField{Key: kv[0], Value: kv[1]})
	}
	Expect(d.writeFrame(d.envelope(ev))).To(Succeed())
	return d.ref
}

// highestSeq reports the highest server_seq the harness has observed, which is
// the value a well-behaved client puts in seen_server_seq.
func (d *driven) highestSeq() uint64 {
	frames, _ := d.captured()
	var high uint64
	for _, f := range frames {
		if p := f.GetPatch(); p != nil && p.GetServerSeq() > high {
			high = p.GetServerSeq()
		}
		if s := f.GetSnapshot(); s != nil && s.GetServerSeq() > high {
			high = s.GetServerSeq()
		}
	}
	return high
}
