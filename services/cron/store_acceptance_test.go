// Copyright 2026 Candace Labs

//go:build acceptance

// The acceptance tier: the cron store's SQL on a real, disposable
// PostgreSQL named by CANDACE_CSF_TEST_DATABASE_URL, opened only through
// csfpg.OpenPool. It holds what pgmem cannot prove: the conditional,
// lock-free fencing statements under PostgreSQL's own planner and types.

package cron_test

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/ipc/db/csfpg"
	grammar "github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/runtime/config"
	cron "github.com/candacelabs/csf/services/cron"
)

const (
	acceptanceDatabaseVariable = "CANDACE_CSF_TEST_DATABASE_URL"
	searchPathParameter        = "search_path"
)

var _ = Describe("the cron store on PostgreSQL", func() {
	It("claims, renews, fences and completes an occurrence under PostgreSQL's own types", func() {
		ctx := context.Background()
		settings, err := csfpg.SettingsFromEnvironment(config.OSEnvironment(), acceptanceDatabaseVariable)
		if err != nil {
			Skip("set " + acceptanceDatabaseVariable + " to run the cron store on PostgreSQL")
		}
		admin, err := csfpg.OpenPool(ctx, settings)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(admin.Close)
		schema := "cron_acceptance_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, err := admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
			Expect(err).NotTo(HaveOccurred())
		})
		location, err := url.Parse(settings.URL)
		Expect(err).NotTo(HaveOccurred())
		query := location.Query()
		query.Set(searchPathParameter, schema)
		location.RawQuery = query.Encode()
		pool, err := csfpg.OpenPool(ctx, csfpg.Settings{URL: location.String()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(pool.Close)
		handle := pool.OpenSQL()
		DeferCleanup(handle.Close)
		Expect(csfpg.ApplySchema(ctx, handle)).To(Succeed())
		store, err := cron.NewStore(pool)
		Expect(err).NotTo(HaveOccurred())

		// Microsecond instants: what PostgreSQL stores and pgmem cannot.
		at := time.Date(2026, time.August, 10, 12, 0, 0, 123456000, time.UTC)
		schedule := grammar.Spec(grammar.Every(time.Minute)).Anchor(at)
		definition := triggerDefinition(rollup, schedule, grammar.CatchUpAll, grammar.OverlapSkip)
		states, err := store.Reconcile(ctx, []grammar.TriggerDefinition{definition}, at)
		Expect(err).NotTo(HaveOccurred())
		Expect(states).To(HaveLen(1))
		scheduledAt := states[0].NextRunAt
		Expect(scheduledAt).To(Equal(at.Add(time.Minute)))
		nextRunAt := nextAfter(schedule, scheduledAt)

		claimedAt := scheduledAt.Add(time.Second)
		first, err := store.Claim(ctx, claim(rollup, scheduledAt, nextRunAt, "token_a", claimedAt, time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Disposition).To(Equal(cron.ClaimAcquired))
		Expect(first.Occurrence.ScheduledAt).To(Equal(scheduledAt))

		overlap, err := store.Claim(ctx, claim(rollup, nextRunAt, nextAfter(schedule, nextRunAt), "token_b", claimedAt.Add(time.Second), time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(overlap.Disposition).To(Equal(cron.ClaimSkippedOverlap))

		renewedAt := claimedAt.Add(10 * time.Second)
		Expect(store.Renew(ctx, cron.LeaseRenewal{OccurrenceID: first.Occurrence.ID, LeaseToken: "stale", RenewedAt: renewedAt, LeaseUntil: renewedAt.Add(time.Minute)})).To(MatchError(cron.ErrLeaseLost))
		Expect(store.Renew(ctx, cron.LeaseRenewal{OccurrenceID: first.Occurrence.ID, LeaseToken: "token_a", RenewedAt: renewedAt, LeaseUntil: renewedAt.Add(time.Minute)})).To(Succeed())
		Expect(store.Complete(ctx, cron.Completion{OccurrenceID: first.Occurrence.ID, LeaseToken: "token_a", Status: grammar.OccurrenceFailed, FinishedAt: renewedAt.Add(time.Second), Error: "rollup refused"})).To(Succeed())
		Expect(store.Complete(ctx, cron.Completion{OccurrenceID: first.Occurrence.ID, LeaseToken: "token_a", Status: grammar.OccurrenceSucceeded, FinishedAt: renewedAt.Add(2 * time.Second)})).To(MatchError(cron.ErrLeaseLost))

		snapshot, err := store.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Triggers).To(HaveLen(1))
		Expect(snapshot.Triggers[0].NextRunAt).To(Equal(nextAfter(schedule, nextRunAt)))
		Expect(snapshot.Occurrences).To(HaveLen(2))
		Expect(snapshot.Occurrences[0]).To(And(HaveField("Status", grammar.OccurrenceFailed), HaveField("Error", "rollup refused")))
		Expect(snapshot.Occurrences[1]).To(And(HaveField("Status", grammar.OccurrenceSkipped), HaveField("SkipReason", "overlap")))
		expired, err := store.Expired(ctx, renewedAt.Add(time.Hour), 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(expired).To(BeEmpty())
	})
})
