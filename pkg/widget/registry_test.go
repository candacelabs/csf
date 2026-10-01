package widget_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	"github.com/a-h/templ"
	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/patience"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/pkg/widget/internal/mocks"
)

var connectionBudget = patience.Budget{Within: 10 * time.Second}

var _ = Describe("Widget connection ownership", func() {
	DescribeTable("shares one connection independently of widgets and effect workers", func(widgets, workers int) {
		registry := widget.NewRegistry[live.AnonymousIdentity]()
		controller := gomock.NewController(GinkgoT())
		var started, stopped atomic.Int64
		for index := range widgets {
			addConnectionWidget(controller, registry, index, workers, &started, &stopped)
		}
		config, err := registry.LiveConfig(widget.MountOptions[live.AnonymousIdentity]{
			Origins: []string{live.AnyOrigin}, Authenticate: live.Anonymous,
			Authorize: live.AllowAll[live.AnonymousIdentity], CSRF: live.NoCSRFCheck,
		})
		Expect(err).NotTo(HaveOccurred())
		app, err := live.New(config)
		Expect(err).NotTo(HaveOccurred())
		server := httptest.NewServer(app.Handler())
		DeferCleanup(server.Close)
		ctx, cancel := context.WithTimeout(context.Background(), connectionBudget.Within)
		DeferCleanup(cancel)
		connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"),
			&websocket.DialOptions{Subprotocols: []string{"gotth-live.v1"}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = connection.CloseNow() })
		_, _, err = connection.Read(ctx)
		Expect(err).NotTo(HaveOccurred())
		patience.Await(GinkgoT(), "widget effects started", connectionBudget, started.Load,
			func(count int64) bool { return count == int64(widgets*workers) })
		Expect(app.ActiveConnections()).To(Equal(1))
		Expect(connection.CloseNow()).To(Succeed())
		Expect(app.Close(ctx)).To(Succeed())
		Expect(app.ActiveConnections()).To(BeZero())
		Expect(stopped.Load()).To(Equal(started.Load()))
	}, Entry("date widget", 1, 0), Entry("many widgets", 32, 0),
		Entry("one widget with a thousand workers", 1, 1000), Entry("many widgets with workers", 32, 1))
})

func addConnectionWidget(controller *gomock.Controller, registry *widget.Registry[live.AnonymousIdentity], index, workers int, started, stopped *atomic.Int64) {
	GinkgoHelper()
	instance := stub[int](controller, registrationFor(fmt.Sprintf("Widget%d", index),
		fmt.Sprintf("widget.%d", index), fmt.Sprintf("widget.%d.ping", index)))
	effects := make([]live.Effect[live.AnonymousIdentity], workers)
	for worker := range workers {
		effects[worker] = live.Effect[live.AnonymousIdentity]{Source: fmt.Sprintf("worker.%d", worker),
			Run: func(ctx context.Context, peer live.Session[live.AnonymousIdentity], emit live.Emitter) error {
				started.Add(1)
				defer stopped.Add(1)
				<-ctx.Done()
				return ctx.Err()
			}}
	}
	instance.EXPECT().Mount(gomock.Any(), gomock.Any()).Return(index, effects, nil)
	instance.EXPECT().Render(index).Return(templ.Raw("<span>widget</span>"))
	instance.EXPECT().Unmount(gomock.Any(), gomock.Any(), index)
	Expect(widget.Register(registry, instance)).To(Succeed())
}

// namedEffect is an effect that does nothing under a given name. A name is the
// whole of what these specs assert about one, and since live.Effect[live.AnonymousIdentity] became a
// concrete struct that is also the whole of what they CAN assert: Run is a
// function value, and Go compares two of those only when both are nil.
func namedEffect(name string) live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: name,
		Run: func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			return nil
		},
	}
}

// render draws a component to text. Two templ components are compared through
// the markup they produce rather than by identity: a component is a function
// value, and function values are never equal to each other.
func render(component templ.Component) string {
	markup := &strings.Builder{}
	ExpectWithOffset(1, component.Render(context.Background(), markup)).To(Succeed())
	return markup.String()
}

// stub returns a widget of state type S that answers Register and nothing else;
// every spec adds the expectations for the phases it drives.
//
// The two widgets a spec registers below have deliberately different state
// types — int and string — because that is the heterogeneity the registry
// exists to hold, and a registry of two widgets with one state type would prove
// nothing about it.
func stub[S any](controller *gomock.Controller, registration widget.Registration) *mocks.MockIWidget[S, live.AnonymousIdentity] {
	instance := mocks.NewMockIWidget[S, live.AnonymousIdentity](controller)
	instance.EXPECT().Register().Return(registration).AnyTimes()
	return instance
}

// dirtyDeclaring is a widget that declares its own dirty test: the generated
// mock of the contract, plus the one optional method [widget.IDirtyDeclarer]
// adds. It is written here rather than generated because mockgen's directive
// names IWidget alone, and the toolchain container gen.sh runs in deliberately
// carries no mockgen — see generate.go.
type dirtyDeclaring struct {
	*mocks.MockIWidget[int, live.AnonymousIdentity]
	dirty func(previous int, next int) bool
}

func (instance dirtyDeclaring) Dirty(previous int, next int) bool {
	return instance.dirty(previous, next)
}

// registrationFor builds the registration of one widget in a two-widget host:
// distinct name, distinct region, distinct wire names.
func registrationFor(name, region, event string) widget.Registration {
	return widget.Registration{
		Name:   name,
		Region: region,
		Events: []string{event},
	}
}

var _ = Describe("Registry", func() {
	var (
		controller *gomock.Controller
		registry   *widget.Registry[live.AnonymousIdentity]
		alpha      *mocks.MockIWidget[int, live.AnonymousIdentity]
		beta       *mocks.MockIWidget[string, live.AnonymousIdentity]
	)

	BeforeEach(func() {
		controller = gomock.NewController(GinkgoT())
		registry = widget.NewRegistry[live.AnonymousIdentity]()
		alpha = stub[int](controller, registrationFor("Alpha", "widget.alpha", "widget.alpha.ping"))
		beta = stub[string](controller, registrationFor("Beta", "widget.beta", "widget.beta.ping"))
	})

	Describe("Register", func() {
		It("takes two widgets that collide over nothing, and over nothing includes their state types", func() {
			Expect(widget.Register(registry, alpha)).To(Succeed())
			Expect(widget.Register(registry, beta)).To(Succeed())
		})

		It("refuses a nil widget rather than panicking at the first session", func() {
			Expect(widget.Register[int, live.AnonymousIdentity](registry, nil)).To(MatchError(widget.ErrEmptyName))
		})

		It("refuses a registration that is already invalid on its own", func() {
			Expect(widget.Register(registry, stub[int](controller, widget.Registration{Name: "Gamma"}))).
				To(MatchError(widget.ErrInvalidRegion))
		})

		It("refuses two widgets claiming one name", func() {
			Expect(widget.Register(registry, alpha)).To(Succeed())
			twin := stub[int](controller, registrationFor("Alpha", "widget.twin", "widget.twin.ping"))

			Expect(widget.Register(registry, twin)).To(MatchError(widget.ErrDuplicateName))
		})

		It("refuses two widgets claiming one live region, which would be a region that stops updating", func() {
			Expect(widget.Register(registry, alpha)).To(Succeed())
			twin := stub[int](controller, registrationFor("Twin", "widget.alpha", "widget.twin.ping"))

			registrationError := widget.Register(registry, twin)

			Expect(registrationError).To(MatchError(widget.ErrDuplicateRegion))
			Expect(registrationError.Error()).To(ContainSubstring("Alpha"))
		})

		It("refuses two widgets claiming one wire name, which would be an event delivered to the wrong widget", func() {
			Expect(widget.Register(registry, alpha)).To(Succeed())
			twin := stub[int](controller, registrationFor("Twin", "widget.twin", "widget.alpha.ping"))

			Expect(widget.Register(registry, twin)).To(MatchError(widget.ErrDuplicateEvent))
		})

		It("refuses a collision against an internal name too, because both are routed", func() {
			registration := registrationFor("Alpha", "widget.alpha", "widget.alpha.ping")
			registration.Internal = []string{"widget.alpha.sync"}
			Expect(widget.Register(registry, stub[int](controller, registration))).To(Succeed())

			twin := stub[int](controller, registrationFor("Twin", "widget.twin", "widget.alpha.sync"))

			Expect(widget.Register(registry, twin)).To(MatchError(widget.ErrDuplicateEvent))
		})

		It("leaves the registry unchanged when it refuses", func() {
			Expect(widget.Register(registry, alpha)).To(Succeed())
			Expect(widget.Register(registry,
				stub[int](controller, registrationFor("Twin", "widget.alpha", "widget.twin.ping")))).
				To(HaveOccurred())

			Expect(registry.List()).To(HaveLen(1))
			_, present := registry.Lookup("Twin")
			Expect(present).To(BeFalse())
		})
	})

	Describe("MustRegister", func() {
		It("registers a sound widget", func() {
			Expect(func() { widget.MustRegister(registry, alpha) }).ToNot(Panic())
			Expect(registry.List()).To(HaveLen(1))
		})

		It("panics with the error Register would have returned", func() {
			Expect(func() {
				widget.MustRegister(registry, stub[int](controller, widget.Registration{Name: "Gamma"}))
			}).To(PanicWith(MatchError(widget.ErrInvalidRegion)))
		})
	})

	Describe("List and Lookup", func() {
		BeforeEach(func() {
			Expect(widget.Register(registry, alpha)).To(Succeed())
			Expect(widget.Register(registry, beta)).To(Succeed())
		})

		It("lists registrations in registration order", func() {
			Expect(registry.List()).To(HaveLen(2))
			Expect(registry.List()[0].Name).To(Equal("Alpha"))
			Expect(registry.List()[1].Name).To(Equal("Beta"))
		})

		It("hands out a copy, so a caller cannot reorder what the host renders", func() {
			listed := registry.List()
			listed[0], listed[1] = listed[1], listed[0]

			Expect(registry.List()[0].Name).To(Equal("Alpha"))
		})

		It("finds a registration by name", func() {
			found, present := registry.Lookup("Beta")

			Expect(present).To(BeTrue())
			Expect(found.Region).To(Equal("widget.beta"))
		})

		It("reports an unregistered name rather than returning a zero registration", func() {
			_, present := registry.Lookup("Gamma")

			Expect(present).To(BeFalse())
		})

		It("hands back a typed widget to a caller that knows its state type", func() {
			found, present := widget.LookupWidget[string, live.AnonymousIdentity](registry, "Beta")

			Expect(present).To(BeTrue())
			Expect(found).To(BeIdenticalTo(widget.IWidget[string, live.AnonymousIdentity](beta)))
		})

		It("refuses a typed lookup asking for the wrong state type, rather than panicking", func() {
			// Beta's state is a string. Asking for its widget as an IWidget[int]
			// is a question with no answer, and answering it would hand a caller
			// a widget whose every method would fail on the first call.
			found, present := widget.LookupWidget[int, live.AnonymousIdentity](registry, "Beta")

			Expect(present).To(BeFalse())
			Expect(found).To(BeNil())
		})
	})

	Describe("LiveConfig", func() {
		var (
			ctx     context.Context
			session live.Session[live.AnonymousIdentity]
			options widget.MountOptions[live.AnonymousIdentity]
		)

		BeforeEach(func() {
			ctx = context.Background()
			session = live.Session[live.AnonymousIdentity]{}
			options = widget.MountOptions[live.AnonymousIdentity]{
				Origins:      []string{live.AnyOrigin},
				Authenticate: live.Anonymous,
				Authorize:    live.AllowAll[live.AnonymousIdentity],
				CSRF:         live.NoCSRFCheck,
			}
		})

		It("refuses a host with nothing on it, which would serve a page with no live region", func() {
			_, configError := registry.LiveConfig(options)

			Expect(configError).To(MatchError(widget.ErrNoWidgets))
		})

		Context("with two widgets registered", func() {
			var config live.Config[widget.HostState, live.AnonymousIdentity]

			BeforeEach(func() {
				Expect(widget.Register(registry, alpha)).To(Succeed())
				Expect(widget.Register(registry, beta)).To(Succeed())
			})

			// mountBoth arms both widgets' mount phase and builds the config.
			mountBoth := func() widget.HostState {
				alpha.EXPECT().Mount(gomock.Any(), gomock.Any()).Return(1, nil, nil)
				beta.EXPECT().Mount(gomock.Any(), gomock.Any()).Return("b", nil, nil)

				var configError error
				config, configError = registry.LiveConfig(options)
				Expect(configError).ToNot(HaveOccurred())

				state, _, initError := config.Init(ctx, session)
				Expect(initError).ToNot(HaveOccurred())
				return state
			}

			It("declares one fragment per widget, in registration order, identified by its region", func() {
				var configError error
				config, configError = registry.LiveConfig(options)

				Expect(configError).ToNot(HaveOccurred())
				Expect(config.Fragments).To(HaveLen(2))
				Expect(config.Fragments[0].ID).To(Equal("widget.alpha"))
				Expect(config.Fragments[1].ID).To(Equal("widget.beta"))
			})

			It("registers the union of the browser-sendable names and no internal one", func() {
				registry = widget.NewRegistry[live.AnonymousIdentity]()
				withInternal := registrationFor("Alpha", "widget.alpha", "widget.alpha.ping")
				withInternal.Internal = []string{"widget.alpha.sync"}
				Expect(widget.Register(registry, stub[int](controller, withInternal))).To(Succeed())
				Expect(widget.Register(registry, beta)).To(Succeed())

				config, _ = registry.LiveConfig(options)

				Expect(config.Events).To(Equal([]string{"widget.alpha.ping", "widget.beta.ping"}))
			})

			It("still routes an internal name to its own widget, which is what leaving it unregistered costs nothing", func() {
				// Registration is what makes a name sendable by a browser;
				// routing is a separate question, and an internal name has to
				// keep its answer or a widget's own stream could not deliver to
				// it. This is the half of the split that is easy to break.
				registry = widget.NewRegistry[live.AnonymousIdentity]()
				withInternal := registrationFor("Alpha", "widget.alpha", "widget.alpha.ping")
				withInternal.Internal = []string{"widget.alpha.sync"}
				internallyDelivered := stub[int](controller, withInternal)
				Expect(widget.Register(registry, internallyDelivered)).To(Succeed())
				internallyDelivered.EXPECT().Mount(gomock.Any(), gomock.Any()).Return(1, nil, nil)
				config, _ = registry.LiveConfig(options)
				state, _, _ := config.Init(ctx, session)

				internallyDelivered.EXPECT().Reduce(1, gomock.Any()).Return(2, nil)

				config.Reduce(state, live.Event{Name: "widget.alpha.sync"})
			})

			It("is a configuration the live library accepts", func() {
				alpha.EXPECT().Mount(gomock.Any(), gomock.Any()).Return(1, nil, nil).AnyTimes()
				beta.EXPECT().Mount(gomock.Any(), gomock.Any()).Return("b", nil, nil).AnyTimes()
				config, _ = registry.LiveConfig(options)

				app, newError := live.New(config)

				Expect(newError).ToNot(HaveOccurred())
				Expect(app).ToNot(BeNil())
			})

			Describe("the mount phase", func() {
				It("mounts every widget and keeps its state at its own index", func() {
					state := mountBoth()

					Expect(state.Len()).To(Equal(2))

					alpha.EXPECT().Snapshot(1).Return(widget.Snapshot{Widget: "Alpha"})
					beta.EXPECT().Snapshot("b").Return(widget.Snapshot{Widget: "Beta"})
					Expect(registry.Snapshots(state)).To(Equal([]widget.Snapshot{
						{Widget: "Alpha"}, {Widget: "Beta"},
					}))
				})

				It("carries the host's own startup effects ahead of the widgets'", func() {
					options.Init = func(ctx context.Context, session live.Session[live.AnonymousIdentity]) ([]live.Effect[live.AnonymousIdentity], error) {
						return []live.Effect[live.AnonymousIdentity]{namedEffect("host.open")}, nil
					}
					alpha.EXPECT().Mount(gomock.Any(), gomock.Any()).
						Return(1, []live.Effect[live.AnonymousIdentity]{namedEffect("alpha.watch")}, nil)
					beta.EXPECT().Mount(gomock.Any(), gomock.Any()).Return("b", nil, nil)
					config, _ = registry.LiveConfig(options)

					_, effects, initError := config.Init(ctx, session)

					Expect(initError).ToNot(HaveOccurred())
					Expect(effects).To(HaveLen(2))
					Expect(effects[0].Source).To(Equal("host.open"))
					Expect(effects[1].Source).To(Equal("alpha.watch"))
				})

				It("reports a widget that could not mount, naming it", func() {
					alpha.EXPECT().Mount(gomock.Any(), gomock.Any()).
						Return(0, nil, errors.New("no source"))
					config, _ = registry.LiveConfig(options)

					_, _, initError := config.Init(ctx, session)

					Expect(initError).To(MatchError(ContainSubstring("Alpha")))
					Expect(initError).To(MatchError(ContainSubstring("no source")))
				})

				It("reports a host that could not start, before any widget mounts", func() {
					options.Init = func(ctx context.Context, session live.Session[live.AnonymousIdentity]) ([]live.Effect[live.AnonymousIdentity], error) {
						return nil, errors.New("no data plane")
					}
					config, _ = registry.LiveConfig(options)

					_, _, initError := config.Init(ctx, session)

					Expect(initError).To(MatchError("no data plane"))
				})
			})

			Describe("the event phase", func() {
				It("routes an event to the widget whose region raised it", func() {
					state := mountBoth()
					beta.EXPECT().Reduce("b", gomock.Any()).Return("raised", nil)

					next, _ := config.Reduce(state, live.Event{FragmentID: "widget.beta", Name: "anything"})

					beta.EXPECT().Snapshot("raised").Return(widget.Snapshot{Widget: "Beta"})
					alpha.EXPECT().Snapshot(1).Return(widget.Snapshot{Widget: "Alpha"})
					Expect(registry.Snapshots(next)).To(HaveLen(2))
				})

				It("routes an event by its wire name when no region named a widget", func() {
					state := mountBoth()
					alpha.EXPECT().Reduce(1, gomock.Any()).Return(2, nil)

					config.Reduce(state, live.Event{Name: "widget.alpha.ping"})
				})

				It("delivers a library-minted event to every widget, because a failure is not a phase", func() {
					state := mountBoth()
					alpha.EXPECT().Reduce(1, gomock.Any()).Return(2, nil)
					beta.EXPECT().Reduce("b", gomock.Any()).Return("c", nil)

					config.Reduce(state, live.Event{Name: live.EffectFailedEvent})
				})

				It("leaves the state it was given untouched, so a panicking reducer costs nothing", func() {
					state := mountBoth()
					alpha.EXPECT().Reduce(1, gomock.Any()).Return(99, nil)

					config.Reduce(state, live.Event{Name: "widget.alpha.ping"})

					alpha.EXPECT().Snapshot(1).Return(widget.Snapshot{Widget: "Alpha"})
					beta.EXPECT().Snapshot(gomock.Any()).Return(widget.Snapshot{Widget: "Beta"})
					Expect(registry.Snapshots(state)[0].Widget).To(Equal("Alpha"))
				})
			})

			// The effect phase has no registry code left to specify, and that
			// is the finding rather than a gap. Routing an effect back to its
			// owner, the host's fallback executor and ErrHostEffect all existed
			// because an effect was an opaque interface value somebody had to
			// match on; a live.Effect[live.AnonymousIdentity] carries its own Run, so a widget's effect
			// performs the widget's closure and a host's performs the host's,
			// with nothing in between to get wrong. What survives is that the
			// registry passes effects through unchanged, which the mount and
			// event phases above already assert.
			Describe("the effect phase", func() {
				It("passes a widget's effect through under its own name", func() {
					state := mountBoth()
					alpha.EXPECT().Reduce(gomock.Any(), gomock.Any()).
						Return(2, []live.Effect[live.AnonymousIdentity]{namedEffect("alpha.watch")})

					_, effects := config.Reduce(state, live.Event{Name: "widget.alpha.ping"})
					Expect(effects).To(HaveLen(1))
					Expect(effects[0].Source).To(Equal("alpha.watch"))
					Expect(effects[0].Run(ctx, session, nil)).To(Succeed())
				})
			})

			Describe("the render phase", func() {
				It("renders each widget into its own region, from that widget's own state", func() {
					state := mountBoth()
					alpha.EXPECT().Render(1).Return(templ.Raw("<p>alpha</p>"))

					Expect(render(config.Fragments[0].Render(state))).To(Equal("<p>alpha</p>"))
				})

				It("declares a region dirty when its own widget's state moved", func() {
					state := mountBoth()
					moved := widget.HostState{}
					alpha.EXPECT().Reduce(gomock.Any(), gomock.Any()).Return(2, nil)
					moved, _ = config.Reduce(state, live.Event{Name: "widget.alpha.ping"})

					// Neither of these widgets declares a dirty test, so this is
					// the whole-state fallback: safe, and what a hand-written
					// widget gets for free.
					_, declares := widget.IWidget[int, live.AnonymousIdentity](alpha).(widget.IDirtyDeclarer[int])
					Expect(declares).To(BeFalse())
					Expect(config.Fragments[0].Dirty(state, moved)).To(BeTrue())
					Expect(config.Fragments[1].Dirty(state, moved)).To(BeFalse())
				})

				It("asks a widget that declares its own dirty test, and hands it only its own state", func() {
					asked := [][2]int{}
					declaring := dirtyDeclaring{
						MockIWidget: alpha,
						dirty: func(previous int, next int) bool {
							asked = append(asked, [2]int{previous, next})
							return false
						},
					}
					alpha.EXPECT().Mount(gomock.Any(), gomock.Any()).Return(1, nil, nil)
					declaringRegistry := widget.NewRegistry[live.AnonymousIdentity]()
					Expect(widget.Register[int, live.AnonymousIdentity](declaringRegistry, declaring)).To(Succeed())
					declaringConfig, configError := declaringRegistry.LiveConfig(options)
					Expect(configError).ToNot(HaveOccurred())
					mounted, _, initError := declaringConfig.Init(ctx, session)
					Expect(initError).ToNot(HaveOccurred())

					// The two states differ, so the whole-state fallback would
					// have reported dirty; only the widget's own declaration can
					// answer otherwise, which is how this spec tells them apart.
					Expect(declaringConfig.Fragments[0].Dirty(mounted, widget.HostState{})).To(BeFalse())

					// The zero int is the second entry, and it is the audited
					// assertion's one non-trivial case: a HostState from before
					// this widget mounted carries no entry for it, and the zero
					// state is what the widget is handed rather than a panic.
					Expect(asked).To(Equal([][2]int{{1, 0}}))
				})

				It("renders a state no session has mounted rather than panicking on it", func() {
					config, _ = registry.LiveConfig(options)
					alpha.EXPECT().Render(0).Return(templ.NopComponent)

					Expect(config.Fragments[0].Render(widget.HostState{})).ToNot(BeNil())
				})
			})

			Describe("the unmount phase", func() {
				It("unmounts in reverse registration order, so nothing is torn down under a widget that needs it", func() {
					state := mountBoth()
					gomock.InOrder(
						beta.EXPECT().Unmount(gomock.Any(), gomock.Any(), "b"),
						alpha.EXPECT().Unmount(gomock.Any(), gomock.Any(), 1),
					)

					config.Teardown(ctx, session, state)
				})
			})
		})
	})
})
