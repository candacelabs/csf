//go:build integration

package csf_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/candacelabs/csf/csf"
	mocks "github.com/candacelabs/csf/csf/internal/mocks"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("PostgreSQL knowledge and agent configuration", func() {
	It("retains a cited graph through a model-attributed service", func() {
		ctx := context.Background()
		fixture := buildCSFPostgresFixture(ctx)
		artifacts, err := csf.NewArtifacts(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(artifacts.Close)
		index := mocks.NewMockIKnowledgeIndex(gomock.NewController(GinkgoT()))
		service, err := csf.New(csf.WithKnowledge(fixture.store, index, artifacts))
		Expect(err).NotTo(HaveOccurred())
		text := "A retained source anchors each cited claim."
		digest := sha256.Sum256([]byte(text))
		ingested, err := service.IngestDocument(ctx, &pb.IngestDocumentRequest{Document: &pb.SourceDocument{
			SourceId: "knowledge-graph", Revision: "v1", SourceUri: "urn:csf:knowledge-graph",
			Title: "Knowledge graph fixture", MediaType: "text/plain", License: "test-fixture",
			ContentHash: hex.EncodeToString(digest[:]), RetrievedAt: "2026-09-20T00:00:00Z",
		}, Text: text})
		Expect(err).NotTo(HaveOccurred())
		start, end := uint64(0), uint64(len(text))
		root := &pb.KnowledgeNode{
			NodeId: "root-claim", Kind: pb.KnowledgeKind_KNOWLEDGE_KIND_CLAIM,
			SymbolKey: "knowledge.graph.root", Title: "Root claim", Statement: "A root claim is retained.",
			AuthorKind: pb.AuthorKind_AUTHOR_KIND_MODEL, AuthorRef: "fixture-model",
			CitationContentHash: ingested.Result.Document.ContentHash, CitationSourceId: ingested.Result.Document.SourceId,
			CitationSourceRevision: ingested.Result.Document.Revision, CitationChunkId: "chunk-0",
			CitationStartByte: &start, CitationEndByte: &end, TextProjectionRef: "text/fixture", VectorProjectionRef: "vector/fixture",
		}
		child := &pb.KnowledgeNode{
			NodeId: "child-claim", Kind: pb.KnowledgeKind_KNOWLEDGE_KIND_CLAIM,
			SymbolKey: "knowledge.graph.child", Title: "Child claim", Statement: "A child claim is retained.",
			ParentNodeId: root.NodeId, AuthorKind: pb.AuthorKind_AUTHOR_KIND_MODEL, AuthorRef: "fixture-model",
			CitationContentHash: ingested.Result.Document.ContentHash, CitationSourceId: ingested.Result.Document.SourceId,
			CitationSourceRevision: ingested.Result.Document.Revision, CitationChunkId: "chunk-0",
			CitationStartByte: &start, CitationEndByte: &end,
		}
		_, err = service.PutNode(ctx, &pb.PutNodeRequest{Node: &pb.KnowledgeNode{AuthorKind: pb.AuthorKind_AUTHOR_KIND_CHECKER}})
		Expect(errors.Is(err, csf.ErrInvalidRequest)).To(BeTrue())
		storedRoot, err := service.PutNode(ctx, &pb.PutNodeRequest{Node: root})
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(storedRoot.Node, root)).To(BeTrue())
		storedChild, err := service.PutNode(ctx, &pb.PutNodeRequest{Node: child})
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(storedChild.Node, child)).To(BeTrue())
		edge := &pb.KnowledgeEdge{
			FromNodeId: root.NodeId, ToNodeId: child.NodeId, Relation: pb.RelationKind_RELATION_KIND_SUPPORTS,
			AuthorKind: pb.AuthorKind_AUTHOR_KIND_MODEL, AuthorRef: "fixture-model", Rationale: "The source supports the child claim.",
		}
		storedEdge, err := service.PutEdge(ctx, &pb.PutEdgeRequest{Edge: edge})
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(storedEdge.Edge, edge)).To(BeTrue())
		_, err = service.PutEdge(ctx, &pb.PutEdgeRequest{Edge: edge})
		Expect(err).NotTo(HaveOccurred())
		full, err := service.GetGraph(ctx, &pb.GetGraphRequest{Request: &pb.GraphRequest{RootId: root.NodeId, Limit: 2}})
		Expect(err).NotTo(HaveOccurred())
		Expect(full.Snapshot.Truncated).To(BeFalse())
		Expect(full.Snapshot.Nodes).To(HaveLen(2))
		nodes := make(map[string]*pb.KnowledgeNode, len(full.Snapshot.Nodes))
		for _, node := range full.Snapshot.Nodes {
			nodes[node.NodeId] = node
		}
		Expect(proto.Equal(nodes[root.NodeId], root)).To(BeTrue())
		Expect(proto.Equal(nodes[child.NodeId], child)).To(BeTrue())
		Expect(full.Snapshot.Edges).To(HaveLen(1))
		Expect(proto.Equal(full.Snapshot.Edges[0], edge)).To(BeTrue())
		truncated, err := service.GetGraph(ctx, &pb.GetGraphRequest{Request: &pb.GraphRequest{RootId: root.NodeId, Limit: 1}})
		Expect(err).NotTo(HaveOccurred())
		Expect(truncated.Snapshot.Truncated).To(BeTrue())
		Expect(truncated.Snapshot.Nodes).To(HaveLen(1))
		Expect(truncated.Snapshot.Nodes[0].NodeId).To(Equal(root.NodeId))
	})

	It("retains an agent-owned external-tool configuration through compare-and-swap", func() {
		ctx := context.Background()
		fixture := buildCSFPostgresFixture(ctx)
		initial := buildValidAgentConfigFixture(configurationAgentID)
		created := callAgentConfigurationTool(fixture.store, `"name":"UpdateOwnAgentConfiguration","arguments":{"configuration":`+mustJSON(initial)+`}`)
		Expect(created.Code).To(Equal(http.StatusOK), created.Body.String())
		Expect(created.Body.String()).NotTo(ContainSubstring(`"isError":true`))
		stored, err := fixture.store.GetAgentConfiguration(ctx, configurationAgentID)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Revision).To(Equal(uint32(1)))
		Expect(proto.Equal(stored.Langfuse, initial.Langfuse)).To(BeTrue())
		Expect(proto.Equal(stored.Opensearch, initial.Opensearch)).To(BeTrue())
		_, err = time.Parse(time.RFC3339Nano, stored.UpdatedAt)
		Expect(err).NotTo(HaveOccurred())
		read := callAgentConfigurationTool(fixture.store, `"name":"GetOwnAgentConfiguration","arguments":{}`)
		Expect(read.Code).To(Equal(http.StatusOK), read.Body.String())
		Expect(read.Body.String()).To(ContainSubstring(`"agentId":"configuration-agent"`))
		updatedInput := buildValidAgentConfigFixture(configurationAgentID)
		updatedInput.Opensearch.Index = "agent-events-v2"
		updated := callAgentConfigurationTool(fixture.store, `"name":"UpdateOwnAgentConfiguration","arguments":{"expectedRevision":1,"configuration":`+mustJSON(updatedInput)+`}`)
		Expect(updated.Code).To(Equal(http.StatusOK), updated.Body.String())
		Expect(updated.Body.String()).NotTo(ContainSubstring(`"isError":true`))
		retained, err := fixture.store.GetAgentConfiguration(ctx, configurationAgentID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retained.Revision).To(Equal(uint32(2)))
		Expect(proto.Equal(retained.Langfuse, updatedInput.Langfuse)).To(BeTrue())
		Expect(proto.Equal(retained.Opensearch, updatedInput.Opensearch)).To(BeTrue())
		_, err = fixture.store.PutAgentConfiguration(ctx, configurationAgentID, 0, initial)
		Expect(errors.Is(err, csf.ErrConflict)).To(BeTrue())
		_, err = fixture.store.PutAgentConfiguration(ctx, configurationAgentID, 1, initial)
		Expect(errors.Is(err, csf.ErrConflict)).To(BeTrue())
		_, err = fixture.store.GetAgentConfiguration(ctx, "missing-agent")
		Expect(errors.Is(err, csf.ErrNotFound)).To(BeTrue())
	})
})
