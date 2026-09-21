// Command apisurface counts the library's exported surface and holds it
// against the ledger.
//
// The requirement is that CI reports the exported-identifier count delta on
// every change, and the failure it exists to catch is an exported identifier
// arriving without a row in docs/api-surface.md. Exported symbols are
// permanent; the ledger is where the argument for each one is written down,
// and a count that nothing checks is a count that drifts.
//
// # What is counted
//
// Types, funcs, methods, consts and vars declared at package level and
// exported, plus the exported fields of exported structs, in the two packages
// the module exports. Interface methods are not counted separately: the ledger
// gives an interface one row and describes its method in that row, so counting
// the method again would report a delta the ledger cannot express.
//
// # What is compared, and why counts were not enough
//
// The counts, and the set of exported NAMES per package.
//
// The names arrived after a measured failure. A count is a projection, and a
// gate's enforcement surface is exactly the projection it compares: the P3
// slice renamed 61 interfaces onto the house prefix — every one of them gained
// a leading I — and then named 374 parameters, and both times this tool printed
//
//	live                56/56       53/53      109/109     (measured/ledger)
//	the surface matches the ledger
//
// because a rename changes no count and a parameter name changes no count. The
// enforced ledger would have passed with every symbol row in it stale, twice,
// on the same day, and nothing would have said so. Both times the rows were
// corrected by hand and by reading, which is the only reason they are right
// (docs/lab/2026-09-02-p3-cs1-rename/entry.md and
// .../2026-09-02-p3-cs2-names/entry.md, and lessons.md "A gate that reads
// counts cannot see a spelling").
//
// Names are a projection this ledger already carries — §1 through §6 list
// every symbol by name in their own tables — so comparing them adds no field
// to §0 and derives nothing. That matters, because §0's rule is that it holds
// measurements and nothing derived, and the fix for a blind gate must not be a
// stored copy of a computed number. This reader takes the names the document
// already states and holds them against the source.
//
// Names, not signatures. A parameter name is inside a signature the ledger
// writes out in full, and comparing those would mean parsing Go out of a
// markdown cell and re-deciding what counts as the same type — a much larger
// promise, and one whose failures would be about formatting. So CS-2's half of
// the P3 blindness is still on a human, and this doc comment is where that is
// admitted rather than in nothing.
//
// A method is named the way the ledger spells it: the receiver type without its
// pointer or type arguments, a dot, the method. `(*App[S]).Handler()` is
// App.Handler on both sides.
//
// live is compared in both directions, as its counts are: a measured name with
// no row is an identifier nobody argued for, and a ledgered name that no longer
// exists is exactly the stale row a rename leaves behind. live/livetest is a
// ceiling in names as in counts — its ledger describes a v0.1 target surface
// whose unimplemented symbols are meant to be listed and not yet measured — so
// only growth past the ledger fails there.
//
// # Why the two packages are checked differently
//
// live is complete, so its measured counts must equal the ledger's exactly and
// any difference in either direction fails.
//
// live/livetest is deliberately partial — the ledger describes the v0.1 target
// surface and most of its symbols wait on the benchmark harness that fixes
// their shape. Requiring equality there would mean either a red build for a
// year or a ledger that lies about the target. So growth is what fails:
// measured may be below the ledger and may never exceed it, because exceeding
// it is exactly the "identifier without a row" case.
//
// # Why the ledger's table is read whole
//
// This tool refuses a counts table containing a cell or a row it does not read,
// rather than skipping it. The first version matched two label patterns
// anywhere in the file and consumed the first two numeric cells of each, so the
// table's shape was never its business: a third column could hold any number
// and the tool still reported that the surface matched the ledger. That is the
// hand-maintained-number failure this program exists to end, reproduced inside
// the program. Reading the table whole means the only way to add a cell is to
// decide what reads it.
//
// Usage:
//
//	go run ./apisurface            # report the counts and check them
//	go run ./apisurface -report    # report only, exit 0
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// counts is one package's measured or ledgered surface.
type counts struct {
	Identifiers int
	Fields      int
}

func (c counts) total() int { return c.Identifiers + c.Fields }

// surface is what one walk of a package yields: the counts §0 holds, and the
// exported names §1 through §6 hold.
//
// One walk produces both, deliberately. Two walks would be two answers to
// "what is exported here", and the first thing anyone would do with a
// disagreement is pick the one that made the build green.
type surface struct {
	Counts counts
	Names  map[string]bool
}

// sorted returns the names in a stable order, for a message a reader can diff.
func sorted(names map[string]bool) []string {
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// missingFrom returns the names in want that are absent from have.
func missingFrom(want map[string]bool, have map[string]bool) []string {
	var out []string
	for _, name := range sorted(want) {
		if !have[name] {
			out = append(out, name)
		}
	}
	return out
}

func main() {
	reportOnly := flag.Bool("report", false, "print the counts and exit 0")
	root := flag.String("root", "..", "path to the gotth-live module root")
	flag.Parse()

	ledgerPath := filepath.Join(*root, "docs", "api-surface.md")
	ledger, err := readLedger(ledgerPath)
	if err != nil {
		fail("reading the ledger: %v", err)
	}
	ledgerNames, err := readLedgerNames(ledgerPath)
	if err != nil {
		fail("reading the ledger's symbol rows: %v", err)
	}

	measured := map[string]surface{}
	for _, pkg := range []string{"live", "live/livetest"} {
		s, err := measure(filepath.Join(*root, filepath.FromSlash(pkg)))
		if err != nil {
			fail("measuring %s: %v", pkg, err)
		}
		measured[pkg] = s
	}

	fmt.Println("exported surface (FR-65)")
	fmt.Printf("  %-16s %11s %11s %11s\n", "package", "identifiers", "fields", "total")
	var ok = true
	for _, pkg := range []string{"live", "live/livetest"} {
		got, want := measured[pkg].Counts, ledger[pkg]
		fmt.Printf("  %-16s %5d/%-5d %5d/%-5d %5d/%-5d   (measured/ledger)\n",
			pkg, got.Identifiers, want.Identifiers, got.Fields, want.Fields, got.total(), want.total())

		switch pkg {
		case "live":
			if got != want {
				ok = false
				fmt.Fprintf(os.Stderr,
					"\n%s: the exported surface does not match docs/api-surface.md.\n"+
						"  measured %d identifiers and %d struct fields; the ledger says %d and %d.\n"+
						"  Every exported identifier needs a row and a requirement it satisfies.\n"+
						"  Update the ledger in the same commit, or drop the export.\n",
					pkg, got.Identifiers, got.Fields, want.Identifiers, want.Fields)
			}
		default:
			if got.Identifiers > want.Identifiers || got.Fields > want.Fields {
				ok = false
				fmt.Fprintf(os.Stderr,
					"\n%s: the exported surface has grown past the ledger's target.\n"+
						"  measured %d identifiers and %d struct fields; the ledger's target is %d and %d.\n",
					pkg, got.Identifiers, got.Fields, want.Identifiers, want.Fields)
			}
		}
	}

	total := counts{}
	for _, s := range measured {
		total.Identifiers += s.Counts.Identifiers
		total.Fields += s.Counts.Fields
	}
	fmt.Printf("  %-16s %5d       %5d       %5d\n", "total", total.Identifiers, total.Fields, total.total())

	if !compareNames(measured, ledger, ledgerNames) {
		ok = false
	}

	if *reportOnly {
		return
	}
	if !ok {
		os.Exit(1)
	}
	fmt.Println("  the surface matches the ledger")
}

// compareNames holds each package's exported names against the rows that
// describe them. It reports every difference rather than the first, and returns
// whether the ledger still describes the source.
func compareNames(measured map[string]surface, ledger map[string]counts, ledgered map[string]map[string]bool) bool {
	fmt.Println("exported names (sections 1-6)")
	fmt.Printf("  %-16s %11s %11s\n", "package", "measured", "ledgered")
	ok := true
	for _, pkg := range []string{"live", "live/livetest"} {
		got, want := measured[pkg].Names, ledgered[pkg]
		fmt.Printf("  %-16s %11d %11d\n", pkg, len(got), len(want))

		// A name in the source with no row is the FR-65 case the counts already
		// catch when the count moves — and do not catch when a rename keeps it
		// still. It fails for both packages, because live/livetest's ceiling is
		// about symbols the ledger describes and the source does not have yet,
		// never the other way round.
		if unrowed := missingFrom(got, want); len(unrowed) > 0 {
			ok = false
			fmt.Fprintf(os.Stderr,
				"\n%s: %d exported name(s) have no row in docs/api-surface.md:\n    %s\n"+
					"  Every exported identifier needs a row and a requirement it satisfies.\n"+
					"  If one of these is a rename, the row it renamed away from is in the list below.\n",
				pkg, len(unrowed), strings.Join(unrowed, "\n    "))
		}

		// A row for a name the source does not have. For live this is the stale
		// row a rename leaves behind — the exact failure two P3 stages walked
		// past while the counts said the surface matched. For live/livetest it
		// is the documented state of a partial package: the ledger describes a
		// v0.1 target and Audit and Report are in it and unimplemented, so it is
		// reported and does not fail.
		unbuilt := missingFrom(want, got)
		if len(unbuilt) == 0 {
			continue
		}
		if pkg == "live" {
			ok = false
			fmt.Fprintf(os.Stderr,
				"\n%s: docs/api-surface.md has %d row(s) for names that are not exported:\n    %s\n"+
					"  A rename changes no count, so a stale symbol row is invisible to the numbers\n"+
					"  above. Update the row in the same commit, and record the rename in section 10.\n",
				pkg, len(unbuilt), strings.Join(unbuilt, "\n    "))
			continue
		}
		fmt.Printf("  %-16s %d ledgered name(s) not implemented yet: %s\n",
			"", len(unbuilt), strings.Join(unbuilt, ", "))

		// Said out loud rather than reconciled: the ceiling row in section 0 and
		// the rows in section 6 are two hand-maintained statements of one target,
		// and this is the first thing that has ever compared them. Moving the
		// enforced baseline is the ledger owner's call and needs a section 10
		// entry, so this reports the disagreement and does not fail on it.
		if len(want) != ledger[pkg].Identifiers {
			fmt.Printf("  %-16s note: section 6 lists %d symbols; the section 0 ceiling says %d\n",
				"", len(want), ledger[pkg].Identifiers)
		}
	}
	if ok {
		fmt.Println("  every exported name has its row, and every row its name")
	}
	return ok
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "apisurface: "+format+"\n", args...)
	os.Exit(2)
}

// measure reads one package's exported surface from its source: the counts and
// the names, from one walk.
//
// It parses rather than builds, so it needs no toolchain beyond the parser and
// works on a package that does not compile — which matters, because the most
// likely moment to run this is while a change is in progress.
func measure(dir string) (surface, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return surface{}, err
	}

	measured := surface{Names: map[string]bool{}}
	add := func(name string) error {
		if measured.Names[name] {
			// Go forbids two package-level declarations of one name and two
			// methods of one name on one type, so this cannot happen against a
			// package that compiles. It is checked because the alternative is a
			// count and a name set that disagree by one and no line saying so.
			return fmt.Errorf("%s: the exported name %q is declared twice", dir, name)
		}
		measured.Names[name] = true
		measured.Counts.Identifiers++
		return nil
	}

	for name, pkg := range pkgs {
		if strings.HasSuffix(name, "_test") {
			continue
		}
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if symbol, isSurface := funcName(d); isSurface {
						if err := add(symbol); err != nil {
							return surface{}, err
						}
					}
				case *ast.GenDecl:
					symbols, fields := genNames(d)
					for _, symbol := range symbols {
						if err := add(symbol); err != nil {
							return surface{}, err
						}
					}
					measured.Counts.Fields += fields
				}
			}
		}
	}
	return measured, nil
}

// funcName returns the ledger's spelling of an exported function or method, and
// whether it is surface at all.
//
// A method is `Type.Method`, with the receiver's pointer and type arguments
// dropped, because that is what the ledger's `(*App[S]).Handler()` reduces to
// and both sides have to reduce the same way.
func funcName(d *ast.FuncDecl) (string, bool) {
	if !d.Name.IsExported() {
		return "", false
	}
	if d.Recv == nil {
		return d.Name.Name, true
	}
	// A method on an unexported type is not reachable by a consumer, so it is
	// not surface.
	receiver, exported := receiverName(d.Recv)
	if !exported {
		return "", false
	}
	return receiver + "." + d.Name.Name, true
}

func receiverName(recv *ast.FieldList) (string, bool) {
	if len(recv.List) == 0 {
		return "", false
	}
	expr := recv.List[0].Type
	if star, isPointer := expr.(*ast.StarExpr); isPointer {
		expr = star.X
	}
	// A generic receiver is written App[S], so the name is under the index.
	if index, generic := expr.(*ast.IndexExpr); generic {
		expr = index.X
	}
	if index, generic := expr.(*ast.IndexListExpr); generic {
		expr = index.X
	}
	ident, named := expr.(*ast.Ident)
	if !named || !ident.IsExported() {
		return "", false
	}
	return ident.Name, true
}

func genNames(d *ast.GenDecl) (identifiers []string, fields int) {
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			identifiers = append(identifiers, s.Name.Name)
			if st, isStruct := s.Type.(*ast.StructType); isStruct {
				fields += countFields(st)
			}
		case *ast.ValueSpec:
			for _, name := range s.Names {
				if name.IsExported() {
					identifiers = append(identifiers, name.Name)
				}
			}
		}
	}
	return identifiers, fields
}

func countFields(st *ast.StructType) int {
	n := 0
	for _, field := range st.Fields.List {
		// An embedded field has no names; the module has none in its exported
		// structs, and counting one would need a rule the ledger does not have.
		for _, name := range field.Names {
			if name.IsExported() {
				n++
			}
		}
	}
	return n
}

// countsHeader anchors the counts table in the ledger's §0.
//
// The table is located by its header rather than by a row pattern, and that is
// deliberate. The previous reader matched two label patterns anywhere in the
// file and took the first two numeric cells of each, which meant it consumed a
// prefix of a table whose shape it never checked: a third column could hold any
// number at all and the tool reported that the surface matched the ledger.
// L9-1 demonstrated it by writing 9001 into that column and watching CI pass —
// the precise failure this tool was written to end, reproduced inside it.
//
// Anchoring on the header makes the whole table this reader's business. It then
// refuses anything it does not read, so a cell nobody checks cannot exist.
// The anchor is deliberately loose — it finds the table, and the checks below
// decide whether its shape is one this tool reads. A strict anchor would report
// an added column as "no counts table found", which sends the reader looking
// for the wrong mistake.
var countsHeader = regexp.MustCompile("`live` +\\(exact\\).*`live/livetest` +\\(ceiling\\)")

// countsColumns are the header's cells, after the empty label cell.
var countsColumns = []string{"`live` (exact)", "`live/livetest` (ceiling)"}

// countsLabels are the two data rows, in the order the table states them.
// The label carries the package-independent meaning of the row; the two columns
// carry the packages.
var countsLabels = []string{"Exported identifiers", "Exported struct fields"}

// tableSeparator is the markdown alignment row under a header.
var tableSeparator = regexp.MustCompile(`^\|[\s:|-]+\|$`)

func readLedger(path string) (map[string]counts, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")

	// Exactly one counts table. A second one — a worked example, a proposal in
	// a changelog entry — would leave the reader silently picking one of them.
	var headers []int
	for i, line := range lines {
		if countsHeader.MatchString(strings.TrimRight(line, " \t\r")) {
			headers = append(headers, i)
		}
	}
	switch len(headers) {
	case 1:
	case 0:
		return nil, fmt.Errorf(
			"%s: no counts table found.\n"+
				"  This tool anchors on the §0 header row:\n"+
				"      | | `live` (exact) | `live/livetest` (ceiling) |\n"+
				"  That table is the FR-65 baseline, so a change to its shape is a change here too.",
			path)
	default:
		return nil, fmt.Errorf(
			"%s: %d counts tables found, at lines %v. There must be exactly one: "+
				"a second copy of the baseline is a second answer to the question CI asks",
			path, len(headers), plusOne(headers))
	}

	if err := checkHeader(lines[headers[0]]); err != nil {
		return nil, fmt.Errorf("%s: the counts table header at line %d: %w", path, headers[0]+1, err)
	}

	body, err := tableBody(lines, headers[0])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(body) != len(countsLabels) {
		return nil, fmt.Errorf(
			"%s: the counts table has %d data rows and this tool reads %d.\n"+
				"  Rows read: %s.\n"+
				"  A row nobody reads can hold any number at all, which is the failure this check exists to prevent.",
			path, len(body), len(countsLabels), strings.Join(countsLabels, ", "))
	}

	out := map[string]counts{}
	for i, row := range body {
		cells, err := splitRow(row)
		if err != nil {
			return nil, fmt.Errorf("%s: counts row %d: %w", path, i+1, err)
		}
		if len(cells) != 3 {
			return nil, fmt.Errorf(
				"%s: counts row %d has %d cells and this tool reads 3 "+
					"(label, `live`, `live/livetest`).\n"+
					"  Row: %s\n"+
					"  A derived column is a stored copy of a computed number; the tool prints the totals.",
				path, i+1, len(cells), strings.TrimSpace(row))
		}

		label := strings.TrimSpace(strings.Trim(cells[0], "* "))
		if !strings.HasPrefix(label, countsLabels[i]) {
			return nil, fmt.Errorf(
				"%s: counts row %d is labelled %q; this tool reads the rows in the order %s",
				path, i+1, label, strings.Join(countsLabels, ", then "))
		}

		live, err := cell(cells[1])
		if err != nil {
			return nil, fmt.Errorf("%s: counts row %d, the live column: %w", path, i+1, err)
		}
		livetest, err := cell(cells[2])
		if err != nil {
			return nil, fmt.Errorf("%s: counts row %d, the live/livetest column: %w", path, i+1, err)
		}

		if i == 0 {
			out["live"] = counts{Identifiers: live}
			out["live/livetest"] = counts{Identifiers: livetest}
			continue
		}
		out["live"] = counts{Identifiers: out["live"].Identifiers, Fields: live}
		out["live/livetest"] = counts{Identifiers: out["live/livetest"].Identifiers, Fields: livetest}
	}
	return out, nil
}

// packageHeading anchors a symbol section to the package it describes: the
// ledger's §1 through §6 are each "N. Package `live`" or "N. Package
// `live/livetest`", and a `###` subsection under one of them (§5.1, §5.2)
// belongs to whichever `##` it sits below.
var packageHeading = regexp.MustCompile("^## .*Package `(live(?:/livetest)?)`")

// symbolHeader is the header row of a symbol table. The field tables — §1.1's
// `| Field | Type | …`, §2.1's `| Struct | Field | …`, §5.1's `| Field |
// Default | …` — are deliberately not this shape and are not read here.
//
// That is a real limit and it is stated rather than implied: this tool compares
// exported *identifier* names and not exported field names. It cannot compare
// field names, because the ledger does not list all of them in tables — `Bind`'s
// six live in a §5.2 summary sentence and `Frame`'s in a §6 one — so a
// field-name comparison would be a comparison against a projection this
// document does not carry, which is the thing §0 forbids. The field COUNTS are
// still compared, exactly as they were.
var symbolHeader = regexp.MustCompile(`^\|\s*Symbol\s*\|\s*Kind\s*\|`)

// methodSymbol matches the ledger's spelling of a method: `(*App[S]).Handler(…)`
// or `(Session).ID() ID`. The receiver's pointer and type arguments are dropped
// so that both sides of the comparison reduce a method to `Type.Method`.
var methodSymbol = regexp.MustCompile(`^\(\*?([A-Za-z_][A-Za-z0-9_]*)(?:\[[^\]]*\])?\)\.([A-Za-z_][A-Za-z0-9_]*)`)

// plainSymbol matches everything else — `Config[S]`, `New[S](Config[S]) (…)`,
// `AnyOrigin` — by taking the identifier the cell opens with.
var plainSymbol = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)`)

// backticked pulls the code spans out of a Symbol cell. A cell usually holds
// one; §6's `FramePatch`, `FrameSnapshot`, … row holds six, which is why the
// number of rows is not the number of symbols and why this reads spans rather
// than cells.
var backticked = regexp.MustCompile("`([^`]+)`")

// readLedgerNames returns the exported names each package's sections declare.
//
// It reads the file a second time rather than sharing readLedger's pass, and
// that is deliberate: the two readers are strict about different tables for
// different reasons — one about a cell that could hold any number, one about a
// row that could name any symbol — and folding them together would produce one
// function whose failure messages have to explain which half of the document
// they are about.
//
// Its own refusals mirror readLedger's. A symbol table outside a package
// section is an error rather than something to skip, because a table this
// reader passes over is a table whose rows could say anything; and a package
// section with no symbol table at all is an error too, since that is what a
// section renamed out of the pattern looks like from in here.
func readLedgerNames(path string) (map[string]map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")

	names := map[string]map[string]bool{}
	tables := map[string]int{}
	current := ""
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t\r")
		if strings.HasPrefix(line, "## ") {
			current = ""
			if match := packageHeading.FindStringSubmatch(line); match != nil {
				current = match[1]
			}
		}
		if !symbolHeader.MatchString(strings.TrimSpace(line)) {
			continue
		}
		if current == "" {
			return nil, fmt.Errorf(
				"%s: the symbol table at line %d is not under a `## N. Package …` heading.\n"+
					"  Every symbol row belongs to one of the two exported packages, and a table this\n"+
					"  reader cannot attribute is a table whose rows nothing compares.",
				path, i+1)
		}
		if i+1 >= len(lines) || !tableSeparator.MatchString(strings.TrimSpace(lines[i+1])) {
			return nil, fmt.Errorf(
				"%s: the symbol table header at line %d is not followed by a markdown separator row",
				path, i+1)
		}
		tables[current]++
		if names[current] == nil {
			names[current] = map[string]bool{}
		}
		for row := i + 2; row < len(lines); row++ {
			body := strings.TrimSpace(lines[row])
			if !strings.HasPrefix(body, "|") {
				i = row - 1
				break
			}
			i = row
			cells, err := splitRow(body)
			if err != nil {
				return nil, fmt.Errorf("%s: symbol row %d: %w", path, row+1, err)
			}
			spans := backticked.FindAllStringSubmatch(cells[0], -1)
			if len(spans) == 0 {
				return nil, fmt.Errorf(
					"%s: the symbol row at line %d names no `code span`: %s\n"+
						"  A row whose symbol this reader cannot find is a row that cannot go stale visibly.",
					path, row+1, strings.TrimSpace(cells[0]))
			}
			for _, span := range spans {
				symbol, err := symbolName(span[1])
				if err != nil {
					return nil, fmt.Errorf("%s: the symbol row at line %d: %w", path, row+1, err)
				}
				if names[current][symbol] {
					return nil, fmt.Errorf(
						"%s: line %d names %s twice in package %s. Two rows for one symbol are two\n"+
							"  places to update and one of them will be missed.",
						path, row+1, symbol, current)
				}
				names[current][symbol] = true
			}
		}
	}

	for _, pkg := range []string{"live", "live/livetest"} {
		if tables[pkg] == 0 {
			return nil, fmt.Errorf(
				"%s: no symbol table found under any \"## N. Package %s\" heading.\n"+
					"  Sections 1 through 6 are where the names live; a section this reader stops\n"+
					"  finding is a section whose symbols nothing holds against the source.",
				path, pkg)
		}
	}
	return names, nil
}

// symbolName reduces one ledger code span to the name this tool compares.
func symbolName(span string) (string, error) {
	text := strings.TrimSpace(strings.Trim(strings.TrimSpace(span), "*"))
	if match := methodSymbol.FindStringSubmatch(text); match != nil {
		return match[1] + "." + match[2], nil
	}
	if match := plainSymbol.FindStringSubmatch(text); match != nil {
		return match[1], nil
	}
	return "", fmt.Errorf("%q does not begin with a Go identifier or a `(Receiver).Method`", span)
}

// checkHeader holds the table to the two columns this tool reads.
//
// A third column is the specific mistake worth naming, because it is the one
// that was made: a derived total sat there, unread, for months. The message
// therefore says what to do about it rather than only that something is wrong.
func checkHeader(line string) error {
	cells, err := splitRow(line)
	if err != nil {
		return err
	}
	if len(cells) != len(countsColumns)+1 {
		return fmt.Errorf(
			"it has %d cells and this tool reads %d (an empty label cell, then %s).\n"+
				"  Header: %s\n"+
				"  A column this tool does not read can hold any number at all. If the column is derived, "+
				"delete it — the tool prints every total, including the module's true measured surface.",
			len(cells), len(countsColumns)+1, strings.Join(countsColumns, " and "), strings.TrimSpace(line))
	}
	for i, want := range countsColumns {
		if got := strings.TrimSpace(cells[i+1]); got != want {
			return fmt.Errorf("column %d is %q, and this tool reads %q", i+1, got, want)
		}
	}
	return nil
}

// tableBody returns the data rows of the table whose header is at headerLine.
func tableBody(lines []string, headerLine int) ([]string, error) {
	if headerLine+1 >= len(lines) || !tableSeparator.MatchString(strings.TrimSpace(lines[headerLine+1])) {
		return nil, fmt.Errorf("the counts header at line %d is not followed by a markdown separator row", headerLine+1)
	}
	var body []string
	for i := headerLine + 2; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "|") {
			break
		}
		body = append(body, line)
	}
	return body, nil
}

// splitRow returns a markdown row's cells, without the delimiting pipes.
func splitRow(row string) ([]string, error) {
	row = strings.TrimSpace(row)
	if !strings.HasPrefix(row, "|") || !strings.HasSuffix(row, "|") {
		return nil, fmt.Errorf("%q is not a delimited markdown row", row)
	}
	return strings.Split(strings.Trim(row, "|"), "|"), nil
}

// cell reads one count, tolerating the bold markers the table uses for emphasis
// and nothing else. A cell that is not a number is an error rather than a zero:
// Atoi's zero on failure is how a typo used to become a silently wrong baseline.
func cell(s string) (int, error) {
	text := strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "*"))
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0, fmt.Errorf("%q is not a count", strings.TrimSpace(s))
	}
	return n, nil
}

func plusOne(lines []int) []int {
	out := make([]int, len(lines))
	for i, n := range lines {
		out[i] = n + 1
	}
	return out
}
