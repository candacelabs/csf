// Copyright 2026 Candace Labs

package dispatch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/csf"
	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/dispatch"
	"github.com/candacelabs/csf/services/dispatch/mocks"
)

var _ = Describe("DispatchService", func() {
	var (
		ctx        context.Context
		controller *gomock.Controller
		harness    *harnessDouble
		baseline   goleak.Option
	)

	BeforeEach(func() {
		baseline = goleak.IgnoreCurrent()
		// Registered first, so it runs last: after every scope a spec opened
		// has closed.
		DeferCleanup(func() {
			Expect(goleak.Find(baseline)).To(Succeed(), "every goroutine the service started must join")
		})
		ctx = context.Background()
		controller = gomock.NewController(GinkgoT())
		harness = newHarnessDouble(controller)
		harness.workerCap = 2
	})

	// twoWide is a host with room for two sessions.
	twoWide := func(options ...dispatch.Option) *dispatch.DispatchService {
		service, _ := started(ctx, append([]dispatch.Option{
			dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}),
		}, options...)...)
		return service
	}

	Describe("construction and lifecycle", func() {
		It("requires the session host and rejects options it cannot use", func() {
			_, err := dispatch.NewDispatchService()
			Expect(err).To(MatchError(dispatch.ErrNoSessions))
			_, err = dispatch.NewDispatchService(dispatch.WithSessions(harness.sessions), dispatch.WithClock(nil))
			Expect(err).To(MatchError(dispatch.ErrInvalidOption))
			_, err = dispatch.NewDispatchService(dispatch.WithSessions(harness.sessions), dispatch.WithDatabase(nil))
			Expect(err).To(MatchError(dispatch.ErrInvalidOption))
			_, err = dispatch.NewDispatchService(dispatch.WithSessions(harness.sessions), nil)
			Expect(err).To(MatchError(dispatch.ErrInvalidOption))
			_, err = dispatch.NewPostgresDispatchDatabase(nil)
			Expect(err).To(MatchError(dispatch.ErrInvalidOption))
		})

		It("answers nothing before it is started and reports not running after it stopped", func() {
			service, err := dispatch.NewDispatchService(dispatch.WithSessions(harness.sessions))
			Expect(err).NotTo(HaveOccurred())
			bounded, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			_, err = service.List(bounded, &dispatchv1.ListSlicesRequest{})
			Expect(err).To(MatchError(context.DeadlineExceeded))

			scope := runtime.NewScope(ctx, "stopped")
			Expect(service.Start(scope)).To(Succeed())
			Expect(scope.Close()).To(Succeed())
			_, err = service.List(ctx, &dispatchv1.ListSlicesRequest{})
			Expect(err).To(MatchError(dispatch.ErrNotStarted))
		})
	})

	Describe("the slice graph", func() {
		It("rejects a cycle in depends_on, a self-edge, an unknown slice, a repeat and an unrunnable recipe", func() {
			service := twoWide()
			_, err := service.Enqueue(ctx, sliceRequest("a", nil))
			Expect(err).NotTo(HaveOccurred())
			_, err = service.Enqueue(ctx, sliceRequest("b", &dispatchv1.SliceEdges{DependsOn: []string{"a"}, RequiredBy: []string{"a"}}))
			Expect(err).To(MatchError(dispatch.ErrCycle))
			Expect(err).To(MatchError(csf.ErrInvalidRequest))
			_, err = service.Enqueue(ctx, sliceRequest("b", dependsOn("b")))
			Expect(err).To(MatchError(dispatch.ErrCycle))
			_, err = service.Enqueue(ctx, sliceRequest("b", dependsOn("missing")))
			Expect(err).To(MatchError(dispatch.ErrUnknownSlice))
			_, err = service.Enqueue(ctx, sliceRequest("a", nil))
			Expect(err).To(MatchError(dispatch.ErrSliceExists))
			_, err = service.Enqueue(ctx, &dispatchv1.EnqueueSliceRequest{})
			Expect(err).To(MatchError(dispatch.ErrInvalidSlice))
			noWorkspace := sliceRequest("c", nil)
			noWorkspace.Slice.Recipe.Workspace = nil
			_, err = service.Enqueue(ctx, noWorkspace)
			Expect(err).To(MatchError(dispatch.ErrInvalidSlice))

			listed, err := service.List(ctx, &dispatchv1.ListSlicesRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetNodes()).To(HaveLen(1), "a rejected slice leaves no node")
			Expect(listed.GetPersisted()).To(BeFalse())
			harness.submitted()
		})

		It("reports the priority breakdown: critical path, urgency, enqueue order and frontier rank", func() {
			service := twoWide()
			for _, request := range []*dispatchv1.EnqueueSliceRequest{
				sliceRequest("a", nil), sliceRequest("b", dependsOn("a")), sliceRequest("c", dependsOn("b")), sliceRequest("d", nil),
			} {
				_, err := service.Enqueue(ctx, request)
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(harness.submittedSet(2)).To(ConsistOf(assignmentOf("a"), assignmentOf("d")), "the frontier fills the cap")
			listed, err := service.List(ctx, &dispatchv1.ListSlicesRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetConcurrencyCap()).To(Equal(uint32(2)), "the harness check's worker cap")
			paths := map[string]uint32{}
			for _, node := range listed.GetNodes() {
				paths[node.GetSlice().GetSliceId()] = node.GetPriority().GetCriticalPath()
			}
			Expect(paths).To(Equal(map[string]uint32{"a": 3, "b": 2, "c": 1, "d": 1}))
			Expect(listed.GetNodes()[0].GetPriority().GetDeclaredOrder()).To(Equal(uint64(1)))
			Expect(listed.GetRunning()).To(Equal(uint32(2)))
			frontier, err := service.Frontier(ctx, &dispatchv1.GetFrontierRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(frontier.GetNodes()).To(BeEmpty(), "every ready slice runs")
		})
	})

	Describe("dispatch", func() {
		It("never runs two contending slices at once and respects the cap", func() {
			service := twoWide()
			_, err := service.Enqueue(ctx, sliceRequest("schema-a", nil, "001_init.sql"))
			Expect(err).NotTo(HaveOccurred())
			_, err = service.Enqueue(ctx, sliceRequest("schema-b", nil, "001_init.sql"))
			Expect(err).NotTo(HaveOccurred())
			_, err = service.Enqueue(ctx, sliceRequest("docs", nil, "README.md"))
			Expect(err).NotTo(HaveOccurred())
			_, err = service.Enqueue(ctx, sliceRequest("ontology", nil, "architecture.csf"))
			Expect(err).NotTo(HaveOccurred())
			Expect(harness.submittedSet(2)).To(ConsistOf(assignmentOf("schema-a"), assignmentOf("docs")), "schema-b contends with schema-a; docs takes the second machine")
			harness.nothingSubmitted()
			Expect(stateOf(ctx, service, "schema-b").GetState()).To(Equal(dispatchv1.SliceState_SLICE_STATE_QUEUED))
			Expect(stateOf(ctx, service, "schema-b").GetPriority().GetRank()).To(Equal(uint32(1)), "first in the frontier, held by contends")

			merged, err := service.MarkMerged(ctx, &dispatchv1.MarkSliceMergedRequest{SliceId: "docs", PullRequestUrl: "https://example.invalid/pull/3"})
			Expect(err).NotTo(HaveOccurred())
			Expect(merged.GetNodes()[0].GetState()).To(Equal(dispatchv1.SliceState_SLICE_STATE_MERGED))
			Eventually(harness.cancels, settleBudget.Within).Should(Receive(Equal(assignmentOf("docs"))), "a merged slice's session is asked to stop")
			Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("ontology")), "the freed machine takes the next non-contending slice")
			harness.nothingSubmitted()

			_, err = service.MarkMerged(ctx, &dispatchv1.MarkSliceMergedRequest{SliceId: "schema-a"})
			Expect(err).NotTo(HaveOccurred())
			Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("schema-b")), "schema-b runs once schema-a is out")
			listed, err := service.List(ctx, &dispatchv1.ListSlicesRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetRunning()).To(Equal(uint32(2)))
		})

		It("releases dependents when a predecessor merges and records session outcomes", func() {
			service := twoWide()
			_, err := service.Enqueue(ctx, sliceRequest("a", nil))
			Expect(err).NotTo(HaveOccurred())
			_, err = service.Enqueue(ctx, sliceRequest("b", dependsOn("a")))
			Expect(err).NotTo(HaveOccurred())
			Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("a")))
			harness.nothingSubmitted()

			service.ObserveSession(&harnessv1.AgentSessionState{AssignmentId: assignmentOf("a"), Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING, PullRequestUrl: "https://example.invalid/pull/1"})
			Expect(awaitPullRequest(ctx, service, "a")).To(Equal("https://example.invalid/pull/1"))
			released, err := service.MarkMerged(ctx, &dispatchv1.MarkSliceMergedRequest{SliceId: "a"})
			Expect(err).NotTo(HaveOccurred())
			Expect(released.GetNodes()).To(HaveLen(2), "the merged slice and the one it released")
			Expect(released.GetNodes()[1].GetSlice().GetSliceId()).To(Equal("b"))
			Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("b")))

			finished(service, assignmentOf("b"), harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED, 2)
			failed := awaitState(ctx, service, "b", dispatchv1.SliceState_SLICE_STATE_FAILED)
			Expect(failed.GetAssignmentId()).To(BeEmpty())
			_, err = service.MarkMerged(ctx, &dispatchv1.MarkSliceMergedRequest{SliceId: "a"})
			Expect(err).NotTo(HaveOccurred(), "merging twice is idempotent")
			_, err = service.MarkMerged(ctx, &dispatchv1.MarkSliceMergedRequest{SliceId: "nope"})
			Expect(err).To(MatchError(dispatch.ErrUnknownSlice))
		})

		It("marks merged the slice whose recorded pull request GitHub reports merged, and no other", func() {
			service := twoWide()
			_, err := service.Enqueue(ctx, sliceRequest("a", nil))
			Expect(err).NotTo(HaveOccurred())
			_, err = service.Enqueue(ctx, sliceRequest("b", dependsOn("a")))
			Expect(err).NotTo(HaveOccurred())
			Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("a")))
			service.ObserveSession(&harnessv1.AgentSessionState{AssignmentId: assignmentOf("a"), Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING, PullRequestUrl: "https://example.invalid/pull/1"})
			Expect(awaitPullRequest(ctx, service, "a")).To(Equal("https://example.invalid/pull/1"))

			marked, err := service.MarkPullRequestMerged(ctx, "https://example.invalid/pull/2")
			Expect(err).NotTo(HaveOccurred())
			Expect(marked).To(BeEmpty(), "no slice recorded that pull request")
			marked, err = service.MarkPullRequestMerged(ctx, "https://example.invalid/pull/1")
			Expect(err).NotTo(HaveOccurred())
			Expect(marked).To(Equal([]string{"a"}))
			Expect(stateOf(ctx, service, "a").GetState()).To(Equal(dispatchv1.SliceState_SLICE_STATE_MERGED))
			Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("b")), "the merge moved the frontier")
			marked, err = service.MarkPullRequestMerged(ctx, "https://example.invalid/pull/1")
			Expect(err).NotTo(HaveOccurred())
			Expect(marked).To(BeEmpty(), "a redelivered merge finds the slice merged already")
		})

		It("preempts a lower-ranked running session for an urgent intent at its safepoint, then re-enqueues it with a checkpoint", func() {
			harness.workerCap = 1
			oneWide, _ := started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}))
			_, err := oneWide.Enqueue(ctx, sliceRequest("slow", nil, "schema"))
			Expect(err).NotTo(HaveOccurred())
			Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("slow")))
			_, err = oneWide.Enqueue(ctx, sliceRequest("hotfix", nil, "ontology"))
			Expect(err).NotTo(HaveOccurred())
			harness.nothingSubmitted()

			declared, err := oneWide.DeclareIntent(ctx, &dispatchv1.DeclareIntentRequest{Intent: &dispatchv1.Intent{
				IntentId: "now", Statement: "ship the hotfix", Scope: "hotfix", Urgency: dispatchv1.Urgency_URGENCY_URGENT,
			}})
			Expect(err).NotTo(HaveOccurred())
			Expect(declared.GetNodes()).To(HaveLen(1))
			Expect(declared.GetNodes()[0].GetPriority().GetUrgency()).To(Equal(dispatchv1.Urgency_URGENCY_URGENT))
			Eventually(harness.cancels, settleBudget.Within).Should(Receive(Equal(assignmentOf("slow"))), "the running session is asked to stop at its safepoint")
			Expect(stateOf(ctx, oneWide, "slow").GetState()).To(Equal(dispatchv1.SliceState_SLICE_STATE_PREEMPTED))
			harness.nothingSubmitted()

			finished(oneWide, assignmentOf("slow"), harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED, 3)
			requeued := awaitState(ctx, oneWide, "slow", dispatchv1.SliceState_SLICE_STATE_QUEUED)
			Expect(requeued.GetCheckpoint()).To(ContainSubstring("attempt 1 stopped at a safepoint after 3 turns"))
			Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("hotfix")), "the urgent slice takes the machine")

			_, err = oneWide.MarkMerged(ctx, &dispatchv1.MarkSliceMergedRequest{SliceId: "hotfix"})
			Expect(err).NotTo(HaveOccurred())
			resumed := harness.submitted()
			Expect(resumed.GetAssignmentId()).To(Equal(resumedAssignmentOf("slow", 2)), "the second attempt runs under a derived assignment")
			Expect(resumed.GetWorkspace().GetBranch()).To(Equal("p1/slow-r2"))
			Expect(resumed.GetTask()).To(ContainSubstring("Resume from the checkpoint"))
			Expect(stateOf(ctx, oneWide, "slow").GetAttempts()).To(Equal(uint32(2)))
		})

		It("does not preempt for an urgent intent on a slice that ranks below what runs", func() {
			harness.workerCap = 1
			oneWide, _ := started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}))
			_, err := oneWide.Enqueue(ctx, sliceRequest("root", nil))
			Expect(err).NotTo(HaveOccurred())
			_, err = oneWide.Enqueue(ctx, sliceRequest("leaf", dependsOn("root")))
			Expect(err).NotTo(HaveOccurred())
			_, err = oneWide.Enqueue(ctx, sliceRequest("aside", nil))
			Expect(err).NotTo(HaveOccurred())
			Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("root")))
			_, err = oneWide.DeclareIntent(ctx, &dispatchv1.DeclareIntentRequest{Intent: &dispatchv1.Intent{IntentId: "aside-now", Statement: "do aside", Scope: "aside", Urgency: dispatchv1.Urgency_URGENCY_URGENT}})
			Expect(err).NotTo(HaveOccurred())
			Consistently(harness.cancels, quiet).ShouldNot(Receive(), "root's critical path outranks aside's urgency")
		})
	})

	Describe("routing", func() {
		It("steers into a running slice, queues onto a waiting one, creates a new one and answers already done", func() {
			ontology := filepath.Join(GinkgoT().TempDir(), "architecture.csf")
			Expect(os.WriteFile(ontology, []byte("term dispatch \"Dispatch\" \"...\";\nterm intent \"Intent\" \"...\";\n"), 0o600)).To(Succeed())
			service := twoWide(dispatch.WithOntologySource(ontology))
			_, err := service.Enqueue(ctx, sliceRequest("schema", nil, "001_init.sql"))
			Expect(err).NotTo(HaveOccurred())
			_, err = service.Enqueue(ctx, sliceRequest("views", dependsOn("schema"), "chat.html"))
			Expect(err).NotTo(HaveOccurred())
			Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("schema")))

			steered, err := service.Route(ctx, &dispatchv1.RouteMessageRequest{
				Intent:  &dispatchv1.Intent{IntentId: "i1", Statement: "add an index", Terms: []string{"001_init.sql"}},
				Message: "add an index on created_at",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(steered.GetDecision()).To(Equal(dispatchv1.RouteDecision_ROUTE_DECISION_STEER))
			Expect(steered.GetSliceId()).To(Equal("schema"))
			Expect(steered.GetTurnId()).To(Equal("2"))
			Expect(steered.GetNode().GetIntents()).To(HaveLen(1))

			queued, err := service.Route(ctx, &dispatchv1.RouteMessageRequest{
				Intent: &dispatchv1.Intent{IntentId: "i2", Statement: "theme the chat", Terms: []string{"chat.html"}, Urgency: dispatchv1.Urgency_URGENCY_HIGH},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(queued.GetDecision()).To(Equal(dispatchv1.RouteDecision_ROUTE_DECISION_QUEUE))
			Expect(queued.GetSliceId()).To(Equal("views"))
			Expect(queued.GetNode().GetPriority().GetUrgency()).To(Equal(dispatchv1.Urgency_URGENCY_HIGH))

			gap, err := service.Route(ctx, &dispatchv1.RouteMessageRequest{Intent: &dispatchv1.Intent{IntentId: "i3", Statement: "fix warden", Terms: []string{"warden"}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(gap.GetDecision()).To(Equal(dispatchv1.RouteDecision_ROUTE_DECISION_NEW))
			Expect(gap.GetNode()).To(BeNil())
			Expect(gap.GetFindings()).To(ContainElement(ContainSubstring("no slice's touch-set covers")))

			created, err := service.Route(ctx, &dispatchv1.RouteMessageRequest{
				Intent:   &dispatchv1.Intent{IntentId: "i3", Statement: "fix warden", Terms: []string{"warden"}},
				NewSlice: sliceRequest("warden", nil, "warden"),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(created.GetDecision()).To(Equal(dispatchv1.RouteDecision_ROUTE_DECISION_NEW))
			Expect(created.GetNode().GetSlice().GetSliceId()).To(Equal("warden"))
			Expect(created.GetNode().GetIntents()).To(HaveLen(1))
			Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("warden")))

			done, err := service.Route(ctx, &dispatchv1.RouteMessageRequest{Intent: &dispatchv1.Intent{IntentId: "i4", Statement: "define dispatch and intent", Scope: dispatch.ScopeOntology, Terms: []string{"dispatch", "intent"}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(done.GetDecision()).To(Equal(dispatchv1.RouteDecision_ROUTE_DECISION_ALREADY_DONE))
			Expect(done.GetReference()).To(Equal(ontology))
			Expect(done.GetFindings()).To(ContainElement(ContainSubstring("gates is not implemented")))

			missing, err := service.Route(ctx, &dispatchv1.RouteMessageRequest{Intent: &dispatchv1.Intent{IntentId: "i5", Statement: "define frontier", Scope: dispatch.ScopeOntology, Terms: []string{"dispatch", "frontier"}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(missing.GetDecision()).To(Equal(dispatchv1.RouteDecision_ROUTE_DECISION_NEW))
			Expect(missing.GetFindings()).To(ContainElement("term not defined in the ontology: frontier"))

			_, err = service.MarkMerged(ctx, &dispatchv1.MarkSliceMergedRequest{SliceId: "schema", PullRequestUrl: "https://example.invalid/pull/9"})
			Expect(err).NotTo(HaveOccurred())
			merged, err := service.Route(ctx, &dispatchv1.RouteMessageRequest{Intent: &dispatchv1.Intent{IntentId: "i6", Statement: "add the table", Terms: []string{"001_init.sql"}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(merged.GetDecision()).To(Equal(dispatchv1.RouteDecision_ROUTE_DECISION_ALREADY_DONE))
			Expect(merged.GetReference()).To(Equal("https://example.invalid/pull/9"))

			_, err = service.Route(ctx, &dispatchv1.RouteMessageRequest{})
			Expect(err).To(MatchError(dispatch.ErrInvalidIntent))
			_, err = service.Reprioritize(ctx, &dispatchv1.ReprioritizeRequest{Intent: &dispatchv1.Intent{IntentId: "never", Statement: "x"}})
			Expect(err).To(MatchError(dispatch.ErrUnknownIntent))
			_, err = service.DeclareIntent(ctx, &dispatchv1.DeclareIntentRequest{})
			Expect(err).To(MatchError(dispatch.ErrInvalidIntent))
		})

		It("lets a superseding intent replace the superseded one on its slice", func() {
			service := twoWide()
			_, err := service.Enqueue(ctx, sliceRequest("a", nil, "x"))
			Expect(err).NotTo(HaveOccurred())
			_, err = service.DeclareIntent(ctx, &dispatchv1.DeclareIntentRequest{Intent: &dispatchv1.Intent{IntentId: "first", Statement: "first", Scope: "a", Urgency: dispatchv1.Urgency_URGENCY_HIGH}})
			Expect(err).NotTo(HaveOccurred())
			changed, err := service.Reprioritize(ctx, &dispatchv1.ReprioritizeRequest{Intent: &dispatchv1.Intent{IntentId: "first", Statement: "first, calmer", Scope: "a", Urgency: dispatchv1.Urgency_URGENCY_LOW}})
			Expect(err).NotTo(HaveOccurred())
			Expect(changed.GetNodes()[0].GetIntents()).To(HaveLen(1))
			Expect(changed.GetNodes()[0].GetPriority().GetUrgency()).To(Equal(dispatchv1.Urgency_URGENCY_LOW))
			replaced, err := service.DeclareIntent(ctx, &dispatchv1.DeclareIntentRequest{Intent: &dispatchv1.Intent{IntentId: "second", Statement: "second", Scope: "a", Supersedes: "first"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(replaced.GetNodes()[0].GetIntents()).To(HaveLen(1))
			Expect(replaced.GetNodes()[0].GetIntents()[0].GetIntentId()).To(Equal("second"))
			harness.submitted()
		})
	})

	Describe("persistence", func() {
		It("survives a restart: the graph, its edges and intents come back and running slices return to the frontier", func() {
			database := openDatabase(ctx)
			first, err := dispatch.NewDispatchService(dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}), dispatch.WithDatabase(database))
			Expect(err).NotTo(HaveOccurred())
			firstScope := runtime.NewScope(ctx, "first")
			Expect(first.Start(firstScope)).To(Succeed())
			_, err = first.Enqueue(ctx, sliceRequest("a", nil, "schema"))
			Expect(err).NotTo(HaveOccurred())
			_, err = first.Enqueue(ctx, sliceRequest("b", dependsOn("a"), "schema"))
			Expect(err).NotTo(HaveOccurred())
			_, err = first.Enqueue(ctx, sliceRequest("c", &dispatchv1.SliceEdges{Contends: []string{"a"}}, "docs"))
			Expect(err).NotTo(HaveOccurred())
			_, err = first.DeclareIntent(ctx, &dispatchv1.DeclareIntentRequest{Intent: &dispatchv1.Intent{IntentId: "keep", Statement: "keep b", Scope: "b", Urgency: dispatchv1.Urgency_URGENCY_HIGH}})
			Expect(err).NotTo(HaveOccurred())
			Expect(harness.submitted().GetAssignmentId()).To(Equal(assignmentOf("a")))
			harness.nothingSubmitted()
			listed, err := first.List(ctx, &dispatchv1.ListSlicesRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetPersisted()).To(BeTrue())
			Expect(firstScope.Close()).To(Succeed())

			second, _ := started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithClock(fakeClock{}), dispatch.WithDatabase(database))
			restored, err := second.List(ctx, &dispatchv1.ListSlicesRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(restored.GetNodes()).To(HaveLen(3))
			ids := []string{}
			for _, node := range restored.GetNodes() {
				ids = append(ids, node.GetSlice().GetSliceId())
			}
			Expect(ids).To(Equal([]string{"a", "b", "c"}), "enqueue order survives")
			Expect(restored.GetNodes()[1].GetEdges().GetDependsOn()).To(Equal([]string{"a"}))
			Expect(restored.GetNodes()[1].GetIntents()).To(HaveLen(1))
			Expect(restored.GetNodes()[1].GetPriority().GetUrgency()).To(Equal(dispatchv1.Urgency_URGENCY_HIGH))
			Expect(restored.GetNodes()[2].GetEdges().GetContends()).To(Equal([]string{"a"}))
			Expect(restored.GetNodes()[0].GetCheckpoint()).To(ContainSubstring("attempt 1"), "the slice that was running was checkpointed")
			resumed := harness.submitted()
			Expect(resumed.GetAssignmentId()).To(Equal(resumedAssignmentOf("a", 2)), "the restored frontier is dispatched again")
			Expect(stateOf(ctx, second, "a").GetAttempts()).To(Equal(uint32(2)))
			harness.nothingSubmitted()
			Expect(stateOf(ctx, second, "c").GetState()).To(Equal(dispatchv1.SliceState_SLICE_STATE_QUEUED), "c still contends with a")
		})

		It("keeps the graph unchanged when the database refuses a write", func() {
			database := mocks.NewMockIDispatchDatabase(controller)
			database.EXPECT().ListSlices(gomock.Any()).Return(nil, nil)
			database.EXPECT().ListSliceEdges(gomock.Any()).Return(nil, nil)
			database.EXPECT().ListIntents(gomock.Any()).Return(nil, nil)
			database.EXPECT().ListDispatchControls(gomock.Any()).Return(nil, nil)
			refused := errors.New("disk full")
			database.EXPECT().Transact(gomock.Any(), gomock.Any()).Return(refused)
			service := twoWide(dispatch.WithDatabase(database))
			_, err := service.Enqueue(ctx, sliceRequest("a", nil))
			Expect(err).To(MatchError(refused))
			listed, err := service.List(ctx, &dispatchv1.ListSlicesRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetNodes()).To(BeEmpty())
			harness.nothingSubmitted()
		})

		It("fails to start on a database it cannot read", func() {
			database := mocks.NewMockIDispatchDatabase(controller)
			unreadable := errors.New("connection refused")
			database.EXPECT().ListSlices(gomock.Any()).Return(nil, unreadable)
			service, err := dispatch.NewDispatchService(dispatch.WithSessions(harness.sessions), dispatch.WithDatabase(database))
			Expect(err).NotTo(HaveOccurred())
			scope := runtime.NewScope(ctx, "unreadable")
			Expect(service.Start(scope)).To(MatchError(unreadable))
			Expect(scope.Close()).To(Succeed())
		})
	})
})
