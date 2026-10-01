package livetest_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

type tester string

func (t tester) Subject() string { return string(t) }

var _ = Describe("NewSession", func() {
	// The gap this closes, stated as the thing that was actually wrong: a
	// Session an application can build itself carries the ZERO identity and the
	// zero identifier, and identity is the entire reason the hooks take a
	// Session.
	//
	// "Zero" rather than "nil" since 2026-09-03: the identity is the
	// application's own type now, so a Session built from an empty composite
	// literal has an identity whose Subject() is empty rather than one that
	// panics. That is a smaller trap than the nil it replaces, and it is still
	// a trap — an empty subject is not a session anybody authenticated.
	It("is the difference between a Session and a useful one", func() {
		zero := live.Session[tester]{}
		Expect(zero.Identity().Subject()).To(BeEmpty())
		Expect(zero.ID()).To(Equal(live.ID{}))

		s := livetest.NewSession(GinkgoTB(), live.ID{1, 2, 3}, tester("alice"))

		Expect(s.ID()).To(Equal(live.ID{1, 2, 3}))
		Expect(s.Identity().Subject()).To(Equal("alice"))
	})

	// One subject, many sessions — which is what Limits.MaxSessionsPerIdentity
	// is about, and why the identifier is the caller's rather than derived from
	// the identity. Two tabs belonging to one user is the first thing the chat
	// example needs.
	It("gives one identity as many distinct sessions as the caller asks for", func() {
		user := tester("alice")

		first := livetest.NewSession(GinkgoTB(), live.ID{0xa}, user)
		second := livetest.NewSession(GinkgoTB(), live.ID{0xb}, user)

		Expect(first.ID()).NotTo(Equal(second.ID()))
		Expect(first.Identity().Subject()).To(Equal(second.Identity().Subject()))
	})

	// The "fails on a nil identity" spec is gone, and its absence is the point.
	// NewSession is generic in the identity type since 2026-09-03, so `nil` is
	// not a value it can be handed unless the caller chose a pointer type and
	// passed a nil of it — which is that caller's own bug, in its own Subject(),
	// rather than a trap this constructor sets and then has to guard.

	// The point of the helper: a Config hook that takes a Session becomes
	// callable from a spec, with no running server and no adapter invented by
	// the application to work around the gap.
	It("makes a Config hook callable without a server", func() {
		var seen live.Session[tester]
		cfg := live.Config[counter, tester]{
			Init: func(_ context.Context, s live.Session[tester]) (counter, []live.Effect[tester], error) {
				seen = s
				return counter{Label: s.Identity().Subject()}, nil, nil
			},
		}

		state, effects, err := cfg.Init(context.Background(),
			livetest.NewSession(GinkgoTB(), live.ID{7}, tester("bob")))

		Expect(err).NotTo(HaveOccurred())
		Expect(effects).To(BeEmpty())
		Expect(state.Label).To(Equal("bob"))
		Expect(seen.ID()).To(Equal(live.ID{7}))
	})
})
