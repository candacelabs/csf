// Copyright 2026 Candace Labs

package prod

// The golden metrics are the numbers the merge quality gate holds a pull
// request to, comparing main against the merge result. The operator's ruling
// ("seed and violations over the chief rows only") defines the first three:
//
//   - the seed files are the directories with zero chief violations;
//   - a break is a new chief violation inside a directory main's seed held
//     clean;
//   - violations are the chief counts, summed per item.
//
// The fourth is the derived share (derived.go): the fraction of the tree that
// is source or derived, measured by reproduction. It is the metric the
// derivation slices raise, so the gate refuses a merge that lowers it, beside
// the seed and the violation gates.
//
// They are pure functions over two readings, so the gate is testable without
// a checker and without a repository on disk. The gate runs inside the merge
// tool (csf/githubtools), which measures the pull request's base revision and
// head revision and applies this comparison.

// MergeGate is one reason the merge quality gate refuses a merge.
type MergeGate string

const (
	// MergeGateSeedFilesFall refuses a merge that lowers the number of seed
	// files: main held more directories clean than the merge result does.
	MergeGateSeedFilesFall MergeGate = "seed_files_fall"
	// MergeGateSeedBreaks refuses a merge that adds a chief violation inside a
	// directory main's seed held clean.
	MergeGateSeedBreaks MergeGate = "seed_breaks"
	// MergeGateChiefViolationsRise refuses a merge that raises the chief
	// violation count.
	MergeGateChiefViolationsRise MergeGate = "chief_violations_rise"
	// MergeGateDerivedShareFalls refuses a merge that lowers the derived share
	// (derived.go): the merge result holds a smaller fraction of source and
	// reproduced files than main does.
	MergeGateDerivedShareFalls MergeGate = "derived_share_falls"
)

// Violations counts the chief violations across every directory, unweighted:
// one chief instance is one item. The ruling counts violations "over the chief
// rows only", so the older observations are not counted here — this is the
// "chief_violations" the gate watches, distinct from Penalty, which weights
// each count and includes the older rows.
func (r Reading) Violations() int {
	total := 0
	for _, counts := range r.Chief {
		for _, count := range counts {
			total += count
		}
	}
	return total
}

// SeedFiles is how many directories are in the seed: the directories whose
// chief rows all hold. The gate refuses a merge that lowers it.
func (r Reading) SeedFiles() int { return len(r.Seed()) }

// MergeMetrics is the golden metrics of merging after onto main, and the
// decision the merge quality gate makes from them.
type MergeMetrics struct {
	SeedFilesBefore  int
	SeedFilesAfter   int
	SeedBreaks       int
	ViolationsBefore int
	ViolationsAfter  int
	// DerivedShareBefore and DerivedShareAfter are the derived share of main
	// and of the merge result (derived.go), measured by reproduction.
	DerivedShareBefore float64
	DerivedShareAfter  float64
}

// Metrics measures the golden metrics of merging after onto main: the seed
// file counts, the chief violations per side, the seed breaks — the chief
// rows that rose from zero inside a directory main's seed held clean — and the
// derived share on each side.
func Metrics(main, after Reading) MergeMetrics {
	breaks := 0
	for _, directory := range main.Seed() {
		for _, row := range ChiefRows {
			if chiefCount(after.Chief, directory, row.ID) > chiefCount(main.Chief, directory, row.ID) {
				breaks++
			}
		}
	}
	return MergeMetrics{
		SeedFilesBefore:    main.SeedFiles(),
		SeedFilesAfter:     after.SeedFiles(),
		SeedBreaks:         breaks,
		ViolationsBefore:   main.Violations(),
		ViolationsAfter:    after.Violations(),
		DerivedShareBefore: main.DerivedShare(),
		DerivedShareAfter:  after.DerivedShare(),
	}
}

// Reasons are the merge gates the merge violates, in a fixed order: the seed
// files fell, the seed broke, the chief violations rose, or the derived share
// fell.
func (m MergeMetrics) Reasons() []MergeGate {
	var reasons []MergeGate
	if m.SeedFilesAfter < m.SeedFilesBefore {
		reasons = append(reasons, MergeGateSeedFilesFall)
	}
	if m.SeedBreaks > 0 {
		reasons = append(reasons, MergeGateSeedBreaks)
	}
	if m.ViolationsAfter > m.ViolationsBefore {
		reasons = append(reasons, MergeGateChiefViolationsRise)
	}
	if m.DerivedShareAfter < m.DerivedShareBefore {
		reasons = append(reasons, MergeGateDerivedShareFalls)
	}
	return reasons
}

// Refused reports whether the merge quality gate refuses the merge.
func (m MergeMetrics) Refused() bool { return len(m.Reasons()) > 0 }

// GateName is the merge quality gate's name, as the operator's ruling names
// it: the one gate every pull request passes through.
const GateName = "csf_github_pulls_merge"

// FileName is the file the merge quality gate's verdict is carried under in
// the state directory, and the name the ops view panel reads.
const FileName = "golden_metrics.json"

// Snapshot is the merge quality gate's verdict on merging one pull request
// onto main: the golden metrics on each side and the gate's decision. It is
// the JSON the ops view panel reads.
type Snapshot struct {
	Gate              string      `json:"gate"`
	MainRevision      string      `json:"main_revision"`
	MergeRevision     string      `json:"merge_revision"`
	SeedFilesMain     int         `json:"seed_files_main"`
	SeedFilesMerge    int         `json:"seed_files_merge"`
	SeedBreaks        int         `json:"seed_breaks"`
	ViolationsMain    int         `json:"violations_main"`
	ViolationsMerge   int         `json:"violations_merge"`
	DerivedShareMain  float64     `json:"derived_share_main"`
	DerivedShareMerge float64     `json:"derived_share_merge"`
	// AuthoredFamilies is the merge result's authored files grouped by directory,
	// most files first: the report(authored, by(family), descending) the next
	// derivation slices read (derived.go). Its head is top_families(authored), so
	// the report names where the tree is still hand-written and which family to
	// derive next.
	AuthoredFamilies []Family    `json:"authored_families"`
	Refused          bool        `json:"refused"`
	Reasons          []MergeGate `json:"reasons"`
}

// SnapshotOf measures the merge quality gate over the two readings: main and
// the merge result, each named by its revision. The derived shares are rounded
// to four places, like the score, so the record is stable. It also carries the
// merge result's authored families, the report the next derivation slices read.
func SnapshotOf(mainRevision, mergeRevision string, main, merge Reading) Snapshot {
	metrics := Metrics(main, merge)
	return Snapshot{
		Gate:              GateName,
		MainRevision:      mainRevision,
		MergeRevision:     mergeRevision,
		SeedFilesMain:     metrics.SeedFilesBefore,
		SeedFilesMerge:    metrics.SeedFilesAfter,
		SeedBreaks:        metrics.SeedBreaks,
		ViolationsMain:    metrics.ViolationsBefore,
		ViolationsMerge:   metrics.ViolationsAfter,
		DerivedShareMain:  Round4(metrics.DerivedShareBefore),
		DerivedShareMerge: Round4(metrics.DerivedShareAfter),
		AuthoredFamilies:  merge.AuthoredFamilies(),
		Refused:           metrics.Refused(),
		Reasons:           metrics.Reasons(),
	}
}
