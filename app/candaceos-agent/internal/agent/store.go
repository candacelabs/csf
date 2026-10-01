package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"google.golang.org/protobuf/proto"
)

// persistedSnapshot is the stable on-disk JSON boundary. It intentionally
// keeps the original snake_case field names and short desired-state strings
// while the in-memory agent consumes generated protobuf messages directly.
type persistedSnapshot struct {
	Fence      *persistedFence      `json:"fence"`
	Assignment *persistedAssignment `json:"assignment,omitempty"`
	Commands   []Command            `json:"commands,omitempty"`
	UpdatedAt  *time.Time           `json:"updated_at,omitempty"`
}

type persistedFence struct {
	Term     uint64 `json:"term"`
	LeaderID string `json:"leader_id"`
}

type persistedAssignment struct {
	App            string  `json:"app"`
	Project        string  `json:"project"`
	Path           string  `json:"path"`
	DesiredState   string  `json:"desired_state"`
	SourceRevision *string `json:"source_revision,omitempty"`
	ContentSHA256  *string `json:"content_sha256,omitempty"`
}

// IStore persists a reconciliation snapshot.
type IStore interface {
	Load() (Snapshot, bool, error)
	Save(snapshot Snapshot) error
}

// FileStore atomically and durably stores one JSON state record.
type FileStore struct {
	path string
	mu   sync.Mutex
}

// NewFileStore constructs a file-backed state store.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// Load reads the current state, reporting ok=false when it does not exist.
func (s *FileStore) Load() (Snapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot, legacy, found, err := s.readSnapshotFile()
	if err != nil {
		return Snapshot{}, false, err
	}
	if !found {
		return Snapshot{}, false, nil
	}
	if err := validatePersistedFence(snapshot); err != nil {
		return Snapshot{}, false, err
	}
	if snapshot.Assignment == nil {
		return cloneSnapshot(snapshot), true, nil
	}
	if legacy {
		return s.adoptLegacyFenceOnlySnapshot(snapshot)
	}
	if err := candaceosv1.ValidateAssignment(snapshot.Assignment); err != nil {
		return Snapshot{}, false, fmt.Errorf("validating persisted assignment: %w", err)
	}
	return cloneSnapshot(snapshot), true, nil
}

func (s *FileStore) readSnapshotFile() (Snapshot, bool, bool, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return Snapshot{}, false, false, nil
	}
	if err != nil {
		return Snapshot{}, false, false, fmt.Errorf("reading state file: %w", err)
	}
	snapshot, legacy, err := decodePersistedSnapshot(data)
	if err != nil {
		return Snapshot{}, false, false, fmt.Errorf("decoding state file: %w", err)
	}
	return snapshot, legacy, true, nil
}

func validatePersistedFence(snapshot Snapshot) error {
	if snapshot.Fence == nil || snapshot.Fence.GetTerm() == 0 {
		return nil
	}
	if err := candaceosv1.ValidateFence(snapshot.Fence); err != nil {
		return fmt.Errorf("validating persisted fence: %w", err)
	}
	return nil
}

func (s *FileStore) adoptLegacyFenceOnlySnapshot(snapshot Snapshot) (Snapshot, bool, error) {
	// Pre-provenance state cannot safely name executable bytes. Preserve its
	// accepted fence, but require the leader to converge a fresh revision-bound
	// assignment before reporting an app as reconciled.
	snapshot.Assignment = nil
	snapshot.Commands = nil
	snapshot.UpdatedAt = time.Time{}
	if err := s.writeSnapshotAtomically(snapshot); err != nil {
		return Snapshot{}, false, fmt.Errorf("adopting legacy state file: %w", err)
	}
	return cloneSnapshot(snapshot), true, nil
}

// Save writes state through a same-directory temporary file, fsync, rename,
// and directory fsync. A crash cannot expose a partially written fence.
func (s *FileStore) Save(snapshot Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeSnapshotAtomically(snapshot)
}

func (s *FileStore) writeSnapshotAtomically(snapshot Snapshot) error {
	data, err := json.Marshal(encodePersistedSnapshot(snapshot))
	if err != nil {
		return fmt.Errorf("encoding state: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temporary state file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setting temporary state permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temporary state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing temporary state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temporary state: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("installing state file: %w", err)
	}
	dirHandle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening state directory for sync: %w", err)
	}
	defer dirHandle.Close()
	if err := dirHandle.Sync(); err != nil {
		return fmt.Errorf("syncing state directory: %w", err)
	}
	return nil
}

func decodePersistedSnapshot(data []byte) (Snapshot, bool, error) {
	var persisted persistedSnapshot
	if err := json.Unmarshal(data, &persisted); err != nil {
		return Snapshot{}, false, err
	}
	snapshot := Snapshot{Commands: cloneCommands(persisted.Commands)}
	if persisted.Fence != nil {
		snapshot.Fence = &candaceosv1.Fence{
			Term: persisted.Fence.Term, LeaderId: persisted.Fence.LeaderID,
		}
	}
	if persisted.UpdatedAt != nil {
		snapshot.UpdatedAt = *persisted.UpdatedAt
	}
	if persisted.Assignment == nil {
		return snapshot, false, nil
	}
	assignment, legacy := decodePersistedAssignment(persisted.Assignment)
	snapshot.Assignment = assignment
	return snapshot, legacy, nil
}

func decodePersistedAssignment(persisted *persistedAssignment) (*candaceosv1.Assignment, bool) {
	desiredState := candaceosv1.DesiredState_DESIRED_STATE_UNSPECIFIED
	switch persisted.DesiredState {
	case "running":
		desiredState = candaceosv1.DesiredState_DESIRED_STATE_RUNNING
	case "stopped":
		desiredState = candaceosv1.DesiredState_DESIRED_STATE_STOPPED
	}
	assignment := &candaceosv1.Assignment{
		App:          persisted.App,
		Project:      persisted.Project,
		Path:         persisted.Path,
		DesiredState: desiredState,
	}
	if persisted.SourceRevision != nil {
		assignment.SourceRevision = *persisted.SourceRevision
	}
	if persisted.ContentSHA256 != nil {
		assignment.ContentSha256 = *persisted.ContentSHA256
	}
	if persisted.SourceRevision != nil || persisted.ContentSHA256 != nil {
		return assignment, false
	}
	legacyProbe := proto.Clone(assignment).(*candaceosv1.Assignment)
	legacyProbe.SourceRevision = strings.Repeat("0", 40)
	legacyProbe.ContentSha256 = "sha256:" + strings.Repeat("0", 64)
	return assignment, candaceosv1.ValidateAssignment(legacyProbe) == nil
}

func encodePersistedSnapshot(snapshot Snapshot) persistedSnapshot {
	persisted := persistedSnapshot{Commands: cloneCommands(snapshot.Commands)}
	if snapshot.Fence != nil {
		persisted.Fence = &persistedFence{
			Term: snapshot.Fence.GetTerm(), LeaderID: snapshot.Fence.GetLeaderId(),
		}
	}
	if snapshot.Assignment != nil {
		sourceRevision := snapshot.Assignment.GetSourceRevision()
		contentSHA256 := snapshot.Assignment.GetContentSha256()
		persisted.Assignment = &persistedAssignment{
			App:            snapshot.Assignment.GetApp(),
			Project:        snapshot.Assignment.GetProject(),
			Path:           snapshot.Assignment.GetPath(),
			DesiredState:   persistedDesiredState(snapshot.Assignment.GetDesiredState()),
			SourceRevision: &sourceRevision,
			ContentSHA256:  &contentSHA256,
		}
	}
	if !snapshot.UpdatedAt.IsZero() {
		updatedAt := snapshot.UpdatedAt
		persisted.UpdatedAt = &updatedAt
	}
	return persisted
}

func persistedDesiredState(state candaceosv1.DesiredState) string {
	switch state {
	case candaceosv1.DesiredState_DESIRED_STATE_RUNNING:
		return "running"
	case candaceosv1.DesiredState_DESIRED_STATE_STOPPED:
		return "stopped"
	default:
		return ""
	}
}

// MemoryStore is a concurrent in-memory store used by tests and embedders.
type MemoryStore struct {
	mu       sync.Mutex
	snapshot Snapshot
	saved    bool
}

func (s *MemoryStore) Load() (Snapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSnapshot(s.snapshot), s.saved, nil
}

func (s *MemoryStore) Save(snapshot Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = cloneSnapshot(snapshot)
	s.saved = true
	return nil
}
