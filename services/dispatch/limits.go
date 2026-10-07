// Copyright 2026 Candace Labs

package dispatch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	stdfs "io/fs"
	"path"
	"slices"
	"time"

	"github.com/candacelabs/csf/pkg/config"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/ouroboros"
)

// providerAnthropic is the provider a bare model name is served by: a name
// with no provider prefix runs on the operator's own Anthropic credential.
const providerAnthropic = "anthropic"

// providerOfModel is the provider that serves a model name: the prefix before
// the first slash, or Anthropic for a bare name, which no slash names. It is
// the provider a rate limit scoped to a provider is measured against.
func providerOfModel(model string) string {
	parsed, err := config.ParseProviderModel(model)
	if err != nil {
		return providerAnthropic
	}
	return parsed.ProviderID
}

// providerOf is the provider that served the run in run, read from the model
// the run recorded in its run.json: a session's recipe is rewritten mid-run —
// the router moves it to a free model — but the model it started on is what
// its rate_limit_event reports on. A run with no recorded model ran on a bare
// Anthropic model.
func providerOf(state stdfs.FS, run string) string {
	content, err := stdfs.ReadFile(state, path.Join(run, session.RunStateFile))
	if err != nil {
		return providerAnthropic
	}
	var recorded session.RunState
	if json.Unmarshal(content, &recorded) != nil {
		return providerAnthropic
	}
	return providerOfModel(recorded.Model)
}

// LimitName says which measure a [Limit] was derived from.
type LimitName string

// The measures the slice dispatcher's admission is the minimum of.
const (
	// LimitHarness is the harness's launch check: the idle cores (cores
	// minus the one-minute load) and the disk floor.
	LimitHarness LimitName = "harness"
	// LimitBudget is the mining loop's daily budget.
	LimitBudget LimitName = "budget"
	// LimitRate is the provider's rate-limit headroom, read from the latest
	// rate_limit_event any session logged.
	LimitRate LimitName = "rate"
)

// The units a limit's remaining resource is measured in.
const (
	unitSessions = "sessions"
	unitUSD      = "usd"
	unitHeadroom = "fraction of the provider window"
)

// Limit is one bound on the slice dispatcher's launches, measured from data
// at one pass, with the derivation that produced it in words.
type Limit struct {
	Name LimitName `json:"name"`
	// Provider is the provider this limit applies to, empty when it bounds
	// every launch. A rate limit is scoped to the provider its newest
	// rate_limit_event came from, so it bounds only that provider's launches
	// and never another's.
	Provider string `json:"provider,omitempty"`
	// Bounded is false when the measure sets no bound now.
	Bounded bool `json:"bounded"`
	// Launches is how many more dispatched sessions the measure admits now;
	// it is read only when Bounded.
	Launches int `json:"launches"`
	// Remaining is what is left of the measured resource, in Unit.
	Remaining float64 `json:"remaining"`
	Unit      string  `json:"unit"`
	// Derivation is how the bound follows from the data.
	Derivation string `json:"derivation"`
}

// LimitSource measures one limit at a pass; running is how many dispatched
// sessions hold a session now. A source that cannot measure returns a
// bounded limit of no launches saying why: the dispatcher fails closed.
type LimitSource func(ctx context.Context, running int) Limit

// unmeasured is the limit of a source that could not read its data.
func unmeasured(name LimitName, unit string, err error) Limit {
	return Limit{Name: name, Bounded: true, Unit: unit, Derivation: fmt.Sprintf("not measured, so no launches: %v", err)}
}

// admission is how many dispatched sessions the limits admit at once: those
// running when they were measured plus the fewest launches any bounded
// unscoped limit allows. A provider-scoped limit bounds only launches of its
// own provider, so it does not bound the total here: the schedule drops its
// provider's slices instead.
func admission(limits []Limit, running int) int {
	launches := -1
	for _, limit := range limits {
		if limit.Provider != "" || !limit.Bounded {
			continue
		}
		if launches < 0 || limit.Launches < launches {
			launches = max(0, limit.Launches)
		}
	}
	if launches < 0 {
		return running
	}
	return running + launches
}

// harnessLimit reads the harness's own launch check. The worker cap is the
// idle cores, floored at one; it is a cap on the dispatched sessions, so the
// launches are the cap less what runs. Free disk below the floor, twice the
// largest run directory measured, admits no launch; until a run has been
// measured the floor is unknown and does not bound. A held admission admits
// none.
func harnessLimit(sessions ISessionHost) LimitSource {
	return func(ctx context.Context, running int) Limit {
		check, err := sessions.Check(ctx)
		if err != nil {
			return unmeasured(LimitHarness, unitSessions, err)
		}
		launches := max(0, int(check.GetWorkerCap())-running)
		limit := Limit{Name: LimitHarness, Bounded: true, Launches: launches, Remaining: float64(launches), Unit: unitSessions}
		limit.Derivation = fmt.Sprintf("%d cores − load1 %.2f = worker cap %d; %d dispatched sessions run → %d launches",
			check.GetCores(), check.GetLoadOneMinute(), check.GetWorkerCap(), running, launches)
		switch floor, free := check.GetDiskFloorBytes(), check.GetFreeBytes(); {
		case floor == 0:
			limit.Derivation += "; disk floor unknown: no run directory measured yet"
		case free < floor:
			limit.Launches, limit.Remaining = 0, 0
			limit.Derivation += fmt.Sprintf("; %s free is below the disk floor of %s (twice the largest run directory) → no launches", gigabytes(free), gigabytes(floor))
		default:
			limit.Derivation += fmt.Sprintf("; %s free ≥ disk floor %s (twice the largest run directory)", gigabytes(free), gigabytes(floor))
		}
		return limit
	}
}

func gigabytes(bytes uint64) string {
	return fmt.Sprintf("%.1f GB", float64(bytes)/1e9)
}

// DailyBudgetReader reads the mining loop's daily budget:
// (*ouroboros.Loop).DailyBudget.
type DailyBudgetReader func(ctx context.Context) (ouroboros.DailyBudget, error)

// usdMicrosPerUSD is the scale of every amount the ledger keeps.
const usdMicrosPerUSD = 1e6

// DailyBudgetLimit bounds launches by the mining loop's daily budget: what
// is left of the day's cap after the spend and the running fixers'
// reservation buys that many sessions at what one is expected to cost — the
// ledger's mean once it has measured enough finished sessions, the measured
// baseline before. The ledger reserves only for its fixers, so each running
// dispatched session reserves one more of those, and the launches are what
// remains.
func DailyBudgetLimit(read DailyBudgetReader) LimitSource {
	return func(ctx context.Context, running int) Limit {
		budget, err := read(ctx)
		if err != nil {
			return unmeasured(LimitBudget, unitUSD, err)
		}
		left := budget.LeftUSDMicros()
		limit := Limit{Name: LimitBudget, Bounded: true, Remaining: float64(left) / usdMicrosPerUSD, Unit: unitUSD}
		expectation := "the measured baseline"
		if budget.MeasuredSessions > 0 {
			expectation = fmt.Sprintf("the mean of %d finished sessions", budget.MeasuredSessions)
		}
		sessions := 0
		if budget.ExpectedSessionUSDMicros > 0 {
			sessions = int(left / budget.ExpectedSessionUSDMicros)
		}
		limit.Launches = max(0, sessions-running)
		limit.Derivation = fmt.Sprintf("day %s: cap $%.2f − spent $%.2f − reserved $%.2f = $%.2f left; one session is expected to cost $%.2f (%s), so it buys %d; %d dispatched sessions run → %d launches",
			budget.Day, float64(budget.CapUSDMicros)/usdMicrosPerUSD, float64(budget.SpentUSDMicros)/usdMicrosPerUSD,
			float64(budget.ReservedUSDMicros)/usdMicrosPerUSD, limit.Remaining, float64(budget.ExpectedSessionUSDMicros)/usdMicrosPerUSD,
			expectation, sessions, running, limit.Launches)
		return limit
	}
}

// The part of a turn executor's rate_limit_event the rate limit reads, as the
// session's event log records it.
const (
	rateLimitEventType = "rate_limit_event"
	// rateStatusRejected is the provider's status once a window is spent:
	// no launches until the window it names resets.
	rateStatusRejected = "rejected"
	// rateWarnUtilization is the lowest window utilization the provider
	// marked as surpassing its threshold, measured over the 1,772
	// rate_limit_event records of this host's session logs on 2026-10-05:
	// allowed_warning with surpassedThreshold 0.75 on seven_day (280
	// records, utilization 0.75 to 0.99) and 0.9 on five_hour (39 records).
	// The 61 allowed_warning records with no surpassedThreshold sat at 0.27
	// to 0.31 of the seven-day window, an early notice rather than a limit,
	// so the status alone does not stop launches.
	rateWarnUtilization = 0.75
)

// rateWindow is one provider window a rate_limit_event reports: how much of
// it is used and when it resets.
type rateWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resetsAt"`
}

// rateRecordInfo is the rate_limit_info a rate_limit_event carries.
type rateRecordInfo struct {
	Status   string                `json:"status"`
	Type     string                `json:"rateLimitType"`
	ResetsAt int64                 `json:"resetsAt"`
	Windows  map[string]rateWindow `json:"unifiedWindows"`
}

// rateRecordEvent is the event object of a rate_limit_event log line.
type rateRecordEvent struct {
	Info rateRecordInfo `json:"rate_limit_info"`
}

// rateRecord is one rate_limit_event as a session's event log records it.
type rateRecord struct {
	Time      time.Time       `json:"time"`
	EventType string          `json:"event_type"`
	Event     rateRecordEvent `json:"event"`
}

// rateEvent is the newest rate_limit_event any session logged, with the run
// whose log holds it: the run names the provider the event reports on.
type rateEvent struct {
	Record rateRecord
	Run    string
}

// RateLimit bounds launches by the active provider's rate-limit headroom,
// read from the latest rate_limit_event the runs launched through that
// provider logged under state (the harness state directory, one run directory
// per session). A run is attributed to the provider its event log's
// harness_provider_applied record names, and a run with none ran on the
// executor's own provider; only the active provider's records count, so a
// window spent on a provider the host has switched away from does not hold
// launches. The headroom is one less the highest utilization of a window that
// has not reset. Launches stop while that utilization is at or above
// rateWarnUtilization, the lowest threshold the provider has warned at, and
// while the record is rejected and its window has not reset. Otherwise, or
// with no record at all, the limit sets no bound.
//
// activeProvider is the active provider's base URL, or empty for the
// executor's own provider, which the host records no router entry for.
func RateLimit(state stdfs.FS, clock harness.IClock, activeProvider string) LimitSource {
	return func(_ context.Context, _ int) Limit {
		event, err := latestRateRecord(state, activeProvider)
		if err != nil {
			return unmeasured(LimitRate, unitHeadroom, err)
		}
		limit := Limit{Name: LimitRate, Remaining: 1, Unit: unitHeadroom}
		if event.Run == "" {
			limit.Derivation = fmt.Sprintf("no rate_limit_event from %s in any session log yet; no bound", providerName(activeProvider))
			return limit
		}
		limit.Provider = providerOf(state, event.Run)
		now := clock.Now()
		info := event.Record.Event.Info
		binding := ""
		names := make([]string, 0, len(info.Windows))
		for name := range info.Windows {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			window := info.Windows[name]
			if time.Unix(window.ResetsAt, 0).After(now) && 1-window.Utilization < limit.Remaining {
				limit.Remaining, binding = 1-window.Utilization, name
			}
		}
		limit.Remaining = max(0, limit.Remaining)
		seen := fmt.Sprintf("latest rate_limit_event from %s at %s (run %s, provider %s): status %s", providerName(activeProvider), event.Record.Time.UTC().Format(time.RFC3339), event.Run, limit.Provider, info.Status)
		headroom := fmt.Sprintf("headroom %.2f", limit.Remaining)
		if binding != "" {
			headroom += " on the " + binding + " window"
		}
		resets := time.Unix(info.ResetsAt, 0)
		switch {
		case info.Status == rateStatusRejected && resets.After(now):
			limit.Bounded = true
			limit.Derivation = fmt.Sprintf("%s on the %s window until %s; %s → no launches", seen, info.Type, resets.UTC().Format(time.RFC3339), headroom)
		case 1-limit.Remaining >= rateWarnUtilization:
			limit.Bounded = true
			limit.Derivation = fmt.Sprintf("%s; %s, at or above the provider's lowest warning threshold %.2f → no launches", seen, headroom, rateWarnUtilization)
		default:
			limit.Derivation = fmt.Sprintf("%s; %s, below the provider's lowest warning threshold %.2f; no bound", seen, headroom, rateWarnUtilization)
		}
		return limit
	}
}

// providerName names the provider whose records the limit reads: the base URL
// when a router is active, the executor's own provider otherwise.
func providerName(activeProvider string) string {
	if activeProvider == "" {
		return "the executor's own provider"
	}
	return activeProvider
}

// latestRateRecord finds the most recent rate_limit_event the runs launched
// through activeProvider logged, and the run that logged it. A run is
// attributed to the provider its harness_provider_applied record names, empty
// for a run with none, so a record from a provider the host has switched away
// from is left out. Logs are read newest first, and a log last written before
// the best record found cannot hold a later one, so a busy state directory
// reads only its recent logs.
func latestRateRecord(state stdfs.FS, activeProvider string) (rateEvent, error) {
	entries, err := stdfs.ReadDir(state, ".")
	if err != nil {
		return rateEvent{}, fmt.Errorf("dispatch: list session logs: %w", err)
	}
	type log struct {
		run      string
		modified time.Time
	}
	var logs []log
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := stdfs.Stat(state, path.Join(entry.Name(), session.EventsFile))
		if err != nil {
			continue
		}
		logs = append(logs, log{run: entry.Name(), modified: info.ModTime()})
	}
	slices.SortFunc(logs, func(a, b log) int { return b.modified.Compare(a.modified) })
	var latest rateEvent
	for _, candidate := range logs {
		if latest.Run != "" && candidate.modified.Before(latest.Record.Time) {
			break
		}
		content, err := stdfs.ReadFile(state, path.Join(candidate.run, session.EventsFile))
		if err != nil {
			return rateEvent{}, fmt.Errorf("dispatch: read %s: %w", candidate.run, err)
		}
		if runProvider(content) != activeProvider {
			continue
		}
		if record, found := lastRateRecord(content); found && (latest.Run == "" || record.Time.After(latest.Record.Time)) {
			latest = rateEvent{Record: record, Run: candidate.run}
		}
	}
	return latest, nil
}

// runProvider is the provider base URL one run's event log recorded, from its
// harness_provider_applied record; empty for a run that recorded none, which
// launched on the executor's own provider.
func runProvider(content []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64<<10), maxEventLine)
	marker := []byte(session.EventTypeProviderApplied)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, marker) {
			continue
		}
		var record struct {
			EventType string `json:"event_type"`
			BaseURL   string `json:"provider_base_url"`
		}
		if json.Unmarshal(line, &record) != nil || record.EventType != session.EventTypeProviderApplied {
			continue
		}
		return record.BaseURL
	}
	return ""
}

// lastRateRecord is the last rate_limit_event of one event log.
func lastRateRecord(content []byte) (rateRecord, bool) {
	var last rateRecord
	found := false
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64<<10), maxEventLine)
	marker := []byte(rateLimitEventType)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, marker) {
			continue
		}
		var record rateRecord
		if json.Unmarshal(line, &record) != nil || record.EventType != rateLimitEventType || record.Time.IsZero() {
			continue
		}
		last, found = record, true
	}
	return last, found
}

// maxEventLine bounds one event-log line, as the mining loop reads them.
const maxEventLine = 8 << 20
