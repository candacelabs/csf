// Copyright 2026 Candace Labs

package dreamer

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdfs "io/fs"
	"maps"
	"slices"
	"strings"
	"time"

	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
	"github.com/candacelabs/csf/services/ouroboros"
)

const (
	// window is how far back a source counts occurrences: one week, the
	// unit the objective is measured in.
	window = 7 * 24 * time.Hour
	// examplesShown bounds the instances a candidate's evidence quotes.
	examplesShown = 3
	// mergesRead bounds the merge train's records the refusal source reads.
	mergesRead = 500
	// claimMarker is how a session claims a ticket in its body.
	claimMarker = "Claimed by CSF session"
	// languageLabel marks the operator-reviewed language tickets, which the
	// dreamer never takes.
	languageLabel = "lang"
	keySeparator  = "/"
	unitFrequency = 1.0
	unitReason    = "one instance; its weekly effect on the struggle rate is not measured, so it counts once"
	localBar      = "the local bar passes: the build, go test -race on the touched packages, the house lint with 0 mandatory findings and the operator-identifier check"
	oneSlice      = "delivered as one vertical slice with its spec and proof in the pull request body"
)

// rulingLabels mark an issue as an operator ruling or intent.
var rulingLabels = []string{"ruling", "intent"}

// weekKey is the key's week part: a class that persists may be worked on
// again the next week.
func weekKey(now time.Time) string { return ouroboros.WeekOf(now.UTC().Format(time.DateOnly)) }

// minerOutcomes are the mining loop's fixer outcomes by miner: finished
// fixers, and those that reached a ready pull request.
func minerOutcomes(fixers []ouroboros.Fixer) map[string]Outcomes {
	outcomes := map[string]Outcomes{}
	for _, fixer := range fixers {
		if !fixer.Outcome.Finished() {
			continue
		}
		outcome := outcomes[fixer.Miner]
		outcome.Attempted++
		if fixer.Outcome == ouroboros.OutcomeReady {
			outcome.Fixed++
		}
		outcomes[fixer.Miner] = outcome
	}
	return outcomes
}

// StruggleClasses proposes the mining loop's struggle classes: the findings
// of the week on in-sample runs, grouped by miner and rule. Frequency is the
// class's count in the week; the earlier work is the loop's fixers for the
// miner. Held-out runs are never read.
func StruggleClasses(store ouroboros.IStore) Source {
	return Source{Name: string(SourceStruggle), Find: func(ctx context.Context, world World) ([]Candidate, error) {
		since := world.Now.Add(-window)
		findings, err := store.FindingsSince(ctx, since)
		if err != nil {
			return nil, err
		}
		fixers, err := store.FixersStartedSince(ctx, time.Time{})
		if err != nil {
			return nil, err
		}
		outcomes := minerOutcomes(fixers)
		classes := map[string][]ouroboros.Finding{}
		for _, finding := range findings {
			if HeldOut(RunOf(finding.Item)) {
				continue
			}
			class := finding.Miner + keySeparator + finding.Rule
			classes[class] = append(classes[class], finding)
		}
		candidates := make([]Candidate, 0, len(classes))
		for _, class := range slices.Sorted(maps.Keys(classes)) {
			members := classes[class]
			miner, rule := members[0].Miner, members[0].Rule
			outcome := outcomes[miner]
			candidate := Candidate{
				Source: SourceStruggle, Key: string(SourceStruggle) + keySeparator + class + keySeparator + weekKey(world.Now),
				Title:     fmt.Sprintf("STRUGGLE %s: stop the %s class at its source (%d in-sample findings this week)", class, rule, len(members)),
				Frequency: float64(len(members)), FrequencyDerivation: fmt.Sprintf("%d findings of miner %s, rule %s, on in-sample runs since %s", len(members), miner, rule, since.UTC().Format(time.DateOnly)),
				Fixed: outcome.Fixed, Attempted: outcome.Attempted, Known: outcome.Attempted > 0,
				Acceptance: []string{
					fmt.Sprintf("a gate, miner rule or harness change stops the %s class at its source, %s", class, oneSlice),
					"a spec reproduces at least one evidence instance below and passes after the change",
					fmt.Sprintf("the dreamer's next comparison counts fewer than %d in-sample findings of the class in a week", len(members)),
					localBar,
				},
			}
			for _, finding := range members[:min(len(members), examplesShown)] {
				candidate.Evidence = append(candidate.Evidence, fmt.Sprintf("%s %s on %s (%s, run %s)", finding.Severity, finding.Subject, finding.FoundAt.UTC().Format(time.DateOnly), finding.Rule, RunOf(finding.Item)))
			}
			candidate.Evidence = append(candidate.Evidence, fmt.Sprintf("the loop's fixers for miner %s: %d finished, %d reached a ready pull request", miner, outcome.Attempted, outcome.Fixed))
			candidates = append(candidates, candidate)
		}
		return candidates, nil
	}}
}

// Backlog proposes the open issues of CSF's own repository that no slice
// delivers and no session claimed: mining tickets with no ready or running
// fixer are ungated findings, issues labelled ruling or intent are
// unenforced rulings, and the rest are unowned tickets. Language tickets are
// the operator's and are never taken. store, when given, supplies the
// mining tickets' fixers.
func Backlog(list func(ctx context.Context) ([]Issue, error), store ouroboros.IStore) Source {
	return Source{Name: "backlog", Find: func(ctx context.Context, world World) ([]Candidate, error) {
		issues, err := list(ctx)
		if err != nil {
			return nil, err
		}
		var candidates []Candidate
		for _, issue := range issues {
			if strings.Contains(issue.Body, claimMarker) || slices.Contains(issue.Labels, languageLabel) {
				continue
			}
			candidate := Candidate{
				Source: SourceTicket, Key: fmt.Sprintf("ticket/%d", issue.Number), Title: issue.Title, TicketURL: issue.URL,
				Frequency: unitFrequency, FrequencyDerivation: unitReason,
				Evidence:   []string{fmt.Sprintf("open issue #%d, labels [%s], with no slice in the graph and no session's claim", issue.Number, strings.Join(issue.Labels, ", "))},
				Acceptance: []string{"the acceptance the ticket states, " + oneSlice, localBar},
			}
			if slices.ContainsFunc(issue.Labels, func(label string) bool { return slices.Contains(rulingLabels, label) }) {
				candidate.Source = SourceRuling
				candidate.Acceptance = append([]string{"the ruling is enforced by a gate or a typed check, so breaking it fails a run rather than needing an operator correction"}, candidate.Acceptance...)
			}
			if _, mining := ouroboros.MiningTicket(issue.Number, issue.Title, issue.Labels); mining {
				candidate.Source = SourceFinding
				if store != nil {
					fixers, err := store.FixersForTicket(ctx, issue.Number)
					if err != nil {
						return nil, err
					}
					if slices.ContainsFunc(fixers, func(fixer ouroboros.Fixer) bool {
						return fixer.Outcome == ouroboros.OutcomeRunning || fixer.Outcome == ouroboros.OutcomeReady
					}) {
						continue
					}
					outcome := minerOutcomes(fixers)
					for _, counts := range outcome {
						candidate.Fixed += counts.Fixed
						candidate.Attempted += counts.Attempted
					}
					candidate.Known = candidate.Attempted > 0
				}
				candidate.Acceptance = append([]string{"the accepted finding gains a gate that fails on the instance the ticket names"}, candidate.Acceptance...)
			}
			candidates = append(candidates, candidate)
		}
		return candidates, nil
	}}
}

// scoreRecord is the part of an ontology-score record the source reads.
type scoreRecord struct {
	Revision string  `json:"revision"`
	Score    float64 `json:"score"`
	Penalty  int     `json:"penalty"`
	Signals  []struct {
		ID           string `json:"id"`
		Weight       int    `json:"weight"`
		Status       string `json:"status"`
		Count        *int   `json:"count"`
		Contribution int    `json:"contribution"`
		Meaning      string `json:"meaning"`
	} `json:"signals"`
}

const signalMeasured = "measured"

// OntologySignals proposes every measured ontology alignment signal with a
// count above zero in the score record name, as tools/ontology-score.sh
// prints it, read from files. With no record there is nothing to propose.
func OntologySignals(files stdfs.FS, name string) Source {
	return Source{Name: string(SourceOntology), Find: func(_ context.Context, world World) ([]Candidate, error) {
		content, err := stdfs.ReadFile(files, name)
		if errors.Is(err, stdfs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var record scoreRecord
		if err := json.Unmarshal(content, &record); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		var candidates []Candidate
		for _, signal := range record.Signals {
			if signal.Status != signalMeasured || signal.Count == nil || *signal.Count == 0 {
				continue
			}
			candidates = append(candidates, Candidate{
				Source: SourceOntology, Key: string(SourceOntology) + keySeparator + signal.ID + keySeparator + weekKey(world.Now),
				Title:     fmt.Sprintf("ONTOLOGY %s: bring the tree back to the ontology (%d found)", signal.ID, *signal.Count),
				Frequency: unitFrequency, FrequencyDerivation: unitReason,
				Evidence: []string{
					fmt.Sprintf("%s: count %d × weight %d = %d of the penalty: %s", signal.ID, *signal.Count, signal.Weight, signal.Contribution, signal.Meaning),
					fmt.Sprintf("ontology-score record of revision %s: score %.4f, penalty %d", record.Revision, record.Score, record.Penalty),
				},
				Acceptance: []string{
					fmt.Sprintf("the next ontology-score record counts fewer than %d for %s, with no code kept or dropped for the score", *signal.Count, signal.ID),
					localBar,
				},
			})
		}
		return candidates, nil
	}}
}

// group is one class of a harness gap: its count, its examples and its
// measured amounts.
type group struct {
	count    int
	examples []string
	amount   float64
	ready    int
}

func (entry *group) note(example string) {
	entry.count++
	if len(entry.examples) < examplesShown {
		entry.examples = append(entry.examples, example)
	}
}

// firstLine is a reason's first line, the part refusals are grouped by.
func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

// HarnessGaps is the fallback: the harness's own gaps measured from its
// records over the week. Refusals are the merge train's refused merges and
// the dispatcher's failed slices, grouped by their reason's first line; cost
// is the loop's fixer spend per miner that reached no ready pull request,
// highest first; the slowest are the miners whose fixers took longest on
// average.
func HarnessGaps(store ouroboros.IStore) Source {
	return Source{Name: "harness_gaps", Fallback: true, Find: func(ctx context.Context, world World) ([]Candidate, error) {
		since := world.Now.Add(-window)
		merges, err := store.RecentMerges(ctx, mergesRead)
		if err != nil {
			return nil, err
		}
		fixers, err := store.FixersStartedSince(ctx, since)
		if err != nil {
			return nil, err
		}
		refusals := map[string]*group{}
		refusal := func(reason string) *group {
			if refusals[reason] == nil {
				refusals[reason] = &group{}
			}
			return refusals[reason]
		}
		for _, merge := range merges {
			if !merge.Merged && !merge.RecordedAt.Before(since) {
				refusal(firstLine(merge.Reason)).note(fmt.Sprintf("merge of pull request #%d at %s refused (exit %d)", merge.PullRequest, merge.HeadSHA, merge.ExitCode))
			}
		}
		for _, node := range world.Slices {
			if node.GetState() == dispatchv1.SliceState_SLICE_STATE_FAILED && !node.GetUpdatedAt().AsTime().Before(since) {
				refusal(firstLine(node.GetError())).note(fmt.Sprintf("slice %s failed after %d attempts", node.GetSlice().GetSliceId(), node.GetAttempts()))
			}
		}
		spend, slow := map[string]*group{}, map[string]*group{}
		for _, fixer := range fixers {
			if !fixer.Outcome.Finished() {
				continue
			}
			for _, measured := range []map[string]*group{spend, slow} {
				if measured[fixer.Miner] == nil {
					measured[fixer.Miner] = &group{}
				}
			}
			spend[fixer.Miner].amount += float64(fixer.CostUSDMicros) / 1e6
			spend[fixer.Miner].note(fmt.Sprintf("fixer %s on ticket #%d: %s, $%.2f", fixer.AssignmentID, fixer.Ticket, fixer.Outcome, float64(fixer.CostUSDMicros)/1e6))
			slow[fixer.Miner].amount += float64(fixer.Seconds)
			slow[fixer.Miner].note(fmt.Sprintf("fixer %s on ticket #%d took %s", fixer.AssignmentID, fixer.Ticket, time.Duration(fixer.Seconds)*time.Second))
			if fixer.Outcome == ouroboros.OutcomeReady {
				spend[fixer.Miner].ready++
			}
		}
		week := weekKey(world.Now)
		var candidates []Candidate
		for _, reason := range slices.Sorted(maps.Keys(refusals)) {
			entry := refusals[reason]
			candidates = append(candidates, Candidate{
				Source: SourceRefusal, Key: string(SourceRefusal) + keySeparator + reason + keySeparator + week,
				Title:     fmt.Sprintf("REFUSAL: %d refusals this week for %q", entry.count, reason),
				Frequency: float64(entry.count), FrequencyDerivation: fmt.Sprintf("%d merge refusals and failed slices since %s with this reason", entry.count, since.UTC().Format(time.DateOnly)),
				Evidence: entry.examples,
				Acceptance: []string{"the cause of the refusal is removed or turned into a check that fails before a session spends on it, " + oneSlice,
					"a spec reproduces one evidence instance", localBar},
			})
		}
		costly := worst(spend, func(entry *group) float64 { return entry.amount / float64(entry.ready+1) })
		if costly != "" {
			entry := spend[costly]
			candidates = append(candidates, Candidate{
				Source: SourceCost, Key: string(SourceCost) + keySeparator + costly + keySeparator + week,
				Title:     fmt.Sprintf("COST %s: $%.2f of fixer spend this week for %d ready pull requests", costly, entry.amount, entry.ready),
				Frequency: float64(entry.count), FrequencyDerivation: fmt.Sprintf("%d finished fixer sessions of miner %s since %s", entry.count, costly, since.UTC().Format(time.DateOnly)),
				Evidence: entry.examples,
				Acceptance: []string{fmt.Sprintf("the spend per ready pull request of miner %s falls below $%.2f the week after the merge", costly, entry.amount/float64(entry.ready+1)),
					localBar},
			})
		}
		slowest := worst(slow, func(entry *group) float64 { return entry.amount / float64(entry.count) })
		if slowest != "" {
			entry := slow[slowest]
			mean := time.Duration(entry.amount/float64(entry.count)) * time.Second
			candidates = append(candidates, Candidate{
				Source: SourceSlowest, Key: string(SourceSlowest) + keySeparator + slowest + keySeparator + week,
				Title:     fmt.Sprintf("SLOWEST %s: fixer sessions average %s", slowest, mean),
				Frequency: float64(entry.count), FrequencyDerivation: fmt.Sprintf("%d finished fixer sessions of miner %s since %s", entry.count, slowest, since.UTC().Format(time.DateOnly)),
				Evidence:   entry.examples,
				Acceptance: []string{fmt.Sprintf("the mean fixer session of miner %s takes less than %s the week after the merge", slowest, mean), localBar},
			})
		}
		return candidates, nil
	}}
}

// worst is the key whose measure is highest, the first in name order among
// equals; empty for none.
func worst(groups map[string]*group, measure func(entry *group) float64) string {
	best := ""
	for _, key := range slices.Sorted(maps.Keys(groups)) {
		if best == "" || cmp.Compare(measure(groups[key]), measure(groups[best])) > 0 {
			best = key
		}
	}
	return best
}
