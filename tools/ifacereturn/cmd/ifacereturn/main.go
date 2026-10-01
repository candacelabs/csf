// Command ifacereturn reports every function and method result in a Go module
// through which an interface reaches a caller — the result's own type, or an
// interface-typed field of a struct it hands back.
//
// # Running it
//
// In this monorepo, through the CLI:
//
//	candace style ifacereturn
//
// That wraps tools/check-ifacereturn.sh, which knows where the modules are and
// runs this command in the pinned toolchain container. It is the invocation an
// operator here should use; the raw one below exists for the other audience.
//
// It is the flagging lane of house rule CS-8. The blocking lane is a separate
// lexical gate, narrow by design, that fails CI. This one is type-aware and as
// wide as the rule's headline sentence: methods included, stdlib and
// third-party interfaces included, `any` included, `error` excluded. It exits
// 0 with findings printed, because most of what it reports has already been
// ruled correct and a gate whose findings have no fix is one people learn to
// route around.
//
// For consumers of the published candacelabs/csf module, who have no
// private CLI, the portable invocation is a go run from a module root:
//
//	go run github.com/candacelabs/csf/tools/ifacereturn/cmd/ifacereturn ./...
//
// or, for the two-module sweep CI does, build it once and run the binary from
// each module root. `-strict` exits 1 when anything is reported, so the lane
// can be made blocking later without changing the tool; it is not what CI
// passes today.
//
// Exit codes are three, and the third is the point: 0 scanned and reported,
// 1 scanned and reported under -strict, 2 could not scan (a load error, or no
// packages at all). A scanner that cannot read its corpus must not be able to
// print "0 findings" — that is the vacuous pass this repository keeps
// rediscovering.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"golang.org/x/tools/go/packages"

	"github.com/candacelabs/csf/tools/ifacereturn"
)

// loadMode is everything Inspect needs and nothing else: the syntax trees and
// the type information for them, plus names for the error messages.
const loadMode = packages.NeedName |
	packages.NeedFiles |
	packages.NeedCompiledGoFiles |
	packages.NeedSyntax |
	packages.NeedTypes |
	packages.NeedTypesInfo

// report is one finding rendered for output, carrying the sort key separately
// so ordering does not depend on how the string was formatted, and the
// interface's name separately so the summary can tally without re-parsing the
// line it just formatted.
type report struct {
	file          string
	offset        int
	interfaceName string
	line          string
}

func main() {
	strict := flag.Bool("strict", false, "exit 1 when any interface result is reported (the lane flags rather than blocks by default)")
	flag.Parse()

	patterns := flag.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}

	reports, err := scan(patterns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ifacereturn: %v\n", err)
		os.Exit(2)
	}
	for _, entry := range reports {
		fmt.Println(entry.line)
	}
	fmt.Fprintf(os.Stderr, "ifacereturn: %d interface-typed result(s) across %v\n", len(reports), patterns)
	for _, row := range tally(reports) {
		fmt.Fprintf(os.Stderr, "ifacereturn:   %5d  %s\n", row.count, row.name)
	}
	writeStepSummary(reports)
	if *strict && len(reports) > 0 {
		os.Exit(1)
	}
}

// tally counts the findings by interface name, most frequent first, ties
// broken by name so the order is stable across runs. It is what turns a wall
// of several hundred lines into a readable summary without dropping one of
// them: the listing stays complete, the tally says what it is made of.
func tally(reports []report) []interfaceCount {
	counts := make(map[string]int)
	for _, entry := range reports {
		counts[entry.interfaceName]++
	}
	tallied := make([]interfaceCount, 0, len(counts))
	for name, count := range counts {
		tallied = append(tallied, interfaceCount{name: name, count: count})
	}
	sort.Slice(tallied, func(left int, right int) bool {
		if tallied[left].count != tallied[right].count {
			return tallied[left].count > tallied[right].count
		}
		return tallied[left].name < tallied[right].name
	})
	return tallied
}

// interfaceCount is one row of the tally: how many results hand back this
// interface.
type interfaceCount struct {
	name  string
	count int
}

// scan loads the patterns and returns the rendered findings, sorted by file
// and offset so two runs over one tree produce identical output.
func scan(patterns []string) ([]report, error) {
	loaded, err := packages.Load(&packages.Config{Mode: loadMode, Tests: true}, patterns...)
	if err != nil {
		return nil, fmt.Errorf("loading %v: %w", patterns, err)
	}
	if len(loaded) == 0 {
		return nil, fmt.Errorf("%v matched no packages; refusing to report a vacuous 0", patterns)
	}
	var loadErrors int
	packages.Visit(loaded, nil, func(loadedPackage *packages.Package) {
		for _, packageError := range loadedPackage.Errors {
			fmt.Fprintf(os.Stderr, "ifacereturn: %s: %v\n", loadedPackage.PkgPath, packageError)
			loadErrors++
		}
	})
	if loadErrors > 0 {
		return nil, fmt.Errorf("%d package load error(s); the corpus was not fully read", loadErrors)
	}

	// `Tests: true` loads the in-package test variant as well, which repeats
	// every non-test file. Deduplicating on the position is what makes the
	// count a count of code rather than of package variants.
	seen := make(map[string]struct{})
	var collected []report
	for _, loadedPackage := range loaded {
		if loadedPackage.Fset == nil || loadedPackage.TypesInfo == nil {
			continue
		}
		// InspectIn rather than Inspect: the package is what decides whether an
		// unexported field of a returned struct is reachable by the caller, and
		// this command must report exactly what the analyzer does.
		for _, finding := range ifacereturn.InspectIn(
			loadedPackage.Syntax, loadedPackage.TypesInfo, loadedPackage.Types,
		) {
			position := loadedPackage.Fset.Position(finding.Pos)
			line := fmt.Sprintf("%s:%d:%d: %s", position.Filename, position.Line, position.Column, finding.Message())
			if _, repeated := seen[line]; repeated {
				continue
			}
			seen[line] = struct{}{}
			collected = append(collected, report{
				file:          position.Filename,
				offset:        position.Offset,
				interfaceName: finding.Interface,
				line:          line,
			})
		}
	}
	sort.Slice(collected, func(left int, right int) bool {
		if collected[left].file != collected[right].file {
			return collected[left].file < collected[right].file
		}
		return collected[left].offset < collected[right].offset
	})
	return collected, nil
}

// writeStepSummary appends the count and the by-interface tally to the GitHub
// Actions step summary when one is available, so a non-blocking step is still
// loud on the run page. A failure to write is reported and never fatal: the
// summary is a courtesy, the stdout listing is the record.
func writeStepSummary(reports []report) {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ifacereturn: step summary unavailable: %v\n", err)
		return
	}
	defer file.Close()
	summary := fmt.Sprintf("### ifacereturn \u2014 CS-8 flagging lane\n\n**%d** interface-typed result(s). This lane never fails the build.\n\n| Count | Interface |\n|---:|---|\n", len(reports))
	for _, row := range tally(reports) {
		summary += fmt.Sprintf("| %d | `%s` |\n", row.count, row.name)
	}
	if _, err := fmt.Fprint(file, summary); err != nil {
		fmt.Fprintf(os.Stderr, "ifacereturn: step summary not written: %v\n", err)
	}
}
