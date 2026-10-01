package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These are table-driven standard-library tests rather than Ginkgo specs, and
// the reason is the module rather than the style. tools/ exists so that a
// minifier's dependencies cannot reach a consumer's go.mod, and readLedger is a
// pure function from markdown text to two pairs of integers — there is no
// behaviour here that reads better as prose than as a table of inputs and
// expected outcomes. Adding Ginkgo and Gomega to this module to describe
// "given a table with three columns, it errors" would buy nothing and would
// widen the graph the module's own doc comment exists to keep narrow.

// ledger renders a §0 counts table with the given rows, wrapped in enough
// surrounding document to be realistic.
func ledger(rows ...string) string {
	return strings.Join(append([]string{
		"# a ledger",
		"",
		"## 0. The rule this document enforces",
		"",
		"**Counts.** This table is the FR-65 baseline.",
		"",
		"| | `live` (exact) | `live/livetest` (ceiling) |",
		"|---|---:|---:|",
	}, rows...), "\n") + "\n\n## 1. Something else\n"
}

const (
	identifierRow = "| Exported identifiers (types, funcs, methods, consts, vars) | **45** | 8 |"
	fieldRow      = "| Exported struct fields | **49** | 6 |"
)

func writeLedger(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api-surface.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing the scratch ledger: %v", err)
	}
	return path
}

func TestReadLedgerAcceptsTheDocumentedShape(t *testing.T) {
	got, err := readLedger(writeLedger(t, ledger(identifierRow, fieldRow)))
	if err != nil {
		t.Fatalf("the documented shape was refused: %v", err)
	}

	want := map[string]counts{
		"live":          {Identifiers: 45, Fields: 49},
		"live/livetest": {Identifiers: 8, Fields: 6},
	}
	for pkg, w := range want {
		if got[pkg] != w {
			t.Errorf("%s: read %+v, want %+v", pkg, got[pkg], w)
		}
	}
}

// The regression this reader was rewritten for.
//
// Every case below was accepted silently by the previous reader, which matched
// two label patterns anywhere in the file and took the first two numeric cells
// of each. A third column, a third row, a second table and a non-numeric cell
// were all invisible to it — so a cell nobody read could hold any number, which
// L9-1 proved by writing 9001 into one and watching CI report that the surface
// matched the ledger.
func TestReadLedgerRefusesWhatItDoesNotRead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		wants   string
	}{
		{
			name: "a derived total column",
			content: strings.Join([]string{
				"| | `live` (exact) | `live/livetest` (ceiling) | total |",
				"|---|---:|---:|---:|",
				"| Exported identifiers (types, funcs, methods, consts, vars) | **45** | 8 | **9001** |",
				"| Exported struct fields | **49** | 6 | **55** |",
			}, "\n"),
			wants: "it has 4 cells and this tool reads 3",
		},
		{
			name:    "a derived total column on the rows alone",
			content: ledger(identifierRow+" **9001** |", fieldRow+" 55 |"),
			wants:   "has 4 cells and this tool reads 3",
		},
		{
			name:    "a derived total row",
			content: ledger(identifierRow, fieldRow, "| **Total incl. fields** | **94** | 14 |"),
			wants:   "has 3 data rows and this tool reads 2",
		},
		{
			name:    "a missing row",
			content: ledger(identifierRow),
			wants:   "has 1 data rows and this tool reads 2",
		},
		{
			name:    "the rows in the wrong order",
			content: ledger(fieldRow, identifierRow),
			wants:   "is labelled",
		},
		{
			name:    "a cell that is not a number",
			content: ledger("| Exported identifiers | forty-five | 8 |", fieldRow),
			wants:   "is not a count",
		},
		{
			name:    "no table at all",
			content: "# a ledger\n\nnothing here counts anything.\n",
			wants:   "no counts table found",
		},
		{
			name:    "two counts tables",
			content: ledger(identifierRow, fieldRow) + ledger(identifierRow, fieldRow),
			wants:   "2 counts tables found",
		},
		{
			name: "a header with no separator under it",
			content: strings.Join([]string{
				"| | `live` (exact) | `live/livetest` (ceiling) |",
				identifierRow,
				fieldRow,
			}, "\n"),
			wants: "is not followed by a markdown separator row",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readLedger(writeLedger(t, tc.content))
			if err == nil {
				t.Fatalf("accepted a ledger it does not read: a cell or row nobody checks can hold any number")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error does not say what is wrong\n got: %v\nwant it to contain: %q", err, tc.wants)
			}
		})
	}
}

// The ledger CI actually reads, parsed by the code that reads it. A shape
// change to §0 that this tool cannot follow is a change that must fail here
// rather than in a CI log a week later.
func TestReadLedgerReadsTheCommittedLedger(t *testing.T) {
	got, err := readLedger(filepath.Join("..", "..", "docs", "api-surface.md"))
	if err != nil {
		t.Fatalf("the committed ledger is unreadable: %v", err)
	}
	if got["live"].Identifiers == 0 || got["live"].Fields == 0 {
		t.Errorf("the committed ledger parsed to zero counts for live: %+v", got["live"])
	}
}

// --- the symbol rows ---------------------------------------------------------
//
// The counts above are one projection of the ledger and the names below are the
// other. They were added after two P3 stages watched this tool report "the
// surface matches the ledger" while every symbol row in it was stale — a rename
// changes no count, so the numbers could not have said otherwise.

// namesLedger renders a document with the two package sections and whatever
// symbol rows each is given, wrapped in enough surrounding text to be realistic.
func namesLedger(liveRows []string, livetestRows []string) string {
	out := []string{"# a ledger", "", "## 0. The rule this document enforces", "",
		"prose that names no package.", "", "## 1. Package `live` — construction", "",
		"| Symbol | Kind | Summary | Status | Req'd by |", "|---|---|---|---|---|"}
	out = append(out, liveRows...)
	out = append(out, "", "some prose between the tables.", "",
		"## 6. Package `live/livetest`", "",
		"| Symbol | Kind | Summary | Status | Req'd by |", "|---|---|---|---|---|")
	out = append(out, livetestRows...)
	return strings.Join(out, "\n") + "\n"
}

const (
	configRow  = "| `Config[S]` | struct | Declares one live application. | stable | FR-14 |"
	handlerRow = "| `(*App[S]).Handler() http.Handler` | method | The live route. | stable | FR-33 |"
	clientRow  = "| `Client` | struct | Drives a real session. | experimental | FR-63 |"
)

func TestReadLedgerNamesReadsTheDocumentedShape(t *testing.T) {
	path := writeLedger(t, namesLedger(
		[]string{configRow, handlerRow,
			"| `AnyOrigin` | const `string` | Sentinel. | stable | FR-45 |"},
		[]string{clientRow,
			"| `(*Client).Send(name string) uint64` | method | Sends one event. | experimental | FR-63 |",
			// The one row in §6 that holds six symbols. Rows are not symbols,
			// which is why this reader takes code spans and not cells.
			"| `FramePatch`, `FrameSnapshot` | consts | The kinds. | experimental | FR-63 |"},
	))

	got, err := readLedgerNames(path)
	if err != nil {
		t.Fatalf("the documented shape was refused: %v", err)
	}
	want := map[string][]string{
		"live":          {"AnyOrigin", "App.Handler", "Config"},
		"live/livetest": {"Client", "Client.Send", "FramePatch", "FrameSnapshot"},
	}
	for pkg, names := range want {
		if diff := strings.Join(sorted(got[pkg]), ","); diff != strings.Join(names, ",") {
			t.Errorf("%s: read %s, want %s", pkg, diff, strings.Join(names, ","))
		}
	}
}

// Each case is a shape whose rows this reader would otherwise pass over, and a
// row nobody reads can name any symbol at all — which is the counts table's
// failure (a cell nobody reads can hold any number) one projection along.
func TestReadLedgerNamesRefusesWhatItDoesNotRead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		wants   string
	}{
		{
			name: "a symbol table under a heading that names no package",
			content: strings.Join([]string{
				"# a ledger", "", "## 7. What was cut, and why", "",
				"| Symbol | Kind | Summary | Status | Req'd by |", "|---|---|---|---|---|",
				configRow,
			}, "\n"),
			wants: "is not under a `## N. Package",
		},
		{
			name: "a symbol header with no separator under it",
			content: strings.Join([]string{
				"# a ledger", "", "## 1. Package `live` — construction", "",
				"| Symbol | Kind | Summary | Status | Req'd by |",
				configRow,
			}, "\n"),
			wants: "is not followed by a markdown separator row",
		},
		{
			name:    "a row whose symbol cell has no code span",
			content: namesLedger([]string{"| Config | struct | no backticks | stable | FR-14 |"}, []string{clientRow}),
			wants:   "names no `code span`",
		},
		{
			name:    "one symbol with two rows",
			content: namesLedger([]string{configRow, configRow}, []string{clientRow}),
			wants:   "twice in package live",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readLedgerNames(writeLedger(t, tc.content))
			if err == nil {
				t.Fatalf("accepted a ledger it does not read: a row nobody checks can name any symbol")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error does not say what is wrong\n got: %v\nwant it to contain: %q", err, tc.wants)
			}
		})
	}
}

// A section that stops matching the pattern is how this reader goes blind
// without anything looking wrong: every table it still finds parses, and a
// whole package's symbols simply stop being compared. Both ways that can
// happen are refused, and they are refused with different messages because
// they are different mistakes.
func TestReadLedgerNamesRefusesALedgerThatHasLostAPackageSection(t *testing.T) {
	whole := namesLedger([]string{configRow}, []string{clientRow})

	t.Run("the heading is renamed and its table is left behind", func(t *testing.T) {
		content := strings.Replace(whole, "## 6. Package `live/livetest`", "## 6. The test helpers", 1)
		_, err := readLedgerNames(writeLedger(t, content))
		if err == nil {
			t.Fatal("accepted a symbol table under a heading naming no package; its rows would go uncompared")
		}
		if !strings.Contains(err.Error(), "is not under a `## N. Package") {
			t.Errorf("error does not name the unattributable table: %v", err)
		}
	})

	t.Run("the section is gone entirely", func(t *testing.T) {
		content := whole[:strings.Index(whole, "## 6. Package")]
		_, err := readLedgerNames(writeLedger(t, content))
		if err == nil {
			t.Fatal("accepted a ledger with no live/livetest section at all")
		}
		if !strings.Contains(err.Error(), "no symbol table found") {
			t.Errorf("error does not name the missing section: %v", err)
		}
	})
}

func TestSymbolNameReducesTheLedgersSpellings(t *testing.T) {
	for _, tc := range []struct{ span, want string }{
		{"Config[S]", "Config"},
		{"New[S](Config[S]) (*App[S], error)", "New"},
		{"(*App[S]).Handler() http.Handler", "App.Handler"},
		{"(Session).ID() ID", "Session.ID"},
		{"(Fields).All(func(k, v string) bool)", "Fields.All"},
		{"(*Client).NextErr(timeout time.Duration) (*Frame, error)", "Client.NextErr"},
		{"AnyOrigin", "AnyOrigin"},
		{"**Document**", "Document"},
		{"ReplayN[S](testing.TB, live.Reducer[S], S, []live.Event, int)", "ReplayN"},
	} {
		got, err := symbolName(tc.span)
		if err != nil {
			t.Errorf("%q: %v", tc.span, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q reduced to %q, want %q", tc.span, got, tc.want)
		}
	}
	if _, err := symbolName("(broken"); err == nil {
		t.Error("accepted a span that begins with neither an identifier nor a receiver")
	}
}

// The committed ledger's own symbol rows, read by the code that reads them.
func TestReadLedgerNamesReadsTheCommittedLedger(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "api-surface.md")
	names, err := readLedgerNames(path)
	if err != nil {
		t.Fatalf("the committed ledger's symbol rows are unreadable: %v", err)
	}
	for _, pkg := range []string{"live", "live/livetest"} {
		if len(names[pkg]) == 0 {
			t.Errorf("%s: the committed ledger parsed to zero symbol names", pkg)
		}
	}
	// live is exact in both directions, so its two hand-maintained statements
	// of the same surface — §0's count and §1-§5's rows — have to agree. They
	// are separately maintained, which is the whole reason to check.
	ledger, err := readLedger(path)
	if err != nil {
		t.Fatalf("the committed ledger's counts are unreadable: %v", err)
	}
	if len(names["live"]) != ledger["live"].Identifiers {
		t.Errorf("live: section 0 says %d exported identifiers; sections 1-5 list %d symbols",
			ledger["live"].Identifiers, len(names["live"]))
	}
}

// The failure this whole projection exists for, as a test rather than as an
// anecdote: the counts are identical and the names are not.
func TestCompareNamesCatchesTheRenameTheCountsCannotSee(t *testing.T) {
	stale := map[string]surface{
		"live":          {Counts: counts{Identifiers: 1}, Names: map[string]bool{"IThing": true}},
		"live/livetest": {Counts: counts{Identifiers: 1}, Names: map[string]bool{"Client": true}},
	}
	ledger := map[string]counts{
		"live":          {Identifiers: 1},
		"live/livetest": {Identifiers: 1},
	}
	ledgered := map[string]map[string]bool{
		"live":          {"Thing": true},
		"live/livetest": {"Client": true},
	}
	if compareNames(stale, ledger, ledgered) {
		t.Error("a ledger row left at its pre-rename spelling was accepted; the counts cannot see it, so nothing would have")
	}

	ledgered["live"] = map[string]bool{"IThing": true}
	if !compareNames(stale, ledger, ledgered) {
		t.Error("a ledger whose rows name exactly the exported symbols was rejected")
	}
}

// live/livetest is a ceiling in names as in counts: a ledgered symbol nobody
// has implemented is the documented state of that package, and a measured
// symbol with no row is not.
func TestCompareNamesTreatsLivetestAsACeiling(t *testing.T) {
	ledger := map[string]counts{"live": {Identifiers: 1}, "live/livetest": {Identifiers: 2}}
	partial := map[string]surface{
		"live":          {Counts: counts{Identifiers: 1}, Names: map[string]bool{"Config": true}},
		"live/livetest": {Counts: counts{Identifiers: 1}, Names: map[string]bool{"Client": true}},
	}
	ledgered := map[string]map[string]bool{
		"live":          {"Config": true},
		"live/livetest": {"Client": true, "Audit": true},
	}
	if !compareNames(partial, ledger, ledgered) {
		t.Error("an unimplemented livetest symbol failed the gate; the ledger describes a target surface")
	}

	partial["live/livetest"].Names["Undocumented"] = true
	if compareNames(partial, ledger, ledgered) {
		t.Error("a livetest symbol with no row was accepted; growth past the ledger is the case that fails")
	}
}
