package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/candacelabs/csf/csf"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestConsumer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "External CSF consumer")
}

var _ = Describe("CSF in a consumer-owned host", func() {
	const requestBudget = 15 * time.Second
	var (
		ctx       context.Context
		server    *httptest.Server
		client    *csf.Client
		agent     *mcp.ClientSession
		themePath string
	)

	BeforeEach(func() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), requestBudget)
		DeferCleanup(cancel)
		directory := GinkgoT().TempDir()
		themePath = filepath.Join(directory, csf.WorkbenchThemeFileName)
		Expect(os.WriteFile(themePath, []byte("body { color: #123456; }"), 0600)).To(Succeed())
		router, err := newConsumerRouter(directory, "")
		Expect(err).NotTo(HaveOccurred())
		server = httptest.NewServer(router)
		DeferCleanup(server.Close)
		client, err = csf.NewClient(server.URL, server.Client())
		Expect(err).NotTo(HaveOccurred())
		mcpClient := mcp.NewClient(&mcp.Implementation{Name: "external-consumer", Version: "1"}, nil)
		agent, err = mcpClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + mcpPath}, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(agent.Close)
	})

	It("discovers and calls real tools and the consumer's custom endpoint", func() {
		listed, err := agent.ListTools(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		names := make([]string, len(listed.Tools))
		for index, tool := range listed.Tools {
			names[index] = tool.Name
		}
		Expect(names).To(ContainElements("GetSnapshot", "GetWorkbenchTheme", "ReloadWorkbenchTheme"))
		snapshot, err := client.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.GetSnapshot().GetIssues()).To(ContainElement("no event source configured"))
		result, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "GetSnapshot", Arguments: map[string]string{}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeFalse())
		Expect(result.Content).To(HaveLen(1))
		text, ok := result.Content[0].(*mcp.TextContent)
		Expect(ok).To(BeTrue())
		decoded := &pb.GetSnapshotResponse{}
		Expect(protojson.Unmarshal([]byte(text.Text), decoded)).To(Succeed())
		Expect(decoded.GetSnapshot().GetIssues()).To(Equal(snapshot.GetSnapshot().GetIssues()))
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+summaryPath, nil)
		Expect(err).NotTo(HaveOccurred())
		response, err := server.Client().Do(request)
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(response.Body.Close()).To(Succeed()) }()
		body, err := io.ReadAll(response.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(string(body)).To(Equal("Events: 0\nTruncated: false\nIssues: no event source configured\n"))
	})

	It("reloads the fixed host file and rejects tool-supplied paths", func() {
		Expect(os.WriteFile(themePath, []byte("body { color: #654321; }"), 0600)).To(Succeed())
		result, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "ReloadWorkbenchTheme", Arguments: map[string]string{}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeFalse())
		theme, err := client.GetWorkbenchTheme(ctx, &pb.GetWorkbenchThemeRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(theme.GetTheme().GetCustomCss()).To(Equal("body { color: #654321; }"))
		Expect(theme.GetTheme().GetFilePath()).To(Equal(themePath))
		rejected, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "ReloadWorkbenchTheme", Arguments: map[string]string{"filePath": "/another/file.css"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(rejected.IsError).To(BeTrue())
		after, err := client.GetWorkbenchTheme(ctx, &pb.GetWorkbenchThemeRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(after.GetTheme().GetCustomCss()).To(Equal(theme.GetTheme().GetCustomCss()))
	})

	It("releases the caller-owned listener during cleanup", func() {
		server.Close()
		_, err := client.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
		Expect(err).To(HaveOccurred())
	})
})
