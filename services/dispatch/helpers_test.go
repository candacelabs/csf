// Copyright 2026 Candace Labs

package dispatch_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/pkg/pgmem"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/dispatch"
	"github.com/candacelabs/csf/services/dispatch/mocks"
)

// settleBudget bounds a spec's wait for the owner goroutine to dispatch;
// generous because the costs are not symmetric (CS-9).
var settleBudget = eventually.Budget{Within: 5 * time.Second}

// quiet is how long a spec watches for a dispatch that must not happen.
const quiet = 100 * time.Millisecond

var frozen = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

// fakeClock stands still.
type fakeClock struct{}

func (fakeClock) Now() time.Time { return frozen }

func (fakeClock) After(_ time.Duration) (<-chan time.Time, func() bool) {
	return make(chan time.Time), func() bool { return true }
}

func (fakeClock) AfterFunc(_ time.Duration, _ func()) func() bool { return func() bool { return true } }

// movableClock is a clock a spec moves: the rate limit reads it to tell a
// provider window that has reset from one that has not.
type movableClock struct {
	now atomic.Int64
}

func newMovableClock(at time.Time) *movableClock {
	clock := &movableClock{}
	clock.set(at)
	return clock
}

func (clock *movableClock) set(at time.Time) { clock.now.Store(at.UnixNano()) }

func (clock *movableClock) Now() time.Time { return time.Unix(0, clock.now.Load()).UTC() }

func (clock *movableClock) After(_ time.Duration) (<-chan time.Time, func() bool) {
	return make(chan time.Time), func() bool { return true }
}

func (clock *movableClock) AfterFunc(_ time.Duration, _ func()) func() bool {
	return func() bool { return true }
}

// assignmentOf is the first attempt's assignment for a slice: the recipe's.
func assignmentOf(slice string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(slice)).String()
}

// resumedAssignmentOf is the assignment a later attempt of a slice runs
// under, as the service derives it.
func resumedAssignmentOf(slice string, attempt int) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "%s#%d", slice, attempt)).String()
}

// newRecipe is a recipe the harness could run for the slice.
func newRecipe(slice string) *pb.AgentAssignmentRecipe {
	return &pb.AgentAssignmentRecipe{
		AssignmentId: assignmentOf(slice),
		Agent:        &pb.AgentDefinition{Id: "scratch", Revision: 1, DisplayName: "Scratch", Instructions: "Work in the worktree."},
		TicketUrl:    "https://example.invalid/issues/1",
		Task:         "Deliver slice " + slice + ".",
		Model:        "sonnet",
		RepositoryId: "scratch",
		Workspace: &pb.AgentWorkspace{
			RepositoryPath: "/repository", BaseBranch: "main", Branch: "p1/" + slice,
			AllowedTools: []string{"Bash", "Edit"}, PullRequestTitle: "Slice " + slice,
		},
	}
}

// sliceRequest is an Enqueue of one slice touching the given hotspots.
func sliceRequest(slice string, edges *dispatchv1.SliceEdges, hotspots ...string) *dispatchv1.EnqueueSliceRequest {
	return &dispatchv1.EnqueueSliceRequest{
		Slice:      &dispatchv1.Slice{SliceId: slice, Title: "Slice " + slice, Recipe: newRecipe(slice)},
		Edges:      edges,
		TouchSet:   &dispatchv1.TouchSet{Hotspots: hotspots},
		Provenance: &dispatchv1.Provenance{Source: "spec"},
	}
}

func dependsOn(slices ...string) *dispatchv1.SliceEdges {
	return &dispatchv1.SliceEdges{DependsOn: slices}
}

// harnessDouble is the agent harness as the service sees it: every Submit
// is recorded and answered open, every Cancel is recorded, the launch check
// reports workerCap, and the spec delivers the session's later phases
// through ObserveSession.
type harnessDouble struct {
	sessions *mocks.MockISessionHost
	submits  chan *pb.AgentAssignmentRecipe
	cancels  chan string
	// workerCap is what the launch check reports; set it before the
	// service starts.
	workerCap uint32
	// check, when set before the service starts, replaces the launch check:
	// a full disk, or a held admission as its error.
	check func() (*harnessv1.LaunchCheck, error)
}

func newHarnessDouble(controller *gomock.Controller) *harnessDouble {
	double := &harnessDouble{
		sessions: mocks.NewMockISessionHost(controller),
		submits:  make(chan *pb.AgentAssignmentRecipe, 64),
		cancels:  make(chan string, 64),
	}
	double.sessions.EXPECT().Submit(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, request *harnessv1.SubmitAgentSessionRequest) (*harnessv1.SubmitAgentSessionResponse, error) {
		double.submits <- request.GetRecipe()
		return &harnessv1.SubmitAgentSessionResponse{
			Session: &harnessv1.AgentSessionState{AssignmentId: request.GetRecipe().GetAssignmentId(), Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN},
			Check:   &harnessv1.LaunchCheck{WorkerCap: double.workerCap, Admitted: true},
		}, nil
	}).AnyTimes()
	double.sessions.EXPECT().Cancel(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, request *harnessv1.CancelAgentSessionRequest) (*harnessv1.CancelAgentSessionResponse, error) {
		double.cancels <- request.GetAssignmentId()
		return &harnessv1.CancelAgentSessionResponse{Session: &harnessv1.AgentSessionState{AssignmentId: request.GetAssignmentId(), Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELING}}, nil
	}).AnyTimes()
	double.sessions.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, request *harnessv1.SendAgentSessionMessageRequest) (*harnessv1.SendAgentSessionMessageResponse, error) {
		return &harnessv1.SendAgentSessionMessageResponse{TurnId: "2", Session: &harnessv1.AgentSessionState{AssignmentId: request.GetAssignmentId()}}, nil
	}).AnyTimes()
	double.sessions.EXPECT().Check(gomock.Any()).DoAndReturn(func(_ context.Context) (*harnessv1.LaunchCheck, error) {
		if double.check != nil {
			return double.check()
		}
		return &harnessv1.LaunchCheck{Cores: double.workerCap + 1, LoadOneMinute: 1, WorkerCap: double.workerCap, FreeBytes: 1 << 40, DiskFloorBytes: 1 << 30}, nil
	}).AnyTimes()
	return double
}

// submitted waits for the next Submit and returns its recipe.
func (double *harnessDouble) submitted() *pb.AgentAssignmentRecipe {
	var recipe *pb.AgentAssignmentRecipe
	Eventually(double.submits, settleBudget.Within).Should(Receive(&recipe), "a session is submitted")
	return recipe
}

// submittedSet waits for count Submits and returns their assignments.
func (double *harnessDouble) submittedSet(count int) []string {
	assignments := make([]string, 0, count)
	for range count {
		assignments = append(assignments, double.submitted().GetAssignmentId())
	}
	return assignments
}

// nothingSubmitted asserts no Submit arrives for a while.
func (double *harnessDouble) nothingSubmitted() {
	Consistently(double.submits, quiet).ShouldNot(Receive(), "no session is submitted")
}

// openDatabase is a pgmem database holding CSF's real schema, served through
// the pgx surface the service's store runs on.
func openDatabase(ctx context.Context) *dispatch.PostgresDispatchDatabase {
	engine := pgmem.MustNew()
	DeferCleanup(engine.Close)
	handle := engine.Open()
	DeferCleanup(handle.Close)
	Expect(csfpg.ApplySchema(ctx, handle)).To(Succeed())
	pgx := engine.Public().OpenPGX()
	DeferCleanup(pgx.Close)
	database, err := dispatch.NewPostgresDispatchDatabase(pgx)
	Expect(err).NotTo(HaveOccurred())
	return database
}

// started builds and starts a service on a fresh scope; the scope is closed
// after the spec.
func started(ctx context.Context, options ...dispatch.Option) (*dispatch.DispatchService, *runtime.Scope) {
	service, err := dispatch.NewDispatchService(options...)
	Expect(err).NotTo(HaveOccurred())
	scope := runtime.NewScope(ctx, "spec")
	Expect(service.Start(scope)).To(Succeed())
	DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })
	return service, scope
}

// stateOf reads one slice's state from List.
func stateOf(ctx context.Context, service *dispatch.DispatchService, slice string) *dispatchv1.SliceNode {
	listed, err := service.List(ctx, &dispatchv1.ListSlicesRequest{})
	Expect(err).NotTo(HaveOccurred())
	for _, node := range listed.GetNodes() {
		if node.GetSlice().GetSliceId() == slice {
			return node
		}
	}
	return nil
}

// awaitState waits for a slice to reach a state.
func awaitState(ctx context.Context, service *dispatch.DispatchService, slice string, want dispatchv1.SliceState) *dispatchv1.SliceNode {
	return eventually.Await(GinkgoT(), slice+" to reach "+want.String(), settleBudget,
		func() *dispatchv1.SliceNode { return stateOf(ctx, service, slice) },
		func(node *dispatchv1.SliceNode) bool { return node.GetState() == want })
}

// awaitPullRequest waits for a slice to carry a pull request URL.
func awaitPullRequest(ctx context.Context, service *dispatch.DispatchService, slice string) string {
	return eventually.Await(GinkgoT(), slice+" to carry a pull request", settleBudget,
		func() string { return stateOf(ctx, service, slice).GetPullRequestUrl() },
		func(url string) bool { return url != "" })
}

// finished delivers a session's terminal phase to the service.
func finished(service *dispatch.DispatchService, assignment string, phase harnessv1.AgentSessionPhase, turns uint32) {
	service.ObserveSession(&harnessv1.AgentSessionState{AssignmentId: assignment, Phase: phase, Turns: turns, Branch: "p1/branch", PullRequestUrl: "https://example.invalid/pull/" + assignment[:8]})
}
