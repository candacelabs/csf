// Copyright 2026 Candace Labs

package dispatch

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/candacelabs/csf/ipc/db/csfpg"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
)

// IDispatchQueries is the part of csfpg's generated queries the service runs.
// *csfpg.Queries satisfies it; the SQL is owned by ipc/db/csfpg/dispatch.sql.
type IDispatchQueries interface {
	InsertSlice(ctx context.Context, arg csfpg.InsertSliceParams) (csfpg.CsfSlice, error)
	UpdateSliceState(ctx context.Context, arg csfpg.UpdateSliceStateParams) (csfpg.CsfSlice, error)
	ListSlices(ctx context.Context) ([]csfpg.CsfSlice, error)
	InsertSliceEdge(ctx context.Context, arg csfpg.InsertSliceEdgeParams) error
	ListSliceEdges(ctx context.Context) ([]csfpg.CsfSliceEdge, error)
	InsertIntent(ctx context.Context, arg csfpg.InsertIntentParams) (csfpg.CsfIntent, error)
	ListIntents(ctx context.Context) ([]csfpg.CsfIntent, error)
}

// IDispatchDatabase is the service's database: the queries outside a
// transaction, and Transact for work that must commit or roll back as one
// unit.
//
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=database.go -destination=mocks/mock_database.go -package=mocks
type IDispatchDatabase interface {
	IDispatchQueries
	Transact(ctx context.Context, work func(queries IDispatchQueries) error) error
}

// PostgresDispatchDatabase is the IDispatchDatabase over the PostgreSQL
// capability. It borrows the capability and never closes it.
type PostgresDispatchDatabase struct {
	*csfpg.Queries
	database csfpg.IDB
}

// NewPostgresDispatchDatabase returns the dispatch database over a pool the
// binary opened through ipc/db/csfpg.
func NewPostgresDispatchDatabase(database csfpg.IDB) (*PostgresDispatchDatabase, error) {
	if database == nil {
		return nil, fmt.Errorf("%w: nil database", ErrInvalidOption)
	}
	return &PostgresDispatchDatabase{Queries: csfpg.New(database), database: database}, nil
}

// Transact runs work in one transaction.
func (database *PostgresDispatchDatabase) Transact(ctx context.Context, work func(queries IDispatchQueries) error) error {
	return csfpg.Transact(ctx, database.database, func(tx pgx.Tx) error {
		return work(database.Queries.WithTx(tx))
	})
}

// sliceStates maps the proto state onto the schema's enum and back.
var sliceStates = map[dispatchv1.SliceState]csfpg.CsfSliceState{
	dispatchv1.SliceState_SLICE_STATE_QUEUED:    csfpg.CsfSliceStateQueued,
	dispatchv1.SliceState_SLICE_STATE_RUNNING:   csfpg.CsfSliceStateRunning,
	dispatchv1.SliceState_SLICE_STATE_PREEMPTED: csfpg.CsfSliceStatePreempted,
	dispatchv1.SliceState_SLICE_STATE_MERGED:    csfpg.CsfSliceStateMerged,
	dispatchv1.SliceState_SLICE_STATE_FAILED:    csfpg.CsfSliceStateFailed,
	dispatchv1.SliceState_SLICE_STATE_CANCELED:  csfpg.CsfSliceStateCanceled,
}

func storedState(state dispatchv1.SliceState) csfpg.CsfSliceState {
	return sliceStates[state]
}

func protoState(state csfpg.CsfSliceState) dispatchv1.SliceState {
	for candidate, stored := range sliceStates {
		if stored == state {
			return candidate
		}
	}
	return dispatchv1.SliceState_SLICE_STATE_UNSPECIFIED
}

func timestamp(at time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: at, Valid: true}
}

// encode is the stored form of a typed part: protobuf JSON.
func encode(message proto.Message) ([]byte, error) {
	return protojson.Marshal(message)
}

// decode reads a stored typed part back; it reports which column failed.
func decode[Message proto.Message](column string, stored []byte, message Message) error {
	if err := protojson.Unmarshal(stored, message); err != nil {
		return fmt.Errorf("dispatch: decode %s: %w", column, err)
	}
	return nil
}

// insertSliceParams is the row a new node is written as.
func insertSliceParams(node *dispatchv1.SliceNode, sequence uint64, at time.Time) (csfpg.InsertSliceParams, error) {
	recipe, err := encode(node.GetSlice().GetRecipe())
	if err != nil {
		return csfpg.InsertSliceParams{}, err
	}
	touch, err := encode(node.GetTouchSet())
	if err != nil {
		return csfpg.InsertSliceParams{}, err
	}
	provenance, err := encode(node.GetProvenance())
	if err != nil {
		return csfpg.InsertSliceParams{}, err
	}
	return csfpg.InsertSliceParams{
		SliceID: node.GetSlice().GetSliceId(), Sequence: int64(sequence), Title: node.GetSlice().GetTitle(),
		Recipe: recipe, TouchSet: touch, Provenance: provenance, State: storedState(node.GetState()), CreatedAt: timestamp(at),
	}, nil
}

// updateSliceParams is the row a node's changed state is written as.
func updateSliceParams(node *dispatchv1.SliceNode, at time.Time) csfpg.UpdateSliceStateParams {
	return csfpg.UpdateSliceStateParams{
		SliceID: node.GetSlice().GetSliceId(), State: storedState(node.GetState()), AssignmentID: node.GetAssignmentId(),
		PullRequestUrl: node.GetPullRequestUrl(), Attempts: int32(node.GetAttempts()), Checkpoint: node.GetCheckpoint(),
		Error: node.GetError(), UpdatedAt: timestamp(at),
	}
}

// nodeFromRow rebuilds a node from its row; edges and intents are attached
// by the caller.
func nodeFromRow(row csfpg.CsfSlice) (*dispatchv1.SliceNode, error) {
	recipe := &pb.AgentAssignmentRecipe{}
	if err := decode("recipe", row.Recipe, recipe); err != nil {
		return nil, err
	}
	touch := &dispatchv1.TouchSet{}
	if err := decode("touch_set", row.TouchSet, touch); err != nil {
		return nil, err
	}
	provenance := &dispatchv1.Provenance{}
	if err := decode("provenance", row.Provenance, provenance); err != nil {
		return nil, err
	}
	node := &dispatchv1.SliceNode{
		Slice:          &dispatchv1.Slice{SliceId: row.SliceID, Title: row.Title, Recipe: recipe},
		Edges:          &dispatchv1.SliceEdges{},
		TouchSet:       touch,
		Provenance:     provenance,
		State:          protoState(row.State),
		AssignmentId:   row.AssignmentID,
		PullRequestUrl: row.PullRequestUrl,
		Attempts:       uint32(row.Attempts),
		Checkpoint:     row.Checkpoint,
		Error:          row.Error,
	}
	if row.CreatedAt.Valid {
		node.CreatedAt = timestamppbOf(row.CreatedAt.Time)
	}
	if row.UpdatedAt.Valid {
		node.UpdatedAt = timestamppbOf(row.UpdatedAt.Time)
	}
	return node, nil
}

// edgeRow is one csf_slice_edges row to write.
type edgeRow struct {
	from     string
	to       string
	relation string
}

// edgeRows is every row a node's declared edges become: depends_on and
// required_by both as depends_on rows in edge direction, contends as
// contends rows.
func edgeRows(record *dispatchv1.SliceNode) []edgeRow {
	id := record.GetSlice().GetSliceId()
	var rows []edgeRow
	for _, predecessor := range record.GetEdges().GetDependsOn() {
		rows = append(rows, edgeRow{from: predecessor, to: id, relation: string(RelationDependsOn)})
	}
	for _, successor := range record.GetEdges().GetRequiredBy() {
		rows = append(rows, edgeRow{from: id, to: successor, relation: string(RelationDependsOn)})
	}
	for _, other := range record.GetEdges().GetContends() {
		rows = append(rows, edgeRow{from: id, to: other, relation: string(RelationContends)})
	}
	return rows
}

func (row edgeRow) params() csfpg.InsertSliceEdgeParams {
	return csfpg.InsertSliceEdgeParams{FromSliceID: row.from, ToSliceID: row.to, Relation: row.relation}
}

func timestamppbOf(at time.Time) *timestamppb.Timestamp {
	return timestamppb.New(at)
}

// insertIntentParams is the row an intent is written as, attached to slice
// or to none.
func insertIntentParams(intent *dispatchv1.Intent, slice string, at time.Time) (csfpg.InsertIntentParams, error) {
	encoded, err := encode(intent)
	if err != nil {
		return csfpg.InsertIntentParams{}, err
	}
	params := csfpg.InsertIntentParams{IntentID: intent.GetIntentId(), Intent: encoded, CreatedAt: timestamp(at)}
	if slice != "" {
		params.SliceID = &slice
	}
	return params, nil
}
