// Copyright 2026 Candace Labs

package costs

import (
	"math"
	"math/rand/v2"
	"slices"

	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// Every interval here is 95%. A bootstrap draws from a generator with a
// fixed seed, so the same record always yields the same interval.
const (
	resamples = 2000
	seedHigh  = 0x6b61726c696e
	seedLow   = 0x626c73
	lowerTail = 0.025
	upperTail = 0.975
)

// bootstrap is the percentile interval of a statistic over units resampled
// with replacement; the statistic receives the indices drawn.
func bootstrap(units int, statistic func(indices []int) float64) *harnessv1.Interval {
	if units == 0 {
		return &harnessv1.Interval{}
	}
	generator := rand.New(rand.NewPCG(seedHigh, seedLow))
	values := make([]float64, resamples)
	indices := make([]int, units)
	for draw := range resamples {
		for position := range indices {
			indices[position] = generator.IntN(units)
		}
		values[draw] = statistic(indices)
	}
	slices.Sort(values)
	return &harnessv1.Interval{Low: quantile(values, lowerTail), High: quantile(values, upperTail)}
}

// quantile is the linearly interpolated quantile of sorted values.
func quantile(sorted []float64, probability float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	position := probability * float64(len(sorted)-1)
	below := int(math.Floor(position))
	above := min(below+1, len(sorted)-1)
	return sorted[below] + (position-float64(below))*(sorted[above]-sorted[below])
}

// mean is the arithmetic mean of the values at the indices.
func mean(values []float64, indices []int) float64 {
	if len(indices) == 0 {
		return 0
	}
	total := 0.0
	for _, index := range indices {
		total += values[index]
	}
	return total / float64(len(indices))
}

// median is the median of the values at the indices.
func median(values []float64, indices []int) float64 {
	drawn := make([]float64, len(indices))
	for position, index := range indices {
		drawn[position] = values[index]
	}
	slices.Sort(drawn)
	return quantile(drawn, 0.5)
}

// all is every index of n units.
func all(n int) []int {
	indices := make([]int, n)
	for index := range indices {
		indices[index] = index
	}
	return indices
}

// wilson is the Wilson score interval of a proportion.
func wilson(successes int, n int) *harnessv1.Interval {
	if n == 0 {
		return &harnessv1.Interval{}
	}
	proportion, z := float64(successes)/float64(n), normalQuantile
	denominator := 1 + z*z/float64(n)
	centre := (proportion + z*z/(2*float64(n))) / denominator
	spread := z * math.Sqrt(proportion*(1-proportion)/float64(n)+z*z/(4*float64(n)*float64(n))) / denominator
	return &harnessv1.Interval{Low: centre - spread, High: centre + spread}
}
