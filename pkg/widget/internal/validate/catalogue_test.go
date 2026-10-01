package validate_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The error catalogue is written down three times, and each copy is load-bearing
// on its own: the constants in internal/diag are what the interpreter reports,
// the rows in docs/errors.md are what an author reads and what the message
// obligations are stated against, and the entries of the catalogue table above
// are what asserts a class can actually be produced. Three copies of one list is
// three chances to drift, and the P2 audit found the drift by reading rather
// than by running: W502 is entered twice and W412 is entered not at all.
//
// This is the spec that would have found both. It reads all three copies and
// compares them, and it encodes the one legitimate gap explicitly instead of
// widening the comparison until the gap fits through it.
//
// It reads its own source file to do it. That is deliberate: the table IS the
// third copy, and a spec that asked something other than "what does the table
// say" would be asserting about a proxy. The Bazel target carries all three as
// data for the same reason.

// unspecifiedClasses are the catalogue classes with no entry in the table, and
// the list is exactly one long.
//
// W412 is unreachable in dialect 0. `field <wire_name> writes <stateField>`
// carries no type of its own — the IR takes the state field's — so an event
// field's type and the state field's cannot disagree, and no edit to a fixture
// produces the class. errors.md says so, and the dedicated Describe in
// validate_test asserts the unreachability across all four field types rather
// than leaving the class untested. Anything else appearing here is a class
// nothing checks.
var unspecifiedClasses = []string{"W412"}

var (
	diagConstantPattern  = regexp.MustCompile(`(?m)^\t(Class[A-Za-z]+)\s+Class = "(W\d{3})"$`)
	catalogueRowPattern  = regexp.MustCompile(`(?m)^\| \*\*(W\d{3})\*\* \|`)
	catalogueEntryPrefix = regexp.MustCompile(`(?m)^\tEntry\("(W\d{3})[^"]*",[^\n]*?\bdiag\.(Class[A-Za-z]+),`)
)

var _ = Describe("The error catalogue, in the three places it is written down", func() {
	It("declares the same classes in diag.go and in docs/errors.md", func() {
		constants := declaredClasses()
		rows := documentedClasses()

		Expect(constants).ToNot(BeEmpty(), "no constant matched; the reader has drifted from diag.go")
		Expect(rows).ToNot(BeEmpty(), "no row matched; the reader has drifted from errors.md")
		Expect(sortedKeys(constants)).To(Equal(rows),
			"a class the interpreter can report and the catalogue does not document, or the reverse")
	})

	It("gives every documented class an entry, except the one dialect 0 cannot produce", func() {
		entries := tableEntries()
		Expect(entries).ToNot(BeEmpty(), "no entry matched; the reader has drifted from the table")

		specified := map[string]bool{}
		for _, entry := range entries {
			specified[entry.class] = true
		}
		unspecified := []string{}
		for _, class := range documentedClasses() {
			if !specified[class] {
				unspecified = append(unspecified, class)
			}
		}
		Expect(unspecified).To(Equal(unspecifiedClasses),
			"every class needs an entry that produces it, and the only documented exception is W412")
	})

	It("names each entry after the class the entry asserts", func() {
		constants := declaredClasses()
		for _, entry := range tableEntries() {
			Expect(constants).To(HaveKey(entry.constant),
				"entry %q names diag.%s, which diag.go does not declare", entry.description, entry.constant)
			Expect(constants[entry.constant]).To(Equal(entry.class),
				"entry %q opens with %s but asserts diag.%s, which is %s",
				entry.description, entry.class, entry.constant, constants[entry.constant])
		}
	})

	It("enters no class the catalogue does not document", func() {
		documented := map[string]bool{}
		for _, class := range documentedClasses() {
			documented[class] = true
		}
		for _, entry := range tableEntries() {
			Expect(documented).To(HaveKey(entry.class), "entry %q asserts an undocumented class", entry.description)
		}
	})
})

// declaredClasses maps each constant name in internal/diag to the identifier it
// carries, which is the pairing the third spec above compares against.
func declaredClasses() map[string]string {
	classes := map[string]string{}
	for _, match := range diagConstantPattern.FindAllStringSubmatch(readCatalogueSource(filepath.Join("..", "diag", "diag.go")), -1) {
		classes[match[1]] = match[2]
	}
	return classes
}

// documentedClasses is every identifier with a row in the catalogue, sorted.
// The continuation rows errors.md writes as `| ↑ |` carry no identifier and are
// notes on the row above, so they are not classes and do not match.
func documentedClasses() []string {
	classes := []string{}
	for _, match := range catalogueRowPattern.FindAllStringSubmatch(readCatalogueSource(filepath.Join("..", "..", "docs", "errors.md")), -1) {
		classes = append(classes, match[1])
	}
	sort.Strings(classes)
	return classes
}

// catalogueEntry is one row of the table: the identifier its description opens
// with, the diag constant it asserts, and the description itself for a failure
// message that names which row is wrong.
type catalogueEntry struct {
	class       string
	constant    string
	description string
}

func tableEntries() []catalogueEntry {
	entries := []catalogueEntry{}
	source := readCatalogueSource("validate_test.go")
	for _, match := range catalogueEntryPrefix.FindAllStringSubmatch(source, -1) {
		entries = append(entries, catalogueEntry{
			class:       match[1],
			constant:    match[2],
			description: strings.TrimSpace(match[0]),
		})
	}
	return entries
}

func readCatalogueSource(path string) string {
	content, readError := os.ReadFile(path)
	Expect(readError).ToNot(HaveOccurred(), "the catalogue source %s is not readable from the package directory", path)
	return string(content)
}

func sortedKeys(classes map[string]string) []string {
	identifiers := make([]string, 0, len(classes))
	for _, identifier := range classes {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	return identifiers
}

// numberWords spells the counts the catalogue can plausibly reach, so the spec
// below can compare the sentence errors.md opens with against the rows it
// actually has. A count written in prose is exactly the kind of number that
// stops being true quietly; this is what makes updating it part of adding a
// class rather than something a reader notices two slices later.
var numberWords = map[int]string{
	66: "Sixty-six", 67: "Sixty-seven", 68: "Sixty-eight", 69: "Sixty-nine",
	70: "Seventy", 71: "Seventy-one", 72: "Seventy-two", 73: "Seventy-three",
	74: "Seventy-four", 75: "Seventy-five",
}

var _ = Describe("The count the catalogue states about itself", func() {
	It("is the number of rows it has", func() {
		rows := documentedClasses()
		spelled, spellable := numberWords[len(rows)]
		Expect(spellable).To(BeTrue(), "the catalogue has %d rows and numberWords does not spell that; extend it", len(rows))
		Expect(readCatalogueSource(filepath.Join("..", "..", "docs", "errors.md"))).To(
			ContainSubstring(spelled+" classes."),
			"docs/errors.md opens by stating its own size, and it has %d rows", len(rows))
	})
})
