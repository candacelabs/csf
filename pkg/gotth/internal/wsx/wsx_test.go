package wsx_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/internal/render"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
	"github.com/candacelabs/csf/pkg/gotth/internal/wsx"
)

func TestWSX(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Transport Suite")
}

const allowedOrigin = "https://app.example"

type state struct{ N int }

type subject string

func (s subject) Subject() string { return string(s) }

type app struct {
	authorize func(ctx context.Context, peer session.Peer[subject], event session.Event) error
}

func (a *app) Init(ctx context.Context, peer session.Peer[subject]) (any, []session.Effect[subject], error) {
	return state{}, nil, nil
}

func (a *app) Authorize(ctx context.Context, p session.Peer[subject], ev session.Event) error {
	if a.authorize == nil {
		return nil
	}
	return a.authorize(ctx, p, ev)
}

func (a *app) Reduce(s any, ev session.Event) (any, []session.Effect[subject]) {
	st := s.(state)
	if ev.Name == "counter.increment" {
		st.N++
	}
	return st, nil
}

func (a *app) Teardown(ctx context.Context, peer session.Peer[subject], state any) {}

func (a *app) Registry() *render.Registry {
	reg, err := render.NewRegistry([]render.Fragment{{
		ID: "counter",
		Render: func(_ context.Context, s any, w io.Writer) error {
			_, err := fmt.Fprintf(w, "<b>%d</b>", s.(state).N)
			return err
		},
	}})
	Expect(err).NotTo(HaveOccurred())
	return reg
}

func (a *app) Registered(name string) bool { return name == "counter.increment" }

func (a *app) StateComparable() bool { return true }

// server wires a handler onto a real HTTP test server, so every spec below
// exercises the handshake, the framing and the acknowledgement path a browser
// would.
type server struct {
	handler *wsx.Handler[subject]
	http    *httptest.Server
	url     string
}

func newServer(mutate func(options *wsx.Options[subject])) *server {
	GinkgoHelper()

	behaviour := &app{}
	opts := wsx.Options[subject]{
		Origins:      []string{allowedOrigin},
		Authenticate: func(request *http.Request) (subject, error) { return subject("tester"), nil },
		CSRF:         func(request *http.Request) error { return nil },
		NewApp:       func(request *http.Request) session.IApp[subject] { return behaviour },
		Limits:       session.DefaultLimits(),
	}
	if mutate != nil {
		mutate(&opts)
	}

	h, err := wsx.NewHandler[subject](opts)
	Expect(err).NotTo(HaveOccurred())

	ts := httptest.NewServer(h)
	return &server{handler: h, http: ts, url: "ws" + strings.TrimPrefix(ts.URL, "http")}
}

func (s *server) stop() {
	Expect(s.handler.Close(contextWithTimeout(2 * time.Second))).To(Succeed())
	s.http.Close()
}

func contextWithTimeout(d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	DeferCleanup(cancel)
	return ctx
}

// dial performs the upgrade the way a client runtime would.
func (s *server) dial(ctx context.Context, headers http.Header) (*websocket.Conn, *http.Response, error) {
	if headers == nil {
		headers = http.Header{}
		headers.Set("Origin", allowedOrigin)
	}
	return websocket.Dial(ctx, s.url, &websocket.DialOptions{
		HTTPHeader:   headers,
		Subprotocols: []string{protocol.Subprotocol},
	})
}

func (s *server) mustDial(ctx context.Context) *websocket.Conn {
	GinkgoHelper()
	c, _, err := s.dial(ctx, nil)
	Expect(err).NotTo(HaveOccurred())
	return c
}

func readFrame(ctx context.Context, c *websocket.Conn) *pb.Frame {
	GinkgoHelper()
	typ, data, err := c.Read(ctx)
	Expect(err).NotTo(HaveOccurred())
	Expect(typ).To(Equal(websocket.MessageBinary), "the server sent a non-binary message")

	var f pb.Frame
	Expect(proto.Unmarshal(data, &f)).To(Succeed())
	return &f
}

func writeFrame(ctx context.Context, c *websocket.Conn, f *pb.Frame) {
	GinkgoHelper()
	b, err := proto.Marshal(f)
	Expect(err).NotTo(HaveOccurred())
	Expect(c.Write(ctx, websocket.MessageBinary, b)).To(Succeed())
}

func sessionIDOf(f *pb.Frame) []byte { return f.GetSessionId() }

var _ = Describe("The handshake", func() {
	// The order is the security property: origin, then authentication, then
	// the CSRF token, then the subprotocol, and only then the upgrade. Nothing
	// per-session is allocated before authentication succeeds.
	var s *server

	AfterEach(func() {
		if s != nil {
			s.stop()
			s = nil
		}
	})

	It("refuses a disallowed origin before allocating anything", func() {
		s = newServer(nil)

		headers := http.Header{}
		headers.Set("Origin", "https://evil.example")
		_, resp, err := s.dial(contextWithTimeout(time.Second), headers)

		Expect(err).To(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
		Expect(s.handler.Sessions()).To(BeZero())
	})

	It("refuses a request that sends no origin at all", func() {
		s = newServer(nil)

		_, resp, err := s.dial(contextWithTimeout(time.Second), http.Header{})

		Expect(err).To(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
	})

	It("accepts any origin only through the named sentinel", func() {
		s = newServer(func(o *wsx.Options[subject]) { o.Origins = []string{wsx.AnyOrigin} })

		headers := http.Header{}
		headers.Set("Origin", "https://anywhere.example")
		c, _, err := s.dial(contextWithTimeout(time.Second), headers)

		Expect(err).NotTo(HaveOccurred())
		defer c.CloseNow()
	})

	It("refuses an unauthenticated request before allocating anything", func() {
		s = newServer(func(o *wsx.Options[subject]) {
			o.Authenticate = func(request *http.Request) (subject, error) {
				return "", errors.New("no cookie")
			}
		})

		_, resp, err := s.dial(contextWithTimeout(time.Second), nil)

		Expect(err).To(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		Expect(s.handler.Sessions()).To(BeZero())
	})

	It("refuses a request that fails the CSRF check", func() {
		s = newServer(func(o *wsx.Options[subject]) {
			o.CSRF = func(request *http.Request) error { return errors.New("bad token") }
		})

		_, resp, err := s.dial(contextWithTimeout(time.Second), nil)

		Expect(err).To(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
	})

	It("refuses a client that does not offer the subprotocol", func() {
		s = newServer(nil)

		headers := http.Header{}
		headers.Set("Origin", allowedOrigin)
		_, resp, err := websocket.Dial(contextWithTimeout(time.Second), s.url,
			&websocket.DialOptions{HTTPHeader: headers})

		Expect(err).To(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusUpgradeRequired))
	})

	It("refuses a connection past the per-identity session limit", func() {
		s = newServer(func(o *wsx.Options[subject]) { o.MaxSessionsPerIdentity = 1 })
		ctx := contextWithTimeout(2 * time.Second)

		first := s.mustDial(ctx)
		defer first.CloseNow()
		readFrame(ctx, first)

		_, resp, err := s.dial(ctx, nil)

		Expect(err).To(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusServiceUnavailable))
	})

	// BR-8. Options.MaxSessions says it "bounds the whole process", and nothing
	// asserted it — the suite covered only the per-identity limit, which is why
	// this went unnoticed. admit checked len(h.sessions), which nothing had
	// written yet: registration happens after mintID, after websocket.Accept —
	// a network write — and after NewApp, so every upgrade racing through that
	// window read the same stale length and all of them passed.
	//
	// Concurrent rather than serial, and that is the whole point: serially, the
	// registration of the first has always completed before the second is
	// admitted, so a serial spec passes against the defect.
	It("admits exactly one of many concurrent upgrades against a process limit of one", func() {
		s = newServer(func(o *wsx.Options[subject]) {
			o.MaxSessions = 1
			// Not the limit under test: one identity dials every one of these.
			o.MaxSessionsPerIdentity = 0
		})
		ctx := contextWithTimeout(5 * time.Second)

		const upgrades = 16
		var (
			wg       sync.WaitGroup
			mu       sync.Mutex
			admitted []*websocket.Conn
			refused  int
		)
		start := make(chan struct{})

		for i := 0; i < upgrades; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				<-start

				c, resp, err := s.dial(ctx, nil)
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					admitted = append(admitted, c)
					return
				}
				if resp != nil && resp.StatusCode == http.StatusServiceUnavailable {
					refused++
				}
			}()
		}
		close(start)
		wg.Wait()

		for _, c := range admitted {
			defer c.CloseNow()
		}

		Expect(admitted).To(HaveLen(1),
			"%d concurrent upgrades passed a process limit of 1", len(admitted))
		Expect(refused).To(Equal(upgrades - 1))
		Expect(s.handler.Sessions()).To(Equal(1))
	})

	// The other half of the reservation: a slot released by a connection that
	// never registered has to come back, or the process bleeds capacity until
	// it admits nothing at all.
	It("returns a process slot when an admitted connection closes", func() {
		s = newServer(func(o *wsx.Options[subject]) { o.MaxSessions = 1 })
		ctx := contextWithTimeout(5 * time.Second)

		first := s.mustDial(ctx)
		readFrame(ctx, first)
		Eventually(s.handler.Sessions).Should(Equal(1))

		_, resp, err := s.dial(ctx, nil)
		Expect(err).To(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusServiceUnavailable))

		Expect(first.Close(websocket.StatusNormalClosure, "")).To(Succeed())
		Eventually(s.handler.Sessions).Should(BeZero())

		second := s.mustDial(ctx)
		defer second.CloseNow()
		Expect(readFrame(ctx, second).GetSnapshot()).NotTo(BeNil(),
			"the process slot was never returned, so the limit ratcheted down to zero")
	})

	It("sends a snapshot as the first frame and nothing before it", func() {
		s = newServer(nil)
		ctx := contextWithTimeout(2 * time.Second)

		c := s.mustDial(ctx)
		defer c.CloseNow()

		f := readFrame(ctx, c)
		Expect(f.GetSnapshot()).NotTo(BeNil())
		Expect(f.GetProtocolVersion()).To(Equal(protocol.Version))
		Expect(f.GetSessionId()).To(HaveLen(16))
		Expect(f.GetSnapshot().GetServerSeq()).To(Equal(uint64(1)))
	})
})

var _ = Describe("A live connection", func() {
	var (
		s   *server
		ctx context.Context
		c   *websocket.Conn
		id  []byte
	)

	BeforeEach(func() {
		s = newServer(nil)
		ctx = contextWithTimeout(5 * time.Second)
		c = s.mustDial(ctx)
		id = sessionIDOf(readFrame(ctx, c))
	})

	AfterEach(func() {
		c.CloseNow()
		s.stop()
	})

	event := func(name string, ref uint64) *pb.Frame {
		return &pb.Frame{
			ProtocolVersion: protocol.Version,
			SessionId:       id,
			Payload: &pb.Frame_Event{Event: &pb.Event{
				ClientRef: ref, Name: name, FragmentId: "counter", SeenServerSeq: 1,
			}},
		}
	}

	It("answers an event with a patch carrying the causal chain", func() {
		writeFrame(ctx, c, event("counter.increment", 1))

		f := readFrame(ctx, c)
		p := f.GetPatch()

		Expect(p).NotTo(BeNil())
		Expect(p.GetServerSeq()).To(Equal(uint64(2)))
		Expect(p.GetOrigin().GetClientRef()).To(Equal(uint64(1)))
		Expect(p.GetOrigin().GetSource()).To(Equal("event:counter.increment"))
		Expect(p.GetUpdates()[0].GetHtml()).To(Equal("<b>1</b>"))
	})

	It("closes on a text frame rather than trying to interpret it", func() {
		Expect(c.Write(ctx, websocket.MessageText, []byte("hello"))).To(Succeed())

		_, _, err := c.Read(ctx)
		Expect(websocket.CloseStatus(err)).To(Equal(websocket.StatusCode(protocol.CloseProtocolViolation)))
	})

	It("refuses a frame naming another session and closes the connection", func() {
		f := event("counter.increment", 1)
		f.SessionId = []byte("fedcba9876543210")
		writeFrame(ctx, c, f)

		reply := readFrame(ctx, c)
		Expect(reply.GetError().GetCode()).To(Equal(pb.ErrorCode_INVALID_FRAME))

		_, _, err := c.Read(ctx)
		Expect(websocket.CloseStatus(err)).To(Equal(websocket.StatusCode(protocol.CloseProtocolViolation)))
	})

	It("refuses an unsupported protocol version with a reason rather than as malformed", func() {
		f := event("counter.increment", 1)
		f.ProtocolVersion = 99
		writeFrame(ctx, c, f)

		reply := readFrame(ctx, c)
		Expect(reply.GetError().GetCode()).To(Equal(pb.ErrorCode_UNSUPPORTED_VERSION))

		_, _, err := c.Read(ctx)
		Expect(websocket.CloseStatus(err)).To(Equal(websocket.StatusCode(protocol.CloseUnsupportedVersion)))
	})

	It("survives a frame it can refuse without closing", func() {
		bad := event("counter.increment", 1)
		bad.GetEvent().Fields = []*pb.EventField{{Key: "bad key", Value: "x"}}
		writeFrame(ctx, c, bad)

		reply := readFrame(ctx, c)
		Expect(reply.GetError()).NotTo(BeNil())
		Expect(reply.GetError().GetFatal()).To(BeFalse())

		writeFrame(ctx, c, event("counter.increment", 2))
		Expect(readFrame(ctx, c).GetPatch()).NotTo(BeNil())
	})

	It("refuses random bytes without panicking or hanging", func() {
		Expect(c.Write(ctx, websocket.MessageBinary, []byte{0xff, 0x01, 0x02, 0x03})).To(Succeed())

		reply := readFrame(ctx, c)
		Expect(reply.GetError()).NotTo(BeNil())
	})

	It("refuses an oversize frame at the transport, before it is decoded", func() {
		huge := make([]byte, session.DefaultLimits().MaxInboundFrameBytes+1024)
		Expect(c.Write(ctx, websocket.MessageBinary, huge)).To(Succeed())

		_, _, err := c.Read(ctx)
		Expect(err).To(HaveOccurred())
	})

	It("answers an unauthorized event without closing, and never reduces it", func() {
		denied := newServer(func(o *wsx.Options[subject]) {
			o.NewApp = func(request *http.Request) session.IApp[subject] {
				return &app{authorize: func(ctx context.Context, peer session.Peer[subject], event session.Event) error {
					return &session.DenyError{Reason: "not yours"}
				}}
			}
		})
		defer denied.stop()

		dctx := contextWithTimeout(2 * time.Second)
		dc := denied.mustDial(dctx)
		defer dc.CloseNow()
		did := sessionIDOf(readFrame(dctx, dc))

		writeFrame(dctx, dc, &pb.Frame{
			ProtocolVersion: protocol.Version,
			SessionId:       did,
			Payload: &pb.Frame_Event{Event: &pb.Event{
				ClientRef: 1, Name: "counter.increment", FragmentId: "counter", SeenServerSeq: 1,
			}},
		})

		reply := readFrame(dctx, dc)
		Expect(reply.GetError().GetCode()).To(Equal(pb.ErrorCode_UNAUTHORIZED))
		Expect(reply.GetError().GetFatal()).To(BeFalse())
	})
})

var _ = Describe("Draining", func() {
	It("closes every live session with the going-away code", func() {
		s := newServer(nil)
		ctx := contextWithTimeout(5 * time.Second)

		c := s.mustDial(ctx)
		defer c.CloseNow()
		readFrame(ctx, c)
		Eventually(s.handler.Sessions).Should(Equal(1))

		// A real client is always reading, and a graceful close is a handshake
		// rather than a hang-up: the spec reads the way a client does so that
		// what it asserts is the code the client actually receives.
		closed := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, _, err := c.Read(context.Background())
			closed <- err
		}()

		Expect(s.handler.Close(ctx)).To(Succeed())

		var err error
		Eventually(closed, 5*time.Second).Should(Receive(&err))
		Expect(websocket.CloseStatus(err)).To(Equal(websocket.StatusCode(protocol.CloseGoingAway)))
		Expect(s.handler.Sessions()).To(BeZero())
		s.http.Close()
	})

	It("refuses new connections once draining has begun", func() {
		s := newServer(nil)
		ctx := contextWithTimeout(5 * time.Second)
		Expect(s.handler.Close(ctx)).To(Succeed())

		_, resp, err := s.dial(ctx, nil)

		Expect(err).To(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusServiceUnavailable))
		s.http.Close()
	})
})

// D-10: the leak criterion's memory half.
//
// FR-22's checkpoint-1 criterion (CP1-16) asks 10,000 connect/disconnect cycles
// to return "goroutine count AND RSS to baseline within stated tolerance". The
// goroutine half has run and passed since checkpoint 1. RSS was never sampled at
// all, which is what QA-1 recorded as D-10 and what PM-1 folded into the
// checkpoint-3 G2 box, on the ground that the leak test is where the RSS sample
// belongs.
//
// # Two signals, because one of them is blunt and says so
//
// The criterion names RSS, so RSS is measured — /proc/self/statm, the process's
// real resident set. But `go test -race` is how ci.sh runs this suite, and the
// race detector's shadow memory grows with the distinct addresses a process has
// touched, is never returned, and is not memory this library allocated. Measured
// over three runs of the 10,000-cycle soak in dis-gotth-live:latest with -race:
// 20.4, 23.7 and 22.8 MiB of RSS retained, i.e. ≈2.3 KB per cycle of residue no
// leak of ours causes. An RSS budget must sit above that, and one that does
// cannot see a leak smaller than about 2.6 KB per cycle.
//
// So the live heap is measured too: runtime/metrics `/gc/heap/live:bytes` after
// debug.FreeOSMemory, which is exactly "what the last GC found still
// REACHABLE". It is blind to the race detector's shadow and to the allocator's
// arena growth, and over the same three runs it retained 141,720 / 114,280 /
// 116,040 B — ≈12–14 B per cycle. That is the sharp half.
//
// Both are asserted. Neither budget is a round number picked for comfort; each
// is stated below with the measurement it came from and the smallest leak it can
// see.
const (
	// liveHeapBudgetPerCycle is set from what must be CAUGHT, not from what was
	// observed: 128 B is the smallest per-session line RFC-0001 §6.2 sizes (the
	// 16 fragment render hashes), and 64 B is half of it. A cycle that retained
	// even that smallest line would fail this spec at 10,000 cycles.
	//
	// Against the measurement it is 4.5× the observed ≈14 B/cycle worst case, and
	// with the fixed allowance below it leaves the spec able to see a per-cycle
	// retention of ≈76 B — under the 128 B line, under the 256 B acknowledgement
	// channel, under the 512 B mailbox, under the 1,024 B window, and two orders
	// of magnitude under the 22,239 B of live heap the G2 baseline measures for
	// one idle session (docs/bench/g2-baseline.md §5).
	liveHeapBudgetPerCycle = 64

	// liveHeapFixedAllowance is what the measured run needs that no later cycle
	// repeats — lazily built maps and per-P caches that the 500-cycle warm-up
	// below does not always finish growing. Observed worst case after warm-up:
	// 36,136 B. It is charged ONCE rather than folded into the per-cycle figure,
	// because folding a fixed cost into a per-cycle budget hands the 10,000-cycle
	// case 10,000 times the slack it needs, which is how a threshold stops being
	// one.
	liveHeapFixedAllowance = 256 << 10

	// rssBudgetPerCycle is one page, which is the granularity RSS can move in at
	// all, and ≈1.8× the ≈2.3 KB/cycle of race-detector residue measured above.
	//
	// What this half can and cannot see is stated rather than implied: with the
	// fixed allowance below it fails on a per-cycle retention of ≈2.6 KB or more
	// — a leaked 4,096 B WebSocket read buffer, a leaked pair of 8 KiB goroutine
	// stacks, a leaked session — and it CANNOT distinguish a leaked 512 B mailbox
	// from the race detector's own noise. The live-heap half is what covers that
	// range, and this half is here because CP1-16 says RSS and a criterion is not
	// satisfied by measuring something adjacent to it.
	rssBudgetPerCycle = 4 << 10

	// rssFixedOverhead covers the first measured cycles' page-in of regions the
	// warm-up had not touched. Observed worst case after warm-up: 749,568 B.
	rssFixedOverhead = 8 << 20

	// churnWarmUp is run before either baseline is taken, for the same reason
	// equivalence-spec §3.6 warms up before M(0): a baseline taken on a process
	// that has never served a connection charges the measured window with every
	// one-time cost of serving the first one, and the budget then has to be wide
	// enough to absorb it. 500 cycles cut the observed 10,000-cycle RSS retention
	// from ≈30 MiB to ≈21 MiB and the live-heap retention from ≈550 kB to
	// ≈130 kB; the tightness of both budgets comes from here.
	churnWarmUp = 500
)

var _ = Describe("Connection lifecycle", func() {
	// The leak assertion, in two halves that fail for different reasons.
	//
	// Goroutines: every goroutine this library starts has an owner, a stop
	// condition and something that waits for it; the check is over the process
	// count, because a goroutine with a name and no exit is still a leak.
	//
	// Memory: a goroutine count back at baseline says every goroutine exited and
	// says nothing about what they left behind. A session's mailbox, its
	// acknowledgement channel, its unacked window and its read buffer are heap
	// the actor's exit is supposed to make unreachable, and one reference held a
	// level up — a registry entry not deleted, a closure captured by a timer, a
	// slice appended to and never truncated — retains all of it with every
	// goroutine properly accounted for. That is the class this half exists for
	// and the class the goroutine half is structurally unable to see.
	churn := func(s *server, ctx context.Context, n int) {
		GinkgoHelper()
		for i := 0; i < n; i++ {
			c := s.mustDial(ctx)
			readFrame(ctx, c)
			Expect(c.Close(websocket.StatusNormalClosure, "")).To(Succeed())
		}
	}

	cycles := func(n int) {
		GinkgoHelper()
		s := newServer(nil)
		defer s.stop()

		ctx := contextWithTimeout(10 * time.Minute)

		// The goroutine baseline is taken before the warm-up, so that half of
		// the assertion covers churnWarmUp + n cycles rather than n. Widening a
		// check that already passes is free; narrowing one is not.
		baseline := settled()
		churn(s, ctx, churnWarmUp)

		baselineRSS := residentAfterRelease()
		baselineLive := liveHeapAfterRelease()

		churn(s, ctx, n)

		Eventually(s.handler.Sessions, 30*time.Second).Should(BeZero())
		Eventually(settled, 30*time.Second).Should(BeNumerically("<=", baseline+4),
			"goroutines outlived the connections that owned them")

		liveBudget := int64(liveHeapFixedAllowance) + int64(n)*liveHeapBudgetPerCycle
		Eventually(liveHeapAfterRelease, 60*time.Second, 2*time.Second).
			Should(BeNumerically("<=", baselineLive+liveBudget),
				"%d cycles left more than %d bytes of LIVE HEAP behind "+
					"(%d B/cycle over a one-off %d B), measured by /gc/heap/live:bytes "+
					"after debug.FreeOSMemory: every goroutine exited and something is "+
					"still holding what they allocated",
				n, liveBudget, liveHeapBudgetPerCycle, liveHeapFixedAllowance)

		rssBudget := int64(rssFixedOverhead) + int64(n)*rssBudgetPerCycle
		Eventually(residentAfterRelease, 60*time.Second, 2*time.Second).
			Should(BeNumerically("<=", baselineRSS+rssBudget),
				"%d cycles left more than %d bytes RESIDENT (%d B/cycle over a "+
					"one-off %d B), measured from /proc/self/statm after "+
					"debug.FreeOSMemory: this is CP1-16's RSS half",
				n, rssBudget, rssBudgetPerCycle, rssFixedOverhead)

		// The margins are published, not merely asserted. A threshold whose
		// distance from the measurement nobody records is a threshold that drifts
		// into being unfalsifiable one commit at a time; these two entries are
		// what let the next reader re-derive the budgets above instead of
		// trusting them.
		AddReportEntry("D-10 live heap retained after release", fmt.Sprintf(
			"%d cycles: %d B (%.1f B/cycle) against a budget of %d B",
			n, liveHeapAfterRelease()-baselineLive,
			float64(liveHeapAfterRelease()-baselineLive)/float64(n), liveBudget))
		AddReportEntry("D-10 RSS retained after release", fmt.Sprintf(
			"%d cycles: %d B (%.1f B/cycle) against a budget of %d B",
			n, residentAfterRelease()-baselineRSS,
			float64(residentAfterRelease()-baselineRSS)/float64(n), rssBudget))
	}

	It("returns goroutines, live heap and RSS to baseline across a hundred connect and disconnect cycles", func() {
		cycles(100)
	})

	It("returns goroutines, live heap and RSS to baseline across ten thousand cycles", Label("soak"), func() {
		if testing.Short() {
			Skip("the ten-thousand-cycle soak is skipped under -short")
		}
		cycles(10000)
	})
})

// settled lets the scheduler quiesce before counting, so the figure is the one
// that matters rather than whatever was mid-exit.
//
// CS-9 keep: a best-effort quiesce is not an await. It asserts nothing and
// cannot fail — a count that never stops moving is returned anyway — so there
// is no condition for patience.Await to own, and making one up here would turn
// a sampling helper into an assertion its callers never asked for.
func settled() int {
	var last int
	for i := 0; i < 40; i++ {
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

// release collects and hands back everything the runtime can hand back, so that
// the readings below are about what is still REACHABLE rather than about when
// the background scavenger last ran.
//
// Go's scavenger returns free pages on its own schedule, so an unforced reading
// a second after the last cycle is dominated by memory that is free and has
// simply not been given back yet: the number would drift downwards for minutes
// and the assertion would be a race against a timer. debug.FreeOSMemory
// collects and releases synchronously. It runs three times because one pass can
// make objects unreachable that only the next pass sweeps.
//
// Note what forcing here does NOT do to the G2 figure. G2 is steady-state
// memory per LIVE idle connection, measured from outside the container with
// nothing forced, because that is what a deployment sees (equivalence-spec §3.6,
// docs/bench/g2-baseline.md). This is retention after every connection is gone.
// Forcing is right for exactly one of those two questions.
func release() {
	for i := 0; i < 3; i++ {
		runtime.GC()
		debug.FreeOSMemory()
	}
}

// liveHeapAfterRelease is the heap the last GC found reachable.
func liveHeapAfterRelease() int64 {
	release()
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(sample)
	return int64(sample[0].Value.Uint64())
}

// residentAfterRelease is the process's resident set, CP1-16's literal signal.
func residentAfterRelease() int64 {
	GinkgoHelper()
	release()
	return residentBytes()
}

// residentBytes reads the resident set from /proc/self/statm, field 2, in pages.
//
// It fails rather than skips when /proc is not there. A skip would make this
// spec pass on any platform that cannot answer the question, and `go test` folds
// a skip into exit 0 — which is exactly how D-20's browser specs ran nineteen
// deep and green. This module's dev image, its CI image and its deployment
// target are Linux; a platform where CP1-16's criterion cannot be evaluated
// should say so in red.
func residentBytes() int64 {
	GinkgoHelper()
	raw, err := os.ReadFile("/proc/self/statm")
	Expect(err).NotTo(HaveOccurred(),
		"the resident set is read from /proc/self/statm and this platform has no /proc: "+
			"FR-22's criterion (CP1-16) names RSS, and a spec that skips when it cannot "+
			"measure RSS is a spec that passes for the wrong reason")
	fields := strings.Fields(string(raw))
	Expect(len(fields)).To(BeNumerically(">=", 2), "/proc/self/statm: %q", string(raw))
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	Expect(err).NotTo(HaveOccurred(), "/proc/self/statm resident field: %q", fields[1])
	return pages * int64(os.Getpagesize())
}
