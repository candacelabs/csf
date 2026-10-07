// Copyright 2026 Candace Labs

package progress_test

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/progress"
	"github.com/candacelabs/csf/services/harness/session"
)

// assistantRecord is one assistant message as the event log holds it: the
// blocks it carried, the model that wrote it and why it stopped.
func assistantRecord(at time.Time, model, stop string, blocks ...session.ContentBlock) session.Record {
	content, err := json.Marshal(blocks)
	Expect(err).NotTo(HaveOccurred())
	event, err := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"model":       model,
			"stop_reason": stop,
			"content":     json.RawMessage(content),
		},
	})
	Expect(err).NotTo(HaveOccurred())
	return session.Record{Time: at, EventType: session.EventTypeAssistant, Event: event}
}

// kinds is the kind of every line, in order, so a spec can pin the shape of a
// fold without restating each line.
func kinds(lines []progress.Line) []progress.Kind {
	seen := make([]progress.Kind, 0, len(lines))
	for _, line := range lines {
		seen = append(seen, line.Kind)
	}
	return seen
}

var _ = Describe("folding a session's event log", func() {
	at := time.Date(2026, time.October, 6, 0, 27, 31, 0, time.UTC)

	row := func() progress.Progress {
		return progress.Progress{Assignment: "a1", Agent: "observe-sessions"}
	}
	fold := func(record session.Record) (progress.Progress, []progress.Line) {
		return progress.Fold(row(), &record)
	}

	It("opens a run, marks the turn running, and opens again when it finishes", func() {
		folded, lines := fold(session.Record{Time: at, EventType: session.EventTypeRunStarted})
		Expect(folded.Phase).To(Equal(progress.PhaseOpen))
		Expect(kinds(lines)).To(Equal([]progress.Kind{progress.KindTurn}))
		Expect(lines[0].Text).To(Equal("session started"))
		Expect(lines[0].Agent).To(Equal("observe-sessions"))
		Expect(lines[0].Time).To(Equal(at))

		folded, lines = progress.Fold(folded, &session.Record{Time: at.Add(time.Second), EventType: session.EventTypeTurnRequested, Turn: 3})
		Expect(folded.Phase).To(Equal(progress.PhaseRunning))
		Expect(folded.Turn).To(Equal(3))
		Expect(lines[0].Text).To(Equal("turn 3"))

		folded, lines = progress.Fold(folded, &session.Record{Time: at.Add(2 * time.Second), EventType: session.EventTypeRunFinished, Turn: 3})
		Expect(folded.Phase).To(Equal(progress.PhaseOpen))
		Expect(lines[0].Text).To(Equal("turn 3 done"))
	})

	It("counts a turn's tool calls, takes its model and keeps the last thing said", func() {
		record := assistantRecord(at, "claude-opus-5", session.StopReasonEndTurn,
			session.ContentBlock{Type: session.BlockToolUse, Name: "Bash", Input: json.RawMessage(`{"command":"git status --porcelain"}`)},
			session.ContentBlock{Type: session.BlockToolUse, Name: "Read", Input: json.RawMessage(`{"file_path":"/work/a.go"}`)},
			session.ContentBlock{Type: session.BlockText, Text: "the tree is clean\nand the branch is pushed"},
		)
		folded, lines := fold(record)
		Expect(folded.ToolCalls).To(Equal(2))
		Expect(folded.Model).To(Equal("claude-opus-5"))
		Expect(folded.LastNote).To(Equal("the tree is clean"))
		Expect(kinds(lines)).To(Equal([]progress.Kind{progress.KindTool, progress.KindTool, progress.KindTurn}))
		Expect(lines[0].Text).To(Equal("Bash git status --porcelain"))
		Expect(lines[1].Text).To(Equal("Read /work/a.go"))
		Expect(lines[2].Text).To(Equal("the tree is clean"))
	})

	It("keeps what was said without a turn line when the message does not end the turn", func() {
		folded, lines := fold(assistantRecord(at, "claude-opus-5", "",
			session.ContentBlock{Type: session.BlockText, Text: "still working"},
		))
		Expect(folded.LastNote).To(Equal("still working"))
		Expect(lines).To(BeEmpty())
	})

	It("names a gate's decision and why", func() {
		_, lines := fold(session.Record{Time: at, EventType: session.EventTypeGateDecision, Gate: "github", Decision: "deny", Reason: "gh command"})
		Expect(kinds(lines)).To(Equal([]progress.Kind{progress.KindGate}))
		Expect(lines[0].Text).To(Equal("github deny: gh command"))
	})

	It("names an inbound message", func() {
		event, err := json.Marshal(map[string]any{
			"type":    "user",
			"message": map[string]any{"content": []session.ContentBlock{{Type: session.BlockText, Text: "tail the sessions"}}},
		})
		Expect(err).NotTo(HaveOccurred())
		_, lines := fold(session.Record{Time: at, EventType: session.EventTypeUser, Direction: session.DirectionIn, Event: event})
		Expect(kinds(lines)).To(Equal([]progress.Kind{progress.KindTurn}))
		Expect(lines[0].Text).To(Equal("message: tail the sessions"))
	})

	It("reports a failure as an error, and not the turn it finished", func() {
		_, lines := fold(session.Record{Time: at, EventType: session.EventTypeRunFinished, Turn: 2, Error: "context deadline exceeded"})
		Expect(kinds(lines)).To(Equal([]progress.Kind{progress.KindError}))
		Expect(lines[0].Text).To(Equal("context deadline exceeded"))
	})

	It("folds a record to the same row and the same lines every time", func() {
		record := assistantRecord(at, "claude-opus-5", session.StopReasonEndTurn,
			session.ContentBlock{Type: session.BlockToolUse, Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`)},
			session.ContentBlock{Type: session.BlockText, Text: "done"},
		)
		first, firstLines := fold(record)
		second, secondLines := fold(record)
		Expect(second).To(Equal(first))
		Expect(secondLines).To(Equal(firstLines))
	})

	It("keeps the newest activity and the highest turn whichever order records arrive in", func() {
		folded, _ := fold(session.Record{Time: at, EventType: session.EventTypeTurnRequested, Turn: 4})
		folded, _ = progress.Fold(folded, &session.Record{Time: at.Add(-time.Hour), EventType: session.EventTypeRunStarted, Turn: 1})
		Expect(folded.Turn).To(Equal(4))
		Expect(folded.LastActivity).To(Equal(at))
		Expect(folded.Phase).To(Equal(progress.PhaseOpen))
	})

	DescribeTable("folds a control action to the line it names",
		func(action string, kind progress.Kind, text string) {
			_, lines := fold(session.Record{Time: at, EventType: session.EventTypeControlAction, Action: action})
			Expect(kinds(lines)).To(Equal([]progress.Kind{kind}))
			Expect(lines[0].Text).To(Equal(text))
		},
		Entry("a launch", session.ActionSubmit, progress.KindTurn, "launched"),
		Entry("a send", session.ActionSend, progress.KindTurn, "message sent"),
		Entry("a cancel", session.ActionCancel, progress.KindTurn, "cancel requested"),
		Entry("a ready", session.ActionReady, progress.KindMerge, "pull request marked ready"),
		Entry("a merge starting", session.ActionMergeStarted, progress.KindMerge, "merge started"),
		Entry("a merge landing", session.ActionMerge, progress.KindMerge, "pull request merged"),
		Entry("an action with no line of its own", "wiggle", progress.KindTurn, "control action: wiggle"),
	)
})

var _ = Describe("the line kinds", func() {
	It("names every kind, in order", func() {
		Expect(progress.Kinds()).To(Equal([]progress.Kind{
			progress.KindTurn, progress.KindTool, progress.KindGate, progress.KindMerge, progress.KindError,
		}))
	})

	It("parses one kind by its spelling", func() {
		kind, err := progress.ParseKind("tool")
		Expect(err).NotTo(HaveOccurred())
		Expect(kind).To(Equal(progress.KindTool))
		_, err = progress.ParseKind("bogus")
		Expect(err).To(MatchError(ContainSubstring("unknown kind")))
		Expect(err.Error()).To(ContainSubstring("turn, tool, gate, merge, error"))
	})

	It("parses a list into the kinds to keep, and an empty list into all of them", func() {
		keep, err := progress.ParseKinds("")
		Expect(err).NotTo(HaveOccurred())
		Expect(keep).To(HaveLen(len(progress.Kinds())))

		keep, err = progress.ParseKinds("turn, merge")
		Expect(err).NotTo(HaveOccurred())
		Expect(keep).To(Equal(map[progress.Kind]bool{progress.KindTurn: true, progress.KindMerge: true}))

		_, err = progress.ParseKinds("turn, bogus")
		Expect(err).To(MatchError(ContainSubstring("unknown kind")))
	})
})

var _ = Describe("a tail line", func() {
	It("renders as its time, agent, kind and text, and never as JSON", func() {
		at := time.Date(2026, time.October, 6, 0, 27, 31, 0, time.UTC)
		line := progress.Line{Time: at, Agent: "observe-sessions", Kind: progress.KindTool, Text: "Bash ls"}
		Expect(line.String()).To(Equal("00:27:31 observe-sessions tool Bash ls"))
		Expect(line.String()).NotTo(ContainSubstring("{"))
	})
})
