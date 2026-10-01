package cron

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	completionTimeout = 5 * time.Second
	maxRecordedError  = 4 << 10
)

// JobFunc executes one scheduled invocation. It should stop promptly when ctx
// is cancelled. Returning an error records a failed occurrence; it does not
// stop the scheduler.
type JobFunc func(ctx context.Context, invocation Invocation) error

// Invocation identifies the logical occurrence passed to a job handler.
type Invocation struct {
	ID          string    `json:"id"`
	JobName     string    `json:"job_name"`
	ScheduledAt time.Time `json:"scheduled_at"`
	StartedAt   time.Time `json:"started_at"`
	Attempt     uint32    `json:"attempt"`
}

// Service reconciles static jobs, schedules deterministic occurrences, and
// executes handlers under durable leases.
type Service struct {
	store         IStore
	jobs          map[string]registeredJob
	jobNames      []string
	leaseDuration time.Duration
	catchUpLimit  int
	leaseOwner    string
	now           func() time.Time

	mu      sync.RWMutex
	running bool
	workers sync.WaitGroup
}

type runtimeJob struct {
	registered registeredJob
	schedule   Schedule
	state      JobState
	startup    bool
}

type dueOccurrence struct {
	scheduledAt time.Time
	nextRunAt   time.Time
}

// New constructs a stopped Service. An IStore and at least one WithJob are
// required; construction performs no persistence and starts no goroutines.
func New(options ...Option) (*Service, error) {
	config := serviceConfig{
		leaseDuration: defaultLeaseDuration,
		catchUpLimit:  defaultCatchUpLimit,
		now:           time.Now,
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil service option", ErrInvalidConfiguration)
		}
		if err := option(&config); err != nil {
			return nil, err
		}
	}
	if config.store == nil {
		return nil, ErrStoreRequired
	}
	if len(config.jobs) == 0 {
		return nil, ErrNoJobs
	}
	if config.leaseOwner == "" {
		identity, err := randomIdentity("runner")
		if err != nil {
			return nil, fmt.Errorf("cron: generating lease owner: %w", err)
		}
		config.leaseOwner = identity
	}

	jobs := make(map[string]registeredJob, len(config.jobs))
	jobNames := make([]string, 0, len(config.jobs))
	for _, job := range config.jobs {
		if _, duplicate := jobs[job.name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate job %q", ErrInvalidConfiguration, job.name)
		}
		jobs[job.name] = job
		jobNames = append(jobNames, job.name)
	}
	return &Service{
		store:         config.store,
		jobs:          jobs,
		jobNames:      jobNames,
		leaseDuration: config.leaseDuration,
		catchUpLimit:  config.catchUpLimit,
		leaseOwner:    config.leaseOwner,
		now:           config.now,
	}, nil
}

// Run blocks until ctx is cancelled or durable scheduler state cannot be read
// or written safely. Job failures and panics are recorded as failed
// occurrences and do not stop Run. On return, all cooperative handlers have
// exited and their terminal state has been offered to IStore.
func (service *Service) Run(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil run context", ErrInvalidConfiguration)
	}
	if err := service.beginRun(); err != nil {
		return err
	}
	runContext, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		service.workers.Wait()
		service.endRun()
	}()

	startedAt := service.now().UTC()
	jobs, err := service.reconcile(runContext, startedAt)
	if err != nil {
		return normalizeRunError(ctx, err)
	}
	runtimeErrors := make(chan error, 1)
	immediate, err := service.cycle(runContext, jobs, startedAt, runtimeErrors)
	if err != nil {
		return normalizeRunError(ctx, err)
	}

	for {
		now := service.now().UTC()
		wait := nextWait(jobs, now, service.recoveryInterval())
		if immediate {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return nil
		case err := <-runtimeErrors:
			stopTimer(timer)
			return normalizeRunError(ctx, err)
		case <-timer.C:
			immediate, err = service.cycle(runContext, jobs, service.now().UTC(), runtimeErrors)
			if err != nil {
				return normalizeRunError(ctx, err)
			}
		}
	}
}

func (service *Service) reconcile(ctx context.Context, now time.Time) ([]*runtimeJob, error) {
	definitions := make([]JobDefinition, 0, len(service.jobNames))
	for _, name := range service.jobNames {
		job := service.jobs[name]
		definition, err := job.schedule.Definition()
		if err != nil {
			return nil, fmt.Errorf("cron: job %q schedule projection: %w", name, err)
		}
		definitions = append(definitions, JobDefinition{
			Name:     name,
			Schedule: definition,
			CatchUp:  job.config.catchUp,
			Overlap:  job.config.overlap,
		})
	}
	states, err := service.store.Reconcile(ctx, definitions, now)
	if err != nil {
		return nil, fmt.Errorf("cron: reconcile static jobs: %w", err)
	}
	if len(states) != len(service.jobs) {
		return nil, fmt.Errorf("cron: reconcile returned %d active jobs, want %d", len(states), len(service.jobs))
	}

	seen := make(map[string]struct{}, len(states))
	jobs := make([]*runtimeJob, 0, len(states))
	for _, state := range states {
		name := state.Definition.Name
		registered, exists := service.jobs[name]
		if !exists {
			return nil, fmt.Errorf("cron: reconcile returned unknown job %q", name)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("cron: reconcile returned duplicate job %q", name)
		}
		seen[name] = struct{}{}
		if state.NextRunAt.IsZero() {
			return nil, fmt.Errorf("cron: reconcile returned zero next occurrence for job %q", name)
		}
		schedule, err := ScheduleFromDefinition(state.Definition.Schedule)
		if err != nil {
			return nil, fmt.Errorf("cron: reconstruct reconciled job %q schedule: %w", name, err)
		}
		registered.schedule = schedule
		registered.config.catchUp = state.Definition.CatchUp
		registered.config.overlap = state.Definition.Overlap
		jobs = append(jobs, &runtimeJob{registered: registered, schedule: schedule, state: state, startup: true})
	}
	return jobs, nil
}

func (service *Service) cycle(
	ctx context.Context,
	jobs []*runtimeJob,
	now time.Time,
	runtimeErrors chan<- error,
) (bool, error) {
	immediate, err := service.recoverExpired(ctx, jobs, now, runtimeErrors)
	if err != nil {
		return false, err
	}
	for _, job := range jobs {
		due, more, err := collectDue(job.schedule, job.state.NextRunAt, now, service.catchUpLimit)
		if err != nil {
			return false, fmt.Errorf("cron: job %q: %w", job.registered.name, err)
		}
		if len(due) == 0 {
			job.startup = false
			continue
		}
		firstToRun := occurrenceRunIndex(job.registered.config.catchUp, len(due), job.startup, more)
		for index, occurrence := range due {
			if index < firstToRun {
				reason := "superseded"
				if job.startup && job.registered.config.catchUp == CatchUpNone {
					reason = "catch_up_disabled"
				}
				if err := service.skip(ctx, job, occurrence, reason); err != nil {
					return false, err
				}
				continue
			}
			if _, err := service.claimAndRun(ctx, job, occurrence, runtimeErrors); err != nil {
				return false, err
			}
		}
		immediate = immediate || more
		if !more {
			job.startup = false
		}
	}
	return immediate, nil
}

func (service *Service) recoverExpired(
	ctx context.Context,
	jobs []*runtimeJob,
	now time.Time,
	runtimeErrors chan<- error,
) (bool, error) {
	expired, err := service.store.Expired(ctx, now, service.catchUpLimit)
	if err != nil {
		return false, fmt.Errorf("cron: list expired occurrence leases: %w", err)
	}
	byName := make(map[string]*runtimeJob, len(jobs))
	for _, job := range jobs {
		byName[job.registered.name] = job
	}
	progressed := false
	for _, record := range expired {
		job, exists := byName[record.JobName]
		if !exists {
			return false, fmt.Errorf("cron: expired occurrence %s references inactive job %q", record.ID, record.JobName)
		}
		nextRunAt, err := job.schedule.Next(record.ScheduledAt)
		if err != nil {
			return false, fmt.Errorf("cron: expired job %q next occurrence: %w", record.JobName, err)
		}
		disposition, err := service.claimAndRun(ctx, job, dueOccurrence{
			scheduledAt: record.ScheduledAt,
			nextRunAt:   nextRunAt,
		}, runtimeErrors)
		if err != nil {
			return false, err
		}
		progressed = progressed || disposition != ClaimLeaseHeld
	}
	return progressed && len(expired) == service.catchUpLimit, nil
}

func (service *Service) skip(
	ctx context.Context,
	job *runtimeJob,
	occurrence dueOccurrence,
	reason string,
) error {
	now := service.now().UTC()
	request := SkipRequest{
		OccurrenceID: OccurrenceID(job.registered.name, occurrence.scheduledAt),
		JobName:      job.registered.name,
		ScheduledAt:  occurrence.scheduledAt,
		NextRunAt:    occurrence.nextRunAt,
		SkippedAt:    now,
		Reason:       reason,
	}
	if err := service.store.Skip(ctx, request); err != nil {
		return fmt.Errorf("cron: skip job %q occurrence %s: %w", job.registered.name, request.OccurrenceID, err)
	}
	job.state.NextRunAt = occurrence.nextRunAt
	job.state.UpdatedAt = now
	return nil
}

func (service *Service) claimAndRun(
	ctx context.Context,
	job *runtimeJob,
	occurrence dueOccurrence,
	runtimeErrors chan<- error,
) (ClaimDisposition, error) {
	now := service.now().UTC()
	token, err := randomIdentity("lease")
	if err != nil {
		return "", fmt.Errorf("cron: job %q generate lease token: %w", job.registered.name, err)
	}
	request := ClaimRequest{
		OccurrenceID: OccurrenceID(job.registered.name, occurrence.scheduledAt),
		JobName:      job.registered.name,
		ScheduledAt:  occurrence.scheduledAt,
		NextRunAt:    occurrence.nextRunAt,
		LeaseOwner:   service.leaseOwner,
		LeaseToken:   token,
		ClaimedAt:    now,
		LeaseUntil:   now.Add(service.leaseDuration),
	}
	result, err := service.store.Claim(ctx, request)
	if err != nil {
		return "", fmt.Errorf("cron: claim job %q occurrence %s: %w", job.registered.name, request.OccurrenceID, err)
	}
	if job.state.NextRunAt.Before(occurrence.nextRunAt) {
		job.state.NextRunAt = occurrence.nextRunAt
	}
	job.state.UpdatedAt = now
	if result.Disposition != ClaimAcquired {
		return result.Disposition, nil
	}
	invocation := Invocation{
		ID:          result.Occurrence.ID,
		JobName:     result.Occurrence.JobName,
		ScheduledAt: result.Occurrence.ScheduledAt,
		StartedAt:   result.Occurrence.StartedAt,
		Attempt:     result.Occurrence.Attempt,
	}
	service.startInvocation(ctx, job.registered.handler, invocation, token, runtimeErrors)
	return result.Disposition, nil
}

func (service *Service) startInvocation(
	parent context.Context,
	handler JobFunc,
	invocation Invocation,
	leaseToken string,
	runtimeErrors chan<- error,
) {
	service.workers.Add(1)
	go func() {
		defer service.workers.Done()
		jobContext, cancel := context.WithCancel(parent)
		renewed := service.renew(jobContext, cancel, invocation.ID, leaseToken)
		jobError := callJob(jobContext, handler, invocation)
		cancel()
		renewError := <-renewed

		status := OccurrenceSucceeded
		switch {
		case errors.Is(jobError, context.Canceled) || errors.Is(jobError, context.DeadlineExceeded):
			status = OccurrenceCanceled
		case jobError != nil:
			status = OccurrenceFailed
		}
		finishedAt := service.now().UTC()
		completionContext, completionCancel := context.WithTimeout(context.Background(), completionTimeout)
		completionError := service.store.Complete(completionContext, Completion{
			OccurrenceID: invocation.ID,
			LeaseToken:   leaseToken,
			Status:       status,
			FinishedAt:   finishedAt,
			Error:        boundedError(jobError),
		})
		completionCancel()
		if renewError != nil || completionError != nil {
			combined := errors.Join(renewError, completionError)
			service.reportRuntimeError(runtimeErrors, fmt.Errorf("cron: persist job %q occurrence %s: %w", invocation.JobName, invocation.ID, combined))
		}
	}()
}

func (service *Service) renew(
	ctx context.Context,
	cancel context.CancelFunc,
	occurrenceID string,
	leaseToken string,
) <-chan error {
	done := make(chan error, 1)
	interval := service.leaseDuration / 3
	if interval <= 0 {
		interval = service.leaseDuration
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- nil
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					done <- nil
					return
				}
				renewedAt := service.now().UTC()
				if err := service.store.Renew(ctx, LeaseRenewal{
					OccurrenceID: occurrenceID,
					LeaseToken:   leaseToken,
					RenewedAt:    renewedAt,
					LeaseUntil:   renewedAt.Add(service.leaseDuration),
				}); err != nil {
					// Handler completion and a renewal tick can become ready at
					// the same instant. Cancellation after normal completion wins;
					// it is not a scheduler failure.
					if ctx.Err() != nil {
						done <- nil
						return
					}
					cancel()
					done <- fmt.Errorf("renew lease: %w", err)
					return
				}
			}
		}
	}()
	return done
}

func (service *Service) reportRuntimeError(target chan<- error, err error) {
	select {
	case target <- err:
	default:
	}
}

func (service *Service) beginRun() error {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.running {
		return ErrAlreadyRunning
	}
	service.running = true
	return nil
}

func (service *Service) endRun() {
	service.mu.Lock()
	service.running = false
	service.mu.Unlock()
}

func (service *Service) isRunning() bool {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.running
}

func collectDue(schedule Schedule, next, now time.Time, limit int) ([]dueOccurrence, bool, error) {
	occurrences := make([]dueOccurrence, 0)
	for !next.After(now) {
		if len(occurrences) == limit {
			return occurrences, true, nil
		}
		following, err := schedule.Next(next)
		if err != nil {
			return nil, false, err
		}
		occurrences = append(occurrences, dueOccurrence{
			scheduledAt: next.UTC(),
			nextRunAt:   following.UTC(),
		})
		next = following
	}
	return occurrences, false, nil
}

func occurrenceRunIndex(policy CatchUpPolicy, count int, startup, more bool) int {
	if count == 0 || policy == CatchUpAll {
		return 0
	}
	if startup && policy == CatchUpNone {
		return count
	}
	if more {
		return count
	}
	return count - 1
}

func nextWait(jobs []*runtimeJob, now time.Time, maximum time.Duration) time.Duration {
	next := jobs[0].state.NextRunAt
	for _, job := range jobs[1:] {
		if job.state.NextRunAt.Before(next) {
			next = job.state.NextRunAt
		}
	}
	if !next.After(now) {
		return 0
	}
	wait := next.Sub(now)
	if wait > maximum {
		return maximum
	}
	return wait
}

func (service *Service) recoveryInterval() time.Duration {
	interval := service.leaseDuration / 3
	if interval <= 0 {
		return service.leaseDuration
	}
	return interval
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func callJob(ctx context.Context, handler JobFunc, invocation Invocation) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("job panic: %v", recovered)
		}
	}()
	return handler(ctx, invocation)
}

func boundedError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToValidUTF8(err.Error(), "?")
	if len(message) <= maxRecordedError {
		return message
	}
	message = message[:maxRecordedError]
	for !utf8.ValidString(message) {
		message = message[:len(message)-1]
	}
	return strings.TrimSpace(message)
}

func randomIdentity(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(value), nil
}

func normalizeRunError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	cause := context.Cause(ctx)
	if cause != nil && (errors.Is(err, cause) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return nil
	}
	return err
}
