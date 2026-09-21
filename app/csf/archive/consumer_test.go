//go:build archiveintegration

package archive_test

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/patience"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const archiveSessionTimeout = 60 * time.Second

var archiveReadyBudget = patience.Budget{Within: 20 * time.Second}
var archiveRoot = flag.String("csf-archive-root", "", "built extraction in a disposable network namespace")

func TestArchiveConsumer(test *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(test, "Extracted CSF archive MCP consumer")
}

var _ = Describe("agent setup from the shipped archive", func() {
	It("discovers and calls the shared host through its shipped MCP configuration", func() {
		root := *archiveRoot
		Expect(root).NotTo(BeEmpty(), "supply -csf-archive-root pointing to the built extraction in a disposable network namespace")
		content, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
		Expect(err).NotTo(HaveOccurred())
		// Copilot owns the connection configuration; no parallel transport DTO.
		var config map[string]map[string]copilot.MCPHTTPServerConfig
		Expect(json.Unmarshal(content, &config)).To(Succeed())
		endpoint := config["mcpServers"]["csf"]
		address, err := url.Parse(endpoint.URL)
		Expect(err).NotTo(HaveOccurred())
		Expect(address.Hostname()).To(Equal("127.0.0.1"))
		Expect(address.Path).To(Equal("/mcp"))
		Expect(endpoint.Tools).To(ContainElement("*"))

		ctx, cancel := context.WithTimeout(context.Background(), archiveSessionTimeout)
		DeferCleanup(cancel)
		// The documented default host and config must agree. This fixture runs
		// with --network none so it cannot attach to a consumer's live host.
		command := exec.CommandContext(ctx, filepath.Join(root, "out", "csf"), "serve")
		command.Dir = GinkgoT().TempDir()
		command.Stdout, command.Stderr = GinkgoWriter, GinkgoWriter
		command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
		command.WaitDelay = archiveReadyBudget.Within
		Expect(command.Start()).To(Succeed())
		DeferCleanup(func() {
			cancel()
			_ = command.Wait()
			Expect(command.ProcessState).NotTo(BeNil(), "the consumer owns and reaps its host")
			Expect(command.ProcessState.Exited()).To(BeTrue(), "the host stops before forced termination")
		})
		transport := &http.Client{Timeout: time.Second}
		api, err := csf.NewClient(address.Scheme+"://"+address.Host, transport)
		Expect(err).NotTo(HaveOccurred())
		patience.Await(GinkgoT(), "extracted HTTP host ready", archiveReadyBudget, func() error {
			_, err := api.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
			return err
		}, func(err error) bool { return err == nil })

		client := mcp.NewClient(&mcp.Implementation{Name: "archive-consumer", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint.URL}, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(session.Close)
		Expect(session.InitializeResult().ServerInfo).NotTo(BeNil())
		listed, err := session.ListTools(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		names := make([]string, len(listed.Tools))
		for index, tool := range listed.Tools {
			names[index] = tool.Name
			Expect(tool.Description).NotTo(BeEmpty(), tool.Name)
			Expect(tool.InputSchema).NotTo(BeNil(), tool.Name)
		}
		Expect(names).To(ConsistOf(csf.CLIOperations()))

		By("reading a fixture observation through the generated typed tool")
		observation := &pb.ResearchEvent{SchemaVersion: 1, RecordedAt: time.Now().UTC().Format(time.RFC3339),
			Payload: &pb.ResearchEvent_Status{Status: &pb.RunStatus{RunId: "archive-consumer", Phase: "inspection", Message: "consumer fixture"}}}
		event, err := protojson.Marshal(observation)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(command.Dir, "events.jsonl"), append(event, '\n'), 0600)).To(Succeed())
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "GetSnapshot", Arguments: &pb.GetSnapshotRequest{}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeFalse())
		encoded, err := json.Marshal(result.StructuredContent)
		Expect(err).NotTo(HaveOccurred())
		snapshot := &pb.GetSnapshotResponse{}
		Expect(protojson.Unmarshal(encoded, snapshot)).To(Succeed())
		Expect(snapshot.GetSnapshot().GetIssues()).To(BeEmpty())
		Expect(snapshot.GetSnapshot().GetEvents()).To(HaveLen(1))
		Expect(proto.Equal(snapshot.GetSnapshot().GetEvents()[0], observation)).To(BeTrue())

		By("rejecting schema drift and reporting absent optional dependencies")
		result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "GetSnapshot", Arguments: map[string]bool{"inventedField": true}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeTrue())
		result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "ListSimulations", Arguments: &pb.ListSimulationsRequest{}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeTrue(), "no simulator/database profile was supplied")
	})
})
