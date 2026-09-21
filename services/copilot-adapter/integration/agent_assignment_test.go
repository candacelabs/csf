package integration_test

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"os"

	"github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/pkg/pgmem"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/httpserver"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/store"
)

type agentAssignmentConsumer struct {
	adapter   *copilotadapter.CopilotAdapter
	bridge    *MockICopilotBridge
	worktrees *MockIWorktreeManager
	server    *httptest.Server
	client    *api.ClientWithResponses
}

func newAgentAssignmentConsumer(database *sql.DB) *agentAssignmentConsumer {
	GinkgoHelper()
	controller := gomock.NewController(GinkgoT())
	consumer := &agentAssignmentConsumer{bridge: NewMockICopilotBridge(controller), worktrees: NewMockIWorktreeManager(controller)}
	terminals := NewMockITerminalManager(controller)
	terminals.EXPECT().Close().Return(nil).AnyTimes()
	persistence, err := store.NewPostgresStore(database)
	Expect(err).NotTo(HaveOccurred())
	consumer.adapter, err = copilotadapter.NewCopilotAdapter(
		copilotadapter.WithBridge(consumer.bridge), copilotadapter.WithStore(persistence),
		copilotadapter.WithWorktreeManager(consumer.worktrees), copilotadapter.WithTerminalManager(terminals),
		copilotadapter.WithScheduleStore(cron.NewMemoryStore()),
	)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(consumer.adapter.Close)
	router := httpserver.NewEngine("agent-assignment-consumer")
	Expect(consumer.adapter.Register(router)).To(Succeed())
	consumer.server = httptest.NewServer(router)
	DeferCleanup(consumer.server.Close)
	consumer.client, err = api.NewClientWithResponses(consumer.server.URL, api.WithHTTPClient(consumer.server.Client()))
	Expect(err).NotTo(HaveOccurred())
	return consumer
}

func agentAssignmentRecipe() *pb.AgentAssignmentRecipe {
	GinkgoHelper()
	data, err := os.ReadFile("../../../examples/csf-agent/agent.json")
	Expect(err).NotTo(HaveOccurred())
	recipe := &pb.AgentAssignmentRecipe{}
	Expect(protojson.Unmarshal(data, recipe)).To(Succeed())
	return recipe
}

func expectAgentAssignment(consumer *agentAssignmentConsumer, recipe *pb.AgentAssignmentRecipe) <-chan copilotadapter.BridgePrompt {
	GinkgoHelper()
	sent := make(chan copilotadapter.BridgePrompt, 2)
	consumer.worktrees.EXPECT().Reuse(gomock.Any(), recipe.RepositoryId, "/tmp/agent-work").Return(copilotadapter.PreparedWorktree{
		Repository: copilotadapter.Repository{ID: recipe.RepositoryId, Root: "/tmp/agent-work", DefaultRef: "HEAD"},
		Path:       "/tmp/agent-work", BaseRef: "HEAD",
	}, nil).AnyTimes()
	consumer.worktrees.EXPECT().Prepare(gomock.Any(), gomock.Any()).Return(copilotadapter.PreparedWorktree{
		Repository: copilotadapter.Repository{ID: recipe.RepositoryId, Root: "/tmp/agent-work", DefaultRef: "HEAD"},
		Path:       "/tmp/agent-work", BaseRef: "HEAD",
	}, nil)
	consumer.bridge.EXPECT().CreateSession(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, spec copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
			Expect(spec.SystemInstructions).To(Equal(recipe.Agent.Instructions))
			Expect(spec.Model).To(Equal(recipe.Model))
			return copilotadapter.BridgeSession{
				Send: func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
					sent <- prompt
					return copilotadapter.BridgePromptDeliveryAccepted, nil
				},
				Close: func(_ context.Context) error { return nil },
			}, nil
		})
	return sent
}

func submitAgentExample(consumer *agentAssignmentConsumer, recipe *pb.AgentAssignmentRecipe) *pb.AgentAssignmentReceipt {
	GinkgoHelper()
	plan, err := csf.PrepareAgentAssignment(recipe)
	Expect(err).NotTo(HaveOccurred())
	receipt, err := csf.SubmitAgentAssignmentHTTP(context.Background(), consumer.client, consumer.server.URL, plan)
	Expect(err).NotTo(HaveOccurred())
	Expect(receipt.TurnId).NotTo(BeEmpty())
	return receipt
}

var _ = Describe("Agent recipe through real Workbench storage and generated transports", func() {
	var database *sql.DB
	var consumer *agentAssignmentConsumer
	var recipe *pb.AgentAssignmentRecipe
	BeforeEach(func() {
		memory := pgmem.MustNew()
		DeferCleanup(memory.Close)
		database = memory.Open()
		DeferCleanup(database.Close)
		Expect(store.ApplyMigrations(context.Background(), database)).To(Succeed())
		consumer = newAgentAssignmentConsumer(database)
		recipe = agentAssignmentRecipe()
	})

	It("creates a named session, records ticket provenance and replays without another dispatch", func() {
		sent := expectAgentAssignment(consumer, recipe)
		first := submitAgentExample(consumer, recipe)
		second := submitAgentExample(consumer, recipe)
		Expect(second.SessionId).To(Equal(first.SessionId))
		Expect(second.TurnId).To(Equal(first.TurnId))
		Expect(second.WorktreeId).To(Equal(first.WorktreeId))
		Expect(sent).To(HaveLen(1))
		Expect((<-sent).Text).To(ContainSubstring(recipe.TicketUrl))
		session, err := consumer.client.GetSessionWithResponse(context.Background(), uuid.MustParse(first.SessionId))
		Expect(err).NotTo(HaveOccurred())
		Expect(session.JSON200).NotTo(BeNil())
		Expect(session.JSON200.DisplayName).To(Equal(recipe.Agent.DisplayName))
	})

	It("retains the session receipt and rejects edited work under an existing assignment ID", func() {
		expectAgentAssignment(consumer, recipe)
		first := submitAgentExample(consumer, recipe)
		recipe.Task += " Also change the implementation."
		plan, err := csf.PrepareAgentAssignment(recipe)
		Expect(err).NotTo(HaveOccurred())
		partial, err := csf.SubmitAgentAssignmentHTTP(context.Background(), consumer.client, consumer.server.URL, plan)
		Expect(err).To(MatchError(ContainSubstring("HTTP 409")))
		Expect(partial.SessionId).To(Equal(first.SessionId))
		Expect(partial.TurnId).To(BeEmpty())
	})

	It("recovers the same session and turn links after the host is reconstructed", func() {
		expectAgentAssignment(consumer, recipe)
		first := submitAgentExample(consumer, recipe)
		consumer.server.Close()
		Expect(consumer.adapter.Close()).To(Succeed())
		consumer = newAgentAssignmentConsumer(database)
		expectAgentRestoration(consumer, recipe)
		Expect(consumer.adapter.RestoreSessions(context.Background())).To(Succeed())
		recovered := submitAgentExample(consumer, recipe)
		Expect(recovered.SessionId).To(Equal(first.SessionId))
		Expect(recovered.TurnId).To(Equal(first.TurnId))
		Expect(recovered.WorktreeId).To(Equal(first.WorktreeId))
	})
})

func expectAgentRestoration(consumer *agentAssignmentConsumer, recipe *pb.AgentAssignmentRecipe) {
	consumer.worktrees.EXPECT().Reuse(gomock.Any(), recipe.RepositoryId, "/tmp/agent-work").Return(copilotadapter.PreparedWorktree{
		Repository: copilotadapter.Repository{ID: recipe.RepositoryId, Root: "/tmp/agent-work", DefaultRef: "HEAD"},
		Path:       "/tmp/agent-work", BaseRef: "HEAD",
	}, nil).AnyTimes()
	consumer.bridge.EXPECT().ResumeSession(gomock.Any(), gomock.Any()).Return(copilotadapter.BridgeSession{
		Close: func(_ context.Context) error { return nil },
	}, nil)
}
