package integration_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "copilot adapter integration suite")
}

func newWorktreeSessionBody(model string, repositoryID string) api.CreateSessionJSONRequestBody {
	return newWorktreeSessionBodyWithKey(model, repositoryID, uuid.New())
}

func newWorktreeSessionBodyWithKey(model string, repositoryID string, idempotencyKey uuid.UUID) api.CreateSessionJSONRequestBody {
	return newWorktreeSessionBodyWithKeyAndAgent(model, repositoryID, idempotencyKey, nil)
}

func newWorktreeSessionBodyWithKeyAndAgent(
	model string,
	repositoryID string,
	idempotencyKey uuid.UUID,
	agentID *string,
) api.CreateSessionJSONRequestBody {
	var body api.CreateSessionJSONRequestBody
	err := body.FromNewWorktreeSessionRequest(api.NewWorktreeSessionRequest{
		IdempotencyKey: idempotencyKey, Model: model, AgentId: agentID, RepositoryId: repositoryID,
	})
	if err != nil {
		panic(err)
	}
	return body
}

func currentWorktreeSessionBody(model string, repositoryID string) api.CreateSessionJSONRequestBody {
	var body api.CreateSessionJSONRequestBody
	err := body.FromCurrentWorktreeSessionRequest(api.CurrentWorktreeSessionRequest{
		IdempotencyKey: uuid.New(), Model: model, RepositoryId: repositoryID,
	})
	if err != nil {
		panic(err)
	}
	return body
}

func existingWorktreeSessionBody(model string, repositoryID string, worktreeID uuid.UUID) api.CreateSessionJSONRequestBody {
	var body api.CreateSessionJSONRequestBody
	err := body.FromExistingWorktreeSessionRequest(api.ExistingWorktreeSessionRequest{
		IdempotencyKey: uuid.New(), Model: model, RepositoryId: repositoryID, WorktreeId: worktreeID,
	})
	if err != nil {
		panic(err)
	}
	return body
}

func resolutionBody(decision api.ResolveDecision) api.ResolveSessionRequestJSONRequestBody {
	return api.ResolveSessionRequestJSONRequestBody{Decision: decision}
}

func bytesReader(body string) *strings.Reader { return strings.NewReader(body) }
