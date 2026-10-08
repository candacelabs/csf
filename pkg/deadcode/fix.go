// Copyright 2026 Candace Labs

package deadcode

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"golang.org/x/tools/go/packages"

	"github.com/candacelabs/csf/pkg/atomicfile"
)

const sourceFileMode = 0o644

// Fix removes every removable dead function under patterns until a pass
// removes nothing, and returns what it removed. Removals are tried a
// directory at a time and kept only while that directory's package and its
// tests still type-check; a directory that breaks is retried one function at
// a time, and a function whose removal breaks the build is kept, which is
// how a method an interface needs survives.
func Fix(dir string, patterns ...string) ([]Function, error) {
	var removed []Function
	refused := map[string]bool{}
	for {
		found, err := Find(dir, patterns...)
		if err != nil {
			return removed, err
		}
		byDirectory := map[string][]Function{}
		for _, function := range found {
			if function.Removable() && !function.Protected() && !refused[function.identity()] {
				directory := filepath.Dir(function.Position.Filename)
				byDirectory[directory] = append(byDirectory[directory], function)
			}
		}
		if len(byDirectory) == 0 {
			return removed, nil
		}
		for _, directory := range slices.Sorted(maps.Keys(byDirectory)) {
			kept, failed, err := removeChecked(directory, byDirectory[directory])
			if err != nil {
				return removed, err
			}
			removed = append(removed, kept...)
			for _, function := range failed {
				refused[function.identity()] = true
			}
		}
	}
}

// removeChecked removes functions from the package in directory, all at
// once, and keeps the removal when it still type-checks. Otherwise it
// restores the files and tries each function alone, stopping at the first
// removal that holds: positions after it have moved, so the next pass finds
// the rest afresh. failed are the functions whose removal alone breaks the
// build.
func removeChecked(directory string, functions []Function) (kept []Function, failed []Function, err error) {
	ok, err := tryRemoval(directory, functions)
	if err != nil || ok {
		return functions, nil, err
	}
	if len(functions) == 1 {
		return nil, functions, nil
	}
	for _, function := range functions {
		ok, err := tryRemoval(directory, []Function{function})
		if err != nil {
			return nil, failed, err
		}
		if ok {
			return []Function{function}, failed, nil
		}
		failed = append(failed, function)
	}
	return nil, failed, nil
}

// tryRemoval writes the removal, type-checks the package and its tests, and
// restores the original files when they no longer type-check.
func tryRemoval(directory string, functions []Function) (bool, error) {
	rewritten, err := Remove(functions)
	if err != nil {
		return false, err
	}
	originals := map[string][]byte{}
	for path := range rewritten {
		content, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		originals[path] = content
	}
	if err := writeAll(rewritten); err != nil {
		return false, err
	}
	if typeChecks(directory) {
		return true, nil
	}
	return false, writeAll(originals)
}

func writeAll(files map[string][]byte) error {
	for path, content := range files {
		if err := atomicfile.WriteFile(path, content, sourceFileMode); err != nil {
			return fmt.Errorf("deadcode: write %s: %w", path, err)
		}
	}
	return nil
}

// typeChecks reports whether the package in directory and its tests load
// without an error.
func typeChecks(directory string) bool {
	config := &packages.Config{Dir: directory, Mode: packages.LoadSyntax, Tests: true}
	loaded, err := packages.Load(config, ".")
	if err != nil {
		return false
	}
	failed := false
	packages.Visit(loaded, nil, func(pkg *packages.Package) {
		if len(pkg.Errors) > 0 {
			failed = true
		}
	})
	return !failed
}
