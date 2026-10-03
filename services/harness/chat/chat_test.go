// Copyright 2026 Candace Labs

package chat

import (
	"bytes"
	"context"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

func TestChat(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "services/harness/chat Suite")
}

const (
	userRecord      = `{"time":"2026-10-02T01:10:03Z","msg":"turn executor event","event_type":"user","turn":1,"direction":"in","event":{"type":"user","message":{"role":"user","content":"Fix the build."}}}`
	assistantRecord = `{"time":"2026-10-02T01:10:04Z","msg":"turn executor event","event_type":"assistant","turn":1,"direction":"out","event":{"type":"assistant","message":{"content":[{"type":"text","text":"On it."},{"type":"tool_use","name":"Bash","input":{"command":"go build ./...\necho done"}}]}}}`
	toolRecord      = `{"time":"2026-10-02T01:10:05Z","msg":"turn executor event","event_type":"user","turn":1,"direction":"out","event":{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}}`
	resultRecord    = `{"time":"2026-10-02T01:10:06Z","msg":"turn executor event","event_type":"result","turn":1,"direction":"out","event":{"type":"result","subtype":"success","duration_ms":2500}}`
	gateRecord      = `{"time":"2026-10-02T01:10:07Z","msg":"session gate decision","event_type":"session_gate_decision","turn":1,"decision":"deny","reason":"Rejected by the CSF session gate. pgrep_full"}`
	allowRecord     = `{"time":"2026-10-02T01:10:07Z","msg":"session gate decision","event_type":"session_gate_decision","turn":1,"decision":"allow"}`
	harnessRecord   = `{"time":"2026-10-02T01:10:08Z","msg":"turn finished","event_type":"harness_run_finished","turn":1}`
	systemRecord    = `{"time":"2026-10-02T01:10:03Z","msg":"turn executor event","event_type":"system","turn":1,"direction":"out","event":{"type":"system","subtype":"init"}}`
)

var _ = Describe("the transcript", func() {
	DescribeTable("reads one record into one entry",
		func(line string, kind string, text string) {
			got := entryOf([]byte(line))
			Expect(got.Kind).To(Equal(kind))
			Expect(got.Text).To(ContainSubstring(text))
			if kind != "" {
				Expect(got.Turn).To(Equal(1))
				Expect(got.Time).To(MatchRegexp(`^\d\d:\d\d:\d\d$`))
			}
		},
		Entry("the operator's message", userRecord, kindUser, "Fix the build."),
		Entry("what the assistant said", assistantRecord, kindAssistant, "On it."),
		Entry("a tool result", toolRecord, kindTool, "tool result: ok"),
		Entry("the end of a turn", resultRecord, kindResult, "turn 1 finished: success after 2s"),
		Entry("a gate denial", gateRecord, kindGate, "pgrep_full"),
		Entry("a gate allowance is not shown", allowRecord, "", ""),
		Entry("a harness record", harnessRecord, kindHarness, "turn finished"),
		Entry("a system event is not shown", systemRecord, "", ""),
		Entry("a line that is not a record", "not json", "", ""),
	)

	It("lists the tools an assistant message called, rendered apart from its text", func() {
		got := entryOf([]byte(assistantRecord))
		Expect(got.Tools).To(Equal([]string{"→ Bash go build ./..."}))
		Expect(string(got.HTML)).To(Equal("<p>On it.</p>\n"))
	})

	It("renders Markdown and omits raw HTML, so a message cannot script the page", func() {
		Expect(string(renderMarkdown("Use **care** with `rm`.\n\n```go\nfunc main() {}\n```\n\n<script>alert(1)</script>"))).To(Equal(
			"<p>Use <strong>care</strong> with <code>rm</code>.</p>\n<pre><code class=\"language-go\">func main() {}\n</code></pre>\n<!-- raw HTML omitted -->\n"))
	})

	It("keeps the transcript bounded", func() {
		var entries []entry
		for range transcriptCap + 5 {
			entries = appendEntry(entries, entry{Kind: kindUser, Text: "x"})
		}
		Expect(entries).To(HaveLen(transcriptCap))
		Expect(appendEntry(entries, entry{})).To(HaveLen(transcriptCap), "a hidden record adds nothing")
		Expect(appendEntry(entries, entry{Kind: kindAssistant})).To(HaveLen(transcriptCap), "an empty assistant record adds nothing")
	})
})

var _ = Describe("the reducer", func() {
	var chat *Chat

	BeforeEach(func() { chat = &Chat{} })

	It("appends records and advances the cursor", func() {
		state, effects := chat.reduce(chatState{Cursor: 4}, live.Event{Name: eventRecord, Fields: live.NewFields(map[string]string{fieldLine: userRecord})})
		Expect(effects).To(BeEmpty())
		Expect(state.Cursor).To(Equal(5))
		Expect(state.Entries).To(HaveLen(1))
		state, _ = chat.reduce(state, live.Event{Name: eventRecord, Fields: live.NewFields(map[string]string{fieldLine: systemRecord})})
		Expect(state.Cursor).To(Equal(6), "every record moves the cursor, shown or not")
		Expect(state.Entries).To(HaveLen(1))
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

	It("reports a sent turn and a failed effect", func() {
		state, _ := chat.reduce(chatState{Pending: true}, live.Event{Name: eventSent, Fields: live.NewFields(map[string]string{fieldTurn: "4"})})
		Expect(state.Pending).To(BeFalse())
		Expect(state.Notice).To(Equal("Queued as turn 4."))
		state, _ = chat.reduce(chatState{Pending: true}, live.Event{Name: live.EffectFailedEvent, Fields: live.NewFields(map[string]string{live.EffectFailedSourceField: sourceSend, live.EffectFailedErrorField: "the session has finished"})})
		Expect(state.Pending).To(BeFalse())
		Expect(state.Notice).To(Equal("chat.send failed: the session has finished"))
	})

	It("asks for a cancel as an effect", func() {
		_, effects := chat.reduce(chatState{Assignment: "a"}, live.Event{Name: eventCancel})
		Expect(effects).To(HaveLen(1))
		Expect(effects[0].Source).To(Equal(sourceCancel))
	})

	It("declares what each fragment depends on", func() {
		before := chatState{Cursor: 1, Session: sessionView{Phase: "open"}}
		Expect(statusChanged(before, before)).To(BeFalse())
		Expect(transcriptChanged(before, before)).To(BeFalse())
		Expect(composerChanged(before, before)).To(BeFalse())
		Expect(statusChanged(before, chatState{Cursor: 1, Session: sessionView{Phase: "running"}})).To(BeTrue())
		Expect(transcriptChanged(before, chatState{Cursor: 2, Session: before.Session})).To(BeTrue())
		Expect(composerChanged(before, chatState{Cursor: 1, Session: before.Session, Pending: true})).To(BeTrue())
	})
})

var _ = Describe("the page", func() {
	It("renders the three regions and the live runtime for the session's mount", func() {
		state := chatState{Assignment: "0caf2d51-1ee7-468a-882f-ac478a437640", Mount: "/chat/0caf2d51-1ee7-468a-882f-ac478a437640/live",
			Session: sessionView{AgentID: "orchestrator", Phase: "open", Turns: 2}, Entries: []entry{entryOf([]byte(userRecord)), {Kind: kindTool, Turn: 1, Time: "01:10:04", Text: "<b>Fix</b> it"}}}
		var page bytes.Buffer
		Expect(renderPage(state).Render(context.Background(), &page)).To(Succeed())
		html := page.String()
		for _, region := range []string{fragmentStatus, fragmentTranscript, fragmentComposer} {
			Expect(html).To(ContainSubstring(`data-gotth-region="` + region + `"`))
		}
		Expect(html).To(ContainSubstring("submit:chat.send"))
		Expect(html).To(ContainSubstring("click:chat.cancel"))
		Expect(html).To(ContainSubstring(state.Mount))
		Expect(html).To(ContainSubstring("&lt;b&gt;Fix&lt;/b&gt; it"), "plain transcript text is escaped")
		Expect(html).To(ContainSubstring("<p>Fix the build.</p>"), "a message is rendered from Markdown")
		Expect(html).To(ContainSubstring("data-chat-scroll"), "the transcript scrolls inside the page")
		Expect(strings.Count(html, "<html")).To(Equal(1))
	})
})
