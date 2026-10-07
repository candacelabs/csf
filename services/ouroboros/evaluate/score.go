// Copyright 2026 Candace Labs

package evaluate

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/candacelabs/csf/services/ouroboros"
)

// The node kinds a replay runs on: this host, or a cloud burst job.
const (
	NodeHost  = "host"
	NodeBurst = "burst"

	usdMicrosPerUSD = 1_000_000
)

var (
	// ErrUnscored reports a build with no complete score on the suite.
	ErrUnscored = errors.New("evaluate: the build has no complete score on the current suite")
	// ErrRegressed reports a build whose score is worse than the live
	// build's beyond the bound.
	ErrRegressed = errors.New("evaluate: the build regressed beyond the bound")
)

// Replay is one suite ticket replayed on one build, read at the suite's
// budget: its tool calls and struggle episodes, the share of the reference's
// files it touched, what it cost and how long it ran.
type Replay struct {
	Build         string    `json:"build"`
	SuiteVersion  int       `json:"suite_version"`
	Ticket        int64     `json:"ticket"`
	Node          string    `json:"node"`
	Assignment    string    `json:"assignment"`
	ToolCalls     int64     `json:"tool_calls"`
	Episodes      int64     `json:"episodes"`
	Recall        float64   `json:"recall"`
	CostUSDMicros int64     `json:"cost_usd_micros"`
	Seconds       int64     `json:"seconds"`
	RecordedAt    time.Time `json:"recorded_at"`
}

// Read is what one replay's event log contributes at budget: its tool calls
// and struggle episodes by the loop's definition.
func Read(events []byte, budget int) (toolCalls int64, episodes int64) {
	reading := ouroboros.StrugglesWithin(events, time.UTC, budget)
	for _, count := range reading.Calls {
		toolCalls += count
	}
	for _, count := range reading.Episodes {
		episodes += count
	}
	return toolCalls, episodes
}

// Recall is the share of the reference's files the replay's change touched;
// a reference with no files is fully recalled by nothing, so 0.
func Recall(reference []string, touched []string) float64 {
	if len(reference) == 0 {
		return 0
	}
	hits := 0
	for _, file := range reference {
		if slices.Contains(touched, file) {
			hits++
		}
	}
	return float64(hits) / float64(len(reference))
}

// Score is a build's result on one suite version: struggle episodes per
// 1,000 tool calls over its replays with the 95% Garwood interval (the
// headline), the mean recall of the reference, what scoring cost and how
// long it took, and the replays per node kind.
type Score struct {
	Build        string         `json:"build"`
	SuiteVersion int            `json:"suite_version"`
	Replays      int            `json:"replays"`
	Expected     int            `json:"expected"`
	Complete     bool           `json:"complete"`
	ToolCalls    int64          `json:"tool_calls"`
	Episodes     int64          `json:"episodes"`
	PerK         *float64       `json:"per_1k"`
	Low          *float64       `json:"low"`
	High         *float64       `json:"high"`
	Recall       float64        `json:"recall"`
	CostUSD      float64        `json:"cost_usd"`
	WallSeconds  float64        `json:"wall_seconds"`
	Nodes        map[string]int `json:"nodes"`
}

// ScoreOf folds a build's replays on suite into its score; wall is the
// scoring run's wall clock, zero when none was recorded.
func ScoreOf(build string, suite Suite, replays []Replay, wall time.Duration) Score {
	score := Score{Build: build, SuiteVersion: suite.Version, Expected: len(suite.Tickets), Nodes: map[string]int{}, WallSeconds: wall.Seconds()}
	var cost int64
	for _, replay := range replays {
		if replay.Build != build || replay.SuiteVersion != suite.Version || !suite.Holds(replay.Ticket) {
			continue
		}
		score.Replays++
		score.ToolCalls += replay.ToolCalls
		score.Episodes += replay.Episodes
		score.Recall += replay.Recall
		cost += replay.CostUSDMicros
		score.Nodes[replay.Node]++
	}
	score.Complete = score.Expected > 0 && score.Replays == score.Expected
	if score.Replays > 0 {
		score.Recall /= float64(score.Replays)
	}
	score.CostUSD = float64(cost) / usdMicrosPerUSD
	score.PerK, score.Low, score.High = ouroboros.PoissonRate(score.Episodes, score.ToolCalls)
	return score
}

// Comparison is a candidate build against the live one on the same suite
// version: the difference of the pooled scores and the bound it is judged
// by, from the suite's own run-to-run variance (the spread of the per-ticket
// differences of the two builds' replays).
type Comparison struct {
	Delta  float64 `json:"delta_per_1k"`
	Bound  float64 `json:"bound_per_1k"`
	Paired int     `json:"paired_tickets"`
}

// Compare judges candidate against live: delta is candidate minus live
// (negative is better) and bound is z95 times the standard error of the
// mean per-ticket difference. With fewer than two paired tickets there is
// no variance to bound by, and the bound is infinite.
func Compare(candidate []Replay, live []Replay) Comparison {
	rates := func(replays []Replay) map[int64]float64 {
		byTicket := map[int64]float64{}
		for _, replay := range replays {
			if replay.ToolCalls > 0 {
				byTicket[replay.Ticket] = perThousand * float64(replay.Episodes) / float64(replay.ToolCalls)
			}
		}
		return byTicket
	}
	candidateRates, liveRates := rates(candidate), rates(live)
	var differences []float64
	for ticket, rate := range candidateRates {
		if liveRate, paired := liveRates[ticket]; paired {
			differences = append(differences, rate-liveRate)
		}
	}
	pooled := func(replays []Replay) float64 {
		var calls, episodes int64
		for _, replay := range replays {
			calls += replay.ToolCalls
			episodes += replay.Episodes
		}
		if calls == 0 {
			return 0
		}
		return perThousand * float64(episodes) / float64(calls)
	}
	comparison := Comparison{Delta: pooled(candidate) - pooled(live), Bound: math.Inf(1), Paired: len(differences)}
	if len(differences) < 2 {
		return comparison
	}
	var mean float64
	for _, difference := range differences {
		mean += difference
	}
	mean /= float64(len(differences))
	var variance float64
	for _, difference := range differences {
		variance += (difference - mean) * (difference - mean)
	}
	variance /= float64(len(differences) - 1)
	comparison.Bound = z95 * math.Sqrt(variance/float64(len(differences)))
	return comparison
}

// Admit is the score-before-live check: the candidate must have a complete
// score on the suite, and when the live build has one too, the candidate
// must not be worse beyond the bound. It returns the comparison it judged by,
// nil when the live build had no complete score to compare with.
func Admit(suite Suite, candidate []Replay, live []Replay, candidateBuild string, liveBuild string) (*Comparison, error) {
	if !ScoreOf(candidateBuild, suite, candidate, 0).Complete {
		return nil, fmt.Errorf("%w: build %s, suite v%d", ErrUnscored, candidateBuild, suite.Version)
	}
	if candidateBuild == liveBuild || !ScoreOf(liveBuild, suite, live, 0).Complete {
		return nil, nil
	}
	comparison := Compare(candidate, live)
	if comparison.Delta > comparison.Bound {
		return &comparison, fmt.Errorf("%w: build %s is %+.1f per 1k worse than live %s, bound %.1f (suite v%d)",
			ErrRegressed, candidateBuild, comparison.Delta, liveBuild, comparison.Bound, suite.Version)
	}
	return &comparison, nil
}
