// Copyright 2026 Candace Labs

package opsview_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	ipcfs "github.com/candacelabs/csf/ipc/fs"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/opsview"
)

// Integration specs: the view through its exported API alone, over a run
// directory the test framework creates, with the kernel's notification queue
// replaced by a gomock watcher whose channel the spec feeds. The wire is
// real: a livetest client drives the same handshake and frames a browser does.
const (
	specOrigin       = "http://opsview.example"
	specService      = "opsview-spec"
	firstAssignment  = "0caf2d51-1ee7-468a-882f-ac478a437640"
	secondAssignment = "4b2d1c0e-9f6a-4c3b-8d2e-1f0a9b8c7d6e"
	firstRegion      = opsview.BoardRegion + ":" + firstAssignment

	runRecord = `{"assignment_id":"%s","agent_id":"scratch","ticket_url":"https://example.invalid/issues/1","session_id":"d71e73c9-9a92-5ef1-86a3-59228e5aa3b2","trace_id":"d6f0bee711be2663cc9e743295c96ac5","span_id":"cb78bf9e9a2c1403","repository":"/repository","worktree":"/repository/worktree","branch":"h1/scratch","base_branch":"main","pull_request_title":"H1 scratch","turns":1}`

	startedLine   = `{"time":"2026-10-02T02:48:35Z","level":"INFO","msg":"harness run started","turn":0,"sequence":1,"event_type":"harness_run_started","assignment_id":"%s","agent_id":"scratch","branch":"h1/scratch"}` + "\n"
	requestedLine = `{"time":"2026-10-02T02:48:36Z","level":"INFO","msg":"turn requested","turn":1,"sequence":3,"event_type":"harness_turn_requested","resume":false}` + "\n"
	oneToolLine   = `{"time":"2026-10-02T02:48:44Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"out","event_type":"assistant","event":{"type":"assistant","message":{"model":"claude-fable-5-1","content":[{"type":"tool_use","name":"Bash","input":{"description":"List the worktree"}}]}}}` + "\n"
	twoToolsLine  = `{"time":"2026-10-02T02:48:48Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"out","event_type":"assistant","event":{"type":"assistant","message":{"model":"claude-fable-5-1","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/workspace/README.md"}},{"type":"tool_use","name":"Grep","input":{"pattern":"term harness"}}]}}}` + "\n"

	toolCallsMark = `data-opsview="tool-calls">`
	followBudget  = 10 * time.Second
)

// specWatcher is the double for the kernel's queue: Add is recorded, and the
// changes channel is the spec's to feed; it closes when the connection's
// context ends, exactly as the real watch does, so the follow effect joins.
type specWatcher struct {
	mock    *MockIWatcher
	changes chan ipcfs.Change
	added   chan string
}

func newSpecWatcher(controller *gomock.Controller) *specWatcher {
	watcher := &specWatcher{mock: NewMockIWatcher(controller), changes: make(chan ipcfs.Change, 8), added: make(chan string, 8)}
	watcher.mock.EXPECT().Watch(gomock.Any()).DoAndReturn(func(ctx context.Context) (*ipcfs.Watch, error) {
		forward := make(chan ipcfs.Change)
		go func() {
			defer close(forward)
			for {
				select {
				case <-ctx.Done():
					return
				case change := <-watcher.changes:
					select {
					case forward <- change:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
		return &ipcfs.Watch{Changes: forward, Add: func(name string) error {
			watcher.added <- name
			return nil
		}}, nil
	}).AnyTimes()
	return watcher
}

// writeSession writes one run directory the way the harness lays it out.
func writeSession(directory string, assignment string, lines ...string) {
	GinkgoHelper()
	run := filepath.Join(directory, assignment)
	Expect(os.MkdirAll(run, 0o700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(run, session.RunStateFile), []byte(strings.ReplaceAll(runRecord, "%s", assignment)), 0o600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(run, session.EventsFile), []byte(strings.Join(lines, "")), 0o600)).To(Succeed())
}

// appendLine lands one more line in a session's event log, as one write.
func appendLine(directory string, assignment string, line string) {
	GinkgoHelper()
	file, err := os.OpenFile(filepath.Join(directory, assignment, session.EventsFile), os.O_APPEND|os.O_WRONLY, 0o600)
	Expect(err).NotTo(HaveOccurred())
	_, err = file.WriteString(line)
	Expect(err).NotTo(HaveOccurred())
	Expect(file.Close()).To(Succeed())
}

// mountView builds the view over directory with watcher, registers it on an
// engine the way the harness host app does, and starts it on a scope the
// spec closes.
func mountView(directory string, watcher ipcfs.IWatcher) *gin.Engine {
	GinkgoHelper()
	files, err := ipcfs.NewHostFiles(directory)
	Expect(err).NotTo(HaveOccurred())
	view, err := opsview.NewOpsView(files, watcher, []string{specOrigin}, slog.New(slog.DiscardHandler))
	Expect(err).NotTo(HaveOccurred())
	engine := httpserver.NewEngine(specService)
	view.Register(engine)
	scope := runtime.NewScope(context.Background(), specService)
	Expect(view.Start(scope)).To(Succeed())
	DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })
	return engine
}

func connect(engine *gin.Engine) *livetest.Client {
	GinkgoHelper()
	client := livetest.NewClient(GinkgoTB(), engine, livetest.ClientOptions{Path: opsview.LivePath, Origin: specOrigin})
	client.Ack(client.Snapshot().Patch.ServerSeq)
	return client
}

func showsToolCalls(count string) func(html string) bool {
	return func(html string) bool { return strings.Contains(html, toolCallsMark+count+"<") }
}

var _ = Describe("Building the ops view", func() {
	It("refuses a missing capability", func() {
		controller := gomock.NewController(GinkgoT())
		files, err := ipcfs.NewHostFiles(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		_, err = opsview.NewOpsView(nil, NewMockIWatcher(controller), []string{specOrigin}, nil)
		Expect(err).To(MatchError(opsview.ErrNoFiles))
		_, err = opsview.NewOpsView(files, nil, []string{specOrigin}, nil)
		Expect(err).To(MatchError(opsview.ErrNoWatcher))
	})

	It("refuses an empty origin allowlist, which would admit no browser", func() {
		controller := gomock.NewController(GinkgoT())
		files, err := ipcfs.NewHostFiles(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		_, err = opsview.NewOpsView(files, NewMockIWatcher(controller), nil, nil)
		Expect(err).To(HaveOccurred())
	})

	It("names its page under the host's address", func() {
		Expect(opsview.PageURL("http://127.0.0.1:14121/")).To(Equal("http://127.0.0.1:14121/"))
	})
})

var _ = Describe("Following the harness state directory", func() {
	var (
		directory string
		watcher   *specWatcher
		engine    *gin.Engine
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		writeSession(directory, firstAssignment, strings.ReplaceAll(startedLine, "%s", firstAssignment), requestedLine, oneToolLine)
		watcher = newSpecWatcher(gomock.NewController(GinkgoT()))
		engine = mountView(directory, watcher.mock)
	})

	It("shows every session as a card, and patches only that card when a line lands", func() {
		client := connect(engine)
		Eventually(watcher.added, followBudget).Should(Receive(Equal(".")), "the state directory is watched for new run directories")
		Eventually(watcher.added, followBudget).Should(Receive(Equal(firstAssignment)), "the run directory is watched for its files")

		board := client.WaitFor(opsview.BoardRegion, showsToolCalls("1"))
		client.Ack(board.Patch.ServerSeq)
		html, _ := board.Patch.Fragment(opsview.BoardRegion)
		Expect(html).To(ContainSubstring(`class="card running"`))
		Expect(html).To(ContainSubstring("scratch"))
		Expect(html).To(ContainSubstring("h1/scratch"))
		Expect(html).To(ContainSubstring("claude-fable-5-1"))
		Expect(html).To(ContainSubstring(`href="https://example.invalid/issues/1"`))
		Expect(html).To(ContainSubstring("Bash: List the worktree"))

		appendLine(directory, firstAssignment, twoToolsLine)
		watcher.changes <- ipcfs.Change{Name: firstAssignment + "/" + session.EventsFile, Op: ipcfs.ChangeWritten}

		card := client.WaitFor(firstRegion, showsToolCalls("3"))
		Expect(card.Patch.FragmentIDs()).To(Equal([]string{firstRegion}), "the board itself did not re-render")
		client.Ack(card.Patch.ServerSeq)
	})

	It("shows a card's earlier events when the browser asks, and keeps that choice as the log grows", func() {
		for range 6 {
			appendLine(directory, firstAssignment, oneToolLine)
		}
		client := connect(engine)
		board := client.WaitFor(opsview.BoardRegion, showsToolCalls("7"))
		client.Ack(board.Patch.ServerSeq)
		html, _ := board.Patch.Fragment(opsview.BoardRegion)
		Expect(html).To(ContainSubstring("show 4 earlier"))
		Expect(strings.Count(html, `<li class="tool">`)).To(Equal(5))

		client.Send(opsview.EventExpand, firstRegion, nil)
		card := client.WaitFor(firstRegion, func(html string) bool { return strings.Contains(html, "show fewer") })
		client.Ack(card.Patch.ServerSeq)
		html, _ = card.Patch.Fragment(firstRegion)
		Expect(strings.Count(html, `<li class="tool">`)).To(Equal(7))

		appendLine(directory, firstAssignment, oneToolLine)
		watcher.changes <- ipcfs.Change{Name: firstAssignment + "/" + session.EventsFile, Op: ipcfs.ChangeWritten}
		card = client.WaitFor(firstRegion, showsToolCalls("8"))
		html, _ = card.Patch.Fragment(firstRegion)
		Expect(html).To(ContainSubstring("show fewer"), "the choice survived the card being replaced")
	})

	It("adopts a run directory that appears later, once it has a run record", func() {
		client := connect(engine)
		board := client.WaitFor(opsview.BoardRegion, showsToolCalls("1"))
		client.Ack(board.Patch.ServerSeq)

		Expect(os.Mkdir(filepath.Join(directory, secondAssignment), 0o700)).To(Succeed())
		watcher.changes <- ipcfs.Change{Name: secondAssignment, Op: ipcfs.ChangeCreated}
		Eventually(watcher.added, followBudget).Should(Receive(Equal(secondAssignment)))
		Consistently(func() int { return len(client.Received()) }, 300*time.Millisecond).Should(Equal(2), "a directory with no run record is not a session yet")

		writeSession(directory, secondAssignment, strings.ReplaceAll(startedLine, "%s", secondAssignment), requestedLine)
		watcher.changes <- ipcfs.Change{Name: secondAssignment + "/" + session.RunStateFile, Op: ipcfs.ChangeCreated}
		board = client.WaitFor(opsview.BoardRegion, func(html string) bool {
			return strings.Contains(html, `data-opsview-assignment="`+secondAssignment+`"`)
		})
		client.Ack(board.Patch.ServerSeq)
	})

	It("ignores changes to anything that is not a session's record or log", func() {
		client := connect(engine)
		board := client.WaitFor(opsview.BoardRegion, showsToolCalls("1"))
		client.Ack(board.Patch.ServerSeq)

		watcher.changes <- ipcfs.Change{Name: "harness.log", Op: ipcfs.ChangeWritten}
		watcher.changes <- ipcfs.Change{Name: firstAssignment + "/worktree", Op: ipcfs.ChangeCreated}
		watcher.changes <- ipcfs.Change{Name: firstAssignment + "/" + session.EventsFile, Op: ipcfs.ChangeWritten}
		Consistently(func() int { return len(client.Received()) }, 300*time.Millisecond).Should(Equal(2), "nothing a card shows changed")
	})

	It("tells the page when the follow effect fails", func() {
		failing := NewMockIWatcher(gomock.NewController(GinkgoT()))
		failing.EXPECT().Watch(gomock.Any()).Return(nil, ipcfs.ErrNotGranted)
		client := connect(mountView(directory, failing))
		board := client.WaitFor(opsview.BoardRegion, func(html string) bool { return strings.Contains(html, "opsview.follow failed") })
		html, _ := board.Patch.Fragment(opsview.BoardRegion)
		Expect(html).To(ContainSubstring(ipcfs.ErrNotGranted.Error()))
	})
})
