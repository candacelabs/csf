// Copyright 2026 Candace Labs

package opsview_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing/fstest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/kernel/clock"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/ipc/proc"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/opsview"
)

// The Workbench: the same page given the control plane. Every action is a
// call on the gomock control plane, which stands where csf serve grants the
// CSF service; the page's outcome is read off the live wire.
const (
	sampleTemplate = `{"agent":{"id":"scratch","revision":1,"instructions":"Work in the worktree."},"model":"sonnet","repositoryId":"scratch",` +
		`"workspace":{"repositoryPath":"/repository","baseBranch":"main","branch":"wb/sample","allowedTools":["Bash"],"pullRequestTitle":"Sample","briefPath":"brief.md"}}`
	sampleBrief = "Add one line to notes.txt and commit it.\n"
	ticketURL   = "https://example.invalid/issues/328"

	readyLine       = `{"time":"2026-10-05T01:00:00Z","level":"INFO","msg":"control action: ready","event_type":"harness_control_action","action":"ready","pull_request_url":"https://example.invalid/pull/7"}` + "\n"
	mergedLine      = `{"time":"2026-10-05T01:05:00Z","level":"INFO","msg":"control action: merge","event_type":"harness_control_action","action":"merge","pull_request_url":"https://example.invalid/pull/7"}` + "\n"
	refusedSendLine = `{"time":"2026-10-05T01:06:00Z","level":"ERROR","msg":"control action: send","event_type":"harness_control_action","action":"send","operator_authored":true,"turn_id":"","error":"the session has finished"}` + "\n"
	firstResult     = `{"time":"2026-10-04T23:50:00Z","event_type":"result","turn":1,"event":{"type":"result","subtype":"success","total_cost_usd":1.25,"duration_ms":1000}}` + "\n"
	secondResult    = `{"time":"2026-10-05T00:10:00Z","event_type":"result","turn":2,"event":{"type":"result","subtype":"success","total_cost_usd":2.0,"duration_ms":1000}}` + "\n"
	reopenedResult  = `{"time":"2026-10-05T00:20:00Z","event_type":"result","turn":3,"event":{"type":"result","subtype":"success","total_cost_usd":0.5,"duration_ms":1000}}` + "\n"
	publishedLine   = `{"time":"2026-10-02T02:49:00Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"out","event_type":"system","event":{"type":"system","subtype":"code_change_published","url":"https://example.invalid/pull/7"}}` + "\n"

	githubPublishedLine = `{"time":"2026-10-05T02:49:00Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"out","event_type":"system","event":{"type":"system","subtype":"code_change_published","url":"https://github.com/candacelabs/repository/pull/9"}}` + "\n"
	finishedLine        = `{"time":"2026-10-05T02:50:00Z","level":"INFO","msg":"harness run finished","turn":1,"event_type":"harness_run_finished"}` + "\n"
	mergedListing       = `[{"number":9,"title":"Fix the gate","url":"https://github.com/candacelabs/repository/pull/9","mergedAt":"2026-10-05T03:00:00Z"},` +
		`{"number":8,"title":"Yesterday","url":"https://github.com/candacelabs/repository/pull/8","mergedAt":"2026-10-04T23:00:00Z"}]`
	openListing = `[{"url":"https://github.com/candacelabs/repository/pull/9","additions":12,"deletions":3,"changedFiles":2,` +
		`"statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"},{"status":"COMPLETED","conclusion":"FAILURE"}]}]`

	dispatchSnapshot = `{"at":"2026-10-05T01:00:00Z","paused":false,"capacity":4,"limits":[],"queue":[{"slice_id":"wb-polish","title":"WORKBENCH-POLISH","state":"queued","rank":1,"critical_path":1,"urgency":"normal","attempts":0,"waiting":"depends on wb-control"}],"running":[{"slice_id":"wb-control","title":"WORKBENCH-CONTROL","state":"running","critical_path":2,"urgency":"normal","attempts":1,"assignment_id":"` + firstAssignment + `"}],"held":[]}`
)

func templatesWithSample() *iofs.HostFiles {
	GinkgoHelper()
	directory := GinkgoT().TempDir()
	Expect(os.MkdirAll(filepath.Join(directory, "sample"), 0o700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(directory, "sample", opsview.RecipeFile), []byte(sampleTemplate), 0o600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(directory, "sample", "brief.md"), []byte(sampleBrief), 0o600)).To(Succeed())
	files, err := iofs.NewHostFiles(directory)
	Expect(err).NotTo(HaveOccurred())
	return files
}

// expectForgeFacts holds when a card's markup says its pull request merged and
// carries the diff stat and the checks the forge lists for it.
func expectForgeFacts(html string) {
	GinkgoHelper()
	Expect(html).To(ContainSubstring(`data-opsview="pr">PR #9 merged<`))
	Expect(html).To(ContainSubstring(`<span class="plus">+12</span> <span class="minus">−3</span> · 2 files`), "the row's diff stat, as the forge lists it")
	Expect(html).To(ContainSubstring(`data-opsview="checks">checks failing<`), "one failed check fails the rollup")
}

var _ = Describe("The Workbench's card actions", func() {
	var (
		directory  string
		watcher    *specWatcher
		operations *MockIWorkbenchOperations
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		writeSession(directory, firstAssignment, strings.ReplaceAll(startedLine, "%s", firstAssignment), requestedLine, oneToolLine, publishedLine)
		controller := gomock.NewController(GinkgoT())
		watcher = newSpecWatcher(controller)
		operations = NewMockIWorkbenchOperations(controller)
		operations.EXPECT().GetWorkbenchTheme(gomock.Any(), gomock.Any()).Return(&pb.GetWorkbenchThemeResponse{}, nil).AnyTimes()
		operations.EXPECT().ListAgentSessions(gomock.Any(), gomock.Any()).Return(&harnessv1.ListAgentSessionsResponse{}, nil).AnyTimes()
	})

	It("sends the operator's message, marks ready, and merges and cancels only once confirmed, each through one operation", func() {
		operations.EXPECT().SendAgentSessionMessage(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, request *harnessv1.SendAgentSessionMessageRequest) (*harnessv1.SendAgentSessionMessageResponse, error) {
			Expect(request.GetAssignmentId()).To(Equal(firstAssignment))
			Expect(request.GetMessage()).To(Equal("Rebase on main."))
			Expect(request.GetOperatorAuthored()).To(BeTrue(), "what the Workbench sends is the operator's own words")
			return &harnessv1.SendAgentSessionMessageResponse{TurnId: "2"}, nil
		})
		operations.EXPECT().ReadyAgentSessionPullRequest(gomock.Any(), gomock.Cond(func(request *harnessv1.ReadyAgentSessionPullRequestRequest) bool {
			return request.GetAssignmentId() == firstAssignment
		})).Return(&harnessv1.ReadyAgentSessionPullRequestResponse{}, nil)
		release := make(chan struct{})
		operations.EXPECT().MergeAgentSessionPullRequest(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ *harnessv1.MergeAgentSessionPullRequestRequest) (*harnessv1.MergeAgentSessionPullRequestResponse, error) {
			<-release
			return nil, errors.New("merge-pr: REFUSED: the ontology penalty rose")
		}).Times(1)
		operations.EXPECT().CancelAgentSession(gomock.Any(), gomock.Any()).Return(&harnessv1.CancelAgentSessionResponse{
			Session: &harnessv1.AgentSessionState{Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELING}}, nil).Times(1)

		client := connect(mountView(directory, watcher.mock, opsview.WithOperations(operations)))
		board := client.WaitFor(opsview.BoardRegion, showsToolCalls("1"))
		client.Ack(board.Patch.ServerSeq)
		html, _ := board.Patch.Fragment(opsview.BoardRegion)
		Expect(html).NotTo(ContainSubstring(`data-opsview="message"`), "the composer opens on demand")
		client.Send(opsview.EventExpand, firstRegion, nil)
		card := client.WaitFor(firstRegion, contains(`data-opsview="ready"`))
		client.Ack(card.Patch.ServerSeq)
		client.Send(opsview.EventCompose, firstRegion, nil)
		card = client.WaitFor(firstRegion, contains(`data-opsview="message"`))
		client.Ack(card.Patch.ServerSeq)

		client.Send(opsview.EventSend, firstRegion, map[string]string{opsview.FieldMessage: "  "})
		card = client.WaitFor(firstRegion, contains("Write a message first."))
		client.Ack(card.Patch.ServerSeq)
		client.Send(opsview.EventSend, firstRegion, map[string]string{opsview.FieldMessage: "Rebase on main."})
		card = client.WaitFor(firstRegion, contains("Sent: queued as turn 2."))
		client.Ack(card.Patch.ServerSeq)
		client.Send(opsview.EventReady, firstRegion, nil)
		card = client.WaitFor(firstRegion, contains("Pull request marked ready."))
		client.Ack(card.Patch.ServerSeq)

		client.Send(opsview.EventMerge, firstRegion, nil)
		card = client.WaitFor(firstRegion, contains(`aria-label="confirm merge"`))
		client.Ack(card.Patch.ServerSeq)
		html, _ = card.Patch.Fragment(firstRegion)
		Expect(html).To(ContainSubstring("Merge pull request #7 from <strong>scratch</strong>?"), "a merge asks first, naming the session")
		client.Send(opsview.EventMerge, firstRegion, nil)
		card = client.WaitFor(firstRegion, contains("Merging…"))
		client.Ack(card.Patch.ServerSeq)
		html, _ = card.Patch.Fragment(firstRegion)
		Expect(html).To(ContainSubstring(`data-opsview="cancel" `), "the card shows the merge's progress")
		Expect(html).To(MatchRegexp(`data-opsview="cancel" [^>]* disabled`), "nothing else runs while it waits for the typed result")
		close(release)
		card = client.WaitFor(firstRegion, contains("Merge failed: REFUSED: the ontology penalty rose"))
		client.Ack(card.Patch.ServerSeq)

		client.Send(opsview.EventCancel, firstRegion, nil)
		card = client.WaitFor(firstRegion, contains(`aria-label="confirm cancel"`))
		client.Ack(card.Patch.ServerSeq)
		client.Send(opsview.EventConfirm, firstRegion, map[string]string{opsview.FieldAction: ""})
		card = client.WaitFor(firstRegion, func(html string) bool { return !strings.Contains(html, `aria-label="confirm cancel"`) })
		client.Ack(card.Patch.ServerSeq)
		client.Send(opsview.EventConfirm, firstRegion, map[string]string{opsview.FieldAction: opsview.EventCancel})
		client.Ack(client.WaitFor(firstRegion, contains(`aria-label="confirm cancel"`)).Patch.ServerSeq)
		client.Send(opsview.EventCancel, firstRegion, nil)
		card = client.WaitFor(firstRegion, contains("Canceled: the session is canceling."))
		client.Ack(card.Patch.ServerSeq)

		appendLine(directory, firstAssignment, readyLine)
		watcher.changes <- iofs.Change{Name: firstAssignment + "/" + session.EventsFile, Op: iofs.ChangeWritten}
		card = client.WaitFor(firstRegion, func(html string) bool {
			return !strings.Contains(html, `data-opsview="ready"`) && strings.Contains(html, `data-opsview="pr">PR #7 ready<`)
		})
		html, _ = card.Patch.Fragment(firstRegion)
		Expect(html).To(ContainSubstring("Canceled: the session is canceling."), "the notice survives the card being replaced")
		Expect(html).To(ContainSubstring(`data-opsview="merge"`), "a ready pull request can still be merged")
	})

	It("is read-only without the control plane: no controls, and a forged action changes nothing", func() {
		client := connect(mountView(directory, watcher.mock))
		board := client.WaitFor(opsview.BoardRegion, showsToolCalls("1"))
		client.Ack(board.Patch.ServerSeq)
		client.Send(opsview.EventExpand, firstRegion, nil)
		card := client.WaitFor(firstRegion, contains(`aria-expanded="true" data-opsview="expand"`))
		client.Ack(card.Patch.ServerSeq)
		html, _ := card.Patch.Fragment(firstRegion)
		Expect(html).NotTo(ContainSubstring(`data-opsview="compose"`))
		Expect(html).NotTo(ContainSubstring(`data-opsview="merge"`))
		client.Send(opsview.EventCancel, firstRegion, nil)
		card = client.WaitFor(firstRegion, contains("read-only"))
		client.Ack(card.Patch.ServerSeq)
	})

	It("refuses construction with a nil control plane or template capability", func() {
		files, err := iofs.NewHostFiles(directory)
		Expect(err).NotTo(HaveOccurred())
		_, err = opsview.NewOpsView(files, watcher.mock, []string{specOrigin}, nil, opsview.WithOperations(nil))
		Expect(err).To(MatchError(opsview.ErrNoOperations))
		_, err = opsview.NewOpsView(files, watcher.mock, []string{specOrigin}, nil, opsview.WithRecipeTemplates(nil))
		Expect(err).To(MatchError(opsview.ErrNoTemplates))
		_, err = opsview.NewOpsView(files, watcher.mock, []string{specOrigin}, nil, nil)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("The Workbench's launch form", func() {
	var (
		directory  string
		watcher    *specWatcher
		operations *MockIWorkbenchOperations
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		controller := gomock.NewController(GinkgoT())
		watcher = newSpecWatcher(controller)
		operations = NewMockIWorkbenchOperations(controller)
		operations.EXPECT().GetWorkbenchTheme(gomock.Any(), gomock.Any()).Return(&pb.GetWorkbenchThemeResponse{}, nil).AnyTimes()
		operations.EXPECT().ListAgentSessions(gomock.Any(), gomock.Any()).Return(&harnessv1.ListAgentSessionsResponse{}, nil).AnyTimes()
	})

	It("checks a ticket and template, shows the admission check, then submits exactly the recipe it checked", func() {
		var checked *pb.AgentAssignmentRecipe
		operations.EXPECT().CheckAgentSessionAdmission(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, request *harnessv1.CheckAgentSessionAdmissionRequest) (*harnessv1.CheckAgentSessionAdmissionResponse, error) {
			checked = request.GetRecipe()
			return &harnessv1.CheckAgentSessionAdmissionResponse{Check: &harnessv1.LaunchCheck{Cores: 32, WorkerCap: 30, Admitted: false, ReportOnly: true, Findings: []string{"no run directory measured yet, so the disk floor is unknown"}}}, nil
		})
		operations.EXPECT().SubmitAgentSession(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, request *harnessv1.SubmitAgentSessionRequest) (*harnessv1.SubmitAgentSessionResponse, error) {
			Expect(request.GetRecipe().GetAssignmentId()).To(Equal(checked.GetAssignmentId()), "the session launched is the one whose check was shown")
			return &harnessv1.SubmitAgentSessionResponse{Session: &harnessv1.AgentSessionState{AssignmentId: request.GetRecipe().GetAssignmentId()}}, nil
		})

		client := connect(mountView(directory, watcher.mock, opsview.WithOperations(operations), opsview.WithRecipeTemplates(templatesWithSample())))
		client.Send(opsview.EventSection, opsview.LaunchRegion, map[string]string{opsview.FieldSection: "launch"})
		form := client.WaitFor(opsview.LaunchRegion, contains(`<option value="sample"`))
		client.Ack(form.Patch.ServerSeq)

		client.Send(opsview.EventLaunch, opsview.LaunchRegion, nil)
		form = client.WaitFor(opsview.LaunchRegion, contains("Check a ticket and template first."))
		client.Ack(form.Patch.ServerSeq)

		client.Send(opsview.EventCheck, opsview.LaunchRegion, map[string]string{opsview.FieldTicket: ticketURL, opsview.FieldTemplate: "sample"})
		form = client.WaitFor(opsview.LaunchRegion, contains(`data-opsview="launch"`))
		client.Ack(form.Patch.ServerSeq)
		html, _ := form.Patch.Fragment(opsview.LaunchRegion)
		Expect(html).To(ContainSubstring(`data-opsview="admitted">no (report only)<`))
		Expect(html).To(ContainSubstring("the disk floor is unknown"))
		Expect(checked.GetTicketUrl()).To(Equal(ticketURL))
		Expect(checked.GetTask()).To(Equal(sampleBrief))
		Expect(checked.GetWorkspace().GetBranch()).To(Equal("wb/sample-" + checked.GetAssignmentId()[:8]))

		client.Send(opsview.EventLaunch, opsview.LaunchRegion, nil)
		form = client.WaitFor(opsview.LaunchRegion, contains("Launched "+checked.GetAssignmentId()))
		client.Ack(form.Patch.ServerSeq)
	})

	It("switches the default executor through one operation and says what new sessions run on", func() {
		operations.EXPECT().SetAgentExecutorDefault(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, request *harnessv1.SetAgentExecutorDefaultRequest) (*harnessv1.SetAgentExecutorDefaultResponse, error) {
			Expect(request.GetExecutorDefault().GetExecutor()).To(Equal("copilot"))
			return &harnessv1.SetAgentExecutorDefaultResponse{ExecutorDefault: request.GetExecutorDefault(), Previous: &harnessv1.AgentExecutorDefault{Executor: "claude-code"}}, nil
		})

		client := connect(mountView(directory, watcher.mock, opsview.WithOperations(operations), opsview.WithRecipeTemplates(templatesWithSample())))
		client.Send(opsview.EventSection, opsview.LaunchRegion, map[string]string{opsview.FieldSection: "launch"})
		form := client.WaitFor(opsview.LaunchRegion, contains(`data-opsview="switch-executor"`))
		client.Ack(form.Patch.ServerSeq)

		client.Send(opsview.EventExecutor, opsview.LaunchRegion, map[string]string{opsview.FieldExecutor: "copilot", opsview.FieldModel: " claude-haiku-4.5 "})
		form = client.WaitFor(opsview.LaunchRegion, contains("New sessions run on copilot (claude-haiku-4.5); was claude-code with each recipe&#39;s model."))
		client.Ack(form.Patch.ServerSeq)
	})

	It("shows a refusal for a ticket that is not a URL or a check the control plane refuses", func() {
		operations.EXPECT().CheckAgentSessionAdmission(gomock.Any(), gomock.Any()).Return(nil, errors.New("conflict: admission is held"))
		client := connect(mountView(directory, watcher.mock, opsview.WithOperations(operations), opsview.WithRecipeTemplates(templatesWithSample())))
		client.Send(opsview.EventSection, opsview.LaunchRegion, map[string]string{opsview.FieldSection: "launch"})
		form := client.WaitFor(opsview.LaunchRegion, contains(`<option value="sample"`))
		client.Ack(form.Patch.ServerSeq)

		client.Send(opsview.EventCheck, opsview.LaunchRegion, map[string]string{opsview.FieldTicket: "issue 328", opsview.FieldTemplate: "sample"})
		form = client.WaitFor(opsview.LaunchRegion, contains("is not a ticket URL"))
		client.Ack(form.Patch.ServerSeq)
		client.Send(opsview.EventCheck, opsview.LaunchRegion, map[string]string{opsview.FieldTicket: ticketURL, opsview.FieldTemplate: "sample"})
		form = client.WaitFor(opsview.LaunchRegion, contains("admission is held"))
		html, _ := form.Patch.Fragment(opsview.LaunchRegion)
		Expect(html).NotTo(ContainSubstring(`data-opsview="launch"`), "nothing checked, nothing to launch")
	})
})

var _ = Describe("Reading a recipe template", func() {
	It("refuses a brief outside the template, a template with both task and brief, and a name that is a path", func() {
		templates := fstest.MapFS{
			"escape/agent.json": {Data: []byte(`{"workspace":{"briefPath":"../other/brief.md"}}`)},
			"both/agent.json":   {Data: []byte(`{"task":"inline","workspace":{"briefPath":"brief.md"}}`)},
			"both/brief.md":     {Data: []byte("brief")},
			"plain/agent.json":  {Data: []byte(`{"task":"inline"}`)},
		}
		_, err := opsview.RecipeFromTemplate(templates, "escape", ticketURL, firstAssignment)
		Expect(err).To(MatchError(opsview.ErrBriefOutside))
		_, err = opsview.RecipeFromTemplate(templates, "both", ticketURL, firstAssignment)
		Expect(err).To(MatchError(opsview.ErrBriefConflict))
		_, err = opsview.RecipeFromTemplate(templates, "../plain", ticketURL, firstAssignment)
		Expect(err).To(MatchError(ContainSubstring("is not a template name")))
		recipe, err := opsview.RecipeFromTemplate(templates, "plain", ticketURL, firstAssignment)
		Expect(err).NotTo(HaveOccurred())
		Expect(recipe.GetTask()).To(Equal("inline"))
		Expect(recipe.GetWorkspace().GetBranch()).To(BeEmpty(), "a template with no branch gets none")

		names, err := opsview.ListTemplates(templates)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(Equal([]string{"both", "escape", "plain"}))
	})
})

var _ = Describe("The Workbench's first screen", func() {
	It("counts running against capacity and spend by sessions and fixers for today, each tile with its definition, and fills the queue", func() {
		directory := GinkgoT().TempDir()
		writeSession(directory, firstAssignment, strings.ReplaceAll(startedLine, "%s", firstAssignment), requestedLine,
			firstResult, secondResult, reopenedResult, publishedLine, readyLine, mergedLine, refusedSendLine)
		watcher := newSpecWatcher(gomock.NewController(GinkgoT()))
		today := clock.NewManualClock(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC))
		client := connect(mountView(directory, watcher.mock, opsview.WithClock(today)))

		tiles := client.WaitFor(opsview.SummaryRegion, contains(`data-opsview="spend">$1.25<`))
		client.Ack(tiles.Patch.ServerSeq)
		html, _ := tiles.Patch.Fragment(opsview.SummaryRegion)
		Expect(html).To(ContainSubstring("fixers $0.00 / $100.00"), "0.75 as the cost rose to 2.00, then 0.50 after the executor reopened; the loop's default cap until its snapshot names one")
		Expect(html).To(ContainSubstring(`data-opsview="running">1<`))
		Expect(html).To(ContainSubstring("no dispatcher"))
		Expect(html).To(ContainSubstring(`data-opsview="merges">–<`), "no forge to read merges from")
		Expect(html).NotTo(ContainSubstring(`data-opsview="definition"`))

		client.Send(opsview.EventDefine, opsview.SummaryRegion, map[string]string{opsview.FieldTile: "spend"})
		tiles = client.WaitFor(opsview.SummaryRegion, contains(`data-opsview="definition"`))
		client.Ack(tiles.Patch.ServerSeq)
		html, _ = tiles.Patch.Fragment(opsview.SummaryRegion)
		Expect(html).To(ContainSubstring("Model spend on 2026-10-05 (UTC)"))
		client.Send(opsview.EventDefine, opsview.SummaryRegion, map[string]string{opsview.FieldTile: "spend"})
		tiles = client.WaitFor(opsview.SummaryRegion, func(html string) bool { return !strings.Contains(html, `data-opsview="definition"`) })
		client.Ack(tiles.Patch.ServerSeq)

		board, _ := client.Snapshot().Patch.Fragment(opsview.QueueRegion)
		Expect(board).To(ContainSubstring("no snapshot"))
		Expect(os.WriteFile(filepath.Join(directory, opsview.QueueFile), []byte(dispatchSnapshot), 0o600)).To(Succeed())
		watcher.changes <- iofs.Change{Name: opsview.QueueFile, Op: iofs.ChangeRenamed}
		panel := client.WaitFor(opsview.QueueRegion, contains("1 running · 1 queued"))
		client.Ack(panel.Patch.ServerSeq)
		html, _ = panel.Patch.Fragment(opsview.SummaryRegion)
		Expect(html).To(ContainSubstring("of 4 slots"), "running is counted against the dispatcher's capacity")

		client.Send(opsview.EventSection, opsview.QueueRegion, map[string]string{opsview.FieldSection: "queue"})
		panel = client.WaitFor(opsview.QueueRegion, contains(`data-opsview-slice="wb-control"`))
		html, _ = panel.Patch.Fragment(opsview.QueueRegion)
		Expect(html).To(ContainSubstring("WORKBENCH-CONTROL"))
		Expect(html).To(ContainSubstring("depends on wb-control"), "a queued slice says why it waits")
	})

	It("reads the merges on main from the forge for each repository a session's pull request lives in, and marks the card merged", func() {
		directory := GinkgoT().TempDir()
		writeSession(directory, firstAssignment, strings.ReplaceAll(startedLine, "%s", firstAssignment), requestedLine, githubPublishedLine, finishedLine)
		controller := gomock.NewController(GinkgoT())
		watcher := newSpecWatcher(controller)
		launcher := NewMockILauncher(controller)
		// The forge lists the merges at once and the open pull requests when the
		// spec lets it, so the order the page learns them in is the spec's, not
		// the scheduler's.
		openListed := make(chan struct{})
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, command proc.Command) (proc.Result, error) {
			Expect(command.Executable).To(Equal("gh"))
			if slices.Contains(command.Arguments, "open") {
				Expect(command.Arguments).To(ContainElements("--repo", "candacelabs/repository", "--json", "url,additions,deletions,changedFiles,statusCheckRollup"))
				select {
				case <-openListed:
					return proc.Result{Stdout: []byte(openListing)}, nil
				case <-ctx.Done():
					return proc.Result{}, ctx.Err()
				}
			}
			Expect(command.Arguments).To(ContainElements("--repo", "candacelabs/repository", "--base", "main", "--search", "merged:>=2026-10-05"))
			return proc.Result{Stdout: []byte(mergedListing)}, nil
		}).MinTimes(2)
		today := clock.NewManualClock(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC))
		client := connect(mountView(directory, watcher.mock, opsview.WithClock(today), opsview.WithGitHub(launcher)))

		merged := client.WaitFor(opsview.BoardRegion, contains(`aria-label="merged today"`))
		client.Ack(merged.Patch.ServerSeq)
		tiles, _ := merged.Patch.Fragment(opsview.SummaryRegion)
		Expect(tiles).To(ContainSubstring(`data-opsview="merges">1<`))
		html, _ := merged.Patch.Fragment(opsview.BoardRegion)
		Expect(html).To(ContainSubstring("#9</span> Fix the gate"))
		Expect(html).NotTo(ContainSubstring("Yesterday"), "yesterday's merge is not today's")
		Expect(html).NotTo(ContainSubstring(`aria-label="needs you"`), "a landed pull request needs nobody")
		Expect(html).NotTo(ContainSubstring(`data-opsview="merges-toggle"`), "one merge needs no fold")

		// Opened before the forge lists the open pull requests, the board shows
		// the landed card without their diff stat or checks.
		client.Send(opsview.EventOthers, opsview.BoardRegion, nil)
		board := client.WaitFor(opsview.BoardRegion, contains(`data-opsview="pr">PR #9 merged<`))
		client.Ack(board.Patch.ServerSeq)
		html, _ = board.Patch.Fragment(opsview.BoardRegion)
		Expect(html).NotTo(ContainSubstring(`data-opsview="checks"`), "the forge has not listed the open pull requests")
		Expect(html).To(ContainSubstring(`href="/chat/`+firstAssignment+`" data-opsview="open-transcript"`), "the row opens its transcript")

		// The listing then reaches the card alone: a card's own change is its
		// own patch, and the board does not re-render for it.
		close(openListed)
		card := client.WaitFor(firstRegion, contains(`data-opsview="checks"`))
		client.Ack(card.Patch.ServerSeq)
		Expect(card.Patch.FragmentIDs()).To(Equal([]string{firstRegion}), "the board itself did not re-render")
		html, _ = card.Patch.Fragment(firstRegion)
		expectForgeFacts(html)

		// Folded and opened again, the board draws the card with what the page
		// already knows of it.
		client.Send(opsview.EventOthers, opsview.BoardRegion, nil)
		board = client.WaitFor(opsview.BoardRegion, func(html string) bool { return !strings.Contains(html, `data-opsview="pr">PR #9 merged<`) })
		client.Ack(board.Patch.ServerSeq)
		client.Send(opsview.EventOthers, opsview.BoardRegion, nil)
		board = client.WaitFor(opsview.BoardRegion, contains(`data-opsview="checks"`))
		client.Ack(board.Patch.ServerSeq)
		html, _ = board.Patch.Fragment(opsview.BoardRegion)
		expectForgeFacts(html)
		Expect(html).To(ContainSubstring(`href="/chat/`+firstAssignment+`" data-opsview="open-transcript"`), "the row opens its transcript")
	})

	It("says sessions as two numbers: virtual sessions by phase and the real sessions they keep alive, with their ratio", func() {
		directory := GinkgoT().TempDir()
		controller := gomock.NewController(GinkgoT())
		operations := NewMockIWorkbenchOperations(controller)
		operations.EXPECT().GetWorkbenchTheme(gomock.Any(), gomock.Any()).Return(&pb.GetWorkbenchThemeResponse{}, nil).AnyTimes()
		phases := []harnessv1.AgentSessionPhase{
			harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING,
			harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN,
			harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED,
		}
		var sessions []*harnessv1.AgentSessionState
		for _, phase := range phases {
			sessions = append(sessions, &harnessv1.AgentSessionState{Phase: phase})
		}
		operations.EXPECT().ListAgentSessions(gomock.Any(), gomock.Any()).Return(&harnessv1.ListAgentSessionsResponse{Sessions: sessions}, nil).MinTimes(1)
		client := connect(mountView(directory, newSpecWatcher(controller).mock, opsview.WithOperations(operations)))

		tiles := client.WaitFor(opsview.SummaryRegion, contains(`data-opsview="sessions">6 / 5<`))
		client.Ack(tiles.Patch.ServerSeq)
		html, _ := tiles.Patch.Fragment(opsview.SummaryRegion)
		Expect(html).To(ContainSubstring("3 open · ×1.2"), "six virtual sessions on five real sessions alive")
		Expect(html).To(ContainSubstring(`data-opsview="running">2<`), "running is the service's own phase")
		client.Send(opsview.EventDefine, opsview.SummaryRegion, map[string]string{opsview.FieldTile: "sessions"})
		tiles = client.WaitFor(opsview.SummaryRegion, contains(`data-opsview="definition"`))
		html, _ = tiles.Patch.Fragment(opsview.SummaryRegion)
		Expect(html).To(ContainSubstring("Now: 6 virtual sessions, 2 running a turn, 3 open between turns (0 of them suspended), 0 starting, 1 canceled, 0 failed, 0 closed; 5 real sessions alive."))
	})

	It("refuses a nil launcher or clock", func() {
		files, err := iofs.NewHostFiles(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		watcher := newSpecWatcher(gomock.NewController(GinkgoT()))
		_, err = opsview.NewOpsView(files, watcher.mock, []string{specOrigin}, nil, opsview.WithGitHub(nil))
		Expect(err).To(MatchError(opsview.ErrNoLauncher))
		_, err = opsview.NewOpsView(files, watcher.mock, []string{specOrigin}, nil, opsview.WithClock(nil))
		Expect(err).To(MatchError(opsview.ErrNoClock))
	})
})
