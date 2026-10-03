// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"errors"
	stdfs "io/fs"
	"strings"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/services/harness/session"
)

// sourceFollow names the follow effect in provenance: every patch a card
// takes carries "effect:opsview.follow".
const sourceFollow = "opsview.follow"

// rootName is the state directory itself in the capabilities' naming.
const rootName = "."

// initialize opens one connection: no cards yet, and the follow effect that
// delivers them. The first paint shows an empty board for the instant it
// takes the effect to read the run directories, and the snapshot that follows
// morphs the cards in.
func (view *OpsView) initialize(ctx context.Context, session live.Session[live.AnonymousIdentity]) (viewState, []live.Effect[live.AnonymousIdentity], error) {
	return viewState{}, []live.Effect[live.AnonymousIdentity]{{Source: sourceFollow, Run: view.follow}}, nil
}

// follow is the one I/O of the view: it reads every run directory once, then
// re-reads a session's files whenever the kernel reports a change to them,
// and emits the session's card each time it changed. It owns the tails for
// the life of the connection; nothing else reads them.
func (view *OpsView) follow(ctx context.Context, _ live.Session[live.AnonymousIdentity], emit live.Emitter) error {
	watch, err := view.watcher.Watch(ctx)
	if err != nil {
		return err
	}
	// The root is watched before the directories are listed, so a run
	// directory created during the listing is seen either way.
	if err := watch.Add(rootName); err != nil {
		return err
	}
	tails := map[string]*tail{}
	entries, err := view.files.ReadDir(rootName)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := view.adopt(watch.Add, tails, entry.Name(), emit); err != nil {
			return err
		}
	}
	for change := range watch.Changes {
		if change.Err != nil {
			return change.Err
		}
		assignment, file, followed := splitChange(change.Name)
		switch {
		case !followed:
		case file == "" && view.isDirectory(assignment):
			if _, known := tails[assignment]; !known {
				if err := view.adopt(watch.Add, tails, assignment, emit); err != nil {
					return err
				}
			}
		case file == session.RunStateFile || file == session.EventsFile:
			position, known := tails[assignment]
			if !known {
				continue
			}
			if err := view.deliver(position, assignment, emit); err != nil {
				return err
			}
		}
	}
	return nil
}

// adopt starts following one run directory: watches it, reads it and emits
// its card if it is a session already.
func (view *OpsView) adopt(add func(name string) error, tails map[string]*tail, assignment string, emit live.Emitter) error {
	if err := add(assignment); err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return nil
		}
		return err
	}
	position := &tail{}
	tails[assignment] = position
	return view.deliver(position, assignment, emit)
}

// deliver reads what the session's files hold past the tail and emits the
// card when it changed. A file the harness is mid-way through replacing reads
// again on its next change; a record the view cannot decode is logged and
// skipped rather than ending the follow.
func (view *OpsView) deliver(position *tail, assignment string, emit live.Emitter) error {
	changed, err := position.read(view.files, assignment)
	if err != nil {
		view.logger.Warn("ops view: session not read", "assignment", assignment, "error", err)
		return nil
	}
	if !changed {
		return nil
	}
	region, err := view.cards.Region(assignment)
	if err != nil {
		view.logger.Warn("ops view: run directory name cannot be a region", "assignment", assignment, "error", err)
		return nil
	}
	event, err := CardEvent(region, position.card)
	if err != nil {
		return err
	}
	return emit(event)
}

// splitChange reads a change's name as <assignment>/<file>. A name with no
// slash is an entry of the state directory itself and reads as an empty
// file; a deeper name is nothing the view follows.
func splitChange(name string) (assignment string, file string, followed bool) {
	assignment, file, _ = strings.Cut(name, "/")
	return assignment, file, !strings.Contains(file, "/")
}

func (view *OpsView) isDirectory(name string) bool {
	info, err := stdfs.Stat(view.files, name)
	return err == nil && info.IsDir()
}
