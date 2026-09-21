package livetest_test

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

// The wire values this suite asserts on, from proto/gotthlive/v1/frame.proto.
//
// They are spelled here rather than exported from livetest for the reason the
// Origin godoc gives: the .proto is the public artifact, and a consumer holding
// a capture reads the same numbers. Only the ones asserted on are named — an
// unnamed value arriving is a failure with a number in it, which is more useful
// than a missing constant.
const (
	originClientEvent = 1
	originMount       = 5

	codeUnknownEvent = 4
)

const (
	testOrigin        = "http://127.0.0.1:8080"
	fragmentValue     = "probe.value"
	fragmentLabel     = "probe.label"
	eventIncrement    = "probe.increment"
	eventRelabel      = "probe.relabel"
	eventUnregistered = "probe.never_registered"
)

// probeApp is the smallest application that can answer every question this
// suite asks: two independent regions so "which fragments did that patch
// carry" has an answer, and two events so one can move each.
func probeApp() *live.App[counter, live.AnonymousIdentity] {
	GinkgoHelper()

	app, err := live.New(live.Config[counter, live.AnonymousIdentity]{
		Init: func(ctx context.Context, session live.Session[live.AnonymousIdentity]) (counter, []live.Effect[live.AnonymousIdentity], error) {
			return counter{}, nil, nil
		},
		Reduce: func(state counter, ev live.Event) (counter, []live.Effect[live.AnonymousIdentity]) {
			switch ev.Name {
			case eventIncrement:
				state.N++
			case eventRelabel:
				state.Label = ev.Fields.Get("to")
			}
			return state, nil
		},
		Fragments: []live.Fragment[counter]{
			{
				ID:     fragmentValue,
				Render: func(s counter) templ.Component { return text("<b>%d</b>", s.N) },
				Dirty:  func(prev, next counter) bool { return prev.N != next.N },
			},
			{
				ID:     fragmentLabel,
				Render: func(s counter) templ.Component { return text("<i>%s</i>", s.Label) },
				Dirty:  func(prev, next counter) bool { return prev.Label != next.Label },
			},
		},
		Events:       []string{eventIncrement, eventRelabel},
		Origins:      []string{testOrigin},
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
	})
	Expect(err).NotTo(HaveOccurred())

	DeferCleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		Expect(app.Close(ctx)).To(Succeed())
	})
	return app
}

func dial(app *live.App[counter, live.AnonymousIdentity]) *livetest.Client {
	GinkgoHelper()
	return livetest.NewClient(GinkgoTB(), app.Handler(), livetest.ClientOptions{
		Path:    "/",
		Origin:  testOrigin,
		Timeout: 30 * time.Second,
	})
}

var _ = Describe("Client: the handshake", func() {
	It("returns only once the mount snapshot has arrived, carrying every region", func() {
		c := dial(probeApp())

		snap := c.Snapshot()
		Expect(snap.Kind).To(Equal(livetest.FrameSnapshot),
			"the first frame on a connection is the Snapshot (H-10)")
		Expect(snap.Patch.Origin.Kind).To(BeNumerically("==", originMount))
		Expect(snap.Patch.FragmentIDs()).To(ConsistOf(fragmentValue, fragmentLabel),
			"a snapshot renders every region; that is what makes it the expensive frame")
		Expect(c.SessionID()).To(HaveLen(16))
		Expect(c.Seq()).To(Equal(snap.Patch.ServerSeq))
	})

	It("carries the session parameters the client half needs to obey the protocol", func() {
		snap := dial(probeApp()).Snapshot()

		Expect(snap.Patch.AckWindow).To(BeNumerically(">", 0))
		Expect(snap.Patch.MaxInboundFrameBytes).To(BeNumerically(">", 0))
		Expect(snap.Patch.HeartbeatIntervalMS).To(BeNumerically(">", 0))
	})

	It("hands back a session identifier the caller cannot corrupt", func() {
		c := dial(probeApp())

		got := c.SessionID()
		for i := range got {
			got[i] = 0xff
		}

		Expect(c.SessionID()).NotTo(Equal(got),
			"SessionID returns the session's identity; handing out the backing array would let "+
				"one assertion silently break every frame written after it")
	})
})

var _ = Describe("Client: events and patches", func() {
	It("sends an event and receives the patch it caused, attributed to it", func() {
		c := dial(probeApp())

		ref := c.Send(eventIncrement, fragmentValue, nil)
		f := c.Await("the increment's patch", 5*time.Second,
			func(f *livetest.Frame) bool { return f.Kind == livetest.FramePatch })

		Expect(f.Patch.Origin.Kind).To(BeNumerically("==", originClientEvent))
		Expect(f.Patch.Origin.ClientRef).To(Equal(ref),
			"the client reference is what correlates a log line to the interaction that caused it")
		Expect(f.Patch.Origin.Source).To(Equal("event:" + eventIncrement))

		html, ok := f.Patch.Fragment(fragmentValue)
		Expect(ok).To(BeTrue())
		Expect(html).To(ContainSubstring("<b>1</b>"))
	})

	It("patches only the region the transition moved", func() {
		c := dial(probeApp())

		c.Send(eventRelabel, fragmentLabel, map[string]string{"to": "moved"})
		f := c.Await("the relabel's patch", 5*time.Second,
			func(f *livetest.Frame) bool { return f.Kind == livetest.FramePatch })

		Expect(f.Patch.FragmentIDs()).To(ConsistOf(fragmentLabel),
			"the value region did not move, so a patch carrying it would be a dirty-tracking defect")
	})

	It("returns a distinct client reference per event, in order", func() {
		c := dial(probeApp())

		first := c.Send(eventIncrement, fragmentValue, nil)
		c.Await("the first patch", 5*time.Second, isPatch)
		second := c.Send(eventIncrement, fragmentValue, nil)
		c.Await("the second patch", 5*time.Second, isPatch)

		Expect(second).To(BeNumerically(">", first))
	})

	It("reports the highest sequence it has seen, which is what an event frame claims", func() {
		c := dial(probeApp())
		start := c.Seq()

		c.Send(eventIncrement, fragmentValue, nil)
		f := c.Await("a patch", 5*time.Second, isPatch)

		Expect(c.Seq()).To(Equal(f.Patch.ServerSeq))
		Expect(c.Seq()).To(BeNumerically(">", start))
	})

	It("WaitFor blocks until the markup satisfies the predicate", func() {
		c := dial(probeApp())

		go func() {
			defer GinkgoRecover()
			for i := 0; i < 3; i++ {
				c.Send(eventIncrement, fragmentValue, nil)
				// CS-9 keep: pacing, not an await. The point of this spec is
				// that WaitFor blocks across several patches, so the sends have
				// to arrive spread out; three sent back to back could coalesce
				// into one patch and the spec would prove nothing.
				time.Sleep(20 * time.Millisecond)
			}
		}()

		f := c.WaitFor(fragmentValue, func(html string) bool {
			return strings.Contains(html, "<b>3</b>")
		})

		Expect(f.Patch.ServerSeq).To(BeNumerically(">", 0))
	})
})

var _ = Describe("Client: the window protocol", func() {
	// The property that makes the backpressure ladder reachable at all. A
	// client that acknowledged on its own could never be made slow without
	// throttling a socket, which is a different experiment.
	It("never acknowledges a patch on its own", func() {
		c := dial(probeApp())

		window := int(c.Snapshot().Patch.AckWindow)
		for i := 0; i < window+2; i++ {
			c.Send(eventIncrement, fragmentValue, nil)
		}
		c.Settle(300 * time.Millisecond)

		// Nothing acknowledged, so the server stopped at the window rather
		// than sending every patch the events asked for.
		var patches int
		for _, f := range c.Received() {
			if f.Kind == livetest.FramePatch {
				patches++
			}
		}
		Expect(patches).To(BeNumerically("<=", window),
			"the outbound window is %d and this client acknowledged nothing, so a patch beyond "+
				"it means either the window is not enforced or the harness acknowledged for us",
			window)
	})

	It("resumes once the client acknowledges", func() {
		c := dial(probeApp())

		window := int(c.Snapshot().Patch.AckWindow)
		for i := 0; i < window+2; i++ {
			c.Send(eventIncrement, fragmentValue, nil)
		}
		stalled := c.Settle(300 * time.Millisecond)
		Expect(stalled).NotTo(BeEmpty())

		c.Ack(stalled[len(stalled)-1].Patch.ServerSeq)

		Expect(c.Await("the patch the window was holding", 5*time.Second, isPatch)).NotTo(BeNil())
	})
})

var _ = Describe("Client: hostile input", func() {
	It("survives a payload that is not a frame, and reports the server's answer", func() {
		c := dial(probeApp())

		// Wire type 7 does not exist, so this is not decodable as any message.
		Expect(c.WriteRaw([]byte{0xff, 0xff, 0xff, 0xff})).To(Succeed())

		Expect(c.Closed(5*time.Second)).To(BeTrue(),
			"an undecodable frame is a protocol violation and the session ends; a client that "+
				"hung here would make every hostile-input spec a timeout")
	})

	It("delivers the typed error for an event the application never registered", func() {
		c := dial(probeApp())

		c.Send(eventUnregistered, fragmentValue, nil)
		f := c.Await("the UNKNOWN_EVENT error", 5*time.Second,
			func(f *livetest.Frame) bool { return f.Kind == livetest.FrameError })

		Expect(f.Error.Code).To(BeNumerically("==", codeUnknownEvent))
		Expect(f.Error.Fatal).To(BeFalse(),
			"an unregistered name is the client being wrong about one event, not the session failing")
	})

	It("refuses a dial whose Origin the application does not admit", func() {
		app := probeApp()

		r := run(func(tb testing.TB) {
			livetest.NewClient(tb, app.Handler(), livetest.ClientOptions{
				Path:    "/",
				Origin:  "http://evil.example",
				Timeout: 5 * time.Second,
			})
		})

		Expect(r.failed).To(BeTrue())
		Expect(r.message).To(ContainSubstring("dialling"))
	})

	DescribeTable("refuses a configuration that would hang instead of failing",
		func(opts livetest.ClientOptions, want string) {
			app := probeApp()
			r := run(func(tb testing.TB) { livetest.NewClient(tb, app.Handler(), opts) })

			Expect(r.failed).To(BeTrue())
			Expect(r.message).To(ContainSubstring(want))
		},
		Entry("no path", livetest.ClientOptions{Origin: testOrigin}, "ClientOptions.Path is empty"),
		Entry("no origin", livetest.ClientOptions{Path: "/"}, "ClientOptions.Origin is empty"),
	)

	It("refuses a nil handler by name rather than by nil dereference", func() {
		r := run(func(tb testing.TB) {
			livetest.NewClient(tb, nil, livetest.ClientOptions{Path: "/", Origin: testOrigin})
		})

		Expect(r.failed).To(BeTrue())
		Expect(r.message).To(ContainSubstring("the handler is nil"))
	})
})

// ---------------------------------------------------------------------------
// FR-58, on the value NextErr hands back.
//
// FR-58: "Every library-produced error MUST name the session, the causal ID
// where one exists, and the actionable next step."
//
// docs/error-audit.md §3.4 grades five of this package's messages as satisfying
// the session clause "via Client.where()". That was true of Next, Await and
// write, and false of NextErr — the one exported method that returns the error
// instead of failing the spec with it — until 2026-08-05: where() was called on
// the tb.Fatalf paths alone, so a caller who held the value held a sentence
// with no session in it. QA-1 drove it and filed it as F-1, a condition on
// Phase 4's box 12.
//
// These specs are the falsifier the five rows did not have. They assert on the
// RIGHT session — the identifier this client holds — rather than on some hex
// run being present, which is the standard live/fr58_test.go sets for a
// session clause and the reason "an error that names some session is not what
// FR-58 asks for" is written there.
// ---------------------------------------------------------------------------
var _ = Describe("Client: FR-58 on the error NextErr returns", func() {
	It("names the session when nothing arrived, and keeps the next step", func() {
		c := dial(probeApp())

		_, err := c.NextErr(300 * time.Millisecond)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(HavePrefix("livetest: "))
		Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("(session %x)", c.SessionID())),
			"a spec that stores, wraps or logs this value beside the server's own records "+
				"cannot say which session it is about unless the value says so")
		Expect(err.Error()).To(ContainSubstring("no frame arrived within 300ms"))
		Expect(err.Error()).To(ContainSubstring("the outbound window is full and nothing was acknowledged"),
			"the next-step clause has to survive the prefix, not be replaced by it")
	})

	It("names the session on the transport's read error, which this package did not author", func() {
		c := dial(probeApp())
		session := fmt.Sprintf("(session %x)", c.SessionID())

		// Wire type 7 does not exist, so the server takes this as a protocol
		// violation and closes. The error that ends the stream is then
		// coder/websocket's rather than one of this package's sentences, which
		// is the arm most in need of a session identifier: nothing else in it
		// names the connection it belongs to.
		Expect(c.WriteRaw([]byte{0xff, 0xff, 0xff, 0xff})).To(Succeed())

		var err error
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err = c.NextErr(time.Until(deadline)); err != nil {
				break
			}
		}

		Expect(err).To(HaveOccurred(), "the session was closed and NextErr must say so")
		Expect(err.Error()).To(HavePrefix("livetest: "))
		Expect(err.Error()).To(ContainSubstring(session))
	})

	It("fails the spec with exactly the string it returns, and says livetest once", func() {
		tb := &recordingTB{cleanupTB: &cleanupTB{}}
		c := livetest.NewClient(tb, probeApp().Handler(), livetest.ClientOptions{
			Path: "/", Origin: testOrigin, Timeout: 30 * time.Second,
		})
		DeferCleanup(tb.runCleanups)

		_, err := c.NextErr(200 * time.Millisecond)
		c.Next(200 * time.Millisecond)

		Expect(err).To(HaveOccurred())
		Expect(tb.message).To(Equal(err.Error()),
			"one prefix, applied in one place: the two paths drifting apart is the defect F-1 "+
				"filed, and identical strings are what stops it happening twice")
		Expect(strings.Count(tb.message, "livetest: ")).To(Equal(1),
			"Next must not re-apply a prefix NextErr already carries")
	})
})

var _ = Describe("Client: teardown", func() {
	// The leak this asserts against is structural rather than statistical: the
	// read pump is one goroutine per Client and it blocks in conn.Read, so a
	// Client whose cleanup did not run leaves a goroutine parked for the life
	// of the process. Counting goroutines would be flaky under a parallel
	// suite; naming the frame is not.
	It("joins the read goroutine, so no pump outlives its client", func() {
		app := probeApp()

		tb := &cleanupTB{}
		c := livetest.NewClient(tb, app.Handler(), livetest.ClientOptions{
			Path: "/", Origin: testOrigin, Timeout: 30 * time.Second,
		})
		c.Send(eventIncrement, fragmentValue, nil)
		c.Await("a patch", 5*time.Second, isPatch)

		Expect(stacks()).To(ContainSubstring("livetest.(*Client).pump"),
			"this spec is worthless if the pump was not running when it started")

		tb.runCleanups()

		Eventually(stacks, 5*time.Second, 20*time.Millisecond).
			ShouldNot(ContainSubstring("livetest.(*Client).pump"))
	})

	It("Close ends the session and a second Close is not an error", func() {
		c := dial(probeApp())

		Expect(c.Close()).To(Succeed())
		Expect(c.Close()).To(Succeed(),
			"teardown is registered with tb.Cleanup, so an explicit Close is always a second one")
	})
})

func isPatch(f *livetest.Frame) bool { return f.Kind == livetest.FramePatch }

// stacks is every goroutine's stack, which is how the teardown spec names the
// goroutine it cares about instead of counting them.
func stacks() string {
	buf := make([]byte, 1<<20)
	return string(buf[:runtime.Stack(buf, true)])
}

// cleanupTB is a testing.TB that collects Cleanup functions instead of running
// them at the end of a spec, so a spec can run teardown and then assert on what
// it released.
//
// It embeds a nil testing.TB: a method this suite never reaches panics rather
// than quietly doing nothing, which is the failure mode to want from a stub.
type cleanupTB struct {
	testing.TB
	cleanups []func()
}

func (t *cleanupTB) Helper()                      {}
func (t *cleanupTB) Name() string                 { return "cleanupTB" }
func (t *cleanupTB) Cleanup(fn func())            { t.cleanups = append(t.cleanups, fn) }
func (t *cleanupTB) Fatal(args ...any)            { Fail(fmt.Sprint(args...)) }
func (t *cleanupTB) Fatalf(f string, args ...any) { Fail(fmt.Sprintf(f, args...)) }

// recordingTB is a cleanupTB whose Fatalf records the failure instead of
// raising it, so a spec can hold what Next told the test beside what NextErr
// returned. Next returns after Fatalf rather than assuming it stopped the
// goroutine, so recording is enough and no stop-the-test panic is needed.
type recordingTB struct {
	*cleanupTB
	message string
}

func (t *recordingTB) Fatalf(f string, args ...any) { t.message = fmt.Sprintf(f, args...) }

func (t *cleanupTB) runCleanups() {
	for i := len(t.cleanups) - 1; i >= 0; i-- {
		t.cleanups[i]()
	}
	t.cleanups = nil
}
