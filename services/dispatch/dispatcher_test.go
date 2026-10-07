// Copyright 2026 Candace Labs

package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdfs "io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/httpserver"
	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/dispatch"
	harnessservice "github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/ouroboros"
)

// rateLine is one rate_limit_event record as a session's event log holds it.
func rateLine(at time.Time, status string, binding string, resets time.Time, fiveHour float64, sevenDay float64) string {
	return fmt.Sprintf(`{"time":%q,"event_type":"rate_limit_event","event":{"type":"rate_limit_event","rate_limit_info":{"status":%q,"resetsAt":%d,"rateLimitType":%q,"unifiedWindows":{"five_hour":{"utilization":%g,"resetsAt":%d},"seven_day":{"utilization":%g,"resetsAt":%d}}}}}`+"\n",
		at.Format(time.RFC3339Nano), status, resets.Unix(), binding, fiveHour, resets.Unix(), sevenDay, resets.Unix())
}

// providerLine is the harness_provider_applied record a run launched through
// the router logs, naming the provider's base URL and no credential.
func providerLine(baseURL string) string {
	return fmt.Sprintf(`{"time":"2026-10-02T08:58:00Z","event_type":"harness_provider_applied","provider_base_url":%q,"model":"claude-opus-5-5","provider_model":"anthropic/claude-opus-5.5"}`+"\n", baseURL)
}

// inProcess answers every request with the handler itself, so an MCP client
// reaches the CSF service with no listener.
type inProcess struct {
	handler http.Handler
}

func (transport inProcess) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

// unlistable is a state directory that cannot be read.
type unlistable struct{}

func (unlistable) Open(name string) (stdfs.File, error) {
	return nil, &stdfs.PathError{Op: "open", Path: name, Err: stdfs.ErrPermission}
}

// limitNamed is the snapshot's limit of one measure.
func limitNamed(snapshot dispatch.Snapshot, name dispatch.LimitName) dispatch.Limit {
	for _, limit := range snapshot.Limits {
		if limit.Name == name {
			return limit
		}
	}
	Fail("no " + string(name) + " limit in the snapshot")
	return dispatch.Limit{}
}

// sliceIDs lists the snapshot rows' slices.
func sliceIDs(views []dispatch.SliceView) []string {
	ids := make([]string, 0, len(views))
	for _, view := range views {
		ids = append(ids, view.SliceID)
	}
	return ids
}

// stagesOf is every slice of the snapshot by its stage.
func stagesOf(snapshot dispatch.Snapshot) map[string]dispatch.Stage {
	stages := map[string]dispatch.Stage{}
	for _, staged := range snapshot.Staged() {
		stages[staged.Slice.SliceID] = staged.Stage
	}
	return stages
}

func enqueueAll(ctx context.Context, service *dispatch.DispatchService, requests ...*dispatchv1.EnqueueSliceRequest) {
	GinkgoHelper()
	for _, request := range requests {
		_, err := service.Enqueue(ctx, request)
		Expect(err).NotTo(HaveOccurred())
	}
}

var _ = Describe("The slice dispatcher", func() {
	var (
		ctx        context.Context
		controller *gomock.Controller
		harness    *harnessDouble
		published  chan dispatch.Snapshot
	)

	BeforeEach(func() {
		baseline := goleak.IgnoreCurrent()
		DeferCleanup(func() {
			Expect(goleak.Find(baseline)).To(Succeed(), "every goroutine the service started must join")
		})
		ctx = context.Background()
		controller = gomock.NewController(GinkgoT())
		harness = newHarnessDouble(controller)
		harness.workerCap = 5
		published = make(chan dispatch.Snapshot, 256)
	})

	// sink keeps the latest snapshots the way the binary's file would.
	sink := func(snapshot dispatch.Snapshot) error {
		select {
		case published <- snapshot:
		default:
		}
		return nil
	}

	It("launches exactly the ready, uncontended slices, holds the contender until its pair merges and the dependent until its dependency merges", func() {
		var merged sync.Map
		service, _ := started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}), dispatch.WithSnapshotSink(sink),
			dispatch.WithMergeCheck(func(_ context.Context, url string) (bool, error) {
				_, done := merged.Load(url)
				return done, nil
			}))
		enqueueAll(ctx, service,
			sliceRequest("ontology-a", nil, "architecture.csf"),
			sliceRequest("ontology-b", nil, "architecture.csf"),
			sliceRequest("schema", nil, "001_init.sql"),
			sliceRequest("views", dependsOn("schema"), "chat.html"),
			sliceRequest("docs", nil, "README.md"),
		)
		_, err := service.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(harness.submittedSet(3)).To(ConsistOf(assignmentOf("ontology-a"), assignmentOf("schema"), assignmentOf("docs")), "the five slices' ready, uncontended three")
		harness.nothingSubmitted()

		snapshot, err := service.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		harness.nothingSubmitted()
		Expect(sliceIDs(snapshot.Running)).To(Equal([]string{"ontology-a", "schema", "docs"}))
		Expect(sliceIDs(snapshot.Queue)).To(Equal([]string{"ontology-b", "views"}))
		Expect(snapshot.Queue[0].Waiting).To(Equal("contends with ontology-a, which runs"))
		Expect(snapshot.Queue[1].Waiting).To(Equal("depends on schema"))
		Expect(snapshot.Queue[0].Contends).To(Equal([]string{"ontology-a"}))
		Expect(snapshot.Queue[1].DependsOn).To(Equal([]string{"schema"}))
		Expect(snapshot.Queue[1].CreatedAt).To(Equal(frozen))
		Expect(stagesOf(snapshot)).To(Equal(map[string]dispatch.Stage{
			"ontology-a": dispatch.StageRunning, "schema": dispatch.StageRunning, "docs": dispatch.StageRunning,
			"ontology-b": dispatch.StageContended, "views": dispatch.StageBlocked,
		}))
		Expect(snapshot.Finished).To(BeEmpty())
		Expect(snapshot.NextLaunch).To(BeNil(), "nothing ready can run")
		Expect(snapshot.Capacity).To(Equal(5))
		Expect(limitNamed(snapshot, dispatch.LimitHarness).Derivation).To(Equal("6 cores − load1 1.00 = worker cap 5; 3 dispatched sessions run → 2 launches; 1099.5 GB free ≥ disk floor 1.1 GB (twice the largest run directory)"))
		Expect(snapshot.NextPassAt).To(Equal(frozen.Add(dispatch.DispatcherInterval)))
		var latest dispatch.Snapshot
		Eventually(published).Should(Receive(&latest))
		Expect(latest.Limits).NotTo(BeEmpty(), "every pass publishes its snapshot")

		// ontology-a's session opens its pull request, which merges outside
		// the dispatcher: the next pass sees it.
		pull := "https://example.invalid/pull/41"
		service.ObserveSession(&harnessv1.AgentSessionState{AssignmentId: assignmentOf("ontology-a"), Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING, PullRequestUrl: pull})
		Expect(awaitPullRequest(ctx, service, "ontology-a")).To(Equal(pull))
		harness.nothingSubmitted()
		merged.Store(pull, true)
		_, err = service.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		Eventually(harness.cancels, settleBudget.Within).Should(Receive(Equal(assignmentOf("ontology-a"))), "the merged slice's session is asked to stop")
		Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("ontology-b")), "the contender runs once its pair merged")
		harness.nothingSubmitted()

		_, err = service.MarkMerged(ctx, &dispatchv1.MarkSliceMergedRequest{SliceId: "schema"})
		Expect(err).NotTo(HaveOccurred())
		Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("views")), "the dependent runs once its dependency merged")
		snapshot, err = service.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(sliceIDs(snapshot.Running)).To(ConsistOf("ontology-b", "views", "docs"))
		Expect(snapshot.Queue).To(BeEmpty())
		Expect(sliceIDs(snapshot.Finished)).To(Equal([]string{"ontology-a", "schema"}), "the merged slices stay in the graph, in enqueue order")
		Expect(stagesOf(snapshot)).To(Equal(map[string]dispatch.Stage{
			"ontology-a": dispatch.StageMerged, "schema": dispatch.StageMerged,
			"ontology-b": dispatch.StageRunning, "views": dispatch.StageRunning, "docs": dispatch.StageRunning,
		}))
	})

	It("stops launches while the provider's latest rate_limit_event warns, and launches once that window resets", func() {
		rateClock := newMovableClock(frozen)
		resets := frozen.Add(time.Hour)
		logs := fstest.MapFS{
			"older/events.jsonl":  {Data: []byte(rateLine(frozen.Add(-2*time.Hour), "allowed", "five_hour", resets, 0.2, 0.5)), ModTime: frozen.Add(-2 * time.Hour)},
			"newest/events.jsonl": {Data: []byte("{\"time\":\"2026-10-02T08:58:00Z\",\"event_type\":\"assistant\"}\n" + rateLine(frozen.Add(-time.Minute), "allowed_warning", "seven_day", resets, 0.68, 0.94)), ModTime: frozen.Add(-time.Minute)},
			"silent/events.jsonl": {Data: []byte("{\"time\":\"2026-10-02T08:59:30Z\",\"event_type\":\"assistant\"}\n"), ModTime: frozen.Add(-30 * time.Second)},
			"notes.txt":           {Data: []byte("not a run")},
		}
		service, _ := started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}), dispatch.WithLimit(dispatch.RateLimit(logs, rateClock, "")))
		enqueueAll(ctx, service, sliceRequest("a", nil))
		snapshot, err := service.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		harness.nothingSubmitted()
		rate := limitNamed(snapshot, dispatch.LimitRate)
		Expect(rate.Bounded).To(BeTrue())
		Expect(rate.Provider).To(Equal("anthropic"), "the logging run recorded a bare model, so the event is Anthropic's")
		Expect(rate.Launches).To(BeZero())
		Expect(rate.Remaining).To(BeNumerically("~", 0.06, 1e-9))
		Expect(rate.Derivation).To(Equal("latest rate_limit_event from the executor's own provider at 2026-10-02T08:59:00Z (run newest, provider anthropic): status allowed_warning; headroom 0.06 on the seven_day window, at or above the provider's lowest warning threshold 0.75 → no launches"))
		Expect(snapshot.Capacity).To(Equal(5), "a provider-scoped rate limit no longer bounds the whole admission")
		Expect(snapshot.NextLaunch.SliceID).To(Equal("a"))
		Expect(snapshot.NextLaunch.Waiting).To(Equal("the rate limit for provider anthropic admits no launches until its window resets"))
		Expect(stagesOf(snapshot)).To(Equal(map[string]dispatch.Stage{"a": dispatch.StageReady}), "on the frontier, contended by nothing: it waits on its own provider's rate limit alone")

		rateClock.set(resets.Add(time.Second))
		snapshot, err = service.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("a")), "the window reset, so the headroom is back")
		rate = limitNamed(snapshot, dispatch.LimitRate)
		Expect(rate.Bounded).To(BeFalse())
		Expect(rate.Remaining).To(Equal(1.0))
	})

	It("launches a slice whose own provider has headroom while the latest event bounds a different provider", func() {
		// The newest rate_limit_event is Anthropic's: the run that logged it
		// started on a bare model. The queued slice runs on the router's
		// model, another provider, so the stale Anthropic rejection never
		// gates its launches.
		resets := frozen.Add(time.Hour)
		logs := fstest.MapFS{
			"anthropic-run/events.jsonl": {Data: []byte(rateLine(frozen.Add(-time.Minute), "rejected", "seven_day", resets, 0.68, 0.75)), ModTime: frozen.Add(-time.Minute)},
			"anthropic-run/run.json":     {Data: []byte(`{"model":"claude-opus-5-5"}`), ModTime: frozen.Add(-time.Minute)},
		}
		service, _ := started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}), dispatch.WithLimit(dispatch.RateLimit(logs, fakeClock{}, "")))
		router := sliceRequest("router-slice", nil)
		router.GetSlice().GetRecipe().Model = "orcarouter/auto"
		enqueueAll(ctx, service, router)
		snapshot, err := service.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		rate := limitNamed(snapshot, dispatch.LimitRate)
		Expect(rate.Bounded).To(BeTrue(), "the Anthropic window is spent")
		Expect(rate.Provider).To(Equal("anthropic"), "the event is attributed to the run's own provider")
		Expect(snapshot.Capacity).To(BeNumerically(">", 0), "one provider's event never zeroes another's admission")
		Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("router-slice")), "the orcarouter slice launches on its own provider's headroom")
		Expect(sliceIDs(snapshot.Running)).To(Equal([]string{"router-slice"}))
		Expect(snapshot.NextLaunch).To(BeNil())
	})

	It("sets no rate bound before any session logged a rate_limit_event, and fails closed on logs it cannot list", func() {
		none := dispatch.RateLimit(fstest.MapFS{"run/events.jsonl": {Data: []byte("{}\n")}}, fakeClock{}, "")(ctx, 0)
		Expect(none.Bounded).To(BeFalse())
		Expect(none.Derivation).To(Equal("no rate_limit_event from the executor's own provider in any session log yet; no bound"))
		resets := frozen.Add(time.Hour)
		// The provider's early notice: allowed_warning with no threshold
		// passed, at a third of the week.
		early := dispatch.RateLimit(fstest.MapFS{"run/events.jsonl": {Data: []byte(rateLine(frozen.Add(-time.Minute), "allowed_warning", "seven_day", resets, 0.13, 0.31)), ModTime: frozen}}, fakeClock{}, "")(ctx, 0)
		Expect(early.Bounded).To(BeFalse())
		Expect(early.Derivation).To(HaveSuffix("headroom 0.69 on the seven_day window, below the provider's lowest warning threshold 0.75; no bound"))
		rejected := dispatch.RateLimit(fstest.MapFS{"run/events.jsonl": {Data: []byte(rateLine(frozen.Add(-time.Minute), "rejected", "five_hour", resets, 0.5, 0.4)), ModTime: frozen}}, fakeClock{}, "")(ctx, 0)
		Expect(rejected.Bounded).To(BeTrue())
		Expect(rejected.Launches).To(BeZero())
		Expect(rejected.Derivation).To(ContainSubstring("status rejected on the five_hour window until 2026-10-02T10:00:00Z"))
		unreadable := dispatch.RateLimit(unlistable{}, fakeClock{}, "")(ctx, 0)
		Expect(unreadable.Bounded).To(BeTrue())
		Expect(unreadable.Launches).To(BeZero())
		Expect(unreadable.Derivation).To(HavePrefix("not measured, so no launches: dispatch: list session logs"))
	})

	It("keys the rate limit by the active provider: it ignores a spent window a host has switched away from", func() {
		resets := frozen.Add(time.Hour)
		// The subscription provider's window is spent, and its run records no
		// provider; the router's run logged a fresh window. While the router is
		// active the spent subscription window is ignored, and while it is not
		// the subscription window holds launches.
		logs := fstest.MapFS{
			"subscription/events.jsonl": {Data: []byte(rateLine(frozen.Add(-2*time.Minute), "rejected", "five_hour", resets, 0.0, 0.0)), ModTime: frozen.Add(-2 * time.Minute)},
			"router/events.jsonl":       {Data: []byte(providerLine("https://provider.example.invalid") + rateLine(frozen.Add(-time.Minute), "allowed", "seven_day", resets, 0.1, 0.2)), ModTime: frozen.Add(-time.Minute)},
		}
		route := dispatch.RateLimit(logs, fakeClock{}, "https://provider.example.invalid")(ctx, 0)
		Expect(route.Bounded).To(BeFalse(), "the router's own fresh window sets no bound, and the subscription's spent one is ignored")
		Expect(route.Derivation).To(Equal("latest rate_limit_event from https://provider.example.invalid at 2026-10-02T08:59:00Z (run router, provider anthropic): status allowed; headroom 0.80 on the seven_day window, below the provider's lowest warning threshold 0.75; no bound"))

		subscription := dispatch.RateLimit(logs, fakeClock{}, "")(ctx, 0)
		Expect(subscription.Bounded).To(BeTrue(), "the subscription is the active provider again, so its spent window holds launches")
		Expect(subscription.Launches).To(BeZero())
		Expect(subscription.Derivation).To(ContainSubstring("latest rate_limit_event from the executor's own provider at 2026-10-02T08:58:00Z (run subscription, provider anthropic): status rejected"))

		none := dispatch.RateLimit(fstest.MapFS{"router/events.jsonl": {Data: []byte(providerLine("https://provider.example.invalid") + rateLine(frozen.Add(-time.Minute), "allowed", "seven_day", resets, 0.9, 0.9)), ModTime: frozen}}, fakeClock{}, "https://other.example.invalid")(ctx, 0)
		Expect(none.Bounded).To(BeFalse())
		Expect(none.Derivation).To(Equal("no rate_limit_event from https://other.example.invalid in any session log yet; no bound"))
	})

	It("launches no more than the daily budget's remainder buys, and none when the budget cannot be read", func() {
		budget := ouroboros.DailyBudget{Day: "2026-10-02", CapUSDMicros: 100_000_000, SpentUSDMicros: 98_000_000, ReservedUSDMicros: 741_776, ExpectedSessionUSDMicros: 741_776}
		service, _ := started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}),
			dispatch.WithLimit(dispatch.DailyBudgetLimit(func(_ context.Context) (ouroboros.DailyBudget, error) { return budget, nil })))
		enqueueAll(ctx, service, sliceRequest("a", nil), sliceRequest("b", nil), sliceRequest("c", nil))
		_, err := service.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("a")))
		snapshot, err := service.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		harness.nothingSubmitted()
		limit := limitNamed(snapshot, dispatch.LimitBudget)
		Expect(limit.Launches).To(BeZero(), "the running dispatched session holds the one session the remainder buys")
		Expect(limit.Derivation).To(Equal("day 2026-10-02: cap $100.00 − spent $98.00 − reserved $0.74 = $1.26 left; one session is expected to cost $0.74 (the measured baseline), so it buys 1; 1 dispatched sessions run → 0 launches"))

		failed := dispatch.DailyBudgetLimit(func(_ context.Context) (ouroboros.DailyBudget, error) {
			return ouroboros.DailyBudget{}, errors.New("ledger down")
		})(ctx, 0)
		Expect(failed.Bounded).To(BeTrue())
		Expect(failed.Launches).To(BeZero())
		Expect(failed.Derivation).To(ContainSubstring("ledger down"))
	})

	It("admits one launch per measured session even when passes measure at once and launch one after another", func() {
		// Three passes run: the first pass Start runs and two the spec runs. All
		// three measure the limits while nothing runs, so every limit admits the
		// launches it admitted then; none of them may launch past that count
		// because an earlier pass has launched in the meantime.
		const passes = 3
		var measuring atomic.Int32
		arrived := make(chan struct{}, passes)
		measured := make(chan struct{})
		oneSession := func(_ context.Context, running int) dispatch.Limit {
			if measuring.Add(1) <= passes {
				arrived <- struct{}{}
				<-measured
			}
			return dispatch.Limit{Name: dispatch.LimitBudget, Bounded: true, Launches: max(0, 1-running), Remaining: 1, Unit: "sessions", Derivation: "the budget buys one session"}
		}
		service, _ := started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}), dispatch.WithLimit(oneSession))
		enqueueAll(ctx, service, sliceRequest("a", nil), sliceRequest("b", nil), sliceRequest("c", nil))
		var running sync.WaitGroup
		for range passes - 1 {
			running.Go(func() {
				defer GinkgoRecover()
				_, err := service.Dispatch(ctx)
				Expect(err).NotTo(HaveOccurred())
			})
		}
		for range passes {
			Eventually(arrived, settleBudget.Within).Should(Receive(), "every pass measures before any pass launches")
		}
		close(measured)
		running.Wait()
		Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("a")))
		harness.nothingSubmitted()
		snapshot, err := service.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(sliceIDs(snapshot.Running)).To(Equal([]string{"a"}))
		Expect(snapshot.Capacity).To(Equal(1))
	})

	It("launches nothing while free disk is below the harness's floor or its admission is held", func() {
		harness.check = func() (*harnessv1.LaunchCheck, error) {
			return &harnessv1.LaunchCheck{Cores: 32, LoadOneMinute: 2, WorkerCap: 30, FreeBytes: 3e9, DiskFloorBytes: 8e9}, nil
		}
		full, _ := started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}))
		enqueueAll(ctx, full, sliceRequest("a", nil))
		snapshot, err := full.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		harness.nothingSubmitted()
		Expect(limitNamed(snapshot, dispatch.LimitHarness).Derivation).To(HaveSuffix("3.0 GB free is below the disk floor of 8.0 GB (twice the largest run directory) → no launches"))

		held := newHarnessDouble(controller)
		held.check = func() (*harnessv1.LaunchCheck, error) {
			return nil, fmt.Errorf("%w: free disk is below the housekeeping floor", harnessservice.ErrAdmissionHeld)
		}
		service, _ := started(ctx, dispatch.WithSessions(held.sessions), dispatch.WithClock(fakeClock{}))
		enqueueAll(ctx, service, sliceRequest("b", nil))
		snapshot, err = service.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		held.nothingSubmitted()
		Expect(limitNamed(snapshot, dispatch.LimitHarness).Derivation).To(ContainSubstring("admission is held"))
	})

	It("launches nothing while paused, says why each slice waits, and launches at once on resume", func() {
		service, _ := started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}))
		paused, err := service.Pause(ctx, dispatch.DispatcherControlInput{Reason: "the operator reviews the queue"})
		Expect(err).NotTo(HaveOccurred())
		Expect(paused.Paused).To(BeTrue())
		Expect(paused.PauseReason).To(Equal("the operator reviews the queue"))
		enqueueAll(ctx, service, sliceRequest("a", nil), sliceRequest("b", dependsOn("a")))
		snapshot, err := service.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		harness.nothingSubmitted()
		Expect(snapshot.Queue[0].Waiting).To(Equal("the dispatcher is paused: the operator reviews the queue"))

		_, err = service.Pause(ctx, dispatch.DispatcherControlInput{Reason: " "})
		Expect(err).To(MatchError(dispatch.ErrInvalidControl))
		Expect(err).To(MatchError(csf.ErrInvalidRequest))
		resumed, err := service.Resume(ctx, dispatch.DispatcherControlInput{Reason: "reviewed"})
		Expect(err).NotTo(HaveOccurred())
		Expect(resumed.Paused).To(BeFalse())
		Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("a")))
	})

	It("holds and releases one slice, and refuses a hold it cannot apply", func() {
		service, _ := started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}))
		_, err := service.Pause(ctx, dispatch.DispatcherControlInput{Reason: "stage"})
		Expect(err).NotTo(HaveOccurred())
		enqueueAll(ctx, service, sliceRequest("a", nil), sliceRequest("b", nil))
		snapshot, err := service.Hold(ctx, dispatch.SliceControlInput{SliceID: "a", Reason: "waits for a ruling"})
		Expect(err).NotTo(HaveOccurred())
		Expect(sliceIDs(snapshot.Held)).To(Equal([]string{"a"}))
		Expect(snapshot.Held[0].Held).To(Equal("waits for a ruling"))
		_, err = service.Resume(ctx, dispatch.DispatcherControlInput{Reason: "staged"})
		Expect(err).NotTo(HaveOccurred())
		Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("b")), "the held slice is passed over")
		harness.nothingSubmitted()
		_, err = service.Release(ctx, dispatch.SliceControlInput{SliceID: "a", Reason: "ruled"})
		Expect(err).NotTo(HaveOccurred())
		Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("a")))

		_, err = service.Hold(ctx, dispatch.SliceControlInput{SliceID: "missing", Reason: "x"})
		Expect(err).To(MatchError(dispatch.ErrUnknownSlice))
		_, err = service.Hold(ctx, dispatch.SliceControlInput{Reason: "x"})
		Expect(err).To(MatchError(dispatch.ErrInvalidControl))
		_, err = service.MarkMerged(ctx, &dispatchv1.MarkSliceMergedRequest{SliceId: "b"})
		Expect(err).NotTo(HaveOccurred())
		_, err = service.Hold(ctx, dispatch.SliceControlInput{SliceID: "b", Reason: "too late"})
		Expect(err).To(MatchError(dispatch.ErrSliceFinished))
	})

	It("keeps a pause and a hold across a restart, with their reasons", func() {
		database := openDatabase(ctx)
		first, err := dispatch.NewDispatchService(dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}), dispatch.WithDatabase(database))
		Expect(err).NotTo(HaveOccurred())
		firstScope := runtime.NewScope(ctx, "first")
		Expect(first.Start(firstScope)).To(Succeed())
		_, err = first.Pause(ctx, dispatch.DispatcherControlInput{Reason: "maintenance"})
		Expect(err).NotTo(HaveOccurred())
		enqueueAll(ctx, first, sliceRequest("a", nil), sliceRequest("b", nil))
		_, err = first.Hold(ctx, dispatch.SliceControlInput{SliceID: "a", Reason: "needs the schema slice"})
		Expect(err).NotTo(HaveOccurred())
		_, err = first.Hold(ctx, dispatch.SliceControlInput{SliceID: "b", Reason: "briefly"})
		Expect(err).NotTo(HaveOccurred())
		_, err = first.Release(ctx, dispatch.SliceControlInput{SliceID: "b", Reason: "done"})
		Expect(err).NotTo(HaveOccurred())
		Expect(firstScope.Close()).To(Succeed())

		second, _ := started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}), dispatch.WithDatabase(database))
		snapshot, err := second.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		harness.nothingSubmitted()
		Expect(snapshot.PauseReason).To(Equal("maintenance"))
		Expect(sliceIDs(snapshot.Held)).To(Equal([]string{"a"}))
		Expect(snapshot.Held[0].Held).To(Equal("needs the schema slice"))
		_, err = second.Resume(ctx, dispatch.DispatcherControlInput{Reason: "back"})
		Expect(err).NotTo(HaveOccurred())
		Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("b")))
		harness.nothingSubmitted()
	})

	It("reports a held slice's branch held, from the recipe, across a restart", func() {
		database := openDatabase(ctx)
		first, err := dispatch.NewDispatchService(dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}), dispatch.WithDatabase(database))
		Expect(err).NotTo(HaveOccurred())
		firstScope := runtime.NewScope(ctx, "first")
		Expect(first.Start(firstScope)).To(Succeed())
		enqueueAll(ctx, first, sliceRequest("a", nil), sliceRequest("b", nil))
		_, err = first.Hold(ctx, dispatch.SliceControlInput{SliceID: "a", Reason: "the operator is reviewing it"})
		Expect(err).NotTo(HaveOccurred())

		reason, held, err := first.HeldBranch(ctx, "p1/a")
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeTrue())
		Expect(reason).To(Equal("the operator is reviewing it"))
		reason, held, err = first.HeldBranch(ctx, "p1/b")
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeFalse(), "an unheld slice's branch is free")
		Expect(reason).To(BeEmpty())
		Expect(firstScope.Close()).To(Succeed())

		second, _ := started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}), dispatch.WithDatabase(database))
		reason, held, err = second.HeldBranch(ctx, "p1/a")
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeTrue(), "the hold is read from the recipe's branch after a restart")
		Expect(reason).To(Equal("the operator is reviewing it"))
		reason, held, err = second.HeldBranch(ctx, "p1/b")
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeFalse())
		Expect(reason).To(BeEmpty())
	})

	It("declares its pass as a cron trigger", func() {
		service, err := dispatch.NewDispatchService(dispatch.WithSessions(harness.sessions))
		Expect(err).NotTo(HaveOccurred())
		Expect(service.Trigger()).NotTo(BeNil())
		_, err = dispatch.NewDispatchService(dispatch.WithSessions(harness.sessions), dispatch.WithLimit(nil))
		Expect(err).To(MatchError(dispatch.ErrInvalidOption))
		_, err = dispatch.NewDispatchService(dispatch.WithSessions(harness.sessions), dispatch.WithMergeCheck(nil))
		Expect(err).To(MatchError(dispatch.ErrInvalidOption))
		_, err = dispatch.NewDispatchService(dispatch.WithSessions(harness.sessions), dispatch.WithSnapshotSink(nil))
		Expect(err).To(MatchError(dispatch.ErrInvalidOption))
	})
})

var _ = Describe("The slice dispatcher's operations", func() {
	var (
		ctx     context.Context
		harness *harnessDouble
		service *dispatch.DispatchService
		router  *gin.Engine
	)

	BeforeEach(func() {
		ctx = context.Background()
		harness = newHarnessDouble(gomock.NewController(GinkgoT()))
		harness.workerCap = 2
		service, _ = started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}))
		api, err := csf.New(service.Tools()...)
		Expect(err).NotTo(HaveOccurred())
		router = httpserver.NewEngine("dispatch-spec")
		api.Register(router)
		service.Register(router)
		router.Any("/mcp", gin.WrapH(api.MCPHandler()))
	})

	// recipeArgument is the recipe as an MCP client sends it: agent.json's
	// own JSON.
	recipeArgument := func(slice string) map[string]any {
		content, err := json.Marshal(newRecipe(slice))
		Expect(err).NotTo(HaveOccurred())
		var argument map[string]any
		Expect(json.Unmarshal(content, &argument)).To(Succeed())
		return argument
	}

	It("lets an MCP client add a slice and hold it, and the dispatcher honors both", func() {
		transport := &mcp.StreamableClientTransport{Endpoint: "http://csf.invalid/mcp", HTTPClient: &http.Client{Transport: inProcess{handler: router}}, DisableStandaloneSSE: true}
		session, err := mcp.NewClient(&mcp.Implementation{Name: "dispatch-spec", Version: "1"}, nil).Connect(ctx, transport, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(session.Close)
		call := func(tool string, arguments map[string]any) map[string]any {
			GinkgoHelper()
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: arguments})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsError).To(BeFalse(), "%s: %v", tool, result.Content)
			content, err := json.Marshal(result.StructuredContent)
			Expect(err).NotTo(HaveOccurred())
			var structured map[string]any
			Expect(json.Unmarshal(content, &structured)).To(Succeed())
			return structured
		}

		listed, err := session.ListTools(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		var names []string
		for _, tool := range listed.Tools {
			names = append(names, tool.Name)
		}
		Expect(names).To(ContainElements(dispatch.AddSliceTool, dispatch.HoldSliceTool, dispatch.ReleaseSliceTool, dispatch.PauseDispatcherTool, dispatch.ResumeDispatcherTool, dispatch.DispatcherStateTool))

		call(dispatch.PauseDispatcherTool, map[string]any{"reason": "stage a slice"})
		added := call(dispatch.AddSliceTool, map[string]any{"slice_id": "mcp-slice", "ticket_url": "https://example.invalid/issues/7", "recipe": recipeArgument("mcp-slice")})
		Expect(added).To(HaveKeyWithValue("slice_id", "mcp-slice"))
		Expect(added).To(HaveKeyWithValue("title", "Slice mcp-slice"), "the title defaults to the recipe's pull request title")
		held := call(dispatch.HoldSliceTool, map[string]any{"slice_id": "mcp-slice", "reason": "wait for the operator"})
		Expect(held["held"]).To(HaveLen(1))
		call(dispatch.ResumeDispatcherTool, map[string]any{"reason": "staged"})
		_, err = service.Dispatch(ctx)
		Expect(err).NotTo(HaveOccurred())
		harness.nothingSubmitted()
		snapshot := call(dispatch.DispatcherStateTool, map[string]any{})
		Expect(snapshot["paused"]).To(BeFalse())
		Expect(snapshot["held"]).To(ConsistOf(HaveKeyWithValue("held", "wait for the operator")))

		call(dispatch.ReleaseSliceTool, map[string]any{"slice_id": "mcp-slice", "reason": "go"})
		Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("mcp-slice")))
		node := stateOf(ctx, service, "mcp-slice")
		Expect(node.GetProvenance().GetSource()).To(Equal(dispatch.ProvenanceSliceAdd))
		Expect(node.GetSlice().GetRecipe().GetTicketUrl()).To(Equal("https://example.invalid/issues/1"), "the recipe's own ticket is kept")
		Expect(node.GetProvenance().GetIssueUrl()).To(Equal("https://example.invalid/issues/7"))

		refused, err := session.CallTool(ctx, &mcp.CallToolParams{Name: dispatch.HoldSliceTool, Arguments: map[string]any{"slice_id": "nope", "reason": "x"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(refused.IsError).To(BeTrue(), "an unknown slice is the tool's error, not the transport's")
	})

	It("serves the same operations over HTTP with the defined error classes", func() {
		serve := func(method string, path string, body string) *httptest.ResponseRecorder {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
			router.ServeHTTP(recorder, request)
			return recorder
		}
		recipe, err := json.Marshal(newRecipe("http-slice"))
		Expect(err).NotTo(HaveOccurred())
		added := serve(http.MethodPost, dispatch.AddPath, `{"slice_id":"http-slice","ticket_url":"https://example.invalid/issues/8","recipe":`+string(recipe)+`}`)
		Expect(added.Code).To(Equal(http.StatusOK), added.Body.String())
		Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("http-slice")))

		Expect(serve(http.MethodPost, dispatch.AddPath, `{"slice_id":"no-ticket","recipe":`+string(recipe)+`}`).Code).To(Equal(http.StatusBadRequest))
		Expect(serve(http.MethodPost, dispatch.AddPath, `{"slice_id":"http-slice","ticket_url":"https://example.invalid/issues/8","recipe":`+string(recipe)+`}`).Code).To(Equal(http.StatusConflict), "a slice is added once")
		Expect(serve(http.MethodPost, dispatch.HoldPath, `{"slice_id":"http-slice"}`).Code).To(Equal(http.StatusBadRequest), "a hold needs its reason")
		Expect(serve(http.MethodPost, dispatch.HoldPath, `{"slice_id":"nope","reason":"x"}`).Code).To(Equal(http.StatusNotFound))
		Expect(serve(http.MethodPost, dispatch.PausePath, `not json`).Code).To(Equal(http.StatusBadRequest))
		Expect(serve(http.MethodPost, dispatch.PausePath, `{"reason":"lunch"}`).Code).To(Equal(http.StatusOK))

		state := serve(http.MethodGet, dispatch.SnapshotPath, "")
		Expect(state.Code).To(Equal(http.StatusOK))
		var snapshot dispatch.Snapshot
		Expect(json.Unmarshal(state.Body.Bytes(), &snapshot)).To(Succeed())
		Expect(snapshot.Paused).To(BeTrue())
		Expect(sliceIDs(snapshot.Running)).To(Equal([]string{"http-slice"}))
	})
})
