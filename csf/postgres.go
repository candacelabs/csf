package csf

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	db "github.com/candacelabs/csf/csf/internal/brainspinedb"
	"github.com/candacelabs/csf/pkg/sqlmigrate"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/guregu/null/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// Postgres owns the relational projection of retained source and experiment data.
type Postgres struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

func NewPostgres(pool *pgxpool.Pool) (*Postgres, error) {
	if pool == nil {
		return nil, fmt.Errorf("database pool required")
	}
	return &Postgres{pool: pool, queries: db.New(pool)}, nil
}

// Initialize bootstraps an empty, explicitly selected database, then applies
// additive migrations. Each phase commits atomically. Existing installations
// fail bootstrap rather than silently adopting a schema.
func (store *Postgres) Initialize(ctx context.Context) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, db.Schema); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return store.Migrate(ctx)
}

// Migrate uses the shared migration ledger for additive, transactional upgrades.
func (store *Postgres) Migrate(ctx context.Context) error {
	connection := stdlib.OpenDBFromPool(store.pool)
	defer func() { _ = connection.Close() }()
	return sqlmigrate.ApplyPrefixed(ctx, connection, db.Migrations, projectionMigrationDirectory, projectionMigrationPrefix)
}

const projectionMigrationDirectory = "migrations"
const projectionMigrationPrefix = "brainspine"

func (store *Postgres) PutDocument(ctx context.Context, document *pb.SourceDocument) (*pb.SourceDocument, error) {
	if document == nil || document.SizeBytes > math.MaxInt64 {
		return nil, fmt.Errorf("invalid document")
	}
	stamp, err := time.Parse(time.RFC3339Nano, document.RetrievedAt)
	if err != nil {
		return nil, err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := store.queries.WithTx(tx)
	blob, err := queries.CreateDocument(ctx, db.CreateDocumentParams{ContentHash: document.ContentHash, ByteSize: int64(document.SizeBytes), ArtifactRef: document.ArtifactRef})
	if err != nil {
		return nil, err
	}
	source, err := queries.CreateSourceRevision(ctx, db.CreateSourceRevisionParams{SourceID: document.SourceId, Revision: document.Revision, ContentHash: document.ContentHash, RawSourceContentHash: null.NewString(document.RawSourceContentHash, document.RawSourceContentHash != "").Ptr(), SourceUri: document.SourceUri, Title: document.Title, MediaType: document.MediaType, License: document.License, RetrievedAt: pgtype.Timestamptz{Time: stamp, Valid: true}})
	if err != nil {
		return nil, err
	}
	if _, err := queries.EnqueueProjectionTask(ctx, db.EnqueueProjectionTaskParams{SourceID: source.SourceID, Revision: source.Revision}); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return documentMessage(source, blob), nil
}

func (store *Postgres) GetDocument(ctx context.Context, request *pb.DocumentRequest) (*pb.SourceDocument, error) {
	source, err := store.queries.GetSourceRevision(ctx, db.GetSourceRevisionParams{SourceID: request.SourceId, Revision: request.Revision})
	if err != nil {
		return nil, err
	}
	blob, err := store.queries.GetDocument(ctx, source.ContentHash)
	if err != nil {
		return nil, err
	}
	return documentMessage(source, blob), nil
}

func documentMessage(source db.BrainspineSourceRevision, blob db.BrainspineDocument) *pb.SourceDocument {
	return &pb.SourceDocument{SourceId: source.SourceID, Revision: source.Revision, ContentHash: source.ContentHash, SourceUri: source.SourceUri, Title: source.Title, MediaType: source.MediaType, License: source.License, RetrievedAt: source.RetrievedAt.Time.UTC().Format(time.RFC3339Nano), ArtifactRef: blob.ArtifactRef, SizeBytes: uint64(blob.ByteSize), RawSourceContentHash: null.StringFromPtr(source.RawSourceContentHash).String}
}

func (store *Postgres) PutNode(ctx context.Context, node *pb.KnowledgeNode) (*pb.KnowledgeNode, error) {
	if node == nil || (node.CitationStartByte != nil && *node.CitationStartByte > math.MaxInt64) || (node.CitationEndByte != nil && *node.CitationEndByte > math.MaxInt64) {
		return nil, fmt.Errorf("invalid citation offset")
	}
	parameters := db.CreateNodeParams{NodeID: node.NodeId, Kind: strings.ToLower(strings.TrimPrefix(node.Kind.String(), "KNOWLEDGE_KIND_")), SymbolKey: node.SymbolKey, Title: node.Title, Statement: node.Statement, ParentNodeID: null.NewString(node.ParentNodeId, node.ParentNodeId != "").Ptr(), AuthorKind: strings.ToLower(strings.TrimPrefix(node.AuthorKind.String(), "AUTHOR_KIND_")), AuthorRef: node.AuthorRef, CitationContentHash: null.NewString(node.CitationContentHash, node.CitationContentHash != "").Ptr(), CitationSourceID: null.NewString(node.CitationSourceId, node.CitationSourceId != "").Ptr(), CitationRevision: null.NewString(node.CitationSourceRevision, node.CitationSourceRevision != "").Ptr(), CitationChunkID: null.NewString(node.CitationChunkId, node.CitationChunkId != "").Ptr(), TextProjectionRef: null.NewString(node.TextProjectionRef, node.TextProjectionRef != "").Ptr(), VectorProjectionRef: null.NewString(node.VectorProjectionRef, node.VectorProjectionRef != "").Ptr()}
	if node.CitationStartByte != nil {
		value := int64(*node.CitationStartByte)
		parameters.CitationStartByte = &value
	}
	if node.CitationEndByte != nil {
		value := int64(*node.CitationEndByte)
		parameters.CitationEndByte = &value
	}
	result, err := store.queries.CreateNode(ctx, parameters)
	if err != nil {
		return nil, err
	}
	return nodeMessage(result), nil
}

func (store *Postgres) PutEdge(ctx context.Context, edge *pb.KnowledgeEdge) (*pb.KnowledgeEdge, error) {
	if edge == nil {
		return nil, fmt.Errorf("edge required")
	}
	result, err := store.queries.CreateEdge(ctx, db.CreateEdgeParams{FromNodeID: edge.FromNodeId, ToNodeID: edge.ToNodeId, Relation: strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(edge.Relation.String(), "RELATION_KIND_"), "_", "-")), AuthorKind: strings.ToLower(strings.TrimPrefix(edge.AuthorKind.String(), "AUTHOR_KIND_")), AuthorRef: edge.AuthorRef, Rationale: edge.Rationale})
	if err != nil {
		return nil, err
	}
	return edgeMessage(result), nil
}

func (store *Postgres) GetGraph(ctx context.Context, request *pb.GraphRequest) (*pb.GraphSnapshot, error) {
	if request == nil || request.Limit == 0 || request.Limit > 500 {
		return nil, fmt.Errorf("graph limit must be 1..500")
	}
	snapshot := &pb.GraphSnapshot{}
	limit := int(request.Limit)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := store.queries.WithTx(tx)
	queue := []db.BrainspineNode{}
	if request.RootId != "" {
		node, err := queries.GetNode(ctx, request.RootId)
		if err != nil {
			return nil, err
		}
		queue = append(queue, node)
	} else {
		queue, err = queries.ListChildNodes(ctx, db.ListChildNodesParams{ResultLimit: int32(limit + 1)})
		if err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	edges := map[string]bool{}
	for len(queue) > 0 && len(snapshot.Nodes) < limit {
		node := queue[0]
		queue = queue[1:]
		if seen[node.NodeID] {
			continue
		}
		seen[node.NodeID] = true
		snapshot.Nodes = append(snapshot.Nodes, nodeMessage(node))
		children, err := queries.ListChildNodes(ctx, db.ListChildNodesParams{ParentNodeID: &node.NodeID, ResultLimit: int32(limit + 1)})
		if err != nil {
			return nil, err
		}
		queue = append(queue, children...)
		adjacent, err := queries.ListNodeEdges(ctx, db.ListNodeEdgesParams{NodeID: node.NodeID, ResultLimit: int32(limit + 1)})
		if err != nil {
			return nil, err
		}
		for _, edge := range adjacent {
			key := edge.FromNodeID + "\x00" + edge.ToNodeID + "\x00" + edge.Relation
			if edges[key] {
				continue
			}
			edges[key] = true
			if len(snapshot.Edges) >= limit {
				snapshot.Truncated = true
				break
			}
			snapshot.Edges = append(snapshot.Edges, edgeMessage(edge))
		}
	}
	snapshot.Truncated = snapshot.Truncated || len(queue) > 0
	// Edges may cite nodes outside the returned tree; those remain explicit IDs.
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func nodeMessage(node db.BrainspineNode) *pb.KnowledgeNode {
	result := &pb.KnowledgeNode{NodeId: node.NodeID, Kind: pb.KnowledgeKind(pb.KnowledgeKind_value["KNOWLEDGE_KIND_"+strings.ToUpper(node.Kind)]), SymbolKey: node.SymbolKey, Title: node.Title, Statement: node.Statement, ParentNodeId: null.StringFromPtr(node.ParentNodeID).String, AuthorKind: pb.AuthorKind(pb.AuthorKind_value["AUTHOR_KIND_"+strings.ToUpper(node.AuthorKind)]), AuthorRef: node.AuthorRef, CitationContentHash: null.StringFromPtr(node.CitationContentHash).String, CitationSourceId: null.StringFromPtr(node.CitationSourceID).String, CitationSourceRevision: null.StringFromPtr(node.CitationRevision).String, CitationChunkId: null.StringFromPtr(node.CitationChunkID).String, TextProjectionRef: null.StringFromPtr(node.TextProjectionRef).String, VectorProjectionRef: null.StringFromPtr(node.VectorProjectionRef).String}
	if node.CitationStartByte != nil {
		value := uint64(*node.CitationStartByte)
		result.CitationStartByte = &value
	}
	if node.CitationEndByte != nil {
		value := uint64(*node.CitationEndByte)
		result.CitationEndByte = &value
	}
	return result
}
func edgeMessage(edge db.BrainspineEdge) *pb.KnowledgeEdge {
	return &pb.KnowledgeEdge{FromNodeId: edge.FromNodeID, ToNodeId: edge.ToNodeID, Relation: pb.RelationKind(pb.RelationKind_value["RELATION_KIND_"+strings.ToUpper(strings.ReplaceAll(edge.Relation, "-", "_"))]), AuthorKind: pb.AuthorKind(pb.AuthorKind_value["AUTHOR_KIND_"+strings.ToUpper(edge.AuthorKind)]), AuthorRef: edge.AuthorRef, Rationale: edge.Rationale}
}
