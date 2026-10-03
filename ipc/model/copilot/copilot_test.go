// Copyright 2026 Candace Labs

package copilot_test

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/ipc/model"
	"github.com/candacelabs/csf/ipc/model/copilot"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

func createdSession(sessionID uuid.UUID, worktreeID uuid.UUID) *api.CreateSessionResponse {
	return &api.CreateSessionResponse{
		HTTPResponse: &http.Response{StatusCode: http.StatusCreated},
		JSON201:      &api.Session{Id: &sessionID, WorktreeId: &worktreeID},
	}
}

func acceptedTurn(turnID uuid.UUID) *api.SubmitPromptResponse {
	return &api.SubmitPromptResponse{HTTPResponse: &http.Response{StatusCode: http.StatusAccepted}, JSON202: &api.Turn{Id: &turnID}}
}

var _ = Describe("The Copilot brain", func() {
	var client *MockIWorkbenchClient
	var brain *copilot.CopilotBrain
	var assignment *copilot.Assignment
	var sessionID, worktreeID, turnID uuid.UUID

	BeforeEach(func() {
		client = NewMockIWorkbenchClient(gomock.NewController(GinkgoT()))
		var err error
		brain, err = copilot.NewCopilotBrain(client)
		Expect(err).NotTo(HaveOccurred())
		assignment = &copilot.Assignment{Prompt: api.SubmitPromptJSONRequestBody{IdempotencyKey: uuid.New(), Text: "review the ticket"}}
		sessionID, worktreeID, turnID = uuid.New(), uuid.New(), uuid.New()
	})

	It("satisfies the brain contract", func() {
		var _ model.IBrain[*copilot.Assignment, copilot.Turn] = brain
	})

	It("proposes the accepted turn as its single action", func() {
		gomock.InOrder(
			client.EXPECT().CreateSessionWithResponse(gomock.Any(), assignment.Session).Return(createdSession(sessionID, worktreeID), nil),
			client.EXPECT().SubmitPromptWithResponse(gomock.Any(), sessionID, assignment.Prompt).Return(acceptedTurn(turnID), nil),
		)
		proposal, err := brain.Propose(context.Background(), assignment)
		Expect(err).NotTo(HaveOccurred())
		Expect(proposal.Provider).To(Equal(copilot.ProviderName))
		Expect(proposal.Actions).To(Equal([]copilot.Turn{{SessionID: sessionID, WorktreeID: worktreeID, TurnID: turnID}}))
	})

	It("proposes nothing when the session cannot be created", func() {
		client.EXPECT().CreateSessionWithResponse(gomock.Any(), gomock.Any()).Return(nil, errors.New("connection refused"))
		proposal, err := brain.Propose(context.Background(), assignment)
		Expect(proposal).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("create agent session; retry the original plan: connection refused")))
	})

	It("reports the adapter's status when it does not create a session", func() {
		client.EXPECT().CreateSessionWithResponse(gomock.Any(), gomock.Any()).Return(
			&api.CreateSessionResponse{HTTPResponse: &http.Response{StatusCode: http.StatusBadRequest}}, nil)
		_, err := brain.Propose(context.Background(), assignment)
		Expect(err).To(MatchError("create agent session: HTTP 400"))
	})

	It("keeps the session identities when prompt acceptance is unconfirmed", func() {
		client.EXPECT().CreateSessionWithResponse(gomock.Any(), gomock.Any()).Return(createdSession(sessionID, worktreeID), nil)
		client.EXPECT().SubmitPromptWithResponse(gomock.Any(), sessionID, gomock.Any()).Return(
			&api.SubmitPromptResponse{HTTPResponse: &http.Response{StatusCode: http.StatusConflict}}, nil)
		proposal, err := brain.Propose(context.Background(), assignment)
		Expect(proposal).To(BeNil())
		unconfirmed, ok := errors.AsType[*copilot.UnconfirmedTurnError](err)
		Expect(ok).To(BeTrue())
		Expect(unconfirmed.Turn).To(Equal(copilot.Turn{SessionID: sessionID, WorktreeID: worktreeID}))
		Expect(err).To(MatchError("submit agent prompt: HTTP 409"))
	})

	It("unwraps a transport failure behind an unconfirmed turn", func() {
		cause := errors.New("connection reset")
		client.EXPECT().CreateSessionWithResponse(gomock.Any(), gomock.Any()).Return(createdSession(sessionID, worktreeID), nil)
		client.EXPECT().SubmitPromptWithResponse(gomock.Any(), sessionID, gomock.Any()).Return(nil, cause)
		_, err := brain.Propose(context.Background(), assignment)
		Expect(err).To(MatchError(cause))
		Expect(err).To(BeAssignableToTypeOf(&copilot.UnconfirmedTurnError{}))
	})

	It("requires a client and an assignment", func() {
		_, err := copilot.NewCopilotBrain(nil)
		Expect(err).To(MatchError(ContainSubstring("Workbench client is required")))
		_, err = brain.Propose(context.Background(), nil)
		Expect(err).To(MatchError(ContainSubstring("assignment is required")))
	})
})
