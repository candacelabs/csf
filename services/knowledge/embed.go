// Copyright 2026 Candace Labs

package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/io/net/model/ollama"
)

// The embedding model: qwen3-embedding:0.6b on the LABELER's Ollama server,
// 1024 dimensions (measured 2026-10-05), and the instruction its queries
// carry, as the model's card asks of retrieval queries.
const (
	EmbeddingModel     = "qwen3-embedding:0.6b"
	EmbeddingDimension = 1024
	queryInstruction   = "Instruct: Given a question about the CSF repository, its tickets or its agent runs, retrieve the passages that answer it\nQuery: "
	// embeddingStay keeps the model loaded between the documents of one
	// refresh and the queries that follow it, then frees the GPU.
	embeddingStay = 10 * time.Minute
)

// ErrGPUBusy reports a document embedding refused because another model holds
// the GPU: indexing yields, and the projection retries it later.
var ErrGPUBusy = errors.New("knowledge: another model holds the GPU; indexing yields")

// IEmbeddingModel is the part of the Ollama brain an embedder uses.
type IEmbeddingModel interface {
	Model() string
	Embed(ctx context.Context, inputs []string, stay time.Duration) ([][]float32, error)
	Loaded(ctx context.Context) ([]ollama.LoadedModel, error)
}

var _ IEmbeddingModel = (*ollama.OllamaBrain)(nil)

// NewOllamaEmbedder embeds through model. Queries carry the model's retrieval
// instruction and always run, so a search answers; documents yield the GPU
// while the server holds another model, which is the labeler at work. A
// server that cannot say what it holds does not stop indexing.
func NewOllamaEmbedder(model IEmbeddingModel) csf.Embedder {
	return func(ctx context.Context, texts []string, query bool) ([][]float32, error) {
		if query {
			instructed := make([]string, len(texts))
			for position, text := range texts {
				instructed[position] = queryInstruction + text
			}
			return model.Embed(ctx, instructed, embeddingStay)
		}
		if loaded, err := model.Loaded(ctx); err == nil {
			var others []string
			for _, held := range loaded {
				if held.Name != model.Model() {
					others = append(others, held.Name)
				}
			}
			if len(others) > 0 {
				return nil, fmt.Errorf("%w: %s", ErrGPUBusy, strings.Join(others, ", "))
			}
		}
		return model.Embed(ctx, texts, embeddingStay)
	}
}
