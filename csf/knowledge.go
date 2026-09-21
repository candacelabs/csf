package csf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"google.golang.org/protobuf/proto"
)

// IKnowledgeStore owns durable source identities and immutable symbolic facts.
type IKnowledgeStore interface {
	PutDocument(ctx context.Context, document *pb.SourceDocument) (*pb.SourceDocument, error)
	GetDocument(ctx context.Context, request *pb.DocumentRequest) (*pb.SourceDocument, error)
	PutNode(ctx context.Context, node *pb.KnowledgeNode) (*pb.KnowledgeNode, error)
	PutEdge(ctx context.Context, edge *pb.KnowledgeEdge) (*pb.KnowledgeEdge, error)
	GetGraph(ctx context.Context, request *pb.GraphRequest) (*pb.GraphSnapshot, error)
	ClaimProjection(ctx context.Context) (*pb.ProjectionTask, error)
	CompleteProjection(ctx context.Context, task *pb.ProjectionTask) error
	FailProjection(ctx context.Context, task *pb.ProjectionTask, problem string) error
	GetProjection(ctx context.Context, request *pb.DocumentRequest) (*pb.ProjectionTask, error)
	CountProjections(ctx context.Context) ([]*pb.ProjectionCount, error)
}

// IKnowledgeIndex is a rebuildable projection, never the evidence authority.
type IKnowledgeIndex interface {
	Index(ctx context.Context, document *pb.SourceDocument, text string) error
	Search(ctx context.Context, request *pb.SearchRequest) (*pb.SearchResult, error)
}

// IAgentConfigurationStore retains one revisioned external-tool configuration
// per authenticated agent. It stores opaque secret references, never values.
type IAgentConfigurationStore interface {
	GetAgentConfiguration(ctx context.Context, agentID string) (*pb.AgentConfiguration, error)
	PutAgentConfiguration(ctx context.Context, agentID string, expectedRevision uint32, configuration *pb.AgentConfigurationInput) (*pb.AgentConfiguration, error)
}

func (service *Service) Compile(ctx context.Context, request *pb.CompileRequest) (*pb.CompileResponse, error) {
	program, err := Compile(request.GetController())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	return &pb.CompileResponse{Program: program}, nil
}
func (service *Service) GetSnapshot(ctx context.Context, request *pb.GetSnapshotRequest) (*pb.GetSnapshotResponse, error) {
	if service.dashboard == nil {
		return &pb.GetSnapshotResponse{Snapshot: &pb.Snapshot{Issues: []string{"no event source configured"}}}, nil
	}
	return &pb.GetSnapshotResponse{Snapshot: service.dashboard.Snapshot()}, nil
}

func (service *Service) IngestDocument(ctx context.Context, request *pb.IngestDocumentRequest) (*pb.IngestDocumentResponse, error) {
	if service.store == nil {
		return nil, fmt.Errorf("knowledge capability unavailable")
	}
	if request == nil || request.Document == nil || request.Text == "" || !utf8.ValidString(request.Text) || len(request.Text) > maxAPIBytes {
		return nil, fmt.Errorf("%w: a bounded UTF-8 document is required", ErrInvalidRequest)
	}
	document := proto.CloneOf(request.Document)
	if err := pb.ValidateSourceDocument(document); err != nil {
		return nil, err
	}
	if _, err := time.Parse(time.RFC3339Nano, document.RetrievedAt); err != nil {
		return nil, fmt.Errorf("%w: retrieved_at must be RFC3339", ErrInvalidRequest)
	}
	content := []byte(request.Text)
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != document.ContentHash {
		return nil, fmt.Errorf("%w: document content hash mismatch", ErrInvalidRequest)
	}
	_, ref, err := service.artifacts.Put(content)
	if err != nil {
		return nil, err
	}
	document.ArtifactRef = ref
	document.SizeBytes = uint64(len(request.Text))
	stored, err := service.store.PutDocument(ctx, document)
	if err != nil {
		return nil, err
	}
	// The store commits source registration and enqueue in the same transaction.
	// Wakeups only reduce latency; the durable queue owns recovery and retries.
	select {
	case service.wake <- struct{}{}:
	default:
	}
	task, err := service.store.GetProjection(ctx, &pb.DocumentRequest{SourceId: stored.SourceId, Revision: stored.Revision})
	if err != nil {
		return nil, err
	}
	result := &pb.IngestDocumentResult{Document: stored, Projection: task, ProjectionError: task.GetLastError(), Indexed: task.GetState() == pb.ProjectionState_PROJECTION_STATE_SUCCEEDED, Queued: task.GetState() == pb.ProjectionState_PROJECTION_STATE_PENDING || task.GetState() == pb.ProjectionState_PROJECTION_STATE_RUNNING}
	return &pb.IngestDocumentResponse{Result: result}, nil
}

func (service *Service) GetDocument(ctx context.Context, request *pb.GetDocumentRequest) (*pb.GetDocumentResponse, error) {
	if service.store == nil {
		return nil, fmt.Errorf("knowledge capability unavailable")
	}
	if request == nil || request.Request == nil || request.Request.SourceId == "" || request.Request.Revision == "" {
		return nil, fmt.Errorf("%w: source and revision required", ErrInvalidRequest)
	}
	document, err := service.store.GetDocument(ctx, request.Request)
	if err != nil {
		return nil, err
	}
	content, err := service.artifacts.Get(document.ContentHash)
	if err != nil {
		return nil, err
	}
	task, err := service.store.GetProjection(ctx, request.Request)
	if err != nil {
		return nil, err
	}
	return &pb.GetDocumentResponse{Result: &pb.DocumentResult{Document: document, Text: string(content), Projection: task}}, nil
}

func (service *Service) Search(ctx context.Context, request *pb.SearchRequest) (*pb.SearchResponse, error) {
	if service.index == nil {
		return nil, fmt.Errorf("knowledge capability unavailable")
	}
	if request == nil {
		return nil, fmt.Errorf("%w: search request required", ErrInvalidRequest)
	}
	if err := pb.ValidateSearchRequest(request); err != nil {
		return nil, err
	}
	result, err := service.index.Search(ctx, request)
	if err != nil {
		return nil, err
	}
	return &pb.SearchResponse{Result: result}, nil
}

func (service *Service) PutNode(ctx context.Context, request *pb.PutNodeRequest) (*pb.PutNodeResponse, error) {
	if service.store == nil {
		return nil, fmt.Errorf("knowledge capability unavailable")
	}
	if request == nil || request.Node == nil || request.Node.AuthorKind != pb.AuthorKind_AUTHOR_KIND_MODEL {
		return nil, fmt.Errorf("%w: agent writes must be attributed to model; trusted evidence has a separate owner", ErrInvalidRequest)
	}
	node, err := service.store.PutNode(ctx, request.Node)
	return &pb.PutNodeResponse{Node: node}, err
}

func (service *Service) PutEdge(ctx context.Context, request *pb.PutEdgeRequest) (*pb.PutEdgeResponse, error) {
	if service.store == nil {
		return nil, fmt.Errorf("knowledge capability unavailable")
	}
	if request == nil || request.Edge == nil || request.Edge.AuthorKind != pb.AuthorKind_AUTHOR_KIND_MODEL {
		return nil, fmt.Errorf("%w: agent writes must be attributed to model", ErrInvalidRequest)
	}
	edge, err := service.store.PutEdge(ctx, request.Edge)
	return &pb.PutEdgeResponse{Edge: edge}, err
}

func (service *Service) GetGraph(ctx context.Context, request *pb.GetGraphRequest) (*pb.GetGraphResponse, error) {
	if service.store == nil {
		return nil, fmt.Errorf("knowledge capability unavailable")
	}
	if request == nil || request.Request == nil || request.Request.Limit == 0 || request.Request.Limit > 500 {
		return nil, fmt.Errorf("%w: graph limit must be 1..500", ErrInvalidRequest)
	}
	snapshot, err := service.store.GetGraph(ctx, request.Request)
	return &pb.GetGraphResponse{Snapshot: snapshot}, err
}

// Artifacts retains bytes under their SHA256. Writes never replace a blob.
type Artifacts struct{ root *os.Root }

func NewArtifacts(directory string) (*Artifacts, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &Artifacts{root: root}, nil
}
func (artifacts *Artifacts) Close() error { return artifacts.root.Close() }
func (artifacts *Artifacts) Put(content []byte) (string, string, error) {
	if len(content) > maxAPIBytes {
		return "", "", fmt.Errorf("artifact exceeds limit")
	}
	digest := sha256.Sum256(content)
	hash := hex.EncodeToString(digest[:])
	ref := filepath.Join(hash[:2], hash)
	if err := artifacts.root.MkdirAll(hash[:2], 0700); err != nil {
		return "", "", err
	}
	// A temporary file is fsynced before its complete bytes become visible.
	temporary := ref + fmt.Sprintf(".%d.tmp", time.Now().UnixNano())
	file, err := artifacts.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = artifacts.root.Remove(temporary) }()
	_, writeErr := file.Write(content)
	syncErr := file.Sync()
	closeErr := file.Close()
	for _, err := range []error{writeErr, syncErr, closeErr} {
		if err != nil {
			return "", "", err
		}
	}
	// Link is atomic and fails if the immutable destination already exists.
	if err := artifacts.root.Link(temporary, ref); err != nil && !os.IsExist(err) {
		return "", "", err
	}
	verified, err := artifacts.Get(hash)
	if err != nil {
		return "", "", err
	}
	if len(verified) != len(content) {
		return "", "", fmt.Errorf("artifact size mismatch")
	}
	directory, err := artifacts.root.Open(hash[:2])
	if err != nil {
		return "", "", err
	}
	syncErr = directory.Sync()
	closeErr = directory.Close()
	if syncErr != nil {
		return "", "", syncErr
	}
	if closeErr != nil {
		return "", "", closeErr
	}
	return hash, ref, nil
}
func (artifacts *Artifacts) Get(hash string) ([]byte, error) {
	if len(hash) != 64 {
		return nil, fmt.Errorf("invalid content hash")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return nil, err
	}
	file, err := artifacts.root.Open(filepath.Join(hash[:2], hash))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, maxAPIBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxAPIBytes {
		return nil, fmt.Errorf("artifact exceeds limit")
	}
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != hash {
		return nil, fmt.Errorf("artifact hash verification failed")
	}
	return content, nil
}
