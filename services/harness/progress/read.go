// Copyright 2026 Candace Labs

package progress

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"path"
	"sort"
	"strings"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/harness/session"
)

// The two run directory files this projection reads beside the run record and
// the event log. services/harness owns writing them, under the names QueueFile
// and EndedFile; the projection reads the same bytes those name.
const (
	queueFile = "queue.json"
	endedFile = "ended"
	// rootName is the state directory itself in the capability's naming.
	rootName = "."
)

// eventBuffer bounds one read of an event log.
const eventBuffer = 64 << 10

// Session reads a session's run record into the row it labels, and the depth
// of its queue, before its event log is folded. A directory with no run record
// is not a session and reads as the zero row.
func Session(files iofs.IFiles, assignment string) (Progress, error) {
	content, err := files.ReadFile(path.Join(assignment, session.RunStateFile))
	if errors.Is(err, stdfs.ErrNotExist) {
		return Progress{}, nil
	}
	if err != nil {
		return Progress{}, err
	}
	var state session.RunState
	if err := json.Unmarshal(content, &state); err != nil {
		return Progress{}, fmt.Errorf("progress: decode the run record of %s: %w", assignment, err)
	}
	row := Progress{Assignment: assignment, Agent: state.AgentID, Model: state.Model}
	if state.AssignmentID != "" {
		row.Assignment = state.AssignmentID
	}
	row.worktree = state.Worktree
	row.baseBranch = state.BaseBranch
	row.Queued = queueDepth(files, assignment)
	return row, nil
}

// ReadAll projects every session in the granted state directory into one row
// each, ordered by assignment. A directory that is not a session is skipped.
// It reads each log whole; [Work] fills the git fields, which need a process.
func ReadAll(files iofs.IFiles) ([]Progress, error) {
	entries, err := files.ReadDir(rootName)
	if err != nil {
		return nil, err
	}
	rows := []Progress{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		assignment := entry.Name()
		row, err := Session(files, assignment)
		if err != nil {
			return nil, err
		}
		if row.Assignment == "" {
			continue
		}
		folded, err := Follow(files, assignment, row, 0)
		if err != nil {
			return nil, err
		}
		rows = append(rows, endPhase(files, assignment, folded.Row))
	}
	sort.Slice(rows, func(left, right int) bool { return rows[left].Assignment < rows[right].Assignment })
	return rows, nil
}

// Projection is every session's row and every tail line the state directory
// folds to, in the order each verb reads them.
type Projection struct {
	Rows  []Progress
	Lines []Line
}

// ReadAllLines projects every session in the granted state directory and
// collects every tail line it folds, ordered by time then assignment. It is
// the read csf tail makes; [Work] fills the rows' git fields, which need a
// process, and the lines' agents are already set.
func ReadAllLines(files iofs.IFiles) (Projection, error) {
	entries, err := files.ReadDir(rootName)
	if err != nil {
		return Projection{}, err
	}
	rows := []Progress{}
	lines := []Line{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		assignment := entry.Name()
		row, err := Session(files, assignment)
		if err != nil {
			return Projection{}, err
		}
		if row.Assignment == "" {
			continue
		}
		folded, err := Follow(files, assignment, row, 0)
		if err != nil {
			return Projection{}, err
		}
		rows = append(rows, endPhase(files, assignment, folded.Row))
		lines = append(lines, folded.Lines...)
	}
	sort.Slice(rows, func(left, right int) bool { return rows[left].Assignment < rows[right].Assignment })
	sort.SliceStable(lines, func(left, right int) bool {
		if lines[left].Time.Equal(lines[right].Time) {
			return lines[left].Agent < lines[right].Agent
		}
		return lines[left].Time.Before(lines[right].Time)
	})
	return Projection{Rows: rows, Lines: lines}, nil
}

// Followed is one session's row after its event log is folded from an offset,
// the lines the fold added, and the offset to read from next.
type Followed struct {
	Row    Progress
	Lines  []Line
	Offset int64
}

// Follow reads one session's event log from offset, folds its complete records
// into row and returns the lines they add and the offset to read from next. A
// trailing line with no newline is still being written and is left for the
// next read, so a line is shown once and whole.
func Follow(files iofs.IFiles, assignment string, row Progress, offset int64) (Followed, error) {
	file, err := files.Open(path.Join(assignment, session.EventsFile))
	if errors.Is(err, stdfs.ErrNotExist) {
		return Followed{Row: row, Offset: offset}, nil
	}
	if err != nil {
		return Followed{Row: row, Offset: offset}, err
	}
	defer func() { _ = file.Close() }()
	if err := skip(file, offset); err != nil {
		return Followed{Row: row, Offset: offset}, fmt.Errorf("progress: seek the event log of %s: %w", assignment, err)
	}
	reader := bufio.NewReaderSize(file, eventBuffer)
	var lines []Line
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return Followed{Row: row, Lines: lines, Offset: offset}, nil
			}
			return Followed{Row: row, Lines: lines, Offset: offset}, fmt.Errorf("progress: read the event log of %s: %w", assignment, err)
		}
		offset += int64(len(line))
		var record session.Record
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		var produced []Line
		row, produced = Fold(row, &record)
		lines = append(lines, produced...)
	}
}

// queueFileRecord is the queue file's shape: the messages waiting behind a
// session's running turn.
type queueFileRecord struct {
	Queued []json.RawMessage `json:"queued"`
}

// queueDepth is how many messages wait behind a session's running turn, as its
// run directory's queue records them. A queue that cannot be read is no
// messages.
func queueDepth(files iofs.IFiles, assignment string) int {
	content, err := files.ReadFile(path.Join(assignment, queueFile))
	if err != nil {
		return 0
	}
	var queue queueFileRecord
	if json.Unmarshal(content, &queue) != nil {
		return 0
	}
	return len(queue.Queued)
}

// endPhase is a row's phase as its run directory's end file records it: a run
// the harness canceled or failed does not reopen, so its end file is the
// authority over the phase the event log's last record implies. A run with no
// end file keeps the phase the fold read.
func endPhase(files iofs.IFiles, assignment string, row Progress) Progress {
	content, err := files.ReadFile(path.Join(assignment, endedFile))
	if err != nil {
		return row
	}
	switch strings.TrimSpace(string(content)) {
	case harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED.String():
		row.Phase = PhaseCanceled
	case harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED.String():
		row.Phase = PhaseFailed
	}
	return row
}

// skip moves past the bytes already read: by seeking when the file can, and by
// reading them otherwise, as an in-memory tree's file does.
func skip(file stdfs.File, offset int64) error {
	if offset == 0 {
		return nil
	}
	if seeker, can := file.(io.Seeker); can {
		_, err := seeker.Seek(offset, io.SeekStart)
		return err
	}
	_, err := io.CopyN(io.Discard, file, offset)
	return err
}
