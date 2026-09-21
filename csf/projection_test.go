package csf_test

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/candacelabs/csf/csf"
	mocks "github.com/candacelabs/csf/csf/internal/mocks"
	"github.com/candacelabs/csf/pkg/patience"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var projectionBudget = patience.Budget{Within: 5 * time.Second}

var _ = Describe("in-process projection workers", func() {
	It("bounds concurrency and cancels active work without falsely settling its lease", func() {
		controller := gomock.NewController(GinkgoT())
		store := mocks.NewMockIKnowledgeStore(controller)
		index := mocks.NewMockIKnowledgeIndex(controller)
		artifacts, err := csf.NewArtifacts(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(artifacts.Close)
		hash, _, err := artifacts.Put([]byte("retained source"))
		Expect(err).NotTo(HaveOccurred())
		var claims atomic.Int64
		store.EXPECT().ClaimProjection(gomock.Any()).DoAndReturn(func(ctx context.Context) (*pb.ProjectionTask, error) {
			number := claims.Add(1)
			return &pb.ProjectionTask{Document: &pb.DocumentRequest{SourceId: "source", Revision: "v1"}, LeaseGeneration: number}, nil
		}).AnyTimes()
		store.EXPECT().GetDocument(gomock.Any(), gomock.Any()).Return(&pb.SourceDocument{ContentHash: hash}, nil).AnyTimes()
		entered := make(chan struct{}, 2)
		index.EXPECT().Index(gomock.Any(), gomock.Any(), "retained source").DoAndReturn(func(ctx context.Context, document *pb.SourceDocument, text string) error {
			entered <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		}).Times(2)
		service, err := csf.New(csf.WithKnowledge(store, index, artifacts))
		Expect(err).NotTo(HaveOccurred())
		workers, err := service.StartProjectionWorkers(context.Background())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(workers.Close)
		patience.Await(GinkgoT(), "two executing workers", projectionBudget, func() int { return len(entered) }, func(count int) bool { return count == 2 })
		Expect(workers.Configured()).To(Equal(2))
		Expect(workers.Active()).To(Equal(int64(2)))
		Expect(claims.Load()).To(Equal(int64(2)))
		_, err = service.StartProjectionWorkers(context.Background())
		Expect(err).To(HaveOccurred())
		workers.Close()
		Expect(workers.Active()).To(BeZero())
		// No CompleteProjection or FailProjection expectation: shutdown cannot
		// turn an interrupted external operation into a successful/failed receipt.
	})

	DescribeTable("settles the actual external result and resumes queued work at startup", func(projectionError error, corrupt bool) {
		controller := gomock.NewController(GinkgoT())
		store := mocks.NewMockIKnowledgeStore(controller)
		index := mocks.NewMockIKnowledgeIndex(controller)
		artifacts, err := csf.NewArtifacts(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(artifacts.Close)
		hash, _, err := artifacts.Put([]byte("retained source"))
		Expect(err).NotTo(HaveOccurred())
		if corrupt {
			hash = "invalid"
		}
		task := &pb.ProjectionTask{Document: &pb.DocumentRequest{SourceId: "source", Revision: "v1"}, LeaseGeneration: 7}
		var claimed atomic.Bool
		store.EXPECT().ClaimProjection(gomock.Any()).DoAndReturn(func(ctx context.Context) (*pb.ProjectionTask, error) {
			if claimed.CompareAndSwap(false, true) {
				return task, nil
			}
			return nil, nil
		}).AnyTimes()
		store.EXPECT().GetDocument(gomock.Any(), task.Document).Return(&pb.SourceDocument{ContentHash: hash}, nil)
		if !corrupt {
			index.EXPECT().Index(gomock.Any(), gomock.Any(), "retained source").Return(projectionError)
		}
		var settled atomic.Bool
		if projectionError != nil || corrupt {
			store.EXPECT().FailProjection(gomock.Any(), task, gomock.Any()).DoAndReturn(func(ctx context.Context, value *pb.ProjectionTask, problem string) error {
				settled.Store(true)
				return nil
			})
		} else {
			store.EXPECT().CompleteProjection(gomock.Any(), task).DoAndReturn(func(ctx context.Context, value *pb.ProjectionTask) error { settled.Store(true); return nil })
		}
		service, err := csf.New(csf.WithKnowledge(store, index, artifacts))
		Expect(err).NotTo(HaveOccurred())
		workers, err := service.StartProjectionWorkers(context.Background())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(workers.Close)
		patience.Await(GinkgoT(), "fenced result settlement", projectionBudget, settled.Load, func(done bool) bool { return done })
	}, Entry("successful projection", nil, false), Entry("retryable index failure", errors.New("backend unavailable"), false), Entry("invalid retained artifact", nil, true))
})
