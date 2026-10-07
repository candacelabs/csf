package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
)

// scoreboard prints every program meter as one row of a table: what it
// measures, its latest value and target, the query that computes them, and the
// slices that move it. The program's meters are declared by #485's
// scoreboard.csf; the four the scratchpad demos measured read their value from
// the two demos' recorded output, embedded below and committed beside the
// command so a rerun reproduces the same numbers with no model and no network.
//
// Consistency is the headline (csf_staging#523): the mean satisfied fraction
// across the invariants, C = mean(s(I)) with s(I) = satisfied / checked, equal
// weights, expressed as a percentage. It is the same distance the ruling
// expressed, as a number that rises as a slice raises an invariant — never a
// pass/fail. The ported query reads the committed measurement of csf_staging
// main at 369d12d (consistency_score.py, ported in house style); the evidence is
// embedded and committed beside the command so a rerun reproduces 59.0% with no
// model and no network.
const (
	scoreboardCommand = "scoreboard"

	// confidenceKnee is the pick probability below which a decision counts as
	// ambiguous until the knee is fitted from data. It matches the provisional
	// knee #485 recorded with the ambiguous meter.
	confidenceKnee = 0.6
)

// placeFilesEvidence is the place_files demo's output, committed verbatim under
// testdata; tierFromCSFEvidence is the tier_from_csf demo's. Neither carries an
// operator identifier.
//
//go:embed testdata/scoreboard/place_files.json
var placeFilesEvidence []byte

//go:embed testdata/scoreboard/tier_from_csf.json
var tierFromCSFEvidence []byte

//go:embed testdata/scoreboard/consistency.json
var consistencyEvidence []byte

// meter is one program meter: its name, what it measures, its latest value and
// target, the query that computes them, and the slices that move it. name is an
// identifier; the rest print as text.
type meter struct {
	name     string
	measures string
	now      string
	target   string
	source   string
	movedBy  []string
}

// consistencyReport is the ported consistency_score measurement: the day it was
// measured on, the commit it was measured at, and one row per invariant.
type consistencyReport struct {
	MeasuredOn string         `json:"measured_on"`
	Commit     string         `json:"commit"`
	Invariants []invariantRow `json:"invariants"`
}

// invariantRow is one invariant's row in the consistency measurement: its name,
// the instances it is satisfied over and checked over, the gate mode that holds
// it, and the slice that raises it. s(I) = satisfied / checked.
type invariantRow struct {
	Name      string `json:"name"`
	Satisfied int    `json:"satisfied"`
	Checked   int    `json:"checked"`
	Mode      string `json:"mode"`
	RaisedBy  string `json:"raised_by"`
}

// placementReport is the place_files demo's recorded output: one row per
// sampled file, each with the model's pick, the true top-level directory, the
// top-3 flag and the decision's latency.
type placementReport struct {
	Level1 []placementRow `json:"level1"`
}

type placementRow struct {
	Truth   string  `json:"truth"`
	Pick    string  `json:"pick"`
	Top3    bool    `json:"top3"`
	Latency float64 `json:"latency"`
}

// tierReport is the tier_from_csf demo's recorded output: one row per io/ipc
// package, each with the model's pick, its probability and the ontology's key
// where the package has one.
type tierReport struct {
	Rows []tierRow `json:"rows"`
}

type tierRow struct {
	Pick string  `json:"pick"`
	P    float64 `json:"p"`
	Key  string  `json:"key"`
}

// scoreboard renders the meter table. It takes no arguments and reads only the
// evidence committed with the command.
func scoreboard(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(scoreboardCommand, flag.ContinueOnError)
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("scoreboard takes no arguments; unexpected %q", flags.Arg(0))
	}
	rows, err := meters()
	if err != nil {
		return err
	}
	table := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "METER\tMEASURES\tNOW\tTARGET\tSOURCE\tMOVED BY"); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n",
			row.name, row.measures, row.now, row.target, row.source, strings.Join(row.movedBy, ", ")); err != nil {
			return err
		}
	}
	return table.Flush()
}

// meters returns every program meter in the order #485 declares them. The four
// the scratchpad demos measured (placement, decision_latency, csf_decides,
// ambiguous) are computed from the committed evidence; the rest carry the value
// recorded with the program and name the query that will compute them.
func meters() ([]meter, error) {
	placement, err := placementNow()
	if err != nil {
		return nil, err
	}
	latency, err := decisionLatencyNow()
	if err != nil {
		return nil, err
	}
	decides, err := csfDecidesNow()
	if err != nil {
		return nil, err
	}
	ambiguous, err := ambiguousNow()
	if err != nil {
		return nil, err
	}
	rows := []meter{
		{
			name:     "generated_share",
			measures: "lines csfc generates / all source lines",
			now:      "percent(2.0)",
			target:   "percent(100)",
			source:   "generated_lines",
			movedBy:  []string{"service_generator", "services_backfilled"},
		},
		{
			// harness_generated_share is the share of the harness the emitter
			// generates. slice_planner is its first part: the ordered decision a
			// dispatch pass makes about one queued slice, emitted from its declared
			// block and pinned byte for byte by a golden test.
			name:     "harness_generated_share",
			measures: "lines of the harness csfc generates / all harness lines",
			now:      "unmeasured",
			target:   "percent(100)",
			source:   "harness_generated_lines",
			movedBy:  []string{"slice_planner", "harness_bootstrap"},
		},
		{
			// stage_comparison is the self-hosting check: render the declaration
			// twice and compare. Identical means stage 1 and stage 2 are the same
			// bytes; diff_lines(N) is how far they drifted.
			name:     "stage_comparison",
			measures: "stage 1 emitter output vs stage 2, identical or diff_lines(N)",
			now:      "identical",
			target:   "identical",
			source:   "stage_comparison",
			movedBy:  []string{"slice_planner", "harness_bootstrap"},
		},
		{
			name:     "residue_bits",
			measures: "compressed size of handwritten source",
			now:      "mbit(34.3)",
			target:   "mbit(0)",
			source:   "handwritten_residue_bits",
			movedBy:  []string{"service_generator", "services_backfilled"},
		},
		{
			name:     "dirs_declared",
			measures: "directories with a declared question and options",
			now:      "ratio(37, 628)",
			target:   "ratio(628, 628)",
			source:   "declared_directories",
			movedBy:  []string{"tree_declared"},
		},
		{
			name:     "one_way",
			measures: "options with exactly one template / all options",
			now:      "unmeasured",
			target:   "ratio(1, 1)",
			source:   "options_by_template",
			movedBy:  []string{"kinds_declared", "service_generator"},
		},
		{
			name:     "compose_proven",
			measures: "theorem: every allowed combination of picks compiles",
			now:      "unproven",
			target:   "proven",
			source:   "compose_theorem",
			movedBy:  []string{"compose_proven"},
		},
		{
			name:     "compose_built",
			measures: "sampled allowed combinations that go build passes",
			now:      "unmeasured",
			target:   "ratio(1, 1)",
			source:   "sampled_combinations_build",
			movedBy:  []string{"compose_proven"},
		},
		{
			name:     "csf_decides",
			measures: "accuracy on questions whose answer CSF states (held out)",
			now:      decides,
			target:   "ratio(1, 1)",
			source:   "tier_from_csf",
			movedBy:  []string{"jev_local", "tree_declared"},
		},
		{
			name:     "ambiguous",
			measures: "questions where the best pick is below the confidence knee",
			now:      ambiguous,
			target:   "count(0)",
			source:   "tier_from_csf",
			movedBy:  []string{"kinds_declared", "tree_declared"},
		},
		{
			name:     "jev_share",
			measures: "decisions by local JEV / (decisions + operator answers + LLM-authored choices)",
			now:      "unmeasured",
			target:   "ratio(1, 1)",
			source:   "decision_sources",
			movedBy:  []string{"jev_local", "questions_to_jev"},
		},
		{
			name:     "jev_writable_share",
			measures: "grammar use-sites one JEV pick can write / all grammar use-sites",
			now:      "ratio(33, 80)",
			target:   "ratio(1, 1)",
			source:   "grammar_use_sites",
			movedBy:  []string{"jev_writable"},
		},
		{
			name:     "placement",
			measures: "top-1 placing a file among the 16 top-level directories (181 files)",
			now:      placement,
			target:   "ratio(1, 1)",
			source:   "place_files",
			movedBy:  []string{"tree_declared"},
		},
		{
			name:     "decision_latency",
			measures: "p50 seconds per local decision, RTX 5070, jevk5-9b Q4_K_M",
			now:      latency,
			target:   "falling",
			source:   "place_files",
			movedBy:  []string{"jev_local"},
		},
		{
			name:     "llm_tokens",
			measures: "LLM tokens per merged slice",
			now:      "unmeasured",
			target:   "falling",
			source:   "tokens_per_slice",
			movedBy:  []string{"jev_local"},
		},
		{
			name:     "j",
			measures: "bits(language) + sum p(w) bits(w | language)",
			now:      "unmeasured",
			target:   "falling",
			source:   "language_score",
			movedBy:  []string{"language_score"},
		},
		{
			name:     "derived_share",
			measures: "files that are source or reproduced / all files, by reproduction not by a generated header",
			now:      "percent(23.5)",
			target:   "percent(100)",
			source:   "derived_share",
			movedBy:  []string{"derived_share", "service_generator", "services_backfilled"},
		},
		{
			name:     "cost_per_fix",
			measures: "worker cost per fix, one point per bootstrap round",
			now:      "unmeasured",
			target:   "falling",
			source:   "cost_per_fix",
			movedBy:  []string{"bootstrap_loop"},
		},
	}
	// Consistency is the headline (csf_staging#523): the mean satisfied fraction
	// across the invariants heads the table, and the 18 meters follow it.
	consistency, err := consistencyMeter()
	if err != nil {
		return nil, err
	}
	return append([]meter{consistency}, rows...), nil
}

// consistencyMeter is the scoreboard headline (csf_staging#523): the mean
// satisfied fraction across the invariants, C = mean(s(I)) with
// s(I) = satisfied / checked, equal weights, expressed as a percentage. It rises
// as a slice raises an invariant and never gates, so it is a distance in the
// same sense the ruling meant — 59.0% on main, not yet 100%. The now value is
// computed from the committed measurement, so a rerun reproduces it.
func consistencyMeter() (meter, error) {
	now, err := consistencyNow()
	if err != nil {
		return meter{}, err
	}
	report, err := decodeConsistency()
	if err != nil {
		return meter{}, err
	}
	moved := make([]string, 0, len(report.Invariants))
	seen := make(map[string]bool, len(report.Invariants))
	for _, row := range report.Invariants {
		if row.RaisedBy == "" || seen[row.RaisedBy] {
			continue
		}
		seen[row.RaisedBy] = true
		moved = append(moved, row.RaisedBy)
	}
	return meter{
		name:     "consistency",
		measures: "mean satisfied fraction across the invariants, s(I) = satisfied / checked",
		now:      now,
		target:   "percent(100)",
		source:   "consistency_score",
		movedBy:  moved,
	}, nil
}

// consistencyNow is the consistency query: the mean of s(I) = satisfied / checked
// over the invariants, expressed as a percentage. It reads the committed
// measurement of csf_staging main at 369d12d (the ported consistency_score.py)
// and reproduces 59.0%.
func consistencyNow() (string, error) {
	report, err := decodeConsistency()
	if err != nil {
		return "", err
	}
	if len(report.Invariants) == 0 {
		return "", fmt.Errorf("consistency evidence holds no invariants")
	}
	total := 0.0
	for _, row := range report.Invariants {
		if row.Checked == 0 {
			return "", fmt.Errorf("invariant %q checked 0", row.Name)
		}
		total += float64(row.Satisfied) / float64(row.Checked)
	}
	return fmt.Sprintf("percent(%.1f)", 100*total/float64(len(report.Invariants))), nil
}

func decodeConsistency() (consistencyReport, error) {
	var report consistencyReport
	if err := json.Unmarshal(consistencyEvidence, &report); err != nil {
		return report, fmt.Errorf("decode consistency evidence: %w", err)
	}
	return report, nil
}

// placementNow is the place_files query: the share of files whose first pick is
// the true top-level directory, and the share whose top three include it.
func placementNow() (string, error) {
	report, err := decodePlacement()
	if err != nil {
		return "", err
	}
	top1, top3 := 0, 0
	for _, row := range report.Level1 {
		if row.Pick == row.Truth {
			top1++
		}
		if row.Top3 {
			top3++
		}
	}
	files := float64(len(report.Level1))
	return fmt.Sprintf("percent(%.1f) top3(%.1f)", 100*float64(top1)/files, 100*float64(top3)/files), nil
}

// decisionLatencyNow is the place_files query's latency: the median seconds per
// local decision.
func decisionLatencyNow() (string, error) {
	report, err := decodePlacement()
	if err != nil {
		return "", err
	}
	latencies := make([]float64, 0, len(report.Level1))
	for _, row := range report.Level1 {
		latencies = append(latencies, row.Latency)
	}
	slices.Sort(latencies)
	return fmt.Sprintf("seconds(%.2f)", latencies[len(latencies)/2]), nil
}

// csfDecidesNow is the tier_from_csf query: over the packages whose tier the
// ontology states, how many the model placed on that key.
func csfDecidesNow() (string, error) {
	report, err := decodeTier()
	if err != nil {
		return "", err
	}
	keyed, correct := 0, 0
	for _, row := range report.Rows {
		if row.Key == "" {
			continue
		}
		keyed++
		if row.Pick == row.Key {
			correct++
		}
	}
	return fmt.Sprintf("ratio(%d, %d)", correct, keyed), nil
}

// ambiguousNow is the tier_from_csf query's ambiguity: the decisions whose best
// pick is below the confidence knee. #485 recorded 7 of 22 from an earlier run;
// the committed evidence yields 5, and rerun_reproduces_now_values wants the
// value the evidence reproduces, so the meter carries the computed one.
func ambiguousNow() (string, error) {
	report, err := decodeTier()
	if err != nil {
		return "", err
	}
	below := 0
	for _, row := range report.Rows {
		if row.P < confidenceKnee {
			below++
		}
	}
	return fmt.Sprintf("ratio(%d, %d) below(%.1f, provisional_until_knee_fit)", below, len(report.Rows), confidenceKnee), nil
}

func decodePlacement() (placementReport, error) {
	var report placementReport
	if err := json.Unmarshal(placeFilesEvidence, &report); err != nil {
		return report, fmt.Errorf("decode place_files evidence: %w", err)
	}
	return report, nil
}

func decodeTier() (tierReport, error) {
	var report tierReport
	if err := json.Unmarshal(tierFromCSFEvidence, &report); err != nil {
		return report, fmt.Errorf("decode tier_from_csf evidence: %w", err)
	}
	return report, nil
}
