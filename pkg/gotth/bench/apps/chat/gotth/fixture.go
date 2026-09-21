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
// "Reimplementing a data generator twice invites accidental asymmetry." So
// neither server generates data and this file generates nothing: the bytes are
// produced once by bench/fixtures/generate.mjs — BENCH-1's committed generator,
// including its resolution of the §2.5 seed token (`0xG07TH11VE` is not a hex
// literal; it is FNV-1a'd as the ASCII string it is written as, ambiguity Q-B in
// bench/README.md) — and both servers read the same bytes and replay them
// against the same schedule: tick N is emitted at T0 + N × 100 ms.
//
// The fixture is a BENCH INPUT FILE, never wire traffic (§2.5). This side reads
// it server-side and emits liquid proto frames as always; nothing about the
// file's format reaches the wire, and it does not touch the review checklist's
// §3.2 ban on JSON side channels.

// TickMs is §2.5's schedule. The chat stress row (§2.3: peer traffic at 20 msg/s
// instead of 2) replays the SAME committed bytes with this divided by 10 —
// one fixture, one SHA-256, two rates, and the rate in force recorded in the run
// manifest. BENCH-1's reading R-7, followed here rather than re-derived.
const TickMs = 100

// DefaultFixtureDir is bench/fixtures relative to this app's own directory,
// which is where `go run .` starts. The Next.js side takes the same path from
// BENCH_FIXTURE_DIR for the same reason its comment gives: an explicit location
// with a loud failure beats a relative path that silently resolves to the wrong
// tree and replays an empty fixture.
const DefaultFixtureDir = "../../../fixtures"

// ChatEvent is one thing the fixture says happened. The field names are the
// generator's, verbatim: `{"k":"msg","room":"alpha","author":"ana","body":"…"}`.
type ChatEvent struct {
	Kind   string `json:"k"`
	Room   string `json:"room"`
	Author string `json:"author"`
	Body   string `json:"body"`
}

// The four event kinds bench/fixtures/generate.mjs emits for chat.
const (
	FixtureMsg    = "msg"
	FixtureTyping = "typing"
	FixtureJoin   = "join"
	FixtureLeave  = "leave"
)

// Tick is one line of the JSONL after the first: a tick number and its events.
type Tick struct {
	N int         `json:"n"`
	E []ChatEvent `json:"e"`
}

// Base is line 0: the state the fixture starts from, read rather than computed
// so that E3 ("both servers render identical information at identical times")
// rests on both reading the same bytes rather than on both running the same
// generator.
type Base struct {
	Presence []string `json:"presence"`
	Rooms    []string `json:"rooms"`
}

// Fixture is the whole committed corpus.
type Fixture struct {
	Base   Base
	Ticks  []Tick
	SHA256 string
}

// LoadFixture reads bench/fixtures/chat/ticks.jsonl and hashes the bytes as
// read, so the run manifest can record the SHA-256 §6 asks for and a drifted
// corpus is a startup failure rather than a silently different comparison.
func LoadFixture(dir string) (*Fixture, error) {
	path := filepath.Join(dir, "chat", "ticks.jsonl")
	f, err := os.Open(path) //nolint:gosec // an operator-supplied path is the point of the flag
	if err != nil {
		return nil, fmt.Errorf(
			"chat-gotth: could not read the committed fixture at %s: %w\n"+
				"\tgenerate it with `npm run fixtures` in bench/ (equivalence-spec §2.5);\n"+
				"\tthe generator and the SHA-256 are committed, the JSONL is not",
			path, err)
	}
	defer func() { _ = f.Close() }()

	sum := sha256.New()
	// The scanner's buffer is grown because a single tick line carrying a
	// 500-character body plus its envelope is comfortably past bufio's 64 KB
	// default only in the dashboard's corpus — but one limit for both files is
	// one fewer thing that can differ between the two apps.
	scanner := bufio.NewScanner(io.TeeReader(f, sum))
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
				return nil, fmt.Errorf("chat-gotth: fixture line 0 is not the base record: %w", err)
			}
			fixture.Base = wrapper.Base
			continue
		}
		var tick Tick
		if err := json.Unmarshal(text, &tick); err != nil {
			return nil, fmt.Errorf("chat-gotth: fixture line %d: %w", line, err)
		}
		fixture.Ticks = append(fixture.Ticks, tick)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("chat-gotth: reading %s: %w", path, err)
	}
	fixture.SHA256 = hex.EncodeToString(sum.Sum(nil))
	return fixture, nil
}

// Replay drives a fixture at the monotonic schedule and calls apply once per
// tick.
//
// The schedule is ABSOLUTE, not cumulative: each timer is set against
// T0 + n × interval, so a late tick does not push every subsequent tick later.
// A cumulative ticker would let scheduler jitter accumulate into seconds of
// drift over the fixture's hour, and the push-latency rows would then be
// measuring the drift. Ticks the process is already late for are applied
// without waiting, so a server that stalls catches up to wall-clock position
// rather than replaying history slowly — which matters for M(x), whose window
// is five minutes in.
//
// It is a copy of the Next.js side's Replay class in behaviour and not in code,
// because the two stacks cannot share an implementation. The behaviour is what
// §2.5 constrains, and the two are asserted to agree by the conformance test
// rather than by this comment.
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

// Start fixes T0 and begins emitting. It returns immediately.
func (r *Replay) Start() {
	r.t0 = time.Now()
	go r.run()
}

// Stop ends the replay and waits for it. Idempotent, because a shutdown path
// that panics on a second call is one nobody can call from two places.
func (r *Replay) Stop() {
	r.stopOnce.Do(func() { close(r.stop) })
	<-r.stopped
}

// T0 is the instant tick 0 was due, which is what /api/bench/clock publishes so
// §3.2 can translate `t_input(N) = T0 + N × 100 ms` onto the page's timeline.
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
		due := r.t0.Add(time.Duration(tick.N) * r.interval)
		delay := time.Until(due)
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-r.stop:
				timer.Stop()
				return
			case <-timer.C:
			}
		} else {
			// Already late: apply without waiting, but still check for a stop
			// so a catch-up cannot outrun a shutdown.
			select {
			case <-r.stop:
				return
			default:
			}
		}
		r.apply(tick)
	}
	// The fixture is an hour long and a run is minutes. Reaching the end is a
	// bug or a very long soak; looping would replay old timestamps and make the
	// §2.5 conformance test ambiguous, so it stops.
}
