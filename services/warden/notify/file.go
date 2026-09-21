package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/candacelabs/csf/services/warden"
)

// FileNotifier appends one JSON-encoded incident per line to a file. It is
// used by the e2e harness as a durable, greppable sink.
//
// It is safe for concurrent use without any lock: each Notify opens the file
// with O_APPEND, writes the fully marshaled line (a single []byte, one Write
// syscall) and closes it. On a local filesystem the kernel guarantees an
// O_APPEND write is positioned at end-of-file and applied atomically, so
// concurrent callers never interleave or lose lines.
type FileNotifier struct {
	path string
}

var _ warden.INotifier = (*FileNotifier)(nil)

// NewFileNotifier returns a FileNotifier writing to path.
func NewFileNotifier(path string) *FileNotifier { return &FileNotifier{path: path} }

// Notify appends inc as one JSON line (terminated by '\n') to the file.
func (n *FileNotifier) Notify(ctx context.Context, inc warden.Incident) error {
	line, err := json.Marshal(inc)
	if err != nil {
		return fmt.Errorf("marshaling incident %s: %w", inc.ID, err)
	}
	line = append(line, '\n')

	f, err := os.OpenFile(n.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening notify file %s: %w", n.path, err)
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("appending incident to %s: %w", n.path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing notify file %s: %w", n.path, err)
	}
	return nil
}
