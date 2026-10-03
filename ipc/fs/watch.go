// Copyright 2026 Candace Labs

package fs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// ChangeOp is what happened to a watched name.
type ChangeOp uint8

// The operations a watch reports. One kernel notification is one Change; a
// consumer that cares only that something happened reads the name alone.
const (
	ChangeCreated ChangeOp = iota + 1
	ChangeWritten
	ChangeRemoved
	ChangeRenamed
)

// String names the operation for logs and specs.
func (op ChangeOp) String() string {
	switch op {
	case ChangeCreated:
		return "created"
	case ChangeWritten:
		return "written"
	case ChangeRemoved:
		return "removed"
	case ChangeRenamed:
		return "renamed"
	}
	return "unknown"
}

// Change is one notification from the kernel: the slash-separated, unrooted
// name of the file or directory beneath the granted directory, as [IFiles]
// spells names, and what happened to it. Err, when set, reports that the
// kernel's queue failed and the watch has ended; it is the last Change the
// watch delivers before its channel closes.
type Change struct {
	Name string
	Op   ChangeOp
	Err  error
}

// Watch is one open subscription. Changes delivers every notification under
// every directory added to it, in kernel order, and is closed when the context
// the watch was opened with ends or the watch fails. Add watches one more
// directory beneath the granted directory; a watch reports changes to the
// entries of a watched directory, not to the entries of its subdirectories,
// so a consumer adds each directory it reads.
//
// It is a value of two fields rather than an interface: a channel and a
// function are data, and nothing dispatches on them.
type Watch struct {
	Changes <-chan Change
	Add     func(name string) error
}

// IWatcher is change notification for files beneath one host directory: the
// kernel I/O tier's second file boundary, beside [IFiles]. A service that
// reacts to files changing receives it in its constructor and never opens
// the kernel's notification queue itself; a test grants a double whose
// channel it feeds.
type IWatcher interface {
	// Watch opens one subscription bounded by ctx.
	Watch(ctx context.Context) (*Watch, error)
}

// ErrWatchClosed is returned by [Watch.Add] once the watch has ended.
var ErrWatchClosed = errors.New("ipc/fs: the watch has ended")

// changeBuffer is how many notifications a watch holds while its consumer is
// busy before the delivering goroutine waits; the kernel queues behind it.
const changeBuffer = 64

// HostWatcher is one directory of this host's filesystem, granted as a
// change-notification capability over the kernel's inotify queue. Like
// [HostFiles] it holds no open descriptor itself: each Watch opens its own
// queue and closes it when its context ends.
type HostWatcher struct {
	directory string
}

// NewHostWatcher grants change notification beneath directory, which must
// exist and be a directory; the same checks [NewHostFiles] makes, so a binary
// grants both capabilities over one directory with the same argument.
func NewHostWatcher(directory string) (*HostWatcher, error) {
	files, err := NewHostFiles(directory)
	if err != nil {
		return nil, err
	}
	return &HostWatcher{directory: files.Directory()}, nil
}

// Directory is the absolute host directory the capability watches, or empty
// when nothing was granted.
func (watcher *HostWatcher) Directory() string {
	if watcher == nil {
		return ""
	}
	return watcher.directory
}

// Watch opens one inotify queue and delivers its notifications until ctx
// ends. The goroutine it starts is owned here: it exits on ctx, closes the
// queue and then the channel, so a consumer joins it by draining Changes.
func (watcher *HostWatcher) Watch(ctx context.Context) (*Watch, error) {
	if watcher == nil || watcher.directory == "" {
		return nil, ErrNotGranted
	}
	queue, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("ipc/fs: open the notification queue: %w", err)
	}
	changes := make(chan Change, changeBuffer)
	go watcher.deliver(ctx, queue, changes)
	return &Watch{
		Changes: changes,
		Add: func(name string) error {
			path := filepath.Join(watcher.directory, filepath.FromSlash(name))
			if err := queue.Add(path); err != nil {
				if errors.Is(err, fsnotify.ErrClosed) {
					return ErrWatchClosed
				}
				return fmt.Errorf("ipc/fs: watch %s in %s: %w", name, watcher.directory, err)
			}
			return nil
		},
	}, nil
}

// deliver translates the queue's notifications into Changes until ctx ends
// or the queue fails.
func (watcher *HostWatcher) deliver(ctx context.Context, queue *fsnotify.Watcher, changes chan<- Change) {
	defer close(changes)
	defer func() { _ = queue.Close() }()
	for {
		select {
		case <-ctx.Done():
			return
		case event, open := <-queue.Events:
			if !open {
				return
			}
			change, known := watcher.changeOf(event)
			if !known {
				continue
			}
			select {
			case changes <- change:
			case <-ctx.Done():
				return
			}
		case err, open := <-queue.Errors:
			if !open {
				return
			}
			select {
			case changes <- Change{Err: fmt.Errorf("ipc/fs: the notification queue failed: %w", err)}:
			case <-ctx.Done():
			}
			return
		}
	}
}

// changeOf names one kernel event the way IFiles names files. An event the
// capability has no operation for, such as a permission change, is not a
// Change.
func (watcher *HostWatcher) changeOf(event fsnotify.Event) (Change, bool) {
	var op ChangeOp
	switch {
	case event.Has(fsnotify.Create):
		op = ChangeCreated
	case event.Has(fsnotify.Write):
		op = ChangeWritten
	case event.Has(fsnotify.Remove):
		op = ChangeRemoved
	case event.Has(fsnotify.Rename):
		op = ChangeRenamed
	default:
		return Change{}, false
	}
	relative, err := filepath.Rel(watcher.directory, event.Name)
	if err != nil {
		return Change{}, false
	}
	return Change{Name: filepath.ToSlash(relative), Op: op}, true
}
