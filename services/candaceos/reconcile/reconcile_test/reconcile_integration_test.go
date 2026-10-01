package reconcile_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos"
	"github.com/candacelabs/csf/services/candaceos/agentclient"
	"github.com/candacelabs/csf/services/candaceos/fleet"
	"github.com/candacelabs/csf/services/candaceos/internal/storedb"
	"github.com/candacelabs/csf/services/candaceos/reconcile"
	"github.com/candacelabs/csf/services/candaceos/store"
)

const reconcileTestDatabaseURLEnv = "CANDACEOS_RECONCILE_TEST_DATABASE_URL"

func TestReconcileIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CandaceOS Reconcile Integration Suite")
}

var _ = Describe("dry-run reconciliation integration", func() {
	It("persists immutable intent, the completed run, its receipt, and the exact Warden fence", func(ctx SpecContext) {
		databaseURL := strings.TrimSpace(os.Getenv(reconcileTestDatabaseURLEnv))
		if databaseURL == "" {
			Skip("set " + reconcileTestDatabaseURLEnv + " to run PostgreSQL reconciliation specs")
		}

		schemaName := fmt.Sprintf("candaceos_reconcile_test_%d_%d", os.Getpid(), time.Now().UnixNano())
		isolatedURL, err := isolatedReconcileDatabaseURL(databaseURL, schemaName)
		Expect(err).NotTo(HaveOccurred())

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

		controlStore, err := store.OpenControlStore(ctx, isolatedURL)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(controlStore.Close)

		verificationPool, err := pgxpool.New(ctx, isolatedURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(verificationPool.Ping(ctx)).To(Succeed())
		DeferCleanup(verificationPool.Close)

		workspace := GinkgoT().TempDir()
		writeTestFile(filepath.Join(workspace, "apps", "hello", "compose.yaml"), `services:
  hello:
    image: busybox:1.37.0
`)
		runIntegrationGit(ctx, workspace, "init", "-q", "-b", "main")
		runIntegrationGit(ctx, workspace, "config", "user.name", "CandaceOS Test")
		runIntegrationGit(ctx, workspace, "config", "user.email", "candaceos-test@example.invalid")
		runIntegrationGit(ctx, workspace, "add", "--", "apps/hello/compose.yaml")
		runIntegrationGit(ctx, workspace, "commit", "-q", "-m", "test: add hello app")
		runIntegrationGit(ctx, workspace, "remote", "add", "origin",
			"https://test-user:test-password@example.invalid/candace/hello.git")
		revisionSHA := runIntegrationGit(ctx, workspace, "rev-parse", "HEAD")

		wardenServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			defer GinkgoRecover()
			Expect(request.Method).To(Equal(http.MethodGet))
			Expect(request.URL.Path).To(Equal("/api/status"))
			response.Header().Set("Content-Type", "application/json")
			_, writeErr := io.WriteString(response, `{
  "view": {
    "self": "node-a",
    "role": "leader",
    "term": 42,
    "leader_id": "node-a",
    "authoritative": true,
    "updated_at": "2026-08-17T12:00:00Z",
    "membership": {
      "version": 1,
      "created_in_term": 42,
      "voters": [{"id": "node-a", "addr": "127.0.0.1:7717"}]
    },
    "peers": [{
      "node": {"id": "node-a", "addr": "127.0.0.1:7717"},
      "status": "alive",
      "last_seen": "2026-08-17T12:00:00Z",
      "member": "voter"
    }]
  },
  "incidents": []
}`)
			Expect(writeErr).NotTo(HaveOccurred())
		}))
		DeferCleanup(wardenServer.Close)

		fleetClient, err := fleet.NewWardenClient(wardenServer.URL, wardenServer.Client())
		Expect(err).NotTo(HaveOccurred())
		wardenSnapshot, err := fleetClient.Refresh(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(wardenSnapshot.CanMutate()).To(BeTrue())

		var requestMu sync.Mutex
		var receivedRequest *candaceosv1.ReconcileRequest
		var cancelNextAssignment context.CancelFunc
		agentServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			defer GinkgoRecover()
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer reconcile-test-token"))
			switch request.URL.Path {
			case "/healthz":
				Expect(request.Method).To(Equal(http.MethodGet))
				writeReconcileProto(response, &candaceosv1.HealthResponse{
					Status: "ok", NodeId: "node-a", DryRun: true,
				})
			case "/v1/assignment":
				Expect(request.Method).To(Equal(http.MethodPut))
				Expect(request.Header.Get("Content-Type")).To(Equal("application/json"))
				body, readErr := io.ReadAll(request.Body)
				Expect(readErr).NotTo(HaveOccurred())
				var decoded candaceosv1.ReconcileRequest
				Expect((protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, &decoded)).To(Succeed())
				requestMu.Lock()
				receivedRequest = proto.Clone(&decoded).(*candaceosv1.ReconcileRequest)
				cancelAssignment := cancelNextAssignment
				cancelNextAssignment = nil
				requestMu.Unlock()
				if cancelAssignment != nil {
					cancelAssignment()
					<-request.Context().Done()
					return
				}
				writeReconcileProto(response, &candaceosv1.ReconcileResponse{
					Fence:      proto.Clone(decoded.GetFence()).(*candaceosv1.Fence),
					Assignment: proto.Clone(decoded.GetAssignment()).(*candaceosv1.Assignment),
					DryRun:     true,
					Commands:   []*candaceosv1.Command{{Argv: []string{"docker", "compose", "config", "--quiet"}}},
					UpdatedAt:  timestamppb.New(time.Date(2026, time.August, 17, 12, 0, 1, 0, time.UTC)),
				})
			default:
				http.NotFound(response, request)
			}
		}))
		DeferCleanup(agentServer.Close)

		agentClient, err := agentclient.NewNodeAgentClient(agentServer.URL, "reconcile-test-token", 8094, agentServer.Client())
		Expect(err).NotTo(HaveOccurred())
		reconciler, err := reconcile.NewService(
			workspace,
			map[string]map[string]string{"node-a": {"environment": "integration"}},
			fleetClient,
			agentClient,
			controlStore,
		)
		Expect(err).NotTo(HaveOccurred())
		runInput := &candaceosv1.ReconcileIntent{
			App: "hello", Project: "hello", Path: "apps/hello",
			DesiredState:  candaceosv1.DesiredState_DESIRED_STATE_RUNNING,
			PlacementMode: candaceosv1.PlacementMode_PLACEMENT_MODE_EXACT_NODE,
			NodeId:        "node-a", Stateful: true,
		}
		approvedRunRevision, err := reconciler.Prepare(ctx, runInput)
		Expect(err).NotTo(HaveOccurred())
		Expect(approvedRunRevision.GetSourceRevision()).To(Equal(revisionSHA))
		Expect(approvedRunRevision.GetContentDigest()).To(MatchRegexp(`^sha256:[0-9a-f]{64}$`))
		invalidLabelsReconciler, err := reconcile.NewService(
			workspace,
			map[string]map[string]string{"node-a": {"bad key": "integration"}},
			fleetClient,
			agentClient,
			controlStore,
		)
		Expect(err).NotTo(HaveOccurred())
		_, err = invalidLabelsReconciler.ReconcileApproved(ctx, runInput, approvedRunRevision)
		Expect(errors.Is(err, candaceos.ErrInvalidClusterSnapshot)).To(BeTrue())
		Expect(errors.Is(err, candaceos.ErrInvalidNode)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("refinement violated")))

		result, err := reconciler.ReconcileApproved(ctx, runInput, approvedRunRevision)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.GetDeploymentId()).To(Equal("hello"))
		Expect(result.GetRevisionId()).To(Equal(approvedRunRevision.GetId()))
		Expect(result.GetRevisionId()).To(Equal("hello-" + revisionSHA))
		Expect(result.GetRunIds()).To(HaveLen(1))
		Expect(result.GetNodeIds()).To(Equal([]string{"node-a"}))
		Expect(result.GetReceiptIds()).To(HaveLen(1))
		Expect(result.GetDryRun()).To(BeTrue())

		requestMu.Lock()
		capturedRequest := proto.Clone(receivedRequest).(*candaceosv1.ReconcileRequest)
		requestMu.Unlock()
		Expect(capturedRequest.GetFence()).To(Equal(&candaceosv1.Fence{Term: 42, LeaderId: "node-a"}))
		Expect(capturedRequest.GetAssignment()).To(Equal(&candaceosv1.Assignment{
			App: "hello", Project: "hello", Path: "apps/hello",
			DesiredState:   candaceosv1.DesiredState_DESIRED_STATE_RUNNING,
			SourceRevision: approvedRunRevision.GetSourceRevision(),
			ContentSha256:  approvedRunRevision.GetContentDigest(),
		}))

		deployments, err := controlStore.Queries.ListDeploymentRolloutRows(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(deployments).To(HaveLen(1))
		deployment := deployments[0]
		Expect(deployment.DeploymentID).To(Equal("hello"))
		Expect(deployment.AppRevisionID).To(Equal(result.GetRevisionId()))
		Expect(deployment.SourceRevision).To(Equal(approvedRunRevision.GetSourceRevision()))
		Expect(deployment.SourceSha256).To(Equal(approvedRunRevision.GetContentDigest()))
		Expect(deployment.ComposeFile).To(Equal(approvedRunRevision.GetComposePath()))
		Expect(deployment.DesiredState).To(Equal("running"))
		Expect(deployment.PlacementMode).To(Equal("node"))
		Expect(deployment.ExactNodeID).To(Equal(pgtype.Text{String: "node-a", Valid: true}))
		Expect(deployment.Stateful).To(BeTrue())
		Expect(deployment.LatestNodeID).To(Equal("node-a"))
		Expect(deployment.LatestStatus).To(Equal("succeeded"))
		Expect(deployment.LatestDryRun).To(BeTrue())

		var persistedSource string
		Expect(verificationPool.QueryRow(ctx, `
SELECT source_repository
FROM candaceos_app_revisions
WHERE app_revision_id = $1`, result.GetRevisionId()).Scan(&persistedSource)).To(Succeed())
		Expect(persistedSource).To(Equal("https://example.invalid/candace/hello.git"))
		Expect(persistedSource).NotTo(ContainSubstring("test-password"))

		var persistedRunID, persistedNodeID, persistedLeaderID, persistedStatus string
		var persistedTerm int64
		var persistedDryRun pgtype.Bool
		Expect(verificationPool.QueryRow(ctx, `
SELECT run_id, node_id, warden_term, leader_id, status, dry_run
FROM candaceos_deployment_runs
WHERE run_id = $1`, result.GetRunIds()[0]).Scan(
			&persistedRunID, &persistedNodeID, &persistedTerm,
			&persistedLeaderID, &persistedStatus, &persistedDryRun,
		)).To(Succeed())
		Expect(persistedRunID).To(Equal(result.GetRunIds()[0]))
		Expect(persistedNodeID).To(Equal("node-a"))
		Expect(persistedTerm).To(Equal(int64(42)))
		Expect(persistedLeaderID).To(Equal("node-a"))
		Expect(persistedStatus).To(Equal("succeeded"))
		Expect(persistedDryRun).To(Equal(pgtype.Bool{Bool: true, Valid: true}))

		receipts, err := controlStore.Queries.ListRecentReceipts(ctx, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipts).To(HaveLen(1))
		Expect(receipts[0].ReceiptID).To(Equal(result.GetReceiptIds()[0]))
		Expect(receipts[0].EntityType).To(Equal("deployment_run"))
		Expect(receipts[0].EntityID).To(Equal(result.GetRunIds()[0]))
		Expect(receipts[0].Kind).To(Equal("deployment.dry_run"))

		// Model the previously accepted live placement separately from the dry-run
		// above, then remove its source before issuing the stop.
		priorLiveAt := time.Now().UTC()
		stamp := pgtype.Timestamptz{Time: priorLiveAt, Valid: true}
		Expect(controlStore.Queries.CreateDeploymentRun(ctx, storedb.CreateDeploymentRunParams{
			RunID: "prior-live-run", RolloutID: "prior-live-rollout",
			DeploymentID: "hello", AppRevisionID: result.GetRevisionId(),
			NodeID: "node-a", DesiredState: "running", WardenTerm: 42,
			LeaderID: "node-a", RequestedAt: stamp,
		})).To(Succeed())
		updated, err := controlStore.Queries.FinishDeploymentRun(ctx, storedb.FinishDeploymentRunParams{
			RunID: "prior-live-run", Status: "succeeded",
			DryRun:     pgtype.Bool{Bool: false, Valid: true},
			FinishedAt: pgtype.Timestamptz{Time: priorLiveAt.Add(time.Microsecond), Valid: true},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(updated).To(Equal(int64(1)))
		Expect(os.RemoveAll(filepath.Join(workspace, "apps", "hello"))).To(Succeed())

		stopInput := &candaceosv1.ReconcileIntent{
			App: "hello", Project: "hello", Path: "apps/hello",
			DesiredState: candaceosv1.DesiredState_DESIRED_STATE_STOPPED,
			// Stop uses the persisted placement rather than trusting a new one.
			PlacementMode: candaceosv1.PlacementMode_PLACEMENT_MODE_LEADER,
		}
		approvedStopRevision, err := reconciler.Prepare(ctx, stopInput)
		Expect(err).NotTo(HaveOccurred())
		Expect(approvedStopRevision).To(Equal(approvedRunRevision))
		stopResult, err := reconciler.ReconcileApproved(ctx, stopInput, approvedStopRevision)
		Expect(err).NotTo(HaveOccurred())
		Expect(stopResult.GetRevisionId()).To(Equal(result.GetRevisionId()))
		Expect(stopResult.GetNodeIds()).To(Equal([]string{"node-a"}))
		requestMu.Lock()
		capturedStop := proto.Clone(receivedRequest).(*candaceosv1.ReconcileRequest)
		requestMu.Unlock()
		Expect(capturedStop.GetAssignment()).To(Equal(&candaceosv1.Assignment{
			App: "hello", Project: "hello", Path: "apps/hello",
			DesiredState:   candaceosv1.DesiredState_DESIRED_STATE_STOPPED,
			SourceRevision: approvedStopRevision.GetSourceRevision(),
			ContentSha256:  approvedStopRevision.GetContentDigest(),
		}))

		// Record the stop as a real node outcome, then prove that repeating the
		// approved stop still creates a durable rollout and receipt even though
		// there is no node left to call.
		definitiveStopTime := time.Now().UTC()
		definitiveStopAt := pgtype.Timestamptz{Time: definitiveStopTime, Valid: true}
		Expect(controlStore.Queries.CreateDeploymentRun(ctx, storedb.CreateDeploymentRunParams{
			RunID: "definitive-stop-run", RolloutID: "definitive-stop-rollout",
			DeploymentID: "hello", AppRevisionID: result.GetRevisionId(),
			NodeID: "node-a", DesiredState: "stopped", WardenTerm: 42,
			LeaderID: "node-a", RequestedAt: definitiveStopAt,
		})).To(Succeed())
		updated, err = controlStore.Queries.FinishDeploymentRun(ctx, storedb.FinishDeploymentRunParams{
			RunID: "definitive-stop-run", Status: "succeeded",
			DryRun:     pgtype.Bool{Bool: false, Valid: true},
			FinishedAt: pgtype.Timestamptz{Time: definitiveStopTime.Add(time.Microsecond), Valid: true},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(updated).To(Equal(int64(1)))

		noOpStop, err := reconciler.ReconcileApproved(ctx, stopInput, approvedStopRevision)
		Expect(err).NotTo(HaveOccurred())
		Expect(noOpStop.GetRunIds()).To(BeEmpty())
		Expect(noOpStop.GetNodeIds()).To(BeEmpty())
		Expect(noOpStop.GetReceiptIds()).To(HaveLen(1))
		Expect(noOpStop.GetDryRun()).To(BeFalse())

		deployments, err = controlStore.Queries.ListDeploymentRolloutRows(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(deployments).To(HaveLen(1))
		Expect(deployments[0].LatestRolloutID).NotTo(BeEmpty())
		Expect(deployments[0].LatestRunID).To(BeEmpty())
		Expect(deployments[0].LatestDesiredState).To(Equal("stopped"))
		Expect(deployments[0].LatestStatus).To(Equal("succeeded"))
		Expect(deployments[0].LatestDryRun).To(BeFalse())
		Expect(deployments[0].PossiblyActiveNodeIds).To(BeEmpty())
		receipts, err = controlStore.Queries.ListRecentReceipts(ctx, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipts).To(ContainElement(SatisfyAll(
			WithTransform(func(receipt storedb.CandaceosActivityReceipt) int64 { return receipt.ReceiptID }, Equal(noOpStop.GetReceiptIds()[0])),
			WithTransform(func(receipt storedb.CandaceosActivityReceipt) string { return receipt.EntityType }, Equal("deployment_rollout")),
			WithTransform(func(receipt storedb.CandaceosActivityReceipt) string { return receipt.Kind }, Equal("deployment.noop")),
		)))

		// A canceled request context must not prevent terminal run state and its
		// append-only failure receipt from being recorded.
		canceledRunAt := time.Now().UTC()
		Expect(controlStore.Queries.CreateDeploymentRun(ctx, storedb.CreateDeploymentRunParams{
			RunID: "canceled-run", RolloutID: "canceled-rollout",
			DeploymentID: "hello", AppRevisionID: result.GetRevisionId(),
			NodeID: "node-a", DesiredState: "stopped", WardenTerm: 42,
			LeaderID: "node-a", RequestedAt: pgtype.Timestamptz{Time: canceledRunAt, Valid: true},
		})).To(Succeed())
		canceledContext, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)
		requestMu.Lock()
		cancelNextAssignment = cancel
		requestMu.Unlock()
		_, err = reconciler.ReconcileApproved(canceledContext, stopInput, approvedStopRevision)
		Expect(err).To(MatchError(ContainSubstring("one or more node reconciliations failed")))
		Expect(err).To(MatchError(ContainSubstring("context canceled")))

		deployments, err = controlStore.Queries.ListDeploymentRolloutRows(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(deployments).To(HaveLen(1))
		failedRunID := deployments[0].LatestRunID
		Expect(failedRunID).NotTo(BeEmpty())
		Expect(failedRunID).NotTo(Equal("canceled-run"))
		Expect(deployments[0].LatestStatus).To(Equal("failed"))
		var canceledStatus, canceledError string
		Expect(verificationPool.QueryRow(ctx, `
SELECT status, error_message
FROM candaceos_deployment_runs
WHERE run_id = $1`, failedRunID).Scan(&canceledStatus, &canceledError)).To(Succeed())
		Expect(canceledStatus).To(Equal("failed"))
		Expect(canceledError).To(ContainSubstring("context canceled"))
		receipts, err = controlStore.Queries.ListRecentReceipts(ctx, 20)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipts).To(ContainElement(SatisfyAll(
			WithTransform(func(receipt storedb.CandaceosActivityReceipt) string { return receipt.EntityID }, Equal(failedRunID)),
			WithTransform(func(receipt storedb.CandaceosActivityReceipt) string { return receipt.Kind }, Equal("deployment.failed")),
		)))
	}, NodeTimeout(time.Minute))
})

func isolatedReconcileDatabaseURL(databaseURL, schemaName string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", fmt.Errorf("parse reconciliation test database URL: %w", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", fmt.Errorf("reconciliation test database URL must use postgres or postgresql")
	}
	databaseName := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if decoded, decodeErr := url.PathUnescape(databaseName); decodeErr == nil {
		databaseName = decoded
	}
	if !strings.HasSuffix(databaseName, "_test") {
		return "", fmt.Errorf("reconciliation test database name must end in _test, got %q", databaseName)
	}
	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func runIntegrationGit(ctx context.Context, workspace string, args ...string) string {
	commandArgs := append([]string{"-C", workspace}, args...)
	output, err := exec.CommandContext(ctx, "git", commandArgs...).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git %s failed: %s", strings.Join(args, " "), output)
	return strings.TrimSpace(string(output))
}

func writeTestFile(path, contents string) {
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(contents), 0o644)).To(Succeed())
}

func writeReconcileProto(response http.ResponseWriter, message proto.Message) {
	body, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(message)
	Expect(err).NotTo(HaveOccurred())
	response.Header().Set("Content-Type", "application/json")
	_, err = response.Write(body)
	Expect(err).NotTo(HaveOccurred())
}
