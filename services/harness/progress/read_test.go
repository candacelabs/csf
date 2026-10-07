// Copyright 2026 Candace Labs

package progress_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/ipc/proc"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/harness/progress"
	"github.com/candacelabs/csf/services/harness/session"
)

// The two files the projection reads beside the run record and the event log.
// services/harness owns writing them, under the names QueueFile and EndedFile.
const (
	queueFile = "queue.json"
	endedFile = "ended"
)

// recordBytes is records as the event log holds them: one JSON object a line,
// each with its newline.
func recordBytes(records ...session.Record) []byte {
	lines := make([]string, 0, len(records))
	for _, record := range records {
		content, err := json.Marshal(record)
		Expect(err).NotTo(HaveOccurred())
		lines = append(lines, string(content))
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

var _ = Describe("reading sessions from the state directory", func() {
	var (
		ctx       context.Context
		directory string
		files     iofs.IFiles
		at        time.Time
	)

	BeforeEach(func() {
		ctx = context.Background()
		directory = GinkgoT().TempDir()
		granted, err := iofs.NewHostFiles(directory)
		Expect(err).NotTo(HaveOccurred())
		files = granted
		at = time.Date(2026, time.October, 6, 0, 27, 31, 0, time.UTC)
	})

	writeRun := func(assignment string, state session.RunState) {
		Expect(os.MkdirAll(filepath.Join(directory, assignment), 0o700)).To(Succeed())
		content, err := json.Marshal(state)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(directory, assignment, session.RunStateFile), content, 0o600)).To(Succeed())
	}
	writeEvents := func(assignment string, records ...session.Record) {
		Expect(os.MkdirAll(filepath.Join(directory, assignment), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, assignment, session.EventsFile), recordBytes(records...), 0o600)).To(Succeed())
	}
	appendEvents := func(assignment string, records ...session.Record) {
		file, err := os.OpenFile(filepath.Join(directory, assignment, session.EventsFile), os.O_APPEND|os.O_WRONLY, 0o600)
		Expect(err).NotTo(HaveOccurred())
		_, err = file.Write(recordBytes(records...))
		Expect(file.Close()).To(Succeed())
		Expect(err).NotTo(HaveOccurred())
	}
	write := func(assignment, name, content string) {
		Expect(os.MkdirAll(filepath.Join(directory, assignment), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, assignment, name), []byte(content), 0o600)).To(Succeed())
	}

	It("reads a session's identity, model and queue depth before folding it", func() {
		writeRun("a1", session.RunState{AssignmentID: "a1", AgentID: "observe-sessions", Model: "claude-opus-5"})
		write("a1", queueFile, `{"queued":[{"text":"one"},{"text":"two"}]}`)

		row, err := progress.Session(files, "a1")
		Expect(err).NotTo(HaveOccurred())
		Expect(row.Assignment).To(Equal("a1"))
		Expect(row.Agent).To(Equal("observe-sessions"))
		Expect(row.Model).To(Equal("claude-opus-5"))
		Expect(row.Queued).To(Equal(2))
	})

	It("reads a directory with no run record as no session", func() {
		Expect(os.MkdirAll(filepath.Join(directory, "not-a-session"), 0o700)).To(Succeed())
		row, err := progress.Session(files, "not-a-session")
		Expect(err).NotTo(HaveOccurred())
		Expect(row.Assignment).To(BeEmpty())

		rows, err := progress.ReadAll(files)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(BeEmpty())
	})

	It("projects every session into one row, ordered by assignment", func() {
		writeRun("b2", session.RunState{AssignmentID: "b2", AgentID: "beta"})
		writeEvents("b2", session.Record{Time: at.Add(time.Minute), EventType: session.EventTypeTurnRequested, Turn: 1})
		writeRun("a1", session.RunState{AssignmentID: "a1", AgentID: "alpha"})
		writeEvents("a1", session.Record{Time: at, EventType: session.EventTypeRunStarted})

		rows, err := progress.ReadAll(files)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(2))
		Expect(rows[0].Assignment).To(Equal("a1"))
		Expect(rows[0].Agent).To(Equal("alpha"))
		Expect(rows[0].Phase).To(Equal(progress.PhaseOpen))
		Expect(rows[1].Assignment).To(Equal("b2"))
		Expect(rows[1].Phase).To(Equal(progress.PhaseRunning))
		Expect(rows[1].Turn).To(Equal(1))
	})

	It("lets a run's end file decide a phase the event log cannot reopen", func() {
		writeRun("a1", session.RunState{AssignmentID: "a1", AgentID: "alpha"})
		writeEvents("a1", session.Record{Time: at, EventType: session.EventTypeRunStarted})
		write("a1", endedFile, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED.String()+"\n")

		rows, err := progress.ReadAll(files)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Phase).To(Equal(progress.PhaseCanceled))
	})

	It("collects every folded line, ordered by time then agent", func() {
		writeRun("b2", session.RunState{AssignmentID: "b2", AgentID: "beta"})
		writeEvents("b2", session.Record{Time: at.Add(time.Minute), EventType: session.EventTypeRunStarted})
		writeRun("a1", session.RunState{AssignmentID: "a1", AgentID: "alpha"})
		writeEvents("a1", session.Record{Time: at, EventType: session.EventTypeRunStarted})

		projection, err := progress.ReadAllLines(files)
		Expect(err).NotTo(HaveOccurred())
		Expect(projection.Rows).To(HaveLen(2))
		Expect(projection.Lines).To(HaveLen(2))
		Expect(projection.Lines[0].Agent).To(Equal("alpha"))
		Expect(projection.Lines[1].Agent).To(Equal("beta"))
		Expect(projection.Lines[0].String()).To(Equal("00:27:31 alpha turn session started"))
	})

	It("follows a log from an offset without repeating a line", func() {
		writeRun("a1", session.RunState{AssignmentID: "a1", AgentID: "alpha"})
		first := session.Record{Time: at, EventType: session.EventTypeRunStarted}
		writeEvents("a1", first)

		row, err := progress.Session(files, "a1")
		Expect(err).NotTo(HaveOccurred())
		folded, err := progress.Follow(files, "a1", row, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(folded.Lines).To(HaveLen(1))
		Expect(folded.Offset).To(Equal(int64(len(recordBytes(first)))))

		appendEvents("a1", session.Record{Time: at.Add(time.Minute), EventType: session.EventTypeTurnRequested, Turn: 1})
		next, err := progress.Follow(files, "a1", folded.Row, folded.Offset)
		Expect(err).NotTo(HaveOccurred())
		Expect(next.Lines).To(HaveLen(1))
		Expect(next.Lines[0].Text).To(Equal("turn 1"))
		Expect(next.Offset).To(BeNumerically(">", folded.Offset))
	})

	It("leaves a line still being written for the next read", func() {
		writeRun("a1", session.RunState{AssignmentID: "a1", AgentID: "alpha"})
		content := recordBytes(session.Record{Time: at, EventType: session.EventTypeRunStarted})
		create := filepath.Join(directory, "a1", session.EventsFile)
		Expect(os.MkdirAll(filepath.Dir(create), 0o700)).To(Succeed())
		Expect(os.WriteFile(create, content[:len(content)-1], 0o600)).To(Succeed())

		folded, err := progress.Follow(files, "a1", progress.Progress{Agent: "alpha"}, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(folded.Lines).To(BeEmpty())
		Expect(folded.Offset).To(BeZero())
	})

	It("reads a session with no event log as no lines", func() {
		writeRun("a1", session.RunState{AssignmentID: "a1", AgentID: "alpha"})
		projection, err := progress.ReadAllLines(files)
		Expect(err).NotTo(HaveOccurred())
		Expect(projection.Rows).To(HaveLen(1))
		Expect(projection.Lines).To(BeEmpty())
	})

	Describe("filling the git fields", func() {
		It("counts the commits ahead and the changed files through the launcher", func() {
			writeRun("a1", session.RunState{AssignmentID: "a1", AgentID: "alpha", Worktree: "/worktree", BaseBranch: "origin/main"})
			row, err := progress.Session(files, "a1")
			Expect(err).NotTo(HaveOccurred())

			launcher := NewMockILauncher(gomock.NewController(GinkgoT()))
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, command proc.Command) (proc.Result, error) {
					if slices.Contains(command.Arguments, "rev-list") {
						return proc.Result{Stdout: []byte("3\n")}, nil
					}
					return proc.Result{Stdout: []byte(" M one.go\n M two.go\n?? three.go\n")}, nil
				}).Times(2)

			progress.Work(ctx, launcher, &row)
			Expect(row.CommitsAhead).To(Equal(3))
			Expect(row.ChangedFiles).To(Equal(3))
		})

		It("leaves the counts zero when the session has no worktree yet", func() {
			writeRun("a1", session.RunState{AssignmentID: "a1", AgentID: "alpha"})
			row, err := progress.Session(files, "a1")
			Expect(err).NotTo(HaveOccurred())

			launcher := NewMockILauncher(gomock.NewController(GinkgoT()))
			progress.Work(ctx, launcher, &row)
			Expect(row.CommitsAhead).To(BeZero())
			Expect(row.ChangedFiles).To(BeZero())
		})

		It("leaves the counts zero when git cannot read the tree", func() {
			writeRun("a1", session.RunState{AssignmentID: "a1", AgentID: "alpha", Worktree: "/worktree", BaseBranch: "origin/main"})
			row, err := progress.Session(files, "a1")
			Expect(err).NotTo(HaveOccurred())

			launcher := NewMockILauncher(gomock.NewController(GinkgoT()))
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).Return(proc.Result{}, errors.New("git: not found")).Times(2)

			progress.Work(ctx, launcher, &row)
			Expect(row.CommitsAhead).To(BeZero())
			Expect(row.ChangedFiles).To(BeZero())
		})
	})
})
