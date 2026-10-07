// Copyright 2026 Candace Labs

package jev

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"time"
)

// The held-out eval under the readout: a corpus of typed questions whose
// answers are declared in this tree, measured against every declared decision
// model, and the choice the measurement supports. The corpus and the table it
// produced are committed and compiled into the binary, so `csf decide`'s
// default and the reasons for it travel with the code that answers.
const (
	treeQuestionsFile = "testdata/csf_tree_questions.jsonl"
	evalTableFile     = "testdata/eval_table.json"

	// eceBins is how many confidence buckets the expected calibration error
	// averages over; a confidence lands in bucket floor(confidence × eceBins).
	eceBins = 10
	// latencyKneeFactor bounds the measured latency knee: a candidate whose
	// median latency is more than this factor above the fastest measured
	// candidate's has left the interactive band and cannot be chosen.
	latencyKneeFactor = 2.0
)

var (
	// ErrNoEvalQuestions reports a held-out run with no question to ask.
	ErrNoEvalQuestions = errors.New("jev: the held-out corpus is empty")
	// ErrNoMeasuredModel reports an eval table with no measured candidate, so
	// no latency knee can be taken.
	ErrNoMeasuredModel = errors.New("jev: no decision model was measured")
	// ErrNoChoiceWithinKnee reports an eval in which no measured candidate is
	// within the latency knee, so none may be chosen.
	ErrNoChoiceWithinKnee = errors.New("jev: no measured decision model is within the latency knee")
	// ErrBadTreeQuestion reports a corpus question the decider could not be
	// asked, such as one with no gold answer.
	ErrBadTreeQuestion = errors.New("jev: invalid held-out question")
)

//go:embed testdata/csf_tree_questions.jsonl
//go:embed testdata/eval_table.json
var evalData embed.FS

// TreeQuestion is one held-out question from the csf_tree_questions corpus: the
// state text it is asked about, the typed question, the answer the tree
// declares, and where that answer lives.
type TreeQuestion struct {
	State    string   `json:"state"`
	Kind     Kind     `json:"kind"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Answer   string   `json:"answer"`
	Source   string   `json:"source"`
}

// Typed returns the question the decider is asked, dropping the gold answer and
// the source, which belong to the eval and not to the model.
func (tree TreeQuestion) Typed() Question {
	return Question{Kind: tree.Kind, Question: tree.Question, Options: tree.Options}
}

// LoadTreeQuestions reads the committed held-out corpus. Every question must
// name a kind the model answers and carry the answer the tree declares.
func LoadTreeQuestions() ([]TreeQuestion, error) {
	content, err := evalData.ReadFile(treeQuestionsFile)
	if err != nil {
		return nil, fmt.Errorf("jev: read the held-out corpus: %w", err)
	}
	questions := []TreeQuestion{}
	decoder := json.NewDecoder(bytes.NewReader(content))
	for {
		var question TreeQuestion
		err := decoder.Decode(&question)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("jev: decode the held-out corpus: %w", err)
		}
		if question.Answer == "" {
			return nil, fmt.Errorf("%w: %q carries no answer", ErrBadTreeQuestion, question.Question)
		}
		if err := question.Typed().Validate(); err != nil {
			return nil, fmt.Errorf("%w: %q: %w", ErrBadTreeQuestion, question.Question, err)
		}
		questions = append(questions, question)
	}
	if len(questions) == 0 {
		return nil, ErrNoEvalQuestions
	}
	return questions, nil
}

// EvalMetrics is one held-out run's readout over the corpus: the fraction of
// questions answered as the tree declares, the expected calibration error of
// those answers, the median per-question latency in seconds, and the decisions
// answered per second.
type EvalMetrics struct {
	Accuracy           float64 `json:"accuracy"`
	ECE                float64 `json:"ece"`
	P50Latency         float64 `json:"p50_latency"`
	DecisionsPerSecond float64 `json:"decisions_per_second"`
}

// ModelResult is one declared candidate's held-out run: the model it names and
// the metrics the run measured.
type ModelResult struct {
	Model   string      `json:"model"`
	Metrics EvalMetrics `json:"metrics"`
}

// EvalTable is the committed held-out result, one row per declared candidate.
// It is the measurement [ChosenModel] selects from.
type EvalTable []ModelResult

// LoadEvalTable reads the committed eval table measured over the corpus.
func LoadEvalTable() (EvalTable, error) {
	content, err := evalData.ReadFile(evalTableFile)
	if err != nil {
		return nil, fmt.Errorf("jev: read the eval table: %w", err)
	}
	var table EvalTable
	if err := json.Unmarshal(content, &table); err != nil {
		return nil, fmt.Errorf("jev: decode the eval table: %w", err)
	}
	if len(table) == 0 {
		return nil, ErrNoMeasuredModel
	}
	return table, nil
}

// Evaluate answers every question in the corpus with decider and measures the
// run. One question is one decision, timed on its own, so the median is a
// decision's latency rather than a batch's.
func Evaluate(ctx context.Context, decider IDecider, questions []TreeQuestion) (EvalMetrics, error) {
	if len(questions) == 0 {
		return EvalMetrics{}, ErrNoEvalQuestions
	}
	latencies := make([]float64, 0, len(questions))
	confidences := make([]float64, 0, len(questions))
	correct := make([]bool, 0, len(questions))
	started := time.Now()
	for index, tree := range questions {
		if err := ctx.Err(); err != nil {
			return EvalMetrics{}, err
		}
		question := tree.Typed()
		before := time.Now()
		distributions, err := decider.Decide(ctx, tree.State, []Question{question})
		if err != nil {
			return EvalMetrics{}, fmt.Errorf("jev: question %d %q: %w", index+1, tree.Question, err)
		}
		if len(distributions) != 1 {
			return EvalMetrics{}, fmt.Errorf("jev: question %d %q: the decider answered %d distributions, not one",
				index+1, tree.Question, len(distributions))
		}
		latencies = append(latencies, time.Since(before).Seconds())
		top, confidence := distributions[0].Top()
		correct = append(correct, top == tree.Answer)
		confidences = append(confidences, confidence)
	}
	elapsed := time.Since(started).Seconds()
	return EvalMetrics{
		Accuracy:           accuracy(correct),
		ECE:                calibrationError(confidences, correct),
		P50Latency:         median(latencies),
		DecisionsPerSecond: decisionsPerSecond(len(questions), elapsed),
	}, nil
}

// accuracy is the fraction of questions answered as the tree declares.
func accuracy(correct []bool) float64 {
	if len(correct) == 0 {
		return 0
	}
	hits := 0
	for _, hit := range correct {
		if hit {
			hits++
		}
	}
	return float64(hits) / float64(len(correct))
}

// calibrationError is the expected calibration error of the top answers: the
// confidence-weighted mean gap between each bucket's mean confidence and the
// accuracy observed in it. A well-calibrated model scores near zero.
func calibrationError(confidences []float64, correct []bool) float64 {
	counts := make([]int, eceBins)
	correctCounts := make([]int, eceBins)
	sums := make([]float64, eceBins)
	for index, confidence := range confidences {
		bucket := bucket(confidence)
		counts[bucket]++
		sums[bucket] += confidence
		if correct[index] {
			correctCounts[bucket]++
		}
	}
	total := float64(len(confidences))
	ece := 0.0
	for bucket := 0; bucket < eceBins; bucket++ {
		if counts[bucket] == 0 {
			continue
		}
		observed := float64(correctCounts[bucket]) / float64(counts[bucket])
		predicted := sums[bucket] / float64(counts[bucket])
		ece += float64(counts[bucket]) / total * math.Abs(observed-predicted)
	}
	return ece
}

// bucket is the eceBins bucket a confidence lands in; the top bucket is closed
// at one.
func bucket(confidence float64) int {
	index := int(confidence * eceBins)
	if index < 0 {
		return 0
	}
	if index >= eceBins {
		return eceBins - 1
	}
	return index
}

// median is the middle value of a copy sorted ascending, or the mean of the two
// middle values when there is an even number of them.
func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

// decisionsPerSecond is decisions over elapsed seconds; zero when the run
// measured no time.
func decisionsPerSecond(decisions int, elapsed float64) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(decisions) / elapsed
}

// LatencyKnee is the measured latency knee: latencyKneeFactor times the fastest
// measured candidate's median latency, being the latency past which a candidate
// has left the interactive band. It reports false when no candidate was
// measured.
func LatencyKnee(table EvalTable) (float64, bool) {
	fastest := math.Inf(1)
	measured := false
	for _, row := range table {
		if row.Metrics.P50Latency <= 0 {
			continue
		}
		measured = true
		fastest = math.Min(fastest, row.Metrics.P50Latency)
	}
	if !measured {
		return 0, false
	}
	return fastest * latencyKneeFactor, true
}

// ChosenModel selects the declared decision model the eval supports: the
// measured candidate with the best accuracy whose median latency is within the
// measured latency knee. Ties keep the earlier row, so the declaration order
// decides them.
func ChosenModel(table EvalTable) (DeciderModel, error) {
	knee, measured := LatencyKnee(table)
	if !measured {
		return DeciderModel{}, ErrNoMeasuredModel
	}
	best := ""
	bestAccuracy := 0.0
	for _, row := range table {
		if row.Metrics.P50Latency <= 0 || row.Metrics.P50Latency > knee {
			continue
		}
		if best == "" || row.Metrics.Accuracy > bestAccuracy {
			best, bestAccuracy = row.Model, row.Metrics.Accuracy
		}
	}
	if best == "" {
		return DeciderModel{}, ErrNoChoiceWithinKnee
	}
	return DeclaredModel(best)
}
