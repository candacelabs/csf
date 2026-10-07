// Copyright 2026 Candace Labs

// Package costs is the cost model of the harness's session operations. It
// reads the recorded runs under a state directory, derives one cost event per
// operation a virtual session's life on real sessions cost, measures the
// model's inputs from them, and replays every decision's policies over them
// with a deterministic simulator. Nothing here changes a running session: the
// chosen parameters are records for the hypervisor to read.
package costs

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"slices"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/harness/routing"
	"github.com/candacelabs/csf/services/harness/session"
)

// The session hypervisor's suspend and resume records are the harness
// session package's; older records carry only the session close and the
// resume a restart writes.
const (
	maxRecordBytes = 8 << 20
	// cacheLifetimeOneHour and cacheLifetimeFiveMinutes are the two prompt
	// cache lifetimes the endpoint reports writes under.
	cacheLifetimeOneHour     = time.Hour
	cacheLifetimeFiveMinutes = 5 * time.Minute
)

// Tokens are one call's or one turn's tokens by how they were charged.
type Tokens struct {
	Uncached int64
	Write    int64
	Read     int64
	Output   int64
}

func (tokens Tokens) add(other Tokens) Tokens {
	return Tokens{tokens.Uncached + other.Uncached, tokens.Write + other.Write, tokens.Read + other.Read, tokens.Output + other.Output}
}

// Input is every input token the call submitted.
func (tokens Tokens) Input() int64 { return tokens.Uncached + tokens.Write + tokens.Read }

// HitRatio is the share of submitted input read from the cache.
func (tokens Tokens) HitRatio() float64 {
	if tokens.Input() == 0 {
		return 0
	}
	return float64(tokens.Read) / float64(tokens.Input())
}

func (tokens Tokens) proto() *harnessv1.TokenCounts {
	return &harnessv1.TokenCounts{UncachedInput: tokens.Uncached, CacheWrite: tokens.Write, CacheRead: tokens.Read, Output: tokens.Output}
}

// Turn is one recorded turn: what the record says, before any operation is
// assigned to it.
type Turn struct {
	Virtual string
	Agent   string
	Real    string
	Model   string
	// Start is the turn's request, or its first model call when no request
	// was recorded; End is its result.
	Start time.Time
	End   time.Time
	// Tokens and USD are the turn's, summed over every model it called.
	Tokens Tokens
	USD    float64
	// ByModel is the turn's tokens and money per model: the observations
	// the prices are fitted to.
	ByModel map[string]Priced
	// USDKnown is false for records with no per-model usage.
	USDKnown  bool
	TTFT      time.Duration
	TTFTKnown bool
	// First and Last are the turn's first and last top-level model calls.
	First Tokens
	Last  Tokens
	// Reopened: the turn executor was closed and opened again before it.
	Reopened bool
}

// Priced is one model's tokens in one turn and what they cost.
type Priced struct {
	Tokens Tokens
	USD    float64
}

// Close is one turn executor closed between turns.
type Close struct {
	Virtual string
	Agent   string
	At      time.Time
}

// Run is one recorded virtual session.
type Run struct {
	Virtual string
	Agent   string
	// Kind is the task kind: the first segment of the run's branch.
	Kind  string
	Model string
	Turns []Turn
	// Closes are its turn executors closed between turns.
	Closes []Close
	// PullRequest: the run finished with a pull request.
	PullRequest bool
	// Attached are the real sessions the router attached it to.
	Attached map[string]bool
	// Lifetime1h and Lifetime5m are the cache writes by the lifetime the
	// endpoint reports them under.
	Lifetime1h int64
	Lifetime5m int64
	// Open is how long the virtual session was open: from each start or
	// resume to the next close or the last record.
	Open []Span
	// Worktree, Title and Ticket are the run record's: where its repository
	// paths are rooted, and what the router matches it by.
	Worktree string
	Title    string
	Ticket   string
	// Area is what the run read, edited and was asked.
	Area RunArea
}

// Span is one interval of time.
type Span struct {
	From time.Time
	To   time.Time
}

// ReadRuns reads every run under the state directory, in directory order,
// and the router's attach decisions. A run with no turn is kept: its open
// time still counts.
func ReadRuns(state fs.FS) ([]Run, error) {
	attached, err := readAttached(state)
	if err != nil {
		return nil, err
	}
	paths, err := fs.Glob(state, path.Join("*", session.EventsFile))
	if err != nil {
		return nil, err
	}
	runs := make([]Run, 0, len(paths))
	for _, events := range paths {
		directory := path.Dir(events)
		run := Run{Virtual: directory, Attached: attached[directory]}
		if content, err := fs.ReadFile(state, path.Join(directory, session.RunStateFile)); err == nil {
			var recorded session.RunState
			if json.Unmarshal(content, &recorded) == nil {
				run.Agent, run.Model, run.Kind = recorded.AgentID, recorded.Model, branchKind(recorded.Branch)
				run.Worktree, run.Title, run.Ticket = recorded.Worktree, recorded.PullRequestTitle, recorded.TicketURL
			}
		}
		file, err := state.Open(events)
		if err != nil {
			return nil, err
		}
		err = parseRun(&run, file)
		err = errors.Join(err, file.Close())
		if err != nil {
			return nil, fmt.Errorf("harness costs: %s: %w", events, err)
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// branchKind is a branch's first path segment: dev, miner, widget, fix.
func branchKind(branch string) string {
	for index, character := range branch {
		if character == '/' {
			return branch[:index]
		}
	}
	return branch
}

// readAttached is, per virtual session, the real sessions the router
// attached it to. No decision log is no attaches.
func readAttached(state fs.FS) (map[string]map[string]bool, error) {
	attached := map[string]map[string]bool{}
	file, err := state.Open(path.Join(routing.Directory, routing.DecisionsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return attached, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var decision routing.Decision
		if json.Unmarshal(scanner.Bytes(), &decision) != nil || decision.Arm != routing.ArmAttach {
			continue
		}
		if attached[decision.Virtual] == nil {
			attached[decision.Virtual] = map[string]bool{}
		}
		attached[decision.Virtual][decision.Real] = true
	}
	return attached, scanner.Err()
}

// record is the part of one events.jsonl line the cost model reads.
type record struct {
	Time      time.Time `json:"time"`
	EventType string    `json:"event_type"`
	// Direction is "in" on a message sent to the session.
	Direction string `json:"direction"`
	// PullRequestURL is on the run's finish.
	PullRequestURL string `json:"pull_request_url"`
	Event          struct {
		SessionID       string           `json:"session_id"`
		ParentToolUseID *string          `json:"parent_tool_use_id"`
		TTFT            *int64           `json:"ttft_ms"`
		Usage           *usage           `json:"usage"`
		ModelUsage      map[string]total `json:"modelUsage"`
		// ToolUseResult is present on a tool's result.
		ToolUseResult json.RawMessage `json:"tool_use_result"`
		Message       struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage *usage `json:"usage"`
			// Content is a string or a list of blocks.
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"event"`
}

// usage is the endpoint's usage block of one call or one turn.
type usage struct {
	Input    int64 `json:"input_tokens"`
	Creation int64 `json:"cache_creation_input_tokens"`
	Read     int64 `json:"cache_read_input_tokens"`
	Output   int64 `json:"output_tokens"`
	Lifetime struct {
		OneHour     int64 `json:"ephemeral_1h_input_tokens"`
		FiveMinutes int64 `json:"ephemeral_5m_input_tokens"`
	} `json:"cache_creation"`
}

func (usage usage) tokens() Tokens {
	return Tokens{Uncached: usage.Input, Write: usage.Creation, Read: usage.Read, Output: usage.Output}
}

// total is one model's usage since its turn executor process started.
type total struct {
	Input    int64   `json:"inputTokens"`
	Output   int64   `json:"outputTokens"`
	Read     int64   `json:"cacheReadInputTokens"`
	Creation int64   `json:"cacheCreationInputTokens"`
	USD      float64 `json:"costUSD"`
}

func (later total) minus(earlier total) total {
	return total{later.Input - earlier.Input, later.Output - earlier.Output, later.Read - earlier.Read, later.Creation - earlier.Creation, later.USD - earlier.USD}
}

func (share total) negative() bool {
	return share.Input < 0 || share.Output < 0 || share.Read < 0 || share.Creation < 0 || share.USD < 0
}

// parseRun folds one run's records into its turns, closes and open spans.
// The per-model totals a result carries are cumulative over the turn
// executor process, so a turn's own share is the difference from the
// previous result of the same process; a start, resume or reopen begins a
// new process.
func parseRun(run *Run, reader io.Reader) error {
	cumulative := map[string]total{}
	var current *Turn
	var requested, openedAt, lastSeen time.Time
	closed, reopened := false, false
	area := newAreaReader()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1<<16), maxRecordBytes)
	for scanner.Scan() {
		var line record
		// Records are slog lines of several shapes; one that does not decode
		// carries nothing this model reads.
		if json.Unmarshal(scanner.Bytes(), &line) != nil || line.Time.IsZero() {
			continue
		}
		lastSeen = line.Time
		area.observe(run, line)
		switch line.EventType {
		case session.EventTypeRunStarted:
			cumulative, openedAt = map[string]total{}, line.Time
		case session.EventTypeRunResumed, session.EventTypeSessionResumed:
			cumulative = map[string]total{}
			if openedAt.IsZero() {
				openedAt = line.Time
			}
			reopened, closed = reopened || closed, false
		case session.EventTypeSessionClosed, session.EventTypeSessionSuspended:
			if !closed {
				run.Closes = append(run.Closes, Close{Virtual: run.Virtual, Agent: run.Agent, At: line.Time})
			}
			if !openedAt.IsZero() {
				run.Open = append(run.Open, Span{From: openedAt, To: line.Time})
				openedAt = time.Time{}
			}
			closed = true
		case session.EventTypeTurnRequested:
			requested = line.Time
		case session.EventTypeRunFinished:
			run.PullRequest = run.PullRequest || line.PullRequestURL != ""
		case session.EventTypeAssistant:
			if line.Event.ParentToolUseID != nil || line.Event.Message.Usage == nil {
				continue
			}
			if current == nil {
				current = &Turn{Start: line.Time, Model: line.Event.Message.Model, First: line.Event.Message.Usage.tokens()}
			}
			current.Last = line.Event.Message.Usage.tokens()
		case session.EventTypeResult:
			var turn Turn
			turn, cumulative = run.finishTurn(line, current, requested, reopened, cumulative)
			current, requested, reopened = nil, time.Time{}, false
			if openedAt.IsZero() {
				openedAt = turn.Start
			}
		}
	}
	if !openedAt.IsZero() && lastSeen.After(openedAt) {
		run.Open = append(run.Open, Span{From: openedAt, To: lastSeen})
	}
	return scanner.Err()
}

// finishTurn records the turn a result ends: its calls so far, its request
// time, its share of the process's per-model totals and the cache writes'
// lifetimes. It returns the turn and the totals the next result is measured
// against.
func (run *Run) finishTurn(line record, current *Turn, requested time.Time, reopened bool, cumulative map[string]total) (Turn, map[string]total) {
	turn := Turn{}
	if current != nil {
		turn = *current
	}
	if !requested.IsZero() {
		turn.Start = requested
	}
	if turn.Start.IsZero() {
		turn.Start = line.Time
	}
	turn.Virtual, turn.Agent, turn.Real, turn.End, turn.Reopened = run.Virtual, run.Agent, line.Event.SessionID, line.Time, reopened
	if line.Event.TTFT != nil {
		turn.TTFT, turn.TTFTKnown = time.Duration(*line.Event.TTFT)*time.Millisecond, true
	}
	cumulative = turnShare(&turn, line, cumulative)
	if line.Event.Usage != nil {
		run.Lifetime1h += line.Event.Usage.Lifetime.OneHour
		run.Lifetime5m += line.Event.Usage.Lifetime.FiveMinutes
	}
	if turn.Model == "" {
		turn.Model = run.Model
	}
	run.Turns = append(run.Turns, turn)
	return turn, cumulative
}

// turnShare sets the turn's tokens and money from the per-model totals, and
// returns the totals the next result is measured against. A record with no
// per-model totals has the turn's own usage and no money.
func turnShare(turn *Turn, line record, cumulative map[string]total) map[string]total {
	if len(line.Event.ModelUsage) == 0 {
		if line.Event.Usage != nil {
			turn.Tokens = line.Event.Usage.tokens()
		}
		return cumulative
	}
	shares := map[string]total{}
	restarted := false
	for model, now := range line.Event.ModelUsage {
		shares[model] = now.minus(cumulative[model])
		restarted = restarted || shares[model].negative()
	}
	// Totals that fell mean a process began with no record of it: the
	// totals are then the turn's own.
	if restarted {
		shares = line.Event.ModelUsage
	}
	turn.ByModel, turn.USDKnown = map[string]Priced{}, true
	var costliest float64 = -1
	for _, model := range slices.Sorted(maps.Keys(shares)) {
		share := shares[model]
		tokens := Tokens{Uncached: share.Input, Write: share.Creation, Read: share.Read, Output: share.Output}
		turn.ByModel[model] = Priced{Tokens: tokens, USD: share.USD}
		turn.Tokens, turn.USD = turn.Tokens.add(tokens), turn.USD+share.USD
		if share.USD > costliest {
			costliest, turn.Model = share.USD, model
		}
	}
	return line.Event.ModelUsage
}

// CacheLifetime is the prompt cache lifetime the runs wrote under: the one
// hour lifetime when most cache writes report it, else five minutes. It is
// exact: the endpoint names the lifetime of every write.
func CacheLifetime(runs []Run) (time.Duration, int64) {
	var hour, minutes int64
	for _, run := range runs {
		hour, minutes = hour+run.Lifetime1h, minutes+run.Lifetime5m
	}
	if hour >= minutes {
		return cacheLifetimeOneHour, hour + minutes
	}
	return cacheLifetimeFiveMinutes, hour + minutes
}

// Derive assigns every recorded turn and close its operations: one turn
// event per turn, warm or cold; a bind, rebind, model flip or reopen before
// the turn it made rebuild its prefix; an idle close per close. Rebuild
// money is the first call's cache write at the model's fitted write price,
// so it is inferred; turn money is the record's.
func Derive(runs []Run, lifetime time.Duration, prices map[string]Price, resident int64) []*harnessv1.CostEvent {
	events := []*harnessv1.CostEvent{}
	for _, run := range runs {
		events = append(events, deriveTurns(run, lifetime, prices)...)
		for _, closed := range run.Closes {
			events = append(events, &harnessv1.CostEvent{Operation: harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_IDLE_CLOSE,
				At: timestamppb.New(closed.At), VirtualSession: run.Virtual, Agent: run.Agent, ResidentBytes: resident,
				Inferred: []harnessv1.CostField{harnessv1.CostField_COST_FIELD_RESIDENT},
				Missing:  []harnessv1.CostField{harnessv1.CostField_COST_FIELD_TTFT}})
		}
	}
	slices.SortStableFunc(events, func(left *harnessv1.CostEvent, right *harnessv1.CostEvent) int {
		return left.GetAt().AsTime().Compare(right.GetAt().AsTime())
	})
	return events
}

// deriveTurns is one run's turn events and the rebuilds before them.
func deriveTurns(run Run, lifetime time.Duration, prices map[string]Price) []*harnessv1.CostEvent {
	events := []*harnessv1.CostEvent{}
	seen := map[string]bool{}
	ends := map[string]time.Time{}
	prefixes := map[string]int64{}
	var previousModel string
	for _, turn := range run.Turns {
		base := func(operation harnessv1.HypervisorOperation) *harnessv1.CostEvent {
			event := &harnessv1.CostEvent{Operation: operation, At: timestamppb.New(turn.Start), VirtualSession: run.Virtual,
				RealSession: turn.Real, Agent: run.Agent, Model: turn.Model, PrefixTokens: prefixes[turn.Real]}
			if !ends[turn.Real].IsZero() {
				event.GapMs = turn.Start.Sub(ends[turn.Real]).Milliseconds()
			}
			if turn.TTFTKnown {
				event.TtftMs = turn.TTFT.Milliseconds()
			} else {
				event.Missing = append(event.Missing, harnessv1.CostField_COST_FIELD_TTFT)
			}
			return event
		}
		rebuild := func(operation harnessv1.HypervisorOperation) {
			event := base(operation)
			event.Tokens = Tokens{Write: turn.First.Write}.proto()
			event.Usd = float64(turn.First.Write) * prices[turn.Model].Write
			event.Inferred = append(event.Inferred, harnessv1.CostField_COST_FIELD_TOKENS, harnessv1.CostField_COST_FIELD_USD)
			events = append(events, event)
		}
		switch {
		case !seen[turn.Real] && run.Attached[turn.Real]:
			rebuild(harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_REBIND)
		case !seen[turn.Real]:
			rebuild(harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_BIND)
		case turn.Reopened:
			rebuild(harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_REOPEN)
		}
		if previousModel != "" && turn.Model != "" && turn.Model != previousModel {
			rebuild(harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_MODEL_FLIP)
		}
		operation := harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_TURN_WARM
		if !seen[turn.Real] || turn.Start.Sub(ends[turn.Real]) > lifetime {
			operation = harnessv1.HypervisorOperation_HYPERVISOR_OPERATION_TURN_COLD
		}
		event := base(operation)
		// Warm or cold is read off the gap, not the endpoint's report.
		event.Inferred = append(event.Inferred, harnessv1.CostField_COST_FIELD_OPERATION)
		event.Tokens, event.Usd = turn.Tokens.proto(), turn.USD
		if !turn.USDKnown {
			event.Usd = prices[turn.Model].Of(turn.Tokens)
			event.Inferred = append(event.Inferred, harnessv1.CostField_COST_FIELD_USD)
		}
		events = append(events, event)
		seen[turn.Real], ends[turn.Real], prefixes[turn.Real] = true, turn.End, turn.Last.Input()+turn.Last.Output
		if turn.Model != "" {
			previousModel = turn.Model
		}
	}
	return events
}
