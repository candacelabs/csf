package copilotbridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	copilot "github.com/github/copilot-sdk/go"
)

var errHistorySnapshotRequired = errors.New("copilot bridge: history export requires HistorySourceDirectory")

// HistorySnapshot is one copied native Copilot session and its typed event
// history. The SDK owns event decoding; this wrapper only associates events
// with the metadata used to discover the session.
type HistorySnapshot struct {
	Metadata copilot.SessionMetadata `json:"metadata"`
	Events   []copilot.SessionEvent  `json:"events"`
}

// ListHistorySessions returns typed metadata from the copied native history
// root without resuming or reading any session events. Hosts can use the SDK
// filter to choose IDs before calling ReadSessionHistory.
func (bridge *CopilotBridge) ListHistorySessions(ctx context.Context, filter *copilot.SessionListFilter) ([]copilot.SessionMetadata, error) {
	if err := bridge.requireHistorySnapshot(); err != nil {
		return nil, err
	}
	metadata, err := bridge.listSessions(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("copilot bridge: list history sessions: %w", err)
	}
	return metadata, nil
}

// ReadHistory reads sessions from the bridge's explicitly configured native
// data root. The bridge creates that disposable copy before SDK startup:
// ResumeSession is the SDK's only public route to GetEvents and may update
// runtime metadata.
// No prompt is sent and no tool handler is registered while reading history.
func (bridge *CopilotBridge) ReadHistory(ctx context.Context, filter *copilot.SessionListFilter) ([]HistorySnapshot, error) {
	metadata, err := bridge.ListHistorySessions(ctx, filter)
	if err != nil {
		return nil, err
	}
	snapshots := make([]HistorySnapshot, 0, len(metadata))
	for _, session := range metadata {
		snapshot, readErr := bridge.readHistorySnapshot(ctx, session)
		if readErr != nil {
			return nil, readErr
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

// ReadSessionHistory reads one explicitly selected session from the copied
// native data root. It uses the SDK metadata lookup before opening the session,
// so callers can avoid enumerating unrelated copied sessions.
func (bridge *CopilotBridge) ReadSessionHistory(ctx context.Context, sessionID string) (HistorySnapshot, error) {
	if err := bridge.requireHistorySnapshot(); err != nil {
		return HistorySnapshot{}, err
	}
	metadata, err := bridge.getSessionMetadata(ctx, sessionID)
	if err != nil {
		return HistorySnapshot{}, fmt.Errorf("copilot bridge: inspect history session %s: %w", sessionID, err)
	}
	if metadata == nil {
		return HistorySnapshot{}, fmt.Errorf("copilot bridge: history session %s not found", sessionID)
	}
	return bridge.readHistorySnapshot(ctx, *metadata)
}

func (bridge *CopilotBridge) requireHistorySnapshot() error {
	if bridge.historySnapshotDirectory == "" {
		return errHistorySnapshotRequired
	}
	return nil
}

func prepareHistoryHome(ctx context.Context, source string) (string, func() error, error) {
	if source == "" {
		return "", nil, nil
	}
	if err := ctx.Err(); err != nil {
		return "", nil, fmt.Errorf("copilot bridge: prepare history home: %w", err)
	}
	absoluteSource, err := filepath.Abs(source)
	if err != nil {
		return "", nil, fmt.Errorf("copilot bridge: resolve history source: %w", err)
	}
	info, err := os.Lstat(absoluteSource)
	if err != nil {
		return "", nil, fmt.Errorf("copilot bridge: inspect history source: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", nil, fmt.Errorf("copilot bridge: history source must be a directory, not a symlink")
	}
	destination, err := os.MkdirTemp("", "csf-copilot-history-")
	if err != nil {
		return "", nil, fmt.Errorf("copilot bridge: create disposable history home: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(destination) }
	if err := copyHistoryTree(ctx, absoluteSource, destination); err != nil {
		_ = cleanup()
		return "", nil, fmt.Errorf("copilot bridge: copy history source: %w", err)
	}
	return destination, cleanup, nil
}

func copyHistoryTree(ctx context.Context, source string, destination string) error {
	sourceRoot, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer func() { _ = sourceRoot.Close() }()
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		info, err := sourceRoot.Lstat(relative)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("source contains symlink %q", relative)
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("source contains unsupported file %q", relative)
		}
		return copyHistoryFile(ctx, sourceRoot, relative, target, info.Mode().Perm())
	})
}

func copyHistoryFile(ctx context.Context, sourceRoot *os.Root, source string, destination string, mode fs.FileMode) error {
	input, err := sourceRoot.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			_ = output.Close()
			return err
		}
		readCount, readErr := input.Read(buffer)
		if readCount > 0 {
			if _, writeErr := output.Write(buffer[:readCount]); writeErr != nil {
				_ = output.Close()
				return writeErr
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			_ = output.Close()
			return readErr
		}
	}
	return output.Close()
}

func (bridge *CopilotBridge) readHistorySnapshot(ctx context.Context, metadata copilot.SessionMetadata) (HistorySnapshot, error) {
	events, err := bridge.readHistory(ctx, metadata)
	if err != nil {
		return HistorySnapshot{}, fmt.Errorf("copilot bridge: read history session %s: %w", metadata.SessionID, err)
	}
	return HistorySnapshot{Metadata: metadata, Events: events}, nil
}

func (bridge *CopilotBridge) readHistoryFromSDK(ctx context.Context, metadata copilot.SessionMetadata) ([]copilot.SessionEvent, error) {
	session, err := bridge.resumeSession(ctx, metadata.SessionID, historyResumeConfig())
	if err != nil {
		return nil, fmt.Errorf("resume copied session: %w", err)
	}
	events, readErr := session.GetEvents(ctx)
	cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), bridge.shutdownTimeout)
	cleanupErr := newDisconnectOperation(bridge.disconnects, session.Disconnect).wait(cleanupContext)
	cancelCleanup()
	if readErr != nil && cleanupErr != nil {
		return nil, errors.Join(readErr, fmt.Errorf("disconnect copied session: %w", cleanupErr))
	}
	if readErr != nil {
		return nil, readErr
	}
	if cleanupErr != nil {
		return nil, fmt.Errorf("disconnect copied session: %w", cleanupErr)
	}
	return events, nil
}

func historyResumeConfig() *copilot.ResumeSessionConfig {
	continuePending := false
	disabled := false
	skipCustomInstructions := true
	return &copilot.ResumeSessionConfig{
		SuppressResumeEvent:     true,
		ContinuePendingWork:     &continuePending,
		AvailableTools:          []string{},
		SkipCustomInstructions:  &skipCustomInstructions,
		EnableConfigDiscovery:   &disabled,
		EnableFileHooks:         &disabled,
		EnableHostGitOperations: &disabled,
		EnableSkills:            &disabled,
	}
}
