package csf_test

import (
	"context"

	mocks "github.com/candacelabs/csf/csf/internal/mocks"
	"net/http/httptest"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

// The same consumer-owned Gin host and real transports can exercise any CSF
// capability selected with options; no production interface is mocked here.
type csfConsumer struct {
	context context.Context
	client  *csf.Client
	agent   *mcp.ClientSession
}

func newCSFHTTPService(index csf.IKnowledgeIndex) *csf.Service {
	controller := gomock.NewController(GinkgoT())
	store := mocks.NewMockIKnowledgeStore(controller)
	artifacts, err := csf.NewArtifacts(GinkgoT().TempDir())
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(artifacts.Close)
	service, err := csf.New(csf.WithKnowledge(store, index, artifacts))
	Expect(err).NotTo(HaveOccurred())
	return service
}

func newCSFConsumer(options ...csf.Option) *csfConsumer {
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	service, err := csf.New(options...)
	Expect(err).NotTo(HaveOccurred())
	router := httpserver.NewEngine("csf-consumer")
	service.Register(router)
	router.Any("/mcp", gin.WrapH(service.MCPHandler()))
	server := httptest.NewServer(router)
	DeferCleanup(server.Close)
	client, err := csf.NewClient(server.URL, server.Client())
	Expect(err).NotTo(HaveOccurred())
	return &csfConsumer{context: ctx, client: client, agent: connectCSFConsumer(ctx, server.URL)}
}

func connectCSFConsumer(ctx context.Context, endpoint string) *mcp.ClientSession {
	agent := mcp.NewClient(&mcp.Implementation{Name: "csf-consumer", Version: "1"}, nil)
	session, err := agent.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint + "/mcp"}, nil)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(session.Close)
	return session
}
