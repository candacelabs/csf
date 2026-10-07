// Copyright 2026 Candace Labs

package ouroboros

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/ipc/proc"
)

// The internal check (#120 amendment B): structure gained per operator
// intervention, per week. The numerator is merged enforced structure,
// counted from git on main at each week's end: session gates (the Gate
// constants of the session gate), accepted miners (directories with a
// rules file) and ontology terms (term declarations). Recorded intents live
// in csfpg, not in git, so they are not counted here. The denominator is
// operator interventions: until the AFFECT miner labels corrections, the
// operator-authored messages into sessions (every prompt after a run's
// first turn), so the series is marked proxy. The slope of log(y) on the
// week index is the compounding exponent.
const (
	SeriesStructure         = "ouroboros.structure_per_intervention"
	SeriesStructureExponent = "ouroboros.structure_exponent"
	// StructureProxy marks the denominator as the proxy it is until the
	// AFFECT miner labels corrections.
	StructureProxy = "proxy: operator-authored messages stand in for interventions until the AFFECT miner labels corrections"

	gateFile          = "services/harness/sessiongate/gate.go"
	ontologyFile      = "csf/compiler/language/architecture.csf"
	minersTree        = "services/ouroboros/miners"
	rulesFile         = "rules.dl"
	termPrefix        = "term "
	gitRevList        = "rev-list"
	gitFirst          = "-1"
	gitBefore         = "--before="
	gitLsTree         = "ls-tree"
	gitRecursive      = "-r"
	gitNameOnly       = "--name-only"
	gitShow           = "show"
	gitPathSeparator  = ":"
	gitPathspecEnd    = "--"
	structureMinWeeks = 3
	// exponentDegreesOfFreedom is what an ordinary least-squares slope
	// over n points leaves for its residual variance.
	exponentDegreesOfFreedom = 2
)

// gateExpression is one Gate constant of the session gate.
var gateExpression = regexp.MustCompile(`^\s+Gate[A-Z][A-Za-z]*\s*=`)

// StructureCounts is the enforced structure on main at one commit.
type StructureCounts struct {
	Gates  int `json:"gates"`
	Miners int `json:"miners"`
	Terms  int `json:"terms"`
}

// Total is the structure as one number.
func (counts StructureCounts) Total() int { return counts.Gates + counts.Miners + counts.Terms }

// StructureWeek is one week of the internal check.
type StructureWeek struct {
	Week            string          `json:"week"`
	Commit          string          `json:"commit"`
	Counts          StructureCounts `json:"counts"`
	Gained          int             `json:"gained"`
	Interventions   int64           `json:"interventions"`
	PerIntervention *float64        `json:"per_intervention"`
}

// Exponent is the compounding exponent of the internal check: the slope of
// log(structure per intervention) on the week index, with its 95% interval.
type Exponent struct {
	Slope *float64 `json:"slope"`
	Low   *float64 `json:"low"`
	High  *float64 `json:"high"`
	Weeks int      `json:"weeks"`
	Proxy string   `json:"proxy"`
}

// StructureExponent fits log(y) on the week index by ordinary least squares
// over the weeks with a positive y, and returns the slope with its 95%
// interval; it needs structureMinWeeks such weeks.
func StructureExponent(weeks []StructureWeek) Exponent {
	var xs, ys []float64
	var origin time.Time
	for _, week := range weeks {
		if week.PerIntervention == nil || *week.PerIntervention <= 0 {
			continue
		}
		start, _ := time.Parse(time.DateOnly, week.Week)
		if origin.IsZero() {
			origin = start
		}
		xs = append(xs, math.Round(start.Sub(origin).Hours()/hoursPerDay/daysPerWeek))
		ys = append(ys, math.Log(*week.PerIntervention))
	}
	exponent := Exponent{Weeks: len(xs), Proxy: StructureProxy}
	if len(xs) < structureMinWeeks {
		return exponent
	}
	var xBar, yBar float64
	for index := range xs {
		xBar += xs[index]
		yBar += ys[index]
	}
	xBar, yBar = xBar/float64(len(xs)), yBar/float64(len(ys))
	var sxx, sxy float64
	for index := range xs {
		sxx += (xs[index] - xBar) * (xs[index] - xBar)
		sxy += (xs[index] - xBar) * (ys[index] - yBar)
	}
	if sxx <= 0 {
		return exponent
	}
	slope := sxy / sxx
	intercept := yBar - slope*xBar
	var residuals float64
	for index := range xs {
		residual := ys[index] - intercept - slope*xs[index]
		residuals += residual * residual
	}
	standardError := math.Sqrt(residuals / float64(len(xs)-exponentDegreesOfFreedom) / sxx)
	low, high := slope-z95*standardError, slope+z95*standardError
	exponent.Slope, exponent.Low, exponent.High = &slope, &low, &high
	return exponent
}

// structureWeeks counts the structure main gained in each week, oldest
// first, against the interventions the corpus recorded in that week.
func (loop *Loop) structureWeeks(ctx context.Context, weeks []string, interventions map[string]int64) ([]StructureWeek, error) {
	if len(weeks) == 0 {
		return nil, nil
	}
	first, err := time.ParseInLocation(time.DateOnly, weeks[0], loop.location)
	if err != nil {
		return nil, err
	}
	previous, _, err := loop.structureAt(ctx, first)
	if err != nil {
		return nil, err
	}
	records := make([]StructureWeek, 0, len(weeks))
	for _, week := range weeks {
		start, err := time.ParseInLocation(time.DateOnly, week, loop.location)
		if err != nil {
			return nil, err
		}
		counts, commit, err := loop.structureAt(ctx, start.AddDate(0, 0, daysPerWeek))
		if err != nil {
			return nil, err
		}
		record := StructureWeek{Week: week, Commit: commit, Counts: counts, Gained: counts.Total() - previous.Total(), Interventions: interventions[week]}
		if record.Interventions > 0 {
			perIntervention := float64(record.Gained) / float64(record.Interventions)
			record.PerIntervention = &perIntervention
		}
		records = append(records, record)
		previous = counts
	}
	return records, nil
}

// structureAt counts the structure on main at the last commit before at;
// no commit yet is no structure.
func (loop *Loop) structureAt(ctx context.Context, at time.Time) (StructureCounts, string, error) {
	result, err := loop.git(ctx, gitRevList, gitFirst, gitBefore+at.UTC().Format(time.RFC3339), gitOriginMain)
	if err != nil {
		return StructureCounts{}, "", err
	}
	commit := strings.TrimSpace(string(result.Stdout))
	if commit == "" {
		return StructureCounts{}, "", nil
	}
	var counts StructureCounts
	tree, err := loop.git(ctx, gitLsTree, gitRecursive, gitNameOnly, commit, gitPathspecEnd, minersTree)
	if err != nil {
		return StructureCounts{}, "", err
	}
	for _, path := range strings.Split(string(tree.Stdout), "\n") {
		if strings.HasSuffix(path, "/"+rulesFile) {
			counts.Miners++
		}
	}
	counts.Terms = loop.countLines(ctx, commit, ontologyFile, func(line string) bool { return strings.HasPrefix(line, termPrefix) })
	counts.Gates = loop.countLines(ctx, commit, gateFile, gateExpression.MatchString)
	return counts, commit, nil
}

// countLines counts the lines of a file at a commit that match; a file
// absent at that commit counts none.
func (loop *Loop) countLines(ctx context.Context, commit string, path string, matches func(line string) bool) int {
	result, err := loop.git(ctx, gitShow, commit+gitPathSeparator+path)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		if matches(line) {
			count++
		}
	}
	return count
}

// git runs one git query in the repository; a query git refuses, such as a
// path absent at a commit, is an [*proc.ExitError].
func (loop *Loop) git(ctx context.Context, arguments ...string) (proc.Result, error) {
	result, err := loop.launcher.Run(ctx, proc.Command{Executable: gitExecutable, Arguments: append([]string{gitDirectory, loop.repository.directory}, arguments...)})
	var exit *proc.ExitError
	if errors.As(err, &exit) {
		return result, err
	}
	if err != nil {
		return result, fmt.Errorf("ouroboros: git %s: %w", arguments[0], err)
	}
	return result, nil
}

// weekStarts lists the Monday of every week between the first and the last
// day, oldest first.
func weekStarts(days []string, location *time.Location) ([]string, error) {
	if len(days) == 0 {
		return nil, nil
	}
	sorted := slices.Clone(days)
	slices.Sort(sorted)
	first, err := time.ParseInLocation(time.DateOnly, WeekOf(sorted[0]), location)
	if err != nil {
		return nil, err
	}
	last, err := time.ParseInLocation(time.DateOnly, WeekOf(sorted[len(sorted)-1]), location)
	if err != nil {
		return nil, err
	}
	var weeks []string
	for week := first; !week.After(last); week = week.AddDate(0, 0, daysPerWeek) {
		weeks = append(weeks, week.Format(time.DateOnly))
	}
	return weeks, nil
}
