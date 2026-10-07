// Copyright 2026 Candace Labs

// Package knee reads a threshold from data. The knee of an empirical
// distribution is the point of its cumulative curve farthest from the chord
// between the curve's ends, the place where a sparse tail turns into a dense
// body. A threshold taken this way is always an observed value, so a bound or
// an acceptance level is never a number typed into the source: the caller
// reports the quantile the knee sits at beside every use, and a change in the
// data moves the threshold.
package knee

import (
	"math"
	"slices"
)

// Index returns the index of the point of the cumulative curve over x that
// lies farthest from the chord between the curve's ends. x holds the
// x-coordinate of each rank in ascending order; the point for rank i is
// (x[i], (i+1)/len(x)). Fewer than two points has no chord and Index returns
// zero.
func Index(x []float64) int {
	if len(x) < 2 {
		return 0
	}
	count := float64(len(x))
	x0, y0 := x[0], 1/count
	x1, y1 := x[len(x)-1], 1.0
	chord := math.Hypot(y1-y0, x1-x0)
	knee, farthest := 0, -1.0
	for index := range x {
		y := float64(index+1) / count
		distance := math.Abs((y1-y0)*x[index] - (x1-x0)*y + x1*y0 - y1*x0)
		if chord > 0 {
			distance /= chord
		}
		if distance > farthest {
			knee, farthest = index, distance
		}
	}
	return knee
}

// Result is a threshold read from a sample: the value at the knee, the
// quantile it sits at, and how many points it was read from.
type Result struct {
	Threshold float64 `json:"threshold"`
	Quantile  float64 `json:"quantile"`
	Samples   int     `json:"samples"`
}

// Of sorts sample ascending and returns its knee: the observed value farthest
// from the chord of its cumulative curve, with the quantile it sits at. The
// threshold is always a value drawn from sample, never a typed constant. ok is
// false for fewer than two samples, which have no curve to bend.
func Of(sample []float64) (Result, bool) {
	if len(sample) < 2 {
		return Result{}, false
	}
	sorted := slices.Clone(sample)
	slices.Sort(sorted)
	index := Index(sorted)
	return Result{
		Threshold: sorted[index],
		Quantile:  float64(index+1) / float64(len(sorted)),
		Samples:   len(sorted),
	}, true
}
