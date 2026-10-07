// Copyright 2026 Candace Labs

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/candacelabs/csf/pkg/atomicfile"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/harness/session"
)

const (
	// ResumeQueueFile is the typed record of the resumes a restart holds,
	// under the state directory: why each is held and its position. It is
	// rewritten at every resume pass and lists none once every open run is
	// back.
	ResumeQueueFile = "resume.json"
	// ResumeRetryInterval is how often held resumes are tried again. The
	// kernel recomputes the load average every 5 s (LOAD_FREQ), so trying
	// more often reads the same load and admits nothing more.
	ResumeRetryInterval = 5 * time.Second
)

// ErrResumeHeld reports a resume the launch check did not admit; it stays in
// the resume queue and is tried again.
var ErrResumeHeld = errors.New("harness: resume held by the launch check")

// HeldResume is one open run a restart has not resumed yet.
type HeldResume struct {
	AssignmentID string `json:"assignment_id"`
	// Position is its place in the resume order, from 1.
	Position int `json:"position"`
	// Reason is the launch check's findings that held it.
	Reason string `json:"reason"`
	// HeldSince is when it was first held.
	HeldSince time.Time `json:"held_since"`
}

// ResumeQueue is the resume queue at one pass: how many open runs are back
// and which are held.
type ResumeQueue struct {
	UpdatedAt time.Time    `json:"updated_at"`
	Resumed   int          `json:"resumed"`
	Held      []HeldResume `json:"held"`
}

// openRun is one run a previous process left open.
type openRun struct {
	assignment string
	recipe     *pb.AgentAssignmentRecipe
	// active is whether a turn was running or queued when the process
	// stopped, and since is when that work was recorded.
	active bool
	since  time.Time
}

// listOpenRuns is every run directory with a recorded recipe and run record
// and no end, in resume order: runs with a turn running or queued first,
// oldest first, then idle runs, oldest first.
func listOpenRuns(stateDirectory string) ([]openRun, error) {
	entries, err := os.ReadDir(stateDirectory)
	if err != nil {
		return nil, err
	}
	var runs []openRun
	for _, entry := range entries {
		directory := filepath.Join(stateDirectory, entry.Name())
		if !entry.IsDir() || fileExists(filepath.Join(directory, EndedFile)) || !fileExists(filepath.Join(directory, session.RunStateFile)) {
			continue
		}
		recipe, err := session.ReadRecipe(directory)
		if err != nil {
			continue
		}
		run := openRun{assignment: entry.Name(), recipe: recipe}
		if info, err := os.Stat(filepath.Join(directory, session.RunStateFile)); err == nil {
			run.since = info.ModTime()
		}
		if info, err := os.Stat(filepath.Join(directory, QueueFile)); err == nil && hasPendingWork(filepath.Join(directory, QueueFile)) {
			run.active, run.since = true, info.ModTime()
		}
		runs = append(runs, run)
	}
	slices.SortStableFunc(runs, func(left openRun, right openRun) int {
		if left.active != right.active {
			if left.active {
				return -1
			}
			return 1
		}
		return left.since.Compare(right.since)
	})
	return runs, nil
}

// hasPendingWork is whether a persisted queue holds a turn that was running
// or one queued.
func hasPendingWork(path string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var saved persistedQueue
	if json.Unmarshal(content, &saved) != nil {
		return false
	}
	return saved.Inflight != nil || len(saved.Queued) > 0
}

// resumeInOrder resumes runs in order through admit, which refuses a run
// with ErrResumeHeld while the launch check does not admit it. A held run
// keeps its place, and every run after it waits behind it; record receives
// the queue after every pass, and wait returns false when the resumes are to
// stop. A run admit fails for any other reason is reported through failed
// and dropped.
func resumeInOrder(ctx context.Context, runs []openRun, now func() time.Time,
	admit func(ctx context.Context, run openRun) error,
	wait func(ctx context.Context) bool,
	record func(queue ResumeQueue),
	failed func(run openRun, err error)) {
	queue := ResumeQueue{}
	heldSince := map[string]time.Time{}
	for {
		reason := ""
		for len(runs) > 0 {
			err := admit(ctx, runs[0])
			if errors.Is(err, ErrResumeHeld) {
				reason = err.Error()
				break
			}
			if err != nil {
				failed(runs[0], err)
			} else {
				queue.Resumed++
			}
			runs = runs[1:]
		}
		queue.UpdatedAt = now()
		queue.Held = make([]HeldResume, 0, len(runs))
		for index, run := range runs {
			since, seen := heldSince[run.assignment]
			if !seen {
				since = queue.UpdatedAt
				heldSince[run.assignment] = since
			}
			queue.Held = append(queue.Held, HeldResume{AssignmentID: run.assignment, Position: index + 1, Reason: reason, HeldSince: since})
		}
		record(queue)
		if len(runs) == 0 || !wait(ctx) {
			return
		}
	}
}

// resumeOpenRuns reopens every run a previous process left open, in resume
// order, each through the same launch check as a new launch: at most as many
// as the derived worker cap at once, and none while free disk is below the
// floor. The runs the check holds are recorded in ResumeQueueFile and tried
// again every ResumeRetryInterval until every one is back. Resumed is
// closed after the first pass.
func (service *AgentSessionService) resumeOpenRuns(ctx context.Context) error {
	passed := sync.OnceFunc(func() { close(service.resumed) })
	defer passed()
	runs, err := listOpenRuns(service.runner.StateDirectory())
	if err != nil {
		service.logger.Warn("harness: open runs not listed", "error", err)
		return nil
	}
	if len(runs) == 0 {
		return nil
	}
	path := filepath.Join(service.runner.StateDirectory(), ResumeQueueFile)
	resumeInOrder(ctx, runs, service.clock.Now,
		func(ctx context.Context, run openRun) error {
			_, err := service.submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: run.recipe}, true)
			if errors.Is(err, ErrAdmissionHeld) {
				return fmt.Errorf("%w: %w", ErrResumeHeld, err)
			}
			return err
		},
		func(ctx context.Context) bool {
			delivered, stop := service.clock.After(ResumeRetryInterval)
			defer stop()
			select {
			case <-delivered:
				return true
			case <-ctx.Done():
				return false
			}
		},
		func(queue ResumeQueue) {
			if len(queue.Held) > 0 {
				service.logger.Info("harness: resumes held", "resumed", queue.Resumed, "held", len(queue.Held), "reason", queue.Held[0].Reason, "next", queue.Held[0].AssignmentID)
			} else {
				service.logger.Info("harness: every open run resumed", "resumed", queue.Resumed)
			}
			content, err := json.MarshalIndent(queue, "", "  ")
			if err == nil {
				err = atomicfile.WriteFile(path, append(content, '\n'), stateFileMode)
			}
			if err != nil {
				service.logger.Warn("harness: resume queue not recorded", "error", err)
			}
			passed()
		},
		func(run openRun, err error) {
			service.logger.Warn("harness: open run not resumed", "assignment", run.assignment, "error", err)
		})
	return nil
}
