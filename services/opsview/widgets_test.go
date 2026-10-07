// Copyright 2026 Candace Labs

package opsview_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/net/github"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/opsview"
)

// The widget installed in these specs is the checkout's own, read from the
// widgets directory, so the specs install exactly what the Workbench does.
const (
	mergeTrain      = "merge-train"
	checkoutWidgets = "../../widgets"
	installedMark   = `data-opsview-widget="merge-train" data-opsview-installed="true"`
	refusedMark     = `data-opsview-widget="merge-train" data-opsview-installed="false"`
	malformedFrom   = "requires waiting atMost 0"
	malformedTo     = "requires wating atMost 0"
	malformedReason = `merge-train/definition.widget:20:14: W201: state field &#34;wating&#34; is not declared.`
)

// definitionFiles reads the checkout's definition of one widget.
func definitionFiles(name string) map[string]string {
	GinkgoHelper()
	files := map[string]string{}
	for _, file := range []string{widget.DefinitionFile, widget.BindingFile, widget.FixturesFile} {
		content, err := os.ReadFile(filepath.Join(checkoutWidgets, name, file))
		Expect(err).NotTo(HaveOccurred())
		files[file] = string(content)
	}
	return files
}

// writeDefinition lands one definition directory the way a merge does.
func writeDefinition(directory string, name string, files map[string]string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Join(directory, name), 0o700)).To(Succeed())
	for file, content := range files {
		Expect(os.WriteFile(filepath.Join(directory, name, file), []byte(content), 0o600)).To(Succeed())
	}
}

// mountWorkbench builds the view over the state directory and the widgets
// directory, each with its own watch double.
func mountWorkbench(state string, stateWatcher iofs.IWatcher, definitions string, definitionWatcher iofs.IWatcher) *gin.Engine {
	GinkgoHelper()
	stateFiles, err := iofs.NewHostFiles(state)
	Expect(err).NotTo(HaveOccurred())
	definitionFiles, err := iofs.NewHostFiles(definitions)
	Expect(err).NotTo(HaveOccurred())
	view, err := opsview.NewOpsView(stateFiles, stateWatcher, []string{specOrigin}, slog.New(slog.DiscardHandler),
		opsview.WithWidgetDefinitions(definitionFiles, definitionWatcher))
	Expect(err).NotTo(HaveOccurred())
	engine := httpserver.NewEngine(specService)
	view.Register(engine)
	scope := runtime.NewScope(context.Background(), specService)
	Expect(view.Start(scope)).To(Succeed())
	DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })
	return engine
}

func contains(fragment string) func(html string) bool {
	return func(html string) bool { return strings.Contains(html, fragment) }
}

var _ = Describe("Installing widget definitions live", func() {
	var (
		state, definitions        string
		stateWatcher, defsWatcher *specWatcher
		engine                    *gin.Engine
	)

	BeforeEach(func() {
		state, definitions = GinkgoT().TempDir(), GinkgoT().TempDir()
		writeSession(state, firstAssignment, strings.ReplaceAll(startedLine, "%s", firstAssignment), requestedLine, oneToolLine)
		stateWatcher, defsWatcher = newSpecWatcher(gomock.NewController(GinkgoT())), newSpecWatcher(gomock.NewController(GinkgoT()))
		engine = mountWorkbench(state, stateWatcher.mock, definitions, defsWatcher.mock)
	})

	It("draws a definition the moment it lands, over the loop's snapshot, and removes it the moment it goes", func() {
		client := connect(engine)
		Eventually(defsWatcher.added, followBudget).Should(Receive(Equal(".")), "the widgets directory is watched for new definitions")
		html, present := client.Snapshot().Patch.Fragment(opsview.WidgetsRegion)
		Expect(present).To(BeTrue(), "the panel's region is on the page from the first paint")
		Expect(html).NotTo(ContainSubstring("data-opsview-widget"))

		writeDefinition(definitions, mergeTrain, definitionFiles(mergeTrain))
		defsWatcher.changes <- iofs.Change{Name: mergeTrain, Op: iofs.ChangeCreated}
		panel := client.WaitFor(opsview.WidgetsRegion, contains(installedMark))
		client.Ack(panel.Patch.ServerSeq)
		Eventually(defsWatcher.added, followBudget).Should(Receive(Equal(mergeTrain)), "the definition's own directory is watched for edits")
		html, _ = panel.Patch.Fragment(opsview.WidgetsRegion)
		Expect(html).To(ContainSubstring(`<h2 class="widget-title widget-token-ink" id="widget.merge-train.title">Merge train</h2>`))
		Expect(html).To(ContainSubstring("0 merged"), "no snapshot yet: the widget shows its zero values")

		Expect(os.WriteFile(filepath.Join(state, opsview.LoopFile), []byte(loopSnapshot), 0o600)).To(Succeed())
		stateWatcher.changes <- iofs.Change{Name: opsview.LoopFile, Op: iofs.ChangeRenamed}
		panel = client.WaitFor(opsview.WidgetsRegion, contains("85 findings wait for labels"))
		client.Ack(panel.Patch.ServerSeq)
		html, _ = panel.Patch.Fragment(opsview.WidgetsRegion)
		Expect(html).To(ContainSubstring("5 merged"))
		Expect(html).To(ContainSubstring("12 refused"))
		Expect(html).To(ContainSubstring("ouroboros loop, day 2026-10-03"))

		Expect(os.RemoveAll(filepath.Join(definitions, mergeTrain))).To(Succeed())
		defsWatcher.changes <- iofs.Change{Name: mergeTrain, Op: iofs.ChangeRemoved}
		panel = client.WaitFor(opsview.WidgetsRegion, func(html string) bool { return !strings.Contains(html, "data-opsview-widget") })
		Expect(panel.Patch.FragmentIDs()).To(Equal([]string{opsview.WidgetsRegion}), "only the panel re-rendered")
	})

	It("refuses a malformed definition with the checker's reason and never draws it, and draws it once it is fixed", func() {
		files := definitionFiles(mergeTrain)
		files[widget.DefinitionFile] = strings.Replace(files[widget.DefinitionFile], malformedFrom, malformedTo, 1)
		writeDefinition(definitions, mergeTrain, files)
		client := connect(engine)
		panel := client.WaitFor(opsview.WidgetsRegion, contains(refusedMark))
		client.Ack(panel.Patch.ServerSeq)
		html, _ := panel.Patch.Fragment(opsview.WidgetsRegion)
		Expect(html).To(ContainSubstring(malformedReason))
		Expect(html).NotTo(ContainSubstring(`class="widget"`), "a refused definition is never drawn")

		writeDefinition(definitions, mergeTrain, definitionFiles(mergeTrain))
		defsWatcher.changes <- iofs.Change{Name: mergeTrain + "/" + widget.DefinitionFile, Op: iofs.ChangeWritten}
		client.WaitFor(opsview.WidgetsRegion, contains(installedMark))
	})

	It("refuses the second of two definitions claiming one region", func() {
		writeDefinition(definitions, mergeTrain, definitionFiles(mergeTrain))
		writeDefinition(definitions, "merge-train-copy", definitionFiles(mergeTrain))
		client := connect(engine)
		panel := client.WaitFor(opsview.WidgetsRegion, contains(`data-opsview-widget="merge-train-copy" data-opsview-installed="false"`))
		html, _ := panel.Patch.Fragment(opsview.WidgetsRegion)
		Expect(html).To(ContainSubstring(installedMark))
		Expect(html).To(ContainSubstring(`merge-train-copy: region &#34;widget.merge-train&#34; or widget name MergeTrain is already installed by merge-train`))
	})
})

// recordingLauncher answers git the way a session's worktree would and
// records every command, in order.
func recordingLauncher(controller *gomock.Controller, launched *[]string) *MockILauncher {
	launcher := NewMockILauncher(controller)
	launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, command proc.Command) (proc.Result, error) {
		line := command.Executable + " " + strings.Join(command.Arguments, " ")
		*launched = append(*launched, line)
		switch {
		case strings.Contains(line, "rev-parse HEAD"):
			return proc.Result{Stdout: []byte("5f0c1d2e\n")}, nil
		case strings.Contains(line, "remote get-url origin"):
			return proc.Result{Stdout: []byte("https://github.com/candacelabs/csf.git\n")}, nil
		}
		return proc.Result{}, nil
	}).AnyTimes()
	return launcher
}

var _ = Describe("The widget operations", func() {
	var (
		definitions, state string
		worktree           string
		launched           []string
		operations         *opsview.WidgetOperations
	)

	BeforeEach(func() {
		definitions, state, launched = GinkgoT().TempDir(), GinkgoT().TempDir(), nil
		writeDefinition(definitions, mergeTrain, definitionFiles(mergeTrain))
		writeSession(state, firstAssignment, strings.ReplaceAll(startedLine, "%s", firstAssignment))
		worktree = filepath.Join(state, firstAssignment, session.WorktreeDirectory)
		definitionFiles, err := iofs.NewHostFiles(definitions)
		Expect(err).NotTo(HaveOccurred())
		stateFiles, err := iofs.NewHostFiles(state)
		Expect(err).NotTo(HaveOccurred())
		controller := gomock.NewController(GinkgoT())
		// GitHub answers the branch's pull request lookup with none, then the
		// draft it opens; the requests are recorded beside the commands.
		httpClient := NewMockIHTTPClient(controller)
		httpClient.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *http.Request) (*http.Response, error) {
			launched = append(launched, request.Method+" "+request.URL.Path)
			body := `[]`
			status := http.StatusOK
			if request.Method == http.MethodPost {
				body, status = `{"html_url":"https://github.com/candacelabs/csf/pull/7"}`, http.StatusCreated
			}
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		}).AnyTimes()
		client, err := github.NewGitHubClient(httpClient, "ghs_spec")
		Expect(err).NotTo(HaveOccurred())
		operations, err = opsview.NewWidgetOperations(opsview.WithInstalledDefinitions(definitionFiles),
			opsview.WithSessionWorktrees(stateFiles, recordingLauncher(controller, &launched)), opsview.WithGitHubClient(client))
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses to be built without the widgets directory", func() {
		_, err := opsview.NewWidgetOperations()
		Expect(err).To(MatchError(opsview.ErrNoDefinitions))
	})

	It("lists every definition with its check, and the sources a widget may bind", func() {
		files := definitionFiles(mergeTrain)
		files[widget.DefinitionFile] = strings.Replace(files[widget.DefinitionFile], malformedFrom, malformedTo, 1)
		writeDefinition(definitions, "broken", files)
		listed, err := operations.List(context.Background(), opsview.ListWidgetsInput{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listed.Sources).To(Equal([]string{opsview.LoopFile}))
		Expect(listed.Widgets).To(HaveLen(2))
		Expect(listed.Widgets[0].Name).To(Equal("broken"))
		Expect(listed.Widgets[0].Installable).To(BeFalse())
		Expect(listed.Widgets[0].Reasons[0]).To(HavePrefix(`broken/definition.widget:20:14: W201: state field "wating" is not declared.`))
		Expect(listed.Widgets[1]).To(Equal(opsview.WidgetStatus{Name: mergeTrain, Installable: true, Source: opsview.LoopFile}))
	})

	It("checks a definition in the Workbench's checkout or in a session's worktree, and refuses a name that is not a directory", func() {
		status, err := operations.Check(context.Background(), opsview.CheckWidgetInput{Name: mergeTrain})
		Expect(err).NotTo(HaveOccurred())
		Expect(status.Installable).To(BeTrue())

		status, err = operations.Check(context.Background(), opsview.CheckWidgetInput{Name: mergeTrain, AssignmentID: firstAssignment})
		Expect(err).NotTo(HaveOccurred())
		Expect(status.Installable).To(BeFalse(), "the session has not written it yet")
		Expect(status.Reasons[0]).To(HavePrefix("merge-train/definition.widget: not read:"))

		_, err = operations.Check(context.Background(), opsview.CheckWidgetInput{Name: "../merge-train"})
		Expect(err).To(MatchError(opsview.ErrInvalidWidgetName))
		_, err = operations.Check(context.Background(), opsview.CheckWidgetInput{Name: mergeTrain, AssignmentID: "../" + firstAssignment})
		Expect(err).To(MatchError(opsview.ErrInvalidAssignment))
	})

	It("proposes a clean definition: commits it in the session's worktree and publishes the branch through the harness", func() {
		writeDefinition(filepath.Join(worktree, opsview.WidgetsDirectory), mergeTrain, definitionFiles(mergeTrain))
		proposed, err := operations.Propose(context.Background(), opsview.ProposeWidgetInput{Name: mergeTrain, AssignmentID: firstAssignment, Need: "show the merge train"})
		Expect(err).NotTo(HaveOccurred())
		Expect(proposed.Installable).To(BeTrue())
		Expect(proposed.Commit).To(Equal("5f0c1d2e"))
		Expect(proposed.PullRequestURL).To(Equal("https://github.com/candacelabs/csf/pull/7"))
		Expect(launched).To(HaveLen(7))
		Expect(launched[:3]).To(Equal([]string{
			"git -C /repository/worktree add -- widgets/merge-train",
			"git -C /repository/worktree commit --message WIDGET merge-train: show the merge train\n\nChecked by check_widget: the dialect parses, its refinements hold, every bound field exists in ouroboros.json and every fixture renders.\n -- widgets/merge-train",
			"git -C /repository/worktree rev-parse HEAD",
		}))
		Expect(launched[3]).To(Equal("git -C /repository/worktree push --set-upstream origin h1/scratch"))
		Expect(launched[5:]).To(Equal([]string{"GET /repos/candacelabs/csf/pulls", "POST /repos/candacelabs/csf/pulls"}))
	})

	It("commits nothing for a definition the check refuses, and says why", func() {
		files := definitionFiles(mergeTrain)
		files[widget.DefinitionFile] = strings.Replace(files[widget.DefinitionFile], malformedFrom, malformedTo, 1)
		writeDefinition(filepath.Join(worktree, opsview.WidgetsDirectory), mergeTrain, files)
		proposed, err := operations.Propose(context.Background(), opsview.ProposeWidgetInput{Name: mergeTrain, AssignmentID: firstAssignment, Need: "show the merge train"})
		Expect(err).NotTo(HaveOccurred())
		Expect(proposed.Installable).To(BeFalse())
		Expect(proposed.Reasons[0]).To(HavePrefix(`merge-train/definition.widget:20:14: W201`))
		Expect(launched).To(BeEmpty())
	})

	It("refuses a proposal with no need, or with no session", func() {
		_, err := operations.Propose(context.Background(), opsview.ProposeWidgetInput{Name: mergeTrain, AssignmentID: firstAssignment})
		Expect(err).To(MatchError(opsview.ErrNoNeed))
		_, err = operations.Propose(context.Background(), opsview.ProposeWidgetInput{Name: mergeTrain, Need: "show the merge train"})
		Expect(err).To(MatchError(opsview.ErrInvalidAssignment))
		Expect(slices.ContainsFunc(launched, func(line string) bool { return strings.Contains(line, "commit") })).To(BeFalse())
	})
})
