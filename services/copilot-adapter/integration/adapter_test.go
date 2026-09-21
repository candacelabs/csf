package integration_test

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/pkg/pgmem"

	"github.com/candacelabs/csf/pkg/httpserver"
	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/store"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

// projectionBudget is how long the event goroutine gets to land rows in the
// emulator on a loaded host; generous on purpose (CS-9 counterweight 3).
const (
	projectionBudget = 10 * time.Second
	projectionPoll   = 25 * time.Millisecond
)

type observedScheduleStore struct {
	*cron.MemoryStore
	reconciled     chan struct{}
	reconciledOnce sync.Once
}

func newObservedScheduleStore() *observedScheduleStore {
	return &observedScheduleStore{MemoryStore: cron.NewMemoryStore(), reconciled: make(chan struct{})}
}

func (store *observedScheduleStore) Reconcile(
	ctx context.Context,
	definitions []cron.JobDefinition,
	now time.Time,
) ([]cron.JobState, error) {
	states, err := store.MemoryStore.Reconcile(ctx, definitions, now)
	if err == nil {
		store.reconciledOnce.Do(func() { close(store.reconciled) })
	}
	return states, err
}

func startScheduleRuntime(
	ctx context.Context,
	service *copilotadapter.CopilotAdapter,
	store *observedScheduleStore,
) {
	GinkgoHelper()
	runContext, stop := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() { finished <- service.RunSchedules(runContext) }()
	Eventually(store.reconciled).WithTimeout(projectionBudget).Should(BeClosed())
	DeferCleanup(func() {
		stop()
		Eventually(finished).WithTimeout(projectionBudget).Should(Receive(Succeed()))
	})
}

var _ = Describe("the adapter over pgmem and a mocked CLI", func() {
	var (
		ctx       context.Context
		queries   *storedb.Queries
		bridge    *MockICopilotBridge
		worktrees *MockIWorktreeManager
		terminals *MockITerminalManager
		server    *httptest.Server
		client    *api.ClientWithResponses
		events    chan copilotadapter.BridgeEvent
		sent      chan copilotadapter.BridgePrompt
		resolved  chan copilotadapter.BridgeResolution
		closed    chan struct{}
	)

	BeforeEach(func() {
		ctx = context.Background()
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		Expect(store.ApplyMigrations(ctx, db)).To(Succeed())

		controller := gomock.NewController(GinkgoT())
		bridge = NewMockICopilotBridge(controller)
		worktrees = NewMockIWorktreeManager(controller)
		worktrees.EXPECT().Reuse(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, repositoryID string, path string) (copilotadapter.PreparedWorktree, error) {
				return copilotadapter.PreparedWorktree{
					Repository: copilotadapter.Repository{ID: repositoryID, DisplayName: "Repo", Root: path, DefaultRef: "HEAD"},
					Path:       path, BaseRef: "HEAD",
				}, nil
			}).AnyTimes()
		terminals = NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil).AnyTimes()
		queries = storedb.New(db)
		postgresStore, err := store.NewPostgresStore(db)
		Expect(err).NotTo(HaveOccurred())
		scheduleStore := newObservedScheduleStore()
		service, err := copilotadapter.NewCopilotAdapter(
			copilotadapter.WithBridge(bridge),
			copilotadapter.WithStore(postgresStore),
			copilotadapter.WithWorktreeManager(worktrees),
			copilotadapter.WithTerminalManager(terminals),
			copilotadapter.WithScheduleStore(scheduleStore),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(service.Close)
		startScheduleRuntime(ctx, service, scheduleStore)
		engine := httpserver.NewEngine("copilot-adapter-test")
		Expect(service.Register(engine)).To(Succeed())
		server = httptest.NewServer(engine)
		DeferCleanup(server.Close)
		client, err = api.NewClientWithResponses(server.URL)
		Expect(err).NotTo(HaveOccurred())

		events = make(chan copilotadapter.BridgeEvent, 16)
		sent = make(chan copilotadapter.BridgePrompt, 8)
		resolved = make(chan copilotadapter.BridgeResolution, 8)
		closed = make(chan struct{})
	})

	It("walks one session from creation to its end through the generated client", func() {
		var activeMutex sync.Mutex
		var activeTurn uuid.UUID
		worktrees.EXPECT().Prepare(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request copilotadapter.WorktreeRequest) (copilotadapter.PreparedWorktree, error) {
				Expect(request.RepositoryID).To(Equal("repo"))
				Expect(request.Mode).To(Equal("newWorktree"))
				return copilotadapter.PreparedWorktree{
					Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD"},
					Path:       "/tmp/work", BaseRef: "HEAD",
				}, nil
			})
		bridge.EXPECT().CreateSession(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, spec copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
				Expect(spec.Model).To(Equal("gpt-5"))
				Expect(spec.WorkingDirectory).To(Equal("/tmp/work"))
				return copilotadapter.BridgeSession{
					Events: events,
					Send: func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
						activeMutex.Lock()
						activeTurn = prompt.TurnID
						activeMutex.Unlock()
						sent <- prompt
						return copilotadapter.BridgePromptDeliveryAccepted, nil
					},
					ActiveTurn: func() (uuid.UUID, bool) {
						activeMutex.Lock()
						defer activeMutex.Unlock()
						return activeTurn, activeTurn != uuid.Nil
					},
					Abort: func(_ context.Context, expectedTurnID uuid.UUID) (uuid.UUID, error) {
						activeMutex.Lock()
						defer activeMutex.Unlock()
						Expect(expectedTurnID).To(Equal(activeTurn))
						aborted := activeTurn
						events <- copilotadapter.BridgeEvent{
							Kind: copilotadapter.BridgeEventTurnAborted, TurnID: &aborted,
						}
						return activeTurn, nil
					},
					SetModel: func(_ context.Context, model string) error { return nil },
					Resolve: func(_ context.Context, resolution copilotadapter.BridgeResolution) error {
						resolved <- resolution
						return nil
					},
					Close: func(_ context.Context) error { close(closed); return nil },
				}, nil
			})

		created, err := client.CreateSessionWithResponse(ctx, newWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		Expect(created.JSON201).NotTo(BeNil())
		Expect(created.JSON201.Id).NotTo(BeNil())
		sessionID := *created.JSON201.Id
		Expect(created.JSON201.Status).To(Equal(api.SessionStatusIdle))

		fetched, err := client.GetSessionWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(fetched.StatusCode()).To(Equal(http.StatusOK), string(fetched.Body))
		Expect(fetched.JSON200.Id).To(PointTo(Equal(sessionID)))

		author := "ada"
		idempotencyKey := uuid.New()
		prompted, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: idempotencyKey, Text: "hello", Mode: api.Queue, Author: &author,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(prompted.StatusCode()).To(Equal(http.StatusAccepted), string(prompted.Body))
		Expect(prompted.JSON202).NotTo(BeNil())
		Expect(prompted.JSON202.Status).To(Equal(api.TurnStatusQueued))
		var firstPrompt copilotadapter.BridgePrompt
		Eventually(sent).WithTimeout(projectionBudget).Should(Receive(&firstPrompt))
		Expect(firstPrompt).To(And(
			HaveField("Text", Equal("hello")),
			HaveField("Mode", Equal("queue")),
			HaveField("Author", Equal("ada")),
		))
		retriedPrompt, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: idempotencyKey, Text: "hello", Mode: api.Queue, Author: &author,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(retriedPrompt.StatusCode()).To(Equal(http.StatusAccepted), string(retriedPrompt.Body))
		Expect(retriedPrompt.JSON202.Id).To(Equal(prompted.JSON202.Id))
		Consistently(sent).ShouldNot(Receive(), "an idempotent retry must not cross the SDK boundary twice")
		conflictingPrompt, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: idempotencyKey, Text: "different text", Mode: api.Queue, Author: &author,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(conflictingPrompt.StatusCode()).To(Equal(http.StatusConflict), string(conflictingPrompt.Body))
		Expect(conflictingPrompt.JSON409.Code).To(Equal("idempotency_key_reused"))

		running, err := client.GetSessionWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(running.JSON200.Status).To(Equal(api.SessionStatusRunning))

		now := time.Now().UTC()
		permissionRequestID := uuid.New()
		events <- copilotadapter.BridgeEvent{Kind: copilotadapter.BridgeEventAssistantDelta, OccurredAt: now, TurnID: &firstPrompt.TurnID, Text: "Hel", Author: "copilot"}
		events <- copilotadapter.BridgeEvent{Kind: copilotadapter.BridgeEventAssistantMessage, OccurredAt: now, TurnID: &firstPrompt.TurnID, Text: "Hello, ada.", Author: "copilot"}
		events <- copilotadapter.BridgeEvent{Kind: copilotadapter.BridgeEventToolCall, OccurredAt: now, TurnID: &firstPrompt.TurnID, ToolName: "bash", Text: `{"cmd":"ls"}`, Author: "copilot"}
		events <- copilotadapter.BridgeEvent{Kind: copilotadapter.BridgeEventToolResult, OccurredAt: now, TurnID: &firstPrompt.TurnID, ToolName: "bash", Text: "README.md", Author: "copilot"}
		events <- copilotadapter.BridgeEvent{
			Kind: copilotadapter.BridgeEventRequestOpened, OccurredAt: now,
			TurnID: &firstPrompt.TurnID, RequestID: &permissionRequestID,
			RequestKind: copilotadapter.BridgeRequestPermission, ToolName: "bash", Text: "run rm -rf build?",
		}
		events <- copilotadapter.BridgeEvent{Kind: copilotadapter.BridgeEventTurnCompleted, OccurredAt: now, TurnID: &firstPrompt.TurnID}

		var transcript []api.TranscriptItem
		Eventually(func() []api.TranscriptItem {
			page, err := client.ListTranscriptWithResponse(ctx, sessionID, &api.ListTranscriptParams{})
			if err != nil || page.JSON200 == nil {
				return nil
			}
			transcript = page.JSON200.Data
			return transcript
		}).WithTimeout(projectionBudget).WithPolling(projectionPoll).Should(HaveLen(4))
		Expect(transcript[0].Kind).To(Equal(api.TranscriptItemKindUserMessage))
		Expect(transcript[0].Text).To(Equal("hello"))
		Expect(transcript[0].Author).To(PointTo(Equal("ada")))
		Expect(transcript[1].Kind).To(Equal(api.TranscriptItemKindAssistantMessage))
		Expect(transcript[1].Text).To(Equal("Hello, ada."))
		Expect(transcript[2].Kind).To(Equal(api.TranscriptItemKindToolCall))
		Expect(transcript[2].ToolName).To(PointTo(Equal("bash")))
		Expect(transcript[3].Kind).To(Equal(api.TranscriptItemKindToolResult))
		Expect(*transcript[3].Seq).To(BeNumerically(">", *transcript[2].Seq))

		Eventually(func() api.SessionStatus {
			session, err := client.GetSessionWithResponse(ctx, sessionID)
			if err != nil || session.JSON200 == nil {
				return ""
			}
			return session.JSON200.Status
		}).WithTimeout(projectionBudget).WithPolling(projectionPoll).Should(Equal(api.SessionStatusIdle))

		pending, err := client.ListSessionRequestsWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending.StatusCode()).To(Equal(http.StatusOK), string(pending.Body))
		Expect(pending.JSON200.Data).To(HaveLen(1))
		Expect(pending.JSON200.Data[0].Kind).To(Equal(api.Permission))
		Expect(pending.JSON200.Data[0].Id).NotTo(BeNil())
		requestID := *pending.JSON200.Data[0].Id
		Expect(uuid.UUID(requestID)).To(Equal(permissionRequestID))

		irrelevant, err := client.ResolveSessionRequestWithBodyWithResponse(
			ctx, sessionID, requestID, "application/json",
			bytesReader(`{"decision":"approve","answer":"yes, go ahead"}`),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(irrelevant.StatusCode()).To(Equal(http.StatusBadRequest), string(irrelevant.Body))
		Expect(irrelevant.JSON400.Code).To(Equal("invalid_request"))

		decided, err := client.ResolveSessionRequestWithResponse(ctx, sessionID, requestID,
			resolutionBody(api.Approve))
		Expect(err).NotTo(HaveOccurred())
		Expect(decided.StatusCode()).To(Equal(http.StatusOK), string(decided.Body))
		Eventually(resolved).WithTimeout(projectionBudget).Should(Receive(And(
			HaveField("RequestID", Equal(uuid.UUID(requestID))),
			HaveField("Decision", Equal("approve")),
		)))
		retried, err := client.ResolveSessionRequestWithResponse(ctx, sessionID, requestID,
			resolutionBody(api.Approve))
		Expect(err).NotTo(HaveOccurred())
		Expect(retried.StatusCode()).To(Equal(http.StatusOK), string(retried.Body))
		conflicting, err := client.ResolveSessionRequestWithResponse(ctx, sessionID, requestID,
			resolutionBody(api.Deny))
		Expect(err).NotTo(HaveOccurred())
		Expect(conflicting.StatusCode()).To(Equal(http.StatusConflict), string(conflicting.Body))

		frames := readEventFrames(ctx, server.URL, sessionID, "2")
		Expect(frames).NotTo(BeEmpty())
		Expect(frames[0]).To(HavePrefix("id:3"))

		aborting, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{IdempotencyKey: uuid.New(), Text: "again", Mode: api.Steer})
		Expect(err).NotTo(HaveOccurred())
		Expect(aborting.StatusCode()).To(Equal(http.StatusAccepted), string(aborting.Body))
		Expect(aborting.JSON202.Status).To(Equal(api.TurnStatusQueued))
		abortTurnID := *aborting.JSON202.Id
		events <- copilotadapter.BridgeEvent{ID: uuid.NewString(), Kind: copilotadapter.BridgeEventTurnStarted, TurnID: &abortTurnID}
		Eventually(func() string {
			turn, lookupErr := queries.GetTurn(ctx, abortTurnID)
			if lookupErr != nil {
				return ""
			}
			return turn.Status
		}).WithTimeout(projectionBudget).Should(Equal(string(api.TurnStatusRunning)))
		aborted, err := client.AbortTurnWithResponse(ctx, sessionID, api.AbortTurnJSONRequestBody{
			IdempotencyKey: uuid.New(), TurnId: abortTurnID,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(aborted.StatusCode()).To(Equal(http.StatusOK), string(aborted.Body))
		Expect(aborted.JSON200.Status).To(Equal(api.TurnStatusAborted))

		unfinished, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{IdempotencyKey: uuid.New(), Text: "finish before ending", Mode: api.Queue})
		Expect(err).NotTo(HaveOccurred())
		Expect(unfinished.StatusCode()).To(Equal(http.StatusAccepted), string(unfinished.Body))
		var unfinishedPrompt copilotadapter.BridgePrompt
		Eventually(sent).WithTimeout(projectionBudget).Should(Receive(&unfinishedPrompt))
		requestBeforeEnd := uuid.New()
		events <- copilotadapter.BridgeEvent{
			Kind: copilotadapter.BridgeEventRequestOpened, OccurredAt: time.Now().UTC(),
			RequestID: &requestBeforeEnd, TurnID: &unfinishedPrompt.TurnID,
			RequestKind: copilotadapter.BridgeRequestPermission, Text: "still waiting",
		}
		Eventually(func() int {
			pending, pendingErr := client.ListSessionRequestsWithResponse(ctx, sessionID)
			if pendingErr != nil || pending.JSON200 == nil {
				return 0
			}
			return len(pending.JSON200.Data)
		}).WithTimeout(projectionBudget).Should(Equal(1))
		subagentID := "worker-before-end"
		events <- copilotadapter.BridgeEvent{
			Kind: copilotadapter.BridgeEventSubagentStarted, OccurredAt: time.Now().UTC(),
			TurnID: &unfinishedPrompt.TurnID, AgentID: subagentID, DisplayName: "Worker",
		}
		Eventually(func() string {
			subagent, lookupErr := queries.GetSubagent(ctx, storedb.GetSubagentParams{SessionID: sessionID, ID: subagentID})
			if lookupErr != nil {
				return ""
			}
			return subagent.Status
		}).WithTimeout(projectionBudget).Should(Equal(string(api.SubagentStatusActive)))
		scheduled, err := client.CreateChatScheduleWithResponse(ctx, api.CreateChatScheduleJSONRequestBody{
			IdempotencyKey: uuid.New(), SessionId: sessionID, DisplayName: "daily", Prompt: "review", CronExpression: "0 9 * * *", Timezone: "UTC",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(scheduled.StatusCode()).To(Equal(http.StatusCreated), string(scheduled.Body))

		ended, err := client.EndSessionWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ended.StatusCode()).To(Equal(http.StatusOK), string(ended.Body))
		Expect(ended.JSON200.Status).To(Equal(api.SessionStatusEnded))
		Eventually(closed).WithTimeout(projectionBudget).Should(BeClosed())
		finalTurn, err := queries.GetTurn(ctx, unfinishedPrompt.TurnID)
		Expect(err).NotTo(HaveOccurred())
		Expect(finalTurn.Status).To(Equal(string(api.TurnStatusAborted)))
		finalRequest, err := queries.GetSessionRequest(ctx, requestBeforeEnd)
		Expect(err).NotTo(HaveOccurred())
		Expect(finalRequest.Status).To(Equal(string(api.Denied)))
		Expect(finalRequest.DeliveryStatus).To(Equal("abandoned"))
		finalSubagent, err := queries.GetSubagent(ctx, storedb.GetSubagentParams{SessionID: sessionID, ID: subagentID})
		Expect(err).NotTo(HaveOccurred())
		Expect(finalSubagent.Status).To(Equal(string(api.SubagentStatusFailed)))
		Expect(finalSubagent.CompletedAt.Valid).To(BeTrue())
		allEvents, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
			SessionID: sessionID, RowLimit: 100,
		})
		Expect(err).NotTo(HaveOccurred())
		terminalSubagentEvent := false
		for _, event := range allEvents {
			if event.Kind != string(api.SessionEventKindSubagentUpdated) || !event.SubagentID.Valid || event.SubagentID.String != subagentID {
				continue
			}
			version, versionErr := queries.GetSubagentEventVersion(ctx, storedb.GetSubagentEventVersionParams{
				SessionID: sessionID, EventSeq: event.Seq,
			})
			Expect(versionErr).NotTo(HaveOccurred())
			terminalSubagentEvent = terminalSubagentEvent || version.Status == string(api.SubagentStatusFailed)
		}
		Expect(terminalSubagentEvent).To(BeTrue(), "ending the session must announce the failed subagent snapshot")
		finalSchedule, err := queries.GetChatSchedule(ctx, *scheduled.JSON201.Id)
		Expect(err).NotTo(HaveOccurred())
		Expect(finalSchedule.Status).To(Equal(string(api.ChatScheduleStatusPaused)))
		lateSchedule, err := client.CreateChatScheduleWithResponse(ctx, api.CreateChatScheduleJSONRequestBody{
			IdempotencyKey: uuid.New(), SessionId: sessionID, DisplayName: "late", Prompt: "should not run", CronExpression: "0 10 * * *", Timezone: "UTC",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(lateSchedule.StatusCode()).To(Equal(http.StatusConflict), string(lateSchedule.Body))
		newName := "too late"
		lateUpdate, err := client.UpdateSessionWithResponse(ctx, sessionID, api.UpdateSessionJSONRequestBody{DisplayName: &newName})
		Expect(err).NotTo(HaveOccurred())
		Expect(lateUpdate.StatusCode()).To(Equal(http.StatusConflict), string(lateUpdate.Body))
		Expect(lateUpdate.JSON409.Code).To(Equal("session_terminal"))

		refused, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{IdempotencyKey: uuid.New(), Text: "late", Mode: api.Queue})
		Expect(err).NotTo(HaveOccurred())
		Expect(refused.StatusCode()).To(Equal(http.StatusConflict))
		Expect(refused.JSON409).NotTo(BeNil())
	})

	It("rejects a nil prompt idempotency key before persistence or CLI delivery", func() {
		worktrees.EXPECT().Prepare(gomock.Any(), gomock.Any()).Return(copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD"},
			Path:       "/tmp/work", BaseRef: "HEAD",
		}, nil)
		bridge.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(copilotadapter.BridgeSession{
			Events: events,
			Send: func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
				sent <- prompt
				return copilotadapter.BridgePromptDeliveryAccepted, nil
			},
			Close: func(_ context.Context) error { return nil },
		}, nil)

		created, err := client.CreateSessionWithResponse(ctx, newWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		sessionID := *created.JSON201.Id

		for range 2 {
			response, submitErr := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
				IdempotencyKey: uuid.Nil, Text: "must not run", Mode: api.Queue,
			})
			Expect(submitErr).NotTo(HaveOccurred())
			Expect(response.StatusCode()).To(Equal(http.StatusBadRequest), string(response.Body))
			Expect(response.JSON400).NotTo(BeNil())
			Expect(response.JSON400.Code).To(Equal("invalid_request"))
		}

		Consistently(sent).ShouldNot(Receive(), "a rejected retry must not cross the SDK boundary")
		turnCount, err := queries.CountSessionTurns(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(turnCount).To(BeZero(), "a rejected retry must not create durable turns")
	})

	It("claims a session creation key before provisioning and replays the exact request", func() {
		idempotencyKey := uuid.New()
		agentID := "review-agent"
		var preparedSessionID uuid.UUID
		worktrees.EXPECT().Prepare(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request copilotadapter.WorktreeRequest) (copilotadapter.PreparedWorktree, error) {
				receipt, lookupErr := queries.GetSessionCreation(ctx, idempotencyKey)
				Expect(lookupErr).NotTo(HaveOccurred(), "the durable receipt must exist before worktree provisioning")
				Expect(receipt.SessionID).NotTo(Equal(uuid.Nil))
				Expect(receipt.Model).To(Equal("gpt-5"))
				Expect(receipt.AgentID).To(Equal(null.StringFrom(agentID)))
				Expect(receipt.RepositoryID).To(Equal("repo"))
				Expect(receipt.WorktreeMode).To(Equal("newWorktree"))
				preparedSessionID = receipt.SessionID
				return copilotadapter.PreparedWorktree{
					Repository: copilotadapter.Repository{ID: request.RepositoryID, DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD"},
					Path:       "/tmp/work", BaseRef: "HEAD",
				}, nil
			})
		bridge.EXPECT().CreateSession(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, spec copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
				Expect(spec.AgentID).To(Equal(agentID))
				return copilotadapter.BridgeSession{Close: func(_ context.Context) error { return nil }}, nil
			})

		body := newWorktreeSessionBodyWithKeyAndAgent("gpt-5", "repo", idempotencyKey, &agentID)
		created, err := client.CreateSessionWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		Expect(created.JSON201.Id).To(PointTo(Equal(preparedSessionID)))
		persisted, err := queries.GetSession(ctx, preparedSessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.AgentID).To(Equal(agentID))

		replayed, err := client.CreateSessionWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.StatusCode()).To(Equal(http.StatusCreated), string(replayed.Body))
		Expect(replayed.JSON201.Id).To(Equal(created.JSON201.Id))

		conflicting, err := client.CreateSessionWithResponse(ctx,
			newWorktreeSessionBodyWithKey("different-model", "repo", idempotencyKey))
		Expect(err).NotTo(HaveOccurred())
		Expect(conflicting.StatusCode()).To(Equal(http.StatusConflict), string(conflicting.Body))
		Expect(conflicting.JSON409.Code).To(Equal("idempotency_key_reused"))
	})

	It("replays an exact abort without stopping a successor turn", func() {
		worktrees.EXPECT().Prepare(gomock.Any(), gomock.Any()).Return(copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD"},
			Path:       "/tmp/work", BaseRef: "HEAD",
		}, nil)
		var activeMutex sync.Mutex
		var activeTurn uuid.UUID
		abortCalls := 0
		bridge.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(copilotadapter.BridgeSession{
			Events: events,
			Send: func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
				activeMutex.Lock()
				activeTurn = prompt.TurnID
				activeMutex.Unlock()
				sent <- prompt
				return copilotadapter.BridgePromptDeliveryAccepted, nil
			},
			ActiveTurn: func() (uuid.UUID, bool) {
				activeMutex.Lock()
				defer activeMutex.Unlock()
				return activeTurn, activeTurn != uuid.Nil
			},
			Abort: func(_ context.Context, expectedTurnID uuid.UUID) (uuid.UUID, error) {
				activeMutex.Lock()
				defer activeMutex.Unlock()
				abortCalls += 1
				if activeTurn != expectedTurnID {
					return uuid.Nil, copilotadapter.ErrAbortTargetMismatch
				}
				activeTurn = uuid.Nil
				return expectedTurnID, nil
			},
			Close: func(_ context.Context) error { return nil },
		}, nil)

		created, err := client.CreateSessionWithResponse(ctx, newWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		sessionID := *created.JSON201.Id

		submit := func(text string) uuid.UUID {
			response, submitErr := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
				IdempotencyKey: uuid.New(), Text: text, Mode: api.Queue,
			})
			Expect(submitErr).NotTo(HaveOccurred())
			Expect(response.StatusCode()).To(Equal(http.StatusAccepted), string(response.Body))
			var prompt copilotadapter.BridgePrompt
			Eventually(sent).WithTimeout(projectionBudget).Should(Receive(&prompt))
			Expect(prompt.TurnID).To(Equal(*response.JSON202.Id))
			events <- copilotadapter.BridgeEvent{Kind: copilotadapter.BridgeEventTurnStarted, TurnID: &prompt.TurnID}
			Eventually(func() string {
				turn, lookupErr := queries.GetTurn(ctx, prompt.TurnID)
				if lookupErr != nil {
					return ""
				}
				return turn.Status
			}).WithTimeout(projectionBudget).WithPolling(projectionPoll).Should(Equal(string(api.TurnStatusRunning)))
			return prompt.TurnID
		}

		turnA := submit("turn A")
		abortKey := uuid.New()
		firstAbort, err := client.AbortTurnWithResponse(ctx, sessionID, api.AbortTurnJSONRequestBody{
			IdempotencyKey: abortKey, TurnId: turnA,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(firstAbort.StatusCode()).To(Equal(http.StatusOK), string(firstAbort.Body))
		Expect(firstAbort.JSON200.Id).To(PointTo(Equal(turnA)))

		turnB := submit("turn B")
		replayed, err := client.AbortTurnWithResponse(ctx, sessionID, api.AbortTurnJSONRequestBody{
			IdempotencyKey: abortKey, TurnId: turnA,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.StatusCode()).To(Equal(http.StatusOK), string(replayed.Body))
		Expect(replayed.JSON200.Id).To(PointTo(Equal(turnA)))
		Expect(abortCalls).To(Equal(1), "the exact retry must not cross the SDK boundary again")

		active, err := client.GetActiveTurnWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(active.StatusCode()).To(Equal(http.StatusOK), string(active.Body))
		Expect(active.JSON200.Id).To(PointTo(Equal(turnB)))

		reusedKey, err := client.AbortTurnWithResponse(ctx, sessionID, api.AbortTurnJSONRequestBody{
			IdempotencyKey: abortKey, TurnId: turnB,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(reusedKey.StatusCode()).To(Equal(http.StatusConflict), string(reusedKey.Body))
		Expect(reusedKey.JSON409.Code).To(Equal("idempotency_key_reused"))

		staleTarget, err := client.AbortTurnWithResponse(ctx, sessionID, api.AbortTurnJSONRequestBody{
			IdempotencyKey: uuid.New(), TurnId: turnA,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(staleTarget.StatusCode()).To(Equal(http.StatusConflict), string(staleTarget.Body))
		Expect(staleTarget.JSON409.Code).To(Equal("abort_target_changed"))
		Expect(abortCalls).To(Equal(1))
	})

	It("groups multiple chats in a reused or selected existing worktree", func() {
		root := "/tmp/shared-worktree"
		prepared := copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: root, DefaultRef: "HEAD"},
			Path:       root, BaseRef: "HEAD",
		}
		worktrees.EXPECT().Prepare(gomock.Any(), gomock.Any()).Return(prepared, nil).Times(2)
		bridge.EXPECT().CreateSession(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
				return copilotadapter.BridgeSession{Close: func(_ context.Context) error { return nil }}, nil
			}).Times(3)
		first, err := client.CreateSessionWithResponse(ctx, currentWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(first.StatusCode()).To(Equal(http.StatusCreated), string(first.Body))
		second, err := client.CreateSessionWithResponse(ctx, currentWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(second.StatusCode()).To(Equal(http.StatusCreated), string(second.Body))
		Expect(second.JSON201.WorktreeId).To(Equal(first.JSON201.WorktreeId))

		worktreeID := *first.JSON201.WorktreeId
		third, err := client.CreateSessionWithResponse(ctx, existingWorktreeSessionBody("gpt-5", "repo", worktreeID))
		Expect(err).NotTo(HaveOccurred())
		Expect(third.StatusCode()).To(Equal(http.StatusCreated), string(third.Body))
		Expect(third.JSON201.WorktreeId).To(PointTo(Equal(worktreeID)))

		rows, err := queries.ListWorktrees(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		count, err := queries.CountWorktreeSessions(ctx, worktreeID)
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(int64(3)))
	})

	It("refuses to mutate a persisted session that has no live CLI handle", func() {
		// The restart case: the rows survived, the process did not, so nothing
		// the adapter answers 200 to could ever reach a CLI.
		now := time.Now().UTC()
		sessionID := uuid.New()
		worktreeID := uuid.New()
		_, err := queries.CreateWorktree(ctx, storedb.CreateWorktreeParams{
			ID: worktreeID, RepositoryID: "repo", RepositoryRoot: "/tmp/work", Path: "/tmp/work",
			BaseRef: "HEAD", CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.CreateSession(ctx, storedb.CreateSessionParams{
			ID: sessionID, WorktreeID: worktreeID, DisplayName: "restarted", Model: "gpt-5", WorkingDirectory: "/tmp/work",
			SystemInstructions: "", Status: "idle", CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(queries.EnsureSessionCounter(ctx, sessionID)).To(Succeed())

		refused, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: "hello", Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(refused.StatusCode()).To(Equal(http.StatusConflict), string(refused.Body))
		Expect(refused.JSON409.Code).To(Equal("session_not_live"))

		unchanged, err := client.GetSessionWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(unchanged.JSON200.TurnCount).To(PointTo(BeEquivalentTo(0)))
		Expect(unchanged.JSON200.LastTurnAt).To(BeNil())

		turn, err := queries.CreateTurn(ctx, storedb.CreateTurnParams{
			ID: uuid.New(), SessionID: sessionID, Status: "running", PromptText: "earlier",
			PromptMode: "queue", Author: "ada", CreatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		stopped, err := client.AbortTurnWithResponse(ctx, sessionID, api.AbortTurnJSONRequestBody{
			IdempotencyKey: uuid.New(), TurnId: turn.ID,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(stopped.StatusCode()).To(Equal(http.StatusConflict), string(stopped.Body))
		Expect(stopped.JSON409.Code).To(Equal("session_not_live"))
		stillRunning, err := queries.GetTurn(ctx, turn.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(stillRunning.Status).To(Equal("running"))

		requestID := uuid.New()
		_, err = queries.InsertSessionRequest(ctx, storedb.InsertSessionRequestParams{
			ID: requestID, SessionID: sessionID, TurnID: &turn.ID,
			Kind: "permission", Status: "pending", Prompt: "run rm -rf build?",
			ToolName: null.NewString("bash", true), CreatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		undeliverable, err := client.ResolveSessionRequestWithResponse(ctx, sessionID, requestID,
			resolutionBody(api.Approve))
		Expect(err).NotTo(HaveOccurred())
		Expect(undeliverable.StatusCode()).To(Equal(http.StatusConflict), string(undeliverable.Body))
		Expect(undeliverable.JSON409.Code).To(Equal("session_not_live"))
		stillPending, err := client.ListSessionRequestsWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(stillPending.JSON200.Data).To(HaveLen(1))
	})

	It("delivers exactly the two permission decisions", func() {
		worktrees.EXPECT().Prepare(gomock.Any(), gomock.Any()).Return(copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD"},
			Path:       "/tmp/work", BaseRef: "HEAD",
		}, nil)
		deliveries := make(chan copilotadapter.BridgeResolution, 2)
		bridge.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(copilotadapter.BridgeSession{
			Resolve: func(_ context.Context, resolution copilotadapter.BridgeResolution) error {
				deliveries <- resolution
				return nil
			},
			Close: func(_ context.Context) error { return nil },
		}, nil)
		created, err := client.CreateSessionWithResponse(ctx, newWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		sessionID := *created.JSON201.Id
		accepted := []struct {
			decision api.ResolveDecision
			status   api.SessionRequestStatus
		}{
			{decision: api.Approve, status: api.Approved},
			{decision: api.Deny, status: api.Denied},
		}
		var deniedRequestID uuid.UUID
		for _, acceptedDecision := range accepted {
			requestID := uuid.New()
			if acceptedDecision.decision == api.Deny {
				deniedRequestID = requestID
			}
			_, err := queries.InsertSessionRequest(ctx, storedb.InsertSessionRequestParams{
				ID: requestID, SessionID: sessionID, Kind: string(api.Permission),
				Status: string(api.Pending), Prompt: "resolve this request",
				ToolName: null.NewString("bash", true), CreatedAt: time.Now().UTC(),
			})
			Expect(err).NotTo(HaveOccurred())
			response, err := client.ResolveSessionRequestWithResponse(ctx, sessionID, requestID,
				resolutionBody(acceptedDecision.decision))
			Expect(err).NotTo(HaveOccurred())
			Expect(response.StatusCode()).To(Equal(http.StatusOK), string(response.Body))
			Expect(response.JSON200.Status).To(Equal(acceptedDecision.status))
			Eventually(deliveries).WithTimeout(projectionBudget).Should(Receive(Equal(copilotadapter.BridgeResolution{
				RequestID: requestID, Decision: string(acceptedDecision.decision),
			})))
		}

		retried, err := client.ResolveSessionRequestWithResponse(ctx, sessionID, deniedRequestID,
			resolutionBody(api.Deny))
		Expect(err).NotTo(HaveOccurred())
		Expect(retried.StatusCode()).To(Equal(http.StatusOK), string(retried.Body))
		Consistently(deliveries).ShouldNot(Receive(), "an idempotent retry must not cross the SDK boundary twice")

		transcript, err := client.ListTranscriptWithResponse(ctx, sessionID, &api.ListTranscriptParams{})
		Expect(err).NotTo(HaveOccurred())
		Expect(transcript.StatusCode()).To(Equal(http.StatusOK), string(transcript.Body))
		Expect(transcript.JSON200.Data).To(HaveLen(1), "only denial needs a durable acknowledgement")
		Expect(transcript.JSON200.Data[0]).To(And(
			HaveField("Kind", Equal(api.TranscriptItemKindSystemNotice)),
			HaveField("Text", Equal("Permission denied for bash.")),
		))
	})

	It("serializes ending a session behind an in-flight prompt delivery", func() {
		worktrees.EXPECT().Prepare(gomock.Any(), gomock.Any()).Return(copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD"},
			Path:       "/tmp/work", BaseRef: "HEAD",
		}, nil)
		sendStarted := make(chan struct{})
		releaseSend := make(chan struct{})
		closeCalled := make(chan struct{})
		bridge.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(copilotadapter.BridgeSession{
			Send: func(_ context.Context, _ copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
				close(sendStarted)
				<-releaseSend
				return copilotadapter.BridgePromptDeliveryAccepted, nil
			},
			Close: func(_ context.Context) error {
				close(closeCalled)
				return nil
			},
		}, nil)
		created, err := client.CreateSessionWithResponse(ctx, newWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		sessionID := *created.JSON201.Id

		promptResponses := make(chan *api.SubmitPromptResponse, 1)
		promptErrors := make(chan error, 1)
		go func() {
			response, promptErr := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
				IdempotencyKey: uuid.New(), Text: "commit before close", Mode: api.Queue,
			})
			promptResponses <- response
			promptErrors <- promptErr
		}()
		Eventually(sendStarted).Should(BeClosed())

		endResponses := make(chan *api.EndSessionResponse, 1)
		endErrors := make(chan error, 1)
		go func() {
			response, endErr := client.EndSessionWithResponse(ctx, sessionID)
			endResponses <- response
			endErrors <- endErr
		}()
		Consistently(func() bool {
			select {
			case <-closeCalled:
				return true
			default:
				return false
			}
		}, 150*time.Millisecond, 10*time.Millisecond).Should(BeFalse())

		close(releaseSend)
		var prompted *api.SubmitPromptResponse
		Eventually(promptResponses).Should(Receive(&prompted))
		Expect(<-promptErrors).NotTo(HaveOccurred())
		Expect(prompted.StatusCode()).To(Equal(http.StatusAccepted), string(prompted.Body))
		var ended *api.EndSessionResponse
		Eventually(endResponses).Should(Receive(&ended))
		Expect(<-endErrors).NotTo(HaveOccurred())
		Expect(ended.StatusCode()).To(Equal(http.StatusOK), string(ended.Body))
		Expect(closeCalled).To(BeClosed())
		turn, err := queries.GetTurn(ctx, *prompted.JSON202.Id)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn.DeliveryStatus).To(Equal("accepted"))
		Expect(turn.Status).To(Equal(string(api.TurnStatusAborted)))
	})

	It("drops bridge events observed after a terminal session transition", func() {
		worktrees.EXPECT().Prepare(gomock.Any(), gomock.Any()).Return(copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD"},
			Path:       "/tmp/work", BaseRef: "HEAD",
		}, nil)
		lateEvents := make(chan copilotadapter.BridgeEvent)
		bridge.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(copilotadapter.BridgeSession{
			Events: lateEvents,
			Close:  func(_ context.Context) error { return errors.New("disconnect failed") },
		}, nil)
		created, err := client.CreateSessionWithResponse(ctx, newWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		sessionID := *created.JSON201.Id

		ended, err := client.EndSessionWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ended.StatusCode()).To(Equal(http.StatusInternalServerError), string(ended.Body))
		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Status).To(Equal(string(api.SessionStatusEnded)))

		requestID := uuid.New()
		observed := make(chan struct{})
		go func() {
			lateEvents <- copilotadapter.BridgeEvent{
				Kind: copilotadapter.BridgeEventRequestOpened, RequestID: &requestID,
				RequestKind: copilotadapter.BridgeRequestPermission, Text: "too late",
			}
			lateEvents <- copilotadapter.BridgeEvent{
				Kind: copilotadapter.BridgeEventSubagentStarted, AgentID: "late-worker", DisplayName: "Late Worker",
			}
			// Reading this barrier means the projector finished both mutations
			// above because it handles its channel serially.
			lateEvents <- copilotadapter.BridgeEvent{Kind: copilotadapter.BridgeEventAssistantDelta}
			close(observed)
		}()
		Eventually(observed).Should(BeClosed())
		_, err = queries.GetSessionRequest(ctx, requestID)
		Expect(err).To(MatchError(sql.ErrNoRows))
		subagents, err := queries.ListSubagents(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(subagents).To(BeEmpty())
	})

	DescribeTable("closes a caught-up terminal stream from its authoritative snapshot", func(status api.TerminalStatus) {
		worktreeID, terminalID := uuid.New(), uuid.New()
		cursor := int64(7)
		now := time.Now().UTC()
		snapshot := copilotadapter.TerminalSnapshot{
			ID: terminalID, WorktreeID: worktreeID, Rows: 24, Columns: 80,
			Shell: "/bin/sh", Status: string(status), CreatedAt: now, UpdatedAt: now,
		}
		terminals.EXPECT().Get(terminalID).Return(snapshot, true)
		terminals.EXPECT().EventsAfter(terminalID, cursor).Return(copilotadapter.TerminalEventReplay{
			Snapshot: snapshot, Events: []copilotadapter.TerminalOutput{},
		}, true)

		streamContext, cancelStream := context.WithTimeout(ctx, projectionBudget)
		defer cancelStream()
		response, err := client.StreamTerminalEventsWithResponse(streamContext, worktreeID, terminalID,
			&api.StreamTerminalEventsParams{LastEventID: &cursor})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusOK), string(response.Body))
		Expect(response.Body).To(BeEmpty())
	},
		Entry("after exit", api.TerminalStatusExited),
		Entry("after failure", api.TerminalStatusFailed),
	)

	It("delivers a terminal replay that arrives after an empty running read", func() {
		worktreeID, terminalID := uuid.New(), uuid.New()
		cursor := int64(7)
		now := time.Now().UTC()
		running := copilotadapter.TerminalSnapshot{
			ID: terminalID, WorktreeID: worktreeID, Rows: 24, Columns: 80,
			Shell: "/bin/sh", Status: string(api.TerminalStatusRunning), CreatedAt: now, UpdatedAt: now,
		}
		exitCode := int32(0)
		exited := running
		exited.Status = string(api.TerminalStatusExited)
		exited.ExitCode = &exitCode
		exited.UpdatedAt = now.Add(time.Millisecond)
		changed := make(chan struct{})
		close(changed) // Output may arrive between reading the snapshot and waiting.
		gomock.InOrder(
			terminals.EXPECT().Get(terminalID).Return(running, true),
			terminals.EXPECT().EventsAfter(terminalID, cursor).Return(copilotadapter.TerminalEventReplay{
				Snapshot: running, Events: []copilotadapter.TerminalOutput{}, Changed: changed,
			}, true),
			terminals.EXPECT().EventsAfter(terminalID, cursor).Return(copilotadapter.TerminalEventReplay{
				Snapshot: exited,
				Events: []copilotadapter.TerminalOutput{
					{Seq: 8, TerminalID: terminalID, Kind: string(api.TerminalEventKindOutput), Data: "done\n", OccurredAt: now},
					{Seq: 9, TerminalID: terminalID, Kind: string(api.TerminalEventKindExited), ExitCode: &exitCode, OccurredAt: now.Add(time.Millisecond)},
				},
			}, true),
		)

		streamContext, cancelStream := context.WithTimeout(ctx, projectionBudget)
		defer cancelStream()
		response, err := client.StreamTerminalEventsWithResponse(streamContext, worktreeID, terminalID,
			&api.StreamTerminalEventsParams{LastEventID: &cursor})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusOK), string(response.Body))
		body := string(response.Body)
		Expect(body).To(ContainSubstring("id:8"))
		Expect(body).To(ContainSubstring("id:9"))
		Expect(body).To(ContainSubstring(`"kind":"output"`))
		Expect(body).To(ContainSubstring(`"kind":"exited"`))
	})

	It("answers an unknown session with the shared Error shape", func() {
		missing, err := client.GetSessionWithResponse(ctx, uuid.New())
		Expect(err).NotTo(HaveOccurred())
		Expect(missing.StatusCode()).To(Equal(http.StatusNotFound))
		Expect(missing.JSON404).NotTo(BeNil())
		Expect(missing.JSON404.Code).To(Equal("session_not_found"))
	})

	It("rejects a malformed session id before any handler runs", func() {
		response, err := http.Get(server.URL + "/v1/sessions/not-a-uuid")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = response.Body.Close() }()
		Expect(response.StatusCode).To(Equal(http.StatusBadRequest))
	})
})

// readEventFrames opens the SSE stream with Last-Event-ID and returns the
// frames it receives until the first blank line after an id, then hangs up.
func readEventFrames(ctx context.Context, base string, sessionID uuid.UUID, lastEventID string) []string {
	streamCtx, cancel := context.WithTimeout(ctx, projectionBudget)
	defer cancel()
	request, err := http.NewRequestWithContext(streamCtx, http.MethodGet, base+"/v1/sessions/"+sessionID.String()+"/events", nil)
	Expect(err).NotTo(HaveOccurred())
	request.Header.Set("Last-Event-ID", lastEventID)
	request.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(request)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = response.Body.Close() }()
	Expect(response.StatusCode).To(Equal(http.StatusOK))
	scanner := bufio.NewScanner(response.Body)
	frames := []string{}
	current := []string{}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(current) > 0 {
				frames = append(frames, strings.Join(current, "\n"))
				return frames
			}
			continue
		}
		current = append(current, line)
	}
	return frames
}
