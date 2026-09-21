package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/guregu/null/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/pkg/patience"
	"github.com/candacelabs/csf/pkg/pgmem"

	"github.com/candacelabs/csf/pkg/httpserver"
	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/store"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

// lifecycleBudget is what one projection gets to land its rows in the
// emulator on a loaded host; generous on purpose (CS-9 counterweight 3).
var lifecycleBudget = patience.Budget{Within: 10 * time.Second, Interval: 25 * time.Millisecond}

var errAmbiguousCommit = errors.New("simulated lost commit acknowledgement")
var errForcedRollback = errors.New("simulated transaction rollback")
var errReconciliationRead = errors.New("simulated reconciliation read failure")
var errSessionEventSnapshot = errors.New("simulated session event snapshot failure")
var errSessionView = errors.New("simulated session view failure")
var errDeliveryAcknowledgement = errors.New("simulated delivery acknowledgement failure")

type modelChangeCall struct {
	model      string
	contextErr error
}

type ambiguousCommitStore struct {
	copilotadapter.IStore
	failCommitCountdown  atomic.Int32
	failGetSessionAfter  atomic.Int32
	failSessionEventRead atomic.Bool
	failSDKAttemptAck    atomic.Bool
	rollbackCountdown    atomic.Int32
	blockNextTransact    atomic.Bool
	holdNextTransact     atomic.Bool
	cancelAfterTransact  atomic.Bool
	cancelAfterUpdate    atomic.Bool
	failUpdateAck        atomic.Bool
	failDeleteAck        atomic.Bool
	failScheduleRead     atomic.Int32
	cancelAtSessionList  atomic.Bool
	observationMutex     sync.Mutex
	commitObserved       chan struct{}
	rollbackObserved     chan struct{}
	rollbackRetry        chan struct{}
	requestCancel        context.CancelFunc
	worktreeSessionsSeen chan struct{}
	transactHeld         chan struct{}
	transactRelease      chan struct{}
}

type scheduleCreationRaceStore struct {
	copilotadapter.IStore
	mutex       sync.Mutex
	key         uuid.UUID
	waiting     int
	release     chan struct{}
	claimLosers atomic.Int32
}

type scheduleCreationRaceQueries struct {
	storedb.Querier
	claimLosers *atomic.Int32
}

func (queries *scheduleCreationRaceQueries) ClaimChatScheduleCreation(
	ctx context.Context,
	parameters storedb.ClaimChatScheduleCreationParams,
) (storedb.ChatScheduleCreation, error) {
	receipt, err := queries.Querier.ClaimChatScheduleCreation(ctx, parameters)
	if errors.Is(err, sql.ErrNoRows) {
		queries.claimLosers.Add(1)
	}
	return receipt, err
}

func (store *scheduleCreationRaceStore) Transact(ctx context.Context, transaction copilotadapter.StoreTransaction) error {
	return store.IStore.Transact(ctx, func(queries storedb.Querier) error {
		return transaction(&scheduleCreationRaceQueries{Querier: queries, claimLosers: &store.claimLosers})
	})
}

func (store *scheduleCreationRaceStore) GetChatScheduleCreation(
	ctx context.Context,
	idempotencyKey uuid.UUID,
) (storedb.ChatScheduleCreation, error) {
	receipt, err := store.IStore.GetChatScheduleCreation(ctx, idempotencyKey)
	store.mutex.Lock()
	if idempotencyKey != store.key || !errors.Is(err, sql.ErrNoRows) {
		store.mutex.Unlock()
		return receipt, err
	}
	store.waiting++
	release := store.release
	if store.waiting == 2 {
		close(release)
	}
	store.mutex.Unlock()
	select {
	case <-ctx.Done():
		return storedb.ChatScheduleCreation{}, ctx.Err()
	case <-release:
		return receipt, err
	}
}

func (store *scheduleCreationRaceStore) arm(idempotencyKey uuid.UUID) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.key = idempotencyKey
	store.waiting = 0
	store.release = make(chan struct{})
	store.claimLosers.Store(0)
}

func (store *ambiguousCommitStore) Transact(ctx context.Context, transaction copilotadapter.StoreTransaction) error {
	if store.holdNextTransact.CompareAndSwap(true, false) {
		store.observationMutex.Lock()
		held, release := store.transactHeld, store.transactRelease
		store.transactHeld, store.transactRelease = nil, nil
		store.observationMutex.Unlock()
		close(held)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
		}
	}
	if store.blockNextTransact.CompareAndSwap(true, false) {
		store.observationMutex.Lock()
		retry := store.rollbackRetry
		store.rollbackRetry = nil
		store.observationMutex.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-retry:
		}
	}
	if store.rollbackCountdown.Load() > 0 && store.rollbackCountdown.Add(-1) == 0 {
		err := store.IStore.Transact(ctx, func(queries storedb.Querier) error {
			if err := transaction(queries); err != nil {
				return err
			}
			return errForcedRollback
		})
		store.observationMutex.Lock()
		observed := store.rollbackObserved
		store.rollbackObserved = nil
		if store.rollbackRetry != nil {
			store.blockNextTransact.Store(true)
		}
		store.observationMutex.Unlock()
		if observed != nil {
			close(observed)
		}
		return err
	}
	err := store.IStore.Transact(ctx, transaction)
	if err == nil {
		store.cancelCommittedRequest(&store.cancelAfterTransact)
	}
	if err == nil && store.failCommitCountdown.Load() > 0 && store.failCommitCountdown.Add(-1) == 0 {
		store.observationMutex.Lock()
		observed := store.commitObserved
		store.commitObserved = nil
		store.observationMutex.Unlock()
		if observed != nil {
			close(observed)
		}
		return errAmbiguousCommit
	}
	return err
}

func (store *ambiguousCommitStore) UpdateChatSchedule(
	ctx context.Context,
	parameters storedb.UpdateChatScheduleParams,
) (storedb.ChatSchedule, error) {
	row, err := store.IStore.UpdateChatSchedule(ctx, parameters)
	if err == nil {
		store.cancelCommittedRequest(&store.cancelAfterUpdate)
		if store.failUpdateAck.CompareAndSwap(true, false) {
			return storedb.ChatSchedule{}, errAmbiguousCommit
		}
	}
	return row, err
}

func (store *ambiguousCommitStore) DeleteChatSchedule(
	ctx context.Context,
	parameters storedb.DeleteChatScheduleParams,
) (uuid.UUID, error) {
	identifier, err := store.IStore.DeleteChatSchedule(ctx, parameters)
	if err == nil && store.failDeleteAck.CompareAndSwap(true, false) {
		return uuid.Nil, errAmbiguousCommit
	}
	return identifier, err
}

func (store *ambiguousCommitStore) GetChatScheduleCreation(
	ctx context.Context,
	idempotencyKey uuid.UUID,
) (storedb.ChatScheduleCreation, error) {
	if store.failScheduleRead.Load() > 0 && store.failScheduleRead.Add(-1) == 0 {
		return storedb.ChatScheduleCreation{}, errReconciliationRead
	}
	return store.IStore.GetChatScheduleCreation(ctx, idempotencyKey)
}

func (store *ambiguousCommitStore) ListWorktreeSessions(
	ctx context.Context,
	worktreeID uuid.UUID,
) ([]storedb.Session, error) {
	store.observationMutex.Lock()
	seen := store.worktreeSessionsSeen
	store.worktreeSessionsSeen = nil
	store.observationMutex.Unlock()
	if seen != nil {
		close(seen)
	}
	if store.cancelAtSessionList.CompareAndSwap(true, false) {
		store.observationMutex.Lock()
		cancel := store.requestCancel
		store.requestCancel = nil
		store.observationMutex.Unlock()
		cancel()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return store.IStore.ListWorktreeSessions(ctx, worktreeID)
}

func (store *ambiguousCommitStore) cancelCommittedRequest(armed *atomic.Bool) {
	if !armed.CompareAndSwap(true, false) {
		return
	}
	store.observationMutex.Lock()
	cancel := store.requestCancel
	store.requestCancel = nil
	store.observationMutex.Unlock()
	cancel()
}

func (store *ambiguousCommitStore) armCancelAfterScheduleCommit(
	cancel context.CancelFunc,
	update bool,
) {
	store.observationMutex.Lock()
	store.requestCancel = cancel
	store.observationMutex.Unlock()
	if update {
		store.cancelAfterUpdate.Store(true)
		return
	}
	store.cancelAfterTransact.Store(true)
}

func (store *ambiguousCommitStore) armScheduleUpdateAcknowledgementFailure() {
	store.failUpdateAck.Store(true)
}

func (store *ambiguousCommitStore) armScheduleDeleteAcknowledgementFailure() {
	store.failDeleteAck.Store(true)
}

func (store *ambiguousCommitStore) armScheduleCreationReconciliationFailure() {
	// The first read is the immutable replay preflight; fail the read that
	// follows a committed transaction whose acknowledgement was lost.
	store.failScheduleRead.Store(2)
	store.arm()
}

func (store *ambiguousCommitStore) armWorktreeSessionListCancellation(cancel context.CancelFunc) {
	store.observationMutex.Lock()
	store.requestCancel = cancel
	store.observationMutex.Unlock()
	store.cancelAtSessionList.Store(true)
}

func (store *ambiguousCommitStore) observeWorktreeSessionList() <-chan struct{} {
	seen := make(chan struct{})
	store.observationMutex.Lock()
	store.worktreeSessionsSeen = seen
	store.observationMutex.Unlock()
	return seen
}

func (store *ambiguousCommitStore) GetSession(ctx context.Context, identifier uuid.UUID) (storedb.Session, error) {
	if store.failGetSessionAfter.Load() > 0 && store.failGetSessionAfter.Add(-1) == 0 {
		return storedb.Session{}, errReconciliationRead
	}
	return store.IStore.GetSession(ctx, identifier)
}

func (store *ambiguousCommitStore) GetSessionEventVersion(
	ctx context.Context,
	parameters storedb.GetSessionEventVersionParams,
) (storedb.SessionEventVersion, error) {
	if store.failSessionEventRead.CompareAndSwap(true, false) {
		return storedb.SessionEventVersion{}, errReconciliationRead
	}
	return store.IStore.GetSessionEventVersion(ctx, parameters)
}

func (store *ambiguousCommitStore) BeginSessionCreationSDKAttempt(
	ctx context.Context,
	parameters storedb.BeginSessionCreationSDKAttemptParams,
) (storedb.SessionCreation, error) {
	receipt, err := store.IStore.BeginSessionCreationSDKAttempt(ctx, parameters)
	if err == nil && store.failSDKAttemptAck.CompareAndSwap(true, false) {
		return storedb.SessionCreation{}, errAmbiguousCommit
	}
	return receipt, err
}

func (store *ambiguousCommitStore) arm() {
	store.armAfter(1)
}

func (store *ambiguousCommitStore) armAfter(commits int32) {
	store.failCommitCountdown.Store(commits)
}

func (store *ambiguousCommitStore) armAndObserve() <-chan struct{} {
	observed := make(chan struct{})
	store.observationMutex.Lock()
	store.commitObserved = observed
	store.observationMutex.Unlock()
	store.arm()
	return observed
}

func (store *ambiguousCommitStore) armRollback() {
	store.armRollbackAfter(1)

}

func (store *ambiguousCommitStore) armRollbackAfter(commits int32) {
	store.rollbackCountdown.Store(commits)

}

func (store *ambiguousCommitStore) armRollbackAndPauseRetry() (<-chan struct{}, chan<- struct{}) {
	observed := make(chan struct{})
	retry := make(chan struct{})
	store.observationMutex.Lock()
	store.rollbackObserved = observed
	store.rollbackRetry = retry
	store.observationMutex.Unlock()
	store.armRollback()
	return observed, retry
}

func (store *ambiguousCommitStore) holdTransaction() (<-chan struct{}, chan<- struct{}) {
	held := make(chan struct{})
	release := make(chan struct{})
	store.observationMutex.Lock()
	store.transactHeld = held
	store.transactRelease = release
	store.observationMutex.Unlock()
	store.holdNextTransact.Store(true)
	return held, release
}

func (store *ambiguousCommitStore) armReconciliationReadFailure() {
	// CreateSession now performs a receipt replay lookup before provisioning;
	// fail the following read, which is the ambiguous-transaction
	// reconciliation this seam is intended to exercise.
	store.failGetSessionAfter.Store(2)
}

func (store *ambiguousCommitStore) armSessionEventReconciliationReadFailure() {
	store.failSessionEventRead.Store(true)
}

func (store *ambiguousCommitStore) armSDKAttemptAcknowledgementFailure() {
	store.failSDKAttemptAck.Store(true)
}

type sessionEventFailureStore struct {
	copilotadapter.IStore
	failNext atomic.Bool
}

type sessionEventFailureQueries struct {
	storedb.Querier
	failNext *atomic.Bool
}

func (queries *sessionEventFailureQueries) SnapshotSessionEvent(ctx context.Context, arg storedb.SnapshotSessionEventParams) (storedb.SessionEventVersion, error) {
	if queries.failNext.CompareAndSwap(true, false) {
		return storedb.SessionEventVersion{}, errSessionEventSnapshot
	}
	return queries.Querier.SnapshotSessionEvent(ctx, arg)
}

func (store *sessionEventFailureStore) Transact(ctx context.Context, transaction copilotadapter.StoreTransaction) error {
	return store.IStore.Transact(ctx, func(queries storedb.Querier) error {
		return transaction(&sessionEventFailureQueries{Querier: queries, failNext: &store.failNext})
	})
}

func (store *sessionEventFailureStore) arm() {
	store.failNext.Store(true)
}

type sessionViewFailureStore struct {
	copilotadapter.IStore
	failNext atomic.Bool
	attempts atomic.Int32
}

func (store *sessionViewFailureStore) CountSessionTurns(ctx context.Context, sessionID uuid.UUID) (int64, error) {
	store.attempts.Add(1)
	if store.failNext.CompareAndSwap(true, false) {
		return 0, errSessionView
	}
	return store.IStore.CountSessionTurns(ctx, sessionID)
}

func (store *sessionViewFailureStore) arm() {
	store.failNext.Store(true)
	store.attempts.Store(0)
}

type failBeforeLockStore struct {
	copilotadapter.IStore
	failNext atomic.Bool
}

type failBeforeLockQueries struct {
	storedb.Querier
	failNext *atomic.Bool
}

func (queries *failBeforeLockQueries) LockSession(ctx context.Context, sessionID uuid.UUID) (storedb.Session, error) {
	if queries.failNext.CompareAndSwap(true, false) {
		if _, err := queries.UpdateSessionStatus(ctx, storedb.UpdateSessionStatusParams{
			ID: sessionID, Status: string(api.SessionStatusFailed), UpdatedAt: time.Now().UTC(),
		}); err != nil {
			return storedb.Session{}, err
		}
	}
	return queries.Querier.LockSession(ctx, sessionID)
}

func (store *failBeforeLockStore) Transact(ctx context.Context, transaction copilotadapter.StoreTransaction) error {
	return store.IStore.Transact(ctx, func(queries storedb.Querier) error {
		return transaction(&failBeforeLockQueries{Querier: queries, failNext: &store.failNext})
	})
}

func (store *failBeforeLockStore) arm() {
	store.failNext.Store(true)
}

type deliveryAcknowledgementFailureStore struct {
	copilotadapter.IStore
	failNext  atomic.Bool
	mutex     sync.Mutex
	deadlines []time.Time
}

func (store *deliveryAcknowledgementFailureStore) MarkTurnDeliveryAccepted(
	ctx context.Context,
	identifier uuid.UUID,
) (storedb.Turn, error) {
	deadline, hasDeadline := ctx.Deadline()
	store.mutex.Lock()
	if hasDeadline {
		store.deadlines = append(store.deadlines, deadline)
	}
	store.mutex.Unlock()
	if store.failNext.CompareAndSwap(true, false) {
		return storedb.Turn{}, errDeliveryAcknowledgement
	}
	return store.IStore.MarkTurnDeliveryAccepted(ctx, identifier)
}

func (store *deliveryAcknowledgementFailureStore) arm() {
	store.failNext.Store(true)
}

func (store *deliveryAcknowledgementFailureStore) observedDeadlines() []time.Time {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return append([]time.Time(nil), store.deadlines...)
}

type abortProjectionFixture struct {
	targetTurnID       uuid.UUID
	successorTurnID    uuid.UUID
	targetRequestID    uuid.UUID
	successorRequestID uuid.UUID
}

func seedAbortProjectionFixture(
	ctx context.Context,
	queries *storedb.Queries,
	sessionID uuid.UUID,
	now time.Time,
) abortProjectionFixture {
	fixture := abortProjectionFixture{
		targetTurnID:       uuid.New(),
		successorTurnID:    uuid.New(),
		targetRequestID:    uuid.New(),
		successorRequestID: uuid.New(),
	}
	for _, parameters := range []storedb.CreateTurnParams{
		{ID: fixture.targetTurnID, SessionID: sessionID, Status: string(api.TurnStatusRunning), PromptText: "turn A", PromptMode: string(api.Queue), CreatedAt: now, DeliveryStatus: "accepted"},
		{ID: fixture.successorTurnID, SessionID: sessionID, Status: string(api.TurnStatusQueued), PromptText: "turn B", PromptMode: string(api.Queue), CreatedAt: now.Add(time.Millisecond), DeliveryStatus: "accepted"},
	} {
		_, err := queries.CreateTurn(ctx, parameters)
		Expect(err).NotTo(HaveOccurred())
	}
	_, err := queries.UpdateSessionStatus(ctx, storedb.UpdateSessionStatusParams{
		ID: sessionID, Status: string(api.SessionStatusRunning), UpdatedAt: now,
	})
	Expect(err).NotTo(HaveOccurred())
	for _, parameters := range []storedb.InsertSessionRequestParams{
		{ID: fixture.targetRequestID, SessionID: sessionID, TurnID: &fixture.targetTurnID, Kind: string(api.Permission), Status: string(api.Pending), Prompt: "allow A", CreatedAt: now},
		{ID: fixture.successorRequestID, SessionID: sessionID, TurnID: &fixture.successorTurnID, Kind: string(api.Permission), Status: string(api.Pending), Prompt: "allow B", CreatedAt: now.Add(time.Millisecond)},
	} {
		_, err = queries.InsertSessionRequest(ctx, parameters)
		Expect(err).NotTo(HaveOccurred())
	}
	return fixture
}

func seedChatSchedule(
	ctx context.Context,
	queries *storedb.Queries,
	sessionID uuid.UUID,
	status api.ChatScheduleStatus,
	expression string,
) storedb.ChatSchedule {
	GinkgoHelper()
	now := time.Now().UTC()
	row, err := queries.CreateChatSchedule(ctx, storedb.CreateChatScheduleParams{
		ID: uuid.New(), SessionID: sessionID, DisplayName: "schedule fixture", Prompt: "run fixture",
		CronExpression: expression, Timezone: "UTC", Status: string(status), CreatedAt: now, UpdatedAt: now,
	})
	Expect(err).NotTo(HaveOccurred())
	return row
}

func seedDueChatSchedule(
	ctx context.Context,
	queries *storedb.Queries,
	scheduleStore cron.IStore,
	sessionID uuid.UUID,
	prompt string,
) string {
	GinkgoHelper()
	now := time.Now().UTC()
	scheduledAt := now.Truncate(time.Minute).Add(-time.Minute)
	expression := fmt.Sprintf("%d %d * * *", scheduledAt.Minute(), scheduledAt.Hour())
	row, err := queries.CreateChatSchedule(ctx, storedb.CreateChatScheduleParams{
		ID: uuid.New(), SessionID: sessionID, DisplayName: "due schedule", Prompt: prompt,
		CronExpression: expression, Timezone: "UTC", Status: string(api.ChatScheduleStatusActive),
		CreatedAt: now, UpdatedAt: now,
	})
	Expect(err).NotTo(HaveOccurred())
	schedule := cron.Spec(cron.Raw(expression)).In(time.UTC)
	definition, err := schedule.Definition()
	Expect(err).NotTo(HaveOccurred())
	jobName := "chat/" + row.ID.String()
	_, err = scheduleStore.Reconcile(ctx, []cron.JobDefinition{{
		Name: jobName, Schedule: definition, CatchUp: cron.CatchUpLatest, Overlap: cron.OverlapSkip,
	}}, scheduledAt.Add(-time.Nanosecond))
	Expect(err).NotTo(HaveOccurred())
	return cron.OccurrenceID(jobName, scheduledAt)
}

func awaitScheduleOccurrenceStatus(
	store cron.IStore,
	occurrenceID string,
	status cron.OccurrenceStatus,
) cron.OccurrenceRecord {
	GinkgoHelper()
	return patience.Await(GinkgoT(), "schedule occurrence reaches "+string(status), lifecycleBudget,
		func() cron.OccurrenceRecord {
			snapshot, err := store.Snapshot(context.Background())
			if err != nil {
				return cron.OccurrenceRecord{}
			}
			for _, occurrence := range snapshot.Occurrences {
				if occurrence.ID == occurrenceID {
					return occurrence
				}
			}
			return cron.OccurrenceRecord{}
		},
		func(occurrence cron.OccurrenceRecord) bool { return occurrence.Status == status })
}

func awaitScheduleRuntimeJob(
	store cron.IStore,
	scheduleID uuid.UUID,
) cron.JobState {
	GinkgoHelper()
	expectedName := "chat/" + scheduleID.String()
	return patience.Await(GinkgoT(), "schedule runtime contains the durable job", lifecycleBudget,
		func() cron.JobState {
			snapshot, err := store.Snapshot(context.Background())
			if err != nil {
				return cron.JobState{}
			}
			for _, job := range snapshot.Jobs {
				if job.Definition.Name == expectedName {
					return job
				}
			}
			return cron.JobState{}
		},
		func(job cron.JobState) bool { return job.Definition.Name == expectedName })
}

func awaitEmptyScheduleRuntime(store cron.IStore) {
	GinkgoHelper()
	patience.Await(GinkgoT(), "schedule runtime removes the durable job", lifecycleBudget,
		func() int {
			snapshot, err := store.Snapshot(context.Background())
			if err != nil {
				return -1
			}
			return len(snapshot.Jobs)
		},
		func(count int) bool { return count == 0 })
}

var _ = Describe("the projection of one session's lifecycle events", func() {
	var (
		ctx                      context.Context
		queries                  *storedb.Queries
		client                   *api.ClientWithResponses
		events                   chan copilotadapter.BridgeEvent
		scheduleStore            *observedScheduleStore
		closeSession             func() error
		closeSessionWithContext  func(ctx context.Context) error
		acknowledgeDelivery      func(turnID uuid.UUID)
		activeTurn               func() (uuid.UUID, bool)
		abortTurn                func(ctx context.Context, expectedTurnID uuid.UUID) (uuid.UUID, error)
		abandonResolution        func(requestID uuid.UUID)
		resolveRequest           func(ctx context.Context, resolution copilotadapter.BridgeResolution) error
		prepareWorktree          func(ctx context.Context, request copilotadapter.WorktreeRequest) (copilotadapter.PreparedWorktree, error)
		reuseWorktree            func(ctx context.Context, repositoryID string, path string) (copilotadapter.PreparedWorktree, error)
		inspectWorktree          func(ctx context.Context, path string) (copilotadapter.WorktreeSnapshot, error)
		readWorktreeChanges      func(ctx context.Context, path string) (copilotadapter.WorktreeChanges, error)
		createTerminal           func(ctx context.Context, spec copilotadapter.TerminalSpec) (copilotadapter.TerminalSnapshot, error)
		sendPrompt               func(ctx context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error)
		setModel                 func(ctx context.Context, model string) error
		createBridgeSession      func(spec copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error)
		resumeBridgeSession      func(spec copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error)
		createSessionCalls       atomic.Int32
		resumeSessionCalls       atomic.Int32
		sessionID                uuid.UUID
		service                  *copilotadapter.CopilotAdapter
		storeWithAmbiguousCommit *ambiguousCommitStore
		storeWithEventFailure    *sessionEventFailureStore
		storeWithSessionViewFail *sessionViewFailureStore
		storeWithLockFailure     *failBeforeLockStore
		storeWithAckFailure      *deliveryAcknowledgementFailureStore
		storeWithScheduleRace    *scheduleCreationRaceStore
		worktrees                *MockIWorktreeManager
	)

	BeforeEach(func() {
		ctx = context.Background()
		database := pgmem.MustNew()
		DeferCleanup(database.Close)
		db := database.Open()
		DeferCleanup(db.Close)
		Expect(store.ApplyMigrations(ctx, db)).To(Succeed())
		queries = storedb.New(db)

		events = make(chan copilotadapter.BridgeEvent, 16)
		closeSession = func() error { return nil }
		closeSessionWithContext = func(_ context.Context) error { return closeSession() }
		acknowledgeDelivery = func(_ uuid.UUID) {}
		activeTurn = func() (uuid.UUID, bool) { return uuid.Nil, false }
		abortTurn = func(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
			return uuid.Nil, copilotadapter.ErrNoActiveTurn
		}
		abandonResolution = func(_ uuid.UUID) {}
		resolveRequest = func(_ context.Context, _ copilotadapter.BridgeResolution) error { return nil }
		prepareWorktree = func(_ context.Context, _ copilotadapter.WorktreeRequest) (copilotadapter.PreparedWorktree, error) {
			return copilotadapter.PreparedWorktree{
				Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD"},
				Path:       "/tmp/work", BaseRef: "HEAD",
			}, nil
		}
		reuseWorktree = func(_ context.Context, _ string, path string) (copilotadapter.PreparedWorktree, error) {
			return copilotadapter.PreparedWorktree{
				Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD"},
				Path:       path, BaseRef: "HEAD", Managed: path != "/tmp/work",
			}, nil
		}
		inspectWorktree = func(_ context.Context, _ string) (copilotadapter.WorktreeSnapshot, error) {
			return copilotadapter.WorktreeSnapshot{Branch: "main", HeadSHA: "abc", Clean: true, State: "active"}, nil
		}
		readWorktreeChanges = func(_ context.Context, _ string) (copilotadapter.WorktreeChanges, error) {
			return copilotadapter.WorktreeChanges{HeadSHA: "abc", Clean: true, Captured: time.Now().UTC()}, nil
		}
		createTerminal = func(_ context.Context, spec copilotadapter.TerminalSpec) (copilotadapter.TerminalSnapshot, error) {
			return copilotadapter.TerminalSnapshot{
				ID: uuid.New(), WorktreeID: spec.WorktreeID, Rows: spec.Rows, Columns: spec.Columns,
				Shell: "/bin/bash", Status: string(api.TerminalStatusRunning), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			}, nil
		}
		sendPrompt = func(_ context.Context, _ copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
			return copilotadapter.BridgePromptDeliveryAccepted, nil
		}
		setModel = func(_ context.Context, _ string) error { return nil }
		sessionHandle := func() copilotadapter.BridgeSession {
			return copilotadapter.BridgeSession{
				Events: events,
				AcknowledgeDelivery: func(turnID uuid.UUID) {
					acknowledgeDelivery(turnID)
				},
				Send: func(ctx context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
					return sendPrompt(ctx, prompt)
				},
				ActiveTurn: func() (uuid.UUID, bool) { return activeTurn() },
				Abort: func(ctx context.Context, expectedTurnID uuid.UUID) (uuid.UUID, error) {
					return abortTurn(ctx, expectedTurnID)
				},
				AbandonResolution: func(requestID uuid.UUID) { abandonResolution(requestID) },
				Resolve: func(ctx context.Context, resolution copilotadapter.BridgeResolution) error {
					return resolveRequest(ctx, resolution)
				},
				SetModel: func(ctx context.Context, model string) error { return setModel(ctx, model) },
				Close:    func(ctx context.Context) error { return closeSessionWithContext(ctx) },
			}
		}
		createBridgeSession = func(_ copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
			return sessionHandle(), nil
		}
		resumeBridgeSession = func(_ copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
			return sessionHandle(), nil
		}
		controller := gomock.NewController(GinkgoT())
		bridge := NewMockICopilotBridge(controller)
		worktrees = NewMockIWorktreeManager(controller)
		worktrees.EXPECT().Prepare(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, request copilotadapter.WorktreeRequest) (copilotadapter.PreparedWorktree, error) {
				return prepareWorktree(ctx, request)
			}).AnyTimes()
		worktrees.EXPECT().Reuse(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, repositoryID string, path string) (copilotadapter.PreparedWorktree, error) {
				return reuseWorktree(ctx, repositoryID, path)
			}).AnyTimes()
		worktrees.EXPECT().Inspect(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, path string) (copilotadapter.WorktreeSnapshot, error) {
				return inspectWorktree(ctx, path)
			}).AnyTimes()
		worktrees.EXPECT().Changes(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, path string) (copilotadapter.WorktreeChanges, error) {
				return readWorktreeChanges(ctx, path)
			}).AnyTimes()
		terminals := NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil).AnyTimes()
		terminals.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, spec copilotadapter.TerminalSpec) (copilotadapter.TerminalSnapshot, error) {
				return createTerminal(ctx, spec)
			}).AnyTimes()
		bridge.EXPECT().CreateSession(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, spec copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
				createSessionCalls.Add(1)
				return createBridgeSession(spec)
			}).AnyTimes()
		bridge.EXPECT().ResumeSession(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, spec copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
				resumeSessionCalls.Add(1)
				return resumeBridgeSession(spec)
			}).AnyTimes()

		postgresStore, err := store.NewPostgresStore(db)
		Expect(err).NotTo(HaveOccurred())
		storeWithEventFailure = &sessionEventFailureStore{IStore: postgresStore}
		storeWithAmbiguousCommit = &ambiguousCommitStore{IStore: storeWithEventFailure}
		storeWithLockFailure = &failBeforeLockStore{IStore: storeWithAmbiguousCommit}
		storeWithSessionViewFail = &sessionViewFailureStore{IStore: storeWithLockFailure}
		storeWithAckFailure = &deliveryAcknowledgementFailureStore{IStore: storeWithSessionViewFail}
		storeWithScheduleRace = &scheduleCreationRaceStore{IStore: storeWithAckFailure}
		scheduleStore = newObservedScheduleStore()
		adapterConfig := copilotadapter.DefaultAdapterConfig()
		adapterConfig.DurableTransitionTimeoutMillis = 1000
		adapterConfig.AssistantDeltaMaxEvents = 4
		service, err = copilotadapter.NewCopilotAdapter(
			copilotadapter.WithBridge(bridge),
			copilotadapter.WithStore(storeWithScheduleRace),
			copilotadapter.WithWorktreeManager(worktrees),
			copilotadapter.WithTerminalManager(terminals),
			copilotadapter.WithScheduleStore(scheduleStore),
			copilotadapter.WithConfig(adapterConfig),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(service.Close)
		engine := httpserver.NewEngine("copilot-adapter-lifecycle-test")
		Expect(service.Register(engine)).To(Succeed())
		server := httptest.NewServer(engine)
		DeferCleanup(server.Close)
		client, err = api.NewClientWithResponses(server.URL)
		Expect(err).NotTo(HaveOccurred())

		created, err := client.CreateSessionWithResponse(ctx, newWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		sessionID = *created.JSON201.Id
	})

	submit := func(text string) uuid.UUID {
		prompted, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: text, Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(prompted.StatusCode()).To(Equal(http.StatusAccepted), string(prompted.Body))
		return *prompted.JSON202.Id
	}

	awaitTurnStatus := func(identifier uuid.UUID, status string) {
		patience.Await(GinkgoT(), "turn "+identifier.String()+" reaches "+status, lifecycleBudget,
			func() string {
				turn, err := queries.GetTurn(ctx, identifier)
				if err != nil {
					return ""
				}
				return turn.Status
			},
			func(observed string) bool { return observed == status })
	}

	latestSessionVersion := func() (storedb.SessionEventVersion, bool) {
		events, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
			SessionID: sessionID, RowLimit: 100,
		})
		if err != nil {
			return storedb.SessionEventVersion{}, false
		}
		for index := len(events) - 1; index >= 0; index-- {
			if events[index].Kind != string(api.SessionEventKindSessionUpdated) {
				continue
			}
			version, versionErr := queries.GetSessionEventVersion(ctx, storedb.GetSessionEventVersionParams{
				SessionID: sessionID, EventSeq: events[index].Seq,
			})
			if versionErr == nil {
				return version, true
			}
		}
		return storedb.SessionEventVersion{}, false
	}

	sessionUpdatedCount := func() int {
		rows, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
			SessionID: sessionID, RowLimit: 100,
		})
		Expect(err).NotTo(HaveOccurred())
		count := 0
		for _, row := range rows {
			if row.Kind == string(api.SessionEventKindSessionUpdated) {
				count++
			}
		}
		return count
	}

	expectModelTransactionRollback := func(armFailure func()) {
		GinkgoHelper()
		nextModel := "gpt-5.1"
		modelChanges := make(chan modelChangeCall, 2)
		setModel = func(callContext context.Context, model string) error {
			modelChanges <- modelChangeCall{model: model, contextErr: callContext.Err()}
			return nil
		}
		before, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		armFailure()

		response, err := client.UpdateSessionWithResponse(ctx, sessionID, api.UpdateSessionJSONRequestBody{Model: &nextModel})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusInternalServerError), string(response.Body))
		Expect(<-modelChanges).To(Equal(modelChangeCall{model: nextModel}))
		Expect(<-modelChanges).To(Equal(modelChangeCall{model: before.Model}))
		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Model).To(Equal(before.Model))
		Expect(persisted.UpdatedAt).To(Equal(before.UpdatedAt))
		_, found := latestSessionVersion()
		Expect(found).To(BeFalse())
	}

	It("reconciles an ambiguously committed session before attaching its bridge", func() {
		prepared := copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{
				ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD",
			},
			Path: "/tmp/work/managed-ambiguous", BaseRef: "HEAD", Managed: true,
		}
		prepareWorktree = func(_ context.Context, _ copilotadapter.WorktreeRequest) (copilotadapter.PreparedWorktree, error) {
			return prepared, nil
		}
		var closeAttempts atomic.Int32
		closeSession = func() error {
			closeAttempts.Add(1)
			return nil
		}
		sent := make(chan copilotadapter.BridgePrompt, 1)
		sendPrompt = func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
			sent <- prompt
			return copilotadapter.BridgePromptDeliveryAccepted, nil
		}
		storeWithAmbiguousCommit.arm()

		created, err := client.CreateSessionWithResponse(ctx, newWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		createdID := *created.JSON201.Id
		persisted, err := queries.GetSession(ctx, createdID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.WorkingDirectory).To(Equal(prepared.Path))
		prompted, err := client.SubmitPromptWithResponse(ctx, createdID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: "attached after reconciliation", Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(prompted.StatusCode()).To(Equal(http.StatusAccepted), string(prompted.Body))
		Eventually(sent).WithTimeout(lifecycleBudget.Within).Should(Receive(HaveField("Text", Equal("attached after reconciliation"))))
		Expect(closeAttempts.Load()).To(BeZero())
	})

	It("recovers receipt and managed-worktree residue after the starting transaction rolls back", func() {
		key := uuid.New()
		body := newWorktreeSessionBodyWithKey("gpt-5", "repo", key)
		var preparedIDs []uuid.UUID
		prepareWorktree = func(_ context.Context, request copilotadapter.WorktreeRequest) (copilotadapter.PreparedWorktree, error) {
			preparedIDs = append(preparedIDs, request.SessionID)
			return copilotadapter.PreparedWorktree{
				Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD"},
				Path:       "/tmp/work/chat-" + request.SessionID.String(), BaseRef: "HEAD", Managed: true,
			}, nil
		}
		baselineCreates := createSessionCalls.Load()
		storeWithAmbiguousCommit.armRollback()

		first, err := client.CreateSessionWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.StatusCode()).To(Equal(http.StatusInternalServerError), string(first.Body))
		receipt, err := queries.GetSessionCreation(ctx, key)
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.GetSession(ctx, receipt.SessionID)
		Expect(errors.Is(err, sql.ErrNoRows)).To(BeTrue())

		retried, err := client.CreateSessionWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(retried.StatusCode()).To(Equal(http.StatusCreated), string(retried.Body))
		Expect(retried.JSON201.Id).To(Equal(&receipt.SessionID))
		Expect(preparedIDs).To(Equal([]uuid.UUID{receipt.SessionID, receipt.SessionID}))
		Expect(createSessionCalls.Load() - baselineCreates).To(Equal(int32(1)))
	})

	It("reuses the exact live SDK handle after completion rolls back", func() {
		key := uuid.New()
		body := newWorktreeSessionBodyWithKey("gpt-5", "repo", key)
		baselineCreates := createSessionCalls.Load()
		baselineResumes := resumeSessionCalls.Load()
		var closeAttempts atomic.Int32
		closeSession = func() error { closeAttempts.Add(1); return nil }
		storeWithAmbiguousCommit.armRollbackAfter(2)

		first, err := client.CreateSessionWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.StatusCode()).To(Equal(http.StatusBadGateway), string(first.Body))
		receipt, err := queries.GetSessionCreation(ctx, key)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.SdkCreateAttemptID).NotTo(BeNil())
		persisted, err := queries.GetSession(ctx, receipt.SessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Status).To(Equal(string(api.SessionStatusStarting)))

		retried, err := client.CreateSessionWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(retried.StatusCode()).To(Equal(http.StatusCreated), string(retried.Body))
		Expect(retried.JSON201.Id).To(Equal(&receipt.SessionID))
		Expect(createSessionCalls.Load() - baselineCreates).To(Equal(int32(1)))
		Expect(resumeSessionCalls.Load() - baselineResumes).To(BeZero())
		Expect(closeAttempts.Load()).To(BeZero())
	})

	It("reconciles a lost completion commit acknowledgement without closing the SDK session", func() {
		key := uuid.New()
		body := newWorktreeSessionBodyWithKey("gpt-5", "repo", key)
		baselineCreates := createSessionCalls.Load()
		var closeAttempts atomic.Int32
		closeSession = func() error { closeAttempts.Add(1); return nil }
		storeWithAmbiguousCommit.armAfter(2)

		created, err := client.CreateSessionWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		Expect(created.JSON201.Status).To(Equal(api.SessionStatusIdle))
		replayed, err := client.CreateSessionWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.StatusCode()).To(Equal(http.StatusCreated), string(replayed.Body))
		Expect(replayed.JSON201.Id).To(Equal(created.JSON201.Id))
		Expect(createSessionCalls.Load() - baselineCreates).To(Equal(int32(1)))
		Expect(closeAttempts.Load()).To(BeZero())
	})

	It("owns Create after a lost SDK-attempt commit acknowledgement", func() {
		baselineCreates := createSessionCalls.Load()
		baselineResumes := resumeSessionCalls.Load()
		storeWithAmbiguousCommit.armSDKAttemptAcknowledgementFailure()

		created, err := client.CreateSessionWithResponse(ctx, newWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		Expect(createSessionCalls.Load() - baselineCreates).To(Equal(int32(1)))
		Expect(resumeSessionCalls.Load() - baselineResumes).To(BeZero())
	})

	It("reconciles an ambiguous SDK create through Resume without creating twice", func() {
		baselineCreates := createSessionCalls.Load()
		baselineResumes := resumeSessionCalls.Load()
		createBridgeSession = func(_ copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
			return copilotadapter.BridgeSession{}, errAmbiguousCommit
		}

		created, err := client.CreateSessionWithResponse(ctx, newWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		Expect(createSessionCalls.Load() - baselineCreates).To(Equal(int32(1)))
		Expect(resumeSessionCalls.Load() - baselineResumes).To(Equal(int32(1)))
	})

	It("returns the same failed session when ambiguous Create has no remote identity", func() {
		key := uuid.New()
		body := newWorktreeSessionBodyWithKey("gpt-5", "repo", key)
		baselineCreates := createSessionCalls.Load()
		baselineResumes := resumeSessionCalls.Load()
		createBridgeSession = func(_ copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
			return copilotadapter.BridgeSession{}, errAmbiguousCommit
		}
		resumeBridgeSession = func(_ copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
			return copilotadapter.BridgeSession{}, copilotadapter.ErrBridgeSessionMissing
		}

		created, err := client.CreateSessionWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		Expect(created.JSON201.Status).To(Equal(api.SessionStatusFailed))
		replayed, err := client.CreateSessionWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.StatusCode()).To(Equal(http.StatusCreated), string(replayed.Body))
		Expect(replayed.JSON201).To(Equal(created.JSON201))
		Expect(createSessionCalls.Load() - baselineCreates).To(Equal(int32(1)))
		Expect(resumeSessionCalls.Load() - baselineResumes).To(Equal(int32(1)))
	})

	It("creates one usable session without fallible post-commit turn-count hydration", func() {
		var closeAttempts atomic.Int32
		closeSession = func() error {
			closeAttempts.Add(1)
			return nil
		}
		sent := make(chan copilotadapter.BridgePrompt, 1)
		sendPrompt = func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
			sent <- prompt
			return copilotadapter.BridgePromptDeliveryAccepted, nil
		}
		storeWithSessionViewFail.arm()

		created, err := client.CreateSessionWithResponse(ctx, newWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		Expect(created.JSON201.TurnCount).NotTo(BeNil())
		Expect(*created.JSON201.TurnCount).To(BeEquivalentTo(0))
		Expect(storeWithSessionViewFail.attempts.Load()).To(BeZero())
		createdID := *created.JSON201.Id
		sessions, err := queries.ListResumableSessions(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(sessions).To(HaveLen(2), "the setup session plus exactly one new session must exist")

		prompted, err := client.SubmitPromptWithResponse(ctx, createdID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: "still attached after create", Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(prompted.StatusCode()).To(Equal(http.StatusAccepted), string(prompted.Body))
		Eventually(sent).WithTimeout(lifecycleBudget.Within).Should(Receive(HaveField("Text", Equal("still attached after create"))))
		Expect(closeAttempts.Load()).To(BeZero())
	})

	It("rejects every session mutation while creation is still starting", func() {
		now := time.Now().UTC()
		turnID, requestID, scheduleID := uuid.New(), uuid.New(), uuid.New()
		_, err := queries.CreateTurn(ctx, storedb.CreateTurnParams{
			ID: turnID, SessionID: sessionID, Status: string(api.TurnStatusRunning), PromptText: "not ready",
			PromptMode: string(api.Queue), CreatedAt: now, DeliveryStatus: "accepted",
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.InsertSessionRequest(ctx, storedb.InsertSessionRequestParams{
			ID: requestID, SessionID: sessionID, TurnID: &turnID, Kind: string(api.Permission),
			Status: string(api.Pending), Prompt: "wait for startup", CreatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.CreateChatSchedule(ctx, storedb.CreateChatScheduleParams{
			ID: scheduleID, SessionID: sessionID, DisplayName: "unchanged", Prompt: "not yet",
			CronExpression: "0 9 * * *", Timezone: "UTC", Status: string(api.ChatScheduleStatusActive),
			CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.UpdateSessionStatus(ctx, storedb.UpdateSessionStatusParams{
			ID: sessionID, Status: string(api.SessionStatusStarting), UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())

		var sends, modelChanges, activeChecks, aborts, resolutions, closes atomic.Int32
		sendPrompt = func(_ context.Context, _ copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
			sends.Add(1)
			return copilotadapter.BridgePromptDeliveryAccepted, nil
		}
		setModel = func(_ context.Context, _ string) error { modelChanges.Add(1); return nil }
		activeTurn = func() (uuid.UUID, bool) { activeChecks.Add(1); return turnID, true }
		abortTurn = func(_ context.Context, _ uuid.UUID) (uuid.UUID, error) { aborts.Add(1); return turnID, nil }
		resolveRequest = func(_ context.Context, _ copilotadapter.BridgeResolution) error { resolutions.Add(1); return nil }
		closeSession = func() error { closes.Add(1); return nil }
		assertNotReady := func(status int, code string, body []byte) {
			ExpectWithOffset(1, status).To(Equal(http.StatusConflict), string(body))
			ExpectWithOffset(1, code).To(Equal("session_not_ready"))
		}

		nextModel := "gpt-5.1"
		updated, err := client.UpdateSessionWithResponse(ctx, sessionID, api.UpdateSessionJSONRequestBody{Model: &nextModel})
		Expect(err).NotTo(HaveOccurred())
		assertNotReady(updated.StatusCode(), updated.JSON409.Code, updated.Body)
		ended, err := client.EndSessionWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		assertNotReady(ended.StatusCode(), ended.JSON409.Code, ended.Body)
		prompted, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: "too early", Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		assertNotReady(prompted.StatusCode(), prompted.JSON409.Code, prompted.Body)
		aborted, err := client.AbortTurnWithResponse(ctx, sessionID, api.AbortTurnJSONRequestBody{
			IdempotencyKey: uuid.New(), TurnId: turnID,
		})
		Expect(err).NotTo(HaveOccurred())
		assertNotReady(aborted.StatusCode(), aborted.JSON409.Code, aborted.Body)
		resolved, err := client.ResolveSessionRequestWithResponse(ctx, sessionID, requestID, resolutionBody(api.Deny))
		Expect(err).NotTo(HaveOccurred())
		assertNotReady(resolved.StatusCode(), resolved.JSON409.Code, resolved.Body)
		createdSchedule, err := client.CreateChatScheduleWithResponse(ctx, api.CreateChatScheduleJSONRequestBody{
			IdempotencyKey: uuid.New(), SessionId: sessionID, DisplayName: "too early", Prompt: "no", CronExpression: "0 10 * * *", Timezone: "UTC",
		})
		Expect(err).NotTo(HaveOccurred())
		assertNotReady(createdSchedule.StatusCode(), createdSchedule.JSON409.Code, createdSchedule.Body)
		renamed := "must stay unchanged"
		updatedSchedule, err := client.UpdateChatScheduleWithResponse(ctx, scheduleID, api.UpdateChatScheduleJSONRequestBody{
			DisplayName: &renamed,
		})
		Expect(err).NotTo(HaveOccurred())
		assertNotReady(updatedSchedule.StatusCode(), updatedSchedule.JSON409.Code, updatedSchedule.Body)
		deletedSchedule, err := client.DeleteChatScheduleWithResponse(ctx, scheduleID)
		Expect(err).NotTo(HaveOccurred())
		assertNotReady(deletedSchedule.StatusCode(), deletedSchedule.JSON409.Code, deletedSchedule.Body)

		Expect([]int32{sends.Load(), modelChanges.Load(), activeChecks.Load(), aborts.Load(), resolutions.Load(), closes.Load()}).To(Equal(
			[]int32{0, 0, 0, 0, 0, 0},
		))
		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Status).To(Equal(string(api.SessionStatusStarting)))
		Expect(persisted.Model).To(Equal("gpt-5"))
		turn, err := queries.GetTurn(ctx, turnID)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn.Status).To(Equal(string(api.TurnStatusRunning)))
		request, err := queries.GetSessionRequest(ctx, requestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(request.Status).To(Equal(string(api.Pending)))
		schedule, err := queries.GetChatSchedule(ctx, scheduleID)
		Expect(err).NotTo(HaveOccurred())
		Expect(schedule.DisplayName).To(Equal("unchanged"))
		Expect(schedule.DeletedAt.Valid).To(BeFalse())
		schedules, err := queries.ListChatSchedules(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(schedules).To(HaveLen(1))
	})

	It("replays a schedule persisted before the runtime starts with an authoritative snapshot", func() {
		body := api.CreateChatScheduleJSONRequestBody{
			IdempotencyKey: uuid.New(), SessionId: sessionID, DisplayName: "startup retry",
			Prompt: "retry after startup", CronExpression: "0 9 * * *", Timezone: "UTC",
		}
		beforeStartup, err := client.CreateChatScheduleWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(beforeStartup.StatusCode()).To(Equal(http.StatusInternalServerError), string(beforeStartup.Body))

		rows, err := queries.ListChatSchedules(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1), "the transient runtime failure must not discard the durable receipt")
		scheduleID := rows[0].ID

		startScheduleRuntime(ctx, service, scheduleStore)
		replayed, err := client.CreateChatScheduleWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.StatusCode()).To(Equal(http.StatusCreated), string(replayed.Body))
		Expect(replayed.JSON201.Id).NotTo(BeNil())
		Expect(*replayed.JSON201.Id).To(Equal(scheduleID))
		Expect(replayed.JSON201.NextRunAt).NotTo(BeNil())
		rows, err = queries.ListChatSchedules(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
	})

	It("returns authoritative create update pause and resume scheduler snapshots", func() {
		startScheduleRuntime(ctx, service, scheduleStore)
		body := api.CreateChatScheduleJSONRequestBody{
			IdempotencyKey: uuid.New(), SessionId: sessionID, DisplayName: "snapshot schedule",
			Prompt: "verify scheduler state", CronExpression: "0 9 * * *", Timezone: "UTC",
		}
		created, err := client.CreateChatScheduleWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		Expect(created.JSON201.NextRunAt).NotTo(BeNil())
		Expect(created.JSON201.LastRunAt).To(BeNil())
		scheduleID := *created.JSON201.Id

		snapshot, err := scheduleStore.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Jobs).To(HaveLen(1))
		job := snapshot.Jobs[0]
		scheduledAt := job.NextRunAt
		schedule := cron.Spec(cron.Raw(body.CronExpression)).In(time.UTC)
		definition, err := schedule.Definition()
		Expect(err).NotTo(HaveOccurred())
		Expect(definition).To(Equal(job.Definition.Schedule))
		nextRunAt, err := schedule.Next(scheduledAt)
		Expect(err).NotTo(HaveOccurred())
		occurrenceID := cron.OccurrenceID(job.Definition.Name, scheduledAt)
		claim, err := scheduleStore.Claim(ctx, cron.ClaimRequest{
			OccurrenceID: occurrenceID, JobName: job.Definition.Name, ScheduledAt: scheduledAt, NextRunAt: nextRunAt,
			LeaseOwner: "snapshot-test", LeaseToken: "snapshot-token", ClaimedAt: time.Now().UTC(),
			LeaseUntil: time.Now().UTC().Add(time.Minute),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(claim.Disposition).To(Equal(cron.ClaimAcquired))
		Expect(scheduleStore.Complete(ctx, cron.Completion{
			OccurrenceID: occurrenceID, LeaseToken: "snapshot-token", Status: cron.OccurrenceFailed,
			FinishedAt: time.Now().UTC(), Error: "simulated schedule failure",
		})).To(Succeed())

		updatedName := "updated snapshot schedule"
		updated, err := client.UpdateChatScheduleWithResponse(ctx, scheduleID, api.UpdateChatScheduleJSONRequestBody{
			DisplayName: &updatedName,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.StatusCode()).To(Equal(http.StatusOK), string(updated.Body))
		Expect(updated.JSON200.DisplayName).To(Equal(updatedName))
		Expect(updated.JSON200.NextRunAt).NotTo(BeNil())
		Expect(updated.JSON200.LastRunAt).To(PointTo(BeTemporally("==", scheduledAt)))
		Expect(updated.JSON200.LastRunStatus).To(PointTo(Equal(api.ChatScheduleRunStatusFailed)))
		Expect(updated.JSON200.LastError).To(PointTo(Equal("simulated schedule failure")))

		pausedStatus := api.ChatScheduleStatusPaused
		paused, err := client.UpdateChatScheduleWithResponse(ctx, scheduleID, api.UpdateChatScheduleJSONRequestBody{
			Status: &pausedStatus,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(paused.StatusCode()).To(Equal(http.StatusOK), string(paused.Body))
		Expect(paused.JSON200.NextRunAt).To(BeNil())
		Expect(paused.JSON200.LastRunAt).To(PointTo(BeTemporally("==", scheduledAt)))
		Expect(paused.JSON200.LastRunStatus).To(PointTo(Equal(api.ChatScheduleRunStatusFailed)))

		activeStatus := api.ChatScheduleStatusActive
		resumed, err := client.UpdateChatScheduleWithResponse(ctx, scheduleID, api.UpdateChatScheduleJSONRequestBody{
			Status: &activeStatus,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resumed.StatusCode()).To(Equal(http.StatusOK), string(resumed.Body))
		Expect(resumed.JSON200.NextRunAt).NotTo(BeNil())
		Expect(resumed.JSON200.LastRunAt).To(PointTo(BeTemporally("==", scheduledAt)))
		Expect(resumed.JSON200.LastRunStatus).To(PointTo(Equal(api.ChatScheduleRunStatusFailed)))
		listed, err := client.ListChatSchedulesWithResponse(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(listed.StatusCode()).To(Equal(http.StatusOK), string(listed.Body))
		Expect(listed.JSON200.Data).To(HaveLen(1))
		Expect(listed.JSON200.Data[0]).To(Equal(*resumed.JSON200))
	})

	It("reconciles a committed schedule create after its request is canceled", func() {
		startScheduleRuntime(ctx, service, scheduleStore)
		requestCtx, cancelRequest := context.WithCancel(ctx)
		DeferCleanup(cancelRequest)
		storeWithAmbiguousCommit.armCancelAfterScheduleCommit(cancelRequest, false)
		response, err := service.CreateChatSchedule(requestCtx, api.CreateChatScheduleRequestObject{
			Body: &api.CreateChatScheduleJSONRequestBody{
				IdempotencyKey: uuid.New(), SessionId: sessionID, DisplayName: "canceled create",
				Prompt: "must still run", CronExpression: "0 9 * * *", Timezone: "UTC",
			},
		})
		Expect(response).To(BeNil())
		Expect(err).To(HaveOccurred())
		Expect(requestCtx.Err()).To(MatchError(context.Canceled))
		rows, err := queries.ListChatSchedules(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		awaitScheduleRuntimeJob(scheduleStore, rows[0].ID)
	})

	It("reconciles a committed schedule resume after its request is canceled", func() {
		row := seedChatSchedule(ctx, queries, sessionID, api.ChatScheduleStatusPaused, "0 9 * * *")
		startScheduleRuntime(ctx, service, scheduleStore)
		requestCtx, cancelRequest := context.WithCancel(ctx)
		DeferCleanup(cancelRequest)
		storeWithAmbiguousCommit.armCancelAfterScheduleCommit(cancelRequest, true)
		active := api.ChatScheduleStatusActive
		response, err := service.UpdateChatSchedule(requestCtx, api.UpdateChatScheduleRequestObject{
			ScheduleId: row.ID, Body: &api.UpdateChatScheduleJSONRequestBody{Status: &active},
		})
		Expect(response).To(BeNil())
		Expect(err).To(HaveOccurred())
		Expect(requestCtx.Err()).To(MatchError(context.Canceled))
		persisted, err := queries.GetChatSchedule(ctx, row.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Status).To(Equal(string(api.ChatScheduleStatusActive)))
		awaitScheduleRuntimeJob(scheduleStore, row.ID)
	})

	It("reconciles an ambiguous schedule create even when receipt recovery fails", func() {
		startScheduleRuntime(ctx, service, scheduleStore)
		storeWithAmbiguousCommit.armScheduleCreationReconciliationFailure()
		response, err := client.CreateChatScheduleWithResponse(ctx, api.CreateChatScheduleJSONRequestBody{
			IdempotencyKey: uuid.New(), SessionId: sessionID, DisplayName: "ambiguous create",
			Prompt: "must converge", CronExpression: "0 9 * * *", Timezone: "UTC",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusInternalServerError), string(response.Body))
		rows, err := queries.ListChatSchedules(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		awaitScheduleRuntimeJob(scheduleStore, rows[0].ID)
	})

	It("reconciles an ambiguous paused-to-active schedule update", func() {
		row := seedChatSchedule(ctx, queries, sessionID, api.ChatScheduleStatusPaused, "0 9 * * *")
		startScheduleRuntime(ctx, service, scheduleStore)
		storeWithAmbiguousCommit.armScheduleUpdateAcknowledgementFailure()
		active := api.ChatScheduleStatusActive
		response, err := client.UpdateChatScheduleWithResponse(ctx, row.ID, api.UpdateChatScheduleJSONRequestBody{
			Status: &active,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusInternalServerError), string(response.Body))
		persisted, err := queries.GetChatSchedule(ctx, row.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Status).To(Equal(string(api.ChatScheduleStatusActive)))
		awaitScheduleRuntimeJob(scheduleStore, row.ID)
	})

	It("replaces an active runtime definition after an ambiguous schedule update", func() {
		row := seedChatSchedule(ctx, queries, sessionID, api.ChatScheduleStatusActive, "0 9 * * *")
		startScheduleRuntime(ctx, service, scheduleStore)
		awaitScheduleRuntimeJob(scheduleStore, row.ID)
		storeWithAmbiguousCommit.armScheduleUpdateAcknowledgementFailure()
		expression := "5 10 * * *"
		response, err := client.UpdateChatScheduleWithResponse(ctx, row.ID, api.UpdateChatScheduleJSONRequestBody{
			CronExpression: &expression,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusInternalServerError), string(response.Body))
		job := awaitScheduleRuntimeJob(scheduleStore, row.ID)
		expected, err := cron.Spec(cron.Raw(expression)).In(time.UTC).Definition()
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Definition.Schedule).To(Equal(expected))
	})

	It("removes an active runtime definition after an ambiguous schedule delete", func() {
		row := seedChatSchedule(ctx, queries, sessionID, api.ChatScheduleStatusActive, "0 9 * * *")
		startScheduleRuntime(ctx, service, scheduleStore)
		awaitScheduleRuntimeJob(scheduleStore, row.ID)
		storeWithAmbiguousCommit.armScheduleDeleteAcknowledgementFailure()
		response, err := client.DeleteChatScheduleWithResponse(ctx, row.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusInternalServerError), string(response.Body))
		awaitEmptyScheduleRuntime(scheduleStore)
		_, err = queries.GetChatSchedule(ctx, row.ID)
		Expect(errors.Is(err, sql.ErrNoRows)).To(BeTrue())
	})

	It("reconciles a lost schedule-create commit and replays its immutable receipt", func() {
		scheduleContext, stopSchedules := context.WithCancel(ctx)
		schedulesFinished := make(chan error, 1)
		go func() { schedulesFinished <- service.RunSchedules(scheduleContext) }()
		DeferCleanup(func() {
			stopSchedules()
			Eventually(schedulesFinished).WithTimeout(lifecycleBudget.Within).Should(Receive(Succeed()))
		})

		idempotencyKey := uuid.New()
		body := api.CreateChatScheduleJSONRequestBody{
			IdempotencyKey: idempotencyKey, SessionId: sessionID, DisplayName: "daily review",
			Prompt: "review the current branch", CronExpression: "0  9  * * *", Timezone: "UTC",
		}
		storeWithAmbiguousCommit.arm()
		created, err := client.CreateChatScheduleWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		Expect(created.JSON201.CronExpression).To(Equal("0 9 * * *"))
		scheduleID := *created.JSON201.Id

		body.CronExpression = " 0 9 * * * "
		replayed, err := client.CreateChatScheduleWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.StatusCode()).To(Equal(http.StatusCreated), string(replayed.Body))
		Expect(replayed.JSON201.Id).NotTo(BeNil())
		Expect(*replayed.JSON201.Id).To(Equal(scheduleID))
		rows, err := queries.ListChatSchedules(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		receipt, err := queries.GetChatScheduleCreation(ctx, idempotencyKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.ScheduleID).To(Equal(scheduleID))
		Expect(receipt.CronExpression).To(Equal("0 9 * * *"))
		patience.Await(GinkgoT(), "schedule retry keeps one runtime registration", lifecycleBudget,
			func() int {
				snapshot, snapshotErr := scheduleStore.Snapshot(ctx)
				if snapshotErr != nil {
					return 0
				}
				return len(snapshot.Jobs)
			},
			func(jobs int) bool { return jobs == 1 })

		updatedName := "updated after create"
		updated, err := client.UpdateChatScheduleWithResponse(ctx, scheduleID, api.UpdateChatScheduleJSONRequestBody{
			DisplayName: &updatedName,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.StatusCode()).To(Equal(http.StatusOK), string(updated.Body))
		replayed, err = client.CreateChatScheduleWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.StatusCode()).To(Equal(http.StatusCreated), string(replayed.Body))
		Expect(replayed.JSON201.Id).NotTo(BeNil())
		Expect(*replayed.JSON201.Id).To(Equal(scheduleID))
		Expect(replayed.JSON201.DisplayName).To(Equal(updatedName))

		deleted, err := client.DeleteChatScheduleWithResponse(ctx, scheduleID)
		Expect(err).NotTo(HaveOccurred())
		Expect(deleted.StatusCode()).To(Equal(http.StatusNoContent), string(deleted.Body))
		replayed, err = client.CreateChatScheduleWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.StatusCode()).To(Equal(http.StatusCreated), string(replayed.Body))
		Expect(replayed.JSON201.Id).NotTo(BeNil())
		Expect(*replayed.JSON201.Id).To(Equal(scheduleID))
		Expect(replayed.JSON201.Status).To(Equal(api.ChatScheduleStatusPaused))
		Expect(replayed.JSON201.NextRunAt).To(BeNil())
		rows, err = queries.ListChatSchedules(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(BeEmpty(), "a deleted receipt replay must not create a replacement")

		body.Prompt = "different request"
		conflicting, err := client.CreateChatScheduleWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(conflicting.StatusCode()).To(Equal(http.StatusConflict), string(conflicting.Body))
		Expect(conflicting.JSON409.Code).To(Equal("idempotency_key_reused"))
	})

	It("serializes concurrent exact schedule-create retries to one receipt and schedule", func() {
		startScheduleRuntime(ctx, service, scheduleStore)
		idempotencyKey := uuid.New()
		body := api.CreateChatScheduleJSONRequestBody{
			IdempotencyKey: idempotencyKey, SessionId: sessionID, DisplayName: "concurrent retry",
			Prompt: "create exactly once", CronExpression: "0 9 * * *", Timezone: "UTC",
		}
		type createResult struct {
			response *api.CreateChatScheduleResponse
			err      error
		}
		start := make(chan struct{})
		results := make(chan createResult, 2)
		for range 2 {
			go func() {
				<-start
				response, err := client.CreateChatScheduleWithResponse(ctx, body)
				results <- createResult{response: response, err: err}
			}()
		}
		close(start)
		identifiers := make([]uuid.UUID, 0, 2)
		for range 2 {
			result := <-results
			Expect(result.err).NotTo(HaveOccurred())
			Expect(result.response.StatusCode()).To(Equal(http.StatusCreated), string(result.response.Body))
			Expect(result.response.JSON201.Id).NotTo(BeNil())
			identifiers = append(identifiers, *result.response.JSON201.Id)
		}
		Expect(identifiers[1]).To(Equal(identifiers[0]))
		rows, err := queries.ListChatSchedules(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		receipt, err := queries.GetChatScheduleCreation(ctx, idempotencyKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.ScheduleID).To(Equal(identifiers[0]))
	})

	It("allows one concurrent schedule body to claim a key and rejects every different request", func() {
		startScheduleRuntime(ctx, service, scheduleStore)
		idempotencyKey := uuid.New()
		storeWithScheduleRace.arm(idempotencyKey)
		first := api.CreateChatScheduleJSONRequestBody{
			IdempotencyKey: idempotencyKey, SessionId: sessionID, DisplayName: "first contender",
			Prompt: "first body", CronExpression: "0 9 * * *", Timezone: "UTC",
		}
		second := first
		second.DisplayName = "second contender"
		second.Prompt = "second body"
		baseline, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		otherSessionID := uuid.New()
		createdAt := time.Now().UTC()
		_, err = queries.CreateSession(ctx, storedb.CreateSessionParams{
			ID: otherSessionID, WorktreeID: baseline.WorktreeID, DisplayName: "other chat", Model: "gpt-5",
			WorkingDirectory: baseline.WorkingDirectory, Status: string(api.SessionStatusIdle),
			CreatedAt: createdAt, UpdatedAt: createdAt,
		})
		Expect(err).NotTo(HaveOccurred())
		second.SessionId = otherSessionID
		type createResult struct {
			response *api.CreateChatScheduleResponse
			err      error
		}
		start := make(chan struct{})
		results := make(chan createResult, 2)
		for _, body := range []api.CreateChatScheduleJSONRequestBody{first, second} {
			body := body
			go func() {
				<-start
				response, err := client.CreateChatScheduleWithResponse(ctx, body)
				results <- createResult{response: response, err: err}
			}()
		}
		close(start)
		statuses := make([]int, 0, 2)
		var winner uuid.UUID
		for range 2 {
			result := <-results
			Expect(result.err).NotTo(HaveOccurred())
			statuses = append(statuses, result.response.StatusCode())
			switch result.response.StatusCode() {
			case http.StatusCreated:
				Expect(result.response.JSON201.Id).NotTo(BeNil())
				winner = *result.response.JSON201.Id
			case http.StatusConflict:
				Expect(result.response.JSON409.Code).To(Equal("idempotency_key_reused"))
			}
		}
		Expect(statuses).To(ConsistOf(http.StatusCreated, http.StatusConflict))
		rows, err := queries.ListChatSchedules(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		receipt, err := queries.GetChatScheduleCreation(ctx, idempotencyKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.ScheduleID).To(Equal(winner))
		Expect(storeWithScheduleRace.claimLosers.Load()).To(Equal(int32(1)), "one SQL claim must lose the atomic key race")
		loser := first
		if receipt.SessionID == first.SessionId {
			loser = second
		}
		conflicting, err := client.CreateChatScheduleWithResponse(ctx, loser)
		Expect(err).NotTo(HaveOccurred())
		Expect(conflicting.StatusCode()).To(Equal(http.StatusConflict), string(conflicting.Body))
		Expect(conflicting.JSON409.Code).To(Equal("idempotency_key_reused"))
		rows, err = queries.ListChatSchedules(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
	})

	It("replays an accepted prompt after the session becomes terminal", func() {
		var sends atomic.Int32
		sendPrompt = func(_ context.Context, _ copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
			sends.Add(1)
			return copilotadapter.BridgePromptDeliveryAccepted, nil
		}
		idempotencyKey := uuid.New()
		body := api.SubmitPromptJSONRequestBody{
			IdempotencyKey: idempotencyKey, Text: "finish exactly once", Mode: api.Queue,
		}
		prompted, err := client.SubmitPromptWithResponse(ctx, sessionID, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(prompted.StatusCode()).To(Equal(http.StatusAccepted), string(prompted.Body))

		ended, err := client.EndSessionWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ended.StatusCode()).To(Equal(http.StatusOK), string(ended.Body))
		Expect(ended.JSON200.Status).To(Equal(api.SessionStatusEnded))

		replayed, err := client.SubmitPromptWithResponse(ctx, sessionID, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.StatusCode()).To(Equal(http.StatusAccepted), string(replayed.Body))
		Expect(replayed.JSON202.Id).To(Equal(prompted.JSON202.Id))
		Expect(replayed.JSON202.Status).To(Equal(api.TurnStatusAborted))
		Expect(sends.Load()).To(Equal(int32(1)))

		body.Text = "different terminal prompt"
		conflicting, err := client.SubmitPromptWithResponse(ctx, sessionID, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(conflicting.StatusCode()).To(Equal(http.StatusConflict), string(conflicting.Body))
		Expect(conflicting.JSON409.Code).To(Equal("idempotency_key_reused"))

		fresh, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: "new terminal prompt", Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fresh.StatusCode()).To(Equal(http.StatusConflict), string(fresh.Body))
		Expect(fresh.JSON409.Code).To(Equal("session_terminal"))
	})

	It("converges projector-first abort request cleanup after rollback without touching a successor", func() {
		now := time.Now().UTC()
		turnA, turnB := uuid.New(), uuid.New()
		for _, parameters := range []storedb.CreateTurnParams{
			{ID: turnA, SessionID: sessionID, Status: string(api.TurnStatusRunning), PromptText: "turn A", PromptMode: string(api.Queue), CreatedAt: now, DeliveryStatus: "accepted"},
			{ID: turnB, SessionID: sessionID, Status: string(api.TurnStatusQueued), PromptText: "turn B", PromptMode: string(api.Queue), CreatedAt: now.Add(time.Millisecond), DeliveryStatus: "accepted"},
		} {
			_, err := queries.CreateTurn(ctx, parameters)
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := queries.UpdateSessionStatus(ctx, storedb.UpdateSessionStatusParams{
			ID: sessionID, Status: string(api.SessionStatusRunning), UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		requestA, requestB := uuid.New(), uuid.New()
		for _, parameters := range []storedb.InsertSessionRequestParams{
			{ID: requestA, SessionID: sessionID, TurnID: &turnA, Kind: string(api.Permission), Status: string(api.Pending), Prompt: "allow A", CreatedAt: now},
			{ID: requestB, SessionID: sessionID, TurnID: &turnB, Kind: string(api.Permission), Status: string(api.Pending), Prompt: "allow B", CreatedAt: now.Add(time.Millisecond)},
		} {
			_, err := queries.InsertSessionRequest(ctx, parameters)
			Expect(err).NotTo(HaveOccurred())
		}
		activeTurn = func() (uuid.UUID, bool) { return turnA, true }
		var abortCalls atomic.Int32
		abortTurn = func(_ context.Context, expected uuid.UUID) (uuid.UUID, error) {
			abortCalls.Add(1)
			Expect(expected).To(Equal(turnA))
			_, finalizeErr := queries.FinalizeTurn(ctx, storedb.FinalizeTurnParams{
				ID: turnA, Status: string(api.TurnStatusAborted), CompletedAt: null.TimeFrom(time.Now().UTC()),
			})
			return turnA, finalizeErr
		}
		var abandonmentMutex sync.Mutex
		effectiveAbandons := map[uuid.UUID]int{}
		abandonResolution = func(requestID uuid.UUID) {
			abandonmentMutex.Lock()
			defer abandonmentMutex.Unlock()
			if effectiveAbandons[requestID] == 0 {
				effectiveAbandons[requestID] = 1
			}
		}
		storeWithAmbiguousCommit.armRollback()
		idempotencyKey := uuid.New()
		body := api.AbortTurnJSONRequestBody{IdempotencyKey: idempotencyKey, TurnId: turnA}

		aborted, err := client.AbortTurnWithResponse(ctx, sessionID, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(aborted.StatusCode()).To(Equal(http.StatusOK), string(aborted.Body))
		replayed, err := client.AbortTurnWithResponse(ctx, sessionID, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.StatusCode()).To(Equal(http.StatusOK), string(replayed.Body))
		Expect(replayed.JSON200).To(Equal(aborted.JSON200))
		Expect(abortCalls.Load()).To(Equal(int32(1)))

		persistedA, err := queries.GetSessionRequest(ctx, requestA)
		Expect(err).NotTo(HaveOccurred())
		Expect(persistedA.Status).To(Equal(string(api.Denied)))
		Expect(persistedA.DeliveryStatus).To(Equal("abandoned"))
		persistedB, err := queries.GetSessionRequest(ctx, requestB)
		Expect(err).NotTo(HaveOccurred())
		Expect(persistedB.Status).To(Equal(string(api.Pending)))
		Expect(persistedB.DeliveryStatus).To(Equal("pending"))
		abandonmentMutex.Lock()
		Expect(effectiveAbandons).To(Equal(map[uuid.UUID]int{requestA: 1}))
		abandonmentMutex.Unlock()
		events, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
			SessionID: sessionID, RowLimit: 100,
		})
		Expect(err).NotTo(HaveOccurred())
		resolvedA, resolvedB := 0, 0
		for _, event := range events {
			if event.Kind != string(api.SessionEventKindRequestResolved) || event.RequestID == nil {
				continue
			}
			switch *event.RequestID {
			case requestA:
				resolvedA++
				version, versionErr := queries.GetRequestEventVersion(ctx, storedb.GetRequestEventVersionParams{
					SessionID: sessionID, EventSeq: event.Seq,
				})
				Expect(versionErr).NotTo(HaveOccurred())
				Expect(version.Status).To(Equal(string(api.Denied)))
			case requestB:
				resolvedB++
			}
		}
		Expect(resolvedA).To(Equal(1))
		Expect(resolvedB).To(BeZero())
	})

	It("replays an unknown prompt after its live SDK handle detaches", func() {
		var sends atomic.Int32
		sendPrompt = func(_ context.Context, _ copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
			sends.Add(1)
			return copilotadapter.BridgePromptDeliveryUnknown, errors.New("delivery acknowledgement lost")
		}
		idempotencyKey := uuid.New()
		body := api.SubmitPromptJSONRequestBody{
			IdempotencyKey: idempotencyKey, Text: "ambiguous exactly once", Mode: api.Queue,
		}
		prompted, err := client.SubmitPromptWithResponse(ctx, sessionID, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(prompted.StatusCode()).To(Equal(http.StatusAccepted), string(prompted.Body))
		Expect(service.Close()).To(Succeed())

		replayed, err := client.SubmitPromptWithResponse(ctx, sessionID, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.StatusCode()).To(Equal(http.StatusAccepted), string(replayed.Body))
		Expect(replayed.JSON202.Id).To(Equal(prompted.JSON202.Id))
		Expect(sends.Load()).To(Equal(int32(1)))

		body.Text = "different detached prompt"
		conflicting, err := client.SubmitPromptWithResponse(ctx, sessionID, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(conflicting.StatusCode()).To(Equal(http.StatusConflict), string(conflicting.Body))
		Expect(conflicting.JSON409.Code).To(Equal("idempotency_key_reused"))

		fresh, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: "new detached prompt", Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fresh.StatusCode()).To(Equal(http.StatusConflict), string(fresh.Body))
		Expect(fresh.JSON409.Code).To(Equal("session_not_live"))
	})

	It("keeps an ambiguously delivered prompt unknown until late SDK events durably accept it", func() {
		type sendObservation struct {
			prompt      copilotadapter.BridgePrompt
			hasDeadline bool
			contextErr  error
		}
		sends := make(chan sendObservation, 1)
		acknowledged := make(chan uuid.UUID, 1)
		acknowledgeDelivery = func(turnID uuid.UUID) { acknowledged <- turnID }
		sendPrompt = func(callContext context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
			_, hasDeadline := callContext.Deadline()
			sends <- sendObservation{prompt: prompt, hasDeadline: hasDeadline, contextErr: callContext.Err()}
			return copilotadapter.BridgePromptDeliveryUnknown, context.DeadlineExceeded
		}

		prompted, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: "delivered but acknowledgement lost", Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(prompted.StatusCode()).To(Equal(http.StatusAccepted), string(prompted.Body))
		observation := <-sends
		Expect(observation.hasDeadline).To(BeTrue(), "SDK delivery must outlive the HTTP request but remain bounded")
		Expect(observation.contextErr).NotTo(HaveOccurred())
		turnID := observation.prompt.TurnID
		unknown, err := queries.GetTurn(ctx, turnID)
		Expect(err).NotTo(HaveOccurred())
		Expect(unknown.Status).To(Equal(string(api.TurnStatusQueued)))
		Expect(unknown.DeliveryStatus).To(Equal("unknown"))

		// Lose the first projection commit acknowledgement too. The retry must
		// recognize the durable source-event receipt before releasing the bridge
		// tombstone.
		storeWithAmbiguousCommit.arm()
		now := time.Now().UTC()
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventTurnStarted, TurnID: &turnID, OccurredAt: now,
		}
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventToolCall, TurnID: &turnID,
			ToolName: "bash", ToolCallID: "late-call", Text: "pwd", OccurredAt: now.Add(time.Millisecond),
		}
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventTurnCompleted, TurnID: &turnID,
			OccurredAt: now.Add(2 * time.Millisecond),
		}
		awaitTurnStatus(turnID, string(api.TurnStatusCompleted))
		completed, err := queries.GetTurn(ctx, turnID)
		Expect(err).NotTo(HaveOccurred())
		Expect(completed.DeliveryStatus).To(Equal("accepted"))
		Expect(acknowledged).To(Receive(Equal(turnID)))
		items, err := queries.ListTranscriptItemsAfterSeq(ctx, storedb.ListTranscriptItemsAfterSeqParams{
			SessionID: sessionID, RowLimit: 10,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(ContainElement(And(
			HaveField("TurnID", Equal(&turnID)),
			HaveField("ToolCallID", Equal(null.StringFrom("late-call"))),
		)))
	})

	It("retries one scheduled occurrence after an accepted send loses its durable acknowledgement", func() {
		var bridgeCalls atomic.Int32
		var sdkInvocations atomic.Int32
		seen := sync.Map{}
		type sendObservation struct {
			prompt      copilotadapter.BridgePrompt
			deadline    time.Time
			hasDeadline bool
		}
		sends := make(chan sendObservation, 2)
		acknowledged := make(chan uuid.UUID, 1)
		acknowledgeDelivery = func(turnID uuid.UUID) { acknowledged <- turnID }
		sendPrompt = func(callContext context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
			bridgeCalls.Add(1)
			deadline, hasDeadline := callContext.Deadline()
			sends <- sendObservation{prompt: prompt, deadline: deadline, hasDeadline: hasDeadline}
			if _, loaded := seen.LoadOrStore(prompt.TurnID, struct{}{}); !loaded {
				sdkInvocations.Add(1)
			}
			return copilotadapter.BridgePromptDeliveryAccepted, nil
		}
		storeWithAckFailure.arm()

		prompted, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: "retry this scheduled occurrence", Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(prompted.StatusCode()).To(Equal(http.StatusInternalServerError), string(prompted.Body))
		firstSend := <-sends
		Expect(firstSend.hasDeadline).To(BeTrue())
		turnID := firstSend.prompt.TurnID
		unknown, err := queries.GetTurn(ctx, turnID)
		Expect(err).NotTo(HaveOccurred())
		Expect(unknown.DeliveryStatus).To(Equal("unknown"))

		now := time.Now().UTC()
		scheduledAt := now.Truncate(time.Minute).Add(-time.Minute)
		scheduleID := uuid.New()
		jobName := "chat/" + scheduleID.String()
		expression := fmt.Sprintf("%d %d * * *", scheduledAt.Minute(), scheduledAt.Hour())
		_, err = queries.CreateChatSchedule(ctx, storedb.CreateChatScheduleParams{
			ID: scheduleID, SessionID: sessionID, DisplayName: "retry ambiguous acknowledgement",
			Prompt: firstSend.prompt.Text, CronExpression: expression, Timezone: "UTC",
			Status: string(api.ChatScheduleStatusActive), CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		schedule := cron.Spec(cron.Raw(expression)).In(time.UTC)
		definition, err := schedule.Definition()
		Expect(err).NotTo(HaveOccurred())
		_, err = scheduleStore.Reconcile(ctx, []cron.JobDefinition{{
			Name: jobName, Schedule: definition, CatchUp: cron.CatchUpLatest, Overlap: cron.OverlapSkip,
		}}, scheduledAt.Add(-time.Nanosecond))
		Expect(err).NotTo(HaveOccurred())
		occurrenceID := cron.OccurrenceID(jobName, scheduledAt)
		nextScheduledAt, err := schedule.Next(scheduledAt)
		Expect(err).NotTo(HaveOccurred())
		claim, err := scheduleStore.Claim(ctx, cron.ClaimRequest{
			OccurrenceID: occurrenceID, JobName: jobName, ScheduledAt: scheduledAt, NextRunAt: nextScheduledAt,
			LeaseOwner: "crashed-runner", LeaseToken: "expired-lease",
			ClaimedAt: scheduledAt, LeaseUntil: scheduledAt.Add(time.Second),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(claim.Disposition).To(Equal(cron.ClaimAcquired))
		_, err = queries.ClaimScheduledTurnOccurrence(ctx, storedb.ClaimScheduledTurnOccurrenceParams{
			ScheduleOccurrenceID: occurrenceID, TurnID: turnID,
		})
		Expect(err).NotTo(HaveOccurred())

		scheduleContext, stopSchedules := context.WithCancel(ctx)
		schedulesFinished := make(chan error, 1)
		go func() { schedulesFinished <- service.RunSchedules(scheduleContext) }()
		DeferCleanup(func() {
			stopSchedules()
			Eventually(schedulesFinished).WithTimeout(lifecycleBudget.Within).Should(Receive(Succeed()))
		})
		patience.Await(GinkgoT(), "the scheduled retry persists delivery acceptance", lifecycleBudget,
			func() string {
				turn, lookupErr := queries.GetTurn(ctx, turnID)
				if lookupErr != nil {
					return ""
				}
				return turn.DeliveryStatus
			},
			func(status string) bool { return status == "accepted" })
		secondSend := <-sends
		Expect(secondSend.hasDeadline).To(BeTrue())
		Expect(secondSend.prompt.TurnID).To(Equal(turnID))
		Expect(bridgeCalls.Load()).To(Equal(int32(2)))
		Expect(sdkInvocations.Load()).To(Equal(int32(1)), "the bridge must not reinvoke the accepted TurnID")
		Expect(acknowledged).To(Receive(Equal(turnID)), "only durable acceptance releases the bridge tombstone")
		ackDeadlines := storeWithAckFailure.observedDeadlines()
		Expect(ackDeadlines).To(HaveLen(2))
		Expect(ackDeadlines[0]).To(BeTemporally(">", firstSend.deadline), "acknowledgement needs a fresh budget after Send")
		Expect(ackDeadlines[1]).To(BeTemporally(">", secondSend.deadline), "retry acknowledgement needs a fresh budget after Send")
	})

	It("reconciles a lost scheduled-turn commit before delivering it exactly once", func() {
		sends := make(chan copilotadapter.BridgePrompt, 2)
		var sendCalls atomic.Int32
		sendPrompt = func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
			sendCalls.Add(1)
			sends <- prompt
			return copilotadapter.BridgePromptDeliveryAccepted, nil
		}
		occurrenceID := seedDueChatSchedule(ctx, queries, scheduleStore, sessionID, "committed scheduled prompt")
		storeWithAmbiguousCommit.arm()

		scheduleContext, stopSchedules := context.WithCancel(ctx)
		schedulesFinished := make(chan error, 1)
		go func() { schedulesFinished <- service.RunSchedules(scheduleContext) }()
		DeferCleanup(func() {
			stopSchedules()
			Eventually(schedulesFinished).WithTimeout(lifecycleBudget.Within).Should(Receive(Succeed()))
		})

		var delivered copilotadapter.BridgePrompt
		Eventually(sends).WithTimeout(lifecycleBudget.Within).Should(Receive(&delivered))
		Expect(delivered.Text).To(Equal("committed scheduled prompt"))
		awaitScheduleOccurrenceStatus(scheduleStore, occurrenceID, cron.OccurrenceSucceeded)
		persisted, err := queries.GetTurnByScheduleOccurrence(ctx, occurrenceID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.ID).To(Equal(delivered.TurnID))
		Expect(persisted.DeliveryStatus).To(Equal("accepted"))
		count, err := queries.CountSessionTurns(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(int64(1)))
		Expect(sendCalls.Load()).To(Equal(int32(1)))
		Consistently(sends, 150*time.Millisecond, 10*time.Millisecond).ShouldNot(Receive())
	})

	It("does not deliver a scheduled turn whose transaction rolled back", func() {
		var sendCalls atomic.Int32
		sendPrompt = func(_ context.Context, _ copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
			sendCalls.Add(1)
			return copilotadapter.BridgePromptDeliveryAccepted, nil
		}
		occurrenceID := seedDueChatSchedule(ctx, queries, scheduleStore, sessionID, "rolled-back scheduled prompt")
		storeWithAmbiguousCommit.armRollback()

		scheduleContext, stopSchedules := context.WithCancel(ctx)
		schedulesFinished := make(chan error, 1)
		go func() { schedulesFinished <- service.RunSchedules(scheduleContext) }()
		DeferCleanup(func() {
			stopSchedules()
			Eventually(schedulesFinished).WithTimeout(lifecycleBudget.Within).Should(Receive(Succeed()))
		})

		awaitScheduleOccurrenceStatus(scheduleStore, occurrenceID, cron.OccurrenceFailed)
		_, err := queries.GetTurnByScheduleOccurrence(ctx, occurrenceID)
		Expect(err).To(MatchError(sql.ErrNoRows))
		count, err := queries.CountSessionTurns(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(BeZero())
		Expect(sendCalls.Load()).To(BeZero())
		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Status).To(Equal(string(api.SessionStatusIdle)))
	})

	It("hands off a due occurrence while its durable schedule revision remains active", func() {
		sent := make(chan copilotadapter.BridgePrompt, 1)
		sendPrompt = func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
			sent <- prompt
			return copilotadapter.BridgePromptDeliveryAccepted, nil
		}
		now := time.Now().UTC()
		scheduledAt := now.Truncate(time.Minute).Add(-time.Minute)
		scheduleID := uuid.New()
		jobName := "chat/" + scheduleID.String()
		expression := fmt.Sprintf("%d %d * * *", scheduledAt.Minute(), scheduledAt.Hour())
		_, err := queries.CreateChatSchedule(ctx, storedb.CreateChatScheduleParams{
			ID: scheduleID, SessionID: sessionID, DisplayName: "active schedule",
			Prompt: "active schedule handoff", CronExpression: expression, Timezone: "UTC",
			Status: string(api.ChatScheduleStatusActive), CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		schedule := cron.Spec(cron.Raw(expression)).In(time.UTC)
		definition, err := schedule.Definition()
		Expect(err).NotTo(HaveOccurred())
		_, err = scheduleStore.Reconcile(ctx, []cron.JobDefinition{{
			Name: jobName, Schedule: definition, CatchUp: cron.CatchUpLatest, Overlap: cron.OverlapSkip,
		}}, scheduledAt.Add(-time.Nanosecond))
		Expect(err).NotTo(HaveOccurred())

		scheduleContext, stopSchedules := context.WithCancel(ctx)
		schedulesFinished := make(chan error, 1)
		go func() { schedulesFinished <- service.RunSchedules(scheduleContext) }()
		DeferCleanup(func() {
			stopSchedules()
			Eventually(schedulesFinished).WithTimeout(lifecycleBudget.Within).Should(Receive(Succeed()))
		})
		Eventually(sent).WithTimeout(lifecycleBudget.Within).Should(Receive(And(
			HaveField("Text", Equal("active schedule handoff")),
			HaveField("Author", Equal("Scheduled task")),
		)))
		persisted, err := queries.GetChatSchedule(ctx, scheduleID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Status).To(Equal(string(api.ChatScheduleStatusActive)))
	})

	It("retains managed worktree residue after a definite starting-row rollback", func() {
		prepared := copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{
				ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD",
			},
			Path: "/tmp/work/managed-rollback", BaseRef: "HEAD", Managed: true,
		}
		prepareWorktree = func(_ context.Context, _ copilotadapter.WorktreeRequest) (copilotadapter.PreparedWorktree, error) {
			return prepared, nil
		}
		var closeAttempts atomic.Int32
		closeSession = func() error {
			closeAttempts.Add(1)
			return nil
		}
		storeWithAmbiguousCommit.armRollback()

		created, err := client.CreateSessionWithResponse(ctx, newWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusInternalServerError), string(created.Body))
		Expect(closeAttempts.Load()).To(BeZero())
		worktreesRows, err := queries.ListWorktrees(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(worktreesRows).NotTo(ContainElement(HaveField("Path", Equal(prepared.Path))))
	})

	It("retains a managed worktree when create reconciliation is unknown", func() {
		prepared := copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{
				ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD",
			},
			Path: "/tmp/work/managed-unknown", BaseRef: "HEAD", Managed: true,
		}
		prepareWorktree = func(_ context.Context, _ copilotadapter.WorktreeRequest) (copilotadapter.PreparedWorktree, error) {
			return prepared, nil
		}
		var closeAttempts atomic.Int32
		closeSession = func() error {
			closeAttempts.Add(1)
			return nil
		}
		storeWithAmbiguousCommit.armRollback()
		storeWithAmbiguousCommit.armReconciliationReadFailure()

		created, err := client.CreateSessionWithResponse(ctx, newWorktreeSessionBody("gpt-5", "repo"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusInternalServerError), string(created.Body))
		Expect(closeAttempts.Load()).To(BeZero())
	})

	It("closes the bridge and reloads schedules after an ambiguous end commit", func() {
		closed := make(chan struct{})
		closeSession = func() error {
			close(closed)
			return nil
		}
		now := time.Now().UTC()
		scheduleID := uuid.New()
		_, err := queries.CreateChatSchedule(ctx, storedb.CreateChatScheduleParams{
			ID: scheduleID, SessionID: sessionID, DisplayName: "pause after end",
			Prompt: "must not run", CronExpression: "0 0 1 1 *", Timezone: "UTC",
			Status: string(api.ChatScheduleStatusActive), CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		scheduleContext, stopSchedules := context.WithCancel(ctx)
		schedulesFinished := make(chan error, 1)
		go func() { schedulesFinished <- service.RunSchedules(scheduleContext) }()
		DeferCleanup(func() {
			stopSchedules()
			Eventually(schedulesFinished).WithTimeout(lifecycleBudget.Within).Should(Receive(Succeed()))
		})
		patience.Await(GinkgoT(), "schedule runtime loads the active job", lifecycleBudget,
			func() int {
				snapshot, snapshotErr := scheduleStore.Snapshot(ctx)
				if snapshotErr != nil {
					return 0
				}
				return len(snapshot.Jobs)
			},
			func(jobs int) bool { return jobs == 1 })
		storeWithAmbiguousCommit.arm()

		ended, err := client.EndSessionWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ended.StatusCode()).To(Equal(http.StatusOK), string(ended.Body))
		Eventually(closed).WithTimeout(lifecycleBudget.Within).Should(BeClosed())
		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Status).To(Equal(string(api.SessionStatusEnded)))
		paused, err := queries.GetChatSchedule(ctx, scheduleID)
		Expect(err).NotTo(HaveOccurred())
		Expect(paused.Status).To(Equal(string(api.ChatScheduleStatusPaused)))
		patience.Await(GinkgoT(), "schedule runtime observes the ambiguously committed end", lifecycleBudget,
			func() int {
				snapshot, snapshotErr := scheduleStore.Snapshot(ctx)
				if snapshotErr != nil {
					return -1
				}
				return len(snapshot.Jobs)
			},
			func(jobs int) bool { return jobs == 0 })
	})

	It("detaches the bridge when an ambiguous end commit cannot be reconciled", func() {
		var closeAttempts atomic.Int32
		closeSession = func() error {
			closeAttempts.Add(1)
			return errors.New("simulated detached close failure")
		}
		storeWithAmbiguousCommit.arm()
		storeWithAmbiguousCommit.armSessionEventReconciliationReadFailure()

		ended, err := client.EndSessionWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ended.StatusCode()).To(Equal(http.StatusInternalServerError), string(ended.Body))
		Expect(ended.JSON500.Code).To(Equal("store_error"))
		Expect(closeAttempts.Load()).To(Equal(int32(1)))
		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Status).To(Equal(string(api.SessionStatusEnded)))

		retried, err := client.EndSessionWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retried.StatusCode()).To(Equal(http.StatusOK), string(retried.Body))
		Expect(closeAttempts.Load()).To(Equal(int32(1)), "the unresolved handle must stay detached after Close fails")
	})

	It("ends an already failed session idempotently without erasing its failure", func() {
		failedAt := time.Now().UTC().Add(-time.Minute)
		failed, err := queries.MarkSessionEnded(ctx, storedb.MarkSessionEndedParams{
			ID: sessionID, Status: string(api.SessionStatusFailed),
			EndedAt: null.TimeFrom(failedAt), UpdatedAt: failedAt,
		})
		Expect(err).NotTo(HaveOccurred())
		beforeEvents := sessionUpdatedCount()
		var closeAttempts atomic.Int32
		closeSession = func() error {
			closeAttempts.Add(1)
			return nil
		}

		ended, err := client.EndSessionWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ended.StatusCode()).To(Equal(http.StatusOK), string(ended.Body))
		Expect(ended.JSON200.Status).To(Equal(api.SessionStatusFailed))
		Expect(closeAttempts.Load()).To(Equal(int32(1)))
		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Status).To(Equal(string(api.SessionStatusFailed)))
		Expect(persisted.EndedAt).To(Equal(failed.EndedAt))
		Expect(sessionUpdatedCount()).To(Equal(beforeEvents))
	})

	It("preserves a failure that wins the locked-row race with EndSession", func() {
		beforeEvents := sessionUpdatedCount()
		var closeAttempts atomic.Int32
		closeSession = func() error {
			closeAttempts.Add(1)
			return nil
		}
		storeWithLockFailure.arm()

		ended, err := client.EndSessionWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ended.StatusCode()).To(Equal(http.StatusOK), string(ended.Body))
		Expect(ended.JSON200.Status).To(Equal(api.SessionStatusFailed))
		Expect(closeAttempts.Load()).To(Equal(int32(1)))
		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Status).To(Equal(string(api.SessionStatusFailed)))
		Expect(sessionUpdatedCount()).To(Equal(beforeEvents))
	})

	It("keeps the bridge and schedule active after a definite end rollback", func() {
		var closeAttempts atomic.Int32
		closeSession = func() error {
			closeAttempts.Add(1)
			return nil
		}
		sent := make(chan copilotadapter.BridgePrompt, 1)
		sendPrompt = func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
			sent <- prompt
			return copilotadapter.BridgePromptDeliveryAccepted, nil
		}
		now := time.Now().UTC()
		scheduleID := uuid.New()
		_, err := queries.CreateChatSchedule(ctx, storedb.CreateChatScheduleParams{
			ID: scheduleID, SessionID: sessionID, DisplayName: "survive rolled back end",
			Prompt: "still active", CronExpression: "0 0 1 1 *", Timezone: "UTC",
			Status: string(api.ChatScheduleStatusActive), CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		storeWithAmbiguousCommit.armRollback()

		ended, err := client.EndSessionWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ended.StatusCode()).To(Equal(http.StatusInternalServerError), string(ended.Body))
		Expect(closeAttempts.Load()).To(BeZero())
		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Status).To(Equal(string(api.SessionStatusIdle)))
		active, err := queries.GetChatSchedule(ctx, scheduleID)
		Expect(err).NotTo(HaveOccurred())
		Expect(active.Status).To(Equal(string(api.ChatScheduleStatusActive)))
		_, found := latestSessionVersion()
		Expect(found).To(BeFalse())
		prompted, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: "still attached", Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(prompted.StatusCode()).To(Equal(http.StatusAccepted), string(prompted.Body))
		Eventually(sent).WithTimeout(lifecycleBudget.Within).Should(Receive(HaveField("Text", Equal("still attached"))))
	})

	It("completes the turn the event names and leaves the queued one running", func() {
		first := submit("first")
		second := submit("second")
		Expect(first).NotTo(Equal(second))

		events <- copilotadapter.BridgeEvent{
			Kind: copilotadapter.BridgeEventTurnCompleted, OccurredAt: time.Now().UTC(),
			TurnID: &first,
		}

		awaitTurnStatus(first, "completed")
		later, err := queries.GetTurn(ctx, second)
		Expect(err).NotTo(HaveOccurred())
		Expect(later.Status).NotTo(Equal("completed"))
		Expect(later.CompletedAt.Valid).To(BeFalse())
	})

	It("does not guess a turn when a completion has no correlation", func() {
		only := submit("only")
		events <- copilotadapter.BridgeEvent{Kind: copilotadapter.BridgeEventTurnCompleted, OccurredAt: time.Now().UTC()}
		events <- copilotadapter.BridgeEvent{
			Kind: copilotadapter.BridgeEventAssistantMessage, OccurredAt: time.Now().UTC(),
			Text: "projection barrier", Author: "copilot",
		}
		patience.Await(GinkgoT(), "the later projection lands", lifecycleBudget,
			func() int64 {
				rows, err := queries.ListTranscriptItemsAfterSeq(ctx, storedb.ListTranscriptItemsAfterSeqParams{
					SessionID: sessionID, AfterSeq: 1, RowLimit: 10,
				})
				if err != nil {
					return 0
				}
				return int64(len(rows))
			},
			func(observed int64) bool { return observed == 1 })
		turn, err := queries.GetTurn(ctx, only)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn.Status).To(Equal("queued"))
		Expect(turn.CompletedAt.Valid).To(BeFalse())
	})

	It("carries the whole transcript item, its call id included, into the transcriptAppended frame", func() {
		events <- copilotadapter.BridgeEvent{
			Kind: copilotadapter.BridgeEventToolCall, OccurredAt: time.Now().UTC(),
			ToolName: "bash", ToolCallID: "call-1", Text: `{"cmd":"ls"}`, Author: "copilot",
		}
		events <- copilotadapter.BridgeEvent{
			Kind: copilotadapter.BridgeEventToolResult, OccurredAt: time.Now().UTC(),
			ToolName: "bash", ToolCallID: "call-1", Text: "README.md", Author: "copilot",
		}
		events <- copilotadapter.BridgeEvent{
			Kind: copilotadapter.BridgeEventAssistantMessage, OccurredAt: time.Now().UTC(),
			Text: "message without attribution",
		}

		frames := patience.Await(GinkgoT(), "three transcriptAppended frames", lifecycleBudget,
			func() []storedb.SessionEvent {
				rows, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
					SessionID: sessionID, AfterSeq: 0, RowLimit: 10,
				})
				if err != nil {
					return nil
				}
				return rows
			},
			func(rows []storedb.SessionEvent) bool { return len(rows) >= 3 })

		Expect(frames[0].Kind).To(Equal(string(api.SessionEventKindTranscriptAppended)))
		Expect(frames[0].TranscriptSeq.Valid).To(BeTrue())
		Expect(frames[1].Kind).To(Equal(string(api.SessionEventKindTranscriptAppended)))
		Expect(frames[1].TranscriptSeq.Valid).To(BeTrue())
		Expect(frames[1].TranscriptSeq.Int64).To(BeNumerically(">", frames[0].TranscriptSeq.Int64))

		stored, err := queries.ListTranscriptItemsAfterSeq(ctx, storedb.ListTranscriptItemsAfterSeqParams{
			SessionID: sessionID, AfterSeq: 0, RowLimit: 10,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored[len(stored)-3].ToolCallID.String).To(Equal("call-1"))
		Expect(stored[len(stored)-2].ToolCallID.String).To(Equal("call-1"))
		Expect(stored[len(stored)-2].Kind).To(Equal(string(api.TranscriptItemKindToolResult)))
		Expect(stored[len(stored)-2].Author.String).To(Equal("copilot"))
		Expect(stored[len(stored)-2].ToolName.String).To(Equal("bash"))
		Expect(stored[len(stored)-2].ToolCallID.String).To(Equal("call-1"))
		Expect(stored[len(stored)-2].Author.Valid).To(BeTrue())
		Expect(stored[len(stored)-2].ToolName.Valid).To(BeTrue())
		Expect(stored[len(stored)-2].ToolCallID.Valid).To(BeTrue())
		Expect(stored[len(stored)-1].Author.Valid).To(BeFalse())
		Expect(stored[len(stored)-1].ToolName.Valid).To(BeFalse())
		Expect(stored[len(stored)-1].ToolCallID.Valid).To(BeFalse())

		transcript, err := client.ListTranscriptWithResponse(ctx, sessionID, &api.ListTranscriptParams{})
		Expect(err).NotTo(HaveOccurred())
		Expect(transcript.StatusCode()).To(Equal(http.StatusOK), string(transcript.Body))
		Expect(transcript.JSON200.Data).To(ContainElement(And(
			HaveField("Text", Equal("message without attribution")),
			HaveField("Author", BeNil()),
		)))
		Expect(transcript.JSON200.Data).To(ContainElement(And(
			HaveField("Text", Equal("README.md")),
			HaveField("Author", PointTo(Equal("copilot"))),
			HaveField("ToolName", PointTo(Equal("bash"))),
			HaveField("ToolCallId", PointTo(Equal("call-1"))),
		)))
	})

	It("records an externally completed denial exactly once in the transcript", func() {
		turnID := submit("ask before running")
		requestID := uuid.New()
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventRequestOpened, OccurredAt: time.Now().UTC(),
			TurnID: &turnID, RequestID: &requestID, RequestKind: copilotadapter.BridgeRequestPermission,
			ToolName: "bash", Text: "run the command?",
		}
		patience.Await(GinkgoT(), "permission request projection", lifecycleBudget,
			func() string {
				request, err := queries.GetSessionRequest(ctx, requestID)
				if err != nil {
					return ""
				}
				return request.Status
			},
			func(status string) bool { return status == string(api.Pending) })

		completionID := uuid.NewString()
		events <- copilotadapter.BridgeEvent{
			ID: completionID, Kind: copilotadapter.BridgeEventRequestCompleted, OccurredAt: time.Now().UTC(),
			RequestID: &requestID, ResolutionDecision: string(api.Deny),
		}
		events <- copilotadapter.BridgeEvent{
			ID: completionID, Kind: copilotadapter.BridgeEventRequestCompleted, OccurredAt: time.Now().UTC(),
			RequestID: &requestID, ResolutionDecision: string(api.Deny),
		}
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventAssistantMessage, OccurredAt: time.Now().UTC(),
			TurnID: &turnID, Text: "projection barrier", Author: "copilot",
		}

		patience.Await(GinkgoT(), "completion replay barrier", lifecycleBudget,
			func() bool {
				rows, err := queries.ListTranscriptItemsAfterSeq(ctx, storedb.ListTranscriptItemsAfterSeqParams{
					SessionID: sessionID, AfterSeq: 0, RowLimit: 10,
				})
				if err != nil {
					return false
				}
				for _, row := range rows {
					if row.Body == "projection barrier" {
						return true
					}
				}
				return false
			},
			func(projected bool) bool { return projected })

		resolved, err := queries.GetSessionRequest(ctx, requestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.Status).To(Equal(string(api.Denied)))
		Expect(resolved.DeliveryStatus).To(Equal("delivered"))
		rows, err := queries.ListTranscriptItemsAfterSeq(ctx, storedb.ListTranscriptItemsAfterSeqParams{
			SessionID: sessionID, AfterSeq: 0, RowLimit: 10,
		})
		Expect(err).NotTo(HaveOccurred())
		var notices []storedb.TranscriptItem
		for _, row := range rows {
			if row.Kind == string(api.TranscriptItemKindSystemNotice) {
				notices = append(notices, row)
			}
		}
		Expect(notices).To(ConsistOf(And(
			HaveField("TurnID", PointTo(Equal(turnID))),
			HaveField("Body", Equal("Permission denied for bash.")),
		)))
	})

	It("discards a removed legacy request kind", func() {
		events <- copilotadapter.BridgeEvent{
			Kind: copilotadapter.BridgeEventRequestOpened, OccurredAt: time.Now().UTC(),
			RequestID:   func() *uuid.UUID { v := uuid.New(); return &v }(),
			RequestKind: copilotadapter.BridgeRequestKind("planReview"),
			Text:        `{"summary":"ship it"}`,
		}

		Consistently(func() int {
			rows, err := queries.ListSessionRequests(ctx, storedb.ListSessionRequestsParams{SessionID: sessionID})
			Expect(err).NotTo(HaveOccurred())
			return len(rows)
		}).Should(BeZero())
	})

	It("retries ambiguous projection commits without duplicating domain facts", func() {
		requestID := uuid.New()
		storeWithAmbiguousCommit.arm()
		events <- copilotadapter.BridgeEvent{
			ID: requestID.String(), Kind: copilotadapter.BridgeEventRequestOpened, OccurredAt: time.Now().UTC(),
			RequestID: &requestID, RequestKind: copilotadapter.BridgeRequestPermission, Text: "approve once",
		}
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventAssistantMessage, OccurredAt: time.Now().UTC(),
			Text: "first barrier", Author: "copilot",
		}
		patience.Await(GinkgoT(), "request retry finishes before the barrier", lifecycleBudget,
			func() int {
				rows, err := queries.ListTranscriptItemsAfterSeq(ctx, storedb.ListTranscriptItemsAfterSeqParams{
					SessionID: sessionID, RowLimit: 10,
				})
				if err != nil {
					return 0
				}
				return len(rows)
			},
			func(observed int) bool { return observed == 1 })
		requests, err := queries.ListSessionRequests(ctx, storedb.ListSessionRequestsParams{SessionID: sessionID})
		Expect(err).NotTo(HaveOccurred())
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].ID).To(Equal(requestID))

		storeWithAmbiguousCommit.arm()
		messageID := uuid.NewString()
		events <- copilotadapter.BridgeEvent{
			ID: messageID, Kind: copilotadapter.BridgeEventAssistantMessage, OccurredAt: time.Now().UTC(),
			Text: "persist once", Author: "copilot",
		}
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventAssistantMessage, OccurredAt: time.Now().UTC(),
			Text: "second barrier", Author: "copilot",
		}
		items := patience.Await(GinkgoT(), "transcript retry finishes before the barrier", lifecycleBudget,
			func() []storedb.TranscriptItem {
				rows, err := queries.ListTranscriptItemsAfterSeq(ctx, storedb.ListTranscriptItemsAfterSeqParams{
					SessionID: sessionID, RowLimit: 10,
				})
				if err != nil {
					return nil
				}
				return rows
			},
			func(rows []storedb.TranscriptItem) bool { return len(rows) == 3 })
		bodies := make([]string, 0, len(items))
		for _, item := range items {
			bodies = append(bodies, item.Body)
		}
		Expect(bodies).To(ConsistOf("first barrier", "persist once", "second barrier"))
	})

	It("announces the live subagent aggregate after each activity", func() {
		agentID := "worker-1"
		now := time.Now().UTC()
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventSubagentStarted, OccurredAt: now,
			AgentID: agentID, DisplayName: "Worker", Text: "starting",
		}
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventSubagentProgress, OccurredAt: now.Add(time.Millisecond),
			AgentID: agentID, Text: "current summary",
		}

		version := patience.Await(GinkgoT(), "subagent activity publishes its aggregate", lifecycleBudget,
			func() storedb.SubagentEventVersion {
				events, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
					SessionID: sessionID, RowLimit: 20,
				})
				if err != nil {
					return storedb.SubagentEventVersion{}
				}
				for index := len(events) - 1; index >= 0; index-- {
					if events[index].Kind != string(api.SessionEventKindSubagentUpdated) {
						continue
					}
					row, versionErr := queries.GetSubagentEventVersion(ctx, storedb.GetSubagentEventVersionParams{
						SessionID: sessionID, EventSeq: events[index].Seq,
					})
					if versionErr == nil && row.ActivityCount == 2 {
						return row
					}
				}
				return storedb.SubagentEventVersion{}
			},
			func(row storedb.SubagentEventVersion) bool { return row.ActivityCount == 2 })
		Expect(version.Summary).To(Equal(null.StringFrom("current summary")))
		Expect(version.DisplayName).To(Equal("Worker"), "progress must not replace the SDK display name with the raw agent id")
		Expect(version.Status).To(Equal(string(api.SubagentStatusActive)))
		activity, err := queries.GetSubagentActivity(ctx, storedb.GetSubagentActivityParams{
			SessionID: sessionID, SubagentID: agentID, Seq: 2,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(activity.Kind).To(Equal(string(api.SubagentActivityKindProgress)))
		Expect(activity.Body).To(Equal("current summary"))
	})

	It("coalesces adjacent assistant deltas atomically and preserves UTF-8 chunk bounds", func() {
		now := time.Now().UTC()
		turnID := uuid.New()
		_, err := queries.CreateTurn(ctx, storedb.CreateTurnParams{
			ID: turnID, SessionID: sessionID, Status: string(api.TurnStatusRunning),
			PromptText: "stream a response", PromptMode: string(api.Queue), CreatedAt: now,
			DeliveryStatus: "accepted",
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.UpdateSessionStatus(ctx, storedb.UpdateSessionStatusParams{
			ID: sessionID, Status: string(api.SessionStatusRunning), UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())

		firstID, secondID := uuid.NewString(), uuid.NewString()
		storeWithAmbiguousCommit.arm()
		events <- copilotadapter.BridgeEvent{
			ID: firstID, Kind: copilotadapter.BridgeEventAssistantDelta,
			OccurredAt: now, TurnID: &turnID, Text: "Hel",
		}
		events <- copilotadapter.BridgeEvent{
			ID: secondID, Kind: copilotadapter.BridgeEventAssistantDelta,
			OccurredAt: now.Add(time.Millisecond), TurnID: &turnID, Text: "lo",
		}
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventAssistantMessage,
			OccurredAt: now.Add(2 * time.Millisecond), TurnID: &turnID, Text: "Hello", Author: "copilot",
		}

		persisted := patience.Await(GinkgoT(), "delta batch and transcript boundary commit in order", lifecycleBudget,
			func() []storedb.SessionEvent {
				rows, listErr := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
					SessionID: sessionID, RowLimit: 100,
				})
				if listErr != nil {
					return nil
				}
				return rows
			},
			func(rows []storedb.SessionEvent) bool {
				var deltas, transcripts int
				for _, row := range rows {
					switch row.Kind {
					case string(api.SessionEventKindAssistantDelta):
						if row.TurnID != nil && *row.TurnID == turnID {
							deltas++
						}
					case string(api.SessionEventKindTranscriptAppended):
						transcripts++
					}
				}
				return deltas == 1 && transcripts == 1
			})
		var deltaSeq, transcriptSeq int64
		for _, row := range persisted {
			switch row.Kind {
			case string(api.SessionEventKindAssistantDelta):
				if row.TurnID != nil && *row.TurnID == turnID {
					Expect(row.DeltaText).To(Equal("Hello"))
					deltaSeq = row.Seq
				}
			case string(api.SessionEventKindTranscriptAppended):
				transcriptSeq = row.Seq
			}
		}
		Expect(deltaSeq).To(BeNumerically("<", transcriptSeq))
		transcript, err := client.ListTranscriptWithResponse(ctx, sessionID, &api.ListTranscriptParams{})
		Expect(err).NotTo(HaveOccurred())
		Expect(transcript.StatusCode()).To(Equal(http.StatusOK), string(transcript.Body))
		Expect(transcript.JSON200.Data).To(ContainElement(And(
			HaveField("Kind", Equal(api.TranscriptItemKindAssistantMessage)),
			HaveField("Text", Equal("Hello")),
		)))

		// Both original source IDs committed with the one chunk. Replaying them
		// must not append text even though they arrive as a new in-memory batch.
		events <- copilotadapter.BridgeEvent{ID: firstID, Kind: copilotadapter.BridgeEventAssistantDelta, TurnID: &turnID, Text: "Hel"}
		events <- copilotadapter.BridgeEvent{ID: secondID, Kind: copilotadapter.BridgeEventAssistantDelta, TurnID: &turnID, Text: "lo"}
		Consistently(func() int {
			rows, listErr := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
				SessionID: sessionID, RowLimit: 100,
			})
			if listErr != nil {
				return -1
			}
			count := 0
			for _, row := range rows {
				if row.Kind == string(api.SessionEventKindAssistantDelta) && row.TurnID != nil && *row.TurnID == turnID {
					count++
				}
			}
			return count
		}, 150*time.Millisecond, 10*time.Millisecond).Should(Equal(1))

		largeTurnID := uuid.New()
		_, err = queries.CreateTurn(ctx, storedb.CreateTurnParams{
			ID: largeTurnID, SessionID: sessionID, Status: string(api.TurnStatusQueued),
			PromptText: "stream unicode", PromptMode: string(api.Queue), CreatedAt: now.Add(time.Second),
			DeliveryStatus: "accepted",
		})
		Expect(err).NotTo(HaveOccurred())
		largeText := strings.Repeat("界", 6000)
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventAssistantDelta,
			OccurredAt: now.Add(time.Second), TurnID: &largeTurnID, Text: largeText,
		}
		largeChunks := patience.Await(GinkgoT(), "oversized delta is split on UTF-8 boundaries", lifecycleBudget,
			func() []string {
				rows, listErr := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
					SessionID: sessionID, RowLimit: 100,
				})
				if listErr != nil {
					return nil
				}
				chunks := make([]string, 0, 2)
				for _, row := range rows {
					if row.Kind == string(api.SessionEventKindAssistantDelta) && row.TurnID != nil && *row.TurnID == largeTurnID {
						chunks = append(chunks, row.DeltaText)
					}
				}
				return chunks
			},
			func(chunks []string) bool { return len(chunks) == 2 })
		Expect(strings.Join(largeChunks, "")).To(Equal(largeText))
		deltaMaximum := int(copilotadapter.DefaultAdapterConfig().GetAssistantDeltaMaxBytes())
		for _, chunk := range largeChunks {
			Expect(len(chunk)).To(BeNumerically("<=", deltaMaximum))
			Expect(utf8.ValidString(chunk)).To(BeTrue())
		}

		sourceMaximum := int(copilotadapter.DefaultAdapterConfig().GetAssistantDeltaSourceMaxBytes())
		canonicalAfterOversize := "canonical response after oversized preview"
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventAssistantDelta,
			OccurredAt: now.Add(2 * time.Second), TurnID: &largeTurnID,
			Text: strings.Repeat("x", sourceMaximum+1),
		}
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventAssistantMessage,
			OccurredAt: now.Add(3 * time.Second), TurnID: &largeTurnID,
			Text: canonicalAfterOversize, Author: "copilot",
		}
		patience.Await(GinkgoT(), "canonical message follows rejected oversized preview", lifecycleBudget,
			func() bool {
				page, listErr := client.ListTranscriptWithResponse(ctx, sessionID, &api.ListTranscriptParams{})
				if listErr != nil || page.JSON200 == nil {
					return false
				}
				for _, item := range page.JSON200.Data {
					if item.Text == canonicalAfterOversize {
						return true
					}
				}
				return false
			},
			func(found bool) bool { return found })
		rows, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
			SessionID: sessionID, RowLimit: 100,
		})
		Expect(err).NotTo(HaveOccurred())
		largeChunks = largeChunks[:0]
		for _, row := range rows {
			if row.Kind == string(api.SessionEventKindAssistantDelta) && row.TurnID != nil && *row.TurnID == largeTurnID {
				largeChunks = append(largeChunks, row.DeltaText)
			}
		}
		Expect(largeChunks).To(HaveLen(2))
		Expect(strings.Join(largeChunks, "")).To(Equal(largeText), "oversized preview input must not reach persistence")
	})

	It("bounds a held-store delta burst by event count and flushes the final channel batch", func() {
		now := time.Now().UTC()
		turnID := uuid.New()
		_, err := queries.CreateTurn(ctx, storedb.CreateTurnParams{
			ID: turnID, SessionID: sessionID, Status: string(api.TurnStatusRunning),
			PromptText: "burst", PromptMode: string(api.Queue), CreatedAt: now, DeliveryStatus: "accepted",
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.UpdateSessionStatus(ctx, storedb.UpdateSessionStatusParams{
			ID: sessionID, Status: string(api.SessionStatusRunning), UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())

		transactionHeld, releaseTransaction := storeWithAmbiguousCommit.holdTransaction()
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(releaseTransaction) }) }
		DeferCleanup(release)
		const sourceEvents = 44
		var expected strings.Builder
		for index := range sourceEvents {
			expected.WriteByte(byte('a' + index%26))
		}
		produced := make(chan struct{})
		go func() {
			for index := range sourceEvents {
				events <- copilotadapter.BridgeEvent{
					ID: uuid.NewString(), Kind: copilotadapter.BridgeEventAssistantDelta,
					OccurredAt: now.Add(time.Duration(index) * time.Nanosecond), TurnID: &turnID,
					Text: string(byte('a' + index%26)),
				}
			}
			events <- copilotadapter.BridgeEvent{
				ID: uuid.NewString(), Kind: copilotadapter.BridgeEventAssistantMessage,
				OccurredAt: now.Add(time.Second), TurnID: &turnID, Text: expected.String(), Author: "copilot",
			}
			close(produced)
		}()
		Eventually(transactionHeld).Should(BeClosed())
		Consistently(produced, 100*time.Millisecond, 10*time.Millisecond).ShouldNot(BeClosed(),
			"the bounded event channel must backpressure a source while persistence is held")
		beforeRelease, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
			SessionID: sessionID, RowLimit: 100,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(beforeRelease).To(BeEmpty())

		release()
		Eventually(produced).WithTimeout(lifecycleBudget.Within).Should(BeClosed())
		patience.Await(GinkgoT(), "the batch boundary follows every persisted delta", lifecycleBudget,
			func() bool {
				page, listErr := client.ListTranscriptWithResponse(ctx, sessionID, &api.ListTranscriptParams{})
				if listErr != nil || page.JSON200 == nil {
					return false
				}
				for _, item := range page.JSON200.Data {
					if item.Text == expected.String() {
						return true
					}
				}
				return false
			},
			func(found bool) bool { return found })
		persisted, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
			SessionID: sessionID, RowLimit: 100,
		})
		Expect(err).NotTo(HaveOccurred())
		chunks := make([]string, 0, sourceEvents/4)
		var lastDeltaSeq, transcriptSeq int64
		for _, event := range persisted {
			switch {
			case event.Kind == string(api.SessionEventKindAssistantDelta) && event.TurnID != nil && *event.TurnID == turnID:
				chunks = append(chunks, event.DeltaText)
				lastDeltaSeq = event.Seq
			case event.Kind == string(api.SessionEventKindTranscriptAppended):
				transcriptSeq = event.Seq
			}
		}
		Expect(chunks).To(HaveLen(sourceEvents / 4))
		Expect(strings.Join(chunks, "")).To(Equal(expected.String()))
		Expect(lastDeltaSeq).To(BeNumerically("<", transcriptSeq))

		finalTurnID := uuid.New()
		_, err = queries.CreateTurn(ctx, storedb.CreateTurnParams{
			ID: finalTurnID, SessionID: sessionID, Status: string(api.TurnStatusQueued),
			PromptText: "final flush", PromptMode: string(api.Queue), CreatedAt: now.Add(2 * time.Second),
			DeliveryStatus: "accepted",
		})
		Expect(err).NotTo(HaveOccurred())
		events <- copilotadapter.BridgeEvent{ID: uuid.NewString(), Kind: copilotadapter.BridgeEventAssistantDelta, TurnID: &finalTurnID, Text: "ta"}
		events <- copilotadapter.BridgeEvent{ID: uuid.NewString(), Kind: copilotadapter.BridgeEventAssistantDelta, TurnID: &finalTurnID, Text: "il"}
		close(events)
		patience.Await(GinkgoT(), "closing the source flushes the final bounded batch", lifecycleBudget,
			func() string {
				rows, listErr := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
					SessionID: sessionID, RowLimit: 100,
				})
				if listErr != nil {
					return ""
				}
				for _, event := range rows {
					if event.Kind == string(api.SessionEventKindAssistantDelta) && event.TurnID != nil && *event.TurnID == finalTurnID {
						return event.DeltaText
					}
				}
				return ""
			},
			func(text string) bool { return text == "tail" })
	})

	It("converges a delayed ambiguous abort event on only its target turn and callbacks", func() {
		now := time.Now().UTC()
		fixture := seedAbortProjectionFixture(ctx, queries, sessionID, now)
		turnA, turnB := fixture.targetTurnID, fixture.successorTurnID
		requestA, requestB := fixture.targetRequestID, fixture.successorRequestID
		var callbackMutex sync.Mutex
		callbacks := map[uuid.UUID]int{}
		abandonResolution = func(requestID uuid.UUID) {
			callbackMutex.Lock()
			defer callbackMutex.Unlock()
			callbacks[requestID]++
		}
		storeWithAmbiguousCommit.arm()
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventTurnAborted,
			OccurredAt: now.Add(time.Second), TurnID: &turnA,
		}

		patience.Await(GinkgoT(), "ambiguous abort retry invokes the committed target callback", lifecycleBudget,
			func() int {
				callbackMutex.Lock()
				defer callbackMutex.Unlock()
				return callbacks[requestA]
			},
			func(count int) bool { return count > 0 })
		persistedA, err := queries.GetTurn(ctx, turnA)
		Expect(err).NotTo(HaveOccurred())
		Expect(persistedA.Status).To(Equal(string(api.TurnStatusAborted)))
		persistedB, err := queries.GetTurn(ctx, turnB)
		Expect(err).NotTo(HaveOccurred())
		Expect(persistedB.Status).To(Equal(string(api.TurnStatusQueued)))
		resolvedA, err := queries.GetSessionRequest(ctx, requestA)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolvedA.Status).To(Equal(string(api.Denied)))
		Expect(resolvedA.DeliveryStatus).To(Equal("abandoned"))
		pendingB, err := queries.GetSessionRequest(ctx, requestB)
		Expect(err).NotTo(HaveOccurred())
		Expect(pendingB.Status).To(Equal(string(api.Pending)))
		Expect(pendingB.DeliveryStatus).To(Equal("pending"))
		callbackMutex.Lock()
		Expect(callbacks).To(Equal(map[uuid.UUID]int{requestA: 1}))
		callbackMutex.Unlock()
		session, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(session.Status).To(Equal(string(api.SessionStatusRunning)))
		projected, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
			SessionID: sessionID, RowLimit: 100,
		})
		Expect(err).NotTo(HaveOccurred())
		var turnSnapshots, requestSnapshots, sessionSnapshots int
		for _, event := range projected {
			switch {
			case event.Kind == string(api.SessionEventKindTurnCompleted) && event.TurnID != nil && *event.TurnID == turnA:
				turnSnapshots++
			case event.Kind == string(api.SessionEventKindRequestResolved) && event.RequestID != nil && *event.RequestID == requestA:
				requestSnapshots++
			case event.Kind == string(api.SessionEventKindSessionUpdated):
				sessionSnapshots++
			}
		}
		Expect([]int{turnSnapshots, requestSnapshots, sessionSnapshots}).To(Equal([]int{1, 1, 1}),
			"an ambiguous retry must reuse its source receipt and versioned transition")
	})

	It("keeps abort callbacks and version snapshots behind a rolled-back event transaction", func() {
		now := time.Now().UTC()
		fixture := seedAbortProjectionFixture(ctx, queries, sessionID, now)
		var callbackMutex sync.Mutex
		callbacks := map[uuid.UUID]int{}
		abandonResolution = func(requestID uuid.UUID) {
			callbackMutex.Lock()
			defer callbackMutex.Unlock()
			callbacks[requestID]++
		}
		rollbackObserved, retry := storeWithAmbiguousCommit.armRollbackAndPauseRetry()
		var releaseRetry sync.Once
		allowRetry := func() { releaseRetry.Do(func() { close(retry) }) }
		DeferCleanup(allowRetry)
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventTurnAborted,
			OccurredAt: now.Add(time.Second), TurnID: &fixture.targetTurnID,
		}
		Eventually(rollbackObserved).WithTimeout(lifecycleBudget.Within).Should(BeClosed())

		callbackMutex.Lock()
		Expect(callbacks).To(BeEmpty(), "callbacks must remain postcommit")
		callbackMutex.Unlock()
		targetBeforeRetry, err := queries.GetTurn(ctx, fixture.targetTurnID)
		Expect(err).NotTo(HaveOccurred())
		Expect(targetBeforeRetry.Status).To(Equal(string(api.TurnStatusRunning)))
		targetRequestBeforeRetry, err := queries.GetSessionRequest(ctx, fixture.targetRequestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(targetRequestBeforeRetry.Status).To(Equal(string(api.Pending)))
		beforeRetry, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
			SessionID: sessionID, RowLimit: 100,
		})
		Expect(err).NotTo(HaveOccurred())
		for _, event := range beforeRetry {
			Expect(event.TurnID == nil || *event.TurnID != fixture.targetTurnID ||
				event.Kind != string(api.SessionEventKindTurnCompleted)).To(BeTrue())
			Expect(event.RequestID == nil || *event.RequestID != fixture.targetRequestID ||
				event.Kind != string(api.SessionEventKindRequestResolved)).To(BeTrue())
		}

		allowRetry()
		patience.Await(GinkgoT(), "rolled-back abort retries and invokes only the committed callback", lifecycleBudget,
			func() int {
				callbackMutex.Lock()
				defer callbackMutex.Unlock()
				return callbacks[fixture.targetRequestID]
			},
			func(count int) bool { return count == 1 })
		target, err := queries.GetTurn(ctx, fixture.targetTurnID)
		Expect(err).NotTo(HaveOccurred())
		Expect(target.Status).To(Equal(string(api.TurnStatusAborted)))
		successor, err := queries.GetTurn(ctx, fixture.successorTurnID)
		Expect(err).NotTo(HaveOccurred())
		Expect(successor.Status).To(Equal(string(api.TurnStatusQueued)))
		targetRequest, err := queries.GetSessionRequest(ctx, fixture.targetRequestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(targetRequest.Status).To(Equal(string(api.Denied)))
		Expect(targetRequest.DeliveryStatus).To(Equal("abandoned"))
		successorRequest, err := queries.GetSessionRequest(ctx, fixture.successorRequestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(successorRequest.Status).To(Equal(string(api.Pending)))
		Expect(successorRequest.DeliveryStatus).To(Equal("pending"))
		callbackMutex.Lock()
		Expect(callbacks).To(Equal(map[uuid.UUID]int{fixture.targetRequestID: 1}))
		callbackMutex.Unlock()

		projected, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
			SessionID: sessionID, RowLimit: 100,
		})
		Expect(err).NotTo(HaveOccurred())
		var turnSnapshots, requestSnapshots, sessionSnapshots int
		for _, event := range projected {
			switch {
			case event.Kind == string(api.SessionEventKindTurnCompleted) && event.TurnID != nil && *event.TurnID == fixture.targetTurnID:
				turnSnapshots++
			case event.Kind == string(api.SessionEventKindRequestResolved) && event.RequestID != nil && *event.RequestID == fixture.targetRequestID:
				requestSnapshots++
			case event.Kind == string(api.SessionEventKindSessionUpdated):
				sessionSnapshots++
			}
		}
		Expect([]int{turnSnapshots, requestSnapshots, sessionSnapshots}).To(Equal([]int{1, 1, 1}))
	})

	It("discards a delayed abort target owned by another session and continues projection", func() {
		now := time.Now().UTC()
		current, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		otherSessionID := uuid.New()
		_, err = queries.CreateSession(ctx, storedb.CreateSessionParams{
			ID: otherSessionID, WorktreeID: current.WorktreeID, DisplayName: "other session", Model: "gpt-5",
			WorkingDirectory: current.WorkingDirectory, Status: string(api.SessionStatusRunning),
			CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(queries.EnsureSessionCounter(ctx, otherSessionID)).To(Succeed())
		foreignTurnID := uuid.New()
		_, err = queries.CreateTurn(ctx, storedb.CreateTurnParams{
			ID: foreignTurnID, SessionID: otherSessionID, Status: string(api.TurnStatusRunning),
			PromptText: "foreign turn", PromptMode: string(api.Queue), CreatedAt: now,
			DeliveryStatus: "accepted",
		})
		Expect(err).NotTo(HaveOccurred())
		foreignRequestID := uuid.New()
		_, err = queries.InsertSessionRequest(ctx, storedb.InsertSessionRequestParams{
			ID: foreignRequestID, SessionID: otherSessionID, TurnID: &foreignTurnID,
			Kind: string(api.Permission), Status: string(api.Pending), Prompt: "foreign request", CreatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		var callbacks atomic.Int32
		abandonResolution = func(_ uuid.UUID) { callbacks.Add(1) }

		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventTurnAborted,
			OccurredAt: now.Add(time.Second), TurnID: &foreignTurnID,
		}
		marker := "projection continued after foreign abort"
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventAssistantMessage,
			OccurredAt: now.Add(2 * time.Second), Text: marker, Author: "copilot",
		}
		patience.Await(GinkgoT(), "projector advances past the invalid foreign abort", lifecycleBudget,
			func() bool {
				transcript, listErr := client.ListTranscriptWithResponse(ctx, sessionID, &api.ListTranscriptParams{})
				if listErr != nil || transcript.JSON200 == nil {
					return false
				}
				for _, item := range transcript.JSON200.Data {
					if item.Text == marker {
						return true
					}
				}
				return false
			},
			func(found bool) bool { return found })
		foreignTurn, err := queries.GetTurn(ctx, foreignTurnID)
		Expect(err).NotTo(HaveOccurred())
		Expect(foreignTurn.Status).To(Equal(string(api.TurnStatusRunning)))
		foreignRequest, err := queries.GetSessionRequest(ctx, foreignRequestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(foreignRequest.Status).To(Equal(string(api.Pending)))
		Expect(foreignRequest.DeliveryStatus).To(Equal("pending"))
		Expect(callbacks.Load()).To(BeZero())
	})

	It("atomically finalizes and detaches a session after an unrecoverable bridge event", func() {
		turnID := submit("work interrupted by the bridge")
		now := time.Now().UTC()
		requestID := uuid.New()
		_, err := queries.InsertSessionRequest(ctx, storedb.InsertSessionRequestParams{
			ID: requestID, SessionID: sessionID, TurnID: &turnID,
			Kind: string(api.Permission), Status: string(api.Pending), Prompt: "allow this?", CreatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		agentID := "bridge-failure-worker"
		_, err = queries.UpsertSubagent(ctx, storedb.UpsertSubagentParams{
			SessionID: sessionID, ID: agentID, TurnID: &turnID, DisplayName: "Bridge Failure Worker",
			Status: string(api.SubagentStatusActive), StartedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		scheduleID := uuid.New()
		_, err = queries.CreateChatSchedule(ctx, storedb.CreateChatScheduleParams{
			ID: scheduleID, SessionID: sessionID, DisplayName: "stop after bridge failure",
			Prompt: "must not run", CronExpression: "0 0 1 1 *", Timezone: "UTC",
			Status: string(api.ChatScheduleStatusActive), CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		scheduleContext, stopSchedules := context.WithCancel(ctx)
		schedulesFinished := make(chan error, 1)
		go func() { schedulesFinished <- service.RunSchedules(scheduleContext) }()
		DeferCleanup(func() {
			stopSchedules()
			Eventually(schedulesFinished).WithTimeout(lifecycleBudget.Within).Should(Receive(Succeed()))
		})
		patience.Await(GinkgoT(), "schedule runtime loads the active job", lifecycleBudget,
			func() int {
				snapshot, snapshotErr := scheduleStore.Snapshot(ctx)
				if snapshotErr != nil {
					return 0
				}
				return len(snapshot.Jobs)
			},
			func(jobs int) bool { return jobs == 1 })
		var closeAttempts atomic.Int32
		var closeHadDeadline atomic.Bool
		closeSessionWithContext = func(closeContext context.Context) error {
			closeAttempts.Add(1)
			_, hasDeadline := closeContext.Deadline()
			closeHadDeadline.Store(hasDeadline)
			<-closeContext.Done()
			return closeContext.Err()
		}
		commitObserved := storeWithAmbiguousCommit.armAndObserve()
		failureID := uuid.NewString()
		events <- copilotadapter.BridgeEvent{
			ID: failureID, Kind: copilotadapter.BridgeEventFailed, OccurredAt: now.Add(time.Second),
		}
		Eventually(commitObserved).WithTimeout(lifecycleBudget.Within).Should(BeClosed())

		// End races the projector's retry after the failed state committed but
		// its acknowledgement was lost. It must detach ownership before Close,
		// canceling that retry and avoiding a second close of the broken handle.
		ended, err := client.EndSessionWithResponse(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ended.StatusCode()).To(Equal(http.StatusOK), string(ended.Body))
		Expect(ended.JSON200.Status).To(Equal(api.SessionStatusFailed))

		failed := patience.Await(GinkgoT(), "bridge failure terminalizes the session", lifecycleBudget,
			func() storedb.Session {
				row, lookupErr := queries.GetSession(ctx, sessionID)
				if lookupErr != nil {
					return storedb.Session{}
				}
				return row
			},
			func(row storedb.Session) bool { return row.Status == string(api.SessionStatusFailed) })
		Expect(failed.EndedAt.Valid).To(BeTrue())
		turn, err := queries.GetTurn(ctx, turnID)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn.Status).To(Equal(string(api.TurnStatusAborted)))
		Expect(turn.CompletedAt.Valid).To(BeTrue())
		request, err := queries.GetSessionRequest(ctx, requestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(request.Status).To(Equal(string(api.Denied)))
		Expect(request.DeliveryStatus).To(Equal("abandoned"))
		subagent, err := queries.GetSubagent(ctx, storedb.GetSubagentParams{SessionID: sessionID, ID: agentID})
		Expect(err).NotTo(HaveOccurred())
		Expect(subagent.Status).To(Equal(string(api.SubagentStatusFailed)))
		paused, err := queries.GetChatSchedule(ctx, scheduleID)
		Expect(err).NotTo(HaveOccurred())
		Expect(paused.Status).To(Equal(string(api.ChatScheduleStatusPaused)))
		patience.Await(GinkgoT(), "schedule runtime observes the bridge failure", lifecycleBudget,
			func() int {
				snapshot, snapshotErr := scheduleStore.Snapshot(ctx)
				if snapshotErr != nil {
					return -1
				}
				return len(snapshot.Jobs)
			},
			func(jobs int) bool { return jobs == 0 })
		Eventually(closeAttempts.Load).WithTimeout(lifecycleBudget.Within).Should(Equal(int32(1)))
		Expect(closeHadDeadline.Load()).To(BeTrue())
		Expect(closeAttempts.Load()).To(Equal(int32(1)), "a terminal broken handle must no longer be live")
	})

	It("reloads schedules when quarantine loses the commit acknowledgement", func() {
		closed := make(chan struct{})
		closeSession = func() error {
			close(closed)
			return nil
		}
		now := time.Now().UTC()
		scheduleID := uuid.New()
		_, err := queries.CreateChatSchedule(ctx, storedb.CreateChatScheduleParams{
			ID: scheduleID, SessionID: sessionID, DisplayName: "stop after quarantine",
			Prompt: "must not run", CronExpression: "0 0 1 1 *", Timezone: "UTC",
			Status: string(api.ChatScheduleStatusActive), CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		scheduleContext, stopSchedules := context.WithCancel(ctx)
		schedulesFinished := make(chan error, 1)
		go func() { schedulesFinished <- service.RunSchedules(scheduleContext) }()
		DeferCleanup(func() {
			stopSchedules()
			Eventually(schedulesFinished).WithTimeout(lifecycleBudget.Within).Should(Receive(Succeed()))
		})
		patience.Await(GinkgoT(), "schedule runtime loads the active job", lifecycleBudget,
			func() int {
				snapshot, snapshotErr := scheduleStore.Snapshot(ctx)
				if snapshotErr != nil {
					return 0
				}
				return len(snapshot.Jobs)
			},
			func(jobs int) bool { return jobs == 1 })

		reuseWorktree = func(_ context.Context, _ string, _ string) (copilotadapter.PreparedWorktree, error) {
			return copilotadapter.PreparedWorktree{}, copilotadapter.ErrInvalidWorktree
		}
		storeWithAmbiguousCommit.arm()
		response, err := client.ListWorktreesWithResponse(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusInternalServerError), string(response.Body))
		Eventually(closed).WithTimeout(lifecycleBudget.Within).Should(BeClosed())
		paused, err := queries.GetChatSchedule(ctx, scheduleID)
		Expect(err).NotTo(HaveOccurred())
		Expect(paused.Status).To(Equal(string(api.ChatScheduleStatusPaused)))
		patience.Await(GinkgoT(), "schedule runtime observes the ambiguous committed pause", lifecycleBudget,
			func() int {
				snapshot, snapshotErr := scheduleStore.Snapshot(ctx)
				if snapshotErr != nil {
					return -1
				}
				return len(snapshot.Jobs)
			},
			func(jobs int) bool { return jobs == 0 })
	})

	It("closes and finalizes a live session when its worktree is quarantined", func() {
		sent := make(chan uuid.UUID, 1)
		releaseSend := make(chan struct{})
		sendPrompt = func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
			sent <- prompt.TurnID
			<-releaseSend
			return copilotadapter.BridgePromptDeliveryAccepted, nil
		}
		type promptResult struct {
			response *api.SubmitPromptResponse
			err      error
		}
		prompted := make(chan promptResult, 1)
		go func() {
			response, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
				IdempotencyKey: uuid.New(), Text: "still running", Mode: api.Queue,
			})
			prompted <- promptResult{response: response, err: err}
		}()
		var turnID uuid.UUID
		Eventually(sent).WithTimeout(lifecycleBudget.Within).Should(Receive(&turnID))
		requestID := uuid.New()
		_, err := queries.InsertSessionRequest(ctx, storedb.InsertSessionRequestParams{
			ID: requestID, SessionID: sessionID, TurnID: &turnID,
			Kind: string(api.Permission), Status: string(api.Pending), Prompt: "allow this?",
			CreatedAt: time.Now().UTC(),
		})
		Expect(err).NotTo(HaveOccurred())
		agentID := "active-worker"
		now := time.Now().UTC()
		_, err = queries.UpsertSubagent(ctx, storedb.UpsertSubagentParams{
			SessionID: sessionID, ID: agentID, TurnID: &turnID, DisplayName: "Active Worker",
			Status: string(api.SubagentStatusActive), StartedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		scheduleID := uuid.New()
		_, err = queries.CreateChatSchedule(ctx, storedb.CreateChatScheduleParams{
			ID: scheduleID, SessionID: sessionID, DisplayName: "pause with missing worktree",
			Prompt: "must not run", CronExpression: "0 0 1 1 *", Timezone: "UTC",
			Status: string(api.ChatScheduleStatusActive), CreatedAt: now, UpdatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		scheduleContext, stopSchedules := context.WithCancel(ctx)
		schedulesFinished := make(chan error, 1)
		go func() { schedulesFinished <- service.RunSchedules(scheduleContext) }()
		DeferCleanup(func() {
			stopSchedules()
			Eventually(schedulesFinished).WithTimeout(lifecycleBudget.Within).Should(Receive(Succeed()))
		})
		patience.Await(GinkgoT(), "schedule runtime loads before direct worktree quarantine", lifecycleBudget,
			func() int {
				snapshot, snapshotErr := scheduleStore.Snapshot(ctx)
				if snapshotErr != nil {
					return 0
				}
				return len(snapshot.Jobs)
			},
			func(jobs int) bool { return jobs == 1 })
		before, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		worktreeID := before.WorktreeID

		closed := make(chan struct{})
		closeSession = func() error {
			close(closed)
			return nil
		}
		reuseWorktree = func(_ context.Context, _ string, _ string) (copilotadapter.PreparedWorktree, error) {
			return copilotadapter.PreparedWorktree{}, copilotadapter.ErrInvalidWorktree
		}
		type worktreeResult struct {
			response *api.GetWorktreeResponse
			err      error
		}
		fetched := make(chan worktreeResult, 1)
		go func() {
			response, err := client.GetWorktreeWithResponse(ctx, worktreeID)
			fetched <- worktreeResult{response: response, err: err}
		}()
		Consistently(closed, 150*time.Millisecond, 10*time.Millisecond).ShouldNot(Receive())
		close(releaseSend)
		var promptOutcome promptResult
		Eventually(prompted).WithTimeout(lifecycleBudget.Within).Should(Receive(&promptOutcome))
		Expect(promptOutcome.err).NotTo(HaveOccurred())
		Expect(promptOutcome.response.StatusCode()).To(Equal(http.StatusAccepted), string(promptOutcome.response.Body))
		var worktreeOutcome worktreeResult
		Eventually(fetched).WithTimeout(lifecycleBudget.Within).Should(Receive(&worktreeOutcome))
		Expect(worktreeOutcome.err).NotTo(HaveOccurred())
		Expect(worktreeOutcome.response.StatusCode()).To(Equal(http.StatusOK), string(worktreeOutcome.response.Body))
		Expect(worktreeOutcome.response.JSON200.Id).NotTo(BeNil())
		Expect(*worktreeOutcome.response.JSON200.Id).To(Equal(worktreeID))
		Expect(worktreeOutcome.response.JSON200.State).To(Equal(api.WorktreeStateMissing))
		Expect(worktreeOutcome.response.JSON200.HeadSha).NotTo(BeNil())
		Expect(*worktreeOutcome.response.JSON200.HeadSha).To(BeEmpty())
		Expect(worktreeOutcome.response.JSON200.Branch).NotTo(BeNil())
		Expect(*worktreeOutcome.response.JSON200.Branch).To(BeEmpty())
		Expect(worktreeOutcome.response.JSON200.Clean).NotTo(BeNil())
		Expect(*worktreeOutcome.response.JSON200.Clean).To(BeFalse())
		Eventually(closed).WithTimeout(lifecycleBudget.Within).Should(BeClosed())

		session, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(session.Status).To(Equal(string(api.SessionStatusFailed)))
		Expect(session.EndedAt.Valid).To(BeTrue())
		turn, err := queries.GetTurn(ctx, turnID)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn.Status).To(Equal("aborted"))
		Expect(turn.CompletedAt.Valid).To(BeTrue())
		pendingRequest, err := queries.GetSessionRequest(ctx, requestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(pendingRequest.Status).To(Equal(string(api.Denied)))
		Expect(pendingRequest.DeliveryStatus).To(Equal("abandoned"))
		Expect(pendingRequest.ResolvedAt.Valid).To(BeTrue())
		subagent, err := queries.GetSubagent(ctx, storedb.GetSubagentParams{SessionID: sessionID, ID: agentID})
		Expect(err).NotTo(HaveOccurred())
		Expect(subagent.Status).To(Equal(string(api.SubagentStatusFailed)))
		Expect(subagent.CompletedAt.Valid).To(BeTrue())
		paused, err := queries.GetChatSchedule(ctx, scheduleID)
		Expect(err).NotTo(HaveOccurred())
		Expect(paused.Status).To(Equal(string(api.ChatScheduleStatusPaused)))
		patience.Await(GinkgoT(), "direct worktree quarantine reloads schedules", lifecycleBudget,
			func() int {
				snapshot, snapshotErr := scheduleStore.Snapshot(ctx)
				if snapshotErr != nil {
					return -1
				}
				return len(snapshot.Jobs)
			},
			func(jobs int) bool { return jobs == 0 })

		persistedEvents, err := queries.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
			SessionID: sessionID, RowLimit: 100,
		})
		Expect(err).NotTo(HaveOccurred())
		var turnVersioned, requestVersioned, subagentVersioned, sessionVersioned bool
		for _, event := range persistedEvents {
			switch {
			case event.Kind == string(api.SessionEventKindTurnCompleted) && event.TurnID != nil && *event.TurnID == turnID:
				version, versionErr := queries.GetTurnEventVersion(ctx, storedb.GetTurnEventVersionParams{
					SessionID: sessionID, EventSeq: event.Seq,
				})
				turnVersioned = versionErr == nil && version.Status == string(api.TurnStatusAborted)
			case event.Kind == string(api.SessionEventKindRequestResolved) && event.RequestID != nil && *event.RequestID == requestID:
				version, versionErr := queries.GetRequestEventVersion(ctx, storedb.GetRequestEventVersionParams{
					SessionID: sessionID, EventSeq: event.Seq,
				})
				requestVersioned = versionErr == nil && version.Status == string(api.Denied)
			case event.Kind == string(api.SessionEventKindSubagentUpdated) && event.SubagentID.String == agentID:
				version, versionErr := queries.GetSubagentEventVersion(ctx, storedb.GetSubagentEventVersionParams{
					SessionID: sessionID, EventSeq: event.Seq,
				})
				subagentVersioned = versionErr == nil && version.Status == string(api.SubagentStatusFailed)
			case event.Kind == string(api.SessionEventKindSessionUpdated):
				version, versionErr := queries.GetSessionEventVersion(ctx, storedb.GetSessionEventVersionParams{
					SessionID: sessionID, EventSeq: event.Seq,
				})
				sessionVersioned = sessionVersioned || versionErr == nil && version.Status == string(api.SessionStatusFailed)
			}
		}
		Expect(turnVersioned).To(BeTrue())
		Expect(requestVersioned).To(BeTrue())
		Expect(subagentVersioned).To(BeTrue())
		Expect(sessionVersioned).To(BeTrue())
	})

	DescribeTable("classifies worktree changes failures without quarantining transient errors",
		func(failureStage string, operationErr error, expectedStatus int, quarantined bool) {
			before, err := queries.GetSession(ctx, sessionID)
			Expect(err).NotTo(HaveOccurred())
			var closes, changesCalls atomic.Int32
			closeSession = func() error {
				closes.Add(1)
				return nil
			}
			if failureStage == "validation" {
				reuseWorktree = func(_ context.Context, _ string, _ string) (copilotadapter.PreparedWorktree, error) {
					return copilotadapter.PreparedWorktree{}, operationErr
				}
			} else {
				readWorktreeChanges = func(_ context.Context, _ string) (copilotadapter.WorktreeChanges, error) {
					changesCalls.Add(1)
					return copilotadapter.WorktreeChanges{}, operationErr
				}
			}

			response, err := client.GetWorktreeChangesWithResponse(ctx, before.WorktreeID)
			Expect(err).NotTo(HaveOccurred())
			Expect(response.StatusCode()).To(Equal(expectedStatus), string(response.Body))
			if expectedStatus == http.StatusNotFound {
				Expect(response.JSON404.Code).To(Equal("worktree_not_found"))
			}
			after, err := queries.GetSession(ctx, sessionID)
			Expect(err).NotTo(HaveOccurred())
			if quarantined {
				Expect(after.Status).To(Equal(string(api.SessionStatusFailed)))
				Expect(after.EndedAt.Valid).To(BeTrue())
				Expect(closes.Load()).To(Equal(int32(1)))
			} else {
				Expect(after.Status).To(Equal(before.Status))
				Expect(after.EndedAt).To(Equal(before.EndedAt))
				Expect(closes.Load()).To(BeZero())
			}
			if failureStage == "validation" {
				Expect(changesCalls.Load()).To(BeZero())
			} else {
				Expect(changesCalls.Load()).To(Equal(int32(1)))
			}
		},
		Entry("when validation proves the path is missing", "validation", copilotadapter.ErrInvalidWorktree, http.StatusNotFound, true),
		Entry("when git proves the path disappeared during capture", "changes", copilotadapter.ErrInvalidWorktree, http.StatusNotFound, true),
		Entry("when validation fails transiently", "validation", context.DeadlineExceeded, http.StatusInternalServerError, false),
		Entry("when change capture fails transiently", "changes", context.DeadlineExceeded, http.StatusInternalServerError, false),
	)

	It("finishes definitive worktree quarantine after the request is canceled", func() {
		before, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		var closes atomic.Int32
		closeSession = func() error {
			closes.Add(1)
			return nil
		}
		reuseWorktree = func(_ context.Context, _ string, _ string) (copilotadapter.PreparedWorktree, error) {
			return copilotadapter.PreparedWorktree{}, copilotadapter.ErrInvalidWorktree
		}
		requestCtx, cancelRequest := context.WithCancel(ctx)
		DeferCleanup(cancelRequest)
		storeWithAmbiguousCommit.armWorktreeSessionListCancellation(cancelRequest)

		response, err := service.GetWorktreeChanges(requestCtx, api.GetWorktreeChangesRequestObject{
			WorktreeId: before.WorktreeID,
		})
		Expect(response).To(BeNil())
		Expect(err).To(MatchError("worktree_not_found: the worktree path is missing"))
		Expect(requestCtx.Err()).To(MatchError(context.Canceled))
		after, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.Status).To(Equal(string(api.SessionStatusFailed)))
		Expect(after.EndedAt.Valid).To(BeTrue())
		Expect(closes.Load()).To(Equal(int32(1)))
	})

	It("starts quarantine with a fresh transition budget after waiting on a session mutation", func() {
		before, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		sendEntered := make(chan struct{})
		releaseSend := make(chan struct{})
		var enteredOnce, releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(releaseSend) }) }
		DeferCleanup(release)
		sendPrompt = func(_ context.Context, _ copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
			enteredOnce.Do(func() { close(sendEntered) })
			<-releaseSend
			return copilotadapter.BridgePromptDeliveryAccepted, nil
		}
		type promptResult struct {
			response api.SubmitPromptResponseObject
			err      error
		}
		promptFinished := make(chan promptResult, 1)
		go func() {
			response, promptErr := service.SubmitPrompt(context.Background(), api.SubmitPromptRequestObject{
				SessionId: sessionID,
				Body: &api.SubmitPromptJSONRequestBody{
					IdempotencyKey: uuid.New(), Text: "hold the session mutation", Mode: api.Queue,
				},
			})
			promptFinished <- promptResult{response: response, err: promptErr}
		}()
		Eventually(sendEntered).Should(BeClosed())

		reuseWorktree = func(_ context.Context, _ string, _ string) (copilotadapter.PreparedWorktree, error) {
			return copilotadapter.PreparedWorktree{}, copilotadapter.ErrInvalidWorktree
		}
		sessionsListed := storeWithAmbiguousCommit.observeWorktreeSessionList()
		type changesResult struct {
			response api.GetWorktreeChangesResponseObject
			err      error
		}
		changesFinished := make(chan changesResult, 1)
		go func() {
			response, changesErr := service.GetWorktreeChanges(context.Background(), api.GetWorktreeChangesRequestObject{
				WorktreeId: before.WorktreeID,
			})
			changesFinished <- changesResult{response: response, err: changesErr}
		}()
		Eventually(sessionsListed).Should(BeClosed())
		Consistently(changesFinished, 1100*time.Millisecond, 10*time.Millisecond).ShouldNot(Receive(),
			"quarantine must remain fenced while the session mutation outlives its discovery budget")

		release()
		var promptOutcome promptResult
		Eventually(promptFinished).WithTimeout(lifecycleBudget.Within).Should(Receive(&promptOutcome))
		Expect(promptOutcome.err).NotTo(HaveOccurred())
		var changesOutcome changesResult
		Eventually(changesFinished).WithTimeout(lifecycleBudget.Within).Should(Receive(&changesOutcome))
		Expect(changesOutcome.response).To(BeNil())
		Expect(changesOutcome.err).To(MatchError("worktree_not_found: the worktree path is missing"))
		after, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.Status).To(Equal(string(api.SessionStatusFailed)))
		Expect(after.EndedAt.Valid).To(BeTrue())
	})

	It("quarantines a path that disappears between validation and the worktree snapshot", func() {
		before, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		var closes atomic.Int32
		closeSession = func() error {
			closes.Add(1)
			return nil
		}
		inspectWorktree = func(_ context.Context, _ string) (copilotadapter.WorktreeSnapshot, error) {
			return copilotadapter.WorktreeSnapshot{State: string(api.WorktreeStateMissing)}, nil
		}

		response, err := client.GetWorktreeWithResponse(ctx, before.WorktreeID)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusOK), string(response.Body))
		Expect(response.JSON200.State).To(Equal(api.WorktreeStateMissing))
		after, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.Status).To(Equal(string(api.SessionStatusFailed)))
		Expect(after.EndedAt.Valid).To(BeTrue())
		Expect(closes.Load()).To(Equal(int32(1)))
	})

	It("quarantines a worktree that disappears while its terminal is starting", func() {
		before, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		var closes, creates atomic.Int32
		closeSession = func() error {
			closes.Add(1)
			return nil
		}
		createTerminal = func(_ context.Context, _ copilotadapter.TerminalSpec) (copilotadapter.TerminalSnapshot, error) {
			creates.Add(1)
			return copilotadapter.TerminalSnapshot{}, os.ErrNotExist
		}

		response, err := client.CreateTerminalWithResponse(ctx, before.WorktreeID, api.CreateTerminalJSONRequestBody{
			Rows: 24, Columns: 80,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusNotFound), string(response.Body))
		Expect(response.JSON404.Code).To(Equal("worktree_not_found"))
		after, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.Status).To(Equal(string(api.SessionStatusFailed)))
		Expect(after.EndedAt.Valid).To(BeTrue())
		Expect(creates.Load()).To(Equal(int32(1)))
		Expect(closes.Load()).To(Equal(int32(1)))
	})

	It("fences quarantine against a same-path session activation and captures the new live handle", func() {
		before, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		creationOwnsPath := make(chan struct{})
		releaseCreation := make(chan struct{})
		var pathInvalid atomic.Bool
		reuseWorktree = func(_ context.Context, _ string, path string) (copilotadapter.PreparedWorktree, error) {
			if pathInvalid.Load() {
				return copilotadapter.PreparedWorktree{}, copilotadapter.ErrInvalidWorktree
			}
			close(creationOwnsPath)
			<-releaseCreation
			return copilotadapter.PreparedWorktree{
				Repository: copilotadapter.Repository{ID: "repo", DisplayName: "Repo", Root: "/tmp/work", DefaultRef: "HEAD"},
				Path:       path, BaseRef: "HEAD",
			}, nil
		}
		createBridgeSession = func(_ copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
			pathInvalid.Store(true)
			return copilotadapter.BridgeSession{
				Close: func(ctx context.Context) error { return closeSessionWithContext(ctx) },
			}, nil
		}
		var closes atomic.Int32
		closeSession = func() error {
			closes.Add(1)
			return nil
		}
		type createResult struct {
			response *api.CreateSessionResponse
			err      error
		}
		created := make(chan createResult, 1)
		go func() {
			response, createErr := client.CreateSessionWithResponse(ctx, currentWorktreeSessionBody("gpt-5", "repo"))
			created <- createResult{response: response, err: createErr}
		}()
		Eventually(creationOwnsPath).WithTimeout(lifecycleBudget.Within).Should(BeClosed())

		type changesResult struct {
			response *api.GetWorktreeChangesResponse
			err      error
		}
		changes := make(chan changesResult, 1)
		go func() {
			response, changesErr := client.GetWorktreeChangesWithResponse(ctx, before.WorktreeID)
			changes <- changesResult{response: response, err: changesErr}
		}()
		Consistently(changes, 100*time.Millisecond, 10*time.Millisecond).ShouldNot(Receive())
		close(releaseCreation)

		var createdOutcome createResult
		Eventually(created).WithTimeout(lifecycleBudget.Within).Should(Receive(&createdOutcome))
		Expect(createdOutcome.err).NotTo(HaveOccurred())
		Expect(createdOutcome.response.StatusCode()).To(Equal(http.StatusCreated), string(createdOutcome.response.Body))
		newSessionID := *createdOutcome.response.JSON201.Id
		var changesOutcome changesResult
		Eventually(changes).WithTimeout(lifecycleBudget.Within).Should(Receive(&changesOutcome))
		Expect(changesOutcome.err).NotTo(HaveOccurred())
		Expect(changesOutcome.response.StatusCode()).To(Equal(http.StatusNotFound), string(changesOutcome.response.Body))
		Expect(changesOutcome.response.JSON404.Code).To(Equal("worktree_not_found"))

		for _, identifier := range []uuid.UUID{sessionID, newSessionID} {
			row, lookupErr := queries.GetSession(ctx, identifier)
			Expect(lookupErr).NotTo(HaveOccurred())
			Expect(row.WorktreeID).To(Equal(before.WorktreeID))
			Expect(row.Status).To(Equal(string(api.SessionStatusFailed)))
			Expect(row.EndedAt.Valid).To(BeTrue())
		}
		Expect(closes.Load()).To(Equal(int32(2)), "quarantine must close both handles after the activation fence releases")
	})

	It("blocks same-path session creation behind quarantine and never activates the missing path", func() {
		before, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		quarantineOwnsPath := make(chan struct{})
		releaseQuarantine := make(chan struct{})
		var validations atomic.Int32
		reuseWorktree = func(_ context.Context, _ string, _ string) (copilotadapter.PreparedWorktree, error) {
			if validations.Add(1) == 1 {
				close(quarantineOwnsPath)
				<-releaseQuarantine
			}
			return copilotadapter.PreparedWorktree{}, copilotadapter.ErrInvalidWorktree
		}
		var closes atomic.Int32
		closeSession = func() error {
			closes.Add(1)
			return nil
		}
		baselineSDKCreates := createSessionCalls.Load()
		type changesResult struct {
			response *api.GetWorktreeChangesResponse
			err      error
		}
		changes := make(chan changesResult, 1)
		go func() {
			response, changesErr := client.GetWorktreeChangesWithResponse(ctx, before.WorktreeID)
			changes <- changesResult{response: response, err: changesErr}
		}()
		Eventually(quarantineOwnsPath).WithTimeout(lifecycleBudget.Within).Should(BeClosed())

		type createResult struct {
			response *api.CreateSessionResponse
			err      error
		}
		created := make(chan createResult, 1)
		go func() {
			response, createErr := client.CreateSessionWithResponse(ctx, currentWorktreeSessionBody("gpt-5", "repo"))
			created <- createResult{response: response, err: createErr}
		}()
		Consistently(created, 100*time.Millisecond, 10*time.Millisecond).ShouldNot(Receive())
		Expect(createSessionCalls.Load()).To(Equal(baselineSDKCreates))
		close(releaseQuarantine)

		var changesOutcome changesResult
		Eventually(changes).WithTimeout(lifecycleBudget.Within).Should(Receive(&changesOutcome))
		Expect(changesOutcome.err).NotTo(HaveOccurred())
		Expect(changesOutcome.response.StatusCode()).To(Equal(http.StatusNotFound), string(changesOutcome.response.Body))
		var createdOutcome createResult
		Eventually(created).WithTimeout(lifecycleBudget.Within).Should(Receive(&createdOutcome))
		Expect(createdOutcome.err).NotTo(HaveOccurred())
		Expect(createdOutcome.response.StatusCode()).To(Equal(http.StatusNotFound), string(createdOutcome.response.Body))
		Expect(createdOutcome.response.JSON404.Code).To(Equal("worktree_not_found"))
		Expect(createSessionCalls.Load()).To(Equal(baselineSDKCreates), "a missing path must never cross the SDK boundary")
		count, err := queries.CountWorktreeSessions(ctx, before.WorktreeID)
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(int64(1)))
		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Status).To(Equal(string(api.SessionStatusFailed)))
		Expect(closes.Load()).To(Equal(int32(1)))
	})

	It("does not quarantine a direct worktree read after a transient git failure", func() {
		before, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		var closes atomic.Int32
		closeSession = func() error {
			closes.Add(1)
			return nil
		}
		inspectWorktree = func(_ context.Context, _ string) (copilotadapter.WorktreeSnapshot, error) {
			return copilotadapter.WorktreeSnapshot{}, context.DeadlineExceeded
		}

		response, err := client.GetWorktreeWithResponse(ctx, before.WorktreeID)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusInternalServerError), string(response.Body))
		after, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.Status).To(Equal(before.Status))
		Expect(after.EndedAt).To(Equal(before.EndedAt))
		Expect(closes.Load()).To(BeZero())
	})

	It("retries closing a quarantined session after the bridge close fails", func() {
		closeFailure := errors.New("bridge close failed")
		var closeAttempts atomic.Int32
		closeSession = func() error {
			if closeAttempts.Add(1) == 1 {
				return closeFailure
			}
			return nil
		}
		reuseWorktree = func(_ context.Context, _ string, _ string) (copilotadapter.PreparedWorktree, error) {
			return copilotadapter.PreparedWorktree{}, copilotadapter.ErrInvalidWorktree
		}

		first, err := client.ListWorktreesWithResponse(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.StatusCode()).To(Equal(http.StatusInternalServerError), string(first.Body))
		second, err := client.ListWorktreesWithResponse(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(second.StatusCode()).To(Equal(http.StatusOK), string(second.Body))
		Expect(closeAttempts.Load()).To(Equal(int32(2)))
	})

	It("commits session metadata and its versioned event together", func() {
		displayName := "Renamed session"
		response, err := client.UpdateSessionWithResponse(ctx, sessionID, api.UpdateSessionJSONRequestBody{
			DisplayName: &displayName,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusOK), string(response.Body))
		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		version, found := latestSessionVersion()
		Expect(found).To(BeTrue())
		Expect(version.ID).To(Equal(sessionID))
		Expect(version.DisplayName).To(Equal(displayName))
		Expect(version.Model).To(Equal(persisted.Model))
		Expect(version.UpdatedAt).To(Equal(persisted.UpdatedAt))
	})

	It("keeps the switched bridge model when metadata committed ambiguously", func() {
		nextModel := "gpt-5.1"
		modelChanges := make(chan string, 2)
		setModel = func(_ context.Context, model string) error {
			modelChanges <- model
			return nil
		}
		storeWithAmbiguousCommit.arm()

		response, err := client.UpdateSessionWithResponse(ctx, sessionID, api.UpdateSessionJSONRequestBody{Model: &nextModel})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusOK), string(response.Body))
		Eventually(modelChanges).WithTimeout(lifecycleBudget.Within).Should(Receive(Equal(nextModel)))
		Consistently(modelChanges, 150*time.Millisecond, 10*time.Millisecond).ShouldNot(Receive())
		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Model).To(Equal(nextModel))
		version, found := latestSessionVersion()
		Expect(found).To(BeTrue())
		Expect(version.Model).To(Equal(nextModel))
		Expect(version.UpdatedAt).To(Equal(persisted.UpdatedAt))
	})

	It("restores the bridge model when the metadata transaction rolls back after writing", func() {
		expectModelTransactionRollback(storeWithAmbiguousCommit.armRollback)
	})

	It("detaches a switched bridge when an ambiguous metadata commit cannot be reconciled", func() {
		nextModel := "gpt-5.1"
		modelChanges := make(chan string, 2)
		setModel = func(_ context.Context, model string) error {
			modelChanges <- model
			return nil
		}
		var closeAttempts atomic.Int32
		closeSession = func() error {
			closeAttempts.Add(1)
			return nil
		}
		storeWithAmbiguousCommit.arm()
		storeWithAmbiguousCommit.armReconciliationReadFailure()

		response, err := client.UpdateSessionWithResponse(ctx, sessionID, api.UpdateSessionJSONRequestBody{Model: &nextModel})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode()).To(Equal(http.StatusInternalServerError), string(response.Body))
		Expect(modelChanges).To(Receive(Equal(nextModel)))
		Consistently(modelChanges, 150*time.Millisecond, 10*time.Millisecond).ShouldNot(Receive(),
			"an unknown commit cannot safely restore either model")
		Expect(closeAttempts.Load()).To(Equal(int32(1)))

		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Model).To(Equal(nextModel), "the injected commit did persist despite its lost acknowledgement")
		version, found := latestSessionVersion()
		Expect(found).To(BeTrue())
		Expect(version.Model).To(Equal(nextModel))
		prompted, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: "must not use an unconfirmed model", Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(prompted.StatusCode()).To(Equal(http.StatusConflict), string(prompted.Body))
		Expect(prompted.JSON409.Code).To(Equal("session_not_live"))
		Expect(closeAttempts.Load()).To(Equal(int32(1)))
	})

	It("restores the previous bridge model after an ambiguous model-switch error", func() {
		nextModel := "gpt-5.1"
		modelChanges := make(chan modelChangeCall, 2)
		bridgeModel := "gpt-5"
		updateContext, cancelUpdate := context.WithCancel(ctx)
		setModel = func(callContext context.Context, model string) error {
			bridgeModel = model
			if model == nextModel {
				cancelUpdate()
				modelChanges <- modelChangeCall{model: model, contextErr: callContext.Err()}
				return callContext.Err()
			}
			modelChanges <- modelChangeCall{model: model, contextErr: callContext.Err()}
			return nil
		}

		_, err := service.UpdateSession(updateContext, api.UpdateSessionRequestObject{
			SessionId: sessionID, Body: &api.UpdateSessionJSONRequestBody{Model: &nextModel},
		})
		Expect(err).To(HaveOccurred())
		Expect(<-modelChanges).To(Equal(modelChangeCall{model: nextModel, contextErr: context.Canceled}))
		Expect(<-modelChanges).To(Equal(modelChangeCall{model: "gpt-5"}),
			"rollback must use a fresh bounded context")
		Expect(bridgeModel).To(Equal("gpt-5"))
		persisted, lookupErr := queries.GetSession(ctx, sessionID)
		Expect(lookupErr).NotTo(HaveOccurred())
		Expect(persisted.Model).To(Equal("gpt-5"))
		_, found := latestSessionVersion()
		Expect(found).To(BeFalse())
	})

	It("detaches a session whose failed model switch cannot be rolled back", func() {
		nextModel := "gpt-5.1"
		setModel = func(_ context.Context, model string) error {
			if model == nextModel {
				return context.DeadlineExceeded
			}
			return errForcedRollback
		}
		var closeAttempts atomic.Int32
		closeSession = func() error {
			closeAttempts.Add(1)
			return errors.New("simulated close failure")
		}

		_, err := service.UpdateSession(ctx, api.UpdateSessionRequestObject{
			SessionId: sessionID, Body: &api.UpdateSessionJSONRequestBody{Model: &nextModel},
		})
		Expect(err).To(HaveOccurred())
		Eventually(closeAttempts.Load).WithTimeout(lifecycleBudget.Within).Should(Equal(int32(1)))
		prompted, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{
			IdempotencyKey: uuid.New(), Text: "must not use an unverifiable model", Mode: api.Queue,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(prompted.StatusCode()).To(Equal(http.StatusConflict), string(prompted.Body))
		Expect(prompted.JSON409.Code).To(Equal("session_not_live"))
		Expect(closeAttempts.Load()).To(Equal(int32(1)))
	})

	It("rolls back both metadata and the bridge model when the event write fails", func() {
		expectModelTransactionRollback(storeWithEventFailure.arm)
	})

	It("uses a fresh bounded context to restore a model after request cancellation", func() {
		nextModel := "gpt-5.1"
		modelChanges := make(chan modelChangeCall, 2)
		updateContext, cancelUpdate := context.WithCancel(ctx)
		setModel = func(callContext context.Context, model string) error {
			if model == nextModel {
				cancelUpdate()
			}
			modelChanges <- modelChangeCall{model: model, contextErr: callContext.Err()}
			return nil
		}

		_, err := service.UpdateSession(updateContext, api.UpdateSessionRequestObject{
			SessionId: sessionID, Body: &api.UpdateSessionJSONRequestBody{Model: &nextModel},
		})
		Expect(err).To(HaveOccurred())
		Expect(<-modelChanges).To(Equal(modelChangeCall{model: nextModel, contextErr: context.Canceled}))
		Expect(<-modelChanges).To(Equal(modelChangeCall{model: "gpt-5"}))
		persisted, lookupErr := queries.GetSession(ctx, sessionID)
		Expect(lookupErr).NotTo(HaveOccurred())
		Expect(persisted.Model).To(Equal("gpt-5"))
	})

	It("serializes a session failure behind an in-flight model switch", func() {
		modelChanges := make(chan string, 2)
		releaseModelSwitch := make(chan struct{})
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(releaseModelSwitch) }) }
		DeferCleanup(release)
		nextModel := "gpt-5.1"
		setModel = func(_ context.Context, model string) error {
			modelChanges <- model
			if model == nextModel {
				<-releaseModelSwitch
			}
			return nil
		}
		type updateResult struct {
			response *api.UpdateSessionResponse
			err      error
		}
		results := make(chan updateResult, 1)
		go func() {
			response, err := client.UpdateSessionWithResponse(ctx, sessionID, api.UpdateSessionJSONRequestBody{Model: &nextModel})
			results <- updateResult{response: response, err: err}
		}()

		Eventually(modelChanges).WithTimeout(lifecycleBudget.Within).Should(Receive(Equal(nextModel)))
		events <- copilotadapter.BridgeEvent{
			ID: uuid.NewString(), Kind: copilotadapter.BridgeEventFailed, OccurredAt: time.Now().UTC(),
		}
		Consistently(func() string {
			row, err := queries.GetSession(ctx, sessionID)
			if err != nil {
				return "lookup failed"
			}
			return row.Status
		}, 150*time.Millisecond, 10*time.Millisecond).Should(Equal(string(api.SessionStatusIdle)))
		release()

		var result updateResult
		Eventually(results).WithTimeout(lifecycleBudget.Within).Should(Receive(&result))
		Expect(result.err).NotTo(HaveOccurred())
		Expect(result.response).NotTo(BeNil())
		Expect(result.response.StatusCode()).To(Equal(http.StatusOK), string(result.response.Body))
		failed := patience.Await(GinkgoT(), "session failure lands after the model switch", lifecycleBudget,
			func() storedb.Session {
				row, err := queries.GetSession(ctx, sessionID)
				if err != nil {
					return storedb.Session{}
				}
				return row
			},
			func(row storedb.Session) bool { return row.Status == string(api.SessionStatusFailed) })
		Expect(failed.Model).To(Equal(nextModel))
		Consistently(modelChanges, 150*time.Millisecond, 10*time.Millisecond).ShouldNot(Receive(),
			"the committed model transition must not be rolled back")
		persisted, err := queries.GetSession(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.Model).To(Equal(nextModel))
		Expect(persisted.Status).To(Equal(string(api.SessionStatusFailed)))
	})
})
