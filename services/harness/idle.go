// Copyright 2026 Candace Labs

package harness

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/candacelabs/csf/pkg/knee"
	"github.com/candacelabs/csf/services/harness/session"
)

// The idle bound is derived from data, never set by hand: the measured gaps
// between a turn's end and the next turn's request across every run under
// the state directory. Its derivation is the knee of that distribution, and
// the quantiles around it are reported beside the bound wherever it is used.
//
// A gap shorter than the bound is agent-paced: a queued amendment, an
// orchestrator's next message, a background completion. Past the knee the
// gaps are human-paced and heavy-tailed (hours), and the median remaining
// wait already exceeds the prompt cache's lifetime, so the executor's warmth
// is gone whether or not its process is kept.
const gapScanBuffer = 64 << 10

// reportedQuantiles are the quantiles the derivation reports beside the
// chosen one.
var reportedQuantiles = []float64{0.5, 0.75, 0.9, 0.95, 0.99}

// Quantile is one quantile of the gap distribution.
type Quantile struct {
	P     float64       `json:"p"`
	Value time.Duration `json:"value"`
}

// IdleBoundReport is the derived idle bound with its derivation: the gaps it
// was measured on, the quantile the bound sits at and the reported quantiles.
type IdleBoundReport struct {
	// Bound is how long a session may sit between turns before its executor
	// is suspended.
	Bound time.Duration `json:"bound"`
	// Quantile is the share of measured gaps at or below the bound; zero when
	// the bound is the fallback.
	Quantile float64 `json:"quantile"`
	// Gaps is how many gaps were measured.
	Gaps int `json:"gaps"`
	// Quantiles are the reported quantiles of the measured gaps.
	Quantiles []Quantile `json:"quantiles"`
	// Fallback reports that fewer than two gaps were measured, so the bound
	// is the fallback the caller gave: the prompt cache lifetime.
	Fallback bool `json:"fallback"`
}

// String is the report in a line, for the log and the pull request body.
func (report IdleBoundReport) String() string {
	quantiles := ""
	for _, quantile := range report.Quantiles {
		quantiles += fmt.Sprintf(" q%.2f=%s", quantile.P, quantile.Value.Round(time.Second))
	}
	if report.Fallback {
		return fmt.Sprintf("idle bound %s (fallback: %d gaps measured)", report.Bound, report.Gaps)
	}
	return fmt.Sprintf("idle bound %s at quantile %.3f of %d gaps (knee);%s", report.Bound.Round(time.Second), report.Quantile, report.Gaps, quantiles)
}

// IdleBound derives the idle bound from gaps: the knee of the empirical
// distribution of log(1+seconds), the point of the cumulative curve farthest
// from the chord between its ends. With fewer than two gaps there is no
// curve, and the bound is fallback.
func IdleBound(gaps []time.Duration, fallback time.Duration) IdleBoundReport {
	sorted := slices.Clone(gaps)
	slices.Sort(sorted)
	report := IdleBoundReport{Gaps: len(sorted), Quantiles: quantilesOf(sorted)}
	if len(sorted) < 2 {
		report.Bound, report.Fallback = fallback, true
		return report
	}
	x := make([]float64, len(sorted))
	for index, gap := range sorted {
		x[index] = math.Log1p(gap.Seconds())
	}
	index := knee.Index(x)
	report.Bound = sorted[index]
	report.Quantile = float64(index+1) / float64(len(sorted))
	return report
}

// quantilesOf is the reported quantiles of sorted, by linear interpolation.
func quantilesOf(sorted []time.Duration) []Quantile {
	if len(sorted) == 0 {
		return nil
	}
	quantiles := make([]Quantile, 0, len(reportedQuantiles))
	for _, p := range reportedQuantiles {
		position := p * float64(len(sorted)-1)
		lower := int(math.Floor(position))
		upper := min(lower+1, len(sorted)-1)
		fraction := position - float64(lower)
		value := time.Duration(float64(sorted[lower]) + fraction*float64(sorted[upper]-sorted[lower]))
		quantiles = append(quantiles, Quantile{P: p, Value: value})
	}
	return quantiles
}

// gapMarkers pick the two records a gap is measured between, before the line
// is decoded: the logs hold every executor event, and most lines are neither.
var (
	finishedMarker  = []byte(`"` + session.KeyEventType + `":"` + session.EventTypeRunFinished + `"`)
	requestedMarker = []byte(`"` + session.KeyEventType + `":"` + session.EventTypeTurnRequested + `"`)
)

// TurnGaps measures, across every run directory under stateDirectory, the
// gap from each turn's end to the next turn's request on the same run. A run
// whose log cannot be read contributes nothing.
func TurnGaps(stateDirectory string) ([]time.Duration, error) {
	paths, err := filepath.Glob(filepath.Join(stateDirectory, "*", session.EventsFile))
	if err != nil {
		return nil, fmt.Errorf("harness: list event logs: %w", err)
	}
	gaps := []time.Duration{}
	for _, path := range paths {
		gaps = append(gaps, runGaps(path)...)
	}
	return gaps, nil
}

// runGaps measures one run's gaps.
func runGaps(path string) []time.Duration {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	var gaps []time.Duration
	var finished time.Time
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, gapScanBuffer), session.MaxRecordBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		isFinished := bytes.Contains(line, finishedMarker)
		if !isFinished && !bytes.Contains(line, requestedMarker) {
			continue
		}
		var record struct {
			Time time.Time `json:"time"`
		}
		if json.Unmarshal(line, &record) != nil || record.Time.IsZero() {
			continue
		}
		switch {
		case isFinished:
			finished = record.Time
		case !finished.IsZero():
			if gap := record.Time.Sub(finished); gap >= 0 {
				gaps = append(gaps, gap)
			}
			finished = time.Time{}
		}
	}
	return gaps
}
