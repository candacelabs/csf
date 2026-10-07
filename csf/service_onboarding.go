package csf

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/services/copilot-adapter/copilotbridge"
)

// OnboardingConfig is supplied by the host. ConsumerRoot is an explicitly
// authorized checkout; callers select relative paths within it. Empty CSFRoot
// uses the documentation embedded in this exact build. Omitted revisions use
// each document's content hash, so uncommitted edits retain distinct identities.
type OnboardingConfig struct {
	CSFRoot          string
	CSFRevision      string
	ConsumerRoot     string
	ConsumerRevision string
	// CopilotHistoryReader is opt-in and reads from the bridge's disposable
	// native snapshot for explicitly selected session IDs.
	CopilotHistoryReader CopilotHistoryReader
}

// CopilotHistoryReader reads one explicitly selected native Copilot session.
// The bridge owns SDK lifecycle, source isolation and typed event decoding.
type CopilotHistoryReader func(ctx context.Context, sessionID string) (copilotbridge.HistorySnapshot, error)

const maxCopilotSessionIDs = 16

//go:embed README.md EXTENDING.md docs/standalone_onboarding.md docs/generated/ontology_cgen.md
var onboardingDocuments embed.FS

const onboardingGuidance = `# CSF onboarding

CSF is a composable Go capability. The host owns one process, its listener,
configuration, authentication and cancellation; this service opens no listener
and does not execute consumer code. The first call needs no file selection.
Configure an authorized consumer checkout, then select its relative text paths
in batches of up to 64. CSF queues its embedded guidance and those
selected consumer files through its configured knowledge store, artifact store
and search index.

Consumer tools extend the same MCP server with compiled Go using
csf.WithMCPTool[Input, Output](mcp.Tool{...}, handler). The SDK derives
and validates schemas. Tool names must be unique, and tools/list is the
authoritative catalog. A consumer host should use the Bazel @csf target and
the pinned module contract, then pass optional capabilities through functional
options.

The response includes source path, revision, content hashes and artifact
receipts. Queued documents are not yet searchable: the existing projection
workers own indexing. Use GetDocument to inspect progress, then repeat this
idempotent call or Search to retrieve indexed sources. A missing
knowledge capability is explicit and is never reported as success. The source
selection skips .git, environment files, secrets, databases and private key
files. GitHub Copilot CLI history is an optional, separate read-only source
owned by the CopilotBridge SDK integration. When its history source is configured,
the canonical binary adds ListCopilotHistorySessions to list metadata-only IDs; pass
selected IDs to this operation to queue typed snapshots with native metadata and
event provenance. This operation never opens the live history root.`

func (service *Service) LearnAboutCSF(ctx context.Context, request *pb.LearnAboutCSFRequest) (*pb.LearnAboutCSFResponse, error) {
	response := &pb.LearnAboutCSFResponse{GuidanceMarkdown: onboardingGuidance}
	if request == nil {
		response.KnowledgeError = "invalid request"
		return response, fmt.Errorf("%w: onboarding request required", ErrInvalidRequest)
	}
	if err := pb.ValidateLearnAboutCSFRequest(request); err != nil {
		response.KnowledgeError = err.Error()
		return response, err
	}
	if service.store == nil || service.index == nil || service.artifacts == nil {
		response.KnowledgeError = "knowledge capability unavailable"
		return response, nil
	}
	if len(request.ConsumerPaths) > 64 {
		response.KnowledgeError = "at most 64 explicit consumer paths are allowed"
		return response, fmt.Errorf("%w: at most 64 consumer paths are allowed", ErrInvalidRequest)
	}
	if len(request.CopilotSessionIds) > maxCopilotSessionIDs {
		response.KnowledgeError = "at most 16 explicit Copilot session IDs are allowed"
		return response, fmt.Errorf("%w: at most 16 Copilot session IDs are allowed", ErrInvalidRequest)
	}
	config := OnboardingConfig{}
	if service.onboarding != nil {
		config = *service.onboarding
	}
	if len(request.ConsumerPaths) > 0 && config.ConsumerRoot == "" {
		return response, fmt.Errorf("%w: configure an authorized consumer root before selecting files", ErrInvalidRequest)
	}
	paths, err := onboardingDocumentPaths()
	if err != nil {
		return response, err
	}
	response.KnowledgeAvailable = true
	seen := make(map[string]struct{}, len(paths)+len(request.ConsumerPaths))
	for _, relative := range paths {
		result, err := service.ingestOnboardingFile(ctx, config.CSFRoot, relative, config.CSFRevision, "csf/onboarding/", "Apache-2.0")
		if err != nil {
			response.SkippedPaths = append(response.SkippedPaths, relative)
			response.KnowledgeError = fmt.Sprintf("%s: %v", relative, err)
			continue
		}
		seen[result.Document.SourceId] = struct{}{}
		response.Ingested = append(response.Ingested, result)
	}
	for _, relative := range request.ConsumerPaths {
		clean := filepath.ToSlash(filepath.Clean(relative))
		if !onboardingConsumerPathAllowed(clean) {
			response.SkippedPaths = append(response.SkippedPaths, relative)
			continue
		}
		sourceID := "consumer/onboarding/" + onboardingNamespace(config.ConsumerRoot) + "/" + clean
		if _, exists := seen[sourceID]; exists {
			response.SkippedPaths = append(response.SkippedPaths, relative)
			continue
		}
		// The consumer's licensing is independent of CSF's own source license.
		result, err := service.ingestOnboardingFile(ctx, config.ConsumerRoot, clean, config.ConsumerRevision, "consumer/onboarding/"+onboardingNamespace(config.ConsumerRoot)+"/", "NOASSERTION")
		if err != nil {
			response.SkippedPaths = append(response.SkippedPaths, relative)
			response.KnowledgeError = fmt.Sprintf("%s: %v", relative, err)
			continue
		}
		seen[sourceID] = struct{}{}
		response.Ingested = append(response.Ingested, result)
	}
	for _, sessionID := range request.CopilotSessionIds {
		result, historyErr := service.ingestCopilotSession(ctx, config.CopilotHistoryReader, sessionID)
		if historyErr != nil {
			result.Error = historyErr.Error()
			response.CopilotHistory = append(response.CopilotHistory, result)
			response.SkippedPaths = append(response.SkippedPaths, "copilot://session/"+url.PathEscape(sessionID))
			response.KnowledgeError = result.Error
			continue
		}
		response.CopilotHistory = append(response.CopilotHistory, result)
	}
	indexed := make(map[string]string)
	for _, result := range response.Ingested {
		if result.Indexed {
			indexed[result.Document.SourceId] = result.Document.Revision
		}
	}
	for _, result := range response.CopilotHistory {
		if result.Ingest != nil && result.Ingest.Indexed {
			indexed[result.Ingest.Document.SourceId] = result.Ingest.Document.Revision
		}
	}
	if len(indexed) == 0 {
		return response, nil
	}
	query := request.RetrievalQuery
	if query == "" {
		query = "CSF MCP consumer extension onboarding"
	}
	retrieval, err := service.Search(ctx, &pb.SearchRequest{Query: query, Limit: 10})
	if err != nil {
		response.KnowledgeError = err.Error()
		return response, nil
	}
	if retrieval.Result != nil {
		response.Retrieval = &pb.SearchResult{Mode: retrieval.Result.Mode, EmbeddingModel: retrieval.Result.EmbeddingModel}
		for _, hit := range retrieval.Result.Hits {
			if revision, exists := indexed[hit.GetDocument().GetSourceId()]; exists && revision == hit.GetDocument().GetRevision() {
				response.Retrieval.Hits = append(response.Retrieval.Hits, hit)
			}
		}
	}
	return response, nil
}

func (service *Service) ingestCopilotSession(ctx context.Context, reader CopilotHistoryReader, sessionID string) (*pb.CopilotHistoryResult, error) {
	result := &pb.CopilotHistoryResult{SessionId: sessionID}
	if sessionID == "" {
		return result, fmt.Errorf("Copilot session ID is required")
	}
	if reader == nil {
		return result, fmt.Errorf("Copilot history capability unavailable")
	}
	snapshot, err := reader(ctx, sessionID)
	if err != nil {
		return result, err
	}
	if snapshot.Metadata.SessionID != sessionID {
		return result, fmt.Errorf("Copilot history returned session %q for requested session %q", snapshot.Metadata.SessionID, sessionID)
	}
	receipt, err := service.ingestCopilotHistory(ctx, snapshot)
	if err != nil {
		return result, err
	}
	result.EventCount = uint64(len(snapshot.Events))
	result.Ingest = receipt
	return result, nil
}

func (service *Service) ingestCopilotHistory(ctx context.Context, snapshot copilotbridge.HistorySnapshot) (*pb.IngestDocumentResult, error) {
	content, err := copilotHistoryDocument(snapshot)
	if err != nil {
		return nil, err
	}
	if len(content) == 0 || len(content) > maxAPIBytes || !utf8.Valid(content) {
		return nil, fmt.Errorf("Copilot history is not bounded UTF-8")
	}
	contentDigest := sha256.Sum256(content)
	sessionID := snapshot.Metadata.SessionID
	escapedID := url.PathEscape(sessionID)
	document := &pb.SourceDocument{
		SourceId:             "copilot/history/" + escapedID,
		Revision:             hex.EncodeToString(contentDigest[:]),
		ContentHash:          hex.EncodeToString(contentDigest[:]),
		RawSourceContentHash: hex.EncodeToString(contentDigest[:]),
		SourceUri:            "copilot://session/" + escapedID,
		Title:                "Copilot CLI session " + sessionID,
		MediaType:            "application/json",
		License:              "NOASSERTION",
		RetrievedAt:          time.Now().UTC().Format(time.RFC3339Nano),
	}
	result, err := service.IngestDocument(ctx, &pb.IngestDocumentRequest{Document: document, Text: string(content)})
	if err != nil {
		return nil, err
	}
	return result.Result, nil
}

func copilotHistoryDocument(snapshot copilotbridge.HistorySnapshot) ([]byte, error) {
	return json.Marshal(snapshot)
}

func onboardingDocumentPaths() ([]string, error) {
	var paths []string
	err := fs.WalkDir(onboardingDocuments, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func (service *Service) ingestOnboardingFile(ctx context.Context, root, relative, revision, prefix, license string) (*pb.IngestDocumentResult, error) {
	var content []byte
	var err error
	uri := "urn:csf:onboarding:" + relative
	if root == "" {
		content, err = onboardingDocuments.ReadFile(relative)
	} else {
		content, err = readOnboardingFile(root, relative)
		absolute, absoluteErr := filepath.Abs(filepath.Join(root, relative))
		if absoluteErr != nil {
			return nil, absoluteErr
		}
		uri = (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}).String()
	}
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(content)
	if revision == "" {
		revision = hex.EncodeToString(digest[:])
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	document := &pb.SourceDocument{
		SourceId:             prefix + filepath.ToSlash(relative),
		Revision:             revision,
		ContentHash:          hex.EncodeToString(digest[:]),
		RawSourceContentHash: hex.EncodeToString(digest[:]),
		SourceUri:            uri,
		Title:                filepath.ToSlash(relative),
		MediaType:            "text/plain",
		License:              license,
		RetrievedAt:          now,
	}
	result, err := service.IngestDocument(ctx, &pb.IngestDocumentRequest{Document: document, Text: string(content)})
	if err != nil {
		return nil, err
	}
	return result.Result, nil
}

func readOnboardingFile(root, relative string) ([]byte, error) {
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rootHandle.Close() }()
	// Reject aliases of excluded files and directories inside the root too.
	// os.Root independently prevents traversing outside the authorized root.
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for end := 1; end <= len(parts); end++ {
		info, err := rootHandle.Lstat(filepath.Join(parts[:end]...))
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("onboarding sources must not use symbolic links")
		}
	}
	file, err := rootHandle.Open(relative)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("onboarding source is not a regular file")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxAPIBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxAPIBytes || !utf8.Valid(content) {
		return nil, fmt.Errorf("onboarding source is not bounded UTF-8")
	}
	return content, nil
}

func onboardingConsumerPathAllowed(relative string) bool {
	if relative == "" || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, "../") || strings.Contains(relative, "/../") {
		return false
	}
	for _, part := range strings.Split(relative, "/") {
		if part == ".git" || part == "secrets" || strings.HasPrefix(part, ".env") || part == "node_modules" {
			return false
		}
	}
	for _, suffix := range []string{".pem", ".key", ".db", ".sqlite", ".sqlite3"} {
		if strings.HasSuffix(strings.ToLower(relative), suffix) {
			return false
		}
	}
	return true
}

func onboardingNamespace(root string) string {
	digest := sha256.Sum256([]byte(filepath.Clean(root)))
	return hex.EncodeToString(digest[:])[:16]
}
