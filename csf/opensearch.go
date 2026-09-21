package csf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

const (
	searchTextField        = "text"
	searchEmbeddingField   = "embedding"
	searchRefreshWait      = "wait_for"
	searchModeLexical      = "lexical"
	searchModeSemantic     = "semantic"
	maxSearchResponseBytes = 8 * maxAPIBytes
)

var searchIndexName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`)

var _ IOpenSearchClient = openSearchSDKClient{}

// OpenSearch projects canonical documents using a configured ML Commons model.
// Empty model configuration means lexical search, explicitly labelled as such.
type OpenSearch struct {
	client IOpenSearchClient
	close  func() error
	index  string
	model  string
}

// NewOpenSearch composes the document projection with generated SDK contracts.
// The caller owns the supplied clients and their lifecycle.
func NewOpenSearch(index string, model string, client IOpenSearchClient) (*OpenSearch, error) {
	if !searchIndexName.MatchString(index) {
		return nil, fmt.Errorf("invalid index name")
	}
	if client == nil {
		return nil, fmt.Errorf("OpenSearch client required")
	}
	return &OpenSearch{index: index, model: model, client: client}, nil
}

// ConnectOpenSearch owns a configured upstream SDK client until Close.
func ConnectOpenSearch(endpoint string, index string, model string, transport IHTTPDoer) (*OpenSearch, error) {
	validated, err := NewClient(endpoint, transport)
	if err != nil {
		return nil, err
	}
	if !searchIndexName.MatchString(index) {
		return nil, fmt.Errorf("invalid index name")
	}
	client, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{
		Addresses:             []string{validated.endpoint},
		Transport:             openSearchTransport{http: transport},
		DisableRetry:          true,
		DiscoverNodesOnStart:  new(false),
		DiscoverNodesInterval: -1,
		MaxRetryClusterHealth: -1,
		Router:                opensearchtransport.NewRoundRobinRouter(),
	}})
	if err != nil {
		return nil, fmt.Errorf("create OpenSearch SDK client: %w", err)
	}
	projection, err := NewOpenSearch(index, model, openSearchSDKClient{client: client})
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	projection.close = client.Close
	return projection, nil
}

// Close releases SDK background resources without closing the caller's HTTP client.
func (search *OpenSearch) Close() error {
	if search.close != nil {
		return search.close()
	}
	return nil
}

// The caller owns authentication, deadlines and the HTTP client. This adapter
// enforces our response budget before the SDK buffers and decodes the body;
// generated SDK requests own all OpenSearch paths, parameters and status errors.
type openSearchTransport struct{ http IHTTPDoer }

func (transport openSearchTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.http.Do(request)
	if err != nil {
		return nil, err
	}
	body := response.Body
	defer func() { _ = body.Close() }()
	content, err := io.ReadAll(io.LimitReader(body, maxSearchResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxSearchResponseBytes {
		return nil, fmt.Errorf("search response exceeds limit")
	}
	response.Body = io.NopCloser(bytes.NewReader(content))
	return response, nil
}

func (search *OpenSearch) Index(ctx context.Context, document *pb.SourceDocument, text string) error {
	// Only CSF document metadata and its text field are assembled here;
	// OpenSearch request and response envelopes come from the generated SDK.
	encoded, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(document)
	if err != nil {
		return err
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &record); err != nil {
		return err
	}
	record[searchTextField], err = json.Marshal(text)
	if err != nil {
		return err
	}
	encoded, err = json.Marshal(record)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(document.SourceId + "\x00" + document.Revision))
	_, err = search.client.Index(ctx, opensearchapi.IndexReq{
		Index: search.index, ID: hex.EncodeToString(digest[:]), Body: bytes.NewReader(encoded),
		Params: &opensearchapi.IndexParams{Refresh: searchRefreshWait},
	})
	return err
}

type searchQueryOption func(body *opensearchapi.SearchBody)

func withSearchExcludedField(field string) searchQueryOption {
	return func(body *opensearchapi.SearchBody) {
		source := opensearchapi.NewSearchSourceConfigFromFilter(
			opensearchapi.NewSearchSourceFilterFromExcludesIncludes(opensearchapi.SearchSourceFilterExcludesIncludes{Excludes: &field}),
		)
		body.Source = &source
	}
}

func (search *OpenSearch) query(ctx context.Context, index string, query *opensearchapi.CommonQueryDSLQueryContainer, limit int, options ...searchQueryOption) (*opensearchapi.SearchResp, error) {
	body := &opensearchapi.SearchBody{Size: &limit, Query: query}
	for _, option := range options {
		option(body)
	}
	return search.client.Search(ctx, &opensearchapi.SearchReq{Indices: []string{index}, Body: body})
}

func (search *OpenSearch) Search(ctx context.Context, request *pb.SearchRequest) (*pb.SearchResult, error) {
	if request == nil {
		return nil, fmt.Errorf("search request required")
	}
	if err := pb.ValidateSearchRequest(request); err != nil {
		return nil, err
	}
	query, mode := knowledgeSearchQuery(request, search.model)
	response, err := search.query(ctx, search.index, &query, int(request.Limit), withSearchExcludedField(searchEmbeddingField))
	if err != nil {
		return nil, err
	}
	result := &pb.SearchResult{Mode: mode, EmbeddingModel: search.model}
	for _, hit := range response.Hits.Hits {
		projected, err := knowledgeSearchHit(hit)
		if err != nil {
			return nil, err
		}
		result.Hits = append(result.Hits, projected)
	}
	return result, nil
}

func knowledgeSearchQuery(request *pb.SearchRequest, model string) (opensearchapi.CommonQueryDSLQueryContainer, string) {
	query := opensearchapi.CommonQueryDSLQueryContainer{
		Match: map[string]opensearchapi.CommonQueryDSLMatchQuery{
			searchTextField: opensearchapi.NewCommonQueryDSLMatchQueryFromFieldValue(opensearchapi.NewFieldValueFromString(request.Query)),
		},
	}
	mode := searchModeLexical
	if model != "" {
		mode = searchModeSemantic
		query = opensearchapi.CommonQueryDSLQueryContainer{
			Neural: map[string]opensearchapi.CommonQueryDSLNeuralQuery{
				searchEmbeddingField: {QueryText: &request.Query, ModelID: &model, K: new(int(request.Limit))},
			},
		}
	}
	return query, mode
}

func knowledgeSearchHit(hit opensearchapi.SearchHit) (*pb.SearchHit, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(hit.Source, &fields); err != nil {
		return nil, err
	}
	var text string
	if err := json.Unmarshal(fields[searchTextField], &text); err != nil {
		return nil, err
	}
	delete(fields, searchTextField)
	delete(fields, searchEmbeddingField)
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	document := &pb.SourceDocument{}
	if err := protojson.Unmarshal(encoded, document); err != nil {
		return nil, err
	}
	excerpt := []rune(text)
	if len(excerpt) > 1200 {
		excerpt = excerpt[:1200]
	}
	score := float64(0)
	if hit.Score != nil {
		score = *hit.Score
	}
	return &pb.SearchHit{Document: document, Excerpt: string(excerpt), Score: score}, nil
}
