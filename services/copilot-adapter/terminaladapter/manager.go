// Package terminaladapter owns interactive PTYs for the Copilot workbench.
package terminaladapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"
	"github.com/google/uuid"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	"github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

const (
	defaultReplayBytes     = 1 << 20
	maximumInputCharacters = 65536
	readChunkBytes         = 4 << 10
	terminalEnvironment    = "TERM=xterm-256color"
	procDirectory          = "/proc"
	procStatFilename       = "stat"
)

// Config fixes the shell and replay bound; clients cannot override either.
type Config struct {
	Shell              string
	ReplayBytes        int
	ExitedHistoryLimit int
}

// TerminalManager owns process state until Close.
type TerminalManager struct {
	shell              string
	replayBytes        int
	exitedHistoryLimit int
	mu                 sync.RWMutex
	terminals          map[uuid.UUID]*terminal
	exited             []uuid.UUID
}

type terminal struct {
	mu              sync.RWMutex
	snapshot        copilotadapter.TerminalSnapshot
	command         *exec.Cmd
	pty             *os.File
	processGroup    int
	sessionID       int
	events          []copilotadapter.TerminalOutput
	eventBytes      int
	nextSeq         int64
	replayTruncated bool
	done            chan struct{}
	changed         chan struct{}
	stopOnce        sync.Once
}

var _ copilotadapter.ITerminalManager = (*TerminalManager)(nil)

// NewTerminalManager validates the fixed executable and replay bound.
func NewTerminalManager(config Config) (*TerminalManager, error) {
	if config.Shell == "" {
		return nil, fmt.Errorf("terminal adapter: shell is required")
	}
	if _, err := os.Stat(config.Shell); err != nil {
		return nil, fmt.Errorf("terminal adapter: shell: %w", err)
	}
	if config.ReplayBytes <= 0 {
		config.ReplayBytes = defaultReplayBytes
	}
	if config.ExitedHistoryLimit <= 0 {
		return nil, fmt.Errorf("terminal adapter: exited history limit must be positive")
	}
	return &TerminalManager{
		shell: config.Shell, replayBytes: config.ReplayBytes,
		exitedHistoryLimit: config.ExitedHistoryLimit, terminals: map[uuid.UUID]*terminal{},
	}, nil
}

// List returns snapshots for one worktree.
func (manager *TerminalManager) List(worktreeID uuid.UUID) []copilotadapter.TerminalSnapshot {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	result := []copilotadapter.TerminalSnapshot{}
	for _, process := range manager.terminals {
		process.mu.RLock()
		if process.snapshot.WorktreeID == worktreeID {
			result = append(result, process.snapshot)
		}
		process.mu.RUnlock()
	}
	sort.Slice(result, func(first int, second int) bool {
		if result[first].CreatedAt.Equal(result[second].CreatedAt) {
			return result[first].ID.String() > result[second].ID.String()
		}
		return result[first].CreatedAt.After(result[second].CreatedAt)
	})
	return result
}

// Create starts one fixed shell in the requested worktree directory.
func (manager *TerminalManager) Create(ctx context.Context, spec copilotadapter.TerminalSpec) (copilotadapter.TerminalSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return copilotadapter.TerminalSnapshot{}, err
	}
	if spec.Rows <= 0 || spec.Columns <= 0 {
		return copilotadapter.TerminalSnapshot{}, fmt.Errorf("terminal adapter: rows and columns must be positive")
	}
	identifier := uuid.New()
	now := time.Now().UTC()
	command := exec.Command(manager.shell)
	command.Dir = spec.Directory
	command.Env = append(os.Environ(), terminalEnvironment)
	file, err := pty.StartWithSize(command, &pty.Winsize{Rows: uint16(spec.Rows), Cols: uint16(spec.Columns)})
	if err != nil {
		return copilotadapter.TerminalSnapshot{}, fmt.Errorf("terminal adapter: start shell: %w", err)
	}
	processGroup, err := syscall.Getpgid(command.Process.Pid)
	if err != nil {
		_ = file.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		return copilotadapter.TerminalSnapshot{}, fmt.Errorf("terminal adapter: inspect shell process group: %w", err)
	}
	process := &terminal{
		snapshot: copilotadapter.TerminalSnapshot{
			ID: identifier, WorktreeID: spec.WorktreeID, Rows: spec.Rows, Columns: spec.Columns,
			Shell: manager.shell, Status: string(api.TerminalStatusRunning), CreatedAt: now, UpdatedAt: now,
		},
		command: command, pty: file, processGroup: processGroup, sessionID: command.Process.Pid,
		nextSeq: 1, done: make(chan struct{}), changed: make(chan struct{}),
	}
	snapshot := process.snapshot
	manager.mu.Lock()
	manager.terminals[identifier] = process
	manager.mu.Unlock()
	go manager.drain(process)
	return snapshot, nil
}

// Get reads one current snapshot.
func (manager *TerminalManager) Get(identifier uuid.UUID) (copilotadapter.TerminalSnapshot, bool) {
	process, found := manager.lookup(identifier)
	if !found {
		return copilotadapter.TerminalSnapshot{}, false
	}
	process.mu.RLock()
	defer process.mu.RUnlock()
	return process.snapshot, true
}

// Resize updates the kernel PTY and its snapshot.
func (manager *TerminalManager) Resize(identifier uuid.UUID, rows int32, columns int32) (copilotadapter.TerminalSnapshot, error) {
	if rows <= 0 || columns <= 0 {
		return copilotadapter.TerminalSnapshot{}, fmt.Errorf("terminal adapter: rows and columns must be positive")
	}
	process, found := manager.lookup(identifier)
	if !found {
		return copilotadapter.TerminalSnapshot{}, os.ErrNotExist
	}
	process.mu.RLock()
	if process.snapshot.Status != string(api.TerminalStatusRunning) {
		process.mu.RUnlock()
		return copilotadapter.TerminalSnapshot{}, fmt.Errorf("terminal adapter: terminal is not running")
	}
	process.mu.RUnlock()
	if err := pty.Setsize(process.pty, &pty.Winsize{Rows: uint16(rows), Cols: uint16(columns)}); err != nil {
		return copilotadapter.TerminalSnapshot{}, err
	}
	process.mu.Lock()
	defer process.mu.Unlock()
	process.snapshot.Rows, process.snapshot.Columns = rows, columns
	process.snapshot.UpdatedAt = time.Now().UTC()
	return process.snapshot, nil
}

// Write sends one bounded, valid UTF-8 input chunk.
func (manager *TerminalManager) Write(identifier uuid.UUID, data string) (copilotadapter.TerminalSnapshot, error) {
	process, found := manager.lookup(identifier)
	if !found {
		return copilotadapter.TerminalSnapshot{}, os.ErrNotExist
	}
	if !utf8.ValidString(data) {
		return copilotadapter.TerminalSnapshot{}, fmt.Errorf("terminal adapter: input must be UTF-8")
	}
	if data == "" {
		return copilotadapter.TerminalSnapshot{}, fmt.Errorf("terminal adapter: input must not be empty")
	}
	if utf8.RuneCountInString(data) > maximumInputCharacters {
		return copilotadapter.TerminalSnapshot{}, fmt.Errorf("terminal adapter: input exceeds %d characters", maximumInputCharacters)
	}
	process.mu.RLock()
	if process.snapshot.Status != string(api.TerminalStatusRunning) {
		process.mu.RUnlock()
		return copilotadapter.TerminalSnapshot{}, fmt.Errorf("terminal adapter: terminal is not running")
	}
	process.mu.RUnlock()
	written, err := io.WriteString(process.pty, data)
	if err != nil {
		return copilotadapter.TerminalSnapshot{}, err
	}
	if written != len(data) {
		return copilotadapter.TerminalSnapshot{}, io.ErrShortWrite
	}
	process.mu.Lock()
	defer process.mu.Unlock()
	process.snapshot.UpdatedAt = time.Now().UTC()
	return process.snapshot, nil
}

// Stop kills one owned shell and waits for its drain goroutine to finalize.
func (manager *TerminalManager) Stop(identifier uuid.UUID) (copilotadapter.TerminalSnapshot, error) {
	process, found := manager.lookup(identifier)
	if !found {
		return copilotadapter.TerminalSnapshot{}, os.ErrNotExist
	}
	process.mu.RLock()
	running := process.snapshot.Status == string(api.TerminalStatusRunning)
	process.mu.RUnlock()
	if running {
		process.stopOnce.Do(func() {
			process.mu.RLock()
			stillRunning := process.snapshot.Status == string(api.TerminalStatusRunning)
			processGroup, sessionID := process.processGroup, process.sessionID
			process.mu.RUnlock()
			if stillRunning && processGroup > 0 {
				killSession(sessionID, processGroup)
				_ = process.pty.Close()
			}
		})
	}
	<-process.done
	process.mu.RLock()
	snapshot := process.snapshot
	process.mu.RUnlock()
	return snapshot, nil
}

// killSession terminates background jobs that an interactive shell may place
// in their own process groups while retaining the shell's session id, then
// kills the shell's group. Every selected PID belongs to this PTY session.
func killSession(sessionID int, processGroup int) {
	entries, err := os.ReadDir(procDirectory)
	if err == nil {
		for _, entry := range entries {
			identifier, parseErr := strconv.Atoi(entry.Name())
			if parseErr != nil || identifier == sessionID {
				continue
			}
			body, readErr := os.ReadFile(filepath.Join(procDirectory, entry.Name(), procStatFilename))
			if readErr != nil {
				continue
			}
			closeName := strings.LastIndexByte(string(body), ')')
			if closeName < 0 {
				continue
			}
			fields := strings.Fields(string(body[closeName+1:]))
			if len(fields) < 4 {
				continue
			}
			candidateSession, fieldErr := strconv.Atoi(fields[3])
			if fieldErr == nil && candidateSession == sessionID {
				_ = syscall.Kill(identifier, syscall.SIGKILL)
			}
		}
	}
	_ = syscall.Kill(-processGroup, syscall.SIGKILL)
}

// EventsAfter returns the current snapshot and retained output after the
// cursor from one process-state read.
func (manager *TerminalManager) EventsAfter(identifier uuid.UUID, afterSeq int64) (copilotadapter.TerminalEventReplay, bool) {
	process, found := manager.lookup(identifier)
	if !found {
		return copilotadapter.TerminalEventReplay{}, false
	}
	process.mu.RLock()
	defer process.mu.RUnlock()
	result := []copilotadapter.TerminalOutput{}
	truncated := process.replayTruncated && len(process.events) > 0 && afterSeq < process.events[0].Seq
	for _, event := range process.events {
		if event.Seq > afterSeq {
			result = append(result, event)
		}
	}
	if truncated && len(result) > 0 {
		result[0].ReplayTruncated = true
	}
	return copilotadapter.TerminalEventReplay{Snapshot: process.snapshot, Events: result, Changed: process.changed}, true
}

// Close stops every terminal. It is safe to call once through adapter.Close.
func (manager *TerminalManager) Close() error {
	manager.mu.RLock()
	identifiers := make([]uuid.UUID, 0, len(manager.terminals))
	for identifier := range manager.terminals {
		identifiers = append(identifiers, identifier)
	}
	manager.mu.RUnlock()
	for _, identifier := range identifiers {
		if _, err := manager.Stop(identifier); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (manager *TerminalManager) lookup(identifier uuid.UUID) (*terminal, bool) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	process, found := manager.terminals[identifier]
	return process, found
}

func (manager *TerminalManager) drain(process *terminal) {
	defer func() { _ = process.pty.Close() }()
	buffer := make([]byte, readChunkBytes)
	pending := []byte(nil)
	for {
		count, err := process.pty.Read(buffer)
		if count > 0 {
			var output string
			output, pending = decodeTerminalOutput(pending, buffer[:count], false)
			if output != "" {
				manager.append(process, api.TerminalEventKindOutput, output, nil)
			}
		}
		if err != nil {
			break
		}
	}
	if len(pending) > 0 {
		output, _ := decodeTerminalOutput(pending, nil, true)
		manager.append(process, api.TerminalEventKindOutput, output, nil)
	}
	waitErr := process.command.Wait()
	status, kind := api.TerminalStatusExited, api.TerminalEventKindExited
	var exitCode *int32
	if process.command.ProcessState != nil {
		code := int32(process.command.ProcessState.ExitCode())
		exitCode = &code
	}
	if waitErr != nil {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			status, kind = api.TerminalStatusFailed, api.TerminalEventKindFailed
		}
	}
	manager.finalizeExited(process, status, kind, exitCode)
}

func (manager *TerminalManager) finalizeExited(process *terminal, status api.TerminalStatus, kind api.TerminalEventKind, exitCode *int32) {
	// Publish the terminal snapshot and terminal event under the same lock,
	// with the snapshot first. No observer can see exited/failed in the event
	// stream while Get or List still reports running.
	process.mu.Lock()
	process.snapshot.Status, process.snapshot.ExitCode = string(status), exitCode
	process.snapshot.UpdatedAt = time.Now().UTC()
	manager.appendLocked(process, kind, "", exitCode)
	identifier := process.snapshot.ID
	process.mu.Unlock()

	manager.mu.Lock()
	manager.exited = append(manager.exited, identifier)
	for len(manager.exited) > manager.exitedHistoryLimit {
		oldest := manager.exited[0]
		manager.exited = manager.exited[1:]
		delete(manager.terminals, oldest)
	}
	manager.mu.Unlock()
	close(process.done)
}

func (manager *TerminalManager) append(process *terminal, kind api.TerminalEventKind, data string, exitCode *int32) {
	process.mu.Lock()
	defer process.mu.Unlock()
	manager.appendLocked(process, kind, data, exitCode)
}

func (manager *TerminalManager) appendLocked(process *terminal, kind api.TerminalEventKind, data string, exitCode *int32) {
	defer func() {
		close(process.changed)
		process.changed = make(chan struct{})
	}()
	if len(data) > manager.replayBytes {
		data = utf8ReplaySuffix(data, manager.replayBytes)
		process.replayTruncated = true
	}
	event := copilotadapter.TerminalOutput{
		Seq: process.nextSeq, TerminalID: process.snapshot.ID, Kind: string(kind),
		Data: data, ExitCode: exitCode, OccurredAt: time.Now().UTC(),
	}
	process.nextSeq++
	process.events = append(process.events, event)
	process.eventBytes += len(data)
	for process.eventBytes > manager.replayBytes {
		excess := process.eventBytes - manager.replayBytes
		oldest := &process.events[0]
		if excess < len(oldest.Data) {
			trimmed := utf8ReplaySuffix(oldest.Data, len(oldest.Data)-excess)
			process.eventBytes -= len(oldest.Data) - len(trimmed)
			oldest.Data = trimmed
			process.replayTruncated = true
			break
		}
		process.eventBytes -= len(oldest.Data)
		process.replayTruncated = true
		if len(process.events) == 1 {
			oldest.Data = ""
			break
		}
		process.events = process.events[1:]
	}
}

// decodeTerminalOutput carries an incomplete rune between PTY reads and
// replaces malformed bytes so every generated JSON string is valid UTF-8.
func decodeTerminalOutput(pending []byte, chunk []byte, final bool) (string, []byte) {
	input := make([]byte, 0, len(pending)+len(chunk))
	input = append(input, pending...)
	input = append(input, chunk...)
	output := make([]byte, 0, len(input))
	for len(input) > 0 {
		if !utf8.FullRune(input) && !final {
			return string(output), append([]byte(nil), input...)
		}
		runeValue, size := utf8.DecodeRune(input)
		if runeValue == utf8.RuneError && size == 1 {
			output = utf8.AppendRune(output, utf8.RuneError)
			input = input[1:]
			continue
		}
		output = append(output, input[:size]...)
		input = input[size:]
	}
	return string(output), nil
}

func utf8ReplaySuffix(data string, maximumBytes int) string {
	if len(data) <= maximumBytes {
		return data
	}
	start := len(data) - maximumBytes
	for start < len(data) && !utf8.RuneStart(data[start]) {
		start++
	}
	return data[start:]
}
