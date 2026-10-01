//go:build integration

package csf_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/candacelabs/csf/csf"
	mocks "github.com/candacelabs/csf/csf/internal/mocks"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/pkg/patience"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = Describe("CSF onboarding PostgreSQL integration", func() {
	It("retains queued sources before workers start and retrieves their completed revisions", func() {
		ctx := context.Background()
		fixture := buildCSFPostgresFixture(ctx)
		consumerRoot := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(consumerRoot, "README.md"), []byte("consumer source"), 0600)).To(Succeed())
		artifacts, err := csf.NewArtifacts(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(artifacts.Close)
		index := mocks.NewMockIKnowledgeIndex(gomock.NewController(GinkgoT()))
		service, err := csf.New(csf.WithKnowledge(fixture.store, index, artifacts), csf.WithOnboarding(csf.OnboardingConfig{ConsumerRoot: consumerRoot}))
		Expect(err).NotTo(HaveOccurred())
		engine := httpserver.NewEngine("onboarding-integration")
		service.Register(engine)
		server := httptest.NewServer(engine)
		DeferCleanup(server.Close)
		client, err := csf.NewClient(server.URL, server.Client())
		Expect(err).NotTo(HaveOccurred())
		request := &pb.LearnAboutCSFRequest{ConsumerPaths: []string{"README.md"}, RetrievalQuery: "consumer source"}
		queued, err := client.LearnAboutCSF(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(queued.KnowledgeAvailable).To(BeTrue())
		Expect(queued.KnowledgeError).To(BeEmpty())
		Expect(queued.Ingested).NotTo(BeEmpty())
		Expect(queued.Retrieval).To(BeNil())
		for _, receipt := range queued.Ingested {
			Expect(receipt.Queued).To(BeTrue())
			Expect(receipt.Indexed).To(BeFalse())
			stored, err := client.GetDocument(ctx, &pb.GetDocumentRequest{Request: &pb.DocumentRequest{SourceId: receipt.Document.SourceId, Revision: receipt.Document.Revision}})
			Expect(err).NotTo(HaveOccurred())
			Expect(stored.Result.Text).NotTo(BeEmpty())
			Expect(stored.Result.Document.ArtifactRef).To(Equal(receipt.Document.ArtifactRef))
		}
		// PostgreSQL, artifact storage, generated HTTP and projection workers are
		// real here. Only the external search index is replaced by its typed mock.
		projected := make(chan *pb.SourceDocument, len(queued.Ingested))
		index.EXPECT().Index(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, document *pb.SourceDocument, _ string) error {
			projected <- document
			return nil
		}).Times(len(queued.Ingested))
		workers, err := service.StartProjectionWorkers(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(workers.Close)
		for _, receipt := range queued.Ingested {
			identity := &pb.DocumentRequest{SourceId: receipt.Document.SourceId, Revision: receipt.Document.Revision}
			patience.Await(GinkgoT(), "onboarding projection", csfPostgresDatabaseBudget, func() *pb.ProjectionTask {
				task, err := fixture.store.GetProjection(ctx, identity)
				Expect(err).NotTo(HaveOccurred())
				return task
			}, func(task *pb.ProjectionTask) bool {
				return task.GetState() == pb.ProjectionState_PROJECTION_STATE_SUCCEEDED
			})
		}
		workers.Close()
		Expect(workers.Active()).To(BeZero())
		Expect(projected).To(HaveLen(len(queued.Ingested)))
		index.EXPECT().Search(gomock.Any(), gomock.Any()).Return(&pb.SearchResult{Mode: "lexical", Hits: []*pb.SearchHit{{Document: <-projected}}}, nil)
		replayed, err := client.LearnAboutCSF(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.Ingested).To(HaveLen(len(queued.Ingested)))
		Expect(replayed.KnowledgeError).To(BeEmpty())
		for position, receipt := range replayed.Ingested {
			Expect(receipt.Indexed).To(BeTrue())
			Expect(receipt.Queued).To(BeFalse())
			Expect(receipt.Document.ContentHash).To(Equal(queued.Ingested[position].Document.ContentHash))
			Expect(receipt.Projection.Attempts).To(Equal(int32(1)))
		}
		Expect(replayed.Retrieval.GetHits()).To(HaveLen(1))
	})
})
