package postgres_test

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	cron "github.com/candacelabs/csf/pkg/cron"
	cronpostgres "github.com/candacelabs/csf/pkg/cron/postgres"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const cronTestDatabaseURLEnv = "CANDACE_CRON_TEST_DATABASE_URL"

// cronUpMigration is applied only to a disposable test database that does not
// already have the cron schema. The production migration runner remains the
// owner of migrations outside this opt-in integration suite.
//
//go:embed migrations/000001_create_cron_jobs_and_runs.up.sql
var cronUpMigration string

var (
	integrationAdminDB *sql.DB
	integrationDB      *sql.DB
	integrationSchema  string
	integrationStore   *cronpostgres.Store
	nameSequence       atomic.Uint64
)

var _ = BeforeSuite(func(ctx SpecContext) {
	databaseURL := os.Getenv(cronTestDatabaseURLEnv)
	if databaseURL == "" {
		Skip("set " + cronTestDatabaseURLEnv + " to run PostgreSQL integration specs")
	}

	schemaName := fmt.Sprintf("candace_cron_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	isolatedURL, err := databaseURLWithSearchPath(databaseURL, schemaName)
	Expect(err).NotTo(HaveOccurred())

	adminDB, err := sql.Open("pgx", databaseURL)
	Expect(err).NotTo(HaveOccurred())
	Expect(adminDB.PingContext(ctx)).To(Succeed())
	_, err = adminDB.ExecContext(ctx, "CREATE SCHEMA "+pgx.Identifier{schemaName}.Sanitize())
	Expect(err).NotTo(HaveOccurred())
	integrationAdminDB = adminDB
	integrationSchema = schemaName

	db, err := sql.Open("pgx", isolatedURL)
	Expect(err).NotTo(HaveOccurred())
	Expect(db.PingContext(ctx)).To(Succeed())
	Expect(ensureCronSchema(ctx, db)).To(Succeed())

	store, err := cronpostgres.NewStore(db)
	Expect(err).NotTo(HaveOccurred())
	integrationDB = db
	integrationStore = store
})

var _ = AfterSuite(func() {
	if integrationDB != nil {
		Expect(integrationDB.Close()).To(Succeed())
	}
	if integrationAdminDB != nil && integrationSchema != "" {
		_, err := integrationAdminDB.Exec("DROP SCHEMA " + pgx.Identifier{integrationSchema}.Sanitize() + " CASCADE")
		Expect(err).NotTo(HaveOccurred())
	}
	if integrationAdminDB != nil {
		Expect(integrationAdminDB.Close()).To(Succeed())
	}
})

var _ = Describe("SQLC PostgreSQL Store", func() {
	It("rejects invalid operations before mutating relational state", func(ctx SpecContext) {
		now := instant(2026, time.August, 10, 11, 0, 0)
		_, err := cronpostgres.NewStore(nil)
		Expect(err).To(MatchError(ContainSubstring("nil database")))
		_, err = integrationStore.Reconcile(nil, nil, now)
		Expect(err).To(MatchError(ContainSubstring("nil context")))
		_, err = integrationStore.Reconcile(ctx, nil, time.Time{})
		Expect(err).To(MatchError(ContainSubstring("reconciliation time is required")))
		_, err = integrationStore.Claim(ctx, cron.ClaimRequest{})
		Expect(err).To(MatchError(ContainSubstring("invalid occurrence claim")))
		Expect(integrationStore.Renew(ctx, cron.LeaseRenewal{})).To(MatchError(ContainSubstring("invalid lease renewal")))
		Expect(integrationStore.Complete(ctx, cron.Completion{})).To(MatchError(ContainSubstring("invalid completion")))
		_, err = integrationStore.Expired(ctx, time.Time{}, 0)
		Expect(err).To(MatchError(ContainSubstring("invalid expired occurrence query")))
		Expect(integrationStore.Skip(ctx, cron.SkipRequest{})).To(MatchError(ContainSubstring("invalid occurrence skip")))

		name := jobName("duplicate")
		definition := jobDefinition(name, cron.Spec(cron.Every(time.Minute)), cron.CatchUpNone, cron.OverlapSkip)
		_, err = integrationStore.Reconcile(ctx, []cron.JobDefinition{definition, definition}, now)
		Expect(err).To(MatchError(ContainSubstring("duplicate job")))

		tx, err := integrationDB.BeginTx(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = tx.Rollback() })
		_, err = cronpostgres.New(integrationDB).WithTx(tx).ListJobs(ctx)
		Expect(err).NotTo(HaveOccurred())
	})

	It("reconciles typed schedules and preserves an established interval anchor", func(ctx SpecContext) {
		now := instant(2026, time.August, 10, 12, 0, 0)
		definition := jobDefinition(
			jobName("anchor"),
			cron.Spec(cron.Every(time.Minute)),
			cron.CatchUpAll,
			cron.OverlapSkip,
		)

		states, err := integrationStore.Reconcile(ctx, []cron.JobDefinition{definition}, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(states).To(HaveLen(1))
		Expect(states[0].Definition.Schedule.Kind).To(Equal(cron.ScheduleKindEvery))
		Expect(states[0].Definition.Schedule.HasAnchor).To(BeTrue())
		Expect(states[0].Definition.Schedule.Anchor).To(Equal(now))
		Expect(states[0].NextRunAt).To(Equal(now.Add(time.Minute)))

		states, err = integrationStore.Reconcile(ctx, []cron.JobDefinition{definition}, now.Add(30*time.Second))
		Expect(err).NotTo(HaveOccurred())
		Expect(states).To(HaveLen(1))
		Expect(states[0].Definition.Schedule.Anchor).To(Equal(now))
		Expect(states[0].NextRunAt).To(Equal(now.Add(time.Minute)))

		policyOnly := definition
		policyOnly.CatchUp = cron.CatchUpLatest
		policyOnly.Overlap = cron.OverlapAllow
		states, err = integrationStore.Reconcile(ctx, []cron.JobDefinition{policyOnly}, now.Add(40*time.Second))
		Expect(err).NotTo(HaveOccurred())
		Expect(states[0].Definition.CatchUp).To(Equal(cron.CatchUpLatest))
		Expect(states[0].Definition.Overlap).To(Equal(cron.OverlapAllow))
		Expect(states[0].Definition.Schedule.Anchor).To(Equal(now))
		Expect(states[0].NextRunAt).To(Equal(now.Add(time.Minute)))

		states, err = integrationStore.Reconcile(ctx, nil, now.Add(time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(states).To(BeEmpty())

		reenabledAt := now.Add(2 * time.Minute)
		states, err = integrationStore.Reconcile(ctx, []cron.JobDefinition{definition}, reenabledAt)
		Expect(err).NotTo(HaveOccurred())
		Expect(states).To(HaveLen(1))
		Expect(states[0].Definition.Schedule.Anchor).To(Equal(reenabledAt))
		Expect(states[0].NextRunAt).To(Equal(reenabledAt.Add(time.Minute)))
	})

	It("claims, renews, and completes with lease-token fencing", func(ctx SpecContext) {
		anchor := instant(2026, time.August, 10, 13, 0, 0)
		schedule := cron.Spec(cron.Every(time.Minute)).Anchor(anchor)
		states, err := integrationStore.Reconcile(ctx, []cron.JobDefinition{
			jobDefinition(jobName("fence"), schedule, cron.CatchUpAll, cron.OverlapSkip),
		}, anchor)
		Expect(err).NotTo(HaveOccurred())

		firstAt := states[0].NextRunAt
		nextAt := next(schedule, firstAt)
		claimedAt := firstAt.Add(time.Second)
		request := claim(states[0], firstAt, nextAt, "worker-a", "token-a", claimedAt, time.Minute)
		result, err := integrationStore.Claim(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Disposition).To(Equal(cron.ClaimAcquired))
		Expect(result.Occurrence.Attempt).To(Equal(uint32(1)))

		renewedAt := claimedAt.Add(time.Second)
		Expect(integrationStore.Renew(ctx, cron.LeaseRenewal{
			OccurrenceID: request.OccurrenceID,
			LeaseToken:   request.LeaseToken,
			RenewedAt:    renewedAt,
			LeaseUntil:   renewedAt.Add(2 * time.Minute),
		})).To(Succeed())
		Expect(integrationStore.Renew(ctx, cron.LeaseRenewal{
			OccurrenceID: request.OccurrenceID,
			LeaseToken:   "stale-token",
			RenewedAt:    renewedAt,
			LeaseUntil:   renewedAt.Add(2 * time.Minute),
		})).To(MatchError(cron.ErrLeaseLost))

		finishedAt := renewedAt.Add(time.Second)
		Expect(integrationStore.Complete(ctx, cron.Completion{
			OccurrenceID: request.OccurrenceID,
			LeaseToken:   "stale-token",
			Status:       cron.OccurrenceSucceeded,
			FinishedAt:   finishedAt,
		})).To(MatchError(cron.ErrLeaseLost))
		Expect(integrationStore.Complete(ctx, cron.Completion{
			OccurrenceID: request.OccurrenceID,
			LeaseToken:   request.LeaseToken,
			Status:       cron.OccurrenceSucceeded,
			FinishedAt:   finishedAt,
		})).To(Succeed())

		snapshot, err := integrationStore.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		occurrence := findOccurrence(snapshot, request.OccurrenceID)
		Expect(occurrence.Status).To(Equal(cron.OccurrenceSucceeded))
		Expect(occurrence.LeaseToken).To(BeEmpty())
		Expect(occurrence.LeaseUntil.IsZero()).To(BeTrue())
	})

	It("atomically records a skipped occurrence when overlap is forbidden", func(ctx SpecContext) {
		anchor := instant(2026, time.August, 10, 14, 0, 0)
		schedule := cron.Spec(cron.Every(time.Minute)).Anchor(anchor)
		states, err := integrationStore.Reconcile(ctx, []cron.JobDefinition{
			jobDefinition(jobName("overlap"), schedule, cron.CatchUpAll, cron.OverlapSkip),
		}, anchor)
		Expect(err).NotTo(HaveOccurred())

		firstAt := states[0].NextRunAt
		secondAt := next(schedule, firstAt)
		thirdAt := next(schedule, secondAt)
		claimedAt := secondAt.Add(time.Second)
		_, err = integrationStore.Claim(ctx, claim(states[0], firstAt, secondAt, "worker-a", "first-token", claimedAt, 2*time.Minute))
		Expect(err).NotTo(HaveOccurred())

		second, err := integrationStore.Claim(ctx, claim(states[0], secondAt, thirdAt, "worker-b", "second-token", claimedAt, time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Disposition).To(Equal(cron.ClaimSkippedOverlap))
		Expect(second.Occurrence.Status).To(Equal(cron.OccurrenceSkipped))
		Expect(second.Occurrence.SkipReason).To(Equal("overlap"))
	})

	It("records fresh and expired skips atomically and idempotently", func(ctx SpecContext) {
		anchor := instant(2026, time.August, 10, 14, 30, 0)
		schedule := cron.Spec(cron.Every(time.Minute)).Anchor(anchor)
		freshDefinition := jobDefinition(jobName("fresh-skip"), schedule, cron.CatchUpNone, cron.OverlapSkip)
		states, err := integrationStore.Reconcile(ctx, []cron.JobDefinition{freshDefinition}, anchor)
		Expect(err).NotTo(HaveOccurred())
		freshAt := states[0].NextRunAt
		freshNext := next(schedule, freshAt)
		fresh := cron.SkipRequest{
			OccurrenceID: cron.OccurrenceID(freshDefinition.Name, freshAt),
			JobName:      freshDefinition.Name,
			ScheduledAt:  freshAt,
			NextRunAt:    freshNext,
			SkippedAt:    freshAt.Add(time.Second),
			Reason:       "catch-up disabled",
		}

		missing := fresh
		missing.JobName = jobName("missing")
		missing.OccurrenceID = cron.OccurrenceID(missing.JobName, missing.ScheduledAt)
		Expect(integrationStore.Skip(ctx, missing)).To(MatchError(ContainSubstring("job not found")))
		conflict := fresh
		conflict.NextRunAt = freshNext.Add(time.Minute)
		Expect(integrationStore.Skip(ctx, conflict)).To(MatchError(cron.ErrOccurrenceConflict))
		Expect(integrationStore.Skip(ctx, fresh)).To(Succeed())
		Expect(integrationStore.Skip(ctx, fresh)).To(Succeed())

		expiredDefinition := jobDefinition(jobName("expired-skip"), schedule, cron.CatchUpNone, cron.OverlapSkip)
		states, err = integrationStore.Reconcile(ctx, []cron.JobDefinition{freshDefinition, expiredDefinition}, anchor)
		Expect(err).NotTo(HaveOccurred())
		expiredState := states[0]
		if expiredState.Definition.Name != expiredDefinition.Name {
			expiredState = states[1]
		}
		expiredAt := expiredState.NextRunAt
		expiredNext := next(schedule, expiredAt)
		claimedAt := expiredAt.Add(time.Second)
		claimRequest := claim(expiredState, expiredAt, expiredNext, "worker-a", "expiring-token", claimedAt, time.Minute)
		_, err = integrationStore.Claim(ctx, claimRequest)
		Expect(err).NotTo(HaveOccurred())
		expired := cron.SkipRequest{
			OccurrenceID: claimRequest.OccurrenceID,
			JobName:      claimRequest.JobName,
			ScheduledAt:  claimRequest.ScheduledAt,
			NextRunAt:    claimRequest.NextRunAt,
			SkippedAt:    claimedAt.Add(30 * time.Second),
			Reason:       "recovery disabled",
		}
		Expect(integrationStore.Skip(ctx, expired)).To(MatchError(cron.ErrOccurrenceRunning))
		expired.SkippedAt = claimRequest.LeaseUntil.Add(time.Second)
		Expect(integrationStore.Skip(ctx, expired)).To(Succeed())

		snapshot, err := integrationStore.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		freshRecord := findOccurrence(snapshot, fresh.OccurrenceID)
		Expect(freshRecord.Status).To(Equal(cron.OccurrenceSkipped))
		Expect(freshRecord.SkipReason).To(Equal(fresh.Reason))
		expiredRecord := findOccurrence(snapshot, expired.OccurrenceID)
		Expect(expiredRecord.Status).To(Equal(cron.OccurrenceSkipped))
		Expect(expiredRecord.SkipReason).To(Equal(expired.Reason))
	})

	It("keeps terminal PostgreSQL skip replays from touching an advanced cursor", func(ctx SpecContext) {
		anchor := instant(2026, time.August, 10, 14, 45, 0)
		schedule := cron.Spec(cron.Every(time.Minute)).Anchor(anchor)
		definition := jobDefinition(jobName("stale-skip"), schedule, cron.CatchUpNone, cron.OverlapSkip)
		states, err := integrationStore.Reconcile(ctx, []cron.JobDefinition{definition}, anchor)
		Expect(err).NotTo(HaveOccurred())

		firstAt := states[0].NextRunAt
		secondAt := next(schedule, firstAt)
		thirdAt := next(schedule, secondAt)
		first := cron.SkipRequest{
			OccurrenceID: cron.OccurrenceID(definition.Name, firstAt),
			JobName:      definition.Name,
			ScheduledAt:  firstAt,
			NextRunAt:    secondAt,
			SkippedAt:    firstAt.Add(time.Second),
			Reason:       "catch-up disabled",
		}
		second := cron.SkipRequest{
			OccurrenceID: cron.OccurrenceID(definition.Name, secondAt),
			JobName:      definition.Name,
			ScheduledAt:  secondAt,
			NextRunAt:    thirdAt,
			SkippedAt:    secondAt.Add(time.Second),
			Reason:       "catch-up disabled",
		}
		Expect(integrationStore.Skip(ctx, first)).To(Succeed())
		Expect(integrationStore.Skip(ctx, second)).To(Succeed())

		advanced, err := integrationStore.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		advancedState := findState(advanced, definition.Name)
		advancedOccurrence := findOccurrence(advanced, first.OccurrenceID)
		Expect(integrationStore.Skip(ctx, first)).To(Succeed())
		replayed, err := integrationStore.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(findState(replayed, definition.Name)).To(Equal(advancedState))
		Expect(findOccurrence(replayed, first.OccurrenceID)).To(Equal(advancedOccurrence))

		reconfigured := jobDefinition(
			definition.Name,
			cron.Spec(cron.Daily(cron.Noon())),
			cron.CatchUpNone,
			cron.OverlapSkip,
		)
		_, err = integrationStore.Reconcile(ctx, []cron.JobDefinition{reconfigured}, second.SkippedAt.Add(time.Second))
		Expect(err).NotTo(HaveOccurred())
		beforeStaleReplay, err := integrationStore.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		beforeStaleState := findState(beforeStaleReplay, definition.Name)
		beforeStaleOccurrence := findOccurrence(beforeStaleReplay, first.OccurrenceID)
		Expect(integrationStore.Skip(ctx, first)).To(Succeed())
		afterStaleReplay, err := integrationStore.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(findState(afterStaleReplay, definition.Name)).To(Equal(beforeStaleState))
		Expect(findOccurrence(afterStaleReplay, first.OccurrenceID)).To(Equal(beforeStaleOccurrence))
	})

	It("lists and reclaims expired occurrences without a service restart", func(ctx SpecContext) {
		anchor := instant(2026, time.August, 10, 15, 0, 0)
		schedule := cron.Spec(cron.Every(time.Minute)).Anchor(anchor)
		states, err := integrationStore.Reconcile(ctx, []cron.JobDefinition{
			jobDefinition(jobName("reclaim"), schedule, cron.CatchUpAll, cron.OverlapSkip),
		}, anchor)
		Expect(err).NotTo(HaveOccurred())

		firstAt := states[0].NextRunAt
		nextAt := next(schedule, firstAt)
		firstClaimedAt := firstAt.Add(time.Second)
		first := claim(states[0], firstAt, nextAt, "worker-a", "old-token", firstClaimedAt, time.Minute)
		_, err = integrationStore.Claim(ctx, first)
		Expect(err).NotTo(HaveOccurred())

		recoveryAt := firstClaimedAt.Add(time.Minute + time.Second)
		expired, err := integrationStore.Expired(ctx, recoveryAt, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(expired).To(ContainElement(HaveField("ID", first.OccurrenceID)))

		reclaimed, err := integrationStore.Claim(ctx, claim(states[0], firstAt, nextAt, "worker-b", "new-token", recoveryAt, time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(reclaimed.Disposition).To(Equal(cron.ClaimAcquired))
		Expect(reclaimed.Occurrence.Attempt).To(Equal(uint32(2)))
	})

	It("round-trips relational job and occurrence state through Snapshot", func(ctx SpecContext) {
		location, err := time.LoadLocation("America/Chicago")
		Expect(err).NotTo(HaveOccurred())
		now := instant(2026, time.August, 10, 16, 0, 0)
		schedule := cron.Spec(cron.Weekly(time.Monday, cron.At(5, 30).PM())).In(location)
		definition := jobDefinition(jobName("snapshot"), schedule, cron.CatchUpLatest, cron.OverlapAllow)
		states, err := integrationStore.Reconcile(ctx, []cron.JobDefinition{definition}, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(states).To(HaveLen(1))

		scheduledAt := states[0].NextRunAt
		request := claim(states[0], scheduledAt, next(schedule, scheduledAt), "worker-a", "snapshot-token", scheduledAt.Add(time.Second), time.Minute)
		_, err = integrationStore.Claim(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(integrationStore.Complete(ctx, cron.Completion{
			OccurrenceID: request.OccurrenceID,
			LeaseToken:   request.LeaseToken,
			Status:       cron.OccurrenceSucceeded,
			FinishedAt:   request.ClaimedAt.Add(time.Second),
		})).To(Succeed())

		snapshot, err := integrationStore.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		state := findState(snapshot, definition.Name)
		Expect(state.Definition).To(Equal(definition))
		occurrence := findOccurrence(snapshot, request.OccurrenceID)
		Expect(occurrence.JobName).To(Equal(definition.Name))
		Expect(occurrence.ScheduledAt).To(Equal(scheduledAt))
		Expect(occurrence.Status).To(Equal(cron.OccurrenceSucceeded))
		Expect(occurrence.Attempt).To(Equal(uint32(1)))
	})
})

func ensureCronSchema(ctx context.Context, db *sql.DB) error {
	if _, err := cronpostgres.New(db).ListJobs(ctx); err == nil {
		return nil
	}
	if _, err := db.ExecContext(ctx, cronUpMigration); err != nil {
		return fmt.Errorf("apply cron integration migration: %w", err)
	}
	_, err := cronpostgres.New(db).ListJobs(ctx)
	return err
}

func databaseURLWithSearchPath(databaseURL, schemaName string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", fmt.Errorf("parse cron integration database URL: %w", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", fmt.Errorf("cron integration database URL must use postgres or postgresql")
	}
	databaseName := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if decoded, decodeErr := url.PathUnescape(databaseName); decodeErr == nil {
		databaseName = decoded
	}
	if !strings.HasSuffix(databaseName, "_test") {
		return "", fmt.Errorf("cron integration database name must end in _test, got %q", databaseName)
	}
	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func jobName(kind string) string {
	sequence := nameSequence.Add(1)
	return "it-" + kind + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatUint(sequence, 36)
}

func instant(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, time.UTC)
}

func jobDefinition(name string, schedule cron.Schedule, catchUp cron.CatchUpPolicy, overlap cron.OverlapPolicy) cron.JobDefinition {
	definition, err := schedule.Definition()
	Expect(err).NotTo(HaveOccurred())
	return cron.JobDefinition{Name: name, Schedule: definition, CatchUp: catchUp, Overlap: overlap}
}

func next(schedule cron.Schedule, after time.Time) time.Time {
	value, err := schedule.Next(after)
	Expect(err).NotTo(HaveOccurred())
	return value
}

func claim(state cron.JobState, scheduledAt, nextRunAt time.Time, owner, token string, claimedAt time.Time, leaseDuration time.Duration) cron.ClaimRequest {
	return cron.ClaimRequest{
		OccurrenceID: cron.OccurrenceID(state.Definition.Name, scheduledAt),
		JobName:      state.Definition.Name,
		ScheduledAt:  scheduledAt,
		NextRunAt:    nextRunAt,
		LeaseOwner:   owner,
		LeaseToken:   token,
		ClaimedAt:    claimedAt,
		LeaseUntil:   claimedAt.Add(leaseDuration),
	}
}

func findState(snapshot cron.StoreSnapshot, name string) cron.JobState {
	for _, state := range snapshot.Jobs {
		if state.Definition.Name == name {
			return state
		}
	}
	Fail("job state not found: " + name)
	return cron.JobState{}
}

func findOccurrence(snapshot cron.StoreSnapshot, id string) cron.OccurrenceRecord {
	for _, occurrence := range snapshot.Occurrences {
		if occurrence.ID == id {
			return occurrence
		}
	}
	Fail("occurrence not found: " + id)
	return cron.OccurrenceRecord{}
}
