package csf

import (
	"strings"
	"time"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/services/jobs"
)

//go:generate go run github.com/jmattheis/goverter/cmd/goverter@v1.10.0 gen ./

// goverter:converter
// goverter:output:file ./simulation_views_gen.go
// goverter:output:package github.com/candacelabs/csf/csf
// goverter:ignoreUnexported
// goverter:extend simulatorOf simulationExecutorOf simulationState simulationStamp simulationUnits
type iSimulationViews interface {
	// goverter:map ID RunId
	// goverter:map Kind Simulator
	// goverter:map TotalUnits Steps
	// goverter:map CompletedUnits CompletedSteps
	// goverter:map ExternalID JobId
	// goverter:map Target JobQueue
	// goverter:map Image JobDefinition
	// goverter:map ArtifactURI ArtifactUri
	// goverter:map ReservationUSDMicros ReservationUsdMicros
	// goverter:map Spec.CaptureEvery CaptureEvery
	// goverter:map Trace.URL TraceUrl
	// goverter:map Trace.ExportError TraceExportError
	// goverter:map LogArchive.DocumentID LogDocumentId
	// goverter:map LogArchive.ProjectionError LogProjectionError
	// goverter:map LogArchive.IndexedAt LogIndexedAt
	// goverter:ignore LatestMeasurements Artifacts
	Run(job SimulationJob) *pb.SimulationRun
}

var simulationViews iSimulationViews

// simulationRun is the generated view of one simulation job.
func simulationRun(job SimulationJob) *pb.SimulationRun { return simulationViews.Run(job) }

// simulationMeasurement names the run a ledger measurement belongs to.
func simulationMeasurement(runID string, measurement jobs.RecordedMeasurement) *pb.Measurement {
	return &pb.Measurement{RunId: runID, Metric: measurement.Metric, Step: measurement.Step, Value: measurement.Value}
}

func simulationState(value jobs.State) pb.SimulationState {
	return pb.SimulationState(pb.SimulationState_value["SIMULATION_STATE_"+strings.ToUpper(string(value))])
}
func simulationStamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

// Simulation admissions carry uint32 step counts, so stored units fit.
func simulationUnits(value int64) uint32 { return uint32(value) }
