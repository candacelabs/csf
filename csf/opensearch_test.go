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
	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/csf"
	mocks "github.com/candacelabs/csf/csf/internal/mocks"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

// chunkSource is one indexed chunk's _source as the projection writes it.
func chunkSource(path string, start int, end int, text string) json.RawMessage {
	GinkgoHelper()
	source, err := json.Marshal(map[string]any{"source_id": "source/" + path, "revision": "revision", "content_hash": strings.Repeat("a", 64),
		"title": path, "text": text, "path": path, "line_start": start, "line_end": end})
	Expect(err).NotTo(HaveOccurred())
	return source
}

func searchHit(id string, score float64, source json.RawMessage) opensearchapi.SearchHit {
	return opensearchapi.SearchHit{ID: &id, Score: &score, Source: source}
}

// expectCreated expects the projection to create its index once, answering
// as OpenSearch does when the index is already there.
func expectCreated(client *mocks.MockIOpenSearchClient, embedded bool) {
	client.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request opensearchapi.IndicesCreateReq) (*opensearchapi.IndicesCreateResp, error) {
		Expect(request.Index).To(Equal("brain-knowledge"))
		mapping, err := json.Marshal(request.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(mapping)).To(ContainSubstring(`"source_id":{"type":"keyword"}`))
		Expect(string(mapping)).To(ContainSubstring(`"path":{"analyzer":"simple","type":"text"}`))
		if embedded {
			Expect(string(mapping)).To(ContainSubstring(`"embedding":{"dimension":2,"method":{"engine":"lucene","name":"hnsw","space_type":"cosinesimil"},"type":"knn_vector"}`))
			Expect(string(mapping)).To(ContainSubstring(`"knn":"true"`))
		} else {
			Expect(string(mapping)).NotTo(ContainSubstring("knn"))
		}
		return nil, &opensearch.StructError{Status: 400, Err: opensearch.Err{Type: "resource_already_exists_exception"}}
	})
}

// expectForgotten expects the source's earlier chunks to be deleted by its id.
func expectForgotten(client *mocks.MockIOpenSearchClient, sourceID string) *gomock.Call {
	return client.EXPECT().DeleteByQuery(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request *opensearchapi.DeleteByQueryReq) (*opensearchapi.DeleteByQueryResp, error) {
		Expect(request.Indices).To(Equal([]string{"brain-knowledge"}))
		query, err := json.Marshal(request.Body.Query)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(query)).To(MatchJSON(`{"term":{"source_id":"` + sourceID + `"}}`))
		Expect(request.Params.Refresh).To(Equal("true"))
		return &opensearchapi.DeleteByQueryResp{}, nil
	})
}

var _ = Describe("OpenSearch projection consumer", func() {
	var client *mocks.MockIOpenSearchClient
	BeforeEach(func() {
		controller := gomock.NewController(GinkgoT())
		client = mocks.NewMockIOpenSearchClient(controller)
	})

	It("replaces a source with overlapping line chunks that keep its provenance, the last one waiting for visibility", func() {
		document := &pb.SourceDocument{SourceId: "source", Revision: "revision", Title: "app/upgrade.go", ContentHash: strings.Repeat("a", 64), RawSourceContentHash: strings.Repeat("b", 64), ArtifactRef: "aa/hash", SizeBytes: 5}
		lines := make([]string, 100)
		for position := range lines {
			lines[position] = "line"
		}
		expectCreated(client, false)
		expectForgotten(client, "source")
		var written []map[string]json.RawMessage
		client.EXPECT().Index(gomock.Any(), gomock.Any()).Times(3).DoAndReturn(func(ctx context.Context, request opensearchapi.IndexReq) (*opensearchapi.IndexResp, error) {
			digest := sha256.Sum256([]byte("source\x00revision\x00" + string(rune('0'+len(written)))))
			Expect(request.Index).To(Equal("brain-knowledge"))
			Expect(request.ID).To(Equal(hex.EncodeToString(digest[:])))
			// OpenSearch document _source is application-defined JSON, not an API envelope.
			fields := map[string]json.RawMessage{}
			Expect(json.NewDecoder(request.Body).Decode(&fields)).To(Succeed())
			fields["refresh"] = json.RawMessage(`"` + request.Params.Refresh + `"`)
			written = append(written, fields)
			return &opensearchapi.IndexResp{}, nil
		})
		projection, err := csf.NewOpenSearch("brain-knowledge", "", client)
		Expect(err).NotTo(HaveOccurred())
		Expect(projection.Index(context.Background(), document, strings.Join(lines, "\n")+"\n")).To(Succeed())

		ranges := make([]string, len(written))
		for position, fields := range written {
			ranges[position] = string(fields["line_start"]) + "-" + string(fields["line_end"]) + " " + string(fields["refresh"])
			Expect(string(fields["path"])).To(Equal(`"app/upgrade.go"`))
			Expect(fields).NotTo(HaveKey("embedding"))
		}
		Expect(ranges).To(Equal([]string{`1-40 ""`, `31-70 ""`, `61-100 "wait_for"`}))
		var text string
		Expect(json.Unmarshal(written[0]["text"], &text)).To(Succeed())
		Expect(text).To(Equal(strings.Repeat("line\n", 40)))
		for _, field := range []string{"text", "path", "line_start", "line_end", "refresh"} {
			delete(written[0], field)
		}
		encoded, err := json.Marshal(written[0])
		Expect(err).NotTo(HaveOccurred())
		retained := &pb.SourceDocument{}
		Expect(protojson.Unmarshal(encoded, retained)).To(Succeed())
		Expect(proto.Equal(retained, document)).To(BeTrue())
	})

	It("embeds each chunk under its path in batches and maps the vector field once", func() {
		var embedded [][]string
		embedder := func(ctx context.Context, texts []string, query bool) ([][]float32, error) {
			Expect(query).To(BeFalse())
			embedded = append(embedded, texts)
			vectors := make([][]float32, len(texts))
			for position := range texts {
				vectors[position] = []float32{1, float32(position)}
			}
			return vectors, nil
		}
		expectCreated(client, true)
		expectForgotten(client, "source").Times(2)
		var vectors []string
		client.EXPECT().Index(gomock.Any(), gomock.Any()).Times(2).DoAndReturn(func(ctx context.Context, request opensearchapi.IndexReq) (*opensearchapi.IndexResp, error) {
			fields := map[string]json.RawMessage{}
			Expect(json.NewDecoder(request.Body).Decode(&fields)).To(Succeed())
			vectors = append(vectors, string(fields["embedding"]))
			return &opensearchapi.IndexResp{}, nil
		})
		projection, err := csf.NewOpenSearch("brain-knowledge", "", client, csf.WithEmbedder("tiny", 2, embedder))
		Expect(err).NotTo(HaveOccurred())
		document := &pb.SourceDocument{SourceId: "source", Revision: "revision", Title: "notes.md"}
		Expect(projection.Index(context.Background(), document, "one line\n")).To(Succeed())
		Expect(projection.Index(context.Background(), document, "")).To(Succeed())
		Expect(embedded).To(Equal([][]string{{"notes.md\none line\n"}, {"notes.md\n"}}))
		Expect(vectors).To(Equal([]string{"[1,0]", "[1,0]"}))
	})

	It("matches text and, weighted, path for lexical hits, keeping their scores and pointing at their lines", func() {
		client.EXPECT().Search(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request *opensearchapi.SearchReq) (*opensearchapi.SearchResp, error) {
			Expect(request.Indices).To(Equal([]string{"brain-knowledge"}))
			Expect(request.Body.Size).To(Equal(new(50)))
			filter, err := json.Marshal(request.Body.Source)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(filter)).To(MatchJSON(`{"excludes":"embedding"}`))
			query, err := json.Marshal(request.Body.Query)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(query)).To(MatchJSON(`{"bool":{"should":[{"match":{"text":{"boost":1,"query":"question"}}},{"match":{"path":{"boost":2,"query":"question"}}}]}}`))
			return &opensearchapi.SearchResp{Hits: opensearchapi.SearchHitsMetadata{Hits: []opensearchapi.SearchHit{
				searchHit("a", 1.25, chunkSource("app/a.go", 31, 70, strings.Repeat("界", 1201))),
				searchHit("b", 0.5, chunkSource("app/b.go", 1, 40, "b")),
				searchHit("c", 0.25, chunkSource("app/c.go", 1, 2, "c")),
			}}}, nil
		})
		projection, err := csf.NewOpenSearch("brain-knowledge", "", client)
		Expect(err).NotTo(HaveOccurred())
		result, err := projection.Search(context.Background(), &pb.SearchRequest{Query: "question", Limit: 2})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Mode).To(Equal("lexical"))
		Expect(result.EmbeddingModel).To(BeEmpty())
		Expect(result.Reranker).To(BeEmpty())
		Expect(result.Hits).To(HaveLen(2))
		first := result.Hits[0]
		Expect(first.Score).To(Equal(1.25))
		Expect([]any{first.Path, first.LineStart, first.LineEnd, first.Why}).To(Equal([]any{"app/a.go", uint32(31), uint32(70), "lexical #1"}))
		Expect(first.Document.ContentHash).To(Equal(strings.Repeat("a", 64)))
		Expect(first.Excerpt).To(Equal(strings.Repeat("界", 1200)))
		Expect(result.Hits[1].Path).To(Equal("app/b.go"))
	})

	It("asks the configured ML Commons model for its nearest chunks alone", func() {
		client.EXPECT().Search(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request *opensearchapi.SearchReq) (*opensearchapi.SearchResp, error) {
			Expect(request.Body.Query.Match).To(BeEmpty())
			Expect(request.Body.Query.Neural["embedding"]).To(Equal(opensearchapi.CommonQueryDSLNeuralQuery{QueryText: new("question"), ModelID: new("local-model"), K: new(50)}))
			return &opensearchapi.SearchResp{Hits: opensearchapi.SearchHitsMetadata{Hits: []opensearchapi.SearchHit{searchHit("a", 0.9, chunkSource("app/a.go", 1, 3, "a"))}}}, nil
		})
		projection, err := csf.NewOpenSearch("brain-knowledge", "local-model", client)
		Expect(err).NotTo(HaveOccurred())
		result, err := projection.Search(context.Background(), &pb.SearchRequest{Query: "question", Limit: 3})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Mode).To(Equal("semantic"))
		Expect(result.EmbeddingModel).To(Equal("local-model"))
		Expect(result.Hits[0].Score).To(Equal(0.9))
		Expect(result.Hits[0].Why).To(Equal("vector #1"))
	})

	Describe("with an embedder and a reranker", func() {
		embedder := func(ctx context.Context, texts []string, query bool) ([][]float32, error) {
			Expect(query).To(BeTrue())
			Expect(texts).To(Equal([]string{"question"}))
			return [][]float32{{0.5, 0.5}}, nil
		}
		// Lexical proposes a, b; the vector leg proposes b, c. Fusion puts b,
		// found by both, first.
		expectLegs := func() {
			client.EXPECT().Search(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request *opensearchapi.SearchReq) (*opensearchapi.SearchResp, error) {
				Expect(request.Body.Query.Bool).NotTo(BeNil())
				return &opensearchapi.SearchResp{Hits: opensearchapi.SearchHitsMetadata{Hits: []opensearchapi.SearchHit{
					searchHit("a", 9, chunkSource("app/a.go", 1, 40, "alpha")), searchHit("b", 8, chunkSource("app/b.go", 1, 40, "beta")),
				}}}, nil
			})
			client.EXPECT().Search(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request *opensearchapi.SearchReq) (*opensearchapi.SearchResp, error) {
				Expect(request.Body.Query.KNN["embedding"]).To(Equal(opensearchapi.CommonQueryDSLKNNQuery{Vector: []float32{0.5, 0.5}, K: new(50)}))
				return &opensearchapi.SearchResp{Hits: opensearchapi.SearchHitsMetadata{Hits: []opensearchapi.SearchHit{
					searchHit("b", 0.7, chunkSource("app/b.go", 1, 40, "beta")), searchHit("c", 0.6, chunkSource("app/c.go", 1, 40, "gamma")),
				}}}, nil
			})
		}

		It("fuses both retrievers and returns the reranker's order, comparing it with the fused one", func() {
			var reranked []string
			reranker := func(ctx context.Context, query string, documents []string) ([]float64, error) {
				Expect(query).To(Equal("question"))
				reranked = documents
				return []float64{0.1, 0.2, 3}, nil
			}
			expectLegs()
			projection, err := csf.NewOpenSearch("brain-knowledge", "", client, csf.WithEmbedder("tiny", 2, embedder), csf.WithReranker("cross", reranker))
			Expect(err).NotTo(HaveOccurred())
			fused, result, err := projection.Compare(context.Background(), &pb.SearchRequest{Query: "question", Limit: 3})
			Expect(err).NotTo(HaveOccurred())
			Expect(reranked).To(Equal([]string{"app/b.go\nbeta", "app/a.go\nalpha", "app/c.go\ngamma"}))
			paths := func(result *pb.SearchResult) []string {
				var found []string
				for _, hit := range result.Hits {
					found = append(found, hit.Path+" "+hit.Why)
				}
				return found
			}
			Expect(fused.Mode).To(Equal("hybrid"))
			Expect(fused.Reranker).To(BeEmpty())
			Expect(paths(fused)).To(Equal([]string{"app/b.go lexical #2; vector #1", "app/a.go lexical #1", "app/c.go vector #2"}))
			Expect(result.Mode).To(Equal("hybrid"))
			Expect(result.EmbeddingModel).To(Equal("tiny"))
			Expect(result.Reranker).To(Equal("cross"))
			Expect(paths(result)).To(Equal([]string{"app/c.go vector #2; reranked 3.00", "app/a.go lexical #1; reranked 0.20", "app/b.go lexical #2; vector #1; reranked 0.10"}))
			Expect(result.Hits[0].Score).To(Equal(3.0))
		})

		It("keeps the fused order and says why when the reranker fails", func() {
			reranker := func(ctx context.Context, query string, documents []string) ([]float64, error) {
				return nil, errors.New("GPU busy")
			}
			expectLegs()
			projection, err := csf.NewOpenSearch("brain-knowledge", "", client, csf.WithEmbedder("tiny", 2, embedder), csf.WithReranker("cross", reranker))
			Expect(err).NotTo(HaveOccurred())
			result, err := projection.Search(context.Background(), &pb.SearchRequest{Query: "question", Limit: 1})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Reranker).To(BeEmpty())
			Expect(result.Hits).To(HaveLen(1))
			Expect(result.Hits[0].Path).To(Equal("app/b.go"))
			Expect(result.Hits[0].Why).To(Equal("lexical #2; vector #1; reranker unavailable: GPU busy"))
		})
	})

	It("propagates SDK indexing, index-creation and partial-search failures", func() {
		indexError := errors.New("document rejected")
		partial := &opensearchapi.PartialSearchError{FailedShards: 1, TotalShards: 2}
		expectCreated(client, false)
		expectForgotten(client, "source")
		client.EXPECT().Index(gomock.Any(), gomock.Any()).Return(nil, indexError)
		client.EXPECT().Search(gomock.Any(), gomock.Any()).Return(&opensearchapi.SearchResp{}, partial)
		projection, err := csf.NewOpenSearch("brain-knowledge", "", client)
		Expect(err).NotTo(HaveOccurred())
		Expect(projection.Index(context.Background(), &pb.SourceDocument{SourceId: "source"}, "hello")).To(MatchError(indexError))
		_, err = projection.Search(context.Background(), &pb.SearchRequest{Query: "question", Limit: 3})
		Expect(errors.Is(err, partial)).To(BeTrue())

		blocked := &opensearch.StructError{Status: 403, Err: opensearch.Err{Type: "index_create_block_exception"}}
		client.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, blocked)
		fresh, err := csf.NewOpenSearch("brain-knowledge", "", client)
		Expect(err).NotTo(HaveOccurred())
		Expect(fresh.Index(context.Background(), &pb.SourceDocument{SourceId: "source"}, "hello")).To(MatchError(blocked))
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
		embedder := func(ctx context.Context, texts []string, query bool) ([][]float32, error) { return nil, nil }
		for _, option := range []csf.OpenSearchOption{nil, csf.WithEmbedder("", 2, embedder), csf.WithEmbedder("tiny", 0, embedder), csf.WithEmbedder("tiny", 2, nil), csf.WithReranker("", nil)} {
			_, err = csf.NewOpenSearch("brain-knowledge", "", client, option)
			Expect(err).To(HaveOccurred())
		}
		_, err = csf.NewOpenSearch("brain-knowledge", "local-model", client, csf.WithEmbedder("tiny", 2, embedder))
		Expect(err).To(MatchError(ContainSubstring("configure one")))
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
