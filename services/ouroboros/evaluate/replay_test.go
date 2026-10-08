// Copyright 2026 Candace Labs

package evaluate_test

import (
	"context"
	"encoding/json"
	"path"
	"testing/fstest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/kernel/clock"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/ouroboros/evaluate"
)

var _ = Describe("Replaying the suite on a host", func() {
	const build = "cccccccccccc"
	var (
		ctx       context.Context
		state     fstest.MapFS
		launcher  *MockILauncher
		suite     evaluate.Suite
		submitted []*pb.AgentAssignmentRecipe
		canceled  []string
		phase     harnessv1.AgentSessionPhase
		calls     int
		held      string
		host      evaluate.Host
	)

	BeforeEach(func() {
		ctx = context.Background()
		launcher = NewMockILauncher(gomock.NewController(GinkgoT()))
		state = fstest.MapFS{"run-1/recipe.json": {Data: recipe(1)}, "run-2/recipe.json": {Data: recipe(2)}}
		first, second := ticket(1, 100, 1), ticket(2, 100, 1)
		first.Assignment, second.Assignment = "run-1", "run-2"
		suite = evaluate.Suite{Version: 4, Model: "claude-opus-5-5", Tickets: []evaluate.Ticket{first, second},
			Derivation: evaluate.Derivation{Budget: 20}}
		submitted, canceled, phase, calls, held = nil, nil, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING, 40, ""
		host = evaluate.Host{
			Admit: func(_ context.Context, request *harnessv1.CheckAgentSessionAdmissionRequest) (*harnessv1.CheckAgentSessionAdmissionResponse, error) {
				Expect(request.GetRecipe()).NotTo(BeNil())
				return &harnessv1.CheckAgentSessionAdmissionResponse{Held: held, Check: &harnessv1.LaunchCheck{Admitted: true}}, nil
			},
			Submit: func(_ context.Context, request *harnessv1.SubmitAgentSessionRequest) (*harnessv1.SubmitAgentSessionResponse, error) {
				submitted = append(submitted, request.GetRecipe())
				state[path.Join(request.GetRecipe().GetAssignmentId(), "events.jsonl")] = &fstest.MapFile{Data: eventLog(calls, 1, 30)}
				return &harnessv1.SubmitAgentSessionResponse{}, nil
			},
			Get: func(_ context.Context, request *harnessv1.GetAgentSessionRequest) (*harnessv1.GetAgentSessionResponse, error) {
				return &harnessv1.GetAgentSessionResponse{Session: &harnessv1.AgentSessionState{Phase: phase}}, nil
			},
			Cancel: func(_ context.Context, request *harnessv1.CancelAgentSessionRequest) (*harnessv1.CancelAgentSessionResponse, error) {
				canceled = append(canceled, request.GetAssignmentId())
				return &harnessv1.CancelAgentSessionResponse{}, nil
			},
		}
	})

	newReplayer := func() *evaluate.Replayer {
		replayer, err := evaluate.NewReplayer(evaluate.WithHost(host), evaluate.WithLauncher(launcher),
			evaluate.WithState("/state", state), evaluate.WithClock(clock.NewManualClock(specStart), time.Minute), evaluate.WithParallel(2))
		Expect(err).NotTo(HaveOccurred())
		return replayer
	}

	It("replays each ticket from its base commit, on the suite's model and tools, in the replay repository", func() {
		jobs, err := evaluate.Jobs(suite, state, build, "/eval/repository")
		Expect(err).NotTo(HaveOccurred())
		Expect(jobs).To(HaveLen(2))
		var replayed pb.AgentAssignmentRecipe
		Expect(protojson.Unmarshal(jobs[0].Recipe, &replayed)).To(Succeed())
		workspace := replayed.GetWorkspace()
		Expect(workspace.GetRepositoryPath()).To(Equal("/eval/repository"))
		Expect(workspace.GetBaseBranch()).To(Equal(suite.Tickets[0].BaseCommit))
		Expect(workspace.GetBranch()).To(Equal("eval/cccccccccccc/v4/01"))
		Expect(workspace.GetBriefPath()).To(BeEmpty())
		Expect(workspace.GetAllowedTools()).To(Equal(evaluate.ReplayTools))
		Expect(replayed.GetModel()).To(Equal("claude-opus-5-5"))
		Expect(replayed.GetTask()).To(Equal("the brief"))
		Expect(replayed.GetAssignmentId()).NotTo(Equal("11111111-2222-4333-8444-555555555555"))

		moved, err := evaluate.Relocate(jobs, "/burst/repository")
		Expect(err).NotTo(HaveOccurred())
		Expect(protojson.Unmarshal(moved[0].Recipe, &replayed)).To(Succeed())
		Expect(replayed.GetWorkspace().GetRepositoryPath()).To(Equal("/burst/repository"))
	})

	It("refuses a ticket whose original run left no recipe with the defined error", func() {
		delete(state, "run-2/recipe.json")
		_, err := evaluate.Jobs(suite, state, build, "/eval/repository")
		Expect(err).To(MatchError(evaluate.ErrNoOriginal))
	})

	It("cancels a replay once its budget is spent and records it read at the budget", func() {
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).Return(proc.Result{Stdout: []byte("a.go\nz.go\n")}, nil).Times(2)
		jobs, err := evaluate.Jobs(suite, state, build, "/eval/repository")
		Expect(err).NotTo(HaveOccurred())
		var recorded []evaluate.Replay
		Expect(newReplayer().Run(ctx, build, suite, evaluate.NodeHost, jobs, func(_ context.Context, replay evaluate.Replay) error {
			recorded = append(recorded, replay)
			return nil
		})).To(Succeed())
		Expect(submitted).To(HaveLen(2))
		Expect(canceled).To(HaveLen(2))
		Expect(recorded).To(HaveLen(2))
		for _, replay := range recorded {
			Expect(replay.ToolCalls).To(Equal(int64(20)))
			Expect(replay.Episodes).To(Equal(int64(1)))
			Expect(replay.Recall).To(Equal(0.5))
			Expect(replay.Node).To(Equal(evaluate.NodeHost))
			Expect(replay.Build).To(Equal(build))
		}
	})

	It("records a replay that ended under its budget without canceling it again", func() {
		phase, calls = harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED, 10
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).Return(proc.Result{}, nil).Times(2)
		jobs, err := evaluate.Jobs(suite, state, build, "/eval/repository")
		Expect(err).NotTo(HaveOccurred())
		var recorded []evaluate.Replay
		Expect(newReplayer().Run(ctx, build, suite, evaluate.NodeHost, jobs, func(_ context.Context, replay evaluate.Replay) error {
			recorded = append(recorded, replay)
			return nil
		})).To(Succeed())
		Expect(canceled).To(BeEmpty())
		Expect(recorded).To(HaveLen(2))
		Expect(recorded[0].ToolCalls).To(Equal(int64(10)))
	})

	It("submits nothing while the host holds new sessions, and stops with the context", func() {
		held = "the operator paused the dispatcher"
		jobs, err := evaluate.Jobs(suite, state, build, "/eval/repository")
		Expect(err).NotTo(HaveOccurred())
		canceledContext, cancel := context.WithCancel(ctx)
		cancel()
		err = newReplayer().Run(canceledContext, build, suite, evaluate.NodeHost, jobs, func(ctx context.Context, replay evaluate.Replay) error { return nil })
		Expect(err).To(MatchError(context.Canceled))
		Expect(submitted).To(BeEmpty())
	})

	It("refuses a replayer missing a capability with the defined error", func() {
		_, err := evaluate.NewReplayer(evaluate.WithLauncher(launcher))
		Expect(err).To(MatchError(evaluate.ErrInvalidOption))
		_, err = evaluate.NewReplayer(evaluate.WithHost(evaluate.Host{}))
		Expect(err).To(MatchError(evaluate.ErrInvalidOption))
		_, err = evaluate.NewReplayer(nil)
		Expect(err).To(MatchError(evaluate.ErrInvalidOption))
	})

	It("encodes jobs as data a burst node reads back", func() {
		jobs, err := evaluate.Jobs(suite, state, build, "/eval/repository")
		Expect(err).NotTo(HaveOccurred())
		encoded, err := json.Marshal(jobs)
		Expect(err).NotTo(HaveOccurred())
		var decoded []evaluate.Job
		Expect(json.Unmarshal(encoded, &decoded)).To(Succeed())
		Expect(decoded[1].Ticket.Number).To(Equal(int64(2)))
	})
})
