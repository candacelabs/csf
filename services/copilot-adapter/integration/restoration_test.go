package integration_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"
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
	"github.com/candacelabs/csf/services/copilot-adapter/worktreeadapter"
)

type shutdownReconcileStore struct {
	*cron.MemoryStore
	zeroReconciled chan struct{}
}

func runRestorationGit(directory string, arguments ...string) string {
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), string(output))
	return string(output)
}

func (store *shutdownReconcileStore) Reconcile(
	ctx context.Context,
	definitions []cron.JobDefinition,
	now time.Time,
) ([]cron.JobState, error) {
	states, err := store.MemoryStore.Reconcile(ctx, definitions, now)
	if err != nil || len(definitions) != 0 {
		return states, err
	}
	store.zeroReconciled <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

var _ = Describe("process restart restoration", func() {
	DescribeTable("recovers an incomplete creation receipt through the deterministic worktree identity",
		func(precreateResidue bool) {
			ctx := context.Background()
			database := pgmem.MustNew()
			DeferCleanup(database.Close)
			db := database.Open()
			DeferCleanup(db.Close)
			Expect(store.ApplyMigrations(ctx, db)).To(Succeed())
			queries := storedb.New(db)

			root := GinkgoT().TempDir()
			repositoryRoot := filepath.Join(root, "repository")
			managedRoot := filepath.Join(root, "worktrees")
			Expect(os.MkdirAll(repositoryRoot, 0o750)).To(Succeed())
			Expect(os.MkdirAll(managedRoot, 0o750)).To(Succeed())
			runRestorationGit(repositoryRoot, "init", "--initial-branch=main")
			runRestorationGit(repositoryRoot, "config", "user.name", "Candace Test")
			runRestorationGit(repositoryRoot, "config", "user.email", "test@example.invalid")
			Expect(os.WriteFile(filepath.Join(repositoryRoot, "README.md"), []byte("fixture\n"), 0o600)).To(Succeed())
			runRestorationGit(repositoryRoot, "add", "README.md")
			runRestorationGit(repositoryRoot, "commit", "-m", "fixture")

			sessionID, idempotencyKey := uuid.New(), uuid.New()
			_, err := queries.ClaimSessionCreation(ctx, storedb.ClaimSessionCreationParams{
				IdempotencyKey: idempotencyKey, SessionID: sessionID, Model: "gpt-5",
				RepositoryID: "repo", WorktreeMode: string(api.NewWorktree), CreatedAt: time.Now().UTC(),
			})
			Expect(err).NotTo(HaveOccurred())
			expectedPath := filepath.Join(managedRoot, "chat-"+sessionID.String())
			expectedBranch := "codex/chat-" + sessionID.String()
			if precreateResidue {
				runRestorationGit(repositoryRoot, "worktree", "add", "-b", expectedBranch, expectedPath, "HEAD")
			}

			manager, err := worktreeadapter.NewWorktreeManager(worktreeadapter.Config{
				Repositories: []copilotadapter.Repository{{
					ID: "repo", DisplayName: "Repo", Root: repositoryRoot, DefaultRef: "HEAD",
				}},
				WorktreeRoot: managedRoot,
			})
			Expect(err).NotTo(HaveOccurred())
			controller := gomock.NewController(GinkgoT())
			bridge := NewMockICopilotBridge(controller)
			terminals := NewMockITerminalManager(controller)
			terminals.EXPECT().Close().Return(nil).AnyTimes()
			bridge.EXPECT().CreateSession(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, spec copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
					Expect(spec.SessionID).To(Equal(sessionID))
					Expect(spec.WorkingDirectory).To(Equal(expectedPath))
					return copilotadapter.BridgeSession{Close: func(_ context.Context) error { return nil }}, nil
				},
			).Times(1)
			postgresStore, err := store.NewPostgresStore(db)
			Expect(err).NotTo(HaveOccurred())
			service, err := copilotadapter.NewCopilotAdapter(
				copilotadapter.WithBridge(bridge), copilotadapter.WithStore(postgresStore),
				copilotadapter.WithWorktreeManager(manager), copilotadapter.WithTerminalManager(terminals),
				copilotadapter.WithScheduleStore(cron.NewMemoryStore()),
			)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(service.Close)
			restoreContext, cancelRestore := context.WithTimeout(ctx, 3*time.Second)
			defer cancelRestore()
			Expect(service.RestoreSessions(restoreContext)).To(Succeed())
			Expect(service.RestoreSessions(restoreContext)).To(Succeed())

			session, err := queries.GetSession(ctx, sessionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(session.Status).To(Equal(string(api.SessionStatusIdle)))
			Expect(session.WorkingDirectory).To(Equal(expectedPath))
			receipt, err := queries.GetSessionCreation(ctx, idempotencyKey)
			Expect(err).NotTo(HaveOccurred())
			Expect(receipt.SessionID).To(Equal(sessionID))
			Expect(receipt.SdkCreateAttemptID).NotTo(BeNil())
			Expect(receipt.CompletedAt.Valid).To(BeTrue())
			worktrees, err := queries.ListWorktrees(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(worktrees).To(ConsistOf(And(
				HaveField("ID", Equal(sessionID)),
				HaveField("Path", Equal(expectedPath)),
			)))
			Expect(runRestorationGit(expectedPath, "branch", "--show-current")).To(Equal(expectedBranch + "\n"))
		},
		Entry("from a receipt with no filesystem residue", false),
		Entry("from a receipt plus its full-UUID branch and path", true),
	)

	It("resumes a starting receipt with a durable SDK attempt without creating again", func() {
		ctx := context.Background()
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		Expect(store.ApplyMigrations(ctx, db)).To(Succeed())
		queries := storedb.New(db)
		now := time.Now().UTC()
		sessionID, idempotencyKey, attemptID := uuid.New(), uuid.New(), uuid.New()
		receipt, err := queries.ClaimSessionCreation(ctx, storedb.ClaimSessionCreationParams{
			IdempotencyKey: idempotencyKey, SessionID: sessionID, Model: "gpt-5",
			RepositoryID: "repo", WorktreeMode: string(api.NewWorktree), CreatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		receipt, err = queries.BeginSessionCreationSDKAttempt(ctx, storedb.BeginSessionCreationSDKAttemptParams{
			SdkCreateAttemptID: &attemptID, IdempotencyKey: idempotencyKey, SessionID: sessionID,
		})
		Expect(err).NotTo(HaveOccurred())
		prepared := copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/repository", DefaultRef: "HEAD"},
			Path:       "/tmp/worktrees/chat-" + sessionID.String(), BaseRef: "HEAD", Managed: true,
		}
		_, err = queries.CreateWorktree(ctx, storedb.CreateWorktreeParams{
			ID: sessionID, RepositoryID: prepared.Repository.ID, RepositoryRoot: prepared.Repository.Root,
			Path: prepared.Path, BaseRef: prepared.BaseRef, Managed: true, CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.CreateSession(ctx, storedb.CreateSessionParams{
			ID: sessionID, WorktreeID: sessionID, DisplayName: "starting", Model: "gpt-5",
			WorkingDirectory: prepared.Path, Status: string(api.SessionStatusStarting), CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(queries.EnsureSessionCounter(ctx, sessionID)).To(Succeed())

		controller := gomock.NewController(GinkgoT())
		bridge := NewMockICopilotBridge(controller)
		worktrees := NewMockIWorktreeManager(controller)
		terminals := NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil).AnyTimes()
		worktrees.EXPECT().Reuse(gomock.Any(), "repo", prepared.Path).Return(prepared, nil)
		worktrees.EXPECT().Prepare(gomock.Any(), copilotadapter.WorktreeRequest{
			RepositoryID: "repo", Mode: string(api.NewWorktree), SessionID: sessionID,
		}).Return(prepared, nil)
		bridge.EXPECT().ResumeSession(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, spec copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
				Expect(spec.SessionID).To(Equal(sessionID))
				return copilotadapter.BridgeSession{Close: func(_ context.Context) error { return nil }}, nil
			},
		).Times(1)
		postgresStore, err := store.NewPostgresStore(db)
		Expect(err).NotTo(HaveOccurred())
		service, err := copilotadapter.NewCopilotAdapter(
			copilotadapter.WithBridge(bridge), copilotadapter.WithStore(postgresStore),
			copilotadapter.WithWorktreeManager(worktrees), copilotadapter.WithTerminalManager(terminals),
			copilotadapter.WithScheduleStore(cron.NewMemoryStore()),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(service.Close)
		restored := make(chan error, 1)
		go func() { restored <- service.RestoreSessions(ctx) }()
		Eventually(restored).WithTimeout(3 * time.Second).Should(Receive(Succeed()))
		Expect(service.RestoreSessions(ctx)).To(Succeed())
		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Status).To(Equal(string(api.SessionStatusIdle)))
		completed, err := queries.GetSessionCreation(ctx, idempotencyKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(completed.SdkCreateAttemptID).To(Equal(receipt.SdkCreateAttemptID))
		Expect(completed.CompletedAt.Valid).To(BeTrue())
	})

	It("replays one completed failed creation unchanged across process restart", func() {
		ctx := context.Background()
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		Expect(store.ApplyMigrations(ctx, db)).To(Succeed())
		queries := storedb.New(db)
		controller := gomock.NewController(GinkgoT())
		bridge := NewMockICopilotBridge(controller)
		worktrees := NewMockIWorktreeManager(controller)
		terminals := NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil).AnyTimes()
		prepared := copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/repository", DefaultRef: "HEAD"},
			Path:       "/tmp/failed-create", BaseRef: "HEAD", Managed: true,
		}
		worktrees.EXPECT().Prepare(gomock.Any(), gomock.Any()).Return(prepared, nil).Times(1)
		worktrees.EXPECT().Reuse(gomock.Any(), "repo", prepared.Path).Return(prepared, nil).Times(1)
		bridge.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(
			copilotadapter.BridgeSession{}, errors.New("SDK create acknowledgement was lost"),
		).Times(1)
		bridge.EXPECT().ResumeSession(gomock.Any(), gomock.Any()).Return(
			copilotadapter.BridgeSession{}, copilotadapter.ErrBridgeSessionMissing,
		).Times(1)
		postgresStore, err := store.NewPostgresStore(db)
		Expect(err).NotTo(HaveOccurred())
		newService := func() *copilotadapter.CopilotAdapter {
			service, serviceErr := copilotadapter.NewCopilotAdapter(
				copilotadapter.WithBridge(bridge), copilotadapter.WithStore(postgresStore),
				copilotadapter.WithWorktreeManager(worktrees), copilotadapter.WithTerminalManager(terminals),
				copilotadapter.WithScheduleStore(cron.NewMemoryStore()),
			)
			Expect(serviceErr).NotTo(HaveOccurred())
			return service
		}
		newClient := func(service *copilotadapter.CopilotAdapter) (*api.ClientWithResponses, *httptest.Server) {
			engine := httpserver.NewEngine("copilot-adapter-failed-create-replay-test")
			Expect(service.Register(engine)).To(Succeed())
			server := httptest.NewServer(engine)
			client, clientErr := api.NewClientWithResponses(server.URL)
			Expect(clientErr).NotTo(HaveOccurred())
			return client, server
		}

		idempotencyKey := uuid.New()
		body := newWorktreeSessionBodyWithKey("gpt-5", "repo", idempotencyKey)
		firstService := newService()
		firstClient, firstServer := newClient(firstService)
		first, err := firstClient.CreateSessionWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.StatusCode()).To(Equal(http.StatusCreated), string(first.Body))
		Expect(first.JSON201.Status).To(Equal(api.SessionStatusFailed))
		exact, err := firstClient.CreateSessionWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(exact.StatusCode()).To(Equal(first.StatusCode()), string(exact.Body))
		Expect(exact.JSON201).To(Equal(first.JSON201))
		firstServer.Close()
		Expect(firstService.Close()).To(Succeed())

		restartedService := newService()
		DeferCleanup(restartedService.Close)
		restoreContext, cancelRestore := context.WithTimeout(ctx, 3*time.Second)
		defer cancelRestore()
		Expect(restartedService.RestoreSessions(restoreContext)).To(Succeed())
		restartedClient, restartedServer := newClient(restartedService)
		DeferCleanup(restartedServer.Close)
		restarted, err := restartedClient.CreateSessionWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(restarted.StatusCode()).To(Equal(first.StatusCode()), string(restarted.Body))
		Expect(restarted.JSON201).To(Equal(first.JSON201))
		receipt, err := queries.GetSessionCreation(ctx, idempotencyKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.CompletedAt.Valid).To(BeTrue())
		Expect(receipt.SessionID).To(Equal(*first.JSON201.Id))
	})

	It("quarantines a starting session with an invalid worktree without deadlocking", func() {
		ctx := context.Background()
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		Expect(store.ApplyMigrations(ctx, db)).To(Succeed())
		queries := storedb.New(db)
		now := time.Now().UTC()
		sessionID, idempotencyKey := uuid.New(), uuid.New()
		_, err := queries.ClaimSessionCreation(ctx, storedb.ClaimSessionCreationParams{
			IdempotencyKey: idempotencyKey, SessionID: sessionID, Model: "gpt-5",
			RepositoryID: "repo", WorktreeMode: string(api.NewWorktree), CreatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.CreateWorktree(ctx, storedb.CreateWorktreeParams{
			ID: sessionID, RepositoryID: "repo", RepositoryRoot: "/tmp/repository",
			Path: "/tmp/invalid-starting", BaseRef: "HEAD", Managed: true, CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.CreateSession(ctx, storedb.CreateSessionParams{
			ID: sessionID, WorktreeID: sessionID, DisplayName: "invalid starting", Model: "gpt-5",
			WorkingDirectory: "/tmp/invalid-starting", Status: string(api.SessionStatusStarting), CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(queries.EnsureSessionCounter(ctx, sessionID)).To(Succeed())

		controller := gomock.NewController(GinkgoT())
		bridge := NewMockICopilotBridge(controller)
		worktrees := NewMockIWorktreeManager(controller)
		terminals := NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil).AnyTimes()
		worktrees.EXPECT().Reuse(gomock.Any(), "repo", "/tmp/invalid-starting").Return(
			copilotadapter.PreparedWorktree{}, copilotadapter.ErrInvalidWorktree,
		).Times(2)
		postgresStore, err := store.NewPostgresStore(db)
		Expect(err).NotTo(HaveOccurred())
		service, err := copilotadapter.NewCopilotAdapter(
			copilotadapter.WithBridge(bridge), copilotadapter.WithStore(postgresStore),
			copilotadapter.WithWorktreeManager(worktrees), copilotadapter.WithTerminalManager(terminals),
			copilotadapter.WithScheduleStore(cron.NewMemoryStore()),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(service.Close)
		restored := make(chan error, 1)
		go func() { restored <- service.RestoreSessions(ctx) }()
		Eventually(restored).WithTimeout(3 * time.Second).Should(Receive(Succeed()))
		failed, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(failed.Status).To(Equal(string(api.SessionStatusFailed)))
		Expect(failed.EndedAt.Valid).To(BeTrue())
		receipt, err := queries.GetSessionCreation(ctx, idempotencyKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.CompletedAt.Valid).To(BeTrue())
		Expect(service.RestoreSessions(ctx)).To(Succeed())
	})

	It("reattaches a validated persisted session before accepting new work", func() {
		ctx := context.Background()
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		Expect(store.ApplyMigrations(ctx, db)).To(Succeed())
		queries := storedb.New(db)
		now := time.Now().UTC()
		worktreeID, sessionID := uuid.New(), uuid.New()
		activeTurnID, queuedTurnID := uuid.New(), uuid.New()
		firstSteerID, secondSteerID := uuid.New(), uuid.New()
		unknownDeliveryID, pendingDeliveryID, strandedRequestID := uuid.New(), uuid.New(), uuid.New()
		interruptedAgentID := "interrupted-worker"
		_, err := queries.CreateWorktree(ctx, storedb.CreateWorktreeParams{
			ID: worktreeID, RepositoryID: "repo", RepositoryRoot: "/tmp/work", Path: "/tmp/work",
			BaseRef: "HEAD", CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.CreateSession(ctx, storedb.CreateSessionParams{
			ID: sessionID, WorktreeID: worktreeID, DisplayName: "restored", Model: "gpt-5",
			WorkingDirectory: "/tmp/work", SystemInstructions: "be exact", Status: "idle",
			CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(queries.EnsureSessionCounter(ctx, sessionID)).To(Succeed())
		_, err = queries.CreateTurn(ctx, storedb.CreateTurnParams{
			ID: activeTurnID, SessionID: sessionID, Status: "running", PromptText: "before restart",
			PromptMode: "queue", CreatedAt: now, DeliveryStatus: "accepted",
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.CreateTurn(ctx, storedb.CreateTurnParams{
			ID: queuedTurnID, SessionID: sessionID, Status: "queued", PromptText: "queued before restart",
			PromptMode: "queue", CreatedAt: now.Add(time.Millisecond), DeliveryStatus: "accepted",
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.CreateTurn(ctx, storedb.CreateTurnParams{
			ID: firstSteerID, SessionID: sessionID, Status: "queued", PromptText: "first steer before restart",
			PromptMode: "steer", CreatedAt: now.Add(2 * time.Millisecond), DeliveryStatus: "accepted",
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.CreateTurn(ctx, storedb.CreateTurnParams{
			ID: secondSteerID, SessionID: sessionID, Status: "queued", PromptText: "second steer before restart",
			PromptMode: "steer", CreatedAt: now.Add(3 * time.Millisecond), DeliveryStatus: "accepted",
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.CreateTurn(ctx, storedb.CreateTurnParams{
			ID: unknownDeliveryID, SessionID: sessionID, Status: "queued", PromptText: "sdk acknowledgement lost",
			PromptMode: "queue", CreatedAt: now.Add(4 * time.Millisecond), DeliveryStatus: "unknown",
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.CreateTurn(ctx, storedb.CreateTurnParams{
			ID: pendingDeliveryID, SessionID: sessionID, Status: "queued", PromptText: "commit without sdk delivery",
			PromptMode: "queue", CreatedAt: now.Add(5 * time.Millisecond), DeliveryStatus: "pending",
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.InsertSessionRequest(ctx, storedb.InsertSessionRequestParams{
			ID: strandedRequestID, SessionID: sessionID, TurnID: &activeTurnID, Kind: string(api.Permission),
			Status: string(api.Pending), Prompt: "allow before restart", CreatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.PrepareSessionRequestResolution(ctx, storedb.PrepareSessionRequestResolutionParams{
			ID: strandedRequestID, Decision: string(api.Approve),
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.UpsertSubagent(ctx, storedb.UpsertSubagentParams{
			SessionID: sessionID, ID: interruptedAgentID, TurnID: &activeTurnID,
			DisplayName: "Interrupted worker", Status: string(api.SubagentStatusActive),
			StartedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())

		controller := gomock.NewController(GinkgoT())
		bridge := NewMockICopilotBridge(controller)
		worktrees := NewMockIWorktreeManager(controller)
		terminals := NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil).AnyTimes()
		worktrees.EXPECT().Reuse(gomock.Any(), "repo", "/tmp/work").Return(copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD"},
			Path:       "/tmp/work", BaseRef: "HEAD",
		}, nil)
		sent := make(chan copilotadapter.BridgePrompt, 1)
		bridge.EXPECT().ResumeSession(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, spec copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
				Expect(spec.SessionID).To(Equal(sessionID))
				Expect(spec.RestoredTurns).To(BeEmpty())
				for _, turnID := range []uuid.UUID{activeTurnID, queuedTurnID, firstSteerID, secondSteerID, unknownDeliveryID} {
					interrupted, lookupErr := queries.GetTurn(ctx, turnID)
					Expect(lookupErr).NotTo(HaveOccurred())
					Expect(interrupted.Status).To(Equal(string(api.TurnStatusAborted)))
					Expect(interrupted.CompletedAt.Valid).To(BeTrue())
				}
				unknown, lookupErr := queries.GetTurn(ctx, unknownDeliveryID)
				Expect(lookupErr).NotTo(HaveOccurred())
				Expect(unknown.Status).To(Equal(string(api.TurnStatusAborted)))
				Expect(unknown.DeliveryStatus).To(Equal("unknown"))
				failed, lookupErr := queries.GetTurn(ctx, pendingDeliveryID)
				Expect(lookupErr).NotTo(HaveOccurred())
				Expect(failed.Status).To(Equal(string(api.TurnStatusFailed)))
				Expect(failed.DeliveryStatus).To(Equal("failed"))
				Expect(failed.CompletedAt.Valid).To(BeTrue())
				stranded, lookupErr := queries.GetSessionRequest(ctx, strandedRequestID)
				Expect(lookupErr).NotTo(HaveOccurred())
				Expect(stranded.Status).To(Equal(string(api.Denied)))
				Expect(stranded.DeliveryStatus).To(Equal("abandoned"))
				Expect(stranded.ResolvedAt.Valid).To(BeTrue())
				interruptedAgent, lookupErr := queries.GetSubagent(ctx, storedb.GetSubagentParams{
					SessionID: sessionID, ID: interruptedAgentID,
				})
				Expect(lookupErr).NotTo(HaveOccurred())
				Expect(interruptedAgent.Status).To(Equal(string(api.SubagentStatusFailed)))
				Expect(interruptedAgent.CompletedAt.Valid).To(BeTrue())
				reconciledSession, lookupErr := queries.GetSession(ctx, sessionID)
				Expect(lookupErr).NotTo(HaveOccurred())
				Expect(reconciledSession.Status).To(Equal(string(api.SessionStatusIdle)))
				Expect(reconciledSession.EndedAt.Valid).To(BeFalse())
				return copilotadapter.BridgeSession{
					Send: func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
						sent <- prompt
						return copilotadapter.BridgePromptDeliveryAccepted, nil
					},
					Close: func(_ context.Context) error { return nil },
				}, nil
			})
		postgresStore, err := store.NewPostgresStore(db)
		Expect(err).NotTo(HaveOccurred())
		service, err := copilotadapter.NewCopilotAdapter(
			copilotadapter.WithBridge(bridge), copilotadapter.WithStore(postgresStore),
			copilotadapter.WithWorktreeManager(worktrees), copilotadapter.WithTerminalManager(terminals),
			copilotadapter.WithScheduleStore(cron.NewMemoryStore()),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(service.Close)
		Expect(service.RestoreSessions(ctx)).To(Succeed())
		events, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
			SessionID: sessionID, RowLimit: 100,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(ContainElement(And(
			HaveField("Kind", Equal(string(api.SessionEventKindRequestResolved))),
			HaveField("RequestID", PointTo(Equal(strandedRequestID))),
		)))
		Expect(events).To(ContainElement(And(
			HaveField("Kind", Equal(string(api.SessionEventKindSubagentUpdated))),
			HaveField("SubagentID.String", Equal(interruptedAgentID)),
		)))

		engine := httpserver.NewEngine("copilot-adapter-restoration-test")
		Expect(service.Register(engine)).To(Succeed())
		server := httptest.NewServer(engine)
		DeferCleanup(server.Close)
		client, err := api.NewClientWithResponses(server.URL)
		Expect(err).NotTo(HaveOccurred())
		response, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: "after restart", Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusAccepted), string(response.Body))
		Eventually(sent).Should(Receive(HaveField("Text", Equal("after restart"))))
	})

	It("abandons a request with no turn before resume and keeps the idle session usable", func() {
		ctx := context.Background()
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		Expect(store.ApplyMigrations(ctx, db)).To(Succeed())
		queries := storedb.New(db)
		now := time.Now().UTC()
		worktreeID, sessionID, requestID := uuid.New(), uuid.New(), uuid.New()
		_, err := queries.CreateWorktree(ctx, storedb.CreateWorktreeParams{
			ID: worktreeID, RepositoryID: "repo", RepositoryRoot: "/tmp/work", Path: "/tmp/work",
			BaseRef: "HEAD", CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.CreateSession(ctx, storedb.CreateSessionParams{
			ID: sessionID, WorktreeID: worktreeID, DisplayName: "request only", Model: "gpt-5",
			WorkingDirectory: "/tmp/work", Status: string(api.SessionStatusIdle), CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(queries.EnsureSessionCounter(ctx, sessionID)).To(Succeed())
		_, err = queries.InsertSessionRequest(ctx, storedb.InsertSessionRequestParams{
			ID: requestID, SessionID: sessionID, Kind: string(api.Permission), Status: string(api.Pending),
			Prompt: "permission without a correlated owner", CreatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.PrepareSessionRequestResolution(ctx, storedb.PrepareSessionRequestResolutionParams{
			ID: requestID, Decision: string(api.Deny),
		})
		Expect(err).NotTo(HaveOccurred())

		controller := gomock.NewController(GinkgoT())
		bridge := NewMockICopilotBridge(controller)
		worktrees := NewMockIWorktreeManager(controller)
		terminals := NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil).AnyTimes()
		worktrees.EXPECT().Reuse(gomock.Any(), "repo", "/tmp/work").Return(copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD"},
			Path:       "/tmp/work", BaseRef: "HEAD",
		}, nil)
		sent := make(chan copilotadapter.BridgePrompt, 1)
		bridge.EXPECT().ResumeSession(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, spec copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
				Expect(spec.SessionID).To(Equal(sessionID))
				Expect(spec.RestoredTurns).To(BeEmpty())
				request, lookupErr := queries.GetSessionRequest(ctx, requestID)
				Expect(lookupErr).NotTo(HaveOccurred())
				Expect(request.Status).To(Equal(string(api.Denied)))
				Expect(request.Decision).To(Equal(string(api.Deny)))
				Expect(request.DeliveryStatus).To(Equal("abandoned"))
				Expect(request.TurnID).To(BeNil())
				session, lookupErr := queries.GetSession(ctx, sessionID)
				Expect(lookupErr).NotTo(HaveOccurred())
				Expect(session.Status).To(Equal(string(api.SessionStatusIdle)))
				events, lookupErr := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
					SessionID: sessionID, RowLimit: 10,
				})
				Expect(lookupErr).NotTo(HaveOccurred())
				Expect(events).To(ContainElement(And(
					HaveField("Kind", Equal(string(api.SessionEventKindRequestResolved))),
					HaveField("RequestID", PointTo(Equal(requestID))),
				)))
				Expect(events).To(ContainElement(HaveField("Kind", Equal(string(api.SessionEventKindSessionUpdated)))))
				return copilotadapter.BridgeSession{
					Send: func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
						sent <- prompt
						return copilotadapter.BridgePromptDeliveryAccepted, nil
					},
					Close: func(_ context.Context) error { return nil },
				}, nil
			})
		postgresStore, err := store.NewPostgresStore(db)
		Expect(err).NotTo(HaveOccurred())
		service, err := copilotadapter.NewCopilotAdapter(
			copilotadapter.WithBridge(bridge), copilotadapter.WithStore(postgresStore),
			copilotadapter.WithWorktreeManager(worktrees), copilotadapter.WithTerminalManager(terminals),
			copilotadapter.WithScheduleStore(cron.NewMemoryStore()),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(service.Close)
		Expect(service.RestoreSessions(ctx)).To(Succeed())

		engine := httpserver.NewEngine("copilot-adapter-request-only-restoration-test")
		Expect(service.Register(engine)).To(Succeed())
		server := httptest.NewServer(engine)
		DeferCleanup(server.Close)
		client, err := api.NewClientWithResponses(server.URL)
		Expect(err).NotTo(HaveOccurred())
		response, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: "after request-only restart", Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusAccepted), string(response.Body))
		Eventually(sent).Should(Receive(HaveField("Text", Equal("after request-only restart"))))
	})

	It("terminalizes a missing SDK session once and continues restoring later sessions", func() {
		ctx := context.Background()
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		Expect(store.ApplyMigrations(ctx, db)).To(Succeed())
		queries := storedb.New(db)
		now := time.Now().UTC()
		missingWorktreeID, validWorktreeID := uuid.New(), uuid.New()
		missingSessionID, validSessionID := uuid.New(), uuid.New()
		missingTurnID, validTurnID := uuid.New(), uuid.New()
		missingRequestID, missingScheduleID := uuid.New(), uuid.New()
		validScheduleID := uuid.New()
		missingAgentID, validAgentID := "missing-session-worker", "valid-session-worker"
		for _, row := range []storedb.CreateWorktreeParams{
			{ID: missingWorktreeID, RepositoryID: "repo", RepositoryRoot: "/tmp/missing", Path: "/tmp/missing", BaseRef: "HEAD", CreatedAt: now, UpdatedAt: now},
			{ID: validWorktreeID, RepositoryID: "repo", RepositoryRoot: "/tmp/valid", Path: "/tmp/valid", BaseRef: "HEAD", CreatedAt: now.Add(time.Millisecond), UpdatedAt: now},
		} {
			_, err := queries.CreateWorktree(ctx, row)
			Expect(err).NotTo(HaveOccurred())
		}
		for _, row := range []storedb.CreateSessionParams{
			{ID: missingSessionID, WorktreeID: missingWorktreeID, DisplayName: "missing", Model: "gpt-5", WorkingDirectory: "/tmp/missing", Status: "idle", CreatedAt: now, UpdatedAt: now},
			{ID: validSessionID, WorktreeID: validWorktreeID, DisplayName: "valid", Model: "gpt-5", WorkingDirectory: "/tmp/valid", Status: "idle", CreatedAt: now.Add(time.Millisecond), UpdatedAt: now},
		} {
			_, err := queries.CreateSession(ctx, row)
			Expect(err).NotTo(HaveOccurred())
			Expect(queries.EnsureSessionCounter(ctx, row.ID)).To(Succeed())
		}
		for _, row := range []storedb.CreateTurnParams{
			{ID: missingTurnID, SessionID: missingSessionID, Status: "running", PromptText: "orphaned work", PromptMode: "queue", CreatedAt: now, DeliveryStatus: "accepted"},
			{ID: validTurnID, SessionID: validSessionID, Status: "running", PromptText: "restorable work", PromptMode: "queue", CreatedAt: now, DeliveryStatus: "accepted"},
		} {
			_, err := queries.CreateTurn(ctx, row)
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := queries.InsertSessionRequest(ctx, storedb.InsertSessionRequestParams{
			ID: missingRequestID, SessionID: missingSessionID, TurnID: &missingTurnID,
			Kind: string(api.Permission), Status: string(api.Pending), Prompt: "allow orphaned work", CreatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		for _, row := range []storedb.UpsertSubagentParams{
			{SessionID: missingSessionID, ID: missingAgentID, TurnID: &missingTurnID, DisplayName: "Missing Worker", Status: string(api.SubagentStatusActive), StartedAt: now, UpdatedAt: now},
			{SessionID: validSessionID, ID: validAgentID, TurnID: &validTurnID, DisplayName: "Valid Worker", Status: string(api.SubagentStatusActive), StartedAt: now, UpdatedAt: now},
		} {
			_, err := queries.UpsertSubagent(ctx, row)
			Expect(err).NotTo(HaveOccurred())
		}
		for _, row := range []storedb.CreateChatScheduleParams{
			{ID: missingScheduleID, SessionID: missingSessionID, DisplayName: "missing schedule", Prompt: "must pause", CronExpression: "0 0 1 1 *", Timezone: "UTC", Status: string(api.ChatScheduleStatusActive), CreatedAt: now, UpdatedAt: now},
			{ID: validScheduleID, SessionID: validSessionID, DisplayName: "valid schedule", Prompt: "must remain active", CronExpression: "0 0 1 1 *", Timezone: "UTC", Status: string(api.ChatScheduleStatusActive), CreatedAt: now, UpdatedAt: now},
		} {
			_, err := queries.CreateChatSchedule(ctx, row)
			Expect(err).NotTo(HaveOccurred())
		}

		controller := gomock.NewController(GinkgoT())
		bridge := NewMockICopilotBridge(controller)
		worktrees := NewMockIWorktreeManager(controller)
		terminals := NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil).AnyTimes()
		gomock.InOrder(
			worktrees.EXPECT().Reuse(gomock.Any(), "repo", "/tmp/missing").Return(copilotadapter.PreparedWorktree{
				Repository: copilotadapter.Repository{ID: "repo", Root: "/tmp/missing", DefaultRef: "HEAD"}, Path: "/tmp/missing", BaseRef: "HEAD",
			}, nil),
			worktrees.EXPECT().Reuse(gomock.Any(), "repo", "/tmp/valid").Return(copilotadapter.PreparedWorktree{
				Repository: copilotadapter.Repository{ID: "repo", Root: "/tmp/valid", DefaultRef: "HEAD"}, Path: "/tmp/valid", BaseRef: "HEAD",
			}, nil),
		)
		postgresStore, err := store.NewPostgresStore(db)
		Expect(err).NotTo(HaveOccurred())
		storeWithAmbiguousCommit := &ambiguousCommitStore{IStore: postgresStore}
		sent := make(chan copilotadapter.BridgePrompt, 1)
		gomock.InOrder(
			bridge.EXPECT().ResumeSession(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, spec copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
					Expect(spec.SessionID).To(Equal(missingSessionID))
					Expect(spec.RestoredTurns).To(BeEmpty())
					storeWithAmbiguousCommit.arm()
					return copilotadapter.BridgeSession{}, copilotadapter.ErrBridgeSessionMissing
				}),
			bridge.EXPECT().ResumeSession(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, spec copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
					Expect(spec.SessionID).To(Equal(validSessionID))
					Expect(spec.RestoredTurns).To(BeEmpty())
					return copilotadapter.BridgeSession{
						Send: func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
							sent <- prompt
							return copilotadapter.BridgePromptDeliveryAccepted, nil
						},
						Close: func(_ context.Context) error { return nil },
					}, nil
				}),
		)
		service, err := copilotadapter.NewCopilotAdapter(
			copilotadapter.WithBridge(bridge), copilotadapter.WithStore(storeWithAmbiguousCommit),
			copilotadapter.WithWorktreeManager(worktrees), copilotadapter.WithTerminalManager(terminals),
			copilotadapter.WithScheduleStore(cron.NewMemoryStore()),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(service.Close)
		restored := make(chan error, 1)
		go func() { restored <- service.RestoreSessions(ctx) }()
		Eventually(restored).WithTimeout(3 * time.Second).Should(Receive(Succeed()))
		Expect(service.RestoreSessions(ctx)).To(Succeed())

		missingSession, err := queries.GetSession(ctx, missingSessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(missingSession.Status).To(Equal(string(api.SessionStatusFailed)))
		Expect(missingSession.EndedAt.Valid).To(BeTrue())
		missingTurn, err := queries.GetTurn(ctx, missingTurnID)
		Expect(err).NotTo(HaveOccurred())
		Expect(missingTurn.Status).To(Equal(string(api.TurnStatusAborted)))
		Expect(missingTurn.CompletedAt.Valid).To(BeTrue())
		missingRequest, err := queries.GetSessionRequest(ctx, missingRequestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(missingRequest.Status).To(Equal(string(api.Denied)))
		Expect(missingRequest.DeliveryStatus).To(Equal("abandoned"))
		missingAgent, err := queries.GetSubagent(ctx, storedb.GetSubagentParams{SessionID: missingSessionID, ID: missingAgentID})
		Expect(err).NotTo(HaveOccurred())
		Expect(missingAgent.Status).To(Equal(string(api.SubagentStatusFailed)))
		missingSchedule, err := queries.GetChatSchedule(ctx, missingScheduleID)
		Expect(err).NotTo(HaveOccurred())
		Expect(missingSchedule.Status).To(Equal(string(api.ChatScheduleStatusPaused)))

		events, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
			SessionID: missingSessionID, RowLimit: 100,
		})
		Expect(err).NotTo(HaveOccurred())
		var turnVersioned, requestVersioned, subagentVersioned bool
		sessionVersions := 0
		for _, event := range events {
			switch {
			case event.Kind == string(api.SessionEventKindTurnCompleted) && event.TurnID != nil && *event.TurnID == missingTurnID:
				version, versionErr := queries.GetTurnEventVersion(ctx, storedb.GetTurnEventVersionParams{SessionID: missingSessionID, EventSeq: event.Seq})
				turnVersioned = versionErr == nil && version.Status == string(api.TurnStatusAborted)
			case event.Kind == string(api.SessionEventKindRequestResolved) && event.RequestID != nil && *event.RequestID == missingRequestID:
				version, versionErr := queries.GetRequestEventVersion(ctx, storedb.GetRequestEventVersionParams{SessionID: missingSessionID, EventSeq: event.Seq})
				requestVersioned = versionErr == nil && version.Status == string(api.Denied)
			case event.Kind == string(api.SessionEventKindSubagentUpdated) && event.SubagentID.String == missingAgentID:
				version, versionErr := queries.GetSubagentEventVersion(ctx, storedb.GetSubagentEventVersionParams{SessionID: missingSessionID, EventSeq: event.Seq})
				subagentVersioned = versionErr == nil && version.Status == string(api.SubagentStatusFailed)
			case event.Kind == string(api.SessionEventKindSessionUpdated):
				version, versionErr := queries.GetSessionEventVersion(ctx, storedb.GetSessionEventVersionParams{SessionID: missingSessionID, EventSeq: event.Seq})
				if versionErr == nil && version.Status == string(api.SessionStatusFailed) {
					sessionVersions++
				}
			}
		}
		Expect(turnVersioned).To(BeTrue())
		Expect(requestVersioned).To(BeTrue())
		Expect(subagentVersioned).To(BeTrue())
		Expect(sessionVersions).To(Equal(1))

		validSession, err := queries.GetSession(ctx, validSessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(validSession.Status).To(Equal(string(api.SessionStatusIdle)))
		validTurn, err := queries.GetTurn(ctx, validTurnID)
		Expect(err).NotTo(HaveOccurred())
		Expect(validTurn.Status).To(Equal(string(api.TurnStatusAborted)))
		Expect(validTurn.CompletedAt.Valid).To(BeTrue())
		validAgent, err := queries.GetSubagent(ctx, storedb.GetSubagentParams{SessionID: validSessionID, ID: validAgentID})
		Expect(err).NotTo(HaveOccurred())
		Expect(validAgent.Status).To(Equal(string(api.SubagentStatusFailed)))
		Expect(validAgent.CompletedAt.Valid).To(BeTrue())
		validSchedule, err := queries.GetChatSchedule(ctx, validScheduleID)
		Expect(err).NotTo(HaveOccurred())
		Expect(validSchedule.Status).To(Equal(string(api.ChatScheduleStatusActive)))

		engine := httpserver.NewEngine("copilot-adapter-missing-session-restoration-test")
		Expect(service.Register(engine)).To(Succeed())
		server := httptest.NewServer(engine)
		DeferCleanup(server.Close)
		client, err := api.NewClientWithResponses(server.URL)
		Expect(err).NotTo(HaveOccurred())
		response, err := client.SubmitPromptWithResponse(ctx, validSessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: "after missing session", Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusAccepted), string(response.Body))
		Eventually(sent).Should(Receive(HaveField("Text", Equal("after missing session"))))
	})

	It("aborts restoration without terminalizing an unclassified resume failure", func() {
		ctx := context.Background()
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		Expect(store.ApplyMigrations(ctx, db)).To(Succeed())
		queries := storedb.New(db)
		now := time.Now().UTC()
		worktreeID, sessionID := uuid.New(), uuid.New()
		_, err := queries.CreateWorktree(ctx, storedb.CreateWorktreeParams{
			ID: worktreeID, RepositoryID: "repo", RepositoryRoot: "/tmp/work", Path: "/tmp/work",
			BaseRef: "HEAD", CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.CreateSession(ctx, storedb.CreateSessionParams{
			ID: sessionID, WorktreeID: worktreeID, DisplayName: "retryable", Model: "gpt-5",
			WorkingDirectory: "/tmp/work", Status: "idle", CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(queries.EnsureSessionCounter(ctx, sessionID)).To(Succeed())

		controller := gomock.NewController(GinkgoT())
		bridge := NewMockICopilotBridge(controller)
		worktrees := NewMockIWorktreeManager(controller)
		terminals := NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil).AnyTimes()
		worktrees.EXPECT().Reuse(gomock.Any(), "repo", "/tmp/work").Return(copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{ID: "repo", Root: "/tmp/work", DefaultRef: "HEAD"}, Path: "/tmp/work", BaseRef: "HEAD",
		}, nil).Times(2)
		resumeErr := errors.New("temporary bridge transport failure")
		gomock.InOrder(
			bridge.EXPECT().ResumeSession(gomock.Any(), gomock.Any()).Return(copilotadapter.BridgeSession{}, resumeErr),
			bridge.EXPECT().ResumeSession(gomock.Any(), gomock.Any()).Return(copilotadapter.BridgeSession{Close: func(_ context.Context) error { return nil }}, nil),
		)
		postgresStore, err := store.NewPostgresStore(db)
		Expect(err).NotTo(HaveOccurred())
		service, err := copilotadapter.NewCopilotAdapter(
			copilotadapter.WithBridge(bridge), copilotadapter.WithStore(postgresStore),
			copilotadapter.WithWorktreeManager(worktrees), copilotadapter.WithTerminalManager(terminals),
			copilotadapter.WithScheduleStore(cron.NewMemoryStore()),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(service.Close)
		Expect(service.RestoreSessions(ctx)).To(MatchError(ContainSubstring(resumeErr.Error())))
		preserved, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(preserved.Status).To(Equal(string(api.SessionStatusIdle)))
		Expect(preserved.EndedAt.Valid).To(BeFalse())
		restored := make(chan error, 1)
		go func() { restored <- service.RestoreSessions(ctx) }()
		Eventually(restored).WithTimeout(3 * time.Second).Should(Receive(Succeed()))
	})

	It("quarantines an invalid legacy path without blocking valid restored sessions or the worktree list", func() {
		ctx := context.Background()
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		Expect(store.ApplyMigrations(ctx, db)).To(Succeed())
		queries := storedb.New(db)
		now := time.Now().UTC()
		validWorktreeID, invalidWorktreeID := uuid.New(), uuid.New()
		validSessionID, invalidSessionID := uuid.New(), uuid.New()
		for _, row := range []storedb.CreateWorktreeParams{
			{ID: validWorktreeID, RepositoryID: "repo", RepositoryRoot: "/tmp/valid", Path: "/tmp/valid", BaseRef: "HEAD", CreatedAt: now, UpdatedAt: now},
			{ID: invalidWorktreeID, RepositoryID: "legacy", RepositoryRoot: "/tmp/outside", Path: "/tmp/outside", BaseRef: "HEAD", CreatedAt: now, UpdatedAt: now},
		} {
			_, err := queries.CreateWorktree(ctx, row)
			Expect(err).NotTo(HaveOccurred())
		}
		for _, row := range []storedb.CreateSessionParams{
			{ID: validSessionID, WorktreeID: validWorktreeID, DisplayName: "valid", Model: "gpt-5", WorkingDirectory: "/tmp/valid", Status: "idle", CreatedAt: now, UpdatedAt: now},
			{ID: invalidSessionID, WorktreeID: invalidWorktreeID, DisplayName: "invalid", Model: "gpt-5", WorkingDirectory: "/tmp/outside", Status: "idle", CreatedAt: now, UpdatedAt: now},
		} {
			_, err := queries.CreateSession(ctx, row)
			Expect(err).NotTo(HaveOccurred())
			Expect(queries.EnsureSessionCounter(ctx, row.ID)).To(Succeed())
		}

		controller := gomock.NewController(GinkgoT())
		bridge := NewMockICopilotBridge(controller)
		worktrees := NewMockIWorktreeManager(controller)
		terminals := NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil).AnyTimes()
		repository := copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/valid", DefaultRef: "HEAD"}
		worktrees.EXPECT().Repositories().Return([]copilotadapter.Repository{repository}).AnyTimes()
		worktrees.EXPECT().Reuse(gomock.Any(), "repo", "/tmp/outside").Return(copilotadapter.PreparedWorktree{}, copilotadapter.ErrInvalidWorktree).Times(4)
		worktrees.EXPECT().Reuse(gomock.Any(), "repo", "/tmp/valid").Return(copilotadapter.PreparedWorktree{
			Repository: repository, Path: "/tmp/valid", BaseRef: "HEAD",
		}, nil).Times(2)
		worktrees.EXPECT().Inspect(gomock.Any(), "/tmp/valid").Return(copilotadapter.WorktreeSnapshot{
			Branch: "main", HeadSHA: "abc", Clean: true, State: "active",
		}, nil)
		bridge.EXPECT().ResumeSession(gomock.Any(), gomock.Any()).Return(copilotadapter.BridgeSession{Close: func(_ context.Context) error { return nil }}, nil)
		postgresStore, err := store.NewPostgresStore(db)
		Expect(err).NotTo(HaveOccurred())
		scheduleStore := &shutdownReconcileStore{
			MemoryStore:    cron.NewMemoryStore(),
			zeroReconciled: make(chan struct{}, 1),
		}
		_, err = queries.CreateChatSchedule(ctx, storedb.CreateChatScheduleParams{
			ID: uuid.New(), SessionID: invalidSessionID, DisplayName: "invalid worktree schedule",
			Prompt: "must stop", CronExpression: "0 0 1 1 *", Timezone: "UTC",
			Status: string(api.ChatScheduleStatusActive), CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		service, err := copilotadapter.NewCopilotAdapter(
			copilotadapter.WithBridge(bridge), copilotadapter.WithStore(postgresStore),
			copilotadapter.WithWorktreeManager(worktrees), copilotadapter.WithTerminalManager(terminals),
			copilotadapter.WithScheduleStore(scheduleStore),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(service.Close)
		scheduleContext, stopSchedules := context.WithCancel(ctx)
		schedulesFinished := make(chan error, 1)
		go func() { schedulesFinished <- service.RunSchedules(scheduleContext) }()
		DeferCleanup(func() {
			stopSchedules()
			Eventually(schedulesFinished).WithTimeout(lifecycleBudget.Within).Should(Receive(Succeed()))
		})
		Eventually(func() int {
			snapshot, snapshotErr := scheduleStore.Snapshot(ctx)
			if snapshotErr != nil {
				return 0
			}
			return len(snapshot.Jobs)
		}, 10*time.Second, 25*time.Millisecond).Should(Equal(1))
		restored := make(chan error, 1)
		go func() { restored <- service.RestoreSessions(ctx) }()
		Eventually(restored).WithTimeout(3 * time.Second).Should(Receive(Succeed()))
		Eventually(scheduleStore.zeroReconciled).WithTimeout(lifecycleBudget.Within).Should(Receive())
		Eventually(func() int {
			snapshot, snapshotErr := scheduleStore.Snapshot(ctx)
			if snapshotErr != nil {
				return -1
			}
			return len(snapshot.Jobs)
		}, 10*time.Second, 25*time.Millisecond).Should(Equal(0))
		invalid, err := queries.GetSession(ctx, invalidSessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(invalid.Status).To(Equal(string(api.SessionStatusFailed)))

		engine := httpserver.NewEngine("copilot-adapter-quarantine-test")
		Expect(service.Register(engine)).To(Succeed())
		server := httptest.NewServer(engine)
		DeferCleanup(server.Close)
		client, err := api.NewClientWithResponses(server.URL)
		Expect(err).NotTo(HaveOccurred())
		response, err := client.ListWorktreesWithResponse(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusOK), string(response.Body))
		Expect(response.JSON200.Data).To(HaveLen(2))
		Expect(response.JSON200.Data).To(ContainElement(HaveField("State", Equal(api.WorktreeStateMissing))))
		Expect(response.JSON200.Data).To(ContainElement(HaveField("State", Equal(api.WorktreeStateActive))))
	})

	It("leaves sessions untouched when worktree validation cannot complete", func() {
		ctx := context.Background()
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		Expect(store.ApplyMigrations(ctx, db)).To(Succeed())
		queries := storedb.New(db)
		now := time.Now().UTC()
		worktreeID, sessionID := uuid.New(), uuid.New()
		_, err := queries.CreateWorktree(ctx, storedb.CreateWorktreeParams{
			ID: worktreeID, RepositoryID: "repo", RepositoryRoot: "/tmp/work", Path: "/tmp/work",
			BaseRef: "HEAD", CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.CreateSession(ctx, storedb.CreateSessionParams{
			ID: sessionID, WorktreeID: worktreeID, DisplayName: "retry later", Model: "gpt-5",
			WorkingDirectory: "/tmp/work", Status: "idle", CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(queries.EnsureSessionCounter(ctx, sessionID)).To(Succeed())

		controller := gomock.NewController(GinkgoT())
		bridge := NewMockICopilotBridge(controller)
		worktrees := NewMockIWorktreeManager(controller)
		terminals := NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil).AnyTimes()
		worktrees.EXPECT().Reuse(gomock.Any(), "repo", "/tmp/work").Return(
			copilotadapter.PreparedWorktree{}, errors.New("git validation temporarily unavailable"),
		).Times(2)
		postgresStore, err := store.NewPostgresStore(db)
		Expect(err).NotTo(HaveOccurred())
		service, err := copilotadapter.NewCopilotAdapter(
			copilotadapter.WithBridge(bridge), copilotadapter.WithStore(postgresStore),
			copilotadapter.WithWorktreeManager(worktrees), copilotadapter.WithTerminalManager(terminals),
			copilotadapter.WithScheduleStore(cron.NewMemoryStore()),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(service.Close)
		Expect(service.RestoreSessions(ctx)).To(MatchError(ContainSubstring("temporarily unavailable")))

		preserved, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(preserved.Status).To(Equal(string(api.SessionStatusIdle)))
		Expect(preserved.EndedAt.Valid).To(BeFalse())

		engine := httpserver.NewEngine("copilot-adapter-validation-error-test")
		Expect(service.Register(engine)).To(Succeed())
		server := httptest.NewServer(engine)
		DeferCleanup(server.Close)
		client, err := api.NewClientWithResponses(server.URL)
		Expect(err).NotTo(HaveOccurred())
		response, err := client.ListWorktreesWithResponse(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusInternalServerError), string(response.Body))
		preserved, err = queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(preserved.Status).To(Equal(string(api.SessionStatusIdle)))
	})
})
