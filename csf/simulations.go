package csf

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	batchtypes "github.com/aws/aws-sdk-go-v2/service/batch/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	db "github.com/candacelabs/csf/csf/internal/brainspinedb"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// IBatch is the consumer seam over the upstream generated AWS SDK.
type IBatch interface {
	SubmitJob(ctx context.Context, input *batch.SubmitJobInput, options ...func(options *batch.Options)) (*batch.SubmitJobOutput, error)
	DescribeJobs(ctx context.Context, input *batch.DescribeJobsInput, options ...func(options *batch.Options)) (*batch.DescribeJobsOutput, error)
	TerminateJob(ctx context.Context, input *batch.TerminateJobInput, options ...func(options *batch.Options)) (*batch.TerminateJobOutput, error)
}

// ISimulationLogs reads the job's CloudWatch stream without an inbound worker listener.
type ISimulationLogs interface {
	GetLogEvents(ctx context.Context, input *cloudwatchlogs.GetLogEventsInput, options ...func(options *cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error)
}

const simulationCampaign = "csf-simulator-examples-v1"
const simulationBudgetMicros int64 = 300000000
const simulationProgressMetric = "simulation_steps_completed"
const simulationEventPrefix = "CSF_EVENT "
const simulationJobPrefix = "csf-"
const simulationPollInterval = 5 * time.Second

var simulationID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$`)
var simulationDefinition = regexp.MustCompile(`^arn:[a-z-]+:batch:[a-z0-9-]+:[0-9]{12}:job-definition/[A-Za-z0-9_-]+:[1-9][0-9]*$`)

// Simulations owns job admission and observation; the application owns its worker lifetime.
type Simulations struct {
	store     *Postgres
	artifacts *Artifacts
	batch     IBatch
	logs      ISimulationLogs
	config    *pb.SimulationConfig
	local     *LocalSimulations
	traces    ISimulationTraces
	logSearch *OpenSearch
}

func NewSimulations(store *Postgres, artifacts *Artifacts, config *pb.SimulationConfig, provider IBatch, logs ISimulationLogs, options ...SimulationOption) (*Simulations, error) {
	if store == nil || artifacts == nil {
		return nil, fmt.Errorf("simulations require PostgreSQL and retained artifacts")
	}
	if config != nil {
		if err := pb.ValidateSimulationConfig(config); err != nil {
			return nil, err
		}
		uri, err := url.Parse(config.ArtifactUri)
		if err != nil || uri.Scheme != "s3" || uri.Host == "" || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" || strings.Contains(uri.Path, "..") {
			return nil, fmt.Errorf("simulation artifact_uri must be an S3 prefix")
		}
		if provider == nil || logs == nil || config.Region == "" || config.JobQueue == "" || config.LogGroup == "" || len(config.Profiles) == 0 {
			return nil, fmt.Errorf("AWS simulations require region, queue, log group, profiles and SDK clients")
		}
		seen := map[pb.Simulator]bool{}
		queue, err := arn.Parse(config.JobQueue)
		if err != nil || queue.Service != "batch" || queue.Region != config.Region || !strings.HasPrefix(queue.Resource, "job-queue/") || queue.AccountID == "" {
			return nil, fmt.Errorf("job_queue must be an AWS Batch ARN in the configured region")
		}
		for _, profile := range config.Profiles {
			if profile == nil || seen[profile.Simulator] || (profile.Simulator != pb.Simulator_SIMULATOR_CARLA && profile.Simulator != pb.Simulator_SIMULATOR_ISAAC) || !simulationDefinition.MatchString(profile.JobDefinition) {
				return nil, fmt.Errorf("each simulator needs one revision-pinned Batch job definition")
			}
			if err := pb.ValidateSimulationProfile(profile); err != nil {
				return nil, err
			}
			definition, err := arn.Parse(profile.JobDefinition)
			if err != nil || definition.Region != queue.Region || definition.AccountID != queue.AccountID {
				return nil, fmt.Errorf("job definition and queue must use the same AWS account and region")
			}
			if profile.ReservationUsdMicros > config.BudgetUsdMicros {
				return nil, fmt.Errorf("profile reservation exceeds campaign budget")
			}
			seen[profile.Simulator] = true
		}
	}
	result := &Simulations{store: store, artifacts: artifacts, config: proto.CloneOf(config), batch: provider, logs: logs}
	for _, option := range options {
		option(result)
	}
	if result.local != nil && result.local.config.LogIndex != "" && result.logSearch == nil {
		return nil, fmt.Errorf("simulation log archival requires the shared OpenSearch client")
	}
	return result, nil
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
	rows, err := service.simulations.store.queries.ListSimulations(ctx, int32(request.Limit))
	if err != nil {
		return nil, err
	}
	result := &pb.ListSimulationsResponse{}
	for _, row := range rows {
		result.Runs = append(result.Runs, simulationViews.Run(row))
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
	row, err := service.simulations.store.queries.GetSimulation(ctx, request.RunId)
	if err != nil {
		return nil, err
	}
	if row.Executor != int32(pb.SimulationExecutor_SIMULATION_EXECUTOR_LOCAL) {
		return nil, fmt.Errorf("AWS observations are collected from the configured provider log stream")
	}
	err = service.simulations.record(ctx, request.RunId, request.Events)
	if err != nil {
		return nil, err
	}
	run, err := service.simulations.inspect(ctx, request.RunId)
	return &pb.RecordSimulationEventsResponse{Run: run}, err
}
func (service *Service) CancelSimulation(ctx context.Context, request *pb.CancelSimulationRequest) (*pb.CancelSimulationResponse, error) {
	if service.simulations == nil {
		return nil, fmt.Errorf("simulation capability unavailable")
	}
	_, err := service.simulations.store.queries.RequestSimulationCancellation(ctx, request.GetRunId())
	if err != nil {
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
	if !simulationID.MatchString(request.RunId) || (request.Simulator != pb.Simulator_SIMULATOR_CARLA && request.Simulator != pb.Simulator_SIMULATOR_ISAAC) {
		return nil, fmt.Errorf("valid run identity and simulator required")
	}
	if request.Executor != pb.SimulationExecutor_SIMULATION_EXECUTOR_LOCAL && request.Executor != pb.SimulationExecutor_SIMULATION_EXECUTOR_AWS_BATCH {
		return nil, fmt.Errorf("local or AWS Batch executor required")
	}
	budget := simulationBudgetMicros
	input := db.InsertSimulationParams{RunID: request.RunId, CampaignID: simulationCampaign, Simulator: int32(request.Simulator), Executor: int32(request.Executor), Steps: int32(request.Steps), TimeoutSeconds: 900, CaptureEvery: int32(request.CaptureEvery)}
	if request.Executor == pb.SimulationExecutor_SIMULATION_EXECUTOR_LOCAL && simulations.local != nil {
		profile := simulations.local.profile(request.Simulator)
		if profile == nil {
			return nil, fmt.Errorf("local simulator profile unavailable")
		}
		input.Managed = true
		input.JobDefinition = profile.Image
		input.JobQueue = simulations.local.config.Network
		input.ArtifactVolume = profile.ArtifactVolume
		input.ArtifactUri = strings.TrimRight(profile.ArtifactUrl, "/") + "/" + request.RunId + "/"
		input.TimeoutSeconds = int32(simulations.local.config.TimeoutSeconds)
	}
	if request.Executor == pb.SimulationExecutor_SIMULATION_EXECUTOR_AWS_BATCH {
		if simulations.config == nil {
			return nil, fmt.Errorf("AWS Batch is not configured; set the operator's simulation-config first")
		}
		for _, profile := range simulations.config.Profiles {
			if profile.Simulator == request.Simulator {
				input.JobDefinition = profile.JobDefinition
				input.ReservationUsdMicros = profile.ReservationUsdMicros
			}
		}
		if input.JobDefinition == "" {
			return nil, fmt.Errorf("no configured Batch profile for simulator")
		}
		input.JobQueue = simulations.config.JobQueue
		input.ArtifactUri = strings.TrimRight(simulations.config.ArtifactUri, "/") + "/" + request.RunId + "/"
		input.TimeoutSeconds = int32(simulations.config.TimeoutSeconds)
	}
	if simulations.config != nil {
		budget = simulations.config.BudgetUsdMicros
	}
	budgetBytes, err := protojson.Marshal(&pb.SimulationConfig{BudgetUsdMicros: budget})
	if err != nil {
		return nil, err
	}
	_, budgetRef, err := simulations.artifacts.Put(budgetBytes)
	if err != nil {
		return nil, err
	}
	tx, err := simulations.store.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := simulations.store.queries.WithTx(tx)
	// The one campaign row serializes admission, including concurrent retries.
	_, err = queries.CreateRun(ctx, db.CreateRunParams{RunID: simulationCampaign, Name: simulationCampaign, ConfigurationArtifactRef: budgetRef, BudgetLimitUsdMicros: budget})
	if err != nil {
		return nil, fmt.Errorf("simulation budget is immutable after first admission: %w", err)
	}
	if _, err = queries.LockSimulationBudget(ctx, simulationCampaign); err != nil {
		return nil, err
	}
	existing, err := queries.GetSimulation(ctx, request.RunId)
	if err == nil {
		if existing.Simulator != input.Simulator || existing.Executor != input.Executor || existing.Steps != input.Steps || existing.CaptureEvery != input.CaptureEvery {
			return nil, fmt.Errorf("run identity reused with different inputs")
		}
		return simulationViews.Run(existing), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	reserved, err := queries.ReserveSimulationBudget(ctx, db.ReserveSimulationBudgetParams{CampaignID: simulationCampaign, Amount: input.ReservationUsdMicros})
	if err != nil {
		return nil, err
	}
	if reserved != 1 {
		return nil, fmt.Errorf("simulation admission budget exhausted")
	}
	row, err := queries.InsertSimulation(ctx, input)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return simulationViews.Run(row), nil
}

func (simulations *Simulations) inspect(ctx context.Context, runID string) (*pb.SimulationRun, error) {
	row, err := simulations.store.queries.GetSimulation(ctx, runID)
	if err != nil {
		return nil, err
	}
	result := simulationViews.Run(row)
	measurements, err := simulations.store.queries.LatestSimulationMeasurements(ctx, runID)
	if err != nil {
		return nil, err
	}
	for _, measurement := range measurements {
		result.LatestMeasurements = append(result.LatestMeasurements, simulationViews.Measurement(measurement))
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
			closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

// Tick is the bounded worker iteration used by integration checks and Work.
func (simulations *Simulations) Tick(ctx context.Context) error {
	if simulations.local != nil {
		if err := simulations.tickLocal(ctx); err != nil {
			return err
		}
		if err := simulations.archiveSimulationLogs(ctx); err != nil {
			return err
		}
		if err := simulations.exportSimulationTrace(ctx); err != nil {
			return err
		}
	}
	if simulations.config == nil {
		return nil
	}
	if err := simulations.store.queries.MarkInterruptedSubmissions(ctx); err != nil {
		return err
	}
	row, err := simulations.store.queries.ClaimSimulation(ctx)
	if err == nil {
		deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
		result, submitErr := simulations.batch.SubmitJob(deadline, &batch.SubmitJobInput{
			JobName: aws.String(simulationJobPrefix + row.RunID), JobQueue: aws.String(row.JobQueue), JobDefinition: aws.String(row.JobDefinition),
			RetryStrategy: &batchtypes.RetryStrategy{Attempts: aws.Int32(1)}, Timeout: &batchtypes.JobTimeout{AttemptDurationSeconds: aws.Int32(row.TimeoutSeconds)},
			Tags: map[string]string{"csf-run-id": row.RunID},
			ContainerOverrides: &batchtypes.ContainerOverrides{Environment: []batchtypes.KeyValuePair{
				{Name: aws.String("CSF_RUN_ID"), Value: aws.String(row.RunID)}, {Name: aws.String("CSF_STEPS"), Value: aws.String(strconv.Itoa(int(row.Steps)))}, {Name: aws.String("CSF_ARTIFACT_URI"), Value: aws.String(row.ArtifactUri)},
				{Name: aws.String("CSF_CAPTURE_EVERY"), Value: aws.String(strconv.Itoa(int(row.CaptureEvery)))},
			}},
		}, func(options *batch.Options) { options.RetryMaxAttempts = 1 })
		cancel()
		state, jobID, reason := db.BrainspineSimulationStateQueued, "", ""
		if submitErr != nil {
			state = db.BrainspineSimulationStateSubmissionUnknown
			reason = submitErr.Error()
		} else if result == nil || aws.ToString(result.JobId) == "" {
			state = db.BrainspineSimulationStateSubmissionUnknown
			reason = "AWS returned no job identity"
		} else {
			jobID = aws.ToString(result.JobId)
		}
		if _, err := simulations.store.queries.FinishSimulationSubmission(ctx, db.FinishSimulationSubmissionParams{RunID: row.RunID, State: state, JobID: jobID, Reason: reason}); err != nil {
			return err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	rows, err := simulations.store.queries.PollableSimulations(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
		problem := simulations.poll(deadline, row)
		cancel()
		if problem != nil {
			if err := simulations.store.queries.SetSimulationInspectionError(ctx, db.SetSimulationInspectionErrorParams{RunID: row.RunID, InspectionError: problem.Error()}); err != nil {
				return err
			}
		}
	}
	return nil
}
