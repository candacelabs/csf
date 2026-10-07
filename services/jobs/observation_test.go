// Copyright 2026 Candace Labs

package jobs

import (
	"math"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Unit specs for the internals that judge observations and decode rows.

type unitSpec struct {
	Shards int `json:"shards"`
}

var _ = Describe("planObservation", func() {
	const progress = "units_completed"
	rules := observationRules{progressMetric: progress, metricLimit: 2}
	evidence := strings.Repeat("a", 64)
	recorded := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var job Job[unitSpec]
	var declared map[string]MetricDefinition
	BeforeEach(func() {
		job = Job[unitSpec]{ID: "job", State: StateQueued, TotalUnits: 2}
		declared = map[string]MetricDefinition{progress: {Name: progress, Unit: "count", Description: "units"}}
	})
	plan := func(entries ...ObservationEntry) (observationPlan, error) {
		return planObservation(rules, job, declared, Observation{EvidenceHash: evidence, Entries: entries})
	}
	measure := func(step uint64) ObservationEntry {
		return MeasurementEntry(Measurement{Metric: progress, Step: step, Value: float64(step), RecordedAt: recorded})
	}

	It("grows completed units from the progress metric and completes only when every unit is done", func() {
		_, err := plan(PhaseEntry(PhaseReport{Phase: PhaseCompleted}))
		Expect(err).To(MatchError(ErrIncompleteCompletion))
		result, err := plan(measure(1), measure(2), PhaseEntry(PhaseReport{Phase: PhaseCompleted, Message: "done"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.completedUnits).To(Equal(int64(2)))
		Expect(result.state).To(Equal(StateSucceeded))
		Expect(result.reason).To(Equal("done"))
		Expect(result.measurements).To(HaveLen(2))
	})
	It("lets a later failure override an earlier success and ignores an unrequested cancellation", func() {
		result, err := plan(measure(2), PhaseEntry(PhaseReport{Phase: PhaseCompleted}), PhaseEntry(PhaseReport{Phase: PhaseFailed, Message: "upload"}), PhaseEntry(PhaseReport{Phase: PhaseCancelled}))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.state).To(Equal(StateFailed))
		Expect(result.reason).To(Equal("upload"))
		job.CancellationRequested = true
		result, err = plan(PhaseEntry(PhaseReport{Phase: PhaseCancelled, Message: "stopped"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.state).To(Equal(StateCancelled))
	})
	It("starts only a pending job", func() {
		result, err := plan(PhaseEntry(PhaseReport{Phase: PhaseStarted}))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.state).To(Equal(StateQueued))
		job.State = StatePending
		result, err = plan(PhaseEntry(PhaseReport{Phase: PhaseStarted}))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.state).To(Equal(StateRunning))
	})
	DescribeTable("rejects observations the job did not admit",
		func(want error, entries ...ObservationEntry) {
			_, err := plan(entries...)
			Expect(err).To(MatchError(want))
		},
		Entry("an undeclared metric", ErrUnregisteredMetric, MeasurementEntry(Measurement{Metric: "other", Step: 1, Value: 1, RecordedAt: recorded})),
		Entry("a step past the admitted units", ErrOutsideAdmission, measure(3)),
		Entry("progress that differs from its step", ErrOutsideAdmission, MeasurementEntry(Measurement{Metric: progress, Step: 1, Value: 2, RecordedAt: recorded})),
		Entry("a fractional progress value", ErrOutsideAdmission, MeasurementEntry(Measurement{Metric: progress, Step: 1, Value: 1.5, RecordedAt: recorded})),
		Entry("a non-finite value", ErrInvalidObservation, MeasurementEntry(Measurement{Metric: progress, Step: 1, Value: math.Inf(1), RecordedAt: recorded})),
		Entry("an untimed value", ErrInvalidObservation, MeasurementEntry(Measurement{Metric: progress, Step: 1, Value: 1})),
		Entry("a redefinition", ErrMetricRedefined, DefinitionEntry(MetricDefinition{Name: progress, Unit: "seconds"})),
		Entry("a definition past the metric limit", ErrMetricLimit, DefinitionEntry(MetricDefinition{Name: "b", Unit: "count"}), DefinitionEntry(MetricDefinition{Name: "c", Unit: "count"})),
		Entry("an unknown phase", ErrInvalidObservation, PhaseEntry(PhaseReport{Phase: "paused"})),
		Entry("an entry with two fields", ErrInvalidObservation, ObservationEntry{Definition: &MetricDefinition{Name: "d", Unit: "u"}, Phase: &PhaseReport{Phase: PhaseStarted}}),
		Entry("an empty entry", ErrInvalidObservation, ObservationEntry{}),
	)
	It("requires entries and a SHA-256 evidence hash", func() {
		_, err := planObservation(rules, job, declared, Observation{EvidenceHash: "short", Entries: []ObservationEntry{measure(1)}})
		Expect(err).To(MatchError(ErrInvalidObservation))
		_, err = planObservation(rules, job, declared, Observation{EvidenceHash: evidence})
		Expect(err).To(MatchError(ErrInvalidObservation))
	})
	It("plans an identical redefinition as no insert and a new one as one insert", func() {
		result, err := plan(DefinitionEntry(declared[progress]), DefinitionEntry(MetricDefinition{Name: "seconds", Unit: "s"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.definitions).To(Equal([]MetricDefinition{{Name: "seconds", Unit: "s"}}))
	})
})

var _ = Describe("stored requests", func() {
	It("round-trips a typed request through its stored JSON and hash", func() {
		encoded, digest, err := encodeRequest(unitSpec{Shards: 3})
		Expect(err).NotTo(HaveOccurred())
		Expect(digest).To(MatchRegexp(`^[0-9a-f]{64}$`))
		job, err := decodeJob[unitSpec](csfpg.CsfJob{JobID: "job", Request: encoded, RequestSha256: digest, State: csfpg.CsfJobStateRunning, TimeoutSeconds: 60})
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Spec).To(Equal(unitSpec{Shards: 3}))
		Expect(job.Timeout).To(Equal(time.Minute))
		Expect(job.State.Terminal()).To(BeFalse())
		Expect(job.CreatedAt.IsZero()).To(BeTrue(), "an absent timestamp stays zero")
	})
	It("refuses a stored request of another shape", func() {
		_, err := decodeJob[unitSpec](csfpg.CsfJob{JobID: "job", Request: []byte(`{"shards":"three"}`)})
		Expect(err).To(HaveOccurred())
	})
})
