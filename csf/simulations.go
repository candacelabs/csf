package csf

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/services/jobs"
	"google.golang.org/protobuf/proto"
)

const simulationCampaign = "csf-simulator-examples-v1"
const simulationBudgetMicros int64 = 300000000
const simulationProgressMetric = "simulation_steps_completed"
const simulationEventPrefix = "CSF_EVENT "
const simulationJobPrefix = "csf-"
const simulationOwnerTag = "csf-run-id"
const simulationCancelReason = "CSF operator cancellation"
const simulationLogPage = 100
const simulationPollInterval = 5 * time.Second
const simulationDefaultTimeout = 900 * time.Second
const simulationTraceStopDeadline = 10 * time.Second

// The executors a simulation job can be admitted to.
const (
	simulationExecutorLocal    jobs.ExecutorName = "local"
	simulationExecutorAWSBatch jobs.ExecutorName = "aws_batch"
)

// The environment the simulator worker image reads.
const (
	simulationRunEnvironment          = "CSF_RUN_ID"
	simulationStepsEnvironment        = "CSF_STEPS"
	simulationArtifactEnvironment     = "CSF_ARTIFACT_URI"
	simulationCaptureEveryEnvironment = "CSF_CAPTURE_EVERY"
)

var simulationID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$`)
var simulationDefinition = regexp.MustCompile(`^arn:[a-z-]+:batch:[a-z0-9-]+:[0-9]{12}:job-definition/[A-Za-z0-9_-]+:[1-9][0-9]*$`)

// simulationKinds names each simulator's job kind in the ledger.
var simulationKinds = map[pb.Simulator]jobs.Kind{
	pb.Simulator_SIMULATOR_CARLA: "simulation.carla",
	pb.Simulator_SIMULATOR_ISAAC: "simulation.isaac",
}

// simulationExecutors names each executor in the ledger.
var simulationExecutors = map[pb.SimulationExecutor]jobs.ExecutorName{
	pb.SimulationExecutor_SIMULATION_EXECUTOR_LOCAL:     simulationExecutorLocal,
	pb.SimulationExecutor_SIMULATION_EXECUTOR_AWS_BATCH: simulationExecutorAWSBatch,
}

// SimulationSpec is the simulation-specific part of a job request, stored
// in the job ledger beside the generic admission.
type SimulationSpec struct {
	CaptureEvery   uint32 `json:"capture_every"`
	ArtifactVolume string `json:"artifact_volume,omitempty"`
}

// SimulationJob is one simulation in the job ledger.
type SimulationJob = jobs.Job[SimulationSpec]

func simulatorOf(kind jobs.Kind) pb.Simulator {
	for simulator, candidate := range simulationKinds {
		if candidate == kind {
			return simulator
		}
	}
	return pb.Simulator_SIMULATOR_UNSPECIFIED
}

func simulationExecutorOf(name jobs.ExecutorName) pb.SimulationExecutor {
	for executor, candidate := range simulationExecutors {
		if candidate == name {
			return executor
		}
	}
	return pb.SimulationExecutor_SIMULATION_EXECUTOR_UNSPECIFIED
}

// Simulations owns simulation admission and observation over the job
// ledger; the application owns its worker lifetime.
type Simulations struct {
	ledger    *jobs.JobLedger[SimulationSpec]
	artifacts *Artifacts
	config    *pb.SimulationConfig
	local     *LocalSimulations
	docker    *jobs.LocalDockerExecutor[SimulationSpec]
	traces    ISimulationTraces
	logSearch *OpenSearch
}

func NewSimulations(store *Postgres, artifacts *Artifacts, config *pb.SimulationConfig, provider jobs.IBatch, logs jobs.ICloudWatchLogs, options ...SimulationOption) (*Simulations, error) {
	if store == nil || artifacts == nil {
		return nil, fmt.Errorf("simulations require PostgreSQL and retained artifacts")
	}
	if config != nil {
		if err := validateSimulationConfig(config, provider, logs); err != nil {
			return nil, err
		}
	}
	result := &Simulations{artifacts: artifacts, config: proto.CloneOf(config)}
	for _, option := range options {
		option(result)
	}
	if result.local != nil && result.local.config.LogIndex != "" && result.logSearch == nil {
		return nil, fmt.Errorf("simulation log archival requires the shared OpenSearch client")
	}
	database, err := jobs.NewPostgresJobDatabase(store.database)
	if err != nil {
		return nil, err
	}
	budget := jobs.Budget{Account: simulationCampaign, LimitUSDMicros: simulationBudgetMicros}
	if config != nil {
		budget.LimitUSDMicros = config.BudgetUsdMicros
	}
	ledgerOptions := []jobs.LedgerOption[SimulationSpec]{jobs.WithProgressMetric[SimulationSpec](simulationProgressMetric), jobs.WithMetricLimit[SimulationSpec](maxMetrics)}
	if result.local != nil {
		result.docker, err = jobs.NewLocalDockerExecutor(result.local.docker, artifacts, jobs.LocalDockerSettings{Owner: localSimulationOwner, NamePrefix: localSimulationName, StopGrace: localStopGrace, LogBytes: localLogBytes}, result.local.container)
		if err != nil {
			return nil, err
		}
		ledgerOptions = append(ledgerOptions, jobs.WithHostExecutor[SimulationSpec](simulationExecutorLocal, result.docker))
	}
	if config != nil {
		remote, err := jobs.NewAWSBatchExecutor(provider, logs, jobs.AWSBatchSettings{LogGroup: config.LogGroup, JobNamePrefix: simulationJobPrefix, OwnerTag: simulationOwnerTag, CancelReason: simulationCancelReason, LogPageSize: simulationLogPage}, simulationBatchEnvironment, result.interpretBatchLog)
		if err != nil {
			return nil, err
		}
		ledgerOptions = append(ledgerOptions, jobs.WithRemoteExecutor[SimulationSpec](simulationExecutorAWSBatch, remote))
	}
	kinds := []jobs.Kind{simulationKinds[pb.Simulator_SIMULATOR_CARLA], simulationKinds[pb.Simulator_SIMULATOR_ISAAC]}
	result.ledger, err = jobs.NewJobLedger(database, budget, kinds, ledgerOptions...)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func validateSimulationConfig(config *pb.SimulationConfig, provider jobs.IBatch, logs jobs.ICloudWatchLogs) error {
	if err := pb.ValidateSimulationConfig(config); err != nil {
		return err
	}
	uri, err := url.Parse(config.ArtifactUri)
	if err != nil || uri.Scheme != "s3" || uri.Host == "" || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" || strings.Contains(uri.Path, "..") {
		return fmt.Errorf("simulation artifact_uri must be an S3 prefix")
	}
	if provider == nil || logs == nil || config.Region == "" || config.JobQueue == "" || config.LogGroup == "" || len(config.Profiles) == 0 {
		return fmt.Errorf("AWS simulations require region, queue, log group, profiles and SDK clients")
	}
	seen := map[pb.Simulator]bool{}
	queue, err := arn.Parse(config.JobQueue)
	if err != nil || queue.Service != "batch" || queue.Region != config.Region || !strings.HasPrefix(queue.Resource, "job-queue/") || queue.AccountID == "" {
		return fmt.Errorf("job_queue must be an AWS Batch ARN in the configured region")
	}
	for _, profile := range config.Profiles {
		if profile == nil || seen[profile.Simulator] || simulationKinds[profile.Simulator] == "" || !simulationDefinition.MatchString(profile.JobDefinition) {
			return fmt.Errorf("each simulator needs one revision-pinned Batch job definition")
		}
		if err := pb.ValidateSimulationProfile(profile); err != nil {
			return err
		}
		definition, err := arn.Parse(profile.JobDefinition)
		if err != nil || definition.Region != queue.Region || definition.AccountID != queue.AccountID {
			return fmt.Errorf("job definition and queue must use the same AWS account and region")
		}
		if profile.ReservationUsdMicros > config.BudgetUsdMicros {
			return fmt.Errorf("profile reservation exceeds campaign budget")
		}
		seen[profile.Simulator] = true
	}
	return nil
}

func simulationBatchEnvironment(job SimulationJob) []jobs.EnvironmentVariable {
	return []jobs.EnvironmentVariable{
		{Name: simulationRunEnvironment, Value: job.ID},
		{Name: simulationStepsEnvironment, Value: strconv.FormatInt(job.TotalUnits, 10)},
		{Name: simulationArtifactEnvironment, Value: job.ArtifactURI},
		{Name: simulationCaptureEveryEnvironment, Value: strconv.FormatUint(uint64(job.Spec.CaptureEvery), 10)},
	}
}

func WithSimulations(simulations *Simulations) Option {
	return func(service *Service) { service.simulations = simulations }
}

func (service *Service) SubmitSimulation(ctx context.Context, request *pb.SubmitSimulationRequest) (*pb.SubmitSimulationResponse, error) {
	if service.simulations == nil {
		return nil, fmt.Errorf("simulation capability unavailable")
	}
	run, err := service.simulations.submit(ctx, request)
	return &pb.SubmitSimulationResponse{Run: run}, err
}
func (service *Service) InspectSimulation(ctx context.Context, request *pb.InspectSimulationRequest) (*pb.InspectSimulationResponse, error) {
	if service.simulations == nil {
		return nil, fmt.Errorf("simulation capability unavailable")
	}
	run, err := service.simulations.inspect(ctx, request.GetRunId())
	if err == nil && service.simulations.local != nil {
		run.Artifacts = service.simulations.local.artifactViews(run.RunId, run.Simulator)
	}
	return &pb.InspectSimulationResponse{Run: run}, err
}
func (service *Service) ListSimulations(ctx context.Context, request *pb.ListSimulationsRequest) (*pb.ListSimulationsResponse, error) {
	if service.simulations == nil {
		return nil, fmt.Errorf("simulation capability unavailable")
	}
	if request == nil {
		return nil, fmt.Errorf("list request required")
	}
	if err := pb.ValidateListSimulationsRequest(request); err != nil {
		return nil, err
	}
	listed, err := service.simulations.ledger.List(ctx, int32(request.Limit))
	if err != nil {
		return nil, err
	}
	result := &pb.ListSimulationsResponse{}
	for _, job := range listed {
		result.Runs = append(result.Runs, simulationRun(job))
	}
	return result, nil
}
func (service *Service) RecordSimulationEvents(ctx context.Context, request *pb.RecordSimulationEventsRequest) (*pb.RecordSimulationEventsResponse, error) {
	if service.simulations == nil {
		return nil, fmt.Errorf("simulation capability unavailable")
	}
	if request == nil {
		return nil, fmt.Errorf("events required")
	}
	job, err := service.simulations.ledger.Get(ctx, request.RunId)
	if err != nil {
		return nil, err
	}
	if job.Executor != simulationExecutorLocal {
		return nil, fmt.Errorf("AWS observations are collected from the configured provider log stream")
	}
	observation, err := service.simulations.observe(job, request.Events)
	if err != nil {
		return nil, err
	}
	if err := service.simulations.ledger.Record(ctx, request.RunId, observation); err != nil {
		return nil, err
	}
	run, err := service.simulations.inspect(ctx, request.RunId)
	return &pb.RecordSimulationEventsResponse{Run: run}, err
}
func (service *Service) CancelSimulation(ctx context.Context, request *pb.CancelSimulationRequest) (*pb.CancelSimulationResponse, error) {
	if service.simulations == nil {
		return nil, fmt.Errorf("simulation capability unavailable")
	}
	if _, err := service.simulations.ledger.Cancel(ctx, request.GetRunId()); err != nil {
		return nil, err
	}
	run, err := service.simulations.inspect(ctx, request.GetRunId())
	return &pb.CancelSimulationResponse{Run: run}, err
}

func (simulations *Simulations) submit(ctx context.Context, request *pb.SubmitSimulationRequest) (*pb.SimulationRun, error) {
	if request == nil {
		return nil, fmt.Errorf("submission required")
	}
	if err := pb.ValidateSubmitSimulationRequest(request); err != nil {
		return nil, err
	}
	kind, executor := simulationKinds[request.Simulator], simulationExecutors[request.Executor]
	if !simulationID.MatchString(request.RunId) || kind == "" {
		return nil, fmt.Errorf("valid run identity and simulator required")
	}
	if executor == "" {
		return nil, fmt.Errorf("local or AWS Batch executor required")
	}
	admission := jobs.Admission[SimulationSpec]{ID: request.RunId, Kind: kind, Executor: executor, Spec: SimulationSpec{CaptureEvery: request.CaptureEvery}, TotalUnits: int64(request.Steps), Timeout: simulationDefaultTimeout}
	if executor == simulationExecutorLocal && simulations.local != nil {
		profile := simulations.local.profile(request.Simulator)
		if profile == nil {
			return nil, fmt.Errorf("local simulator profile unavailable")
		}
		admission.Managed = true
		admission.Image = profile.Image
		admission.Target = simulations.local.config.Network
		admission.Spec.ArtifactVolume = profile.ArtifactVolume
		admission.ArtifactURI = strings.TrimRight(profile.ArtifactUrl, "/") + "/" + request.RunId + "/"
		admission.Timeout = time.Duration(simulations.local.config.TimeoutSeconds) * time.Second
	}
	if executor == simulationExecutorAWSBatch {
		if simulations.config == nil {
			return nil, fmt.Errorf("AWS Batch is not configured; set the operator's simulation-config first")
		}
		for _, profile := range simulations.config.Profiles {
			if profile.Simulator == request.Simulator {
				admission.Image = profile.JobDefinition
				admission.ReservationUSDMicros = profile.ReservationUsdMicros
			}
		}
		if admission.Image == "" {
			return nil, fmt.Errorf("no configured Batch profile for simulator")
		}
		admission.Managed = true
		admission.Target = simulations.config.JobQueue
		admission.ArtifactURI = strings.TrimRight(simulations.config.ArtifactUri, "/") + "/" + request.RunId + "/"
		admission.Timeout = time.Duration(simulations.config.TimeoutSeconds) * time.Second
	}
	job, err := simulations.ledger.Submit(ctx, admission)
	if err != nil {
		return nil, err
	}
	return simulationRun(job), nil
}

func (simulations *Simulations) inspect(ctx context.Context, runID string) (*pb.SimulationRun, error) {
	job, err := simulations.ledger.Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	result := simulationRun(job)
	measurements, err := simulations.ledger.Measurements(ctx, runID)
	if err != nil {
		return nil, err
	}
	for _, measurement := range measurements {
		result.LatestMeasurements = append(result.LatestMeasurements, simulationMeasurement(runID, measurement))
	}
	return result, nil
}

// Work runs one bounded queue consumer in the caller's Go runtime. SubmitJob has
// no idempotency token: an uncertain result is retained, never blindly retried.
func (simulations *Simulations) Work(ctx context.Context) error {
	if simulations.traces != nil {
		if err := simulations.traces.Start(ctx); err != nil {
			return err
		}
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), simulationTraceStopDeadline)
			defer cancel()
			_ = simulations.traces.Stop(closeCtx)
		}()
	}
	if simulations.config == nil && simulations.local == nil {
		return nil
	}
	ticker := time.NewTicker(simulationPollInterval)
	defer ticker.Stop()
	for {
		if err := simulations.Tick(ctx); err != nil && ctx.Err() == nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Tick is the bounded worker iteration used by integration checks and Work:
// one ledger reconciliation pass, then the local log and trace projections.
func (simulations *Simulations) Tick(ctx context.Context) error {
	if err := simulations.ledger.Reconcile(ctx); err != nil {
		return err
	}
	if simulations.local == nil {
		return nil
	}
	if err := simulations.archiveSimulationLogs(ctx); err != nil {
		return err
	}
	return simulations.exportSimulationTrace(ctx)
}
