package apptest_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/docs/guide/_samples/apptest"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

// log is the event log both determinism helpers replay.
//
// live.NewFields is the only way to give an event a payload, so it is also the
// only way a determinism test can build a log that carries form values.
func log() []live.Event {
	return []live.Event{
		{Name: apptest.EventInc, FragmentID: apptest.FragmentValue},
		{Name: apptest.EventInc, FragmentID: apptest.FragmentValue},
		{Name: apptest.EventReset, FragmentID: apptest.FragmentValue, Fields: live.NewFields(map[string]string{"by": "admin"})},
		{Name: apptest.EventInc, FragmentID: apptest.FragmentValue},
	}
}

var _ = Describe("the reducer", func() {
	It("is deterministic", func() {
		// FR-15's mandatory harness. It replays the log 25 times and fails
		// unless the state AND the emitted effects are identical every run. A
		// reducer that reads a clock, a random source, or the iteration order
		// of a map fails here; nothing else in a pure function of two values
		// can differ between runs.
		livetest.ReplayN(GinkgoTB(), apptest.Reduce, apptest.State{}, log(), 25)
	})

	It("declares every fragment that moved", func() {
		// Replays the same log against the whole Config and fails if any
		// fragment declared itself unchanged while its rendered bytes moved.
		// Under-declaring is the one rendering mistake that produces a stale
		// region in production and nothing at all in development.
		//
		// It does not catch over-declaring, and says so: over-declaring costs
		// a suppressed render and never a wrong pixel. The signal for that is
		// gotthlive_patches_suppressed_total.
		livetest.AssertDirtyComplete(GinkgoTB(),
			apptest.Config([]string{"http://127.0.0.1:8080"}), apptest.State{}, log())
	})

	It("advances the count", func() {
		state := apptest.State{}
		for _, ev := range log() {
			state, _ = apptest.Reduce(state, ev)
		}
		Expect(state.N).To(Equal(1))
		Expect(state.Resets).To(Equal(1))
	})
})

var _ = Describe("the authorization hook", func() {
	// livetest.NewSession builds the live.Session a Config hook is called with.
	// Session's fields are unexported because identity is bound at the
	// handshake and nothing downstream may mint one, so a zero Session compiles
	// and is useless: its Identity() is the zero identity, and identity is the
	// reason the hook takes a Session at all.
	//
	// Both values are the caller's. Two tabs belonging to one user are two
	// identifiers and one identity, which is what Limits.MaxSessionsPerIdentity
	// is about.
	newSession := func(b byte, identity apptest.User) live.Session[apptest.User] {
		return livetest.NewSession(GinkgoTB(), live.ID{b}, identity)
	}

	It("denies a reset from a guest without closing the connection", func() {
		err := apptest.Authorize(context.Background(),
			newSession(1, apptest.Guest), live.Event{Name: apptest.EventReset})

		var deny *live.DenyError
		Expect(errors.As(err, &deny)).To(BeTrue())
		Expect(deny.Reason).To(Equal("only an admin may reset"))
	})

	It("allows a reset from an admin", func() {
		Expect(apptest.Authorize(context.Background(),
			newSession(2, apptest.Admin), live.Event{Name: apptest.EventReset})).To(Succeed())
	})

	It("allows an increment from anybody", func() {
		Expect(apptest.Authorize(context.Background(),
			newSession(3, apptest.Guest), live.Event{Name: apptest.EventInc})).To(Succeed())
	})
})

var _ = Describe("the configuration", func() {
	It("is rejected at startup when a security hook is missing", func() {
		cfg := apptest.Config([]string{"http://127.0.0.1:8080"})
		cfg.CSRF = nil

		_, err := live.New(cfg)

		// live.New reports a *live.ConfigError naming the field at fault and
		// what to set it to, rather than six error sentinels to match on.
		var cfgErr *live.ConfigError
		Expect(errors.As(err, &cfgErr)).To(BeTrue())
		Expect(cfgErr.Field).To(Equal("CSRF"))
		Expect(cfgErr.Detail).To(ContainSubstring("live.NoCSRFCheck"))
	})

	It("is rejected when Origins is empty and no escape hatch is named", func() {
		_, err := live.New(apptest.Config(nil))

		Expect(err).To(MatchError(ContainSubstring("Origins")))
	})
})
