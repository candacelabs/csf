package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/candacelabs/csf/pkg/core"
	"github.com/candacelabs/csf/services/warden"
)

// defaultFilePollInterval is the fallback poll cadence for a File discoverer
// constructed with a non-positive interval.
const defaultFilePollInterval = 2 * time.Second

// File is a PeerDiscoverer that polls a JSON roster file whose shape is exactly
// warden.Roster: {"nodes":[{"id":"...","addr":"host:port"}, ...]}. It doubles
// as a manual dynamic membership source — an operator edits the file and warden
// picks up the change on the next poll.
//
// A missing, unreadable, or malformed file is an error: File logs a rate-limited
// warning and sends nothing, so consumers keep their last roster. A
// syntactically valid file with an empty node list is a real empty roster and
// is sent faithfully.
type File struct {
	path string
	poll time.Duration
}

var _ warden.IPeerDiscoverer = (*File)(nil)

// NewFile returns a File discoverer for path, polling every poll (defaulting a
// non-positive poll to 2s).
func NewFile(path string, poll time.Duration) *File {
	if poll <= 0 {
		poll = defaultFilePollInterval
	}
	return &File{path: path, poll: poll}
}

// Discover polls the roster file and delivers change-only snapshots until ctx
// ends, then closes the channel.
func (f *File) Discover(ctx context.Context) (<-chan warden.Roster, error) {
	ch := make(chan warden.Roster, 1)
	fetch := func(ctx context.Context) ([]warden.Node, error) { return f.read() }
	logErr := func(err error) {
		if core.Logger != nil {
			core.Logger.Warn().Err(err).Str("file", f.path).
				Msg("discovery(file): reading roster failed; keeping last roster")
		}
	}
	go pollLoop(ctx, ch, f.poll, fetch, logErr)
	return ch, nil
}

// read loads and parses the roster file, returning the sorted node set. An
// empty node list is returned without error (a valid empty roster). Any node
// with an empty id or an addr that is not host:port makes the whole read an
// error, so a half-written or garbage file is never emitted as a partial
// roster.
func (f *File) read() ([]warden.Node, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("discovery(file): reading %q: %w", f.path, err)
	}
	var r warden.Roster
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("discovery(file): parsing %q: %w", f.path, err)
	}
	for _, n := range r.Nodes {
		if n.ID == "" {
			return nil, fmt.Errorf("discovery(file): %q has a node with an empty id", f.path)
		}
		if _, _, err := net.SplitHostPort(n.Addr); err != nil {
			return nil, fmt.Errorf("discovery(file): node %q addr %q is not host:port: %w", n.ID, n.Addr, err)
		}
	}
	nodes := copyNodes(r.Nodes)
	warden.SortNodes(nodes)
	return nodes, nil
}
