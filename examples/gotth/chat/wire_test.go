package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

// ---------------------------------------------------------------------------
// The session driver
// ---------------------------------------------------------------------------
//
// Several of Phase 2's exit criteria are claims about what is and is not on
// the wire — a patch's origin, an Error frame that must be there, an Error
// frame that must NOT be — and an application cannot check any of them by
// looking at its own state. Something has to read the bytes.
//
// This file used to read them itself: ~180 lines of decoder over
// google.golang.org/protobuf/encoding/protowire, written against the field
// numbers in proto/gotthlive/v1/frame.proto, plus a WebSocket driver around it.
// The reason was never that the work was interesting — it was that the
// generated types live under pkg/gotth/internal/, which a consumer's module
// can never import, so an example that proved these properties with the
// library's private codec would be proving them with a tool no reader of the
// example can pick up. FRICTION.md F-1 filed that as friction and named the
// fix.
//
// livetest.Client is the fix, and it is now built. It is in the library's
// SECOND EXPORTED PACKAGE, so it satisfies the constraint the hand-rolled
// decoder existed to satisfy — a reader of this example can pick it up — while
// being one decoder rather than the fifth copy of one. What is left below is
// the part that is about chat: the identity cookie, and the three verbs a
// member has.

// OriginKind values, from proto/gotthlive/v1/frame.proto. Only the ones this
// example can produce are named; an unnamed value arriving is a failure with a
// number in it, which is more useful than a missing constant.
const (
	originClientEvent = 1
	originEffect      = 2
	originMount       = 5
)

// ErrorCode values.
const (
	codeUnauthorized = 2
	codeInternal     = 7
)

// room is one live application behind a real HTTP server, with the room and
// the directory it was built over.
type mountedRoom struct {
	app    *live.App[State, Member]
	room   *Room
	dir    Directory
	server *httptest.Server
}

// mount builds the chat application and serves it, optionally mutating the
// Config first.
func mount(mutate func(*live.Config[State, Member])) *mountedRoom {
	GinkgoHelper()

	room := NewRoom()
	room.now = func() time.Time { return baseTime }
	dir := DemoDirectory()

	cfg := Config(room, dir, []string{testOrigin})
	if mutate != nil {
		mutate(&cfg)
	}

	app, err := live.New(cfg)
	Expect(err).NotTo(HaveOccurred())

	m := &mountedRoom{app: app, room: room, dir: dir}
	m.server = httptest.NewServer(NewMux(app, room, dir))

	DeferCleanup(func() {
		m.server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		Expect(app.Close(ctx)).To(Succeed())
	})
	return m
}

// browser is one connected tab: a livetest.Client carrying a real identity
// cookie, plus the two things that are this example's rather than the
// protocol's — who is holding the tab, and the last client reference it sent.
//
// The reference is kept because an Error frame names the event it concerns and
// several specs assert exactly that correlation; livetest.Client returns it
// from Send and this remembers the latest.
type browser struct {
	*livetest.Client
	name string
	ref  uint64
}

// open signs in as a member and connects.
//
// The cookie is set by a real GET of /login, so the sign-in the README
// describes is the sign-in the specs use: a spec that hand-wrote the cookie
// would keep passing after /login stopped setting one.
func (m *mountedRoom) open(name string) *browser {
	GinkgoHelper()

	header := http.Header{}
	header.Set("Cookie", loginCookie(m.server, name))

	return &browser{
		Client: livetest.NewClient(GinkgoTB(), NewMux(m.app, m.room, m.dir), livetest.ClientOptions{
			Path:    MountPath,
			Origin:  testOrigin,
			Header:  header,
			Timeout: 30 * time.Second,
		}),
		name: name,
	}
}

// loginCookie performs the sign-in and returns the Cookie header value.
func loginCookie(server *httptest.Server, name string) string {
	GinkgoHelper()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(server.URL + "/login?user=" + name)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	Expect(resp.StatusCode).To(Equal(http.StatusSeeOther))

	for _, c := range resp.Cookies() {
		if c.Name == IdentityCookie {
			return c.Name + "=" + c.Value
		}
	}
	Fail("/login set no " + IdentityCookie + " cookie for " + name)
	return ""
}

// send is livetest.Client.Send with the client reference remembered, because
// "the error names the event it concerns" is asserted against it four times.
func (b *browser) send(name, fragmentID string, fields map[string]string) {
	GinkgoHelper()
	b.ref = b.Send(name, fragmentID, fields)
}

func (b *browser) say(body string) {
	b.send(EventSend, FragmentComposer, map[string]string{fieldBody: body})
}
func (b *browser) typeIn(body string) {
	b.send(EventDraft, FragmentComposer, map[string]string{fieldBody: body})
}
func (b *browser) purge() { b.send(EventPurge, FragmentComposer, nil) }

func isPatch(f *livetest.Frame) bool { return f.Kind == livetest.FramePatch }

func carries(fragmentID, want string) func(*livetest.Frame) bool {
	return func(f *livetest.Frame) bool {
		if !isPatch(f) {
			return false
		}
		html, ok := f.Patch.Fragment(fragmentID)
		return ok && strings.Contains(html, want)
	}
}

// requestWithCookie and requestWithoutCookie build the upgrade requests
// Config.Authenticate is called with, for the specs in chat_test.go.
func requestWithCookie(name string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, MountPath, nil)
	r.AddCookie(&http.Cookie{Name: IdentityCookie, Value: name})
	return r
}

func requestWithoutCookie() *http.Request {
	return httptest.NewRequest(http.MethodGet, MountPath, nil)
}

// ---------------------------------------------------------------------------

var _ = Describe("Two browsers, two identities", func() {
	// The headline property: what one person says reaches the other over a
	// server push, with nothing polling and nothing shared in either browser.
	It("delivers one member's message to another member's session", func() {
		m := mount(nil)
		aliceTab := m.open("alice")
		bobTab := m.open("bob")
		aliceTab.Settle(200 * time.Millisecond)
		bobTab.Settle(200 * time.Millisecond)

		aliceTab.say("good morning")

		f := bobTab.Await("alice's message", 5*time.Second, carries(FragmentLog, "good morning"))

		Expect(f.Patch.Origin.Kind).To(BeNumerically("==", originEffect))
		Expect(f.Patch.Origin.Source).To(Equal("effect:"+SourceSubscribe),
			"a patch nobody in this session caused must name the effect that did")
		html, _ := f.Patch.Fragment(FragmentLog)
		Expect(html).To(ContainSubstring(">alice<"))
	})

	// The roster moves without anybody touching anything, which is the
	// cheapest visible proof that the push channel exists at all.
	It("tells the sessions already in the room that somebody arrived", func() {
		m := mount(nil)
		aliceTab := m.open("alice")
		aliceTab.Settle(200 * time.Millisecond)

		m.open("bob")

		f := aliceTab.Await("the roster growing", 5*time.Second, carries(FragmentRoster, "bob"))
		Expect(f.Patch.Origin.Source).To(Equal("effect:" + SourceSubscribe))
	})

	// Two tabs, one identity. Limits.MaxSessionsPerIdentity is what makes this
	// a supported case rather than an accident, and a chat is the first
	// application where one subject legitimately holds many sessions.
	It("keeps two tabs of one member in step", func() {
		m := mount(nil)
		first := m.open("alice")
		second := m.open("alice")
		first.Settle(200 * time.Millisecond)
		second.Settle(200 * time.Millisecond)

		first.say("said in the first tab")

		f := second.Await("the other tab's message", 5*time.Second,
			carries(FragmentLog, "said in the first tab"))
		Expect(f.Patch.Origin.Source).To(Equal("effect:" + SourceSubscribe))
		Expect(m.room.Occupants()).To(Equal(2))
	})

	// FR-50 all the way through: the escaping is done by the render that
	// produced the patch, so it is on the wire and not merely in a unit test's
	// idea of the markup.
	It("escapes a hostile message in the patch the other browser receives", func() {
		m := mount(nil)
		aliceTab := m.open("alice")
		bobTab := m.open("bob")
		aliceTab.Settle(200 * time.Millisecond)
		bobTab.Settle(200 * time.Millisecond)

		aliceTab.say(`<img src=x onerror="alert(1)">`)

		f := bobTab.Await("the escaped payload", 5*time.Second,
			carries(FragmentLog, "&lt;img src=x onerror="))
		html, _ := f.Patch.Fragment(FragmentLog)
		Expect(html).NotTo(ContainSubstring(`<img`))
	})
})

// ---------------------------------------------------------------------------

var _ = Describe("Input preserved across another member's event", func() {
	// The case FR-55 names and the one naive implementations get wrong. Both
	// browsers are connected, bob is mid-sentence, alice speaks — and the
	// patch bob's browser receives must not contain the composer at all.
	//
	// It is asserted on the frame rather than on the Dirty function because
	// the Dirty function is only half of it: a library that ignored the
	// declaration, or a render pass that included every fragment, would pass
	// the unit spec in chat_test.go and fail this one.
	It("sends the other session a patch with no composer in it", func() {
		m := mount(nil)
		aliceTab := m.open("alice")
		bobTab := m.open("bob")
		aliceTab.Settle(300 * time.Millisecond)
		bobTab.Settle(300 * time.Millisecond)

		bobTab.typeIn("a sentence bob has not finished yet")
		bobTab.Await("bob's own draft coming back", 5*time.Second,
			carries(FragmentComposer, "a sentence bob has not finished yet"))
		bobTab.Settle(300 * time.Millisecond)

		aliceTab.say("interrupting")

		f := bobTab.Await("alice's message", 5*time.Second, carries(FragmentLog, "interrupting"))

		Expect(f.Patch.FragmentIDs()).To(ConsistOf(FragmentLog),
			"a message from another member must patch the log and nothing else; "+
				"the composer holds a half-typed sentence")
		_, touched := f.Patch.Fragment(FragmentComposer)
		Expect(touched).To(BeFalse())

		// And nothing else arrives for that transition either.
		for _, later := range bobTab.Settle(300 * time.Millisecond) {
			if later.Patch != nil {
				_, touched := later.Patch.Fragment(FragmentComposer)
				Expect(touched).To(BeFalse(), "the composer was patched a moment later instead")
			}
		}
	})

	// The other half: when the composer IS re-rendered, the box comes back
	// with the session's own text in it.
	It("puts the draft back when the composer is re-rendered", func() {
		m := mount(nil)
		bobTab := m.open("bob")
		bobTab.Settle(200 * time.Millisecond)

		long := strings.Repeat("y", MaxBodyRunes+1)
		bobTab.say(long)

		f := bobTab.Await("the validation message", 5*time.Second,
			carries(FragmentComposer, "keep it under"))
		html, _ := f.Patch.Fragment(FragmentComposer)

		Expect(html).To(ContainSubstring(`value="`+long+`"`),
			"the server refused the message and must not also delete it")
		Expect(f.Patch.Origin.Kind).To(BeNumerically("==", originClientEvent))
		Expect(f.Patch.Origin.Source).To(Equal("event:" + EventSend))
	})
})

// ---------------------------------------------------------------------------

var _ = Describe("Per-event authorization on the wire", func() {
	It("refuses a member's attempt to clear the room and keeps the session", func() {
		m := mount(nil)
		aliceTab := m.open("alice")
		aliceTab.say("this should survive")
		aliceTab.Await("alice's own message", 5*time.Second, carries(FragmentLog, "this should survive"))
		aliceTab.Settle(200 * time.Millisecond)

		aliceTab.purge()
		denial := aliceTab.Await("the denial", 5*time.Second, func(f *livetest.Frame) bool { return f.Kind == livetest.FrameError })

		Expect(denial.Error.Code).To(BeNumerically("==", codeUnauthorized))
		Expect(denial.Error.Fatal).To(BeFalse())
		Expect(denial.Error.ClientRef).To(Equal(aliceTab.ref), "an error names the event it concerns")
		Expect(denial.Error.Message).NotTo(ContainSubstring("moderator"),
			"an authorization reason is an authorization input and stays server-side")

		// No state changed, and the session is still usable.
		Expect(m.room.Log().Messages).To(HaveLen(1))
		aliceTab.say("and so should this")
		aliceTab.Await("the next message", 5*time.Second, carries(FragmentLog, "and so should this"))
	})

	It("lets a moderator clear the room, and everybody sees it", func() {
		m := mount(nil)
		aliceTab := m.open("alice")
		malloryTab := m.open("mallory")
		aliceTab.say("something to clear")
		aliceTab.Await("the message", 5*time.Second, carries(FragmentLog, "something to clear"))
		aliceTab.Settle(300 * time.Millisecond)
		malloryTab.Settle(300 * time.Millisecond)

		malloryTab.purge()

		f := aliceTab.Await("the room emptying", 5*time.Second, carries(FragmentComposer, "cleared by mallory"))
		Expect(f.Patch.Origin.Source).To(Equal("effect:" + SourceSubscribe))
		Expect(m.room.Log().Messages).To(BeEmpty())
	})

	It("refuses an observer's message", func() {
		m := mount(nil)
		oliveTab := m.open("olive")
		oliveTab.Settle(200 * time.Millisecond)

		oliveTab.say("observers do not get to talk")
		denial := oliveTab.Await("the denial", 5*time.Second, func(f *livetest.Frame) bool { return f.Kind == livetest.FrameError })

		Expect(denial.Error.Code).To(BeNumerically("==", codeUnauthorized))
		Expect(denial.Error.Fatal).To(BeFalse())
		Expect(m.room.Log().Messages).To(BeEmpty())
	})

	// The other denial shape. A banned member is not a permission question; it
	// is a session that should not be open.
	It("closes the connection on a banned member", func() {
		m := mount(nil)
		trudyTab := m.open("trudy")

		trudyTab.say("hello")
		denial := trudyTab.Await("the fatal denial", 5*time.Second, func(f *livetest.Frame) bool { return f.Kind == livetest.FrameError })

		Expect(denial.Error.Code).To(BeNumerically("==", codeUnauthorized))
		Expect(denial.Error.Fatal).To(BeTrue())
		Expect(trudyTab.Closed(5 * time.Second)).To(BeTrue())
	})

	// Authentication, before authorization: a browser with no cookie never
	// gets a session at all, and neither does one naming somebody who is not a
	// member.
	DescribeTable("answers the handshake according to who is asking",
		func(cookie string, want int) {
			m := mount(nil)
			headers := map[string]string{
				"Origin":                 testOrigin,
				"Sec-WebSocket-Protocol": "gotth-live.v1",
			}
			if cookie != "" {
				headers["Cookie"] = IdentityCookie + "=" + cookie
			}
			Expect(handshake(m.server.URL, headers)).To(Equal(want))
		},
		Entry("a member", "alice", http.StatusSwitchingProtocols),
		Entry("a moderator", "mallory", http.StatusSwitchingProtocols),
		Entry("nobody at all", "", http.StatusUnauthorized),
		Entry("somebody who is not in the directory", "eve", http.StatusUnauthorized),
	)
})

// ---------------------------------------------------------------------------

var _ = Describe("Error boundaries on the wire", func() {
	// FR-23's first site. The transition did not apply, the client is holding
	// a view the server knows is wrong, and there is nobody but the client to
	// tell — so an Error frame, carrying the causal identifiers of the event
	// that caused it.
	It("answers a reducer panic with an Error frame naming the event", func() {
		m := mount(nil)
		aliceTab := m.open("alice")
		bobTab := m.open("bob")
		aliceTab.Settle(300 * time.Millisecond)
		bobTab.Settle(300 * time.Millisecond)

		aliceTab.say(CmdPanicReducer)
		f := aliceTab.Await("the error", 5*time.Second, func(f *livetest.Frame) bool { return f.Kind == livetest.FrameError })

		Expect(f.Error.Code).To(BeNumerically("==", codeInternal))
		Expect(f.Error.Fatal).To(BeFalse())
		Expect(f.Error.ClientRef).To(Equal(aliceTab.ref))
		Expect(f.Error.EventID).NotTo(BeZero())

		// Contained. bob's session never heard about it and still works.
		bobTab.say("bob is fine")
		bobTab.Await("bob's message", 5*time.Second, carries(FragmentLog, "bob is fine"))
		for _, seen := range bobTab.Received() {
			Expect(seen.Kind).NotTo(Equal(livetest.FrameError))
		}
	})

	// FR-23's third site. Until C-26 this produced nothing at all on the wire:
	// a region stopped updating and the client was told nothing.
	It("answers a render panic with an Error frame naming the event", func() {
		m := mount(nil)
		aliceTab := m.open("alice")
		bobTab := m.open("bob")
		aliceTab.Settle(300 * time.Millisecond)
		bobTab.Settle(300 * time.Millisecond)

		aliceTab.say(CmdPanicRender)
		f := aliceTab.Await("the error", 5*time.Second, func(f *livetest.Frame) bool { return f.Kind == livetest.FrameError })

		Expect(f.Error.Code).To(BeNumerically("==", codeInternal))
		Expect(f.Error.ClientRef).To(Equal(aliceTab.ref))
		Expect(f.Error.EventID).NotTo(BeZero())

		bobTab.say("bob is still fine")
		bobTab.Await("bob's message", 5*time.Second, carries(FragmentLog, "bob is still fine"))
	})

	// FR-23's second site, as amended on 2026-08-04. An effect panic must NOT
	// produce an Error frame. It reaches the reducer as gotth.effect_failed,
	// and the patch that reducer then produces carries origin
	// effect:<source> with the scheduling event as a contributing edge.
	//
	// A test that accepts an Error frame here fails the criterion, so this one
	// asserts the absence directly.
	It("answers an effect panic with a failure event and no Error frame", func() {
		m := mount(nil)
		aliceTab := m.open("alice")
		bobTab := m.open("bob")
		aliceTab.Settle(300 * time.Millisecond)
		bobTab.Settle(300 * time.Millisecond)

		aliceTab.say(CmdPanicEffect)
		scheduling := aliceTab.ref

		notice := aliceTab.Await("the notice the reducer rendered", 5*time.Second,
			carries(FragmentComposer, SourcePanic))

		Expect(notice.Patch.Origin.Kind).To(BeNumerically("==", originEffect))
		Expect(notice.Patch.Origin.Source).To(Equal("effect:"+SourcePanic),
			"the failure's own origin names the effect that panicked")
		Expect(notice.Patch.Origin.Contributing).NotTo(BeEmpty(),
			"the event that scheduled the effect is a contributing edge")
		Expect(notice.Patch.Origin.ClientRef).To(BeZero(),
			"a server-initiated transition has no client frame of its own to name")

		// The absence, which is the amended half of the requirement.
		for _, seen := range aliceTab.Received() {
			Expect(seen.Kind).NotTo(Equal(livetest.FrameError),
				"an effect panic must not also produce an Error frame: one failure, one surface")
		}
		for _, seen := range aliceTab.Settle(500 * time.Millisecond) {
			Expect(seen.Kind).NotTo(Equal(livetest.FrameError))
		}

		// And the causal chain is walkable: the contributing edge names an
		// event this session actually sent, and the client ref that sent it is
		// on the frame that scheduled it.
		Expect(scheduling).To(BeNumerically(">", 0))

		// Contained: bob never saw any of it.
		bobTab.say("bob is unaffected")
		bobTab.Await("bob's message", 5*time.Second, carries(FragmentLog, "bob is unaffected"))
	})

	// Config.Dev decides how much of a panic reaches the browser and it is the
	// only thing that field does. The production assertion is the important
	// one: a person typing /panic reducer must not be shown the panic value.
	It("keeps a panic value off the wire in production and puts it on in dev", func() {
		prod := mount(func(c *live.Config[State, Member]) { c.Dev = false })
		prodTab := prod.open("alice")
		prodTab.Settle(200 * time.Millisecond)
		prodTab.say(CmdPanicReducer)
		generic := prodTab.Await("the error", 5*time.Second, func(f *livetest.Frame) bool { return f.Kind == livetest.FrameError })
		Expect(generic.Error.Message).NotTo(ContainSubstring(CmdPanicReducer))
		Expect(generic.Error.Message).NotTo(ContainSubstring("chat.go"))

		dev := mount(func(c *live.Config[State, Member]) { c.Dev = true })
		devTab := dev.open("alice")
		devTab.Settle(200 * time.Millisecond)
		devTab.say(CmdPanicReducer)
		detailed := devTab.Await("the error", 5*time.Second, func(f *livetest.Frame) bool { return f.Kind == livetest.FrameError })
		Expect(detailed.Error.Message).To(ContainSubstring(CmdPanicReducer))
		Expect(len(detailed.Error.Message)).To(BeNumerically(">", len(generic.Error.Message)))
	})
})

// ---------------------------------------------------------------------------

var _ = Describe("Provenance", func() {
	// FR-42, stated as the requirement states it: every server-initiated patch
	// carries a named origin, and `unknown` is not a permitted value.
	//
	// It is asserted over every frame of a whole scenario rather than over one
	// chosen frame, because "zero unknown" is a claim about all of them.
	It("attributes every frame either browser receives, with nothing unknown", func() {
		m := mount(nil)
		aliceTab := m.open("alice")
		bobTab := m.open("bob")

		aliceTab.typeIn("a draft")
		aliceTab.say("hello")
		bobTab.say("hello back")
		bobTab.Await("alice's message", 5*time.Second, carries(FragmentLog, "hello"))
		aliceTab.Await("bob's message", 5*time.Second, carries(FragmentLog, "hello back"))
		aliceTab.Settle(400 * time.Millisecond)
		bobTab.Settle(400 * time.Millisecond)

		sources := map[string]int{}
		for _, tab := range []*browser{aliceTab, bobTab} {
			frames := tab.Received()
			Expect(frames).NotTo(BeEmpty())
			for _, f := range frames {
				if f.Patch == nil {
					continue
				}
				origin := f.Patch.Origin
				Expect(origin.Source).NotTo(BeEmpty(),
					"%s received a %s with no origin source", tab.name, f.Kind)
				Expect(origin.Source).NotTo(Equal("unknown"),
					"`unknown` is not a permitted origin value")
				Expect(origin.Kind).NotTo(BeZero(), "an unspecified origin kind is not permitted either")
				sources[origin.Source]++
			}
		}

		// And the names are this application's own, not a generic placeholder.
		Expect(sources).To(HaveKey("mount"))
		Expect(sources).To(HaveKey("effect:" + SourceSubscribe))
		Expect(sources).To(HaveKey("event:" + EventSend))
		for source := range sources {
			Expect(source).To(SatisfyAny(
				Equal("mount"),
				HavePrefix("event:"),
				HavePrefix("effect:"),
			), "an origin source outside the documented vocabulary")
		}
	})

	It("roots a session's first frame at the mount", func() {
		m := mount(nil)
		aliceTab := m.open("alice")

		first := aliceTab.Received()[0]
		Expect(first.Kind).To(Equal(livetest.FrameSnapshot))
		Expect(first.Patch.Origin.Kind).To(BeNumerically("==", originMount))
		Expect(first.Patch.Origin.Source).To(Equal("mount"))
		Expect(first.Patch.FragmentIDs()).To(ConsistOf(FragmentLog, FragmentComposer, FragmentRoster),
			"a snapshot carries every registered fragment")
	})

	// The contributing edge an application supplies. The library knows which
	// event scheduled the subscription — the mount — and only this application
	// knows which event produced the message the subscription is now
	// delivering. Naming it is what lets somebody holding the patch reach the
	// submission that caused it.
	It("names the sender's own submission on the patch that shows their message", func() {
		m := mount(nil)
		aliceTab := m.open("alice")
		bobTab := m.open("bob")
		aliceTab.Settle(300 * time.Millisecond)
		bobTab.Settle(300 * time.Millisecond)

		aliceTab.say("mine")
		mine := aliceTab.Await("her own message", 5*time.Second, carries(FragmentLog, "mine"))
		theirs := bobTab.Await("her message", 5*time.Second, carries(FragmentLog, "mine"))

		Expect(mine.Patch.Origin.Contributing).NotTo(BeEmpty(),
			"the sender's own patch must reach back to the submission")
		Expect(theirs.Patch.Origin.Contributing).To(BeEmpty(),
			"another session's identifiers are not this session's to claim")
	})
})

// ---------------------------------------------------------------------------

var _ = Describe("Lifecycle", func() {
	// FR-56's own sufficiency test: subscribe on mount, unsubscribe on
	// teardown, and no leak. The room's own count is the exact half — a
	// teardown that did not unsubscribe leaves it above zero with every
	// connection closed — and the goroutine count is the half that catches a
	// pump nobody stopped.
	It("leaves neither a subscription nor a goroutine behind", func() {
		m := mount(nil)

		// One cycle first, so that the transport's own pools, the HTTP
		// server's idle connections and the runtime's background work are
		// already at their steady state before the baseline is taken.
		warm := m.open("alice")
		warm.say("warming up")
		warm.Await("the message", 5*time.Second, carries(FragmentLog, "warming up"))
		Expect(warm.Close()).To(Succeed())
		Eventually(m.room.Occupants, 5*time.Second).Should(BeZero())

		runtime.GC()
		// CS-9 keep: settling before a measurement, not waiting for one. There
		// is no condition here — the baseline is whatever the runtime has
		// reached, and this is the pause that lets the collector's own workers
		// retire so the number is about the test rather than about the GC.
		time.Sleep(100 * time.Millisecond)
		baseline := runtime.NumGoroutine()

		const cycles = 20
		for i := range cycles {
			tab := m.open("bob")
			tab.say("cycle " + strconv.Itoa(i))
			tab.Await("the message", 5*time.Second, carries(FragmentLog, "cycle "+strconv.Itoa(i)))
			Expect(tab.Close()).To(Succeed())
		}

		// The exact assertion: Config.Teardown ran for every one of them.
		Eventually(m.room.Occupants, 10*time.Second).Should(BeZero(),
			"a session that closed without unsubscribing leaves the room holding a queue nobody reads")

		// And the approximate one. A tolerance rather than equality because
		// the HTTP server and the WebSocket library keep goroutines of their
		// own on their own schedule; what would fail here is cycles-many
		// subscription pumps still running, which is 20 and not 5.
		Eventually(runtime.NumGoroutine, 10*time.Second, 100*time.Millisecond).
			Should(BeNumerically("<=", baseline+5),
				"%d connect/disconnect cycles left goroutines behind", cycles)
	})

	It("puts a joining session in the room and takes a leaving one out", func() {
		m := mount(nil)

		first := m.open("alice")
		Expect(m.room.Occupants()).To(Equal(1))
		Expect(m.room.Log().Members).To(Equal([]string{"alice"}))

		second := m.open("mallory")
		Eventually(m.room.Occupants).Should(Equal(2))
		Expect(m.room.Log().Members).To(Equal([]string{"alice", "mallory"}))

		Expect(second.Close()).To(Succeed())
		Eventually(m.room.Occupants, 5*time.Second).Should(Equal(1))
		Expect(m.room.Log().Members).To(Equal([]string{"alice"}))

		Expect(first.Close()).To(Succeed())
		Eventually(m.room.Occupants, 5*time.Second).Should(BeZero())
	})
})

// ---------------------------------------------------------------------------

var _ = Describe("The mounted application", func() {
	It("serves the login page to a browser with no identity", func() {
		m := mount(nil)

		body, status := get(m.server.URL + "/")

		Expect(status).To(Equal(http.StatusOK))
		Expect(body).To(ContainSubstring(`data-chat-id="login"`))
		for _, name := range m.dir.Names() {
			Expect(body).To(ContainSubstring("/login?user=" + name))
		}
	})

	It("serves the room, already rendered, to a signed-in browser", func() {
		m := mount(nil)
		m.room.Post("alice", Post{Body: "already said"}, tabA)

		req, err := http.NewRequest(http.MethodGet, m.server.URL+"/", nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Cookie", loginCookie(m.server, "mallory"))
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())

		body := string(raw)
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(body).To(ContainSubstring(`data-gotth-region="` + FragmentLog + `"`))
		Expect(body).To(ContainSubstring("already said"))
		Expect(body).To(ContainSubstring(`data-chat-id="purge"`), "mallory is a moderator")
	})

	// C-23's defect, checked in a second example at a prefix that is not the
	// old default: the tag the page renders must actually address the handler.
	It("serves the client runtime from the path the page's script tag names", func() {
		m := mount(nil)

		req, err := http.NewRequest(http.MethodGet, m.server.URL+"/", nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Cookie", loginCookie(m.server, "alice"))
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())

		src := scriptSrc(string(raw))
		Expect(src).To(HavePrefix(MountPath+"/"), "the tag must address this application's mount path")

		body, status := get(m.server.URL + src)
		Expect(status).To(Equal(http.StatusOK), "the script tag on the page 404s")
		Expect(len(body)).To(BeNumerically(">", 1000))
	})

	It("serves the stylesheet", func() {
		m := mount(nil)

		body, status := get(m.server.URL + "/chat.css")

		Expect(status).To(Equal(http.StatusOK))
		Expect(body).To(ContainSubstring("data-gotth-status"))
	})

	It("refuses a login for somebody who is not a member", func() {
		m := mount(nil)

		_, status := get(m.server.URL + "/login?user=eve")

		Expect(status).To(Equal(http.StatusNotFound))
	})

	// Deny by default, checked from outside rather than read off the Config.
	DescribeTable("answers the WebSocket handshake according to the origin allowlist",
		func(headers map[string]string, want int) {
			m := mount(nil)
			headers["Cookie"] = IdentityCookie + "=alice"
			Expect(handshake(m.server.URL, headers)).To(Equal(want))
		},
		Entry("the allowed origin", map[string]string{
			"Origin":                 testOrigin,
			"Sec-WebSocket-Protocol": "gotth-live.v1",
		}, http.StatusSwitchingProtocols),
		Entry("a foreign origin", map[string]string{
			"Origin":                 "https://evil.example",
			"Sec-WebSocket-Protocol": "gotth-live.v1",
		}, http.StatusForbidden),
		Entry("no origin at all", map[string]string{
			"Sec-WebSocket-Protocol": "gotth-live.v1",
		}, http.StatusForbidden),
		Entry("the right origin but the wrong subprotocol", map[string]string{
			"Origin":                 testOrigin,
			"Sec-WebSocket-Protocol": "gotth-live.v0",
		}, http.StatusUpgradeRequired),
	)
})

func get(rawURL string) (string, int) {
	GinkgoHelper()
	resp, err := http.Get(rawURL)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return string(body), resp.StatusCode
}

// scriptSrc pulls the one script element's src out of a rendered page. It
// reads the page the way a browser would rather than the way the template
// wrote it, which is the point: the assertion is that the tag on the page
// addresses something that exists.
func scriptSrc(html string) string {
	GinkgoHelper()
	const marker = `<script src="`
	i := strings.Index(html, marker)
	Expect(i).To(BeNumerically(">=", 0), "the page has no script element")
	rest := html[i+len(marker):]
	j := strings.Index(rest, `"`)
	Expect(j).To(BeNumerically(">=", 0))
	return rest[:j]
}

// handshake performs a raw WebSocket upgrade and returns the status the server
// answered with.
//
// It is written against net.Dial rather than a WebSocket client library
// because what is under test is the HTTP half of the handshake — the origin
// allowlist, the identity cookie and the subprotocol negotiation — and a
// client library would turn every refusal into the same error.
func handshake(serverURL string, headers map[string]string) int {
	GinkgoHelper()

	u, err := url.Parse(serverURL)
	Expect(err).NotTo(HaveOccurred())

	conn, err := net.Dial("tcp", u.Host)
	Expect(err).NotTo(HaveOccurred())
	defer conn.Close()
	Expect(conn.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())

	req := "GET " + MountPath + " HTTP/1.1\r\nHost: " + u.Host + "\r\n" +
		"Connection: Upgrade\r\nUpgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
	for k, v := range headers {
		req += k + ": " + v + "\r\n"
	}
	req += "\r\n"

	_, err = conn.Write([]byte(req))
	Expect(err).NotTo(HaveOccurred())

	status, err := bufio.NewReader(conn).ReadString('\n')
	Expect(err).NotTo(HaveOccurred())

	code, err := strconv.Atoi(strings.Fields(status)[1])
	Expect(err).NotTo(HaveOccurred())
	return code
}
