package csf_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/csf"
	mocks "github.com/candacelabs/csf/csf/internal/mocks"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

var _ = Describe("OpenSearch projection consumer", func() {
	var client *mocks.MockIOpenSearchClient
	BeforeEach(func() {
		controller := gomock.NewController(GinkgoT())
		client = mocks.NewMockIOpenSearchClient(controller)
	})

	It("retains provenance and a stable document identity, requesting immediate visibility", func() {
		document := &pb.SourceDocument{SourceId: "source", Revision: "revision", ContentHash: strings.Repeat("a", 64), RawSourceContentHash: strings.Repeat("b", 64), ArtifactRef: "aa/hash", SizeBytes: 5}
		client.EXPECT().Index(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request opensearchapi.IndexReq) (*opensearchapi.IndexResp, error) {
			digest := sha256.Sum256([]byte("source\x00revision"))
			Expect(request.Index).To(Equal("brain-knowledge"))
			Expect(request.ID).To(Equal(hex.EncodeToString(digest[:])))
			Expect(request.Params.Refresh).To(Equal("wait_for"))
			// OpenSearch document _source is application-defined JSON, not an API envelope.
			fields := map[string]json.RawMessage{}
			Expect(json.NewDecoder(request.Body).Decode(&fields)).To(Succeed())
			var text string
			Expect(json.Unmarshal(fields["text"], &text)).To(Succeed())
			Expect(text).To(Equal("hello"))
			delete(fields, "text")
			encoded, err := json.Marshal(fields)
			Expect(err).NotTo(HaveOccurred())
			retained := &pb.SourceDocument{}
			Expect(protojson.Unmarshal(encoded, retained)).To(Succeed())
			Expect(proto.Equal(retained, document)).To(BeTrue())
			return &opensearchapi.IndexResp{}, nil
		})
		projection, err := csf.NewOpenSearch("brain-knowledge", "", client)
		Expect(err).NotTo(HaveOccurred())
		Expect(projection.Index(context.Background(), document, "hello")).To(Succeed())
	})

	DescribeTable("selects the typed query and preserves scores, provenance and Unicode excerpts",
		func(model string, mode string) {
			// The SDK owns SearchResp/SearchHit; only our document payload is open-ended.
			source, err := json.Marshal(map[string]string{"source_id": "source", "revision": "revision", "content_hash": strings.Repeat("a", 64), "text": strings.Repeat("界", 1201)})
			Expect(err).NotTo(HaveOccurred())
			client.EXPECT().Search(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request *opensearchapi.SearchReq) (*opensearchapi.SearchResp, error) {
				Expect(request.Indices).To(Equal([]string{"brain-knowledge"}))
				Expect(request.Body.Size).To(Equal(new(3)))
				filter, err := json.Marshal(request.Body.Source)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(filter)).To(MatchJSON(`{"excludes":"embedding"}`))
				if model == "" {
					Expect(request.Body.Query.Neural).To(BeEmpty())
					query := request.Body.Query.Match["text"]
					value, err := query.AsFieldValue()
					Expect(err).NotTo(HaveOccurred())
					Expect(value.String()).To(Equal("question"))
				} else {
					Expect(request.Body.Query.Match).To(BeEmpty())
					Expect(request.Body.Query.Neural["embedding"]).To(Equal(opensearchapi.CommonQueryDSLNeuralQuery{QueryText: new("question"), ModelID: new(model), K: new(3)}))
				}
				return &opensearchapi.SearchResp{Hits: opensearchapi.SearchHitsMetadata{Hits: []opensearchapi.SearchHit{{Score: new(1.25), Source: source}, {Source: source}}}}, nil
			})
			projection, err := csf.NewOpenSearch("brain-knowledge", model, client)
			Expect(err).NotTo(HaveOccurred())
			result, err := projection.Search(context.Background(), &pb.SearchRequest{Query: "question", Limit: 3})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Mode).To(Equal(mode))
			Expect(result.EmbeddingModel).To(Equal(model))
			Expect(result.Hits).To(HaveLen(2))
			Expect(result.Hits[0].Score).To(Equal(1.25))
			Expect(result.Hits[0].Document.ContentHash).To(Equal(strings.Repeat("a", 64)))
			Expect(result.Hits[0].Excerpt).To(Equal(strings.Repeat("界", 1200)))
			Expect(result.Hits[1].Score).To(BeZero())
		}, Entry("lexical", "", "lexical"), Entry("semantic", "local-model", "semantic"))

	It("propagates SDK indexing and partial-search failures", func() {
		indexError := errors.New("document rejected")
		partial := &opensearchapi.PartialSearchError{FailedShards: 1, TotalShards: 2}
		client.EXPECT().Index(gomock.Any(), gomock.Any()).Return(nil, indexError)
		client.EXPECT().Search(gomock.Any(), gomock.Any()).Return(&opensearchapi.SearchResp{}, partial)
		projection, err := csf.NewOpenSearch("brain-knowledge", "", client)
		Expect(err).NotTo(HaveOccurred())
		Expect(projection.Index(context.Background(), &pb.SourceDocument{SourceId: "source"}, "hello")).To(MatchError(indexError))
		_, err = projection.Search(context.Background(), &pb.SearchRequest{Query: "question", Limit: 3})
		Expect(errors.Is(err, partial)).To(BeTrue())
	})

	It("passes the caller's cancellation context to the SDK", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		client.EXPECT().Search(ctx, gomock.Any()).Return(nil, context.Canceled)
		projection, err := csf.NewOpenSearch("brain-knowledge", "", client)
		Expect(err).NotTo(HaveOccurred())
		_, err = projection.Search(ctx, &pb.SearchRequest{Query: "question", Limit: 3})
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
	})

	It("rejects incomplete configuration and invalid requests without calling the SDK", func() {
		_, err := csf.NewOpenSearch("../invalid", "", client)
		Expect(err).To(HaveOccurred())
		_, err = csf.NewOpenSearch("brain-knowledge", "", nil)
		Expect(err).To(HaveOccurred())
		projection, err := csf.NewOpenSearch("brain-knowledge", "", client)
		Expect(err).NotTo(HaveOccurred())
		_, err = projection.Search(context.Background(), nil)
		Expect(err).To(HaveOccurred())
		_, err = projection.Search(context.Background(), &pb.SearchRequest{})
		Expect(err).To(HaveOccurred())
		Expect(projection.Close()).To(Succeed())
	})
})

var _ io.Closer = (*csf.OpenSearch)(nil)
