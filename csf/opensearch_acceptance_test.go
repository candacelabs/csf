//go:build acceptance

package csf_test

import (
	"context"
	"hash/fnv"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"

	"github.com/candacelabs/csf/csf"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

// acceptanceDimension is the width of the word-hash vectors the acceptance
// embedder produces: enough buckets that the spec's few words rarely collide.
const acceptanceDimension = 64

// wordHashEmbedder is a deterministic stand-in for a model: each word adds one
// to its hash bucket, so texts sharing words are near each other.
func wordHashEmbedder(_ context.Context, texts []string, _ bool) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for position, text := range texts {
		vector := make([]float32, acceptanceDimension)
		vector[0] = 1 // never the zero vector, which cosine similarity rejects
		for _, word := range strings.Fields(strings.ToLower(text)) {
			hash := fnv.New32a()
			_, _ = hash.Write([]byte(word))
			vector[hash.Sum32()%acceptanceDimension]++
		}
		vectors[position] = vector
	}
	return vectors, nil
}

// pathReranker prefers documents whose first line, the path, names the query.
func pathReranker(_ context.Context, query string, documents []string) ([]float64, error) {
	scores := make([]float64, len(documents))
	for position, document := range documents {
		path, _, _ := strings.Cut(document, "\n")
		if strings.Contains(path, query) {
			scores[position] = 1
		}
	}
	return scores, nil
}

var _ = Describe("OpenSearch chunk projection against a real OpenSearch", func() {
	It("creates its mapping, points hits at line ranges, replaces a source's chunks and forgets them", func() {
		endpoint := os.Getenv(csfTestOpenSearchURLEnvironment)
		if endpoint == "" {
			Skip("set " + csfTestOpenSearchURLEnvironment + " to a disposable OpenSearch instance")
		}
		ctx := context.Background()
		admin, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Addresses: []string{endpoint}}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(admin.Close)
		indexName := "csf-chunks-" + uuid.NewString()
		DeferCleanup(func() {
			_, err := admin.Indices.Delete(ctx, &opensearchapi.IndicesDeleteReq{Indices: []string{indexName}})
			Expect(err).NotTo(HaveOccurred())
		})
		index, err := csf.ConnectOpenSearch(endpoint, indexName, "", &http.Client{Timeout: 10 * time.Second},
			csf.WithEmbedder("word-hash", acceptanceDimension, wordHashEmbedder), csf.WithReranker("path", pathReranker))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(index.Close)

		lines := make([]string, 100)
		for position := range lines {
			lines[position] = "filler line"
		}
		lines[74] = "func runUpgrade() replaces the csf binary"
		upgrade := &pb.SourceDocument{SourceId: "repo/app/upgrade.go", Revision: "one", ContentHash: strings.Repeat("a", 64), Title: "app/upgrade.go"}
		Expect(index.Index(ctx, upgrade, strings.Join(lines, "\n")+"\n")).To(Succeed())
		other := &pb.SourceDocument{SourceId: "repo/docs/other.md", Revision: "one", ContentHash: strings.Repeat("b", 64), Title: "docs/other.md"}
		Expect(index.Index(ctx, other, "runUpgrade is mentioned here too\n")).To(Succeed())

		result, err := index.Search(ctx, &pb.SearchRequest{Query: "runUpgrade", Limit: 5})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Mode).To(Equal("hybrid"))
		Expect(result.Reranker).To(Equal("path"))
		Expect(result.Hits).NotTo(BeEmpty())
		var upgradeHits []*pb.SearchHit
		for _, hit := range result.Hits {
			if hit.Path == "app/upgrade.go" {
				upgradeHits = append(upgradeHits, hit)
			}
		}
		Expect(upgradeHits).NotTo(BeEmpty())
		Expect(upgradeHits[0].LineStart).To(BeNumerically("<=", 75))
		Expect(upgradeHits[0].LineEnd).To(BeNumerically(">=", 75))
		Expect(upgradeHits[0].Excerpt).To(ContainSubstring("func runUpgrade()"))
		Expect(upgradeHits[0].Why).To(ContainSubstring("lexical #"))

		// A new revision replaces every chunk of the source. The vector leg
		// always proposes its nearest chunks, so the source may still appear,
		// but only as its one new chunk.
		upgrade.Revision = "two"
		Expect(index.Index(ctx, upgrade, "short now\n")).To(Succeed())
		result, err = index.Search(ctx, &pb.SearchRequest{Query: "runUpgrade", Limit: 10})
		Expect(err).NotTo(HaveOccurred())
		for _, hit := range result.Hits {
			if hit.Path == "app/upgrade.go" {
				Expect(hit.Excerpt).To(Equal("short now\n"))
				Expect(hit.Document.Revision).To(Equal("two"))
			}
		}

		Expect(index.Forget(ctx, other.SourceId)).To(Succeed())
		// Forget refreshes, so the removal is visible to the next search.
		result, err = index.Search(ctx, &pb.SearchRequest{Query: "runUpgrade", Limit: 10})
		Expect(err).NotTo(HaveOccurred())
		for _, hit := range result.Hits {
			Expect(hit.Path).NotTo(Equal("docs/other.md"))
		}
	})
})
