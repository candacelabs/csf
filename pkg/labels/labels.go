// Package labels canonicalizes case-insensitive label lists, such as CI
// runner labels, so services compare, deduplicate, and match them with one
// set of semantics.
//
// The canonical form of a label trims surrounding whitespace and lowercases
// the remainder. The canonical form of a list applies that to every label,
// drops labels that become empty, and keeps only the first occurrence of each
// label while preserving order.
package labels

import "strings"

// Canonical returns the canonical form of a single label.
func Canonical(label string) string {
	return strings.ToLower(strings.TrimSpace(label))
}

// Canonicalize returns the canonical form of a label list.
func Canonicalize(list []string) []string {
	result := make([]string, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, label := range list {
		canonical := Canonical(label)
		if canonical == "" {
			continue
		}
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	return result
}

// Set returns the canonical membership set of a label list, dropping labels
// whose canonical form is empty.
func Set(list []string) map[string]struct{} {
	set := make(map[string]struct{}, len(list))
	for _, label := range list {
		canonical := Canonical(label)
		if canonical == "" {
			continue
		}
		set[canonical] = struct{}{}
	}
	return set
}

// ContainsAll reports whether list carries every required label, comparing
// both sides canonically. Required labels whose canonical form is empty are
// ignored, so ContainsAll with no effective requirements is always true.
func ContainsAll(list []string, required ...string) bool {
	set := Set(list)
	for _, label := range required {
		canonical := Canonical(label)
		if canonical == "" {
			continue
		}
		if _, ok := set[canonical]; !ok {
			return false
		}
	}
	return true
}
