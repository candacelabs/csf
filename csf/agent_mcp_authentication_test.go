package csf_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/candacelabs/csf/csf"
	mocks "github.com/candacelabs/csf/csf/internal/mocks"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

const (
	agentMCPAuthenticationTestKey       = "agent-mcp-authentication-test-key"
	agentMCPAuthenticationWrongKey      = "agent-mcp-authentication-test-kex"
	agentMCPAuthenticationBearerPrefix  = "Bearer "
	agentMCPAuthenticationTestAgentID   = "agent-mcp-test"
	agentMCPAuthenticationTestSessionID = "2d6d5a52-ae7a-4f8d-b89f-e58beaa4a735"
	agentMCPAuthenticationTestPath      = "/mcp"
	agentMCPAuthenticationTestVersion   = "2025-11-25"
	agentMCPAuthenticationTestAccept    = "application/json, text/event-stream"
	agentMCPAuthenticationToolsList     = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	agentMCPAuthenticationToolCall      = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"GetOwnAgentConfiguration","arguments":{}}}`
	agentMCPAuthenticationUpdatedAt     = "2026-09-18T00:00:00Z"
)

type agentMCPFixture struct {
	authenticator *csf.AgentMCPAuthenticator
	handler       http.Handler
}

var _ = Describe("agent MCP authentication", func() {
	It("requires one nonempty signing key", func() {
		_, err := csf.NewAgentMCPAuthenticator(nil)

		Expect(err).To(HaveOccurred())
	})

	It("serves a bearer-authenticated configuration tool call", func() {
		store := mocks.NewMockIAgentConfigurationStore(gomock.NewController(GinkgoT()))
		store.EXPECT().GetAgentConfiguration(gomock.Any(), agentMCPAuthenticationTestAgentID).Return(&pb.AgentConfiguration{
			AgentId:    agentMCPAuthenticationTestAgentID,
			Revision:   1,
			Langfuse:   &pb.AgentLangfuseConfiguration{},
			Opensearch: &pb.AgentOpenSearchConfiguration{},
			UpdatedAt:  agentMCPAuthenticationUpdatedAt,
		}, nil)
		service, err := csf.New(csf.WithAgentConfigurations(store))
		Expect(err).NotTo(HaveOccurred())
		fixture := buildAgentMCPFixture(service)
		response := httptest.NewRecorder()

		fixture.handler.ServeHTTP(response, buildAgentMCPRequest(fixture, agentMCPAuthenticationToolCall))

		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(response.Body.String()).To(ContainSubstring(`"agentId":"agent-mcp-test"`))
		Expect(response.Body.String()).NotTo(ContainSubstring(`"isError":true`))
	})

	DescribeTable("rejects a request without a valid session credential", func(mutate func(request *http.Request)) {
		service, err := csf.New()
		Expect(err).NotTo(HaveOccurred())
		fixture := buildAgentMCPFixture(service)
		request := buildAgentMCPRequest(fixture, agentMCPAuthenticationToolsList)
		mutate(request)
		response := httptest.NewRecorder()

		fixture.handler.ServeHTTP(response, request)

		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		Expect(response.Body.String()).NotTo(ContainSubstring(agentMCPAuthenticationTestKey))
		Expect(response.Body.String()).NotTo(ContainSubstring(agentMCPAuthenticationWrongKey))
	},
		Entry("absent key", func(request *http.Request) { request.Header.Del(csf.AgentMCPAuthorizationHeader) }),
		Entry("wrong key", func(request *http.Request) {
			request.Header.Set(csf.AgentMCPAuthorizationHeader, agentMCPAuthenticationBearerPrefix+agentMCPAuthenticationWrongKey)
		}),
		Entry("raw signing key", func(request *http.Request) {
			request.Header.Set(csf.AgentMCPAuthorizationHeader, agentMCPAuthenticationBearerPrefix+agentMCPAuthenticationTestKey)
		}),
		Entry("credential signed by another key", func(request *http.Request) {
			authenticator, err := csf.NewAgentMCPAuthenticator([]byte(agentMCPAuthenticationWrongKey))
			Expect(err).NotTo(HaveOccurred())
			headers, err := authenticator.AgentMCPHeaders(agentMCPAuthenticationTestAgentID, agentMCPAuthenticationTestSessionID)
			Expect(err).NotTo(HaveOccurred())
			request.Header.Set(csf.AgentMCPAuthorizationHeader, headers.Get(csf.AgentMCPAuthorizationHeader))
		}),
		Entry("duplicate credential", func(request *http.Request) {
			request.Header.Add(csf.AgentMCPAuthorizationHeader, request.Header.Get(csf.AgentMCPAuthorizationHeader))
		}),
	)

	DescribeTable("rejects a bearer-authenticated request with an invalid identity", func(mutate func(request *http.Request)) {
		store := mocks.NewMockIAgentConfigurationStore(gomock.NewController(GinkgoT()))
		service, err := csf.New(csf.WithAgentConfigurations(store))
		Expect(err).NotTo(HaveOccurred())
		fixture := buildAgentMCPFixture(service)
		request := buildAgentMCPRequest(fixture, agentMCPAuthenticationToolCall)
		mutate(request)
		response := httptest.NewRecorder()

		fixture.handler.ServeHTTP(response, request)

		Expect(response.Code).To(Equal(http.StatusUnauthorized))
	},
		Entry("invalid agent ID", func(request *http.Request) { request.Header.Set(csf.AgentMCPAgentIDHeader, "agent.mcp.test") }),
		Entry("invalid session UUID", func(request *http.Request) { request.Header.Set(csf.AgentMCPSessionIDHeader, "not-a-uuid") }),
		Entry("another valid agent", func(request *http.Request) { request.Header.Set(csf.AgentMCPAgentIDHeader, "another-agent") }),
		Entry("another valid session", func(request *http.Request) {
			request.Header.Set(csf.AgentMCPSessionIDHeader, "312ce098-3743-4eae-b8e2-33c9026933c4")
		}),
		Entry("duplicate agent ID", func(request *http.Request) {
			request.Header.Add(csf.AgentMCPAgentIDHeader, agentMCPAuthenticationTestAgentID)
		}),
		Entry("duplicate session ID", func(request *http.Request) {
			request.Header.Add(csf.AgentMCPSessionIDHeader, agentMCPAuthenticationTestSessionID)
		}),
	)

	It("rejects an impersonated configuration update before persistence", func() {
		store := mocks.NewMockIAgentConfigurationStore(gomock.NewController(GinkgoT()))
		service, err := csf.New(csf.WithAgentConfigurations(store))
		Expect(err).NotTo(HaveOccurred())
		fixture := buildAgentMCPFixture(service)
		input := buildValidAgentConfigFixture(configurationAgentID)
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"UpdateOwnAgentConfiguration","arguments":{"configuration":` + mustJSON(input) + `}}}`
		request := buildAgentMCPRequest(fixture, body)
		request.Header.Set(csf.AgentMCPAgentIDHeader, configurationAgentID)
		response := httptest.NewRecorder()

		fixture.handler.ServeHTTP(response, request)

		Expect(response.Code).To(Equal(http.StatusUnauthorized))
	})

	It("issues distinct session credentials without disclosing the signing key", func() {
		authenticator, err := csf.NewAgentMCPAuthenticator([]byte(agentMCPAuthenticationTestKey))
		Expect(err).NotTo(HaveOccurred())
		first, err := authenticator.AgentMCPHeaders(agentMCPAuthenticationTestAgentID, agentMCPAuthenticationTestSessionID)
		Expect(err).NotTo(HaveOccurred())
		otherAgent, err := authenticator.AgentMCPHeaders("another-agent", agentMCPAuthenticationTestSessionID)
		Expect(err).NotTo(HaveOccurred())
		otherSession, err := authenticator.AgentMCPHeaders(agentMCPAuthenticationTestAgentID, "312ce098-3743-4eae-b8e2-33c9026933c4")
		Expect(err).NotTo(HaveOccurred())

		credential := first.Get(csf.AgentMCPAuthorizationHeader)
		Expect(credential).NotTo(ContainSubstring(agentMCPAuthenticationTestKey))
		Expect(credential).NotTo(Equal(otherAgent.Get(csf.AgentMCPAuthorizationHeader)))
		Expect(credential).NotTo(Equal(otherSession.Get(csf.AgentMCPAuthorizationHeader)))
	})

	It("keeps the legacy MCP handler unauthenticated", func() {
		service, err := csf.New()
		Expect(err).NotTo(HaveOccurred())
		response := httptest.NewRecorder()

		service.MCPHandler().ServeHTTP(response, buildLegacyMCPRequest(agentMCPAuthenticationToolsList))

		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(response.Body.String()).To(ContainSubstring(`"name":"Search"`))
	})
})

func buildAgentMCPFixture(service *csf.Service) *agentMCPFixture {
	authenticator, err := csf.NewAgentMCPAuthenticator([]byte(agentMCPAuthenticationTestKey))
	Expect(err).NotTo(HaveOccurred())
	return &agentMCPFixture{authenticator: authenticator, handler: service.AgentMCPHandler(authenticator)}
}

func buildAgentMCPRequest(fixture *agentMCPFixture, body string) *http.Request {
	headers, err := fixture.authenticator.AgentMCPHeaders(agentMCPAuthenticationTestAgentID, agentMCPAuthenticationTestSessionID)
	Expect(err).NotTo(HaveOccurred())
	request := httptest.NewRequest(http.MethodPost, agentMCPAuthenticationTestPath, strings.NewReader(body))
	request.Header = headers
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", agentMCPAuthenticationTestAccept)
	request.Header.Set("MCP-Protocol-Version", agentMCPAuthenticationTestVersion)
	return request
}

func buildLegacyMCPRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, agentMCPAuthenticationTestPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", agentMCPAuthenticationTestAccept)
	request.Header.Set("MCP-Protocol-Version", agentMCPAuthenticationTestVersion)
	return request
}
