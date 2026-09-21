// Command memsrv is the server under test for the G2 idle-connection memory
// baseline (RFC-0001 §6.1/§6.2, equivalence-spec §3.6).
//
// It is deliberately the smallest complete live application: one fragment, one
// event, one int64 of state. RFC-0001 §6.2's composition budget is a
// LIBRARY-per-connection budget whose application-state line is 500 B ("the
// counter example's is 24 B"), and a measured figure that is meant to correct
// that table must not carry an application the table never budgeted for.
//
// # TLS
//
// This binary constructs exactly one listener and it is plaintext. There is no
// -tls flag, no certificate path, and no crypto/tls import — equivalence-spec
// §3.6's boundary rule ("the measured container serves plaintext HTTP/WebSocket
// on its container port") is enforced here by there being nothing to
// misconfigure, and /introspect reports tls_listeners so the harness can assert
// it from outside rather than trust this comment. In-process TLS is a separate,
// labelled secondary in §3.6 and is NOT what this binary is for.
//
// # Observability
//
// -observability=on wires a real OpenTelemetry meter and tracer provider and a
// slog JSON logger, which is what equivalence-spec §5.6 calls the headline
// configuration ("that is what a user gets") and what puts the provenance log
// inside the measured number. -observability=off leaves all three nil, which is
// the configuration RFC §6.2's table describes. Both are measured; the manifest
// records which.
//
// Usage:
//
//	memsrv -addr :8080 -origin http://127.0.0.1:18080 -observability on
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/a-h/templ"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// mountPath is where the live handler is mounted, and the same string is given
// to live.Script, so the page and the router cannot disagree.
const mountPath = "/live"

const (
	fragmentValue  = "counter.value"
	eventIncrement = "counter.increment"
)

// State is the whole of the application's per-session state: one counter, and
// the session's own identifier so a render can say whose it is.
type State struct {
	Self  live.ID
	Value int64
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "memsrv:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", ":8080", "plaintext address to listen on")
	origins := flag.String("origin", "http://127.0.0.1:8080",
		"comma-separated Origin allowlist; deny by default, no wildcard")
	obsFlag := flag.String("observability", "on",
		"on wires the logger, meter and tracer (equivalence-spec §5.6's headline "+
			"configuration); off leaves all three nil (RFC §6.2's table). "+
			"logger, metrics and tracer wire exactly one of the three and are "+
			"DIAGNOSTIC cells, never a headline: they exist to attribute §5.1's "+
			"goroutine-stack line to a signal")
	profileRate := flag.Int("memprofilerate", runtime.MemProfileRate,
		"runtime.MemProfileRate for /heapprofile. The Go default (512 KiB) samples "+
			"one allocation in every 512 KiB, which quantises a per-session figure "+
			"at N=1000 to ~512 B. A smaller value sharpens the per-component "+
			"attribution and is DIAGNOSTIC ONLY: profiling every allocation is not "+
			"the shipped configuration and must not be used for a measured cell")
	probeFlag := flag.Bool("probe", false,
		"install the stack probe and serve /stackprobe. DIAGNOSTIC only: it takes "+
			"a runtime.Stack on every span start and every log record, so it must "+
			"never be enabled during a measured window")
	flag.Parse()

	observability := *obsFlag
	switch observability {
	case "on", "off", "logger", "metrics", "tracer":
	default:
		return fmt.Errorf("-observability must be one of on, off, logger, metrics, tracer, not %q", observability)
	}
	wantLogger := observability == "on" || observability == "logger"
	wantMetrics := observability == "on" || observability == "metrics"
	wantTracer := observability == "on" || observability == "tracer"

	runtime.MemProfileRate = *profileRate

	var probe *stackProbe
	if *probeFlag {
		probe = newStackProbe()
	}

	// Each connection gets its own subject. A thousand idle sessions are a
	// thousand users, not one user with a thousand tabs, and Limits'
	// MaxSessionsPerIdentity default of 20 is a real production bound that the
	// harness must not have to raise in order to reach N=1000. Raising it would
	// be a benchmark-shaped configuration change inside a measurement whose
	// whole point is that the configuration is the shipped one.
	var subjects atomic.Uint64

	cfg := live.Config[State, subject]{
		Init: func(ctx context.Context, s live.Session[subject]) (State, []live.Effect[subject], error) {
			// The shallowest probe point on the session actor's goroutine:
			// Run → mount → Init. It anchors the high end of that goroutine's
			// observed stack extent.
			probe.note("app.Init", stackAddr())
			return State{Self: s.ID()}, nil, nil
		},
		Reduce: func(st State, ev live.Event) (State, []live.Effect[subject]) {
			if ev.Name == eventIncrement {
				st.Value++
			}
			return st, nil
		},
		Fragments: []live.Fragment[State]{{
			ID: fragmentValue,
			Render: func(st State) templ.Component {
				return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
					// On the actor goroutine, below the render pass and below
					// the per-fragment span the library opens around it.
					probe.note("app.Render", stackAddr())
					_, err := fmt.Fprintf(w,
						`<b data-value="%d">%d</b>`, st.Value, st.Value)
					return err
				})
			},
		}},
		Events:  []string{eventIncrement},
		Origins: strings.Split(*origins, ","),
		Authenticate: func(request *http.Request) (subject, error) {
			return subject(fmt.Sprintf("session-%d", subjects.Add(1))), nil
		},
		Authorize: func(ctx context.Context, s live.Session[subject], ev live.Event) error {
			// The connection read pump's goroutine, below the ingress and the
			// authorize span. It is only reached when a client sends an event;
			// on the Idle workload the read pump's probe points come from the
			// parse span's sampler call instead.
			probe.note("app.Authorize", stackAddr())
			return live.AllowAll[subject](ctx, s, ev)
		},
		CSRF:   live.NoCSRFCheck,
		Limits: live.DefaultLimits(),
	}

	var shutdown []func(ctx context.Context) error
	if wantLogger {
		// The sink is the container's stderr, which the harness sends to the
		// container log and not to a file on the SUT's own disk:
		// equivalence-spec §5.6 asks where the provenance stream was sunk
		// because a sink on the SUT's disk is a T-5 contention source.
		var h slog.Handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
		if probe != nil {
			h = probeHandler{inner: h, probe: probe}
		}
		cfg.Logger = slog.New(h)
	}
	if wantMetrics {
		// A manual reader holds the instruments without starting an export
		// goroutine or a network client, so what is measured is the cost of
		// the metric state itself rather than of an exporter's buffers.
		meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()))
		cfg.Metrics = meter
		shutdown = append(shutdown, meter.Shutdown)
	}
	if wantTracer {
		// A batch processor over an exporter that drops is the shape a
		// production deployment has — spans are queued and flushed on a timer —
		// without a collector to depend on. On the Idle workload the queue is
		// empty within one batch interval of the last mount, well before the
		// sampling window opens.
		opts := []sdktrace.TracerProviderOption{
			sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(discardExporter{})),
		}
		if probe != nil {
			opts = append(opts, sdktrace.WithSampler(probeSampler{
				inner: sdktrace.ParentBased(sdktrace.AlwaysSample()),
				probe: probe,
			}))
		}
		tracer := sdktrace.NewTracerProvider(opts...)
		cfg.Tracer = tracer
		shutdown = append(shutdown, tracer.Shutdown)
	}

	app, err := live.New(cfg)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle(mountPath, app.Handler())
	mux.Handle(mountPath+"/", app.Handler())
	mux.HandleFunc("/", page)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("/introspect", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, introspect(observability, cfg.Limits, false))
	})
	// The post-forced-GC floor of §3.6, behind its own path so it can only
	// happen when the harness asks. It runs AFTER the headline window is
	// closed; calling it during one would make the headline a forced number,
	// which §3.6 forbids on both stacks.
	mux.HandleFunc("/freeosmemory", func(w http.ResponseWriter, _ *http.Request) {
		debug.FreeOSMemory()
		writeJSON(w, introspect(observability, cfg.Limits, true))
	})
	// The stack probe's readout. Absent unless -probe was given, so a measured
	// run cannot accidentally serve it.
	if probe != nil {
		mux.HandleFunc("/stackprobe", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, probe.records())
		})
	}
	// The per-component heap profile RFC-0001 §6.3 asks for as a gotth-live-only
	// diagnostic and docs/bench/g2-baseline.md §7.5 records as owed: it is what
	// separates §6.2's "WebSocket conn struct" line from its "session struct"
	// line, which cgroup accounting cannot see inside the heap to do.
	//
	// It runs a GC first, so what it reports is LIVE heap and not garbage
	// awaiting collection. Like /freeosmemory it is only reached when the
	// harness asks, and never during a sampling window: forcing a collection
	// inside one would make the headline a forced number, which §3.6 forbids.
	mux.HandleFunc("/heapprofile", func(w http.ResponseWriter, _ *http.Request) {
		// Twice, and the second one is not superstition. net/http parses every
		// request through a sync.Pool'd textproto.Reader, and a Pool's entries
		// survive one collection in the victim cache. One GC would leave that
		// pool's buffers in the profile and attribute per-request scratch to
		// per-session live heap — which is exactly the kind of line this
		// profile exists to stop anyone from guessing at.
		runtime.GC()
		runtime.GC()
		w.Header().Set("Content-Type", "application/octet-stream")
		if err := pprof.WriteHeapProfile(w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout: it would cut live connections off mid-session.
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	fmt.Printf("memsrv: plaintext http://%s (observability %s, GOGC=%s GOMEMLIMIT=%s)\n",
		listener.Addr(), observability, os.Getenv("GOGC"), os.Getenv("GOMEMLIMIT"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serving := make(chan error, 1)
	go func() { serving <- srv.Serve(listener) }()

	select {
	case err := <-serving:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	drain, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(drain)
	err = app.Close(drain)
	for _, fn := range shutdown {
		_ = fn(drain)
	}
	return err
}

// page is the full-page load §3.6's warm-up counts. It renders the same region
// the fragment patches, so the warm-up walks the render path rather than a
// static string, and it carries the client runtime so a real browser mounts a
// real session against this same binary.
func page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html><html lang="en"><head><meta charset="utf-8">`+
		`<title>memsrv</title></head><body><div `)
	writeAttrs(w, live.Region(fragmentValue))
	_, _ = io.WriteString(w, `><b data-value="0">0</b></div><button `)
	writeAttrs(w, live.On("click", eventIncrement))
	_, _ = io.WriteString(w, `>+</button>`)
	_ = live.Script(mountPath).Render(r.Context(), w)
	_, _ = io.WriteString(w, `</body></html>`)
}

func writeAttrs(w io.Writer, attrs templ.Attributes) {
	for k, v := range attrs {
		_, _ = fmt.Fprintf(w, ` %s=%q`, k, fmt.Sprint(v))
	}
}

// report is the secondary-figure payload of §3.6: the runtime-internal
// numbers, reported alongside the cgroup headline and never in place of it.
type report struct {
	// Forced records whether debug.FreeOSMemory ran before these figures were
	// taken. §3.6 requires the forced-GC floor to be a LABELLED secondary, so
	// the label travels with the number rather than with the filename.
	Forced bool `json:"forced_gc"`

	Observability string `json:"observability"`
	TLSListeners  int    `json:"tls_listeners"`

	Goroutines int `json:"goroutines"`
	// MemoryClassesTotal is runtime/metrics /memory/classes/total:bytes.
	MemoryClassesTotal uint64 `json:"memory_classes_total_bytes"`
	// GCHeapLive is runtime/metrics /gc/heap/live:bytes.
	//
	// It reports what the PREVIOUS GC marked, so it is zero until a cycle has
	// completed. GCCycles is reported beside it for exactly that reason: a
	// zero here with zero cycles is "no GC has run at this heap size", which is
	// a fact about the workload, and a zero here with cycles above zero would
	// be a fault in the reading.
	GCHeapLive uint64 `json:"gc_heap_live_bytes"`
	GCCycles   uint64 `json:"gc_cycles_total"`
	// The heap and stack classes are carried because RFC-0001 §6.2's budget is
	// written in these terms: two 8,192 B goroutine stacks per connection is a
	// line in that table, and heap/stacks is where it lands.
	MemoryClassesHeapObjects uint64 `json:"memory_classes_heap_objects_bytes"`
	MemoryClassesHeapStacks  uint64 `json:"memory_classes_heap_stacks_bytes"`
	MemoryClassesHeapFree    uint64 `json:"memory_classes_heap_free_bytes"`
	MemoryClassesHeapUnused  uint64 `json:"memory_classes_heap_unused_bytes"`
	MemoryClassesOSStacks    uint64 `json:"memory_classes_os_stacks_bytes"`

	GOGC        string `json:"gogc"`
	GOMEMLIMIT  string `json:"gomemlimit"`
	GOMAXPROCS  int    `json:"gomaxprocs"`
	NumCPU      int    `json:"num_cpu"`
	GoVersion   string `json:"go_version"`
	UnixMilli   int64  `json:"unix_ms"`
	LimitsInUse limits `json:"limits_in_use"`
}

// limits is the subset of live.Limits equivalence-spec Appendix B requires
// every run manifest to state, so a re-tune between runs is visible in the
// data rather than inferable only from a git log.
type limits struct {
	CoalesceFlushAt        int    `json:"coalesce_flush_at"`
	MinResyncInterval      string `json:"min_resync_interval"`
	ResyncBurst            int    `json:"resync_burst"`
	MailboxDepth           int    `json:"mailbox_depth"`
	AckChannelDepth        int    `json:"ack_channel_depth"`
	AckWindow              int    `json:"ack_window"`
	HeartbeatInterval      string `json:"heartbeat_interval"`
	HeartbeatTimeout       string `json:"heartbeat_timeout"`
	IdleTimeout            string `json:"idle_timeout"`
	MaxInboundFrameBytes   int    `json:"max_inbound_frame_bytes"`
	MaxSessionsPerIdentity int    `json:"max_sessions_per_identity"`
}

func introspect(observability string, lim live.Limits, forced bool) report {
	samples := []metrics.Sample{
		{Name: "/memory/classes/total:bytes"},
		{Name: "/gc/heap/live:bytes"},
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/heap/stacks:bytes"},
		{Name: "/memory/classes/heap/free:bytes"},
		{Name: "/memory/classes/heap/unused:bytes"},
		{Name: "/memory/classes/os-stacks:bytes"},
		{Name: "/gc/cycles/total:gc-cycles"},
	}
	metrics.Read(samples)

	return report{
		Forced:        forced,
		Observability: observability,
		// Structural, not asserted: this binary has no crypto/tls import and
		// constructs one net.Listen listener.
		TLSListeners:             0,
		Goroutines:               runtime.NumGoroutine(),
		MemoryClassesTotal:       samples[0].Value.Uint64(),
		GCHeapLive:               samples[1].Value.Uint64(),
		MemoryClassesHeapObjects: samples[2].Value.Uint64(),
		MemoryClassesHeapStacks:  samples[3].Value.Uint64(),
		MemoryClassesHeapFree:    samples[4].Value.Uint64(),
		MemoryClassesHeapUnused:  samples[5].Value.Uint64(),
		MemoryClassesOSStacks:    samples[6].Value.Uint64(),
		GCCycles:                 samples[7].Value.Uint64(),
		GOGC:                     os.Getenv("GOGC"),
		GOMEMLIMIT:               os.Getenv("GOMEMLIMIT"),
		GOMAXPROCS:               runtime.GOMAXPROCS(0),
		NumCPU:                   runtime.NumCPU(),
		GoVersion:                runtime.Version(),
		UnixMilli:                time.Now().UnixMilli(),
		LimitsInUse: limits{
			CoalesceFlushAt:        lim.CoalesceFlushAt,
			MinResyncInterval:      lim.MinResyncInterval.String(),
			ResyncBurst:            lim.ResyncBurst,
			MailboxDepth:           lim.MailboxDepth,
			AckChannelDepth:        lim.AckChannelDepth,
			AckWindow:              lim.AckWindow,
			HeartbeatInterval:      lim.HeartbeatInterval.String(),
			HeartbeatTimeout:       lim.HeartbeatTimeout.String(),
			IdleTimeout:            lim.IdleTimeout.String(),
			MaxInboundFrameBytes:   lim.MaxInboundFrameBytes,
			MaxSessionsPerIdentity: lim.MaxSessionsPerIdentity,
		},
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

type subject string

func (s subject) Subject() string { return string(s) }

// discardExporter is a span exporter that drops. It stands in for a collector
// so the batch processor behaves as it would in production without this
// measurement depending on a second container.
type discardExporter struct{}

func (discardExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return nil
}
func (discardExporter) Shutdown(ctx context.Context) error { return nil }
