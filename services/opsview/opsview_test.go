// Copyright 2026 Candace Labs

package opsview_test

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/pkg/eventually"
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

	toolCallsMark = `data-opsview-tool-calls="`
	viewerID      = "6f3d0a3e-1b0c-4c55-9a8e-2f5b8d1c7e90"
	closedLine    = `{"time":"2026-10-02T02:50:00Z","level":"INFO","msg":"session closed","turn":1,"event_type":"harness_session_closed"}` + "\n"
	scoreMark     = `data-opsview="mutation-score">`
	followBudget  = 10 * time.Second

	// loopSnapshot is the mining loop's projection as it writes it, with
	// the baseline numbers of 2026-10-03.
	loopSnapshot = `{"computed_at":"2026-10-04T05:00:00Z","day":"2026-10-03","struggle_rate":{"day":"2026-10-03","tool_calls":1902,"struggles":8,"per_1k":4.2,"low":1.8,"high":8.3},` +
		`"week":{"day":"2026-09-28","tool_calls":14188,"struggles":504,"per_1k":35.5,"low":32.5,"high":38.8},"weeks":[{"day":"2026-09-28","tool_calls":14188,"struggles":504,"per_1k":35.5,"low":32.5,"high":38.8}],` +
		`"compounding":{"factor":1.019,"low":0.99,"high":1.049,"periods":3},"baseline":{"factor":1.156,"low":1.135,"high":1.177,"rate_per_1k":49.9,"week":"2026-09-28"},` +
		`"compounding_daily":{"factor":1.173,"low":0.961,"high":1.432,"periods":3},` +
		`"structure":[{"week":"2026-09-28","commit":"8798a27","counts":{"gates":3,"miners":1,"terms":126},"gained":130,"interventions":465,"per_intervention":0.2796}],` +
		`"structure_exponent":{"slope":null,"low":null,"high":null,"weeks":1,"proxy":"proxy: operator-authored messages"},"generic_miners":1,"tenant_miners":0,` +
		`"findings":76,"findings_today":2,"findings_per_day":25.3,` +
		`"fixers":{"sessions":107,"running":0,"finished":107,"ready":2,"spend_usd":79.37,"spend_today_usd":0,"budget_usd":100,"cost_per_ready_pull_request_usd":39.685,"yield":0.0187,"launch_enabled":false},` +
		`"merges":{"merged":5,"refused":12},"needs_labels":85,"miners":[{"name":"_template","built":true,"scope":"SCOPE_GENERIC","knee":468,"has_knee":true,"findings":76}],` +
		`"days":[{"day":"2026-10-03","tool_calls":1902,"struggles":8,"per_1k":4.2,"low":1.8,"high":8.3}]}`
	// Two records of one miner's mutation score, as its mutate verb prints
	// them: the template's measured 10/10, then a copy whose test let the
	// written-knee mutant survive.
	acceptedRecord = `{"miner":"draft-pr-late","at":1791100800,"killed":10,"survived":0,"excluded":6,"score":1.0,"floor":1.0,"accepted":true,"surviving":[],"seconds":0.031}` + "\n"
	rejectedRecord = `{"miner":"draft-pr-late","at":1791104400,"killed":9,"survived":1,"excluded":6,"score":0.9,"floor":1.0,"accepted":false,"surviving":[{"kind":"write knee","description":"rule 1: write 468 for knee(K)","source":"invisible(R) :- gated(R), score(R, S), gt(S, 468).","changed":["6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e31"]}],"seconds":0.028}` + "\n"
)

// reconnectBudget bounds the wait for a remembered view: the effect that
// stores it runs beside the patch the viewer saw.
var reconnectBudget = eventually.Budget{Within: 10 * time.Second, Interval: 50 * time.Millisecond}

// specWatcher is the double for the kernel's queue: Add is recorded, and the
// changes channel is the spec's to feed; it closes when the connection's
// context ends, exactly as the real watch does, so the follow effect joins.
type specWatcher struct {
	mock    *MockIWatcher
	changes chan iofs.Change
	added   chan string
}

func newSpecWatcher(controller *gomock.Controller) *specWatcher {
	watcher := &specWatcher{mock: NewMockIWatcher(controller), changes: make(chan iofs.Change, 8), added: make(chan string, 8)}
	watcher.mock.EXPECT().Watch(gomock.Any()).DoAndReturn(func(ctx context.Context) (*iofs.Watch, error) {
		forward := make(chan iofs.Change)
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
		return &iofs.Watch{Changes: forward, Add: func(name string) error {
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
	appendTo(filepath.Join(directory, assignment, session.EventsFile), line)
}

// appendTo lands one more line in a file, as one write.
func appendTo(path string, line string) {
	GinkgoHelper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	Expect(err).NotTo(HaveOccurred())
	_, err = file.WriteString(line)
	Expect(err).NotTo(HaveOccurred())
	Expect(file.Close()).To(Succeed())
}

// mountView builds the view over directory with watcher, registers it on an
// engine the way the harness host app does, and starts it on a scope the
// spec closes.
func mountView(directory string, watcher iofs.IWatcher, options ...opsview.Option) *gin.Engine {
	GinkgoHelper()
	files, err := iofs.NewHostFiles(directory)
	Expect(err).NotTo(HaveOccurred())
	view, err := opsview.NewOpsView(files, watcher, []string{specOrigin}, slog.New(slog.DiscardHandler), options...)
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
	return func(html string) bool { return strings.Contains(html, toolCallsMark+count+`"`) }
}

// unfold opens a folded panel the way a viewer does and returns its markup.
func unfold(client *livetest.Client, region string, section string) string {
	GinkgoHelper()
	client.Send(opsview.EventSection, region, map[string]string{opsview.FieldSection: section})
	frame := client.WaitFor(region, contains(`aria-expanded="true"`))
	client.Ack(frame.Patch.ServerSeq)
	html, _ := frame.Patch.Fragment(region)
	return html
}

// opened holds when a folded panel is open and pred holds of its markup.
func opened(pred func(html string) bool) func(html string) bool {
	return func(html string) bool { return strings.Contains(html, `aria-expanded="true"`) && pred(html) }
}

// mustFragment is a frame's markup for one fragment, which it must carry.
func mustFragment(frame *livetest.Frame, fragmentID string) string {
	GinkgoHelper()
	html, present := frame.Patch.Fragment(fragmentID)
	Expect(present).To(BeTrue(), fragmentID)
	return html
}

func showsScore(label string) func(html string) bool {
	return func(html string) bool { return strings.Contains(html, scoreMark+label+"<") }
}

var _ = Describe("Building the ops view", func() {
	It("refuses a missing capability", func() {
		controller := gomock.NewController(GinkgoT())
		files, err := iofs.NewHostFiles(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		_, err = opsview.NewOpsView(nil, NewMockIWatcher(controller), []string{specOrigin}, nil)
		Expect(err).To(MatchError(opsview.ErrNoFiles))
		_, err = opsview.NewOpsView(files, nil, []string{specOrigin}, nil)
		Expect(err).To(MatchError(opsview.ErrNoWatcher))
	})

	It("refuses an empty origin allowlist, which would admit no browser", func() {
		controller := gomock.NewController(GinkgoT())
		files, err := iofs.NewHostFiles(GinkgoT().TempDir())
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
		Expect(html).To(ContainSubstring(`class="row s-running"`))
		Expect(html).To(ContainSubstring(`<span class="name">scratch</span>`))
		Expect(html).To(ContainSubstring(`data-opsview="activity">List the worktree<`), "the row says what the session is doing")
		Expect(html).NotTo(ContainSubstring(firstAssignment+"</code>"), "internals wait behind the card's disclosure")

		appendLine(directory, firstAssignment, twoToolsLine)
		watcher.changes <- iofs.Change{Name: firstAssignment + "/" + session.EventsFile, Op: iofs.ChangeWritten}

		card := client.WaitFor(firstRegion, showsToolCalls("3"))
		Expect(card.Patch.FragmentIDs()).To(Equal([]string{firstRegion}), "the board itself did not re-render")
		client.Ack(card.Patch.ServerSeq)
	})

	It("opens a card on request, and the open card, its internals and the viewer's choice survive a live patch", func() {
		for range 6 {
			appendLine(directory, firstAssignment, oneToolLine)
		}
		client := connect(engine)
		board := client.WaitFor(opsview.BoardRegion, showsToolCalls("7"))
		client.Ack(board.Patch.ServerSeq)
		html, _ := board.Patch.Fragment(opsview.BoardRegion)
		Expect(html).NotTo(ContainSubstring(`<ol class="recent"`), "a closed row lists no events")

		client.Send(opsview.EventExpand, firstRegion, nil)
		card := client.WaitFor(firstRegion, contains(`aria-expanded="true" data-opsview="expand"`))
		client.Ack(card.Patch.ServerSeq)
		html, _ = card.Patch.Fragment(firstRegion)
		Expect(strings.Count(html, `<li class="tool">`)).To(Equal(7))
		Expect(html).To(ContainSubstring(`href="https://example.invalid/issues/1"`))

		client.Send(opsview.EventInternals, firstRegion, nil)
		card = client.WaitFor(firstRegion, contains(`data-opsview="internals-list"`))
		client.Ack(card.Patch.ServerSeq)
		html, _ = card.Patch.Fragment(firstRegion)
		Expect(html).To(ContainSubstring("<code>h1/scratch</code>"))
		Expect(html).To(ContainSubstring("claude-fable-5-1"))
		Expect(html).To(ContainSubstring(`data-opsview="executor">claude-code<`), "a run recorded without an executor ran on Claude Code")
		Expect(html).To(ContainSubstring("Bash: List the worktree"), "the raw view shows the call as sent")

		appendLine(directory, firstAssignment, twoToolsLine)
		watcher.changes <- iofs.Change{Name: firstAssignment + "/" + session.EventsFile, Op: iofs.ChangeWritten}
		card = client.WaitFor(firstRegion, showsToolCalls("9"))
		html, _ = card.Patch.Fragment(firstRegion)
		Expect(html).To(ContainSubstring(`aria-expanded="true" data-opsview="expand"`), "the card stayed open through the patch")
		Expect(html).To(ContainSubstring(`data-opsview="internals-list"`), "and so did its internals")
	})

	It("remembers a viewer's open cards and filter across a reconnect from the same browser session", func() {
		cookie := http.Header{"Cookie": []string{opsview.ViewerCookie + "=" + viewerID}}
		first := livetest.NewClient(GinkgoTB(), engine, livetest.ClientOptions{Path: opsview.LivePath, Origin: specOrigin, Header: cookie})
		first.Ack(first.Snapshot().Patch.ServerSeq)
		first.Ack(first.WaitFor(opsview.BoardRegion, showsToolCalls("1")).Patch.ServerSeq)
		first.Send(opsview.EventExpand, firstRegion, nil)
		first.Ack(first.WaitFor(firstRegion, contains(`aria-expanded="true" data-opsview="expand"`)).Patch.ServerSeq)
		first.Send(opsview.EventOthers, opsview.BoardRegion, nil)
		first.Ack(first.WaitFor(opsview.BoardRegion, contains(`data-opsview="filter"`)).Patch.ServerSeq)
		first.Send(opsview.EventFilter, opsview.BoardRegion, map[string]string{opsview.FieldFilter: "scratch", opsview.FieldShow: "closed"})
		first.Ack(first.WaitFor(opsview.BoardRegion, contains(`value="closed" selected`)).Patch.ServerSeq)
		Expect(first.Close()).To(Succeed())

		// The remembering effect runs beside the patch; a reconnect reads it.
		eventually.Await(GinkgoTB(), "the view remembered for the next connection", reconnectBudget, func() string {
			again := livetest.NewClient(GinkgoTB(), engine, livetest.ClientOptions{Path: opsview.LivePath, Origin: specOrigin, Header: cookie})
			defer func() { _ = again.Close() }()
			html, _ := again.Snapshot().Patch.Fragment(opsview.BoardRegion)
			return html
		}, func(html string) bool { return strings.Contains(html, `value="closed" selected`) })

		again := livetest.NewClient(GinkgoTB(), engine, livetest.ClientOptions{Path: opsview.LivePath, Origin: specOrigin, Header: cookie})
		again.Ack(again.Snapshot().Patch.ServerSeq)
		card := again.WaitFor(opsview.BoardRegion, showsToolCalls("1"))
		html, _ := card.Patch.Fragment(opsview.BoardRegion)
		Expect(html).To(ContainSubstring(`aria-expanded="true" data-opsview="expand"`), "the card the viewer opened opens again")
		Expect(html).To(ContainSubstring(`value="scratch"`), "the filter is the viewer's")

		stranger := connect(engine)
		html, _ = stranger.WaitFor(opsview.BoardRegion, showsToolCalls("1")).Patch.Fragment(opsview.BoardRegion)
		Expect(html).To(ContainSubstring(`aria-expanded="false" data-opsview="expand"`), "another viewer starts from the default view")
	})

	It("filters the idle and finished list by text and status", func() {
		writeSession(directory, secondAssignment, strings.ReplaceAll(startedLine, "%s", secondAssignment), requestedLine, closedLine)
		client := connect(engine)
		board := client.WaitFor(opsview.BoardRegion, contains(`data-opsview="others-count">1<`))
		client.Ack(board.Patch.ServerSeq)
		html, _ := board.Patch.Fragment(opsview.BoardRegion)
		Expect(html).NotTo(ContainSubstring(`data-opsview-assignment="`+secondAssignment+`"`), "idle sessions wait behind one count")

		client.Send(opsview.EventOthers, opsview.BoardRegion, nil)
		board = client.WaitFor(opsview.BoardRegion, contains(`data-opsview-assignment="`+secondAssignment+`"`))
		client.Ack(board.Patch.ServerSeq)

		client.Send(opsview.EventFilter, opsview.BoardRegion, map[string]string{opsview.FieldFilter: "no such session"})
		board = client.WaitFor(opsview.BoardRegion, contains(`data-opsview="matching">0 of 1<`))
		client.Ack(board.Patch.ServerSeq)

		client.Send(opsview.EventFilter, opsview.BoardRegion, map[string]string{opsview.FieldFilter: "SCRATCH", opsview.FieldShow: "closed"})
		board = client.WaitFor(opsview.BoardRegion, contains(`data-opsview="matching">1 of 1<`))
		html, _ = board.Patch.Fragment(opsview.BoardRegion)
		Expect(html).To(ContainSubstring(`data-opsview-assignment="` + secondAssignment + `"`))
	})

	It("adopts a run directory that appears later, once it has a run record", func() {
		client := connect(engine)
		board := client.WaitFor(opsview.BoardRegion, showsToolCalls("1"))
		client.Ack(board.Patch.ServerSeq)

		Expect(os.Mkdir(filepath.Join(directory, secondAssignment), 0o700)).To(Succeed())
		watcher.changes <- iofs.Change{Name: secondAssignment, Op: iofs.ChangeCreated}
		Eventually(watcher.added, followBudget).Should(Receive(Equal(secondAssignment)))
		received := len(client.Received())
		Consistently(func() int { return len(client.Received()) }, 300*time.Millisecond).Should(Equal(received), "a directory with no run record is not a session yet")

		writeSession(directory, secondAssignment, strings.ReplaceAll(startedLine, "%s", secondAssignment), requestedLine)
		watcher.changes <- iofs.Change{Name: secondAssignment + "/" + session.RunStateFile, Op: iofs.ChangeCreated}
		board = client.WaitFor(opsview.BoardRegion, func(html string) bool {
			return strings.Contains(html, `data-opsview-assignment="`+secondAssignment+`"`)
		})
		client.Ack(board.Patch.ServerSeq)
	})

	It("ignores changes to anything that is not a session's record or log", func() {
		client := connect(engine)
		board := client.WaitFor(opsview.BoardRegion, contains("data-gotth-transition"))
		client.Ack(board.Patch.ServerSeq)
		Expect(showsToolCalls("1")(mustFragment(board, opsview.BoardRegion))).To(BeTrue(), "the first read is done")

		watcher.changes <- iofs.Change{Name: "harness.log", Op: iofs.ChangeWritten}
		watcher.changes <- iofs.Change{Name: firstAssignment + "/worktree", Op: iofs.ChangeCreated}
		watcher.changes <- iofs.Change{Name: firstAssignment + "/" + session.EventsFile, Op: iofs.ChangeWritten}
		received := len(client.Received())
		Consistently(func() int { return len(client.Received()) }, 300*time.Millisecond).Should(Equal(received), "nothing a card shows changed")
	})

	It("shows the mining loop's numbers once the loop projects them, and follows the snapshot", func() {
		client := connect(engine)
		board := client.WaitFor(opsview.BoardRegion, showsToolCalls("1"))
		client.Ack(board.Patch.ServerSeq)
		Expect(os.WriteFile(filepath.Join(directory, opsview.LoopFile), []byte(loopSnapshot), 0o600)).To(Succeed())
		watcher.changes <- iofs.Change{Name: opsview.LoopFile, Op: iofs.ChangeRenamed}
		panel := client.WaitFor(opsview.LoopRegion, func(html string) bool { return strings.Contains(html, `data-opsview="struggle-rate">35.5<`) })
		Expect(panel.Patch.FragmentIDs()).To(ConsistOf(opsview.LoopRegion), "only the panel re-rendered: the tiles read spend from the sessions")
		client.Ack(panel.Patch.ServerSeq)
		html, _ := panel.Patch.Fragment(opsview.LoopRegion)
		Expect(html).To(ContainSubstring(`data-opsview="compounding">×1.019<`), "the folded section's one line")
		client.Send(opsview.EventSection, opsview.LoopRegion, map[string]string{opsview.FieldSection: "loop"})
		panel = client.WaitFor(opsview.LoopRegion, contains(`data-opsview="compounding-interval"`))
		html, _ = panel.Patch.Fragment(opsview.LoopRegion)
		Expect(html).To(ContainSubstring(`data-opsview="compounding-interval">×1.019 (×0.990–×1.049)<`))
		Expect(html).To(ContainSubstring(`data-opsview="baseline">×1.156 (×1.135–×1.177), 49.9 per 1k<`))
		Expect(html).To(ContainSubstring(`data-opsview="today">4.2 per 1k (1.8–8.3), 8 / 1902 calls<`))
		Expect(html).To(ContainSubstring(`data-opsview="compounding-daily">×1.173 (×0.961–×1.432)<`))
		Expect(html).To(ContainSubstring(`data-opsview="structure-exponent">– (–)<`))
		Expect(html).To(ContainSubstring(`data-opsview="scope">1 / 0<`))
		Expect(html).To(ContainSubstring(`data-opsview="findings">76 (2 today, 25.3/day)`))
		Expect(html).To(ContainSubstring(`data-opsview="cost-per-ready">$39.69<`))
		Expect(html).To(ContainSubstring("(fixers off)"))
		Expect(html).To(ContainSubstring("<td>_template</td><td>yes</td><td>SCOPE_GENERIC</td><td>468</td><td>76</td>"))
		Expect(html).To(ContainSubstring("<td>2026-09-28</td><td>3</td><td>1</td><td>126</td><td>130</td><td>465</td><td>0.2796</td>"))
	})

	It("tells the page when the follow effect fails", func() {
		failing := NewMockIWatcher(gomock.NewController(GinkgoT()))
		failing.EXPECT().Watch(gomock.Any()).Return(nil, iofs.ErrNotGranted)
		client := connect(mountView(directory, failing))
		board := client.WaitFor(opsview.BoardRegion, func(html string) bool { return strings.Contains(html, "opsview.follow failed") })
		html, _ := board.Patch.Fragment(opsview.BoardRegion)
		Expect(html).To(ContainSubstring(iofs.ErrNotGranted.Error()))
	})
})

var _ = Describe("Following the mutation score series", func() {
	var (
		directory string
		watcher   *specWatcher
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		writeSession(directory, firstAssignment, strings.ReplaceAll(startedLine, "%s", firstAssignment), requestedLine, oneToolLine)
		watcher = newSpecWatcher(gomock.NewController(GinkgoT()))
	})

	It("shows an empty panel until a record exists", func() {
		client := connect(mountView(directory, watcher.mock))
		board := client.WaitFor(opsview.BoardRegion, showsToolCalls("1"))
		client.Ack(board.Patch.ServerSeq)
		html, present := client.Snapshot().Patch.Fragment(opsview.MinersRegion)
		Expect(present).To(BeTrue(), "the panel's region is on the page from the first paint")
		Expect(html).NotTo(ContainSubstring("<article"))
		Expect(board.Patch.FragmentIDs()).NotTo(ContainElement(opsview.MinersRegion), "no series means no panel patch")
	})

	It("shows each miner's score as a series, and patches only the panel when a record lands", func() {
		appendTo(filepath.Join(directory, opsview.MutationFile), acceptedRecord)
		client := connect(mountView(directory, watcher.mock))
		panel := client.WaitFor(opsview.MinersRegion, showsScore("10/10 = 1.00"))
		client.Ack(panel.Patch.ServerSeq)
		html, _ := panel.Patch.Fragment(opsview.MinersRegion)
		Expect(html).To(ContainSubstring(`data-opsview-miner="draft-pr-late"`))
		Expect(html).To(ContainSubstring(`class="score accepted"`))
		Expect(html).To(ContainSubstring(`<li class="accepted"><span class="time">10-04 08:00</span> 10/10 = 1.00</li>`))

		appendTo(filepath.Join(directory, opsview.MutationFile), rejectedRecord)
		watcher.changes <- iofs.Change{Name: opsview.MutationFile, Op: iofs.ChangeWritten}
		panel = client.WaitFor(opsview.MinersRegion, showsScore("9/10 = 0.90"))
		Expect(panel.Patch.FragmentIDs()).To(Equal([]string{opsview.MinersRegion}), "the board did not re-render")
		client.Ack(panel.Patch.ServerSeq)
		html, _ = panel.Patch.Fragment(opsview.MinersRegion)
		Expect(html).To(ContainSubstring(`class="score rejected"`))
		Expect(strings.Count(html, `<li class="accepted">`)).To(Equal(1), "the earlier point stays in the series")
		Expect(strings.Count(html, `<li class="rejected">`)).To(Equal(1))
		Expect(html).To(ContainSubstring("rule 1: write 468 for knee(K)"), "the surviving mutant is listed")
	})
})
