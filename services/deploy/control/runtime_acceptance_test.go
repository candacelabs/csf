// Copyright 2026 Candace Labs

//go:build acceptance

// The acceptance tier: opt-in specs against a real, disposable PostgreSQL
// selected by DEPLOY_STORE_TEST_DATABASE_URL, opened only through csfpg.OpenPool.
// pgmem cannot yet run the deploy service schema; see the csfpg README and go-rules.md CS-16.

package control

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	deployv1 "github.com/candacelabs/csf/proto/candace/deploy/v1"
	"github.com/candacelabs/csf/services/deploy/fleet"
	"github.com/candacelabs/csf/services/deploy/internal/storedb"
	"github.com/candacelabs/csf/services/deploy/store"
)

const runtimeTestDatabaseURLEnv = "DEPLOY_STORE_TEST_DATABASE_URL"

var _ = Describe("durable fault tracking on PostgreSQL", func() {
	It("accepts an ambiguous fleet commit only when every written fact is durable", func(ctx SpecContext) {
		controlStore := openRuntimeTestStore(ctx)
		labels := map[string]map[string]string{
			"node-a": {"role": "worker", "gpu": "nvidia"},
		}
		runtime := &Runtime{
			store: controlStore, labels: labels,
			persistence: &deployv1.PersistenceTiming{
				FleetPollIntervalNanoseconds: int64(200 * time.Millisecond),
			},
			faults: make(map[string]string),
		}
		snapshot := fleet.Snapshot{
			Term:      12,
			UpdatedAt: time.Date(2026, time.September, 5, 1, 2, 3, 987654321, time.UTC),
			Nodes: []fleet.Node{{
				ID: "node-a", Address: "10.0.0.1:7717", Status: "alive",
				LastSeen: time.Date(2026, time.September, 5, 1, 2, 2, 123456789, time.UTC),
			}},
		}
		transactionCalls := 0
		runtime.recordFleetContext(ctx, snapshot, func(
			transactionContext context.Context,
			apply func(queries *storedb.Queries) error,
		) error {
			transactionCalls++
			if err := controlStore.WithTx(transactionContext, apply); err != nil {
				return err
			}
			return &store.TransactionCommitError{Cause: context.DeadlineExceeded}
		})

		Expect(transactionCalls).To(Equal(1))
		Expect(runtime.durableFault()).NotTo(HaveOccurred())
		configured := fleet.WithConfiguration(snapshot, labels)
		persisted, err := runtime.fleetObservationPersisted(
			ctx, configured, postgresTimestamp(snapshot.UpdatedAt),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted).To(BeTrue())

		rejected := snapshot
		rejected.Term++
		rejectedRuntime := &Runtime{
			store: controlStore, labels: labels,
			persistence: &deployv1.PersistenceTiming{
				FleetPollIntervalNanoseconds: int64(20 * time.Millisecond),
			},
			faults: make(map[string]string),
		}
		rejectedRuntime.recordFleetContext(ctx, rejected, func(
			_ context.Context,
			_ func(queries *storedb.Queries) error,
		) error {
			return &store.TransactionCommitError{Cause: context.DeadlineExceeded}
		})
		Expect(rejectedRuntime.durableFault()).To(MatchError(ContainSubstring(faultFleet)))
		Expect(rejectedRuntime.lastFleetKey).To(BeEmpty())
		Expect(rejectedRuntime.lastFleetWriteAt).To(BeZero())

		mismatchCases := []struct {
			name   string
			mutate func(candidate *fleet.Snapshot, candidateLabels map[string]map[string]string)
		}{
			{name: "observation", mutate: func(candidate *fleet.Snapshot, candidateLabels map[string]map[string]string) {
				candidate.UpdatedAt = candidate.UpdatedAt.Add(time.Minute)
			}},
			{name: "term", mutate: func(candidate *fleet.Snapshot, candidateLabels map[string]map[string]string) {
				candidate.Term++
			}},
			{name: "address", mutate: func(candidate *fleet.Snapshot, candidateLabels map[string]map[string]string) {
				candidate.Nodes[0].Address = "10.0.0.2:7717"
			}},
			{name: "role", mutate: func(candidate *fleet.Snapshot, candidateLabels map[string]map[string]string) {
				candidateLabels["node-a"]["role"] = "control"
			}},
			{name: "status", mutate: func(candidate *fleet.Snapshot, candidateLabels map[string]map[string]string) {
				candidate.Nodes[0].Status = "suspect"
			}},
			{name: "last seen", mutate: func(candidate *fleet.Snapshot, candidateLabels map[string]map[string]string) {
				candidate.Nodes[0].LastSeen = candidate.Nodes[0].LastSeen.Add(time.Second)
			}},
			{name: "labels", mutate: func(candidate *fleet.Snapshot, candidateLabels map[string]map[string]string) {
				candidateLabels["node-a"]["gpu"] = "amd"
			}},
		}
		for _, mismatch := range mismatchCases {
			candidate := snapshot
			candidate.Nodes = append([]fleet.Node(nil), snapshot.Nodes...)
			candidateLabels := map[string]map[string]string{
				"node-a": {"role": "worker", "gpu": "nvidia"},
			}
			mismatch.mutate(&candidate, candidateLabels)
			candidateConfigured := fleet.WithConfiguration(candidate, candidateLabels)
			persisted, err = runtime.fleetObservationPersisted(
				ctx, candidateConfigured, postgresTimestamp(candidate.UpdatedAt),
			)
			Expect(err).NotTo(HaveOccurred(), mismatch.name)
			Expect(persisted).To(BeFalse(), mismatch.name)
		}
	}, NodeTimeout(30*time.Second))
})

func openRuntimeTestStore(ctx SpecContext) *store.Store {
	databaseURL := strings.TrimSpace(os.Getenv(runtimeTestDatabaseURLEnv))
	if databaseURL == "" {
		Skip("set " + runtimeTestDatabaseURLEnv + " to run PostgreSQL control specs")
	}
	parsed, err := url.Parse(databaseURL)
	Expect(err).NotTo(HaveOccurred())
	databaseName := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if decoded, decodeErr := url.PathUnescape(databaseName); decodeErr == nil {
		databaseName = decoded
	}
	Expect(databaseName).To(HaveSuffix("_test"))
	schemaName := fmt.Sprintf("deploy_control_runtime_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()

	admin, err := csfpg.OpenPool(ctx, csfpg.Settings{URL: databaseURL})
	Expect(err).NotTo(HaveOccurred())
	Expect(admin.Ping(ctx)).To(Succeed())
	DeferCleanup(admin.Close)
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schemaName}.Sanitize())
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func(cleanupCtx SpecContext) {
		_, dropErr := admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" CASCADE")
		Expect(dropErr).NotTo(HaveOccurred())
	})
	controlPool, err := csfpg.OpenPool(ctx, csfpg.Settings{URL: parsed.String()})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(controlPool.Close)
	controlStore, err := store.NewControlStore(ctx, controlPool)
	Expect(err).NotTo(HaveOccurred())
	return controlStore
}
