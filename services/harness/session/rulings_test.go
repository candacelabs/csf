// Copyright 2026 Candace Labs

package session_test

import (
	"context"
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
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/session/mocks"
)

var recorded = time.Date(2026, 10, 5, 1, 15, 0, 0, time.UTC)

var _ = Describe("rulings", func() {
	It("are none for a state directory where none was recorded", func() {
		Expect(session.RulingsInForce(GinkgoT().TempDir())).To(BeEmpty())
	})

	It("keep the latest record of each ruling in first-recorded order and drop the superseded ones", func() {
		state := GinkgoT().TempDir()
		for _, ruling := range []session.Ruling{
			{ID: "home", Statement: "CSF is its own home.", Excludes: []string{"shared platform"}, RecordedAt: recorded},
			{ID: "executor", Statement: "One executor.", Excludes: []string{"second executor"}, RecordedAt: recorded},
			{ID: "home", Statement: "CSF is independent of the platform.", Excludes: []string{"foundation Postgres"}, RecordedAt: recorded},
			{ID: "executor-2", Statement: "The default executor is mandatory.", Excludes: []string{"other executor"}, Supersedes: "executor", RecordedAt: recorded},
		} {
			Expect(session.AppendRuling(state, ruling)).To(Succeed())
		}
		inForce, err := session.RulingsInForce(state)
		Expect(err).NotTo(HaveOccurred())
		Expect(inForce).To(Equal([]session.Ruling{
			{ID: "home", Statement: "CSF is independent of the platform.", Excludes: []string{"foundation Postgres"}, RecordedAt: recorded},
			{ID: "executor-2", Statement: "The default executor is mandatory.", Excludes: []string{"other executor"}, Supersedes: "executor", RecordedAt: recorded},
		}))
		info, err := os.Stat(filepath.Join(state, session.RulingsFile))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("reports a rulings file it cannot decode", func() {
		state := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(state, session.RulingsFile), []byte("not json\n"), 0o600)).To(Succeed())
		_, err := session.RulingsInForce(state)
		Expect(err).To(MatchError(ContainSubstring("decode ruling")))
	})

	Describe("in a turn", func() {
		var (
			ctx    context.Context
			runner *session.AgentSessionRunner
			state  string
		)

		BeforeEach(func() {
			ctx = context.Background()
			controller := gomock.NewController(GinkgoT())
			launcher := NewMockILauncher(controller)
			executor := mocks.NewMockIOpenTurnExecutor(controller)
			launcher.EXPECT().Run(gomock.Any(), launched("git")).Return(proc.Result{}, nil).AnyTimes()
			launcher.EXPECT().Run(gomock.Any(), launched("gh")).Return(proc.Result{}, nil).AnyTimes()
			executor.EXPECT().Close(gomock.Any()).Return(nil)
			executor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(&model.Proposal[claudecode.Event]{Provider: claudecode.ProviderName}, nil).AnyTimes()
			state = GinkgoT().TempDir()
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

		It("are recorded after the turn's request, so the question gate reads the ones in force when the turn was sent", func() {
			opened, err := runner.Open(ctx, newRecipe())
			Expect(err).NotTo(HaveOccurred())
			defer func() { Expect(opened.Close(ctx)).To(Succeed()) }()
			_, err = opened.Turn(ctx, "Rebase on main.")
			Expect(err).NotTo(HaveOccurred())
			Expect(eventTypes(records(opened.Directory()))).NotTo(ContainElement(session.EventTypeRulings), "no ruling, no record")

			Expect(session.AppendRuling(state, session.Ruling{ID: "home", Statement: "CSF is its own home.", Excludes: []string{"foundation Postgres"}, RecordedAt: recorded})).To(Succeed())
			_, err = opened.Turn(ctx, "Where does the database go?")
			Expect(err).NotTo(HaveOccurred())
			logged := records(opened.Directory())
			types := eventTypes(logged)
			index := slices.Index(types, session.EventTypeRulings)
			Expect(index).To(BeNumerically(">", 0))
			Expect(types[index-1]).To(Equal(session.EventTypeTurnRequested))
			Expect(logged[index]).To(HaveKeyWithValue(session.KeyTurn, BeNumerically("==", 2)))
			Expect(logged[index]).To(HaveKeyWithValue(session.KeyRulings, ConsistOf(And(
				HaveKeyWithValue("ruling_id", "home"), HaveKeyWithValue("excludes", ConsistOf("foundation Postgres"))))))
		})
	})
})

var _ = Describe("the ruling records", func() {
	opusOnly := session.Ruling{
		ID:         "opus-only",
		Statement:  "Every real session runs claude-opus-5-5. Never Fable.",
		Excludes:   []string{"fable"},
		Quote:      "DON'T USE FABLE JUST USE OPUS",
		RuledOn:    "2026-10-05",
		Scope:      "every real session",
		Why:        "two recipes resumed on Fable despite a prose ruling",
		EnforcedBy: "allowed-models refusal at submit (COPILOT-PARITY #343)",
	}
	nativeTools := session.Ruling{
		ID:          "no-native-tools",
		Statement:   "No executor-native presentation or orchestration tools.",
		Excludes:    []string{},
		Quote:       "dont ever use your native tools again",
		RuledOn:     "2026-10-05",
		PendingGate: "PreToolUse native-tool refusal (YOU-DO #424)",
	}

	It("reads back every field it records, in the order first recorded, and counts the enforced ones", func() {
		state := GinkgoT().TempDir()
		Expect(session.AppendRuling(state, opusOnly)).To(Succeed())
		Expect(session.AppendRuling(state, nativeTools)).To(Succeed())

		inForce, err := session.RulingsInForce(state)
		Expect(err).NotTo(HaveOccurred())
		Expect(inForce).To(Equal([]session.Ruling{opusOnly, nativeTools}))
		Expect(session.CoverageOf(inForce)).To(Equal(session.RulingCoverage{Enforced: 1, Total: 2}))
		Expect(session.CoverageOf(inForce).Unenforced()).To(Equal(1))

		info, err := os.Stat(filepath.Join(state, session.RulingsFile))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("keeps the latest record of an identifier and drops what a later ruling supersedes", func() {
		state := GinkgoT().TempDir()
		gated := nativeTools
		gated.EnforcedBy, gated.PendingGate = "PreToolUse native-tool refusal (YOU-DO #424)", ""
		replacement := session.Ruling{ID: "opus-always", Statement: "Opus, always.", Quote: "just make it opus", RuledOn: "2026-10-05", Supersedes: "opus-only"}
		for _, ruling := range []session.Ruling{opusOnly, nativeTools, gated, replacement} {
			Expect(session.AppendRuling(state, ruling)).To(Succeed())
		}

		inForce, err := session.RulingsInForce(state)
		Expect(err).NotTo(HaveOccurred())
		Expect(inForce).To(Equal([]session.Ruling{gated, replacement}))
		Expect(session.CoverageOf(inForce)).To(Equal(session.RulingCoverage{Enforced: 1, Total: 2}))
	})

	It("reads no rulings file as no rulings, and refuses records it cannot decode", func() {
		inForce, err := session.RulingsInForce(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		Expect(inForce).To(BeEmpty())

		_, err = session.InForce([]byte("{\"ruling_id\":\"a\"}\nnot json\n"))
		Expect(err).To(MatchError(ContainSubstring("decode ruling")))
	})

	It("flags a ruling no gate enforces, naming the pending gate when there is one", func() {
		Expect(opusOnly.Enforcement()).To(Equal("enforced by allowed-models refusal at submit (COPILOT-PARITY #343)"))
		Expect(nativeTools.Enforcement()).To(Equal("UNENFORCED (pending PreToolUse native-tool refusal (YOU-DO #424))"))
		Expect(session.Ruling{ID: "bare"}.Enforcement()).To(Equal(session.Unenforced))
	})

	It("generates the human-readable view from the records: coverage, then each ruling with its gate or UNENFORCED and the operator's words", func() {
		var view strings.Builder
		Expect(session.WriteRulingsView(&view, []session.Ruling{opusOnly, nativeTools})).To(Succeed())
		Expect(view.String()).To(Equal(`# Operator rulings

Generated by ` + "`csf rulings`" + ` from the ruling records under the harness state directory. Record a ruling with the RecordRuling operation; never edit this text.

Enforced by a gate: 1 of 2. UNENFORCED: 1.

## opus-only: enforced by allowed-models refusal at submit (COPILOT-PARITY #343)

Every real session runs claude-opus-5-5. Never Fable.

> DON'T USE FABLE JUST USE OPUS

- Ruled on: 2026-10-05
- Scope: every real session
- Why: two recipes resumed on Fable despite a prose ruling
- Excludes: fable

## no-native-tools: UNENFORCED (pending PreToolUse native-tool refusal (YOU-DO #424))

No executor-native presentation or orchestration tools.

> dont ever use your native tools again

- Ruled on: 2026-10-05
`))
	})
})
