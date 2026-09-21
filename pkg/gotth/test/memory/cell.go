package memory

import (
	"errors"
	"fmt"
	"sort"
)

// InstabilitySpread is equivalence-spec §6's instability rule, applied to this
// dimension by analogy: "if the spread of per-run p50s exceeds 20 % of the
// pooled p50, the cell is marked unstable, the whole cell is re-collected (not
// selectively), and the unstable set is still published."
//
// §6 states the rule for latency percentiles. Nothing in §6 states it for D3,
// and this constant does not invent an amendment: it applies the same
// threshold to the per-run mem_per_session figures and reports the verdict as
// a computed flag, so that a memory cell whose runs disagree is visible in the
// data instead of being averaged into a clean-looking number. A cell marked
// unstable here is published, exactly as §6 requires of the cells it does
// cover.
const InstabilitySpread = 0.20

// Run is one independent run of a cell: one M(0) window, one M(N) window, and
// the figure they produce.
type Run struct {
	ID string `json:"run_id"`
	N  int    `json:"n"`
	// Order records which window was collected first. It is carried because
	// the two windows are separate container lifecycles and a host that drifts
	// during a run would bias every run the same way if the order never
	// changed.
	Order string `json:"order"`

	M0 float64 `json:"m0_bytes"`
	MN float64 `json:"mn_bytes"`

	M0Samples int `json:"m0_samples"`
	MNSamples int `json:"mn_samples"`

	PerSession float64 `json:"mem_per_session_bytes"`
}

// Cell is every run of one (N, workload, configuration) combination.
type Cell struct {
	N    int   `json:"n"`
	Runs []Run `json:"runs"`

	// PooledPerSession is the median of the per-run figures. A median, not a
	// mean, for the same reason §3.6 medians its samples: one run that hit a
	// contended minute must not move the published number.
	PooledPerSession float64 `json:"pooled_mem_per_session_bytes"`
	MinPerSession    float64 `json:"min_mem_per_session_bytes"`
	MaxPerSession    float64 `json:"max_mem_per_session_bytes"`

	// Spread is (max − min) / pooled, and Unstable is that against
	// InstabilitySpread.
	Spread   float64 `json:"per_run_spread"`
	Unstable bool    `json:"unstable"`
}

// Summarize pools a cell's runs.
//
// It refuses a cell whose runs disagree about N, because the only way to pool
// figures from different concurrencies is to publish a number that is about
// neither.
func Summarize(runs []Run) (Cell, error) {
	if len(runs) == 0 {
		return Cell{}, errors.New("a cell with no runs has no figure")
	}
	n := runs[0].N
	vals := make([]float64, 0, len(runs))
	for _, r := range runs {
		if r.N != n {
			return Cell{}, fmt.Errorf(
				"run %s is at N=%d and run %s at N=%d: runs at different concurrencies are not one cell",
				runs[0].ID, n, r.ID, r.N)
		}
		vals = append(vals, r.PerSession)
	}
	sort.Float64s(vals)

	cell := Cell{
		N:             n,
		Runs:          runs,
		MinPerSession: vals[0],
		MaxPerSession: vals[len(vals)-1],
	}
	mid := len(vals) / 2
	if len(vals)%2 == 1 {
		cell.PooledPerSession = vals[mid]
	} else {
		cell.PooledPerSession = (vals[mid-1] + vals[mid]) / 2
	}
	if cell.PooledPerSession != 0 {
		cell.Spread = (cell.MaxPerSession - cell.MinPerSession) / cell.PooledPerSession
	}
	cell.Unstable = cell.Spread > InstabilitySpread
	return cell, nil
}

// SubLinear is RFC-0001 §6.3's check: per-session memory at N = 1000 must be
// within 15 % of N = 100. "If it grows, some structure is O(N) per session and
// that is a design defect, not a tuning problem."
//
// It returns the relative difference and whether it is inside the bound. The
// difference is taken against the SMALLER concurrency, which is the reading
// that makes growth the failing direction.
func SubLinear(small, large Cell) (float64, bool, error) {
	if small.PooledPerSession == 0 {
		return 0, false, errors.New("the N=100 cell has no figure to compare against")
	}
	rel := (large.PooledPerSession - small.PooledPerSession) / small.PooledPerSession
	abs := rel
	if abs < 0 {
		abs = -abs
	}
	return rel, abs <= 0.15, nil
}
