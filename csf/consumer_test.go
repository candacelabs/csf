package csf_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/candacelabs/csf/csf"
	mocks "github.com/candacelabs/csf/csf/internal/mocks"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/pkg/liquidproto"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"
)

var _ = Describe("generated transport consumers", func() {
	DescribeTable("distinguishes invalid input from operation failures over HTTP", func(operationError error, status int) {
		index := mocks.NewMockIKnowledgeIndex(gomock.NewController(GinkgoT()))
		index.EXPECT().Search(gomock.Any(), gomock.Any()).Return(nil, operationError)
		service := newCSFHTTPService(index)
		engine := httpserver.NewEngine("http-errors")
		service.Register(engine)
		body, err := protojson.Marshal(&pb.SearchRequest{Query: "evidence", Limit: 5})
		Expect(err).NotTo(HaveOccurred())
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/knowledge/search", bytes.NewReader(body)))
		Expect(response.Code).To(Equal(status))
	}, Entry("backend unavailable", errors.New("OpenSearch unavailable"), http.StatusInternalServerError),
		Entry("invalid operation input", fmt.Errorf("%w: invalid query", csf.ErrInvalidRequest), http.StatusBadRequest),
		Entry("generated field predicate", &liquidproto.Error{Field: "limit", Predicate: "this > 0"}, http.StatusBadRequest),
		Entry("unauthenticated agent", csf.ErrUnauthorized, http.StatusUnauthorized),
		Entry("missing configuration", csf.ErrNotFound, http.StatusNotFound),
		Entry("stale configuration", csf.ErrConflict, http.StatusConflict))

	It("rejects malformed HTTP input without invoking an operation", func() {
		service, err := csf.New()
		Expect(err).NotTo(HaveOccurred())
		engine := httpserver.NewEngine("http-decode")
		service.Register(engine)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/knowledge/search", strings.NewReader(`{"unknown":true}`)))
		Expect(response.Code).To(Equal(http.StatusBadRequest))
	})

	It("carries generated human meanings into the MCP tool listing", func() {
		operations := csf.HumanOperations()
		Expect(operations).NotTo(BeEmpty())
		consumer := newCSFConsumer()
		ctx, session := consumer.context, consumer.agent
		Expect(session.InitializeResult().ServerInfo.Name).To(Equal("csf"))
		listed, err := session.ListTools(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		meanings := make(map[string]string, len(operations))
		for _, operation := range operations {
			Expect(operation.Description).NotTo(BeEmpty(), operation.Name)
			meanings[operation.Name] = operation.Description
		}
		Expect(listed.Tools).To(HaveLen(len(meanings)))
		for _, tool := range listed.Tools {
			Expect(meanings).To(HaveKeyWithValue(tool.Name, tool.Title))
			delete(meanings, tool.Name)
		}
		Expect(meanings).To(BeEmpty())
	})

	It("uses the same strict typed operation from the CLI and HTTP consumer", func() {
		service, err := csf.New()
		Expect(err).NotTo(HaveOccurred())
		engine := httpserver.NewEngine("cli-consumer-test")
		service.Register(engine)
		server := httptest.NewServer(engine)
		DeferCleanup(server.Close)
		client, err := csf.NewClient(server.URL, server.Client())
		Expect(err).NotTo(HaveOccurred())
		result, err := client.CallOperation(context.Background(), "GetSnapshot", []byte("{}"))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeEmpty())
		_, err = client.CallOperation(context.Background(), "GetSnapshot", []byte(`{"unknown":true}`))
		Expect(err).To(HaveOccurred())
		_, err = client.CallOperation(context.Background(), "unknown", []byte("{}"))
		Expect(err).To(HaveOccurred())
	})
	It("preserves the typed HTTP contract through a consumer-owned interface", func() {
		service, err := csf.New()
		Expect(err).NotTo(HaveOccurred())
		engine := httpserver.NewEngine("test")
		service.Register(engine)
		server := httptest.NewServer(engine)
		DeferCleanup(server.Close)
		client, err := csf.NewClient(server.URL, server.Client())
		Expect(err).NotTo(HaveOccurred())
		var consumer interface {
			GetSnapshot(ctx context.Context, request *pb.GetSnapshotRequest) (*pb.GetSnapshotResponse, error)
		} = client
		result, err := consumer.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Snapshot.Issues).To(ContainElement("no event source configured"))
	})
	It("lists and executes real MCP tools and rejects unknown fields", func() {
		consumer := newCSFConsumer()
		ctx, session := consumer.context, consumer.agent
		var err error
		listed, err := session.ListTools(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		names := csf.CLIOperations()
		listedNames := make([]string, len(listed.Tools))
		for index, tool := range listed.Tools {
			listedNames[index] = tool.Name
		}
		Expect(listedNames).To(ConsistOf(names))
		Expect(csf.CLIOperations()).To(ConsistOf(names))
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "GetSnapshot", Arguments: map[string]string{}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeFalse())
		result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "GetSnapshot", Arguments: map[string]string{"unknown": "rejected"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeTrue())
	})
})

var _ = Describe("knowledge projection failures", func() {
	It("rejects mismatched content before retaining a blob or invoking storage", func() {
		controller := gomock.NewController(GinkgoT())
		store := mocks.NewMockIKnowledgeStore(controller)
		index := mocks.NewMockIKnowledgeIndex(controller)
		root := GinkgoT().TempDir()
		artifacts, err := csf.NewArtifacts(root)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(artifacts.Close)
		service, err := csf.New(csf.WithKnowledge(store, index, artifacts))
		Expect(err).NotTo(HaveOccurred())
		document := &pb.SourceDocument{SourceId: "test", Revision: "v1", ContentHash: strings.Repeat("0", 64), RetrievedAt: "2026-09-17T00:00:00Z"}
		_, err = service.IngestDocument(context.Background(), &pb.IngestDocumentRequest{Document: document, Text: "rejected source bytes"})
		Expect(err).To(MatchError(ContainSubstring("content hash mismatch")))
		Expect(errors.Is(err, csf.ErrInvalidRequest)).To(BeTrue())
		entries, err := os.ReadDir(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("retains evidence and queues work without synchronously calling OpenSearch", func() {
		controller := gomock.NewController(GinkgoT())
		store := mocks.NewMockIKnowledgeStore(controller)
		index := mocks.NewMockIKnowledgeIndex(controller)
		artifacts, err := csf.NewArtifacts(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(artifacts.Close)
		service, err := csf.New(csf.WithKnowledge(store, index, artifacts))
		Expect(err).NotTo(HaveOccurred())
		content := "a retained source observation"
		digest := sha256.Sum256([]byte(content))
		hash := hex.EncodeToString(digest[:])
		document := &pb.SourceDocument{SourceId: "test", Revision: "v1", ContentHash: hash, RetrievedAt: "2026-09-16T00:00:00Z"}
		store.EXPECT().PutDocument(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, value *pb.SourceDocument) (*pb.SourceDocument, error) { return value, nil })
		store.EXPECT().GetProjection(gomock.Any(), gomock.Any()).Return(&pb.ProjectionTask{State: pb.ProjectionState_PROJECTION_STATE_PENDING}, nil)
		result, err := service.IngestDocument(context.Background(), &pb.IngestDocumentRequest{Document: document, Text: content})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Result.Indexed).To(BeFalse())
		Expect(result.Result.ProjectionError).To(BeEmpty())
		Expect(result.Result.Queued).To(BeTrue())
		retained, err := artifacts.Get(hash)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(retained)).To(Equal(content))
		_, err = service.PutNode(context.Background(), &pb.PutNodeRequest{Node: &pb.KnowledgeNode{AuthorKind: pb.AuthorKind_AUTHOR_KIND_CHECKER}})
		Expect(err).To(HaveOccurred())
	})
})
