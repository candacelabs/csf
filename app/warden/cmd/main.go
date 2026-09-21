// Command warden is the candacenet fleet watchdog daemon. Every node runs the
// same binary: nodes elect a leader (Raft-style, majority quorum), the leader
// monitors peer liveness and emails the operator when a peer dies. A single
// bound port per node serves the node-to-node gRPC WardenService and the HTTP
// surface (SSR dashboard, /api/status, /metrics) multiplexed with cmux.
//
// # Concurrency model (CSP)
//
// The wiring below is deliberately channel-first. Each long-running component
// — the election manager, the watchdog, and the single-port server — owns
// exactly one goroutine and reports its terminal error on a shared buffered
// channel. main selects for the first shutdown trigger (an OS signal cancels
// the context; a component error cancels it too), performs a bounded graceful
// server drain (which ends in-flight streams cleanly and finishes in-flight
// unary RPCs), then drains one terminal error from every launched goroutine
// before returning. There are no mutexes and no shared mutable error slice in
// the wiring: cancellation flows through context, results flow through
// channels, and every goroutine is awaited — no abandoned goroutines.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"github.com/candacelabs/csf/pkg/core"
	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/config"
	"github.com/candacelabs/csf/services/warden/dashboard"
	"github.com/candacelabs/csf/services/warden/discovery"
	"github.com/candacelabs/csf/services/warden/election"
	"github.com/candacelabs/csf/services/warden/grpcmux"
	"github.com/candacelabs/csf/services/warden/grpctransport"
	"github.com/candacelabs/csf/services/warden/httpserver"
	"github.com/candacelabs/csf/services/warden/metrics"
	"github.com/candacelabs/csf/services/warden/notify"
	"github.com/candacelabs/csf/services/warden/store"
	"github.com/candacelabs/csf/services/warden/watchdog"
)

// version is the build version, overridable at link time:
//
//	go build -ldflags "-X main.version=$(git describe --tags --always --dirty)"
var version = "dev"

// shutdownGrace bounds the graceful server shutdown.
const shutdownGrace = 5 * time.Second

const (
	// stateFileName is the election-state file written under data_dir.
	stateFileName = "state.json"
	// envConfig and envLogFormat are the two environment variables main reads
	// directly, outside config.applyEnv: the default -config path and the log
	// output format selector.
	envConfig    = "WARDEN_CONFIG"
	envLogFormat = "WARDEN_LOG_FORMAT"
	// logFormatConsole selects the human-readable console log writer; any other
	// value (including empty) leaves the default JSON writer in place.
	logFormatConsole = "console"
)

func main() {
	os.Exit(run())
}

// run performs the whole daemon lifecycle and returns the process exit code:
// 0 on a clean shutdown, non-zero on a component or shutdown error.
func run() int {
	configPath := flag.String("config", os.Getenv(envConfig),
		"path to the warden YAML config file (default: $WARDEN_CONFIG, else defaults+env only)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return 0
	}

	// Logger: JSON to stdout by default (production); human-readable console
	// only when WARDEN_LOG_FORMAT=console. setupLogger installs the chosen
	// logger as core.Logger so every component emits the same format; the
	// global level is applied below once the config is loaded.
	logger := setupLogger(os.Getenv(envLogFormat))

	cfg, err := config.Load(*configPath, os.Getenv)
	if err != nil {
		logger.Fatal().Err(err).Str("config", *configPath).Msg("loading configuration")
	}
	if err := cfg.Validate(); err != nil {
		logger.Fatal().Err(err).Msg("invalid configuration")
	}

	// Apply the configured log level globally (affects sibling packages that
	// log via core.Logger) and to our local logger.
	level, perr := zerolog.ParseLevel(cfg.LogLevel)
	if perr != nil {
		logger.Warn().Str("log_level", cfg.LogLevel).Msg("unrecognized log level; defaulting to info")
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)
	logger = logger.Level(level)

	self, ok := cfg.Self()
	if !ok {
		// A joiner in a discovery mode ships the SAME config as the fleet but
		// is not yet in the peer/voter seed. Validate has confirmed the mode is
		// dynamic and advertise_addr is routable in that case, so we synthesize
		// Self from it; the node starts as an observer until the leader admits
		// it. (In static mode Validate guarantees ok == true, so this branch is
		// discovery-only.)
		self = warden.Node{ID: warden.NodeID(cfg.NodeID), Addr: cfg.AdvertiseAddr()}
		logger.Info().
			Str("node_id", cfg.NodeID).
			Str("advertise_addr", self.Addr).
			Msg("node_id not in peer seed; starting as a discovery joiner (observer until admitted)")
	}

	logger.Info().
		Str("version", version).
		Str("node_id", cfg.NodeID).
		Str("bind", cfg.Bind).
		Int("peers", len(cfg.Peers)).
		Str("notify_mode", cfg.Notify.Mode).
		Str("data_dir", cfg.DataDir).
		Str("discovery_mode", cfg.Discovery.Mode).
		Str("cluster_id", cfg.Discovery.ClusterID).
		Msg("starting warden")
	logger.Debug().Str("config", cfg.Redacted().String()).Msg("effective configuration")

	// --- component construction -----------------------------------------
	clock := warden.NewRealClock()
	st := store.NewFileStore(filepath.Join(cfg.DataDir, stateFileName))
	// tr is the gRPC cluster transport; its per-peer connections are released
	// on Close once every component (including the manager's outbound RPC
	// workers) has stopped.
	tr := grpctransport.New(cfg.Timing.RPCTimeout)
	defer func() { _ = tr.Close() }()

	// Build the membership discoverer for the configured mode. Discovery is
	// advisory: the election manager verifies candidates via identify and only
	// the leader turns stable ones into one-at-a-time voting-membership changes.
	//
	// The interface is declared here, at the seam that accepts one, and each
	// arm assigns the concrete discoverer its constructor already returns
	// (CS-8). Config validation guarantees the mode is one of the three
	// recognized values and that its mode-specific requirements are met, so the
	// default arm safely handles "static".
	var discoverer warden.IPeerDiscoverer
	switch cfg.Discovery.Mode {
	case config.DiscoveryModeTailscale:
		discoverer = discovery.NewTailscale(discovery.TailscaleConfig{
			Socket:       cfg.Discovery.Tailscale.Socket,
			Tag:          cfg.Discovery.Tailscale.Tag,
			HostPattern:  cfg.Discovery.Tailscale.HostPattern,
			Port:         bindPort(cfg.Bind), // 0 => discovery defaults to 7717
			PollInterval: cfg.Discovery.Tailscale.PollInterval,
			IncludeSelf:  true,
		})
	case config.DiscoveryModeFile:
		discoverer = discovery.NewFile(cfg.Discovery.File, cfg.Discovery.FilePollInterval)
	default: // "static"
		// Assigning nothing (not NewStatic, and not a typed nil) is deliberate:
		// a nil Discoverer selects the election manager's static semantics —
		// membership mirrors the config peer list exactly and persisted
		// membership is ignored, so operators change a static fleet by editing
		// config + rolling restart. Passing NewStatic here would flip those
		// nodes into dynamic semantics where a persisted roster overrides
		// config edits. NewStatic remains for tests and embedded composition.
		//
		// The switch lives here rather than in a factory returning
		// warden.IPeerDiscoverer for the same reason. election.Config's
		// discoveryMode is `cfg.Discoverer != nil`, and a *concrete* nil
		// pointer stored in an interface is not a nil interface — so a factory
		// whose static arm returned a typed nil would silently put the whole
		// fleet into discovery mode. An unassigned variable is the one nil that
		// cannot be typed.
	}

	// ViewFreshFor: a follower trusts the leader's piggybacked authoritative
	// view for as long as it would still consider the leader alive, i.e.
	// DeadAfter. (No dedicated config knob; derived here.)
	mgr, err := election.NewManager(election.Config{
		Self:               self,
		Peers:              cfg.Peers,
		HeartbeatInterval:  cfg.Timing.HeartbeatInterval,
		SuspectAfter:       cfg.Timing.SuspectAfter,
		DeadAfter:          cfg.Timing.DeadAfter,
		ElectionTimeoutMin: cfg.Timing.ElectionTimeoutMin,
		ElectionTimeoutMax: cfg.Timing.ElectionTimeoutMax,
		RPCTimeout:         cfg.Timing.RPCTimeout,
		ViewFreshFor:       cfg.Timing.DeadAfter,
		Discoverer:         discoverer,
		ClusterID:          cfg.Discovery.ClusterID,
		BuildVersion:       version,
		JoinStability:      cfg.Discovery.JoinStability,
		RemoveAfter:        cfg.Discovery.RemoveAfter,
	}, tr, st, clock)
	if err != nil {
		logger.Fatal().Err(err).Msg("constructing election manager")
	}

	// The operator notifier, selected the same way and for the same reason: the
	// interface is declared at the seam the watchdog accepts it through, and
	// each arm assigns the concrete notifier its constructor returns. Config
	// validation has already rejected an unknown mode, so the default arm is
	// the unreachable-by-construction one and says so by refusing to start.
	var notifier warden.INotifier
	switch cfg.Notify.Mode {
	case config.NotifyModeSMTP:
		notifier = notify.NewSMTPNotifier(notify.SMTPConfig{
			Host:     cfg.Notify.SMTPHost,
			Port:     cfg.Notify.SMTPPort,
			Username: cfg.Notify.SMTPUser,
			Password: cfg.Notify.SMTPPass,
			From:     cfg.Notify.SMTPFrom,
			To:       cfg.Notify.SMTPTo,
		})
	case config.NotifyModeFile:
		notifier = notify.NewFileNotifier(cfg.Notify.File)
	case config.NotifyModeLog:
		notifier = notify.NewLogNotifier()
	default:
		logger.Fatal().Str("notify_mode", cfg.Notify.Mode).Msg("constructing notifier")
	}

	// CheckInterval: the watchdog re-evaluates peer liveness once per
	// heartbeat cycle. (No dedicated config knob; derived here.)
	wd := watchdog.New(watchdog.Config{
		Cooldown:       cfg.Watchdog.Cooldown,
		NotifyRecovery: *cfg.Watchdog.NotifyRecovery, // resolved non-nil by config.Load
		MaxIncidents:   cfg.Watchdog.MaxIncidents,
		CheckInterval:  cfg.Timing.HeartbeatInterval,
	}, mgr, notifier, clock)

	dash, err := dashboard.New(mgr, wd, version)
	if err != nil {
		logger.Fatal().Err(err).Msg("constructing dashboard")
	}
	mets := metrics.New(mgr)

	// One bound port serves the whole node surface: the gRPC WardenService
	// (cluster RPCs + WatchCluster) and the gin HTTP engine (dashboard +
	// /api/status + /metrics), multiplexed by cmux. The listener is created
	// here so a bind failure is fatal before any component starts.
	lis, err := net.Listen("tcp", cfg.Bind)
	if err != nil {
		logger.Fatal().Err(err).Str("bind", cfg.Bind).Msg("binding listen address")
	}
	muxSrv := grpcmux.New(grpcmux.Config{
		Listener: lis,
		HTTP:     newRouter(dash, mets),
		RPC:      mgr,
		Views:    mgr,
	})

	// --- CSP supervisor --------------------------------------------------
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// componentExit carries a named terminal result. The channel is buffered
	// to the component count so no goroutine ever blocks on send, even after
	// main has stopped reading.
	type componentExit struct {
		name string
		err  error
	}
	const numComponents = 3
	exits := make(chan componentExit, numComponents)

	launch := func(name string, fn func(ctx context.Context) error) {
		go func() { exits <- componentExit{name, fn(ctx)} }()
	}
	launch("election", mgr.Run)
	launch("watchdog", wd.Run)
	go func() {
		logger.Info().Str("bind", cfg.Bind).Msg("serving gRPC + HTTP on one port")
		exits <- componentExit{"mux-server", muxSrv.Serve()}
	}()

	// A single goroutine owns graceful server shutdown, triggered by ctx
	// cancellation (from a signal or from a component failure below). It
	// reports the shutdown error so main can fold it into the exit code.
	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		logger.Info().Msg("shutdown initiated; draining gRPC + HTTP servers")
		sctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		shutdownErr <- muxSrv.Shutdown(sctx)
	}()

	// Collect exactly one terminal result from every component. The first
	// unexpected error cancels the context, which brings the rest down.
	var exitErr error
	for i := 0; i < numComponents; i++ {
		ce := <-exits
		switch {
		case ce.err == nil || errors.Is(ce.err, context.Canceled):
			logger.Info().Str("component", ce.name).Msg("component stopped")
		default:
			logger.Error().Err(ce.err).Str("component", ce.name).Msg("component failed")
			if exitErr == nil {
				exitErr = fmt.Errorf("%s: %w", ce.name, ce.err)
			}
			stop() // cancel ctx -> unwinds the other components + shutdown goroutine
		}
	}

	// Guarantee the shutdown goroutine unblocks even if every component
	// exited cleanly without a signal (defensive: long-running loops
	// shouldn't, but we must never leak that goroutine).
	stop()
	if err := <-shutdownErr; err != nil {
		logger.Error().Err(err).Msg("graceful server shutdown failed")
		if exitErr == nil {
			exitErr = fmt.Errorf("server shutdown: %w", err)
		}
	}

	if exitErr != nil {
		logger.Error().Err(exitErr).Msg("warden exited with error")
		return 1
	}
	logger.Info().Msg("warden shut down cleanly")
	return 0
}

// newRouter composes the gin.Engine that serves a warden node's HTTP surface:
// the SSR dashboard, /api/status, and /metrics. The node-to-node cluster RPCs
// are served by the gRPC WardenService on the same port (see services/warden/grpcmux),
// not by this engine. It builds the shared production engine (release mode,
// minimal recovery middleware, ServeMux-equivalent 405/404 routing — see
// httpserver.NewEngine) and mounts the dashboard and metrics endpoints on it.
// The returned *gin.Engine is an http.Handler.
func newRouter(dash *dashboard.Dashboard, mets *metrics.Metrics) *gin.Engine {
	r := httpserver.NewEngine()
	dash.Register(r)
	mets.Register(r)
	return r
}

// setupLogger builds the process logger for the chosen format and installs it
// as the shared core.Logger, so every package that logs through core.Logger
// (election, watchdog, notify — including the LogNotifier incident lines)
// emits the same format as main. JSON to stdout is the default
// (machine-parseable for production); the human-readable console writer is
// used only when WARDEN_LOG_FORMAT=console for local development. Must be
// called before any component is constructed: the election manager captures
// core.Logger at construction time.
func setupLogger(format string) zerolog.Logger {
	if format != logFormatConsole {
		logger := zerolog.New(os.Stdout).With().Timestamp().Caller().Logger()
		core.Logger = &logger
	}
	return *core.Logger
}

// bindPort extracts the numeric port from a bind address like ":7717" or
// "0.0.0.0:7717". It returns 0 when the port cannot be determined, letting the
// tailscale discoverer fall back to its default warden port.
func bindPort(bind string) int {
	_, portStr, err := net.SplitHostPort(bind)
	if err != nil {
		return 0
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return p
}
