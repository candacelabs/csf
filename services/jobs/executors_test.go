// Copyright 2026 Candace Labs

package jobs_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	batchtypes "github.com/aws/aws-sdk-go-v2/service/batch/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/services/jobs"
	"github.com/candacelabs/csf/services/jobs/jobsmock"
)

func overnightJob(state jobs.State) jobs.Job[overnightSpec] {
	admission := overnightAdmission()
	return jobs.Job[overnightSpec]{ID: admission.ID, Kind: admission.Kind, Executor: admission.Executor, Spec: admission.Spec, State: state,
		TotalUnits: admission.TotalUnits, Managed: true, Target: admission.Target, Image: admission.Image, Timeout: admission.Timeout,
		CreatedAt: time.Now().UTC()}
}

var _ = Describe("AWSBatchExecutor", func() {
	const cancelReason = "operator cancellation"
	var provider *jobsmock.MockIBatch
	var logs *jobsmock.MockICloudWatchLogs
	var executor *jobs.AWSBatchExecutor[overnightSpec]
	var interpreted [][]string
	ctx := context.Background()
	settings := jobs.AWSBatchSettings{LogGroup: "/aws/batch/job", JobNamePrefix: "csf-", OwnerTag: "csf-job-id", CancelReason: cancelReason, LogPageSize: 50}
	environment := func(job jobs.Job[overnightSpec]) []jobs.EnvironmentVariable {
		return []jobs.EnvironmentVariable{{Name: "COMMAND", Value: job.Spec.Command}}
	}
	BeforeEach(func() {
		control := gomock.NewController(GinkgoT())
		provider, logs, interpreted = jobsmock.NewMockIBatch(control), jobsmock.NewMockICloudWatchLogs(control), nil
		var err error
		executor, err = jobs.NewAWSBatchExecutor(provider, logs, settings, environment, func(ctx context.Context, job jobs.Job[overnightSpec], lines []string) (jobs.Observation, error) {
			interpreted = append(interpreted, lines)
			return jobs.Observation{}, nil
		})
		Expect(err).NotTo(HaveOccurred())
	})
	detail := func(status batchtypes.JobStatus) batchtypes.JobDetail {
		return batchtypes.JobDetail{JobId: aws.String("aws-1"), JobQueue: aws.String("queue"), JobDefinition: aws.String("definition:1"), Tags: map[string]string{"csf-job-id": "nightly-1"}, Status: status, Container: &batchtypes.ContainerDetail{LogStreamName: aws.String("stream")}}
	}
	It("refuses to build without its clients and hooks", func() {
		_, err := jobs.NewAWSBatchExecutor[overnightSpec](nil, logs, settings, environment, nil)
		Expect(err).To(MatchError(jobs.ErrInvalidExecutor))
	})
	It("submits once with SDK retries off, the owner tag and the job environment", func() {
		provider.EXPECT().SubmitJob(ctx, gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, input *batch.SubmitJobInput, options ...func(options *batch.Options)) (*batch.SubmitJobOutput, error) {
			Expect(aws.ToString(input.JobName)).To(Equal("csf-nightly-1"))
			Expect(aws.ToString(input.JobQueue)).To(Equal("queue"))
			Expect(aws.ToInt32(input.RetryStrategy.Attempts)).To(Equal(int32(1)))
			Expect(aws.ToInt32(input.Timeout.AttemptDurationSeconds)).To(Equal(int32(3600)))
			Expect(input.Tags).To(Equal(map[string]string{"csf-job-id": "nightly-1"}))
			Expect(input.ContainerOverrides.Environment).To(Equal([]batchtypes.KeyValuePair{{Name: aws.String("COMMAND"), Value: aws.String("sweep")}}))
			configured := batch.Options{}
			options[0](&configured)
			Expect(configured.RetryMaxAttempts).To(Equal(1))
			return &batch.SubmitJobOutput{JobId: aws.String("aws-1")}, nil
		})
		Expect(executor.Submit(ctx, overnightJob(jobs.StateSubmitting))).To(Equal("aws-1"))
	})
	It("refuses to adopt a job whose tag names another identity", func() {
		job := overnightJob(jobs.StateQueued)
		job.ExternalID = "aws-1"
		foreign := detail(batchtypes.JobStatusRunning)
		foreign.Tags["csf-job-id"] = "someone-else"
		provider.EXPECT().DescribeJobs(ctx, gomock.Any()).Return(&batch.DescribeJobsOutput{Jobs: []batchtypes.JobDetail{foreign}}, nil)
		_, err := executor.Observe(ctx, job)
		Expect(err).To(MatchError(jobs.ErrForeignResource))
	})
	It("terminates a running job on cancellation and confirms cleanup only when AWS reports it terminal", func() {
		job := overnightJob(jobs.StateRunning)
		job.ExternalID, job.CancellationRequested = "aws-1", true
		provider.EXPECT().DescribeJobs(ctx, gomock.Any()).Return(&batch.DescribeJobsOutput{Jobs: []batchtypes.JobDetail{detail(batchtypes.JobStatusRunning)}}, nil)
		provider.EXPECT().TerminateJob(ctx, &batch.TerminateJobInput{JobId: aws.String("aws-1"), Reason: aws.String(cancelReason)}).Return(&batch.TerminateJobOutput{}, nil)
		execution, err := executor.Observe(ctx, job)
		Expect(err).NotTo(HaveOccurred())
		Expect(execution.State).To(Equal(jobs.StateCancelling))
		Expect(execution.CleanupConfirmed).To(BeFalse())
		stopped := detail(batchtypes.JobStatusFailed)
		stopped.StatusReason = aws.String(cancelReason)
		provider.EXPECT().DescribeJobs(ctx, gomock.Any()).Return(&batch.DescribeJobsOutput{Jobs: []batchtypes.JobDetail{stopped}}, nil)
		execution, err = executor.Observe(ctx, job)
		Expect(err).NotTo(HaveOccurred())
		Expect(execution).To(Equal(jobs.Execution{State: jobs.StateCancelled, ExternalID: "aws-1", Reason: cancelReason, LogStream: "stream", CleanupConfirmed: true}))
	})
	It("reads a log page from the job's cursor and hands its lines to the interpreter", func() {
		job := overnightJob(jobs.StateRunning)
		job.LogStream, job.LogCursor = "stream", "page-1"
		logs.EXPECT().GetLogEvents(ctx, gomock.Any()).DoAndReturn(func(ctx context.Context, input *cloudwatchlogs.GetLogEventsInput, options ...func(options *cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error) {
			Expect(aws.ToString(input.NextToken)).To(Equal("page-1"))
			Expect(aws.ToInt32(input.Limit)).To(Equal(int32(50)))
			return &cloudwatchlogs.GetLogEventsOutput{Events: []logtypes.OutputLogEvent{{Message: aws.String("line one")}}, NextForwardToken: aws.String("page-2")}, nil
		})
		page, err := executor.ReadLog(ctx, job)
		Expect(err).NotTo(HaveOccurred())
		Expect(page.NextCursor).To(Equal("page-2"))
		Expect(interpreted).To(Equal([][]string{{"line one"}}))
	})
})

var _ = Describe("LocalDockerExecutor", func() {
	const image = "sha256:image"
	var docker *jobsmock.MockIDockerEngine
	var evidence *jobsmock.MockIEvidenceStore
	var executor *jobs.LocalDockerExecutor[overnightSpec]
	var job jobs.Job[overnightSpec]
	ctx := context.Background()
	settings := jobs.LocalDockerSettings{Owner: "candace-test", NamePrefix: "job-", StopGrace: 10 * time.Second, LogBytes: 1024}
	template := func(job jobs.Job[overnightSpec]) (container.Config, container.HostConfig) {
		return container.Config{Cmd: []string{job.Spec.Command}}, container.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: "artifacts", Target: "/artifacts"}}}
	}
	owned := func(state *container.State) client.ContainerInspectResult {
		return client.ContainerInspectResult{Container: container.InspectResponse{ID: "container-id", Image: image, State: state,
			Config: &container.Config{Labels: map[string]string{jobs.ContainerOwnerLabel: "candace-test", jobs.ContainerJobLabel: "nightly-1"}}}, Raw: []byte(`{"Id":"container-id"}`)}
	}
	BeforeEach(func() {
		control := gomock.NewController(GinkgoT())
		docker, evidence = jobsmock.NewMockIDockerEngine(control), jobsmock.NewMockIEvidenceStore(control)
		var err error
		executor, err = jobs.NewLocalDockerExecutor(docker, evidence, settings, template)
		Expect(err).NotTo(HaveOccurred())
		job = overnightJob(jobs.StatePending)
		job.Executor, job.Image, job.Target = hostExecutor, image, "jobs-network"
	})
	It("refuses to build without its client, store and template", func() {
		_, err := jobs.NewLocalDockerExecutor[overnightSpec](docker, nil, settings, template)
		Expect(err).To(MatchError(jobs.ErrInvalidExecutor))
	})
	It("creates the job's own named container and starts it", func() {
		gomock.InOrder(
			docker.EXPECT().ContainerInspect(ctx, "job-nightly-1", gomock.Any()).Return(client.ContainerInspectResult{}, errdefs.ErrNotFound),
			docker.EXPECT().ContainerCreate(ctx, gomock.Any()).DoAndReturn(func(ctx context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
				Expect(options.Name).To(Equal("job-nightly-1"))
				Expect(options.Config.Image).To(Equal(image))
				Expect(options.Config.Cmd).To(Equal([]string{"sweep"}))
				Expect(options.Config.Labels).To(Equal(map[string]string{jobs.ContainerOwnerLabel: "candace-test", jobs.ContainerJobLabel: "nightly-1"}))
				Expect(options.HostConfig.NetworkMode).To(Equal(container.NetworkMode("jobs-network")))
				Expect(options.HostConfig.RestartPolicy.Name).To(Equal(container.RestartPolicyDisabled))
				Expect(options.HostConfig.PortBindings).To(BeEmpty())
				return client.ContainerCreateResult{ID: "container-id"}, nil
			}),
			docker.EXPECT().ContainerInspect(ctx, "job-nightly-1", gomock.Any()).Return(owned(&container.State{Status: container.StateCreated}), nil),
			docker.EXPECT().ContainerStart(ctx, "container-id", gomock.Any()).Return(client.ContainerStartResult{}, nil),
		)
		execution, err := executor.Reconcile(ctx, job)
		Expect(err).NotTo(HaveOccurred())
		Expect(execution.State).To(Equal(jobs.StateQueued))
		Expect(execution.ExternalID).To(Equal("container-id"))
	})
	It("keeps the durable name after a lost create reply and refuses a foreign container", func() {
		docker.EXPECT().ContainerInspect(ctx, "job-nightly-1", gomock.Any()).Return(client.ContainerInspectResult{}, errdefs.ErrNotFound)
		docker.EXPECT().ContainerCreate(ctx, gomock.Any()).Return(client.ContainerCreateResult{}, errors.New("lost reply"))
		execution, err := executor.Reconcile(ctx, job)
		Expect(err).To(MatchError("lost reply"))
		Expect(execution.State).To(Equal(jobs.StateSubmitting))
		Expect(execution.CleanupConfirmed).To(BeFalse())
		foreign := owned(&container.State{Status: container.StateCreated})
		foreign.Container.Config.Labels[jobs.ContainerJobLabel] = "another-job"
		docker.EXPECT().ContainerInspect(ctx, "job-nightly-1", gomock.Any()).Return(foreign, nil)
		_, err = executor.Reconcile(ctx, job)
		Expect(err).To(MatchError(jobs.ErrForeignResource))
	})
	It("retains logs and inspection evidence, removes the container and confirms cleanup of a complete run", func() {
		job.State, job.CompletedUnits = jobs.StateSucceeded, job.TotalUnits
		wire := "\x01\x00\x00\x00\x00\x00\x00\x05done\n"
		gomock.InOrder(
			docker.EXPECT().ContainerInspect(ctx, "job-nightly-1", gomock.Any()).Return(owned(&container.State{Status: container.StateExited}), nil),
			docker.EXPECT().ContainerLogs(ctx, "container-id", gomock.Any()).Return(io.NopCloser(strings.NewReader(wire)), nil),
			evidence.EXPECT().Put([]byte("done\n")).Return(strings.Repeat("1", 64), "logs", nil),
			evidence.EXPECT().Put([]byte(`{"Id":"container-id"}`)).Return(strings.Repeat("2", 64), "inspection", nil),
			docker.EXPECT().ContainerRemove(ctx, "container-id", client.ContainerRemoveOptions{}).Return(client.ContainerRemoveResult{}, nil),
		)
		execution, err := executor.Reconcile(ctx, job)
		Expect(err).NotTo(HaveOccurred())
		Expect(execution).To(Equal(jobs.Execution{State: jobs.StateSucceeded, ExternalID: "container-id", LogStream: strings.Repeat("1", 64), CleanupConfirmed: true}))
	})
	It("fails a run that exited without a complete success report", func() {
		job.State = jobs.StateRunning
		docker.EXPECT().ContainerInspect(ctx, "job-nightly-1", gomock.Any()).Return(owned(&container.State{Status: container.StateExited}), nil)
		docker.EXPECT().ContainerLogs(ctx, "container-id", gomock.Any()).Return(io.NopCloser(strings.NewReader("")), nil)
		evidence.EXPECT().Put(gomock.Any()).Return(strings.Repeat("1", 64), "evidence", nil).Times(2)
		docker.EXPECT().ContainerRemove(ctx, "container-id", gomock.Any()).Return(client.ContainerRemoveResult{}, nil)
		execution, err := executor.Reconcile(ctx, job)
		Expect(err).NotTo(HaveOccurred())
		Expect(execution.State).To(Equal(jobs.StateFailed))
		Expect(execution.Reason).To(ContainSubstring("without a complete successful worker report"))
	})
	It("requires stopped-container evidence before cleaning up a cancelled run", func() {
		job.State, job.CancellationRequested = jobs.StateRunning, true
		running := owned(&container.State{Status: container.StateRunning, Running: true, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)})
		gomock.InOrder(
			docker.EXPECT().ContainerInspect(ctx, "job-nightly-1", gomock.Any()).Return(running, nil),
			docker.EXPECT().ContainerStop(ctx, "container-id", gomock.Any()).Return(client.ContainerStopResult{}, nil),
			docker.EXPECT().ContainerInspect(ctx, "container-id", gomock.Any()).Return(running, nil),
		)
		execution, err := executor.Reconcile(ctx, job)
		Expect(err).To(MatchError(ContainSubstring("has not stopped")))
		Expect(execution.CleanupConfirmed).To(BeFalse())
	})
	It("cancels a queued job without creating a container", func() {
		job.CancellationRequested = true
		docker.EXPECT().ContainerInspect(ctx, "job-nightly-1", gomock.Any()).Return(client.ContainerInspectResult{}, errdefs.ErrNotFound)
		execution, err := executor.Reconcile(ctx, job)
		Expect(err).NotTo(HaveOccurred())
		Expect(execution.State).To(Equal(jobs.StateCancelled))
		Expect(execution.CleanupConfirmed).To(BeTrue())
	})
	It("confirms cleanup when the container is gone after its logs were retained", func() {
		job.State, job.LogStream = jobs.StateCancelled, strings.Repeat("1", 64)
		docker.EXPECT().ContainerInspect(ctx, "job-nightly-1", gomock.Any()).Return(client.ContainerInspectResult{}, errdefs.ErrNotFound)
		execution, err := executor.Reconcile(ctx, job)
		Expect(err).NotTo(HaveOccurred())
		Expect(execution.CleanupConfirmed).To(BeTrue())
		Expect(execution.State).To(Equal(jobs.StateCancelled))
	})
})
