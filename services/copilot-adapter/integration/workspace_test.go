package integration_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
	"github.com/candacelabs/csf/pkg/pgmem"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/kanban"
	"github.com/candacelabs/csf/services/copilot-adapter/store"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

const workspaceOrigin = "http://example.invalid"
const workspaceTaskURL = "https://github.com/example/project/issues/207"

type workspaceFixture struct {
	adapter   *copilotadapter.CopilotAdapter
	board     *kanban.Board
	router    *gin.Engine
	client    *api.ClientWithResponses
	sessionID uuid.UUID
}

func newWorkspaceFixture(options ...copilotadapter.Option) *workspaceFixture {
	GinkgoHelper()
	ctx := context.Background()
	database := pgmem.MustNew()
	DeferCleanup(database.Close)
	db := database.Open()
	DeferCleanup(db.Close)
	Expect(store.ApplyMigrations(ctx, db)).To(Succeed())
	persistence, err := store.NewPostgresStore(db)
	Expect(err).NotTo(HaveOccurred())
	controller := gomock.NewController(GinkgoT())
	worktrees := NewMockIWorktreeManager(controller)
	worktrees.EXPECT().Repositories().Return([]copilotadapter.Repository{}).AnyTimes()
	terminals := NewMockITerminalManager(controller)
	terminals.EXPECT().Close().Return(nil)
	options = append(options, copilotadapter.WithStore(persistence), copilotadapter.WithBridge(NewMockICopilotBridge(controller)),
		copilotadapter.WithWorktreeManager(worktrees), copilotadapter.WithTerminalManager(terminals), copilotadapter.WithScheduleStore(cron.NewMemoryStore()))
	adapter, err := copilotadapter.NewCopilotAdapter(options...)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(adapter.Close)
	board, err := kanban.NewBoard(adapter, []string{workspaceOrigin}, slog.New(slog.NewJSONHandler(GinkgoWriter, nil)))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		Expect(board.Close(ctx)).To(Succeed())
	})
	router := gin.New()
	Expect(adapter.Register(router)).To(Succeed())
	board.Register(router)
	server := httptest.NewServer(router)
	DeferCleanup(server.Close)
	client, err := api.NewClientWithResponses(server.URL)
	Expect(err).NotTo(HaveOccurred())
	return &workspaceFixture{adapter: adapter, board: board, router: router, client: client, sessionID: seedWorkspaceSession(persistence)}
}

func seedWorkspaceSession(persistence *store.PostgresStore) uuid.UUID {
	GinkgoHelper()
	now := time.Now().UTC()
	worktree, err := persistence.CreateWorktree(context.Background(), storedb.CreateWorktreeParams{
		ID: uuid.New(), RepositoryID: "workspace", RepositoryRoot: "/workspace", Path: "/workspace/tree", BaseRef: "HEAD", CreatedAt: now, UpdatedAt: now,
	})
	Expect(err).NotTo(HaveOccurred())
	session, err := persistence.CreateSession(context.Background(), storedb.CreateSessionParams{
		ID: uuid.New(), WorktreeID: worktree.ID, DisplayName: "Named worker", Model: "fixture-model", WorkingDirectory: worktree.Path,
		Status: string(api.SessionStatusIdle), CreatedAt: now, UpdatedAt: now,
	})
	Expect(err).NotTo(HaveOccurred())
	return session.ID
}

func workspaceBrowser(fixture *workspaceFixture) *livetest.Client {
	GinkgoHelper()
	return livetest.NewClient(GinkgoTB(), fixture.router, livetest.ClientOptions{Path: kanban.LivePath, Origin: workspaceOrigin})
}

var _ = Describe("shared workspace through generated HTTP and real widget sockets", func() {
	It("pushes a committed task association to two browsers without polling", func() {
		fixture := newWorkspaceFixture()
		first, second := workspaceBrowser(fixture), workspaceBrowser(fixture)
		linked, err := fixture.client.LinkSessionTaskWithResponse(context.Background(), fixture.sessionID, api.LinkSessionTaskJSONRequestBody{TaskUrl: workspaceTaskURL})
		Expect(err).NotTo(HaveOccurred())
		Expect(linked.StatusCode()).To(Equal(http.StatusOK))
		Expect(linked.JSON200.Generation).To(Equal(int64(1)))
		for _, browser := range []*livetest.Client{first, second} {
			frame := browser.Await("linked card", 10*time.Second, func(frame *livetest.Frame) bool {
				if frame.Patch == nil {
					return false
				}
				for _, update := range frame.Patch.Updates {
					if strings.Contains(update.HTML, workspaceTaskURL) {
						return true
					}
				}
				return false
			})
			Expect(frame.Patch.Updates).To(HaveLen(1))
			Expect(frame.Patch.Updates[0].FragmentID).To(ContainSubstring(fixture.sessionID.String()))
			browser.Ack(frame.Patch.ServerSeq)
		}
		read, err := fixture.client.GetWorkspaceWithResponse(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(read.JSON200.TaskLinks).To(HaveLen(1))
		Expect(read.JSON200.Sessions[0].Status).To(Equal(api.SessionStatusIdle))
	})

	It("rejects stale association writes and only signals successful commits", func() {
		fixture := newWorkspaceFixture()
		subscription := fixture.adapter.SubscribeWorkspace()
		DeferCleanup(subscription.Close)
		_, err := fixture.adapter.LinkWorkspaceTask(context.Background(), fixture.sessionID, workspaceTaskURL, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(subscription.Changed).To(Receive())
		_, err = fixture.adapter.LinkWorkspaceTask(context.Background(), fixture.sessionID, workspaceTaskURL, 0)
		Expect(err).To(HaveOccurred())
		Expect(subscription.Changed).NotTo(Receive())
		_, err = fixture.adapter.LinkWorkspaceTask(context.Background(), fixture.sessionID, "not-an-issue", 1)
		Expect(err).To(HaveOccurred())
		Expect(subscription.Changed).NotTo(Receive())
	})

	It("renders source strings as text and exposes the shared page on the host router", func() {
		fixture := newWorkspaceFixture()
		response := httptest.NewRecorder()
		fixture.router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, kanban.ViewPath, nil))
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(ContainSubstring("Named worker"))
		Expect(response.Body.String()).To(ContainSubstring("Needs task checkpoint"))
		Expect(response.Body.String()).To(ContainSubstring(fixture.sessionID.String()))
		Expect(response.Body.String()).NotTo(ContainSubstring("<script"))
	})
})
