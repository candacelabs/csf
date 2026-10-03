// Copyright 2026 Candace Labs

package dispatch

import (
	"path"
	"strings"

	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
)

// The glob suffixes a path pattern may carry; stripped to find the directory
// a pattern covers.
const (
	globAnyDepth = "/**"
	globOneLevel = "/*"
)

// overlaps reports whether two touch-sets share a hotspot or a path: equal
// patterns, a pattern matching the other, or one under the directory the
// other's glob covers. Two slices whose touch-sets overlap contend.
func overlaps(a *dispatchv1.TouchSet, b *dispatchv1.TouchSet) bool {
	for _, hotspot := range a.GetHotspots() {
		for _, other := range b.GetHotspots() {
			if hotspot == other {
				return true
			}
		}
	}
	for _, pattern := range a.GetPaths() {
		for _, other := range b.GetPaths() {
			if pathsOverlap(pattern, other) {
				return true
			}
		}
	}
	return false
}

// pathsOverlap reports whether two path patterns can name one file.
func pathsOverlap(a string, b string) bool {
	if a == b {
		return true
	}
	if matched, err := path.Match(a, b); err == nil && matched {
		return true
	}
	if matched, err := path.Match(b, a); err == nil && matched {
		return true
	}
	return under(covered(a), b) || under(covered(b), a)
}

// covered is the directory a glob covers, or "" for a pattern that is not a
// directory glob.
func covered(pattern string) string {
	for _, suffix := range []string{globAnyDepth, globOneLevel} {
		if directory, found := strings.CutSuffix(pattern, suffix); found {
			return directory
		}
	}
	return ""
}

// under reports whether target lies in directory.
func under(directory string, target string) bool {
	return directory != "" && (target == directory || strings.HasPrefix(target, directory+"/"))
}

// coverage counts how many of the terms a touch-set covers: a term equal to
// a hotspot, or contained in a path or hotspot, case-insensitively.
func coverage(touch *dispatchv1.TouchSet, terms []string) int {
	count := 0
	for _, term := range terms {
		if covers(touch, term) {
			count++
		}
	}
	return count
}

func covers(touch *dispatchv1.TouchSet, term string) bool {
	needle := strings.ToLower(strings.TrimSpace(term))
	if needle == "" {
		return false
	}
	for _, hotspot := range touch.GetHotspots() {
		if strings.Contains(strings.ToLower(hotspot), needle) {
			return true
		}
	}
	for _, pattern := range touch.GetPaths() {
		if strings.Contains(strings.ToLower(pattern), needle) {
			return true
		}
	}
	return false
}
