// Copyright 2026 Candace Labs

package jobs

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	batchtypes "github.com/aws/aws-sdk-go-v2/service/batch/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

// IBatch is the consumer seam over the upstream generated AWS Batch client.
type IBatch interface {
	SubmitJob(ctx context.Context, input *batch.SubmitJobInput, options ...func(options *batch.Options)) (*batch.SubmitJobOutput, error)
	DescribeJobs(ctx context.Context, input *batch.DescribeJobsInput, options ...func(options *batch.Options)) (*batch.DescribeJobsOutput, error)
	TerminateJob(ctx context.Context, input *batch.TerminateJobInput, options ...func(options *batch.Options)) (*batch.TerminateJobOutput, error)
}

// ICloudWatchLogs reads a job's CloudWatch stream, so the worker needs no
// inbound listener.
type ICloudWatchLogs interface {
	GetLogEvents(ctx context.Context, input *cloudwatchlogs.GetLogEventsInput, options ...func(options *cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error)
}

// EnvironmentVariable is one variable a job's container receives.
type EnvironmentVariable struct {
	Name  string
	Value string
}

// BatchEnvironment returns the container environment of a job, in order.
type BatchEnvironment[Spec any] func(job Job[Spec]) []EnvironmentVariable

// LogInterpreter turns a page of log lines into an observation; it stores
// the evidence the observation's hash names. An empty observation is fine.
type LogInterpreter[Spec any] func(ctx context.Context, job Job[Spec], lines []string) (Observation, error)

// AWSBatchSettings are the operator's AWS Batch choices.
type AWSBatchSettings struct {
	LogGroup string
	// JobNamePrefix prefixes the job identity in the AWS job name.
	JobNamePrefix string
	// OwnerTag is the tag key carrying the job identity, checked on every
	// observation so a foreign job is never adopted.
	OwnerTag string
	// CancelReason is the termination reason, recognized on the way back to
	// tell an operator cancellation from a failure.
	CancelReason string
	// LogPageSize bounds one log read.
	LogPageSize int32
}

// AWSBatchExecutor is the remote executor over AWS Batch and CloudWatch.
type AWSBatchExecutor[Spec any] struct {
	batch       IBatch
	logs        ICloudWatchLogs
	settings    AWSBatchSettings
	environment BatchEnvironment[Spec]
	interpret   LogInterpreter[Spec]
}

// NewAWSBatchExecutor returns the executor over the SDK clients the binary
// built; it never loads AWS configuration itself.
func NewAWSBatchExecutor[Spec any](client IBatch, logs ICloudWatchLogs, settings AWSBatchSettings, environment BatchEnvironment[Spec], interpret LogInterpreter[Spec]) (*AWSBatchExecutor[Spec], error) {
	if client == nil || logs == nil || environment == nil || interpret == nil || settings.LogGroup == "" || settings.OwnerTag == "" || settings.CancelReason == "" || settings.LogPageSize < 1 {
		return nil, fmt.Errorf("%w: AWS Batch needs clients, a log group, an owner tag, a cancel reason, a page size and job hooks", ErrInvalidExecutor)
	}
	return &AWSBatchExecutor[Spec]{batch: client, logs: logs, settings: settings, environment: environment, interpret: interpret}, nil
}

// Submit submits the job once, with SDK retries disabled.
func (executor *AWSBatchExecutor[Spec]) Submit(ctx context.Context, job Job[Spec]) (string, error) {
	variables := executor.environment(job)
	environment := make([]batchtypes.KeyValuePair, 0, len(variables))
	for _, variable := range variables {
		environment = append(environment, batchtypes.KeyValuePair{Name: aws.String(variable.Name), Value: aws.String(variable.Value)})
	}
	result, err := executor.batch.SubmitJob(ctx, &batch.SubmitJobInput{
		JobName: aws.String(executor.settings.JobNamePrefix + job.ID), JobQueue: aws.String(job.Target), JobDefinition: aws.String(job.Image),
		RetryStrategy: &batchtypes.RetryStrategy{Attempts: aws.Int32(1)}, Timeout: &batchtypes.JobTimeout{AttemptDurationSeconds: aws.Int32(int32(job.Timeout.Seconds()))},
		Tags:               map[string]string{executor.settings.OwnerTag: job.ID},
		ContainerOverrides: &batchtypes.ContainerOverrides{Environment: environment},
	}, func(options *batch.Options) { options.RetryMaxAttempts = 1 })
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", ErrNoExternalIdentity
	}
	return aws.ToString(result.JobId), nil
}

// Observe reads the AWS job, terminates it when cancellation was requested,
// and confirms cleanup only once AWS reports it terminal.
func (executor *AWSBatchExecutor[Spec]) Observe(ctx context.Context, job Job[Spec]) (Execution, error) {
	execution := job.Execution()
	result, err := executor.batch.DescribeJobs(ctx, &batch.DescribeJobsInput{Jobs: []string{job.ExternalID}})
	if err != nil {
		return execution, err
	}
	if result == nil || len(result.Jobs) != 1 {
		return execution, fmt.Errorf("AWS did not return the recorded job")
	}
	detail := result.Jobs[0]
	if aws.ToString(detail.JobId) != job.ExternalID || aws.ToString(detail.JobQueue) != job.Target || aws.ToString(detail.JobDefinition) != job.Image || detail.Tags[executor.settings.OwnerTag] != job.ID {
		return execution, ErrForeignResource
	}
	terminal := false
	switch detail.Status {
	case batchtypes.JobStatusSubmitted, batchtypes.JobStatusPending, batchtypes.JobStatusRunnable:
		execution.State = StateQueued
	case batchtypes.JobStatusStarting, batchtypes.JobStatusRunning:
		execution.State = StateRunning
	case batchtypes.JobStatusSucceeded:
		execution.State, terminal = StateSucceeded, true
	case batchtypes.JobStatusFailed:
		execution.State, terminal = StateFailed, true
		if job.CancellationRequested && aws.ToString(detail.StatusReason) == executor.settings.CancelReason {
			execution.State = StateCancelled
		}
	default:
		return execution, fmt.Errorf("unknown AWS job status %q", detail.Status)
	}
	if job.CancellationRequested && !terminal {
		if _, err := executor.batch.TerminateJob(ctx, &batch.TerminateJobInput{JobId: aws.String(job.ExternalID), Reason: aws.String(executor.settings.CancelReason)}); err != nil {
			return job.Execution(), err
		}
		execution.State = StateCancelling
	}
	execution.Reason, execution.CleanupConfirmed, execution.LogStream = aws.ToString(detail.StatusReason), terminal, ""
	if detail.Container != nil {
		execution.LogStream = aws.ToString(detail.Container.LogStreamName)
	}
	return execution, nil
}

// ReadLog reads one page of the job's CloudWatch stream from its cursor.
func (executor *AWSBatchExecutor[Spec]) ReadLog(ctx context.Context, job Job[Spec]) (LogPage, error) {
	input := &cloudwatchlogs.GetLogEventsInput{LogGroupName: aws.String(executor.settings.LogGroup), LogStreamName: aws.String(job.LogStream), StartFromHead: aws.Bool(true), Limit: aws.Int32(executor.settings.LogPageSize)}
	if job.LogCursor != "" {
		input.NextToken = aws.String(job.LogCursor)
	}
	result, err := executor.logs.GetLogEvents(ctx, input)
	if err != nil {
		return LogPage{}, err
	}
	if result == nil {
		return LogPage{}, fmt.Errorf("empty CloudWatch response")
	}
	lines := make([]string, 0, len(result.Events))
	for _, event := range result.Events {
		lines = append(lines, aws.ToString(event.Message))
	}
	observation, err := executor.interpret(ctx, job, lines)
	if err != nil {
		return LogPage{}, err
	}
	return LogPage{Observation: observation, NextCursor: aws.ToString(result.NextForwardToken)}, nil
}
