package live_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The exported effect-failure vocabulary is spelled twice: once in live, where
// an application can reach it, and once in internal/session, which is what
// actually mints the event. Two spellings of one string is a defect waiting for
// its first refactor, so the agreement is held here rather than hoped for.
//
// Aliasing live's constants to the internal ones would make drift impossible
// but would print `EffectFailedEvent = session.EffectFailedEvent` in the
// godoc of a package whose readers cannot see that package. The literal is for
// them; this spec is for the drift.
var _ = Describe("The effect-failure vocabulary", func() {
	It("says the same thing in both packages", func() {
		Expect(live.EffectFailedEvent).To(Equal(session.EffectFailedEvent))
		Expect(live.EffectFailedSourceField).To(Equal(session.EffectFailedSourceField))
		Expect(live.EffectFailedErrorField).To(Equal(session.EffectFailedErrorField))
		Expect(live.EffectFailedRetryableField).To(Equal(session.EffectFailedRetryableField))
	})
})

func TestLive(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Live Suite")
}

type counter struct {
	N     int
	Label string
}

type user string

func (u user) Subject() string { return string(u) }

// logEffect is the effect these specs schedule. The source is fixed, because
// several of them assert on the origin "effect:test.log"; the behaviour is
// whatever the spec hands it, which is exactly the shape an application's own
// effect constructor has since live.Effect[user] became a concrete struct.
func logEffect(run func(ctx context.Context, session live.Session[user], emit live.Emitter) error) live.Effect[user] {
	return live.Effect[user]{Source: "test.log", Run: run}
}

func text(format string, args ...any) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := fmt.Fprintf(w, format, args...)
		return err
	})
}

func validConfig() live.Config[counter, user] {
	return live.Config[counter, user]{
		Init: func(ctx context.Context, session live.Session[user]) (counter, []live.Effect[user], error) {
			return counter{Label: "hits"}, nil, nil
		},
		Reduce: func(state counter, ev live.Event) (counter, []live.Effect[user]) {
			switch ev.Name {
			case "counter.increment":
				state.N++
			case "counter.relabel":
				state.Label = ev.Fields.Get("label")
			}
			return state, nil
		},
		Fragments: []live.Fragment[counter]{{
			ID:     "counter",
			Render: func(s counter) templ.Component { return text("<b>%s %d</b>", s.Label, s.N) },
			Dirty:  func(prev, next counter) bool { return prev != next },
		}},
		Events:       []string{"counter.increment", "counter.relabel"},
		Origins:      []string{"https://app.example"},
		Authenticate: func(request *http.Request) (user, error) { return user("tester"), nil },
		Authorize:    live.AllowAll[user],
		CSRF:         live.NoCSRFCheck,
	}
}

var _ = Describe("New", func() {
	// The zero value is invalid and the error says which field and what to set
	// it to, because every one of these is a startup mistake and finding it at
	// startup is the difference between a failed deploy and a session that
	// misbehaves in production.
	DescribeTable("refuses an incomplete configuration, naming the field",
		func(field string, break_ func(cfg *live.Config[counter, user])) {
			cfg := validConfig()
			break_(&cfg)

			_, err := live.New(cfg)

			var cfgErr *live.ConfigError
			Expect(errors.As(err, &cfgErr)).To(BeTrue(), "got %v", err)
			Expect(cfgErr.Field).To(Equal(field))
			Expect(cfgErr.Detail).NotTo(BeEmpty())
			Expect(cfgErr.Error()).To(ContainSubstring("gotth-live: Config." + field))
		},
		// Init is deliberately not an entry: it is the one optional field of
		// the eight, and the spec directly below is what holds the default it
		// takes instead.
		Entry("no reducer", "Reduce", func(c *live.Config[counter, user]) { c.Reduce = nil }),
		Entry("no fragments", "Fragments", func(c *live.Config[counter, user]) { c.Fragments = nil }),
		Entry("no events", "Events", func(c *live.Config[counter, user]) { c.Events = nil }),
		Entry("no origins", "Origins", func(c *live.Config[counter, user]) { c.Origins = nil }),
		Entry("no authentication", "Authenticate", func(c *live.Config[counter, user]) { c.Authenticate = nil }),
		Entry("no authorization", "Authorize", func(c *live.Config[counter, user]) { c.Authorize = nil }),
		Entry("no CSRF hook", "CSRF", func(c *live.Config[counter, user]) { c.CSRF = nil }),
	)

	// Config.Init is the one field of the eight New fills in rather than
	// refusing, and this is the whole of what the default may be: the zero
	// value of S, no startup effects, and no error. It is asserted against a
	// state type whose zero value is distinguishable from validConfig's — that
	// one starts at Label "hits" — so a default that quietly kept the
	// application's own hook would fail here rather than pass.
	It("accepts a Config with no mount hook, and starts the session at the zero state", func() {
		cfg := validConfig()
		cfg.Init = nil

		app, err := live.New(cfg)

		Expect(err).NotTo(HaveOccurred())
		Expect(app).NotTo(BeNil())

		// Read back through the one route that renders Config.Init's state on
		// this side of a WebSocket. The fragment prints the label and the
		// count, so the zero state is observable as bytes rather than asserted
		// against an internal field.
		rec := httptest.NewRecorder()
		app.PageHandler(func(s counter) templ.Component {
			return text("<i>%q %d</i>", s.Label, s.N)
		}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(Equal(`<i>"" 0</i>`))
	})

	It("refuses a duplicate fragment identifier, naming both declarations", func() {
		cfg := validConfig()
		cfg.Fragments = append(cfg.Fragments, cfg.Fragments[0])

		_, err := live.New(cfg)

		var cfgErr *live.ConfigError
		Expect(errors.As(err, &cfgErr)).To(BeTrue())
		Expect(cfgErr.Field).To(Equal("Fragments"))
		Expect(cfgErr.Detail).To(ContainSubstring("fragments[0] and fragments[1]"))
	})

	It("refuses a fragment identifier the schema could not carry", func() {
		cfg := validConfig()
		cfg.Fragments[0].ID = "not a fragment id"

		_, err := live.New(cfg)

		Expect(err).To(HaveOccurred())
	})

	// BR-2. An event name is one half of the Origin.source of every patch that
	// event causes, and Origin.source is refined on the outbound boundary. A
	// name of fifty-nine bytes composes to sixty-five and every one of those
	// patches was unsendable: the state change never reached the browser, the
	// client received an Error{INTERNAL} it could not act on, and
	// gotthlive_outbound_validation_failed_total — documented as never a client
	// problem — was incremented by ordinary client traffic against an ordinary
	// registration. A registration mistake belongs at startup.
	DescribeTable("refuses an event name that cannot name the origin of its own patch",
		func(name string) {
			cfg := validConfig()
			cfg.Events = append(cfg.Events, name)

			_, err := live.New(cfg)

			var cfgErr *live.ConfigError
			Expect(errors.As(err, &cfgErr)).To(BeTrue(), "got %v", err)
			Expect(cfgErr.Field).To(Equal("Events"))
			Expect(cfgErr.Detail).To(ContainSubstring(name))
		},
		Entry("one byte past what the prefix leaves", "a"+strings.Repeat("b", 58)), // 59 bytes
		Entry("far past it", strings.Repeat("c", 64)),
		Entry("upper case", "Counter.Increment"),
		Entry("a space", "counter increment"),
	)

	It("accepts an event name exactly at the bound the prefix leaves", func() {
		cfg := validConfig()
		cfg.Events = append(cfg.Events, "a"+strings.Repeat("b", 57))

		app, err := live.New(cfg)

		Expect(err).NotTo(HaveOccurred())
		Expect(app.Close(context.Background())).To(Succeed())
	})

	It("accepts a complete configuration", func() {
		app, err := live.New(validConfig())

		Expect(err).NotTo(HaveOccurred())
		Expect(app.Handler()).NotTo(BeNil())
		Expect(app.Close(context.Background())).To(Succeed())
	})

	It("accepts the named escape hatches in place of real hooks", func() {
		cfg := validConfig()
		cfg.Origins = []string{live.AnyOrigin}
		cfg.Authenticate = func(request *http.Request) (user, error) { return user("tester"), nil }
		cfg.Authorize = live.AllowAll[user]
		cfg.CSRF = live.NoCSRFCheck

		app, err := live.New(cfg)

		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Close(context.Background())).To(Succeed()) })

		identity, err := live.Anonymous(nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(identity.Subject()).To(Equal("anonymous"))
	})
})

var _ = Describe("Limits", func() {
	It("reports its documented defaults", func() {
		d := live.DefaultLimits()

		Expect(d.MaxInboundFrameBytes).To(Equal(65536))
		Expect(d.MaxEventsPerSecond).To(Equal(50.0))
		Expect(d.EventBurst).To(Equal(100))
		Expect(d.MailboxDepth).To(Equal(64))
		Expect(d.AckChannelDepth).To(Equal(32))
		Expect(d.AckWindow).To(Equal(16))
		Expect(d.CoalesceFlushAt).To(Equal(512))
		Expect(d.MinResyncInterval).To(Equal(time.Second))
		Expect(d.ResyncBurst).To(Equal(3))
		Expect(d.WriteDeadline).To(Equal(5 * time.Second))
		Expect(d.SlowClientGrace).To(Equal(30 * time.Second))
		Expect(d.HeartbeatInterval).To(Equal(20 * time.Second))
		Expect(d.HeartbeatTimeout).To(Equal(50 * time.Second))
		Expect(d.IdleTimeout).To(Equal(30 * time.Minute))
		Expect(d.EffectDrainTimeout).To(Equal(5 * time.Second))
		Expect(d.MaxSessionsPerIdentity).To(Equal(20))
		Expect(d.MaxSessions).To(BeZero(), "the process limit defaults to unlimited, and the docs say to set it")
		Expect(d.PanicBudget).To(Equal(3))
	})
})

var _ = Describe("Fields", func() {
	// The distinction between an absent key and an empty value is the one that
	// matters in practice: an unchecked checkbox sends nothing at all.
	It("distinguishes an absent key from an empty value", func() {
		app := mount(func(c *live.Config[counter, user]) {
			c.Reduce = func(state counter, ev live.Event) (counter, []live.Effect[user]) {
				if _, ok := ev.Fields.Lookup("checked"); ok {
					state.Label = "present"
				} else {
					state.Label = "absent"
				}
				return state, nil
			}
		})
		defer app.stop()

		app.send("counter.relabel", map[string]string{"checked": ""})
		Expect(app.nextPatch().GetUpdates()[0].GetHtml()).To(Equal("<b>present 0</b>"))

		app.send("counter.relabel", nil)
		Expect(app.nextPatch().GetUpdates()[0].GetHtml()).To(Equal("<b>absent 0</b>"))
	})

	It("iterates in wire order and stops when asked", func() {
		var seen []string
		app := mount(func(c *live.Config[counter, user]) {
			c.Reduce = func(state counter, ev live.Event) (counter, []live.Effect[user]) {
				seen = nil
				ev.Fields.All(func(k, v string) bool {
					seen = append(seen, k+"="+v)
					return len(seen) < 2
				})
				state.N++
				return state, nil
			}
		})
		defer app.stop()

		app.sendOrdered("counter.relabel", [][2]string{{"a", "1"}, {"b", "2"}, {"c", "3"}})
		app.nextPatch()

		Expect(seen).To(Equal([]string{"a=1", "b=2"}))
	})

	// NewFields is what lets an effect emit an event with a payload and a
	// determinism test build the log it replays. Its ordering is part of the
	// contract rather than an implementation detail: Fields is compared by
	// value inside ReplayN, so a copy that inherited Go's map order would make
	// a reducer fail a determinism check that had found nothing wrong.
	It("orders application-constructed fields by key", func() {
		fields := live.NewFields(map[string]string{"delta": "1", "by": "tab-a", "at": "17"})

		var seen []string
		fields.All(func(k, v string) bool {
			seen = append(seen, k+"="+v)
			return true
		})

		Expect(seen).To(Equal([]string{"at=17", "by=tab-a", "delta=1"}))
		Expect(fields.Len()).To(Equal(3))
		Expect(fields.Get("by")).To(Equal("tab-a"))
	})

	It("builds the same Fields from the same map, every time", func() {
		source := map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"}

		first := live.NewFields(source)
		for range 100 {
			Expect(live.NewFields(source)).To(Equal(first))
		}
	})

	It("treats an empty map and a nil map alike", func() {
		Expect(live.NewFields(nil).Len()).To(Equal(0))
		Expect(live.NewFields(map[string]string{}).Len()).To(Equal(0))
		Expect(live.NewFields(nil).Get("anything")).To(BeEmpty())
	})

	// The copy is the point: an application that mutates the map it passed in
	// must not be able to reach into an event the reducer already has.
	It("copies the map rather than aliasing it", func() {
		source := map[string]string{"value": "1"}
		fields := live.NewFields(source)

		source["value"] = "2"

		Expect(fields.Get("value")).To(Equal("1"))
	})
})

var _ = Describe("The templ helpers", func() {
	// The attribute spellings are a contract with the client runtime, and a
	// disagreement between the two is a silent no-op in the browser rather
	// than an error anywhere. That is why they are asserted literally.
	It("marks a live region", func() {
		Expect(live.Region("counter")).To(Equal(templ.Attributes{"data-gotth-region": "counter"}))
	})

	It("binds a DOM event to a server event", func() {
		Expect(live.On("click", "counter.increment")).To(Equal(
			templ.Attributes{"data-gotth-on": "click:counter.increment"}))
	})

	// Fields, Debounce and Throttle used to be asserted here as three separate
	// element attributes. They are components of the binding since FR-54
	// failure 2, and binding_test.go — same package, same suite — specifies the
	// whole grammar rather than one example of it: the component order, the
	// trimming, the key-list expansion, the percent-encoding that keeps a
	// field value from splitting the binding it sits in, and the two-bindings
	// case this file used to state the opposite of.

	It("omits the options that were not set", func() {
		attrs := live.OnWith("click", "counter.increment", live.Bind{})

		Expect(attrs).To(HaveLen(1))
		Expect(attrs).To(HaveKey("data-gotth-on"))
	})

	// The key filter is the third component of the binding and not an
	// attribute of its own, because an attribute belongs to the element and an
	// element carries several bindings: a filter beside them would apply to all
	// of them, including the ones raised by events that carry no key at all.
	It("filters a keyboard binding by key, in the binding itself", func() {
		attrs := live.OnWith("keydown", "chat.clear", live.Bind{Keys: []string{"Escape"}})

		Expect(attrs).To(Equal(templ.Attributes{"data-gotth-on": "keydown:chat.clear:Escape"}))
	})

	// One binding per key, which is what leaves every printable character
	// available as a key value: a separated list would have had to reserve one,
	// and a comma — the obvious choice — is itself a key.
	It("renders a key list as one binding per key, in order", func() {
		attrs := live.OnWith("keydown", "list.move", live.Bind{Keys: []string{"ArrowUp", "ArrowDown", ",", " "}})

		Expect(attrs["data-gotth-on"]).To(Equal(
			"keydown:list.move:ArrowUp;keydown:list.move:ArrowDown;keydown:list.move:,;keydown:list.move: "))
	})

	// Backward compatibility, and it is the reason the filter is optional
	// rather than a separate helper: every binding written before this option
	// existed must still mean "every occurrence of this DOM event".
	It("renders no filter at all for an empty key list", func() {
		Expect(live.OnWith("keydown", "chat.send", live.Bind{Keys: nil})["data-gotth-on"]).
			To(Equal("keydown:chat.send"))
		Expect(live.OnWith("keydown", "chat.send", live.Bind{Keys: []string{}})["data-gotth-on"]).
			To(Equal("keydown:chat.send"))
	})

	// Two spreads of On render the attribute twice and an HTML parser keeps
	// the first, so before OnAll the second binding on an element vanished
	// with no error anywhere. This is the composer case — bound for input and
	// for a key — and the keyboard counter's case, where two keys raise two
	// different events from one focused element.
	It("combines several bindings on one element, in order", func() {
		attrs := live.OnAll(
			live.OnWith("keydown", "counter.increment", live.Bind{Keys: []string{"+", "="}}),
			live.OnWith("keydown", "counter.decrement", live.Bind{Keys: []string{"-"}}),
			live.On("click", "counter.reset"),
		)

		Expect(attrs["data-gotth-on"]).To(Equal(
			"keydown:counter.increment:+;keydown:counter.increment:=;" +
				"keydown:counter.decrement:-;click:counter.reset"))
	})

	It("renders nothing for no bindings at all", func() {
		Expect(live.OnAll()).To(BeEmpty())
	})

	It("marks a preserved subtree", func() {
		Expect(live.Preserve()).To(Equal(templ.Attributes{"data-gotth-preserve": true}))
	})

	DescribeTable("renders a script tag addressing the mount it was given",
		func(mount, want string) {
			var buf strings.Builder
			Expect(live.Script(mount).Render(context.Background(), &buf)).To(Succeed())
			Expect(buf.String()).To(Equal(want))
		},
		Entry("the conventional mount", "/live",
			`<script src="/live/gotth-live.min.js" data-gotth-url="/live" defer></script>`),
		// The defect this parameter exists for. A default of "/live" made this
		// case render a tag that 404s, with no error on either side of the
		// failure: the page loads and nothing is live.
		Entry("a nested mount", "/app/live",
			`<script src="/app/live/gotth-live.min.js" data-gotth-url="/app/live" defer></script>`),
		Entry("the root", "/",
			`<script src="/gotth-live.min.js" data-gotth-url="/" defer></script>`),
	)

	// mux.Handle wants "/live/" and a src attribute wants "/live". That one
	// character is genuinely ambiguous, so it is the one this normalises — and
	// exactly one of it, because a path is a routing decision the caller made
	// and rewriting more of it would make Script a second, quieter router.
	It("renders a trailing slash identically to none", func() {
		var with, without strings.Builder
		Expect(live.Script("/app/live/").Render(context.Background(), &with)).To(Succeed())
		Expect(live.Script("/app/live").Render(context.Background(), &without)).To(Succeed())

		Expect(with.String()).To(Equal(without.String()))
	})

	// The one remaining way to get this wrong, and where the error lands. It
	// cannot be caught inside the library at request time — the router strips
	// the prefix before the handler sees anything, and this renders on the page
	// request — so it is caught at render, where a handler already has an error
	// path and the answer is a 500 rather than a blank page.
	//
	// Every entry below the first two was measured in a real Chromium against
	// two loopback origins before it was written down: each rendered a working
	// tag, and the browser did the thing named in the comment. The reason these
	// are not obvious from the string is that browsers parse URLs with the
	// WHATWG standard rather than RFC 3986, and Go's net/url — the right
	// library, read carefully — calls the second and third same-origin.
	DescribeTable("refuses a mount path that cannot address the handler",
		func(mount, because string) {
			var buf strings.Builder
			err := live.Script(mount).Render(context.Background(), &buf)

			Expect(err).To(MatchError(ContainSubstring(because)))
			Expect(buf.String()).To(BeEmpty(), "a refused mount must not also emit a tag")
		},
		Entry("empty", "", "needs the path the handler is mounted at"),
		Entry("relative", "live", "is not absolute"),
		// The browser fetched the runtime from 127.0.0.1:8081 and opened the
		// gotth-live.v1 WebSocket there. Not a broken tag — a working one,
		// pointed at another origin.
		Entry("an authority", "//127.0.0.1:9/live", `contains "//"`),
		// Identical browser behaviour: WHATWG's special-authority-ignore-slashes
		// state skips every leading "/" and "\" before the host.
		Entry("an authority behind a third slash", "///x/live", `contains "//"`),
		// Same again, and this one survived the old %q writer doubling the
		// backslash: one backslash and two behave the same in a browser.
		Entry("an authority behind a backslash", `/\x/live`, "contains a backslash"),
		// The reachable mistake, with nobody hostile: "/" + prefix + "/live"
		// with an empty prefix. The browser resolved src against the host
		// "live" and used "live" as the WebSocket host.
		Entry("a concatenation with an empty prefix", "//live", `contains "//"`),
		// src became "/live" with "?x=1/gotth-live.min.js" as the query. The
		// runtime file was never requested and the page carried no
		// data-gotth-status at all — the C-23 silent no-op, through a character.
		Entry("a query", "/live?x=1", `contains "?"`),
		// Verbatim from the browser: "Failed to construct 'WebSocket': The URL
		// contains a fragment identifier ('f')."
		Entry("a fragment", "/live#f", `contains "#"`),
		// Browsers remove tab, CR and LF from a URL before parsing it, so the
		// path requested is not the path written.
		Entry("a control byte", "/li\tve", "control byte"),
	)

	// The invariant the two attributes are supposed to hold and did not: with
	// a mount of "/live//", src was "/live/gotth-live.min.js" and
	// data-gotth-url was "/live/", because src was trimmed a second time and
	// the URL was not. Clause 2 makes that particular input unreachable, but
	// the invariant is the thing worth guarding — the next normalisation change
	// will break it again, and the two attributes disagreeing is a session
	// opened somewhere other than where the runtime was fetched from.
	DescribeTable("renders a src and a data-gotth-url that name the same mount",
		func(mount string) {
			var buf strings.Builder
			Expect(live.Script(mount).Render(context.Background(), &buf)).To(Succeed())

			tag := buf.String()
			url := scriptAttr(tag, "data-gotth-url")
			Expect(scriptSrc(tag)).To(Equal(strings.TrimSuffix(url, "/")+"/gotth-live.min.js"),
				"src and %s must address one mount, in %q", "data-gotth-url", tag)
		},
		Entry("the conventional mount", "/live"),
		Entry("a nested mount", "/app/live"),
		Entry("the root", "/"),
		Entry("a mount that is not /live at all", "/ui"),
		Entry("a trailing slash", "/app/live/"),
		Entry("percent-encoding, which is accepted", "/%2f%2f127.0.0.1"),
		Entry("dot-dot segments, which are accepted", "/app/../live"),
	)

	// Script is the module's one hand-rolled markup writer; Region, On, OnWith
	// and Preserve return templ.Attributes and are escaped by templ. It used
	// fmt's %q, which is Go quoting and not HTML attribute quoting, and this is
	// the spec that caught the difference without any hostile input: every
	// character of "/reports&sect;ion/live" is one the clauses above accept,
	// and unescaped the browser decoded &sect; and fetched /reports§ion/live,
	// a path the caller never mounted.
	It("HTML-escapes the mount path it writes into the tag", func() {
		var buf strings.Builder
		Expect(live.Script("/reports&sect;ion/live").Render(context.Background(), &buf)).To(Succeed())

		Expect(buf.String()).To(Equal(
			`<script src="/reports&amp;sect;ion/live/gotth-live.min.js" `+
				`data-gotth-url="/reports&amp;sect;ion/live" defer></script>`),
			"an unescaped & is a character reference the browser decodes")
	})
})

var _ = Describe("The handler", func() {
	It("serves the embedded client runtime, immutably", func() {
		app := mount(nil)
		defer app.stop()

		resp, err := http.Get(app.server.URL + "/gotth-live.min.js")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(body).NotTo(BeEmpty(), "the embedded runtime is empty")
		Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/javascript"))
		Expect(resp.Header.Get("ETag")).NotTo(BeEmpty())
		Expect(resp.Header.Get("Cache-Control")).To(ContainSubstring("immutable"))
	})

	It("answers a conditional request without resending the runtime", func() {
		app := mount(nil)
		defer app.stop()

		first, err := http.Get(app.server.URL + "/gotth-live.min.js")
		Expect(err).NotTo(HaveOccurred())
		first.Body.Close()

		req, err := http.NewRequest(http.MethodGet, app.server.URL+"/gotth-live.min.js", nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("If-None-Match", first.Header.Get("ETag"))

		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusNotModified))
	})

	It("serves the runtime at whatever prefix the application is mounted under", func() {
		app := mountAt("/app/", nil)
		defer app.stop()

		resp, err := http.Get(app.server.URL + "/app/gotth-live.min.js")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
	})

	// The two halves joined, which is the property the FR-33 three-router suite
	// will assert once per router: whatever prefix the handler is mounted at,
	// the src the page renders is fetchable from that same server. Asserting
	// the tag and the route separately is what let them disagree — the handler
	// answered at /app/ and the tag pointed at /live, and each half had a
	// passing test.
	DescribeTable("renders a src the mounted handler actually answers",
		func(prefix, mount string) {
			app := mountAt(prefix, nil)
			defer app.stop()

			var buf strings.Builder
			Expect(live.Script(mount).Render(context.Background(), &buf)).To(Succeed())

			// HavePrefix("/") alone is the predicate that was wrong: it passes
			// for "//evil.example/live/gotth-live.min.js", a src a browser
			// fetches from another origin entirely. The property FR-33 wants
			// is that the src has no authority, so assert both halves — and
			// the http.Get below then exercises it against this server.
			src := scriptSrc(buf.String())
			Expect(src).To(HavePrefix("/"))
			Expect(src).NotTo(HavePrefix("//"),
				"a src beginning // names an authority, not a path on this server")

			resp, err := http.Get(app.server.URL + src)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK),
				"the page tells the browser to fetch %s and the mounted handler answers %d there",
				src, resp.StatusCode)
			Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/javascript"))
		},
		Entry("the conventional mount", "/live/", "/live"),
		Entry("a nested mount", "/app/live/", "/app/live"),
		Entry("a mount that is not /live at all", "/ui/", "/ui"),
		Entry("the root", "/", "/"),
	)
})

// scriptSrc extracts the src attribute from a rendered script tag, so a spec
// fetches exactly what the page tells a browser to fetch rather than a URL the
// spec rebuilt from the same input.
func scriptSrc(tag string) string {
	GinkgoHelper()
	return scriptAttr(tag, "src")
}

// scriptAttr is scriptSrc for any attribute, so the spec asserting that src and
// data-gotth-url name one mount reads both out of the rendered bytes rather
// than recomputing one of them.
func scriptAttr(tag, name string) string {
	GinkgoHelper()
	_, rest, found := strings.Cut(tag, name+`="`)
	Expect(found).To(BeTrue(), "no %s attribute in %q", name, tag)
	value, _, found := strings.Cut(rest, `"`)
	Expect(found).To(BeTrue(), "unterminated %s attribute in %q", name, tag)
	return value
}

var _ = Describe("An application end to end", func() {
	It("mounts, reduces and patches over the real protocol", func() {
		app := mount(nil)
		defer app.stop()

		Expect(app.snapshot.GetUpdates()[0].GetHtml()).To(Equal("<b>hits 0</b>"))

		app.send("counter.increment", nil)
		patch := app.nextPatch()

		Expect(patch.GetOrigin().GetSource()).To(Equal("event:counter.increment"))
		Expect(patch.GetUpdates()[0].GetHtml()).To(Equal("<b>hits 1</b>"))
	})

	It("refuses an event name the application did not declare", func() {
		app := mount(nil)
		defer app.stop()

		app.sendRaw("counter.unknown", nil)

		Expect(app.nextError().GetCode()).To(Equal(pb.ErrorCode_UNKNOWN_EVENT))
	})

	It("denies an event when the hook says so, without closing the connection", func() {
		app := mount(func(c *live.Config[counter, user]) {
			c.Authorize = func(_ context.Context, s live.Session[user], ev live.Event) error {
				Expect(s.Identity().Subject()).To(Equal("tester"))
				Expect(s.ID().String()).To(HaveLen(32))
				return &live.DenyError{Reason: "read-only session"}
			}
		})
		defer app.stop()

		app.send("counter.increment", nil)

		Expect(app.nextError().GetCode()).To(Equal(pb.ErrorCode_UNAUTHORIZED))
	})

	It("treats an unrecognised authorization error as a denial rather than an allow", func() {
		app := mount(func(c *live.Config[counter, user]) {
			c.Authorize = func(ctx context.Context, session live.Session[user], event live.Event) error {
				return errors.New("the policy service is down")
			}
		})
		defer app.stop()

		app.send("counter.increment", nil)

		Expect(app.nextError().GetCode()).To(Equal(pb.ErrorCode_UNAUTHORIZED))
	})

	It("performs an effect and folds its result back in", func() {
		relabel := func(_ context.Context, _ live.Session[user], emit live.Emitter) error {
			return emit(live.Event{
				Name:   "counter.relabel",
				Fields: live.Event{}.Fields,
			})
		}
		app := mount(func(c *live.Config[counter, user]) {
			c.Reduce = func(state counter, ev live.Event) (counter, []live.Effect[user]) {
				switch ev.Name {
				case "counter.increment":
					return state, []live.Effect[user]{logEffect(relabel)}
				case "counter.relabel":
					state.Label = ev.Fields.Get("label")
				}
				return state, nil
			}
		})
		defer app.stop()

		app.send("counter.increment", nil)
		patch := app.nextPatch()

		Expect(patch.GetOrigin().GetKind()).To(Equal(pb.OriginKind_EFFECT))
		Expect(patch.GetOrigin().GetSource()).To(Equal("effect:test.log"))
	})

	// The identity an effect acts as. Authorize sees who is asking and used to
	// be the last hook that did: by the time the effect it permitted actually
	// ran, the only thing left was the effect value, so an application that
	// needed the publisher had to smuggle it into the effect itself. The
	// session an effect's Run receives is the same one every other hook
	// receives, for the same connection.
	It("hands an effect the session it is running for", func() {
		seen := make(chan live.Session[user], 1)
		record := func(_ context.Context, s live.Session[user], _ live.Emitter) error {
			seen <- s
			return nil
		}
		app := mount(func(c *live.Config[counter, user]) {
			c.Reduce = func(state counter, ev live.Event) (counter, []live.Effect[user]) {
				return state, []live.Effect[user]{logEffect(record)}
			}
		})
		defer app.stop()

		app.send("counter.increment", nil)

		var s live.Session[user]
		Eventually(seen).Should(Receive(&s))
		Expect(s.Identity().Subject()).To(Equal("tester"))
		Expect(s.ID().String()).To(Equal(hex.EncodeToString(app.id)),
			"the effect was told about a different session than the one it belongs to")
	})

	// The effect-failure contract, from an application's side of the boundary.
	//
	// It is exercised end to end rather than asserted about, because the way
	// this went wrong before was a reducer matching on a name nothing emits:
	// the arm was never entered, and a test that only checked the arm's body
	// would have agreed with it. Here the effect really fails, the library
	// really synthesises the event, and the reducer's own branch is what
	// produces the patch this spec reads.
	It("turns a failed effect into an event the reducer can handle", func() {
		refuse := func(_ context.Context, _ live.Session[user], _ live.Emitter) error {
			return errors.New("upstream refused")
		}
		app := mount(func(c *live.Config[counter, user]) {
			c.Reduce = func(state counter, ev live.Event) (counter, []live.Effect[user]) {
				switch ev.Name {
				case "counter.increment":
					return state, []live.Effect[user]{logEffect(refuse)}
				case live.EffectFailedEvent:
					retryable, err := strconv.ParseBool(
						ev.Fields.Get(live.EffectFailedRetryableField))
					Expect(err).NotTo(HaveOccurred())
					state.Label = fmt.Sprintf("%s/%s/%t",
						ev.Fields.Get(live.EffectFailedSourceField),
						ev.Fields.Get(live.EffectFailedErrorField),
						retryable)
				}
				return state, nil
			}
		})
		defer app.stop()

		app.send("counter.increment", nil)

		Expect(app.nextPatch().GetUpdates()[0].GetHtml()).
			To(Equal("<b>test.log/upstream refused/false 0</b>"))
	})

	// The same path with the classification the effect is entitled to make.
	// The reducer schedules a second attempt, and the only reason it is allowed
	// to is that the executor said so.
	It("lets an effect classify its own failure as transient", func() {
		var mu sync.Mutex
		attempts := 0
		count := func() int {
			mu.Lock()
			defer mu.Unlock()
			return attempts
		}

		flaky := func(_ context.Context, _ live.Session[user], _ live.Emitter) error {
			mu.Lock()
			attempts++
			n := attempts
			mu.Unlock()
			if n < 3 {
				return live.Retryable(errors.New("the broker is reconnecting"))
			}
			return nil
		}
		app := mount(func(c *live.Config[counter, user]) {
			c.Reduce = func(state counter, ev live.Event) (counter, []live.Effect[user]) {
				switch ev.Name {
				case "counter.increment":
					return state, []live.Effect[user]{logEffect(flaky)}
				case live.EffectFailedEvent:
					if retryable, _ := strconv.ParseBool(
						ev.Fields.Get(live.EffectFailedRetryableField)); retryable {
						return state, []live.Effect[user]{logEffect(flaky)}
					}
				}
				return state, nil
			}
		})
		defer app.stop()

		app.send("counter.increment", nil)

		Eventually(count).Should(Equal(3))
		Consistently(count, 100*time.Millisecond).Should(Equal(3),
			"the third attempt succeeded, so nothing should still be retrying")
	})

	It("runs the teardown hook with the final state", func() {
		done := make(chan counter, 1)
		app := mount(func(c *live.Config[counter, user]) {
			c.Teardown = func(_ context.Context, _ live.Session[user], s counter) { done <- s }
		})

		app.send("counter.increment", nil)
		app.nextPatch()
		app.stop()

		var final counter
		Eventually(done).Should(Receive(&final))
		Expect(final).To(Equal(counter{N: 1, Label: "hits"}))
	})
})

// FR-23 end to end, over the exported surface: a panic in a reducer or in a
// render is contained to its session, produces an Error frame carrying the
// causal ID, and shows the developer the stack in dev mode and the client a
// generic message in production. Config.Dev is set here rather than in the
// actor's own suite because the field an application actually holds is this
// one, and until C-26 nothing in the library read it.
var _ = Describe("The error boundary", func() {

	It("answers a reducer panic with a generic message in production", func() {
		app := mount(panickingReducer(false))
		defer app.stop()

		app.send("counter.increment", nil)

		e := app.nextError()
		Expect(e.GetCode()).To(Equal(pb.ErrorCode_INTERNAL))
		Expect(e.GetEventId()).To(Equal(uint64(1)))
		Expect(e.GetMessage()).To(Equal("the transition failed"))
		Expect(e.GetFatal()).To(BeFalse())

		// The session survived it: the next event still reduces and patches.
		app.send("counter.relabel", map[string]string{"label": "clicks"})
		Expect(app.nextPatch().GetUpdates()[0].GetHtml()).To(Equal("<b>clicks 0</b>"))
	})

	It("answers a reducer panic with the stack when Config.Dev is set", func() {
		app := mount(panickingReducer(true))
		defer app.stop()

		app.send("counter.increment", nil)

		msg := app.nextError().GetMessage()
		Expect(msg).To(HavePrefix("the transition failed\n"))
		Expect(msg).To(ContainSubstring("the reducer exploded"))
		Expect(msg).To(ContainSubstring("goroutine "))
	})

	It("answers a render panic with an Error frame naming the event", func() {
		app := mount(panickingFragment(false))
		defer app.stop()

		// The mount snapshot rendered the broken fragment too, and that
		// failure names no event because no client frame caused it.
		Expect(app.nextError().GetEventId()).To(BeZero())

		app.send("counter.increment", nil)

		e := app.nextError()
		Expect(e.GetCode()).To(Equal(pb.ErrorCode_INTERNAL))
		Expect(e.GetEventId()).To(Equal(uint64(1)))
		Expect(e.GetClientRef()).To(Equal(uint64(1)))
		Expect(e.GetMessage()).To(Equal("a region of the page could not be rendered and is stale"))
	})

	It("answers a render panic with the fragment and the stack when Config.Dev is set", func() {
		app := mount(panickingFragment(true))
		defer app.stop()

		msg := app.nextError().GetMessage()
		Expect(msg).To(HavePrefix("a region of the page could not be rendered and is stale\n"))
		Expect(msg).To(ContainSubstring("fragment bad (render): the render exploded"))
		Expect(msg).To(ContainSubstring("goroutine "))
	})

	It("leaves every other session serving while one session panics", func() {
		app := mount(panickingReducer(false))
		defer app.stop()
		other := app.again()
		defer other.conn.CloseNow()

		app.send("counter.increment", nil)
		Expect(app.nextError().GetCode()).To(Equal(pb.ErrorCode_INTERNAL))

		other.send("counter.relabel", map[string]string{"label": "clicks"})
		Expect(other.nextPatch().GetUpdates()[0].GetHtml()).To(Equal("<b>clicks 0</b>"))
	})
})

func panickingReducer(dev bool) func(cfg *live.Config[counter, user]) {
	return func(c *live.Config[counter, user]) {
		c.Dev = dev
		c.Reduce = func(state counter, ev live.Event) (counter, []live.Effect[user]) {
			if ev.Name == "counter.increment" {
				panic("the reducer exploded")
			}
			state.Label = ev.Fields.Get("label")
			return state, nil
		}
	}
}

func panickingFragment(dev bool) func(cfg *live.Config[counter, user]) {
	return func(c *live.Config[counter, user]) {
		c.Dev = dev
		c.Fragments = append(c.Fragments, live.Fragment[counter]{
			ID: "bad",
			Render: func(state counter) templ.Component {
				return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
					panic("the render exploded")
				})
			},
		})
	}
}

// mounted is a live application behind a real HTTP server and a real dialled
// client, so every spec above crosses the handshake and the wire.
type mounted struct {
	app      *live.App[counter, user]
	server   *httptest.Server
	conn     *websocket.Conn
	ctx      context.Context
	snapshot *pb.Snapshot
	id       []byte
	ref      uint64
}

func mount(mutate func(cfg *live.Config[counter, user])) *mounted {
	return mountAt("/", mutate)
}

func mountAt(prefix string, mutate func(cfg *live.Config[counter, user])) *mounted {
	GinkgoHelper()

	cfg := validConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	app, err := live.New(cfg)
	Expect(err).NotTo(HaveOccurred())

	mux := http.NewServeMux()
	mux.Handle(prefix, http.StripPrefix(strings.TrimSuffix(prefix, "/"), app.Handler()))
	ts := httptest.NewServer(mux)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	DeferCleanup(cancel)

	headers := http.Header{}
	headers.Set("Origin", "https://app.example")
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+strings.TrimSuffix(prefix, "/"),
		&websocket.DialOptions{HTTPHeader: headers, Subprotocols: []string{"gotth-live.v1"}})
	Expect(err).NotTo(HaveOccurred())

	m := &mounted{app: app, server: ts, conn: c, ctx: ctx}
	first := m.next()
	Expect(first.GetSnapshot()).NotTo(BeNil())
	m.snapshot = first.GetSnapshot()
	m.id = first.GetSessionId()
	return m
}

// again dials the same application a second time. It is how a spec asserts
// that a panic contained to one session left the others serving, which is the
// half of FR-23 a single connection cannot show.
func (m *mounted) again() *mounted {
	GinkgoHelper()

	headers := http.Header{}
	headers.Set("Origin", "https://app.example")
	c, _, err := websocket.Dial(m.ctx, "ws"+strings.TrimPrefix(m.server.URL, "http"),
		&websocket.DialOptions{HTTPHeader: headers, Subprotocols: []string{"gotth-live.v1"}})
	Expect(err).NotTo(HaveOccurred())

	n := &mounted{app: m.app, server: m.server, conn: c, ctx: m.ctx}
	first := n.next()
	Expect(first.GetSnapshot()).NotTo(BeNil())
	n.snapshot = first.GetSnapshot()
	n.id = first.GetSessionId()
	return n
}

func (m *mounted) stop() {
	m.conn.CloseNow()
	Expect(m.app.Close(m.ctx)).To(Succeed())
	m.server.Close()
}

func (m *mounted) next() *pb.Frame {
	GinkgoHelper()
	typ, data, err := m.conn.Read(m.ctx)
	Expect(err).NotTo(HaveOccurred())
	Expect(typ).To(Equal(websocket.MessageBinary))

	var f pb.Frame
	Expect(proto.Unmarshal(data, &f)).To(Succeed())
	return &f
}

func (m *mounted) nextPatch() *pb.Patch {
	GinkgoHelper()
	for {
		f := m.next()
		if p := f.GetPatch(); p != nil {
			return p
		}
	}
}

func (m *mounted) nextError() *pb.Error {
	GinkgoHelper()
	for {
		f := m.next()
		if e := f.GetError(); e != nil {
			return e
		}
	}
}

func (m *mounted) send(name string, fields map[string]string) {
	GinkgoHelper()
	var ordered [][2]string
	for k, v := range fields {
		ordered = append(ordered, [2]string{k, v})
	}
	m.sendOrdered(name, ordered)
}

func (m *mounted) sendOrdered(name string, fields [][2]string) {
	GinkgoHelper()
	m.sendRaw(name, fields)
}

// sendTelemetry reports a client-side apply timing for a patch, which is how
// the half of the trace that happens in the browser rejoins the server's.
func (m *mounted) sendTelemetry(patchID uint64) {
	GinkgoHelper()
	b, err := proto.Marshal(&pb.Frame{
		ProtocolVersion: 1,
		SessionId:       m.id,
		Payload: &pb.Frame_ClientTelemetry{ClientTelemetry: &pb.ClientTelemetry{
			PatchId: patchID, MorphMicros: 1200, ApplyMicros: 800,
		}},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(m.conn.Write(m.ctx, websocket.MessageBinary, b)).To(Succeed())
}

func (m *mounted) sendRaw(name string, fields [][2]string) {
	GinkgoHelper()
	m.ref++

	payload := &pb.Event{
		ClientRef: m.ref, Name: name, FragmentId: "counter", SeenServerSeq: 1,
	}
	for _, kv := range fields {
		payload.Fields = append(payload.Fields, &pb.EventField{Key: kv[0], Value: kv[1]})
	}

	b, err := proto.Marshal(&pb.Frame{
		ProtocolVersion: 1,
		SessionId:       m.id,
		Payload:         &pb.Frame_Event{Event: payload},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(m.conn.Write(m.ctx, websocket.MessageBinary, b)).To(Succeed())
}

// sendAck acknowledges every patch up to seq, which is what re-opens a full
// outbound window. It is the client half of the backpressure ladder.
func (m *mounted) sendAck(seq uint64) {
	GinkgoHelper()
	b, err := proto.Marshal(&pb.Frame{
		ProtocolVersion: 1,
		SessionId:       m.id,
		Payload:         &pb.Frame_Ack{Ack: &pb.Ack{ServerSeq: seq}},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(m.conn.Write(m.ctx, websocket.MessageBinary, b)).To(Succeed())
}

// The backpressure vocabulary is exported so that a reducer implementing a
// defined degradation branches on a constant rather than on a string it copied
// out of a document. What holds those constants honest cannot be an equality
// between two constants: each is declared as the internal one the actor
// synthesizes, so an equality spec would agree with itself by construction and
// prove nothing about what a reducer receives.
//
// So this drives a real session over a real socket into the second stage of the
// ladder, and asserts on the name the reducer was actually handed. It is the
// counter's version of what examples/dashboard's backpressure spec does for an
// application, and it lives here because the next application will not have
// that spec — which is the whole of FRICTION.md F-1's argument.
var _ = Describe("The backpressure vocabulary", func() {
	It("reaches a reducer under the names live exports", func() {
		var mu sync.Mutex
		var seen []string

		app := mount(func(c *live.Config[counter, user]) {
			// Two unacknowledged patches is the whole window, so a handful of
			// events overruns it. The grace period is long because eviction is
			// the ladder's third stage and is not what this spec is about.
			c.Limits.AckWindow = 2
			c.Limits.SlowClientGrace = time.Minute

			reduce := c.Reduce
			c.Reduce = func(state counter, ev live.Event) (counter, []live.Effect[user]) {
				mu.Lock()
				seen = append(seen, ev.Name)
				mu.Unlock()
				return reduce(state, ev)
			}
		})
		defer app.stop()

		names := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(seen)
		}

		// Far more transitions than the window can hold, none acknowledged.
		for i := 0; i < 40; i++ {
			app.send("counter.increment", nil)
		}
		Eventually(names).Should(ContainElement(live.SlowClientEvent),
			"a full outbound window must reach the reducer as live.SlowClientEvent")

		// The window filled, so at least one patch is on the wire to acknowledge.
		// Acknowledging it frees a slot onto deferred work, which is what the
		// library answers with the recovery event.
		app.sendAck(app.nextPatch().GetServerSeq())
		Eventually(names).Should(ContainElement(live.ClientRecoveredEvent),
			"a drained outbound window must reach the reducer as live.ClientRecoveredEvent")

		// Neither is registrable, and that is the other half of the contract:
		// these names arrive without being in Config.Events precisely because a
		// browser may not send them.
		Expect(validConfig().Events).NotTo(ContainElement(live.SlowClientEvent))
		Expect(validConfig().Events).NotTo(ContainElement(live.ClientRecoveredEvent))
	})
})
