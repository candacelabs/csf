package control_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/control"
	"github.com/candacelabs/csf/services/candaceos/fleet"
	"github.com/candacelabs/csf/services/candaceos/internal/storedb"
	"github.com/candacelabs/csf/services/candaceos/operator"
	"github.com/candacelabs/csf/services/candaceos/store"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

const (
	controlTestDatabaseURLEnv = "CANDACEOS_STORE_TEST_DATABASE_URL"
	// The runtime permits 15 poll intervals per real PostgreSQL transaction.
	// Leave enough transaction and heartbeat time under whole-module coverage.
	contractFleetPollInterval = time.Second
	contractHeartbeatBudget   = 45 * time.Second
)

type controlApprovalResult struct {
	resolution operator.ApprovalResolution
	err        error
}

func TestControlRuntimeContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "candaceos-core control runtime contract suite")
}

var _ = Describe("Control runtime contract", func() {
	It("projects and persists fleet, run, and approval lifecycle state", func(ctx SpecContext) {
		controlStore, verificationPool, isolatedURL := openControlContractStore(ctx)

		var authoritative atomic.Bool
		authoritative.Store(true)
		wardenServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			defer GinkgoRecover()
			Expect(request.Method).To(Equal(http.MethodGet))
			Expect(request.URL.Path).To(Equal("/api/status"))
			response.Header().Set("Content-Type", "application/json")
			_, writeErr := io.WriteString(response, controlWardenStatus(authoritative.Load()))
			Expect(writeErr).NotTo(HaveOccurred())
		}))
		DeferCleanup(wardenServer.Close)

		fleetClient, err := fleet.NewWardenClient(wardenServer.URL, wardenServer.Client())
		Expect(err).NotTo(HaveOccurred())
		workspace := GinkgoT().TempDir()
		controller, err := operator.NewController(&candaceosv1.CoreConfig{
			HarnessBackend:  candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO,
			Workspace:       workspace,
			DataDir:         filepath.Join(workspace, "data"),
			ApprovalTimeout: int64(30 * time.Second),
		}, fleetClient, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(controller.Close)

		persistence := contractPersistenceTiming()
		_, err = control.NewRuntime(nil, fleetClient, controller, nil, persistence, "test")
		Expect(err).To(MatchError("control runtime requires store, fleet, and controller"))
		_, err = control.NewRuntime(controlStore, nil, controller, nil, persistence, "test")
		Expect(err).To(MatchError("control runtime requires store, fleet, and controller"))
		_, err = control.NewRuntime(controlStore, fleetClient, nil, nil, persistence, "test")
		Expect(err).To(MatchError("control runtime requires store, fleet, and controller"))
		_, err = control.NewRuntime(controlStore, fleetClient, controller, nil, &candaceosv1.PersistenceTiming{}, "test")
		Expect(err).To(MatchError(ContainSubstring("fleet_poll_interval_nanoseconds")))

		// Core produces the snapshot, so the operator-visible identity is a
		// runtime setting rather than something the web UI discovers.
		branded, err := control.NewRuntime(
			controlStore, fleetClient, controller, nil, persistence, "contract-test",
			control.WithBrand(webui.Brand{ProductName: "Atlas", AgentName: "Scout"}),
		)
		Expect(err).NotTo(HaveOccurred())
		brandedSnapshot, err := branded.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(brandedSnapshot.GetSystem().GetName()).To(Equal("Atlas"))
		Expect(brandedSnapshot.GetSystem().GetAgentName()).To(Equal("Scout"))
		_, err = control.NewRuntime(
			controlStore, fleetClient, controller, nil, persistence, "contract-test",
			control.WithBrand(webui.Brand{Palette: webui.Palette{Canvas: "#fff; position: fixed"}}),
		)
		Expect(err).To(MatchError(webui.ErrInvalidPaletteValue))

		labels := map[string]map[string]string{
			"node-a": {"role": "worker", "gpu": "nvidia", "region": "west"},
		}
		runtime, err := control.NewRuntime(controlStore, fleetClient, controller, labels, persistence, "contract-test")
		Expect(err).NotTo(HaveOccurred())
		Expect(runtime.Health(ctx)).To(Succeed())
		Expect(runtime.Abort(ctx)).To(MatchError("no active agent run"))

		initial, err := runtime.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(initial.System).To(Equal(&candaceosv1.WebUISystem{
			Name: "CandaceOS", AgentName: "Claw", Status: "unavailable",
			Summary: "Agent runtime is starting", Version: "contract-test",
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, HarnessImplementation: "demo",
		}))
		Expect(initial.Apps).To(BeEmpty())
		Expect(initial.Activity).To(BeEmpty())

		updates, cancelSubscription := runtime.Subscribe()
		DeferCleanup(cancelSubscription)
		Expect(controller.Start(context.Background())).To(Succeed())

		observedFleet, err := fleetClient.Refresh(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(observedFleet.CanMutate()).To(BeTrue())
		firstObservation := observedFleet
		firstObservation.UpdatedAt = time.Time{}
		firstObservation.Nodes[1].LastSeen = time.Time{}
		runtime.RecordFleetContext(ctx, firstObservation)
		Expect(runtime.Health(ctx)).To(Succeed())

		nodes, err := controlStore.Queries.ListNodes(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(nodes).To(HaveLen(3))
		Expect(nodes[0].NodeID).To(Equal("node-a"))
		Expect(nodes[0].ObservedAt.Valid).To(BeTrue())
		Expect(nodes[1].LastSeenAt.Valid).To(BeFalse())

		labels["node-a"] = map[string]string{"role": "worker", "gpu": "amd"}
		runtime.RecordFleetContext(ctx, observedFleet)
		Expect(runtime.Health(ctx)).To(Succeed())
		var gpu string
		Expect(verificationPool.QueryRow(ctx,
			"SELECT label_value FROM candaceos_node_labels WHERE node_id = $1 AND label_key = $2",
			"node-a", "gpu",
		).Scan(&gpu)).To(Succeed())
		Expect(gpu).To(Equal("amd"))
		var staleLabels int
		Expect(verificationPool.QueryRow(ctx,
			"SELECT count(*) FROM candaceos_node_labels WHERE node_id = $1 AND label_key = $2",
			"node-a", "region",
		).Scan(&staleLabels)).To(Succeed())
		Expect(staleLabels).To(BeZero())

		var persistedObservedAt time.Time
		Expect(verificationPool.QueryRow(ctx,
			"SELECT observed_at FROM candaceos_nodes WHERE node_id = $1", "node-a",
		).Scan(&persistedObservedAt)).To(Succeed())
		heartbeat := observedFleet
		heartbeat.Nodes = append([]fleet.Node(nil), observedFleet.Nodes...)
		heartbeat.UpdatedAt = observedFleet.UpdatedAt.Add(time.Minute)
		for index := range heartbeat.Nodes {
			heartbeat.Nodes[index].LastSeen = heartbeat.Nodes[index].LastSeen.Add(time.Minute)
		}
		runtime.RecordFleetContext(ctx, heartbeat)
		var suppressedObservedAt time.Time
		Expect(verificationPool.QueryRow(ctx,
			"SELECT observed_at FROM candaceos_nodes WHERE node_id = $1", "node-a",
		).Scan(&suppressedObservedAt)).To(Succeed())
		Expect(suppressedObservedAt).To(Equal(persistedObservedAt),
			"poll-only timestamp changes must not force another durable transaction")

		Eventually(func() (time.Time, error) {
			runtime.RecordFleetContext(ctx, heartbeat)
			if err := runtime.Health(ctx); err != nil {
				return time.Time{}, err
			}
			var refreshedObservedAt time.Time
			err := verificationPool.QueryRow(ctx,
				"SELECT observed_at FROM candaceos_nodes WHERE node_id = $1", "node-a",
			).Scan(&refreshedObservedAt)
			return refreshedObservedAt, err
		}, contractHeartbeatBudget, 100*time.Millisecond).Should(BeTemporally("~", heartbeat.UpdatedAt, time.Microsecond),
			"the periodic heartbeat must still refresh durable fleet observations")

		healthy, err := runtime.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(healthy.System.Status).To(Equal("healthy"))
		Expect(healthy.Fleet.LeaderId).To(Equal("node-a"))
		Expect(healthy.Fleet.Term).To(Equal(uint64(12)))
		Expect(healthy.Fleet.Quorum).To(Equal(&candaceosv1.WebUIQuorum{Healthy: true, Online: 2, Required: 2}))
		Expect(healthy.Fleet.Nodes).To(HaveLen(3))
		Expect(controlNode(healthy, "node-a").Role).To(Equal("worker"),
			"dynamic Warden leadership must not replace configured deployment role")

		runID, err := runtime.Send(ctx, "Build a tiny status app")
		Expect(err).NotTo(HaveOccurred())
		Expect(runID).NotTo(BeEmpty())
		Eventually(updates).Should(Receive())
		Eventually(func() string {
			run, queryErr := controlStore.Queries.GetRun(ctx, runID)
			if queryErr != nil {
				return ""
			}
			return run.Status
		}, 5*time.Second).Should(Equal("succeeded"))
		Eventually(controller.Status).Should(Equal("idle"))

		completedRun, err := controlStore.Queries.GetRun(ctx, runID)
		Expect(err).NotTo(HaveOccurred())
		Expect(completedRun.Prompt).To(Equal("Build a tiny status app"))
		Expect(completedRun.FinishedAt.Valid).To(BeTrue())
		completedSnapshot, err := runtime.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(completedSnapshot.Run).NotTo(BeNil())
		Expect(completedSnapshot.Run.Id).To(Equal(runID))
		Expect(completedSnapshot.Run.Status).To(Equal("succeeded"))
		Expect(completedSnapshot.Run.Entries).NotTo(BeEmpty())

		abortedRunID, err := runtime.Send(ctx, "Start another demo turn")
		Expect(err).NotTo(HaveOccurred())
		Expect(runtime.Abort(ctx)).To(Succeed())
		abortedRun, err := controlStore.Queries.GetRun(ctx, abortedRunID)
		Expect(err).NotTo(HaveOccurred())
		Expect(abortedRun.Status).To(Equal("aborted"))
		Expect(abortedRun.FinishedAt.Valid).To(BeTrue())
		Eventually(controller.Status).Should(Equal("idle"))

		authoritative.Store(false)
		readOnlyFleet, err := fleetClient.Refresh(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(readOnlyFleet.CanMutate()).To(BeFalse())
		warningSnapshot, err := runtime.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(warningSnapshot.System.Status).To(Equal("warning"))

		rejectedResult := make(chan controlApprovalResult, 1)
		go func() {
			resolution, requestErr := controller.ApprovalQueue().Request(context.Background(), operator.ApprovalRequest{
				Kind: "deploy", Title: "Deploy status app", Detail: "Apply the revision", Risk: "high",
				Payload: map[string]string{"app": "status"}, RequiresFleetQuorum: true,
			})
			rejectedResult <- controlApprovalResult{resolution: resolution, err: requestErr}
		}()
		Eventually(func() int { return len(controller.ApprovalQueue().Pending()) }).Should(Equal(1))
		rejectedApproval := controller.ApprovalQueue().Pending()[0]
		pendingSnapshot, err := runtime.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(pendingSnapshot.Attention).To(HaveLen(1))
		Expect(pendingSnapshot.Attention[0].Id).To(Equal(rejectedApproval.ID))
		persistedApproval, err := controlStore.Queries.GetApproval(ctx, rejectedApproval.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persistedApproval.Status).To(Equal("pending"))
		Expect(persistedApproval.RunID.Valid).To(BeFalse())

		Expect(runtime.ResolveApproval(rejectedApproval.ID, "approve")).To(MatchError(ContainSubstring("approval blocked")))
		Expect(controller.ApprovalQueue().Pending()).To(HaveLen(1))
		Expect(runtime.ResolveApproval(rejectedApproval.ID, "reject")).To(Succeed())
		var rejected controlApprovalResult
		Eventually(rejectedResult).Should(Receive(&rejected))
		Expect(rejected.err).NotTo(HaveOccurred())
		Expect(rejected.resolution.Decision).To(Equal(operator.DecisionReject))
		Expect(rejected.resolution.Actor).To(Equal(operator.ApprovalActorState().WebOperator))
		persistedApproval, err = controlStore.Queries.GetApproval(ctx, rejectedApproval.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persistedApproval.Status).To(Equal("rejected"))
		Expect(persistedApproval.ResolvedBy).To(Equal(pgtype.Text{String: operator.ApprovalActorState().WebOperator, Valid: true}))

		authoritative.Store(true)
		_, err = fleetClient.Refresh(ctx)
		Expect(err).NotTo(HaveOccurred())
		approvedResult := make(chan controlApprovalResult, 1)
		go func() {
			resolution, requestErr := controller.ApprovalQueue().Request(context.Background(), operator.ApprovalRequest{
				RunID: runID, ToolCallID: "tool-call-1", Kind: "deploy", Title: "Approve status app",
				Detail: "Apply the tested revision", Risk: "medium", Payload: map[string]string{"app": "status"},
				RequiresFleetQuorum: true,
			})
			approvedResult <- controlApprovalResult{resolution: resolution, err: requestErr}
		}()
		Eventually(func() int { return len(controller.ApprovalQueue().Pending()) }).Should(Equal(1))
		approvedApproval := controller.ApprovalQueue().Pending()[0]
		Expect(runtime.ResolveApproval(approvedApproval.ID, "approve")).To(Succeed())
		var approved controlApprovalResult
		Eventually(approvedResult).Should(Receive(&approved))
		Expect(approved.err).NotTo(HaveOccurred())
		Expect(approved.resolution.Decision).To(Equal(operator.DecisionApprove))
		persistedApproval, err = controlStore.Queries.GetApproval(ctx, approvedApproval.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persistedApproval.Status).To(Equal("approved"))
		Expect(persistedApproval.RunID).To(Equal(pgtype.Text{String: runID, Valid: true}))
		Expect(persistedApproval.ToolCallID).To(Equal(pgtype.Text{String: "tool-call-1", Valid: true}))
		Expect(runtime.ResolveApproval(approvedApproval.ID, "approve")).To(MatchError(And(
			ContainSubstring("already resolved as \"approved\""),
			ContainSubstring(operator.ApprovalActorState().WebOperator),
			ContainSubstring(persistedApproval.ResolvedAt.Time.Format(time.RFC3339Nano)),
		)))
		Expect(runtime.ResolveApproval("missing", "approve")).To(MatchError(And(
			ContainSubstring("is not pending"),
			ContainSubstring("no durable approval record exists"),
		)))
		Expect(runtime.ResolveApproval("missing", "later")).To(MatchError("decision must be approve or reject"))

		stamp := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
		Expect(seedControlDeployment(ctx, controlStore, "planned", "running", "node-a", "succeeded", true,
			"0123456789abcdef0123456789abcdef01234567", stamp)).To(Succeed())
		Expect(seedControlDeployment(ctx, controlStore, "running", "running", "node-a", "succeeded", false,
			"shortrev", stamp.Add(time.Minute))).To(Succeed())
		Expect(seedControlDeployment(ctx, controlStore, "stopped", "stopped", "node-b", "succeeded", false,
			"stopped-revision", stamp.Add(2*time.Minute))).To(Succeed())
		Expect(seedControlDeployment(ctx, controlStore, "pending", "running", "", "", false,
			"pending-revision", stamp.Add(3*time.Minute))).To(Succeed())
		_, err = controlStore.AppendReceipt(ctx, "deployment_run", "failed-run", "deployment.failed", "Deployment failed", "", stamp)
		Expect(err).NotTo(HaveOccurred())
		_, err = controlStore.AppendReceipt(ctx, "deployment_run", "plan-run", "deployment.dry_run", "Deployment planned", "digest", stamp)
		Expect(err).NotTo(HaveOccurred())

		projected, err := runtime.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(projected.System.Status).To(Equal("healthy"))
		Expect(projected.Apps).To(HaveLen(4))
		plannedApp := controlApp(projected, "planned")
		Expect(plannedApp.Status).To(Equal("planned"))
		Expect(plannedApp.NodeId).To(BeEmpty(), "a dry-run is not evidence that the app is live on its target")
		Expect(plannedApp.Revision).To(Equal("0123456789ab"))
		runningApp := controlApp(projected, "running")
		Expect(runningApp.Status).To(Equal("running"))
		Expect(runningApp.NodeId).To(Equal("node-a"))
		Expect(runningApp.Revision).To(Equal("shortrev"))
		Expect(controlApp(projected, "stopped").Status).To(Equal("stopped"))
		Expect(controlApp(projected, "pending").Status).To(Equal("pending"))
		Expect(controlNode(projected, "node-a").Apps).To(Equal(uint32(1)))
		Expect(controlNode(projected, "node-b").Apps).To(BeZero())
		Expect(controlActivity(projected, "deployment.failed").Kind).To(Equal("deploy"))
		Expect(controlActivity(projected, "deployment.failed").Status).To(Equal("failed"))
		Expect(controlActivity(projected, "deployment.dry_run").Status).To(Equal("planned"))
		Expect(controlActivity(projected, "approval.requested").Kind).To(Equal("approval"))
		runStarted := controlActivity(projected, "run.started")
		Expect(runStarted.Kind).To(Equal("run"))
		Expect(runStarted.Title).To(Equal("Agent run started with demo"))

		Expect(controller.Close()).To(Succeed())
		stopped, err := runtime.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(stopped.System.Status).To(Equal("unavailable"))
		Expect(stopped.System.Summary).To(Equal("Agent runtime is stopped"))

		closedStore, err := store.OpenControlStore(ctx, isolatedURL)
		Expect(err).NotTo(HaveOccurred())
		closedController, err := operator.NewController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO,
		}, fleetClient, nil)
		Expect(err).NotTo(HaveOccurred())
		closedRuntime, err := control.NewRuntime(closedStore, fleetClient, closedController, nil, persistence, "closed-store")
		Expect(err).NotTo(HaveOccurred())
		closedStore.Close()
		Expect(closedRuntime.Health(ctx)).To(HaveOccurred())
		_, err = closedRuntime.Send(ctx, "must not run")
		Expect(err).To(HaveOccurred())
		_, err = closedRuntime.Snapshot(ctx)
		Expect(err).To(MatchError(ContainSubstring("listing deployments")))
	}, NodeTimeout(2*time.Minute))

	It("projects and receipts the verified Ollama artifact through Controller and Runtime", func(ctx SpecContext) {
		controlStore, _, _ := openControlContractStore(ctx)
		modelDigest := strings.Repeat("c", 64)
		var chatCalls atomic.Int32
		ollamaServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			defer GinkgoRecover()
			response.Header().Set("Content-Type", "application/json")
			switch request.Method + " " + request.URL.Path {
			case "GET /api/version":
				_, err := io.WriteString(response, `{"version":"0.20.4"}`)
				Expect(err).NotTo(HaveOccurred())
			case "POST /api/show":
				_, err := io.WriteString(response, `{"capabilities":["completion","tools"]}`)
				Expect(err).NotTo(HaveOccurred())
			case "GET /api/tags":
				_, err := fmt.Fprintf(response, `{"models":[{"name":"qwen3:8b","model":"qwen3:8b","digest":"%s"}]}`, modelDigest)
				Expect(err).NotTo(HaveOccurred())
			case "POST /api/chat":
				response.Header().Set("Content-Type", "application/x-ndjson")
				if chatCalls.Add(1) == 1 {
					_, err := io.WriteString(response, `{"message":{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"candace_fleet_status","arguments":{}}}]},"done":true}`+"\n")
					Expect(err).NotTo(HaveOccurred())
					return
				}
				_, err := io.WriteString(response, `{"message":{"role":"assistant","content":"The fleet has quorum."},"done":true}`+"\n")
				Expect(err).NotTo(HaveOccurred())
			default:
				http.NotFound(response, request)
			}
		}))
		DeferCleanup(ollamaServer.Close)

		wardenServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			defer GinkgoRecover()
			Expect(request.Method).To(Equal(http.MethodGet))
			Expect(request.URL.Path).To(Equal("/api/status"))
			response.Header().Set("Content-Type", "application/json")
			_, err := io.WriteString(response, controlWardenStatus(true))
			Expect(err).NotTo(HaveOccurred())
		}))
		DeferCleanup(wardenServer.Close)
		fleetClient, err := fleet.NewWardenClient(wardenServer.URL, wardenServer.Client())
		Expect(err).NotTo(HaveOccurred())
		_, err = fleetClient.Refresh(ctx)
		Expect(err).NotTo(HaveOccurred())

		controller, err := operator.NewController(&candaceosv1.CoreConfig{
			HarnessBackend:  candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA,
			Workspace:       GinkgoT().TempDir(),
			ApprovalTimeout: int64(30 * time.Second),
			Ollama: &candaceosv1.OllamaConfig{
				Url: ollamaServer.URL, Model: "qwen3:8b", ModelDigest: modelDigest,
				ContextTokens: 4096, MaxToolCalls: 4, TurnTimeout: int64(time.Minute),
			},
		}, fleetClient, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(controller.Close)
		runtime, err := control.NewRuntime(controlStore, fleetClient, controller, nil, contractPersistenceTiming(), "ollama-contract")
		Expect(err).NotTo(HaveOccurred())
		Expect(controller.Start(ctx)).To(Succeed())

		snapshot, err := runtime.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.System.HarnessBackend).To(Equal(candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA))
		Expect(snapshot.System.HarnessModel).To(Equal("qwen3:8b"))

		runID, err := runtime.Send(ctx, "Use candace_fleet_status and report quorum")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() string {
			run, queryErr := controlStore.Queries.GetRun(ctx, runID)
			if queryErr != nil {
				return ""
			}
			return run.Status
		}).Should(Equal("succeeded"))
		Expect(chatCalls.Load()).To(Equal(int32(2)))

		receipts, err := controlStore.Queries.ListRecentReceipts(ctx, 100)
		Expect(err).NotTo(HaveOccurred())
		var startedReceipt *storedb.CandaceosActivityReceipt
		var succeededReceipt *storedb.CandaceosActivityReceipt
		for index := range receipts {
			if receipts[index].EntityID == runID && receipts[index].Kind == "run.started" {
				startedReceipt = &receipts[index]
			}
			if receipts[index].EntityID == runID && receipts[index].Kind == "run.succeeded" {
				succeededReceipt = &receipts[index]
			}
		}
		Expect(startedReceipt).NotTo(BeNil())
		Expect(startedReceipt.Summary).To(Equal("Agent run started with ollama (qwen3:8b@sha256:" + modelDigest + ")"))
		Expect(startedReceipt.PayloadSha256).To(Equal(pgtype.Text{String: modelDigest, Valid: true}))
		Expect(succeededReceipt).NotTo(BeNil())
		Expect(succeededReceipt.Summary).To(Equal("Agent run succeeded"))
		Expect(succeededReceipt.PayloadSha256.Valid).To(BeFalse())
	}, NodeTimeout(time.Minute))
})

func openControlContractStore(ctx SpecContext) (*store.Store, *pgxpool.Pool, string) {
	databaseURL := strings.TrimSpace(os.Getenv(controlTestDatabaseURLEnv))
	if databaseURL == "" {
		Skip("set " + controlTestDatabaseURLEnv + " to run PostgreSQL control specs")
	}

	schemaName := fmt.Sprintf("candaceos_control_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	isolatedURL, err := isolatedControlDatabaseURL(databaseURL, schemaName)
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
	return controlStore, verificationPool, isolatedURL
}

func contractPersistenceTiming() *candaceosv1.PersistenceTiming {
	return &candaceosv1.PersistenceTiming{
		FleetPollIntervalNanoseconds: int64(contractFleetPollInterval),
	}
}

func isolatedControlDatabaseURL(databaseURL, schemaName string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", fmt.Errorf("parse control test database URL: %w", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", fmt.Errorf("control test database URL must use postgres or postgresql")
	}
	databaseName := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if decoded, decodeErr := url.PathUnescape(databaseName); decodeErr == nil {
		databaseName = decoded
	}
	if !strings.HasSuffix(databaseName, "_test") {
		return "", fmt.Errorf("control test database name must end in _test, got %q", databaseName)
	}
	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func controlWardenStatus(authoritative bool) string {
	return fmt.Sprintf(`{
  "view": {
    "self": "node-b",
    "role": "follower",
    "term": 12,
    "leader_id": "node-a",
    "authoritative": %t,
    "updated_at": "2026-08-19T10:00:00Z",
    "membership": {
      "version": 3,
      "created_in_term": 12,
      "voters": [
        {"id": "node-a", "addr": "10.0.0.1:7717"},
        {"id": "node-b", "addr": "10.0.0.2:7717"},
        {"id": "node-c", "addr": "10.0.0.3:7717"}
      ]
    },
    "peers": [
      {"node": {"id": "node-a", "addr": "10.0.0.1:7717"}, "status": "alive", "last_seen": "2026-08-19T09:59:58Z", "member": "voter"},
      {"node": {"id": "node-b", "addr": "10.0.0.2:7717"}, "status": "alive", "last_seen": "2026-08-19T09:59:59Z", "member": "voter"},
      {"node": {"id": "node-c", "addr": "10.0.0.3:7717"}, "status": "suspect", "last_seen": "2026-08-19T09:59:00Z", "member": "voter"}
    ]
  }
}`, authoritative)
}

func seedControlDeployment(
	ctx context.Context,
	controlStore *store.Store,
	id, desiredState, nodeID, runStatus string,
	dryRun bool,
	revision string,
	at time.Time,
) error {
	return controlStore.WithTx(ctx, func(queries *storedb.Queries) error {
		revisionID := id + "-revision"
		if err := queries.UpsertAppRevision(ctx, storedb.UpsertAppRevisionParams{
			AppRevisionID: revisionID, AppName: id, SourceRepository: "https://example.invalid/candace/" + id + ".git",
			SourceRevision: revision, SourceSha256: "sha256:" + id, ComposeFile: "apps/" + id + "/compose.yaml",
			ImageDigest: pgtype.Text{}, CreatedAt: pgtype.Timestamptz{Time: at, Valid: true},
		}); err != nil {
			return err
		}
		if err := queries.UpsertDeployment(ctx, storedb.UpsertDeploymentParams{
			DeploymentID: id, AppRevisionID: revisionID, ProjectName: "candaceos-" + id,
			WorkspacePath: "apps/" + id, DesiredState: desiredState, PlacementMode: "node",
			ExactNodeID: pgtype.Text{String: "node-a", Valid: true}, Replicas: 1,
			CreatedAt: pgtype.Timestamptz{Time: at, Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: at, Valid: true},
		}); err != nil {
			return err
		}
		if runStatus == "" {
			return nil
		}
		runID := id + "-run"
		if err := queries.CreateDeploymentRun(ctx, storedb.CreateDeploymentRunParams{
			RunID: runID, RolloutID: "rollout-" + runID,
			DeploymentID: id, AppRevisionID: revisionID, NodeID: nodeID,
			DesiredState: "running", WardenTerm: 12, LeaderID: "node-a",
			RequestedAt: pgtype.Timestamptz{Time: at, Valid: true},
		}); err != nil {
			return err
		}
		rows, err := queries.FinishDeploymentRun(ctx, storedb.FinishDeploymentRunParams{
			RunID: runID, Status: runStatus, DryRun: pgtype.Bool{Bool: dryRun, Valid: true},
			FinishedAt: pgtype.Timestamptz{Time: at.Add(time.Second), Valid: true},
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return fmt.Errorf("expected one deployment run, updated %d", rows)
		}
		return nil
	})
}

func controlApp(snapshot *candaceosv1.WebUISnapshot, id string) *candaceosv1.WebUIApp {
	for _, app := range snapshot.Apps {
		if app.Id == id {
			return app
		}
	}
	Fail("missing app " + id)
	return &candaceosv1.WebUIApp{}
}

func controlNode(snapshot *candaceosv1.WebUISnapshot, id string) *candaceosv1.WebUINode {
	for _, node := range snapshot.Fleet.Nodes {
		if node.Id == id {
			return node
		}
	}
	Fail("missing node " + id)
	return &candaceosv1.WebUINode{}
}

func controlActivity(snapshot *candaceosv1.WebUISnapshot, detail string) *candaceosv1.WebUIActivity {
	for _, activity := range snapshot.Activity {
		if activity.Detail == detail {
			return activity
		}
	}
	Fail("missing activity " + detail)
	return &candaceosv1.WebUIActivity{}
}
