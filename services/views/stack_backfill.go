// Copyright 2026 Candace Labs

package views

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/services/ouroboros"
)

const (
	// BackfillFile is the OpenMetrics history, written beside Prometheus's
	// configuration where its container reads it, and removed once loaded.
	BackfillFile = "backfill.om"
	// backfillStep is the spacing of reconstructed samples: the records are
	// bucketed by hour, so a finer step would only repeat values.
	backfillStep = time.Hour
	promtool     = "promtool"
)

// promtoolBackfill is promtool's command that turns OpenMetrics into TSDB
// blocks; the input and the data directory follow it.
var promtoolBackfill = []string{promtool, "tsdb", "create-blocks-from", "openmetrics"}

// History is what the backfill reconstructs from: the corpus, main's merge
// history and the slice each assignment ran for.
type History struct {
	Measurement *Measurement
	Pulls       []MergedPull
	Costs       []CostEvent
	Slices      map[string]string
}

// families collects the reconstructed samples by family.
type families map[string]*dto.MetricFamily

func (collected families) add(name string, at time.Time, value float64, labels ...string) {
	metric, _ := catalog.Lookup(name)
	family, found := collected[name]
	if !found {
		valueType := dto.MetricType_GAUGE
		if metric.Type == Counter {
			valueType = dto.MetricType_COUNTER
		}
		family = &dto.MetricFamily{Name: &metric.Name, Help: &metric.Definition, Type: &valueType}
		collected[name] = family
	}
	sample := &dto.Metric{TimestampMs: ptr(at.UnixMilli())}
	for index, label := range metric.Labels {
		sample.Label = append(sample.Label, &dto.LabelPair{Name: ptr(label), Value: ptr(labels[index])})
	}
	if metric.Type == Counter {
		sample.Counter = &dto.Counter{Value: &value}
	} else {
		sample.Gauge = &dto.Gauge{Value: &value}
	}
	family.Metric = append(family.Metric, sample)
}

func ptr[Value any](value Value) *Value { return &value }

// WriteBackfill writes, as OpenMetrics, every sample the records reconstruct
// at the end of each hour after from and up to through: the per-run counters
// as they stood, the struggle rate as measured through the previous day, and
// the merges, score and signals as main's history stood. It returns how many
// samples it wrote.
func WriteBackfill(writer io.Writer, history History, from time.Time, through time.Time) (int, error) {
	collected := families{}
	if history.Measurement != nil {
		backfillRuns(collected, history.Measurement.Runs, history.Slices, from, through)
		backfillStruggles(collected, history.Measurement.Days, from, through)
	}
	backfillPulls(collected, history.Pulls, from, through)
	backfillHandLabeled(collected, handLabeled, from, through)
	backfillCosts(collected, history.Costs, from, through)
	for _, entry := range lab {
		for _, at := range append([]time.Time{entry.At}, stamps(entry.At, entry.At, through)...) {
			if !at.After(from) || at.After(through) {
				continue
			}
			for _, sample := range entry.Series {
				collected.add(sample.Metric, at, sample.Value, sample.labelValues()...)
			}
		}
	}
	if history.Measurement != nil {
		backfillAttention(collected, history.Measurement.OperatorHours, from, through)
	}
	samples := 0
	for _, name := range slices.Sorted(maps.Keys(collected)) {
		samples += len(collected[name].Metric)
		if _, err := expfmt.MetricFamilyToOpenMetrics(writer, collected[name]); err != nil {
			return samples, err
		}
	}
	_, err := expfmt.FinalizeOpenMetrics(writer)
	return samples, err
}

// stamps are the instants a reconstructed series is sampled at: the end of
// each hour from first's hour on, after from and up to through, and through
// itself.
func stamps(first time.Time, from time.Time, through time.Time) []time.Time {
	var instants []time.Time
	for at := first.UTC().Truncate(backfillStep).Add(backfillStep); !at.After(through); at = at.Add(backfillStep) {
		if at.After(from) {
			instants = append(instants, at)
		}
	}
	if through.After(from) && (len(instants) == 0 || instants[len(instants)-1].Before(through)) {
		instants = append(instants, through)
	}
	return instants
}

func backfillRuns(collected families, runs []Run, assignments map[string]string, from time.Time, through time.Time) {
	hourly := map[RunKey]map[time.Time]*Tally{}
	var first time.Time
	for _, run := range runs {
		key := runKey(run, assignments)
		if hourly[key] == nil {
			hourly[key] = map[time.Time]*Tally{}
		}
		for hour, tally := range run.Hours {
			if first.IsZero() || hour.Before(first) {
				first = hour
			}
			sum, found := hourly[key][hour]
			if !found {
				sum = newTally()
				hourly[key][hour] = sum
			}
			sum.add(tally)
		}
	}
	if first.IsZero() {
		return
	}
	instants := stamps(first, from, through)
	for key, hours := range hourly {
		ordered := slices.SortedFunc(maps.Keys(hours), func(left time.Time, right time.Time) int { return left.Compare(right) })
		if len(ordered) == 0 {
			continue
		}
		cumulative, next := newTally(), 0
		for _, at := range instants {
			for next < len(ordered) && ordered[next].Before(at) {
				cumulative.add(hours[ordered[next]])
				next++
			}
			if next == 0 {
				continue
			}
			emitTally(collected, key, cumulative, at)
		}
	}
}

func emitTally(collected families, key RunKey, tally *Tally, at time.Time) {
	tally.Series(key, func(name string, value float64, labels ...string) {
		collected.add(name, at, value, labels...)
	})
}

// backfillStruggles samples, through each hour, the struggle rate as it was
// measured through the day before: a day's rate is known once it ends.
func backfillStruggles(collected families, days []ouroboros.Rate, from time.Time, through time.Time) {
	if len(days) == 0 {
		return
	}
	first, err := time.Parse(time.DateOnly, days[0].Day)
	if err != nil {
		return
	}
	measured := map[string]Struggle{}
	for _, at := range stamps(first.AddDate(0, 0, 1), from, through) {
		day := at.Add(-time.Nanosecond).UTC().AddDate(0, 0, -1).Format(time.DateOnly)
		struggle, found := measured[day]
		if !found {
			struggle = StruggleThrough(days, day)
			measured[day] = struggle
		}
		for bound, value := range Bounded(struggle.Week.PerK, struggle.Week.Low, struggle.Week.High) {
			collected.add(MetricStruggleRate, at, value, PeriodWeek, bound)
		}
		for bound, value := range Bounded(struggle.Day.PerK, struggle.Day.Low, struggle.Day.High) {
			collected.add(MetricStruggleRate, at, value, PeriodDay, bound)
		}
		for bound, value := range Bounded(struggle.Compounding.Factor, struggle.Compounding.Low, struggle.Compounding.High) {
			collected.add(MetricCompounding, at, value, PeriodWeek, bound)
		}
		for bound, value := range Bounded(struggle.CompoundingDaily.Factor, struggle.CompoundingDaily.Low, struggle.CompoundingDaily.High) {
			collected.add(MetricCompounding, at, value, PeriodDay, bound)
		}
	}
}

func backfillPulls(collected families, pulls []MergedPull, from time.Time, through time.Time) {
	if len(pulls) == 0 {
		return
	}
	for _, at := range stamps(pulls[0].MergedAt, from, through) {
		state := PullsThrough(pulls, at)
		for slice, count := range state.Merges {
			collected.add(MetricMerges, at, float64(count), slice)
		}
		if state.Score != nil {
			collected.add(MetricOntologyScore, at, *state.Score)
		}
		for signal, value := range state.Signals {
			collected.add(MetricOntologySignal, at, value, signal)
		}
	}
}

// backfillHandLabeled samples the hand-labeled window: nothing at its start,
// every correction and attention hour at its end, carried after it, so an
// increase over a range holding the window counts it once.
func backfillHandLabeled(collected families, labeled HandLabeled, from time.Time, through time.Time) {
	if !labeled.From.After(from) || labeled.From.After(through) {
		return
	}
	counts := ByClass(labeled.Corrections)
	for class := range counts {
		collected.add(MetricCorrections, labeled.From, 0, class, SourceHandLabeled)
	}
	collected.add(MetricAttentionHours, labeled.From, 0, SourceHandLabeled)
	for _, at := range append([]time.Time{labeled.Through}, stamps(labeled.Through, labeled.Through, through)...) {
		if at.After(through) {
			continue
		}
		for class, count := range counts {
			collected.add(MetricCorrections, at, float64(count), class, SourceHandLabeled)
		}
		collected.add(MetricAttentionHours, at, float64(labeled.AttentionHours), SourceHandLabeled)
	}
}

// backfillCosts samples the hypervisor's cost events as they had added up at
// the end of each hour.
func backfillCosts(collected families, events []CostEvent, from time.Time, through time.Time) {
	if len(events) == 0 {
		return
	}
	first := events[0].At
	for _, event := range events {
		if event.At.Before(first) {
			first = event.At
		}
	}
	for _, at := range stamps(first, from, through) {
		for key, tally := range CostsThrough(events, at) {
			collected.add(MetricHypervisorCostEvents, at, float64(tally.Events), key.Operation, key.Agent, key.Model)
			collected.add(MetricHypervisorCostUSD, at, tally.USD, key.Operation, key.Agent, key.Model)
		}
	}
}

// backfillAttention samples the operator-attention hours the runs recorded,
// cumulatively.
func backfillAttention(collected families, hours []time.Time, from time.Time, through time.Time) {
	if len(hours) == 0 {
		return
	}
	next := 0
	for _, at := range stamps(hours[0], from, through) {
		for next < len(hours) && hours[next].Before(at) {
			next++
		}
		collected.add(MetricAttentionHours, at, float64(next), SourceRuns)
	}
}

// Backfill writes the history after record.BackfilledThrough and up to
// through into the Prometheus container's mounted directory, has promtool
// turn it into blocks in Prometheus's data directory, which Prometheus loads
// on its next block reload, and removes the file. It returns the samples
// loaded.
func (stack *Stack) Backfill(ctx context.Context, record StackRecord, history History, through time.Time) (int, error) {
	var document bytes.Buffer
	samples, err := WriteBackfill(&document, history, record.BackfilledThrough, through)
	if err != nil || samples == 0 {
		return 0, err
	}
	root := filepath.Join(stack.state, Directory)
	path := filepath.Join(root, prometheusFiles, BackfillFile)
	if err := writeMounted(root, path, document.Bytes()); err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(path) }()
	result, err := stack.containers.Exec(ctx, record.Prometheus.Container, docker.ExecSpec{
		Command: append(slices.Clone(promtoolBackfill), prometheusMount+"/"+BackfillFile, prometheusData),
	})
	if err != nil {
		return 0, err
	}
	if result.ExitCode != 0 {
		return 0, fmt.Errorf("views: %s exited %d: %s", strings.Join(promtoolBackfill, " "), result.ExitCode, bytes.TrimSpace(result.Stderr))
	}
	return samples, stack.MarkBackfilled(through)
}
