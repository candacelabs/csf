package csf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	db "github.com/candacelabs/csf/csf/internal/brainspinedb"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const simulationLogKind = "simulation-source"
const simulationLogIdentityPrefix = "csf.simulation.source.v1\x00"
const simulationDocumentIDField = "_id"

func WithSimulationLogSearch(search *OpenSearch) SimulationOption {
	return func(simulations *Simulations) { simulations.logSearch = search }
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

func simulationLogID(runID string) string {
	digest := sha256.Sum256([]byte(simulationLogIdentityPrefix + runID))
	return hex.EncodeToString(digest[:])
}

func simulationSourceHash(source *pb.SimulationTraceSource) (string, error) {
	content, err := (proto.MarshalOptions{Deterministic: true}).Marshal(source)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

// archiveSimulationLogs retries durable terminal rows in the existing worker.
// An OpenSearch outage does not suppress independent Langfuse export.
func (simulations *Simulations) archiveSimulationLogs(ctx context.Context) error {
	if simulations.logSearch == nil || simulations.local == nil || simulations.local.config.LogIndex == "" {
		return nil
	}
	row, err := simulations.store.queries.NextSimulationLog(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	source, problem := simulations.local.traceSource(row)
	if problem == nil && row.LogStream != "" {
		var content []byte
		content, problem = simulations.artifacts.Get(row.LogStream)
		if problem == nil && !utf8.Valid(content) {
			problem = fmt.Errorf("retained simulator logs are not UTF-8; raw artifact remains available")
		}
		source.Logs = string(content)
	}
	result := db.SetSimulationLogParams{RunID: row.RunID}
	if problem == nil {
		deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
		result.LogDocumentID, problem = simulations.logSearch.IndexSimulationSource(deadline, simulations.local.config.LogIndex, source)
		cancel()
	}
	if problem != nil {
		result.LogDocumentID = ""
		result.LogProjectionError = problem.Error()
	} else {
		result.LogIndexedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	}
	return simulations.store.queries.SetSimulationLog(ctx, result)
}

// RebuildSimulationTrace derives versioned spans from OpenSearch alone. The
// durable delivery ledger prevents repeating an accepted or ambiguous export.
func (service *Service) RebuildSimulationTrace(ctx context.Context, request *pb.RebuildSimulationTraceRequest) (*pb.RebuildSimulationTraceResponse, error) {
	simulations := service.simulations
	if simulations == nil || simulations.local == nil || simulations.traces == nil || simulations.logSearch == nil || simulations.local.config.TraceBaseUrl == "" || simulations.local.config.LogIndex == "" || request == nil || !simulationID.MatchString(request.RunId) {
		return nil, fmt.Errorf("simulation trace reconstruction unavailable or invalid run identity")
	}
	row, err := simulations.store.queries.GetSimulation(ctx, request.RunId)
	if err != nil {
		return nil, err
	}
	if row.Executor != int32(pb.SimulationExecutor_SIMULATION_EXECUTOR_LOCAL) || !simulationTerminal(row.State) || (row.Managed && !row.CleanupConfirmed) {
		return nil, fmt.Errorf("simulation is not terminal with confirmed cleanup")
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	record, err := simulations.logSearch.SimulationSource(deadline, simulations.local.config.LogIndex, request.RunId)
	if err != nil {
		return nil, err
	}
	if record.Simulation.Run.Simulator != pb.Simulator(row.Simulator) || record.Simulation.Run.Steps != uint32(row.Steps) {
		return nil, fmt.Errorf("archived simulation does not match admitted job")
	}
	spans, identity, err := simulations.local.projectTrace(record.Simulation)
	if err != nil {
		return nil, err
	}
	if err := simulations.deliverSimulationTrace(deadline, row, spans, identity); err != nil {
		retained := simulations.store.queries.SetSimulationTrace(ctx, db.SetSimulationTraceParams{RunID: row.RunID, TraceUrl: row.TraceUrl, TraceExportError: err.Error()})
		return nil, errors.Join(err, retained)
	}
	run, err := simulations.inspect(ctx, row.RunID)
	if err != nil {
		return nil, err
	}
	return &pb.RebuildSimulationTraceResponse{Run: run, SourceSha256: record.SourceSha256}, nil
}
