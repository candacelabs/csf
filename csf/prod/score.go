// Copyright 2026 Candace Labs

// Package prod computes the bootstrap production score in Go: the ontology
// alignment score over the reference "older" rows plus the bootstrap "chief"
// rows, and the seed and the two gates the bootstrap loop keeps by. It mirrors
// tools/house_lint/alignment.ml — the same scale, method version, penalty
// arithmetic and checker-output parsers — so a reading over the older rows
// reproduces the reference score, and it adds the per-directory chief rows
// (every_directory_declared, fanout_at_most_16, grammar_decision_fits_one_pick,
// grammar_generated_from_kinds, cli_purpose_declared, csf_only_under_csf) the
// loop drives to zero. The score reads no files and no environment: every input
// is a value, so it is testable against the reference's own vectors.
package prod

import (
	"encoding/json"
	"fmt"
	"math"
)

const (
	// Scale is the penalty at which the score reads one half, fixed with the
	// method version so two scores are comparable only under the same method.
	Scale = 1000
	// MethodVersion is the scoring method the reference checker measures.
	MethodVersion = 1
	// Schema versions the alignment record.
	Schema = "candace.ontology.alignment/v1"
)

// Kind distinguishes the two halves of the score: the bootstrap chief rows,
// measured per directory, and the reference older rows, measured over the tree.
type Kind string

const (
	KindChief Kind = "chief"
	KindOlder Kind = "older"
)

// Row is one scored signal.
type Row struct {
	ID       string
	Kind     Kind
	Weight   int
	Blocking bool
	Meaning  string
}

// ChiefRows are the six bootstrap invariants, measured per directory: a
// directory is clean (in the seed) only when every one holds there. They are
// the chief rows the merge quality gate counts violations over — the operator
// ruled the gate reads "seed and violations over the chief rows only" — and
// the chief(H) atoms of the brief's done(Repo) rule. Each names a meter the
// scoreboard declares (csf/observability/scoreboard.csf).
var ChiefRows = []Row{
	{ID: "every_directory_declared", Kind: KindChief, Weight: 10, Blocking: true, Meaning: "every directory declares its question and options (every_directory_declared)"},
	{ID: "fanout_at_most_16", Kind: KindChief, Weight: 10, Blocking: true, Meaning: "no question offers more than 16 options (fanout_at_most_16)"},
	{ID: "grammar_decision_fits_one_pick", Kind: KindChief, Weight: 10, Blocking: true, Meaning: "every decision grammar fits one pick (grammar_decision_fits_one_pick)"},
	{ID: "grammar_generated_from_kinds", Kind: KindChief, Weight: 10, Blocking: true, Meaning: "every grammar is generated from kinds (grammar_generated_from_kinds)"},
	{ID: "cli_purpose_declared", Kind: KindChief, Weight: 10, Blocking: true, Meaning: "every CLI command declares its purpose (cli_purpose_declared)"},
	{ID: "csf_only_under_csf", Kind: KindChief, Weight: 10, Blocking: true, Meaning: "every .csf source lives under csf/ (csf_only_under_csf)"},
}

// OlderRows are the eight reference alignment signals, with the same ids,
// weights and blocking flags as tools/house_lint/alignment.ml. They are the
// "older" half of the score's rows.
var OlderRows = []Row{
	{ID: "csfc-check", Kind: KindOlder, Weight: 10, Blocking: true, Meaning: "csfc check diagnostics against csf/architecture/architecture.csf"},
	{ID: "generated-drift", Kind: KindOlder, Weight: 10, Blocking: true, Meaning: "generated projections that differ from their source (csfc check-generated and the language generator's check)"},
	{ID: "cs-16", Kind: KindOlder, Weight: 5, Blocking: true, Meaning: "network, gRPC and PostgreSQL crossings outside ipc/"},
	{ID: "cs-15", Kind: KindOlder, Weight: 3, Blocking: false, Meaning: "go statements with no visible owner (no join and no context-driven exit)"},
	{ID: "cs-17", Kind: KindOlder, Weight: 3, Blocking: false, Meaning: "process environment reads outside runtime/config"},
	{ID: "ontology-dirs", Kind: KindOlder, Weight: 3, Blocking: false, Meaning: "directories whose role segment is not an ontology term"},
	{ID: "retired-vocabulary", Kind: KindOlder, Weight: 2, Blocking: false, Meaning: "retired ontology words in tracked READMEs"},
	{ID: "unlinked-terms", Kind: KindOlder, Weight: 1, Blocking: true, Meaning: "ontology terms in tracked READMEs without a link to their definition"},
}

// Rows is the whole registry, chief rows first then older rows.
var Rows = append(append([]Row{}, ChiefRows...), OlderRows...)

// RowByID returns the row with the given id from Rows.
func RowByID(id string) (Row, bool) {
	for _, row := range Rows {
		if row.ID == id {
			return row, true
		}
	}
	return Row{}, false
}

// chiefWeight reports a chief row's weight; an unknown chief id adds nothing.
func chiefWeight(id string) int {
	for _, row := range ChiefRows {
		if row.ID == id {
			return row.Weight
		}
	}
	return 0
}

// Observation is one measured count or an explicit not-measured marker, so an
// absent checker never reads as a clean zero.
type Observation struct {
	count    int
	reason   string
	measured bool
}

// Measured is a signal whose checker ran.
func Measured(count int) Observation { return Observation{count: count, measured: true} }

// NotMeasured marks a signal whose checker has not landed, with the reason.
func NotMeasured(reason string) Observation { return Observation{reason: reason} }

// IsMeasured reports whether the checker ran.
func (o Observation) IsMeasured() bool { return o.measured }

// Count returns the measured count and whether it was measured.
func (o Observation) Count() (int, bool) { return o.count, o.measured }

// Reason returns the not-measured reason, empty when measured.
func (o Observation) Reason() string { return o.reason }

// Contribution is weight times count, zero when not measured.
func (o Observation) Contribution(row Row) int {
	if !o.measured {
		return 0
	}
	return row.Weight * o.count
}

// ObservationEntry pairs a row with its observation, mirroring the reference
// record's ordered (signal, observation) list: a record carries only the
// observations it was given.
type ObservationEntry struct {
	Row   Row
	Value Observation
}

// Entry builds an observation entry for a row id from the registry.
func Entry(id string, observation Observation) (ObservationEntry, bool) {
	row, ok := RowByID(id)
	if !ok {
		return ObservationEntry{}, false
	}
	return ObservationEntry{Row: row, Value: observation}, true
}

// Record is an alignment record over the older rows, mirroring the reference
// record. Observations is an ordered list of only the observations it carries.
type Record struct {
	Revision     string
	Dirty        bool
	CommitTime   int64
	Observations []ObservationEntry
}

// ObservationFor returns the observation for an older row id, defaulting to
// not measured when the record does not name it.
func (r Record) ObservationFor(id string) Observation {
	for _, entry := range r.Observations {
		if entry.Row.ID == id {
			return entry.Value
		}
	}
	return NotMeasured("absent from the record")
}

// Penalty is the weighted sum of measured observations, exactly as the
// reference folds over its observations.
func (r Record) Penalty() int {
	total := 0
	for _, entry := range r.Observations {
		total += entry.Value.Contribution(entry.Row)
	}
	return total
}

// Complete reports whether every carried observation was measured.
func (r Record) Complete() bool {
	for _, entry := range r.Observations {
		if !entry.Value.IsMeasured() {
			return false
		}
	}
	return true
}

// Score is the reference score over the record's older rows.
func (r Record) Score() (float64, error) { return ScoreOfPenalty(r.Penalty()) }

// ScoreOfPenalty maps a penalty into [0, 1) with the fixed scale, exactly as
// the reference: a negative penalty is refused. The value is unrounded; the
// reference rounds only where a record is serialized.
func ScoreOfPenalty(penalty int) (float64, error) {
	if penalty < 0 {
		return 0, fmt.Errorf("alignment penalty cannot be negative: %d", penalty)
	}
	return float64(penalty) / float64(penalty+Scale), nil
}

// Round4 rounds to four decimal places.
func Round4(value float64) float64 { return math.Round(value*10000) / 10000 }

// signalJSON is one row of the alignment record's serialized signals list.
type signalJSON struct {
	ID           string `json:"id"`
	Weight       int    `json:"weight"`
	Blocking     bool   `json:"blocking"`
	Status       string `json:"status"`
	Count        *int   `json:"count"`
	Contribution int    `json:"contribution"`
	Meaning      string `json:"meaning"`
	Reason       string `json:"reason,omitempty"`
}

// recordJSON is the alignment record's serialized shape.
type recordJSON struct {
	Schema        string       `json:"schema"`
	MethodVersion int          `json:"method_version"`
	Revision      string       `json:"revision"`
	Dirty         bool         `json:"dirty"`
	CommitTime    int64        `json:"commit_time"`
	Score         float64      `json:"score"`
	LowerIsBetter bool         `json:"lower_is_better"`
	Penalty       int          `json:"penalty"`
	Scale         int          `json:"scale"`
	Formula       string       `json:"formula"`
	Complete      bool         `json:"complete"`
	Signals       []signalJSON `json:"signals"`
}

// MarshalJSON emits the reference alignment record shape: signals in the
// order they are carried, a measured count as a number and a not-measured
// count as null with its reason.
func (r Record) MarshalJSON() ([]byte, error) {
	score, err := r.Score()
	if err != nil {
		return nil, err
	}
	signals := make([]signalJSON, 0, len(r.Observations))
	for _, entry := range r.Observations {
		row := signalJSON{
			ID:           entry.Row.ID,
			Weight:       entry.Row.Weight,
			Blocking:     entry.Row.Blocking,
			Status:       "measured",
			Contribution: entry.Value.Contribution(entry.Row),
			Meaning:      entry.Row.Meaning,
		}
		if entry.Value.IsMeasured() {
			count, _ := entry.Value.Count()
			row.Count = &count
		} else {
			row.Status = "not_measured"
			row.Reason = entry.Value.Reason()
		}
		signals = append(signals, row)
	}
	return json.Marshal(recordJSON{
		Schema:        Schema,
		MethodVersion: MethodVersion,
		Revision:      r.Revision,
		Dirty:         r.Dirty,
		CommitTime:    r.CommitTime,
		Score:         Round4(score),
		LowerIsBetter: true,
		Penalty:       r.Penalty(),
		Scale:         Scale,
		Formula:       "score = penalty / (penalty + scale); penalty = sum(weight * count) over measured signals",
		Complete:      r.Complete(),
		Signals:       signals,
	})
}
