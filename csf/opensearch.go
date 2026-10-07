package csf

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

const (
	searchTextField        = "text"
	searchEmbeddingField   = "embedding"
	searchPathField        = "path"
	searchSourceIDField    = "source_id"
	searchLineStartField   = "line_start"
	searchLineEndField     = "line_end"
	searchRefreshWait      = "wait_for"
	searchRefreshNow       = "true"
	searchModeLexical      = "lexical"
	searchModeSemantic     = "semantic"
	searchModeHybrid       = "hybrid"
	maxSearchResponseBytes = 8 * maxAPIBytes

	// The index's own mapping vocabulary.
	mappingText       = "text"
	mappingKeyword    = "keyword"
	mappingInteger    = "integer"
	mappingKNNVector  = "knn_vector"
	vectorMethod      = "hnsw"
	vectorEngine      = "lucene"
	vectorSpace       = "cosinesimil"
	pathAnalyzer      = "simple"
	indexKNNEnabled   = "true"
	indexExistsReason = "resource_already_exists_exception"

	// A chunk is chunkLines lines, each starting chunkLines-chunkOverlap after
	// the last, so a definition that straddles a boundary is whole in one of
	// them; chunkRunes cuts a chunk of a few very long lines.
	chunkLines   = 40
	chunkOverlap = 10
	chunkRunes   = 4000
	excerptRunes = 1200
	// embedBatch is how many chunks one embedding request carries.
	embedBatch = 32

	// Each retriever proposes searchCandidates chunks; reciprocal rank fusion
	// (fusionRank is its constant) merges them, and the reranker orders the
	// first rerankCandidates of the fused list, each cut to rerankRunes.
	searchCandidates = 50
	rerankCandidates = 30
	rerankRunes      = 2000
	fusionRank       = 60
	pathBoost        = 2
)

var searchIndexName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`)

var _ IOpenSearchClient = openSearchSDKClient{}

// Embedder returns one vector per text, in text order. query marks texts that
// are queries rather than documents, which some models instruct differently.
type Embedder func(ctx context.Context, texts []string, query bool) ([][]float32, error)

// Reranker returns one relevance score per document for query, in document
// order; a higher score ranks first.
type Reranker func(ctx context.Context, query string, documents []string) ([]float64, error)

// OpenSearchOption configures an [OpenSearch] projection.
type OpenSearchOption func(search *OpenSearch) error

// WithEmbedder embeds every chunk at index time and every query at search
// time with embed, whose vectors have dimension values, and adds the nearest
// chunks to the lexical ones. name is recorded in each result.
func WithEmbedder(name string, dimension int, embed Embedder) OpenSearchOption {
	return func(search *OpenSearch) error {
		if name == "" || dimension <= 0 || embed == nil {
			return fmt.Errorf("embedder needs a name, a positive dimension and a function")
		}
		search.embedName, search.dimension, search.embed = name, dimension, embed
		return nil
	}
}

// WithReranker orders the fused candidates by rerank before the limit is
// applied. A reranker that fails leaves the fused order, and says so.
func WithReranker(name string, rerank Reranker) OpenSearchOption {
	return func(search *OpenSearch) error {
		if name == "" || rerank == nil {
			return fmt.Errorf("reranker needs a name and a function")
		}
		search.rerankName, search.rerank = name, rerank
		return nil
	}
}

// OpenSearch projects canonical documents into chunks a search can point
// into. Empty model configuration means lexical search, explicitly labelled
// as such; an ML Commons model or an [Embedder] adds a vector retriever.
type OpenSearch struct {
	client     IOpenSearchClient
	close      func() error
	index      string
	model      string
	embedName  string
	dimension  int
	embed      Embedder
	rerankName string
	rerank     Reranker
	// prepared is set once the index exists with this projection's mapping.
	prepared atomic.Bool
}

// NewOpenSearch composes the document projection with generated SDK contracts.
// The caller owns the supplied clients and their lifecycle.
func NewOpenSearch(index string, model string, client IOpenSearchClient, options ...OpenSearchOption) (*OpenSearch, error) {
	if !searchIndexName.MatchString(index) {
		return nil, fmt.Errorf("invalid index name")
	}
	if client == nil {
		return nil, fmt.Errorf("OpenSearch client required")
	}
	search := &OpenSearch{index: index, model: model, client: client}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("nil OpenSearch option")
		}
		if err := option(search); err != nil {
			return nil, err
		}
	}
	if search.model != "" && search.embed != nil {
		return nil, fmt.Errorf("an ML Commons model and an embedder are two vector retrievers; configure one")
	}
	return search, nil
}

// ConnectOpenSearch owns a configured upstream SDK client until Close.
func ConnectOpenSearch(endpoint string, index string, model string, transport IHTTPDoer, options ...OpenSearchOption) (*OpenSearch, error) {
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
	projection, err := NewOpenSearch(index, model, openSearchSDKClient{client: client}, options...)
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

// prepare creates the index with the chunk mapping the first time this
// projection writes. An ML Commons model's index is the operator's: its
// embedding field and ingest pipeline belong to the model's configuration.
func (search *OpenSearch) prepare(ctx context.Context) error {
	if search.model != "" || search.prepared.Load() {
		return nil
	}
	properties := map[string]opensearchapi.CommonMappingProperty{
		searchTextField:      opensearchapi.NewCommonMappingPropertyFromTextProperty(opensearchapi.CommonMappingTextProperty{Type: mappingText}),
		searchPathField:      opensearchapi.NewCommonMappingPropertyFromTextProperty(opensearchapi.CommonMappingTextProperty{Type: mappingText, Analyzer: new(pathAnalyzer)}),
		searchSourceIDField:  opensearchapi.NewCommonMappingPropertyFromKeywordProperty(opensearchapi.CommonMappingKeywordProperty{Type: mappingKeyword}),
		searchLineStartField: opensearchapi.NewCommonMappingPropertyFromIntegerNumberProperty(opensearchapi.CommonMappingIntegerNumberProperty{Type: mappingInteger}),
		searchLineEndField:   opensearchapi.NewCommonMappingPropertyFromIntegerNumberProperty(opensearchapi.CommonMappingIntegerNumberProperty{Type: mappingInteger}),
	}
	body := &opensearchapi.IndicesCreateBody{Mappings: &opensearchapi.CommonMappingType{Properties: properties}}
	if search.embed != nil {
		properties[searchEmbeddingField] = opensearchapi.NewCommonMappingPropertyFromKNNVectorProperty(opensearchapi.CommonMappingKNNVectorProperty{
			Type: mappingKNNVector, Dimension: search.dimension,
			Method: &opensearchapi.CommonMappingKNNVectorMethod{Name: vectorMethod, Engine: new(vectorEngine), SpaceType: new(vectorSpace)},
		})
		body.Settings = &opensearchapi.IndicesIndexSettings{Index: &opensearchapi.IndicesIndexSettings{KNN: new(indexKNNEnabled)}}
	}
	_, err := search.client.Create(ctx, opensearchapi.IndicesCreateReq{Index: search.index, Body: body})
	var refused *opensearch.StructError
	if err != nil && !(errors.As(err, &refused) && refused.Err.Type == indexExistsReason) {
		return fmt.Errorf("create search index: %w", err)
	}
	search.prepared.Store(true)
	return nil
}

// Index replaces every chunk of document's source with the chunks of text,
// so a source has exactly its latest revision's chunks in the index.
func (search *OpenSearch) Index(ctx context.Context, document *pb.SourceDocument, text string) error {
	if err := search.prepare(ctx); err != nil {
		return err
	}
	if err := search.Forget(ctx, document.SourceId); err != nil {
		return err
	}
	// Only CSF document metadata and its chunk fields are assembled here;
	// OpenSearch request and response envelopes come from the generated SDK.
	encoded, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(document)
	if err != nil {
		return err
	}
	chunks := chunkText(text)
	vectors, err := search.embedChunks(ctx, document.Title, chunks)
	if err != nil {
		return err
	}
	for position, chunk := range chunks {
		var record map[string]any
		if err := json.Unmarshal(encoded, &record); err != nil {
			return err
		}
		record[searchTextField] = chunk.text
		record[searchPathField] = document.Title
		record[searchLineStartField] = chunk.start
		record[searchLineEndField] = chunk.end
		if vectors != nil {
			record[searchEmbeddingField] = vectors[position]
		}
		content, err := json.Marshal(record)
		if err != nil {
			return err
		}
		// Only the last chunk waits for a refresh: the source becomes visible
		// whole rather than chunk by chunk.
		refresh := ""
		if position == len(chunks)-1 {
			refresh = searchRefreshWait
		}
		digest := sha256.Sum256([]byte(document.SourceId + "\x00" + document.Revision + "\x00" + strconv.Itoa(position)))
		if _, err := search.client.Index(ctx, opensearchapi.IndexReq{
			Index: search.index, ID: hex.EncodeToString(digest[:]), Body: bytes.NewReader(content),
			Params: &opensearchapi.IndexParams{Refresh: refresh},
		}); err != nil {
			return err
		}
	}
	return nil
}

// Forget removes every chunk of the source from the index; the canonical
// document stays in the store, which the index is a projection of.
func (search *OpenSearch) Forget(ctx context.Context, sourceID string) error {
	if err := search.prepare(ctx); err != nil {
		return err
	}
	_, err := search.client.DeleteByQuery(ctx, &opensearchapi.DeleteByQueryReq{
		Indices: []string{search.index},
		Body: &opensearchapi.DeleteByQueryBody{Query: &opensearchapi.CommonQueryDSLQueryContainer{
			Term: map[string]opensearchapi.CommonQueryDSLTermQuery{
				searchSourceIDField: opensearchapi.NewCommonQueryDSLTermQueryFromFieldValue(opensearchapi.NewFieldValueFromString(sourceID)),
			},
		}},
		Params: &opensearchapi.DeleteByQueryParams{Conflicts: opensearchapi.ConflictsProceed, Refresh: searchRefreshNow},
	})
	return err
}

// textChunk is lines start through end, 1-based and inclusive.
type textChunk struct {
	start, end int
	text       string
}

// chunkText cuts text into overlapping runs of lines. An empty text is one
// empty chunk, so every document is findable by its path.
func chunkText(text string) []textChunk {
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []textChunk{{start: 1, end: 1}}
	}
	var chunks []textChunk
	for start := 0; ; start += chunkLines - chunkOverlap {
		end := min(start+chunkLines, len(lines))
		body := []rune(strings.Join(lines[start:end], ""))
		if len(body) > chunkRunes {
			body = body[:chunkRunes]
		}
		chunks = append(chunks, textChunk{start: start + 1, end: end, text: string(body)})
		if end == len(lines) {
			return chunks
		}
	}
}

// embedChunks embeds each chunk with its path above it, in batches; nil when
// no embedder is configured.
func (search *OpenSearch) embedChunks(ctx context.Context, path string, chunks []textChunk) ([][]float32, error) {
	if search.embed == nil {
		return nil, nil
	}
	vectors := make([][]float32, 0, len(chunks))
	for batch := range slices.Chunk(chunks, embedBatch) {
		texts := make([]string, len(batch))
		for position, chunk := range batch {
			texts[position] = path + "\n" + chunk.text
		}
		embedded, err := search.embed(ctx, texts, false)
		if err != nil {
			return nil, fmt.Errorf("embed chunks: %w", err)
		}
		if len(embedded) != len(texts) {
			return nil, fmt.Errorf("embedder returned %d vectors for %d chunks", len(embedded), len(texts))
		}
		vectors = append(vectors, embedded...)
	}
	return vectors, nil
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

// leg runs one retriever's query for its candidates, without their vectors.
func (search *OpenSearch) leg(ctx context.Context, query *opensearchapi.CommonQueryDSLQueryContainer) ([]opensearchapi.SearchHit, error) {
	response, err := search.query(ctx, search.index, query, searchCandidates, withSearchExcludedField(searchEmbeddingField))
	if err != nil {
		return nil, err
	}
	return response.Hits.Hits, nil
}

// Search returns the request's hits in the reranker's order when one is
// configured, in the retrievers' fused order otherwise.
func (search *OpenSearch) Search(ctx context.Context, request *pb.SearchRequest) (*pb.SearchResult, error) {
	fused, reranked, err := search.Compare(ctx, request)
	if err != nil {
		return nil, err
	}
	if reranked != nil {
		return reranked, nil
	}
	return fused, nil
}

// Compare answers request twice from one retrieval: in the retrievers' fused
// order, and in the reranker's order. The reranked result is nil when no
// reranker is configured or it failed; the fused hits then say why.
func (search *OpenSearch) Compare(ctx context.Context, request *pb.SearchRequest) (*pb.SearchResult, *pb.SearchResult, error) {
	if request == nil {
		return nil, nil, fmt.Errorf("search request required")
	}
	if err := pb.ValidateSearchRequest(request); err != nil {
		return nil, nil, err
	}
	candidates, legs, err := search.retrieve(ctx, request.Query)
	if err != nil {
		return nil, nil, err
	}
	mode := searchModeLexical
	if legs > 1 {
		mode = searchModeHybrid
	}
	if search.model != "" {
		mode = searchModeSemantic
	}
	fused := &pb.SearchResult{Mode: mode, EmbeddingModel: cmp.Or(search.model, search.embedName)}
	if search.rerank == nil {
		fused.Hits = projectHits(candidates, int(request.Limit), "")
		return fused, nil, nil
	}
	reranked, rerankErr := search.rerankCandidates(ctx, request.Query, candidates)
	if rerankErr != nil {
		fused.Hits = projectHits(candidates, int(request.Limit), "reranker unavailable: "+rerankErr.Error())
		return fused, nil, nil
	}
	fused.Hits = projectHits(candidates, int(request.Limit), "")
	result := &pb.SearchResult{Mode: mode, EmbeddingModel: fused.EmbeddingModel, Reranker: search.rerankName,
		Hits: projectHits(reranked, int(request.Limit), "")}
	return fused, result, nil
}

// candidate is one chunk a retriever proposed: its 1-based rank in each leg
// (zero where that leg did not propose it) and the stage scores so far.
type candidate struct {
	hit      opensearchapi.SearchHit
	lexical  int
	vector   int
	score    float64
	reranked bool
}

// retrieve runs the lexical leg and, when configured, the vector leg, and
// fuses their ranks. legs is how many retrievers ran.
func (search *OpenSearch) retrieve(ctx context.Context, query string) ([]*candidate, int, error) {
	vectorQuery, err := search.vectorQuery(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	if search.model != "" {
		// The ML Commons path keeps its single neural retriever.
		semantic, err := search.leg(ctx, vectorQuery)
		if err != nil {
			return nil, 0, err
		}
		return fuse(nil, semantic), 1, nil
	}
	lexical, err := search.leg(ctx, lexicalQuery(query))
	if err != nil {
		return nil, 0, err
	}
	if vectorQuery == nil {
		return fuse(lexical, nil), 1, nil
	}
	vector, err := search.leg(ctx, vectorQuery)
	if err != nil {
		return nil, 0, err
	}
	return fuse(lexical, vector), 2, nil
}

// lexicalQuery matches the chunk's text and, weighted, its path.
func lexicalQuery(query string) *opensearchapi.CommonQueryDSLQueryContainer {
	match := func(field string, boost float32) opensearchapi.CommonQueryDSLQueryContainer {
		value := opensearchapi.NewFieldValueFromString(query)
		return opensearchapi.CommonQueryDSLQueryContainer{Match: map[string]opensearchapi.CommonQueryDSLMatchQuery{
			field: opensearchapi.NewCommonQueryDSLMatchQueryFromQuery(opensearchapi.CommonQueryDSLMatchQueryQuery{
				CommonQueryDSLQueryBase: opensearchapi.CommonQueryDSLQueryBase{Boost: &boost}, Query: &value,
			}),
		}}
	}
	should := opensearchapi.NewCommonQueryDSLBoolQueryShouldFromArray([]opensearchapi.CommonQueryDSLQueryContainer{
		match(searchTextField, 1), match(searchPathField, pathBoost),
	})
	return &opensearchapi.CommonQueryDSLQueryContainer{Bool: &opensearchapi.CommonQueryDSLBoolQuery{Should: &should}}
}

// vectorQuery is the configured vector retriever's query, nil when none is.
func (search *OpenSearch) vectorQuery(ctx context.Context, query string) (*opensearchapi.CommonQueryDSLQueryContainer, error) {
	switch {
	case search.model != "":
		return &opensearchapi.CommonQueryDSLQueryContainer{Neural: map[string]opensearchapi.CommonQueryDSLNeuralQuery{
			searchEmbeddingField: {QueryText: &query, ModelID: &search.model, K: new(searchCandidates)},
		}}, nil
	case search.embed != nil:
		vectors, err := search.embed(ctx, []string{query}, true)
		if err != nil {
			return nil, fmt.Errorf("embed query: %w", err)
		}
		if len(vectors) != 1 {
			return nil, fmt.Errorf("embedder returned %d vectors for one query", len(vectors))
		}
		return &opensearchapi.CommonQueryDSLQueryContainer{KNN: map[string]opensearchapi.CommonQueryDSLKNNQuery{
			searchEmbeddingField: {Vector: vectors[0], K: new(searchCandidates)},
		}}, nil
	default:
		return nil, nil
	}
}

// fuse merges two ranked lists by reciprocal rank fusion. A single list keeps
// its own scores, so a one-retriever result reads as that retriever's.
func fuse(lexical []opensearchapi.SearchHit, vector []opensearchapi.SearchHit) []*candidate {
	byID := map[string]*candidate{}
	var ordered []*candidate
	add := func(hits []opensearchapi.SearchHit, leg func(found *candidate, rank int)) {
		for position, hit := range hits {
			id := ""
			if hit.ID != nil {
				id = *hit.ID
			}
			found, seen := byID[id]
			if !seen || id == "" {
				found = &candidate{hit: hit}
				byID[id] = found
				ordered = append(ordered, found)
			}
			leg(found, position+1)
			found.score += 1 / float64(fusionRank+position+1)
		}
	}
	add(lexical, func(found *candidate, rank int) { found.lexical = rank })
	add(vector, func(found *candidate, rank int) { found.vector = rank })
	if len(lexical) == 0 || len(vector) == 0 {
		for _, found := range ordered {
			found.score = 0
			if found.hit.Score != nil {
				found.score = *found.hit.Score
			}
		}
		return ordered
	}
	slices.SortStableFunc(ordered, func(left, right *candidate) int { return cmp.Compare(right.score, left.score) })
	return ordered
}

// rerankCandidates returns the candidates with the first rerankCandidates in
// the reranker's order and the rest after them, in fused order.
func (search *OpenSearch) rerankCandidates(ctx context.Context, query string, candidates []*candidate) ([]*candidate, error) {
	head := candidates[:min(len(candidates), rerankCandidates)]
	documents := make([]string, len(head))
	for position, found := range head {
		fields, err := hitFields(found.hit)
		if err != nil {
			return nil, err
		}
		body := []rune(fields.path + "\n" + fields.text)
		documents[position] = string(body[:min(len(body), rerankRunes)])
	}
	scores, err := search.rerank(ctx, query, documents)
	if err != nil {
		return nil, err
	}
	if len(scores) != len(head) {
		return nil, fmt.Errorf("reranker returned %d scores for %d candidates", len(scores), len(head))
	}
	reranked := make([]*candidate, 0, len(candidates))
	for position, found := range head {
		copied := *found
		copied.score, copied.reranked = scores[position], true
		reranked = append(reranked, &copied)
	}
	slices.SortStableFunc(reranked, func(left, right *candidate) int { return cmp.Compare(right.score, left.score) })
	return append(reranked, candidates[len(head):]...), nil
}

// projectHits is the first limit candidates as hits; note, when set, is added
// to every hit's why.
func projectHits(candidates []*candidate, limit int, note string) []*pb.SearchHit {
	hits := make([]*pb.SearchHit, 0, min(limit, len(candidates)))
	for _, found := range candidates[:min(limit, len(candidates))] {
		hit, err := knowledgeSearchHit(found, note)
		if err != nil {
			// A chunk whose fields do not decode is a projection defect; it
			// is reported as a hit that says so rather than dropped silently.
			hit = &pb.SearchHit{Why: "undecodable chunk: " + err.Error()}
		}
		hits = append(hits, hit)
	}
	return hits
}

// chunkFields is a chunk's own fields beside the document's metadata.
type chunkFields struct {
	text       string
	path       string
	start, end uint32
	document   *pb.SourceDocument
}

func hitFields(hit opensearchapi.SearchHit) (chunkFields, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(hit.Source, &fields); err != nil {
		return chunkFields{}, err
	}
	var decoded chunkFields
	if err := json.Unmarshal(fields[searchTextField], &decoded.text); err != nil {
		return chunkFields{}, err
	}
	// Chunks written before line ranges existed carry no path or lines.
	for field, target := range map[string]any{searchPathField: &decoded.path, searchLineStartField: &decoded.start, searchLineEndField: &decoded.end} {
		if raw, found := fields[field]; found {
			if err := json.Unmarshal(raw, target); err != nil {
				return chunkFields{}, err
			}
		}
	}
	for _, field := range []string{searchTextField, searchEmbeddingField, searchPathField, searchLineStartField, searchLineEndField} {
		delete(fields, field)
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return chunkFields{}, err
	}
	decoded.document = &pb.SourceDocument{}
	if err := protojson.Unmarshal(encoded, decoded.document); err != nil {
		return chunkFields{}, err
	}
	return decoded, nil
}

func knowledgeSearchHit(found *candidate, note string) (*pb.SearchHit, error) {
	fields, err := hitFields(found.hit)
	if err != nil {
		return nil, err
	}
	excerpt := []rune(fields.text)
	if len(excerpt) > excerptRunes {
		excerpt = excerpt[:excerptRunes]
	}
	return &pb.SearchHit{Document: fields.document, Excerpt: string(excerpt), Score: found.score,
		Path: cmp.Or(fields.path, fields.document.Title), LineStart: fields.start, LineEnd: fields.end, Why: why(found, note)}, nil
}

// why names the retrievers that proposed the chunk, at what rank, and the
// reranker's score.
func why(found *candidate, note string) string {
	var reasons []string
	if found.lexical > 0 {
		reasons = append(reasons, fmt.Sprintf("lexical #%d", found.lexical))
	}
	if found.vector > 0 {
		reasons = append(reasons, fmt.Sprintf("vector #%d", found.vector))
	}
	if found.reranked {
		reasons = append(reasons, fmt.Sprintf("reranked %.2f", found.score))
	}
	if note != "" {
		reasons = append(reasons, note)
	}
	return strings.Join(reasons, "; ")
}

// IndexSimulationSource uses the same generated SDK client as knowledge search.
// Source records retain original event/trajectory text, not only rendered spans.
func (search *OpenSearch) IndexSimulationSource(ctx context.Context, index string, source *pb.SimulationTraceSource) (string, error) {
	if !searchIndexName.MatchString(index) || source == nil || source.Run == nil || !simulationID.MatchString(source.Run.RunId) {
		return "", fmt.Errorf("invalid simulation log source or index")
	}
	digest, err := simulationSourceHash(source)
	if err != nil {
		return "", err
	}
	record := &pb.SimulationLogRecord{RecordedAt: source.Run.UpdatedAt, RunId: source.Run.RunId, Kind: simulationLogKind, Message: source.Logs, Simulation: source, SourceSha256: digest}
	content, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(record)
	if err != nil {
		return "", err
	}
	if len(content) > maxSearchResponseBytes/2 {
		return "", fmt.Errorf("simulation log source exceeds archive limit")
	}
	identity := simulationLogID(source.Run.RunId)
	_, err = search.client.Index(ctx, opensearchapi.IndexReq{Index: index, ID: identity, Body: bytes.NewReader(content), Params: &opensearchapi.IndexParams{Refresh: searchRefreshWait}})
	return identity, err
}

func (search *OpenSearch) SimulationSource(ctx context.Context, index string, runID string) (*pb.SimulationLogRecord, error) {
	if !searchIndexName.MatchString(index) || !simulationID.MatchString(runID) {
		return nil, fmt.Errorf("invalid simulation log identity or index")
	}
	response, err := search.query(ctx, index, &opensearchapi.CommonQueryDSLQueryContainer{Term: map[string]opensearchapi.CommonQueryDSLTermQuery{
		simulationDocumentIDField: opensearchapi.NewCommonQueryDSLTermQueryFromFieldValue(opensearchapi.NewFieldValueFromString(simulationLogID(runID))),
	}}, 1)
	if err != nil {
		return nil, err
	}
	if response == nil || len(response.Hits.Hits) != 1 {
		return nil, fmt.Errorf("simulation source not found in OpenSearch")
	}
	record := &pb.SimulationLogRecord{}
	if err := protojson.Unmarshal(response.Hits.Hits[0].Source, record); err != nil {
		return nil, err
	}
	if record.Kind != simulationLogKind || record.RunId != runID || record.GetSimulation().GetRun().GetRunId() != runID {
		return nil, fmt.Errorf("simulation source identity mismatch")
	}
	digest, err := simulationSourceHash(record.Simulation)
	if err != nil {
		return nil, err
	}
	if digest != record.SourceSha256 || record.Message != record.Simulation.Logs {
		return nil, fmt.Errorf("simulation source hash mismatch")
	}
	return record, nil
}
