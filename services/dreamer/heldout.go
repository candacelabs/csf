// Copyright 2026 Candace Labs

package dreamer

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	stdfs "io/fs"
	"path"
	"strings"
	"time"

	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/ouroboros"
)

// heldOutShare: one run in this many is held out. Four fifths stay for
// choosing work, and the held-out interval's width, reported beside its
// factor, shows what the fifth costs in precision.
const heldOutShare = 5

// The partitions a comparison reports.
const (
	PartitionAll      = "all"
	PartitionInSample = "in_sample"
	PartitionHeldOut  = "held_out"
)

// HeldOut reports whether a run is held out: the FNV-1a hash of its name
// modulo heldOutShare is zero. It is deterministic, so a run never changes
// sides, and the dreamer never reads a held-out run to choose work.
func HeldOut(run string) bool {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(run))
	return hash.Sum32()%heldOutShare == 0
}

// RunOf is the run an item of the corpus belongs to: its first path element.
func RunOf(item string) string {
	run, _, _ := strings.Cut(item, "/")
	return run
}

// Partition is one side of the comparison: its runs, its weekly struggle
// rates and their compounding factor per week with the 95% interval.
type Partition struct {
	Name   string           `json:"name"`
	Runs   int              `json:"runs"`
	Weeks  []ouroboros.Rate `json:"weeks"`
	Factor ouroboros.Trend  `json:"factor"`
}

// Comparison is the held-out check: the weekly struggle factor on every run,
// on the in-sample runs the dreamer chooses work from and on the held-out
// runs it never reads, and the exploration weight the held-out series sets.
type Comparison struct {
	ComputedAt time.Time `json:"computed_at"`
	Week       string    `json:"week"`
	All        Partition `json:"all"`
	InSample   Partition `json:"in_sample"`
	HeldOut    Partition `json:"held_out"`
	// Explore is one plus the trailing complete weeks whose held-out rate
	// did not fall below the week before.
	Explore           float64 `json:"explore"`
	ExploreDerivation string  `json:"explore_derivation"`
}

// tally is one partition's counts by calendar day.
type tally struct {
	runs     int
	calls    map[string]int64
	episodes map[string]int64
}

func (counts *tally) add(reading ouroboros.Reading) {
	counts.runs++
	for day, count := range reading.Calls {
		counts.calls[day] += count
	}
	for day, count := range reading.Episodes {
		counts.episodes[day] += count
	}
}

func (counts *tally) partition(name string) Partition {
	rates, _ := ouroboros.DailyRates([]ouroboros.Reading{{Calls: counts.calls, Episodes: counts.episodes}})
	weeks := ouroboros.Weekly(rates)
	return Partition{Name: name, Runs: counts.runs, Weeks: weeks, Factor: ouroboros.Compounding(weeks, ouroboros.PerWeek)}
}

// Compare reads every run's event log in corpus, one run directory each, as
// the mining loop's measure reads it, and compares the partitions at now.
func Compare(ctx context.Context, corpus stdfs.FS, location *time.Location, now time.Time) (Comparison, error) {
	entries, err := stdfs.ReadDir(corpus, ".")
	if err != nil {
		return Comparison{}, fmt.Errorf("dreamer: list the corpus: %w", err)
	}
	newTally := func() *tally { return &tally{calls: map[string]int64{}, episodes: map[string]int64{}} }
	all, inSample, heldOut := newTally(), newTally(), newTally()
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return Comparison{}, err
		}
		if !entry.IsDir() {
			continue
		}
		lines, err := stdfs.ReadFile(corpus, path.Join(entry.Name(), session.EventsFile))
		if errors.Is(err, stdfs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Comparison{}, fmt.Errorf("dreamer: read %s: %w", entry.Name(), err)
		}
		reading := ouroboros.Struggles(lines, location)
		all.add(reading)
		if HeldOut(entry.Name()) {
			heldOut.add(reading)
		} else {
			inSample.add(reading)
		}
	}
	comparison := Comparison{
		ComputedAt: now.UTC(), Week: ouroboros.WeekOf(now.In(location).Format(time.DateOnly)),
		All: all.partition(PartitionAll), InSample: inSample.partition(PartitionInSample), HeldOut: heldOut.partition(PartitionHeldOut),
	}
	comparison.Explore, comparison.ExploreDerivation = exploreWeight(comparison.HeldOut.Weeks, comparison.Week)
	return comparison, nil
}

// exploreWeight is one plus the trailing complete weeks, before current,
// whose held-out rate was not below the week before it: each week the
// struggle rate does not fall doubles down on new classes rather than
// polishing known ones, and one falling week resets it.
func exploreWeight(weeks []ouroboros.Rate, current string) (float64, string) {
	var complete []ouroboros.Rate
	for _, week := range weeks {
		if week.Day < current && week.PerK != nil {
			complete = append(complete, week)
		}
	}
	streak := 0
	for index := len(complete) - 1; index > 0; index-- {
		if *complete[index].PerK < *complete[index-1].PerK {
			break
		}
		streak++
	}
	derivation := fmt.Sprintf("held-out: %d complete weeks with a rate; %d trailing weeks did not fall; explore = 1 + %d", len(complete), streak, streak)
	if len(complete) > 0 {
		last := complete[len(complete)-1]
		derivation += fmt.Sprintf("; latest complete week %s at %.1f per 1k (%d / %d calls)", last.Day, *last.PerK, last.Struggles, last.ToolCalls)
	}
	return float64(1 + streak), derivation
}
