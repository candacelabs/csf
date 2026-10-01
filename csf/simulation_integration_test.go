//go:build integration

package csf_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	bt "github.com/aws/aws-sdk-go-v2/service/batch/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	lt "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/candacelabs/csf/csf"
	mocks "github.com/candacelabs/csf/csf/internal/mocks"
	"github.com/candacelabs/csf/pkg/httpserver"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/containerd/errdefs"
	"github.com/gin-gonic/gin"
	"github.com/moby/moby/api/types/container"
	dc "github.com/moby/moby/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("simulator examples through PostgreSQL and generated transports", func() {
	var ctx context.Context
	var worker *csf.Simulations
	var client *csf.Client
	var provider *mocks.MockIBatch
	var logs *mocks.MockISimulationLogs
	var policy *pb.SimulationConfig
	var service *csf.Service
	var server *httptest.Server
	var store *csf.Postgres
	var artifacts *csf.Artifacts
	BeforeEach(func() {
		ctx = context.Background()
		fixture := buildCSFPostgresFixture(ctx)
		store = fixture.store
		var err error
		artifacts, err = csf.NewArtifacts(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(artifacts.Close)
		control := gomock.NewController(GinkgoT())
		provider = mocks.NewMockIBatch(control)
		logs = mocks.NewMockISimulationLogs(control)
		policy = &pb.SimulationConfig{Region: "us-east-1", JobQueue: "arn:aws:batch:us-east-1:123456789012:job-queue/simulation", ArtifactUri: "s3://example-simulation/runs", LogGroup: "/aws/batch/job", TimeoutSeconds: 600, BudgetUsdMicros: 300000000, Profiles: []*pb.SimulationProfile{{Simulator: pb.Simulator_SIMULATOR_CARLA, JobDefinition: "arn:aws:batch:us-east-1:123456789012:job-definition/carla:1", ReservationUsdMicros: 200000000}}}
		worker, err = csf.NewSimulations(store, artifacts, policy, provider, logs)
		Expect(err).NotTo(HaveOccurred())
		service, err = csf.New(csf.WithSimulations(worker))
		Expect(err).NotTo(HaveOccurred())
		router := httpserver.NewEngine("simulation-integration")
		service.Register(router)
		router.Any("/mcp", gin.WrapH(service.MCPHandler()))
		csf.NewInspection(csf.WithInspectionSimulations(worker)).Register(router)
		server = httptest.NewServer(router)
		DeferCleanup(server.Close)
		client, err = csf.NewClient(server.URL, server.Client())
		Expect(err).NotTo(HaveOccurred())
	})
	It("registers local work, rejects false completion and exposes actual progress through MCP", func() {
		_, err := client.SubmitSimulation(ctx, &pb.SubmitSimulationRequest{RunId: "local", Simulator: pb.Simulator_SIMULATOR_ISAAC, Executor: pb.SimulationExecutor_SIMULATION_EXECUTOR_LOCAL, Steps: 2})
		Expect(err).NotTo(HaveOccurred())
		_, err = client.RecordSimulationEvents(ctx, &pb.RecordSimulationEventsRequest{RunId: "local", Events: []*pb.ResearchEvent{simStatus("local", "completed")}})
		Expect(err).To(HaveOccurred())
		update, err := client.RecordSimulationEvents(ctx, &pb.RecordSimulationEventsRequest{RunId: "local", Events: []*pb.ResearchEvent{simDefinition(), simStatus("local", "started"), simProgress("local", 1)}})
		Expect(err).NotTo(HaveOccurred())
		Expect(update.Run.CompletedSteps).To(Equal(uint32(1)))
		Expect(update.Run.State).To(Equal(pb.SimulationState_SIMULATION_STATE_RUNNING))
		metrics, err := server.Client().Get(server.URL + "/metrics")
		Expect(err).NotTo(HaveOccurred())
		body, err := io.ReadAll(metrics.Body)
		Expect(metrics.Body.Close()).To(Succeed())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring(`candace_holygrail_simulation_latest_progress_ratio{executor="local",simulator="isaac"} 0.5`))
		Expect(string(body)).To(ContainSubstring("candace_holygrail_simulation_collection_readable 1"))
		_, err = client.RecordSimulationEvents(ctx, &pb.RecordSimulationEventsRequest{RunId: "local", Events: []*pb.ResearchEvent{simProgress("another", 2)}})
		Expect(err).To(HaveOccurred())
		session, err := mcp.NewClient(&mcp.Implementation{Name: "simulator-agent", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp"}, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(session.Close)
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "InspectSimulation", Arguments: map[string]string{"runId": "local"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeFalse())
		observed := &pb.InspectSimulationResponse{}
		Expect(protojson.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), observed)).To(Succeed())
		Expect(observed.Run.CompletedSteps).To(Equal(uint32(1)))
		listed, err := client.ListSimulations(ctx, &pb.ListSimulationsRequest{Limit: 10})
		Expect(err).NotTo(HaveOccurred())
		Expect(listed.Runs).To(HaveLen(1))
		update, err = client.RecordSimulationEvents(ctx, &pb.RecordSimulationEventsRequest{RunId: "local", Events: []*pb.ResearchEvent{simProgress("local", 2), simStatus("local", "completed")}})
		Expect(err).NotTo(HaveOccurred())
		Expect(update.Run.State).To(Equal(pb.SimulationState_SIMULATION_STATE_SUCCEEDED))
		update, err = client.RecordSimulationEvents(ctx, &pb.RecordSimulationEventsRequest{RunId: "local", Events: []*pb.ResearchEvent{simStatus("local", "failed"), simStatus("local", "completed")}})
		Expect(err).NotTo(HaveOccurred())
		Expect(update.Run.State).To(Equal(pb.SimulationState_SIMULATION_STATE_FAILED))
		Expect(update.Run.CleanupConfirmed).To(BeFalse())
	})
	It("serializes competing admissions without exceeding the retained budget", func() {
		results := make(chan error, 2)
		for _, identity := range []string{"concurrent-a", "concurrent-b"} {
			go func(runID string) {
				_, err := client.SubmitSimulation(ctx, &pb.SubmitSimulationRequest{RunId: runID, Simulator: pb.Simulator_SIMULATOR_CARLA, Executor: pb.SimulationExecutor_SIMULATION_EXECUTOR_AWS_BATCH, Steps: 2})
				results <- err
			}(identity)
		}
		admitted := 0
		for range 2 {
			if <-results == nil {
				admitted++
			}
		}
		Expect(admitted).To(Equal(1))
		listed, err := client.ListSimulations(ctx, &pb.ListSimulationsRequest{Limit: 10})
		Expect(err).NotTo(HaveOccurred())
		Expect(listed.Runs).To(HaveLen(1))
		Expect(listed.Runs[0].ReservationUsdMicros).To(Equal(int64(200000000)))
	})
	It("admits once, reserves the budget and confirms cleanup only after AWS termination", func() {
		request := &pb.SubmitSimulationRequest{RunId: "cloud", Simulator: pb.Simulator_SIMULATOR_CARLA, Executor: pb.SimulationExecutor_SIMULATION_EXECUTOR_AWS_BATCH, Steps: 2}
		submitted, err := client.SubmitSimulation(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(submitted.Run.State).To(Equal(pb.SimulationState_SIMULATION_STATE_PENDING))
		_, err = client.SubmitSimulation(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		_, err = client.SubmitSimulation(ctx, &pb.SubmitSimulationRequest{RunId: "too-expensive", Simulator: request.Simulator, Executor: request.Executor, Steps: 2})
		Expect(err).To(HaveOccurred())
		job := bt.JobDetail{JobId: aws.String("job-1"), JobQueue: aws.String(policy.JobQueue), JobDefinition: aws.String(policy.Profiles[0].JobDefinition), Tags: map[string]string{"csf-run-id": "cloud"}, Status: bt.JobStatusRunning, Container: &bt.ContainerDetail{LogStreamName: aws.String("stream")}}
		provider.EXPECT().SubmitJob(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(callContext context.Context, input *batch.SubmitJobInput, options ...func(options *batch.Options)) (*batch.SubmitJobOutput, error) {
			Expect(*input.JobName).To(Equal("csf-cloud"))
			Expect(*input.RetryStrategy.Attempts).To(Equal(int32(1)))
			Expect(*input.Timeout.AttemptDurationSeconds).To(Equal(int32(600)))
			optionsValue := batch.Options{}
			options[0](&optionsValue)
			Expect(optionsValue.RetryMaxAttempts).To(Equal(1))
			Expect(input.ContainerOverrides.Environment).To(ContainElement(bt.KeyValuePair{Name: aws.String("CSF_RUN_ID"), Value: aws.String("cloud")}))
			return &batch.SubmitJobOutput{JobId: aws.String("job-1")}, nil
		})
		provider.EXPECT().DescribeJobs(gomock.Any(), gomock.Any()).Return(&batch.DescribeJobsOutput{Jobs: []bt.JobDetail{job}}, nil)
		definition, err := protojson.Marshal(simDefinition())
		Expect(err).NotTo(HaveOccurred())
		measurement, err := protojson.Marshal(simProgress("cloud", 1))
		Expect(err).NotTo(HaveOccurred())
		logs.EXPECT().GetLogEvents(gomock.Any(), gomock.Any()).Return(&cloudwatchlogs.GetLogEventsOutput{Events: []lt.OutputLogEvent{{Message: aws.String("CSF_EVENT " + string(definition))}, {Message: aws.String("CSF_EVENT " + string(measurement))}}, NextForwardToken: aws.String("page-1")}, nil)
		Expect(worker.Tick(ctx)).To(Succeed())
		observed, err := client.InspectSimulation(ctx, &pb.InspectSimulationRequest{RunId: "cloud"})
		Expect(err).NotTo(HaveOccurred())
		Expect(observed.Run.CompletedSteps).To(Equal(uint32(1)))
		Expect(observed.Run.CleanupConfirmed).To(BeFalse())
		_, err = client.CancelSimulation(ctx, &pb.CancelSimulationRequest{RunId: "cloud"})
		Expect(err).NotTo(HaveOccurred())
		provider.EXPECT().DescribeJobs(gomock.Any(), gomock.Any()).Return(&batch.DescribeJobsOutput{Jobs: []bt.JobDetail{job}}, nil)
		provider.EXPECT().TerminateJob(gomock.Any(), gomock.Any()).Return(&batch.TerminateJobOutput{}, nil)
		logs.EXPECT().GetLogEvents(gomock.Any(), gomock.Any()).Return(&cloudwatchlogs.GetLogEventsOutput{NextForwardToken: aws.String("page-1")}, nil)
		Expect(worker.Tick(ctx)).To(Succeed())
		observed, err = client.InspectSimulation(ctx, &pb.InspectSimulationRequest{RunId: "cloud"})
		Expect(err).NotTo(HaveOccurred())
		Expect(observed.Run.CleanupConfirmed).To(BeFalse())
		Expect(observed.Run.State).To(Equal(pb.SimulationState_SIMULATION_STATE_CANCELLING))
		job.Status = bt.JobStatusFailed
		job.StatusReason = aws.String("CSF operator cancellation")
		provider.EXPECT().DescribeJobs(gomock.Any(), gomock.Any()).Return(&batch.DescribeJobsOutput{Jobs: []bt.JobDetail{job}}, nil)
		logs.EXPECT().GetLogEvents(gomock.Any(), gomock.Any()).Return(&cloudwatchlogs.GetLogEventsOutput{NextForwardToken: aws.String("page-1")}, nil)
		Expect(worker.Tick(ctx)).To(Succeed())
		observed, err = client.InspectSimulation(ctx, &pb.InspectSimulationRequest{RunId: "cloud"})
		Expect(err).NotTo(HaveOccurred())
		Expect(observed.Run.CleanupConfirmed).To(BeTrue())
		Expect(observed.Run.State).To(Equal(pb.SimulationState_SIMULATION_STATE_CANCELLED))
	})
	It("retains an ambiguous submission and never retries a possibly paid job", func() {
		request := &pb.SubmitSimulationRequest{RunId: "ambiguous", Simulator: pb.Simulator_SIMULATOR_CARLA, Executor: pb.SimulationExecutor_SIMULATION_EXECUTOR_AWS_BATCH, Steps: 2}
		_, err := client.SubmitSimulation(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		provider.EXPECT().SubmitJob(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("response lost after request sent"))
		Expect(worker.Tick(ctx)).To(Succeed())
		Expect(worker.Tick(ctx)).To(Succeed())
		observed, err := client.InspectSimulation(ctx, &pb.InspectSimulationRequest{RunId: "ambiguous"})
		Expect(err).NotTo(HaveOccurred())
		Expect(observed.Run.State).To(Equal(pb.SimulationState_SIMULATION_STATE_SUBMISSION_UNKNOWN))
		Expect(observed.Run.ReservationUsdMicros).To(Equal(int64(200000000)))
		_, err = client.SubmitSimulation(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(worker.Tick(ctx)).To(Succeed())
	})
	Context("host-owned local jobs", func() {
		var docker *mocks.MockIDockerSimulations
		var observed dc.ContainerInspectResult
		var localConfig *pb.LocalSimulationConfig
		BeforeEach(func() {
			docker = mocks.NewMockIDockerSimulations(gomock.NewController(GinkgoT()))
			image := "sha256:" + strings.Repeat("a", 64)
			localConfig = &pb.LocalSimulationConfig{Network: "simulation-test", ProgressUrl: server.URL + "/api/simulation/events", TimeoutSeconds: 600,
				Profiles: []*pb.LocalSimulationProfile{{Simulator: pb.Simulator_SIMULATOR_CARLA, Image: image, ArtifactVolume: "simulation-artifacts", ArtifactDirectory: GinkgoT().TempDir(), ArtifactUrl: "http://example.invalid/ui/runs"}}}
			local, err := csf.NewLocalSimulations(docker, localConfig)
			Expect(err).NotTo(HaveOccurred())
			worker, err = csf.NewSimulations(store, artifacts, nil, nil, nil, csf.WithLocalSimulations(local))
			Expect(err).NotTo(HaveOccurred())
			csf.WithSimulations(worker)(service)
			observed = dc.ContainerInspectResult{Container: container.InspectResponse{ID: "owned-id", Image: image, State: &container.State{Status: container.StateCreated}, Config: &container.Config{Labels: map[string]string{"candace.owner": "candace-brain-simulator", "candace.run-id": "native"}}}, Raw: []byte(`{"Id":"owned-id"}`)}
		})
		DescribeTable("archives terminal jobs and guards immutable trace delivery without local files", func(exportError error, changeDestination bool) {
			control := gomock.NewController(GinkgoT())
			sdk := mocks.NewMockIOpenSearchClient(control)
			traces := mocks.NewMockISimulationTraces(control)
			search, err := csf.NewOpenSearch("brain-knowledge", "", sdk)
			Expect(err).NotTo(HaveOccurred())
			localConfig.LogIndex, localConfig.TraceBaseUrl = "brain-logs", "http://example.invalid/traces/"
			local, err := csf.NewLocalSimulations(docker, localConfig)
			Expect(err).NotTo(HaveOccurred())
			worker, err = csf.NewSimulations(store, artifacts, nil, nil, nil, csf.WithLocalSimulations(local), csf.WithSimulationLogSearch(search))
			Expect(err).NotTo(HaveOccurred())
			csf.WithSimulations(worker)(service)
			runDirectory := filepath.Join(localConfig.Profiles[0].ArtifactDirectory, "native")
			Expect(os.Mkdir(runDirectory, 0700)).To(Succeed())
			var eventLines []string
			for _, event := range []*pb.ResearchEvent{simProgress("native", 1), simProgress("native", 2), simStatus("native", "completed")} {
				content, err := protojson.Marshal(event)
				Expect(err).NotTo(HaveOccurred())
				eventLines = append(eventLines, string(content))
			}
			Expect(os.WriteFile(filepath.Join(runDirectory, "events.jsonl"), []byte(strings.Join(eventLines, "\n")+"\n"), 0600)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(runDirectory, "trace.jsonl"), []byte("{\"step\":0}\n{\"step\":1}\n"), 0600)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(runDirectory, "manifest.json"), []byte(`{"status":"completed"}`), 0600)).To(Succeed())
			var archived []byte
			sdk.EXPECT().Index(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request opensearchapi.IndexReq) (*opensearchapi.IndexResp, error) {
				Expect(request.Index).To(Equal("brain-logs"))
				archived, err = io.ReadAll(request.Body)
				Expect(err).NotTo(HaveOccurred())
				record := &pb.SimulationLogRecord{}
				Expect(protojson.Unmarshal(archived, record)).To(Succeed())
				Expect(record.Simulation.Logs).To(Equal("native worker complete\n"))
				Expect(record.Simulation.Run.CleanupConfirmed).To(BeTrue())
				return &opensearchapi.IndexResp{}, nil
			})
			exports, searches := 1, 2
			if changeDestination {
				exports, searches = 2, 4
			}
			traces.EXPECT().UploadTraces(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, spans []*tracepb.ResourceSpans) error {
				Expect(spans).To(HaveLen(1))
				Expect(spans[0].ScopeSpans[0].Spans).To(HaveLen(3))
				Expect(spans[0].ScopeSpans[0].Spans[1].Name).To(Equal("Simulation step 1"))
				return exportError
			}).Times(exports)
			accepted, err := client.SubmitSimulation(ctx, &pb.SubmitSimulationRequest{RunId: "native", Simulator: pb.Simulator_SIMULATOR_CARLA, Executor: pb.SimulationExecutor_SIMULATION_EXECUTOR_LOCAL, Steps: 2, CaptureEvery: 1})
			Expect(err).NotTo(HaveOccurred())
			Expect(accepted.Run.Managed).To(BeTrue())
			gomock.InOrder(
				docker.EXPECT().ContainerInspect(gomock.Any(), "csf-simulator-native", gomock.Any()).Return(dc.ContainerInspectResult{}, errdefs.ErrNotFound),
				docker.EXPECT().ContainerCreate(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, options dc.ContainerCreateOptions) (dc.ContainerCreateResult, error) {
					Expect(options.Name).To(Equal("csf-simulator-native"))
					Expect(options.Config.Image).To(Equal(observed.Container.Image))
					Expect(options.Config.Cmd).To(ContainElement("--capture-every"))
					Expect(options.HostConfig.Mounts[0].Source).To(Equal("simulation-artifacts"))
					Expect(options.HostConfig.PortBindings).To(BeEmpty())
					Expect(options.HostConfig.Mounts).To(HaveLen(1))
					return dc.ContainerCreateResult{ID: "owned-id"}, nil
				}),
				docker.EXPECT().ContainerInspect(gomock.Any(), "csf-simulator-native", gomock.Any()).Return(observed, nil),
				docker.EXPECT().ContainerStart(gomock.Any(), "owned-id", gomock.Any()).Return(dc.ContainerStartResult{}, nil),
			)
			Expect(worker.Tick(ctx)).To(Succeed())
			_, err = client.RecordSimulationEvents(ctx, &pb.RecordSimulationEventsRequest{RunId: "native", Events: []*pb.ResearchEvent{simDefinition(), simProgress("native", 2), simStatus("native", "completed")}})
			Expect(err).NotTo(HaveOccurred())
			observed.Container.State = &container.State{Status: container.StateExited, ExitCode: 0}
			wire := strings.NewReader("\x01\x00\x00\x00\x00\x00\x00\x17native worker complete\n")
			gomock.InOrder(
				docker.EXPECT().ContainerInspect(gomock.Any(), "csf-simulator-native", gomock.Any()).Return(observed, nil),
				docker.EXPECT().ContainerLogs(gomock.Any(), "owned-id", gomock.Any()).Return(io.NopCloser(wire), nil),
				docker.EXPECT().ContainerRemove(gomock.Any(), "owned-id", dc.ContainerRemoveOptions{}).Return(dc.ContainerRemoveResult{}, nil),
			)
			Expect(worker.Tick(ctx)).To(Succeed())
			result, err := client.InspectSimulation(ctx, &pb.InspectSimulationRequest{RunId: "native"})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Run.CleanupConfirmed).To(BeTrue())
			Expect(result.Run.State).To(Equal(pb.SimulationState_SIMULATION_STATE_SUCCEEDED))
			log, err := client.ReadSimulationLogs(ctx, &pb.ReadSimulationLogsRequest{RunId: "native", MaxBytes: 1024})
			Expect(err).NotTo(HaveOccurred())
			Expect(log.Content).To(Equal("native worker complete\n"))
			Expect(log.Sha256).To(HaveLen(64))
			Expect(result.Run.LogDocumentId).To(HaveLen(64))
			Expect(result.Run.LogIndexedAt).NotTo(BeEmpty())
			Expect(result.Run.LogProjectionError).To(BeEmpty())
			Expect(os.RemoveAll(runDirectory)).To(Succeed())
			worker, err = csf.NewSimulations(store, artifacts, nil, nil, nil, csf.WithLocalSimulations(local), csf.WithSimulationLogSearch(search), csf.WithSimulationTraces(traces))
			Expect(err).NotTo(HaveOccurred())
			csf.WithSimulations(worker)(service)
			sdk.EXPECT().Search(gomock.Any(), gomock.Any()).Return(&opensearchapi.SearchResp{Hits: opensearchapi.SearchHitsMetadata{Hits: []opensearchapi.SearchHit{{Source: archived}}}}, nil).Times(searches)
			rebuilt, err := client.RebuildSimulationTrace(ctx, &pb.RebuildSimulationTraceRequest{RunId: "native"})
			if exportError != nil {
				Expect(err).To(HaveOccurred())
				_, err = client.RebuildSimulationTrace(ctx, &pb.RebuildSimulationTraceRequest{RunId: "native"})
				Expect(err).To(HaveOccurred()) // The uncertain attempt must never be sent twice.
				inspected, err := client.InspectSimulation(ctx, &pb.InspectSimulationRequest{RunId: "native"})
				Expect(err).NotTo(HaveOccurred())
				Expect(inspected.Run.TraceExportError).To(ContainSubstring("ambiguous"))
				Expect(inspected.Run.TraceUrl).To(BeEmpty())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(rebuilt.SourceSha256).To(HaveLen(64))
			Expect(rebuilt.Run.TraceUrl).To(HavePrefix("http://example.invalid/traces/"))
			repeated, err := client.RebuildSimulationTrace(ctx, &pb.RebuildSimulationTraceRequest{RunId: "native"})
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(repeated, rebuilt)).To(BeTrue()) // A receipt suppresses a second OTLP export.
			if changeDestination {
				for _, destination := range []string{"http://example.invalid/another-project/traces", localConfig.TraceBaseUrl} {
					config := proto.Clone(localConfig).(*pb.LocalSimulationConfig)
					config.TraceBaseUrl = destination
					other, err := csf.NewLocalSimulations(docker, config)
					Expect(err).NotTo(HaveOccurred())
					worker, err = csf.NewSimulations(store, artifacts, nil, nil, nil, csf.WithLocalSimulations(other), csf.WithSimulationLogSearch(search), csf.WithSimulationTraces(traces))
					Expect(err).NotTo(HaveOccurred())
					csf.WithSimulations(worker)(service)
					result, err := client.RebuildSimulationTrace(ctx, &pb.RebuildSimulationTraceRequest{RunId: "native"})
					Expect(err).NotTo(HaveOccurred())
					Expect(result.Run.TraceUrl).To(HavePrefix(strings.TrimRight(destination, "/") + "/"))
					Expect(result.SourceSha256).To(Equal(rebuilt.SourceSha256))
				}
			}
			Expect(worker.Tick(ctx)).To(Succeed()) // No provider call after cleanup.
		}, Entry("accepted export is not repeated", nil, false), Entry("lost reply is retained as ambiguous", errors.New("lost OTLP acknowledgement"), false), Entry("another destination has its own receipt and the original receipt survives", nil, true))
		It("recovers the same named container after a lost create response and refuses foreign ownership", func() {
			request := &pb.SubmitSimulationRequest{RunId: "native", Simulator: pb.Simulator_SIMULATOR_CARLA, Executor: pb.SimulationExecutor_SIMULATION_EXECUTOR_LOCAL, Steps: 2}
			_, err := client.SubmitSimulation(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			docker.EXPECT().ContainerInspect(gomock.Any(), "csf-simulator-native", gomock.Any()).Return(dc.ContainerInspectResult{}, errdefs.ErrNotFound)
			docker.EXPECT().ContainerCreate(gomock.Any(), gomock.Any()).Return(dc.ContainerCreateResult{}, errors.New("lost reply"))
			Expect(worker.Tick(ctx)).To(Succeed())
			result, err := client.InspectSimulation(ctx, &pb.InspectSimulationRequest{RunId: "native"})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Run.InspectionError).To(ContainSubstring("lost reply"))
			observed.Container.Config.Labels["candace.run-id"] = "foreign"
			docker.EXPECT().ContainerInspect(gomock.Any(), "csf-simulator-native", gomock.Any()).Return(observed, nil)
			Expect(worker.Tick(ctx)).To(Succeed())
			result, err = client.InspectSimulation(ctx, &pb.InspectSimulationRequest{RunId: "native"})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Run.InspectionError).To(ContainSubstring("ownership"))
			observed.Container.Config.Labels["candace.run-id"] = "native"
			docker.EXPECT().ContainerInspect(gomock.Any(), "csf-simulator-native", gomock.Any()).Return(observed, nil)
			docker.EXPECT().ContainerStart(gomock.Any(), "owned-id", gomock.Any()).Return(dc.ContainerStartResult{}, nil)
			Expect(worker.Tick(ctx)).To(Succeed()) // No second create.
		})
		It("requires stopped-container evidence and recovers an ambiguous removal", func() {
			_, err := client.SubmitSimulation(ctx, &pb.SubmitSimulationRequest{RunId: "native", Simulator: pb.Simulator_SIMULATOR_CARLA, Executor: pb.SimulationExecutor_SIMULATION_EXECUTOR_LOCAL, Steps: 2})
			Expect(err).NotTo(HaveOccurred())
			observed.Container.State = &container.State{Status: container.StateRunning, Running: true, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			docker.EXPECT().ContainerInspect(gomock.Any(), "csf-simulator-native", gomock.Any()).Return(observed, nil)
			Expect(worker.Tick(ctx)).To(Succeed())
			_, err = client.CancelSimulation(ctx, &pb.CancelSimulationRequest{RunId: "native"})
			Expect(err).NotTo(HaveOccurred())
			docker.EXPECT().ContainerInspect(gomock.Any(), "csf-simulator-native", gomock.Any()).Return(observed, nil)
			docker.EXPECT().ContainerStop(gomock.Any(), "owned-id", gomock.Any()).Return(dc.ContainerStopResult{}, nil)
			docker.EXPECT().ContainerInspect(gomock.Any(), "owned-id", gomock.Any()).Return(observed, nil)
			Expect(worker.Tick(ctx)).To(Succeed())
			result, err := client.InspectSimulation(ctx, &pb.InspectSimulationRequest{RunId: "native"})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Run.CleanupConfirmed).To(BeFalse())
			Expect(result.Run.InspectionError).To(ContainSubstring("has not stopped"))
			observed.Container.State = &container.State{Status: container.StateExited, ExitCode: 143}
			docker.EXPECT().ContainerInspect(gomock.Any(), "csf-simulator-native", gomock.Any()).Return(observed, nil)
			docker.EXPECT().ContainerLogs(gomock.Any(), "owned-id", gomock.Any()).Return(io.NopCloser(strings.NewReader("")), nil)
			docker.EXPECT().ContainerRemove(gomock.Any(), "owned-id", gomock.Any()).Return(dc.ContainerRemoveResult{}, errors.New("lost removal acknowledgement"))
			Expect(worker.Tick(ctx)).To(Succeed())
			result, err = client.InspectSimulation(ctx, &pb.InspectSimulationRequest{RunId: "native"})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Run.CleanupConfirmed).To(BeFalse())
			Expect(result.Run.State).To(Equal(pb.SimulationState_SIMULATION_STATE_CANCELLED))
			docker.EXPECT().ContainerInspect(gomock.Any(), "csf-simulator-native", gomock.Any()).Return(dc.ContainerInspectResult{}, errdefs.ErrNotFound)
			Expect(worker.Tick(ctx)).To(Succeed())
			result, err = client.InspectSimulation(ctx, &pb.InspectSimulationRequest{RunId: "native"})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Run.CleanupConfirmed).To(BeTrue())
			Expect(result.Run.State).To(Equal(pb.SimulationState_SIMULATION_STATE_CANCELLED))
		})

		It("cancels a queued local request without starting a container", func() {
			_, err := client.SubmitSimulation(ctx, &pb.SubmitSimulationRequest{RunId: "native", Simulator: pb.Simulator_SIMULATOR_CARLA, Executor: pb.SimulationExecutor_SIMULATION_EXECUTOR_LOCAL, Steps: 2})
			Expect(err).NotTo(HaveOccurred())
			result, err := client.CancelSimulation(ctx, &pb.CancelSimulationRequest{RunId: "native"})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Run.CleanupConfirmed).To(BeTrue())
			Expect(result.Run.State).To(Equal(pb.SimulationState_SIMULATION_STATE_CANCELLED))
			Expect(worker.Tick(ctx)).To(Succeed())
		})
	})

})

func simDefinition() *pb.ResearchEvent {
	return &pb.ResearchEvent{SchemaVersion: 1, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Payload: &pb.ResearchEvent_Definition{Definition: &pb.MetricDefinition{Name: "simulation_steps_completed", Unit: "count", Description: "Completed fixed steps."}}}
}
func simProgress(runID string, step uint64) *pb.ResearchEvent {
	return &pb.ResearchEvent{SchemaVersion: 1, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Payload: &pb.ResearchEvent_Measurement{Measurement: &pb.Measurement{RunId: runID, Metric: "simulation_steps_completed", Step: step, Value: float64(step)}}}
}
func simStatus(runID string, phase string) *pb.ResearchEvent {
	return &pb.ResearchEvent{SchemaVersion: 1, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Payload: &pb.ResearchEvent_Status{Status: &pb.RunStatus{RunId: runID, Phase: phase, Message: phase}}}
}
