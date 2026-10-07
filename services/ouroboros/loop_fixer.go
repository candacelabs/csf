// Copyright 2026 Candace Labs

package ouroboros

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdfs "io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/harness/session"
)

// The fixer recipe: one agent definition, one brief per ticket, one branch
// per miner, and the tools the brief needs. The brief is the one the ad hoc
// loop handed its sessions, made a harness recipe.
const (
	// FixerAgentID names the fixer agent in every recipe and run record.
	FixerAgentID       = "ouroboros-fixer"
	fixerAgentRevision = 1
	fixerDisplayName   = "Ouroboros fixer"
	fixerRepositoryID  = "ouroboros"
	fixerBaseBranch    = "main"
	fixerBranchPrefix  = "miner/"
	fixerTitleFormat   = "MINER %s (#%d): %s"
	fixerTitleMax      = 240
	ticketURLFormat    = "https://github.com/%s/issues/%d"
	fixerInstructions  = "You are a CSF fixer finishing one ouroboros miner in the git worktree the harness created for you. " +
		"Follow the brief exactly. Commit with git; the commit gate pushes your branch and opens the draft pull request. " +
		"Never invent an instance: labels come only from instances the ticket names and the pre-check found."
	fixerBriefFormat = `Finish the ouroboros miner for ticket #%d (%s) in this checkout.

Read services/ouroboros/README.md, services/ouroboros/contract/contract.mli and the template miner
services/ouroboros/miners/_template/ first. Create services/ouroboros/miners/%s/ as a copy of the
template and make ` + "`tools/bazel.sh test //services/ouroboros/miners/%s:miner_test`" + ` pass by editing files
under that directory only:
- extract.ml: the extractor for the ticket's facts, with name = "%s", package = "services/ouroboros/miners/%s"
  and verdicts = the rule's verdict relations. One corpus item is one file fixtures/<instance>/events.jsonl;
  for an issue, pull request or file corpus store that item's JSON as lines in the same file name.
  A knee comes only from score facts and is never a written number.
- rules.dl: the ticket's rules, valid for this engine: no nested terms, thresholds as knee(K) with gt/ge,
  every head and negated variable bound first.
- fixtures/: real instances only. The pre-check found these in the corpus: %s. Copy only the lines a fact
  needs; no home directory paths, host names or user names. fixtures/labels.tsv has the template's columns:
  instance, + or -, start (UTC), flagged time or -, source. At least one positive must sort after the
  walk-forward split.
- backtest.md: exactly the block the test prints when it is stale.
- README.md: the template's shape for this miner, with the agent-drafted marker.
The ticket's proposal names miner %s and corpus %s. When the test passes, run
python3 tools/check_operator_identifiers.py, commit once with the CSF-Session and CSF-Model trailers,
and stop. Never merge.`

	// resultEventType is the turn executor's result record, which carries
	// the session's cost and usage so far.
	resultEventType = "result"
	maxEventLine    = 8 << 20
	// baselineFixerCostUSDMicros is the measured mean fixer session of
	// 2026-10-03, $79.37 over 107 sessions: what the budget reserves for a
	// running fixer until the ledger has ledgerMeanMinimum finished ones to
	// measure its own mean from.
	baselineFixerCostUSDMicros = 741_776
	ledgerMeanMinimum          = 10
	ticketLookback             = 90 * 24 * time.Hour
	stateOpen                  = "OPEN"
	stateMerged                = "MERGED"
)

var (
	// pullRequestNumberExpression reads the number from a pull request URL.
	pullRequestNumberExpression = regexp.MustCompile(`/pull/([0-9]+)$`)
	// minerNameExpression keeps a proposal's miner name a Bazel package name.
	minerNameExpression = regexp.MustCompile(`[^a-z0-9_]+`)
)

// Usage is what a fixer session cost, folded from its event log.
type Usage struct {
	CostUSDMicros int64
	InputTokens   int64
	OutputTokens  int64
	Seconds       int64
	Results       int
}

// resultRecord is the part of a result record the usage fold reads.
type resultRecord struct {
	Time      time.Time `json:"time"`
	EventType string    `json:"event_type"`
	Event     struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
		Usage        struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"event"`
}

// FoldUsage reads a session's cost from its event log. The executor reports
// total_cost_usd cumulatively within one process and from zero when the
// process is reopened, so the cost is the sum of the maximum of each
// non-decreasing run; tokens are summed over every result; seconds span the
// first record to the last.
func FoldUsage(lines []byte) Usage {
	var usage Usage
	var first, last time.Time
	var runMax, previous float64
	scanner := bufio.NewScanner(strings.NewReader(string(lines)))
	scanner.Buffer(make([]byte, 0, 64<<10), maxEventLine)
	for scanner.Scan() {
		var record resultRecord
		if json.Unmarshal(scanner.Bytes(), &record) != nil || record.Time.IsZero() {
			continue
		}
		if first.IsZero() || record.Time.Before(first) {
			first = record.Time
		}
		if record.Time.After(last) {
			last = record.Time
		}
		if record.EventType != resultEventType {
			continue
		}
		usage.Results++
		usage.InputTokens += record.Event.Usage.InputTokens
		usage.OutputTokens += record.Event.Usage.OutputTokens
		cost := record.Event.TotalCostUSD
		if cost < previous {
			usage.CostUSDMicros += int64(runMax * usdMicrosPerUSD)
			runMax = 0
		}
		runMax = max(runMax, cost)
		previous = cost
	}
	usage.CostUSDMicros += int64(runMax * usdMicrosPerUSD)
	if !first.IsZero() {
		usage.Seconds = int64(last.Sub(first) / time.Second)
	}
	return usage
}

// MinerName is the Bazel package name a proposal's miner gets.
func MinerName(proposed string) string {
	return strings.Trim(minerNameExpression.ReplaceAllString(strings.ToLower(proposed), "_"), "_")
}

// recipe is the fixer session's recipe for one ticket's proposal.
func (loop *Loop) recipe(ticket Ticket, proposal Proposal, check Precheck, repository string) *pb.AgentAssignmentRecipe {
	name := MinerName(proposal.Miner)
	found := make([]string, 0, len(check.Found))
	for _, reference := range check.Found {
		found = append(found, reference.Kind+" "+reference.Value)
	}
	title := fmt.Sprintf(fixerTitleFormat, name, ticket.Number, ticket.Title)
	if len(title) > fixerTitleMax {
		title = title[:fixerTitleMax]
	}
	return &pb.AgentAssignmentRecipe{
		AssignmentId: uuid.NewString(),
		Agent: &pb.AgentDefinition{
			Id: FixerAgentID, Revision: fixerAgentRevision, DisplayName: fixerDisplayName, Instructions: fixerInstructions,
		},
		TicketUrl:    fmt.Sprintf(ticketURLFormat, repository, ticket.Number),
		Task:         fmt.Sprintf(fixerBriefFormat, ticket.Number, ticket.Title, name, name, name, name, strings.Join(found, instanceSeparator), proposal.Miner, strings.Join(proposal.Corpus, instanceSeparator)),
		Model:        loop.model,
		RepositoryId: fixerRepositoryID,
		Workspace: &pb.AgentWorkspace{
			RepositoryPath:   loop.repository.directory,
			BaseBranch:       fixerBaseBranch,
			Branch:           fixerBranchPrefix + name,
			AllowedTools:     fixerTools,
			PullRequestTitle: title,
		},
	}
}

// fixerTools are the Claude Code tool rules a fixer may use without asking:
// the files, the miner's build and test, and read-only git and gh.
var fixerTools = []string{
	"Read", "Write", "Edit", "Glob", "Grep",
	"Bash(tools/bazel.sh test:*)", "Bash(tools/bazel.sh build:*)", "Bash(bazel-bin/services/ouroboros/miners/*)",
	"Bash(python3 tools/check_operator_identifiers.py)", "Bash(git add:*)", "Bash(git commit:*)", "Bash(git status:*)",
	"Bash(gh issue view:*)", "Bash(gh pr view:*)", "Bash(git show:*)", "Bash(git log:*)",
	"Bash(jq:*)", "Bash(ls:*)", "Bash(head:*)", "Bash(tail:*)", "Bash(wc:*)", "Bash(grep:*)", "Bash(cp:*)", "Bash(mkdir:*)",
}

// Fixers is one occurrence of the fixer trigger: reconcile the running
// fixers with the harness, then pre-check every open mining ticket whose
// latest proposal has no verdict yet and launch a fixer for each one that
// passes, one per ticket, while the day's budget and the harness's admission
// allow. A ticket whose proposal names no real instance is queued for the
// labeler at no model cost.
func (loop *Loop) Fixers(ctx context.Context) error {
	if err := loop.reconcileFixers(ctx); err != nil {
		return err
	}
	tickets, err := loop.tickets.ListMiningTickets(ctx)
	if err != nil {
		return err
	}
	repository := loop.tickets.Repository()
	var failures []error
	for _, ticket := range tickets {
		if err := ctx.Err(); err != nil {
			return err
		}
		launched, err := loop.consider(ctx, ticket, repository)
		switch {
		case errors.Is(err, ErrBudgetSpent):
			loop.logger.Info("ouroboros: fixers wait for the next day", "ticket", ticket.Number, "reason", err.Error())
			return nil
		case errors.Is(err, ErrFixersDisabled):
			continue
		case err != nil:
			failures = append(failures, fmt.Errorf("ticket #%d: %w", ticket.Number, err))
		case launched != nil && !launched.GetCheck().GetAdmitted() && !launched.GetCheck().GetReportOnly():
			return errors.Join(failures...)
		}
	}
	return errors.Join(failures...)
}

// consider pre-checks one ticket's latest proposal and launches a fixer when
// it passes. It records nothing a second time: a proposal already judged
// keeps its verdict, and a ticket with a fixer already launched gets none.
func (loop *Loop) consider(ctx context.Context, ticket Ticket, repository string) (*harnessv1.SubmitAgentSessionResponse, error) {
	comments, err := loop.tickets.TicketComments(ctx, ticket.Number)
	if err != nil {
		return nil, err
	}
	proposal, ok := LatestProposal(comments)
	if !ok {
		return nil, nil
	}
	record, judged, err := loop.store.Proposal(ctx, ticket.Number, proposal.ID)
	if err != nil {
		return nil, err
	}
	if judged && record.Verdict == VerdictNeedsLabels {
		return nil, nil
	}
	check := Precheck{Verdict: record.Verdict, Reason: record.Reason}
	if !judged {
		check, err = loop.prechecker.Check(ctx, ticket.Number, proposal)
		if err != nil {
			return nil, err
		}
		if err := loop.store.RecordProposal(ctx, ProposalRecord{
			Ticket: ticket.Number, ID: proposal.ID, Miner: proposal.Miner, Corpus: strings.Join(proposal.Corpus, instanceSeparator),
			Instances: proposal.Instances, Verdict: check.Verdict, Reason: check.Reason, CheckedAt: loop.clock.Now().UTC(),
		}); err != nil {
			return nil, err
		}
		loop.logger.Info("ouroboros: pre-check", "ticket", ticket.Number, "proposal", proposal.ID, "verdict", check.Verdict, "reason", check.Reason)
	}
	if check.Verdict != VerdictLaunch {
		return nil, nil
	}
	fixers, err := loop.store.FixersForTicket(ctx, ticket.Number)
	if err != nil {
		return nil, err
	}
	if len(fixers) > 0 {
		return nil, nil
	}
	if !loop.launch {
		return nil, ErrFixersDisabled
	}
	if err := loop.budgetAllows(ctx); err != nil {
		return nil, err
	}
	recipe := loop.recipe(ticket, proposal, check, repository)
	response, err := loop.sessions.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: recipe})
	if err != nil {
		return nil, fmt.Errorf("launch a fixer: %w", err)
	}
	fixer := Fixer{
		AssignmentID: recipe.GetAssignmentId(), Ticket: ticket.Number, ProposalID: proposal.ID, Miner: MinerName(proposal.Miner),
		Model: loop.model, StartedAt: loop.clock.Now().UTC(), Outcome: OutcomeRunning,
	}
	if err := loop.store.RecordFixer(ctx, fixer); err != nil {
		return response, err
	}
	loop.logger.Info("ouroboros: fixer launched", "ticket", ticket.Number, "assignment", fixer.AssignmentID, "miner", fixer.Miner,
		"admitted", response.GetCheck().GetAdmitted(), "worker_cap", response.GetCheck().GetWorkerCap(), "running", response.GetCheck().GetRunningSessions())
	return response, nil
}

// DailyBudget is the day's fixer budget as the ledger reads it now: the cap,
// what the day's fixers spent, what the running ones are expected to add, and
// that expectation per session with the number of finished sessions it was
// measured from (zero when it is the measured baseline).
type DailyBudget struct {
	Day                      string
	CapUSDMicros             int64
	SpentUSDMicros           int64
	ReservedUSDMicros        int64
	ExpectedSessionUSDMicros int64
	MeasuredSessions         int64
}

// LeftUSDMicros is the cap less the spend and the reservation, never below
// zero.
func (budget DailyBudget) LeftUSDMicros() int64 {
	return max(0, budget.CapUSDMicros-budget.SpentUSDMicros-budget.ReservedUSDMicros)
}

// DailyBudget reads the day's budget from the ledger. The expectation per
// running fixer is the ledger's own mean once it has ledgerMeanMinimum
// finished fixers, the measured baseline before.
func (loop *Loop) DailyBudget(ctx context.Context) (DailyBudget, error) {
	now := loop.clock.Now()
	today, err := loop.store.FixersStartedSince(ctx, loop.dayStart(now))
	if err != nil {
		return DailyBudget{}, err
	}
	history, err := loop.store.FixersStartedSince(ctx, now.Add(-ticketLookback))
	if err != nil {
		return DailyBudget{}, err
	}
	budget := DailyBudget{Day: loop.day(now), CapUSDMicros: loop.budget}
	budget.ExpectedSessionUSDMicros, budget.MeasuredSessions = expectedFixerCost(history)
	for _, fixer := range today {
		budget.SpentUSDMicros += fixer.CostUSDMicros
		if !fixer.Outcome.Finished() {
			budget.ReservedUSDMicros += budget.ExpectedSessionUSDMicros
		}
	}
	return budget, nil
}

// budgetAllows refuses a launch once the day's spend, plus what the running
// fixers are expected to add, reaches the cap.
func (loop *Loop) budgetAllows(ctx context.Context) error {
	budget, err := loop.DailyBudget(ctx)
	if err != nil {
		return err
	}
	if budget.LeftUSDMicros() == 0 {
		return fmt.Errorf("%w: spent %d and reserved %d of %d microdollars today", ErrBudgetSpent, budget.SpentUSDMicros, budget.ReservedUSDMicros, budget.CapUSDMicros)
	}
	return nil
}

// expectedFixerCost is the mean cost of the finished fixers in history and
// how many it was measured over when there are enough to measure, the
// baseline and zero otherwise.
func expectedFixerCost(history []Fixer) (int64, int64) {
	var total, count int64
	for _, fixer := range history {
		if fixer.Outcome.Finished() {
			total += fixer.CostUSDMicros
			count++
		}
	}
	if count < ledgerMeanMinimum {
		return baselineFixerCostUSDMicros, 0
	}
	return total / count, count
}

// reconcileFixers brings every running fixer's ledger row up to date from
// the harness and its event log: cost while it runs, and its outcome once
// its one turn is done, when the loop closes the session.
func (loop *Loop) reconcileFixers(ctx context.Context) error {
	running, err := loop.store.RunningFixers(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, fixer := range running {
		if err := loop.reconcileFixer(ctx, fixer); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (loop *Loop) reconcileFixer(ctx context.Context, fixer Fixer) error {
	usage := FoldUsage(loop.eventLog(fixer.AssignmentID))
	fixer.CostUSDMicros, fixer.InputTokens, fixer.OutputTokens, fixer.Seconds = usage.CostUSDMicros, usage.InputTokens, usage.OutputTokens, usage.Seconds
	state, err := loop.sessions.Get(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: fixer.AssignmentID})
	if err != nil {
		return fmt.Errorf("fixer %s: %w", fixer.AssignmentID, err)
	}
	current := state.GetSession()
	fixer.PullRequestURL = current.GetPullRequestUrl()
	switch phase := current.GetPhase(); {
	case phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED:
		fixer.Outcome = OutcomeFailed
	case phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED, phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CLOSED:
		fixer.Outcome = loop.pullRequestOutcome(ctx, fixer.PullRequestURL, OutcomeCanceled)
	case phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN && current.GetTurns() >= 1 && current.GetQueued() == 0:
		// The one turn is done and the executor idles for a message that never
		// comes: close it and read the outcome from the pull request.
		if _, err := loop.sessions.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: fixer.AssignmentID}); err != nil {
			return fmt.Errorf("close fixer %s: %w", fixer.AssignmentID, err)
		}
		fixer.Outcome = loop.pullRequestOutcome(ctx, fixer.PullRequestURL, OutcomeNoPullRequest)
	}
	if fixer.Outcome.Finished() {
		fixer.FinishedAt = loop.clock.Now().UTC()
		loop.logger.Info("ouroboros: fixer finished", "assignment", fixer.AssignmentID, "ticket", fixer.Ticket, "outcome", fixer.Outcome,
			"cost_usd_micros", fixer.CostUSDMicros, "pull_request", fixer.PullRequestURL)
	}
	return loop.store.UpdateFixer(ctx, fixer)
}

// pullRequestOutcome reads a finished fixer's outcome from its pull
// request: ready, draft, or none when it opened none.
func (loop *Loop) pullRequestOutcome(ctx context.Context, url string, none Outcome) Outcome {
	match := pullRequestNumberExpression.FindStringSubmatch(url)
	if match == nil {
		return none
	}
	number, err := strconv.ParseInt(match[1], referenceNumberBase, 64)
	if err != nil {
		return none
	}
	pull, err := loop.tickets.PullRequest(ctx, number)
	if err != nil {
		loop.logger.Warn("ouroboros: pull request not read", "url", url, "error", err)
		return OutcomeDraft
	}
	if pull.Draft {
		return OutcomeDraft
	}
	return OutcomeReady
}

// eventLog reads a fixer run's event log from the state directory; a run
// with no log yet reads as empty.
func (loop *Loop) eventLog(assignment string) []byte {
	content, err := stdfs.ReadFile(loop.state.files, path.Join(assignment, session.EventsFile))
	if err != nil {
		return nil
	}
	return content
}
