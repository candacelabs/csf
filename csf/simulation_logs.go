package csf

import (
	"crypto/sha256"
	"encoding/hex"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"google.golang.org/protobuf/proto"
)

const simulationLogKind = "simulation-source"
const simulationLogIdentityPrefix = "csf.simulation.source.v1\x00"
const simulationDocumentIDField = "_id"

func WithSimulationLogSearch(search *OpenSearch) SimulationOption {
	return func(simulations *Simulations) { simulations.logSearch = search }
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
