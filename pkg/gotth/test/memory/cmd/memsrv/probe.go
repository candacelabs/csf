package main

// The stack probe: a DIAGNOSTIC, off by default, and never part of a measured
// run.
//
// docs/bench/g2-baseline.md §5.1 attributes ≈11,010 B per session to "at least
// one of the two per-session goroutines" crossing a stack-doubling boundary
// once observability is wired. That sentence was inferred from the shape of the
// number — 8 KiB is what a doubling costs — and RFC §6.1.2 asks for a named
// line rather than a plausible one. This file measures the claim instead:
//
//   - WHICH goroutine. Probe points are placed on both per-session goroutines
//     (the session actor and the connection read pump) and record, per
//     goroutine, the highest and lowest stack address any probe observed. The
//     difference is a lower bound on that goroutine's peak stack usage, and it
//     is compared against Go's stack sizes, which are powers of two.
//
//   - WHICH call path. At the deepest address it saw, each goroutine's probe
//     keeps the runtime.Stack text, so the frame list that reached that depth
//     is recorded rather than guessed at.
//
// The probes hang off things the SUT already supplies to the library — a
// sampler, a slog handler, and the application's own hooks — so nothing in
// gotth-live is modified to be measurable, which is the property that makes the
// reading trustworthy.
//
// It costs a runtime.Stack per probe call and is therefore never enabled during
// a measured window. -probe implies a handful of sessions and a human reading
// /stackprobe.

import (
	"bytes"
	"context"
	"log/slog"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"unsafe"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// stackAddr returns the address of a local in the calling frame.
//
// The conversion to uintptr is immediate and the result is never converted
// back, so this is a measurement of where the frame sits and not a pointer
// escaping into one. It is //go:noinline so the frame it reports is its own
// caller's and does not move with optimisation decisions.
//
//go:noinline
func stackAddr() uintptr {
	var x [8]byte
	return uintptr(unsafe.Pointer(&x[0]))
}

// goroutineID reads the current goroutine's identifier out of its own stack
// dump. There is no supported accessor; the dump's first line is
// "goroutine N [state]:" and that is what this parses.
//
// It is only ever used to group probe records, and only under -probe.
func goroutineID(dump []byte) uint64 {
	const prefix = "goroutine "
	if !bytes.HasPrefix(dump, []byte(prefix)) {
		return 0
	}
	rest := dump[len(prefix):]
	i := bytes.IndexByte(rest, ' ')
	if i < 0 {
		return 0
	}
	id, err := strconv.ParseUint(string(rest[:i]), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// goroutineRecord is one goroutine's observed stack history.
//
// The load-bearing field is Copies. Go grows a goroutine stack by allocating a
// stack of TWICE the size and COPYING the old one into it, so every doubling
// moves every frame to a new address. A probe that sees the same goroutine's
// frame at two addresses megabytes apart has therefore witnessed a doubling
// directly, and the number of such moves is a lower bound on how many times
// that goroutine's stack doubled — which is the mechanism g2-baseline.md §5.1
// names and did not observe.
type goroutineRecord struct {
	GID uint64 `json:"goroutine"`
	// Copies counts observed stack relocations. A goroutine that started at
	// Go's 2 KiB minimum and ended at 16 KiB relocated at least three times.
	Copies int `json:"stack_copies_observed"`
	// MoveSites names the probe that first observed each relocation, so the
	// call path that forced the growth is recorded rather than inferred.
	MoveSites []string `json:"move_sites"`
	// Hi and Lo bound the addresses seen SINCE the last relocation, so the
	// extent below is within one stack epoch and is comparable to a stack size.
	Hi uintptr `json:"-"`
	Lo uintptr `json:"-"`
	// UsedBytes is Hi−Lo within the final epoch: a LOWER BOUND on stack usage,
	// because it starts at the shallowest probe rather than at the goroutine's
	// entry.
	UsedBytes uintptr `json:"used_bytes_lower_bound"`
	// Sites lists every probe name that fired on this goroutine, which is what
	// identifies it as the actor or the read pump.
	Sites []string `json:"sites"`
	// DeepestSite and DeepestStack are the probe that saw Lo and the frame list
	// at that moment.
	DeepestSite  string `json:"deepest_site"`
	DeepestStack string `json:"deepest_stack"`
	Samples      int    `json:"samples"`

	last uintptr
}

// epochJump is how far two observations of one goroutine's stack must be apart
// before they are treated as different stack allocations rather than different
// frames. A goroutine's whole stack is at most a few hundred kilobytes here;
// a relocation moves it by whole spans and is orders of magnitude larger.
const epochJump = 1 << 20

// apart is |a−b| for addresses, without the unsigned wrap that makes the
// subtraction alone unreadable.
func apart(a, b uintptr) uintptr {
	if a > b {
		return a - b
	}
	return b - a
}

// dumpPool holds the runtime.Stack scratch buffers off the stack.
var dumpPool = sync.Pool{New: func() any {
	b := make([]byte, 16384)
	return &b
}}

// stackProbe collects goroutineRecords. One per process.
type stackProbe struct {
	mu sync.Mutex
	by map[uint64]*goroutineRecord
	// cap bounds how many goroutines are tracked, because a probe that grows
	// without bound would be its own memory finding.
	cap int
}

func newStackProbe() *stackProbe {
	return &stackProbe{by: make(map[uint64]*goroutineRecord), cap: 64}
}

// note records one observation. addr must come from stackAddr() called by the
// site itself, so that the address is the site's frame and not this function's.
func (p *stackProbe) note(site string, addr uintptr) {
	if p == nil {
		return
	}
	// The dump buffer is POOLED rather than a local array. A local
	// [8192]byte would put eight kilobytes on the very stack this function
	// exists to measure, forcing the growth it is trying to observe — the
	// probe becoming its own finding.
	buf := dumpPool.Get().(*[]byte)
	defer dumpPool.Put(buf)
	n := runtime.Stack(*buf, false)
	dump := (*buf)[:n]
	gid := goroutineID(dump)

	p.mu.Lock()
	defer p.mu.Unlock()

	rec, ok := p.by[gid]
	if !ok {
		if len(p.by) >= p.cap {
			return
		}
		rec = &goroutineRecord{GID: gid, Hi: addr, Lo: addr, last: addr}
		p.by[gid] = rec
	}
	rec.Samples++

	// A relocation: the frame moved by more than any stack is deep. Everything
	// observed before it belongs to a stack that no longer exists, so the
	// extent restarts here.
	if apart(addr, rec.last) > epochJump {
		rec.Copies++
		if len(rec.MoveSites) < 8 {
			rec.MoveSites = append(rec.MoveSites, site)
		}
		rec.Hi, rec.Lo = addr, addr
		rec.DeepestStack = ""
	}
	rec.last = addr

	if addr > rec.Hi {
		rec.Hi = addr
	}
	if addr <= rec.Lo || rec.DeepestStack == "" {
		rec.Lo = addr
		rec.DeepestSite = site
		rec.DeepestStack = string(dump)
	}
	rec.UsedBytes = rec.Hi - rec.Lo

	for _, s := range rec.Sites {
		if s == site {
			return
		}
	}
	rec.Sites = append(rec.Sites, site)
}

// records returns a stable snapshot, deepest usage first.
func (p *stackProbe) records() []goroutineRecord {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]goroutineRecord, 0, len(p.by))
	for _, r := range p.by {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UsedBytes > out[j].UsedBytes })
	return out
}

// probeSampler wraps a sampler so that every span start is a probe point.
//
// ShouldSample runs inside the tracer's Start, below every frame the library
// contributes, which makes it the deepest point on the tracing path that a
// consumer of the SDK can reach without forking it.
type probeSampler struct {
	inner sdktrace.Sampler
	probe *stackProbe
}

func (s probeSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	s.probe.note("otel.sampler:"+p.Name, stackAddr())
	return s.inner.ShouldSample(p)
}

func (s probeSampler) Description() string { return "probe(" + s.inner.Description() + ")" }

// probeHandler wraps a slog handler so that every emitted record is a probe
// point, for the same reason: Handle runs below every frame the library's log
// helpers contribute.
type probeHandler struct {
	inner slog.Handler
	probe *stackProbe
}

func (h probeHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h probeHandler) Handle(ctx context.Context, r slog.Record) error {
	h.probe.note("slog.Handle:"+r.Message, stackAddr())
	return h.inner.Handle(ctx, r)
}

func (h probeHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return probeHandler{inner: h.inner.WithAttrs(as), probe: h.probe}
}

func (h probeHandler) WithGroup(name string) slog.Handler {
	return probeHandler{inner: h.inner.WithGroup(name), probe: h.probe}
}

// assert at compile time that the wrappers still satisfy what the SDK wants.
var (
	_ sdktrace.Sampler = probeSampler{}
	_ slog.Handler     = probeHandler{}
	_ trace.Tracer     = (trace.Tracer)(nil)
)
