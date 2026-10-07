// Copyright 2026 Candace Labs

// Package evaluate scores csf builds on a held-out, rotated suite of past
// tickets before they go live (EVAL-SUITE, #416). A suite ticket is replayed
// from the commit its pull request started from, on the build being scored,
// and read at a fixed budget of tool calls by the loop's own struggle
// definition (ouroboros.StrugglesWithin); the merged change is the
// reference its files are compared with. The suite is hidden from the
// miners and the loop's corpus readers, rotates weekly, and never holds a
// ticket the loop evolves the harness on.
package evaluate

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/candacelabs/csf/services/ouroboros"
)

const (
	// RotationPeriod is how long one suite version stands: the struggle
	// rate's own reporting period, a week, so every weekly reading of the
	// headline is on one fixed suite.
	RotationPeriod = 7 * 24 * time.Hour
	// budgetQuantile picks the replay budget from the pool's original runs:
	// the tool calls the shortest tenth of them reached, so nine in ten
	// suite tickets were worked at least that far when they were real, and a
	// replay cut there reads the same opening of work on every ticket.
	budgetQuantile = 0.10
	// holdoutShare is the share of the pool one version holds out: half, so
	// consecutive versions share half their tickets in expectation and the
	// other half is fresh, which is what keeps a build from being tuned to
	// one fixed list.
	holdoutShare = 0.5
	z95          = 1.959964
	perThousand  = 1000
)

var (
	// ErrEmptyPool reports a selection with no eligible ticket.
	ErrEmptyPool = errors.New("evaluate: no eligible ticket for the suite")
	// ErrNotDisjoint reports a suite holding a ticket the loop evolves the
	// harness on.
	ErrNotDisjoint = errors.New("evaluate: the suite and the evolution tickets overlap")
)

// Ticket is one past ticket in the pool or the suite: the pull request that
// closed it, the commit that pull request started from, its merge commit,
// the files the merged change touched (the reference) and the original run
// whose recipe a replay reuses, with how that run read at the suite budget.
type Ticket struct {
	Number      int64    `json:"ticket"`
	PullRequest int64    `json:"pull_request"`
	BaseCommit  string   `json:"base_commit"`
	MergeCommit string   `json:"merge_commit"`
	Files       []string `json:"files"`
	Assignment  string   `json:"assignment"`
	// ToolCalls and Episodes are the original run's whole reading, used to
	// derive the budget and the size; they are not a score.
	ToolCalls int64 `json:"tool_calls"`
	Episodes  int64 `json:"episodes"`
}

// Derivation records how a suite's size and budget follow from the pool:
// every number a reader needs to redo the arithmetic.
type Derivation struct {
	Pool       int     `json:"pool"`
	Evolution  int     `json:"evolution_excluded"`
	Budget     int     `json:"budget_tool_calls"`
	BudgetFrom string  `json:"budget_from"`
	Rate       float64 `json:"pool_rate_per_1k"`
	Dispersion float64 `json:"dispersion"`
	Effect     float64 `json:"effect_log"`
	Needed     int     `json:"replays_needed"`
	Size       int     `json:"size"`
	SizeFrom   string  `json:"size_from"`
	Resolves   float64 `json:"resolves_factor"`
}

// Suite is one version of the held-out suite.
type Suite struct {
	Version    int       `json:"version"`
	SelectedAt time.Time `json:"selected_at"`
	// Model is the model every replay of this version runs on, so two
	// builds' scores differ by the build alone.
	Model string `json:"model"`
	// Tools are the tool rules every replay of this version may use; empty
	// in a record made before they were recorded, which means ReplayTools.
	Tools        []string   `json:"tools,omitempty"`
	RotatesAt    time.Time  `json:"rotates_at"`
	Tickets      []Ticket   `json:"tickets"`
	EvolutionSet []int64    `json:"evolution_tickets"`
	Derivation   Derivation `json:"derivation"`
}

// ReplayTools are the tool rules a replay may use: the files, all of git
// (the replay repository's origin is its own local copy, so even a push
// stays there), read-only gh, the pinned build, the repository's own
// scripts and the read-only shell tools. Nothing that reaches past the
// replay is allowed (gh writes, csf send, merge or submit, the csf MCP
// tools, an arbitrary bash -c), because a replay re-runs a real brief that
// may say to do exactly that. A refused call is a struggle signal on every
// build alike, so scores stay comparable.
var ReplayTools = []string{
	"Read", "Write", "Edit", "Glob", "Grep", "TodoWrite",
	"Bash(git:*)",
	"Bash(gh issue view:*)", "Bash(gh issue list:*)", "Bash(gh pr view:*)", "Bash(gh pr list:*)", "Bash(gh pr diff:*)", "Bash(gh run list:*)", "Bash(gh run view:*)",
	// The repository's checks by name: tools/ also holds merge-pr.sh and
	// close-ticket.sh, which act on the real repository.
	"Bash(tools/bazel.sh:*)", "Bash(bash tools/check-house-lint.sh:*)", "Bash(bash tools/ontology-score.sh:*)",
	"Bash(bash tools/check-merge.sh:*)", "Bash(bash tools/check-generated.sh:*)", "Bash(python3 tools/check_operator_identifiers.py:*)",
	"Bash(docker run:*)", "Bash(docker build:*)",
	"Bash(ls:*)", "Bash(cat:*)", "Bash(head:*)", "Bash(tail:*)", "Bash(wc:*)", "Bash(grep:*)", "Bash(rg:*)", "Bash(find:*)",
	"Bash(sed:*)", "Bash(awk:*)", "Bash(sort:*)", "Bash(uniq:*)", "Bash(diff:*)", "Bash(jq:*)", "Bash(stat:*)", "Bash(tree:*)",
	"Bash(echo:*)", "Bash(printf:*)", "Bash(pwd)", "Bash(mkdir:*)", "Bash(cp:*)", "Bash(mv:*)", "Bash(touch:*)", "Bash(gofmt:*)",
}

// tools is the suite's replay tool rules.
func (suite Suite) tools() []string {
	if len(suite.Tools) == 0 {
		return ReplayTools
	}
	return suite.Tools
}

// Due reports whether the suite's rotation time has come at now; no suite
// at all is due.
func (suite *Suite) Due(now time.Time) bool {
	return suite == nil || !now.Before(suite.RotatesAt)
}

// Holds reports whether ticket is in the suite.
func (suite Suite) Holds(ticket int64) bool {
	return slices.ContainsFunc(suite.Tickets, func(held Ticket) bool { return held.Number == ticket })
}

// Select draws suite version from the pool: the tickets the loop evolves
// the harness on are removed, the budget and size are derived from the
// rest, and the held-out tickets are the size first by a hash of the
// version and the ticket, so a version always draws the same tickets and
// the next draws afresh.
func Select(pool []Ticket, evolution []int64, version int, model string, now time.Time) (Suite, error) {
	eligible := make([]Ticket, 0, len(pool))
	for _, ticket := range pool {
		if !slices.Contains(evolution, ticket.Number) && ticket.ToolCalls > 0 {
			eligible = append(eligible, ticket)
		}
	}
	if len(eligible) == 0 {
		return Suite{}, ErrEmptyPool
	}
	derivation := derive(eligible)
	derivation.Evolution = len(pool) - len(eligible)
	slices.SortFunc(eligible, func(a, b Ticket) int {
		return cmp.Compare(drawKey(version, a.Number), drawKey(version, b.Number))
	})
	tickets := eligible[:min(derivation.Size, len(eligible))]
	slices.SortFunc(tickets, func(a, b Ticket) int { return cmp.Compare(a.Number, b.Number) })
	evolutionSet := slices.Clone(evolution)
	slices.Sort(evolutionSet)
	suite := Suite{
		Version: version, SelectedAt: now, Model: model, Tools: slices.Clone(ReplayTools), RotatesAt: now.Add(RotationPeriod),
		Tickets: tickets, EvolutionSet: evolutionSet, Derivation: derivation,
	}
	return suite, Disjoint(suite, evolution)
}

// Disjoint checks that no suite ticket is one the loop evolves the harness
// on (#416 Build 7).
func Disjoint(suite Suite, evolution []int64) error {
	var shared []string
	for _, ticket := range suite.Tickets {
		if slices.Contains(evolution, ticket.Number) {
			shared = append(shared, "#"+strconv.FormatInt(ticket.Number, 10))
		}
	}
	if len(shared) > 0 {
		return fmt.Errorf("%w: %v", ErrNotDisjoint, shared)
	}
	return nil
}

// derive fixes the budget and the size from the eligible tickets' original
// runs. The effect to resolve is one week of the loop's baseline compounding
// (×1.156, #330), on the log scale; the replays needed are those whose
// pooled episodes put the 95% interval of the log rate within that effect,
// allowing for the pool's overdispersion; the size is the smaller of that
// and the held-out share of the pool, and the factor the chosen size
// resolves is recorded beside it.
func derive(eligible []Ticket) Derivation {
	calls := make([]int64, 0, len(eligible))
	var totalCalls, totalEpisodes int64
	for _, ticket := range eligible {
		calls = append(calls, ticket.ToolCalls)
		totalCalls += ticket.ToolCalls
		totalEpisodes += ticket.Episodes
	}
	slices.Sort(calls)
	budget := int(calls[int(math.Floor(budgetQuantile*float64(len(calls)-1)))])
	rate := float64(totalEpisodes) / float64(totalCalls)
	dispersion := 1.0
	if rate > 0 && len(eligible) > 1 {
		var chi float64
		for _, ticket := range eligible {
			expected := rate * float64(ticket.ToolCalls)
			chi += (float64(ticket.Episodes) - expected) * (float64(ticket.Episodes) - expected) / expected
		}
		dispersion = math.Max(1, chi/float64(len(eligible)-1))
	}
	effect := math.Log(ouroboros.BaselineWeeklyFactor)
	perReplay := math.Max(rate*float64(budget), math.SmallestNonzeroFloat64)
	needed := int(math.Ceil(dispersion * (z95 / effect) * (z95 / effect) / perReplay))
	share := max(1, int(math.Floor(holdoutShare*float64(len(eligible)))))
	size, sizeFrom := needed, "replays needed to resolve one week of baseline compounding"
	if share < needed {
		size, sizeFrom = share, "held-out half of the eligible pool (fewer than needed)"
	}
	resolves := math.Exp(z95 * math.Sqrt(dispersion/(perReplay*float64(size))))
	return Derivation{
		Pool: len(eligible), Budget: budget,
		BudgetFrom: fmt.Sprintf("p%.0f of the eligible tickets' original tool calls", budgetQuantile*100),
		Rate:       perThousand * rate, Dispersion: dispersion, Effect: effect,
		Needed: needed, Size: size, SizeFrom: sizeFrom, Resolves: resolves,
	}
}

// drawKey orders tickets for one version's draw.
func drawKey(version int, ticket int64) uint64 {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d/%d", version, ticket)))
	return binary.BigEndian.Uint64(sum[:8])
}
