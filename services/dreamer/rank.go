// Copyright 2026 Candace Labs

package dreamer

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
	"github.com/candacelabs/csf/services/dispatch"
)

// SourceName names where a candidate came from.
type SourceName string

// The sources, primary first, then the harness's own gaps.
const (
	SourceStruggle SourceName = "struggle_class"
	SourceFinding  SourceName = "ungated_finding"
	SourceOntology SourceName = "ontology_signal"
	SourceTicket   SourceName = "unowned_ticket"
	SourceRuling   SourceName = "unenforced_ruling"
	SourceRefusal  SourceName = "harness_refusal"
	SourceCost     SourceName = "harness_cost"
	SourceSlowest  SourceName = "harness_slowest"
)

// Candidate is one piece of work a source proposes, with the inputs its rank
// is computed from and the typed acceptance and evidence its slice carries.
type Candidate struct {
	Source SourceName `json:"source"`
	// Key is the work's stable identity; the slice identifier derives from
	// it, so the same work is never added twice.
	Key   string `json:"key"`
	Title string `json:"title"`
	// TicketURL is the existing ticket the work delivers; empty when the
	// dreamer opens one.
	TicketURL string `json:"ticket_url,omitempty"`
	// Frequency is how many times a week the problem occurs, with how that
	// was measured.
	Frequency           float64 `json:"frequency"`
	FrequencyDerivation string  `json:"frequency_derivation"`
	// Fixed and Attempted are the measured outcomes of earlier work on the
	// same class: finished attempts and those that delivered.
	Fixed     int `json:"fixed"`
	Attempted int `json:"attempted"`
	// Known is true when earlier work on the class exists: exploration does
	// not weigh it.
	Known      bool     `json:"known"`
	Evidence   []string `json:"evidence"`
	Acceptance []string `json:"acceptance"`
}

// Ranked is one candidate with its score and every input it came from.
type Ranked struct {
	Candidate
	Fixability           float64 `json:"fixability"`
	FixabilityDerivation string  `json:"fixability_derivation"`
	Explore              float64 `json:"explore"`
	Score                float64 `json:"score"`
	Rank                 int     `json:"rank"`
}

// World is what a source may read of the dispatcher at the pass that asks
// it: the time and every slice in the graph.
type World struct {
	Now    time.Time
	Slices []*dispatchv1.SliceNode
}

// Source is one registered origin of work. A fallback source is asked only
// when every primary source found nothing.
type Source struct {
	Name     string
	Fallback bool
	Find     func(ctx context.Context, world World) ([]Candidate, error)
}

// Outcomes are finished attempts and the ones that delivered.
type Outcomes struct {
	Fixed     int `json:"fixed"`
	Attempted int `json:"attempted"`
}

// Rank scores every candidate and orders them best first: score, then the
// source order the constants declare, then the key. outcomes are the
// dreamer's own slices' outcomes by source; explore weighs unknown classes.
func Rank(candidates []Candidate, outcomes map[SourceName]Outcomes, explore float64) []Ranked {
	ranked := make([]Ranked, 0, len(candidates))
	for _, candidate := range candidates {
		own := outcomes[candidate.Source]
		fixed, attempted := candidate.Fixed+own.Fixed, candidate.Attempted+own.Attempted
		entry := Ranked{Candidate: candidate, Explore: 1}
		entry.Fixability = float64(fixed+1) / float64(attempted+2)
		entry.FixabilityDerivation = fmt.Sprintf("(%d fixed + 1) / (%d attempted + 2): %d/%d of this class's earlier work and %d/%d of the dreamer's %s slices",
			fixed, attempted, candidate.Fixed, candidate.Attempted, own.Fixed, own.Attempted, candidate.Source)
		if !candidate.Known {
			entry.Explore = explore
		}
		entry.Score = candidate.Frequency * entry.Fixability * entry.Explore
		ranked = append(ranked, entry)
	}
	slices.SortStableFunc(ranked, func(a, b Ranked) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score), cmp.Compare(sourceOrder(a.Source), sourceOrder(b.Source)), cmp.Compare(a.Key, b.Key))
	})
	for index := range ranked {
		ranked[index].Rank = index + 1
	}
	return ranked
}

// sourceOrder is a source's position in the declared order.
func sourceOrder(source SourceName) int {
	return slices.Index([]SourceName{SourceStruggle, SourceFinding, SourceOntology, SourceTicket, SourceRuling, SourceRefusal, SourceCost, SourceSlowest}, source)
}

// Target is how long the ready frontier should be, with what it was derived
// from.
type Target struct {
	Ready    int `json:"ready"`
	Target   int `json:"target"`
	Capacity int `json:"capacity"`
	Running  int `json:"running"`
	// Completions is how many running sessions are expected to finish before
	// the next pass.
	Completions int `json:"completions"`
	// Measured is how many merged slices the median duration is over.
	Measured      int     `json:"measured"`
	MedianSeconds float64 `json:"median_seconds"`
	Derivation    string  `json:"derivation"`
}

// DeriveTarget is the ready frontier the admission can use until the next
// pass: the launches it allows now (capacity less what runs) plus the
// running sessions expected to finish within one period, running × period /
// the median enqueue-to-merge duration of the merged slices. Before any slice
// has merged the second term is zero. ready counts the queued frontier and
// the dreamer's own slices held for visibility, which the next pass releases.
func DeriveTarget(snapshot dispatch.Snapshot, durations []time.Duration, period time.Duration, visibility string) Target {
	target := Target{Capacity: snapshot.Capacity, Running: len(snapshot.Running), Measured: len(durations)}
	for _, view := range snapshot.Queue {
		if view.Ready {
			target.Ready++
		}
	}
	for _, view := range snapshot.Held {
		if view.Held == visibility && view.Ready && view.State == dispatchv1.SliceState_SLICE_STATE_QUEUED.String() {
			target.Ready++
		}
	}
	free := max(0, target.Capacity-target.Running)
	completions := "no merged slice measured yet, so no completions are expected"
	if len(durations) > 0 {
		sorted := slices.Clone(durations)
		slices.Sort(sorted)
		median := sorted[len(sorted)/2]
		if len(sorted)%2 == 0 {
			median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
		}
		target.MedianSeconds = median.Seconds()
		if median > 0 {
			target.Completions = int(math.Ceil(float64(target.Running) * period.Seconds() / median.Seconds()))
		}
		completions = fmt.Sprintf("%d running × %s period / %s median enqueue-to-merge over %d merged slices = %d expected to finish",
			target.Running, period, median.Round(time.Second), len(sorted), target.Completions)
	}
	target.Target = free + target.Completions
	target.Derivation = fmt.Sprintf("capacity %d − %d running = %d launches now; %s; target %d, ready %d",
		target.Capacity, target.Running, free, completions, target.Target, target.Ready)
	return target
}

// zeroLimits are the bounded limits that admit no launch now.
func zeroLimits(limits []dispatch.Limit) []dispatch.Limit {
	var binding []dispatch.Limit
	for _, limit := range limits {
		if limit.Bounded && limit.Launches <= 0 {
			binding = append(binding, limit)
		}
	}
	return binding
}
