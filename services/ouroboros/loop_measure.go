// Copyright 2026 Candace Labs

package ouroboros

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	stdfs "io/fs"
	"math"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/candacelabs/csf/services/harness/session"
)

// The series the measure records, and the constants of the measurement.
// The primary series is candace-server #330's: struggle episodes per 1,000
// agent tool calls, reported weekly with the 95% Garwood Poisson interval,
// and the weekly growth factor from a weighted log-linear fit over the
// active weeks. The struggle definition is the deterministic part of
// #330's (rrsi-mine traces, the nine signals merged into episodes): the
// three signals a harness event log carries without a model are kept, with
// the same episode gap, and every episode counts, since no labeler marks
// noise or harness-fixability here. Days are calendar days in
// ScheduleLocation; weeks start on Monday. The per-day fit is kept as the
// early read while the corpus holds fewer than three active weeks.
const (
	SeriesStruggleRate       = "ouroboros.struggle_rate_per_1k_tool_calls"
	SeriesStruggleRateWeekly = "ouroboros.struggle_rate_per_1k_tool_calls_weekly"
	SeriesCompounding        = "ouroboros.compounding_per_week"
	SeriesCompoundingDaily   = "ouroboros.compounding_per_day"
	SeriesFindings           = "ouroboros.findings"
	SeriesFixerSpend         = "ouroboros.fixer_spend_usd"
	SeriesCostPerReadyPull   = "ouroboros.cost_per_ready_pull_request_usd"
	SeriesFixerYield         = "ouroboros.fixer_yield"

	// The baseline to beat (#330, 2026-10-02 comment): the weekly growth
	// factor of the struggle rate with its interval, and the rate in the
	// week of 2026-09-28. Exponential improvement is this factor below 1,
	// and staying there.
	BaselineWeeklyFactor = 1.156
	BaselineWeeklyLow    = 1.135
	BaselineWeeklyHigh   = 1.177
	BaselineRatePerK     = 49.9
	BaselineWeek         = "2026-09-28"

	// SignalToolError, SignalPermissionDenial and SignalRetry name the three
	// struggle signals read from a harness event log, as rrsi-mine names them.
	SignalToolError        = "tool_error"
	SignalPermissionDenial = "permission_denial"
	SignalRetry            = "retry"

	// episodeGap: hits at most this many events apart belong to one episode
	// (rrsi-mine EPISODE_GAP).
	episodeGap = 6
	// retryLookback: a tool call repeating one of the last this many calls
	// is a retry (rrsi-mine RETRY_LOOKBACK, with exact equality in place of
	// its 0.85 similarity, so the measure stays deterministic).
	retryLookback = 4
	// activeCalls: a week or day with fewer tool calls does not enter the
	// trend fit (measure.py ACTIVE_CALLS).
	activeCalls = 200
	// trendMinimum: the fit needs this many active periods with a struggle
	// (measure.py TREND_MIN_WEEKS).
	trendMinimum = 3
	z95          = 1.959964
	perThousand  = 1000
	daysPerWeek  = 7
	hoursPerDay  = 24
	// firstTurn is the turn whose prompt is the brief; every later prompt
	// is an operator-authored message.
	firstTurn = 1

	eventTypeAssistant = "assistant"
	eventTypeUser      = "user"
	eventTypeSystem    = "system"
	eventTypeGate      = "session_gate_decision"
	subtypeDenied      = "permission_denied"
	decisionDeny       = "deny"
	blockToolUse       = "tool_use"
	blockToolResult    = "tool_result"
	directionIn        = "in"
)

// Period is the unit a compounding fit indexes time by.
type Period int

// The periods the fit runs over.
const (
	PerDay Period = iota
	PerWeek
)

// Rate is one period's struggle rate: episodes per 1,000 tool calls with
// its interval, or no rate when the period had no tool call. Day is the
// calendar day, or the Monday of the week.
type Rate struct {
	Day       string   `json:"day"`
	ToolCalls int64    `json:"tool_calls"`
	Struggles int64    `json:"struggles"`
	PerK      *float64 `json:"per_1k"`
	Low       *float64 `json:"low"`
	High      *float64 `json:"high"`
}

// Trend is a compounding rate: the multiplicative change of the struggle
// rate per period with its 95% interval, over the periods that entered the
// fit.
type Trend struct {
	Factor *float64 `json:"factor"`
	Low    *float64 `json:"low"`
	High   *float64 `json:"high"`
	Weeks  int      `json:"periods"`
}

// Baseline is #330's baseline, carried in every snapshot so the view shows
// what the factor is measured against.
type Baseline struct {
	Factor   float64 `json:"factor"`
	Low      float64 `json:"low"`
	High     float64 `json:"high"`
	RatePerK float64 `json:"rate_per_1k"`
	Week     string  `json:"week"`
}

// FixerSummary is the loop's own yield, from the ledger.
type FixerSummary struct {
	Sessions      int      `json:"sessions"`
	Running       int      `json:"running"`
	Finished      int      `json:"finished"`
	Ready         int      `json:"ready"`
	SpendUSD      float64  `json:"spend_usd"`
	SpendTodayUSD float64  `json:"spend_today_usd"`
	BudgetUSD     float64  `json:"budget_usd"`
	CostPerReady  *float64 `json:"cost_per_ready_pull_request_usd"`
	Yield         *float64 `json:"yield"`
	Launch        bool     `json:"launch_enabled"`
}

// MergeSummary is the merge train's record.
type MergeSummary struct {
	Merged  int `json:"merged"`
	Refused int `json:"refused"`
}

// Snapshot is the loop's numbers at one instant: what the ops view shows
// and the API serves.
type Snapshot struct {
	ComputedAt        time.Time       `json:"computed_at"`
	Day               string          `json:"day"`
	Week              Rate            `json:"week"`
	Weeks             []Rate          `json:"weeks"`
	Compounding       Trend           `json:"compounding"`
	Baseline          Baseline        `json:"baseline"`
	Struggle          Rate            `json:"struggle_rate"`
	Days              []Rate          `json:"days"`
	CompoundingDaily  Trend           `json:"compounding_daily"`
	Structure         []StructureWeek `json:"structure"`
	StructureExponent Exponent        `json:"structure_exponent"`
	Findings          int             `json:"findings"`
	FindingsToday     int             `json:"findings_today"`
	FindingsPerDay    *float64        `json:"findings_per_day"`
	Fixers            FixerSummary    `json:"fixers"`
	Merges            MergeSummary    `json:"merges"`
	Queue             int             `json:"needs_labels"`
	Miners            []MinerStatus   `json:"miners"`
	GenericMiners     int             `json:"generic_miners"`
	TenantMiners      int             `json:"tenant_miners"`
}

// Reading is what one event log contributes to the measure, each count by
// the calendar day it fell on.
type Reading struct {
	Calls         map[string]int64
	Episodes      map[string]int64
	Interventions map[string]int64
}

// logRecord is the part of one event log line the measure reads.
type logRecord struct {
	Time      time.Time       `json:"time"`
	EventType string          `json:"event_type"`
	Direction string          `json:"direction"`
	Turn      int             `json:"turn"`
	Decision  string          `json:"decision"`
	Event     json.RawMessage `json:"event"`
}

type logEvent struct {
	Subtype string `json:"subtype"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type logBlock struct {
	Type    string          `json:"type"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	IsError bool            `json:"is_error"`
}

// sessionEvent is one normalized event of a session: a tool call, its
// result or a denial, in log order.
type sessionEvent struct {
	at   time.Time
	kind string
	call string
	hit  string
}

// WeekOf is the Monday of the week holding a calendar day.
func WeekOf(day string) string {
	date, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return day
	}
	back := (int(date.Weekday()) + daysPerWeek - 1) % daysPerWeek
	return date.AddDate(0, 0, -back).Format(time.DateOnly)
}

// Struggles reads one event log into its tool calls, struggle episodes and
// operator interventions, each dated in location.
func Struggles(lines []byte, location *time.Location) Reading {
	return StrugglesWithin(lines, location, 0)
}

// StrugglesWithin is Struggles over the log's first budget tool calls, and
// everything up to the next one: a fixed budget, the unit the evaluation
// suite reads a replay at. A budget of 0 reads the whole log.
func StrugglesWithin(lines []byte, location *time.Location, budget int) Reading {
	reading := Reading{Calls: map[string]int64{}, Episodes: map[string]int64{}, Interventions: map[string]int64{}}
	var events []sessionEvent
	scanner := bufio.NewScanner(bytes.NewReader(lines))
	scanner.Buffer(make([]byte, 0, 64<<10), maxEventLine)
	for scanner.Scan() {
		var record logRecord
		if json.Unmarshal(scanner.Bytes(), &record) != nil || record.Time.IsZero() {
			continue
		}
		if record.EventType == eventTypeUser && record.Direction == directionIn && record.Turn > firstTurn {
			reading.Interventions[record.Time.In(location).Format(time.DateOnly)]++
		}
		events = append(events, normalize(record)...)
	}
	events = withinBudget(events, budget)
	for index := range events {
		if events[index].kind == blockToolUse {
			reading.Calls[events[index].at.In(location).Format(time.DateOnly)]++
			for back := 1; back <= retryLookback && index-back >= 0; back++ {
				earlier := events[index-back]
				if earlier.kind == blockToolUse && earlier.call == events[index].call {
					events[index].hit = SignalRetry
					break
				}
			}
		}
	}
	last := -episodeGap - 1
	for index, event := range events {
		if event.hit == "" {
			continue
		}
		if index-last > episodeGap {
			reading.Episodes[event.at.In(location).Format(time.DateOnly)]++
		}
		last = index
	}
	return reading
}

// withinBudget cuts events before the tool call that would exceed budget; a
// budget of 0 keeps them all.
func withinBudget(events []sessionEvent, budget int) []sessionEvent {
	if budget <= 0 {
		return events
	}
	calls := 0
	for index, event := range events {
		if event.kind != blockToolUse {
			continue
		}
		calls++
		if calls > budget {
			return events[:index]
		}
	}
	return events
}

// normalize reads the session events one log line carries.
func normalize(record logRecord) []sessionEvent {
	switch record.EventType {
	case eventTypeGate:
		if record.Decision == decisionDeny {
			return []sessionEvent{{at: record.Time, kind: eventTypeGate, hit: SignalPermissionDenial}}
		}
		return nil
	case eventTypeAssistant, eventTypeUser, eventTypeSystem:
	default:
		return nil
	}
	var event logEvent
	if json.Unmarshal(record.Event, &event) != nil {
		return nil
	}
	if record.EventType == eventTypeSystem {
		if event.Subtype == subtypeDenied {
			return []sessionEvent{{at: record.Time, kind: eventTypeSystem, hit: SignalPermissionDenial}}
		}
		return nil
	}
	var blocks []logBlock
	if json.Unmarshal(event.Message.Content, &blocks) != nil {
		return nil
	}
	var events []sessionEvent
	for _, block := range blocks {
		switch block.Type {
		case blockToolUse:
			events = append(events, sessionEvent{at: record.Time, kind: blockToolUse, call: block.Name + string(bytes.TrimSpace(block.Input))})
		case blockToolResult:
			result := sessionEvent{at: record.Time, kind: blockToolResult}
			if block.IsError {
				result.hit = SignalToolError
			}
			events = append(events, result)
		}
	}
	return events
}

// chiSquareQuantile is the 0.975 (upper) or 0.025 quantile of chi-square
// with degrees of freedom, by the Wilson-Hilferty approximation measure.py
// uses.
func chiSquareQuantile(degrees float64, upper bool) float64 {
	z := z95
	if !upper {
		z = -z95
	}
	return degrees * math.Pow(math.Max(0, 1-2/(9*degrees)+z*math.Sqrt(2/(9*degrees))), 3)
}

// PoissonRate is events per 1,000 units of exposure with its 95% Garwood
// interval; no rate when there is no exposure.
func PoissonRate(events int64, exposure int64) (rate *float64, low *float64, high *float64) {
	if exposure <= 0 {
		return nil, nil, nil
	}
	k, n := float64(events), float64(exposure)
	value := perThousand * k / n
	lower := 0.0
	if events > 0 {
		lower = chiSquareQuantile(2*k, false) / 2
	}
	upper := chiSquareQuantile(2*k+2, true) / 2
	lowValue, highValue := perThousand*lower/n, perThousand*upper/n
	return &value, &lowValue, &highValue
}

// Compounding fits ln(struggles/calls) on the period index over the periods
// with at least one struggle and activeCalls tool calls, weighting each by
// its struggles, and returns the per-period factor with its 95% interval:
// measure.py's trend, over days or weeks.
func Compounding(periods []Rate, period Period) Trend {
	var points []Rate
	for _, point := range periods {
		if point.Struggles >= 1 && point.ToolCalls >= activeCalls {
			points = append(points, point)
		}
	}
	trend := Trend{Weeks: len(points)}
	if len(points) < trendMinimum {
		return trend
	}
	unit := hoursPerDay
	if period == PerWeek {
		unit = hoursPerDay * daysPerWeek
	}
	origin, _ := time.Parse(time.DateOnly, points[0].Day)
	var weights, xs, ys []float64
	for _, point := range points {
		date, _ := time.Parse(time.DateOnly, point.Day)
		weights = append(weights, float64(point.Struggles))
		xs = append(xs, math.Round(date.Sub(origin).Hours()/float64(unit)))
		ys = append(ys, math.Log(float64(point.Struggles)/float64(point.ToolCalls)))
	}
	var sumW, xBar, yBar float64
	for index := range weights {
		sumW += weights[index]
		xBar += weights[index] * xs[index]
		yBar += weights[index] * ys[index]
	}
	xBar, yBar = xBar/sumW, yBar/sumW
	var sxx, sxy float64
	for index := range weights {
		sxx += weights[index] * (xs[index] - xBar) * (xs[index] - xBar)
		sxy += weights[index] * (xs[index] - xBar) * (ys[index] - yBar)
	}
	if sxx <= 0 {
		return trend
	}
	slope := sxy / sxx
	standardError := math.Sqrt(1 / sxx)
	factor, low, high := math.Exp(slope), math.Exp(slope-z95*standardError), math.Exp(slope+z95*standardError)
	trend.Factor, trend.Low, trend.High = &factor, &low, &high
	return trend
}

// Weekly folds daily rates into one rate per week, oldest first.
func Weekly(days []Rate) []Rate {
	byWeek := map[string]*Rate{}
	for _, day := range days {
		week := WeekOf(day.Day)
		rate, known := byWeek[week]
		if !known {
			rate = &Rate{Day: week}
			byWeek[week] = rate
		}
		rate.ToolCalls += day.ToolCalls
		rate.Struggles += day.Struggles
	}
	weeks := make([]Rate, 0, len(byWeek))
	for _, rate := range byWeek {
		rate.PerK, rate.Low, rate.High = PoissonRate(rate.Struggles, rate.ToolCalls)
		weeks = append(weeks, *rate)
	}
	slices.SortFunc(weeks, func(a, b Rate) int { return strings.Compare(a.Day, b.Day) })
	return weeks
}

// Measure computes the loop's numbers over the corpus and the ledger,
// records them as series and publishes the snapshot.
func (loop *Loop) Measure(ctx context.Context) (*Snapshot, error) {
	now := loop.clock.Now().UTC()
	today := loop.day(now)
	days, interventions, err := loop.corpusRates(ctx)
	if err != nil {
		return nil, err
	}
	weeks := Weekly(days)
	snapshot := &Snapshot{
		ComputedAt: now, Day: today, Weeks: weeks, Days: days,
		Compounding: Compounding(weeks, PerWeek), CompoundingDaily: Compounding(days, PerDay),
		Baseline: Baseline{Factor: BaselineWeeklyFactor, Low: BaselineWeeklyLow, High: BaselineWeeklyHigh, RatePerK: BaselineRatePerK, Week: BaselineWeek},
		Struggle: Rate{Day: today}, Week: Rate{Day: WeekOf(today)},
	}
	if len(days) > recentDaysShown {
		snapshot.Days = days[len(days)-recentDaysShown:]
	}
	for _, day := range days {
		if day.Day == today {
			snapshot.Struggle = day
		}
	}
	for _, week := range weeks {
		if week.Day == snapshot.Week.Day {
			snapshot.Week = week
		}
	}
	if err := loop.measureStructure(ctx, snapshot, days, interventions); err != nil {
		return nil, err
	}
	if err := loop.measureFindings(ctx, snapshot, now); err != nil {
		return nil, err
	}
	if err := loop.measureFixers(ctx, snapshot, now); err != nil {
		return nil, err
	}
	merges, err := loop.store.RecentMerges(ctx, recentRowLimit)
	if err != nil {
		return nil, err
	}
	for _, merge := range merges {
		if merge.Merged {
			snapshot.Merges.Merged++
		} else {
			snapshot.Merges.Refused++
		}
	}
	queue, err := loop.store.ProposalsNeedingLabels(ctx)
	if err != nil {
		return nil, err
	}
	snapshot.Queue = len(queue)
	if err := loop.recordSeries(ctx, snapshot, days, now); err != nil {
		return nil, err
	}
	return snapshot, loop.writeSnapshot(snapshot)
}

// corpusRates folds every run log in the corpus into one rate per day,
// oldest first, and the operator interventions per week.
func (loop *Loop) corpusRates(ctx context.Context) ([]Rate, map[string]int64, error) {
	entries, err := loop.corpus.files.ReadDir(rootName)
	if err != nil {
		return nil, nil, err
	}
	hidden, err := loop.hiddenRuns(ctx)
	if err != nil {
		return nil, nil, err
	}
	readings := make([]Reading, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if !entry.IsDir() || hidden[entry.Name()] {
			continue
		}
		lines, err := stdfs.ReadFile(loop.corpus.files, path.Join(entry.Name(), session.EventsFile))
		if errors.Is(err, stdfs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		readings = append(readings, Struggles(lines, loop.location))
	}
	days, interventions := DailyRates(readings)
	return days, interventions, nil
}

// DailyRates folds the readings of several event logs into one rate per day,
// oldest first, and the operator interventions per week.
func DailyRates(readings []Reading) ([]Rate, map[string]int64) {
	calls, episodes, interventions := map[string]int64{}, map[string]int64{}, map[string]int64{}
	for _, reading := range readings {
		for day, count := range reading.Calls {
			calls[day] += count
		}
		for day, count := range reading.Episodes {
			episodes[day] += count
		}
		for day, count := range reading.Interventions {
			interventions[WeekOf(day)] += count
		}
	}
	dayNames := map[string]struct{}{}
	for day := range calls {
		dayNames[day] = struct{}{}
	}
	for day := range episodes {
		dayNames[day] = struct{}{}
	}
	days := make([]Rate, 0, len(dayNames))
	for day := range dayNames {
		rate := Rate{Day: day, ToolCalls: calls[day], Struggles: episodes[day]}
		rate.PerK, rate.Low, rate.High = PoissonRate(rate.Struggles, rate.ToolCalls)
		days = append(days, rate)
	}
	slices.SortFunc(days, func(a, b Rate) int { return strings.Compare(a.Day, b.Day) })
	return days, interventions
}

// measureStructure fills the internal check over the weeks the corpus
// spans.
func (loop *Loop) measureStructure(ctx context.Context, snapshot *Snapshot, days []Rate, interventions map[string]int64) error {
	names := make([]string, 0, len(days))
	for _, day := range days {
		names = append(names, day.Day)
	}
	weeks, err := weekStarts(names, loop.location)
	if err != nil {
		return err
	}
	structure, err := loop.structureWeeks(ctx, weeks, interventions)
	if err != nil {
		return err
	}
	snapshot.Structure = structure
	snapshot.StructureExponent = StructureExponent(structure)
	return nil
}

func (loop *Loop) measureFindings(ctx context.Context, snapshot *Snapshot, now time.Time) error {
	findings, err := loop.store.FindingsSince(ctx, time.Time{})
	if err != nil {
		return err
	}
	miners, err := loop.Miners(ctx)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	first := ""
	for _, finding := range findings {
		counts[finding.Miner]++
		day := loop.day(finding.FoundAt)
		if day == snapshot.Day {
			snapshot.FindingsToday++
		}
		if first == "" || day < first {
			first = day
		}
	}
	snapshot.Findings = len(findings)
	if first != "" {
		start, _ := time.ParseInLocation(time.DateOnly, first, loop.location)
		spanned := math.Floor(loop.dayStart(now).Sub(start).Hours()/hoursPerDay) + 1
		perDay := float64(len(findings)) / spanned
		snapshot.FindingsPerDay = &perDay
	}
	for _, miner := range miners {
		status := MinerStatus{Name: miner.Name, Built: miner.Built, Scope: miner.Scope, Findings: counts[miner.Name]}
		if miner.Built {
			knee, hasKnee, err := loop.knee(ctx, miner)
			if err != nil {
				loop.logger.Warn("ouroboros: knee not fitted", "miner", miner.Name, "error", err)
			}
			status.Knee, status.HasKnee = knee, hasKnee
		}
		switch miner.Scope {
		case ScopeGeneric:
			snapshot.GenericMiners++
		case ScopeTenant:
			snapshot.TenantMiners++
		}
		snapshot.Miners = append(snapshot.Miners, status)
	}
	return nil
}

func (loop *Loop) measureFixers(ctx context.Context, snapshot *Snapshot, now time.Time) error {
	fixers, err := loop.store.FixersStartedSince(ctx, time.Time{})
	if err != nil {
		return err
	}
	summary := FixerSummary{Sessions: len(fixers), BudgetUSD: float64(loop.budget) / usdMicrosPerUSD, Launch: loop.launch}
	var spent int64
	dayStart := loop.dayStart(now)
	for _, fixer := range fixers {
		spent += fixer.CostUSDMicros
		if !fixer.StartedAt.Before(dayStart) {
			summary.SpendTodayUSD += float64(fixer.CostUSDMicros) / usdMicrosPerUSD
		}
		switch fixer.Outcome {
		case OutcomeRunning:
			summary.Running++
		case OutcomeReady:
			summary.Finished++
			summary.Ready++
		default:
			summary.Finished++
		}
	}
	summary.SpendUSD = float64(spent) / usdMicrosPerUSD
	if summary.Ready > 0 {
		perReady := summary.SpendUSD / float64(summary.Ready)
		summary.CostPerReady = &perReady
	}
	if summary.Finished > 0 {
		yield := float64(summary.Ready) / float64(summary.Finished)
		summary.Yield = &yield
	}
	snapshot.Fixers = summary
	return nil
}

// recordSeries writes every day and week of the struggle rate, every week
// of the internal check and today's point of every other series.
func (loop *Loop) recordSeries(ctx context.Context, snapshot *Snapshot, days []Rate, now time.Time) error {
	var points []Point
	for _, day := range days {
		points = append(points, Point{Series: SeriesStruggleRate, Day: day.Day, Value: day.PerK, Low: day.Low, High: day.High, Numerator: day.Struggles, Denominator: day.ToolCalls})
	}
	for _, week := range snapshot.Weeks {
		points = append(points, Point{Series: SeriesStruggleRateWeekly, Day: week.Day, Value: week.PerK, Low: week.Low, High: week.High, Numerator: week.Struggles, Denominator: week.ToolCalls})
	}
	for _, week := range snapshot.Structure {
		points = append(points, Point{Series: SeriesStructure, Day: week.Week, Value: week.PerIntervention, Numerator: int64(week.Gained), Denominator: week.Interventions})
	}
	findings := float64(snapshot.FindingsToday)
	spend := snapshot.Fixers.SpendTodayUSD
	points = append(points,
		Point{Series: SeriesCompounding, Day: snapshot.Day, Value: snapshot.Compounding.Factor, Low: snapshot.Compounding.Low, High: snapshot.Compounding.High, Numerator: int64(snapshot.Compounding.Weeks)},
		Point{Series: SeriesCompoundingDaily, Day: snapshot.Day, Value: snapshot.CompoundingDaily.Factor, Low: snapshot.CompoundingDaily.Low, High: snapshot.CompoundingDaily.High, Numerator: int64(snapshot.CompoundingDaily.Weeks)},
		Point{Series: SeriesStructureExponent, Day: snapshot.Day, Value: snapshot.StructureExponent.Slope, Low: snapshot.StructureExponent.Low, High: snapshot.StructureExponent.High, Numerator: int64(snapshot.StructureExponent.Weeks)},
		Point{Series: SeriesFindings, Day: snapshot.Day, Value: &findings, Numerator: int64(snapshot.FindingsToday)},
		Point{Series: SeriesFixerSpend, Day: snapshot.Day, Value: &spend, Numerator: int64(snapshot.Fixers.Sessions)},
		Point{Series: SeriesCostPerReadyPull, Day: snapshot.Day, Value: snapshot.Fixers.CostPerReady, Numerator: int64(snapshot.Fixers.Ready)},
		Point{Series: SeriesFixerYield, Day: snapshot.Day, Value: snapshot.Fixers.Yield, Numerator: int64(snapshot.Fixers.Ready), Denominator: int64(snapshot.Fixers.Finished)},
	)
	for _, point := range points {
		point.ComputedAt = now
		if err := loop.store.RecordSeries(ctx, point); err != nil {
			return err
		}
	}
	return nil
}
