// Copyright 2026 Candace Labs

package csf_test

import (
	"context"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/ipc/model"
	"github.com/candacelabs/csf/ipc/model/copilot"
	"github.com/candacelabs/csf/ipc/model/stub"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

const stubWorkbenchEndpoint = "http://workbench.example.invalid/"

var _ = Describe("Agent assignment handed to a brain", func() {
	var plan *pb.AgentAssignmentPlan
	var turn copilot.Turn

	BeforeEach(func() {
		var err error
		plan, err = csf.PrepareAgentAssignment(exampleAgentRecipe())
		Expect(err).NotTo(HaveOccurred())
		turn = copilot.Turn{SessionID: uuid.New(), WorktreeID: uuid.New(), TurnID: uuid.New()}
	})

	It("links the stub brain's proposed turn to the plan with no model at all", func() {
		brain := stub.NewCannedBrain[*csf.AgentWorkbenchRequests](turn)
		receipt, err := csf.SubmitAgentAssignment(context.Background(), brain, stubWorkbenchEndpoint, plan)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(receipt.Plan, plan)).To(BeTrue(), "receipt plan %v differs from %v", receipt.Plan, plan)
		Expect(receipt.SessionId).To(Equal(turn.SessionID.String()))
		Expect(receipt.WorktreeId).To(Equal(turn.WorktreeID.String()))
		Expect(receipt.TurnId).To(Equal(turn.TurnID.String()))
		Expect(receipt.SessionUrl).To(Equal("http://workbench.example.invalid/ui/#/sessions/" + turn.SessionID.String()))
	})

	It("returns a partial receipt when the proposed turn was not accepted", func() {
		turn.TurnID = uuid.Nil
		brain := stub.NewCannedBrain[*csf.AgentWorkbenchRequests](turn)
		receipt, err := csf.SubmitAgentAssignment(context.Background(), brain, stubWorkbenchEndpoint, plan)
		Expect(err).To(MatchError(ContainSubstring(`brain "stub" proposed no accepted turn`)))
		Expect(receipt.SessionId).To(Equal(turn.SessionID.String()))
		Expect(receipt.TurnId).To(BeEmpty())
	})

	It("refuses a brain that proposes nothing or several turns", func() {
		_, err := csf.SubmitAgentAssignment(context.Background(), stub.NewCannedBrain[*csf.AgentWorkbenchRequests, copilot.Turn](), stubWorkbenchEndpoint, plan)
		Expect(err).To(MatchError(model.ErrNoProposal))
		_, err = csf.SubmitAgentAssignment(context.Background(), stub.NewCannedBrain[*csf.AgentWorkbenchRequests](turn, turn), stubWorkbenchEndpoint, plan)
		Expect(err).To(MatchError(ContainSubstring("proposed 2 actions where one was expected")))
	})

	It("validates the plan before asking any brain", func() {
		plan.RecipeSha256 = "edited"
		_, err := csf.SubmitAgentAssignment(context.Background(), stub.NewCannedBrain[*csf.AgentWorkbenchRequests](turn), stubWorkbenchEndpoint, plan)
		Expect(err).To(MatchError(csf.ErrInvalidRequest))
		_, err = csf.SubmitAgentAssignment(context.Background(), nil, stubWorkbenchEndpoint, plan)
		Expect(err).To(HaveOccurred())
	})
})
