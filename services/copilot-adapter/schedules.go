package copilotadapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/cron"
	"github.com/google/uuid"
	"github.com/guregu/null/v5"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

const (
	errorCodeInvalidSchedule  = "invalid_schedule"
	errorCodeScheduleNotFound = "schedule_not_found"
	scheduleJobPrefix         = "chat/"
	scheduledAuthor           = "Scheduled task"
)

var errScheduleRuntimeNotRunning = errors.New("copilot-adapter schedules: runtime is not running")

type chatScheduleSubmission struct {
	IdempotencyKey uuid.UUID
	SessionID      uuid.UUID
	DisplayName    string
	Prompt         string
	CronExpression string
	Timezone       string
}

type scheduleReloadRequest struct {
	response chan scheduleReloadResult
}

type scheduleReloadResult struct {
	snapshot cron.StoreSnapshot
	err      error
}

// RunSchedules owns the reloadable Candace cron runtime. The mounting binary
// runs it beside HTTP; each active product row becomes an actual cron job, so
// cron owns recurrence, catch-up, overlap, leases and occurrence completion.
func (adapter *CopilotAdapter) RunSchedules(ctx context.Context) error {
	runDone, err := adapter.beginScheduleRun()
	if err != nil {
		return err
	}
	defer adapter.endScheduleRun(runDone)

	var acknowledgements []chan scheduleReloadResult
	for {
		options, definitions, snapshot, err := adapter.reconcileSchedules(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			acknowledgeScheduleReloads(acknowledgements, cron.StoreSnapshot{}, err)
			return err
		}
		acknowledgeScheduleReloads(acknowledgements, snapshot, nil)
		acknowledgements = nil
		if len(definitions) == 0 {
			select {
			case <-ctx.Done():
				return nil
			case request := <-adapter.scheduleReload:
				if request.response != nil {
					acknowledgements = append(acknowledgements, request.response)
				}
				continue
			}
		}
		runtime, err := cron.New(options...)
		if err != nil {
			return fmt.Errorf("copilot-adapter schedules: construct runtime: %w", err)
		}
		runContext, cancel := context.WithCancel(ctx)
		finished := make(chan error, 1)
		go func() { finished <- runtime.Run(runContext) }()
		select {
		case <-ctx.Done():
			cancel()
			<-finished
			return nil
		case request := <-adapter.scheduleReload:
			cancel()
			if err := <-finished; err != nil {
				if request.response != nil {
					acknowledgeScheduleReloads([]chan scheduleReloadResult{request.response}, cron.StoreSnapshot{}, err)
				}
				return err
			}
			if request.response != nil {
				acknowledgements = append(acknowledgements, request.response)
			}
		case err := <-finished:
			cancel()
			return err
		}
	}
}

func (adapter *CopilotAdapter) beginScheduleRun() (chan struct{}, error) {
	adapter.scheduleRunMutex.Lock()
	defer adapter.scheduleRunMutex.Unlock()
	if adapter.scheduleRunActive {
		return nil, fmt.Errorf("copilot-adapter schedules: %w", cron.ErrAlreadyRunning)
	}
	done := make(chan struct{})
	adapter.scheduleRunActive = true
	adapter.scheduleRunDone = done
	return done, nil
}

func (adapter *CopilotAdapter) endScheduleRun(done chan struct{}) {
	adapter.scheduleRunMutex.Lock()
	defer adapter.scheduleRunMutex.Unlock()
	if adapter.scheduleRunDone != done {
		return
	}
	adapter.scheduleRunActive = false
	adapter.scheduleRunDone = nil
	close(done)
}

func (adapter *CopilotAdapter) scheduleRunState() (bool, <-chan struct{}) {
	adapter.scheduleRunMutex.Lock()
	defer adapter.scheduleRunMutex.Unlock()
	return adapter.scheduleRunActive, adapter.scheduleRunDone
}

func (adapter *CopilotAdapter) reconcileSchedules(ctx context.Context) (
	[]cron.Option,
	[]cron.JobDefinition,
	cron.StoreSnapshot,
	error,
) {
	rows, err := adapter.store.ListChatSchedules(ctx)
	if err != nil {
		return nil, nil, cron.StoreSnapshot{}, fmt.Errorf("copilot-adapter schedules: list: %w", err)
	}
	options := []cron.Option{cron.WithStore(adapter.scheduleStore)}
	definitions := make([]cron.JobDefinition, 0, len(rows))
	for _, row := range rows {
		if row.Status != string(api.ChatScheduleStatusActive) {
			continue
		}
		schedule, err := parseChatSchedule(row.CronExpression, row.Timezone)
		if err != nil {
			return nil, nil, cron.StoreSnapshot{}, fmt.Errorf("copilot-adapter schedules: %s: %w", row.ID, err)
		}
		definition, err := schedule.Definition()
		if err != nil {
			return nil, nil, cron.StoreSnapshot{}, fmt.Errorf("copilot-adapter schedules: define %s: %w", row.ID, err)
		}
		definitions = append(definitions, cron.JobDefinition{
			Name: scheduleJobName(row.ID), Schedule: definition,
			CatchUp: cron.CatchUpLatest, Overlap: cron.OverlapSkip,
		})
		captured := row
		options = append(options, cron.WithJob(
			scheduleJobName(row.ID), schedule,
			adapter.chatScheduleJob(captured),
			cron.WithCatchUp(cron.CatchUpLatest),
			cron.WithOverlap(cron.OverlapSkip),
		))
	}
	if _, err := adapter.scheduleStore.Reconcile(ctx, definitions, time.Now().UTC()); err != nil {
		return nil, nil, cron.StoreSnapshot{}, fmt.Errorf("copilot-adapter schedules: reconcile: %w", err)
	}
	snapshot, err := adapter.scheduleStore.Snapshot(ctx)
	if err != nil {
		return nil, nil, cron.StoreSnapshot{}, fmt.Errorf("copilot-adapter schedules: snapshot: %w", err)
	}
	return options, definitions, snapshot, nil
}

func acknowledgeScheduleReloads(responses []chan scheduleReloadResult, snapshot cron.StoreSnapshot, err error) {
	for _, response := range responses {
		response <- scheduleReloadResult{snapshot: snapshot, err: err}
	}
}

func (adapter *CopilotAdapter) chatScheduleJob(captured storedb.ChatSchedule) cron.JobFunc {
	return func(ctx context.Context, invocation cron.Invocation) error {
		unlock := adapter.mutations.lock(captured.SessionID)
		defer unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := adapter.store.GetChatSchedule(ctx, captured.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("copilot-adapter schedule %s status: %w", captured.ID, err)
		}
		if current.Status != string(api.ChatScheduleStatusActive) ||
			!current.UpdatedAt.Equal(captured.UpdatedAt) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		_, _, err = adapter.submitPromptLocked(ctx, current.SessionID, promptSubmission{
			Text: current.Prompt, Mode: api.Queue, Author: scheduledAuthor,
			ScheduleOccurrenceID: invocation.ID,
		})
		return err
	}
}

func (adapter *CopilotAdapter) reloadSchedules() {
	select {
	case adapter.scheduleReload <- scheduleReloadRequest{}:
	default:
	}
}

func (adapter *CopilotAdapter) reloadSchedulesAndSnapshot(ctx context.Context) (cron.StoreSnapshot, error) {
	running, done := adapter.scheduleRunState()
	if !running {
		return cron.StoreSnapshot{}, errScheduleRuntimeNotRunning
	}
	response := make(chan scheduleReloadResult, 1)
	select {
	case <-ctx.Done():
		return cron.StoreSnapshot{}, ctx.Err()
	case <-done:
		return cron.StoreSnapshot{}, errScheduleRuntimeNotRunning
	case adapter.scheduleReload <- scheduleReloadRequest{response: response}:
	}
	select {
	case <-ctx.Done():
		return cron.StoreSnapshot{}, ctx.Err()
	case <-done:
		return cron.StoreSnapshot{}, errScheduleRuntimeNotRunning
	case result := <-response:
		return result.snapshot, result.err
	}
}

// reconcileScheduleMutation uses process-owned time only after a metadata
// mutation attempt. A disconnected caller cannot strand durable schedule state
// outside the live runtime.
func (adapter *CopilotAdapter) reconcileScheduleMutation() (cron.StoreSnapshot, error) {
	durableContext, cancel := adapter.durableTransitionContext()
	defer cancel()
	return adapter.reloadSchedulesAndSnapshot(durableContext)
}

func (adapter *CopilotAdapter) reconcileScheduleMutationUnless(reconciled *bool) {
	if *reconciled {
		return
	}
	if _, err := adapter.reconcileScheduleMutation(); err != nil && !errors.Is(err, errScheduleRuntimeNotRunning) {
		adapter.logger.Warn("reconcile schedule runtime after mutation", "error", err)
	}
}

// ListChatSchedules returns product metadata joined with cron's runtime state.
func (adapter *CopilotAdapter) ListChatSchedules(ctx context.Context, request api.ListChatSchedulesRequestObject) (api.ListChatSchedulesResponseObject, error) {
	ctx = requestContext(ctx)
	rows, err := adapter.store.ListChatSchedules(ctx)
	if err != nil {
		return nil, storeFailure(err)
	}
	snapshot, err := adapter.scheduleStore.Snapshot(ctx)
	if err != nil {
		return nil, storeFailure(err)
	}
	data := make([]api.ChatSchedule, 0, len(rows))
	for _, row := range rows {
		data = append(data, chatScheduleView(row, snapshot))
	}
	sortChatSchedules(data)
	return api.ListChatSchedules200JSONResponse(api.ChatScheduleList{Data: data}), nil
}

func sortChatSchedules(schedules []api.ChatSchedule) {
	slices.SortFunc(schedules, compareChatSchedules)
}

func compareChatSchedules(left api.ChatSchedule, right api.ChatSchedule) int {
	leftRank, rightRank := chatScheduleSortRank(left), chatScheduleSortRank(right)
	if leftRank != rightRank {
		return leftRank - rightRank
	}
	if left.NextRunAt != nil && right.NextRunAt != nil {
		if order := left.NextRunAt.Compare(*right.NextRunAt); order != 0 {
			return order
		}
	}
	if left.CreatedAt != nil && right.CreatedAt != nil {
		if order := right.CreatedAt.Compare(*left.CreatedAt); order != 0 {
			return order
		}
	}
	leftID, rightID := "", ""
	if left.Id != nil {
		leftID = left.Id.String()
	}
	if right.Id != nil {
		rightID = right.Id.String()
	}
	return strings.Compare(rightID, leftID)
}

func chatScheduleSortRank(schedule api.ChatSchedule) int {
	if schedule.NextRunAt != nil {
		return 0
	}
	if schedule.Status == api.ChatScheduleStatusActive {
		return 1
	}
	return 2
}

// CreateChatSchedule validates through candace/pkg/cron and stores its
// canonical five-field expression as relational product metadata.
func (adapter *CopilotAdapter) CreateChatSchedule(ctx context.Context, request api.CreateChatScheduleRequestObject) (api.CreateChatScheduleResponseObject, error) {
	ctx = requestContext(ctx)
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidBody, "a schedule body is required")
	}
	if request.Body.IdempotencyKey == uuid.Nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidRequest, "idempotencyKey is required")
	}
	schedule, err := parseChatSchedule(request.Body.CronExpression, request.Body.Timezone)
	if err != nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidSchedule, err.Error())
	}
	canonical, err := schedule.Canonical()
	if err != nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidSchedule, err.Error())
	}
	submission := chatScheduleSubmission{
		IdempotencyKey: request.Body.IdempotencyKey, SessionID: request.Body.SessionId,
		DisplayName: request.Body.DisplayName, Prompt: request.Body.Prompt,
		CronExpression: canonical, Timezone: request.Body.Timezone,
	}
	unlockControl := adapter.scheduleControls.lock(submission.SessionID)
	defer unlockControl()
	reconciled := false
	defer adapter.reconcileScheduleMutationUnless(&reconciled)
	row, err := adapter.persistChatScheduleCreation(ctx, submission)
	if err != nil {
		return nil, err
	}
	snapshot, err := adapter.reconcileScheduleMutation()
	if err != nil {
		return nil, storeFailure(err)
	}
	reconciled = true
	if err := ctx.Err(); err != nil {
		return nil, storeFailure(err)
	}
	return api.CreateChatSchedule201JSONResponse(chatScheduleView(row, snapshot)), nil
}

func (adapter *CopilotAdapter) persistChatScheduleCreation(
	ctx context.Context,
	submission chatScheduleSubmission,
) (storedb.ChatSchedule, error) {
	unlock := adapter.mutations.lock(submission.SessionID)
	defer unlock()
	if row, replayed, replayErr := loadChatScheduleCreation(ctx, adapter.store, submission); replayErr != nil {
		return storedb.ChatSchedule{}, chatScheduleCreationFailure(replayErr)
	} else if replayed {
		return row, nil
	}
	session, err := adapter.store.GetSession(ctx, submission.SessionID)
	if err != nil {
		return storedb.ChatSchedule{}, lookupFailure(err, errorCodeSessionNotFound, "no session with that id")
	}
	if session.Status == string(api.SessionStatusEnded) || session.Status == string(api.SessionStatusFailed) {
		return storedb.ChatSchedule{}, fail(http.StatusConflict, errorCodeSessionTerminal, "the session has ended")
	}
	if err := rejectStartingSession(session); err != nil {
		return storedb.ChatSchedule{}, err
	}
	now := time.Now().UTC()
	scheduleID := uuid.New()
	var row storedb.ChatSchedule
	err = adapter.store.Transact(ctx, func(queries storedb.Querier) error {
		_, claimErr := queries.ClaimChatScheduleCreation(ctx, storedb.ClaimChatScheduleCreationParams{
			IdempotencyKey: submission.IdempotencyKey, ScheduleID: scheduleID,
			SessionID: submission.SessionID, DisplayName: submission.DisplayName,
			Prompt: submission.Prompt, CronExpression: submission.CronExpression,
			Timezone: submission.Timezone, CreatedAt: now,
		})
		if errors.Is(claimErr, sql.ErrNoRows) {
			var replayed bool
			row, replayed, claimErr = loadChatScheduleCreation(ctx, queries, submission)
			if claimErr == nil && !replayed {
				claimErr = errors.New("claimed schedule creation receipt is missing")
			}
			return claimErr
		}
		if claimErr != nil {
			return claimErr
		}
		row, claimErr = queries.CreateChatSchedule(ctx, storedb.CreateChatScheduleParams{
			ID: scheduleID, SessionID: submission.SessionID, DisplayName: submission.DisplayName,
			Prompt: submission.Prompt, CronExpression: submission.CronExpression, Timezone: submission.Timezone,
			Status: string(api.ChatScheduleStatusActive), CreatedAt: now, UpdatedAt: now,
		})
		return claimErr
	})
	if err != nil {
		transactionErr := err
		reconcileContext, cancelReconcile := adapter.durableTransitionContext()
		var replayed bool
		var reconcileErr error
		row, replayed, reconcileErr = loadChatScheduleCreation(reconcileContext, adapter.store, submission)
		cancelReconcile()
		if reconcileErr != nil {
			var requestFailure failure
			if errors.As(reconcileErr, &requestFailure) {
				return storedb.ChatSchedule{}, requestFailure
			}
			return storedb.ChatSchedule{}, storeFailure(errors.Join(transactionErr, reconcileErr))
		}
		if !replayed {
			return storedb.ChatSchedule{}, chatScheduleCreationFailure(transactionErr)
		}
	}
	return row, nil
}

func loadChatScheduleCreation(
	ctx context.Context,
	queries storedb.Querier,
	submission chatScheduleSubmission,
) (storedb.ChatSchedule, bool, error) {
	receipt, err := queries.GetChatScheduleCreation(ctx, submission.IdempotencyKey)
	if errors.Is(err, sql.ErrNoRows) {
		return storedb.ChatSchedule{}, false, nil
	}
	if err != nil {
		return storedb.ChatSchedule{}, false, err
	}
	if !sameChatScheduleCreation(receipt, submission) {
		return storedb.ChatSchedule{}, true, fail(
			http.StatusConflict,
			errorCodeIdempotencyKeyReused,
			"idempotencyKey already identifies a different schedule request",
		)
	}
	row, err := queries.GetChatScheduleByCreationKey(ctx, submission.IdempotencyKey)
	return row, true, err
}

func sameChatScheduleCreation(receipt storedb.ChatScheduleCreation, submission chatScheduleSubmission) bool {
	return receipt.IdempotencyKey == submission.IdempotencyKey && receipt.SessionID == submission.SessionID &&
		receipt.DisplayName == submission.DisplayName && receipt.Prompt == submission.Prompt &&
		receipt.CronExpression == submission.CronExpression && receipt.Timezone == submission.Timezone
}

func chatScheduleCreationFailure(err error) error {
	var requestFailure failure
	if errors.As(err, &requestFailure) {
		return requestFailure
	}
	return storeFailure(err)
}

// GetChatSchedule reads one schedule joined with cron state.
func (adapter *CopilotAdapter) GetChatSchedule(ctx context.Context, request api.GetChatScheduleRequestObject) (api.GetChatScheduleResponseObject, error) {
	ctx = requestContext(ctx)
	row, err := adapter.store.GetChatSchedule(ctx, request.ScheduleId)
	if err != nil {
		return nil, lookupFailure(err, errorCodeScheduleNotFound, "no schedule with that id")
	}
	snapshot, err := adapter.scheduleStore.Snapshot(ctx)
	if err != nil {
		return nil, storeFailure(err)
	}
	return api.GetChatSchedule200JSONResponse(chatScheduleView(row, snapshot)), nil
}

// UpdateChatSchedule edits metadata or pauses the corresponding cron job.
func (adapter *CopilotAdapter) UpdateChatSchedule(ctx context.Context, request api.UpdateChatScheduleRequestObject) (api.UpdateChatScheduleResponseObject, error) {
	ctx = requestContext(ctx)
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidBody, "a schedule patch is required")
	}
	if request.Body.DisplayName == nil && request.Body.Prompt == nil && request.Body.CronExpression == nil &&
		request.Body.Timezone == nil && request.Body.Status == nil {
		return nil, fail(http.StatusBadRequest, errorCodeEmptyPatch,
			"at least one of displayName, prompt, cronExpression, timezone or status is required")
	}
	row, err := adapter.store.GetChatSchedule(ctx, request.ScheduleId)
	if err != nil {
		return nil, lookupFailure(err, errorCodeScheduleNotFound, "no schedule with that id")
	}
	unlockControl := adapter.scheduleControls.lock(row.SessionID)
	defer unlockControl()
	reconciled := false
	defer adapter.reconcileScheduleMutationUnless(&reconciled)
	row, err = adapter.persistChatScheduleUpdate(ctx, request, row.SessionID)
	if err != nil {
		return nil, err
	}
	snapshot, err := adapter.reconcileScheduleMutation()
	if err != nil {
		return nil, storeFailure(err)
	}
	reconciled = true
	if err := ctx.Err(); err != nil {
		return nil, storeFailure(err)
	}
	return api.UpdateChatSchedule200JSONResponse(chatScheduleView(row, snapshot)), nil
}

func (adapter *CopilotAdapter) persistChatScheduleUpdate(
	ctx context.Context,
	request api.UpdateChatScheduleRequestObject,
	sessionID uuid.UUID,
) (storedb.ChatSchedule, error) {
	unlock := adapter.mutations.lock(sessionID)
	defer unlock()
	row, err := adapter.store.GetChatSchedule(ctx, request.ScheduleId)
	if err != nil {
		return storedb.ChatSchedule{}, lookupFailure(err, errorCodeScheduleNotFound, "no schedule with that id")
	}
	session, sessionErr := adapter.store.GetSession(ctx, row.SessionID)
	if sessionErr != nil {
		return storedb.ChatSchedule{}, lookupFailure(sessionErr, errorCodeSessionNotFound, "no session with that id")
	}
	if err := rejectStartingSession(session); err != nil {
		return storedb.ChatSchedule{}, err
	}
	displayName, prompt := row.DisplayName, row.Prompt
	expression, timezone, status := row.CronExpression, row.Timezone, row.Status
	if request.Body.DisplayName != nil {
		displayName = *request.Body.DisplayName
	}
	if request.Body.Prompt != nil {
		prompt = *request.Body.Prompt
	}
	if request.Body.CronExpression != nil {
		expression = *request.Body.CronExpression
	}
	if request.Body.Timezone != nil {
		timezone = *request.Body.Timezone
	}
	if request.Body.Status != nil {
		status = string(*request.Body.Status)
	}
	schedule, err := parseChatSchedule(expression, timezone)
	if err != nil {
		return storedb.ChatSchedule{}, fail(http.StatusBadRequest, errorCodeInvalidSchedule, err.Error())
	}
	if status == string(api.ChatScheduleStatusActive) {
		if session.Status == string(api.SessionStatusEnded) || session.Status == string(api.SessionStatusFailed) {
			return storedb.ChatSchedule{}, fail(http.StatusConflict, errorCodeSessionTerminal, "the session has ended")
		}
	}
	expression, err = schedule.Canonical()
	if err != nil {
		return storedb.ChatSchedule{}, fail(http.StatusBadRequest, errorCodeInvalidSchedule, err.Error())
	}
	row, err = adapter.store.UpdateChatSchedule(ctx, storedb.UpdateChatScheduleParams{
		DisplayName: displayName, Prompt: prompt, CronExpression: expression, Timezone: timezone,
		Status: status, UpdatedAt: time.Now().UTC(), ID: request.ScheduleId,
	})
	if err != nil {
		return storedb.ChatSchedule{}, storeFailure(err)
	}
	return row, nil
}

// DeleteChatSchedule soft-deletes metadata and disables its cron job on the
// next immediate reconciliation.
func (adapter *CopilotAdapter) DeleteChatSchedule(ctx context.Context, request api.DeleteChatScheduleRequestObject) (api.DeleteChatScheduleResponseObject, error) {
	ctx = requestContext(ctx)
	row, err := adapter.store.GetChatSchedule(ctx, request.ScheduleId)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fail(http.StatusNotFound, errorCodeScheduleNotFound, "no schedule with that id")
	}
	if err != nil {
		return nil, storeFailure(err)
	}
	unlockControl := adapter.scheduleControls.lock(row.SessionID)
	defer unlockControl()
	reconciled := false
	defer adapter.reconcileScheduleMutationUnless(&reconciled)
	err = adapter.persistChatScheduleDeletion(ctx, request.ScheduleId, row.SessionID)
	if err != nil {
		return nil, err
	}
	if _, err = adapter.reconcileScheduleMutation(); err != nil {
		adapter.logger.Warn("reconcile schedule runtime after deletion", "scheduleId", request.ScheduleId, "error", err)
	} else {
		reconciled = true
	}
	return api.DeleteChatSchedule204Response{}, nil
}

func (adapter *CopilotAdapter) persistChatScheduleDeletion(
	ctx context.Context,
	scheduleID uuid.UUID,
	sessionID uuid.UUID,
) error {
	unlock := adapter.mutations.lock(sessionID)
	defer unlock()
	row, err := adapter.store.GetChatSchedule(ctx, scheduleID)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(http.StatusNotFound, errorCodeScheduleNotFound, "no schedule with that id")
	}
	if err != nil {
		return storeFailure(err)
	}
	session, sessionErr := adapter.store.GetSession(ctx, row.SessionID)
	if sessionErr != nil {
		return lookupFailure(sessionErr, errorCodeSessionNotFound, "no session with that id")
	}
	if err := rejectStartingSession(session); err != nil {
		return err
	}
	_, err = adapter.store.DeleteChatSchedule(ctx, storedb.DeleteChatScheduleParams{
		ID: scheduleID, DeletedAt: null.TimeFrom(time.Now().UTC()),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fail(http.StatusNotFound, errorCodeScheduleNotFound, "no schedule with that id")
	}
	if err != nil {
		return storeFailure(err)
	}
	return nil
}

func parseChatSchedule(expression string, timezone string) (cron.Schedule, error) {
	if strings.EqualFold(timezone, "local") {
		return cron.Schedule{}, fmt.Errorf("timezone Local is not portable")
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return cron.Schedule{}, fmt.Errorf("timezone: %w", err)
	}
	schedule := cron.Spec(cron.Raw(expression)).In(location)
	if err := schedule.Validate(); err != nil {
		return cron.Schedule{}, err
	}
	return schedule, nil
}

func scheduleJobName(identifier uuid.UUID) string {
	return scheduleJobPrefix + identifier.String()
}

func chatScheduleView(row storedb.ChatSchedule, snapshot cron.StoreSnapshot) api.ChatSchedule {
	id, sessionID := row.ID, row.SessionID
	created, updated := row.CreatedAt.UTC(), row.UpdatedAt.UTC()
	view := api.ChatSchedule{
		Id: &id, SessionId: &sessionID, DisplayName: row.DisplayName, Prompt: row.Prompt,
		CronExpression: row.CronExpression, Timezone: row.Timezone,
		Status: api.ChatScheduleStatus(row.Status), CreatedAt: &created, UpdatedAt: &updated,
	}
	jobName := scheduleJobName(row.ID)
	for _, job := range snapshot.Jobs {
		if job.Definition.Name == jobName && row.Status == string(api.ChatScheduleStatusActive) {
			next := job.NextRunAt.UTC()
			view.NextRunAt = &next
			break
		}
	}
	var latest *cron.OccurrenceRecord
	for index := range snapshot.Occurrences {
		occurrence := &snapshot.Occurrences[index]
		if occurrence.JobName == jobName && (latest == nil || occurrence.ScheduledAt.After(latest.ScheduledAt)) {
			latest = occurrence
		}
	}
	if latest != nil {
		last := latest.ScheduledAt.UTC()
		view.LastRunAt = &last
		status := api.ChatScheduleRunStatus(latest.Status)
		view.LastRunStatus = &status
		if latest.Error != "" {
			view.LastError = &latest.Error
		} else if latest.SkipReason != "" {
			view.LastError = &latest.SkipReason
		}
	}
	return view
}
