// Copyright 2026 Candace Labs

package jobs

import (
	"fmt"
	"math"
	"time"
)

// ObservationEntry is one item of an Observation: exactly one of its fields is set.
type ObservationEntry struct {
	Definition  *MetricDefinition
	Measurement *Measurement
	Phase       *PhaseReport
}

// DefinitionEntry wraps a metric definition as an entry.
func DefinitionEntry(definition MetricDefinition) ObservationEntry {
	return ObservationEntry{Definition: &definition}
}

// MeasurementEntry wraps a measurement as an entry.
func MeasurementEntry(measurement Measurement) ObservationEntry {
	return ObservationEntry{Measurement: &measurement}
}

// PhaseEntry wraps a phase report as an entry.
func PhaseEntry(report PhaseReport) ObservationEntry { return ObservationEntry{Phase: &report} }

// MetricDefinition declares a metric before it is measured. Redeclaring it
// with a different unit or description fails.
type MetricDefinition struct {
	Name        string
	Unit        string
	Description string
}

// Measurement is one value of a declared metric at a step.
type Measurement struct {
	Metric     string
	Step       uint64
	Value      float64
	RecordedAt time.Time
}

// RecordedMeasurement is a stored measurement with its evidence hash.
type RecordedMeasurement struct {
	Measurement
	EvidenceHash string
}

// Phase is a lifecycle report from whatever runs the job.
type Phase string

// The phases a worker may report.
const (
	PhaseStarted   Phase = "started"
	PhaseCompleted Phase = "completed"
	PhaseFailed    Phase = "failed"
	PhaseCancelled Phase = "cancelled"
)

// PhaseReport is a worker's lifecycle report with its message.
type PhaseReport struct {
	Phase   Phase
	Message string
}

// Observation is a batch of entries read from one piece of evidence, whose
// SHA-256 every measurement in the batch carries.
type Observation struct {
	EvidenceHash string
	Entries      []ObservationEntry
}

// observationRules are the ledger's settings that judge an observation.
type observationRules struct {
	progressMetric string
	metricLimit    int
}

// observationPlan is what applying an observation writes: the definitions
// and measurements to insert, in order, and the job's resulting progress.
type observationPlan struct {
	definitions    []MetricDefinition
	measurements   []Measurement
	completedUnits int64
	state          State
	reason         string
}

// planObservation judges an observation against the job and its declared
// metrics without touching the database. The inserts it plans can still be
// refused by the database when they conflict with a stored replay.
func planObservation[Spec any](rules observationRules, job Job[Spec], declared map[string]MetricDefinition, observation Observation) (observationPlan, error) {
	plan := observationPlan{completedUnits: job.CompletedUnits, state: job.State, reason: job.Reason}
	if len(observation.Entries) == 0 || !evidenceSHA256.MatchString(observation.EvidenceHash) {
		return plan, fmt.Errorf("%w: entries and a SHA-256 evidence hash are required", ErrInvalidObservation)
	}
	registry := make(map[string]MetricDefinition, len(declared))
	for name, definition := range declared {
		registry[name] = definition
	}
	for _, entry := range observation.Entries {
		var err error
		switch {
		case entry.Definition != nil && entry.Measurement == nil && entry.Phase == nil:
			err = plan.define(rules, registry, *entry.Definition)
		case entry.Measurement != nil && entry.Definition == nil && entry.Phase == nil:
			err = plan.measure(rules, job.TotalUnits, registry, *entry.Measurement)
		case entry.Phase != nil && entry.Definition == nil && entry.Measurement == nil:
			err = plan.report(job.TotalUnits, job.CancellationRequested, *entry.Phase)
		default:
			err = fmt.Errorf("%w: an entry sets exactly one field", ErrInvalidObservation)
		}
		if err != nil {
			return plan, err
		}
	}
	return plan, nil
}

func (plan *observationPlan) define(rules observationRules, registry map[string]MetricDefinition, definition MetricDefinition) error {
	if definition.Name == "" || definition.Unit == "" {
		return fmt.Errorf("%w: a metric needs a name and a unit", ErrInvalidObservation)
	}
	if previous, exists := registry[definition.Name]; exists {
		if previous != definition {
			return fmt.Errorf("%w: %s", ErrMetricRedefined, definition.Name)
		}
		return nil
	}
	if len(registry) >= rules.metricLimit {
		return ErrMetricLimit
	}
	registry[definition.Name] = definition
	plan.definitions = append(plan.definitions, definition)
	return nil
}

func (plan *observationPlan) measure(rules observationRules, totalUnits int64, registry map[string]MetricDefinition, measurement Measurement) error {
	if _, exists := registry[measurement.Metric]; !exists {
		return fmt.Errorf("%w: %s", ErrUnregisteredMetric, measurement.Metric)
	}
	if math.IsNaN(measurement.Value) || math.IsInf(measurement.Value, 0) || measurement.RecordedAt.IsZero() {
		return fmt.Errorf("%w: a measurement needs a finite value and a time", ErrInvalidObservation)
	}
	if measurement.Step > uint64(totalUnits) {
		return fmt.Errorf("%w: step %d of %d", ErrOutsideAdmission, measurement.Step, totalUnits)
	}
	if measurement.Metric == rules.progressMetric {
		value := measurement.Value
		if value != math.Trunc(value) || value < 0 || value > float64(totalUnits) || uint64(value) != measurement.Step {
			return fmt.Errorf("%w: completed units must equal the step within the admitted total", ErrOutsideAdmission)
		}
		plan.completedUnits = max(plan.completedUnits, int64(value))
	}
	plan.measurements = append(plan.measurements, measurement)
	return nil
}

func (plan *observationPlan) report(totalUnits int64, cancellationRequested bool, report PhaseReport) error {
	switch report.Phase {
	case PhaseStarted:
		if plan.state == StatePending {
			plan.state = StateRunning
		}
	case PhaseCompleted:
		if !plan.state.Terminal() {
			if plan.completedUnits != totalUnits {
				return ErrIncompleteCompletion
			}
			plan.state, plan.reason = StateSucceeded, report.Message
		}
	case PhaseFailed:
		// A retained worker failure, including one in artifact delivery or
		// cleanup after the work completed, invalidates an earlier success.
		plan.state, plan.reason = StateFailed, report.Message
	case PhaseCancelled:
		if cancellationRequested {
			plan.state, plan.reason = StateCancelled, report.Message
		}
	default:
		return fmt.Errorf("%w: unsupported phase %q", ErrInvalidObservation, report.Phase)
	}
	return nil
}
