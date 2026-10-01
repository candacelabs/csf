//go:build integration

package csf_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/pkg/patience"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/services/copilot-adapter/copilotbridge"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

const csfTestOpenSearchURLEnvironment = "CANDACE_CSF_TEST_OPENSEARCH_URL"

var _ = Describe("CSF onboarding external knowledge integration", func() {
	It("projects retained sources into OpenSearch and retrieves only the requested revision", func() {
		endpoint := os.Getenv(csfTestOpenSearchURLEnvironment)
		if endpoint == "" {
			Skip("set " + csfTestOpenSearchURLEnvironment + " to a disposable OpenSearch instance")
		}
		ctx := context.Background()
		fixture := buildCSFPostgresFixture(ctx)
		admin, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Addresses: []string{endpoint}}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(admin.Close)
		Expect(patience.Await(GinkgoT(), "onboarding OpenSearch", csfPostgresDatabaseBudget, func() error {
			requestContext, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			_, err := admin.Cluster.Health(requestContext, &opensearchapi.ClusterHealthReq{})
			return err
		}, func(err error) bool { return err == nil })).To(Succeed())
		indexName := "csf-onboarding-" + uuid.NewString()
		_, err = admin.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: indexName})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, err := admin.Indices.Delete(ctx, &opensearchapi.IndicesDeleteReq{Indices: []string{indexName}})
			Expect(err).NotTo(HaveOccurred())
		})
		index, err := csf.ConnectOpenSearch(endpoint, indexName, "", &http.Client{Timeout: 5 * time.Second})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(index.Close)
		artifacts, err := csf.NewArtifacts(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(artifacts.Close)
		consumerRoot := GinkgoT().TempDir()
		consumerPath := filepath.Join(consumerRoot, "README.md")
		Expect(os.WriteFile(consumerPath, []byte("csfonboardingconsumerproof first revision"), 0o600)).To(Succeed())
		history := copilotbridge.HistorySnapshot{
			Metadata: copilot.SessionMetadata{SessionID: "synthetic-history", ModifiedTime: time.Unix(42, 0)},
			Events:   []copilot.SessionEvent{{ID: "synthetic-user", Data: &rpc.UserMessageData{Content: "csfcopilothistoryproof retained native session"}}},
		}
		service, err := csf.New(csf.WithKnowledge(fixture.store, index, artifacts), csf.WithOnboarding(csf.OnboardingConfig{
			ConsumerRoot: consumerRoot,
			CopilotHistoryReader: func(_ context.Context, sessionID string) (copilotbridge.HistorySnapshot, error) {
				Expect(sessionID).To(Equal(history.Metadata.SessionID))
				return history, nil
			},
		}))
		Expect(err).NotTo(HaveOccurred())
		engine := httpserver.NewEngine("onboarding-search-integration")
		service.Register(engine)
		server := httptest.NewServer(engine)
		DeferCleanup(server.Close)
		client, err := csf.NewClient(server.URL, server.Client())
		Expect(err).NotTo(HaveOccurred())
		request := &pb.LearnAboutCSFRequest{
			ConsumerPaths: []string{"README.md"}, RetrievalQuery: "csfonboardingconsumerproof",
			CopilotSessionIds: []string{history.Metadata.SessionID},
		}
		queued, err := client.LearnAboutCSF(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(queued.KnowledgeError).To(BeEmpty())
		Expect(queued.Ingested).NotTo(BeEmpty())
		for _, receipt := range queued.Ingested {
			Expect(receipt.Queued).To(BeTrue())
			Expect(receipt.Indexed).To(BeFalse())
		}
		Expect(queued.CopilotHistory).To(HaveLen(1))
		historyReceipt := queued.CopilotHistory[0]
		Expect(historyReceipt.Error).To(BeEmpty())
		Expect(historyReceipt.EventCount).To(Equal(uint64(len(history.Events))))
		Expect(historyReceipt.Ingest.Queued).To(BeTrue())
		workers, err := service.StartProjectionWorkers(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(workers.Close)
		awaitOnboardingProjection(ctx, fixture.store, queued.Ingested)
		awaitOnboardingProjection(ctx, fixture.store, []*pb.IngestDocumentResult{historyReceipt.Ingest})
		historySearch, err := client.Search(ctx, &pb.SearchRequest{Query: "csfcopilothistoryproof", Limit: 10})
		Expect(err).NotTo(HaveOccurred())
		Expect(historySearch.Result.GetHits()).To(HaveLen(1))
		Expect(historySearch.Result.Hits[0].Document.SourceId).To(Equal(historyReceipt.Ingest.Document.SourceId))
		Expect(historySearch.Result.Hits[0].Excerpt).To(ContainSubstring("retained native session"))
		first, err := client.LearnAboutCSF(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.KnowledgeError).To(BeEmpty())
		Expect(first.Retrieval.GetMode()).To(Equal("lexical"))
		Expect(first.Retrieval.GetHits()).To(HaveLen(1))
		firstSource := first.Retrieval.Hits[0].Document
		Expect(first.Retrieval.Hits[0].Excerpt).To(ContainSubstring("first revision"))

		Expect(os.WriteFile(consumerPath, []byte("csfonboardingconsumerproof second revision"), 0o600)).To(Succeed())
		changed, err := client.LearnAboutCSF(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed.KnowledgeError).To(BeEmpty())
		// The old searchable revision cannot stand in for the newly queued one.
		for _, hit := range changed.Retrieval.GetHits() {
			Expect(hit.Document.Revision).NotTo(Equal(firstSource.Revision))
		}
		awaitOnboardingProjection(ctx, fixture.store, changed.Ingested)
		second, err := client.LearnAboutCSF(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(second.KnowledgeError).To(BeEmpty())
		Expect(second.Retrieval.GetHits()).To(HaveLen(1))
		secondSource := second.Retrieval.Hits[0].Document
		Expect(secondSource.SourceId).To(Equal(firstSource.SourceId))
		Expect(secondSource.Revision).NotTo(Equal(firstSource.Revision))
		Expect(second.Retrieval.Hits[0].Excerpt).To(ContainSubstring("second revision"))
		retained, err := client.GetDocument(ctx, &pb.GetDocumentRequest{Request: &pb.DocumentRequest{SourceId: firstSource.SourceId, Revision: firstSource.Revision}})
		Expect(err).NotTo(HaveOccurred())
		Expect(retained.Result.Text).To(Equal("csfonboardingconsumerproof first revision"))
	})
})

func awaitOnboardingProjection(ctx context.Context, store *csf.Postgres, receipts []*pb.IngestDocumentResult) {
	for _, receipt := range receipts {
		identity := &pb.DocumentRequest{SourceId: receipt.Document.SourceId, Revision: receipt.Document.Revision}
		patience.Await(GinkgoT(), "onboarding search projection", csfPostgresDatabaseBudget, func() *pb.ProjectionTask {
			task, err := store.GetProjection(ctx, identity)
			Expect(err).NotTo(HaveOccurred())
			return task
		}, func(task *pb.ProjectionTask) bool {
			return task.GetState() == pb.ProjectionState_PROJECTION_STATE_SUCCEEDED
		})
	}
}
