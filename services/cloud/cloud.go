// Copyright 2026 Candace Labs

// Package cloud is CSF's record of the paid jobs it runs on other people's
// machines, and its hand on them: every job it launches, what each has spent
// against its cap, a stop the operator presses that is checked against the
// provider's own job list, and a watcher that cancels a job at its cap and
// every job at the day's cap even when the provider's own timeout was set
// wrong. Every start, stop and cap hit is a notice to the operator.
//
// The record is one file under the harness state directory, [LedgerFile],
// replaced whole on every change, which the Workbench's Cloud panel follows.
// It is a service: the binary grants the provider, the state directory and the
// notifier, and mounts it; one goroutine owns the record.
package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/io/net/hfjobs"
	"github.com/candacelabs/csf/pkg/atomicfile"
	"github.com/candacelabs/csf/runtime"
)

const (
	// LedgerFile is the record, under the harness state directory.
	LedgerFile = "cloud.json"

	// WatchInterval is the watcher's period. A job can overrun its cap by at
	// most one period of its own spend: at cpu-xl's measured price, 16,667
	// micro-dollars a minute (/api/jobs/hardware, 2026-10-05), 30 s is $0.0083,
	// under 1% of a one-dollar cap, while one list call a period is nothing
	// against the API's limits.
	WatchInterval = 30 * time.Second
	// DefaultDailyCapUSD is the operator's hard cap on a day's fixer spend
	// (ruling of 2026-10-04: $100 a day), which cloud spend is part of.
	DefaultDailyCapUSD = 100.0

	// ownerName names the goroutine that owns the record.
	ownerName = "cloud"
	// noticesKept bounds the notices the record keeps, newest last.
	noticesKept = 50
	ledgerMode  = 0o600
	microDollar = 1e-6
)

// NoticeKind is what a notice reports.
type NoticeKind string

const (
	NoticeStart NoticeKind = "start"
	NoticeStop  NoticeKind = "stop"
	NoticeCap   NoticeKind = "cap"
)

// StopReason is why a job stopped.
type StopReason string

const (
	// StopOperator is the operator's Stop.
	StopOperator StopReason = "operator"
	// StopCap is the job's own cap, reached.
	StopCap StopReason = "cap"
	// StopDailyCap is the day's cap, reached.
	StopDailyCap StopReason = "daily cap"
	// StopEnded is a job the provider ended: it finished, failed, or its
	// timeout fired.
	StopEnded StopReason = "ended"
)

var (
	// ErrNoProvider reports a service built without a provider.
	ErrNoProvider = errors.New("cloud: a job provider is required")
	// ErrNoState reports a service built without the state directory.
	ErrNoState = errors.New("cloud: the harness state directory is required")
	// ErrNotStarted reports an operation before the service was mounted.
	ErrNotStarted = errors.New("cloud: the service is not running")
	// ErrUnknownJob reports a job the record does not hold.
	ErrUnknownJob = errors.New("cloud: no job in the record has that identifier")
	// ErrAlreadyStopped reports a stop of a job that is not running.
	ErrAlreadyStopped = errors.New("cloud: the job is not running")
	// ErrNotConfirmed reports a cancel the provider's job list does not show
	// yet; the watcher records the stop when it does.
	ErrNotConfirmed = errors.New("cloud: cancel requested, but the provider's job list does not show the job stopped yet")
	// ErrOverDailyCap reports a launch whose cap would take the day's spend
	// past the day's cap.
	ErrOverDailyCap = errors.New("cloud: the job's cap would pass the day's cap")
	// ErrInvalidCap reports a cap that buys less than a minute, or none.
	ErrInvalidCap = errors.New("cloud: the cap must buy at least one minute of the flavor")
	// ErrUnknownFlavor reports a flavor the provider does not price.
	ErrUnknownFlavor = errors.New("cloud: the provider has no such flavor")
)

// IJobProvider is the provider capability: start, list and cancel jobs, and
// price a flavor. *hfjobs.HFJobsClient and *DryRunProvider satisfy it.
//
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=cloud.go -destination=mock_cloud_test.go -package=cloud_test
type IJobProvider interface {
	Run(ctx context.Context, spec hfjobs.Spec) (hfjobs.Job, error)
	List(ctx context.Context) ([]hfjobs.Job, error)
	Cancel(ctx context.Context, id string) error
	Hardware(ctx context.Context) ([]hfjobs.Hardware, error)
	JobURL(id string) string
}

// INotifier tells the operator about one notice: the phone path.
type INotifier interface {
	Notify(ctx context.Context, notice Notice) error
}

// CloudJob is one job in the record.
type CloudJob struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Flavor   string `json:"flavor"`
	// Purpose is what the job is for: the burst and its slices.
	Purpose string `json:"purpose"`
	// URL is the job's own page at the provider.
	URL        string       `json:"url,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
	FinishedAt *time.Time   `json:"finished_at,omitempty"`
	Stage      hfjobs.Stage `json:"stage"`
	CapUSD     float64      `json:"cap_usd"`
	// UnitCostMicroUSD is the flavor's price per minute when it launched.
	UnitCostMicroUSD int64 `json:"unit_cost_micro_usd"`
	// TimeoutSeconds is the provider's own stop: the minutes the cap buys.
	TimeoutSeconds int64 `json:"timeout_seconds"`
	// SpendUSD is the spend so far: the minutes since the job was created,
	// until it finished, at the flavor's price. Billing from creation rather
	// than from the start of running reads high, never low.
	SpendUSD   float64    `json:"spend_usd"`
	StopReason StopReason `json:"stop_reason,omitempty"`
	// Slices are the dispatcher's slices the job runs, held on this host.
	Slices []string `json:"slices,omitempty"`
	// Branches are the work branches its sessions push.
	Branches []string `json:"branches,omitempty"`
	// PullRequests is how many of those branches' pull requests merged,
	// counted once the job ended; -1 until then.
	PullRequests int `json:"pull_requests"`
}

// Running reports whether the job may still be spending.
func (job CloudJob) Running() bool { return !job.Stage.Terminal() }

// Notice is one message to the operator.
type Notice struct {
	At    time.Time  `json:"at"`
	Kind  NoticeKind `json:"kind"`
	JobID string     `json:"job_id"`
	Text  string     `json:"text"`
}

// Ledger is the record: every job, the latest notices, and the day's cap.
type Ledger struct {
	DailyCapUSD float64    `json:"daily_cap_usd"`
	Jobs        []CloudJob `json:"jobs"`
	Notices     []Notice   `json:"notices"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// SpentOn is the spend of every job created on day's UTC date.
func (ledger Ledger) SpentOn(day time.Time) float64 {
	start := day.UTC().Truncate(24 * time.Hour)
	total := 0.0
	for _, job := range ledger.Jobs {
		if !job.CreatedAt.Before(start) && job.CreatedAt.Before(start.Add(24*time.Hour)) {
			total += job.SpendUSD
		}
	}
	return total
}

// RunningJobs is how many jobs may still be spending.
func (ledger Ledger) RunningJobs() int {
	count := 0
	for _, job := range ledger.Jobs {
		if job.Running() {
			count++
		}
	}
	return count
}

// ReadLedger decodes a record; an empty or unreadable one is an empty record.
func ReadLedger(content []byte) Ledger {
	ledger := Ledger{}
	if json.Unmarshal(content, &ledger) != nil {
		ledger = Ledger{}
	}
	if ledger.Jobs == nil {
		ledger.Jobs = []CloudJob{}
	}
	if ledger.Notices == nil {
		ledger.Notices = []Notice{}
	}
	return ledger
}

// FormatUSD is a cap in dollars with the digits it needs: $0.002, $1, $100.
func FormatUSD(amount float64) string {
	return "$" + strconv.FormatFloat(amount, 'f', -1, 64)
}

// spend is a job's spend at now.
func spend(job CloudJob, now time.Time) float64 {
	end := now
	if job.FinishedAt != nil {
		end = *job.FinishedAt
	}
	minutes := end.Sub(job.CreatedAt).Minutes()
	if minutes < 0 {
		return 0
	}
	return minutes * float64(job.UnitCostMicroUSD) * microDollar
}

// timeoutFor is how many whole seconds cap buys at a per-minute price.
func timeoutFor(capUSD float64, unitCostMicroUSD int64) (int64, error) {
	if capUSD <= 0 || unitCostMicroUSD <= 0 {
		return 0, ErrInvalidCap
	}
	seconds := int64(capUSD / (float64(unitCostMicroUSD) * microDollar) * 60)
	if seconds < 60 {
		return 0, ErrInvalidCap
	}
	return seconds, nil
}

// CloudJobs is the record and the operations on it.
type CloudJobs struct {
	provider     IJobProvider
	providerName string
	directory    string
	notifier     INotifier
	clock        clock.IClock
	logger       *slog.Logger
	dailyCapUSD  float64
	burst        burstSettings
	// merged counts the merged pull requests of a set of branches.
	merged   func(ctx context.Context, branches []string) (int, error)
	commands chan command
	// running is true while the owner runs; an operation outside that fails
	// rather than waiting for an owner that is not there.
	running atomic.Bool
}

// command is one operation run on the owner, over the record.
type command struct {
	run  func(ctx context.Context, ledger *Ledger) error
	done chan error
}

// Option configures [CloudJobs].
type Option func(jobs *CloudJobs) error

// WithProvider grants the provider, recorded on each job by name. Required.
func WithProvider(name string, provider IJobProvider) Option {
	return func(jobs *CloudJobs) error {
		jobs.providerName, jobs.provider = name, provider
		return nil
	}
}

// WithStateDirectory grants the harness state directory the record lives in
// and providers.json is read from. Required.
func WithStateDirectory(directory string) Option {
	return func(jobs *CloudJobs) error {
		jobs.directory = directory
		return nil
	}
}

// WithNotifier grants the path notices reach the operator's phone by.
// Without it a notice is on the Workbench only.
func WithNotifier(notifier INotifier) Option {
	return func(jobs *CloudJobs) error {
		jobs.notifier = notifier
		return nil
	}
}

// WithClock grants the clock spend is measured and the watcher paced by.
func WithClock(source clock.IClock) Option {
	return func(jobs *CloudJobs) error {
		jobs.clock = source
		return nil
	}
}

// WithLogger grants the logger.
func WithLogger(logger *slog.Logger) Option {
	return func(jobs *CloudJobs) error {
		jobs.logger = logger
		return nil
	}
}

// WithMergedCount grants the count of a set of branches' merged pull
// requests, which a burst's output is measured by once it ends.
func WithMergedCount(merged func(ctx context.Context, branches []string) (int, error)) Option {
	return func(jobs *CloudJobs) error {
		jobs.merged = merged
		return nil
	}
}

// WithDailyCap replaces [DefaultDailyCapUSD].
func WithDailyCap(capUSD float64) Option {
	return func(jobs *CloudJobs) error {
		if capUSD <= 0 {
			return fmt.Errorf("cloud: the day's cap must be positive, got %g", capUSD)
		}
		jobs.dailyCapUSD = capUSD
		return nil
	}
}

// NewCloudJobs validates the option set before building the service.
func NewCloudJobs(options ...Option) (*CloudJobs, error) {
	jobs := &CloudJobs{clock: clock.NewSystemClock(), logger: slog.New(slog.DiscardHandler), dailyCapUSD: DefaultDailyCapUSD, commands: make(chan command)}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("cloud: nil option")
		}
		if err := option(jobs); err != nil {
			return nil, err
		}
	}
	switch {
	case jobs.provider == nil:
		return nil, ErrNoProvider
	case jobs.directory == "":
		return nil, ErrNoState
	}
	return jobs, nil
}

var _ runtime.IService = (*CloudJobs)(nil)

// Start starts the owner: it holds the record, runs every operation in turn
// and watches every running job once a [WatchInterval].
func (jobs *CloudJobs) Start(scope *runtime.Scope) error {
	ledger := jobs.read()
	ledger.DailyCapUSD = jobs.dailyCapUSD
	jobs.write(&ledger)
	jobs.running.Store(true)
	return scope.GoOwner(ownerName, func(ctx context.Context) error {
		defer jobs.running.Store(false)
		tick := jobs.clock.After(0)
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-tick:
				// A record with nothing running is not rewritten, so the panel
				// following it hears only of changes.
				if ledger.RunningJobs() > 0 {
					jobs.watch(ctx, &ledger)
					jobs.write(&ledger)
				}
				tick = jobs.clock.After(WatchInterval)
			case request := <-jobs.commands:
				err := request.run(ctx, &ledger)
				jobs.write(&ledger)
				request.done <- err
			}
		}
	})
}

// do runs one operation on the owner.
func (jobs *CloudJobs) do(ctx context.Context, run func(ctx context.Context, ledger *Ledger) error) error {
	if !jobs.running.Load() {
		return ErrNotStarted
	}
	request := command{run: run, done: make(chan error, 1)}
	select {
	case jobs.commands <- request:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ListCloudJobsInput takes nothing.
type ListCloudJobsInput struct{}

// Jobs is the record now.
func (jobs *CloudJobs) Jobs(ctx context.Context, input ListCloudJobsInput) (Ledger, error) {
	var copied Ledger
	err := jobs.do(ctx, func(ctx context.Context, ledger *Ledger) error {
		copied = *ledger
		copied.Jobs, copied.Notices = slices.Clone(ledger.Jobs), slices.Clone(ledger.Notices)
		return nil
	})
	return copied, err
}

// StopCloudJobInput names the job to stop.
type StopCloudJobInput struct {
	ID string `json:"id" jsonschema:"the job's identifier, as the record and the Cloud panel show it"`
}

// Stop cancels one job at the provider and checks the provider's own job
// list shows it stopped: the Workbench's Stop button.
func (jobs *CloudJobs) Stop(ctx context.Context, input StopCloudJobInput) (CloudJob, error) {
	var stopped CloudJob
	err := jobs.do(ctx, func(ctx context.Context, ledger *Ledger) error {
		index := slices.IndexFunc(ledger.Jobs, func(job CloudJob) bool { return job.ID == input.ID })
		if index < 0 {
			return fmt.Errorf("%w: %q", ErrUnknownJob, input.ID)
		}
		if !ledger.Jobs[index].Running() {
			stopped = ledger.Jobs[index]
			return fmt.Errorf("%w: %s is %s", ErrAlreadyStopped, input.ID, ledger.Jobs[index].Stage)
		}
		if err := jobs.cancel(ctx, ledger, index, StopOperator); err != nil {
			return err
		}
		stopped = ledger.Jobs[index]
		if stopped.Running() {
			return fmt.Errorf("%w: %s is %s", ErrNotConfirmed, input.ID, stopped.Stage)
		}
		return nil
	})
	return stopped, err
}

// cancel asks the provider to stop one job, reads its job list back and
// records what it shows. The notice is the stop when the list shows it, and
// is left to the watcher otherwise.
func (jobs *CloudJobs) cancel(ctx context.Context, ledger *Ledger, index int, reason StopReason) error {
	job := &ledger.Jobs[index]
	if err := jobs.provider.Cancel(ctx, job.ID); err != nil {
		return fmt.Errorf("cloud: cancel %s: %w", job.ID, err)
	}
	job.StopReason = reason
	listed, err := jobs.provider.List(ctx)
	if err != nil {
		return fmt.Errorf("cloud: read the job list back after cancelling %s: %w", job.ID, err)
	}
	jobs.observe(ctx, ledger, listed)
	return nil
}

// watch reads the provider's job list into the record, then cancels each
// job past its cap and, once the day's cap is reached, every running job.
func (jobs *CloudJobs) watch(ctx context.Context, ledger *Ledger) {
	listed, err := jobs.provider.List(ctx)
	if err != nil {
		jobs.logger.Warn("cloud: job list not read", "error", err)
	} else {
		jobs.observe(ctx, ledger, listed)
	}
	now := jobs.clock.Now()
	overDay := ledger.SpentOn(now) >= ledger.DailyCapUSD
	for index := range ledger.Jobs {
		job := ledger.Jobs[index]
		if !job.Running() || job.StopReason != "" {
			continue
		}
		reason := StopReason("")
		switch {
		case overDay:
			reason = StopDailyCap
		case job.SpendUSD >= job.CapUSD:
			reason = StopCap
		default:
			continue
		}
		jobs.notify(ctx, ledger, Notice{Kind: NoticeCap, JobID: job.ID, Text: fmt.Sprintf("%s reached its %s: $%.4f spent of %s; cancelling.", job.ID, reason, job.SpendUSD, FormatUSD(job.CapUSD))})
		if err := jobs.cancel(ctx, ledger, index, reason); err != nil {
			jobs.logger.Warn("cloud: cancel at the cap failed", "job", job.ID, "error", err)
		}
	}
}

// observe takes each recorded job's stage and finish from the provider's
// list, measures its spend, and notices each stop the first time it shows.
func (jobs *CloudJobs) observe(ctx context.Context, ledger *Ledger, listed []hfjobs.Job) {
	now := jobs.clock.Now()
	for index := range ledger.Jobs {
		job := &ledger.Jobs[index]
		wasRunning := job.Running()
		if found := slices.IndexFunc(listed, func(candidate hfjobs.Job) bool { return candidate.ID == job.ID }); found >= 0 {
			job.Stage = listed[found].Status.Stage
			if job.Stage.Terminal() && job.FinishedAt == nil {
				finished := now
				if listed[found].FinishedAt != nil {
					finished = *listed[found].FinishedAt
				}
				job.FinishedAt = &finished
			}
		}
		if wasRunning {
			job.SpendUSD = spend(*job, now)
		}
		if wasRunning && !job.Running() {
			if job.StopReason == "" {
				job.StopReason = StopEnded
			}
			jobs.notify(ctx, ledger, Notice{Kind: NoticeStop, JobID: job.ID, Text: fmt.Sprintf("%s stopped (%s, %s): $%.4f spent of %s.", job.ID, job.StopReason, job.Stage, job.SpendUSD, FormatUSD(job.CapUSD))})
			if len(job.Branches) > 0 && jobs.merged != nil {
				if count, err := jobs.merged(ctx, job.Branches); err != nil {
					jobs.logger.Warn("cloud: merged pull requests not counted", "job", job.ID, "error", err)
				} else {
					job.PullRequests = count
				}
			}
		}
	}
}

// notify records a notice and sends it to the operator's phone when a
// notifier is granted; a notice that does not send is logged, never lost
// from the record.
func (jobs *CloudJobs) notify(ctx context.Context, ledger *Ledger, notice Notice) {
	notice.At = jobs.clock.Now().UTC()
	ledger.Notices = append(ledger.Notices, notice)
	if len(ledger.Notices) > noticesKept {
		ledger.Notices = ledger.Notices[len(ledger.Notices)-noticesKept:]
	}
	if jobs.notifier == nil {
		return
	}
	if err := jobs.notifier.Notify(ctx, notice); err != nil {
		jobs.logger.Warn("cloud: notice not sent", "job", notice.JobID, "kind", notice.Kind, "error", err)
	}
}

// launch starts one job under a cap: the provider's timeout is the minutes
// the cap buys, the day's cap is checked first, and the job is in the record
// with its notice before it returns.
func (jobs *CloudJobs) launch(ctx context.Context, ledger *Ledger, spec hfjobs.Spec, purpose string, capUSD float64, slices []string, branches []string) (CloudJob, error) {
	price, err := jobs.price(ctx, spec.Flavor)
	if err != nil {
		return CloudJob{}, err
	}
	timeout, err := timeoutFor(capUSD, price)
	if err != nil {
		return CloudJob{}, err
	}
	if spent := ledger.SpentOn(jobs.clock.Now()); spent+capUSD > ledger.DailyCapUSD {
		return CloudJob{}, fmt.Errorf("%w: $%.4f spent today, %s more, cap %s", ErrOverDailyCap, spent, FormatUSD(capUSD), FormatUSD(ledger.DailyCapUSD))
	}
	spec.TimeoutSeconds = timeout
	started, err := jobs.provider.Run(ctx, spec)
	if err != nil {
		return CloudJob{}, fmt.Errorf("cloud: launch: %w", err)
	}
	created := started.CreatedAt
	if created.IsZero() {
		created = jobs.clock.Now().UTC()
	}
	job := CloudJob{
		ID: started.ID, Provider: jobs.providerName, Flavor: spec.Flavor, Purpose: purpose, URL: jobs.provider.JobURL(started.ID),
		CreatedAt: created, Stage: started.Status.Stage, CapUSD: capUSD, UnitCostMicroUSD: price, TimeoutSeconds: timeout,
		Slices: slices, Branches: branches, PullRequests: -1,
	}
	ledger.Jobs = append(ledger.Jobs, job)
	jobs.notify(ctx, ledger, Notice{Kind: NoticeStart, JobID: job.ID, Text: fmt.Sprintf("%s started on %s %s for %s: cap %s, provider timeout %s.", job.ID, job.Provider, job.Flavor, purpose, FormatUSD(capUSD), time.Duration(timeout)*time.Second)})
	// The provider echoes the timeout it holds; one that differs leaves CSF's
	// watcher the only stop at the cap, which the operator is told.
	if started.Timeout != timeout {
		jobs.notify(ctx, ledger, Notice{Kind: NoticeCap, JobID: job.ID, Text: fmt.Sprintf("%s: the provider holds a timeout of %ds, not the %ds sent; CSF's watcher is the only stop at the cap.", job.ID, started.Timeout, timeout)})
	}
	return job, nil
}

// price is a flavor's per-minute price at the provider now.
func (jobs *CloudJobs) price(ctx context.Context, flavor string) (int64, error) {
	hardware, err := jobs.provider.Hardware(ctx)
	if err != nil {
		return 0, fmt.Errorf("cloud: read the provider's prices: %w", err)
	}
	for _, offered := range hardware {
		if offered.Name == flavor {
			return offered.UnitCostMicroUSD, nil
		}
	}
	return 0, fmt.Errorf("%w: %q", ErrUnknownFlavor, flavor)
}

// read loads the record a previous run left; none is an empty record.
func (jobs *CloudJobs) read() Ledger {
	content, _ := os.ReadFile(filepath.Join(jobs.directory, LedgerFile))
	return ReadLedger(content)
}

// write replaces the record whole, so a reader sees one version or the next.
func (jobs *CloudJobs) write(ledger *Ledger) {
	ledger.UpdatedAt = jobs.clock.Now().UTC()
	content, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		jobs.logger.Warn("cloud: record not encoded", "error", err)
		return
	}
	target := filepath.Join(jobs.directory, LedgerFile)
	if err := atomicfile.WriteFile(target, append(content, '\n'), ledgerMode); err != nil {
		jobs.logger.Warn("cloud: record not written", "error", err)
	}
}
