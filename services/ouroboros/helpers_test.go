// Copyright 2026 Candace Labs

package ouroboros_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing/fstest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/kernel/clock"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/services/cron/crontest"
	"github.com/candacelabs/csf/services/ouroboros"
	"github.com/candacelabs/csf/services/ouroboros/mocks"
)

// The integration specs grant the loop every capability as a double or
// pgmem: a gomock launcher stands in for the miners, git and the merge
// path, a gomock ticket capability for gh, a gomock session capability for
// the harness, an in-memory tree for the corpus and the repository, and
// the ledger runs on pgmem with CSF's real schema. Nothing crosses a
// boundary.
const (
	specRepository   = "candacelabs/csf_staging"
	specMerger       = "csf-serve/4242"
	runAssignment    = "6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60"
	otherAssignment  = "6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e59"
	runLog           = runAssignment + "/events.jsonl"
	otherLog         = otherAssignment + "/events.jsonl"
	minerRules       = "services/ouroboros/miners/_template/rules.dl"
	minerExecutable  = "bazel-bin/services/ouroboros/miners/_template/miner.exe"
	minerName        = "_template"
	backtestJSON     = `{"labels":[],"split":1790964285,"knee":468,"families":3,"tp":["` + runAssignment + `"],"fp":[],"fn":[],"lead":304}` + "\n"
	findingJSON      = `{"miner":"draft-pr-late","rule":"invisible","subject":[{"text":"` + runAssignment + `"}],"severity":"SEVERITY_S3","scope":"SCOPE_GENERIC","proof":[]}` + "\n"
	specProposalBody = "| field | value |\n|---|---|\n| miner | `no_self_merge` |\n| corpus | pull_request, jsonl |\n\n" +
		"| labeled instance | sign | source |\n|---|---|---|\n| `" + runAssignment + "` | + | ticket #249 |\n"
	syntheticBody = "| field | value |\n|---|---|\n| miner | `serve_drain_resume` |\n| corpus | jsonl |\n\n" +
		"| labeled instance | sign | source |\n|---|---|---|\n| `test-drain-ok-001` | + | ticket title |\n"
	logLine = `{"time":"2026-10-03T19:03:08Z","event_type":"harness_run_started","assignment_id":"` + runAssignment + `"}` + "\n"
)

var (
	specStart = time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	// ledgerBudget bounds a wait on ledger state the watch goroutine writes.
	ledgerBudget = eventually.Budget{Within: 10 * time.Second, Interval: 10 * time.Millisecond}
)

// fixture is one spec's loop with every double it was granted.
type fixture struct {
	loop       *ouroboros.Loop
	ledger     *ouroboros.Ledger
	launcher   *MockILauncher
	sessions   *MockIAgentSessions
	tickets    *mocks.MockITickets
	watcher    *MockIWatcher
	clock      *clock.ManualClock
	corpus     fstest.MapFS
	repository fstest.MapFS
	state      string
}

// newFixture builds a loop over pgmem with the doubles; extra options come
// last, so a spec can replace the ledger or turn fixers on.
func newFixture(extra ...ouroboros.LoopOption) *fixture {
	GinkgoHelper()
	controller := gomock.NewController(GinkgoT())
	store := crontest.OpenStore(GinkgoT())
	ledger, err := ouroboros.NewLedger(store.Database())
	Expect(err).NotTo(HaveOccurred())
	spec := &fixture{
		ledger:   ledger,
		launcher: NewMockILauncher(controller),
		sessions: NewMockIAgentSessions(controller),
		tickets:  mocks.NewMockITickets(controller),
		watcher:  NewMockIWatcher(controller),
		clock:    clock.NewManualClock(specStart),
		corpus:   fstest.MapFS{runLog: &fstest.MapFile{Data: []byte(logLine)}},
		repository: fstest.MapFS{
			minerRules:      &fstest.MapFile{Data: []byte("invisible(R) :- gated(R).\n")},
			minerExecutable: &fstest.MapFile{Data: []byte("binary"), Mode: 0o755},
		},
		state: GinkgoT().TempDir(),
	}
	spec.tickets.EXPECT().Repository().Return(specRepository).AnyTimes()
	stateFiles, err := iofs.NewHostFiles(spec.state)
	Expect(err).NotTo(HaveOccurred())
	options := append([]ouroboros.LoopOption{
		ouroboros.WithLedger(ledger),
		ouroboros.WithSessions(spec.sessions),
		ouroboros.WithLauncher(spec.launcher),
		ouroboros.WithTickets(spec.tickets),
		ouroboros.WithClock(spec.clock),
		ouroboros.WithCorpus("/corpus", spec.corpus, spec.watcher),
		ouroboros.WithState(spec.state, stateFiles),
		ouroboros.WithRepository("/repository", spec.repository),
		ouroboros.WithMergerIdentity(specMerger),
		ouroboros.WithLogger(slog.New(slog.DiscardHandler)),
	}, extra...)
	spec.loop, err = ouroboros.NewLoop(options...)
	Expect(err).NotTo(HaveOccurred())
	return spec
}

// minerVerb matches the template miner's invocation of one verb.
func minerVerb(verbs ...string) gomock.Matcher {
	return gomock.Cond(func(command proc.Command) bool {
		return command.Executable == filepath.Join("/repository", minerExecutable) && len(command.Arguments) > 0 && slices.Contains(verbs, command.Arguments[0])
	})
}

// gitQuery matches a git query the internal check makes.
func gitQuery() gomock.Matcher {
	return gomock.Cond(func(command proc.Command) bool { return command.Executable == "git" })
}

// expectMiner answers the template miner's invocations: its scope whenever
// it is listed, and exactly times backtests (the knee fit) and findings
// over the items.
func (spec *fixture) expectMiner(times int) {
	spec.launcher.EXPECT().Run(gomock.Any(), minerVerb("scope")).Return(proc.Result{Stdout: []byte(ouroboros.ScopeGeneric + "\n")}, nil).AnyTimes()
	spec.launcher.EXPECT().Run(gomock.Any(), minerVerb("backtest", "findings")).DoAndReturn(func(_ context.Context, command proc.Command) (proc.Result, error) {
		switch command.Arguments[0] {
		case "backtest":
			Expect(command.Arguments[1]).To(Equal("--json"))
			return proc.Result{Stdout: []byte(backtestJSON)}, nil
		case "findings":
			Expect(command.Arguments[1:3]).To(Equal([]string{"--knee", "468"}))
			return proc.Result{Stdout: []byte(findingJSON)}, nil
		}
		return proc.Result{ExitCode: -1}, proc.ErrExecutableRequired
	}).Times(times)
}

// expectStructure answers the internal check's git queries with main at
// one commit holding one miner, three gates and two terms.
func (spec *fixture) expectStructure() {
	spec.launcher.EXPECT().Run(gomock.Any(), gitQuery()).DoAndReturn(func(_ context.Context, command proc.Command) (proc.Result, error) {
		switch command.Arguments[2] {
		case "rev-list":
			return proc.Result{Stdout: []byte("8798a27\n")}, nil
		case "ls-tree":
			return proc.Result{Stdout: []byte("services/ouroboros/miners/_template/extract.ml\nservices/ouroboros/miners/_template/rules.dl\n")}, nil
		case "show":
			if strings.HasSuffix(command.Arguments[3], "gate.go") {
				return proc.Result{Stdout: []byte("const (\n\tGateWait   = \"wait\"\n\tGateReady  = \"ready\"\n\tGateCommit = \"commit\"\n)\n")}, nil
			}
			return proc.Result{Stdout: []byte("term observe \"Observe\" \"x\";\nterm miner \"Miner\" \"y\";\n")}, nil
		}
		return proc.Result{ExitCode: -1}, proc.ErrExecutableRequired
	}).AnyTimes()
}

func (spec *fixture) snapshotPath() string { return filepath.Join(spec.state, ouroboros.SnapshotFile) }

func (spec *fixture) readSnapshot() []byte {
	content, err := os.ReadFile(spec.snapshotPath())
	if err != nil {
		return nil
	}
	return content
}
