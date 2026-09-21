package chaos_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
	"github.com/candacelabs/csf/pkg/gotth/internal/obstest"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The three equivalence-spec Appendix B measurements QA-2 owns for Phase 3.
//
// They are measurements, not gates: each asserts only the properties that have
// to hold for the number to mean anything, and PRINTS the number. A measurement
// that also carried a threshold would turn a contended host into a red build
// and would tempt whoever met it into widening the threshold, which is how a
// measured value becomes a chosen one.
//
// dashboardHz is equivalence-spec §2.4's dashboard workload, ≈53 logical
// updates per second. The tick interval below is its reciprocal.
const (
	dashboardHz       = 53
	dashboardInterval = time.Second / dashboardHz
)

// ---------------------------------------------------------------------------
// QA3-1 — is coalesce_flush_at = 512 the right value?
// ---------------------------------------------------------------------------

// The deciding numbers are frames emitted per second and the LARGEST
// contributing_event_ids a frame carried, so that the margin below H-4's
// ceiling of 1,024 is a measured distance rather than a chosen fraction.
//
// One thing had to be established before any of it meant anything, and it is
// the finding rather than the measurement: the union only grows when something
// PUTS identifiers in it. deferPatch folds the deferred origin's own event
// identifier into the pending set, and an effect emission's origin has
// event_id = 0 — so a purely server-initiated update stream, which is exactly
// what FR-62's dashboard is, accumulates NOTHING unless the application sets
// Event.Contributing on each emission. Both arms are measured below.
var _ = Describe("QA3-1 — the coalescing flush trigger", Label("measure"), func() {

	// A client that never acknowledges is the cheapest way to hold one behind,
	// and it is the same condition the mobile profile and DSH-8's 4× CPU
	// throttle produce by a slower route: the window fills and stays full. What
	// it does NOT reproduce is the browser's own apply cost, which is stated as
	// not measured in the report rather than approximated here.
	type sample struct {
		patches       int     // Patch frames emitted in the window
		flushes       int     // patches whose union reached the trigger
		maxUnion      int     // largest contributing_event_ids on the wire
		framesPerSec  float64 // every frame kind, which is what the wire carries
		patchesPerSec float64
	}

	sweep := func(flushAt int, contributing bool, window time.Duration) sample {
		GinkgoHelper()

		rec := obstest.NewMetrics()
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Metrics = rec
			cfg.Logger = nil
			cfg.Limits.CoalesceFlushAt = flushAt
			cfg.Limits.AckWindow = 16
			// Long enough that eviction does not end the measurement window
			// early: what is being measured is coalescing behaviour, not the
			// ladder's third stage.
			cfg.Limits.SlowClientGrace = window + 60*time.Second
			cfg.Limits.WriteDeadline = 30 * time.Second
			cfg.Limits.HeartbeatInterval = 5 * time.Second
			cfg.Limits.HeartbeatTimeout = window + 120*time.Second
			cfg.Limits.IdleTimeout = window + 120*time.Second
		})

		w := dialWire(s.addr(), wireOpts{acks: ackNever})
		ticks := int(window/dashboardInterval) + 100
		w.startTicks(dashboardInterval, ticks, contributing)

		start := time.Now()
		time.Sleep(window)
		elapsed := time.Since(start)

		var out sample
		for _, p := range w.patches() {
			out.patches++
			n := len(p.GetOrigin().GetContributingEventIds())
			if n > out.maxUnion {
				out.maxUnion = n
			}
			if n >= flushAt {
				out.flushes++
			}
		}
		frames := rec.Total("gotthlive_frames_sent_total")
		out.framesPerSec = frames / elapsed.Seconds()
		out.patchesPerSec = float64(out.patches) / elapsed.Seconds()
		return out
	}

	It("shows that a purely server-initiated stream does not grow the union at all", func() {
		measureOnly()

		const window = 30 * time.Second
		got := sweep(512, false, window)

		AddReportEntry("QA3-1 — server-initiated, no application Contributing", fmt.Sprintf(
			"%d updates/s for %s against a client that never acknowledges: %.2f frames/s total, %d patches (%.2f/s), "+
				"largest contributing union %d, 0 flushes",
			dashboardHz, window, got.framesPerSec, got.patches, got.patchesPerSec, got.maxUnion))

		// The finding, asserted so it cannot quietly stop being true.
		//
		// deferPatch accumulates the deferred origin's own event identifier and
		// its contributing edges. An effect emission's origin carries
		// event_id = 0 — the origin SOURCE names its cause instead — and its
		// contributing list is exactly the one scheduledBy edge the library adds,
		// which is the SAME identifier on every emission and deduplicates to a
		// set of size one. So the union does not grow with the number of deferred
		// updates: it sits at 1 forever, the flush trigger can never fire, and
		// RFC §7.4's "~1,590 contributing events before eviction at the dashboard
		// workload" does not describe the workload it names. It describes a
		// client-event-driven workload, or one whose application sets
		// Event.Contributing per emission.
		Expect(got.maxUnion).To(BeNumerically("<=", 1),
			"a server-initiated stream with no application Contributing grew the union to %d, so it "+
				"accumulates after all and RFC §7.4's arithmetic applies to this workload: this "+
				"measurement needs re-taking", got.maxUnion)
		Expect(got.flushes).To(BeZero(), "a flush fired on a union that cannot reach the trigger")
	})

	It("measures frames per second and the largest union across the flush-trigger range", func() {
		measureOnly()

		const window = 30 * time.Second
		// 959 is MaxCoalesceFlushAt: 1024 - 1 - 64, the largest value
		// live.Limits accepts.
		for _, flushAt := range []int{64, 128, 256, 512, 959} {
			got := sweep(flushAt, true, window)

			// The property that must hold for the number to mean anything: the
			// flush is a trigger, never a truncation, so the union stays under
			// H-4's ceiling and the measured distance to it is what is printed.
			Expect(got.maxUnion).To(BeNumerically("<=", 1024),
				"CoalesceFlushAt=%d produced a union of %d, past H-4's ceiling", flushAt, got.maxUnion)

			AddReportEntry(fmt.Sprintf("QA3-1 — CoalesceFlushAt=%d", flushAt), fmt.Sprintf(
				"%d updates/s for %s, client never acknowledges: %.2f frames/s, %d patches (%.2f/s), "+
					"%d of them flushes (%.3f flushes/s), largest union %d, margin below H-4's 1024 = %d",
				dashboardHz, window, got.framesPerSec, got.patches, got.patchesPerSec,
				got.flushes, float64(got.flushes)/window.Seconds(), got.maxUnion, 1024-got.maxUnion))
		}
	})
})

// ---------------------------------------------------------------------------
// QA3-2 — how often is a LEGITIMATE client rate-limited?
// ---------------------------------------------------------------------------

// The amplification bound is already proven (RFC §7.6 E13, and
// test/internal/conformance/limits_test.go). What is unknown is the
// false-positive rate: how often a client that is behaving exactly as FR-11
// requires — one request per gap, latched until the answering Snapshot — is
// answered with Error{RATE_LIMITED} and no render.
//
// The client below implements that rule, so every request it makes is a
// legitimate one, and every refusal is a false positive by construction.
var _ = Describe("QA3-2 — MinResyncInterval and ResyncBurst against a legitimate client", Label("measure"), func() {

	It("measures the refusal rate a lossy link produces", func() {
		measureOnly()

		const window = 20 * time.Second
		type result struct {
			loss       int
			requested  int
			snapshots  int
			refused    int
			closed     bool
			closeCode  int
			patchesIn  int
			refusalPct float64
		}
		var results []result

		for _, loss := range []int{1, 5, 10, 25} {
			s := serve(func(cfg *live.Config[board, chaosUser]) {
				cfg.Logger = nil
				cfg.Limits.MinResyncInterval = time.Second
				cfg.Limits.ResyncBurst = 3
				cfg.Limits.SlowClientGrace = window + 60*time.Second
				cfg.Limits.HeartbeatInterval = 5 * time.Second
				cfg.Limits.HeartbeatTimeout = window + 120*time.Second
				cfg.Limits.IdleTimeout = window + 120*time.Second
			})

			w := dialWire(s.addr(), wireOpts{
				acks:        ackAuto,
				resyncOnGap: true,
				lossPercent: loss,
			})
			ticks := int(window/dashboardInterval) + 100
			w.startTicks(dashboardInterval, ticks, false)

			// CS-9 keep: an observation window, not an await. This whole file
			// measures rather than asserts, and what it measures is what a
			// session does over a fixed span at a fixed loss rate. There is no
			// condition to stop early on; stopping early would shorten the
			// span the numbers below are per-unit of.
			time.Sleep(window)

			var refused int
			for _, e := range w.errors() {
				if e.GetCode().String() == "RATE_LIMITED" {
					refused++
				}
			}
			var resyncSnapshots int
			for _, snap := range w.snapshots() {
				if snap.GetSupersededFromSeq() != 0 {
					resyncSnapshots++
				}
			}
			r := result{
				loss:      loss,
				requested: w.resyncCount(),
				snapshots: resyncSnapshots,
				refused:   refused,
				closed:    w.isClosed(),
				closeCode: int(w.code()),
				patchesIn: len(w.patches()),
			}
			if r.requested > 0 {
				r.refusalPct = 100 * float64(r.refused) / float64(r.requested)
			}
			results = append(results, r)

			AddReportEntry(fmt.Sprintf("QA3-2 — %d%% patch loss", loss), fmt.Sprintf(
				"%s at %d updates/s: %d patches seen, %d legitimate resync requests (%.2f/s), %d answered with a Snapshot, %d refused with RATE_LIMITED (%.1f%% of requests), session closed=%v code=%d",
				window, dashboardHz, r.patchesIn, r.requested,
				float64(r.requested)/window.Seconds(), r.snapshots, r.refused, r.refusalPct,
				r.closed, r.closeCode))
		}

		// The property that has to hold for the numbers to be about a
		// legitimate client rather than an abusive one: at most one outstanding
		// request per gap. If the harness ever sent more, every refusal figure
		// above would be measuring the harness.
		for _, r := range results {
			Expect(r.requested).To(BeNumerically("<=", r.patchesIn+1),
				"the harness sent %d resync requests against %d patches: that is not one per gap",
				r.requested, r.patchesIn)
		}
	})
})

// ---------------------------------------------------------------------------
// QA3-3 — provenance-log volume
// ---------------------------------------------------------------------------

// instrumentation §4A.2/§4A.4 estimates ≈200 B per provenance record and hence
// ≈10.6 KB/s per session at the dashboard workload. The estimate is what makes
// D3's active-heavy N = 1000 imply ≈10.6 MB/s out of one container, which is a
// host-contention source under equivalence-spec T-5 and plausibly a buffer line
// inside M(x) itself. So the real per-record size and the real per-session rate
// are what is measured here, along with the log path's share of the
// default-on-versus-off delta.
var _ = Describe("QA3-3 — provenance-log volume", Label("measure"), func() {

	It("measures bytes per record, bytes per second per session, and the log path's share of the on/off delta", func() {
		measureOnly()

		const window = 20 * time.Second

		// --- volume ------------------------------------------------------
		//
		// The bytes counted are the ones a consumer's handler would write, in
		// the shape a consumer's handler would write them: slog's JSON encoding
		// of exactly the records carrying logger=gotthlive.provenance, and
		// nothing else. Counting the whole log would fold in warnings and
		// lifecycle records that instrumentation §4A is not about.
		sink := newProvenanceSink()
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Logger = slog.New(sink)
			cfg.Metrics = nil
			cfg.Tracer = nil
		})

		w := dialWire(s.addr(), wireOpts{acks: ackAuto})
		ticks := int(window/dashboardInterval) + 100
		w.startTicks(dashboardInterval, ticks, false)

		start := time.Now()
		time.Sleep(window)
		elapsed := time.Since(start)

		bytesWritten, records := sink.totals()
		Expect(records).To(BeNumerically(">", 0),
			"no provenance record was written, so there is no volume to report")

		perRecord := float64(bytesWritten) / float64(records)
		perSecond := float64(bytesWritten) / elapsed.Seconds()

		AddReportEntry("QA3-3 — volume", fmt.Sprintf(
			"%d updates/s for %s: %d provenance records, %d B total, %.1f B/record, %.0f B/s/session (%.2f KB/s). "+
				"instrumentation §4A.2 estimates ≈200 B/record and ≈10.6 KB/s/session.",
			dashboardHz, window, records, bytesWritten, perRecord, perSecond, perSecond/1000))

		// --- the on/off delta --------------------------------------------
		//
		// THROUGHPUT over a fixed window, not time-to-N, and transitions
		// counted from the RENDERED VALUE rather than from frames. The first
		// attempt counted patches and undercounted badly: coalescing collapses
		// many transitions into one patch, so a patch count measures the
		// window's behaviour and not the reducer's. state.Ticks is incremented
		// once per transition and rendered into the "ticks" fragment, so the
		// largest value that reaches the wire is exactly how many transitions
		// completed — coalescing included, because a coalesced patch still
		// carries the latest render.
		const throughputWindow = 12 * time.Second
		type run struct {
			name        string
			logger      *slog.Logger
			transitions int
		}
		runs := []run{
			{name: "provenance off (Logger nil)"},
			{name: "provenance on, discarded"},
			{name: "provenance on, JSON to a counting sink"},
		}
		runs[1].logger = discardLogger()
		runs[2].logger = slog.New(newProvenanceSink())
		for i := range runs {
			runs[i].transitions = driveTransitions(runs[i].logger, throughputWindow)
		}

		off := runs[0].transitions
		for _, r := range runs {
			overhead := 0.0
			if off > 0 {
				overhead = 100 * (float64(off) - float64(r.transitions)) / float64(off)
			}
			AddReportEntry("QA3-3 — on/off", fmt.Sprintf(
				"%s of server-initiated transitions, %s: %d transitions (%.0f/s), %+.1f%% throughput cost against Logger nil",
				throughputWindow, r.name, r.transitions,
				float64(r.transitions)/throughputWindow.Seconds(), overhead))
		}
	})
})

// driveTransitions runs a server-initiated update stream for a fixed window and
// returns how many transitions completed, with metrics and traces off so the
// only variable is the log.
func driveTransitions(logger *slog.Logger, window time.Duration) int {
	GinkgoHelper()

	s := serve(func(cfg *live.Config[board, chaosUser]) {
		cfg.Logger = logger
		cfg.Metrics = nil
		cfg.Tracer = nil
		// Large, so backpressure is not the variable. 256 is the protocol's
		// ceiling on ack_window (D-23's third field).
		cfg.Limits.AckWindow = 256
		cfg.Limits.MailboxDepth = 1024
		cfg.Limits.SlowClientGrace = 5 * time.Minute
		cfg.Limits.HeartbeatInterval = time.Second
		cfg.Limits.HeartbeatTimeout = 5 * time.Minute
		cfg.Limits.IdleTimeout = 5 * time.Minute
	})

	w := dialWire(s.addr(), wireOpts{acks: ackAuto})
	// The tightest interval the ticker will honour: what is being measured is
	// the server's own cost per transition, so the workload must be
	// server-bound rather than timer-bound.
	w.startTicks(time.Microsecond, 1<<30, false)
	time.Sleep(window)

	return highestTick(w)
}

// highestTick reads the largest value the "ticks" fragment ever rendered, which
// is the number of transitions that completed.
func highestTick(w *wire) int {
	high := 0
	for _, p := range w.patches() {
		if n, ok := tickValue(patchHTML(p, "ticks")); ok && n > high {
			high = n
		}
	}
	for _, snap := range w.snapshots() {
		if n, ok := tickValue(snapshotHTML(snap, "ticks")); ok && n > high {
			high = n
		}
	}
	return high
}

// tickValue parses "<span>N</span>".
func tickValue(html string) (int, bool) {
	const open, close = "<span>", "</span>"
	if !strings.HasPrefix(html, open) || !strings.HasSuffix(html, close) {
		return 0, false
	}
	n, err := strconv.Atoi(html[len(open) : len(html)-len(close)])
	if err != nil {
		return 0, false
	}
	return n, true
}

// ---------------------------------------------------------------------------
// The provenance sink
// ---------------------------------------------------------------------------

// provenanceSink counts the bytes slog's JSON handler writes for exactly the
// records carrying logger=gotthlive.provenance.
//
// The library's Logger.prov is `l.With(slog.String("logger", ProvenanceLogger))`,
// so the marker arrives through WithAttrs rather than on the record — which is
// why this splits at WithAttrs and hands the provenance branch a handler
// writing to the counter, and everything else one writing to io.Discard.
type provenanceSink struct {
	counter *countingWriter
	handler slog.Handler
	prov    bool
	records *atomic.Int64
}

func newProvenanceSink() *provenanceSink {
	c := &countingWriter{}
	return &provenanceSink{
		counter: c,
		handler: slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}),
		records: &atomic.Int64{},
	}
}

func (s *provenanceSink) totals() (int64, int64) {
	return s.counter.total(), s.records.Load()
}

func (s *provenanceSink) Enabled(ctx context.Context, level slog.Level) bool { return true }

func (s *provenanceSink) Handle(ctx context.Context, r slog.Record) error {
	if s.prov {
		s.records.Add(1)
	}
	return s.handler.Handle(ctx, r)
}

func (s *provenanceSink) WithAttrs(attrs []slog.Attr) slog.Handler {
	prov := s.prov
	for _, a := range attrs {
		if a.Key == "logger" && a.Value.String() == obs.ProvenanceLogger {
			prov = true
		}
	}
	out := &provenanceSink{counter: s.counter, prov: prov, records: s.records}
	if prov {
		out.handler = slog.NewJSONHandler(s.counter, &slog.HandlerOptions{Level: slog.LevelDebug}).WithAttrs(attrs)
	} else {
		out.handler = s.handler.WithAttrs(attrs)
	}
	return out
}

func (s *provenanceSink) WithGroup(name string) slog.Handler {
	return &provenanceSink{
		counter: s.counter,
		handler: s.handler.WithGroup(name),
		prov:    s.prov,
		records: s.records,
	}
}

// countingWriter counts bytes and discards them, so the measurement is of what
// the log path produces rather than of what a filesystem does with it.
type countingWriter struct {
	mu sync.Mutex
	n  int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.n += int64(len(p))
	c.mu.Unlock()
	return len(p), nil
}

func (c *countingWriter) total() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}
