// Copyright 2026 Candace Labs

package llamacpp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	stdhttp "net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/net/model/llamacpp"
)

// rerankBody is what the pinned llama.cpp server returned on 2026-10-05 for
// bge-reranker-v2-m3 Q8_0, given the three documents the first spec sends.
const (
	specEndpoint = "http://reranker.example:8080"
	rerankBody   = `{"model":"/cache/bge-reranker-v2-m3-Q8_0.gguf","object":"list","usage":{"prompt_tokens":113,"total_tokens":113},"results":[{"index":0,"relevance_score":-2.81788969039917},{"index":1,"relevance_score":-4.922410011291504},{"index":2,"relevance_score":-10.762112617492676}]}`
)

func respond(status int, body string) *stdhttp.Response {
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

var _ = Describe("The llama.cpp reranker", func() {
	var client *MockIHTTPClient
	var reranker *llamacpp.LlamaReranker
	documents := []string{"func runUpgrade", "Package database", "burst credentials"}

	BeforeEach(func() {
		client = NewMockIHTTPClient(gomock.NewController(GinkgoT()))
		var err error
		reranker, err = llamacpp.NewLlamaReranker(client, specEndpoint+"/")
		Expect(err).NotTo(HaveOccurred())
	})

	It("posts the query and documents and returns one score per document, in document order", func() {
		var sent map[string]any
		client.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
			Expect(request.URL.String()).To(Equal(specEndpoint + "/v1/rerank"))
			content, err := io.ReadAll(request.Body)
			Expect(err).NotTo(HaveOccurred())
			Expect(json.Unmarshal(content, &sent)).To(Succeed())
			return respond(stdhttp.StatusOK, rerankBody), nil
		})
		scores, err := reranker.Rerank(context.Background(), "where is the csf upgrade verb", documents)
		Expect(err).NotTo(HaveOccurred())
		Expect(sent).To(HaveKeyWithValue("query", "where is the csf upgrade verb"))
		Expect(sent).To(HaveKeyWithValue("documents", []any{"func runUpgrade", "Package database", "burst credentials"}))
		Expect(scores).To(Equal([]float64{-2.81788969039917, -4.922410011291504, -10.762112617492676}))
	})

	It("places scores by the index the server names, whatever order it answers in", func() {
		client.EXPECT().Do(gomock.Any()).Return(respond(stdhttp.StatusOK,
			`{"results":[{"index":2,"relevance_score":3},{"index":0,"relevance_score":1},{"index":1,"relevance_score":2}]}`), nil)
		scores, err := reranker.Rerank(context.Background(), "query", documents)
		Expect(err).NotTo(HaveOccurred())
		Expect(scores).To(Equal([]float64{1, 2, 3}))
	})

	It("refuses scores that do not cover every document exactly once", func() {
		for _, body := range []string{
			`{"results":[{"index":0,"relevance_score":1}]}`,
			`{"results":[{"index":0,"relevance_score":1},{"index":0,"relevance_score":1},{"index":1,"relevance_score":1}]}`,
			`{"results":[{"index":0,"relevance_score":1},{"index":1,"relevance_score":1},{"index":7,"relevance_score":1}]}`,
		} {
			client.EXPECT().Do(gomock.Any()).Return(respond(stdhttp.StatusOK, body), nil)
			_, err := reranker.Rerank(context.Background(), "query", documents)
			Expect(err).To(MatchError(llamacpp.ErrScoreCount))
		}
	})

	It("reports a refusal with the server's status and text", func() {
		client.EXPECT().Do(gomock.Any()).Return(respond(stdhttp.StatusServiceUnavailable, `{"error":"Loading model"}`), nil)
		_, err := reranker.Rerank(context.Background(), "query", documents)
		Expect(err).To(MatchError(ContainSubstring("HTTP 503")))
	})

	It("reports a transport failure", func() {
		client.EXPECT().Do(gomock.Any()).Return(nil, errors.New("dial tcp: connection refused"))
		_, err := reranker.Rerank(context.Background(), "query", documents)
		Expect(err).To(MatchError(ContainSubstring("connection refused")))
	})

	It("sends nothing for no documents", func() {
		scores, err := reranker.Rerank(context.Background(), "query", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(scores).To(BeEmpty())
	})
})

var _ = Describe("Building the llama.cpp reranker", func() {
	It("requires the http capability and an endpoint", func() {
		_, err := llamacpp.NewLlamaReranker(nil, specEndpoint)
		Expect(err).To(MatchError(llamacpp.ErrNoClient))
		_, err = llamacpp.NewLlamaReranker(NewMockIHTTPClient(gomock.NewController(GinkgoT())), "")
		Expect(err).To(MatchError(llamacpp.ErrNoEndpoint))
	})
})
