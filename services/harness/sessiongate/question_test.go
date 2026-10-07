// Copyright 2026 Candace Labs

package sessiongate_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/pkg/telemetry"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/sessiongate"
)

// The fixtures of #145. magnitudeQuestion is the shape of the #329 instance;
// meaningQuestion is a definition only the operator can give; askedDatabase
// is the 2026-10-05 AskUserQuestion whose second option contradicted a
// recorded ruling; densityAsked and boilerplateReply are the same night's
// measurement request and the substituted metric reported for it.
const (
	magnitudeQuestion = "Which minimum compilability level should each kind require before it counts as structure?"
	meaningQuestion   = "[meaning] D1: should `literature` mean the relation a term has to prior art (same_as, specializes, diverges, novel), or the list of works it cites?"
	askedDatabase     = `{"questions":[{"question":"Where should CSF's durable database live?","header":"Database","multiSelect":false,"options":[` +
		`{"label":"CSF runs its own (Recommended)","description":"csf serve starts and owns its own Postgres container on first run."},` +
		`{"label":"Use the foundation Postgres","description":"Add a csf database and role to the platform's shared data plane."}]}]}`
	densityAsked     = "also not sure why we're using english prose english prose has bad information density can we like mine for this we need high information density when communicating between agents"
	boilerplateReply = "Measured tonight's briefs: 41% of their bytes are boilerplate shared across briefs. Starting a slice to remove it."
)

var independence = session.Ruling{
	ID: "csf-independent", Statement: "CSF is independent of the parked platform; CSF is its one home.",
	Excludes: []string{"foundation Postgres", "platform data plane"},
}

func judged(questions []sessiongate.Question, rulings ...session.Ruling) ([]session.QuestionVerdict, []sessiongate.ReplyFinding) {
	return sessiongate.JudgeQuestions(questions, rulings)
}

func measurementBlock(quantity string, measured string, same bool, difference string) string {
	encoded, err := json.Marshal(map[string]any{"quantity": quantity, "measured": measured, "same": same, "difference": difference})
	Expect(err).NotTo(HaveOccurred())
	return "\n\n```" + sessiongate.MeasurementFence + "\n" + string(encoded) + "\n```"
}

var _ = Describe("questions to the operator", func() {
	It("reads only the questions a reply ends on, each offering the list items before it", func() {
		Expect(sessiongate.ReplyQuestions("Why did it fail? The cache was stale. Fixed it.")).To(BeEmpty())
		Expect(sessiongate.ReplyQuestions("Qwen3.8 fits.\n\nShould I bring up Qwen3.8-27B at 3-bit?")).To(Equal([]sessiongate.Question{
			{Text: "Should I bring up Qwen3.8-27B at 3-bit?", Options: []string{}},
		}))
		Expect(sessiongate.ReplyQuestions("Two ways:\n\n1. CSF runs its own Postgres.\n2. Use the foundation Postgres.\n\nWhich one? Or both?")).To(Equal([]sessiongate.Question{
			{Text: "Which one?", Options: []string{"CSF runs its own Postgres.", "Use the foundation Postgres."}},
			{Text: "Or both?", Options: []string{"CSF runs its own Postgres.", "Use the foundation Postgres."}},
		}))
		Expect(sessiongate.ReplyQuestions("Done.\n\n```sh\nready?\n```")).To(BeEmpty(), "code is not prose")
	})

	It("reads an AskUserQuestion call's questions with each option's label and description, and nothing from another shape", func() {
		Expect(sessiongate.AskedQuestions(json.RawMessage(askedDatabase))).To(Equal([]sessiongate.Question{{
			Text: "Where should CSF's durable database live?", Header: "Database",
			Options: []string{
				"CSF runs its own (Recommended) csf serve starts and owns its own Postgres container on first run.",
				"Use the foundation Postgres Add a csf database and role to the platform's shared data plane.",
			},
		}}))
		Expect(sessiongate.AskedQuestions(json.RawMessage(`"not an object"`))).To(BeEmpty())
	})

	It("refuses the #329 magnitude question with the derivation the agent owes", func() {
		verdicts, findings := judged(sessiongate.ReplyQuestions("Drafted the levels.\n\n" + magnitudeQuestion))
		Expect(verdicts).To(Equal([]session.QuestionVerdict{{Class: "magnitude", Blocked: true}}))
		Expect(findings).To(Equal([]sessiongate.ReplyFinding{{Rule: sessiongate.RuleQuestionClass, Message: `question_class: "` + magnitudeQuestion +
			`" falls in the magnitude sub-class, which is not the operator's to answer: replace the question with a derivation plan (the metric, its outcome curve, the knee, the recompute trigger), derive the value from data and proceed report-only`}}))
	})

	It("passes a meaning question that names its class", func() {
		verdicts, findings := judged(sessiongate.ReplyQuestions("The grammar needs one decision.\n\n" + meaningQuestion))
		Expect(verdicts).To(Equal([]session.QuestionVerdict{{Class: "meaning"}}))
		Expect(findings).To(BeEmpty())
		verdicts, _ = judged([]sessiongate.Question{{Text: "May a session reach the tailnet?", Header: "[trust-boundary]"}})
		Expect(verdicts).To(Equal([]session.QuestionVerdict{{Class: "trust boundary"}}), "the tag may sit in the header")
	})

	DescribeTable("places every other question in the sub-class whose resolution it owes",
		func(question string, class string, resolution string) {
			verdicts, findings := judged([]sessiongate.Question{{Text: question}})
			Expect(verdicts).To(Equal([]session.QuestionVerdict{{Class: class, Blocked: true}}))
			Expect(findings).To(ConsistOf(HaveField("Message", ContainSubstring(resolution))))
		},
		Entry("permission after a blanket go-ahead", "Should I merge it?", "authorized", "proceed: your assignment already authorizes it"),
		Entry("a fact one can look up", "Does the harness already export a metrics endpoint?", "fact", "look it up"),
		Entry("a house convention", "What naming should the new package use?", "default", "apply the house rule"),
		Entry("a number, even tagged", "[meaning] What daily budget fits the loop?", "magnitude", "derivation plan"),
		Entry("no class named", "Where should the docs site be exposed?", "unnamed", "tag such a question [meaning], [trust boundary] or [class membership]"),
	)

	It("refuses the 2026-10-05 option that contradicted a recorded ruling, naming the ruling", func() {
		verdicts, findings := judged(sessiongate.AskedQuestions(json.RawMessage(askedDatabase)), independence)
		Expect(verdicts).To(Equal([]session.QuestionVerdict{{Class: "re-litigation", Blocked: true}}))
		Expect(findings).To(Equal([]sessiongate.ReplyFinding{{Rule: sessiongate.RuleRuling, Message: `ruling: in "Where should CSF's durable database live?", ` +
			`"Use the foundation Postgres Add a csf database and role to the platform's shared data plane." names "foundation Postgres", which ruling csf-independent rules out ` +
			`(CSF is independent of the parked platform; CSF is its one home.). Drop every alternative a ruling rules out; when one alternative is left, drop the question and proceed with it`}}))
	})

	It("matches an excluded alternative word by word after stemming and folding case, in a reply's list or its question", func() {
		_, findings := judged(sessiongate.ReplyQuestions("Options:\n\n- Postgres of our own\n- the Foundation Postgres, as it stands\n- a Platform Data Planes slot\n\n[trust boundary] Which may hold CSF's state?"), independence)
		Expect(findings).To(ConsistOf(HaveField("Rule", sessiongate.RuleRuling)))
		_, findings = judged([]sessiongate.Question{{Text: "[trust boundary] Should CSF keep its state in its own Postgres or a platform-wide one?"}}, independence)
		Expect(findings).To(BeEmpty(), "no excluded phrase, word for word")
	})
})

var _ = Describe("the measured quantity", func() {
	It("refuses the substituted metric reported for the operator's density question", func() {
		Expect(sessiongate.JudgeMeasurement(densityAsked, boilerplateReply)).To(ConsistOf(And(
			HaveField("Rule", sessiongate.RuleMeasuredQuantity),
			HaveField("Message", HavePrefix("measured_quantity: the reply reports a number but carries no measurement block.")))))
	})

	It("refuses a block whose quantity is not the operator's and whose introduced term has no research check", func() {
		findings := sessiongate.JudgeMeasurement(densityAsked, boilerplateReply+measurementBlock("boilerplate", "boilerplate", true, ""))
		Expect(findings).To(ConsistOf(HaveField("Message", HavePrefix(`measured_quantity: quantity "boilerplate" is not in the operator's words; `+
			`measured introduces boilerplate, which the operator did not use: add a research check for each.`))))
	})

	It("refuses a different quantity that does not say how it differs", func() {
		findings := sessiongate.JudgeMeasurement(densityAsked, boilerplateReply+measurementBlock("information density", "information density", false, ""))
		Expect(findings).To(ConsistOf(HaveField("Message", ContainSubstring("the measured quantity is not the one asked for and difference does not say how"))))
	})

	It("passes the reply that names the operator's quantity, says what it measured instead and checks the term it introduced", func() {
		reply := boilerplateReply + measurementBlock("information density", "byte overlap of shared sections across briefs", false,
			"overlap counts repeated bytes; density counts checkable statements per token") + "\n\n" + check("overlap")
		Expect(sessiongate.JudgeMeasurement(densityAsked, reply)).To(BeEmpty())
	})

	It("holds nothing when the operator asked for no measurement, wrote nothing, or no number was reported", func() {
		Expect(sessiongate.JudgeMeasurement("rebase it on main", boilerplateReply)).To(BeEmpty())
		Expect(sessiongate.JudgeMeasurement("", boilerplateReply)).To(BeEmpty())
		Expect(sessiongate.JudgeMeasurement(densityAsked, "Measuring first.")).To(BeEmpty())
	})

	It("names a measurement block that is not JSON", func() {
		findings := sessiongate.JudgeMeasurement(densityAsked, boilerplateReply+"\n\n```measurement\nnot json\n```")
		Expect(findings).To(ConsistOf(HaveField("Message", ContainSubstring("measurement block 1 is not a JSON object"))))
	})
})

var _ = Describe("the question gate in a session", func() {
	var (
		ctx       context.Context
		directory string
		gate      *sessiongate.SessionGate
	)

	appendLog := func(lines ...string) {
		file, err := os.OpenFile(filepath.Join(directory, session.EventsFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		Expect(err).NotTo(HaveOccurred())
		_, err = file.WriteString(strings.Join(lines, "\n") + "\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(file.Close()).To(Succeed())
	}
	streamed := func(eventType string, direction string, text string) string {
		encoded, err := json.Marshal(map[string]any{"event_type": eventType, "turn": 1, "direction": direction,
			"event": map[string]any{"type": eventType, "message": map[string]any{"content": []map[string]any{{"type": session.BlockText, "text": text}}}}})
		Expect(err).NotTo(HaveOccurred())
		return string(encoded)
	}
	rulingsLine := func() string {
		encoded, err := json.Marshal(map[string]any{"event_type": session.EventTypeRulings, "turn": 1, session.KeyRulings: []session.Ruling{independence}})
		Expect(err).NotTo(HaveOccurred())
		return string(encoded)
	}
	hook := func(event string, tool string, toolInput string) []byte {
		encoded, err := json.Marshal(map[string]any{"session_id": sessionID, "hook_event_name": event, "tool_name": tool,
			"tool_use_id": toolUseID, "tool_input": json.RawMessage(toolInput)})
		Expect(err).NotTo(HaveOccurred())
		return encoded
	}
	decisionsOf := func(name string) []map[string]any {
		kept := []map[string]any{}
		for _, record := range gateRecords(directory) {
			if record["gate"] == name {
				kept = append(kept, record)
			}
		}
		return kept
	}

	BeforeEach(func() {
		ctx = context.Background()
		directory = GinkgoT().TempDir()
		trace, err := telemetry.NewTraceContext(telemetry.TraceFlagsSampled)
		Expect(err).NotTo(HaveOccurred())
		state, err := json.Marshal(&session.RunState{AssignmentID: "0caf2d51-1ee7-468a-882f-ac478a437640", SessionID: sessionID,
			TraceID: trace.GetTraceId(), SpanID: trace.GetSpanId(), Worktree: worktree, Branch: branch, Turns: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(directory, session.RunStateFile), state, 0o600)).To(Succeed())
		gate, err = sessiongate.NewSessionGate(sessiongate.WithLauncher(NewMockILauncher(gomock.NewController(GinkgoT()))), sessiongate.WithRunDirectory(directory))
		Expect(err).NotTo(HaveOccurred())
		appendLog(`{"event_type":"harness_turn_requested","turn":1}`, rulingsLine())
	})

	It("denies the AskUserQuestion call whose option contradicts a ruling before the operator sees it, and records the verdicts", func() {
		decision, err := gate.Handle(ctx, session.HookPreToolUse, hook(session.HookPreToolUse, session.ToolAskUserQuestion, askedDatabase))
		Expect(err).NotTo(HaveOccurred())
		Expect(decision.HookSpecificOutput.PermissionDecision).To(Equal("deny"))
		Expect(decision.HookSpecificOutput.PermissionDecisionReason).To(HavePrefix(`Rejected by the CSF question gate. ruling: in "Where should CSF's durable database live?"`))
		Expect(decisionsOf(sessiongate.GateQuestion)).To(ConsistOf(And(
			HaveKeyWithValue("decision", sessiongate.DecisionDeny),
			HaveKeyWithValue("rules", ConsistOf("ruling")),
			HaveKeyWithValue(sessiongate.KeyQuestions, ConsistOf(map[string]any{"class": "re-litigation", "blocked": true})))))
		Expect(decisionsOf(sessiongate.GateWait)).To(BeEmpty(), "AskUserQuestion is not a shell command")
	})

	It("lets a tagged meaning question through to the operator", func() {
		decision, err := gate.Handle(ctx, session.HookPreToolUse, hook(session.HookPreToolUse, session.ToolAskUserQuestion,
			`{"questions":[{"question":"`+strings.ReplaceAll(meaningQuestion, "`", "")+`","header":"D1","options":[{"label":"relation"},{"label":"citations"}]}]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(decision).To(BeNil())
		Expect(decisionsOf(sessiongate.GateQuestion)).To(ConsistOf(HaveKeyWithValue("decision", sessiongate.DecisionAllow)))
	})

	It("makes the turn continue when the reply ends on the #329 question, and passes the meaning question", func() {
		appendLog(streamed(session.EventTypeAssistant, session.DirectionOut, "Drafted the levels.\n\n"+magnitudeQuestion))
		decision, err := gate.Handle(ctx, session.HookStop, hook(session.HookStop, "", `{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(decision.Decision).To(Equal("block"))
		Expect(decision.Reason).To(HavePrefix(`Rejected by the CSF reply gate. question_class: "` + magnitudeQuestion + `" falls in the magnitude sub-class`))

		appendLog(streamed(session.EventTypeAssistant, session.DirectionOut, "The grammar needs one decision.\n\n"+meaningQuestion))
		decision, err = gate.Handle(ctx, session.HookStop, hook(session.HookStop, "", `{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(decision).To(BeNil())
		Expect(decisionsOf(sessiongate.GateReply)).To(HaveExactElements(
			HaveKeyWithValue(sessiongate.KeyQuestions, ConsistOf(map[string]any{"class": "magnitude", "blocked": true})),
			HaveKeyWithValue(sessiongate.KeyQuestions, ConsistOf(map[string]any{"class": "meaning", "blocked": false}))))
	})

	It("holds an operator's turn to the quantity it asked for, and not a turn the operator did not write", func() {
		appendLog(`{"event_type":"unvetted_terms","turn":1,"terms":[]}`, streamed(session.EventTypeUser, session.DirectionIn, densityAsked),
			streamed(session.EventTypeAssistant, session.DirectionOut, boilerplateReply))
		decision, err := gate.Handle(ctx, session.HookStop, hook(session.HookStop, "", `{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(decision.Reason).To(HavePrefix("Rejected by the CSF reply gate. measured_quantity: the reply reports a number but carries no measurement block."))

		appendLog(`{"event_type":"harness_turn_requested","turn":2}`, streamed(session.EventTypeUser, session.DirectionIn, densityAsked),
			streamed(session.EventTypeAssistant, session.DirectionOut, boilerplateReply))
		decision, err = gate.Handle(ctx, session.HookStop, hook(session.HookStop, "", `{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(decision).To(BeNil(), "a relayed brief is not the operator's request")
	})
})
