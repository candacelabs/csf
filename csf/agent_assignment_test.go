package csf_test

import (
	"os"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/csf"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

func exampleAgentRecipe() *pb.AgentAssignmentRecipe {
	GinkgoHelper()
	content, err := os.ReadFile("../examples/csf-agent/agent.json")
	Expect(err).NotTo(HaveOccurred())
	recipe := &pb.AgentAssignmentRecipe{}
	Expect(protojson.Unmarshal(content, recipe)).To(Succeed())
	return recipe
}

var _ = Describe("Agent assignment scaffold", func() {
	It("prepares the shipped example through MCP and the generated HTTP client", func() {
		consumer := newCSFConsumer()
		request := &pb.PrepareAgentAssignmentRequest{Recipe: exampleAgentRecipe()}
		result, err := consumer.agent.CallTool(consumer.context, &mcp.CallToolParams{Name: "PrepareAgentAssignment", Arguments: request})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeFalse())
		response, err := consumer.client.PrepareAgentAssignment(consumer.context, request)
		Expect(err).NotTo(HaveOccurred())
		requests, err := csf.NewAgentWorkbenchRequests(response.Plan)
		Expect(err).NotTo(HaveOccurred())
		session, err := requests.Session.AsNewWorktreeSessionRequest()
		Expect(err).NotTo(HaveOccurred())
		Expect(session.AgentId).To(HaveValue(Equal(request.Recipe.Agent.Id)))
		Expect(session.SystemInstructions).To(HaveValue(Equal(request.Recipe.Agent.Instructions)))
		Expect(session.WorktreeMode).To(Equal(api.NewWorktreeRequestMode))
		Expect(requests.Prompt.Text).To(ContainSubstring(request.Recipe.TicketUrl))
		Expect(requests.Prompt.Text).To(ContainSubstring(response.Plan.RecipeSha256))
	})

	It("retains retry identity across edits so existing Workbench receipts detect conflicts", func() {
		recipe := exampleAgentRecipe()
		first, err := csf.PrepareAgentAssignment(recipe)
		Expect(err).NotTo(HaveOccurred())
		recipe.Agent.Instructions += " Include a shutdown review."
		second, err := csf.PrepareAgentAssignment(recipe)
		Expect(err).NotTo(HaveOccurred())
		Expect(second.SessionKey).To(Equal(first.SessionKey))
		Expect(second.PromptKey).To(Equal(first.PromptKey))
		Expect(second.DefinitionSha256).NotTo(Equal(first.DefinitionSha256))
		Expect(second.RecipeSha256).NotTo(Equal(first.RecipeSha256))
		Expect(first.Recipe.Agent.Instructions).NotTo(Equal(recipe.Agent.Instructions))
	})

	It("gives a new assignment different session and turn retry identities", func() {
		recipe := exampleAgentRecipe()
		first, err := csf.PrepareAgentAssignment(recipe)
		Expect(err).NotTo(HaveOccurred())
		recipe.AssignmentId = uuid.NewString()
		second, err := csf.PrepareAgentAssignment(recipe)
		Expect(err).NotTo(HaveOccurred())
		Expect(second.SessionKey).NotTo(Equal(first.SessionKey))
		Expect(second.PromptKey).NotTo(Equal(first.PromptKey))
		Expect(second.DefinitionSha256).To(Equal(first.DefinitionSha256))
	})

	It("rejects a modified plan before producing executable requests", func() {
		plan, err := csf.PrepareAgentAssignment(exampleAgentRecipe())
		Expect(err).NotTo(HaveOccurred())
		plan.PromptKey = uuid.NewString()
		_, err = csf.NewAgentWorkbenchRequests(plan)
		Expect(err).To(MatchError(ContainSubstring("does not match its recipe")))
	})

	It("keeps constructed requests independent of later caller edits", func() {
		plan, err := csf.PrepareAgentAssignment(exampleAgentRecipe())
		Expect(err).NotTo(HaveOccurred())
		name := plan.Recipe.Agent.DisplayName
		requests, err := csf.NewAgentWorkbenchRequests(plan)
		Expect(err).NotTo(HaveOccurred())
		plan.Recipe.Agent.DisplayName = "different worker"
		Expect(requests.Prompt.Author).To(HaveValue(Equal(name)))
	})

	DescribeTable("rejects unusable recipes at the consumer boundary", func(change func(recipe *pb.AgentAssignmentRecipe)) {
		consumer := newCSFConsumer()
		recipe := proto.CloneOf(exampleAgentRecipe())
		change(recipe)
		_, err := consumer.client.PrepareAgentAssignment(consumer.context, &pb.PrepareAgentAssignmentRequest{Recipe: recipe})
		Expect(err).To(MatchError(ContainSubstring("HTTP 400")))
	},
		Entry("missing definition", func(recipe *pb.AgentAssignmentRecipe) { recipe.Agent = nil }),
		Entry("zero assignment identity", func(recipe *pb.AgentAssignmentRecipe) { recipe.AssignmentId = uuid.Nil.String() }),
		Entry("non-HTTP ticket", func(recipe *pb.AgentAssignmentRecipe) { recipe.TicketUrl = "file:///tmp/ticket" }),
		Entry("credential-bearing ticket", func(recipe *pb.AgentAssignmentRecipe) {
			recipe.TicketUrl = "https://user:password@example.invalid/ticket"
		}),
	)
})
