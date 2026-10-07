// Copyright 2026 Candace Labs

// Package llamacpp reaches a llama.cpp server started with --reranking: a
// cross-encoder that scores how well each document answers one query. It is
// the reranker behind CSF's search, reached over the server's HTTP API through
// the http capability the constructor receives.
package llamacpp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"strings"

	iohttp "github.com/candacelabs/csf/io/net/http"
)

const (
	rerankPath = "/v1/rerank"

	headerContentType = "Content-Type"
	contentTypeJSON   = "application/json"

	// maxAnswerBytes bounds one response body read.
	maxAnswerBytes = 1 << 20
)

var (
	// ErrNoClient reports a reranker constructed without the http capability.
	ErrNoClient = errors.New("llama.cpp reranker: an HTTP client capability is required")
	// ErrNoEndpoint reports a reranker constructed without the server's URL.
	ErrNoEndpoint = errors.New("llama.cpp reranker: an endpoint is required")
	// ErrScoreCount reports a server whose scores do not cover its documents.
	ErrScoreCount = errors.New("llama.cpp reranker: the server's scores do not cover its documents")
)

// LlamaReranker scores documents against a query on one llama.cpp server. It
// holds no state between calls, so one value is safe to share.
type LlamaReranker struct {
	client   iohttp.IHTTPClient
	endpoint string
}

// NewLlamaReranker reaches the server at endpoint, a URL with scheme and host.
// The caller owns the client's lifetime.
func NewLlamaReranker(client iohttp.IHTTPClient, endpoint string) (*LlamaReranker, error) {
	if client == nil {
		return nil, ErrNoClient
	}
	trimmed := strings.TrimRight(endpoint, "/")
	if trimmed == "" {
		return nil, ErrNoEndpoint
	}
	return &LlamaReranker{client: client, endpoint: trimmed}, nil
}

// Rerank returns one relevance score per document, in document order; a
// higher score is a better answer to query. The scale is the model's logit,
// comparable only within one call.
func (reranker *LlamaReranker) Rerank(ctx context.Context, query string, documents []string) ([]float64, error) {
	if len(documents) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(rerankRequest{Query: query, Documents: documents})
	if err != nil {
		return nil, fmt.Errorf("llama.cpp reranker: encode request: %w", err)
	}
	request, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, reranker.endpoint+rerankPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llama.cpp reranker: build request: %w", err)
	}
	request.Header.Set(headerContentType, contentTypeJSON)
	response, err := reranker.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("llama.cpp reranker: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	content, err := io.ReadAll(io.LimitReader(response.Body, maxAnswerBytes))
	if err != nil {
		return nil, fmt.Errorf("llama.cpp reranker: read answer: %w", err)
	}
	if response.StatusCode != stdhttp.StatusOK {
		return nil, fmt.Errorf("llama.cpp reranker: HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(content)))
	}
	var answer rerankResponse
	if err := json.Unmarshal(content, &answer); err != nil {
		return nil, fmt.Errorf("llama.cpp reranker: decode answer: %w", err)
	}
	scores := make([]float64, len(documents))
	seen := make([]bool, len(documents))
	for _, result := range answer.Results {
		if result.Index < 0 || result.Index >= len(documents) || seen[result.Index] {
			return nil, fmt.Errorf("%w: index %d of %d", ErrScoreCount, result.Index, len(documents))
		}
		scores[result.Index], seen[result.Index] = result.RelevanceScore, true
	}
	if len(answer.Results) != len(documents) {
		return nil, fmt.Errorf("%w: %d scores for %d documents", ErrScoreCount, len(answer.Results), len(documents))
	}
	return scores, nil
}

// The rerank request and answer as the server reads and writes them.
type rerankRequest struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type rerankResponse struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
}
