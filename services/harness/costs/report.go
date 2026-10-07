// Copyright 2026 Candace Labs

package costs

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// ReportFile is the cost model's file under the state directory, written
// whole: the hypervisor reads its parameters and the Workbench its series.
const ReportFile = "costs.json"

// The parameters the hypervisor reads.
const (
	ParameterCacheLifetime     = "cache_lifetime_seconds"
	ParameterKeepAliveRequests = "keep_alive_requests"
	ParameterIdleClose         = "idle_close_seconds"
	ParameterSharedPrefix      = "placement_shared_prefix_tokens"
	ParameterAreaContext       = "area_attach_max_context_tokens"

	unitRequests = "requests"
)

// Policy names, as the tables print them.
const (
	policyNever        = "never"
	policyFixed        = "fixed"
	policyBreakEven    = "break-even rule"
	policyExpected     = "expected-cost minimum"
	policyOffline      = "offline optimum (lower bound)"
	policyStay         = "stay on the model"
	policyRouter       = "current router"
	policyGrouping     = "prefix-aware grouping"
	policyAlwaysNew    = "always new"
	policyTitles       = "title overlap (today's router)"
	policyAffinity     = "area affinity"
	policyBounded      = "area affinity within the break-even context"
	parameterSeparator = "/"
)

// Candidate fixed thresholds the tables compare the derived rules with:
// keep-alive request limits and idle close thresholds. They are what a
// person might have hand-set, not choices.
var (
	fixedRequestLimits  = []int{1, 2, 4, 8}
	fixedIdleThresholds = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}
)

// Build is the whole cost model of the runs: their cost events, the
// measured inputs, every decision's policies replayed over the record, the
// chosen parameters and the daily series. resident is the sampled memory of
// the running turn executors; terms are the ontology's, for area signatures.
func Build(runs []Run, resident []int64, terms []Term, at time.Time) *harnessv1.CostReport {
	lifetime, written := CacheLifetime(runs)
	fits := FitPrices(runs)
	prices := Prices(fits)
	residentMean := 0.0
	if len(resident) > 0 {
		residentMean = mean(int64s(resident), all(len(resident)))
	}
	events := Derive(runs, lifetime, prices, int64(residentMean))
	turns := Classify(runs, lifetime)
	gaps := Gaps(runs)
	binds := Binds(runs)
	days := spanDays(turns)
	report := &harnessv1.CostReport{At: timestamppb.New(at), Runs: int64(len(runs)), Days: int64(len(daySet(turns))), Operations: map[string]int64{}}
	for _, event := range events {
		report.Operations[event.GetOperation().String()]++
	}
	report.Inputs = slices.Concat(gapInputs(gaps), rebuildInputs(turns), hitInputs(turns, lifetime),
		[]*harnessv1.MeasuredInput{residentInput(resident)}, priceInputs(fits), successInputs(runs))
	report.Parameters = append(report.Parameters, &harnessv1.HypervisorParameter{Name: ParameterCacheLifetime, Value: lifetime.Seconds(),
		Unit: unitSeconds, Decision: harnessv1.HypervisorDecision_HYPERVISOR_DECISION_KEEP_ALIVE, Chosen: true,
		Derivation: fmt.Sprintf("the lifetime the endpoint reports for most of the %d cache-written tokens in the record", written)})

	keepAlive := KeepAlive{Lifetime: lifetime, Prices: prices}
	keepPolicies, keepParameter, keepSaving := KeepAlivePolicies(keepAlive, gaps, days)
	report.Policies = append(report.Policies, keepPolicies...)
	report.Parameters = append(report.Parameters, keepParameter)

	premium, latency := reopenCost(turns, prices)
	idle := IdleClose{Lifetime: lifetime, ResidentBytes: residentMean, ReopenPremium: premium.GetValue(), ReopenLatency: time.Duration(latency.GetValue()) * time.Millisecond}
	report.Inputs = append(report.Inputs, premium, latency)
	idlePolicies, idleParameter := idleClosePolicies(idle, gaps, days)
	report.Policies = append(report.Policies, idlePolicies...)
	report.Parameters = append(report.Parameters, idleParameter)
	report.Thin = append(report.Thin, "idle close: the record prices neither turn executor memory nor the operator's wait, "+
		"so the ski-rental break-even (reopen cost over holding cost per second) has no common unit; the table keeps memory, "+
		"latency and dollars apart and nothing is chosen until a memory price is given")

	flips := report.Operations[harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_MODEL_FLIP.String()]
	report.Policies = append(report.Policies, &harnessv1.PolicyOutcome{Decision: harnessv1.HypervisorDecision_HYPERVISOR_DECISION_MODEL_FLIP,
		Policy: policyStay, Today: true, Chosen: true, Note: fmt.Sprintf("%d recorded switches", flips)})
	report.Thin = append(report.Thin, fmt.Sprintf("model flip: %d recorded switches; the metrical task system (Borodin, Linial and Saks) "+
		"needs a recorded switch to replay, and the record has no per-turn outcome to charge a model's quality, so staying is kept", flips))

	placement := NewPlacement(binds, prices)
	placementPolicies, placementParameters, placementSaving := placementPolicies(placement, binds, turns, days)
	report.Policies = append(report.Policies, placementPolicies...)
	report.Parameters = append(report.Parameters, placementParameters...)

	replay := &AreaReplay{Runs: runs, Terms: terms, Lifetime: lifetime, Prices: prices}
	areaInputs, areaPolicies, areaParameter := AreaAffinityPolicies(replay, days)
	report.Inputs = append(report.Inputs, areaInputs...)
	report.Policies = append(report.Policies, areaPolicies...)
	report.Parameters = append(report.Parameters, areaParameter)
	report.Thin = append(report.Thin, "area affinity: the embedding match for a request that names no path or term is not built; "+
		"the inputs give the share of requests it would serve")

	// The series' saving is against the spend the record holds. Area
	// affinity's baseline is a replay of today's router, which the recorded
	// runs mostly predate, so its saving stays in its table.
	report.Series = series(runs, events, turns, func(day string) float64 { return keepSaving[day] + placementSaving[day] })
	return report
}

func int64s(values []int64) []float64 {
	converted := make([]float64, len(values))
	for index, value := range values {
		converted[index] = float64(value)
	}
	return converted
}

// spanDays is the record's length in days, first turn to last.
func spanDays(turns []Classified) float64 {
	if len(turns) == 0 {
		return 1
	}
	first, last := turns[0].Turn.Start, turns[0].Turn.End
	for _, turn := range turns {
		first, last = minTime(first, turn.Turn.Start), maxTime(last, turn.Turn.End)
	}
	return max(last.Sub(first).Hours()/24, 1.0/24)
}

func minTime(left time.Time, right time.Time) time.Time {
	if right.Before(left) {
		return right
	}
	return left
}

func maxTime(left time.Time, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}

func daySet(turns []Classified) map[string]bool {
	days := map[string]bool{}
	for _, turn := range turns {
		days[turn.Turn.Start.UTC().Format(dayFormat)] = true
	}
	return days
}

// outcome replays one policy's per-unit costs against today's: dollars per
// day with the bootstrap interval over the units, and the paired saving.
func outcome(decision harnessv1.HypervisorDecision, policy string, parameter float64, costs []float64, today []float64, days float64) *harnessv1.PolicyOutcome {
	total := func(values []float64, indices []int) float64 {
		return mean(values, indices) * float64(len(indices)) / days
	}
	saving := make([]float64, len(costs))
	for index := range costs {
		saving[index] = today[index] - costs[index]
	}
	return &harnessv1.PolicyOutcome{Decision: decision, Policy: policy, Parameter: parameter,
		UsdPerDay: total(costs, all(len(costs))), UsdPerDayInterval: bootstrap(len(costs), func(indices []int) float64 { return total(costs, indices) }),
		SavingUsdPerDay: total(saving, all(len(saving))), SavingInterval: bootstrap(len(saving), func(indices []int) float64 { return total(saving, indices) })}
}

// choose marks the cheapest selectable policy chosen when its saving's
// interval excludes zero, and today's otherwise: a saving the record cannot
// tell from noise does not move a parameter.
func choose(outcomes []*harnessv1.PolicyOutcome, selectable func(outcome *harnessv1.PolicyOutcome) bool) *harnessv1.PolicyOutcome {
	var best, today *harnessv1.PolicyOutcome
	for _, candidate := range outcomes {
		if candidate.GetToday() {
			today = candidate
		}
		if selectable(candidate) && (best == nil || candidate.GetUsdPerDay() < best.GetUsdPerDay()) {
			best = candidate
		}
	}
	if best == nil || best.GetSavingInterval().GetLow() <= 0 {
		best = today
	}
	best.Chosen = true
	return best
}

// KeepAlivePolicies replays cache keep-alive as ski rental over every idle
// gap: never (today), fixed request limits, the break-even rule, the limit
// with the least expected cost under the recorded gaps, and the offline
// optimum. It returns the outcomes, the chosen limit, and the chosen
// policy's saving per day of the record.
func KeepAlivePolicies(keepAlive KeepAlive, gaps []Gap, days float64) ([]*harnessv1.PolicyOutcome, *harnessv1.HypervisorParameter, map[string]float64) {
	decision := harnessv1.HypervisorDecision_HYPERVISOR_DECISION_KEEP_ALIVE
	costsAt := func(limit func(gap Gap) int) []float64 {
		costs := make([]float64, len(gaps))
		for index, gap := range gaps {
			costs[index] = keepAlive.Cost(gap, limit(gap))
		}
		return costs
	}
	constant := func(limit int) func(gap Gap) int { return func(gap Gap) int { return limit } }
	today := costsAt(constant(0))
	never := outcome(decision, policyNever, 0, today, today, days)
	never.Today = true
	outcomes := []*harnessv1.PolicyOutcome{never}
	for _, limit := range fixedRequestLimits {
		outcomes = append(outcomes, outcome(decision, policyFixed, float64(limit), costsAt(constant(limit)), today, days))
	}
	outcomes = append(outcomes, outcome(decision, policyBreakEven, -1, costsAt(keepAlive.BreakEven), today, days))
	longest := 0
	for _, gap := range gaps {
		longest = max(longest, keepAlive.Requests(gap))
	}
	expected, expectedTotal := 0, 0.0
	for limit := 0; limit <= longest; limit++ {
		total := 0.0
		for _, cost := range costsAt(constant(limit)) {
			total += cost
		}
		if limit == 0 || total < expectedTotal {
			expected, expectedTotal = limit, total
		}
	}
	outcomes = append(outcomes, outcome(decision, policyExpected, float64(expected), costsAt(constant(expected)), today, days))
	offline := make([]float64, len(gaps))
	for index, gap := range gaps {
		offline[index] = keepAlive.Offline(gap)
	}
	bound := outcome(decision, policyOffline, -1, offline, today, days)
	bound.Note = "not a policy: what knowing every gap in advance would pay"
	outcomes = append(outcomes, bound)
	chosen := choose(outcomes, func(candidate *harnessv1.PolicyOutcome) bool {
		return candidate.GetPolicy() == policyFixed || candidate.GetPolicy() == policyExpected || candidate.GetToday()
	})
	parameter := &harnessv1.HypervisorParameter{Name: ParameterKeepAliveRequests, Value: chosen.GetParameter(), Unit: unitRequests,
		Decision: decision, Chosen: chosen != never,
		Derivation: fmt.Sprintf("%s over %d idle gaps: %.2f $/day [%.2f, %.2f], saving %.2f $/day [%.2f, %.2f] against never; one request reads the prefix just before each expiry",
			chosen.GetPolicy(), len(gaps), chosen.GetUsdPerDay(), chosen.GetUsdPerDayInterval().GetLow(), chosen.GetUsdPerDayInterval().GetHigh(),
			chosen.GetSavingUsdPerDay(), chosen.GetSavingInterval().GetLow(), chosen.GetSavingInterval().GetHigh())}
	saving := map[string]float64{}
	for _, gap := range gaps {
		saving[gap.Day] += keepAlive.Cost(gap, 0) - keepAlive.Cost(gap, int(chosen.GetParameter()))
	}
	return outcomes, parameter, saving
}

// reopenCost is a reopen within the cache lifetime against a warm turn: the
// cache write premium of its first call priced at its model's write price,
// and its added time to first token.
func reopenCost(turns []Classified, prices map[string]Price) (*harnessv1.MeasuredInput, *harnessv1.MeasuredInput) {
	writes, ttfts := map[TurnClass][]float64{}, map[TurnClass][]float64{}
	for _, turn := range turns {
		if turn.Class != ClassReopen && turn.Class != ClassWarm {
			continue
		}
		writes[turn.Class] = append(writes[turn.Class], float64(turn.Turn.First.Write)*prices[turn.Turn.Model].Write)
		if turn.Turn.TTFTKnown {
			ttfts[turn.Class] = append(ttfts[turn.Class], float64(turn.Turn.TTFT.Milliseconds()))
		}
	}
	difference := func(name string, unit string, values map[TurnClass][]float64, method string) *harnessv1.MeasuredInput {
		reopened, warm := values[ClassReopen], values[ClassWarm]
		low := bootstrap(len(reopened), func(indices []int) float64 { return mean(reopened, indices) })
		high := bootstrap(len(warm), func(indices []int) float64 { return mean(warm, indices) })
		return &harnessv1.MeasuredInput{Name: name, Group: classNames[ClassReopen], Unit: unit, N: int64(len(reopened)),
			Value:    max(0, mean(reopened, all(len(reopened)))-mean(warm, all(len(warm)))),
			Interval: &harnessv1.Interval{Low: max(0, low.GetLow()-high.GetHigh()), High: max(0, low.GetHigh()-high.GetLow())}, Method: method}
	}
	return difference(InputReopenPremium, unitUSD, writes, "mean first-call cache write in dollars, reopened turns less warm turns, floored at zero"),
		difference(InputReopenPremium, unitMilliseconds, ttfts, "mean time to first token, reopened turns less warm turns, floored at zero")
}

// idleClosePolicies replays idle close over every idle gap: never (today)
// and fixed thresholds, in dollars, memory held and latency added.
func idleClosePolicies(idle IdleClose, gaps []Gap, days float64) ([]*harnessv1.PolicyOutcome, *harnessv1.HypervisorParameter) {
	decision := harnessv1.HypervisorDecision_HYPERVISOR_DECISION_IDLE_CLOSE
	replay := func(threshold time.Duration) ([]float64, float64, float64) {
		costs := make([]float64, len(gaps))
		var held, latency float64
		for index, gap := range gaps {
			cost := idle.Cost(gap, threshold)
			costs[index], held, latency = cost.USD, held+cost.ResidentGBHours, latency+cost.Latency.Seconds()
		}
		return costs, held / days, latency / days
	}
	today, held, latency := replay(-1)
	never := outcome(decision, policyNever, -1, today, today, days)
	never.Today, never.Chosen, never.ResidentGbHoursPerDay, never.AddedLatencySecondsPerDay = true, true, held, latency
	outcomes := []*harnessv1.PolicyOutcome{never}
	for _, threshold := range fixedIdleThresholds {
		costs, held, latency := replay(threshold)
		closed := outcome(decision, policyFixed, threshold.Seconds(), costs, today, days)
		closed.ResidentGbHoursPerDay, closed.AddedLatencySecondsPerDay = held, latency
		outcomes = append(outcomes, closed)
	}
	parameter := &harnessv1.HypervisorParameter{Name: ParameterIdleClose, Value: -1, Unit: unitSeconds, Decision: decision,
		Derivation: fmt.Sprintf("not chosen: holding costs %.0f MB per executor and a reopen %.4f $ and %s, with no price between memory and dollars or latency in the record; -1 is never, today's behaviour",
			idle.ResidentBytes/1e6, idle.ReopenPremium, idle.ReopenLatency)}
	return outcomes, parameter
}

// placementPolicies replays placement over every bind: the current router
// against prefix-aware grouping, with h_fleet under each.
func placementPolicies(placement Placement, binds []Bind, turns []Classified, days float64) ([]*harnessv1.PolicyOutcome, []*harnessv1.HypervisorParameter, map[string]float64) {
	decision := harnessv1.HypervisorDecision_HYPERVISOR_DECISION_PLACEMENT
	today, grouped := make([]float64, len(binds)), make([]float64, len(binds))
	var shifted int64
	for index, bind := range binds {
		price := placement.Prices[bind.Model]
		today[index] = float64(bind.First.Write) * price.Write
		grouped[index] = today[index] - placement.Saving(bind)
		shifted += placement.Shift(bind)
	}
	var fleet Tokens
	for _, turn := range turns {
		fleet = fleet.add(turn.Turn.Tokens)
	}
	router := outcome(decision, policyRouter, fleet.HitRatio(), today, today, days)
	router.Today, router.Note = true, fmt.Sprintf("h_fleet %.4f", fleet.HitRatio())
	groupedHit := 0.0
	if fleet.Input() > 0 {
		groupedHit = float64(fleet.Read+shifted) / float64(fleet.Input())
	}
	grouping := outcome(decision, policyGrouping, groupedHit, grouped, today, days)
	grouping.Note = fmt.Sprintf("h_fleet %.4f; assumes the shared prefix is the same bytes on every bind of a model, as the endpoint's reads show, until the executor's version changes it", groupedHit)
	outcomes := []*harnessv1.PolicyOutcome{router, grouping}
	chosen := choose(outcomes, func(candidate *harnessv1.PolicyOutcome) bool { return true })
	parameters := []*harnessv1.HypervisorParameter{}
	saving := map[string]float64{}
	for _, model := range slices.Sorted(maps.Keys(placement.Shared)) {
		parameters = append(parameters, &harnessv1.HypervisorParameter{Name: ParameterSharedPrefix + parameterSeparator + model,
			Value: float64(placement.Shared[model]), Unit: unitTokens, Decision: decision, Chosen: chosen == grouping,
			Derivation: fmt.Sprintf("the longest prefix the endpoint already held on a bind's first call of the model; grouping binds so it is still cached saves %.2f $/day [%.2f, %.2f] fleet-wide",
				grouping.GetSavingUsdPerDay(), grouping.GetSavingInterval().GetLow(), grouping.GetSavingInterval().GetHigh())})
	}
	if chosen == grouping {
		for _, bind := range binds {
			saving[bind.Day] += placement.Saving(bind)
		}
	}
	return outcomes, parameters, saving
}

// series is the record per UTC day: dollars per virtual-session-hour by
// operation, h_fleet and the chosen policies' saving.
func series(runs []Run, events []*harnessv1.CostEvent, turns []Classified, saving func(day string) float64) []*harnessv1.CostDay {
	hours := map[string]float64{}
	for _, run := range runs {
		for _, span := range run.Open {
			for from := span.From; from.Before(span.To); {
				day := from.UTC().Truncate(24 * time.Hour)
				to := minTime(span.To, day.Add(24*time.Hour))
				hours[day.Format(dayFormat)] += to.Sub(from).Hours()
				from = to
			}
		}
	}
	usd := map[string]map[string]float64{}
	for _, event := range events {
		day := event.GetAt().AsTime().UTC().Format(dayFormat)
		if usd[day] == nil {
			usd[day] = map[string]float64{}
		}
		usd[day][event.GetOperation().String()] += event.GetUsd()
	}
	fleet := map[string]Tokens{}
	for _, turn := range turns {
		day := turn.Turn.Start.UTC().Format(dayFormat)
		fleet[day] = fleet[day].add(turn.Turn.Tokens)
	}
	days := []*harnessv1.CostDay{}
	for _, day := range slices.Sorted(maps.Keys(fleet)) {
		point := &harnessv1.CostDay{Day: day, SessionHours: hours[day], HFleet: fleet[day].HitRatio(), SavingUsd: saving(day),
			UsdPerSessionHour: map[string]float64{}}
		for operation, dollars := range usd[day] {
			if hours[day] > 0 {
				point.UsdPerSessionHour[operation] = dollars / hours[day]
			}
		}
		days = append(days, point)
	}
	return days
}

// The area-affinity inputs' names.
const (
	InputTokensPerByte = "tokens per read byte"
	InputRereadTokens  = "re-read of a file another live real session had read"
	InputRereadShare   = "re-read share of read bytes"
	InputRereadUSD     = "re-read cost at the cache write price"
	InputProseOnly     = "requests naming no path or term"

	unitTokensPerDay = "tokens/day"
	unitUSDPerDay    = "$/day"
)

// AreaAffinityPolicies measures the prize area affinity goes after and
// replays it against today's router and against always opening a new real
// session. Every policy's dollars are against always new. It returns the
// inputs, the outcomes and the break-even context.
func AreaAffinityPolicies(replay *AreaReplay, days float64) ([]*harnessv1.MeasuredInput, []*harnessv1.PolicyOutcome, *harnessv1.HypervisorParameter) {
	decision := harnessv1.HypervisorDecision_HYPERVISOR_DECISION_AREA_AFFINITY
	ratios := TokensPerByte(replay.Runs)
	replay.TokensPerByte = median(ratios, all(len(ratios)))
	inputs := []*harnessv1.MeasuredInput{{Name: InputTokensPerByte, Group: groupAll, Unit: unitRatio, N: int64(len(ratios)), Value: replay.TokensPerByte,
		Interval: bootstrap(len(ratios), func(indices []int) float64 { return median(ratios, indices) }), Exact: false,
		Method: "median cache write of the call after a lone read result of at least 8 KiB, over the result's bytes"}}

	inputs = append(append(inputs, rereadInputs(replay, days)...), proseInput(replay))

	affinity := replay.Affinity(-1)
	breakEvens := []float64{}
	for _, route := range affinity {
		if route.Attached && route.Credit > 0 {
			breakEvens = append(breakEvens, route.BreakEven)
		}
	}
	bound := median(breakEvens, all(len(breakEvens)))
	deltas := func(routes []AreaRoute) []float64 {
		values := make([]float64, len(routes))
		for index, route := range routes {
			values[index] = route.Delta
		}
		return values
	}
	router := replay.Router()
	today := deltas(router)
	attached := func(routes []AreaRoute) int {
		count := 0
		for _, route := range routes {
			if route.Attached {
				count++
			}
		}
		return count
	}
	bounded := replay.Affinity(int64(bound))
	rows := []struct {
		policy string
		routes []AreaRoute
	}{{policyAlwaysNew, make([]AreaRoute, len(router))}, {policyTitles, router}, {policyAffinity, affinity}, {policyBounded, bounded}}
	outcomes := []*harnessv1.PolicyOutcome{}
	for _, row := range rows {
		parameter := 0.0
		if row.policy == policyBounded {
			parameter = bound
		}
		replayed := outcome(decision, row.policy, parameter, deltas(row.routes), today, days)
		replayed.Today = row.policy == policyTitles
		replayed.Note = fmt.Sprintf("%d of %d binds attached; dollars against always new, which the recorded runs did; today's router is replayed, not recorded", attached(row.routes), len(row.routes))
		outcomes = append(outcomes, replayed)
	}
	chosen := choose(outcomes, func(candidate *harnessv1.PolicyOutcome) bool { return !candidate.GetToday() })
	parameter := &harnessv1.HypervisorParameter{Name: ParameterAreaContext, Value: bound, Unit: unitTokens, Decision: decision, Chosen: len(breakEvens) > 0,
		Derivation: fmt.Sprintf("median over %d area-affinity attaches with avoided exploration of credit * (write - read) / (read * calls) [%.0f, %.0f]: "+
			"a warm real session carrying more context costs more per call than the exploration it saves, so it is closed or compacted instead of attached; chosen policy %q",
			len(breakEvens), bootstrap(len(breakEvens), func(indices []int) float64 { return median(breakEvens, indices) }).GetLow(),
			bootstrap(len(breakEvens), func(indices []int) float64 { return median(breakEvens, indices) }).GetHigh(), chosen.GetPolicy())}
	return inputs, outcomes, parameter
}

// ratioOf is a count over a total, zero for none.
func ratioOf(count int, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(count) / float64(total)
}

// rereadInputs is the prize area affinity goes after: reads of a file
// another real session, live at the time, had already read, in tokens per
// day, as a share of all reads, and in dollars per day.
func rereadInputs(replay *AreaReplay, days float64) []*harnessv1.MeasuredInput {
	rereads, readBytes := Rereads(replay.Runs)
	perRun, dollarsPerRun, readPerRun := make([]float64, len(replay.Runs)), make([]float64, len(replay.Runs)), make([]float64, len(replay.Runs))
	for _, reread := range rereads {
		tokens := float64(reread.Bytes) * replay.TokensPerByte
		perRun[reread.Run] += tokens
		dollarsPerRun[reread.Run] += tokens * replay.Prices[reread.Model].Write
	}
	for index, run := range replay.Runs {
		for _, access := range run.Area.Accesses {
			if access.Kind == AccessRead {
				readPerRun[index] += float64(access.Bytes) * replay.TokensPerByte
			}
		}
	}
	perDay := func(values []float64) func(indices []int) float64 {
		return func(indices []int) float64 { return mean(values, indices) * float64(len(indices)) / days }
	}
	share := func(indices []int) float64 {
		reread, read := mean(perRun, indices), mean(readPerRun, indices)
		if read == 0 {
			return 0
		}
		return reread / read
	}
	units := len(replay.Runs)
	method := "reads of a path that another real session, open and current at the time, had read before; bytes times tokens per byte; interval over runs"
	return []*harnessv1.MeasuredInput{
		&harnessv1.MeasuredInput{Name: InputRereadTokens, Group: groupAll, Unit: unitTokensPerDay, N: int64(len(rereads)), Value: perDay(perRun)(all(units)),
			Interval: bootstrap(units, perDay(perRun)), Method: method},
		&harnessv1.MeasuredInput{Name: InputRereadShare, Group: groupAll, Unit: unitRatio, N: int64(len(rereads)), Value: share(all(units)),
			Interval: bootstrap(units, share), Method: fmt.Sprintf("re-read bytes over all %d read bytes; interval over runs", readBytes)},
		&harnessv1.MeasuredInput{Name: InputRereadUSD, Group: groupAll, Unit: unitUSDPerDay, N: int64(len(rereads)), Value: perDay(dollarsPerRun)(all(units)),
			Interval: bootstrap(units, perDay(dollarsPerRun)), Method: "each re-read written once to the reader's cache at its model's write price; its later reads are not counted"}}
}

// proseInput is the share of requests naming no known path and no term:
// where an embedding match would be the only way to read their area.
func proseInput(replay *AreaReplay) *harnessv1.MeasuredInput {
	binds := replay.bindsOf()
	prose := 0
	for _, bind := range binds {
		if len(replay.Area(bind)) == 0 {
			prose++
		}
	}
	return &harnessv1.MeasuredInput{Name: InputProseOnly, Group: groupAll, Unit: unitRatio, N: int64(len(binds)),
		Value: ratioOf(prose, len(binds)), Interval: wilson(prose, len(binds)), Exact: true,
		Method: "first prompts naming no known repository path and no ontology term, where an embedding match would act; Wilson interval"}
}
