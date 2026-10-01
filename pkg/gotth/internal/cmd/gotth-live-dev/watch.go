package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// stamp is what this watcher considers "the state of a file".
//
// Modification time and size, and deliberately not a content hash. The tree
// being watched is a Go module under active editing — hashing every .go file
// four times a second is a measurable cost for a distinction that does not
// matter here, because the thing that decides whether the BROWSER reloads is
// the hash of the built executable, not this. A save that changes no bytes
// costs a rebuild that produces an identical binary, and the page correctly
// does not reload. See executableBuildID in live/devreload.go.
type stamp struct {
	mod  time.Time
	size int64
}

// options are the tree-walking rules.
type options struct {
	// exts are the extensions that count as source, with their dots.
	exts []string
	// exclude are directory NAMES — not paths — that are never descended
	// into. Names rather than paths because the directories that must be
	// skipped are the ones that appear at every depth: .git at the root,
	// node_modules wherever npm put it, testdata beside whichever package
	// needed it.
	exclude []string
}

// watchable reports whether one walked entry is source this watcher cares
// about, and whether a directory should be descended into.
//
// It answers both questions because the walk has to ask both at once, and
// because the interesting cases are the ones where the answers differ: a
// directory is never "source" however it is named, and a dotted directory is
// skipped whole while a dotted FILE is perfectly ordinary.
func watchable(name string, dir bool, o options) (descend, source bool) {
	if dir {
		if name == "." || name == "" {
			return true, false
		}
		// Hidden directories are skipped wholesale. .git is the one that
		// matters — a git checkout rewrites hundreds of files under it during
		// an ordinary commit, and every one of them would look like a source
		// change — and the rest of the family (.idea, .cache, .dis) is
		// tooling state for the same reason.
		if strings.HasPrefix(name, ".") {
			return false, false
		}
		for _, skip := range o.exclude {
			if name == skip {
				return false, false
			}
		}
		return true, false
	}
	ext := filepath.Ext(name)
	for _, want := range o.exts {
		if ext == want {
			return false, true
		}
	}
	return false, false
}

// scan walks root and stamps every watchable file.
//
// An unreadable directory is skipped rather than fatal: an editor writing a
// swap file, a build dropping a temporary tree, and a permissions oddity in a
// mounted volume are all things that happen while a developer works, and none
// of them is a reason for the watcher to exit and take the application with
// it.
func scan(root string, o options) (map[string]stamp, error) {
	out := map[string]stamp{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		descend, source := watchable(d.Name(), d.IsDir(), o)
		if d.IsDir() {
			if !descend {
				return fs.SkipDir
			}
			return nil
		}
		if !source {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		out[filepath.ToSlash(rel)] = stamp{mod: info.ModTime(), size: info.Size()}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// changed returns the paths that differ between two scans, sorted.
//
// Added, removed and modified all count, and all three are reported the same
// way. A deleted .templ file is as much a reason to regenerate and rebuild as
// an edited one, and a watcher that only noticed writes is a watcher that goes
// quiet exactly when a developer moves a file.
func changed(prev, next map[string]stamp) []string {
	var out []string
	for path, now := range next {
		was, existed := prev[path]
		if !existed || was != now {
			out = append(out, path)
		}
	}
	for path := range prev {
		if _, still := next[path]; !still {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// needsTempl reports whether any changed path is a templ source.
//
// It is what decides whether `templ generate` runs before `go build`. Running
// it unconditionally would be simpler and is deliberately not done: templ
// rewrites every _templ.go it owns, which changes their modification times,
// which the next scan reads as a change, which regenerates — a watcher that
// rebuilds forever with nobody typing.
func needsTempl(paths []string) bool {
	for _, path := range paths {
		if filepath.Ext(path) == ".templ" {
			return true
		}
	}
	return false
}

// splitList parses a comma-separated flag into a trimmed, non-empty list.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// normalizeExts gives every extension its leading dot, so "-ext go,templ" and
// "-ext .go,.templ" mean the same thing. Both spellings are what people type.
func normalizeExts(list []string) []string {
	out := make([]string, 0, len(list))
	for _, ext := range list {
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		out = append(out, ext)
	}
	return out
}

// abs resolves a directory the way the flags mean it, and fails loudly if it
// does not exist — a watcher pointed at a typo would otherwise sit forever
// reporting no changes, which looks exactly like a watcher that is working.
func abs(dir string) (string, error) {
	path, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		// Not an *os.PathError. That type renders as "watch <path>: invalid
		// argument", which is a true sentence that tells the person who typed
		// -dir nothing they can act on — FR-58's whole complaint.
		return "", fmt.Errorf("%s is a file and this watches a directory: "+
			"point -dir at the module directory holding your application, not at one of its files", path)
	}
	return path, nil
}
