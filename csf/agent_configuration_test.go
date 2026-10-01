package csf_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/candacelabs/csf/csf"
	mocks "github.com/candacelabs/csf/csf/internal/mocks"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
)

const (
	configurationAgentID   = "configuration-agent"
	configurationSessionID = "2d6d5a52-ae7a-4f8d-b89f-e58beaa4a735"
	configurationUpdatedAt = "2026-09-18T00:00:00Z"
)

func buildValidAgentConfigFixture(agentID string) *pb.AgentConfigurationInput {
	return &pb.AgentConfigurationInput{
		Langfuse: &pb.AgentLangfuseConfiguration{
			EndpointUrl:        "https://langfuse.example.invalid",
			PublicKeySecretRef: "agent/" + agentID + "/langfuse-public",
			SecretKeySecretRef: "agent/" + agentID + "/langfuse-secret",
		},
		Opensearch: &pb.AgentOpenSearchConfiguration{
			EndpointUrl:          "https://opensearch.example.invalid",
			Index:                "agent-events",
			EmbeddingModel:       "text-embedding",
			CredentialsSecretRef: "agent/" + agentID + "/opensearch",
		},
	}
}

func buildValidAgentConfigReceiptFixture(agentID string, revision uint32) *pb.AgentConfiguration {
	input := buildValidAgentConfigFixture(agentID)
	return &pb.AgentConfiguration{
		AgentId: agentID, Revision: revision,
		Langfuse: proto.CloneOf(input.Langfuse), Opensearch: proto.CloneOf(input.Opensearch),
		UpdatedAt: configurationUpdatedAt,
	}
}

var _ = Describe("agent-owned external-tool configuration", func() {
	It("binds an update to the bearer-authenticated transport identity", func() {
		store := mocks.NewMockIAgentConfigurationStore(gomock.NewController(GinkgoT()))
		input := buildValidAgentConfigFixture(configurationAgentID)
		retained := buildValidAgentConfigReceiptFixture(configurationAgentID, 1)
		store.EXPECT().PutAgentConfiguration(gomock.Any(), configurationAgentID, uint32(0), gomock.Any()).DoAndReturn(func(_ context.Context, agentID string, revision uint32, configuration *pb.AgentConfigurationInput) (*pb.AgentConfiguration, error) {
			Expect(agentID).To(Equal(configurationAgentID))
			Expect(revision).To(BeZero())
			Expect(proto.Equal(configuration, input)).To(BeTrue())
			Expect(configuration).NotTo(BeIdenticalTo(input))
			return retained, nil
		})
		response := callAgentConfigurationTool(store, `"name":"UpdateOwnAgentConfiguration","arguments":{"configuration":`+mustJSON(input)+`}`)
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(response.Body.String()).To(ContainSubstring(`"agentId":"configuration-agent"`))
		Expect(response.Body.String()).NotTo(ContainSubstring(`"isError":true`))
	})

	It("rejects a reference outside the caller's secret namespace before persistence", func() {
		store := mocks.NewMockIAgentConfigurationStore(gomock.NewController(GinkgoT()))
		input := buildValidAgentConfigFixture(configurationAgentID)
		input.Langfuse.PublicKeySecretRef = "agent/another-agent/langfuse-public"
		response := callAgentConfigurationTool(store, `"name":"UpdateOwnAgentConfiguration","arguments":{"configuration":`+mustJSON(input)+`}`)
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(response.Body.String()).To(ContainSubstring(`"isError":true`))
		Expect(response.Body.String()).To(ContainSubstring("secret reference must remain under"))
	})

	It("runs generated nested field checks before persistence", func() {
		store := mocks.NewMockIAgentConfigurationStore(gomock.NewController(GinkgoT()))
		input := buildValidAgentConfigFixture(configurationAgentID)
		input.Langfuse.EndpointUrl = strings.Repeat("x", 2049)
		response := callAgentConfigurationTool(store, `"name":"UpdateOwnAgentConfiguration","arguments":{"configuration":`+mustJSON(input)+`}`)
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(response.Body.String()).To(ContainSubstring(`"isError":true`))
		Expect(response.Body.String()).To(ContainSubstring("endpoint_url"))
	})

	It("preserves revision conflicts from the durable store", func() {
		store := mocks.NewMockIAgentConfigurationStore(gomock.NewController(GinkgoT()))
		store.EXPECT().PutAgentConfiguration(gomock.Any(), configurationAgentID, uint32(3), gomock.Any()).Return(nil, csf.ErrConflict)
		input := buildValidAgentConfigFixture(configurationAgentID)
		response := callAgentConfigurationTool(store, `"name":"UpdateOwnAgentConfiguration","arguments":{"expectedRevision":3,"configuration":`+mustJSON(input)+`}`)
		Expect(response.Body.String()).To(ContainSubstring(`"isError":true`))
		Expect(response.Body.String()).To(ContainSubstring("revision conflict"))
	})

	It("rejects a configuration receipt for another agent", func() {
		store := mocks.NewMockIAgentConfigurationStore(gomock.NewController(GinkgoT()))
		store.EXPECT().PutAgentConfiguration(gomock.Any(), configurationAgentID, uint32(0), gomock.Any()).Return(buildValidAgentConfigReceiptFixture("another-agent", 1), nil)
		input := buildValidAgentConfigFixture(configurationAgentID)
		response := callAgentConfigurationTool(store, `"name":"UpdateOwnAgentConfiguration","arguments":{"configuration":`+mustJSON(input)+`}`)
		Expect(response.Body.String()).To(ContainSubstring(`"isError":true`))
		Expect(response.Body.String()).To(ContainSubstring("another identity"))
	})

	It("rejects a retained timestamp that is not RFC3339", func() {
		store := mocks.NewMockIAgentConfigurationStore(gomock.NewController(GinkgoT()))
		retained := buildValidAgentConfigReceiptFixture(configurationAgentID, 1)
		retained.UpdatedAt = "not-a-timestamp"
		store.EXPECT().GetAgentConfiguration(gomock.Any(), configurationAgentID).Return(retained, nil)
		response := callAgentConfigurationTool(store, `"name":"GetOwnAgentConfiguration","arguments":{}`)
		Expect(response.Body.String()).To(ContainSubstring(`"isError":true`))
		Expect(response.Body.String()).To(ContainSubstring("invalid retained agent configuration timestamp"))
	})

	It("requires an authenticated identity on the public agent handler", func() {
		service, err := csf.New()
		Expect(err).NotTo(HaveOccurred())
		authenticator, err := csf.NewAgentMCPAuthenticator([]byte(agentMCPAuthenticationTestKey))
		Expect(err).NotTo(HaveOccurred())
		request := httptest.NewRequest(http.MethodPost, agentMCPAuthenticationTestPath, strings.NewReader(agentMCPAuthenticationToolsList))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", agentMCPAuthenticationTestAccept)
		response := httptest.NewRecorder()
		service.AgentMCPHandler(authenticator).ServeHTTP(response, request)
		Expect(response.Code).To(Equal(http.StatusUnauthorized))
	})
})

func callAgentConfigurationTool(store csf.IAgentConfigurationStore, params string) *httptest.ResponseRecorder {
	service, err := csf.New(csf.WithAgentConfigurations(store))
	Expect(err).NotTo(HaveOccurred())
	authenticator, err := csf.NewAgentMCPAuthenticator([]byte(agentMCPAuthenticationTestKey))
	Expect(err).NotTo(HaveOccurred())
	headers, err := authenticator.AgentMCPHeaders(configurationAgentID, configurationSessionID)
	Expect(err).NotTo(HaveOccurred())
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + params + `}}`
	request := httptest.NewRequest(http.MethodPost, agentMCPAuthenticationTestPath, strings.NewReader(body))
	request.Header = headers
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", agentMCPAuthenticationTestAccept)
	request.Header.Set("MCP-Protocol-Version", agentMCPAuthenticationTestVersion)
	response := httptest.NewRecorder()
	service.AgentMCPHandler(authenticator).ServeHTTP(response, request)
	return response
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	Expect(err).NotTo(HaveOccurred())
	return string(encoded)
}
