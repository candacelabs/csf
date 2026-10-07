// Copyright 2026 Candace Labs

package costs

import (
	"bytes"
	"cmp"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// The measured inputs' names, units and the agent group every agent pools
// into.
const (
	InputGap           = "gap between turns"
	InputRebuildWrite  = "first-call cache write"
	InputTTFT          = "time to first token"
	InputHitByGap      = "first-call hit ratio by gap"
	InputResident      = "turn executor resident memory"
	InputPrice         = "price"
	InputSuccess       = "runs that opened a pull request"
	InputReopenPremium = "reopen premium"

	unitSeconds      = "s"
	unitTokens       = "tokens"
	unitMilliseconds = "ms"
	unitRatio        = "ratio"
	unitMegabytes    = "MB"
	unitPerMillion   = "$/Mtok"
	unitUSD          = "$"

	groupAll = "all"
	// gapAgents is how many agents, most gaps first, the gap table names
	// beside the pooled row.
	gapAgents = 5
	// shortGap is the shortest band of the hit-ratio table: Claude Code's
	// five-minute cache lifetime.
	shortGap = cacheLifetimeFiveMinutes
)

// TurnClass is what a turn's first call met.
type TurnClass int

const (
	// ClassBind: the first turn on a new real session.
	ClassBind TurnClass = iota
	// ClassWarm: within the cache lifetime of the conversation's last turn,
	// with its executor kept open.
	ClassWarm
	// ClassReopen: within the lifetime, after its executor closed.
	ClassReopen
	// ClassExpired: past the cache lifetime.
	ClassExpired
)

var classNames = map[TurnClass]string{ClassBind: "bind", ClassWarm: "warm", ClassReopen: "reopen within lifetime", ClassExpired: "after expiry"}

// Classified is one turn with its class and the gap before it.
type Classified struct {
	Turn  Turn
	Class TurnClass
	Gap   time.Duration
}

// Classify gives every turn with a model its class.
func Classify(runs []Run, lifetime time.Duration) []Classified {
	turns := []Classified{}
	for _, run := range runs {
		ends := map[string]time.Time{}
		for _, turn := range run.Turns {
			if turn.Model == "" {
				continue
			}
			end, seen := ends[turn.Real]
			classified := Classified{Turn: turn, Class: ClassBind}
			if seen {
				classified.Gap = max(0, turn.Start.Sub(end))
				switch {
				case classified.Gap > lifetime:
					classified.Class = ClassExpired
				case turn.Reopened:
					classified.Class = ClassReopen
				default:
					classified.Class = ClassWarm
				}
			}
			turns = append(turns, classified)
			ends[turn.Real] = turn.End
		}
	}
	return turns
}

// meanInput is the mean of values with its bootstrap interval.
func meanInput(name string, group string, unit string, values []float64, exact bool, method string) *harnessv1.MeasuredInput {
	return &harnessv1.MeasuredInput{Name: name, Group: group, Unit: unit, N: int64(len(values)), Value: mean(values, all(len(values))),
		Interval: bootstrap(len(values), func(indices []int) float64 { return mean(values, indices) }), Exact: exact, Method: method}
}

// gapInputs is the median gap between turns, pooled and for the agents with
// the most gaps.
func gapInputs(gaps []Gap) []*harnessv1.MeasuredInput {
	byAgent := map[string][]float64{}
	pooled := []float64{}
	for _, gap := range gaps {
		if gap.Returned {
			byAgent[gap.Agent] = append(byAgent[gap.Agent], gap.Length.Seconds())
			pooled = append(pooled, gap.Length.Seconds())
		}
	}
	agents := slices.SortedFunc(maps.Keys(byAgent), func(left string, right string) int {
		if difference := len(byAgent[right]) - len(byAgent[left]); difference != 0 {
			return difference
		}
		return strings.Compare(left, right)
	})
	medianOf := func(group string, values []float64) *harnessv1.MeasuredInput {
		return &harnessv1.MeasuredInput{Name: InputGap, Group: group, Unit: unitSeconds, N: int64(len(values)), Value: median(values, all(len(values))),
			Interval: bootstrap(len(values), func(indices []int) float64 { return median(values, indices) }), Exact: true,
			Method: "median seconds from a turn's result to the conversation's next turn request"}
	}
	inputs := []*harnessv1.MeasuredInput{medianOf(groupAll, pooled)}
	for _, agent := range agents[:min(gapAgents, len(agents))] {
		inputs = append(inputs, medianOf(agent, byAgent[agent]))
	}
	return inputs
}

// rebuildInputs is, per turn class, the first call's cache write and the
// turn's time to first token: what a cold start costs against a warm turn.
func rebuildInputs(turns []Classified) []*harnessv1.MeasuredInput {
	writes, ttfts := map[TurnClass][]float64{}, map[TurnClass][]float64{}
	for _, turn := range turns {
		writes[turn.Class] = append(writes[turn.Class], float64(turn.Turn.First.Write))
		if turn.Turn.TTFTKnown {
			ttfts[turn.Class] = append(ttfts[turn.Class], float64(turn.Turn.TTFT.Milliseconds()))
		}
	}
	inputs := []*harnessv1.MeasuredInput{}
	for _, class := range []TurnClass{ClassBind, ClassWarm, ClassReopen, ClassExpired} {
		inputs = append(inputs,
			meanInput(InputRebuildWrite, classNames[class], unitTokens, writes[class], true, "mean cache write of the turn's first top-level call"),
			meanInput(InputTTFT, classNames[class], unitMilliseconds, ttfts[class], true, "mean time to first token the turn's result reports"))
	}
	return inputs
}

// hitInputs is the token-weighted hit ratio of first calls by the gap
// before them: where the cache's lifetime shows.
func hitInputs(turns []Classified, lifetime time.Duration) []*harnessv1.MeasuredInput {
	bands := []struct {
		name     string
		contains func(gap time.Duration) bool
	}{
		{"under " + shortGap.String(), func(gap time.Duration) bool { return gap < shortGap }},
		{shortGap.String() + " to " + lifetime.String(), func(gap time.Duration) bool { return gap >= shortGap && gap <= lifetime }},
		{"over " + lifetime.String(), func(gap time.Duration) bool { return gap > lifetime }},
	}
	inputs := []*harnessv1.MeasuredInput{}
	for _, band := range bands {
		firsts := []Tokens{}
		for _, turn := range turns {
			if turn.Class != ClassBind && turn.Class != ClassReopen && band.contains(turn.Gap) {
				firsts = append(firsts, turn.Turn.First)
			}
		}
		ratio := func(indices []int) float64 {
			var sum Tokens
			for _, index := range indices {
				sum = sum.add(firsts[index])
			}
			return sum.HitRatio()
		}
		inputs = append(inputs, &harnessv1.MeasuredInput{Name: InputHitByGap, Group: band.name, Unit: unitRatio, N: int64(len(firsts)),
			Value: ratio(all(len(firsts))), Interval: bootstrap(len(firsts), ratio), Exact: true,
			Method: "cache read over all input of the first call after the gap, summed over turns"})
	}
	return inputs
}

// residentInput is the turn executors' resident memory as sampled.
func residentInput(samples []int64) *harnessv1.MeasuredInput {
	values := make([]float64, len(samples))
	for index, sample := range samples {
		values[index] = float64(sample) / 1e6
	}
	return meanInput(InputResident, ExecutorCommand, unitMegabytes, values, true, "mean VmRSS of every running turn executor process when the model was built")
}

// kindNames name the token kinds, in the order of Price's fields.
var kindNames = [kinds]string{"uncached input", "cache write", "cache read", "output"}

// priceInputs is every fitted price with its interval.
func priceInputs(fits []PriceFit) []*harnessv1.MeasuredInput {
	inputs := []*harnessv1.MeasuredInput{}
	for _, fit := range fits {
		for kind := range kinds {
			if !fit.Determined[kind] {
				continue
			}
			value := [kinds]float64{fit.Price.Uncached, fit.Price.Write, fit.Price.Read, fit.Price.Output}[kind]
			inputs = append(inputs, &harnessv1.MeasuredInput{Name: InputPrice + " " + kindNames[kind], Group: fit.Model, Unit: unitPerMillion,
				N: int64(fit.N), Value: value * 1e6, Interval: &harnessv1.Interval{Low: fit.Low[kind] * 1e6, High: fit.High[kind] * 1e6},
				Method: fmt.Sprintf("least squares on the money each turn reports per model; residual %.4f $/turn", fit.Residual)})
		}
	}
	return inputs
}

// successInputs is, per model and task kind, the share of runs that opened
// a pull request.
func successInputs(runs []Run) []*harnessv1.MeasuredInput {
	type key struct{ model, kind string }
	counts := map[key][2]int{}
	for _, run := range runs {
		if run.Model == "" || len(run.Turns) == 0 {
			continue
		}
		count := counts[key{run.Model, run.Kind}]
		count[1]++
		if run.PullRequest {
			count[0]++
		}
		counts[key{run.Model, run.Kind}] = count
	}
	keys := slices.SortedFunc(maps.Keys(counts), func(left key, right key) int {
		return cmp.Or(strings.Compare(left.model, right.model), strings.Compare(left.kind, right.kind))
	})
	inputs := []*harnessv1.MeasuredInput{}
	for _, key := range keys {
		count := counts[key]
		inputs = append(inputs, &harnessv1.MeasuredInput{Name: InputSuccess, Group: key.model + " " + key.kind, Unit: unitRatio, N: int64(count[1]),
			Value: float64(count[0]) / float64(count[1]), Interval: wilson(count[0], count[1]), Exact: true,
			Method: "runs whose finish recorded a pull request, over runs with a turn; Wilson interval"})
	}
	return inputs
}

// ExecutorCommand is the process name of a Claude Code turn executor.
const ExecutorCommand = "claude"

// SampleResident reads the resident memory of every process named command
// from a proc filesystem. A process that exits while it is read is skipped.
func SampleResident(proc fs.FS, command string) []int64 {
	entries, err := fs.ReadDir(proc, ".")
	if err != nil {
		return nil
	}
	samples := []int64{}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		name, err := fs.ReadFile(proc, entry.Name()+procCommand)
		if err != nil || string(bytes.TrimSpace(name)) != command {
			continue
		}
		status, err := fs.ReadFile(proc, entry.Name()+procStatus)
		if err != nil {
			continue
		}
		if kilobytes, ok := statusField(status, residentField); ok {
			samples = append(samples, kilobytes*1024)
		}
	}
	return samples
}

// The proc files a process's name and status are read from, and the status
// line that holds resident memory in kB.
const (
	procCommand   = "/comm"
	procStatus    = "/status"
	residentField = "VmRSS:"
)

// statusField is the number on the status line that starts with field.
func statusField(status []byte, field string) (int64, bool) {
	for line := range bytes.SplitSeq(status, []byte("\n")) {
		if rest, found := bytes.CutPrefix(line, []byte(field)); found {
			fields := bytes.Fields(rest)
			if len(fields) == 0 {
				return 0, false
			}
			value, err := strconv.ParseInt(string(fields[0]), 10, 64)
			return value, err == nil
		}
	}
	return 0, false
}
