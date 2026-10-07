// Copyright 2026 Candace Labs

package csf

import (
	"debug/buildinfo"
	"runtime/debug"
	"sync"
)

const (
	// VersionHeader carries the serving binary's version on every operation
	// response, so a client can name both versions when they disagree.
	VersionHeader = "X-CSF-Version"
	// UnknownVersion is the version of a binary built without a VCS stamp,
	// such as one built from a tree that is not a git checkout.
	UnknownVersion = "unknown"

	vcsRevision     = "vcs.revision"
	vcsModified     = "vcs.modified"
	modifiedSuffix  = "-modified"
	revisionLength  = 12
	modifiedSetting = "true"
)

// Version is this binary's version: the commit it was built from, shortened,
// with -modified when the tree had local changes.
var Version = sync.OnceValue(func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return UnknownVersion
	}
	return versionOf(info)
})

// BinaryVersion is the version of the Go binary at path, read from its build
// information without running it.
func BinaryVersion(path string) (string, error) {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return UnknownVersion, err
	}
	return versionOf(info), nil
}

func versionOf(info *debug.BuildInfo) string {
	revision, modified := "", false
	for _, setting := range info.Settings {
		switch setting.Key {
		case vcsRevision:
			revision = setting.Value
		case vcsModified:
			modified = setting.Value == modifiedSetting
		}
	}
	if revision == "" {
		return UnknownVersion
	}
	if len(revision) > revisionLength {
		revision = revision[:revisionLength]
	}
	if modified {
		revision += modifiedSuffix
	}
	return revision
}
