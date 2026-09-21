package control

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/fleet"
	"github.com/candacelabs/csf/services/candaceos/internal/storedb"
	"github.com/candacelabs/csf/services/candaceos/store"
)

const runtimeTestDatabaseURLEnv = "CANDACEOS_STORE_TEST_DATABASE_URL"

func TestControlRuntime(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "candaceos-core control runtime suite")
}

var _ = Describe("durable fault tracking", func() {
	It("clears only the operation that successfully retries", func() {
		runtime := &Runtime{}
		runtime.rememberRecordError(faultRunStatus+"run-1", errors.New("first receipt write failed"))
		runtime.rememberRecordError(faultRunStatus+"run-2", errors.New("second receipt write failed"))
		runtime.rememberError(faultFleet, errors.New("fleet write failed"))

		Expect(runtime.durableFault()).To(MatchError("unrepaired durable control-plane write fault: fleet_observation, run_status:run-1, run_status:run-2"))
		runtime.rememberError(faultFleet, nil)
		Expect(runtime.durableFault()).To(MatchError("unrepaired durable control-plane write fault: run_status:run-1, run_status:run-2"))
		// A later successful callback is not a repair for either missing record.
		runtime.rememberRecordError(faultRunStatus+"run-2", nil)
		Expect(runtime.durableFault()).To(MatchError("unrepaired durable control-plane write fault: run_status:run-1, run_status:run-2"))
		runtime.rememberError(faultRunStatus+"run-1", nil)
		runtime.rememberError(faultRunStatus+"run-2", nil)
		Expect(runtime.durableFault()).NotTo(HaveOccurred())
	})

	It("fails closed before narrowing an overflowing Warden term", func(ctx SpecContext) {
		runtime := &Runtime{}
		runtime.RecordFleetContext(ctx, fleet.Snapshot{Term: uint64(math.MaxInt64) + 1})

		Expect(runtime.durableFault()).To(MatchError(ContainSubstring("fleet_observation")))
	})

	It("clears a transient fleet fault when the known durable state returns", func(ctx SpecContext) {
		snapshot := fleet.Snapshot{
			Term:  9,
			Nodes: []fleet.Node{{ID: "node-a", Address: "10.0.0.1:7717", Status: "alive"}},
		}
		labels := map[string]map[string]string{"node-a": {"role": "worker"}}
		configured := fleet.WithConfiguration(snapshot, labels)
		runtime := &Runtime{
			labels: labels, faults: map[string]string{faultFleet: "transient failure"},
			persistence:  testPersistenceTiming(),
			lastFleetKey: fleetPersistenceKey(configured), lastFleetWriteAt: time.Now(),
		}

		runtime.RecordFleetContext(ctx, snapshot)

		Expect(runtime.durableFault()).NotTo(HaveOccurred())
	})

	It("backs off a failed fleet write while retaining the fault", func(ctx SpecContext) {
		runtime := &Runtime{
			faults:           map[string]string{faultFleet: "disk is still unavailable"},
			persistence:      testPersistenceTiming(),
			lastFleetFailure: time.Now(),
		}

		runtime.RecordFleetContext(ctx, fleet.Snapshot{Term: 10})

		Expect(runtime.durableFault()).To(MatchError(ContainSubstring(faultFleet)))
	})

	It("accepts an ambiguous commit only after durable read-back confirms it", func() {
		attempts := 0
		runtime := &Runtime{persistence: &candaceosv1.PersistenceTiming{
			FleetPollIntervalNanoseconds: int64(20 * time.Millisecond),
		}}
		err := runtime.reconcileCommitError(
			&store.TransactionCommitError{Cause: context.DeadlineExceeded},
			func(readContext context.Context) (bool, error) {
				attempts++
				return attempts == 2, nil
			},
		)

		Expect(err).NotTo(HaveOccurred())
		Expect(attempts).To(Equal(2))
		definitive := errors.New("statement rejected")
		Expect(runtime.reconcileCommitError(definitive, func(readContext context.Context) (bool, error) {
			Fail("definitive failures must not be read back as ambiguous commits")
			return false, nil
		})).To(MatchError(definitive))
	})

	It("accepts an ambiguous fleet commit only when every written fact is durable", func(ctx SpecContext) {
		controlStore := openRuntimeTestStore(ctx)
		labels := map[string]map[string]string{
			"node-a": {"role": "worker", "gpu": "nvidia"},
		}
		runtime := &Runtime{
			store: controlStore, labels: labels,
			persistence: &candaceosv1.PersistenceTiming{
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
			persistence: &candaceosv1.PersistenceTiming{
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
	schemaName := fmt.Sprintf("candaceos_control_runtime_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()

	admin, err := pgxpool.New(ctx, databaseURL)
	Expect(err).NotTo(HaveOccurred())
	Expect(admin.Ping(ctx)).To(Succeed())
	DeferCleanup(admin.Close)
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schemaName}.Sanitize())
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func(cleanupCtx SpecContext) {
		_, dropErr := admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" CASCADE")
		Expect(dropErr).NotTo(HaveOccurred())
	})
	controlStore, err := store.OpenControlStore(ctx, parsed.String())
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(controlStore.Close)
	return controlStore
}

func testPersistenceTiming() *candaceosv1.PersistenceTiming {
	return &candaceosv1.PersistenceTiming{
		FleetPollIntervalNanoseconds: int64(2 * time.Second),
	}
}

var _ = Describe("fleet persistence keys", func() {
	It("ignores poll timestamps but changes with every durable fleet field", func() {
		base := fleet.ConfiguredSnapshot{
			Term: 12,
			Nodes: []fleet.ConfiguredNode{
				{ID: "node-b", Address: "10.0.0.2:7717", Role: "worker", Status: "alive", Labels: map[string]string{"role": "worker"}, LastSeen: time.Unix(20, 0)},
				{ID: "node-a", Address: "10.0.0.1:7717", Role: "control", Status: "alive", Labels: map[string]string{"zone": "west", "role": "control"}, LastSeen: time.Unix(10, 0)},
			},
			UpdatedAt: time.Unix(30, 0),
		}
		pollOnly := base
		pollOnly.Nodes = append([]fleet.ConfiguredNode(nil), base.Nodes...)
		pollOnly.Nodes[0].LastSeen = time.Unix(40, 0)
		pollOnly.UpdatedAt = time.Unix(50, 0)
		pollOnly.Nodes[0], pollOnly.Nodes[1] = pollOnly.Nodes[1], pollOnly.Nodes[0]
		pollOnly.Nodes[0].Labels = map[string]string{"role": "control", "zone": "west"}

		Expect(fleetPersistenceKey(pollOnly)).To(Equal(fleetPersistenceKey(base)))

		changes := []fleet.ConfiguredSnapshot{}
		term := base
		term.Term++
		changes = append(changes, term)
		for _, mutate := range []func(node *fleet.ConfiguredNode){
			func(node *fleet.ConfiguredNode) { node.ID = "node-c" },
			func(node *fleet.ConfiguredNode) { node.Address = "10.0.0.9:7717" },
			func(node *fleet.ConfiguredNode) { node.Role = "worker" },
			func(node *fleet.ConfiguredNode) { node.Status = "suspect" },
			func(node *fleet.ConfiguredNode) { node.Labels = map[string]string{"role": "control", "zone": "east"} },
		} {
			changed := base
			changed.Nodes = append([]fleet.ConfiguredNode(nil), base.Nodes...)
			mutate(&changed.Nodes[1])
			changes = append(changes, changed)
		}
		removed := base
		removed.Nodes = append([]fleet.ConfiguredNode(nil), base.Nodes[:1]...)
		changes = append(changes, removed)
		added := base
		added.Nodes = append(append([]fleet.ConfiguredNode(nil), base.Nodes...), fleet.ConfiguredNode{ID: "node-c"})
		changes = append(changes, added)
		for _, changed := range changes {
			Expect(fleetPersistenceKey(changed)).NotTo(Equal(fleetPersistenceKey(base)))
		}
	})
})

var _ = Describe("activity status", func() {
	It("renders restart-interrupted runs as failures", func() {
		Expect(activityStatus("run.interrupted")).To(Equal("failed"))
	})
})

var _ = Describe("deployment rollout projection", func() {
	It("aggregates every replica and lets any failed run fail the rollout", func() {
		firstFinished := time.Date(2026, time.August, 19, 1, 0, 0, 0, time.UTC)
		secondFinished := firstFinished.Add(time.Second)
		base := storedb.ListDeploymentRolloutRowsRow{
			DeploymentID: "hello", AppName: "hello", LatestRolloutID: "rollout-1", LatestRunID: "run-a",
			LatestNodeID: "node-a", LatestDesiredState: "running", LatestStatus: "succeeded",
			LatestDryRun:          true,
			LatestFinishedAt:      pgtype.Timestamptz{Time: firstFinished, Valid: true},
			PossiblyActiveNodeIds: []string{"node-a", "node-b"},
		}
		failed := base
		failed.LatestRunID = "run-b"
		failed.LatestNodeID = "node-b"
		failed.LatestStatus = "failed"
		failed.LatestDryRun = false
		failed.LatestFinishedAt = pgtype.Timestamptz{Time: secondFinished, Valid: true}

		rollouts := aggregateDeploymentRollouts([]storedb.ListDeploymentRolloutRowsRow{base, failed})

		Expect(rollouts).To(HaveLen(1))
		Expect(rollouts[0].nodeIDs).To(Equal([]string{"node-a", "node-b"}))
		Expect(rollouts[0].latestStatus).To(Equal("failed"))
		Expect(rollouts[0].latestDryRun).To(BeFalse())
		Expect(rollouts[0].latestFinishedAt.Time).To(Equal(secondFinished))
	})

	It("omits obsolete stop targets from the active node projection", func() {
		rows := []storedb.ListDeploymentRolloutRowsRow{
			{DeploymentID: "hello", LatestRolloutID: "rollout-1", LatestRunID: "run-a", LatestNodeID: "node-a", LatestDesiredState: "stopped", LatestStatus: "succeeded", PossiblyActiveNodeIds: []string{"node-b"}},
			{DeploymentID: "hello", LatestRolloutID: "rollout-1", LatestRunID: "run-b", LatestNodeID: "node-b", LatestDesiredState: "running", LatestStatus: "succeeded", PossiblyActiveNodeIds: []string{"node-b"}},
		}

		rollouts := aggregateDeploymentRollouts(rows)

		Expect(rollouts).To(HaveLen(1))
		Expect(rollouts[0].nodeIDs).To(Equal([]string{"node-b"}))
	})

	It("projects a persisted zero-target rollout as a live succeeded stop", func() {
		finished := time.Date(2026, time.August, 19, 1, 0, 0, 0, time.UTC)
		rows := []storedb.ListDeploymentRolloutRowsRow{{
			DeploymentID: "hello", LatestRolloutID: "rollout-noop",
			LatestDesiredState: "stopped", LatestStatus: "succeeded",
			LatestDryRun: false, LatestFinishedAt: pgtype.Timestamptz{Time: finished, Valid: true},
		}}

		rollouts := aggregateDeploymentRollouts(rows)

		Expect(rollouts).To(HaveLen(1))
		Expect(rollouts[0].nodeIDs).To(BeEmpty())
		Expect(rollouts[0].latestStatus).To(Equal("succeeded"))
		Expect(rollouts[0].latestDryRun).To(BeFalse())
		Expect(rollouts[0].latestFinishedAt.Time).To(Equal(finished))
	})
})
