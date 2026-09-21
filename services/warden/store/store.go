// Package store provides persistence for warden.PersistentState (the Raft
// current term and vote). Two implementations are offered: FileStore, which
// writes durably to disk with an atomic temp-file-plus-rename, and MemStore,
// an in-memory store for tests. Both are safe for concurrent use.
//
// # On-disk format: protobuf-JSON, reads both encodings
//
// FileStore WRITES the state file as canonical protobuf-JSON (protojson with
// UseProtoNames, compacted for byte-stability) of the proto PersistentState,
// via the wireconv boundary, so the durable record and the gRPC wire share one
// source of truth. It READS with a single protojson decoder configured with
// DiscardUnknown: proto3 JSON accepts a 64-bit field as either a JSON number or
// string, so this ONE decoder losslessly parses BOTH the new protojson form
// (term as the string "7") AND the legacy encoding/json form (term as the bare
// number 7, zero-valued fields present) written by earlier binaries — including
// a persisted membership and its created_in_term. See store_contract_test.go
// for the migration proof.
package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/services/warden"
	wardenv1 "github.com/candacelabs/csf/services/warden/proto/warden/v1"
	"github.com/candacelabs/csf/services/warden/wireconv"
)

// compile-time interface assertions.
var (
	_ warden.IStore = (*FileStore)(nil)
	_ warden.IStore = (*MemStore)(nil)
)

// stateMarshal renders PersistentState with proto field names (snake_case);
// stateUnmarshal tolerates unknown fields for forward compatibility and (via
// proto3 JSON's number-or-string rule) the legacy numeric 64-bit encoding.
var (
	stateMarshal   = protojson.MarshalOptions{UseProtoNames: true}
	stateUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}
)

// marshalState serialises st to compacted canonical protobuf-JSON. Compaction
// removes protojson's deliberately-randomised insignificant whitespace so the
// on-disk bytes are deterministic.
func marshalState(st warden.PersistentState) ([]byte, error) {
	raw, err := stateMarshal.Marshal(wireconv.PersistentStateToProto(st))
	if err != nil {
		return nil, fmt.Errorf("marshaling persistent state: %w", err)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, fmt.Errorf("compacting persistent state: %w", err)
	}
	return buf.Bytes(), nil
}

// unmarshalState parses a state file (either encoding) into the domain type.
func unmarshalState(data []byte) (warden.PersistentState, error) {
	var pb wardenv1.PersistentState
	if err := stateUnmarshal.Unmarshal(data, &pb); err != nil {
		return warden.PersistentState{}, err
	}
	return wireconv.PersistentStateFromProto(&pb), nil
}

// FileStore persists PersistentState to a single JSON file. Save is atomic
// and durable: it writes to a temp file in the same directory, fsyncs it,
// then renames it over the target (and fsyncs the directory) so a crash never
// leaves a partially written state file.
type FileStore struct {
	path string
	mu   sync.Mutex
}

// NewFileStore returns a FileStore backed by path. Parent directories are
// created on the first Save.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// Save atomically persists st. It returns only after the data is durable on
// disk.
func (s *FileStore) Save(st warden.PersistentState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating state dir %q: %w", dir, err)
	}

	data, err := marshalState(st)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp state file: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail out before the rename succeeds.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsyncing temp state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp state file: %w", err)
	}

	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("renaming state file into place: %w", err)
	}

	// Fsync the directory so the rename itself is durable. A failure here is
	// non-fatal to correctness (the file content is already synced) but we
	// surface it.
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("fsyncing state dir: %w", err)
	}
	return nil
}

// Load reads the persisted state. ok is false when no state file exists yet.
// A malformed file returns an error.
func (s *FileStore) Load() (warden.PersistentState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return warden.PersistentState{}, false, nil
		}
		return warden.PersistentState{}, false, fmt.Errorf("reading state file: %w", err)
	}

	st, err := unmarshalState(data)
	if err != nil {
		return warden.PersistentState{}, false, fmt.Errorf("unmarshaling state file %q: %w", s.path, err)
	}
	return st, true, nil
}

// syncDir opens dir and fsyncs it so a preceding rename is durable.
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// MemStore is an in-memory warden.IStore for tests. It is safe for concurrent
// use and can be reused across simulated process restarts (construct a new
// Manager against the same MemStore to model a restart with intact state).
type MemStore struct {
	mu    sync.Mutex
	state warden.PersistentState
	saved bool
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{}
}

// Save records st in memory.
func (s *MemStore) Save(st warden.PersistentState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = st
	s.saved = true
	return nil
}

// Load returns the last saved state; ok is false if Save was never called.
func (s *MemStore) Load() (warden.PersistentState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.saved, nil
}
