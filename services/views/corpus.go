// Copyright 2026 Candace Labs

package views

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	stdfs "io/fs"
	"log/slog"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/sessiongate"
	"github.com/candacelabs/csf/services/ouroboros"
)

// TokenKind is one kind of token a turn's usage reports.
type TokenKind string

// The token kinds, as csf_tokens_total labels them.
const (
	TokensUncached   TokenKind = "uncached"
	TokensCacheWrite TokenKind = "cache_write"
	TokensCacheRead  TokenKind = "cache_read"
	TokensOutput     TokenKind = "output"
)

// The outcomes of a control action, as csf_merge_checks_total labels them.
const (
	OutcomeOK     = "ok"
	OutcomeFailed = "failed"
)

const (
	// maxRecordBytes bounds one event log line, as the other folds over the
	// corpus do.
	maxRecordBytes = 8 << 20
	rootDirectory  = "."
)

// tokenKinds is every kind, in the order a scrape reports them.
var tokenKinds = []TokenKind{TokensUncached, TokensCacheWrite, TokensCacheRead, TokensOutput}

// failedLevel is how the event log marks a record of a failure.
var failedLevel = slog.LevelError.String()

// GateKey is one gate's decision.
type GateKey struct {
	Gate     string
	Decision string
}

// CheckKey is one control action's outcome.
type CheckKey struct {
	Action  string
	Outcome string
}

// Tally is what a run's records added: in one hour, or in all of them.
type Tally struct {
	Turns    int64
	Tokens   map[TokenKind]int64
	CostUSD  float64
	Gates    map[GateKey]int64
	Refusals map[string]int64
	Checks   map[CheckKey]int64
}

// Series reports every sample a tally adds to the per-run families, one call
// to emit per series, so a scrape and a backfill walk the same series.
func (tally *Tally) Series(key RunKey, emit func(name string, value float64, labels ...string)) {
	emit(MetricTurns, float64(tally.Turns), key.values()...)
	emit(MetricCost, tally.CostUSD, key.values()...)
	for _, kind := range tokenKinds {
		emit(MetricTokens, float64(tally.Tokens[kind]), key.values(string(kind))...)
	}
	for gate, count := range tally.Gates {
		emit(MetricGateDecisions, float64(count), key.values(gate.Gate, gate.Decision)...)
	}
	for rule, count := range tally.Refusals {
		emit(MetricReplyRefusals, float64(count), key.values(rule)...)
	}
	for check, count := range tally.Checks {
		emit(MetricMergeChecks, float64(count), key.values(check.Action, check.Outcome)...)
	}
}

func newTally() *Tally {
	return &Tally{Tokens: map[TokenKind]int64{}, Gates: map[GateKey]int64{}, Refusals: map[string]int64{}, Checks: map[CheckKey]int64{}}
}

// add adds other into tally.
func (tally *Tally) add(other *Tally) {
	tally.Turns += other.Turns
	tally.CostUSD += other.CostUSD
	for kind, count := range other.Tokens {
		tally.Tokens[kind] += count
	}
	for key, count := range other.Gates {
		tally.Gates[key] += count
	}
	for rule, count := range other.Refusals {
		tally.Refusals[rule] += count
	}
	for key, count := range other.Checks {
		tally.Checks[key] += count
	}
}

// Run is one run directory folded: who ran it, what its records added by the
// hour they fell in, and its struggle reading. A published Run is never
// written again.
type Run struct {
	Assignment string
	Agent      string
	Executor   string
	Model      string
	// Hours maps the start of each UTC hour to what the run added in it.
	Hours   map[time.Time]*Tally
	Total   *Tally
	Reading ouroboros.Reading
	// OperatorHours are the UTC hours holding an operator-authored message
	// sent into the run.
	OperatorHours map[time.Time]bool
}

// Measurement is the corpus folded at one instant: every run and the
// struggle rate by day. It is published whole and never written again.
type Measurement struct {
	At   time.Time
	Runs []Run
	Days []ouroboros.Rate
	// OperatorHours are the distinct UTC hours holding an operator-authored
	// message in any run, oldest first: an hour two runs share counts once.
	OperatorHours []time.Time
}

// record is the part of one event log line the fold reads.
type record struct {
	Time      time.Time `json:"time"`
	EventType string    `json:"event_type"`
	Level     string    `json:"level"`
	Provider  string    `json:"provider"`
	Gate      string    `json:"gate"`
	Decision  string    `json:"decision"`
	Rules     []string  `json:"rules"`
	Action    string    `json:"action"`
	Operator  bool      `json:"operator_authored"`
	Event     struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
		Usage        *struct {
			Input      int64 `json:"input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			Output     int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"event"`
}

// FoldRun reads one run's event log. Usage is per turn and summed;
// total_cost_usd is cumulative within one executor process, so each result
// adds its rise over the previous one, or its whole value once it fell,
// which is a reopened process (ouroboros.FoldUsage's reading).
func FoldRun(assignment string, state session.RunState, lines []byte) Run {
	run := Run{Assignment: assignment, Agent: state.AgentID, Model: state.Model, Hours: map[time.Time]*Tally{}, Total: newTally(),
		OperatorHours: map[time.Time]bool{},
		Reading:       ouroboros.Struggles(lines, time.UTC)}
	var previousCost float64
	scanner := bufio.NewScanner(bytes.NewReader(lines))
	scanner.Buffer(make([]byte, 0, 64<<10), maxRecordBytes)
	for scanner.Scan() {
		var line record
		if json.Unmarshal(scanner.Bytes(), &line) != nil || line.Time.IsZero() {
			continue
		}
		if run.Executor == "" && line.Provider != "" {
			run.Executor = line.Provider
		}
		hour := line.Time.UTC().Truncate(time.Hour)
		if line.EventType == session.EventTypeControlAction && line.Action == session.ActionSend && line.Operator {
			run.OperatorHours[hour] = true
		}
		tally, found := run.Hours[hour]
		if !found {
			tally = newTally()
		}
		if !foldRecord(tally, line, &previousCost) {
			continue
		}
		run.Hours[hour] = tally
	}
	for _, tally := range run.Hours {
		run.Total.add(tally)
	}
	return run
}

// foldRecord adds one record to tally and reports whether it counted.
func foldRecord(tally *Tally, line record, previousCost *float64) bool {
	switch line.EventType {
	case session.EventTypeResult:
		if line.Event.Usage == nil {
			return false
		}
		usage := line.Event.Usage
		tally.Turns++
		tally.Tokens[TokensUncached] += usage.Input
		tally.Tokens[TokensCacheWrite] += usage.CacheWrite
		tally.Tokens[TokensCacheRead] += usage.CacheRead
		tally.Tokens[TokensOutput] += usage.Output
		cost := line.Event.TotalCostUSD
		if cost >= *previousCost {
			tally.CostUSD += cost - *previousCost
		} else {
			tally.CostUSD += cost
		}
		*previousCost = cost
	case session.EventTypeGateDecision:
		tally.Gates[GateKey{Gate: line.Gate, Decision: line.Decision}]++
		if line.Gate == sessiongate.GateReply && line.Decision == sessiongate.DecisionDeny {
			for _, rule := range line.Rules {
				tally.Refusals[rule]++
			}
		}
	case session.EventTypeControlAction:
		if line.Action != session.ActionReady && line.Action != session.ActionMerge {
			return false
		}
		outcome := OutcomeOK
		if line.Level == failedLevel {
			outcome = OutcomeFailed
		}
		tally.Checks[CheckKey{Action: line.Action, Outcome: outcome}]++
	default:
		return false
	}
	return true
}

// cachedRun is a folded run with the size and time of the event log it was
// folded from.
type cachedRun struct {
	size     int64
	modified time.Time
	run      Run
}

// corpus folds the state directory's runs, refolding only a run whose event
// log changed since the last fold. One goroutine owns it.
type corpus struct {
	files  iofs.IFiles
	cached map[string]cachedRun
}

func newCorpus(files iofs.IFiles) *corpus {
	return &corpus{files: files, cached: map[string]cachedRun{}}
}

// measure folds every run directory, reusing each unchanged one, and returns
// the corpus measured at now.
func (folding *corpus) measure(ctx context.Context, now time.Time) (*Measurement, error) {
	entries, err := folding.files.ReadDir(rootDirectory)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		folded, found, err := folding.fold(entry.Name())
		if err != nil {
			return nil, err
		}
		if found {
			seen[entry.Name()] = true
			folding.cached[entry.Name()] = folded
		}
	}
	measurement := &Measurement{At: now, Runs: make([]Run, 0, len(seen))}
	readings := make([]ouroboros.Reading, 0, len(seen))
	for name, folded := range folding.cached {
		if !seen[name] {
			delete(folding.cached, name)
			continue
		}
		measurement.Runs = append(measurement.Runs, folded.run)
		readings = append(readings, folded.run.Reading)
	}
	slices.SortFunc(measurement.Runs, func(left Run, right Run) int { return strings.Compare(left.Assignment, right.Assignment) })
	measurement.Days, _ = ouroboros.DailyRates(readings)
	hours := map[time.Time]bool{}
	for _, run := range measurement.Runs {
		maps.Copy(hours, run.OperatorHours)
	}
	measurement.OperatorHours = slices.SortedFunc(maps.Keys(hours), func(left time.Time, right time.Time) int { return left.Compare(right) })
	return measurement, nil
}

// fold is the run in directory, refolded only when its event log changed; a
// directory with no event log is no run.
func (folding *corpus) fold(directory string) (cachedRun, bool, error) {
	events := path.Join(directory, session.EventsFile)
	info, err := stdfs.Stat(folding.files, events)
	if errors.Is(err, stdfs.ErrNotExist) {
		return cachedRun{}, false, nil
	}
	if err != nil {
		return cachedRun{}, false, err
	}
	if previous, found := folding.cached[directory]; found && previous.size == info.Size() && previous.modified.Equal(info.ModTime()) {
		return previous, true, nil
	}
	lines, err := folding.files.ReadFile(events)
	if err != nil {
		return cachedRun{}, false, err
	}
	var state session.RunState
	if content, err := folding.files.ReadFile(path.Join(directory, session.RunStateFile)); err == nil {
		// A run record that does not decode leaves the run unlabeled rather
		// than unmeasured.
		_ = json.Unmarshal(content, &state)
	}
	return cachedRun{size: info.Size(), modified: info.ModTime(), run: FoldRun(directory, state, lines)}, true, nil
}
