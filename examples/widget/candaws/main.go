// Command candaws is the CandaWS fleet: five parody cloud services, five
// engines, five generated widgets, and one binary.
//
// # Monolithic microservices, taken literally
//
// A continuous-delivery pipeline, a message queue, an object store, a
// function-as-a-service runtime and a metrics service. In a real cloud those are
// five products, five control planes and five bills. Here they are five packages
// in one process, and what separates any two of them is what separates two
// goroutines: nothing but the scheduler. There is no per-service port, no
// per-widget connection, and no inter-service call that is not a channel send.
//
// # Running it
//
//	go run ./examples/widget/candaws                  # http://127.0.0.1:8081
//	go run ./examples/widget/candaws -addr :9000
//	go run ./examples/widget/candaws -seed 7          # a different fleet, reproducibly
//	go run ./examples/widget/candaws -pace 0.25       # everything four times faster
//	go run ./examples/widget/candaws -trouble 0       # nothing fails anywhere
//	go run ./examples/widget/candaws -goroutines 5s   # report the scheduler's own census
//
// -pace multiplies every interval in the fleet and -trouble sets every
// probability, so the two flags between them cover the pacing knobs each engine
// exposes without asking anybody to remember five names for one idea. The
// per-service knobs are still there, in each package's Config.
//
// Both flags answer for themselves at their edges. -pace is accepted in
// [0.001, 1000] and refused by name outside it, rather than passed on to become
// an engine reporting an interval it never chose. -trouble reads a negative
// value as zero — the fleet where nothing goes wrong — and the banner prints the
// value the fleet is actually running at.
//
// # What is generated and what is not
//
// Nothing on the page was hand-written. The five widget documents in docs/ are
// turned into examples/widget/candaws/<service>/{view.templ,widget.gen.go} by
// pkg/widget/gen.sh — the state, the reducers, the bindings, the labels, the SVG
// scenes, the motion and every word on every card. This file registers them,
// resolves the six sources their declared streams name against the five engines,
// hands the registry the four security decisions a library may not make for a
// host, and serves the result.
//
// Yakshave declares two streams, which is why there are six sources and not
// five: one for its runs and one for its minutes, because the two tickers behind
// them are two.
//
// # Two commands the host acts on
//
// Queuecumber's redrive button and Coldstart's prewarm button emit events that
// change no widget state at all. The registry routes them to their widgets,
// whose reducers do nothing with them; this host wraps the registry's reducer
// and schedules an effect of its own, which is where a redrive and a prewarm
// actually happen. That is the same seam a stream's source name is: the document
// declares that the event exists, and the host decides what it means.
//
// # Security posture
//
// live.Anonymous, live.AllowAll[live.AnonymousIdentity] and live.NoCSRFCheck, each because a
// single-page demo has no accounts to check against. Origins is a real allowlist
// derived from the listen address rather than live.AnyOrigin.
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/examples/widget/candaws/blobfish"
	"github.com/candacelabs/csf/examples/widget/candaws/coldstart"
	"github.com/candacelabs/csf/examples/widget/candaws/dashbored"
	"github.com/candacelabs/csf/examples/widget/candaws/queuecumber"
	"github.com/candacelabs/csf/examples/widget/candaws/yakshave"
	"github.com/candacelabs/csf/examples/widget/hosting"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
)

// mountPath is where the live handler is mounted. It is one constant because the
// router and the runtime script tag in page.templ must agree on it, and a
// disagreement is a script tag that 404s and a page that loads and never updates.
const mountPath = "/live"

// pageStylesheet is this host's own chrome. The widgets' CSS is the SDK's and
// hosting.Serve puts it in front of this, because the token mapping, the scene's
// structure and the motion gate are the widget language's rather than any host's.
//
//go:embed widget.css
var pageStylesheet []byte

func main() {
	if runError := run(); runError != nil {
		fmt.Fprintln(os.Stderr, "candaws:", runError)
		os.Exit(1)
	}
}

func run() error {
	address := flag.String("addr", "127.0.0.1:8081", "address to listen on")
	seed := flag.Int64("seed", 1, "seeds every engine's per-goroutine random streams")
	pace := flag.Float64("pace", 1,
		"multiplies every interval in the fleet, in [0.001, 1000]; below one is a faster fleet")
	trouble := flag.Float64("trouble", 1,
		"scales every failure, drop and breach probability; negative reads as zero, "+
			"and zero is a fleet where nothing goes wrong")
	census := flag.Duration("goroutines", 0,
		"after this long, print how many goroutines the fleet is actually running; zero never does")
	flag.Parse()

	fleet, buildError := build(*seed, *pace, *trouble)
	if buildError != nil {
		return buildError
	}

	// One context for the whole process: every engine, the census and the HTTP
	// server end on the same interrupt, and every engine's goroutines are joined
	// before run returns rather than left racing the exit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	running := fleet.start(ctx)
	if *census > 0 {
		go reportCensus(ctx, *census)
	}

	registry := hostWidgets()
	palette, paletteError := hosting.OnePalette(
		yakshave.YakshavePalette,
		queuecumber.QueuecumberPalette,
		blobfish.BlobfishPalette,
		coldstart.ColdstartPalette,
		dashbored.DashboredPalette,
	)
	if paletteError != nil {
		return paletteError
	}

	config, configError := registry.LiveConfig(widget.MountOptions[live.AnonymousIdentity]{
		Origins:      hosting.BrowserOrigins(*address),
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
		Init:         fleet.sources,
		Dev:          true,
	})
	if configError != nil {
		return configError
	}
	config.Reduce = fleet.commands(config.Reduce)

	app, appError := live.New(config)
	if appError != nil {
		return appError
	}

	// The banner prints the values the fleet is running at rather than the ones
	// that were typed: -trouble -5 builds the same fleet as -trouble 0, and a
	// line saying otherwise would be describing a fleet nobody started. %g for
	// the same reason one step further on — two decimal places rounded the
	// smallest accepted pace to "0.00", which is a number this fleet refuses.
	fmt.Printf("candaws: five services, one binary, pace %g, trouble %g\n",
		*pace, effectiveTrouble(*trouble))
	serveError := hosting.Serve(ctx, *address, app, hosting.Site{
		MountPath: mountPath,
		Palette:   palette,
		Chrome:    pageStylesheet,
		Page: func(state widget.HostState) templ.Component {
			return Page(app, hosting.Regions(config, state))
		},
	})
	if serveError != nil {
		return serveError
	}
	return <-running
}

// hostWidgets is the set of widgets this host serves, in the order their regions
// appear on the page.
//
// Registration order is the whole of what adding a service cost on the widget
// side: one line here, and no edit to the page at all. It is a function rather
// than a literal in run because a specification asserting on this host's live
// path has to register the same set, and a second literal is a second set the
// day one of them changes.
func hostWidgets() *widget.Registry[live.AnonymousIdentity] {
	registry := widget.NewRegistry[live.AnonymousIdentity]()
	widget.MustRegister(registry, yakshave.NewYakshave[live.AnonymousIdentity]())
	widget.MustRegister(registry, queuecumber.NewQueuecumber[live.AnonymousIdentity]())
	widget.MustRegister(registry, blobfish.NewBlobfish[live.AnonymousIdentity]())
	widget.MustRegister(registry, coldstart.NewColdstart[live.AnonymousIdentity]())
	widget.MustRegister(registry, dashbored.NewDashbored[live.AnonymousIdentity]())
	return registry
}

// reportCensus prints the scheduler's own count of what the fleet is running.
//
// It is the "monolithic microservices" claim made checkable: five services, one
// process, and a number a reader can compare against the goroutines each engine
// documents. It is off by default because a demo that printed a number nobody
// asked for would be a demo with an opinion about its own README.
func reportCensus(ctx context.Context, after time.Duration) {
	settle := time.NewTimer(after)
	defer settle.Stop()
	select {
	case <-ctx.Done():
		return
	case <-settle.C:
	}
	fmt.Printf("candaws: %d goroutines at steady state, %s in\n", runtime.NumGoroutine(), after)
}
