// Copyright 2026 Candace Labs

package cloud

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/candacelabs/csf/csf"
)

// The cloud operations: each is one MCP tool on the CSF service and one HTTP
// route on the host's router; the Workbench's Cloud panel and the csf burst
// verb are clients of the same calls.
const (
	ListCloudJobsTool = "ListCloudJobs"
	StopCloudJobTool  = "StopCloudJob"
	LaunchProbeTool   = "LaunchCloudProbe"
	LaunchBurstTool   = "LaunchBurstSessions"
	LaunchSuiteTool   = "LaunchSuiteShard"

	JobsPath  = "/api/cloud"
	StopPath  = "/api/cloud/stop"
	ProbePath = "/api/cloud/probe"
	BurstPath = "/api/cloud/burst"
	SuitePath = "/api/cloud/suite"
)

// operations is every typed operation of the cloud service.
func (jobs *CloudJobs) operations() []csf.Operation {
	return []csf.Operation{
		csf.NewOperation(ListCloudJobsTool, "Read the cloud record: every paid cloud job CSF launched, with provider, flavor, purpose, start, stage, spend against its cap and its provider page, the latest notices to the operator, and the day's cap.",
			http.MethodGet, JobsPath, jobs.Jobs),
		csf.NewOperation(StopCloudJobTool, "Stop one cloud job: cancel it at the provider, then check the provider's own job list shows it stopped. Fails, naming the stage, when the list does not show it stopped yet.",
			http.MethodPost, StopPath, jobs.Stop),
		csf.NewOperation(LaunchProbeTool, "Launch the smallest cloud job: a pinned busybox that sleeps until the provider's timeout, which is the minutes its cap buys. It runs nothing of CSF and carries no secret; it proves the Cloud panel, the Stop and the caps.",
			http.MethodPost, ProbePath, jobs.LaunchProbe),
		csf.NewOperation(LaunchBurstTool, "Launch a burst of fixer sessions in one disposable cloud job, a whole harness in the session image: the ready slices no session here contends with, at most sessions of them, held here while the job runs. The credentials are job secrets from providers.json. With dry_run, show the job spec, secrets redacted, and what providers.json still lacks, and launch nothing.",
			http.MethodPost, BurstPath, jobs.LaunchBurst),
		csf.NewOperation(LaunchSuiteTool, "Launch one shard of an evaluation suite's replays (csf eval score) in a disposable cloud job: the build's released binary replays the given suite tickets at the suite's budget, pushes nothing out of the job, merges nothing, and uploads its replays for csf eval record. With dry_run, show the job spec, secrets redacted, and what is still missing, and launch nothing.",
			http.MethodPost, SuitePath, jobs.LaunchSuiteShard),
	}
}

// Tools is every operation as an MCP tool for the CSF service.
func (jobs *CloudJobs) Tools() []csf.Option { return csf.OperationTools(jobs.operations()) }

// Register mounts every operation's HTTP route on the caller's router.
func (jobs *CloudJobs) Register(router gin.IRouter) {
	csf.RegisterOperations(router, jobs.operations())
}
