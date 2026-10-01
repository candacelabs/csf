package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The committed fixture, and the monotonic schedule both servers replay it on
// (equivalence-spec §2.5).
//
// Neither server generates data and this file generates nothing: the bytes are
// produced once by bench/fixtures/generate.mjs — BENCH-1's committed generator,
// including its resolution of the §2.5 seed token (ambiguity Q-B in
// bench/README.md) — and both servers read the same bytes and replay them
// against the same schedule: tick N is emitted at T0 + N × 100 ms.
//
// §2.5 sizes this corpus at "36 000 ticks = 1 hour at 10 Hz".

// TickMs is §2.5's schedule.
const TickMs = 100

// DefaultFixtureDir is bench/fixtures relative to this app's own directory.
const DefaultFixtureDir = "../../../fixtures"

// The four event kinds bench/fixtures/generate.mjs emits for the dashboard, one
// per region that a tick can move. Region E has none: §2.4 makes it "a small
// panel refreshed by an explicit button press", so it is not on the feed.
const (
	FixtureKPI    = "kpi"
	FixtureSeries = "series"
	FixtureRows   = "rows"
	FixtureLog    = "log"
)

// RowUpdate is one changed row inside a `rows` event. §2.4's region B churns
// "20 rows changed per tick (10 % churn)", and the name is not among the
// changed fields — a row's identity does not move.
type RowUpdate struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	M1     int    `json:"m1"`
	M2     int    `json:"m2"`
	M3     int    `json:"m3"`
	TS     int64  `json:"ts"`
}

// DashEvent is one thing the fixture says happened.
type DashEvent struct {
	Kind string      `json:"k"`
	V    []int       `json:"v"`
	R    []RowUpdate `json:"r"`
	Seq  int         `json:"seq"`
	Text string      `json:"text"`
}

// Tick is one line of the JSONL after the first.
type Tick struct {
	N int         `json:"n"`
	E []DashEvent `json:"e"`
}

// BaseRow is one row of the initial table, read rather than computed so that E3
// rests on both servers reading the same bytes rather than on both running the
// same generator.
type BaseRow struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	M1     int    `json:"m1"`
	M2     int    `json:"m2"`
	M3     int    `json:"m3"`
	TS     int64  `json:"ts"`
}

// Base is line 0 of the fixture: the state at tick 0, including the 60 points of
// sparkline history and 120 of series history that make regions A and C full
// before the first tick.
type Base struct {
	KPILabels []string  `json:"kpiLabels"`
	Rows      []BaseRow `json:"rows"`
	KPI       []int     `json:"kpi"`
	Spark     [][]int   `json:"spark"`
	Series    [][]int   `json:"series"`
}

// Fixture is the whole committed corpus.
type Fixture struct {
	Base   Base
	Ticks  []Tick
	SHA256 string
}

// LoadFixture reads bench/fixtures/dashboard/ticks.jsonl and hashes the bytes
// as read, so the run manifest can record the SHA-256 §6 asks for.
func LoadFixture(dir string) (*Fixture, error) {
	path := filepath.Join(dir, "dashboard", "ticks.jsonl")
	f, err := os.Open(path) //nolint:gosec // an operator-supplied path is the point of the flag
	if err != nil {
		return nil, fmt.Errorf(
			"dashboard-gotth: could not read the committed fixture at %s: %w\n"+
				"\tgenerate it with `npm run fixtures` in bench/ (equivalence-spec §2.5);\n"+
				"\tthe generator and the SHA-256 are committed, the ~11 MB of JSONL is not",
			path, err)
	}
	defer func() { _ = f.Close() }()

	sum := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(f, sum))
	// The base record carries 200 rows plus 8×60 and 2×120 points of history and
	// is comfortably past bufio's 64 KB default line.
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)

	fixture := &Fixture{}
	for line := 0; scanner.Scan(); line++ {
		text := scanner.Bytes()
		if len(text) == 0 {
			continue
		}
		if line == 0 {
			var wrapper struct {
				Base Base `json:"base"`
			}
			if err := json.Unmarshal(text, &wrapper); err != nil {
				return nil, fmt.Errorf("dashboard-gotth: fixture line 0 is not the base record: %w", err)
			}
			fixture.Base = wrapper.Base
			continue
		}
		var tick Tick
		if err := json.Unmarshal(text, &tick); err != nil {
			return nil, fmt.Errorf("dashboard-gotth: fixture line %d: %w", line, err)
		}
		fixture.Ticks = append(fixture.Ticks, tick)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("dashboard-gotth: reading %s: %w", path, err)
	}
	fixture.SHA256 = hex.EncodeToString(sum.Sum(nil))
	return fixture, nil
}

// Replay drives a fixture at the monotonic schedule and calls apply once per
// tick.
//
// The schedule is ABSOLUTE, not cumulative: each wait is computed against
// T0 + n × interval, so a late tick does not push every subsequent tick later.
// A cumulative ticker would let scheduler jitter accumulate into seconds of
// drift over the fixture's hour and the push-latency rows would be measuring
// the drift. Ticks the process is already late for are applied without waiting,
// so a server that stalls catches up to wall-clock position rather than
// replaying history slowly — which matters for M(x), whose window is five
// minutes in.
type Replay struct {
	ticks    []Tick
	apply    func(Tick)
	interval time.Duration

	t0       time.Time
	stop     chan struct{}
	stopped  chan struct{}
	stopOnce sync.Once
}

// NewReplay returns a replay at rest. Start begins it and fixes T0.
func NewReplay(ticks []Tick, interval time.Duration, apply func(Tick)) *Replay {
	return &Replay{
		ticks:    ticks,
		apply:    apply,
		interval: interval,
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}
}

// Start fixes T0 and begins emitting.
func (r *Replay) Start() {
	r.t0 = time.Now()
	go r.run()
}

// Stop ends the replay and waits for it. Idempotent.
func (r *Replay) Stop() {
	r.stopOnce.Do(func() { close(r.stop) })
	<-r.stopped
}

// T0 is the instant tick 0 was due, which /api/bench/clock publishes so §3.2 can
// translate t_input(N) = T0 + N × 100 ms onto the page's timeline.
func (r *Replay) T0() time.Time { return r.t0 }

// TickNow is the tick number the schedule says should have been emitted by now.
func (r *Replay) TickNow() int {
	if r.t0.IsZero() {
		return 0
	}
	return int(time.Since(r.t0) / r.interval)
}

func (r *Replay) run() {
	defer close(r.stopped)
	for _, tick := range r.ticks {
		delay := time.Until(r.t0.Add(time.Duration(tick.N) * r.interval))
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-r.stop:
				timer.Stop()
				return
			case <-timer.C:
			}
		} else {
			select {
			case <-r.stop:
				return
			default:
			}
		}
		r.apply(tick)
	}
	// The fixture is an hour long and a run is minutes. Looping would replay old
	// timestamps and make the §2.5 conformance test ambiguous, so it stops.
}
