// Package postgres provides the SQLC-backed durable cron Store.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/candacelabs/csf/pkg/cron"
)

// Store persists cron state in PostgreSQL. Callers own the database lifecycle.
type Store struct{ db *sql.DB }

var _ cron.IStore = (*Store)(nil)

// NewStore binds the durable cron adapter to a caller-owned database pool.
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("cron postgres: nil database")
	}
	return &Store{db: db}, nil
}

func (s *Store) Reconcile(ctx context.Context, defs []cron.JobDefinition, now time.Time) ([]cron.JobState, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if now.IsZero() {
		return nil, fmt.Errorf("%w: reconciliation time is required", cron.ErrInvalidConfiguration)
	}
	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q := New(tx)
	if err := q.LockReconciliation(ctx); err != nil {
		return nil, err
	}
	rows, err := q.ListJobsForUpdate(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]CandaceCronJob, len(rows))
	for _, row := range rows {
		existing[row.JobName] = row
	}
	seen := make(map[string]bool, len(defs))
	for _, in := range defs {
		if seen[in.Name] {
			return nil, fmt.Errorf("%w: duplicate job %q", cron.ErrInvalidConfiguration, in.Name)
		}
		seen[in.Name] = true
		var old cron.JobDefinition
		var had bool
		var wasActive bool
		if row, ok := existing[in.Name]; ok {
			old, err = definition(row)
			if err != nil {
				return nil, err
			}
			had = true
			wasActive = row.Enabled
			if wasActive {
				in = preserveAnchor(in, old)
			}
		}
		in, schedule, err := normalize(in, now)
		if err != nil {
			return nil, err
		}
		scheduleChanged := !wasActive || !reflect.DeepEqual(in.Schedule, old.Schedule)
		if wasActive && !scheduleChanged && reflect.DeepEqual(in, old) {
			continue
		}
		next := now
		if wasActive && !scheduleChanged && existing[in.Name].NextRunAt.Valid {
			next = existing[in.Name].NextRunAt.Time.UTC()
		} else {
			next, err = schedule.Next(now)
			if err != nil {
				return nil, err
			}
		}
		oldRow := existing[in.Name]
		if had && (!wasActive || scheduleChanged) {
			oldRow.ScheduleCursorAt = sql.NullTime{}
		}
		_, err = q.UpsertJob(ctx, jobParams(in, next, oldRow))
		if err != nil {
			return nil, err
		}
	}
	for name, row := range existing {
		if row.Enabled && !seen[name] {
			if _, err := q.SetJobEnabled(ctx, SetJobEnabledParams{JobName: name, Enabled: false}); err != nil {
				return nil, err
			}
		}
	}
	finalRows, err := q.ListJobsForUpdate(ctx)
	if err != nil {
		return nil, err
	}
	states := make([]cron.JobState, 0, len(finalRows))
	for _, row := range finalRows {
		if !row.Enabled {
			continue
		}
		if !row.NextRunAt.Valid {
			return nil, fmt.Errorf("%w: missing next run", cron.ErrInvalidConfiguration)
		}
		definition, err := definition(row)
		if err != nil {
			return nil, err
		}
		states = append(states, cron.JobState{Definition: definition, NextRunAt: row.NextRunAt.Time.UTC(), CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC()})
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Definition.Name < states[j].Definition.Name })
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return states, nil
}

func (s *Store) Claim(ctx context.Context, r cron.ClaimRequest) (cron.ClaimResult, error) {
	if err := contextError(ctx); err != nil {
		return cron.ClaimResult{}, err
	}
	if err := validClaim(r); err != nil {
		return cron.ClaimResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return cron.ClaimResult{}, err
	}
	defer tx.Rollback()
	q := New(tx)
	job, err := q.GetJobForUpdate(ctx, r.JobName)
	if errors.Is(err, sql.ErrNoRows) {
		return cron.ClaimResult{}, fmt.Errorf("%w: %q", cron.ErrJobNotFound, r.JobName)
	}
	if err != nil {
		return cron.ClaimResult{}, err
	}
	if !job.Enabled {
		return cron.ClaimResult{}, fmt.Errorf("%w: %q", cron.ErrJobNotFound, r.JobName)
	}
	persisted, err := definition(job)
	if err != nil {
		return cron.ClaimResult{}, err
	}
	schedule, err := cron.ScheduleFromDefinition(persisted.Schedule)
	if err != nil {
		return cron.ClaimResult{}, err
	}
	expectedNext, err := schedule.Next(r.ScheduledAt)
	if err != nil || !expectedNext.Equal(r.NextRunAt) {
		return cron.ClaimResult{}, cron.ErrOccurrenceConflict
	}
	advance := func() error {
		n, e := q.AdvanceJobNextRun(ctx, AdvanceJobNextRunParams{JobName: r.JobName, NextRunAt: nullTime(r.NextRunAt), ScheduleCursorAt: nullTime(r.ScheduledAt), UpdatedAt: r.ClaimedAt.UTC()})
		if e != nil {
			return e
		}
		if n != 1 {
			return fmt.Errorf("%w: %q", cron.ErrJobNotFound, r.JobName)
		}
		return nil
	}
	row, e := q.GetRunForUpdate(ctx, r.OccurrenceID)
	if e == nil {
		if row.JobName != r.JobName || !row.ScheduledAt.Equal(r.ScheduledAt) {
			return cron.ClaimResult{}, cron.ErrOccurrenceConflict
		}
		rec := occurrence(row)
		if rec.Status != cron.OccurrenceRunning {
			if err := tx.Commit(); err != nil {
				return cron.ClaimResult{}, err
			}
			return cron.ClaimResult{Disposition: cron.ClaimAlreadyTerminal, Occurrence: rec}, nil
		}
		if rec.LeaseUntil.After(r.ClaimedAt) {
			if err := tx.Commit(); err != nil {
				return cron.ClaimResult{}, err
			}
			return cron.ClaimResult{Disposition: cron.ClaimLeaseHeld, Occurrence: rec}, nil
		}
		if job.OverlapPolicy == string(cron.OverlapSkip) {
			live, e := q.HasLiveRunForJob(ctx, HasLiveRunForJobParams{JobName: r.JobName, ClaimedAt: nullTime(r.ClaimedAt), ExcludedOccurrenceID: r.OccurrenceID})
			if e != nil {
				return cron.ClaimResult{}, e
			}
			if live {
				if err := tx.Commit(); err != nil {
					return cron.ClaimResult{}, err
				}
				return cron.ClaimResult{Disposition: cron.ClaimLeaseHeld, Occurrence: rec}, nil
			}
		}
		if err := advance(); err != nil {
			return cron.ClaimResult{}, err
		}
		row, err = q.AcquireExpiredRun(ctx, AcquireExpiredRunParams{OccurrenceID: r.OccurrenceID, WorkerID: nullString(r.LeaseOwner), LeaseToken: nullString(r.LeaseToken), ClaimedAt: nullTime(r.ClaimedAt), LeaseUntil: nullTime(r.LeaseUntil)})
		if err != nil {
			return cron.ClaimResult{}, err
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return cron.ClaimResult{}, e
	} else {
		if !job.NextRunAt.Valid || !job.NextRunAt.Time.Equal(r.ScheduledAt) {
			return cron.ClaimResult{}, cron.ErrOccurrenceConflict
		}
		live, e := q.HasLiveRunForJob(ctx, HasLiveRunForJobParams{JobName: r.JobName, ClaimedAt: nullTime(r.ClaimedAt), ExcludedOccurrenceID: r.OccurrenceID})
		if e != nil {
			return cron.ClaimResult{}, e
		}
		if job.OverlapPolicy == string(cron.OverlapSkip) && live {
			row, err = q.InsertSkippedRun(ctx, InsertSkippedRunParams{OccurrenceID: r.OccurrenceID, JobName: r.JobName, ScheduledAt: r.ScheduledAt.UTC(), SkippedAt: nullTime(r.ClaimedAt), SkipReason: nullString("overlap")})
			if err != nil {
				return cron.ClaimResult{}, err
			}
			if err := advance(); err != nil {
				return cron.ClaimResult{}, err
			}
			if err := tx.Commit(); err != nil {
				return cron.ClaimResult{}, err
			}
			return cron.ClaimResult{Disposition: cron.ClaimSkippedOverlap, Occurrence: occurrence(row)}, nil
		}
		row, err = q.InsertRunningRun(ctx, InsertRunningRunParams{OccurrenceID: r.OccurrenceID, JobName: r.JobName, ScheduledAt: r.ScheduledAt.UTC(), ClaimedAt: nullTime(r.ClaimedAt), WorkerID: nullString(r.LeaseOwner), LeaseToken: nullString(r.LeaseToken), LeaseUntil: nullTime(r.LeaseUntil)})
		if err != nil {
			return cron.ClaimResult{}, err
		}
		if err := advance(); err != nil {
			return cron.ClaimResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return cron.ClaimResult{}, err
	}
	return cron.ClaimResult{Disposition: cron.ClaimAcquired, Occurrence: occurrence(row)}, nil
}

func (s *Store) Renew(ctx context.Context, r cron.LeaseRenewal) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if r.OccurrenceID == "" || r.LeaseToken == "" || len(r.LeaseToken) > 256 || r.RenewedAt.IsZero() ||
		r.LeaseUntil.IsZero() || !durablyAfter(r.LeaseUntil, r.RenewedAt) {
		return fmt.Errorf("%w: invalid lease renewal", cron.ErrInvalidConfiguration)
	}
	n, e := New(s.db).RenewRunLease(ctx, RenewRunLeaseParams{OccurrenceID: r.OccurrenceID, LeaseToken: nullString(r.LeaseToken), RenewedAt: r.RenewedAt.UTC(), LeaseUntil: nullTime(r.LeaseUntil)})
	if e != nil {
		return e
	}
	if n != 1 {
		return cron.ErrLeaseLost
	}
	return nil
}
func (s *Store) Complete(ctx context.Context, r cron.Completion) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if r.OccurrenceID == "" || r.LeaseToken == "" || len(r.LeaseToken) > 256 || r.FinishedAt.IsZero() ||
		(r.Status != cron.OccurrenceSucceeded && r.Status != cron.OccurrenceFailed && r.Status != cron.OccurrenceCanceled) ||
		len(r.Error) > 4096 || (r.Status == cron.OccurrenceSucceeded && r.Error != "") {
		return fmt.Errorf("%w: invalid completion", cron.ErrInvalidConfiguration)
	}
	n, e := New(s.db).FinishRun(ctx, FinishRunParams{OccurrenceID: r.OccurrenceID, LeaseToken: nullString(r.LeaseToken), Status: string(r.Status), FinishedAt: nullTime(r.FinishedAt), ErrorSummary: nullString(r.Error)})
	if e != nil {
		return e
	}
	if n != 1 {
		return cron.ErrLeaseLost
	}
	return nil
}

// Expired returns a bounded, deterministic recovery work list without moving
// any cursor. Claim fences and reclaims the selected occurrence.
func (s *Store) Expired(ctx context.Context, now time.Time, limit int) ([]cron.OccurrenceRecord, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if now.IsZero() || limit <= 0 || limit > 1<<31-1 {
		return nil, fmt.Errorf("%w: invalid expired occurrence query", cron.ErrInvalidConfiguration)
	}
	rows, err := New(s.db).ListExpiredRunningRuns(ctx, ListExpiredRunningRunsParams{ExpiredAt: nullTime(now), RowLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]cron.OccurrenceRecord, len(rows))
	for i, row := range rows {
		out[i] = occurrence(row)
	}
	return out, nil
}
func (s *Store) Skip(ctx context.Context, r cron.SkipRequest) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if r.OccurrenceID == "" || r.JobName == "" || r.ScheduledAt.IsZero() || r.NextRunAt.IsZero() || r.SkippedAt.IsZero() || len(r.Reason) == 0 || len(r.Reason) > 4096 || !r.NextRunAt.After(r.ScheduledAt) || r.OccurrenceID != cron.OccurrenceID(r.JobName, r.ScheduledAt) {
		return fmt.Errorf("%w: invalid occurrence skip", cron.ErrInvalidConfiguration)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := New(tx)
	job, err := q.GetJobForUpdate(ctx, r.JobName)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %q", cron.ErrJobNotFound, r.JobName)
	} else if err != nil {
		return err
	}
	if !job.Enabled {
		return fmt.Errorf("%w: %q", cron.ErrJobNotFound, r.JobName)
	}
	row, runErr := q.GetRunForUpdate(ctx, r.OccurrenceID)
	if runErr == nil {
		if row.JobName != r.JobName || !row.ScheduledAt.Equal(r.ScheduledAt) {
			return cron.ErrOccurrenceConflict
		}
		if row.Status != string(cron.OccurrenceRunning) {
			return tx.Commit()
		}
	} else if !errors.Is(runErr, sql.ErrNoRows) {
		return runErr
	}
	persisted, err := definition(job)
	if err != nil {
		return err
	}
	schedule, err := cron.ScheduleFromDefinition(persisted.Schedule)
	if err != nil {
		return err
	}
	expectedNext, err := schedule.Next(r.ScheduledAt)
	if err != nil || !expectedNext.Equal(r.NextRunAt) {
		return cron.ErrOccurrenceConflict
	}
	if runErr == nil {
		if row.LeaseUntil.Valid && row.LeaseUntil.Time.After(r.SkippedAt) {
			return cron.ErrOccurrenceRunning
		}
		if _, err := q.MarkExpiredRunSkipped(ctx, MarkExpiredRunSkippedParams{OccurrenceID: r.OccurrenceID, SkippedAt: nullTime(r.SkippedAt), SkipReason: sql.NullString{String: r.Reason, Valid: true}}); err != nil {
			return err
		}
	} else {
		if !job.NextRunAt.Valid || !job.NextRunAt.Time.Equal(r.ScheduledAt) {
			return cron.ErrOccurrenceConflict
		}
		expected, e := schedule.Next(r.ScheduledAt)
		if e != nil || !expected.Equal(r.NextRunAt) {
			return cron.ErrOccurrenceConflict
		}
		if _, err := q.InsertSkippedRun(ctx, InsertSkippedRunParams{OccurrenceID: r.OccurrenceID, JobName: r.JobName, ScheduledAt: r.ScheduledAt.UTC(), SkippedAt: nullTime(r.SkippedAt), SkipReason: sql.NullString{String: r.Reason, Valid: true}}); err != nil {
			return err
		}
	}
	if _, err := q.AdvanceJobNextRun(ctx, AdvanceJobNextRunParams{JobName: r.JobName, NextRunAt: nullTime(r.NextRunAt), ScheduleCursorAt: nullTime(r.ScheduledAt), UpdatedAt: r.SkippedAt.UTC()}); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Snapshot(ctx context.Context) (cron.StoreSnapshot, error) {
	if err := contextError(ctx); err != nil {
		return cron.StoreSnapshot{}, err
	}
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		return cron.StoreSnapshot{}, e
	}
	defer tx.Rollback()
	q := New(tx)
	jobs, e := q.ListJobs(ctx)
	if e != nil {
		return cron.StoreSnapshot{}, e
	}
	states := make([]cron.JobState, 0, len(jobs))
	for _, job := range jobs {
		if !job.Enabled {
			continue
		}
		if !job.NextRunAt.Valid {
			return cron.StoreSnapshot{}, fmt.Errorf("%w: missing next run", cron.ErrInvalidConfiguration)
		}
		d, e := definition(job)
		if e != nil {
			return cron.StoreSnapshot{}, e
		}
		states = append(states, cron.JobState{Definition: d, NextRunAt: job.NextRunAt.Time.UTC(), CreatedAt: job.CreatedAt.UTC(), UpdatedAt: job.UpdatedAt.UTC()})
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Definition.Name < states[j].Definition.Name })
	rows, e := q.ListRecentRuns(ctx, int32(cron.SnapshotOccurrenceLimit))
	if e != nil {
		return cron.StoreSnapshot{}, e
	}
	out := make([]cron.OccurrenceRecord, len(rows))
	for i, row := range rows {
		out[i] = occurrence(row)
	}
	if e := tx.Commit(); e != nil {
		return cron.StoreSnapshot{}, e
	}
	return cron.StoreSnapshot{Jobs: states, Occurrences: out}, nil
}

func normalize(d cron.JobDefinition, now time.Time) (cron.JobDefinition, cron.Schedule, error) {
	if d.Name == "" {
		return d, cron.Schedule{}, fmt.Errorf("%w: job name", cron.ErrInvalidConfiguration)
	}
	if (d.CatchUp != cron.CatchUpNone && d.CatchUp != cron.CatchUpLatest && d.CatchUp != cron.CatchUpAll) ||
		(d.Overlap != cron.OverlapSkip && d.Overlap != cron.OverlapAllow) {
		return d, cron.Schedule{}, fmt.Errorf("%w: job %q has invalid policies", cron.ErrInvalidConfiguration, d.Name)
	}
	if d.Schedule.HasAnchor {
		d.Schedule.Anchor = d.Schedule.Anchor.UTC().Truncate(time.Microsecond)
	}
	s, e := cron.ScheduleFromDefinition(d.Schedule)
	if e != nil {
		return d, s, e
	}
	if d.Schedule.Kind == cron.ScheduleKindEvery && !d.Schedule.HasAnchor {
		s = s.Anchor(now.UTC().Truncate(time.Microsecond))
	}
	d.Schedule, e = s.Definition()
	if e != nil {
		return d, s, e
	}
	return d, s, nil
}
func preserveAnchor(in, old cron.JobDefinition) cron.JobDefinition {
	if in.Schedule.Kind == cron.ScheduleKindEvery && !in.Schedule.HasAnchor && old.Schedule.Kind == cron.ScheduleKindEvery && old.Schedule.HasAnchor && in.Schedule.Interval == old.Schedule.Interval && in.Schedule.Timezone == old.Schedule.Timezone {
		in.Schedule.Anchor = old.Schedule.Anchor
		in.Schedule.HasAnchor = true
	}
	return in
}
func jobParams(d cron.JobDefinition, next time.Time, old CandaceCronJob) UpsertJobParams {
	p := UpsertJobParams{JobName: d.Name, ScheduleKind: string(d.Schedule.Kind), Timezone: d.Schedule.Timezone, CatchUpPolicy: string(d.CatchUp), OverlapPolicy: string(d.Overlap), Enabled: true, NextRunAt: nullTime(next)}
	switch d.Schedule.Kind {
	case cron.ScheduleKindRaw:
		p.RawExpression = nullString(d.Schedule.Canonical)
	case cron.ScheduleKindDaily, cron.ScheduleKindWeekly, cron.ScheduleKindMonthly, cron.ScheduleKindLastDayOfMonth:
		p.LocalHour = nullInt16(d.Schedule.Hour)
		p.LocalMinute = nullInt16(d.Schedule.Minute)
	}
	if d.Schedule.Kind == cron.ScheduleKindWeekly {
		p.Weekday = nullInt16(int(d.Schedule.Weekday))
	}
	if d.Schedule.Kind == cron.ScheduleKindMonthly {
		p.MonthDay = nullInt16(d.Schedule.MonthDay)
	}
	if d.Schedule.Kind == cron.ScheduleKindEvery {
		p.IntervalNanoseconds = sql.NullInt64{Int64: int64(d.Schedule.Interval), Valid: true}
		p.IntervalAnchorAt = nullTime(d.Schedule.Anchor)
	}
	if old.ScheduleCursorAt.Valid {
		p.ScheduleCursorAt = old.ScheduleCursorAt
	}
	return p
}
func definition(r CandaceCronJob) (cron.JobDefinition, error) {
	d := cron.JobDefinition{Name: r.JobName, CatchUp: cron.CatchUpPolicy(r.CatchUpPolicy), Overlap: cron.OverlapPolicy(r.OverlapPolicy), Schedule: cron.ScheduleDefinition{Kind: cron.ScheduleKind(r.ScheduleKind), Timezone: r.Timezone}}
	if r.LocalHour.Valid {
		d.Schedule.Hour = int(r.LocalHour.Int16)
		d.Schedule.Minute = int(r.LocalMinute.Int16)
	}
	if r.Weekday.Valid {
		d.Schedule.Weekday = time.Weekday(r.Weekday.Int16)
	}
	if r.MonthDay.Valid {
		d.Schedule.MonthDay = int(r.MonthDay.Int16)
	}
	if r.IntervalNanoseconds.Valid {
		d.Schedule.Interval = time.Duration(r.IntervalNanoseconds.Int64)
		d.Schedule.Anchor = r.IntervalAnchorAt.Time
		d.Schedule.HasAnchor = r.IntervalAnchorAt.Valid
	}
	if r.RawExpression.Valid {
		d.Schedule.Canonical = r.RawExpression.String
	}
	s, e := cron.ScheduleFromDefinition(d.Schedule)
	if e != nil {
		return d, e
	}
	d.Schedule, e = s.Definition()
	return d, e
}
func occurrence(r CandaceCronRun) cron.OccurrenceRecord {
	return cron.OccurrenceRecord{ID: r.OccurrenceID, JobName: r.JobName, ScheduledAt: r.ScheduledAt.UTC(), Status: cron.OccurrenceStatus(r.Status), Attempt: uint32(r.Attempt), StartedAt: valueTime(r.StartedAt), FinishedAt: valueTime(r.FinishedAt), LeaseOwner: valueString(r.WorkerID), LeaseToken: valueString(r.LeaseToken), LeaseUntil: valueTime(r.LeaseUntil), Error: valueString(r.ErrorSummary), SkipReason: valueString(r.SkipReason), LastModified: r.UpdatedAt.UTC()}
}
func validClaim(r cron.ClaimRequest) error {
	if r.OccurrenceID == "" || r.JobName == "" || r.LeaseOwner == "" || r.LeaseToken == "" ||
		len(r.LeaseOwner) > 128 || len(r.LeaseToken) > 256 || r.ScheduledAt.IsZero() ||
		r.NextRunAt.IsZero() || r.ClaimedAt.IsZero() || r.LeaseUntil.IsZero() ||
		!r.NextRunAt.After(r.ScheduledAt) || !durablyAfter(r.LeaseUntil, r.ClaimedAt) {
		return fmt.Errorf("%w: invalid occurrence claim", cron.ErrInvalidConfiguration)
	}
	if r.OccurrenceID != cron.OccurrenceID(r.JobName, r.ScheduledAt) {
		return cron.ErrOccurrenceConflict
	}
	return nil
}
func nullTime(v time.Time) sql.NullTime {
	return sql.NullTime{Time: v.UTC().Truncate(time.Microsecond), Valid: !v.IsZero()}
}
func durablyAfter(later, earlier time.Time) bool {
	return later.UTC().Truncate(time.Microsecond).After(earlier.UTC().Truncate(time.Microsecond))
}
func nullString(v string) sql.NullString { return sql.NullString{String: v, Valid: v != ""} }
func nullInt16(v int) sql.NullInt16      { return sql.NullInt16{Int16: int16(v), Valid: true} }
func valueTime(v sql.NullTime) time.Time {
	if v.Valid {
		return v.Time.UTC()
	}
	return time.Time{}
}
func valueString(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", cron.ErrInvalidConfiguration)
	}
	return ctx.Err()
}
