package main

// The resync-cost measurement.
//
// PRD Phase 3 asks for "bytes and latency for a full resync of the dashboard
// example", and the two words are doing different work. **Bytes** is a length of
// something the server sent, which the server's own state does not have — so it
// is read off the wire, by the decoder in wire.go, from the frame as it arrived.
// **Latency** is an interval between two events on opposite sides of a
// connection, so it is measured by the side that can see both: the client writes
// the request, the client sees the Snapshot, and the difference is what a
// browser would wait.
//
// It lives in the example binary rather than in a spec on purpose. A number
// whose method is "run this test with these flags" is a number nobody re-runs,
// and this one has to be re-runnable by a reader who wants to know what a resync
// of THEIR dashboard costs. `go run . -resync-cost 200` prints it, states its
// own method, and says which knob it moved.
//
// It is not a benchmark. There is no warm-up discipline beyond the stated
// steady-state precondition, no allocation accounting, and the latency includes
// a real loopback round trip and this process's own scheduler — it is what a
// client on the same machine waits, reported as a distribution rather than as a
// single figure, because reporting a mean of a network-shaped quantity is how
// measurements become fiction. bench/ is where the project's real latency
// harness lives.

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// measurementResyncInterval and measurementBurstHeadroom are the resync rate
// budget this measurement runs under, in place of the library's one-second
// interval and burst of three.
//
// The default budget exists because a resync is the one client frame that costs
// a full re-render, so fifty a second from one authenticated client would be a
// self-service denial of service. That is a limit on how OFTEN a client may ask;
// it is not part of what one resync COSTS, and leaving it at a second would mean
// a 200-sample run spent three and a half minutes sleeping to measure a few
// milliseconds of work.
//
// The burst is what actually has to move, and finding that out cost a run: a
// 1 ms interval refills one token per millisecond, a loopback resync completes
// in rather less than that, and a bucket of three empties on the fourth request.
// So the burst is the whole run plus headroom — the bucket cannot empty, and the
// thing being measured is never the rate limiter waiting. The alternative,
// sleeping between samples to stay inside a tight bucket, would measure the same
// interval with a sleep in the middle of the loop and one more thing to get
// wrong.
//
// Both are relaxed loudly: Print states them beside the numbers, because a
// measurement taken under non-default limits that does not say so is the kind of
// figure that gets quoted for years.
const (
	measurementResyncInterval = time.Millisecond
	measurementBurstHeadroom  = 8
)

// steadyStatePatches is how many patches the session folds before the first
// measurement is taken.
//
// A snapshot of a session that has just connected is smaller than a snapshot of
// one that has been open a while — the sparkline is empty and the alert log is
// whatever the feed had. MaxWindow patches is exactly the point at which the
// sparkline reaches full length and stops growing, so it is the earliest moment
// the state is representative. Measuring before it would produce a number that
// is real, reproducible, and about a page nobody is looking at.
const steadyStatePatches = MaxWindow

// ResyncReport is one measurement run.
//
// Every field is a raw observation or a count of them. Nothing here is
// summarised at construction time, so Print can state the distribution and a
// caller with a different question can ask it of the same numbers.
type ResyncReport struct {
	// Samples is how many resyncs were requested and answered.
	Samples int

	// FrameBytes is each Snapshot's encoded length as it arrived: the WebSocket
	// message payload, which is the whole frame apart from the 2-14 byte RFC
	// 6455 header. Compression is disabled by the library, so this is not a
	// compressed length and a deployment behind permessage-deflate would send
	// fewer bytes than this.
	FrameBytes []int

	// HTMLBytes is the rendered markup inside each Snapshot, which is the part
	// that grows with the application rather than with the protocol. The
	// difference between this and FrameBytes is the protocol's own overhead:
	// provenance, the supersession range, fragment identifiers and framing.
	HTMLBytes []int

	// Latency is the interval from writing the ResyncRequest to reading the
	// Snapshot, measured on the client side of a loopback connection.
	Latency []time.Duration

	// PerFragment is the markup each region contributed to the LAST snapshot,
	// so the total has an attribution rather than only a size.
	PerFragment map[string]int

	// AlertsAtMeasurement is how many alert rows the last snapshot carried and
	// StateVersion is the state it rendered. DistinctStates is how many
	// different state versions the run's snapshots covered, which is the number
	// that says whether these are samples of a moving system or repeated
	// measurements of one frozen state. All three are here because a snapshot is
	// a function of the state it renders, and a byte count with no statement of
	// what was in the state is not reproducible.
	AlertsAtMeasurement int
	StateVersion        uint64
	DistinctStates      int

	// FeedInterval, ResyncBudget and ResyncBurst are the settings that shaped
	// the run and the two that were not the library's defaults.
	FeedInterval time.Duration
	ResyncBudget time.Duration
	ResyncBurst  int

	// LibraryResyncBytes is the library's own `gotthlive_resync_bytes`
	// histogram over the same run. It is here to be COMPARED with FrameBytes,
	// not to replace it: the two are produced by different code on opposite
	// sides of the connection, and agreement between them is evidence that
	// neither is measuring something else. Disagreement would be a defect
	// report, and which of the two was wrong would not be obvious.
	LibraryResyncBytes Distribution
}

// MeasureResync runs the measurement and returns the report.
//
// It builds its own application over the supplied feed and serves it on a
// loopback listener, because a measurement that borrowed the running server's
// app would be measuring whatever else that server was doing. The feed is
// shared, and is expected to be running: a dashboard whose feed is stopped
// resyncs a state that never changes, which is not the state anybody's browser
// holds.
func MeasureResync(ctx context.Context, feed *Feed, samples int) (*ResyncReport, error) {
	if samples <= 0 {
		return nil, fmt.Errorf("dashboard: -resync-cost needs a positive sample count, got %d", samples)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer listener.Close() //nolint:errcheck // the server below closes it

	addr := listener.Addr().String()
	meters := NewMeters()

	cfg := Config(feed, allowedOrigins(addr, ""))
	cfg.Metrics = meters
	cfg.Limits.MinResyncInterval = measurementResyncInterval
	cfg.Limits.ResyncBurst = samples + measurementBurstHeadroom

	app, err := live.New(cfg)
	if err != nil {
		return nil, err
	}

	srv := &http.Server{Handler: NewMux(app, feed, nil, nil), ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(listener) //nolint:errcheck // Serve returns when Shutdown closes the listener
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
		_ = app.Close(shutdown)
	}()

	dial, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	headers := http.Header{}
	headers.Set("Origin", "http://"+addr)
	conn, _, err := websocket.Dial(dial, "ws://"+addr+MountPath,
		&websocket.DialOptions{HTTPHeader: headers, Subprotocols: []string{Subprotocol}})
	if err != nil {
		return nil, fmt.Errorf("dashboard: the measurement client could not connect: %w", err)
	}
	defer conn.CloseNow() //nolint:errcheck // the measurement is over either way

	report := &ResyncReport{
		Samples:      samples,
		FeedInterval: feed.interval,
		ResyncBudget: cfg.Limits.MinResyncInterval,
		ResyncBurst:  cfg.Limits.ResyncBurst,
		PerFragment:  map[string]int{},
	}

	client := &measureConn{conn: conn, ctx: dial}

	// The mount snapshot, which is every connection's first frame, carries the
	// session identifier every later frame must echo.
	first, err := client.read()
	if err != nil {
		return nil, err
	}
	if first.Kind != "snapshot" {
		return nil, fmt.Errorf("dashboard: the first frame was %q, not the mount snapshot", first.Kind)
	}
	client.sessionID = first.SessionID
	if err := client.ack(first.Patch.ServerSeq); err != nil {
		return nil, err
	}

	// Wait for steady state, acknowledging as a real client does so the outbound
	// window never fills. A measurement taken through a degraded session would be
	// measuring backpressure, which is a different criterion with its own specs.
	if err := client.awaitMeters(steadyStatePatches); err != nil {
		return nil, err
	}

	versions := map[uint64]struct{}{}
	for i := 0; i < samples; i++ {
		// The gap each request is about, opened deliberately: one patch is read
		// and left UNACKNOWLEDGED, so the cursor this client can honestly claim
		// is strictly behind what the server has emitted.
		//
		// This measurement used to send last_applied_seq=1 every time and be
		// answered anyway, which made every snapshot supersede the whole
		// session. The server now clamps the claim up to what it already knows
		// — the client's own acknowledged high-water mark and the sequence of
		// the last snapshot it sent — before deciding whether the request
		// describes a gap at all, so a caught-up client can no longer ask for a
		// snapshot by understating the field: the claim clamps back to
		// server_seq, the request describes no gap, and the answer is an Ack.
		// That is not a limitation to work around. What the old form produced
		// was a supersession range covering patches this client had already
		// applied, and the shipped runtime's applied() closes 4002 on exactly
		// that overlap — so the measurement was timing a frame a browser would
		// have hung up on. The gap is made real instead of claimed, which is
		// also the only state a browser ever asks from.
		//
		// It costs one feed interval per sample and buys what the old trailing
		// wait bought: each snapshot renders a state that has moved since the
		// last one, rather than repeating one frozen state. The samples stay
		// comparable — a resync Snapshot renders every region whatever it
		// supersedes, so a tail range rather than a whole-session one moves two
		// varints and nothing else.
		if err := client.holdBack(); err != nil {
			return nil, err
		}

		start := time.Now()
		if err := client.write(
			EncodeResyncFrame(client.sessionID, client.applied, resyncReasonClientRequest)); err != nil {
			return nil, err
		}

		snapshot, err := client.awaitSnapshot()
		if err != nil {
			return nil, err
		}
		elapsed := time.Since(start)

		total, per := snapshot.Patch.HTMLBytes()
		report.Latency = append(report.Latency, elapsed)
		report.FrameBytes = append(report.FrameBytes, snapshot.Bytes)
		report.HTMLBytes = append(report.HTMLBytes, total)
		report.PerFragment = per
		report.StateVersion = snapshot.Patch.StateVersion
		versions[snapshot.Patch.StateVersion] = struct{}{}

		alerts, _ := snapshot.Patch.Fragment(FragmentAlerts)
		report.AlertsAtMeasurement = strings.Count(alerts, `class="alert"`)

		// One cumulative acknowledgement repairs the gap: it covers the patch
		// held back above and everything that overtook the request, which is
		// what makes the next iteration's held-back patch a fresh gap rather
		// than a widening one.
		//
		// The wait for the next meters patch is now at the top of the loop
		// rather than here, and it is the same wait for the same reason.
		// Without one, the whole run finishes inside a single 50 ms tick: 200
		// resyncs of the same state, a byte spread of six, and an alert log that
		// never has anything in it because the feed has taken thirty-one samples
		// in total. That was the first version of this measurement and its
		// numbers were real and useless. It costs the run `samples × interval`
		// of wall clock and buys a snapshot of a state that is genuinely moving
		// underneath it — which is what a resync is for.
		if err := client.ack(snapshot.Patch.ServerSeq); err != nil {
			return nil, err
		}
	}

	report.DistinctStates = len(versions)
	report.LibraryResyncBytes = awaitResyncCount(meters, samples)
	return report, nil
}

// awaitResyncCount reads the library's own resync histogram once it has counted
// every snapshot this run received.
//
// The wait is not defensive padding. The library records
// gotthlive_resync_bytes *after* it has written the frame, so the client can be
// holding a snapshot the server has not yet counted — and reading the counter in
// the same instant made the spec that compares the two figures fail about one run
// in six, reporting a disagreement that was its own race rather than the
// library's. There is nothing to select on from this side, so this converges on
// the count with a bound and then stops: a figure that never arrives is left
// short, and the caller compares it and says so, which is better than a
// measurement that blocks forever waiting to agree with itself.
func awaitResyncCount(meters *Meters, samples int) Distribution {
	deadline := time.Now().Add(5 * time.Second)
	for {
		d := meters.Histogram(MetricResyncBytes)
		if d.Count >= int64(samples) || time.Now().After(deadline) {
			return d
		}
		time.Sleep(time.Millisecond)
	}
}

// measureConn is the measurement's client: one connection, read synchronously,
// acknowledging what it is sent.
type measureConn struct {
	conn      *websocket.Conn
	ctx       context.Context
	sessionID []byte

	// applied is the highest sequence this client has acknowledged, and is the
	// only value it may honestly put in a ResyncRequest. Ack.server_seq is "the
	// highest CONTIGUOUS seq applied" (protocol.md §3.2), so a request claiming
	// less than this contradicts what this connection has already told the
	// server — and the server, which keeps the same number, clamps it back.
	applied uint64
}

// read takes one frame. An Error frame is a failed measurement rather than a
// frame to skip: the two things that produce one here are the resync rate
// budget refusing a request and a malformed frame, and a run that quietly
// carried on past either would report a distribution over the requests that
// happened to succeed.
func (c *measureConn) read() (*WireFrame, error) {
	typ, data, err := c.conn.Read(c.ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard: the measurement connection ended: %w", err)
	}
	if typ != websocket.MessageBinary {
		return nil, fmt.Errorf("dashboard: a %v message arrived where a binary frame was expected", typ)
	}
	f, err := DecodeFrame(data)
	if err != nil {
		return nil, err
	}
	if f.Kind == "error" {
		return nil, fmt.Errorf("dashboard: the server refused the measurement: code %d, %s",
			f.Error.Code, f.Error.Message)
	}
	return f, nil
}

// holdBack reads until a meters patch arrives and deliberately does NOT
// acknowledge it.
//
// That is the whole state a resync is answered from: an applied cursor strictly
// behind server_seq, with the frames above it unaccounted for. A client that has
// acknowledged everything has no gap to describe, and the server tells it so
// with an Ack. Patches passed over on the way are acknowledged as usual, because
// they are below the one being held and a cumulative ack of them says nothing
// untrue.
func (c *measureConn) holdBack() error {
	for {
		f, err := c.read()
		if err != nil {
			return err
		}
		if f.Patch == nil {
			continue
		}
		if _, ok := f.Patch.Fragment(FragmentMeters); ok {
			return nil
		}
		if err := c.ack(f.Patch.ServerSeq); err != nil {
			return err
		}
	}
}

// awaitSnapshot reads until the Snapshot arrives, acknowledging nothing on the
// way.
//
// The patches that overtake the request are not noise to be filtered out: the
// feed is still sampling while the resync is answered, which is the condition a
// real resync happens under. They are deliberately NOT acknowledged. An Ack
// names the highest contiguous sequence applied; a client with an outstanding
// resync has a hole below these frames, so acknowledging them would claim a
// contiguity it does not have and would move the server's floor above the gap
// this request is about. The shipped runtime discards them under its gap latch
// for the same reason, and the Snapshot's own cumulative Ack repairs the lot.
// They are counted into neither the bytes nor the latency, both of which are
// attributed to the Snapshot frame alone.
func (c *measureConn) awaitSnapshot() (*WireFrame, error) {
	for {
		f, err := c.read()
		if err != nil {
			return nil, err
		}
		if f.Kind == "snapshot" {
			return f, nil
		}
	}
}

// awaitMeters reads until n patches carrying the meters region have arrived,
// acknowledging everything on the way so the outbound window never fills.
func (c *measureConn) awaitMeters(n int) error {
	for folded := 0; folded < n; {
		f, err := c.read()
		if err != nil {
			return err
		}
		if f.Patch == nil {
			continue
		}
		if err := c.ack(f.Patch.ServerSeq); err != nil {
			return err
		}
		if _, ok := f.Patch.Fragment(FragmentMeters); ok {
			folded++
		}
	}
	return nil
}

func (c *measureConn) ack(seq uint64) error {
	if seq > c.applied {
		c.applied = seq
	}
	return c.write(EncodeAckFrame(c.sessionID, seq))
}

func (c *measureConn) write(b []byte) error {
	return c.conn.Write(c.ctx, websocket.MessageBinary, b)
}

// Print writes the report, method first.
//
// The method comes first because these numbers are only meaningful with it, and
// a reader who quotes the median without the two lines above it will quote a
// figure taken under a relaxed rate budget, over a loopback socket, against a
// simulated feed, as though it were a production latency.
func (r *ResyncReport) Print(w io.Writer) {
	fmt.Fprintf(w, "resync cost — %d full resyncs of the dashboard example\n\n", r.Samples)

	fmt.Fprintf(w, "method\n")
	fmt.Fprintf(w, "  one loopback WebSocket to this process, acknowledging every patch as a\n")
	fmt.Fprintf(w, "  browser does. After %d patches — the point at which the sparkline reaches\n", steadyStatePatches)
	fmt.Fprintf(w, "  full length — each sample holds one patch back unacknowledged and writes\n")
	fmt.Fprintf(w, "  a ResyncRequest naming the sequence it has really applied, which is the\n")
	fmt.Fprintf(w, "  only request the server answers with a Snapshot rather than an Ack, and\n")
	fmt.Fprintf(w, "  times the interval until that Snapshot is read. Each snapshot therefore\n")
	fmt.Fprintf(w, "  supersedes the tail since the last acknowledgement rather than the whole\n")
	fmt.Fprintf(w, "  session. One feed sample passes between measurements, so these are\n")
	fmt.Fprintf(w, "  samples of a moving state and not repeats of one. Bytes are the frame as\n")
	fmt.Fprintf(w, "  it arrived, decoded from the wire; compression is disabled by the\n")
	fmt.Fprintf(w, "  library.\n")
	fmt.Fprintf(w, "  feed interval %s. The resync rate budget is relaxed from the library's\n", r.FeedInterval)
	fmt.Fprintf(w, "  %s / burst %d to %s / burst %d, so the bucket cannot empty during the\n",
		live.DefaultLimits().MinResyncInterval, live.DefaultLimits().ResyncBurst,
		r.ResyncBudget, r.ResyncBurst)
	fmt.Fprintf(w, "  run. That bounds how OFTEN a client may ask, not what one resync costs.\n")
	fmt.Fprintf(w, "  Latency includes the loopback round trip and this process's scheduler,\n")
	fmt.Fprintf(w, "  and excludes everything a browser does with the frame after reading it.\n")

	fmt.Fprintf(w, "\nstate the snapshots rendered\n")
	fmt.Fprintf(w, "  %d distinct state versions across %d samples; last was version %d\n",
		r.DistinctStates, r.Samples, r.StateVersion)
	fmt.Fprintf(w, "  at the last snapshot: %d alert rows, %d-sample sparkline\n",
		r.AlertsAtMeasurement, steadyStatePatches)

	fmt.Fprintf(w, "\nbytes on the wire, per snapshot\n")
	writeIntStats(w, "  frame", r.FrameBytes)
	writeIntStats(w, "  markup", r.HTMLBytes)
	if n := len(r.FrameBytes); n > 0 {
		fmt.Fprintf(w, "  protocol overhead (frame - markup, median): %d B\n",
			median(r.FrameBytes)-median(r.HTMLBytes))
	}

	fmt.Fprintf(w, "\n  markup by region, last snapshot\n")
	for _, id := range slices.Sorted(maps.Keys(r.PerFragment)) {
		fmt.Fprintf(w, "    %-22s %d B\n", id, r.PerFragment[id])
	}

	fmt.Fprintf(w, "\n  the library's own gotthlive_resync_bytes over the same run:\n")
	fmt.Fprintf(w, "    n=%d mean=%.1f B max=%.0f B\n",
		r.LibraryResyncBytes.Count, r.LibraryResyncBytes.Mean(), r.LibraryResyncBytes.Max)

	fmt.Fprintf(w, "\nlatency, request written to snapshot read\n")
	writeDurationStats(w, "  resync", r.Latency)
}

// Percentiles are nearest-rank on the sorted samples, which is the definition
// that needs no interpolation and cannot report a value that was never
// observed.
func writeIntStats(w io.Writer, label string, values []int) {
	if len(values) == 0 {
		fmt.Fprintf(w, "%s: no samples\n", label)
		return
	}
	sorted := slices.Sorted(slices.Values(values))
	fmt.Fprintf(w, "%s: min %d  p50 %d  p90 %d  max %d  (n=%d)\n",
		label, sorted[0], percentileInt(sorted, 50), percentileInt(sorted, 90),
		sorted[len(sorted)-1], len(sorted))
}

func writeDurationStats(w io.Writer, label string, values []time.Duration) {
	if len(values) == 0 {
		fmt.Fprintf(w, "%s: no samples\n", label)
		return
	}
	sorted := slices.Sorted(slices.Values(values))
	fmt.Fprintf(w, "%s: min %s  p50 %s  p90 %s  max %s  (n=%d)\n",
		label, round(sorted[0]), round(percentileDuration(sorted, 50)),
		round(percentileDuration(sorted, 90)), round(sorted[len(sorted)-1]), len(sorted))
}

func round(d time.Duration) time.Duration { return d.Round(time.Microsecond) }

func percentileInt(sorted []int, p int) int {
	return sorted[rank(len(sorted), p)]
}

func percentileDuration(sorted []time.Duration, p int) time.Duration {
	return sorted[rank(len(sorted), p)]
}

func rank(n, p int) int {
	return clamp((n*p+99)/100-1, 0, n-1)
}

func median(values []int) int {
	return percentileInt(slices.Sorted(slices.Values(values)), 50)
}
