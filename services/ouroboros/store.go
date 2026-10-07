// Copyright 2026 Candace Labs

package ouroboros

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/candacelabs/csf/io/ipc/db/csfpg"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=store.go -destination=mocks/mock_store.go -package=mocks

// ErrDatabaseRequired reports a ledger built without the database capability.
var ErrDatabaseRequired = errors.New("ouroboros: a database is required")

// Verdict is the pre-check's answer on one proposal.
type Verdict string

// The verdicts, as the ledger stores them.
const (
	// VerdictLaunch: the ticket names an instance that exists in the corpus,
	// so a fixer may be launched.
	VerdictLaunch Verdict = "launch"
	// VerdictNeedsLabels: no named instance exists, so no model is paid and
	// the proposal waits in the labeler's queue.
	VerdictNeedsLabels Verdict = "needs_labels"
)

// Outcome is where a fixer session stands.
type Outcome string

// The outcomes, as the ledger stores them.
const (
	OutcomeRunning       Outcome = "running"
	OutcomeReady         Outcome = "ready"
	OutcomeDraft         Outcome = "draft"
	OutcomeNoPullRequest Outcome = "no_pull_request"
	OutcomeFailed        Outcome = "failed"
	OutcomeCanceled      Outcome = "canceled"
)

// Finished reports whether the outcome is terminal.
func (outcome Outcome) Finished() bool { return outcome != OutcomeRunning }

// Item is one corpus file a miner has read, at the size it read it.
type Item struct {
	Miner    string
	Item     string
	ByteSize int64
	MinedAt  time.Time
}

// Finding is one verdict row a miner derived over one item: the subject's
// instance, the rule, the severity and the miner's own record whole.
type Finding struct {
	Miner    string
	Rule     string
	Subject  string
	Item     string
	Severity string
	Scope    string
	Record   json.RawMessage
	FoundAt  time.Time
}

// ProposalRecord is the pre-check's verdict on one mining proposal of one
// ticket. A verdict of needs_labels is the labeler's queue entry.
type ProposalRecord struct {
	Ticket    int64
	ID        string
	Miner     string
	Corpus    string
	Instances []string
	Verdict   Verdict
	Reason    string
	CheckedAt time.Time
}

// Fixer is one fixer session the loop launched, with what it cost.
type Fixer struct {
	AssignmentID   string
	Ticket         int64
	ProposalID     string
	Miner          string
	Model          string
	StartedAt      time.Time
	FinishedAt     time.Time
	Outcome        Outcome
	PullRequestURL string
	CostUSDMicros  int64
	InputTokens    int64
	OutputTokens   int64
	Seconds        int64
}

// Merge is one run of the merge path over one pull request head.
type Merge struct {
	PullRequest    int64
	HeadSHA        string
	AuthorSessions []string
	Merger         string
	Merged         bool
	ExitCode       int
	Reason         string
	RecordedAt     time.Time
}

// Point is one day of one series: a value with its interval, or no value
// when the day had no exposure, and the counts it was computed from.
type Point struct {
	Series      string
	Day         string
	Value       *float64
	Low         *float64
	High        *float64
	Numerator   int64
	Denominator int64
	ComputedAt  time.Time
}

// IStore is the ledger: every proposal, pre-check verdict, fixer session,
// merge result and series point the loop records, in the out-of-process
// store the binary grants.
type IStore interface {
	Item(ctx context.Context, miner string, item string) (Item, bool, error)
	RecordItem(ctx context.Context, item Item) error
	ItemsMinedSince(ctx context.Context, since time.Time) ([]Item, error)
	RecordFindings(ctx context.Context, findings []Finding) (int64, error)
	FindingsSince(ctx context.Context, since time.Time) ([]Finding, error)
	RecentFindings(ctx context.Context, limit int) ([]Finding, error)
	Proposal(ctx context.Context, ticket int64, proposal string) (ProposalRecord, bool, error)
	RecordProposal(ctx context.Context, record ProposalRecord) error
	ProposalsNeedingLabels(ctx context.Context) ([]ProposalRecord, error)
	RecordFixer(ctx context.Context, fixer Fixer) error
	UpdateFixer(ctx context.Context, fixer Fixer) error
	RunningFixers(ctx context.Context) ([]Fixer, error)
	FixersStartedSince(ctx context.Context, since time.Time) ([]Fixer, error)
	FixersForTicket(ctx context.Context, ticket int64) ([]Fixer, error)
	RecentFixers(ctx context.Context, limit int) ([]Fixer, error)
	Merge(ctx context.Context, pullRequest int64, headSHA string) (Merge, bool, error)
	RecordMerge(ctx context.Context, merge Merge) error
	RecentMerges(ctx context.Context, limit int) ([]Merge, error)
	RecordSeries(ctx context.Context, point Point) error
	Series(ctx context.Context, series string) ([]Point, error)
}

// authorSessionSeparator joins a pull request's author sessions in one
// column; scopeUnspecified is the scope of a finding from a miner built
// before scopes existed.
const (
	authorSessionSeparator = ","
	scopeUnspecified       = "SCOPE_UNSPECIFIED"
)

func scopeOrUnspecified(scope string) string {
	if scope == "" {
		return scopeUnspecified
	}
	return scope
}

// Ledger is the IStore over CSF's PostgreSQL schema, reached through the
// csfpg capability: the csf_ouroboros_* tables and the queries csfpg
// generates for them. It borrows the capability and never closes it.
type Ledger struct {
	queries *csfpg.Queries
}

var _ IStore = (*Ledger)(nil)

// NewLedger returns the ledger over a pool the binary opened through
// ipc/db/csfpg, or over pgmem's IDB in a spec.
func NewLedger(database csfpg.IDB) (*Ledger, error) {
	if database == nil {
		return nil, ErrDatabaseRequired
	}
	return &Ledger{queries: csfpg.New(database)}, nil
}

// Item reads where one miner stands on one corpus item.
func (ledger *Ledger) Item(ctx context.Context, miner string, item string) (Item, bool, error) {
	row, err := ledger.queries.GetOuroborosItem(ctx, csfpg.GetOuroborosItemParams{Miner: miner, Item: item})
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, err
	}
	return Item{Miner: row.Miner, Item: row.Item, ByteSize: row.ByteSize, MinedAt: csfpg.Time(row.MinedAt)}, true, nil
}

// RecordItem records that a miner read an item at a size.
func (ledger *Ledger) RecordItem(ctx context.Context, item Item) error {
	return ledger.queries.UpsertOuroborosItem(ctx, csfpg.UpsertOuroborosItemParams{
		Miner: item.Miner, Item: item.Item, ByteSize: item.ByteSize, MinedAt: csfpg.Timestamp(item.MinedAt),
	})
}

// ItemsMinedSince lists every item a miner last read at or after since,
// oldest first.
func (ledger *Ledger) ItemsMinedSince(ctx context.Context, since time.Time) ([]Item, error) {
	rows, err := ledger.queries.ListOuroborosItemsSince(ctx, csfpg.Timestamp(sinceOrEpoch(since)))
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, Item{Miner: row.Miner, Item: row.Item, ByteSize: row.ByteSize, MinedAt: csfpg.Time(row.MinedAt)})
	}
	return items, nil
}

// RecordFindings records the findings not yet recorded and reports how many
// were new.
func (ledger *Ledger) RecordFindings(ctx context.Context, findings []Finding) (int64, error) {
	var inserted int64
	for _, finding := range findings {
		rows, err := ledger.queries.InsertOuroborosFinding(ctx, csfpg.InsertOuroborosFindingParams{
			Miner: finding.Miner, Rule: finding.Rule, Subject: finding.Subject, Item: finding.Item,
			Severity: finding.Severity, Scope: scopeOrUnspecified(finding.Scope), Finding: finding.Record, FoundAt: csfpg.Timestamp(finding.FoundAt),
		})
		if err != nil {
			return inserted, err
		}
		inserted += rows
	}
	return inserted, nil
}

// sinceOrEpoch is the instant a "since" bound reads from: a zero since
// means everything, and csfpg stores a zero instant as NULL, which no row
// is at or after.
func sinceOrEpoch(since time.Time) time.Time {
	if since.IsZero() {
		return time.Unix(0, 0).UTC()
	}
	return since
}

// FindingsSince lists the findings recorded at or after since, oldest
// first; a zero since lists every finding.
func (ledger *Ledger) FindingsSince(ctx context.Context, since time.Time) ([]Finding, error) {
	rows, err := ledger.queries.ListOuroborosFindingsSince(ctx, csfpg.Timestamp(sinceOrEpoch(since)))
	if err != nil {
		return nil, err
	}
	return findingRecords(rows), nil
}

// RecentFindings lists the newest findings, newest first.
func (ledger *Ledger) RecentFindings(ctx context.Context, limit int) ([]Finding, error) {
	rows, err := ledger.queries.ListOuroborosFindings(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	return findingRecords(rows), nil
}

func findingRecords(rows []csfpg.CsfOuroborosFinding) []Finding {
	findings := make([]Finding, len(rows))
	for index, row := range rows {
		findings[index] = Finding{
			Miner: row.Miner, Rule: row.Rule, Subject: row.Subject, Item: row.Item, Severity: row.Severity, Scope: row.Scope,
			Record: json.RawMessage(row.Finding), FoundAt: csfpg.Time(row.FoundAt),
		}
	}
	return findings
}

// Proposal reads the verdict recorded on one proposal of one ticket.
func (ledger *Ledger) Proposal(ctx context.Context, ticket int64, proposal string) (ProposalRecord, bool, error) {
	row, err := ledger.queries.GetOuroborosProposal(ctx, csfpg.GetOuroborosProposalParams{Ticket: ticket, Proposal: proposal})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProposalRecord{}, false, nil
	}
	if err != nil {
		return ProposalRecord{}, false, err
	}
	record, err := proposalRecord(row)
	return record, err == nil, err
}

// RecordProposal records or replaces the verdict on one proposal.
func (ledger *Ledger) RecordProposal(ctx context.Context, record ProposalRecord) error {
	instances, err := json.Marshal(record.Instances)
	if err != nil {
		return err
	}
	_, err = ledger.queries.UpsertOuroborosProposal(ctx, csfpg.UpsertOuroborosProposalParams{
		Ticket: record.Ticket, Proposal: record.ID, Miner: record.Miner, Corpus: record.Corpus, Instances: instances,
		Verdict: string(record.Verdict), Reason: record.Reason, CheckedAt: csfpg.Timestamp(record.CheckedAt),
	})
	return err
}

// ProposalsNeedingLabels is the labeler's queue, oldest first.
func (ledger *Ledger) ProposalsNeedingLabels(ctx context.Context) ([]ProposalRecord, error) {
	rows, err := ledger.queries.ListOuroborosProposalsNeedingLabels(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]ProposalRecord, 0, len(rows))
	for _, row := range rows {
		record, err := proposalRecord(row)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func proposalRecord(row csfpg.CsfOuroborosProposal) (ProposalRecord, error) {
	var instances []string
	if err := json.Unmarshal(row.Instances, &instances); err != nil {
		return ProposalRecord{}, fmt.Errorf("ouroboros: decode the instances of #%d: %w", row.Ticket, err)
	}
	return ProposalRecord{
		Ticket: row.Ticket, ID: row.Proposal, Miner: row.Miner, Corpus: row.Corpus, Instances: instances,
		Verdict: Verdict(row.Verdict), Reason: row.Reason, CheckedAt: csfpg.Time(row.CheckedAt),
	}, nil
}

// RecordFixer records a fixer session the loop launched, as running.
func (ledger *Ledger) RecordFixer(ctx context.Context, fixer Fixer) error {
	_, err := ledger.queries.InsertOuroborosFixer(ctx, csfpg.InsertOuroborosFixerParams{
		AssignmentID: fixer.AssignmentID, Ticket: fixer.Ticket, Proposal: fixer.ProposalID, Miner: fixer.Miner,
		Model: fixer.Model, StartedAt: csfpg.Timestamp(fixer.StartedAt),
	})
	return err
}

// UpdateFixer records what a fixer session cost and where it stands.
func (ledger *Ledger) UpdateFixer(ctx context.Context, fixer Fixer) error {
	updated, err := ledger.queries.UpdateOuroborosFixer(ctx, csfpg.UpdateOuroborosFixerParams{
		FinishedAt: csfpg.Timestamp(fixer.FinishedAt), Outcome: string(fixer.Outcome), PullRequestUrl: fixer.PullRequestURL,
		CostUsdMicros: fixer.CostUSDMicros, InputTokens: fixer.InputTokens, OutputTokens: fixer.OutputTokens,
		Seconds: fixer.Seconds, AssignmentID: fixer.AssignmentID,
	})
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("ouroboros: fixer %s is not in the ledger", fixer.AssignmentID)
	}
	return nil
}

// RunningFixers lists the fixers not yet finished, oldest first.
func (ledger *Ledger) RunningFixers(ctx context.Context) ([]Fixer, error) {
	rows, err := ledger.queries.ListOuroborosFixersRunning(ctx)
	if err != nil {
		return nil, err
	}
	return fixerRecords(rows), nil
}

// FixersStartedSince lists the fixers started at or after since, oldest
// first; a zero since lists every fixer.
func (ledger *Ledger) FixersStartedSince(ctx context.Context, since time.Time) ([]Fixer, error) {
	rows, err := ledger.queries.ListOuroborosFixersStartedSince(ctx, csfpg.Timestamp(sinceOrEpoch(since)))
	if err != nil {
		return nil, err
	}
	return fixerRecords(rows), nil
}

// FixersForTicket lists every fixer launched for one ticket, oldest first.
func (ledger *Ledger) FixersForTicket(ctx context.Context, ticket int64) ([]Fixer, error) {
	rows, err := ledger.queries.ListOuroborosFixersForTicket(ctx, ticket)
	if err != nil {
		return nil, err
	}
	return fixerRecords(rows), nil
}

// RecentFixers lists the newest fixers, newest first.
func (ledger *Ledger) RecentFixers(ctx context.Context, limit int) ([]Fixer, error) {
	rows, err := ledger.queries.ListOuroborosFixers(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	return fixerRecords(rows), nil
}

func fixerRecords(rows []csfpg.CsfOuroborosFixer) []Fixer {
	fixers := make([]Fixer, len(rows))
	for index, row := range rows {
		fixers[index] = Fixer{
			AssignmentID: row.AssignmentID, Ticket: row.Ticket, ProposalID: row.Proposal, Miner: row.Miner, Model: row.Model,
			StartedAt: csfpg.Time(row.StartedAt), FinishedAt: csfpg.Time(row.FinishedAt), Outcome: Outcome(row.Outcome),
			PullRequestURL: row.PullRequestUrl, CostUSDMicros: row.CostUsdMicros, InputTokens: row.InputTokens,
			OutputTokens: row.OutputTokens, Seconds: row.Seconds,
		}
	}
	return fixers
}

// Merge reads the merge path's result on one pull request head.
func (ledger *Ledger) Merge(ctx context.Context, pullRequest int64, headSHA string) (Merge, bool, error) {
	row, err := ledger.queries.GetOuroborosMerge(ctx, csfpg.GetOuroborosMergeParams{PullRequest: pullRequest, HeadSha: headSHA})
	if errors.Is(err, pgx.ErrNoRows) {
		return Merge{}, false, nil
	}
	if err != nil {
		return Merge{}, false, err
	}
	return mergeRecord(row), true, nil
}

// RecordMerge records one run of the merge path.
func (ledger *Ledger) RecordMerge(ctx context.Context, merge Merge) error {
	_, err := ledger.queries.InsertOuroborosMerge(ctx, csfpg.InsertOuroborosMergeParams{
		PullRequest: merge.PullRequest, HeadSha: merge.HeadSHA, AuthorSessions: strings.Join(merge.AuthorSessions, authorSessionSeparator),
		Merger: merge.Merger, Merged: merge.Merged, ExitCode: int32(merge.ExitCode), Reason: merge.Reason,
		RecordedAt: csfpg.Timestamp(merge.RecordedAt),
	})
	return err
}

// RecentMerges lists the newest merge results, newest first.
func (ledger *Ledger) RecentMerges(ctx context.Context, limit int) ([]Merge, error) {
	rows, err := ledger.queries.ListOuroborosMerges(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	merges := make([]Merge, len(rows))
	for index, row := range rows {
		merges[index] = mergeRecord(row)
	}
	return merges, nil
}

func mergeRecord(row csfpg.CsfOuroborosMerge) Merge {
	var sessions []string
	if row.AuthorSessions != "" {
		sessions = strings.Split(row.AuthorSessions, authorSessionSeparator)
	}
	return Merge{
		PullRequest: row.PullRequest, HeadSHA: row.HeadSha, AuthorSessions: sessions, Merger: row.Merger, Merged: row.Merged,
		ExitCode: int(row.ExitCode), Reason: row.Reason, RecordedAt: csfpg.Time(row.RecordedAt),
	}
}

// RecordSeries records or replaces one day of one series.
func (ledger *Ledger) RecordSeries(ctx context.Context, point Point) error {
	return ledger.queries.UpsertOuroborosSeries(ctx, csfpg.UpsertOuroborosSeriesParams{
		Series: point.Series, Day: point.Day, Value: point.Value, Low: point.Low, High: point.High,
		Numerator: point.Numerator, Denominator: point.Denominator, ComputedAt: csfpg.Timestamp(point.ComputedAt),
	})
}

// Series lists one series, oldest day first.
func (ledger *Ledger) Series(ctx context.Context, series string) ([]Point, error) {
	rows, err := ledger.queries.ListOuroborosSeries(ctx, series)
	if err != nil {
		return nil, err
	}
	points := make([]Point, len(rows))
	for index, row := range rows {
		points[index] = Point{
			Series: row.Series, Day: row.Day, Value: row.Value, Low: row.Low, High: row.High,
			Numerator: row.Numerator, Denominator: row.Denominator, ComputedAt: csfpg.Time(row.ComputedAt),
		}
	}
	return points, nil
}
