package widget_test

import (
	"context"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
)

// The erasure site, attacked with the state types its comment is a promise
// about.
//
// `erasedWidget[S]` claims its assertion is total by construction, and the
// construction is that only the registry ever puts a value where the assertion
// reads one. What the claim does not say — and what the fallback comparison
// makes load-bearing — is that S may be any Go type at all, including the ones
// `==` refuses. A registry that compared two erased states with `==` would
// panic at runtime on a map, a slice or a function, from inside a fragment's
// dirty test, on a page that had been serving for hours. `reflect.DeepEqual` is
// what stands between that and the host, and the difference between the two is
// invisible in a suite whose only state types are `int` and `string`.
//
// Every widget below is hand-written rather than mocked. A mock's state type is
// whatever the spec names, but its methods are recorded expectations, and what
// these specs drive is the adapter's own behaviour when a phase hands it back a
// value that cannot be compared, cannot be hashed, and in one case cannot be
// distinguished from the zero value except by identity.

// hostile is a widget over an arbitrary S: it mounts the value it was built
// with, replaces state with the value its reducer was built with, and renders
// nothing. It declares no dirty test, so every comparison below is the
// registry's own fallback.
type hostile[S any] struct {
	registration widget.Registration
	mounted      S
	reduced      S
}

func (widgetUnderTest hostile[S]) Register() widget.Registration {
	return widgetUnderTest.registration
}

func (widgetUnderTest hostile[S]) Mount(
	_ context.Context, _ live.Session[live.AnonymousIdentity],
) (S, []live.Effect[live.AnonymousIdentity], error) {
	return widgetUnderTest.mounted, nil, nil
}

func (widgetUnderTest hostile[S]) Reduce(_ S, _ live.Event) (S, []live.Effect[live.AnonymousIdentity]) {
	return widgetUnderTest.reduced, nil
}

func (widgetUnderTest hostile[S]) Render(_ S) templ.Component { return templ.NopComponent }

func (widgetUnderTest hostile[S]) Unmount(_ context.Context, _ live.Session[live.AnonymousIdentity], _ S) {
}

func (widgetUnderTest hostile[S]) Snapshot(_ S) widget.Snapshot {
	return widget.Snapshot{Widget: widgetUnderTest.registration.Name}
}

// hostileOptions is the minimum posture LiveConfig accepts. None of these specs
// reaches a security hook; they exist because a nil one is refused.
func hostileOptions() widget.MountOptions[live.AnonymousIdentity] {
	return widget.MountOptions[live.AnonymousIdentity]{
		Origins:      []string{"http://127.0.0.1"},
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
	}
}

// mounted is one widget's host, after its mount: the fragment whose dirty test
// and render these specs drive, the state a session would hold, and the way to
// get a second state of the right shape.
//
// The second state has to come from the configuration's own Reduce, because
// HostState is opaque — deliberately, since a spec that built one by hand would
// be asserting about a value the registry cannot produce.
type mounted struct {
	fragment live.Fragment[widget.HostState]
	state    widget.HostState
	reduce   func(state widget.HostState) widget.HostState
}

// mountOne registers one widget, builds its configuration and runs its mount.
func mountOne[S any](instance widget.IWidget[S, live.AnonymousIdentity]) mounted {
	GinkgoHelper()

	registry := widget.NewRegistry[live.AnonymousIdentity]()
	Expect(widget.Register(registry, instance)).To(Succeed())
	config, configError := registry.LiveConfig(hostileOptions())
	Expect(configError).ToNot(HaveOccurred())

	state, _, mountError := config.Init(context.Background(), live.Session[live.AnonymousIdentity]{})
	Expect(mountError).ToNot(HaveOccurred())
	return mounted{
		fragment: config.Fragments[0],
		state:    state,
		reduce: func(state widget.HostState) widget.HostState {
			next, _ := config.Reduce(state, live.Event{Name: "widget.hostile.ping"})
			return next
		},
	}
}

func hostileRegistration() widget.Registration {
	return widget.Registration{
		Name:   "Hostile",
		Region: "widget.hostile",
		Events: []string{"widget.hostile.ping"},
	}
}

var _ = Describe("The erasure site, over state types Go cannot compare", func() {
	// The four shapes `==` refuses at runtime, plus the two that compare fine
	// and are here as the control: a spec that only drove uncomparable types
	// could not tell "DeepEqual is used" from "nothing is compared at all".
	It("compares a map state without panicking, and reports the change", func() {
		instance := hostile[map[string]int]{
			registration: hostileRegistration(),
			mounted:      map[string]int{"voters": 3},
			reduced:      map[string]int{"voters": 5},
		}
		host := mountOne[map[string]int](instance)

		var moved widget.HostState
		Expect(func() { moved = host.reduce(host.state) }).ToNot(Panic())
		Expect(host.fragment.Dirty(host.state, moved)).To(BeTrue())
		Expect(host.fragment.Dirty(host.state, host.state)).To(BeFalse(),
			"two equal maps are not a change, which `==` could not have answered at all")
	})

	It("compares a slice state without panicking", func() {
		instance := hostile[[]string]{
			registration: hostileRegistration(),
			mounted:      []string{"a"},
			reduced:      []string{"a", "b"},
		}
		host := mountOne[[]string](instance)
		moved := host.reduce(host.state)

		Expect(host.fragment.Dirty(host.state, moved)).To(BeTrue())
		Expect(host.fragment.Dirty(host.state, host.state)).To(BeFalse())
	})

	It("compares a state carrying a channel and a function without panicking", func() {
		// The shape a real widget reaches this way: a struct whose fields are
		// mostly comparable and one of which is not. `==` on the erased pair
		// panics on the whole struct, not on the field.
		type carrier struct {
			Term    uint64
			Updates chan int
			Format  func(term uint64) string
		}
		updates := make(chan int)
		instance := hostile[carrier]{
			registration: hostileRegistration(),
			mounted:      carrier{Term: 1, Updates: updates},
			reduced:      carrier{Term: 2, Updates: updates},
		}
		host := mountOne[carrier](instance)
		moved := host.reduce(host.state)

		Expect(func() { host.fragment.Dirty(host.state, moved) }).ToNot(Panic())
		Expect(host.fragment.Dirty(host.state, moved)).To(BeTrue())
	})

	It("renders a pointer state that is nil, rather than asserting it away", func() {
		// A pointer S whose zero value is nil is the case where "the assertion
		// yields the zero S" and "the assertion failed" produce the same value,
		// so the only way to tell them apart is that the phase still runs.
		type view struct{ Term uint64 }
		instance := hostile[*view]{registration: hostileRegistration()}
		host := mountOne[*view](instance)

		Expect(host.state.Len()).To(Equal(1))
		Expect(func() { host.fragment.Render(host.state) }).ToNot(Panic())
		Expect(host.fragment.Dirty(host.state, host.state)).To(BeFalse(),
			"a nil pointer has not moved")
		Expect(host.fragment.Dirty(widget.HostState{}, host.state)).To(BeFalse(),
			"a state from before this widget mounted is the zero *view, which is "+
				"the same nil the mount produced — so nothing moved")
	})

	It("agrees with a declared dirty test about the transition out of a pre-mount state", func() {
		// The fallback comparison and the declaring branch answer one question,
		// so they have to answer it the same way. The pre-mount case is where
		// they can differ: `nil` is what a HostState carries where a widget's
		// state has not been put yet, and `nil` is deeply equal to no zero value
		// of any type — not a pointer's nil, and not an `int`'s 0.
		//
		// The fallback is the safe direction either way, so the cost of the two
		// disagreeing is a patch nobody needed rather than a region that stops
		// updating. It is still two answers to one question.
		instance := hostile[int]{registration: hostileRegistration(), mounted: 0}
		host := mountOne[int](instance)

		Expect(host.fragment.Dirty(widget.HostState{}, host.state)).To(BeFalse(),
			"the widget mounted the zero int, and a pre-mount state is the zero int")
		Expect(host.fragment.Dirty(widget.HostState{}, widget.HostState{})).To(BeFalse())
	})

	It("hands a state from before the mount to the widget as the zero S, not as nil", func() {
		// The one non-trivial case the erasure comment names. `int` is used
		// because its zero value is distinguishable from nil, which a pointer's
		// is not.
		seen := []int{}
		instance := observing[int]{
			hostile: hostile[int]{registration: hostileRegistration(), mounted: 7},
			observe: func(state int) { seen = append(seen, state) },
		}
		host := mountOne[int](instance)

		Expect(func() { host.fragment.Render(widget.HostState{}) }).ToNot(Panic())
		Expect(seen).To(Equal([]int{0}))
	})
})

// observing is a hostile widget that reports the state its render was handed.
type observing[S any] struct {
	hostile[S]
	observe func(state S)
}

func (instance observing[S]) Render(state S) templ.Component {
	instance.observe(state)
	return templ.NopComponent
}

var _ = Describe("A registry holding two widgets of one state type", func() {
	// The registry's own comment is about heterogeneity, and the suite's two
	// widgets are `int` and `string` because of it. The homogeneous case is the
	// one a real host reaches first — two cards generated from two documents
	// are two packages with two identical-looking state structs — and nothing
	// asserted that the registry keeps them apart, which it does by index
	// rather than by type.
	twins := func() (*widget.Registry[live.AnonymousIdentity], hostile[int], hostile[int]) {
		GinkgoHelper()
		first := hostile[int]{
			registration: widget.Registration{
				Name: "First", Region: "widget.first", Events: []string{"widget.first.ping"},
			},
			mounted: 11, reduced: 12,
		}
		second := hostile[int]{
			registration: widget.Registration{
				Name: "Second", Region: "widget.second", Events: []string{"widget.second.ping"},
			},
			mounted: 21, reduced: 22,
		}
		registry := widget.NewRegistry[live.AnonymousIdentity]()
		Expect(widget.Register[int, live.AnonymousIdentity](registry, first)).To(Succeed())
		Expect(widget.Register[int, live.AnonymousIdentity](registry, second)).To(Succeed())
		return registry, first, second
	}

	It("registers one concrete widget type twice under two identities", func() {
		registry, _, _ := twins()

		names := []string{}
		for _, registration := range registry.List() {
			names = append(names, registration.Name)
		}
		Expect(names).To(Equal([]string{"First", "Second"}))
	})

	It("routes each event to its own entry rather than to the first of the type", func() {
		registry, _, _ := twins()
		config, configError := registry.LiveConfig(hostileOptions())
		Expect(configError).ToNot(HaveOccurred())
		mounted, _, mountError := config.Init(context.Background(), live.Session[live.AnonymousIdentity]{})
		Expect(mountError).ToNot(HaveOccurred())

		moved, _ := config.Reduce(mounted, live.Event{Name: "widget.second.ping"})

		Expect(config.Fragments[0].Dirty(mounted, moved)).To(BeFalse(),
			"the first widget's state must not move when the second's event arrives")
		Expect(config.Fragments[1].Dirty(mounted, moved)).To(BeTrue())
	})

	It("hands a typed lookup the entry filed under the name it asked for", func() {
		registry, first, second := twins()

		found, matches := widget.LookupWidget[int, live.AnonymousIdentity](registry, "Second")
		Expect(matches).To(BeTrue())
		Expect(found.Register().Name).To(Equal(second.Register().Name))
		Expect(found.Register().Name).ToNot(Equal(first.Register().Name))
	})
})

var _ = Describe("Wire-name collisions across two widgets", func() {
	// `Registration.wireNames` puts Events and Internal in one namespace, and
	// the registry's byWire index is what routes an event to a widget. All four
	// directions of a collision are one fault, and each one of them is an event
	// delivered to the wrong widget — so each is asserted rather than trusting
	// that one loop covers the pairs.
	collide := func(firstEvents, firstInternal, secondEvents, secondInternal []string) error {
		GinkgoHelper()
		registry := widget.NewRegistry[live.AnonymousIdentity]()
		Expect(widget.Register[int, live.AnonymousIdentity](registry, hostile[int]{registration: widget.Registration{
			Name: "First", Region: "widget.first",
			Events: firstEvents, Internal: firstInternal,
		}})).To(Succeed())
		return widget.Register[int, live.AnonymousIdentity](registry, hostile[int]{registration: widget.Registration{
			Name: "Second", Region: "widget.second",
			Events: secondEvents, Internal: secondInternal,
		}})
	}

	DescribeTable("is refused whichever half of each registration declares the name",
		func(firstEvents, firstInternal, secondEvents, secondInternal []string) {
			Expect(collide(firstEvents, firstInternal, secondEvents, secondInternal)).
				To(MatchError(widget.ErrDuplicateEvent))
		},
		Entry("browser-sendable against browser-sendable",
			[]string{"shared"}, nil, []string{"shared"}, nil),
		Entry("browser-sendable against internal",
			[]string{"shared"}, nil, []string(nil), []string{"shared"}),
		Entry("internal against browser-sendable",
			[]string(nil), []string{"shared"}, []string{"shared"}, nil),
		Entry("internal against internal",
			[]string(nil), []string{"shared"}, []string(nil), []string{"shared"}),
	)

	It("takes two widgets whose names differ, so the check is the name and not the count", func() {
		Expect(collide([]string{"first"}, nil, []string{"second"}, nil)).To(Succeed())
	})
})
