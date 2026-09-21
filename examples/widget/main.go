// Command widget is the widget SDK's smallest end-to-end host: two generated
// widgets, one registry, one page, one binary — and, behind one of them, a real
// consensus protocol.
//
// # Running it
//
//	go run .                       # http://127.0.0.1:8080
//	go run . -addr 127.0.0.1:9000
//	go run . -nodes 5              # a five-node cluster; quorum becomes three
//	go run . -heartbeat 400ms      # a faster protocol, and a faster picture
//	go run . -chaos 0              # elect once and never fail a node again
//
// Nothing on the page was hand-written. examples/widget/clusterheartbeats and
// examples/widget/nodestatus are emitted from the two widget documents in
// pkg/widget/docs/examples by pkg/widget/gen.sh — the state, the reducers, the
// bindings, the labels, the SVG scenes and the motion — and this file registers
// them, hands the registry the four security decisions a library may not make
// for a host, and serves the result.
//
// # What to watch
//
// The raft card is drawing an election that is actually happening. Three
// goroutines in this process hold terms and cast votes at each other over
// channels — examples/widget/raftdemo, Raft's election half with no log — and
// one heartbeat round of that protocol is one snapshot is one round of pulses on
// the card. A pulse crossing an edge is a heartbeat that crossed a channel.
//
// Every -chaos interval the current leader is crashed and, an interval later,
// restarted. The card's motion gate closes while the survivors campaign, its
// indicator turns, its scene's text alternative is rewritten, and the term in
// its stat line goes up by one when somebody wins — none of which is scripted,
// and all of which would look exactly the same if the failure were real. The
// button pauses the pulses, and a viewer who has asked for reduced motion never
// sees them at all. The node card's caption alternates between "reachable" and
// "unreachable" on a timer, because that widget has nothing behind it yet.
//
// None of that behaviour is in this file. Events arrive, the generated reducers
// write state, and the generated bindings decide what everything says.
//
// # The two sources
//
// Each widget declared a stream it cannot open — a widget document names no
// host, no address and no credential, which is what makes one publishable — so
// resolving those names against something real is the host's job. Here
// "widget.cluster.watch" resolves to a subscription on the election running in
// this process and "widget.node-status.watch" resolves to a ticker.
//
// The cluster is one per process and the subscription is one per session, which
// is the right way round: every browser watching is watching the same election
// rather than one private to it.
//
// # Security posture
//
// The three escape hatches are live.Anonymous, live.AllowAll[live.AnonymousIdentity] and
// live.NoCSRFCheck, each because a single-page demo has no accounts to check
// against. Origins is a real allowlist derived from the listen address rather
// than live.AnyOrigin, because that one has a production replacement worth
// showing.
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/examples/widget/clusterheartbeats"
	"github.com/candacelabs/csf/examples/widget/hosting"
	"github.com/candacelabs/csf/examples/widget/nodestatus"
	"github.com/candacelabs/csf/examples/widget/raftdemo"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
)

// mountPath is where the live handler is mounted. It is one constant because
// the router and the runtime script tag in page.templ must agree on it, and a
// disagreement is a script tag that 404s and a page that loads and never
// updates.
const mountPath = "/live"

// pageStylesheet is this host's own chrome. The widgets' CSS is the SDK's and is
// served in front of it by hosting.Serve, because the token mapping, the scene's
// structure and the motion gate are the widget language's rather than any
// host's — a host that kept its own copy would be hand-maintaining the one
// projection the SDK derives.
//
//go:embed widget.css
var pageStylesheet []byte

func main() {
	if runError := run(); runError != nil {
		fmt.Fprintln(os.Stderr, "widget:", runError)
		os.Exit(1)
	}
}

func run() error {
	defaults := raftdemo.DefaultConfig()
	address := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	health := flag.Duration("health-interval", 3*time.Second,
		"how often this host's stand-in health check moves the node card")
	nodes := flag.Int("nodes", defaults.Nodes, "how many nodes the election runs between")
	heartbeat := flag.Duration("heartbeat", defaults.Heartbeat,
		"how often a leader beats, and therefore how often the raft card's pulses travel")
	electionTimeout := flag.Duration("election-timeout", defaults.ElectionTimeout,
		"how long a follower waits to hear from a leader before standing for election")
	electionJitter := flag.Duration("election-jitter", defaults.ElectionJitter,
		"the width of the random interval added to the election timeout, per node, per wait")
	seed := flag.Int64("seed", defaults.Seed, "seeds the per-node election jitter")
	chaos := flag.Duration("chaos", 12*time.Second,
		"how often to crash the current leader, and an interval later restart it; 0 never fails a node")
	flag.Parse()

	cluster, clusterError := raftdemo.New(raftdemo.Config{
		Nodes:           *nodes,
		Heartbeat:       *heartbeat,
		ElectionTimeout: *electionTimeout,
		ElectionJitter:  *electionJitter,
		Seed:            *seed,
	})
	if clusterError != nil {
		return clusterError
	}

	// One context for the whole process: the cluster, the chaos loop and the
	// HTTP server all end on the same interrupt, and the cluster's goroutines
	// are joined before run returns rather than left racing the exit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	elections := make(chan error, 1)
	go func() { elections <- cluster.Run(ctx) }()
	go func() {
		if chaosError := runChaos(ctx, cluster, *chaos); chaosError != nil {
			fmt.Fprintln(os.Stderr, "widget: the chaos loop stopped:", chaosError)
		}
	}()

	registry := hostWidgets()
	palette, paletteError := hosting.OnePalette(
		clusterheartbeats.ClusterHeartbeatsPalette, nodestatus.NodeStatusPalette)
	if paletteError != nil {
		return paletteError
	}

	config, configError := registry.LiveConfig(widget.MountOptions[live.AnonymousIdentity]{
		Origins:      hosting.BrowserOrigins(*address),
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
		Init: func(ctx context.Context, session live.Session[live.AnonymousIdentity]) ([]live.Effect[live.AnonymousIdentity], error) {
			return []live.Effect[live.AnonymousIdentity]{
				clusterSourceEffect(cluster),
				healthSourceEffect(*health),
			}, nil
		},
		Dev: true,
	})
	if configError != nil {
		return configError
	}

	app, appError := live.New(config)
	if appError != nil {
		return appError
	}

	fmt.Printf("widget: %d nodes, a %s heartbeat, elections every %s\n",
		*nodes, *heartbeat, *chaos)
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
	return <-elections
}

// hostWidgets is the set of widgets this host serves, in the order their regions
// appear on the page.
//
// Registration order is the whole of what adding a second widget cost: one line
// here, and no edit to the page at all. It is a function rather than a literal
// in run because a specification asserting on this host's live path has to
// register the same set, and a second literal is a second set the day one of
// them changes.
func hostWidgets() *widget.Registry[live.AnonymousIdentity] {
	registry := widget.NewRegistry[live.AnonymousIdentity]()
	widget.MustRegister(registry, clusterheartbeats.NewClusterHeartbeats[live.AnonymousIdentity]())
	widget.MustRegister(registry, nodestatus.NewNodeStatus[live.AnonymousIdentity]())
	return registry
}

// The two stand-ins for the data plane the widgets' declared streams name. One
// of them is no longer a stand-in.
//
// Each is a constructor returning a concrete live.Effect[live.AnonymousIdentity] rather than a value
// implementing an effect interface, and the source it stamps is what provenance
// carries: "effect:widgetdemo.health_source" on every patch that effect causes.
// There is no host executor beside them any more — an effect performs itself,
// so the switch that used to find the right arm has nothing to decide.

// healthSourceEffect delivers the node card's health check. It is a ticker,
// because that widget has nothing behind it yet.
func healthSourceEffect(interval time.Duration) live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: "widgetdemo.health_source",
		Run: func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			return runHealthSource(ctx, interval, emit)
		},
	}
}

// clusterSourceEffect delivers the raft card's fleet view. It is a subscription
// on the election running in this process: one cluster, one subscription per
// session, so every browser watching watches the same protocol.
func clusterSourceEffect(cluster *raftdemo.Cluster) live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: "widgetdemo.cluster_source",
		Run: func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			return runClusterSource(ctx, cluster, emit)
		},
	}
}

// runHealthSource flips the node's reachability on a timer.
func runHealthSource(ctx context.Context, interval time.Duration, emit live.Emitter) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	reachable := true
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			reachable = !reachable
			// Event.At is left zero: the actor boundary stamps it, and a value
			// set here is rejected rather than silently replaced.
			emitError := emit(live.Event{
				Name:   nodestatus.NodeStatusEventHealth,
				Fields: live.NewFields(map[string]string{"reachable": strconv.FormatBool(reachable)}),
			})
			if emitError != nil {
				// The session is saturated or closing. Returning the error is
				// how this effect learns about backpressure rather than having
				// its event vanish.
				return emitError
			}
		}
	}
}

// runClusterSource forwards one session's view of the election onto the wire.
//
// Everything it does is translation. It decides nothing about what the card
// draws, holds no state of its own, and would look identical if the cluster it
// subscribed to were four processes on four machines rather than three
// goroutines in this one.
func runClusterSource(ctx context.Context, cluster *raftdemo.Cluster, emit live.Emitter) error {
	views, subscribeError := cluster.Subscribe(ctx)
	if subscribeError != nil {
		return subscribeError
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case view, open := <-views:
			if !open {
				// The cluster stopped. The session outlives it and keeps
				// rendering the last view it had, which is what a stream ending
				// looks like from a browser.
				return nil
			}
			emitError := emit(live.Event{
				Name:   clusterheartbeats.ClusterHeartbeatsEventSnapshot,
				Fields: live.NewFields(clusterFields(view)),
			})
			if emitError != nil {
				return emitError
			}
		}
	}
}

// clusterFields is the whole of the seam between the election and the widget.
//
// The widget document declared eight wire field names and the engine minted a
// fleet view; this maps one onto the other and does nothing else. "connected" is
// the one field with no counterpart, because a view arriving *is* the stream
// being alive — the widget's own connection status is about the browser's link
// to this process, which the library owns and neither of these two does.
func clusterFields(view raftdemo.Snapshot) map[string]string {
	return map[string]string{
		"sequence":      strconv.FormatUint(view.Sequence, 10),
		"connected":     "true",
		"authoritative": strconv.FormatBool(view.Authoritative),
		"leader_known":  strconv.FormatBool(view.LeaderKnown),
		"has_quorum":    strconv.FormatBool(view.HasQuorum),
		"term":          strconv.FormatUint(view.Term, 10),
		"voters":        strconv.Itoa(view.Voters),
		"alive_voters":  strconv.Itoa(view.AliveVoters),
	}
}

// runChaos is this host's failure injection: crash the current leader, and an
// interval later start it again.
//
// It exists because a healthy cluster elects once and then never elects again,
// and an election is the thing worth watching. What it injects is a real
// failure — the node stops answering and keeps the term and vote it had — so
// everything the card then shows is the protocol's own reaction rather than a
// script's. Zero disables it, which is how to watch a steady leader beat.
func runChaos(ctx context.Context, cluster *raftdemo.Cluster, every time.Duration) error {
	if every <= 0 {
		return nil
	}

	views, subscribeError := cluster.Subscribe(ctx)
	if subscribeError != nil {
		return subscribeError
	}

	ticker := time.NewTicker(every)
	defer ticker.Stop()

	leader, crashed := "", ""
	for {
		select {
		case <-ctx.Done():
			return nil
		case view, open := <-views:
			if !open {
				return nil
			}
			if view.LeaderKnown {
				leader = view.Leader
			}
		case <-ticker.C:
			// One event per interval, alternating, so the cluster is never
			// losing a node and regaining one in the same instant: crash, watch
			// the survivors elect, restart, watch it rejoin.
			if crashed != "" {
				if recoverError := cluster.Recover(ctx, crashed); recoverError != nil {
					return recoverError
				}
				crashed = ""
				continue
			}
			if leader == "" {
				continue
			}
			if crashError := cluster.Crash(ctx, leader); crashError != nil {
				return crashError
			}
			crashed, leader = leader, ""
		}
	}
}
