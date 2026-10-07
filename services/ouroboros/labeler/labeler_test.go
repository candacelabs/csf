// Copyright 2026 Candace Labs

package labeler_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/io/ipc/docker"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/net/model/ollama"
	"github.com/candacelabs/csf/io/net/model/stub"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/eventually"
	ouroborosv1 "github.com/candacelabs/csf/proto/candace/ouroboros/v1"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/ouroboros/labeler"
)

// Integration specs: the labeler through its exported API over a state
// directory the spec lays out, a canned brain, a gomock launcher and gomock
// containers. Nothing crosses to a model, the Engine or a program.
const (
	specRepository  = "example/repo"
	specModel       = "qwen3:8b"
	specModelServer = "ollama-1"
	specMiner       = "pgrep_self_match_escape"
	specMinerBinary = "/opt/miners/pgrep_self_match_escape.exe"
	loopedRun       = "aaaaaaaa-0000-4000-8000-000000000001"
	quietRun        = "bbbbbbbb-0000-4000-8000-000000000002"
	gateLine        = `{"time":"2026-10-02T02:48:40Z","event_type":"session_gate_decision","msg":"session gate decision","gate":"wait","decision":"deny","command":"until ! pgrep -f session-id; do sleep 1; done"}`
	pushLine        = `{"time":"2026-10-02T03:00:05Z","event_type":"session_gate_decision","msg":"session gate decision","gate":"commit","decision":"allow","command":"git push"}`
	startedLine     = `{"time":"2026-10-02T02:48:35Z","event_type":"harness_run_started","msg":"harness run started"}`
	assistantLine   = `{"time":"2026-10-02T02:48:44Z","event_type":"assistant","msg":"turn executor event"}`
	cannedAnswer    = `{"labels":[{"candidate":1,"label":"+","quote":"until ! pgrep -f session-id","reason":"a self-matching loop"},{"candidate":2,"label":"-","quote":"git push","reason":"a push, not a loop"},{"candidate":2,"label":"+","quote":"not on this line","reason":"invented"},{"candidate":9,"label":"+","quote":"x","reason":"nowhere"}]}`
	gpuSample       = "37, 2048\n"
	minerFacts      = `{"relation":"run","args":[{"text":"` + loopedRun + `"}],"span":{"source":"x","line":1}}` + "\n" + `{"relation":"score","args":[{"text":"` + loopedRun + `"},{"number":1259}],"span":{"source":"x","line":2}}` + "\n"
	yieldBudget     = 10 * time.Second
	yieldPoll       = 10 * time.Millisecond
)

var specStart = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func specRequest() *ouroborosv1.LabelRequest {
	return &ouroborosv1.LabelRequest{
		Repository: specRepository, Ticket: 105, Proposal: "IC_2", Miner: specMiner, Corpus: []string{labeler.CorpusEvents},
		Predicate: "S0 GATE ESCAPE: pgrep self-match recurred in the ungated operator session",
		Facts:     []string{"gated(R) from session_gate_decision event type", "until_loop(R) from tool_call matching 'until !' pattern"},
	}
}

// ticketJSON is what gh issue view answers for the spec's ticket: a proposal
// comment with the miner, corpus and facts the spec request spells.
const ticketJSON = `{"title":"S0 GATE ESCAPE: pgrep self-match recurred in the ungated operator session","body":"The operator session looped.","comments":[{"id":"IC_1","body":"first"},{"id":"IC_2","body":"Miner proposal.\n\n| field | value |\n|---|---|\n| miner | ` + "`" + specMiner + "`" + ` |\n| corpus | jsonl |\n\nFacts:\n- ` + "`" + `gated(R) from session_gate_decision event type` + "`" + `\n- ` + "`" + `until_loop(R) from tool_call matching 'until !' pattern` + "`" + `\n\ndone\n"}]}`

// writeState lays out two run directories the way the harness does.
func writeState() (string, *iofs.HostFiles) {
	GinkgoHelper()
	directory := GinkgoT().TempDir()
	for run, lines := range map[string][]string{loopedRun: {startedLine, gateLine, assistantLine}, quietRun: {startedLine, pushLine}} {
		Expect(os.MkdirAll(filepath.Join(directory, run), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, run, session.EventsFile), []byte(strings.Join(lines, "\n")+"\n"), 0o600)).To(Succeed())
	}
	files, err := iofs.NewHostFiles(directory)
	Expect(err).NotTo(HaveOccurred())
	return directory, files
}

func cannedBrain(content string) *stub.CannedBrain[*ollama.Prompt, ollama.Answer] {
	return stub.NewCannedBrain[*ollama.Prompt, ollama.Answer](ollama.Answer{Content: content, Usage: ollama.Usage{Load: 4662908774, PromptTokens: 1000, Tokens: 80}})
}

func running(id string, name string) container.Summary {
	return container.Summary{ID: id, Names: []string{"/" + name}, State: container.StateRunning}
}

func gpuContainer() client.ContainerInspectResult {
	return client.ContainerInspectResult{Container: container.InspectResponse{
		HostConfig: &container.HostConfig{Resources: container.Resources{DeviceRequests: []container.DeviceRequest{{Driver: "nvidia", Capabilities: [][]string{{"compute", "utility"}}}}}},
	}}
}

// executable matches a launched command by its program.
func executable(name string) gomock.Matcher {
	return gomock.Cond(func(command proc.Command) bool { return command.Executable == name })
}

type recorded struct {
	labels []*ouroborosv1.ProposedLabel
	runs   []*ouroborosv1.LabelerRun
}

func (sink *recorded) label(label *ouroborosv1.ProposedLabel) error {
	sink.labels = append(sink.labels, label)
	return nil
}

func (sink *recorded) run(run *ouroborosv1.LabelerRun) error {
	sink.runs = append(sink.runs, run)
	return nil
}

func (sink *recorded) phases() []string {
	var phases []string
	for _, run := range sink.runs {
		phases = append(phases, run.GetPhase())
	}
	return phases
}

var _ = Describe("Labeling a ticket", func() {
	var controller *gomock.Controller
	var launcher *MockILauncher
	var containers *MockIContainers
	var manual *clock.ManualClock
	var sink *recorded
	var directory string
	var files *iofs.HostFiles

	BeforeEach(func() {
		controller = gomock.NewController(GinkgoT())
		launcher = NewMockILauncher(controller)
		containers = NewMockIContainers(controller)
		manual = clock.NewManualClock(specStart)
		sink = &recorded{}
		directory, files = writeState()
		launcher.EXPECT().Run(gomock.Any(), executable("nvidia-smi")).Return(proc.Result{Stdout: []byte(gpuSample)}, nil).AnyTimes()
	})

	build := func(options ...labeler.LabelerOption) *labeler.Labeler {
		GinkgoHelper()
		built, err := labeler.NewLabeler(append([]labeler.LabelerOption{
			labeler.WithBrain(cannedBrain(cannedAnswer), specModel),
			labeler.WithState(directory, files),
			labeler.WithLauncher(launcher),
			labeler.WithContainers(containers, specModelServer),
			labeler.WithClock(manual),
			labeler.WithLabelSink(sink.label),
			labeler.WithRunSink(sink.run),
		}, options...)...)
		Expect(err).NotTo(HaveOccurred())
		return built
	}

	It("keeps the positive that reproduces, records the rest and measures the run", func() {
		containers.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return(client.ContainerListResult{Items: []container.Summary{running("m1", specModelServer)}}, nil)
		labels, err := build().Label(context.Background(), specRequest())
		Expect(err).NotTo(HaveOccurred())
		Expect(labels).To(HaveLen(3))
		Expect(labels).To(Equal(sink.labels))

		Expect(labels[0].GetInstance()).To(Equal(loopedRun))
		Expect(labels[0].GetPositive()).To(BeTrue())
		Expect(labels[0].GetSpan()).To(Equal(&ouroborosv1.Span{Source: loopedRun + "/" + session.EventsFile, Line: 2}))
		Expect(labels[0].GetQuote()).To(Equal("until ! pgrep -f session-id"))
		Expect(labels[0].GetAcceptance()).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_REPRODUCED_SPAN))
		Expect(labels[0].GetModel()).To(Equal(specModel))
		Expect(labels[0].GetTicket()).To(Equal(int64(105)))

		Expect(labels[1].GetInstance()).To(Equal(quietRun))
		Expect(labels[1].GetPositive()).To(BeFalse())
		Expect(labels[1].GetAcceptance()).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_UNCHECKED))

		Expect(labels[2].GetInstance()).To(Equal(quietRun))
		Expect(labels[2].GetAcceptance()).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_REJECTED))
		Expect(labels[2].GetRejection()).To(Equal("quote is not verbatim on the line"))

		last := sink.runs[len(sink.runs)-1]
		Expect(last.GetPhase()).To(Equal(labeler.PhaseLabeling))
		Expect(last.GetCurrent()).To(Equal(int64(105)))
		Expect(last.GetTickets()).To(Equal(int64(1)))
		Expect(last.GetTicketsWithInstance()).To(Equal(int64(1)))
		Expect(last.GetProposed()).To(Equal(int64(4)))
		Expect(last.GetHeldOut()).To(Equal(int64(2)))
		Expect(last.GetAccepted()).To(Equal(int64(1)))
		Expect(last.GetRejected()).To(Equal(int64(2)))
		Expect(last.GetPrecision()).To(Equal(0.5))
		Expect(last.GetBatches()).To(Equal(int64(1)))
		Expect(last.GetGpuUtilization()).To(Equal(37.0))
		Expect(last.GetGpuMemoryBytes()).To(Equal(int64(2048 << 20)))
		Expect(last.GetLoadSeconds()).To(Equal(int64(4)))
		Expect(last.GetKeepAliveSeconds()).To(BeZero(), "no gap and no load were measured before the only batch")
		Expect(last.GetModel()).To(Equal(specModel))
		Expect(last.GetStarted()).To(Equal(specStart.Unix()))

		Expect(build().Finish()).To(Succeed())
		Expect(sink.runs[len(sink.runs)-1].GetPhase()).To(Equal(labeler.PhaseFinished))
	})

	It("yields the GPU while another consumer runs and labels once it has gone", func() {
		gomock.InOrder(
			containers.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return(client.ContainerListResult{Items: []container.Summary{running("m1", specModelServer), running("s1", "game-stream")}}, nil),
			containers.EXPECT().ContainerInspect(gomock.Any(), "s1", gomock.Any()).Return(gpuContainer(), nil),
			containers.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return(client.ContainerListResult{Items: []container.Summary{running("m1", specModelServer)}}, nil),
		)
		subject := build()
		done := make(chan error, 1)
		go func() { // Joined below: the spec reads done before it ends.
			_, err := subject.Label(context.Background(), specRequest())
			done <- err
		}()
		eventually.Await(GinkgoT(), "the labeler to arm its yield wait", eventually.Budget{Within: yieldBudget, Interval: yieldPoll}, manual.Waiting, func(waiting int) bool { return waiting == 1 })
		manual.Advance(labeler.DefaultYieldWait)
		Expect(<-done).To(Succeed())
		Expect(sink.phases()).To(ContainElement(labeler.PhaseYielding))
		Expect(sink.runs[len(sink.runs)-1].GetYields()).To(Equal(int64(1)))
		Expect(sink.labels).To(HaveLen(3))
	})

	It("does not yield to a container labelled as a model server, such as the search reranker", func() {
		reranker := running("r1", "csf-reranker-0123456789ab")
		reranker.Labels = map[string]string{docker.ModelServerLabel: "true"}
		// No inspect is expected: a labelled model server is never a consumer.
		containers.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return(client.ContainerListResult{Items: []container.Summary{running("m1", specModelServer), reranker}}, nil)
		labels, err := build().Label(context.Background(), specRequest())
		Expect(err).NotTo(HaveOccurred())
		Expect(labels).To(HaveLen(3))
		Expect(sink.phases()).NotTo(ContainElement(labeler.PhaseYielding))
	})

	It("accepts a positive through the ticket's miner when one is registered", func() {
		containers.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return(client.ContainerListResult{}, nil)
		launcher.EXPECT().Run(gomock.Any(), executable(specMinerBinary)).DoAndReturn(func(_ context.Context, command proc.Command) (proc.Result, error) {
			Expect(command.Arguments[0]).To(Equal("facts"))
			Expect(command.Arguments[1:]).To(ConsistOf(filepath.Join(directory, loopedRun, session.EventsFile), filepath.Join(directory, quietRun, session.EventsFile)))
			return proc.Result{Stdout: []byte(minerFacts)}, nil
		})
		request := specRequest()
		request.Miner = specMiner
		labels, err := build(labeler.WithMiner(specMiner, specMinerBinary)).Label(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
		Expect(labels[0].GetAcceptance()).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_REPRODUCED_FACT))
		Expect(labels[2].GetAcceptance()).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_REJECTED))
		Expect(labels[2].GetRejection()).To(Equal("the miner's extractor emits no fact for the instance"))
	})

	It("reports a brain that proposes nothing", func() {
		containers.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return(client.ContainerListResult{}, nil)
		subject := build(labeler.WithBrain(stub.NewCannedBrain[*ollama.Prompt, ollama.Answer](), specModel))
		_, err := subject.Label(context.Background(), specRequest())
		Expect(err).To(MatchError(ContainSubstring("proposed no action")))
	})

	It("completes a bare queue row by reading the ticket itself", func() {
		containers.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return(client.ContainerListResult{}, nil)
		launcher.EXPECT().Run(gomock.Any(), executable("gh")).DoAndReturn(func(_ context.Context, command proc.Command) (proc.Result, error) {
			Expect(command.Arguments).To(Equal([]string{"issue", "view", "105", "--repo", specRepository, "--json", "title,body,comments"}))
			return proc.Result{Stdout: []byte(ticketJSON)}, nil
		})
		request := &ouroborosv1.LabelRequest{Ticket: 105, Instances: []string{quietRun}, Reason: "no labeled instance exists"}
		labels, err := build(labeler.WithRepository(specRepository)).Label(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
		Expect(request.GetProposal()).To(Equal("IC_2"))
		Expect(request.GetMiner()).To(Equal(specMiner))
		Expect(request.GetCorpus()).To(Equal([]string{labeler.CorpusEvents}))
		Expect(request.GetPredicate()).To(HavePrefix("S0 GATE ESCAPE"))
		Expect(request.GetFacts()).To(HaveLen(2))
		Expect(request.GetReason()).To(Equal("no labeled instance exists"), "the loop's own fields are kept")
		Expect(labels).To(HaveLen(3))
	})

	It("refuses a missing or invalid request", func() {
		subject := build()
		_, err := subject.Label(context.Background(), nil)
		Expect(err).To(MatchError(labeler.ErrNoRequest))
		_, err = subject.Label(context.Background(), &ouroborosv1.LabelRequest{Repository: specRepository})
		Expect(err).To(MatchError(labeler.ErrNoRequest))
		_, err = subject.Label(context.Background(), &ouroborosv1.LabelRequest{Ticket: 7})
		Expect(err).To(MatchError(labeler.ErrNoRepository))
		request := specRequest()
		request.Miner = ""
		_, err = subject.Label(context.Background(), request)
		Expect(err).To(MatchError(ContainSubstring("miner")))
	})
})

var _ = Describe("Building the labeler", func() {
	It("requires the brain, the state directory, the launcher and the containers", func() {
		_, err := labeler.NewLabeler()
		Expect(err).To(MatchError(labeler.ErrMissingCapability))
	})

	It("refuses an option it cannot use", func() {
		for _, option := range []labeler.LabelerOption{nil, labeler.WithBatches(0), labeler.WithContextWindow(10), labeler.WithYieldWait(0), labeler.WithMiner("", "x"), labeler.WithCheckout("")} {
			_, err := labeler.NewLabeler(option)
			Expect(err).To(MatchError(labeler.ErrInvalidOption))
		}
	})
})
