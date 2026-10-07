// Copyright 2026 Candace Labs

package opsview

import (
	"encoding/json"
	"errors"
	stdfs "io/fs"
)

// readSnapshot reads and decodes one panel's snapshot file from the state
// directory. A file that does not exist yet is a nil snapshot, which renders
// the empty panel; readable is false, with the cause logged under noun, when
// the file cannot be read or decoded and the panel is left as it was.
func readSnapshot[Snapshot any](view *OpsView, file string, noun string) (snapshot *Snapshot, readable bool) {
	content, err := view.files.ReadFile(file)
	switch {
	case errors.Is(err, stdfs.ErrNotExist):
		return nil, true
	case err != nil:
		view.logger.Warn("ops view: "+noun+" not read", "file", file, "error", err)
		return nil, false
	}
	snapshot = new(Snapshot)
	if err := json.Unmarshal(content, snapshot); err != nil {
		view.logger.Warn("ops view: "+noun+" not decoded", "file", file, "error", err)
		return nil, false
	}
	return snapshot, true
}
