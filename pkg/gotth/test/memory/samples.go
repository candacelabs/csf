package memory

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// SpecSamples and SpecPeriodMS are equivalence-spec §3.6's sampling window,
// spelled out so that a window which is not that window is a failure rather
// than a footnote: "the median of 60 samples taken at 1 Hz over the last 60 s
// of a 5-minute steady-state window".
//
// They are constants and not options. A harness that could be asked for 20
// samples would eventually be asked for 20 samples, and the number in the
// report would still be called a §3.6 figure.
const (
	SpecSamples  = 60
	SpecPeriodMS = 1000
)

// PeriodToleranceMS is how far a sample's spacing may drift from 1 Hz before
// the window is rejected.
//
// It exists because the sampler is a shell loop on a contended host, not a
// real-time system: a 1 Hz loop that sleeps for the remainder of each second
// lands within a few milliseconds when the host is quiet and can land tens of
// milliseconds late when it is not. 150 ms is wide enough that ordinary
// scheduling jitter does not throw away a five-minute window, and narrow
// enough that a sampler which missed a whole second — the failure that would
// silently shorten the window §3.6 fixes at 60 s — cannot pass.
const PeriodToleranceMS = 150

// Sample is one reading of the measured container's cgroup v2 accounting,
// taken from the host.
//
// Current and File are the two fields §3.6's definition of M(x) is written in;
// the rest are carried because they are free at sampling time and because an
// unexpected M(x) is answered by which line moved, not by re-running.
type Sample struct {
	// UnixMilli is when the sample was taken, on the host clock.
	UnixMilli int64
	// Current is memory.current.
	Current int64
	// File is memory.stat's file (page cache), the term §3.6 subtracts.
	File int64
	// Anon, Sock, Slab and Kernel are memory.stat's lines of the same names.
	Anon   int64
	Sock   int64
	Slab   int64
	Kernel int64
}

// Workload is the sample's value of M(x)'s integrand: memory.current minus
// page cache, i.e. anonymous plus kernel memory attributable to the workload.
func (s Sample) Workload() int64 { return s.Current - s.File }

// csvHeader is the column order measure.sh writes and ParseCSV requires. It is
// required rather than inferred: a reordered column read positionally is a
// number that is wrong and looks fine.
var csvHeader = []string{
	"unix_ms", "memory_current", "file", "anon", "sock", "slab", "kernel",
}

// ParseCSV reads a sample file written by measure.sh.
func ParseCSV(r io.Reader) ([]Sample, error) {
	rows, err := csv.NewReader(r).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("reading samples: %w", err)
	}
	if len(rows) == 0 {
		return nil, errors.New("the sample file is empty")
	}
	if got := rows[0]; !equalStrings(got, csvHeader) {
		return nil, fmt.Errorf("unexpected header %q: want %q",
			strings.Join(got, ","), strings.Join(csvHeader, ","))
	}

	out := make([]Sample, 0, len(rows)-1)
	for i, row := range rows[1:] {
		if len(row) != len(csvHeader) {
			return nil, fmt.Errorf("row %d has %d fields, want %d", i+1, len(row), len(csvHeader))
		}
		var vals [7]int64
		for j, cell := range row {
			v, err := strconv.ParseInt(strings.TrimSpace(cell), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("row %d, column %s: %w", i+1, csvHeader[j], err)
			}
			vals[j] = v
		}
		out = append(out, Sample{
			UnixMilli: vals[0], Current: vals[1], File: vals[2],
			Anon: vals[3], Sock: vals[4], Slab: vals[5], Kernel: vals[6],
		})
	}
	return out, nil
}

// CheckWindow reports whether a sample set is the window §3.6 specifies: 60
// samples, 1 Hz, monotonic in time.
//
// It is separate from Median so that the report says which of the two failed,
// and it is called before any figure is produced rather than alongside it.
func CheckWindow(samples []Sample) error {
	if len(samples) != SpecSamples {
		return fmt.Errorf(
			"the window holds %d samples, and equivalence-spec §3.6 fixes it at %d "+
				"(60 samples at 1 Hz over the last 60 s): a shorter window is not that window",
			len(samples), SpecSamples)
	}
	for i := 1; i < len(samples); i++ {
		gap := samples[i].UnixMilli - samples[i-1].UnixMilli
		if gap <= 0 {
			return fmt.Errorf("sample %d is %d ms after sample %d: the window is not monotonic",
				i, gap, i-1)
		}
		if drift := gap - SpecPeriodMS; drift > PeriodToleranceMS || drift < -PeriodToleranceMS {
			return fmt.Errorf(
				"samples %d and %d are %d ms apart, outside 1 Hz ± %d ms: "+
					"the sampler missed its deadline and the window is not 60 s of steady state",
				i-1, i, gap, PeriodToleranceMS)
		}
	}
	return nil
}

// Median returns M(x): the median of the samples' workload bytes.
//
// The sample count §3.6 fixes is even, so "the median" needs a definition. It
// is the arithmetic mean of the two central order statistics — the ordinary
// convention, and the one that does not silently prefer the lower reading of a
// pair on a metric that only ever climbs. The return is float64 so that
// convention is not rounded away before the subtraction in PerSession; the
// report rounds once, at the end.
func Median(samples []Sample) (float64, error) {
	if len(samples) == 0 {
		return 0, errors.New("no samples")
	}
	vals := make([]int64, len(samples))
	for i, s := range samples {
		vals[i] = s.Workload()
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })

	mid := len(vals) / 2
	if len(vals)%2 == 1 {
		return float64(vals[mid]), nil
	}
	return (float64(vals[mid-1]) + float64(vals[mid])) / 2, nil
}

// PerSession is §3.6's headline: ( M(N) − M(0) ) / N.
//
// A negative result is returned rather than clamped. M(N) below M(0) means the
// two windows are not comparable — a different warm-up, a different container
// generation, or a host that moved underneath the run — and hiding it behind a
// zero would turn a broken run into a flattering one.
func PerSession(m0, mN float64, n int) (float64, error) {
	if n <= 0 {
		return 0, fmt.Errorf("the session count is %d: per-session memory is undefined without sessions", n)
	}
	return (mN - m0) / float64(n), nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i]) != b[i] {
			return false
		}
	}
	return true
}
