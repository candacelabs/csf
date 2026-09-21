package copilotadapter_test

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/candacelabs/csf/pkg/cron"
	"github.com/google/uuid"
	"github.com/guregu/null/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/pkg/httpserver"
	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

var _ = Describe("New", func() {
	var (
		controller *gomock.Controller
		bridge     *MockICopilotBridge
		store      *MockIStore
		worktrees  *MockIWorktreeManager
		terminals  *MockITerminalManager
	)

	BeforeEach(func() {
		controller = gomock.NewController(GinkgoT())
		bridge = NewMockICopilotBridge(controller)
		store = NewMockIStore(controller)
		worktrees = NewMockIWorktreeManager(controller)
		terminals = NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil).AnyTimes()
	})

	It("flushes PTY output before waiting even when the session poll is one minute", func() {
		config := copilotadapter.DefaultAdapterConfig()
		config.EventStreamPollMillis = 60000
		service, err := copilotadapter.NewCopilotAdapter(
			copilotadapter.WithConfig(config), copilotadapter.WithBridge(bridge),
			copilotadapter.WithStore(store), copilotadapter.WithWorktreeManager(worktrees),
			copilotadapter.WithTerminalManager(terminals), copilotadapter.WithScheduleStore(cron.NewMemoryStore()),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(service.Close)
		engine := httpserver.NewEngine("terminal-flush-test")
		Expect(service.Register(engine)).To(Succeed())
		server := httptest.NewServer(engine)
		DeferCleanup(server.Close)
		worktreeID, terminalID := uuid.New(), uuid.New()
		snapshot := copilotadapter.TerminalSnapshot{ID: terminalID, WorktreeID: worktreeID, Status: string(api.TerminalStatusRunning)}
		changed := make(chan struct{})
		terminals.EXPECT().Get(terminalID).Return(snapshot, true)
		gomock.InOrder(
			terminals.EXPECT().EventsAfter(terminalID, int64(0)).Return(copilotadapter.TerminalEventReplay{
				Snapshot: snapshot, Changed: changed,
				Events: []copilotadapter.TerminalOutput{{Seq: 1, TerminalID: terminalID, Kind: string(api.TerminalEventKindOutput), Data: "ready"}},
			}, true),
			terminals.EXPECT().EventsAfter(terminalID, int64(1)).Return(copilotadapter.TerminalEventReplay{Snapshot: snapshot, Changed: changed}, true).AnyTimes(),
		)
		// The request budget is generous for loaded CI but shorter than the
		// configured poll. Waiting for that ticker therefore cannot pass.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/worktrees/"+worktreeID.String()+"/terminals/"+terminalID.String()+"/events", nil)
		Expect(err).NotTo(HaveOccurred())
		started := time.Now()
		response, err := http.DefaultClient.Do(request)
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(response.Body.Close()).To(Succeed()) }()
		scanner := bufio.NewScanner(response.Body)
		Expect(scanner.Scan()).To(BeTrue())
		Expect(scanner.Text()).To(Equal("id:1"))
		AddReportEntry("terminal first frame", time.Since(started).String())
	})

	It("refuses to build without a bridge", func() {
		_, err := copilotadapter.NewCopilotAdapter(copilotadapter.WithStore(store))
		Expect(err).To(MatchError(ContainSubstring("WithBridge is required")))
	})

	It("refuses to build without a store", func() {
		_, err := copilotadapter.NewCopilotAdapter(copilotadapter.WithBridge(bridge))
		Expect(err).To(MatchError(ContainSubstring("WithStore is required")))
	})

	It("rejects a nil option value", func() {
		_, err := copilotadapter.NewCopilotAdapter(copilotadapter.WithBridge(nil))
		Expect(err).To(MatchError(ContainSubstring("WithBridge needs a bridge")))
	})

	It("returns the concrete service once both seams are supplied", func() {
		service, err := copilotadapter.NewCopilotAdapter(
			copilotadapter.WithBridge(bridge),
			copilotadapter.WithStore(store),
			copilotadapter.WithWorktreeManager(worktrees),
			copilotadapter.WithTerminalManager(terminals),
			copilotadapter.WithScheduleStore(cron.NewMemoryStore()),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(service).NotTo(BeNil())
		DeferCleanup(service.Close)
	})

	It("has no listener to open: the binary's engine is the only mount point", func() {
		service, err := copilotadapter.NewCopilotAdapter(
			copilotadapter.WithBridge(bridge),
			copilotadapter.WithStore(store),
			copilotadapter.WithWorktreeManager(worktrees),
			copilotadapter.WithTerminalManager(terminals),
			copilotadapter.WithScheduleStore(cron.NewMemoryStore()),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(service.Close)
		Expect(service.Register(httpserver.NewEngine("copilot-adapter-test"))).To(Succeed())
	})
})

var _ = Describe("Handler", func() {
	var (
		controller *gomock.Controller
		bridge     *MockICopilotBridge
		store      *MockIStore
		worktrees  *MockIWorktreeManager
		terminals  *MockITerminalManager
		server     *httptest.Server
		client     *api.ClientWithResponses
		schedules  *cron.MemoryStore
	)

	BeforeEach(func() {
		controller = gomock.NewController(GinkgoT())
		bridge = NewMockICopilotBridge(controller)
		store = NewMockIStore(controller)
		worktrees = NewMockIWorktreeManager(controller)
		terminals = NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil).AnyTimes()
		schedules = cron.NewMemoryStore()
		service, err := copilotadapter.NewCopilotAdapter(
			copilotadapter.WithBridge(bridge),
			copilotadapter.WithStore(store),
			copilotadapter.WithWorktreeManager(worktrees),
			copilotadapter.WithTerminalManager(terminals),
			copilotadapter.WithScheduleStore(schedules),
			copilotadapter.WithVersion("test"),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(service.Close)
		engine := httpserver.NewEngine("copilot-adapter-test")
		Expect(service.Register(engine)).To(Succeed())
		server = httptest.NewServer(engine)
		DeferCleanup(server.Close)
		client, err = api.NewClientWithResponses(server.URL)
		Expect(err).NotTo(HaveOccurred())
	})

	It("projects provider values without converting missing measurements into zero", func() {
		at := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
		store.EXPECT().ListSessionTelemetry(gomock.Any(), gomock.Any()).Return([]storedb.ListSessionTelemetryRow{{
			SessionID: uuid.New(), DisplayName: "measured", Model: "model", Status: "idle", CreatedAt: at, UpdatedAt: at,
			TurnCount: 2, KnownStartedTurnCount: 1, KnownCompletedTurnCount: 2, DurationKnownTurnCount: 1,
			DurationSeconds: null.FloatFrom(4), InputTokens: null.IntFrom(0), PremiumRequests: null.FloatFrom(0),
		}}, nil)
		store.EXPECT().CountTraceDeliveries(gomock.Any()).Return([]storedb.CountTraceDeliveriesRow{{Status: "accepted", Count: 1}}, nil)
		response, err := client.GetTelemetryWithResponse(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(response.JSON200).NotTo(BeNil())
		Expect(response.JSON200.Sessions).To(HaveLen(1))
		observed := response.JSON200.Sessions[0]
		Expect(observed.InputTokens).To(PointTo(BeZero()))
		Expect(observed.OutputTokens).To(BeNil())
		Expect(observed.PremiumRequests).To(PointTo(BeZero()))
		Expect(observed.DurationSeconds).To(PointTo(Equal(float64(4))))
		Expect(observed.LatestStartedAt).To(BeNil())
		Expect(response.JSON200.Traces[0].State).To(Equal(api.TraceAccepted))
	})

	It("fails the telemetry request when retained facts cannot be read", func() {
		store.EXPECT().ListSessionTelemetry(gomock.Any(), gomock.Any()).Return(nil, errors.New("database unavailable"))
		response, err := client.GetTelemetryWithResponse(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusInternalServerError))
		Expect(response.JSON200).To(BeNil())
	})

	It("answers the health probe through the generated client", func() {
		bridge.EXPECT().ListModels(gomock.Any()).Return(nil, nil)
		response, err := client.GetHealthWithResponse(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusOK))
		Expect(response.JSON200).NotTo(BeNil())
		Expect(response.JSON200.Version).To(PointTo(Equal("test")))
	})

	It("lists the models the bridge reports", func() {
		bridge.EXPECT().ListModels(gomock.Any()).Return([]copilotadapter.BridgeModel{
			{ID: "gpt-5", DisplayName: "GPT-5", Capabilities: []api.ModelCapability{api.Chat}},
		}, nil)
		response, err := client.ListModelsWithResponse(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusOK))
		Expect(response.JSON200).NotTo(BeNil())
		Expect(response.JSON200.Data).To(HaveLen(1))
		Expect(response.JSON200.Data[0].Id).To(Equal("gpt-5"))
		Expect(response.JSON200.Data[0].Capabilities).To(Equal([]api.ModelCapability{api.Chat}))
	})

	It("answers the contract's empty model list when the CLI cannot enumerate", func() {
		bridge.EXPECT().ListModels(gomock.Any()).Return(nil, errors.New("copilot: command not found"))
		response, err := client.ListModelsWithResponse(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusOK), string(response.Body))
		Expect(response.JSON200).NotTo(BeNil())
		Expect(response.JSON200.Data).To(BeEmpty())
	})

	It("rejects a session body the contract forbids with the shared Error shape", func() {
		response, err := client.CreateSessionWithBodyWithResponse(context.Background(), "application/json",
			bytesReader(`{"model": ""}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusBadRequest))
		Expect(response.JSON400).NotTo(BeNil())
		Expect(response.JSON400.Message).NotTo(BeEmpty())
	})

	DescribeTable("rejects invalid worktree-mode request shapes at the contract boundary", func(body string) {
		response, err := client.CreateSessionWithBodyWithResponse(context.Background(), "application/json", bytesReader(body))
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusBadRequest), string(response.Body))
		Expect(response.JSON400).NotTo(BeNil())
		Expect(response.JSON400.Code).To(Equal("invalid_request"))
	},
		Entry("without an idempotency key", `{"model":"gpt-5","repositoryId":"repo","worktreeMode":"newWorktree"}`),
		Entry("when reuseExisting omits worktreeId", `{"idempotencyKey":"11111111-1111-4111-8111-111111111111","model":"gpt-5","repositoryId":"repo","worktreeMode":"reuseExistingWorktree"}`),
		Entry("when newWorktree supplies worktreeId", `{"idempotencyKey":"11111111-1111-4111-8111-111111111111","model":"gpt-5","repositoryId":"repo","worktreeMode":"newWorktree","worktreeId":"22222222-2222-4222-8222-222222222222"}`),
		Entry("when reuseCurrent supplies baseRef", `{"idempotencyKey":"11111111-1111-4111-8111-111111111111","model":"gpt-5","repositoryId":"repo","worktreeMode":"reuseCurrentWorktree","baseRef":"main"}`),
	)

	It("rejects removed answer bodies and legacy request kinds before preparing delivery", func() {
		ctx := context.Background()
		sessionID := uuid.New()
		requestID := uuid.New()

		approvedWithAnswer, err := client.ResolveSessionRequestWithBodyWithResponse(
			ctx, sessionID, requestID, "application/json",
			bytesReader(`{"decision":"approve","answer":"this answer must not be ignored"}`),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(approvedWithAnswer.StatusCode()).To(Equal(http.StatusBadRequest), string(approvedWithAnswer.Body))
		Expect(approvedWithAnswer.JSON400).NotTo(BeNil())
		Expect(approvedWithAnswer.JSON400.Code).To(Equal("invalid_request"))
		removedAnswer, err := client.ResolveSessionRequestWithBodyWithResponse(
			ctx, sessionID, requestID, "application/json", bytesReader(`{"decision":"answer"}`),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(removedAnswer.StatusCode()).To(Equal(http.StatusBadRequest), string(removedAnswer.Body))
		Expect(removedAnswer.JSON400).NotTo(BeNil())
		Expect(removedAnswer.JSON400.Code).To(Equal("invalid_request"))

		legacy := storedb.PendingRequest{
			ID: requestID, SessionID: sessionID, Kind: "userInput",
			Status: string(api.Pending), DeliveryStatus: "pending",
		}
		// There is deliberately no PrepareSessionRequestResolution expectation:
		// a legacy request must fail before it can reach SDK delivery.
		store.EXPECT().GetSessionRequest(gomock.Any(), requestID).Return(legacy, nil)
		approved, err := client.ResolveSessionRequestWithResponse(ctx, sessionID, requestID, resolutionBody(api.Approve))
		Expect(err).NotTo(HaveOccurred())
		Expect(approved.StatusCode()).To(Equal(http.StatusBadRequest), string(approved.Body))
		Expect(approved.JSON400).NotTo(BeNil())
		Expect(approved.JSON400.Code).To(Equal("invalid_request"))
	})

	It("orders active schedules by next run and puts paused schedules last", func() {
		ctx := context.Background()
		now := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
		nearID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
		farID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
		pausedNewID := uuid.MustParse("00000000-0000-0000-0000-000000000003")
		pausedOldID := uuid.MustParse("00000000-0000-0000-0000-000000000004")
		sessionID := uuid.New()
		rows := []storedb.ChatSchedule{
			{ID: pausedOldID, SessionID: sessionID, DisplayName: "paused old", Prompt: "old", CronExpression: "0 10 * * *", Timezone: "UTC", Status: "paused", CreatedAt: now.Add(-4 * time.Hour), UpdatedAt: now},
			{ID: farID, SessionID: sessionID, DisplayName: "far", Prompt: "far", CronExpression: "0 14 * * *", Timezone: "UTC", Status: "active", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now},
			{ID: pausedNewID, SessionID: sessionID, DisplayName: "paused new", Prompt: "new", CronExpression: "0 11 * * *", Timezone: "UTC", Status: "paused", CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
			{ID: nearID, SessionID: sessionID, DisplayName: "near", Prompt: "near", CronExpression: "0 13 * * *", Timezone: "UTC", Status: "active", CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now},
		}
		store.EXPECT().ListChatSchedules(gomock.Any()).Return(rows, nil)
		definitions := make([]cron.JobDefinition, 0, 2)
		for _, row := range []storedb.ChatSchedule{rows[1], rows[3]} {
			schedule := cron.Spec(cron.Raw(row.CronExpression)).In(time.UTC)
			definition, err := schedule.Definition()
			Expect(err).NotTo(HaveOccurred())
			definitions = append(definitions, cron.JobDefinition{
				Name: "chat/" + row.ID.String(), Schedule: definition,
				CatchUp: cron.CatchUpLatest, Overlap: cron.OverlapSkip,
			})
		}
		_, err := schedules.Reconcile(ctx, definitions, now)
		Expect(err).NotTo(HaveOccurred())

		response, err := client.ListChatSchedulesWithResponse(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusOK), string(response.Body))
		identifiers := make([]uuid.UUID, 0, len(response.JSON200.Data))
		for _, schedule := range response.JSON200.Data {
			identifiers = append(identifiers, *schedule.Id)
		}
		Expect(identifiers).To(Equal([]uuid.UUID{nearID, farID, pausedNewID, pausedOldID}))
		Expect(response.JSON200.Data[0].NextRunAt).NotTo(BeNil())
		Expect(response.JSON200.Data[2].NextRunAt).To(BeNil())
	})
})
