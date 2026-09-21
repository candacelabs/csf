package copilotadapter

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/pkg/patience"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

type schedulePatchStore struct {
	IStore
	row     storedb.ChatSchedule
	lookups int
	updates int
}

type schedulePauseRaceStore struct {
	IStore
	mutex                  sync.Mutex
	row                    storedb.ChatSchedule
	lookups                int
	sessionLookups         int
	patchLookupEntered     chan struct{}
	continuePatchLookup    chan struct{}
	scheduledLookupEntered chan struct{}
	continueScheduled      chan struct{}
	deleted                bool
}

type dueScheduleStore struct {
	*cron.MemoryStore
}

func (store *dueScheduleStore) Reconcile(
	ctx context.Context,
	definitions []cron.JobDefinition,
	now time.Time,
) ([]cron.JobState, error) {
	return store.MemoryStore.Reconcile(ctx, definitions, now.Add(-24*time.Hour))
}

func (store *schedulePauseRaceStore) GetChatSchedule(ctx context.Context, identifier uuid.UUID) (storedb.ChatSchedule, error) {
	store.mutex.Lock()
	store.lookups++
	lookup := store.lookups
	row := store.row
	deleted := store.deleted
	store.mutex.Unlock()
	if identifier != row.ID {
		return storedb.ChatSchedule{}, sql.ErrNoRows
	}
	switch lookup {
	case 2:
		close(store.patchLookupEntered)
		select {
		case <-ctx.Done():
			return storedb.ChatSchedule{}, ctx.Err()
		case <-store.continuePatchLookup:
		}
	case 3:
		close(store.scheduledLookupEntered)
		select {
		case <-ctx.Done():
			return storedb.ChatSchedule{}, ctx.Err()
		case <-store.continueScheduled:
		}
	}
	if deleted {
		return storedb.ChatSchedule{}, sql.ErrNoRows
	}
	return row, nil
}

func (store *schedulePauseRaceStore) ListChatSchedules(ctx context.Context) ([]storedb.ChatSchedule, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.deleted {
		return nil, nil
	}
	return []storedb.ChatSchedule{store.row}, nil
}

func (store *schedulePauseRaceStore) UpdateChatSchedule(ctx context.Context, parameters storedb.UpdateChatScheduleParams) (storedb.ChatSchedule, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.row.DisplayName = parameters.DisplayName
	store.row.Prompt = parameters.Prompt
	store.row.CronExpression = parameters.CronExpression
	store.row.Timezone = parameters.Timezone
	store.row.Status = parameters.Status
	store.row.UpdatedAt = parameters.UpdatedAt
	return store.row, nil
}

func (store *schedulePauseRaceStore) DeleteChatSchedule(
	ctx context.Context,
	parameters storedb.DeleteChatScheduleParams,
) (uuid.UUID, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if parameters.ID != store.row.ID || store.deleted {
		return uuid.Nil, sql.ErrNoRows
	}
	store.deleted = true
	return parameters.ID, nil
}

func (store *schedulePauseRaceStore) GetSession(ctx context.Context, identifier uuid.UUID) (storedb.Session, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.sessionLookups++
	return storedb.Session{ID: identifier, Status: string(api.SessionStatusIdle)}, nil
}

func (store *schedulePauseRaceStore) observedSessionLookups() int {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return store.sessionLookups
}

func mutationReferences(adapter *CopilotAdapter, identifier uuid.UUID) int {
	adapter.mutations.mutex.Lock()
	defer adapter.mutations.mutex.Unlock()
	mutation := adapter.mutations.entries[identifier]
	if mutation == nil {
		return 0
	}
	return mutation.references
}

func (store *schedulePatchStore) GetChatSchedule(ctx context.Context, identifier uuid.UUID) (storedb.ChatSchedule, error) {
	store.lookups++
	if identifier != store.row.ID {
		return storedb.ChatSchedule{}, sql.ErrNoRows
	}
	return store.row, nil
}

func (store *schedulePatchStore) UpdateChatSchedule(ctx context.Context, parameters storedb.UpdateChatScheduleParams) (storedb.ChatSchedule, error) {
	store.updates++
	store.row.DisplayName = parameters.DisplayName
	store.row.Prompt = parameters.Prompt
	store.row.CronExpression = parameters.CronExpression
	store.row.Timezone = parameters.Timezone
	store.row.Status = parameters.Status
	store.row.UpdatedAt = parameters.UpdatedAt
	return store.row, nil
}

var _ = Describe("schedule patches", func() {
	var (
		adapter   *CopilotAdapter
		persisted *schedulePatchStore
	)

	BeforeEach(func() {
		now := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
		persisted = &schedulePatchStore{row: storedb.ChatSchedule{
			ID:          uuid.MustParse("00000000-0000-0000-0000-000000000001"),
			SessionID:   uuid.MustParse("00000000-0000-0000-0000-000000000002"),
			DisplayName: "daily", Prompt: "review", CronExpression: "0 9 * * *", Timezone: "UTC",
			Status: string(api.ChatScheduleStatusPaused), CreatedAt: now, UpdatedAt: now,
		}}
		adapter = &CopilotAdapter{
			store: persisted, scheduleStore: cron.NewMemoryStore(), logger: slog.Default(), config: DefaultAdapterConfig(),
			scheduleReload: make(chan scheduleReloadRequest, 1), mutations: newMutationRegistry[uuid.UUID](),
			scheduleControls: newMutationRegistry[uuid.UUID](),
		}
	})

	It("rejects an empty patch before looking up an unknown schedule", func() {
		unknownID := uuid.MustParse("00000000-0000-0000-0000-000000000099")
		response, err := adapter.UpdateChatSchedule(context.Background(), api.UpdateChatScheduleRequestObject{
			ScheduleId: unknownID,
			Body:       &api.UpdateChatScheduleRequest{},
		})

		var rejected failure
		Expect(response).To(BeNil())
		Expect(errors.As(err, &rejected)).To(BeTrue())
		Expect(rejected.status).To(Equal(http.StatusBadRequest))
		Expect(rejected.code).To(Equal(errorCodeEmptyPatch))
		Expect(persisted.lookups).To(BeZero())
	})

	It("leaves an existing row unchanged without requesting a schedule reload", func() {
		before := persisted.row
		response, err := adapter.UpdateChatSchedule(context.Background(), api.UpdateChatScheduleRequestObject{
			ScheduleId: persisted.row.ID,
			Body:       &api.UpdateChatScheduleRequest{},
		})

		var rejected failure
		Expect(response).To(BeNil())
		Expect(errors.As(err, &rejected)).To(BeTrue())
		Expect(rejected.status).To(Equal(http.StatusBadRequest))
		Expect(rejected.code).To(Equal(errorCodeEmptyPatch))
		Expect(persisted.row).To(Equal(before))
		Expect(persisted.lookups).To(BeZero())
		Expect(persisted.updates).To(BeZero())
		Expect(adapter.scheduleReload).To(HaveLen(0))
	})

	It("fences a captured job behind the same lock before acknowledging a pause", func() {
		now := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
		raceStore := &schedulePauseRaceStore{
			row: storedb.ChatSchedule{
				ID:          uuid.MustParse("00000000-0000-0000-0000-000000000011"),
				SessionID:   uuid.MustParse("00000000-0000-0000-0000-000000000012"),
				DisplayName: "daily", Prompt: "must not run", CronExpression: "0 9 * * *", Timezone: "UTC",
				Status: string(api.ChatScheduleStatusActive), CreatedAt: now, UpdatedAt: now,
			},
			patchLookupEntered:     make(chan struct{}),
			continuePatchLookup:    make(chan struct{}),
			scheduledLookupEntered: make(chan struct{}),
			continueScheduled:      make(chan struct{}),
		}
		adapter := &CopilotAdapter{
			store: raceStore, scheduleStore: cron.NewMemoryStore(), logger: slog.Default(), config: DefaultAdapterConfig(),
			scheduleReload: make(chan scheduleReloadRequest, 1), mutations: newMutationRegistry[uuid.UUID](),
			scheduleControls: newMutationRegistry[uuid.UUID](),
		}
		runContext, cancelRun := context.WithCancel(context.Background())
		runFinished := make(chan error, 1)
		go func() { runFinished <- adapter.RunSchedules(runContext) }()
		DeferCleanup(func() {
			cancelRun()
			Eventually(runFinished).Should(Receive(Succeed()))
		})
		captured := raceStore.row
		paused := api.ChatScheduleStatusPaused
		patchFinished := make(chan error, 1)
		go func() {
			_, err := adapter.UpdateChatSchedule(context.Background(), api.UpdateChatScheduleRequestObject{
				ScheduleId: captured.ID,
				Body:       &api.UpdateChatScheduleRequest{Status: &paused},
			})
			patchFinished <- err
		}()
		Eventually(raceStore.patchLookupEntered).Should(BeClosed())

		jobFinished := make(chan error, 1)
		go func() {
			jobFinished <- adapter.chatScheduleJob(captured)(context.Background(), cron.Invocation{ID: "occurrence"})
		}()
		patience.Await(GinkgoT(), "the captured job waiting on the pause mutation", patience.Budget{
			Within: time.Second, Interval: time.Millisecond,
		}, func() int {
			return mutationReferences(adapter, captured.SessionID)
		}, func(references int) bool {
			return references == 2
		})

		close(raceStore.continuePatchLookup)
		Eventually(patchFinished).Should(Receive(Succeed()))
		Eventually(raceStore.scheduledLookupEntered).Should(BeClosed())
		close(raceStore.continueScheduled)
		Eventually(jobFinished).Should(Receive(Succeed()))
		Expect(raceStore.observedSessionLookups()).To(Equal(1), "the pause must verify its session is mutable before stopping the job")
		Expect(adapter.scheduleReload).To(HaveLen(0))
	})

	It("releases the mutation fence before acknowledging a deletion reload", func() {
		now := time.Now().UTC()
		raceStore := &schedulePauseRaceStore{
			row: storedb.ChatSchedule{
				ID:          uuid.MustParse("00000000-0000-0000-0000-000000000031"),
				SessionID:   uuid.MustParse("00000000-0000-0000-0000-000000000032"),
				DisplayName: "due", Prompt: "must not run", CronExpression: "0 9 * * *", Timezone: "UTC",
				Status: string(api.ChatScheduleStatusActive), CreatedAt: now, UpdatedAt: now,
			},
			patchLookupEntered:     make(chan struct{}),
			continuePatchLookup:    make(chan struct{}),
			scheduledLookupEntered: make(chan struct{}),
			continueScheduled:      make(chan struct{}),
		}
		adapter := &CopilotAdapter{
			store: raceStore, scheduleStore: &dueScheduleStore{MemoryStore: cron.NewMemoryStore()},
			logger: slog.Default(), config: DefaultAdapterConfig(),
			scheduleReload: make(chan scheduleReloadRequest, 1), mutations: newMutationRegistry[uuid.UUID](),
			scheduleControls: newMutationRegistry[uuid.UUID](),
		}
		var releasePatchOnce, releaseScheduledOnce sync.Once
		releasePatch := func() { releasePatchOnce.Do(func() { close(raceStore.continuePatchLookup) }) }
		releaseScheduled := func() { releaseScheduledOnce.Do(func() { close(raceStore.continueScheduled) }) }
		DeferCleanup(releasePatch)
		DeferCleanup(releaseScheduled)

		type deleteResult struct {
			response api.DeleteChatScheduleResponseObject
			err      error
		}
		deleteFinished := make(chan deleteResult, 1)
		go func() {
			response, err := adapter.DeleteChatSchedule(context.Background(), api.DeleteChatScheduleRequestObject{
				ScheduleId: raceStore.row.ID,
			})
			deleteFinished <- deleteResult{response: response, err: err}
		}()
		Eventually(raceStore.patchLookupEntered).Should(BeClosed())

		runContext, cancelRun := context.WithCancel(context.Background())
		runFinished := make(chan error, 1)
		go func() { runFinished <- adapter.RunSchedules(runContext) }()
		DeferCleanup(func() {
			cancelRun()
			Eventually(runFinished).Should(Receive(Succeed()))
		})
		patience.Await(GinkgoT(), "the due worker waiting behind the delete mutation", patience.Budget{
			Within: time.Second, Interval: time.Millisecond,
		}, func() int {
			return mutationReferences(adapter, raceStore.row.SessionID)
		}, func(references int) bool {
			return references == 2
		})

		releasePatch()
		releaseScheduled()
		var result deleteResult
		Eventually(deleteFinished).Should(Receive(&result),
			"the delete must release its mutation fence before the runtime drains its queued worker")
		Expect(result.err).NotTo(HaveOccurred())
		Expect(result.response).To(BeAssignableToTypeOf(api.DeleteChatSchedule204Response{}))
		snapshot, err := adapter.scheduleStore.Snapshot(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Jobs).To(BeEmpty())
		Expect(raceStore.observedSessionLookups()).To(Equal(1))
	})

	It("does not hand off a stale active capture after an update or resume", func() {
		now := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
		current := storedb.ChatSchedule{
			ID:          uuid.MustParse("00000000-0000-0000-0000-000000000021"),
			SessionID:   uuid.MustParse("00000000-0000-0000-0000-000000000022"),
			DisplayName: "updated", Prompt: "new prompt", CronExpression: "5 9 * * *", Timezone: "UTC",
			Status: string(api.ChatScheduleStatusActive), CreatedAt: now, UpdatedAt: now.Add(time.Second),
		}
		raceStore := &schedulePauseRaceStore{row: current}
		adapter := &CopilotAdapter{
			store: raceStore, scheduleStore: cron.NewMemoryStore(), logger: slog.Default(), config: DefaultAdapterConfig(),
			scheduleReload: make(chan scheduleReloadRequest, 1), mutations: newMutationRegistry[uuid.UUID](),
			scheduleControls: newMutationRegistry[uuid.UUID](),
		}
		captured := current
		captured.Prompt = "old prompt"
		captured.CronExpression = "0 9 * * *"
		captured.UpdatedAt = now

		Expect(adapter.chatScheduleJob(captured)(context.Background(), cron.Invocation{ID: "stale-occurrence"})).To(Succeed())
		Expect(raceStore.observedSessionLookups()).To(BeZero(), "a stale revision must stop before turn submission")
	})
})
