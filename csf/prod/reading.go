// Copyright 2026 Candace Labs

package prod

import (
	"maps"
	"slices"
)

// Reading is one measured snapshot of a repository: the global alignment
// record over the older rows plus the per-directory chief counts.
type Reading struct {
	Record Record
	// Chief is a directory's count for each chief row id; an absent id is zero.
	Chief map[string]map[string]int
	// Files are the tracked files of the revision, by repository-relative slash
	// path. DerivedShare classifies them; Equal and the seed gates read Chief
	// and Record only, so they ignore Files.
	Files []string
}

// chiefCount returns a directory's count for a chief row, zero when absent.
func chiefCount(chief map[string]map[string]int, directory, id string) int {
	if chief == nil {
		return 0
	}
	return chief[directory][id]
}

// Penalty adds the per-directory chief contribution to the older penalty: the
// score's rows are chief plus older.
func (r Reading) Penalty() int {
	total := r.Record.Penalty()
	for _, counts := range r.Chief {
		for id, count := range counts {
			total += chiefWeight(id) * count
		}
	}
	return total
}

// Score is the production score over every row, chief plus older.
func (r Reading) Score() (float64, error) { return ScoreOfPenalty(r.Penalty()) }

// Seed is the directories where every chief row holds (count zero), sorted:
// the part of the repository the loop already trusts.
func (r Reading) Seed() []string {
	var seed []string
	for directory, counts := range r.Chief {
		clean := true
		for _, count := range counts {
			if count > 0 {
				clean = false
				break
			}
		}
		if clean {
			seed = append(seed, directory)
		}
	}
	slices.Sort(seed)
	return seed
}

// AllHold reports whether every directory has all chief rows at zero: the
// chief half of the brief's done(Repo) rule.
func (r Reading) AllHold() bool {
	for _, counts := range r.Chief {
		for _, count := range counts {
			if count > 0 {
				return false
			}
		}
	}
	return true
}

// DerivedShare is the share of the revision's files that are source or derived,
// measured by reproduction rather than the generated header a file's text
// carries: Classify splits Files, and DerivedShare folds the split. A reading
// with no files is wholly derived, vacuously.
func (r Reading) DerivedShare() float64 { return Classify(r.Files).DerivedShare() }

// AuthoredFamilies groups the revision's authored files by directory, most
// files first: the report(authored, by(family), descending) the next derivation
// slices read.
func (r Reading) AuthoredFamilies() []Family { return Classify(r.Files).AuthoredFamilies() }

// Equal reports whether two readings are the same measurement: the same
// observations and the same per-directory chief counts. It backs the loop's
// equal(S1, S0) fixed-point check.
func Equal(a, b Reading) bool {
	if len(a.Chief) != len(b.Chief) {
		return false
	}
	for directory, counts := range a.Chief {
		if !maps.Equal(counts, b.Chief[directory]) {
			return false
		}
	}
	return equalObservations(a.Record.Observations, b.Record.Observations)
}

// equalObservations compares two observation lists as an unordered set keyed
// by row id, so a measurement is order-independent.
func equalObservations(a, b []ObservationEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for _, entry := range a {
		matched := false
		for _, other := range b {
			if entry.Row.ID == other.Row.ID && entry.Value == other.Value {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// Grew reports whether the seed grew strictly: every directory that was clean
// is still clean and at least one more became clean. It is the seed_grows
// predicate keep_if requires.
func Grew(before, after Reading) bool {
	beforeSeed := before.Seed()
	afterSeed := after.Seed()
	if len(afterSeed) <= len(beforeSeed) {
		return false
	}
	remaining := make(map[string]bool, len(beforeSeed))
	for _, directory := range beforeSeed {
		remaining[directory] = true
	}
	for _, directory := range afterSeed {
		delete(remaining, directory)
	}
	return len(remaining) == 0
}

// Gate is one refusal the loop applies to a candidate change.
type Gate string

const (
	// GateNewViolationInSeed refuses a change that breaks a directory that was
	// clean: a chief row there rose from zero. It is refuse(new_violation_in_seed).
	GateNewViolationInSeed Gate = "new_violation_in_seed"
	// GateRowCountRises refuses a change that raises the total penalty. It is
	// refuse(row_count_rises).
	GateRowCountRises Gate = "row_count_rises"
)

// Refusal is one refused change, logged by the loop.
type Refusal struct {
	Gate      Gate
	Signal    string
	Directory string
	Before    int
	After     int
}

// Refuses applies the two gates to a candidate change. A directory that was
// clean and now carries a chief violation is one refusal per chief row it
// broke; a rise in the total penalty is one more. An empty result is the
// nothing_breaks predicate keep_if requires.
func Refuses(before, after Reading) []Refusal {
	var refusals []Refusal
	for _, directory := range before.Seed() {
		for _, row := range ChiefRows {
			beforeCount := chiefCount(before.Chief, directory, row.ID)
			afterCount := chiefCount(after.Chief, directory, row.ID)
			if afterCount > beforeCount {
				refusals = append(refusals, Refusal{
					Gate:      GateNewViolationInSeed,
					Signal:    row.ID,
					Directory: directory,
					Before:    beforeCount,
					After:     afterCount,
				})
			}
		}
	}
	if after.Penalty() > before.Penalty() {
		refusals = append(refusals, Refusal{
			Gate:   GateRowCountRises,
			Before: before.Penalty(),
			After:  after.Penalty(),
		})
	}
	return refusals
}
