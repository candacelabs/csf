package csf

import (
	db "github.com/candacelabs/csf/csf/internal/brainspinedb"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/jackc/pgx/v5/pgtype"
	"strings"
	"time"
)

//go:generate go run github.com/jmattheis/goverter/cmd/goverter@v1.10.0 gen ./

// goverter:converter
// goverter:output:file ./simulation_views_gen.go
// goverter:output:package github.com/candacelabs/csf/csf
// goverter:matchIgnoreCase
// goverter:ignoreUnexported
// goverter:extend simulationState simulationStamp simulationStep simulationStepCount
type iSimulationViews interface {
	// goverter:ignore LatestMeasurements Artifacts
	Run(row db.BrainspineSimulation) *pb.SimulationRun
	// goverter:ignore CandidateId Split
	Measurement(row db.BrainspineSimulationMeasurement) *pb.Measurement
}

var simulationViews iSimulationViews

func simulationState(value db.BrainspineSimulationState) pb.SimulationState {
	return pb.SimulationState(pb.SimulationState_value["SIMULATION_STATE_"+strings.ToUpper(string(value))])
}
func simulationStamp(value pgtype.Timestamptz) string {
	if !value.Valid {
		return ""
	}
	return value.Time.UTC().Format(time.RFC3339Nano)
}

// The owning SQL CHECK constraints bound these values to 0..10000.
func simulationStep(value int64) uint64      { return uint64(value) }
func simulationStepCount(value int32) uint32 { return uint32(value) }
