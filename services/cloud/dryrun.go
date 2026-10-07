// Copyright 2026 Candace Labs

package cloud

import (
	"context"
	"fmt"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/io/net/hfjobs"
)

// ProviderDryRun names [DryRunProvider] in the record.
const ProviderDryRun = "dry-run"

// dryRunPrices are the CPU flavors and their prices as /api/jobs/hardware
// listed them on 2026-10-05.
var dryRunPrices = []hfjobs.Hardware{
	{Name: "cpu-basic", PrettyName: "CPU Basic", CPU: "2 vCPU", RAM: "16 GB", EphemeralStorage: "50 GB", UnitCostMicroUSD: 167, UnitLabel: "minute"},
	{Name: "cpu-upgrade", PrettyName: "CPU Upgrade", CPU: "8 vCPU", RAM: "32 GB", EphemeralStorage: "50 GB", UnitCostMicroUSD: 500, UnitLabel: "minute"},
	{Name: "cpu-xl", PrettyName: "CPU XL", CPU: "16 vCPU", RAM: "124 GB", EphemeralStorage: "1000 GB", UnitCostMicroUSD: 16667, UnitLabel: "minute"},
}

// DryRunProvider is a provider that runs nothing: a job it starts is a
// record that runs until it is cancelled, priced as the real flavor is. It is
// the fake job the Cloud panel is proved on before anything is paid for.
// Only the owner of [CloudJobs] calls it, one call at a time, so it holds its
// jobs without a lock.
type DryRunProvider struct {
	clock clock.IClock
	jobs  []hfjobs.Job
}

var _ IJobProvider = (*DryRunProvider)(nil)

// NewDryRunProvider builds the provider over the clock its jobs are stamped by.
func NewDryRunProvider(source clock.IClock) *DryRunProvider {
	return &DryRunProvider{clock: source}
}

// Run records a running job.
func (provider *DryRunProvider) Run(ctx context.Context, spec hfjobs.Spec) (hfjobs.Job, error) {
	job := hfjobs.Job{
		ID: fmt.Sprintf("dryrun%04d", len(provider.jobs)+1), CreatedAt: provider.clock.Now().UTC(), DockerImage: spec.DockerImage,
		Flavor: spec.Flavor, Labels: spec.Labels, Status: hfjobs.Status{Stage: hfjobs.StageRunning}, Timeout: spec.TimeoutSeconds,
	}
	provider.jobs = append(provider.jobs, job)
	return job, nil
}

// List is every job it started.
func (provider *DryRunProvider) List(ctx context.Context) ([]hfjobs.Job, error) {
	return append([]hfjobs.Job{}, provider.jobs...), nil
}

// Cancel ends one running job.
func (provider *DryRunProvider) Cancel(ctx context.Context, id string) error {
	for index := range provider.jobs {
		if provider.jobs[index].ID == id && !provider.jobs[index].Status.Stage.Terminal() {
			finished := provider.clock.Now().UTC()
			provider.jobs[index].Status.Stage, provider.jobs[index].FinishedAt = hfjobs.StageCanceled, &finished
			return nil
		}
	}
	return fmt.Errorf("dry run: no running job %q", id)
}

// Hardware is the CPU flavors at their measured prices.
func (provider *DryRunProvider) Hardware(ctx context.Context) ([]hfjobs.Hardware, error) {
	return append([]hfjobs.Hardware{}, dryRunPrices...), nil
}

// JobURL is empty: a dry-run job has no page.
func (provider *DryRunProvider) JobURL(id string) string { return "" }
