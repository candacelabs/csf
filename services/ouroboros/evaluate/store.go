// Copyright 2026 Candace Labs

package evaluate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/candacelabs/csf/io/ipc/db/csfpg"
)

var (
	// ErrDatabaseRequired reports a store built without the database.
	ErrDatabaseRequired = errors.New("evaluate: the csfpg database is required")
	// ErrNoSuite reports a read of the suite before any was selected.
	ErrNoSuite = errors.New("evaluate: no suite has been selected; run csf eval suite")
)

// Run is one scoring run of a build on a suite version: its wall clock.
type Run struct {
	Build        string
	SuiteVersion int
	StartedAt    time.Time
	FinishedAt   time.Time
}

// SuiteStore keeps the suites, replays and scoring runs in CSF's PostgreSQL
// schema (the csf_eval_* tables), the one place results from every node are
// aggregated. It borrows the capability and never closes it.
type SuiteStore struct {
	queries *csfpg.Queries
}

// NewSuiteStore returns the store over a pool the binary opened through
// ipc/db/csfpg, or over pgmem's IDB in a spec.
func NewSuiteStore(database csfpg.IDB) (*SuiteStore, error) {
	if database == nil {
		return nil, ErrDatabaseRequired
	}
	return &SuiteStore{queries: csfpg.New(database)}, nil
}

// RecordSuite records a new suite version.
func (store *SuiteStore) RecordSuite(ctx context.Context, suite Suite) error {
	encoded, err := json.Marshal(suite)
	if err != nil {
		return err
	}
	return store.queries.InsertEvalSuite(ctx, csfpg.InsertEvalSuiteParams{
		Version: int32(suite.Version), SelectedAt: csfpg.Timestamp(suite.SelectedAt), Suite: encoded,
	})
}

// LatestSuite reads the current suite version.
func (store *SuiteStore) LatestSuite(ctx context.Context) (Suite, error) {
	row, err := store.queries.GetLatestEvalSuite(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return Suite{}, ErrNoSuite
	}
	if err != nil {
		return Suite{}, err
	}
	return decodeSuite(row)
}

// Suites reads every suite version, oldest first.
func (store *SuiteStore) Suites(ctx context.Context) ([]Suite, error) {
	rows, err := store.queries.ListEvalSuites(ctx)
	if err != nil {
		return nil, err
	}
	suites := make([]Suite, 0, len(rows))
	for _, row := range rows {
		suite, err := decodeSuite(row)
		if err != nil {
			return nil, err
		}
		suites = append(suites, suite)
	}
	return suites, nil
}

func decodeSuite(row csfpg.CsfEvalSuite) (Suite, error) {
	var suite Suite
	if err := json.Unmarshal(row.Suite, &suite); err != nil {
		return Suite{}, fmt.Errorf("evaluate: decode suite v%d: %w", row.Version, err)
	}
	return suite, nil
}

// RecordReplay records one replay, replacing an earlier reading of the same
// build, version and ticket.
func (store *SuiteStore) RecordReplay(ctx context.Context, replay Replay) error {
	return store.queries.UpsertEvalReplay(ctx, csfpg.UpsertEvalReplayParams{
		Build: replay.Build, SuiteVersion: int32(replay.SuiteVersion), Ticket: replay.Ticket, Node: replay.Node,
		AssignmentID: replay.Assignment, ToolCalls: replay.ToolCalls, Episodes: replay.Episodes, Recall: replay.Recall,
		CostUsdMicros: replay.CostUSDMicros, Seconds: replay.Seconds, RecordedAt: csfpg.Timestamp(replay.RecordedAt),
	})
}

// Replays reads a build's replays on a suite version, by ticket.
func (store *SuiteStore) Replays(ctx context.Context, build string, version int) ([]Replay, error) {
	rows, err := store.queries.ListEvalReplays(ctx, csfpg.ListEvalReplaysParams{Build: build, SuiteVersion: int32(version)})
	if err != nil {
		return nil, err
	}
	replays := make([]Replay, len(rows))
	for index, row := range rows {
		replays[index] = Replay{
			Build: row.Build, SuiteVersion: int(row.SuiteVersion), Ticket: row.Ticket, Node: row.Node, Assignment: row.AssignmentID,
			ToolCalls: row.ToolCalls, Episodes: row.Episodes, Recall: row.Recall, CostUSDMicros: row.CostUsdMicros,
			Seconds: row.Seconds, RecordedAt: csfpg.Time(row.RecordedAt),
		}
	}
	return replays, nil
}

// RecordRun records a scoring run's wall clock.
func (store *SuiteStore) RecordRun(ctx context.Context, run Run) error {
	return store.queries.UpsertEvalScore(ctx, csfpg.UpsertEvalScoreParams{
		Build: run.Build, SuiteVersion: int32(run.SuiteVersion),
		StartedAt: csfpg.Timestamp(run.StartedAt), FinishedAt: csfpg.Timestamp(run.FinishedAt),
	})
}

// Runs reads every scoring run, oldest first.
func (store *SuiteStore) Runs(ctx context.Context) ([]Run, error) {
	rows, err := store.queries.ListEvalScores(ctx)
	if err != nil {
		return nil, err
	}
	runs := make([]Run, len(rows))
	for index, row := range rows {
		runs[index] = Run{Build: row.Build, SuiteVersion: int(row.SuiteVersion), StartedAt: csfpg.Time(row.StartedAt), FinishedAt: csfpg.Time(row.FinishedAt)}
	}
	return runs, nil
}

// ScoreOn is a build's score on suite from the records, with its scoring
// run's wall clock when one was recorded.
func (store *SuiteStore) ScoreOn(ctx context.Context, build string, suite Suite) (Score, error) {
	replays, err := store.Replays(ctx, build, suite.Version)
	if err != nil {
		return Score{}, err
	}
	runs, err := store.Runs(ctx)
	if err != nil {
		return Score{}, err
	}
	var wall time.Duration
	for _, run := range runs {
		if run.Build == build && run.SuiteVersion == suite.Version {
			wall = run.FinishedAt.Sub(run.StartedAt)
		}
	}
	return ScoreOf(build, suite, replays, wall), nil
}

// suiteVersion reads one recorded suite version.
func (store *SuiteStore) suiteVersion(ctx context.Context, version int) (Suite, error) {
	suites, err := store.Suites(ctx)
	if err != nil {
		return Suite{}, err
	}
	for _, suite := range suites {
		if suite.Version == version {
			return suite, nil
		}
	}
	return Suite{}, fmt.Errorf("%w: no suite v%d", ErrNoSuite, version)
}

// Recompute gives the citation line the records hold for build on a suite
// version against build vs: the Recompute the proof check runs.
func (store *SuiteStore) Recompute(ctx context.Context, build string, version int, versus string) (string, error) {
	suite, err := store.suiteVersion(ctx, version)
	if err != nil {
		return "", err
	}
	score, err := store.ScoreOn(ctx, build, suite)
	if err != nil {
		return "", err
	}
	if !score.Complete {
		return "", fmt.Errorf("%w: build %s, suite v%d (%d of %d replays)", ErrUnscored, build, version, score.Replays, score.Expected)
	}
	against, err := store.ScoreOn(ctx, versus, suite)
	if err != nil {
		return "", err
	}
	if !against.Complete {
		return "", fmt.Errorf("%w: build %s, suite v%d (%d of %d replays)", ErrUnscored, versus, version, against.Replays, against.Expected)
	}
	return Citation(score, against)
}

// HiddenTickets is every ticket any suite version has held: once held out,
// a ticket stays hidden from the miners and the loop's corpus readers.
func (store *SuiteStore) HiddenTickets(ctx context.Context) (map[int64]bool, error) {
	suites, err := store.Suites(ctx)
	if err != nil {
		return nil, err
	}
	hidden := map[int64]bool{}
	for _, suite := range suites {
		for _, ticket := range suite.Tickets {
			hidden[ticket.Number] = true
		}
	}
	return hidden, nil
}
