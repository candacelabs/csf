package routers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	chi "github.com/go-chi/chi/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

func TestRouters(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "FR-33 Three-Router Mount Suite")
}

const (
	// fragmentBadge is the one live region. Its identifier is a contract in
	// three places at once — the Config, the markup, and every patch frame on
	// the wire — so it is a constant here for the same reason the examples
	// make it one.
	fragmentBadge = "routers.badge"

	// eventBump is the one registered event. Config.Events is default-deny, so
	// this is also the whole of what a browser may send.
	eventBump = "routers.bump"

	// subprotocol is the negotiated WebSocket subprotocol. A dial that does
	// not offer it is refused, and a connection that comes back without it
	// selected is not a gotth-live session, so both halves are asserted.
	subprotocol = "gotth-live.v1"

	// testOrigin is the browser Origin the specs send. Config.Origins is a
	// real allowlist rather than live.AnyOrigin, because an httptest server's
	// address is not known when the Config is built and a wildcard here would
	// quietly stop testing the deny-by-default path this library ships.
	testOrigin = "https://routers.example"

	// runtimeFile is the client artifact's filename, as live.Script appends it
	// to the mount. The specs never build a URL from it — they read the
	// rendered src — but the 404 specs need to name a path that exists under
	// one prefix and must not exist under another.
	runtimeFile = "gotth-live.min.js"
)

// state is one session's view. A counter is enough: this suite is about where
// the handler is reachable, not about what it computes.
type state struct{ N int }

func newApp() *live.App[state, live.AnonymousIdentity] {
	GinkgoHelper()

	app, err := live.New(live.Config[state, live.AnonymousIdentity]{
		Init: func(ctx context.Context, session live.Session[live.AnonymousIdentity]) (state, []live.Effect[live.AnonymousIdentity], error) {
			return state{}, nil, nil
		},
		Reduce: func(s state, ev live.Event) (state, []live.Effect[live.AnonymousIdentity]) {
			if ev.Name == eventBump {
				s.N++
			}
			return s, nil
		},
		Fragments: []live.Fragment[state]{{
			ID: fragmentBadge,
			Render: func(s state) templ.Component {
				return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
					_, err := fmt.Fprintf(w, "<b>bumps %d</b>", s.N)
					return err
				})
			},
			Dirty: func(prev, next state) bool { return prev != next },
		}},
		Events:  []string{eventBump},
		Origins: []string{testOrigin},
		// The three escape hatches, each because a mounting test has no
		// accounts to check against and each named so that a grep finds them.
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
	})
	Expect(err).NotTo(HaveOccurred())
	return app
}

// ---------------------------------------------------------------------------
// The three routers
// ---------------------------------------------------------------------------
//
// Each function is the whole of what mounting gotth-live under that router
// costs, and that is the claim FR-33 makes: ordinary http.Handler values, no
// adapter, no framework-specific shim, no StripPrefix. If any of these grew a
// wrapper the library needed, FR-33 would be false and this file is where it
// would show.

// netHTTPMux mounts under the standard library's own router.
//
// Two patterns, because the mount path is the WebSocket endpoint itself and
// mount+"/" is where the runtime is served from. The handler tells them apart
// by path suffix, so no StripPrefix is needed — and none is used, which is
// what makes this the same two lines the counter example's README shows.
func netHTTPMux(prefix string, h http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(prefix, h)
	mux.Handle(prefix+"/", h)
	return mux
}

// chiRouter mounts under github.com/go-chi/chi/v5.
//
// chi.Handle rather than chi.Mount: Mount rewrites RoutePath and is chi's own
// sub-router protocol, and using it here would be testing chi's adapter rather
// than the library's claim to need none.
func chiRouter(prefix string, h http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Handle(prefix, h)
	r.Handle(prefix+"/*", h)
	return r
}

// ginEngine mounts under github.com/gin-gonic/gin.
//
// gin.WrapH is gin's stdlib adapter and is the one framework-specific token in
// this file. It is gin's, not gotth-live's: it exists because gin's handler
// signature takes a *gin.Context, and it takes an http.Handler unmodified —
// which is precisely FR-33's property, seen from the framework side.
func ginEngine(prefix string, h http.Handler) http.Handler {
	gin.SetMode(gin.TestMode)
	g := gin.New()
	g.Any(prefix, gin.WrapH(h))
	g.Any(prefix+"/*rest", gin.WrapH(h))
	return g
}

// mountSpec is one router at one prefix.
//
// The three prefixes are DISTINCT and two of them are not "/live". That is
// L9-1 condition C-23 and it is the whole design of this table: mounting all
// three at /live would satisfy FR-33's wording literally while testing nothing
// about prefixes, and it is a non-/live prefix that would have caught the
// Script() hardcoded-mount defect. /app/live is nested; /ui/gotth shares no
// segment with either of the others, so a hardcoded "/live" anywhere in the
// library cannot accidentally answer it.
type mountSpec struct {
	router string
	prefix string
	build  func(prefix string, h http.Handler) http.Handler
}

var mounts = []mountSpec{
	{"net/http", "/live", netHTTPMux},
	{"chi", "/app/live", chiRouter},
	{"gin", "/ui/gotth", ginEngine},
}

// mounted is one live application behind one router behind a real HTTP server.
type mounted struct {
	spec   mountSpec
	app    *live.App[state, live.AnonymousIdentity]
	server *httptest.Server
}

func (m mountSpec) mount() *mounted {
	GinkgoHelper()

	app := newApp()
	server := httptest.NewServer(m.build(m.prefix, app.Handler()))

	DeferCleanup(func() {
		server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		Expect(app.Close(ctx)).To(Succeed())
	})
	return &mounted{spec: m, app: app, server: server}
}

// tag renders the script tag this application's page would carry, from the
// prefix it is actually mounted at.
func (m *mounted) tag() string {
	GinkgoHelper()

	var buf strings.Builder
	Expect(live.Script(m.spec.prefix).Render(context.Background(), &buf)).To(Succeed())
	return buf.String()
}

// get fetches a path from this router. The path is passed through unchanged,
// so a spec asserts against exactly what the rendered tag names rather than
// against a URL the spec rebuilt from the same input.
func (m *mounted) get(path string) (*http.Response, []byte) {
	GinkgoHelper()

	resp, err := http.Get(m.server.URL + path)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return resp, body
}

// ---------------------------------------------------------------------------
// A connected browser
// ---------------------------------------------------------------------------

// browser is one tab: a real dialled WebSocket with every frame it received
// retained.
type browser struct {
	conn *websocket.Conn
	ctx  context.Context

	sessionID []byte
	seq       uint64
	ref       uint64

	incoming chan *wireFrame
	readErr  chan error

	mu     sync.Mutex
	frames []*wireFrame
}

// open dials the URL the rendered tag told the browser to dial.
//
// url is the data-gotth-url attribute, read out of the tag rather than
// recomputed, which is the point of the spec: the page names a path and the
// session has to be there.
func (m *mounted) open(url string) *browser {
	GinkgoHelper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	DeferCleanup(cancel)

	headers := http.Header{}
	headers.Set("Origin", testOrigin)

	conn, _, err := websocket.Dial(ctx,
		"ws"+strings.TrimPrefix(m.server.URL, "http")+url,
		&websocket.DialOptions{HTTPHeader: headers, Subprotocols: []string{subprotocol}})
	Expect(err).NotTo(HaveOccurred(),
		"the tag rendered for the %s mount points the browser at %s, and no session is there",
		m.spec.router, url)

	b := &browser{
		conn:     conn,
		ctx:      ctx,
		incoming: make(chan *wireFrame, 64),
		readErr:  make(chan error, 1),
	}
	go b.pump()
	DeferCleanup(func() { _ = conn.CloseNow() })
	return b
}

// dialFails reports the error from dialling a path no session is mounted at,
// so the 404 spec can assert on the WebSocket half as well as the GET half.
func (m *mounted) dialFails(url string) error {
	GinkgoHelper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	headers := http.Header{}
	headers.Set("Origin", testOrigin)

	conn, _, err := websocket.Dial(ctx,
		"ws"+strings.TrimPrefix(m.server.URL, "http")+url,
		&websocket.DialOptions{HTTPHeader: headers, Subprotocols: []string{subprotocol}})
	if err == nil {
		_ = conn.CloseNow()
	}
	return err
}

// pump reads for the life of the connection. It is the only caller of
// conn.Read, so no spec can close the socket by timing out: coder/websocket
// closes the connection when a read's context is cancelled, because a
// half-consumed message cannot be abandoned safely.
func (b *browser) pump() {
	defer close(b.incoming)
	for {
		typ, data, err := b.conn.Read(b.ctx)
		if err != nil {
			select {
			case b.readErr <- err:
			default:
			}
			return
		}
		if typ != websocket.MessageBinary {
			select {
			case b.readErr <- fmt.Errorf("routers: a non-binary message arrived, type %v", typ):
			default:
			}
			return
		}
		f, err := decodeFrame(data)
		if err != nil {
			select {
			case b.readErr <- err:
			default:
			}
			return
		}

		b.mu.Lock()
		b.frames = append(b.frames, f)
		b.mu.Unlock()
		b.incoming <- f
	}
}

// next returns the next frame that is not a heartbeat, failing with what it
// did see rather than with a bare timeout.
func (b *browser) next(timeout time.Duration) *wireFrame {
	GinkgoHelper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case f, ok := <-b.incoming:
			if !ok {
				select {
				case err := <-b.readErr:
					Fail("routers: the connection ended: " + err.Error())
				default:
					Fail("routers: the connection closed with no frame")
				}
			}
			if f.Kind == "heartbeat" {
				continue
			}
			return f
		case <-timer.C:
			Fail("routers: no frame arrived within " + timeout.String() +
				"; frames so far: " + b.summary())
		}
	}
}

// snapshot takes the first frame and requires it to be the Snapshot.
func (b *browser) snapshot(timeout time.Duration) *wireFrame {
	GinkgoHelper()

	first := b.next(timeout)
	Expect(first.Kind).To(Equal("snapshot"),
		"the first frame on a connection is the Snapshot (H-10); got a %s", first.describe())
	Expect(first.SessionID).NotTo(BeEmpty())

	b.sessionID = first.SessionID
	b.seq = first.Patch.ServerSeq
	return first
}

// send writes an event frame, exactly as the client runtime would.
func (b *browser) send(name string) {
	GinkgoHelper()

	b.ref++
	frame := encodeEventFrame(b.sessionID, b.ref, name, fragmentBadge, b.seq)
	Expect(b.conn.Write(b.ctx, websocket.MessageBinary, frame)).To(Succeed())
}

func (b *browser) summary() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.frames) == 0 {
		return "none"
	}
	out := make([]string, 0, len(b.frames))
	for _, f := range b.frames {
		out = append(out, f.describe())
	}
	return strings.Join(out, "; ")
}

// ---------------------------------------------------------------------------

// attr pulls one attribute out of the rendered script tag.
//
// The specs read src and data-gotth-url out of the rendered bytes rather than
// recomputing either from the mount path, because a spec that rebuilt the URL
// it then fetched would pass for a tag that names something else entirely —
// which is the shape of the defect C-23 exists to end.
func attr(tag, name string) string {
	GinkgoHelper()

	_, rest, found := strings.Cut(tag, name+`="`)
	Expect(found).To(BeTrue(), "no %s attribute in %q", name, tag)
	value, _, found := strings.Cut(rest, `"`)
	Expect(found).To(BeTrue(), "unterminated %s attribute in %q", name, tag)
	return value
}
