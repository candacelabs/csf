// Copyright 2026 Candace Labs

package session_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/pkg/affect"
	"github.com/candacelabs/csf/pkg/terms"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/session/mocks"
)

const (
	siblingAssignment = "2b3c4d5e-6f70-4182-93a4-b5c6d7e8f901"
	turnRequested     = `{"event_type":"harness_turn_requested","turn":1}`
	unvettedCgroup    = `{"event_type":"unvetted_terms","turn":1,"terms":["cgroup"]}`
	// decoy carries the marker text without being the record kind.
	decoy = `{"event_type":"user","turn":1,"direction":"in","event":{"type":"user","message":{"content":"unvetted_terms minikernel"}}}`
)

// writeLog writes lines as the event log of a run directory.
func writeLog(directory string, lines ...string) {
	Expect(os.MkdirAll(directory, 0o700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(directory, session.EventsFile), []byte(strings.Join(lines, "\n")+"\n"), 0o600)).To(Succeed())
}

// unvettedRecords are the unvetted terms records of a run's log, in order.
func unvettedRecords(directory string) []map[string]any {
	kept := []map[string]any{}
	for _, record := range records(directory) {
		if record[session.KeyEventType] == session.EventTypeUnvettedTerms {
			kept = append(kept, record)
		}
	}
	return kept
}

var _ = Describe("an operator-authored turn", func() {
	var (
		ctx      context.Context
		executor *mocks.MockIOpenTurnExecutor
		runner   *session.AgentSessionRunner
		state    string
		run      string
		prompts  []string
	)

	BeforeEach(func() {
		ctx = context.Background()
		controller := gomock.NewController(GinkgoT())
		launcher := NewMockILauncher(controller)
		executor = mocks.NewMockIOpenTurnExecutor(controller)
		// The worktree, its hooks path and initial commit, the push remote and
		// the pull request lookup after each turn.
		launcher.EXPECT().Run(gomock.Any(), launched("git")).Return(proc.Result{}, nil).AnyTimes()
		launcher.EXPECT().Run(gomock.Any(), launched("gh")).Return(proc.Result{}, nil).AnyTimes()
		executor.EXPECT().Close(gomock.Any()).Return(nil)
		prompts = nil
		executor.EXPECT().Propose(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, turn *claudecode.Turn) (*model.Proposal[claudecode.Event], error) {
				prompts = append(prompts, promptOf(turn))
				return &model.Proposal[claudecode.Event]{Provider: claudecode.ProviderName}, nil
			}).AnyTimes()
		state = GinkgoT().TempDir()
		run = session.RunDirectory(state, assignmentID)
		var err error
		runner, err = session.NewAgentSessionRunner(
			session.WithLauncher(launcher),
			session.WithStateDirectory(state),
			session.WithGateCommand(gateBinary, gateVerb),
			session.WithOpenTurnExecutors(func(ctx context.Context, opened session.TurnExecutorSpec) (session.IOpenTurnExecutor, error) {
				return executor, nil
			}),
		)
		Expect(err).NotTo(HaveOccurred())
	})

	It("records the message's unvetted terms against the operator's vocabulary and puts them into the turn", func() {
		writeLog(filepath.Join(state, siblingAssignment), turnRequested, unvettedCgroup, decoy)
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(opened.Close(ctx)).To(Succeed()) }()

		_, err = opened.Turn(ctx, "no dude i think cgroup sandboxing", session.OperatorAuthored())
		Expect(err).NotTo(HaveOccurred())
		Expect(prompts).To(HaveLen(1))
		Expect(prompts[0]).To(HavePrefix("no dude i think cgroup sandboxing\n\n---\nCSF unvetted terms: sandboxing."))
		Expect(prompts[0]).To(ContainSubstring("```" + session.ResearchCheckFence + "\n"))
		Expect(prompts[0]).To(ContainSubstring(session.ResearchCheckSkeleton("sandboxing")))
		Expect(prompts[0]).To(ContainSubstring(`"fits_goal":"yes|no|partly"`))

		logged := records(run)
		types := eventTypes(logged)
		index := slices.Index(types, session.EventTypeUnvettedTerms)
		Expect(index).To(BeNumerically(">", 0))
		Expect(types[index-1]).To(Equal(session.EventTypeTurnRequested), "the record follows its turn's request")
		Expect(logged[index]).To(HaveKeyWithValue(session.KeyTerms, ConsistOf("sandboxing")))
		Expect(logged[index]).To(HaveKeyWithValue("message_terms", BeNumerically("==", 2)))
		Expect(logged[index]).To(HaveKeyWithValue("vocabulary", BeNumerically("==", 1)))
		Expect(logged[index]).To(HaveKeyWithValue(session.KeyTurn, BeNumerically("==", 1)))

		_, err = opened.Turn(ctx, "sandboxing it is, then", session.OperatorAuthored())
		Expect(err).NotTo(HaveOccurred())
		Expect(prompts[1]).To(Equal("sandboxing it is, then"), "the turn's own record vetted the term; nothing is appended")
		unvetted := unvettedRecords(run)
		Expect(unvetted).To(HaveLen(2))
		Expect(unvetted[1]).To(HaveKeyWithValue(session.KeyTerms, BeEmpty()))
		Expect(unvetted[1]).To(HaveKeyWithValue("vocabulary", BeNumerically("==", 2)))
	})

	It("records the message's operator affect as the label of the turn it answers", func() {
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(opened.Close(ctx)).To(Succeed()) }()

		_, err = opened.Turn(ctx, "make a pr when done", session.OperatorAuthored())
		Expect(err).NotTo(HaveOccurred())
		_, err = opened.Turn(ctx, "it says that session is dead moron", session.OperatorAuthored())
		Expect(err).NotTo(HaveOccurred())
		Expect(prompts[0]).NotTo(ContainSubstring("CSF operator affect"), "a calm message carries no notice")
		Expect(prompts[1]).To(And(HavePrefix("it says that session is dead moron\n\n---\nCSF unvetted terms: moron."), HaveSuffix(
			"\n\n---\nCSF operator affect: the operator's strain reads high. Answer in at most 800 words; the reply gate refuses a longer reply, and the turn continues.")))

		readings := []map[string]any{}
		for _, record := range records(run) {
			if record[session.KeyEventType] == session.EventTypeOperatorAffect {
				readings = append(readings, record)
			}
		}
		Expect(readings).To(HaveLen(2))
		Expect(readings[0]).To(HaveKeyWithValue(session.KeyTargetTurn, BeNumerically("==", 0)), "the first message answers no turn")
		Expect(readings[0]).To(HaveKeyWithValue(session.KeyAffect, HaveKeyWithValue("strain", "low")))
		Expect(readings[1]).To(HaveKeyWithValue(session.KeyTurn, BeNumerically("==", 2)))
		Expect(readings[1]).To(HaveKeyWithValue(session.KeyTargetTurn, BeNumerically("==", 1)))
		Expect(readings[1]).To(HaveKeyWithValue(session.KeyAffect, And(
			HaveKeyWithValue("strain", "high"), HaveKeyWithValue("correction", true), HaveKeyWithValue("kind", "directive"))))

		turn, err := session.ReadTurnRecords(run)
		Expect(err).NotTo(HaveOccurred())
		affected := slices.IndexFunc(turn, func(record session.Record) bool { return record.Affect != nil })
		Expect(affected).To(BeNumerically(">=", 0), "the reply gate reads the reading back")
		Expect(turn[affected].Affect.Strain).To(Equal(affect.StrainHigh))
	})

	It("leaves a message that is not the operator's alone", func() {
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(opened.Close(ctx)).To(Succeed()) }()
		_, err = opened.Turn(ctx, "Address the review of cgroup sandboxing.")
		Expect(err).NotTo(HaveOccurred())
		Expect(prompts).To(Equal([]string{"Address the review of cgroup sandboxing."}))
		Expect(eventTypes(records(run))).NotTo(ContainElement(BeElementOf(session.EventTypeUnvettedTerms, session.EventTypeOperatorAffect)))
	})
})

var _ = Describe("OperatorVocabulary", func() {
	It("is the union of every run's recorded unvetted terms, skipping what it cannot read", func() {
		state := GinkgoT().TempDir()
		writeLog(filepath.Join(state, siblingAssignment), turnRequested, unvettedCgroup, decoy)
		writeLog(filepath.Join(state, assignmentID), `{"event_type":"unvetted_terms","turn":3,"terms":["Landlock","seccomp"]}`, "not a record")
		Expect(os.MkdirAll(filepath.Join(state, "empty-run"), 0o700)).To(Succeed())
		vocabulary, err := session.OperatorVocabulary(state)
		Expect(err).NotTo(HaveOccurred())
		Expect(vocabulary.Len()).To(Equal(3))
		Expect(vocabulary.Has("cgroups")).To(BeTrue())
		Expect(vocabulary.Has("landlock")).To(BeTrue())
		Expect(vocabulary.Has("minikernel")).To(BeFalse(), "a decoy is not a record of the kind")
	})

	It("is empty for a state directory with no runs", func() {
		vocabulary, err := session.OperatorVocabulary(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		Expect(vocabulary.Len()).To(BeZero())
	})
})

var _ = Describe("OperatorCorrections", func() {
	It("is every recorded operator correction across runs, oldest first, skipping calm messages, decoys and unreadable lines", func() {
		state := GinkgoT().TempDir()
		writeLog(filepath.Join(state, siblingAssignment),
			`{"time":"2026-10-05T01:05:00Z","event_type":"operator_affect","turn":3,"target_turn":2,"affect":{"strain":"high","correction":true}}`,
			`{"time":"2026-10-05T00:45:00Z","event_type":"operator_affect","turn":2,"target_turn":1,"affect":{"strain":"low","correction":false}}`,
			`{"time":"2026-10-05T00:50:00Z","event_type":"user","event":{"message":{"content":"operator_affect \"correction\":true"}}}`)
		writeLog(filepath.Join(state, assignmentID),
			`{"time":"2026-10-04T23:55:00Z","event_type":"operator_affect","turn":1,"target_turn":0,"affect":{"strain":"high","correction":true}}`, "not a record")
		corrections, err := session.OperatorCorrections(state)
		Expect(err).NotTo(HaveOccurred())
		Expect(corrections).To(Equal([]session.OperatorCorrection{
			{Assignment: assignmentID, TargetTurn: 0, At: time.Date(2026, 10, 4, 23, 55, 0, 0, time.UTC)},
			{Assignment: siblingAssignment, TargetTurn: 2, At: time.Date(2026, 10, 5, 1, 5, 0, 0, time.UTC)},
		}))
	})

	It("is empty for a state directory with no runs", func() {
		corrections, err := session.OperatorCorrections(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		Expect(corrections).To(BeEmpty())
	})
})

var _ = Describe("the event log read back", func() {
	const assistantLine = `{"event_type":"assistant","turn":2,"direction":"out","event":{"type":"assistant","message":{"content":[{"type":"text","text":"Agreed."},{"type":"tool_use","name":"Bash","input":{"command":"git commit -m x"}},{"type":"text","text":"Done."}]}}}`

	It("returns the running turn's records from the last turn request on", func() {
		directory := GinkgoT().TempDir()
		writeLog(directory, turnRequested, unvettedCgroup, `{"event_type":"harness_turn_requested","turn":2}`,
			`{"event_type":"unvetted_terms","turn":2,"terms":["sandboxing"]}`, assistantLine)
		turn, err := session.ReadTurnRecords(directory)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn).To(HaveLen(3))
		Expect(turn[0].EventType).To(Equal(session.EventTypeTurnRequested))
		Expect(turn[0].Turn).To(Equal(2))
		Expect(turn[1].Terms).To(Equal([]terms.Term{"sandboxing"}))
		text, tools := turn[2].Assistant()
		Expect(text).To(Equal([]string{"Agreed.", "Done."}))
		Expect(tools).To(HaveLen(1))
		Expect(tools[0].Name).To(Equal("Bash"))
		Expect(string(tools[0].Input)).To(Equal(`{"command":"git commit -m x"}`))
	})

	It("returns no records before the first turn request and skips lines that are not records", func() {
		directory := GinkgoT().TempDir()
		writeLog(directory, "not json", unvettedCgroup)
		turn, err := session.ReadTurnRecords(directory)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn).To(BeEmpty())
	})

	It("reports a log that cannot be opened", func() {
		_, err := session.ReadTurnRecords(filepath.Join(GinkgoT().TempDir(), "missing"))
		Expect(err).To(HaveOccurred())
	})

	It("reads a string content as one text block and a record of another kind as nothing", func() {
		var record session.Record
		Expect(json.Unmarshal([]byte(decoy), &record)).To(Succeed())
		message, ok := record.Stream()
		Expect(ok).To(BeTrue())
		Expect(message.Blocks()).To(Equal([]session.ContentBlock{{Type: session.BlockText, Text: "unvetted_terms minikernel"}}))
		text, tools := record.Assistant()
		Expect(text).To(BeNil())
		Expect(tools).To(BeNil())
		_, ok = (&session.Record{}).Stream()
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("ResearchCheck", func() {
	It("names its blank fields and its enumerations outside their values", func() {
		Expect(session.ResearchCheck{}.Incomplete()).To(Equal([]string{"term", "what_it_is", "what_it_does_not_do", "fits_goal", "why", "likely_source"}))
		check := session.ResearchCheck{Term: "cgroup", WhatItIs: "a kernel resource controller", WhatItDoesNotDo: "isolate a filesystem",
			FitsGoal: "somewhat", Why: "caps and kill, not a sandbox", LikelySource: "person"}
		Expect(check.Incomplete()).To(Equal([]string{"fits_goal"}))
		check.FitsGoal = session.FitsGoalPartly
		Expect(check.Incomplete()).To(BeEmpty())
	})

	It("spells the enumerations in its skeleton", func() {
		Expect(session.ResearchCheckSkeleton("cgroup")).To(Equal(
			`{"term":"cgroup","what_it_is":"","what_it_does_not_do":"","fits_goal":"yes|no|partly","why":"","likely_source":"agent output|paper|person|unknown"}`))
	})
})
