// Copyright 2026 Candace Labs

package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/net/model/copilotcli"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/gotth/live"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

func TestChat(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "services/harness/chat Suite")
}

const (
	userRecord   = `{"time":"2026-10-02T01:10:03Z","msg":"turn executor event","event_type":"user","turn":1,"direction":"in","event":{"type":"user","message":{"role":"user","content":"Fix the build."}}}`
	textRecord   = `{"time":"2026-10-02T01:10:04Z","msg":"turn executor event","event_type":"assistant","turn":1,"direction":"out","provider":"claudecode","event":{"type":"assistant","message":{"model":"claude-opus-5-5","content":[{"type":"text","text":"On it."}],"usage":{"input_tokens":2,"cache_read_input_tokens":1000,"cache_creation_input_tokens":500}}}}`
	bashRecord   = `{"time":"2026-10-02T01:10:04Z","msg":"turn executor event","event_type":"assistant","turn":1,"direction":"out","event":{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"go build ./...\necho done","description":"Build"}}]}}}`
	doneRecord   = `{"time":"2026-10-02T01:10:06.5Z","msg":"turn executor event","event_type":"user","turn":1,"direction":"out","event":{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}}}`
	failRecord   = `{"time":"2026-10-02T01:10:05Z","msg":"turn executor event","event_type":"user","turn":1,"direction":"out","event":{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"exit status 1"}],"is_error":true}]}}}`
	resultRecord = `{"time":"2026-10-02T01:10:06Z","msg":"turn executor event","event_type":"result","turn":1,"direction":"out","event":{"type":"result","subtype":"success","duration_ms":2500,"total_cost_usd":0.5}}`
	startRecord  = `{"time":"2026-10-02T01:10:02Z","msg":"turn requested","event_type":"harness_turn_requested","turn":1}`
	denyRecord   = `{"time":"2026-10-02T01:10:07Z","msg":"session gate decision","event_type":"session_gate_decision","turn":1,"gate":"wait","decision":"deny","reason":"Rejected by the CSF session gate. pgrep_full"}`
	replyRecord  = `{"time":"2026-10-02T01:10:07Z","msg":"session gate decision","event_type":"session_gate_decision","turn":1,"gate":"reply","decision":"allow"}`
	waitRecord   = `{"time":"2026-10-02T01:10:07Z","msg":"session gate decision","event_type":"session_gate_decision","turn":1,"gate":"wait","decision":"allow"}`
	skipRecord   = `{"time":"2026-10-02T01:10:07Z","msg":"session gate decision","event_type":"session_gate_decision","turn":1,"gate":"commit","decision":"skip"}`
	harnessLine  = `{"time":"2026-10-02T01:10:08Z","msg":"turn finished","event_type":"harness_run_finished","turn":1}`
	controlLine  = `{"time":"2026-10-02T01:10:08Z","msg":"control action","event_type":"harness_control_action","action":"send"}`
	systemRecord = `{"time":"2026-10-02T01:10:03Z","msg":"turn executor event","event_type":"system","turn":1,"direction":"out","event":{"type":"system","subtype":"init","model":"claude-opus-5-5"}}`
	editRecord   = `{"time":"2026-10-02T01:10:04Z","msg":"turn executor event","event_type":"assistant","turn":1,"direction":"out","event":{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_2","name":"Edit","input":{"file_path":"/work/tree/pkg/x.go","old_string":"a\nb\nc","new_string":"a\nB\nc\nd"}}]}}}`
	todoRecord   = `{"time":"2026-10-02T01:10:04Z","msg":"turn executor event","event_type":"assistant","turn":1,"direction":"out","event":{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_3","name":"TodoWrite","input":{"todos":[{"content":"Read it","status":"completed","activeForm":"Reading it"},{"content":"Fix it","status":"in_progress","activeForm":"Fixing it"},{"content":"Test it","status":"pending","activeForm":"Testing it"}]}}]}}}`
	worktree     = "/work/tree"
)

// folded is the transcript the lines fold to, from empty.
func folded(lines ...string) transcript {
	var shown transcript
	for _, line := range lines {
		shown = shown.apply([]byte(line), worktree)
	}
	return shown
}

var _ = Describe("the transcript", func() {
	DescribeTable("folds one record into at most one row",
		func(line string, kind string, text string) {
			rows := folded(line).Rows
			if kind == "" {
				Expect(rows).To(BeEmpty())
				return
			}
			Expect(rows).To(HaveLen(1))
			Expect(rows[0].Kind).To(Equal(kind))
			Expect(rows[0].Text).To(ContainSubstring(text))
			Expect(rows[0].Time).To(MatchRegexp(`^\d\d:\d\d$`))
		},
		Entry("the operator's message", userRecord, rowOperator, "Fix the build."),
		Entry("what the assistant said", textRecord, rowAgent, "On it."),
		Entry("the end of a turn", resultRecord, rowTurn, "Turn 1 finished in 2.5s"),
		Entry("a gate's refusal", denyRecord, rowGate, "The wait gate refused: "),
		Entry("the reply gate's every decision", replyRecord, rowGate, "Reply gate allowed"),
		Entry("the wait gate's allowance is not shown", waitRecord, "", ""),
		Entry("a gate that skipped is not shown", skipRecord, "", ""),
		Entry("a harness record", harnessLine, rowSystem, "turn finished"),
		Entry("a control action is not shown: the transcript shows what it did", controlLine, "", ""),
		Entry("a system event is not shown", systemRecord, "", ""),
		Entry("a line that is not a record", "not json", "", ""),
	)

	It("shows a tool call as one row that runs, then finishes with its output and elapsed time", func() {
		running := folded(startRecord, bashRecord)
		Expect(running.Rows).To(HaveLen(2))
		call := running.Rows[1].Tool
		Expect(call).To(And(HaveField("Name", "Bash"), HaveField("Target", "go build ./..."), HaveField("State", toolRunning)))
		Expect(call.Input).To(Equal("{\n  \"command\": \"go build ./...\\necho done\",\n  \"description\": \"Build\"\n}"), "the input in full, keys sorted")
		Expect(running.Status.Phase).To(Equal(phaseTool))

		done := running.apply([]byte(doneRecord), worktree)
		Expect(done.Rows[1].Tool.State).To(Equal(toolDone))
		Expect(done.Rows[1].Tool.Output).To(Equal("ok"))
		Expect(done.Rows[1].Tool.Elapsed).To(Equal("2.5s"))
		Expect(done.Status.Phase).To(Equal(phaseThinking))
		Expect(running.Rows[1].Tool.State).To(Equal(toolRunning), "the fold returns a new transcript and leaves the one it was given alone")

		failed := running.apply([]byte(failRecord), worktree)
		Expect(failed.Rows[1].Tool.State).To(Equal(toolFailed))
		Expect(failed.Rows[1].Tool.Output).To(Equal("exit status 1"), "a result's text blocks are its output")
	})

	It("ends a call that never reported a result with its turn, and goes idle", func() {
		ended := folded(startRecord, bashRecord, resultRecord)
		Expect(ended.Rows[1].Tool.State).To(Equal(toolEnded))
		Expect(ended.Status.Phase).To(Equal(phaseIdle))
	})

	It("keeps a long output's first lines and the rest behind a disclosure, cut at its bound", func() {
		head, rest, more, cut := splitOutput("1\n2\n3\n4\n5\n6\n7\n8\n")
		Expect(head).To(Equal("1\n2\n3\n4\n5\n6"))
		Expect(rest).To(Equal("7\n8"))
		Expect(more).To(Equal(2))
		Expect(cut).To(BeFalse())
		_, _, _, cut = splitOutput(strings.Repeat("x", outputLimit+1))
		Expect(cut).To(BeTrue())
	})

	It("shows an edit as numbered red and green lines with its counts, from a pinned diff library", func() {
		call := folded(editRecord).Rows[0].Tool
		Expect(call.Name).To(Equal("Edit"))
		Expect(call.Target).To(Equal("pkg/x.go"), "relative to the worktree")
		Expect(call.Diff.Added).To(Equal(2))
		Expect(call.Diff.Removed).To(Equal(1))
		Expect(call.Diff.Lines).To(Equal([]diffLine{
			{Kind: lineKept, Old: 1, New: 1, Text: "a"},
			{Kind: lineRemoved, Old: 2, Text: "b"},
			{Kind: lineAdded, New: 2, Text: "B"},
			{Kind: lineKept, Old: 3, New: 3, Text: "c"},
			{Kind: lineAdded, New: 4, Text: "d"},
		}))
		written := diffOf(toolWrite, json.RawMessage(`{"file_path":"/work/tree/n.md","content":"one\ntwo\n"}`))
		Expect(written.Added).To(Equal(2), "a Write adds every line")
		Expect(written.Removed).To(BeZero())
	})

	It("bounds an edit's shown lines and counts the rest", func() {
		change := diff{}.with("", strings.Repeat("line\n", diffLineCap+5))
		Expect(change.Lines).To(HaveLen(diffLineCap))
		Expect(change.Hidden).To(Equal(5))
		Expect(change.Added).To(Equal(diffLineCap + 5))
	})

	It("pins the session's task list, replaced whole by each TodoWrite, and shows no row for it", func() {
		shown := folded(todoRecord)
		Expect(shown.Rows).To(BeEmpty())
		Expect(shown.Tasks).To(Equal([]task{{Text: "Read it", State: taskDone}, {Text: "Fixing it", State: taskActive}, {Text: "Test it", State: taskPending}}))
	})

	It("reads the status bar: executor, model, context, turn and session cost, turn times and phase", func() {
		shown := folded(startRecord, systemRecord, textRecord)
		Expect(shown.Status.Executor).To(Equal("claudecode"))
		Expect(shown.Status.Model).To(Equal("claude-opus-5-5"))
		Expect(shown.Status.Context).To(Equal(int64(1502)))
		Expect(shown.Status.Phase).To(Equal(phaseThinking))
		shown = shown.apply([]byte(resultRecord), worktree)
		Expect(shown.Status.CostKnown).To(BeTrue())
		Expect(shown.Status.TurnCost).To(Equal(0.5))
		shown = shown.apply([]byte(startRecord), worktree)
		shown = shown.apply([]byte(strings.Replace(resultRecord, `0.5}`, `0.75}`, 1)), worktree)
		Expect(shown.Status.TurnCost).To(Equal(0.25), "the executor's cost is cumulative within its process")
		Expect(shown.Status.SessionCost).To(Equal(0.75))
		shown = shown.apply([]byte(startRecord), worktree)
		shown = shown.apply([]byte(strings.Replace(resultRecord, `0.5}`, `0.1}`, 1)), worktree)
		Expect(shown.Status.TurnCost).To(Equal(0.1), "a reopened executor counts from zero")
		Expect(shown.Status.SessionCost).To(BeNumerically("~", 0.85, 1e-9))
		Expect(barOf(chatState{Transcript: shown})).To(Equal(barView{Executor: "Claude Code", Model: "claude-opus-5-5", Context: "1.5k tokens", TurnCost: "$0.10", Session: "$0.85", Elapsed: "4.0s", Phase: phaseIdle}))
	})

	It("keeps every string valid UTF-8, so the page can always be sent", func() {
		// A gate reason of multi-byte characters past the bound used to be cut
		// mid-character, and the invalid frame closed every connection 4012.
		reason := strings.Repeat("é", gateLimit)
		row := folded(`{"time":"2026-10-02T01:10:07Z","event_type":"session_gate_decision","turn":1,"gate":"wait","decision":"deny","reason":"` + reason + `"}`).Rows[0]
		Expect(utf8.ValidString(row.Text)).To(BeTrue())
		Expect(row.Text).To(HaveSuffix("…"))
	})

	It("renders Markdown and omits raw HTML, so a message cannot script the page", func() {
		Expect(string(renderMarkdown("Use **care** with `rm`.\n\n```go\nfunc main() {}\n```\n\n<script>alert(1)</script>"))).To(Equal(
			"<p>Use <strong>care</strong> with <code>rm</code>.</p>\n<pre><code class=\"language-go\">func main() {}\n</code></pre>\n<!-- raw HTML omitted -->\n"))
	})

	It("keeps the transcript bounded", func() {
		var shown transcript
		for range rowCap + 5 {
			shown = shown.apply([]byte(userRecord), worktree)
		}
		Expect(shown.Rows).To(HaveLen(rowCap))
	})

	It("folds the proof session's turn into the rows the page shows", func() {
		content, err := os.ReadFile(filepath.Join("testdata", "proof-turn.jsonl"))
		Expect(err).NotTo(HaveOccurred())
		shown := folded(strings.Split(strings.TrimSpace(string(content)), "\n")...)
		var kinds []string
		for _, row := range shown.Rows {
			kinds = append(kinds, row.Kind+":"+row.Tool.Name)
		}
		Expect(kinds).To(Equal([]string{"system:", "operator:", "agent:", "tool:Read", "tool:Edit", "tool:Bash", "agent:", "gate:", "turn:"}))
		Expect(shown.Tasks).To(HaveEach(HaveField("State", taskDone)))
		Expect(shown.Status.Phase).To(Equal(phaseIdle))
	})
})

// The same turn as Claude Code records it and as Copilot writes it. Copilot's
// records go through the copilotcli executor, which translates them into
// the event log exactly as a Copilot session's log holds them.
const (
	parityPrompt = "Rename the greeting and run its tests."
	parityFile   = "1\tpackage notes\n2\tfunc Greeting() string { return \"Hello, world\" }"
	parityTest   = "ok  \texample.com/notes\t0.012s"
	paritySaid   = "I'll change the greeting, then run the **notes** tests."
	parityDone   = "Done: the greeting reads **Hello, CSF**."
)

var parityClaude = []string{
	`{"msg":"turn executor event","turn":1,"provider":"claudecode","direction":"in","event_type":"user","event":{"type":"user","message":{"role":"user","content":"` + parityPrompt + `"}}}`,
	`{"msg":"turn executor event","turn":1,"provider":"claudecode","direction":"out","event_type":"assistant","event":{"type":"assistant","message":{"content":[{"type":"text","text":"` + jsonText(paritySaid) + `"}]}}}`,
	`{"msg":"turn executor event","turn":1,"provider":"claudecode","direction":"out","event_type":"assistant","event":{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_02","name":"Read","input":{"file_path":"/work/tree/notes/greeting.go"}}]}}}`,
	`{"msg":"turn executor event","turn":1,"provider":"claudecode","direction":"out","event_type":"user","event":{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_02","content":"` + jsonText(parityFile) + `"}]}}}`,
	`{"msg":"turn executor event","turn":1,"provider":"claudecode","direction":"out","event_type":"assistant","event":{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_04","name":"Edit","input":{"file_path":"/work/tree/notes/greeting.go","old_string":"Hello, world","new_string":"Hello, CSF"}}]}}}`,
	`{"msg":"turn executor event","turn":1,"provider":"claudecode","direction":"out","event_type":"user","event":{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_04","content":"The file was updated."}]}}}`,
	`{"msg":"turn executor event","turn":1,"provider":"claudecode","direction":"out","event_type":"assistant","event":{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_06","name":"Bash","input":{"command":"go test ./notes/...","description":"Run the notes tests"}}]}}}`,
	`{"msg":"turn executor event","turn":1,"provider":"claudecode","direction":"out","event_type":"user","event":{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_06","content":"` + jsonText(parityTest) + `"}]}}}`,
	`{"msg":"turn executor event","turn":1,"provider":"claudecode","direction":"out","event_type":"assistant","event":{"type":"assistant","message":{"content":[{"type":"text","text":"` + jsonText(parityDone) + `"}]}}}`,
	`{"msg":"turn executor event","turn":1,"provider":"claudecode","direction":"out","event_type":"result","event":{"type":"result","subtype":"success","duration_ms":18000}}`,
}

var paritySession = uuid.MustParse("3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a02")

// parityCopilot is Copilot 1.0.90's output for the turn, in its JSON record
// shapes.
var parityCopilot = []string{
	`{"type":"user.message","data":{"content":"` + parityPrompt + `"},"id":"u1","timestamp":"2026-10-05T06:40:00Z"}`,
	`{"type":"assistant.message","data":{"content":"` + jsonText(paritySaid) + `","toolRequests":[{"toolCallId":"toolu_02","name":"view","arguments":{"path":"/work/tree/notes/greeting.go"}}]},"id":"a1","timestamp":"2026-10-05T06:40:01Z"}`,
	`{"type":"tool.execution_complete","data":{"toolCallId":"toolu_02","success":true,"result":{"content":"` + jsonText(parityFile) + `"}},"id":"t1","timestamp":"2026-10-05T06:40:01Z"}`,
	`{"type":"assistant.message","data":{"content":"","toolRequests":[{"toolCallId":"toolu_04","name":"edit","arguments":{"path":"/work/tree/notes/greeting.go","old_str":"Hello, world","new_str":"Hello, CSF"}}]},"id":"a2","timestamp":"2026-10-05T06:40:02Z"}`,
	`{"type":"tool.execution_complete","data":{"toolCallId":"toolu_04","success":true,"result":{"content":"The file was updated."}},"id":"t2","timestamp":"2026-10-05T06:40:02Z"}`,
	`{"type":"assistant.message","data":{"content":"","toolRequests":[{"toolCallId":"toolu_06","name":"bash","arguments":{"command":"go test ./notes/...","description":"Run the notes tests"}}]},"id":"a3","timestamp":"2026-10-05T06:40:03Z"}`,
	`{"type":"tool.execution_complete","data":{"toolCallId":"toolu_06","success":true,"result":{"content":"` + jsonText(parityTest) + `"}},"id":"t3","timestamp":"2026-10-05T06:40:04Z"}`,
	`{"type":"assistant.message","data":{"content":"` + jsonText(parityDone) + `","toolRequests":[]},"id":"a4","timestamp":"2026-10-05T06:40:05Z"}`,
	`{"type":"result","timestamp":"2026-10-05T06:40:05Z","sessionId":"3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a02","exitCode":0,"usage":{"sessionDurationMs":18000}}`,
}

// jsonText is text as the inside of a JSON string. It runs as the fixtures
// are declared, before any spec; marshalling a string cannot fail.
func jsonText(text string) string {
	encoded, _ := json.Marshal(text)
	return string(encoded[1 : len(encoded)-1])
}

// stamped is the executor event records among lines, each given the same
// time, so the two executors' clocks do not differ in what is compared.
func stamped(lines []string) []string {
	var events []string
	for _, line := range lines {
		// A log record is an object of slog's choosing.
		var record map[string]any
		Expect(json.Unmarshal([]byte(line), &record)).To(Succeed())
		if record["msg"] != "turn executor event" {
			continue
		}
		record["time"] = "2026-10-05T06:40:00Z"
		encoded, err := json.Marshal(record)
		Expect(err).NotTo(HaveOccurred())
		events = append(events, string(encoded))
	}
	return events
}

// copilotLog runs the Copilot turn through the copilotcli executor over a
// launcher double and returns the event log lines it writes.
func copilotLog() []string {
	controller := gomock.NewController(GinkgoT())
	launcher := NewMockILauncher(controller)
	launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, command proc.Command) (proc.Result, error) {
		_, err := io.ReadAll(command.Stdin)
		Expect(err).NotTo(HaveOccurred())
		for _, line := range parityCopilot {
			_, err := io.WriteString(command.Stdout, line+"\n")
			Expect(err).NotTo(HaveOccurred())
		}
		return proc.Result{}, nil
	})
	var log bytes.Buffer
	executor, err := copilotcli.NewCopilotCLIExecutor(launcher, paritySession,
		copilotcli.WithExecutable("/opt/copilot"), copilotcli.WithDirectory(worktree),
		copilotcli.WithUsageFile(filepath.Join(GinkgoT().TempDir(), "usage.json")),
		copilotcli.WithLogger(slog.New(slog.NewJSONHandler(&log, nil))))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(executor.Close(context.Background())).To(Succeed()) })
	_, err = executor.Propose(context.Background(), &copilotcli.Turn{Prompt: parityPrompt})
	Expect(err).NotTo(HaveOccurred())
	var lines []string
	scanner := bufio.NewScanner(&log)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<22)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

// transcriptHTML is the transcript region a page shows for lines.
func transcriptHTML(lines []string) string {
	var page bytes.Buffer
	Expect(renderTranscript(chatState{Transcript: folded(lines...)}).Render(context.Background(), &page)).To(Succeed())
	return page.String()
}

var _ = Describe("a Copilot session's turn", func() {
	It("renders exactly as the same turn from Claude Code: one model, one mapper per executor", func() {
		claude := transcriptHTML(stamped(parityClaude))
		copilot := transcriptHTML(stamped(copilotLog()))
		Expect(copilot).To(Equal(claude))
		Expect(claude).To(ContainSubstring(`data-chat-state="done"`))
		Expect(claude).To(ContainSubstring(`<span class="plus">+1</span> <span class="minus">−1</span>`), "the edit's diff, from either executor's input")
		Expect(strings.Count(claude, `data-chat="tool"`)).To(Equal(3))
	})
})

var _ = Describe("the reducer", func() {
	var chat *Chat

	BeforeEach(func() { chat = &Chat{} })

	It("folds records into the transcript and advances the cursor on every record", func() {
		state, effects := chat.reduce(chatState{Cursor: 4}, live.Event{Name: eventRecord, Fields: live.NewFields(map[string]string{fieldLine: userRecord})})
		Expect(effects).To(BeEmpty())
		Expect(state.Cursor).To(Equal(5))
		Expect(state.Transcript.Rows).To(HaveLen(1))
		state, _ = chat.reduce(state, live.Event{Name: eventRecord, Fields: live.NewFields(map[string]string{fieldLine: systemRecord})})
		Expect(state.Cursor).To(Equal(6), "every record moves the cursor, shown or not")
		Expect(state.Transcript.Rows).To(HaveLen(1))
	})

	It("folds a frame of several records in order, advancing the cursor once per record", func() {
		frame := strings.Join([]string{startRecord, bashRecord, doneRecord}, "\n")
		state, _ := chat.reduce(chatState{}, live.Event{Name: eventRecord, Fields: live.NewFields(map[string]string{fieldLine: frame})})
		Expect(state.Cursor).To(Equal(3))
		Expect(state.Transcript.Rows).To(HaveLen(2))
		Expect(state.Transcript.Rows[1].Tool.State).To(Equal(toolDone))
	})

	It("opens and closes a row on the viewer's word, and ignores a key that is not a row", func() {
		state, _ := chat.reduce(chatState{}, live.Event{Name: eventToggle, Fields: live.NewFields(map[string]string{fieldRow: "3"})})
		Expect(state.Open).To(Equal([]int{3}))
		state, _ = chat.reduce(state, live.Event{Name: eventToggle, Fields: live.NewFields(map[string]string{fieldRow: "nope"})})
		Expect(state.Open).To(Equal([]int{3}))
		state, _ = chat.reduce(state, live.Event{Name: eventToggle, Fields: live.NewFields(map[string]string{fieldRow: "3"})})
		Expect(state.Open).To(BeEmpty())
	})

	It("keeps an open row open when a patch lands on it, and patches that row alone", func() {
		running, _ := chat.reduce(chatState{}, live.Event{Name: eventRecord, Fields: live.NewFields(map[string]string{fieldLine: startRecord + "\n" + bashRecord})})
		seq := running.Transcript.Rows[1].Seq
		opened, _ := chat.reduce(running, live.Event{Name: eventToggle, Fields: live.NewFields(map[string]string{fieldRow: strconv.Itoa(seq)})})
		Expect(rowChanged(seq)(running, opened)).To(BeTrue(), "opening is the row's change")

		finished, _ := chat.reduce(opened, live.Event{Name: eventRecord, Fields: live.NewFields(map[string]string{fieldLine: doneRecord})})
		Expect(rowChanged(seq)(opened, finished)).To(BeTrue())
		Expect(rowChanged(running.Transcript.Rows[0].Seq)(opened, finished)).To(BeFalse(), "the other row is not patched")
		Expect(transcriptChanged(opened, finished)).To(BeFalse(), "the transcript itself is not re-sent")
		var markup bytes.Buffer
		Expect(renderRow(seq)(finished).Render(context.Background(), &markup)).To(Succeed())
		Expect(markup.String()).To(HavePrefix(`<li id="chat.transcript:r` + strconv.Itoa(seq) + `" data-gotth-region="chat.transcript:r` + strconv.Itoa(seq) + `" class="tool done"`))
		Expect(markup.String()).To(ContainSubstring(`data-chat-open="true"><details open>`), "the patch renders the row open")
		Expect(markup.String()).To(ContainSubstring("click:chat.toggle"))
	})

	It("keys every row by a sequence number it keeps for life, past the cap", func() {
		var state chatState
		for range rowCap + 3 {
			state, _ = chat.reduce(state, live.Event{Name: eventRecord, Fields: live.NewFields(map[string]string{fieldLine: userRecord})})
		}
		regions := rowRegions(state)
		Expect(regions).To(HaveLen(rowCap))
		Expect(regions[0].ID).To(Equal("chat.transcript:r3"), "the three oldest rows dropped; the rest keep their keys")
		Expect(regions[rowCap-1].ID).To(Equal(rowRegion(rowCap + 2)))
	})

	It("takes the session's state from the follow effect", func() {
		state, _ := chat.reduce(chatState{}, live.Event{Name: eventState, Fields: live.NewFields(map[string]string{fieldState: `{"AgentID":"orchestrator","Phase":"open","Turns":3,"Queued":1}`})})
		Expect(state.Session).To(Equal(sessionView{AgentID: "orchestrator", Phase: "open", Turns: 3, Queued: 1}))
	})

	It("sends a message as an effect and refuses an empty one", func() {
		state, effects := chat.reduce(chatState{Assignment: "a"}, live.Event{Name: eventSend, Fields: live.NewFields(map[string]string{fieldMessage: "  hello  "})})
		Expect(state.Pending).To(BeTrue())
		Expect(effects).To(HaveLen(1))
		Expect(effects[0].Source).To(Equal(sourceSend))
		state, effects = chat.reduce(chatState{}, live.Event{Name: eventSend, Fields: live.NewFields(map[string]string{fieldMessage: "   "})})
		Expect(effects).To(BeEmpty())
		Expect(state.Notice).To(Equal("Write a message first."))
	})

	It("shows a sent message as queued until the session reads it", func() {
		state, _ := chat.reduce(chatState{Pending: true}, live.Event{Name: eventSent, Fields: live.NewFields(map[string]string{fieldTurn: "4", fieldMessage: "Fix the build."})})
		Expect(state.Pending).To(BeFalse())
		Expect(state.Queued).To(Equal([]string{"Fix the build."}))
		state, _ = chat.reduce(state, live.Event{Name: eventRecord, Fields: live.NewFields(map[string]string{fieldLine: userRecord})})
		Expect(state.Queued).To(BeEmpty(), "the session read it")
	})

	It("reports a failed effect", func() {
		state, _ := chat.reduce(chatState{Pending: true}, live.Event{Name: live.EffectFailedEvent, Fields: live.NewFields(map[string]string{live.EffectFailedSourceField: sourceSend, live.EffectFailedErrorField: "the session has finished"})})
		Expect(state.Pending).To(BeFalse())
		Expect(state.Notice).To(Equal("Send failed: The session has finished"))
	})

	It("cancels only once the operator confirmed it", func() {
		state, effects := chat.reduce(chatState{Assignment: "a"}, live.Event{Name: eventCancel})
		Expect(effects).To(BeEmpty(), "an unconfirmed cancel asks first")
		Expect(state.Confirm).To(BeTrue())
		state, _ = chat.reduce(state, live.Event{Name: eventKeep})
		Expect(state.Confirm).To(BeFalse())
		state, _ = chat.reduce(state, live.Event{Name: eventAskCancel})
		state, effects = chat.reduce(state, live.Event{Name: eventCancel})
		Expect(effects).To(HaveLen(1))
		Expect(effects[0].Source).To(Equal(sourceCancel))
		Expect(state.Confirm).To(BeFalse())
	})

	It("says the real session's state beside the virtual session's phase", func() {
		Expect(viewOf(&harnessv1.AgentSessionState{Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN}).Real).To(Equal(realAlive))
		Expect(viewOf(&harnessv1.AgentSessionState{Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN, Suspended: true}).Real).To(Equal(realSuspended))
		Expect(viewOf(&harnessv1.AgentSessionState{Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CLOSED}).Real).To(Equal(realClosed))
	})

	It("declares what each fragment depends on", func() {
		before := chatState{Cursor: 1, Session: sessionView{Phase: "open"}}
		for _, dirty := range []func(previous, next chatState) bool{statusChanged, transcriptChanged, barChanged, composerChanged} {
			Expect(dirty(before, before)).To(BeFalse())
		}
		Expect(statusChanged(before, chatState{Cursor: 1, Session: sessionView{Phase: "running"}})).To(BeTrue())
		Expect(tasksChanged(before, chatState{Session: before.Session, Transcript: transcript{TasksRev: 1}})).To(BeTrue())
		Expect(transcriptChanged(before, chatState{Session: before.Session, Queued: []string{"x"}})).To(BeTrue())
		Expect(barChanged(before, chatState{Session: before.Session, Transcript: transcript{Status: statusBar{Phase: phaseTool}}})).To(BeTrue())
		Expect(composerChanged(before, chatState{Cursor: 1, Session: before.Session, Pending: true})).To(BeTrue())
	})
})

var _ = Describe("the page", func() {
	It("renders its five regions and the live runtime for the session's mount", func() {
		state := chatState{Assignment: "0caf2d51-1ee7-468a-882f-ac478a437640", Mount: "/chat/0caf2d51-1ee7-468a-882f-ac478a437640/live",
			Session: sessionView{AgentID: "orchestrator", Phase: "open", Turns: 2, Real: realAlive}, Queued: []string{"<b>Also</b> push it"},
			Transcript: folded(startRecord, userRecord, todoRecord, editRecord, `{"time":"2026-10-02T01:10:07Z","event_type":"harness_note","msg":"<b>Fix</b> it"}`)}
		var page bytes.Buffer
		Expect(renderPage(state).Render(context.Background(), &page)).To(Succeed())
		html := page.String()
		for _, region := range []string{fragmentStatus, fragmentTasks, fragmentTranscript, fragmentBar, fragmentComposer} {
			Expect(html).To(ContainSubstring(`data-gotth-region="` + region + `"`))
		}
		Expect(html).To(ContainSubstring("submit:chat.send"))
		Expect(html).To(ContainSubstring("keydown:chat.send:Enter"), "Enter sends")
		Expect(html).To(ContainSubstring("keydown:chat.ask_cancel:Escape"), "Esc interrupts, asking first")
		Expect(html).To(ContainSubstring("click:chat.ask_cancel"), "cancel asks before it acts")
		Expect(html).To(ContainSubstring(`real session <b data-chat="real">alive</b>`))
		Expect(html).To(ContainSubstring(`href="/"`), "the header links back to the Workbench")
		Expect(html).To(ContainSubstring(state.Mount))
		Expect(html).To(ContainSubstring("&lt;b&gt;Fix&lt;/b&gt; it"), "plain transcript text is escaped")
		Expect(html).To(ContainSubstring("&lt;b&gt;Also&lt;/b&gt; push it"), "a queued message is escaped")
		Expect(html).To(ContainSubstring(`data-chat="queued"`))
		Expect(html).To(ContainSubstring("<p>Fix the build.</p>"), "a message is rendered from Markdown")
		Expect(html).To(ContainSubstring("Tasks · 1 of 3 done"))
		Expect(html).To(ContainSubstring(`<td class="n">2</td><td class="n"></td><td class="sign">−</td><td>b</td></tr>`))
		Expect(html).To(ContainSubstring(`<tr class="removed" style="--line: 1">`), "each diff line carries its place, which staggers its reveal")
		Expect(html).To(ContainSubstring(`data-chat-since="2026-10-02T01:10:02Z"`), "the bar counts a running turn's time")
		Expect(html).To(ContainSubstring("data-chat-scroll"), "the transcript scrolls inside the page")
		Expect(strings.Count(html, "<html")).To(Equal(1))
	})
})
