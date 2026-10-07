// Copyright 2026 Candace Labs

package cloud_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/io/net/hfjobs"
	"github.com/candacelabs/csf/pkg/eventually"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/cloud"
	"github.com/candacelabs/csf/services/dispatch"
)

// Integration specs: the service through its exported API, the provider and
// the notifier gomock doubles, time a manual clock. The prices are the
// measured ones (cpu-basic 167 micro-dollars a minute).
const (
	specJob     = "687fb701029421ae5549d998"
	specFlavor  = "cpu-basic"
	specPrice   = 167
	specPurpose = "panel proof"
	specURL     = "https://hub.example/jobs/operator/" + specJob
)

// ownerBudget is how long the owner goroutine may take to act on a tick or a
// command on a loaded machine.
var ownerBudget = eventually.Budget{Within: 10 * time.Second}

var specStart = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func runningJob(created time.Time) hfjobs.Job {
	return hfjobs.Job{ID: specJob, CreatedAt: created, Flavor: specFlavor, Status: hfjobs.Status{Stage: hfjobs.StageRunning}}
}

// echoed is a started job as the provider answers: holding the spec's timeout.
func echoed(job hfjobs.Job, spec hfjobs.Spec) hfjobs.Job {
	job.Timeout = spec.TimeoutSeconds
	return job
}

func canceledJob(created time.Time, finished time.Time) hfjobs.Job {
	job := runningJob(created)
	job.Status.Stage, job.FinishedAt = hfjobs.StageCanceled, &finished
	return job
}

var _ = Describe("Cloud jobs", func() {
	var (
		provider  *MockIJobProvider
		notifier  *MockINotifier
		manual    *clock.ManualClock
		directory string
		jobs      *cloud.CloudJobs
		scope     *runtime.Scope
		notices   chan cloud.Notice
		options   []cloud.Option
		ctx       = context.Background()
	)

	BeforeEach(func() {
		controller := gomock.NewController(GinkgoT())
		provider, notifier = NewMockIJobProvider(controller), NewMockINotifier(controller)
		manual = clock.NewManualClock(specStart)
		directory = GinkgoT().TempDir()
		notices = make(chan cloud.Notice, 16)
		notifier.EXPECT().Notify(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(_ context.Context, notice cloud.Notice) error {
			notices <- notice
			return nil
		})
		provider.EXPECT().Hardware(gomock.Any()).AnyTimes().Return([]hfjobs.Hardware{{Name: specFlavor, UnitCostMicroUSD: specPrice, UnitLabel: "minute"}}, nil)
		provider.EXPECT().JobURL(specJob).AnyTimes().Return(specURL)
		options = []cloud.Option{cloud.WithProvider("huggingface", provider), cloud.WithStateDirectory(directory), cloud.WithNotifier(notifier), cloud.WithClock(manual)}
	})

	start := func(extra ...cloud.Option) {
		var err error
		jobs, err = cloud.NewCloudJobs(append(options, extra...)...)
		Expect(err).NotTo(HaveOccurred())
		scope = runtime.NewScope(ctx, "spec")
		Expect(jobs.Start(scope)).To(Succeed())
		DeferCleanup(func() {
			scope.Cancel()
			Expect(scope.Wait()).To(Succeed())
		})
	}

	readLedger := func() cloud.Ledger {
		content, _ := os.ReadFile(filepath.Join(directory, cloud.LedgerFile))
		return cloud.ReadLedger(content)
	}

	// tick moves the clock one watch period once the owner has armed it.
	tick := func() {
		eventually.Await(GinkgoT(), "the watcher to arm its next period", ownerBudget, manual.Waiting, func(waiting int) bool { return waiting > 0 })
		manual.Advance(cloud.WatchInterval)
	}

	launchProbe := func(capUSD float64) cloud.CloudJob {
		provider.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, spec hfjobs.Spec) (hfjobs.Job, error) {
			Expect(spec.DockerImage).To(Equal(cloud.ProbeImage))
			Expect(spec.Secrets).To(BeEmpty(), "a probe carries no secret")
			return echoed(runningJob(specStart), spec), nil
		})
		job, err := jobs.LaunchProbe(ctx, cloud.LaunchProbeInput{Flavor: specFlavor, Purpose: specPurpose, CapUSD: capUSD})
		Expect(err).NotTo(HaveOccurred())
		return job
	}

	It("launches under the cap: the provider's timeout is the minutes the cap buys, and the start is a notice", func() {
		start()
		job := launchProbe(0.01)
		// $0.01 at 167 micro-dollars a minute is 59.88 minutes.
		Expect(job.TimeoutSeconds).To(Equal(int64(3592)))
		Expect(job.URL).To(Equal(specURL))
		Expect(job.Provider).To(Equal("huggingface"))
		Expect((<-notices).Kind).To(Equal(cloud.NoticeStart))
		recorded := readLedger()
		Expect(recorded.Jobs).To(HaveLen(1))
		Expect(recorded.Notices).To(HaveLen(1))
		info, err := os.Stat(filepath.Join(directory, cloud.LedgerFile))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("tells the operator when the provider does not hold the timeout it was sent", func() {
		start()
		provider.EXPECT().Run(gomock.Any(), gomock.Any()).Return(runningJob(specStart), nil)
		_, err := jobs.LaunchProbe(ctx, cloud.LaunchProbeInput{Flavor: specFlavor, Purpose: specPurpose, CapUSD: 0.01})
		Expect(err).NotTo(HaveOccurred())
		Expect((<-notices).Kind).To(Equal(cloud.NoticeStart))
		notice := <-notices
		Expect(notice.Kind).To(Equal(cloud.NoticeCap))
		Expect(notice.Text).To(ContainSubstring("CSF's watcher is the only stop at the cap"))
	})

	It("cancels a job at its cap even when the provider's timeout has not fired", func() {
		start()
		launchProbe(0.001) // six minutes of cpu-basic
		<-notices
		provider.EXPECT().List(gomock.Any()).AnyTimes().Return([]hfjobs.Job{runningJob(specStart)}, nil)
		for range 11 {
			tick()
		}
		eventually.Await(GinkgoT(), "the spend to be measured", ownerBudget, readLedger, func(ledger cloud.Ledger) bool {
			return ledger.Jobs[0].SpendUSD > 0.0009
		})
		canceled := make(chan struct{})
		provider.EXPECT().Cancel(gomock.Any(), specJob).DoAndReturn(func(ctx context.Context, id string) error {
			close(canceled)
			return nil
		})
		tick()
		<-canceled
		Expect((<-notices).Kind).To(Equal(cloud.NoticeCap))
		ledger := eventually.Await(GinkgoT(), "the cap to be recorded", ownerBudget, readLedger, func(ledger cloud.Ledger) bool {
			return ledger.Jobs[0].StopReason == cloud.StopCap
		})
		Expect(ledger.Jobs[0].SpendUSD).To(BeNumerically(">=", 0.001))
	})

	It("stops a job when the operator presses Stop and the provider's list shows it stopped", func() {
		start()
		launchProbe(0.01)
		<-notices
		// The spend runs to the finish the provider's list reports.
		gomock.InOrder(
			provider.EXPECT().Cancel(gomock.Any(), specJob).Return(nil),
			provider.EXPECT().List(gomock.Any()).Return([]hfjobs.Job{canceledJob(specStart, specStart.Add(time.Minute))}, nil),
		)
		stopped, err := jobs.Stop(ctx, cloud.StopCloudJobInput{ID: specJob})
		Expect(err).NotTo(HaveOccurred())
		Expect(stopped.Stage).To(Equal(hfjobs.StageCanceled))
		Expect(stopped.StopReason).To(Equal(cloud.StopOperator))
		Expect(stopped.SpendUSD).To(BeNumerically("~", 0.000167, 1e-9))
		notice := <-notices
		Expect(notice.Kind).To(Equal(cloud.NoticeStop))
		Expect(notice.JobID).To(Equal(specJob))
	})

	It("says so when the provider's list does not show the job stopped yet", func() {
		start()
		launchProbe(0.01)
		<-notices
		provider.EXPECT().Cancel(gomock.Any(), specJob).Return(nil)
		provider.EXPECT().List(gomock.Any()).Return([]hfjobs.Job{runningJob(specStart)}, nil)
		_, err := jobs.Stop(ctx, cloud.StopCloudJobInput{ID: specJob})
		Expect(err).To(MatchError(cloud.ErrNotConfirmed))
	})

	It("refuses a Stop of a job it does not hold or that already stopped", func() {
		start()
		_, err := jobs.Stop(ctx, cloud.StopCloudJobInput{ID: "unknown"})
		Expect(err).To(MatchError(cloud.ErrUnknownJob))
		launchProbe(0.01)
		<-notices
		provider.EXPECT().Cancel(gomock.Any(), specJob).Return(nil)
		provider.EXPECT().List(gomock.Any()).Return([]hfjobs.Job{canceledJob(specStart, specStart)}, nil)
		_, err = jobs.Stop(ctx, cloud.StopCloudJobInput{ID: specJob})
		Expect(err).NotTo(HaveOccurred())
		_, err = jobs.Stop(ctx, cloud.StopCloudJobInput{ID: specJob})
		Expect(err).To(MatchError(cloud.ErrAlreadyStopped))
	})

	It("refuses a launch whose cap would pass the day's cap, and a cap that buys under a minute", func() {
		start(cloud.WithDailyCap(0.005))
		_, err := jobs.LaunchProbe(ctx, cloud.LaunchProbeInput{Flavor: specFlavor, Purpose: specPurpose, CapUSD: 0.01})
		Expect(err).To(MatchError(cloud.ErrOverDailyCap))
		_, err = jobs.LaunchProbe(ctx, cloud.LaunchProbeInput{Flavor: specFlavor, Purpose: specPurpose, CapUSD: 0.0001})
		Expect(err).To(MatchError(cloud.ErrInvalidCap))
		_, err = jobs.LaunchProbe(ctx, cloud.LaunchProbeInput{Flavor: "gpu-huge", Purpose: specPurpose, CapUSD: 1})
		Expect(err).To(MatchError(cloud.ErrUnknownFlavor))
	})

	It("refuses every operation before it is started", func() {
		var err error
		jobs, err = cloud.NewCloudJobs(options...)
		Expect(err).NotTo(HaveOccurred())
		_, err = jobs.Jobs(ctx, cloud.ListCloudJobsInput{})
		Expect(err).To(MatchError(cloud.ErrNotStarted))
	})

	It("refuses to build without a provider or a state directory", func() {
		_, err := cloud.NewCloudJobs(cloud.WithStateDirectory(directory))
		Expect(err).To(MatchError(cloud.ErrNoProvider))
		_, err = cloud.NewCloudJobs(cloud.WithProvider("huggingface", provider))
		Expect(err).To(MatchError(cloud.ErrNoState))
	})

	Describe("a burst", func() {
		var holds map[string]string

		ready := []dispatch.ReadySlice{{SliceID: "S1", TicketURL: "https://tickets.example/1", Recipe: &pb.AgentAssignmentRecipe{
			AssignmentId: "11111111-2222-3333-4444-555555555555", TicketUrl: "https://tickets.example/1", Task: "fix", Model: "m", RepositoryId: "r",
			Workspace: &pb.AgentWorkspace{RepositoryPath: "/host/checkout", BaseBranch: "main", Branch: "dev/s1", PullRequestTitle: "S1"},
		}}}

		BeforeEach(func() {
			holds = map[string]string{}
			options = append(options,
				cloud.WithBurstImage("registry.example/csf-session@sha256:abc"), cloud.WithBurstRepository("owner/repository"),
				cloud.WithSliceQueue(func(_ context.Context, limit int) ([]dispatch.ReadySlice, error) {
					return ready[:min(limit, len(ready))], nil
				},
					func(_ context.Context, slice string, reason string) error {
						holds[slice] = reason
						return nil
					}))
		})

		writeProviders := func(mode os.FileMode) {
			content, err := json.Marshal(map[string]any{"burst": map[string]any{
				"github_token": "github-secret", "hf_token": "hf-secret", "corpus_dataset": "owner/corpus",
				"executor_environment": map[string]string{"ANTHROPIC_API_KEY": "model-secret"},
			}})
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(directory, cloud.ProvidersFile), content, mode)).To(Succeed())
		}

		It("shows its spec on a dry run, every secret redacted, and names what providers.json lacks", func() {
			start()
			output, err := jobs.LaunchBurst(ctx, cloud.LaunchBurstInput{Sessions: 2, Flavor: specFlavor, CapUSD: 0.01, DryRun: true})
			Expect(err).NotTo(HaveOccurred())
			Expect(output.Missing).To(ConsistOf("burst.github_token", "burst.hf_token", "burst.corpus_dataset", "burst.executor_environment"))
			Expect(output.Slices).To(Equal([]string{"S1"}))
			Expect(output.Spec.TimeoutSeconds).To(Equal(int64(3592)))
			Expect(output.Spec.Command[0]).To(Equal("bash"))
			Expect(output.Spec.Environment).To(HaveKeyWithValue("BURST_REPOSITORY", "owner/repository"))
			Expect(output.Spec.Environment["BURST_RECIPES"]).To(ContainSubstring(`"branch":"dev/s1"`))
			Expect(output.Job).To(BeNil())
			Expect(holds).To(BeEmpty(), "a dry run holds nothing")

			writeProviders(0o600)
			output, err = jobs.LaunchBurst(ctx, cloud.LaunchBurstInput{Sessions: 2, Flavor: specFlavor, CapUSD: 0.01, DryRun: true})
			Expect(err).NotTo(HaveOccurred())
			Expect(output.Missing).To(BeEmpty())
			Expect(output.Spec.Secrets).To(Equal(map[string]string{"GH_TOKEN": "(secret)", "HF_TOKEN": "(secret)", "ANTHROPIC_API_KEY": "(secret)"}))
		})

		It("launches with the credentials as job secrets and holds its slices here", func() {
			writeProviders(0o600)
			start()
			provider.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, spec hfjobs.Spec) (hfjobs.Job, error) {
				Expect(spec.Secrets).To(Equal(map[string]string{"GH_TOKEN": "github-secret", "HF_TOKEN": "hf-secret", "ANTHROPIC_API_KEY": "model-secret"}))
				Expect(spec.Environment).NotTo(ContainElement(ContainSubstring("secret")), "no secret is in the plain environment")
				Expect(spec.TimeoutSeconds).To(Equal(int64(3592)))
				return echoed(runningJob(specStart), spec), nil
			})
			output, err := jobs.LaunchBurst(ctx, cloud.LaunchBurstInput{Sessions: 2, Flavor: specFlavor, CapUSD: 0.01})
			Expect(err).NotTo(HaveOccurred())
			Expect(output.Job.Slices).To(Equal([]string{"S1"}))
			Expect(output.Job.Branches).To(Equal([]string{"dev/s1"}))
			Expect(holds).To(HaveKeyWithValue("S1", "running in cloud job "+specJob))
			content, err := os.ReadFile(filepath.Join(directory, cloud.LedgerFile))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).NotTo(ContainSubstring("secret"), "the record holds no secret")
		})

		It("refuses a launch the credentials do not cover, and providers.json readable by others", func() {
			start()
			_, err := jobs.LaunchBurst(ctx, cloud.LaunchBurstInput{Sessions: 1, Flavor: specFlavor, CapUSD: 0.01})
			Expect(err).To(MatchError(cloud.ErrMissingCredentials))
			writeProviders(0o644)
			_, err = jobs.LaunchBurst(ctx, cloud.LaunchBurstInput{Sessions: 1, Flavor: specFlavor, CapUSD: 0.01, DryRun: true})
			Expect(err).To(MatchError(ContainSubstring("owner-only")))
		})

		It("names a missing session image in the dry run and refuses the launch", func() {
			writeProviders(0o600)
			start(cloud.WithBurstImage(""))
			output, err := jobs.LaunchBurst(ctx, cloud.LaunchBurstInput{Sessions: 1, Flavor: specFlavor, CapUSD: 0.01, DryRun: true})
			Expect(err).NotTo(HaveOccurred())
			Expect(output.Missing).To(Equal([]string{"serve -burst-image"}))
			_, err = jobs.LaunchBurst(ctx, cloud.LaunchBurstInput{Sessions: 1, Flavor: specFlavor, CapUSD: 0.01})
			Expect(err).To(MatchError(cloud.ErrNoImage))
		})

		It("refuses a session count out of range", func() {
			start()
			_, err := jobs.LaunchBurst(ctx, cloud.LaunchBurstInput{Sessions: 0, Flavor: specFlavor, CapUSD: 1})
			Expect(err).To(MatchError(cloud.ErrInvalidSessions))
		})
	})
})
