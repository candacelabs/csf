package csf_test

import (
	"context"
	"errors"
	"io"

	"github.com/candacelabs/csf/csf"
	mocks "github.com/candacelabs/csf/csf/internal/mocks"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("OpenSearch simulator source archive", func() {
	var sdk *mocks.MockIOpenSearchClient
	var search *csf.OpenSearch
	var source *pb.SimulationTraceSource
	BeforeEach(func() {
		sdk = mocks.NewMockIOpenSearchClient(gomock.NewController(GinkgoT()))
		var err error
		search, err = csf.NewOpenSearch("brain-knowledge", "", sdk)
		Expect(err).NotTo(HaveOccurred())
		source = &pb.SimulationTraceSource{
			Run:         &pb.SimulationRun{RunId: "native", UpdatedAt: "2026-09-16T00:00:00Z", Steps: 1},
			EventsJsonl: "{\"measurement\":{\"step\":\"1\"}}\n", TrajectoryJsonl: "{\"position\":[1,2,3]}\n", ManifestJson: "{}",
			Logs: "actual vendor output\nUnicode: 界\n",
		}
	})
	It("round-trips original bytes and typed provenance through the generated SDK", func() {
		var encoded []byte
		var documentID string
		sdk.EXPECT().Index(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request opensearchapi.IndexReq) (*opensearchapi.IndexResp, error) {
			Expect(request.Index).To(Equal("brain-logs"))
			Expect(request.Params.Refresh).To(Equal("wait_for"))
			var err error
			encoded, err = io.ReadAll(request.Body)
			Expect(err).NotTo(HaveOccurred())
			documentID = request.ID
			return &opensearchapi.IndexResp{}, nil
		})
		sdk.EXPECT().Search(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request *opensearchapi.SearchReq) (*opensearchapi.SearchResp, error) {
			Expect(request.Indices).To(Equal([]string{"brain-logs"}))
			Expect(request.Body.Source).To(BeNil()) // Replay needs the complete archived source.
			term := request.Body.Query.Term["_id"]
			query, err := term.AsFieldValue()
			Expect(err).NotTo(HaveOccurred())
			Expect(query.String()).To(Equal(documentID))
			return &opensearchapi.SearchResp{Hits: opensearchapi.SearchHitsMetadata{Hits: []opensearchapi.SearchHit{{Source: encoded}}}}, nil
		})
		identity, err := search.IndexSimulationSource(context.Background(), "brain-logs", source)
		Expect(err).NotTo(HaveOccurred())
		Expect(identity).To(HaveLen(64))
		record, err := search.SimulationSource(context.Background(), "brain-logs", "native")
		Expect(err).NotTo(HaveOccurred())
		Expect(record.SourceSha256).To(HaveLen(64))
		Expect(record.Message).To(Equal(source.Logs))
		Expect(proto.Equal(record.Simulation, source)).To(BeTrue())
	})
	It("rejects changed source bytes rather than reconstructing unsupported evidence", func() {
		var record pb.SimulationLogRecord
		sdk.EXPECT().Index(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request opensearchapi.IndexReq) (*opensearchapi.IndexResp, error) {
			content, err := io.ReadAll(request.Body)
			Expect(err).NotTo(HaveOccurred())
			Expect(protojson.Unmarshal(content, &record)).To(Succeed())
			return &opensearchapi.IndexResp{}, nil
		})
		_, err := search.IndexSimulationSource(context.Background(), "brain-logs", source)
		Expect(err).NotTo(HaveOccurred())
		record.Simulation.TrajectoryJsonl = "{\"position\":[0,0,0]}\n"
		changed, err := protojson.Marshal(&record)
		Expect(err).NotTo(HaveOccurred())
		sdk.EXPECT().Search(gomock.Any(), gomock.Any()).Return(&opensearchapi.SearchResp{Hits: opensearchapi.SearchHitsMetadata{Hits: []opensearchapi.SearchHit{{Source: changed}}}}, nil)
		_, err = search.SimulationSource(context.Background(), "brain-logs", "native")
		Expect(err).To(MatchError(ContainSubstring("hash mismatch")))
	})
	It("preserves dependency failures and rejects absent sources", func() {
		failure := errors.New("OpenSearch unavailable")
		sdk.EXPECT().Index(gomock.Any(), gomock.Any()).Return(nil, failure)
		_, err := search.IndexSimulationSource(context.Background(), "brain-logs", source)
		Expect(err).To(MatchError(failure))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		sdk.EXPECT().Search(ctx, gomock.Any()).Return(nil, context.Canceled)
		_, err = search.SimulationSource(ctx, "brain-logs", "native")
		Expect(err).To(MatchError(context.Canceled))
		sdk.EXPECT().Search(gomock.Any(), gomock.Any()).Return(&opensearchapi.SearchResp{}, nil)
		_, err = search.SimulationSource(context.Background(), "brain-logs", "native")
		Expect(err).To(MatchError(ContainSubstring("not found")))
	})
})
