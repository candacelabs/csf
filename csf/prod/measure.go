// Copyright 2026 Candace Labs

package prod

import "strings"

// The parsers below are ports of tools/house_lint/alignment.ml's captured-text
// parsers: pure functions over checker output, so the loop can feed a real
// checker's text into the same measurements the reference makes.

// lines splits captured output on newlines, dropping blank lines.
func lines(text string) []string {
	var result []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result
}

// allDigits reports whether text is one or more decimal digits.
func allDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, character := range text {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// isDiagnostic matches csfc's FILE:ROW:COLUMN: CODE: MESSAGE diagnostic form.
func isDiagnostic(line string) bool {
	parts := strings.Split(line, ":")
	if len(parts) < 5 {
		return false
	}
	return allDigits(parts[1]) && allDigits(parts[2]) && strings.TrimSpace(parts[3]) != ""
}

// diagnosticCode returns the code field of a diagnostic line, empty otherwise.
func diagnosticCode(line string) string {
	parts := strings.Split(line, ":")
	if len(parts) < 5 {
		return ""
	}
	return strings.TrimSpace(parts[3])
}

// Findings counts csfc diagnostics; summaries and malformed lines are not
// findings.
func Findings(text string) int {
	count := 0
	for _, line := range lines(text) {
		if isDiagnostic(line) {
			count++
		}
	}
	return count
}

// Drift counts only the CSF_GENERATED_DRIFT diagnostics.
func Drift(text string) int {
	count := 0
	for _, line := range lines(text) {
		if isDiagnostic(line) && diagnosticCode(line) == "CSF_GENERATED_DRIFT" {
			count++
		}
	}
	return count
}

const documentationPrefix = "generated documentation differs: "

// DocumentationDrift counts the files named in the language generator's drift
// message. The second result reports whether that message was present.
func DocumentationDrift(text string) (count int, present bool) {
	for _, line := range lines(text) {
		index := strings.Index(line, documentationPrefix)
		if index < 0 {
			continue
		}
		total := 0
		for _, path := range strings.Split(line[index+len(documentationPrefix):], ",") {
			if strings.TrimSpace(path) != "" {
				total++
			}
		}
		return total, true
	}
	return 0, false
}

const (
	retiredMarker  = ": retired word '"
	unlinkedMarker = ": unlinked ontology term '"
)

// VocabularyCounts separates retired words from unlinked ontology terms in one
// language-generator lint run.
func VocabularyCounts(text string) (retired, unlinked int) {
	for _, line := range lines(text) {
		if strings.Contains(line, retiredMarker) {
			retired++
		}
		if strings.Contains(line, unlinkedMarker) {
			unlinked++
		}
	}
	return retired, unlinked
}

// LintUnsupported detects a generator that predates the lint verb.
func LintUnsupported(text string) bool {
	return strings.Contains(text, "usage: generate") && !strings.Contains(text, "lint")
}
