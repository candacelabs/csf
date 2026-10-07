// Copyright 2026 Candace Labs

package opsview

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/services/harness/session"
)

// Records in the shapes the harness and its turn executor write, read from
// a real event log and reduced to the fields this view folds.
const (
	startedRecord   = `{"time":"2026-10-02T02:48:35.592623634Z","level":"INFO","msg":"harness run started","session_id":"ff7c7118","turn":0,"sequence":1,"event_type":"harness_run_started","assignment_id":"a1","agent_id":"g0","branch":"g0/staging-gates"}`
	requestedRecord = `{"time":"2026-10-02T02:48:36.803036478Z","level":"INFO","msg":"turn requested","turn":1,"sequence":3,"event_type":"harness_turn_requested","resume":false}`
	assistantRecord = `{"time":"2026-10-02T02:48:44.541585227Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"out","event_type":"assistant","event":{"type":"assistant","message":{"model":"claude-fable-5-1","role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"ls -la && git log --oneline -3","description":"List worktree root, workflows, tools"}}]}}}`
	twoToolsRecord  = `{"time":"2026-10-02T02:48:48.356181754Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"out","event_type":"assistant","event":{"type":"assistant","message":{"model":"claude-fable-5-1","content":[{"type":"text","text":"Reading."},{"type":"tool_use","name":"Read","input":{"file_path":"/workspace/README.md"}},{"type":"tool_use","name":"Grep","input":{"pattern":"term harness"}}]}}}`
	thinkingRecord  = `{"time":"2026-10-02T02:48:41.080274212Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"out","event_type":"system","event":{"type":"system","subtype":"thinking_tokens","estimated_tokens":50}}`
	allowRecord     = `{"time":"2026-10-02T02:48:44.560285178Z","level":"INFO","msg":"session gate decision","turn":1,"sequence":1,"event_type":"session_gate_decision","gate":"wait","decision":"allow","hook_event":"PreToolUse","command":"ls -la"}`
	denyRecord      = `{"time":"2026-10-02T02:32:38.054357671Z","level":"INFO","msg":"session gate decision","turn":1,"sequence":1,"event_type":"session_gate_decision","gate":"wait","decision":"deny","hook_event":"PreToolUse","command":"sleep 3","rules":["foreground_sleep"],"reason":"Rejected by the CSF session gate. foreground_sleep: \"sleep 3\" is not allowed in a CSF session; start the command with run_in_background"}`
	deniedRecord    = `{"time":"2026-10-02T03:34:50.862248739Z","level":"INFO","msg":"turn executor event","turn":2,"direction":"out","event_type":"system","event":{"type":"system","subtype":"permission_denied","tool_name":"Bash","tool_use_id":"toolu_02","decision_reason_type":"mode","message":"Permission to use Bash has been denied"}}`
	publishedRecord = `{"time":"2026-10-02T03:19:16.417999721Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"out","event_type":"system","event":{"type":"system","subtype":"code_change_published","provider":"github","url":"https://example.invalid/org/repo/pull/8","repo":"org/repo","identifier":"8","action":"pushed"}}`
	resultRecord    = `{"time":"2026-10-02T03:26:39.512113044Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"out","event_type":"result","event":{"type":"result","subtype":"success","duration_ms":2282708,"stop_reason":"end_turn"}}`
	finishedRecord  = `{"time":"2026-10-02T03:26:39.9213668Z","level":"INFO","msg":"turn finished","turn":1,"sequence":4,"event_type":"harness_run_finished","pull_request_url":"https://example.invalid/org/repo/pull/8"}`
	failedRecord    = `{"time":"2026-10-02T03:26:40Z","level":"ERROR","msg":"turn failed","turn":2,"sequence":6,"event_type":"harness_run_finished","error":"harness session: the turn failed: ipc/proc: claude: terminated signal received"}`
	closedRecord    = `{"time":"2026-10-02T03:43:15.710075025Z","level":"INFO","msg":"turn executor closed","turn":2,"sequence":7,"event_type":"harness_session_closed"}`
	// A suspend, the resume for the next message, and that turn's cost.
	suspendedRecord   = `{"time":"2026-10-02T03:40:00Z","level":"INFO","msg":"session suspended: turn executor closed","turn":2,"sequence":7,"event_type":"harness_session_suspended","idle_seconds":661}`
	resumedRecord     = `{"time":"2026-10-02T04:10:00Z","level":"INFO","msg":"session resumed: turn executor opened","turn":2,"sequence":8,"event_type":"harness_session_resumed","suspended_seconds":1800}`
	turnResumedRecord = `{"time":"2026-10-02T04:10:09Z","level":"INFO","msg":"turn on a resumed session","turn":3,"sequence":12,"event_type":"harness_turn_resumed","time_to_first_token_ms":3497.2,"suspended_seconds":1800}`
	messageRecord     = `{"time":"2026-10-02T03:30:00Z","level":"INFO","msg":"turn executor event","turn":2,"direction":"in","event_type":"user","event":{"type":"user","message":{"role":"user","content":"Push the fix and mark the PR ready.\nThen stop."}}}`
	questionRecord    = `{"time":"2026-10-02T03:31:00Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"out","event_type":"assistant","event":{"type":"assistant","message":{"model":"claude-fable-5-1","content":[{"type":"text","text":"Done with the gate.\n\nShould I rebase onto main first?"}]}}}`
	summaryRecord     = `{"time":"2026-10-02T03:31:00Z","level":"INFO","msg":"turn executor event","turn":1,"direction":"out","event_type":"assistant","event":{"type":"assistant","message":{"model":"claude-fable-5-1","content":[{"type":"text","text":"**Done:**\n- Merged the gate and **rebased** the branch.\n\n**Still open:** nothing."}]}}}`
)

func foldAll(lines ...string) SessionCard {
	var card SessionCard
	for _, line := range lines {
		var read bool
		card, read = Fold(card, []byte(line))
		Expect(read).To(BeTrue(), line)
	}
	return card
}

var _ = Describe("folding an event log into a card", func() {
	It("reads a session's start as running and keeps the first and last times", func() {
		card := foldAll(startedRecord, requestedRecord)
		Expect(card.Status).To(Equal(StatusRunning))
		Expect(card.StartedAt.Format("15:04:05")).To(Equal("02:48:35"))
		Expect(card.LastAt.Format("15:04:05")).To(Equal("02:48:36"))
		Expect(card.Elapsed).To(Equal("<1m"))
		Expect(card.Recent).To(HaveLen(2))
		Expect(card.Recent[1]).To(Equal(RecentEvent{Time: "02:48:36", Kind: KindHarness, Text: "Turn 1 started", Raw: "turn requested"}))
	})

	It("counts every tool call, takes the model from the first message and keeps a gist", func() {
		card := foldAll(startedRecord, assistantRecord, twoToolsRecord)
		Expect(card.Model).To(Equal("claude-fable-5-1"))
		Expect(card.ToolCalls).To(Equal(3))
		Expect(card.Recent[1].Raw).To(Equal("Bash: List worktree root, workflows, tools"))
		Expect(card.Recent[2].Raw).To(Equal("Read: /workspace/README.md"))
		Expect(card.Recent[3].Raw).To(Equal("Grep: term harness"))
	})

	It("counts only a gate's denials, and shows the executor's own denial without counting it", func() {
		card := foldAll(startedRecord, allowRecord, denyRecord, deniedRecord)
		Expect(card.GateDenials).To(Equal(1))
		Expect(card.Recent).To(HaveLen(3))
		Expect(card.Recent[1].Kind).To(Equal(KindGate))
		Expect(card.Recent[1].Raw).To(HavePrefix("gate wait denied: Rejected by the CSF session gate. foreground_sleep"))
		Expect(card.Recent[2].Raw).To(Equal("Bash denied by the executor"))
	})

	It("moves nothing on the wire on a thinking record, so no patch is needed", func() {
		before := foldAll(startedRecord, requestedRecord)
		after, read := Fold(before, []byte(thinkingRecord))
		Expect(read).To(BeTrue())
		Expect(after.LastAt).To(BeTemporally(">", before.LastAt), "the effect still knows the log moved")
		Expect(cardsEqual(before, after)).To(BeTrue(), "but the card on the wire is the same")
	})

	It("ends a turn as finished with its pull request, then closed", func() {
		card := foldAll(startedRecord, publishedRecord, resultRecord, finishedRecord)
		Expect(card.Status).To(Equal(StatusFinished))
		Expect(card.PullRequestURL).To(Equal("https://example.invalid/org/repo/pull/8"))
		Expect(card.Elapsed).To(Equal("38m"))
		Expect(card.Recent[len(card.Recent)-2].Raw).To(Equal("turn 1: success after 2283s"))
		Expect(card.Recent[len(card.Recent)-1].Raw).To(Equal("turn finished"))

		card = foldAll(startedRecord, finishedRecord, closedRecord)
		Expect(card.Status).To(Equal(StatusClosed))
	})

	It("reads a suspend as suspended, a resume as running, and names the resumed turn's cost", func() {
		card := foldAll(startedRecord, finishedRecord, suspendedRecord)
		Expect(card.Status).To(Equal(StatusSuspended))
		Expect(card.Recent[len(card.Recent)-1].Text).To(Equal("Suspended: idle, its real session closed until the next message"))
		Expect(card.Recent[len(card.Recent)-1].Raw).To(Equal("session suspended: turn executor closed"))

		card = foldAll(startedRecord, finishedRecord, suspendedRecord, resumedRecord, turnResumedRecord)
		Expect(card.Status).To(Equal(StatusRunning))
		Expect(card.Resumes).To(Equal(1))
		Expect(card.Recent[len(card.Recent)-1].Text).To(Equal("resumed after 1800s suspended: first token after 3.5s"))
	})

	It("reads a failed turn as failed with its error", func() {
		card := foldAll(startedRecord, failedRecord)
		Expect(card.Status).To(Equal(StatusFailed))
		Expect(card.Error).To(HavePrefix("harness session: the turn failed"))
		Expect(card.Recent[1].Raw).To(HavePrefix("turn failed: harness session: the turn failed"))
	})

	It("shows the operator's message by its first line and keeps only the last few events", func() {
		card := foldAll(startedRecord, requestedRecord, assistantRecord, twoToolsRecord, messageRecord)
		Expect(card.Recent).To(HaveLen(6))
		Expect(card.Recent[5]).To(Equal(RecentEvent{Time: "03:30:00", Kind: KindMessage, Text: "Message: Push the fix and mark the PR ready.", Raw: "message: Push the fix and mark the PR ready."}))
		for range recentKept {
			card, _ = Fold(card, []byte(assistantRecord))
		}
		Expect(card.Recent).To(HaveLen(recentKept), "the oldest events dropped off")
		Expect(card.Recent[0].Raw).To(Equal("Bash: List worktree root, workflows, tools"))
	})

	It("keeps the browser's expand choice across the card being replaced", func() {
		instance := sessionCardsOver(nil)(BoardRegion + ":a1")
		expanded, effects := instance.Reduce(SessionCard{}, live.Event{Name: EventExpand})
		Expect(effects).To(BeEmpty())
		Expect(expanded.Expanded).To(BeTrue())
		event, err := CardEvent(BoardRegion+":a1", foldAll(startedRecord, requestedRecord))
		Expect(err).NotTo(HaveOccurred())
		replaced, _ := instance.Reduce(expanded, event)
		Expect(replaced.Status).To(Equal(StatusRunning))
		Expect(replaced.Expanded).To(BeTrue())
		collapsed, _ := instance.Reduce(replaced, live.Event{Name: EventExpand})
		Expect(collapsed.Expanded).To(BeFalse())
	})

	It("says each event in words, and keeps what the session last said and whether it asked", func() {
		card := foldAll(startedRecord, requestedRecord, assistantRecord, twoToolsRecord)
		Expect(card.Recent[2].Text).To(Equal("List worktree root, workflows, tools"))
		Expect(card.Recent[3].Text).To(Equal("Reading /workspace/README.md"))
		Expect(card.Recent[4].Text).To(Equal("Searching for “term harness”"))
		Expect(card.Activity).To(Equal("Searching for “term harness”"), "the latest line is what it is doing now")

		card = foldAll(startedRecord, questionRecord)
		Expect(card.Said).To(Equal("Should I rebase onto main first?"))
		Expect(card.Question).To(BeTrue())

		card = foldAll(startedRecord, summaryRecord)
		Expect(card.Said).To(Equal("Merged the gate and rebased the branch."), "a heading line gives way to what it heads, without its Markdown")
		Expect(card.Question).To(BeFalse())

		card = foldAll(startedRecord, failedRecord)
		Expect(card.Problem).To(Equal(card.Recent[1].Text), "a failure is worded once, on the row and in its events")
		Expect(card.Problem).NotTo(HavePrefix("harness session:"))
	})

	It("reads the end of a long log first, and the whole log when asked", func() {
		var log strings.Builder
		log.WriteString(startedRecord + "\n")
		for log.Len() < tailWindow*2 {
			log.WriteString(assistantRecord + "\n")
		}
		log.WriteString(requestedRecord + "\n")
		directory := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(directory, "a1"), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, "a1", session.RunStateFile), []byte(`{"assignment_id":"a1","agent_id":"scratch","worktree":"/workspace"}`), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, "a1", session.EventsFile), []byte(log.String()), 0o600)).To(Succeed())
		host, err := iofs.NewHostFiles(directory)
		Expect(err).NotTo(HaveOccurred())

		brief := &tail{}
		changed, err := brief.read(host, "a1", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())
		Expect(brief.card.Partial).To(BeTrue())
		Expect(brief.card.Status).To(Equal(StatusRunning))
		Expect(brief.card.StartedAt.Format("15:04:05")).To(Equal("02:48:35"), "the first record still dates the session")
		partialCalls := brief.card.ToolCalls

		_, err = brief.read(host, "a1", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(brief.card.Partial).To(BeFalse())
		Expect(brief.card.ToolCalls).To(BeNumerically(">", partialCalls), "the whole log counts every call")
		whole := &tail{}
		_, err = whole.read(host, "a1", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(brief.card.ToolCalls).To(Equal(whole.card.ToolCalls))
	})

	It("reports a line that is not a record without changing the card", func() {
		before := foldAll(startedRecord)
		after, read := Fold(before, []byte("not json\n"))
		Expect(read).To(BeFalse())
		Expect(after).To(Equal(before))
	})

	DescribeTable("labels elapsed time at minute resolution",
		func(seconds int, label string) {
			Expect(elapsedLabel(secondsOf(seconds))).To(Equal(label))
		},
		Entry("under a minute", 59, "<1m"),
		Entry("minutes", 59*60+30, "59m"),
		Entry("hours", 3*3600+5*60, "3h 05m"),
	)
})

var _ = Describe("the sandbox receipt", func() {
	const receiptRecord = `{"time":"2026-10-02T02:49:00Z","level":"INFO","msg":"sandbox usage","turn":1,"event_type":"harness_sandbox_receipt","cpu_usec":52192000,"memory_peak_bytes":22315008}`

	It("shows the cgroup cpu and memory peak on the card", func() {
		card, read := Fold(SessionCard{}, []byte(receiptRecord))
		Expect(read).To(BeTrue())
		Expect(card.CPU).To(Equal("52.2s"))
		Expect(card.MemoryPeak).To(Equal("21.3 MiB"))
	})
})

var _ = Describe("the launch receipt", func() {
	It("shows a container session's launch, cpu and memory peak", func() {
		card, read := Fold(SessionCard{}, []byte(`{"time":"2026-10-05T00:31:57Z","level":"INFO","msg":"session launch receipt","turn":1,"event_type":"harness_launch_receipt","launch":"container","container_id":"264c278a95d6","cpu_usec":52439573,"memory_peak_bytes":1041604608}`))
		Expect(read).To(BeTrue())
		Expect([]string{card.Launch, card.CPU, card.MemoryPeak}).To(Equal([]string{"container", "52.4s", "993.4 MiB"}))
	})

	It("shows a host session's launch with no usage", func() {
		card, _ := Fold(SessionCard{}, []byte(`{"time":"2026-10-05T00:31:57Z","level":"INFO","msg":"session launch receipt","turn":1,"event_type":"harness_launch_receipt","launch":"host"}`))
		Expect([]string{card.Launch, card.CPU, card.MemoryPeak}).To(Equal([]string{"host", "", ""}))
	})
})

var _ = Describe("background tasks", func() {
	const (
		twoRunning = `{"time":"2026-10-02T03:27:00Z","level":"INFO","msg":"turn executor event","turn":0,"direction":"out","event_type":"system","event":{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"b1","task_type":"local_bash","description":"Run the proof"},{"task_id":"b2","task_type":"local_bash","description":"Run the house lint"}]}}`
		noneLeft   = `{"time":"2026-10-02T04:30:00Z","level":"INFO","msg":"turn executor event","turn":0,"direction":"out","event_type":"system","event":{"type":"system","subtype":"background_tasks_changed","tasks":[]}}`
		woken      = `{"time":"2026-10-02T04:30:01Z","level":"INFO","msg":"background result","turn":2,"event_type":"harness_background_result","task_id":"b1","status":"completed","summary":"Run the proof","task_elapsed_ms":3780000,"exit_code":0,"output_tail":"ok\n[exited with code 0]\n"}`
	)

	It("shows a finished turn with tasks still running as waiting, and finished again once none is", func() {
		card := foldAll(startedRecord, requestedRecord, finishedRecord, twoRunning)
		Expect(card.Status).To(Equal(StatusWaiting))
		Expect(card.Background).To(Equal(2))

		card = foldAll(startedRecord, requestedRecord, finishedRecord, twoRunning, noneLeft, woken)
		Expect(card.Status).To(Equal(StatusFinished))
		Expect(card.Background).To(Equal(0))
		Expect(card.BackgroundTurns).To(Equal(1))
		Expect(card.Recent[len(card.Recent)-1]).To(Equal(RecentEvent{Time: "04:30:01", Kind: KindHarness, Text: "turn 2 woken: Run the proof completed after 3780s, exit 0", Raw: "turn 2 woken: Run the proof completed after 3780s, exit 0"}))
	})

	It("keeps a running turn running whatever runs in the background, and a closed session runs none", func() {
		Expect(foldAll(startedRecord, requestedRecord, twoRunning).Status).To(Equal(StatusRunning))
		card := foldAll(startedRecord, finishedRecord, twoRunning, closedRecord)
		Expect([]any{card.Status, card.Background}).To(Equal([]any{StatusClosed, 0}))
	})

	It("ranks a waiting session between running and finished ones", func() {
		Expect(statusRank(StatusRunning)).To(BeNumerically("<", statusRank(StatusWaiting)))
		Expect(statusRank(StatusWaiting)).To(BeNumerically("<", statusRank(StatusFinished)))
	})
})

var _ = Describe("the display order", func() {
	It("puts running sessions first, then the most recently started", func() {
		running := foldAll(startedRecord, requestedRecord)
		finished := foldAll(startedRecord, finishedRecord)
		later := foldAll(strings.Replace(startedRecord, "02:48:35", "03:48:35", 1), requestedRecord)
		suspended := foldAll(startedRecord, finishedRecord, suspendedRecord)
		state, err := newCards(cardOf("old-finished", finished), cardOf("running", running), cardOf("late", later), cardOf("suspended", suspended))
		Expect(err).NotTo(HaveOccurred())
		Expect(displayOrder(state)).To(Equal([]string{"late", "running", "old-finished", "suspended"}), "a suspended session ranks after one whose executor is open")
	})
})
