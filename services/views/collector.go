// Copyright 2026 Candace Labs

package views

import (
	"context"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/candacelabs/csf/services/ouroboros"
)

// The metric families, by name; catalog.json defines each.
const (
	MetricSessions             = "csf_sessions"
	MetricTurns                = "csf_session_turns_total"
	MetricTokens               = "csf_tokens_total"
	MetricCost                 = "csf_cost_usd_total"
	MetricWorkerCap            = "csf_admission_worker_cap"
	MetricFreeDisk             = "csf_admission_free_disk_bytes"
	MetricDiskFloor            = "csf_admission_disk_floor_bytes"
	MetricAdmissionHeld        = "csf_admission_held"
	MetricDispatchSlices       = "csf_dispatch_slices"
	MetricDispatchLimit        = "csf_dispatch_limit_remaining"
	MetricDispatchStage        = "csf_dispatch_stage_slices"
	MetricDispatchStageAge     = "csf_dispatch_stage_age_seconds"
	MetricDispatchLeadTime     = "csf_dispatch_lead_time_seconds"
	MetricGateDecisions        = "csf_gate_decisions_total"
	MetricReplyRefusals        = "csf_reply_gate_refusals_total"
	MetricStruggleRate         = "csf_struggle_rate_per_1k_tool_calls"
	MetricCompounding          = "csf_compounding_factor"
	MetricMerges               = "csf_merges_total"
	MetricOntologyScore        = "csf_ontology_alignment_score"
	MetricOntologySignal       = "csf_ontology_alignment_signal"
	MetricCorrections          = "csf_operator_corrections_total"
	MetricAttentionHours       = "csf_operator_attention_hours_total"
	MetricHypervisorCostEvents = "csf_hypervisor_cost_events_total"
	MetricHypervisorCostUSD    = "csf_hypervisor_cost_usd_total"
	MetricInformationDensity   = "csf_information_density"
	MetricMergeChecks          = "csf_merge_checks_total"
	MetricBazelWall            = "csf_bazel_build_wall_seconds"
	MetricBazelMemory          = "csf_bazel_peak_memory_bytes"
	MetricHostPressure         = "csf_host_pressure_some_avg10"
	MetricResumesHeld          = "csf_resumes_held"
)

// The values of the period, bound and state labels.
const (
	PeriodWeek    = "week"
	PeriodDay     = "day"
	BoundRate     = "rate"
	BoundLow      = "low"
	BoundHigh     = "high"
	StateQueued   = "queued"
	StateHeld     = "held"
	StateRunning  = "running"
	heldValue     = 1
	collectBudget = 10 * time.Second
)

// SessionState is one agent session as the session service reports it.
type SessionState struct {
	Assignment string
	Agent      string
	Phase      string
}

// SessionSource lists the host's agent sessions.
type SessionSource func(ctx context.Context) ([]SessionState, error)

// Admission is the harness launch check.
type Admission struct {
	WorkerCap      int64
	FreeBytes      uint64
	DiskFloorBytes uint64
	Held           bool
	// Pressure is the percent of the last 10 s some task waited, by
	// resource; a resource the kernel does not report is absent.
	Pressure map[string]float64
	// ResumesHeld is how many open runs the last restart still holds.
	ResumesHeld int
}

// AdmissionSource reads the harness launch check.
type AdmissionSource func(ctx context.Context) (Admission, error)

// Dispatch is the dispatcher's snapshot as the collector reads it.
type Dispatch struct {
	Queued  int
	Held    int
	Running int
	// Remaining is the launches each bounded limit still allows, by name.
	Remaining map[string]int64
	// Slices maps a launched slice's assignment to the slice.
	Slices map[string]string
	// Stages is how many slices stand in each stage, and StageAges how long
	// the oldest of them has stood there, in seconds, by stage.
	Stages    map[string]int
	StageAges map[string]float64
	// LeadTime is the mean seconds from enqueue to merge of the slices merged
	// in the last day; nil when none merged.
	LeadTime *float64
}

// DispatchSource reads the dispatcher's snapshot.
type DispatchSource func(ctx context.Context) (Dispatch, error)

// RunKey is the label set every per-run family carries.
type RunKey struct {
	Agent    string
	Executor string
	Model    string
	Slice    string
}

func (key RunKey) values(leading ...string) []string {
	return append(leading, key.Agent, key.Executor, key.Model, key.Slice)
}

// runKey labels a run, its slice read from the dispatcher's assignments.
func runKey(run Run, slices map[string]string) RunKey {
	return RunKey{Agent: run.Agent, Executor: run.Executor, Model: run.Model, Slice: slices[run.Assignment]}
}

// collector exports the families at each scrape: the per-run ones from the
// latest published measurement, the live ones from their sources.
type collector struct {
	views       *Views
	descriptors map[string]*prometheus.Desc
}

func newCollector(views *Views) *collector {
	descriptors := map[string]*prometheus.Desc{}
	for _, name := range []string{
		MetricSessions, MetricTurns, MetricTokens, MetricCost, MetricWorkerCap, MetricFreeDisk, MetricDiskFloor, MetricAdmissionHeld,
		MetricDispatchSlices, MetricDispatchLimit, MetricDispatchStage, MetricDispatchStageAge, MetricDispatchLeadTime, MetricGateDecisions, MetricReplyRefusals, MetricStruggleRate, MetricCompounding,
		MetricMerges, MetricOntologyScore, MetricOntologySignal, MetricCorrections, MetricAttentionHours,
		MetricHypervisorCostEvents, MetricHypervisorCostUSD, MetricInformationDensity,
		MetricMergeChecks, MetricBazelWall, MetricBazelMemory, MetricHostPressure, MetricResumesHeld,
	} {
		descriptors[name] = descriptor(name)
	}
	return &collector{views: views, descriptors: descriptors}
}

// Describe sends every family's descriptor, the pending ones included.
func (exporter *collector) Describe(output chan<- *prometheus.Desc) {
	for _, described := range exporter.descriptors {
		output <- described
	}
}

// Collect sends every family that has samples now. A source that fails
// sends an invalid metric, which the scrape reports, and the others still
// export.
func (exporter *collector) Collect(output chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), collectBudget)
	defer cancel()
	views := exporter.views
	var dispatch Dispatch
	if views.dispatch != nil {
		var err error
		if dispatch, err = views.dispatch(ctx); err != nil {
			output <- prometheus.NewInvalidMetric(exporter.descriptors[MetricDispatchSlices], err)
		} else {
			exporter.collectDispatch(output, dispatch)
		}
	}
	measurement := views.measurement.Load()
	if measurement != nil {
		exporter.collectRuns(output, measurement, dispatch.Slices)
		exporter.collectStruggles(output, measurement)
	}
	if views.sessions != nil {
		exporter.collectSessions(ctx, output, measurement, dispatch.Slices)
	}
	if views.admission != nil {
		exporter.collectAdmission(ctx, output)
	}
	if pulls := views.pulls.Load(); pulls != nil {
		exporter.collectPulls(output, *pulls)
	}
	exporter.collectCorrections(ctx, output, measurement)
	if events := views.costs.Load(); events != nil {
		for key, tally := range CostsThrough(*events, views.clock.Now()) {
			exporter.send(output, MetricHypervisorCostEvents, float64(tally.Events), key.Operation, key.Agent, key.Model)
			exporter.send(output, MetricHypervisorCostUSD, tally.USD, key.Operation, key.Agent, key.Model)
		}
	}
	for _, entry := range lab {
		for _, sample := range entry.Series {
			exporter.send(output, sample.Metric, sample.Value, sample.labelValues()...)
		}
	}
}

// collectCorrections sends the hand-labeled window, the attention hours the
// runs recorded and, once AFFECT types them, the corrections it finds.
func (exporter *collector) collectCorrections(ctx context.Context, output chan<- prometheus.Metric, measurement *Measurement) {
	for class, count := range ByClass(handLabeled.Corrections) {
		exporter.send(output, MetricCorrections, float64(count), class, SourceHandLabeled)
	}
	exporter.send(output, MetricAttentionHours, float64(handLabeled.AttentionHours), SourceHandLabeled)
	if measurement != nil {
		exporter.send(output, MetricAttentionHours, float64(len(measurement.OperatorHours)), SourceRuns)
	}
	if exporter.views.corrections == nil {
		return
	}
	corrections, err := exporter.views.corrections(ctx)
	if err != nil {
		output <- prometheus.NewInvalidMetric(exporter.descriptors[MetricCorrections], err)
		return
	}
	for class, count := range ByClass(corrections) {
		exporter.send(output, MetricCorrections, float64(count), class, SourceAffect)
	}
}

func (exporter *collector) send(output chan<- prometheus.Metric, name string, value float64, labels ...string) {
	valueType := prometheus.GaugeValue
	if metric, _ := catalog.Lookup(name); metric.Type == Counter {
		valueType = prometheus.CounterValue
	}
	output <- prometheus.MustNewConstMetric(exporter.descriptors[name], valueType, value, labels...)
}

func (exporter *collector) collectRuns(output chan<- prometheus.Metric, measurement *Measurement, assignments map[string]string) {
	for key, tally := range Totals(measurement.Runs, assignments) {
		tally.Series(key, func(name string, value float64, labels ...string) {
			exporter.send(output, name, value, labels...)
		})
	}
}

// Totals sums every run's tally by its label set.
func Totals(runs []Run, assignments map[string]string) map[RunKey]*Tally {
	totals := map[RunKey]*Tally{}
	for _, run := range runs {
		key := runKey(run, assignments)
		total, found := totals[key]
		if !found {
			total = newTally()
			totals[key] = total
		}
		total.add(run.Total)
	}
	return totals
}

// Struggle is the struggle rate and its compounding as of one day.
type Struggle struct {
	Week             ouroboros.Rate
	Day              ouroboros.Rate
	Compounding      ouroboros.Trend
	CompoundingDaily ouroboros.Trend
}

// StruggleThrough measures the struggle rate over the days up to and
// including day, as the loop does: the week holding day, day itself, and
// the compounding over weeks and over days.
func StruggleThrough(days []ouroboros.Rate, day string) Struggle {
	through := make([]ouroboros.Rate, 0, len(days))
	for _, rate := range days {
		if rate.Day <= day {
			through = append(through, rate)
		}
	}
	weeks := ouroboros.Weekly(through)
	struggle := Struggle{Week: ouroboros.Rate{Day: ouroboros.WeekOf(day)}, Day: ouroboros.Rate{Day: day},
		Compounding: ouroboros.Compounding(weeks, ouroboros.PerWeek), CompoundingDaily: ouroboros.Compounding(through, ouroboros.PerDay)}
	if index := slices.IndexFunc(weeks, func(rate ouroboros.Rate) bool { return rate.Day == struggle.Week.Day }); index >= 0 {
		struggle.Week = weeks[index]
	}
	if index := slices.IndexFunc(through, func(rate ouroboros.Rate) bool { return rate.Day == day }); index >= 0 {
		struggle.Day = through[index]
	}
	return struggle
}

// Bounded is one estimate with its interval, by the bound label.
func Bounded(value *float64, low *float64, high *float64) map[string]float64 {
	bounds := map[string]float64{}
	for bound, estimate := range map[string]*float64{BoundRate: value, BoundLow: low, BoundHigh: high} {
		if estimate != nil {
			bounds[bound] = *estimate
		}
	}
	return bounds
}

func (exporter *collector) collectStruggles(output chan<- prometheus.Metric, measurement *Measurement) {
	struggle := StruggleThrough(measurement.Days, measurement.At.UTC().Format(time.DateOnly))
	for bound, value := range Bounded(struggle.Week.PerK, struggle.Week.Low, struggle.Week.High) {
		exporter.send(output, MetricStruggleRate, value, PeriodWeek, bound)
	}
	for bound, value := range Bounded(struggle.Day.PerK, struggle.Day.Low, struggle.Day.High) {
		exporter.send(output, MetricStruggleRate, value, PeriodDay, bound)
	}
	for bound, value := range Bounded(struggle.Compounding.Factor, struggle.Compounding.Low, struggle.Compounding.High) {
		exporter.send(output, MetricCompounding, value, PeriodWeek, bound)
	}
	for bound, value := range Bounded(struggle.CompoundingDaily.Factor, struggle.CompoundingDaily.Low, struggle.CompoundingDaily.High) {
		exporter.send(output, MetricCompounding, value, PeriodDay, bound)
	}
}

func (exporter *collector) collectSessions(ctx context.Context, output chan<- prometheus.Metric, measurement *Measurement, assignments map[string]string) {
	sessions, err := exporter.views.sessions(ctx)
	if err != nil {
		output <- prometheus.NewInvalidMetric(exporter.descriptors[MetricSessions], err)
		return
	}
	runs := map[string]Run{}
	if measurement != nil {
		for _, run := range measurement.Runs {
			runs[run.Assignment] = run
		}
	}
	type phaseKey struct {
		phase string
		run   RunKey
	}
	counts := map[phaseKey]int{}
	for _, state := range sessions {
		run := runs[state.Assignment]
		run.Assignment, run.Agent = state.Assignment, state.Agent
		counts[phaseKey{phase: state.Phase, run: runKey(run, assignments)}]++
	}
	for key, count := range counts {
		exporter.send(output, MetricSessions, float64(count), key.run.values(key.phase)...)
	}
}

func (exporter *collector) collectAdmission(ctx context.Context, output chan<- prometheus.Metric) {
	admission, err := exporter.views.admission(ctx)
	if err != nil {
		output <- prometheus.NewInvalidMetric(exporter.descriptors[MetricWorkerCap], err)
		return
	}
	for resource, percent := range admission.Pressure {
		exporter.send(output, MetricHostPressure, percent, resource)
	}
	exporter.send(output, MetricResumesHeld, float64(admission.ResumesHeld))
	// A held admission reports only that it is held: the launch check measures
	// nothing then, and a zero would read as a measured cap.
	if admission.Held {
		exporter.send(output, MetricAdmissionHeld, heldValue)
		return
	}
	exporter.send(output, MetricAdmissionHeld, 0)
	exporter.send(output, MetricWorkerCap, float64(admission.WorkerCap))
	exporter.send(output, MetricFreeDisk, float64(admission.FreeBytes))
	exporter.send(output, MetricDiskFloor, float64(admission.DiskFloorBytes))
}

func (exporter *collector) collectDispatch(output chan<- prometheus.Metric, dispatch Dispatch) {
	exporter.send(output, MetricDispatchSlices, float64(dispatch.Queued), StateQueued)
	exporter.send(output, MetricDispatchSlices, float64(dispatch.Held), StateHeld)
	exporter.send(output, MetricDispatchSlices, float64(dispatch.Running), StateRunning)
	for limit, remaining := range dispatch.Remaining {
		exporter.send(output, MetricDispatchLimit, float64(remaining), limit)
	}
	for stage, count := range dispatch.Stages {
		exporter.send(output, MetricDispatchStage, float64(count), stage)
	}
	for stage, seconds := range dispatch.StageAges {
		exporter.send(output, MetricDispatchStageAge, seconds, stage)
	}
	if dispatch.LeadTime != nil {
		exporter.send(output, MetricDispatchLeadTime, *dispatch.LeadTime)
	}
}

// PullState is main's merge history folded through one instant: merges by
// slice, and the latest reported score and signals.
type PullState struct {
	Merges  map[string]int64
	Score   *float64
	Signals map[string]float64
}

// PullsThrough folds the merges at or before at.
func PullsThrough(pulls []MergedPull, at time.Time) PullState {
	state := PullState{Merges: map[string]int64{}, Signals: map[string]float64{}}
	for _, pull := range pulls {
		if pull.MergedAt.After(at) {
			continue
		}
		state.Merges[pull.Slice]++
		if pull.Score != nil {
			state.Score = pull.Score
		}
		if len(pull.Signals) > 0 {
			state.Signals = pull.Signals
		}
	}
	return state
}

func (exporter *collector) collectPulls(output chan<- prometheus.Metric, pulls []MergedPull) {
	state := PullsThrough(pulls, exporter.views.clock.Now())
	for slice, count := range state.Merges {
		exporter.send(output, MetricMerges, float64(count), slice)
	}
	if state.Score != nil {
		exporter.send(output, MetricOntologyScore, *state.Score)
	}
	for signal, value := range state.Signals {
		exporter.send(output, MetricOntologySignal, value, signal)
	}
}
