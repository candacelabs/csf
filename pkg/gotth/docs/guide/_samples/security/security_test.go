package security_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/docs/guide/_samples/security"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

// These specs are the executable half of docs/guide/security.md.
//
// The origin block below exists because of a live defect. Six copies of an
// allowlist helper diverged, the documented way to run three of this project's
// own examples produced an allowlist nothing could match, every upgrade was
// refused with 403 — and the spec that was supposed to catch it asserted only
// that the result was not live.AnyOrigin. An allowlist that allows nobody is
// not the wildcard. So nothing here asserts what the allowlist is NOT: every
// arm sends the Origin header a browser would send and reads the status back.

// upgrade issues the request a browser's WebSocket constructor issues, minus
// the Sec-WebSocket-Protocol header.
//
// Leaving the subprotocol off is what makes the assertions crisp. The handshake
// checks in order — origin, Authenticate, CSRF, subprotocol — so a request that
// clears the origin allowlist stops at the subprotocol with 426, and one that
// does not stops at the origin with 403. Two distinguishable numbers, and
// neither of them is the ambiguous "the connection failed somehow".
func upgrade(base, origin string, headers map[string]string) int {
	GinkgoHelper()

	req, err := http.NewRequest(http.MethodGet, base+"/", nil)
	Expect(err).NotTo(HaveOccurred())

	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func member(name, role string) map[string]string {
	return map[string]string{"X-Room-Member": name, "X-Room-Role": role}
}

var _ = Describe("the origin allowlist", func() {
	var base string

	BeforeEach(func() {
		app, err := live.New(security.Config(&security.Room{}, security.Origins()))
		Expect(err).NotTo(HaveOccurred())

		server := httptest.NewServer(app.Handler())
		DeferCleanup(server.Close)
		base = server.URL
	})

	It("admits the origins it lists, spelled the way a browser spells them", func() {
		for _, origin := range security.Origins() {
			Expect(upgrade(base, origin, member("ada", "member"))).To(Equal(http.StatusUpgradeRequired),
				"origin %q is in Config.Origins and was refused", origin)
		}
	})

	It("matches case-insensitively, because a host name is not case-sensitive", func() {
		Expect(upgrade(base, "https://APP.example.com", member("ada", "member"))).
			To(Equal(http.StatusUpgradeRequired))
	})

	It("refuses an origin with a trailing slash, which is the commonest way to write one wrong", func() {
		Expect(upgrade(base, "https://app.example.com/", member("ada", "member"))).
			To(Equal(http.StatusForbidden))
	})

	It("refuses the same host on the wrong scheme", func() {
		Expect(upgrade(base, "http://app.example.com", member("ada", "member"))).
			To(Equal(http.StatusForbidden))
	})

	It("refuses a subdomain the list does not name, because there is no wildcard", func() {
		Expect(upgrade(base, "https://evil.app.example.com", member("ada", "member"))).
			To(Equal(http.StatusForbidden))
	})

	It("refuses a request that sends no Origin at all", func() {
		Expect(upgrade(base, "", member("ada", "member"))).To(Equal(http.StatusForbidden))
	})

	It("refuses on the origin BEFORE it looks at the identity, which is the ordering that bounds memory", func() {
		// No identity headers at all. A request from an allowed origin gets as
		// far as Authenticate and is answered 401; the same request from a
		// refused origin never reaches it and is answered 403.
		Expect(upgrade(base, "https://app.example.com", nil)).To(Equal(http.StatusUnauthorized))
		Expect(upgrade(base, "https://evil.example.com", nil)).To(Equal(http.StatusForbidden))
	})
})

var _ = Describe("the allowlist that allows nobody", func() {
	It("is refused by live.New, so it cannot reach a deployment", func() {
		_, err := live.New(security.Config(&security.Room{}, nil))
		Expect(err).To(HaveOccurred())

		var cfgErr *live.ConfigError
		Expect(err).To(BeAssignableToTypeOf(cfgErr))
	})

	It("is NOT refused when it merely lists an origin no browser sends", func() {
		// This is the D-1 shape, and it is here as a warning rather than as a
		// feature: "0.0.0.0" is a bind address and no browser ever sends it as
		// an Origin, so this Config is accepted, starts, and refuses every
		// upgrade a browser makes. The library cannot tell the difference
		// between a list that is wrong and a list that is unusual.
		app, err := live.New(security.Config(&security.Room{}, []string{"http://0.0.0.0:8080"}))
		Expect(err).NotTo(HaveOccurred())

		server := httptest.NewServer(app.Handler())
		DeferCleanup(server.Close)

		Expect(upgrade(server.URL, "http://localhost:8080", member("ada", "member"))).
			To(Equal(http.StatusForbidden))
		Expect(upgrade(server.URL, "http://127.0.0.1:8080", member("ada", "member"))).
			To(Equal(http.StatusForbidden))
	})
})

// session builds the live.Session[security.Member] a Config hook is called with. Session's
// fields are unexported — identity is bound at the handshake and nothing
// downstream may mint one — so livetest.NewSession is the way a spec calls a
// hook directly instead of through a running server.
func session(b byte, identity security.Member) live.Session[security.Member] {
	GinkgoHelper()
	return livetest.NewSession(GinkgoTB(), live.ID{b}, identity)
}

var _ = Describe("the three places one rule is enforced", func() {
	var observer, ada live.Session[security.Member]

	BeforeEach(func() {
		observer = session(1, security.Member{Name: "obs", Role: security.RoleObserver})
		ada = session(2, security.Member{Name: "ada", Role: security.RoleMember})
	})

	Describe("Authorize, which cannot render", func() {
		It("lets an observer's post through, because its refusal has to be seen", func() {
			Expect(security.Authorize(context.Background(), observer, live.Event{Name: security.EventPost})).
				To(Succeed())
		})

		It("refuses a purge from a non-moderator without closing the session", func() {
			err := security.Authorize(context.Background(), ada, live.Event{Name: security.EventPurge})

			var deny *live.DenyError
			Expect(err).To(BeAssignableToTypeOf(deny))
		})

		// "closes the session for an identity that is not a member" is gone.
		// A live.Session is typed by the identity Authenticate produced since
		// 2026-09-03, so an identity of another shape is a compile error at the
		// call site rather than a runtime deny — which means the failure mode
		// this spec guarded against is one the hook can no longer have.
	})

	Describe("the reducer, which is where the refusal becomes markup", func() {
		It("renders the observer's refusal and schedules no effect", func() {
			state := security.State{Me: "obs", Role: security.RoleObserver}

			next, effects := security.Reducer(&security.Room{})(state, live.Event{
				Name:   security.EventPost,
				Fields: live.NewFields(map[string]string{security.FieldBody: "hello"}),
			})

			Expect(next.Notice).To(Equal(security.ObserverRefusal))
			Expect(effects).To(BeEmpty())
		})

		It("schedules the write for an identity that may post", func() {
			state := security.State{Me: "ada", Role: security.RoleMember}

			next, effects := security.Reducer(&security.Room{})(state, live.Event{
				Name:   security.EventPost,
				Fields: live.NewFields(map[string]string{security.FieldBody: "hello"}),
			})

			Expect(next.Notice).To(BeEmpty())
			Expect(effects).To(HaveLen(1))
			Expect(effects[0].Source).To(Equal("room.post"))
		})
	})

	Describe("the effect's own Run, which is what a wrong reducer cannot get past", func() {
		It("refuses an observer's post even when the effect reaches it", func() {
			room := &security.Room{}

			err := room.PostEffect("obs", "hello").
				Run(context.Background(), observer, func(live.Event) error { return nil })

			Expect(err).To(HaveOccurred())
			Expect(room.Posted).To(BeEmpty())
		})

		It("performs it for an identity that may post", func() {
			room := &security.Room{}

			Expect(room.PostEffect("ada", "hello").
				Run(context.Background(), ada, func(live.Event) error { return nil })).To(Succeed())
			Expect(room.Posted).To(ConsistOf("ada: hello"))
		})
	})
})

var _ = Describe("the dev-only routes", func() {
	serve := func(dev bool) string {
		GinkgoHelper()

		cfg := security.Config(&security.Room{}, security.Origins())
		cfg.Dev = dev

		app, err := live.New(cfg)
		Expect(err).NotTo(HaveOccurred())

		server := httptest.NewServer(app.Handler())
		DeferCleanup(server.Close)
		return server.URL
	}

	get := func(base, path string) int {
		GinkgoHelper()

		resp, err := http.Get(base + path)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}

	// The three paths Config.Dev gates, by the names they are served under.
	// They are written out rather than derived, because a reader who wants to
	// check a deployment needs the strings to curl.
	devPaths := []string{
		"/gotth-live-inspector.min.js",
		"/gotth-live-dev-reload.min.js",
		"/gotth-live-dev-build",
	}

	It("answer 404 with Config.Dev false, which is how the gate is enforced rather than asserted", func() {
		base := serve(false)
		for _, path := range devPaths {
			Expect(get(base, path)).To(Equal(http.StatusNotFound), "%s was served in production", path)
		}
	})

	It("answer 200 with Config.Dev true, so the 404 above is a gate and not a missing route", func() {
		base := serve(true)
		for _, path := range devPaths {
			Expect(get(base, path)).To(Equal(http.StatusOK), "%s is gated but was not served in dev either", path)
		}
	})

	It("do not gate the client runtime, which every mode serves", func() {
		Expect(get(serve(false), "/gotth-live.min.js")).To(Equal(http.StatusOK))
		Expect(get(serve(true), "/gotth-live.min.js")).To(Equal(http.StatusOK))
	})
})
