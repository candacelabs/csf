package csf_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/candacelabs/csf/csf"
	mocks "github.com/candacelabs/csf/csf/internal/mocks"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/services/copilot-adapter/copilotbridge"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = Describe("CSF onboarding", func() {
	var store *mocks.MockIKnowledgeStore
	var index *mocks.MockIKnowledgeIndex
	var artifacts *csf.Artifacts
	var consumerRoot string
	BeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())
		store = mocks.NewMockIKnowledgeStore(ctrl)
		index = mocks.NewMockIKnowledgeIndex(ctrl)
		var err error
		artifacts, err = csf.NewArtifacts(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(artifacts.Close)
		consumerRoot = GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(consumerRoot, "README.md"), []byte("consumer integration"), 0600)).To(Succeed())
		store.EXPECT().PutDocument(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, document *pb.SourceDocument) (*pb.SourceDocument, error) { return document, nil }).AnyTimes()
	})
	It("queues embedded guidance on a first empty call without waiting for workers", func() {
		store.EXPECT().GetProjection(gomock.Any(), gomock.Any()).Return(&pb.ProjectionTask{State: pb.ProjectionState_PROJECTION_STATE_PENDING}, nil).AnyTimes()
		service, err := csf.New(csf.WithKnowledge(store, index, artifacts))
		Expect(err).NotTo(HaveOccurred())
		response, err := service.LearnAboutCSF(context.Background(), &pb.LearnAboutCSFRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.KnowledgeAvailable).To(BeTrue())
		Expect(response.KnowledgeError).To(BeEmpty())
		Expect(response.Ingested).NotTo(BeEmpty())
		Expect(response.Retrieval).To(BeNil())
		for _, receipt := range response.Ingested {
			Expect(receipt.Queued).To(BeTrue())
			Expect(receipt.Indexed).To(BeFalse())
			Expect(receipt.Document.SourceId).To(HavePrefix("csf/onboarding/"))
			Expect(receipt.Document.Revision).To(Equal(receipt.Document.ContentHash))
			content, err := artifacts.Get(receipt.Document.ContentHash)
			Expect(err).NotTo(HaveOccurred())
			Expect(content).NotTo(BeEmpty())
		}
	})
	It("keeps excluded files, aliases, traversal and duplicate paths out of ingestion", func() {
		Expect(os.WriteFile(filepath.Join(consumerRoot, ".env"), []byte("secret"), 0600)).To(Succeed())
		Expect(os.Mkdir(filepath.Join(consumerRoot, "secrets"), 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(consumerRoot, "secrets", "private.txt"), []byte("secret"), 0600)).To(Succeed())
		Expect(os.Symlink(".env", filepath.Join(consumerRoot, "alias.txt"))).To(Succeed())
		Expect(os.Symlink("secrets", filepath.Join(consumerRoot, "alias-dir"))).To(Succeed())
		store.EXPECT().GetProjection(gomock.Any(), gomock.Any()).Return(&pb.ProjectionTask{State: pb.ProjectionState_PROJECTION_STATE_PENDING}, nil).AnyTimes()
		service, err := csf.New(csf.WithKnowledge(store, index, artifacts), csf.WithOnboarding(csf.OnboardingConfig{ConsumerRoot: consumerRoot}))
		Expect(err).NotTo(HaveOccurred())
		skipped := []string{".env", "secrets/private.txt", "alias.txt", "alias-dir/private.txt", "../README.md", "README.md", "history.sqlite3"}
		response, err := service.LearnAboutCSF(context.Background(), &pb.LearnAboutCSFRequest{ConsumerPaths: append([]string{"README.md"}, skipped...)})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.SkippedPaths).To(ConsistOf(skipped))
		var consumerDocuments []*pb.SourceDocument
		for _, receipt := range response.Ingested {
			if receipt.Document.License == "NOASSERTION" {
				consumerDocuments = append(consumerDocuments, receipt.Document)
			}
		}
		Expect(consumerDocuments).To(HaveLen(1))
		Expect(consumerDocuments[0].SourceId).To(HavePrefix("consumer/onboarding/"))
		Expect(consumerDocuments[0].SourceUri).To(HavePrefix("file:///"))
	})
	It("only returns retrieval evidence for the ingested revision", func() {
		store.EXPECT().GetProjection(gomock.Any(), gomock.Any()).Return(&pb.ProjectionTask{State: pb.ProjectionState_PROJECTION_STATE_SUCCEEDED}, nil).AnyTimes()
		index.EXPECT().Search(gomock.Any(), gomock.Any()).Return(&pb.SearchResult{Mode: "lexical", Hits: []*pb.SearchHit{
			{Document: &pb.SourceDocument{SourceId: "csf/onboarding/README.md", Revision: "reviewed"}},
			{Document: &pb.SourceDocument{SourceId: "csf/onboarding/README.md", Revision: "stale"}},
			{Document: &pb.SourceDocument{SourceId: "other/source", Revision: "reviewed"}},
		}}, nil)
		service, err := csf.New(csf.WithKnowledge(store, index, artifacts), csf.WithOnboarding(csf.OnboardingConfig{CSFRevision: "reviewed"}))
		Expect(err).NotTo(HaveOccurred())
		response, err := service.LearnAboutCSF(context.Background(), &pb.LearnAboutCSFRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Retrieval.GetHits()).To(HaveLen(1))
		Expect(response.Retrieval.Hits[0].Document.Revision).To(Equal("reviewed"))
	})
	It("requires an authorized checkout when consumer paths are selected", func() {
		service, err := csf.New(csf.WithKnowledge(store, index, artifacts))
		Expect(err).NotTo(HaveOccurred())
		_, err = service.LearnAboutCSF(context.Background(), &pb.LearnAboutCSFRequest{ConsumerPaths: []string{"README.md"}})
		Expect(err).To(MatchError(ContainSubstring("authorized consumer root")))
	})
	It("queues explicitly selected typed Copilot history with native provenance", func() {
		store.EXPECT().GetProjection(gomock.Any(), gomock.Any()).Return(&pb.ProjectionTask{State: pb.ProjectionState_PROJECTION_STATE_PENDING}, nil).AnyTimes()
		selected := "synthetic-session"
		metadata := copilot.SessionMetadata{SessionID: selected, ModifiedTime: time.Unix(42, 0)}
		events := []copilot.SessionEvent{
			{ID: "user-1", Data: &rpc.UserMessageData{Content: "synthetic request"}},
			{ID: "assistant-1", Data: &rpc.AssistantMessageData{Content: "synthetic answer"}},
		}
		service, err := csf.New(
			csf.WithKnowledge(store, index, artifacts),
			csf.WithOnboarding(csf.OnboardingConfig{CopilotHistoryReader: func(_ context.Context, sessionID string) (copilotbridge.HistorySnapshot, error) {
				Expect(sessionID).To(Equal(selected))
				return copilotbridge.HistorySnapshot{
					Metadata: metadata,
					Events:   events,
				}, nil
			}}),
		)
		Expect(err).NotTo(HaveOccurred())

		response, err := service.LearnAboutCSF(context.Background(), &pb.LearnAboutCSFRequest{CopilotSessionIds: []string{selected}})

		Expect(err).NotTo(HaveOccurred())
		Expect(response.CopilotHistory).To(HaveLen(1))
		result := response.CopilotHistory[0]
		Expect(result.Error).To(BeEmpty())
		Expect(result.EventCount).To(Equal(uint64(2)))
		Expect(result.Ingest.Queued).To(BeTrue())
		Expect(result.Ingest.Indexed).To(BeFalse())
		Expect(result.Ingest.Document.SourceUri).To(Equal("copilot://session/" + selected))
		Expect(result.Ingest.Document.MediaType).To(Equal("application/json"))
		Expect(result.Ingest.Document.Revision).To(Equal(result.Ingest.Document.ContentHash))
		content, err := artifacts.Get(result.Ingest.Document.ContentHash)
		Expect(err).NotTo(HaveOccurred())
		var retained copilotbridge.HistorySnapshot
		Expect(json.Unmarshal(content, &retained)).To(Succeed())
		Expect(retained.Metadata.SessionID).To(Equal(metadata.SessionID))
		Expect(retained.Metadata.ModifiedTime).To(BeTemporally("==", metadata.ModifiedTime))
		Expect(retained.Events).To(HaveLen(len(events)))
		Expect(retained.Events[0].ID).To(Equal(events[0].ID))
		Expect(retained.Events[0].Type()).To(Equal(events[0].Type()))
		Expect(retained.Events[0].Data.(*rpc.UserMessageData).Content).To(Equal(events[0].Data.(*rpc.UserMessageData).Content))
		Expect(retained.Events[1].ID).To(Equal(events[1].ID))
		Expect(retained.Events[1].Type()).To(Equal(events[1].Type()))
		Expect(retained.Events[1].Data.(*rpc.AssistantMessageData).Content).To(Equal(events[1].Data.(*rpc.AssistantMessageData).Content))
		digest := sha256.Sum256(content)
		Expect(result.Ingest.Document.Revision).To(Equal(hex.EncodeToString(digest[:])))
	})
	It("rejects a history snapshot whose metadata does not match the selected ID", func() {
		store.EXPECT().GetProjection(gomock.Any(), gomock.Any()).Return(&pb.ProjectionTask{State: pb.ProjectionState_PROJECTION_STATE_PENDING}, nil).AnyTimes()
		service, err := csf.New(
			csf.WithKnowledge(store, index, artifacts),
			csf.WithOnboarding(csf.OnboardingConfig{CopilotHistoryReader: func(_ context.Context, _ string) (copilotbridge.HistorySnapshot, error) {
				return copilotbridge.HistorySnapshot{Metadata: copilot.SessionMetadata{SessionID: "different-session"}}, nil
			}}),
		)
		Expect(err).NotTo(HaveOccurred())

		response, err := service.LearnAboutCSF(context.Background(), &pb.LearnAboutCSFRequest{CopilotSessionIds: []string{"selected-session"}})

		Expect(err).NotTo(HaveOccurred())
		Expect(response.CopilotHistory).To(HaveLen(1))
		Expect(response.CopilotHistory[0].Error).To(ContainSubstring("returned session"))
		Expect(response.CopilotHistory[0].Ingest).To(BeNil())
		Expect(response.SkippedPaths).To(ContainElement("copilot://session/selected-session"))
	})
	It("keeps Copilot history opt-in and bounded", func() {
		store.EXPECT().GetProjection(gomock.Any(), gomock.Any()).Return(&pb.ProjectionTask{State: pb.ProjectionState_PROJECTION_STATE_PENDING}, nil).AnyTimes()
		service, err := csf.New(csf.WithKnowledge(store, index, artifacts))
		Expect(err).NotTo(HaveOccurred())
		response, err := service.LearnAboutCSF(context.Background(), &pb.LearnAboutCSFRequest{CopilotSessionIds: []string{"unconfigured"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.CopilotHistory).To(HaveLen(1))
		Expect(response.CopilotHistory[0].Error).To(Equal("Copilot history capability unavailable"))
		Expect(response.SkippedPaths).To(ContainElement("copilot://session/unconfigured"))

		tooMany := make([]string, 17)
		_, err = service.LearnAboutCSF(context.Background(), &pb.LearnAboutCSFRequest{CopilotSessionIds: tooMany})
		Expect(err).To(MatchError(ContainSubstring("at most 16 Copilot session IDs")))
	})
	It("discovers and calls onboarding over MCP when knowledge is unavailable", func() {
		service, err := csf.New()
		Expect(err).NotTo(HaveOccurred())
		response, err := service.LearnAboutCSF(context.Background(), &pb.LearnAboutCSFRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.KnowledgeAvailable).To(BeFalse())
		Expect(response.KnowledgeError).To(Equal("knowledge capability unavailable"))
		Expect(response.GuidanceMarkdown).NotTo(BeEmpty())
		Expect(response.Ingested).To(BeEmpty())
		handler := service.MCPHandler()
		list := callMCP(handler, consumerMCPToolList)
		Expect(list.Code).To(Equal(http.StatusOK))
		Expect(list.Body.String()).To(ContainSubstring(`"name":"LearnAboutCSF"`))
		called := callMCP(handler, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"LearnAboutCSF","arguments":{}}}`)
		Expect(called.Code).To(Equal(http.StatusOK), called.Body.String())
		Expect(called.Body.String()).To(ContainSubstring("knowledge capability unavailable"))
		Expect(called.Body.String()).NotTo(ContainSubstring(`"isError":true`))
	})
})
