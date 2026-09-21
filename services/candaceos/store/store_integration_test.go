package store

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/candaceos/internal/storedb"
)

const storeTestDatabaseURLEnv = "CANDACEOS_STORE_TEST_DATABASE_URL"

var (
	integrationAdmin       *pgxpool.Pool
	integrationSchema      string
	integrationStore       *Store
	integrationDatabaseURL string
)

func TestStore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CandaceOS Store Integration Suite")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	databaseURL := os.Getenv(storeTestDatabaseURLEnv)
	if databaseURL == "" {
		Skip("set " + storeTestDatabaseURLEnv + " to run PostgreSQL integration specs")
	}

	schemaName := fmt.Sprintf("candaceos_store_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	isolatedURL, _, err := isolatedStoreDatabaseURL(databaseURL, schemaName)
	Expect(err).NotTo(HaveOccurred())

	admin, err := pgxpool.New(ctx, databaseURL)
	Expect(err).NotTo(HaveOccurred())
	Expect(admin.Ping(ctx)).To(Succeed())
	integrationAdmin = admin
	integrationSchema = schemaName

	_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schemaName}.Sanitize())
	Expect(err).NotTo(HaveOccurred())

	controlStore, err := OpenControlStore(ctx, isolatedURL)
	Expect(err).NotTo(HaveOccurred())
	integrationStore = controlStore
	integrationDatabaseURL = isolatedURL
})

var _ = AfterSuite(func(ctx SpecContext) {
	if integrationStore != nil {
		integrationStore.Close()
	}
	if integrationAdmin != nil && integrationSchema != "" {
		_, err := integrationAdmin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{integrationSchema}.Sanitize()+" CASCADE")
		Expect(err).NotTo(HaveOccurred())
	}
	if integrationAdmin != nil {
		integrationAdmin.Close()
	}
})

var _ = Describe("PostgreSQL migrations", func() {
	It("applies the embedded migrations repeatedly without duplicating history", func(ctx SpecContext) {
		Expect(applyMigrations(ctx, integrationStore.pool)).To(Succeed())
		Expect(applyMigrations(ctx, integrationStore.pool)).To(Succeed())

		secondStore, err := OpenControlStore(ctx, integrationDatabaseURL)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(secondStore.Close)

		versions, err := secondStore.Queries.ListAppliedMigrationVersions(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(versions).To(Equal([]int32{1, 2, 3, 4}))

		var migrationRows int
		err = secondStore.pool.QueryRow(ctx,
			"SELECT count(*) FROM candaceos_schema_migrations WHERE version = 1 AND name = '001_initial.sql'",
		).Scan(&migrationRows)
		Expect(err).NotTo(HaveOccurred())
		Expect(migrationRows).To(Equal(1))
	})

	It("repairs published legacy rollout state and retains rollback inserts", func(ctx SpecContext) {
		schemaName := fmt.Sprintf("candaceos_upgrade_test_%d_%d", os.Getpid(), time.Now().UnixNano())
		isolatedURL, _, err := isolatedStoreDatabaseURL(os.Getenv(storeTestDatabaseURLEnv), schemaName)
		Expect(err).NotTo(HaveOccurred())
		_, err = integrationAdmin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schemaName}.Sanitize())
		Expect(err).NotTo(HaveOccurred())

		upgradePool, err := pgxpool.New(ctx, isolatedURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(upgradePool.Ping(ctx)).To(Succeed())
		DeferCleanup(func(cleanupCtx SpecContext) {
			upgradePool.Close()
			_, dropErr := integrationAdmin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" CASCADE")
			Expect(dropErr).NotTo(HaveOccurred())
		})

		registry, err := migrations.ReadFile("migrations/000_registry.sql")
		Expect(err).NotTo(HaveOccurred())
		_, err = upgradePool.Exec(ctx, string(registry))
		Expect(err).NotTo(HaveOccurred())
		for version, filename := range []string{
			"001_initial.sql", "002_deployment_rollouts.sql", "003_legacy_deployment_run_inserts.sql",
		} {
			contents, readErr := migrations.ReadFile("migrations/" + filename)
			Expect(readErr).NotTo(HaveOccurred())
			_, execErr := upgradePool.Exec(ctx, string(contents))
			Expect(execErr).NotTo(HaveOccurred())
			_, execErr = upgradePool.Exec(ctx, `
INSERT INTO candaceos_schema_migrations (version, name, applied_at)
VALUES ($1, $2, $3)`, version+1, filename, time.Now().UTC())
			Expect(execErr).NotTo(HaveOccurred())
		}

		legacyAt := time.Date(2026, time.August, 17, 11, 0, 0, 0, time.UTC)
		_, err = upgradePool.Exec(ctx, `
INSERT INTO candaceos_app_revisions (
    app_revision_id, app_name, source_repository, source_revision,
    source_sha256, compose_file, created_at
) VALUES (
    'upgrade-revision', 'upgrade', 'https://example.invalid/upgrade.git', 'upgrade-sha',
    'sha256:upgrade', 'apps/upgrade/compose.yaml', $1
)`, legacyAt)
		Expect(err).NotTo(HaveOccurred())
		_, err = upgradePool.Exec(ctx, `
INSERT INTO candaceos_deployments (
    deployment_id, app_revision_id, project_name, workspace_path, desired_state,
    placement_mode, exact_node_id, replicas, stateful, created_at, updated_at
) VALUES (
    'upgrade', 'upgrade-revision', 'upgrade', 'apps/upgrade', 'stopped',
    'node', 'node-a', 1, TRUE, $1, $1
)`, legacyAt)
		Expect(err).NotTo(HaveOccurred())
		_, err = upgradePool.Exec(ctx, `
INSERT INTO candaceos_deployment_runs (
    run_id, rollout_id, deployment_id, app_revision_id, node_id, desired_state,
    warden_term, leader_id, status, dry_run, requested_at, finished_at
) VALUES (
    'upgrade-historical-run', 'upgrade:' || $1::timestamptz::text,
    'upgrade', 'upgrade-revision', 'node-a', 'stopped', 1, 'node-a',
    'succeeded', FALSE, $1, $1
)`, legacyAt)
		Expect(err).NotTo(HaveOccurred())

		// Model the briefly published trigger that inferred a missing run
		// direction from mutable deployment state.
		_, err = upgradePool.Exec(ctx, `
CREATE OR REPLACE FUNCTION candaceos_fill_legacy_deployment_run_columns()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.rollout_id IS NULL THEN
        NEW.rollout_id := NEW.deployment_id || ':' || NEW.requested_at::text;
    END IF;
    IF NEW.desired_state IS NULL THEN
        SELECT desired_state INTO NEW.desired_state
        FROM candaceos_deployments
        WHERE deployment_id = NEW.deployment_id;
    END IF;
    RETURN NEW;
END;
$$;`)
		Expect(err).NotTo(HaveOccurred())

		Expect(applyMigrations(ctx, upgradePool)).To(Succeed())
		versions, err := storedb.New(upgradePool).ListAppliedMigrationVersions(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(versions).To(Equal([]int32{1, 2, 3, 4}))

		var repairedState string
		Expect(upgradePool.QueryRow(ctx, `
SELECT desired_state FROM candaceos_deployment_runs
WHERE run_id = 'upgrade-historical-run'`).Scan(&repairedState)).To(Succeed())
		Expect(repairedState).To(Equal("running"))

		rollbackAt := legacyAt.Add(time.Minute)
		_, err = upgradePool.Exec(ctx, `
INSERT INTO candaceos_deployment_runs (
    run_id, deployment_id, app_revision_id, node_id, warden_term, leader_id,
    status, requested_at
) VALUES (
    'upgrade-rollback-run', 'upgrade', 'upgrade-revision', 'node-b', 1,
    'node-a', 'running', $1
)`, rollbackAt)
		Expect(err).NotTo(HaveOccurred())

		var rollbackState, rollbackID string
		var parentRows int
		Expect(upgradePool.QueryRow(ctx, `
SELECT desired_state, rollout_id FROM candaceos_deployment_runs
WHERE run_id = 'upgrade-rollback-run'`).Scan(&rollbackState, &rollbackID)).To(Succeed())
		Expect(rollbackState).To(Equal("running"))
		Expect(upgradePool.QueryRow(ctx, `
SELECT count(*) FROM candaceos_deployment_rollouts
WHERE rollout_id = $1 AND deployment_id = 'upgrade'`, rollbackID).Scan(&parentRows)).To(Succeed())
		Expect(parentRows).To(Equal(1))
	})

	It("materializes the complete relational schema without wire-format blobs", func(ctx SpecContext) {
		rows, err := integrationStore.pool.Query(ctx, `
			SELECT table_name, column_name, udt_name
			FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name LIKE 'candaceos_%'
			ORDER BY table_name, ordinal_position`)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(rows.Close)

		allowedTypes := []string{"bool", "int4", "int8", "text", "timestamptz"}
		seenTables := make(map[string]struct{})
		for rows.Next() {
			var table, column, postgresType string
			Expect(rows.Scan(&table, &column, &postgresType)).To(Succeed())
			seenTables[table] = struct{}{}
			Expect(allowedTypes).To(ContainElement(postgresType),
				"%s.%s unexpectedly persists %s instead of relational scalar columns", table, column, postgresType)
			Expect(strings.ToLower(column)).NotTo(ContainSubstring("protobuf"))
			Expect(strings.ToLower(column)).NotTo(ContainSubstring("wire_blob"))
		}
		Expect(rows.Err()).NotTo(HaveOccurred())

		Expect(sortedTableNames(seenTables)).To(Equal([]string{
			"candaceos_activity_receipts",
			"candaceos_app_revisions",
			"candaceos_deployment_labels",
			"candaceos_deployment_rollouts",
			"candaceos_deployment_runs",
			"candaceos_deployments",
			"candaceos_node_labels",
			"candaceos_nodes",
			"candaceos_operator_approvals",
			"candaceos_operator_runs",
			"candaceos_possibly_active_deployment_nodes",
			"candaceos_schema_migrations",
		}))
	})
})

var _ = Describe("SQLC relational persistence", Ordered, func() {
	var requestedAt time.Time

	BeforeAll(func() {
		requestedAt = time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	})

	It("persists desired state, fencing, and append-only receipts as queryable columns", func(ctx SpecContext) {
		stamp := timestamp(requestedAt)
		var requestedReceiptID int64
		err := integrationStore.WithTx(ctx, func(queries *storedb.Queries) error {
			if err := queries.UpsertNode(ctx, storedb.UpsertNodeParams{
				NodeID: "node-a", Address: "100.64.0.10:7718", Role: "leader", Status: "alive",
				WardenTerm: 7, LastSeenAt: stamp, ObservedAt: stamp,
			}); err != nil {
				return fmt.Errorf("upserting node: %w", err)
			}
			if err := queries.UpsertNodeLabel(ctx, storedb.UpsertNodeLabelParams{
				NodeID: "node-a", LabelKey: "gpu", LabelValue: "nvidia",
			}); err != nil {
				return fmt.Errorf("upserting node label: %w", err)
			}
			if err := queries.UpsertAppRevision(ctx, storedb.UpsertAppRevisionParams{
				AppRevisionID: "hello-revision", AppName: "hello",
				SourceRepository: "https://example.test/candace/hello.git",
				SourceRevision:   "0123456789abcdef0123456789abcdef01234567",
				SourceSha256:     "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				ComposeFile:      "apps/hello/compose.yaml",
				ImageDigest:      text("sha256:abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				CreatedAt:        stamp,
			}); err != nil {
				return fmt.Errorf("upserting app revision: %w", err)
			}
			if err := queries.UpsertDeployment(ctx, storedb.UpsertDeploymentParams{
				DeploymentID: "hello", AppRevisionID: "hello-revision", ProjectName: "hello",
				WorkspacePath: "apps/hello", DesiredState: "running", PlacementMode: "node",
				ExactNodeID: text("node-a"), Replicas: 1, Stateful: true,
				CreatedAt: stamp, UpdatedAt: stamp,
			}); err != nil {
				return fmt.Errorf("upserting deployment: %w", err)
			}
			if err := queries.CreateDeploymentRun(ctx, storedb.CreateDeploymentRunParams{
				RunID: "deployment-run-1", RolloutID: "deployment-rollout-1",
				DeploymentID: "hello", AppRevisionID: "hello-revision",
				NodeID: "node-a", DesiredState: "running",
				WardenTerm: 7, LeaderID: "node-a", RequestedAt: stamp,
			}); err != nil {
				return fmt.Errorf("creating deployment run: %w", err)
			}
			var err error
			requestedReceiptID, err = AppendReceipt(
				ctx, queries, "deployment_run", "deployment-run-1", "deployment.requested",
				"Approved assignment recorded", "0123456789abcdef", requestedAt,
			)
			return err
		})
		Expect(err).NotTo(HaveOccurred())

		finishedAt := requestedAt.Add(time.Second)
		updated, err := integrationStore.Queries.FinishDeploymentRun(ctx, storedb.FinishDeploymentRunParams{
			Status: "succeeded", DryRun: pgtype.Bool{Bool: true, Valid: true},
			FinishedAt: timestamp(finishedAt), RunID: "deployment-run-1",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(updated).To(Equal(int64(1)))
		finishedReceiptID, err := integrationStore.AppendReceipt(
			ctx, "deployment_run", "deployment-run-1", "deployment.dry_run",
			"Node agent validated the assignment", "", finishedAt,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(finishedReceiptID).To(BeNumerically(">", requestedReceiptID))

		nodes, err := integrationStore.Queries.ListNodes(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(nodes).To(ContainElement(SatisfyAll(
			WithTransform(func(node storedb.CandaceosNode) string { return node.NodeID }, Equal("node-a")),
			WithTransform(func(node storedb.CandaceosNode) int64 { return node.WardenTerm }, Equal(int64(7))),
		)))

		deployments, err := integrationStore.Queries.ListDeploymentRolloutRows(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(deployments).To(HaveLen(1))
		deployment := deployments[0]
		Expect(deployment.DeploymentID).To(Equal("hello"))
		Expect(deployment.SourceRevision).To(Equal("0123456789abcdef0123456789abcdef01234567"))
		Expect(deployment.SourceSha256).To(Equal("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
		Expect(deployment.ComposeFile).To(Equal("apps/hello/compose.yaml"))
		Expect(deployment.LatestNodeID).To(Equal("node-a"))
		Expect(deployment.LatestStatus).To(Equal("succeeded"))
		Expect(deployment.LatestDryRun).To(BeTrue())

		receipts, err := integrationStore.Queries.ListRecentReceipts(ctx, 100)
		Expect(err).NotTo(HaveOccurred())
		deploymentReceipts := make([]storedb.CandaceosActivityReceipt, 0, 2)
		for _, receipt := range receipts {
			if receipt.EntityType == "deployment_run" && receipt.EntityID == "deployment-run-1" {
				deploymentReceipts = append(deploymentReceipts, receipt)
			}
		}
		Expect(deploymentReceipts).To(HaveLen(2))
		Expect(deploymentReceipts[0].ReceiptID).To(Equal(finishedReceiptID))
		Expect(deploymentReceipts[0].PayloadSha256.Valid).To(BeFalse())
		Expect(deploymentReceipts[1].ReceiptID).To(Equal(requestedReceiptID))
		Expect(deploymentReceipts[1].PayloadSha256.String).To(Equal("0123456789abcdef"))
	})

	It("returns every run in the latest replica rollout", func(ctx SpecContext) {
		stamp := timestamp(requestedAt)
		Expect(integrationStore.Queries.CreateDeploymentRun(ctx, storedb.CreateDeploymentRunParams{
			RunID: "deployment-run-2", RolloutID: "deployment-rollout-1",
			DeploymentID: "hello", AppRevisionID: "hello-revision",
			NodeID: "node-b", DesiredState: "running",
			WardenTerm: 7, LeaderID: "node-a", RequestedAt: stamp,
		})).To(Succeed())
		updated, err := integrationStore.Queries.FinishDeploymentRun(ctx, storedb.FinishDeploymentRunParams{
			Status: "failed", DryRun: pgtype.Bool{Bool: false, Valid: true},
			ErrorMessage: text("node-b rejected the assignment"),
			FinishedAt:   timestamp(requestedAt.Add(2 * time.Second)), RunID: "deployment-run-2",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(updated).To(Equal(int64(1)))

		rows, err := integrationStore.Queries.ListDeploymentRolloutRows(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(2))
		Expect([]string{rows[0].LatestNodeID, rows[1].LatestNodeID}).To(Equal([]string{"node-a", "node-b"}))
		Expect([]string{rows[0].LatestStatus, rows[1].LatestStatus}).To(ConsistOf("succeeded", "failed"))
	})

	It("keeps the pre-002 deployment-run insert compatible after migration", func(ctx SpecContext) {
		compatibilityAt := requestedAt.Add(10 * time.Minute)
		stamp := timestamp(compatibilityAt)
		Expect(integrationStore.Queries.UpsertDeployment(ctx, storedb.UpsertDeploymentParams{
			DeploymentID: "hello", AppRevisionID: "hello-revision", ProjectName: "hello",
			WorkspacePath: "apps/hello", DesiredState: "stopped", PlacementMode: "node",
			ExactNodeID: text("node-a"), Replicas: 1, Stateful: true,
			CreatedAt: stamp, UpdatedAt: stamp,
		})).To(Succeed())

		// This is the exact column list emitted by Core before migration 002.
		_, err := integrationStore.pool.Exec(ctx, `
INSERT INTO candaceos_deployment_runs (
    run_id, deployment_id, app_revision_id, node_id, warden_term, leader_id,
    status, requested_at
) VALUES
    ('legacy-stop-node-a', 'hello', 'hello-revision', 'node-a', 7, 'node-a', 'running', $1),
    ('legacy-stop-node-b', 'hello', 'hello-revision', 'node-b', 7, 'node-a', 'running', $1)`, compatibilityAt)
		Expect(err).NotTo(HaveOccurred())

		rows, err := integrationStore.Queries.ListDeploymentRolloutRows(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(2), "both legacy inserts must remain grouped as one rollout")
		Expect([]string{rows[0].LatestNodeID, rows[1].LatestNodeID}).To(Equal([]string{"node-a", "node-b"}))
		Expect([]string{rows[0].LatestDesiredState, rows[1].LatestDesiredState}).To(Equal([]string{"running", "running"}))

		var rolloutCount int
		Expect(integrationStore.pool.QueryRow(ctx, `
SELECT count(DISTINCT rollout_id)
FROM candaceos_deployment_runs
WHERE run_id IN ('legacy-stop-node-a', 'legacy-stop-node-b')`).Scan(&rolloutCount)).To(Succeed())
		Expect(rolloutCount).To(Equal(1))

		var parentCount int
		Expect(integrationStore.pool.QueryRow(ctx, `
SELECT count(*)
FROM candaceos_deployment_rollouts AS rollout
JOIN candaceos_deployment_runs AS run USING (rollout_id)
WHERE run.run_id IN ('legacy-stop-node-a', 'legacy-stop-node-b')`).Scan(&parentCount)).To(Succeed())
		Expect(parentCount).To(Equal(2), "both legacy children must reference the persisted rollout parent")

		for _, runID := range []string{"legacy-stop-node-a", "legacy-stop-node-b"} {
			updated, finishErr := integrationStore.Queries.FinishDeploymentRun(ctx, storedb.FinishDeploymentRunParams{
				RunID: runID, Status: "succeeded",
				DryRun:     pgtype.Bool{Bool: false, Valid: true},
				FinishedAt: timestamp(compatibilityAt.Add(time.Second)),
			})
			Expect(finishErr).NotTo(HaveOccurred())
			Expect(updated).To(Equal(int64(1)))
		}
	})

	It("keeps a node possibly active until a successful live stop proves otherwise", func(ctx SpecContext) {
		baseAt := requestedAt.Add(20 * time.Minute)
		Expect(integrationStore.Queries.UpsertDeployment(ctx, storedb.UpsertDeploymentParams{
			DeploymentID: "active-semantics", AppRevisionID: "hello-revision", ProjectName: "active-semantics",
			WorkspacePath: "apps/hello", DesiredState: "stopped", PlacementMode: "node",
			ExactNodeID: text("node-stopped"), Replicas: 1, Stateful: true,
			CreatedAt: timestamp(baseAt), UpdatedAt: timestamp(baseAt),
		})).To(Succeed())

		type attempt struct {
			runID, nodeID, desiredState, status string
			dryRun                              bool
			leaveRunning                        bool
		}
		attempts := []attempt{
			{runID: "active-stopped", nodeID: "node-stopped", desiredState: "stopped", status: "succeeded"},
			{runID: "active-failed-stop", nodeID: "node-failed-stop", desiredState: "stopped", status: "failed"},
			{runID: "active-running", nodeID: "node-running", desiredState: "running", status: "succeeded"},
			{runID: "active-incomplete", nodeID: "node-incomplete", desiredState: "stopped", leaveRunning: true},
			{runID: "active-dry-prior", nodeID: "node-dry", desiredState: "running", status: "succeeded"},
			{runID: "active-dry-stop", nodeID: "node-dry", desiredState: "stopped", status: "succeeded", dryRun: true},
		}
		for index, candidate := range attempts {
			at := baseAt.Add(time.Duration(index) * time.Second)
			Expect(integrationStore.Queries.CreateDeploymentRun(ctx, storedb.CreateDeploymentRunParams{
				RunID: candidate.runID, RolloutID: "rollout-" + candidate.runID,
				DeploymentID: "active-semantics", AppRevisionID: "hello-revision",
				NodeID: candidate.nodeID, DesiredState: candidate.desiredState,
				WardenTerm: 7, LeaderID: "node-a", RequestedAt: timestamp(at),
			})).To(Succeed())
			if candidate.leaveRunning {
				continue
			}
			updated, err := integrationStore.Queries.FinishDeploymentRun(ctx, storedb.FinishDeploymentRunParams{
				RunID: candidate.runID, Status: candidate.status,
				DryRun: pgtype.Bool{Bool: candidate.dryRun, Valid: true}, FinishedAt: timestamp(at.Add(time.Second)),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(updated).To(Equal(int64(1)))
		}

		nodeIDs, err := integrationStore.Queries.ListPossiblyActiveDeploymentNodes(ctx, "active-semantics")
		Expect(err).NotTo(HaveOccurred())
		Expect(nodeIDs).To(Equal([]string{"node-dry", "node-failed-stop", "node-incomplete", "node-running"}))

		// Keep the broader suite independent of startup-recovery ordering while
		// preserving the same conservative possibly-active result.
		updated, err := integrationStore.Queries.FinishDeploymentRun(ctx, storedb.FinishDeploymentRunParams{
			RunID: "active-incomplete", Status: "failed", DryRun: pgtype.Bool{Bool: false, Valid: false},
			FinishedAt: timestamp(baseAt.Add(time.Minute)),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(updated).To(Equal(int64(1)))
	})

	It("persists approval decisions with first-resolution-wins semantics", func(ctx SpecContext) {
		stamp := timestamp(requestedAt.Add(time.Minute))
		Expect(integrationStore.Queries.CreateRun(ctx, storedb.CreateRunParams{
			RunID: "operator-run-1", SessionID: "copilot-session-1", Title: "Deploy hello",
			Prompt: "deploy hello", Status: "running", StartedAt: stamp,
		})).To(Succeed())
		Expect(integrationStore.Queries.CreateApproval(ctx, storedb.CreateApprovalParams{
			ApprovalID: "approval-1", RunID: text("operator-run-1"), ToolCallID: text("tool-call-1"),
			RequestKind: "deploy", Title: "Deploy hello", Detail: "hello to node-a", Risk: "high",
			PayloadSha256: "abcdef0123456789", RequestedAt: stamp,
			ExpiresAt: timestamp(requestedAt.Add(2 * time.Minute)),
		})).To(Succeed())

		pending, err := integrationStore.Queries.ListPendingApprovals(ctx, stamp)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].PayloadSha256).To(Equal("abcdef0123456789"))

		resolution := storedb.ResolveApprovalParams{
			Status: "approved", ResolvedAt: timestamp(requestedAt.Add(90 * time.Second)),
			ResolvedBy: text("owner"), ApprovalID: "approval-1",
		}
		updated, err := integrationStore.Queries.ResolveApproval(ctx, resolution)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated).To(Equal(int64(1)))
		updated, err = integrationStore.Queries.ResolveApproval(ctx, resolution)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated).To(BeZero())

		finishedAt := timestamp(requestedAt.Add(2 * time.Minute))
		updated, err = integrationStore.Queries.UpdateRunStatus(ctx, storedb.UpdateRunStatusParams{
			Status: "succeeded", FinishedAt: finishedAt, RunID: "operator-run-1",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(updated).To(Equal(int64(1)))
		run, err := integrationStore.Queries.GetRun(ctx, "operator-run-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(run.Status).To(Equal("succeeded"))
		Expect(run.FinishedAt.Valid).To(BeTrue())
		Expect(run.FinishedAt.Time.Equal(finishedAt.Time)).To(BeTrue())
	})

	It("rolls back all writes when a typed transaction fails", func(ctx SpecContext) {
		sentinel := errors.New("cancel intent")
		err := integrationStore.WithTx(ctx, func(queries *storedb.Queries) error {
			if err := queries.CreateRun(ctx, storedb.CreateRunParams{
				RunID: "rolled-back-run", SessionID: "session", Title: "Do not keep",
				Prompt: "rollback", Status: "running", StartedAt: timestamp(requestedAt),
			}); err != nil {
				return err
			}
			return sentinel
		})
		Expect(err).To(MatchError(sentinel))

		_, err = integrationStore.Queries.GetRun(ctx, "rolled-back-run")
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
	})
})

var _ = Describe("startup recovery", Ordered, func() {
	var recoveryAt time.Time

	BeforeAll(func() {
		recoveryAt = time.Date(2026, time.August, 17, 15, 0, 0, 0, time.UTC)
	})

	It("requires an explicit interruption timestamp", func(ctx SpecContext) {
		result, err := integrationStore.RecoverInterruptedOperatorWork(ctx, time.Time{})
		Expect(err).To(MatchError("recovering interrupted operator work: interruption time is required"))
		Expect(result).To(Equal(StartupRecovery{}))
	})

	It("atomically terminalizes stale work, writes one receipt each, and is idempotent", func(ctx SpecContext) {
		openRuns := []struct {
			id     string
			status string
		}{
			{id: "restart-run-queued", status: "queued"},
			{id: "restart-run-running", status: "running"},
			{id: "restart-run-waiting", status: "waiting"},
		}
		terminalRuns := []struct {
			id     string
			status string
		}{
			{id: "restart-run-succeeded", status: "succeeded"},
			{id: "restart-run-failed", status: "failed"},
			{id: "restart-run-aborted", status: "aborted"},
		}

		err := integrationStore.WithTx(ctx, func(queries *storedb.Queries) error {
			for _, run := range append(openRuns, terminalRuns...) {
				if err := queries.CreateRun(ctx, storedb.CreateRunParams{
					RunID: run.id, SessionID: "restart-session", Title: "Restart test",
					Prompt: "survive restart", Status: run.status,
					StartedAt: timestamp(recoveryAt.Add(-time.Minute)),
				}); err != nil {
					return fmt.Errorf("creating %s run %q: %w", run.status, run.id, err)
				}
			}
			if err := queries.CreateRun(ctx, storedb.CreateRunParams{
				RunID: "restart-run-clock-skewed", SessionID: "restart-session", Title: "Clock-skewed run",
				Prompt: "still stale at startup", Status: "running", StartedAt: timestamp(recoveryAt.Add(time.Minute)),
			}); err != nil {
				return fmt.Errorf("creating future run: %w", err)
			}
			if err := queries.UpsertAppRevision(ctx, storedb.UpsertAppRevisionParams{
				AppRevisionID: "restart-deployment-revision", AppName: "restart-deployment",
				SourceRepository: "https://example.invalid/restart-deployment.git",
				SourceRevision:   "restart-deployment-sha", SourceSha256: "sha256:restart-deployment",
				ComposeFile: "apps/restart-deployment/compose.yaml",
				CreatedAt:   timestamp(recoveryAt.Add(-time.Minute)),
			}); err != nil {
				return fmt.Errorf("creating restart deployment revision: %w", err)
			}
			if err := queries.UpsertDeployment(ctx, storedb.UpsertDeploymentParams{
				DeploymentID: "restart-deployment", AppRevisionID: "restart-deployment-revision",
				ProjectName: "restart-deployment", WorkspacePath: "apps/restart-deployment",
				DesiredState: "running", PlacementMode: "node", ExactNodeID: text("restart-node"),
				Replicas: 1, Stateful: true, CreatedAt: timestamp(recoveryAt.Add(-time.Minute)),
				UpdatedAt: timestamp(recoveryAt.Add(-time.Minute)),
			}); err != nil {
				return fmt.Errorf("creating restart deployment: %w", err)
			}
			if err := queries.CreateDeploymentRun(ctx, storedb.CreateDeploymentRunParams{
				RunID: "restart-deployment-run", RolloutID: "restart-deployment-rollout",
				DeploymentID: "restart-deployment", AppRevisionID: "restart-deployment-revision",
				NodeID: "restart-node", DesiredState: "running", WardenTerm: 1,
				LeaderID: "restart-node", RequestedAt: timestamp(recoveryAt.Add(-time.Minute)),
			}); err != nil {
				return fmt.Errorf("creating interrupted deployment run: %w", err)
			}

			approvals := []storedb.CreateApprovalParams{
				{
					ApprovalID: "restart-approval-bound", RunID: text("restart-run-waiting"),
					RequestKind: "deploy", Title: "Deploy", Detail: "deploy stale run", Risk: "high",
					PayloadSha256: "bound-payload-sha", RequestedAt: timestamp(recoveryAt.Add(-time.Minute)),
					ExpiresAt: timestamp(recoveryAt.Add(time.Hour)),
				},
				{
					ApprovalID: "restart-approval-orphan", RequestKind: "shell", Title: "Run shell",
					Detail: "stale standalone approval", Risk: "medium", PayloadSha256: "orphan-payload-sha",
					RequestedAt: timestamp(recoveryAt.Add(-time.Minute)), ExpiresAt: timestamp(recoveryAt.Add(time.Hour)),
				},
				{
					ApprovalID: "restart-approval-clock-skewed", RequestKind: "deploy", Title: "Clock-skewed approval",
					Detail: "still stale at startup", Risk: "low", PayloadSha256: "clock-skewed-payload-sha",
					RequestedAt: timestamp(recoveryAt.Add(time.Minute)), ExpiresAt: timestamp(recoveryAt.Add(time.Hour)),
				},
				{
					ApprovalID: "restart-approval-approved", RequestKind: "deploy", Title: "Already approved",
					Detail: "terminal", Risk: "low", PayloadSha256: "approved-payload-sha",
					RequestedAt: timestamp(recoveryAt.Add(-time.Minute)), ExpiresAt: timestamp(recoveryAt.Add(time.Hour)),
				},
			}
			for _, approval := range approvals {
				if err := queries.CreateApproval(ctx, approval); err != nil {
					return fmt.Errorf("creating approval %q: %w", approval.ApprovalID, err)
				}
			}
			rows, err := queries.ResolveApproval(ctx, storedb.ResolveApprovalParams{
				Status: "approved", ResolvedAt: timestamp(recoveryAt.Add(-30 * time.Second)),
				ResolvedBy: text("owner"), ApprovalID: "restart-approval-approved",
			})
			if err != nil {
				return fmt.Errorf("resolving terminal approval: %w", err)
			}
			if rows != 1 {
				return fmt.Errorf("resolving terminal approval: expected one row, updated %d", rows)
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		beforeReceipts, err := integrationStore.Queries.ListRecentReceipts(ctx, 100)
		Expect(err).NotTo(HaveOccurred())
		recovery, err := integrationStore.RecoverInterruptedOperatorWork(ctx, recoveryAt)
		Expect(err).NotTo(HaveOccurred())
		Expect(recovery.InterruptedRuns).To(Equal(4))
		Expect(recovery.InterruptedDeploymentRuns).To(Equal(1))
		Expect(recovery.ExpiredApprovals).To(Equal(3))
		Expect(recovery.ReceiptIDs).To(HaveLen(8))

		for _, expected := range openRuns {
			run, err := integrationStore.Queries.GetRun(ctx, expected.id)
			Expect(err).NotTo(HaveOccurred())
			Expect(run.Status).To(Equal("failed"))
			Expect(run.FinishedAt.Valid).To(BeTrue())
			Expect(run.FinishedAt.Time.Equal(recoveryAt)).To(BeTrue())
		}
		for _, expected := range terminalRuns {
			run, err := integrationStore.Queries.GetRun(ctx, expected.id)
			Expect(err).NotTo(HaveOccurred())
			Expect(run.Status).To(Equal(expected.status))
			Expect(run.FinishedAt.Valid).To(BeFalse())
		}
		clockSkewedRun, err := integrationStore.Queries.GetRun(ctx, "restart-run-clock-skewed")
		Expect(err).NotTo(HaveOccurred())
		Expect(clockSkewedRun.Status).To(Equal("failed"))
		Expect(clockSkewedRun.FinishedAt.Valid).To(BeTrue())
		Expect(clockSkewedRun.FinishedAt.Time.Equal(recoveryAt)).To(BeTrue())

		var deploymentStatus, deploymentError string
		var deploymentDryRun pgtype.Bool
		var deploymentFinishedAt pgtype.Timestamptz
		Expect(integrationStore.pool.QueryRow(ctx, `
SELECT status, dry_run, error_message, finished_at
FROM candaceos_deployment_runs
WHERE run_id = 'restart-deployment-run'`).Scan(
			&deploymentStatus, &deploymentDryRun, &deploymentError, &deploymentFinishedAt,
		)).To(Succeed())
		Expect(deploymentStatus).To(Equal("failed"))
		Expect(deploymentDryRun.Valid).To(BeFalse(), "unknown node outcomes must not become a definitive live result")
		Expect(deploymentError).To(Equal("CandaceOS Core restarted before the node outcome was recorded"))
		Expect(deploymentFinishedAt.Valid).To(BeTrue())
		Expect(deploymentFinishedAt.Time.Equal(recoveryAt)).To(BeTrue())
		possiblyActive, err := integrationStore.Queries.ListPossiblyActiveDeploymentNodes(ctx, "restart-deployment")
		Expect(err).NotTo(HaveOccurred())
		Expect(possiblyActive).To(Equal([]string{"restart-node"}))

		approvalPayloads := map[string]string{
			"restart-approval-bound":        "bound-payload-sha",
			"restart-approval-orphan":       "orphan-payload-sha",
			"restart-approval-clock-skewed": "clock-skewed-payload-sha",
		}
		for approvalID, payloadSHA := range approvalPayloads {
			approval, err := integrationStore.Queries.GetApproval(ctx, approvalID)
			Expect(err).NotTo(HaveOccurred())
			Expect(approval.Status).To(Equal("expired"))
			Expect(approval.ResolvedBy).To(Equal(text("core-restart")))
			Expect(approval.ResolvedAt.Valid).To(BeTrue())
			Expect(approval.ResolvedAt.Time.Equal(recoveryAt)).To(BeTrue())
			Expect(approval.PayloadSha256).To(Equal(payloadSHA))
		}
		approved, err := integrationStore.Queries.GetApproval(ctx, "restart-approval-approved")
		Expect(err).NotTo(HaveOccurred())
		Expect(approved.Status).To(Equal("approved"))
		Expect(approved.ResolvedBy).To(Equal(text("owner")))

		afterReceipts, err := integrationStore.Queries.ListRecentReceipts(ctx, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(afterReceipts).To(HaveLen(len(beforeReceipts) + 8))
		recoveryReceiptIDs := make(map[int64]struct{}, len(recovery.ReceiptIDs))
		for _, receiptID := range recovery.ReceiptIDs {
			recoveryReceiptIDs[receiptID] = struct{}{}
		}
		seenRecoveryReceipts := 0
		for _, receipt := range afterReceipts {
			if _, ok := recoveryReceiptIDs[receipt.ReceiptID]; !ok {
				continue
			}
			seenRecoveryReceipts++
			Expect(receipt.OccurredAt.Valid).To(BeTrue())
			Expect(receipt.OccurredAt.Time.Equal(recoveryAt)).To(BeTrue())
			switch receipt.EntityType {
			case "operator_run":
				Expect(receipt.Kind).To(Equal("run.interrupted"))
				Expect(receipt.Summary).To(Equal("Agent run interrupted by CandaceOS Core restart"))
				Expect(receipt.PayloadSha256.Valid).To(BeFalse())
			case "deployment_run":
				Expect(receipt.EntityID).To(Equal("restart-deployment-run"))
				Expect(receipt.Kind).To(Equal("deployment.interrupted"))
				Expect(receipt.PayloadSha256.Valid).To(BeFalse())
			case "approval":
				Expect(receipt.Kind).To(Equal("approval.expired"))
				Expect(receipt.PayloadSha256.Valid).To(BeTrue())
				Expect(receipt.PayloadSha256.String).To(Equal(approvalPayloads[receipt.EntityID]))
			default:
				Fail("unexpected startup recovery receipt entity " + receipt.EntityType)
			}
		}
		Expect(seenRecoveryReceipts).To(Equal(8))

		replayed, err := integrationStore.RecoverInterruptedOperatorWork(ctx, recoveryAt)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed).To(Equal(StartupRecovery{ReceiptIDs: []int64{}}))
		replayedReceipts, err := integrationStore.Queries.ListRecentReceipts(ctx, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayedReceipts).To(HaveLen(len(afterReceipts)))

		// Do not make the outcome of this top-level container depend on
		// Ginkgo's randomized ordering relative to the persistence container.
		_, err = integrationStore.pool.Exec(ctx, `
DELETE FROM candaceos_activity_receipts
WHERE entity_type = 'deployment_run' AND entity_id = 'restart-deployment-run'`)
		Expect(err).NotTo(HaveOccurred())
		_, err = integrationStore.pool.Exec(ctx, `
DELETE FROM candaceos_deployment_runs WHERE run_id = 'restart-deployment-run'`)
		Expect(err).NotTo(HaveOccurred())
		_, err = integrationStore.pool.Exec(ctx, `
DELETE FROM candaceos_deployment_rollouts WHERE rollout_id = 'restart-deployment-rollout'`)
		Expect(err).NotTo(HaveOccurred())
		_, err = integrationStore.pool.Exec(ctx, `
DELETE FROM candaceos_deployments WHERE deployment_id = 'restart-deployment'`)
		Expect(err).NotTo(HaveOccurred())
		_, err = integrationStore.pool.Exec(ctx, `
DELETE FROM candaceos_app_revisions WHERE app_revision_id = 'restart-deployment-revision'`)
		Expect(err).NotTo(HaveOccurred())
	})
})

func isolatedStoreDatabaseURL(databaseURL, schemaName string) (string, string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", "", fmt.Errorf("parse CandaceOS store integration database URL: %w", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", "", fmt.Errorf("CandaceOS store integration database URL must use postgres or postgresql")
	}
	databaseName := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if decoded, decodeErr := url.PathUnescape(databaseName); decodeErr == nil {
		databaseName = decoded
	}
	if !strings.HasSuffix(databaseName, "_test") {
		return "", "", fmt.Errorf("CandaceOS store integration database name must end in _test, got %q", databaseName)
	}
	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()
	return parsed.String(), databaseName, nil
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func text(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func sortedTableNames(tables map[string]struct{}) []string {
	names := make([]string, 0, len(tables))
	for table := range tables {
		names = append(names, table)
	}
	slices.Sort(names)
	return names
}
