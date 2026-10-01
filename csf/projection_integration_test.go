//go:build integration

package csf_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"time"

	"github.com/candacelabs/csf/csf"
	mocks "github.com/candacelabs/csf/csf/internal/mocks"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/pkg/patience"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

// A claim can commit even when its response is lost. Allow a 60-second lease
// to expire and the next worker to index and settle it on a loaded runner.
var projectionRecoveryBudget = patience.Budget{Within: 2 * time.Minute, Interval: 100 * time.Millisecond}

var _ = Describe("PostgreSQL projection integration", func() {
	It("persists queued ingestion and retries through the shared generated HTTP contract", func() {
		ctx := context.Background()
		fixture := buildCSFPostgresFixture(ctx)
		pool := fixture.pool
		store := fixture.store
		artifacts, err := csf.NewArtifacts(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(artifacts.Close)
		index := mocks.NewMockIKnowledgeIndex(gomock.NewController(GinkgoT()))
		service, err := csf.New(csf.WithKnowledge(store, index, artifacts))
		Expect(err).NotTo(HaveOccurred())
		engine := httpserver.NewEngine("projection-integration")
		service.Register(engine)
		server := httptest.NewServer(engine)
		DeferCleanup(server.Close)
		client, err := csf.NewClient(server.URL, server.Client())
		Expect(err).NotTo(HaveOccurred())
		text := "immutable evidence queued in PostgreSQL"
		digest := sha256.Sum256([]byte(text))
		request := &pb.IngestDocumentRequest{Document: &pb.SourceDocument{SourceId: "integration", Revision: "v1", SourceUri: "urn:integration", Title: "Integration fixture", MediaType: "text/plain", License: "test-fixture", ContentHash: hex.EncodeToString(digest[:]), RetrievedAt: time.Now().UTC().Format(time.RFC3339Nano)}, Text: text}
		ingested, err := client.IngestDocument(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(ingested.Result.Queued).To(BeTrue())
		Expect(ingested.Result.Indexed).To(BeFalse())
		Expect(ingested.Result.Projection.State).To(Equal(pb.ProjectionState_PROJECTION_STATE_PENDING))
		// Model a claimant disappearing before it indexes or acknowledges work.
		abandoned, err := store.ClaimProjection(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(abandoned).NotTo(BeNil())
		Expect(abandoned.Attempts).To(Equal(int32(1)))
		// Advance only this disposable fixture's lease instead of sleeping a minute.
		_, err = pool.Exec(ctx, "UPDATE brainspine_projection_tasks SET lease_until = statement_timestamp() - interval '1 second' WHERE source_id = $1 AND revision = $2", request.Document.SourceId, request.Document.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(store.CompleteProjection(ctx, abandoned)).To(MatchError(pgx.ErrNoRows))
		// Reopen connections and reconstruct the service without an in-memory wakeup.
		pool.Close()
		pool, err = pgxpool.NewWithConfig(ctx, fixture.config.Copy())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(pool.Close)
		store, err = csf.NewPostgres(pool)
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Migrate(ctx)).To(Succeed())
		service, err = csf.New(csf.WithKnowledge(store, index, artifacts))
		Expect(err).NotTo(HaveOccurred())
		first := index.EXPECT().Index(gomock.Any(), gomock.Any(), text).Return(errors.New("transient projection failure"))
		index.EXPECT().Index(gomock.Any(), gomock.Any(), text).Return(nil).After(first).MaxTimes(3).MinTimes(1)
		workers, err := service.StartProjectionWorkers(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(workers.Close)
		identity := &pb.DocumentRequest{SourceId: "integration", Revision: "v1"}
		task := patience.Await(GinkgoT(), "abandoned lease and durable retry complete", projectionRecoveryBudget, func() *pb.ProjectionTask {
			task, err := store.GetProjection(ctx, identity)
			if err != nil {
				return nil
			}
			return task
		}, func(task *pb.ProjectionTask) bool {
			return task.GetState() == pb.ProjectionState_PROJECTION_STATE_SUCCEEDED
		})
		// Claims include the abandoned lease and may include a lost response or
		// acknowledgement; the queue promises at-least-once indexing, not exactly-once.
		Expect(task.Attempts).To(BeNumerically(">=", 3))
		Expect(task.Attempts).To(BeNumerically("<=", 5))
		Expect(task.LeaseGeneration).To(Equal(int64(task.Attempts)))
		Expect(task.LastError).To(BeEmpty())
		Expect(store.CompleteProjection(ctx, abandoned)).To(MatchError(pgx.ErrNoRows))
		Expect(store.FailProjection(ctx, abandoned, "stale claimant")).To(MatchError(pgx.ErrNoRows))
		workers.Close()
		engine = httpserver.NewEngine("projection-reopened")
		service.Register(engine)
		reopened := httptest.NewServer(engine)
		DeferCleanup(reopened.Close)
		client, err = csf.NewClient(reopened.URL, reopened.Client())
		Expect(err).NotTo(HaveOccurred())
		read, err := client.GetDocument(ctx, &pb.GetDocumentRequest{Request: identity})
		Expect(err).NotTo(HaveOccurred())
		Expect(read.Result.Text).To(Equal(text))
		Expect(read.Result.Projection.State).To(Equal(pb.ProjectionState_PROJECTION_STATE_SUCCEEDED))
		replay, err := client.IngestDocument(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(replay.Result.Indexed).To(BeTrue())
		Expect(replay.Result.Queued).To(BeFalse())
		Expect(replay.Result.Projection.Attempts).To(Equal(task.Attempts))
		counts, err := store.CountProjections(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(counts).To(HaveLen(4))
		Expect(workers.Active()).To(BeZero())
	})
})
