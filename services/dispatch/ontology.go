// Copyright 2026 Candace Labs

package dispatch

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ScopeOntology is the intent scope whose term slots name ontology terms: an
// intent in this scope asks that its terms be defined, so it is already done
// when every term exists in the ontology source.
const ScopeOntology = "ontology"

// termKeyword opens a term definition in architecture.csf.
const termKeyword = "term "

// ontologyTerms reads the term identifiers defined in an architecture.csf.
// The language compiler owns the grammar; this reads only the one line shape
// a term definition always starts with, "term <id> ...".
func ontologyTerms(source string) (map[string]bool, error) {
	file, err := os.Open(source)
	if err != nil {
		return nil, fmt.Errorf("dispatch: read ontology: %w", err)
	}
	defer func() { _ = file.Close() }()
	terms := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		rest, found := strings.CutPrefix(scanner.Text(), termKeyword)
		if !found {
			continue
		}
		if fields := strings.Fields(rest); len(fields) > 0 {
			terms[fields[0]] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("dispatch: read ontology: %w", err)
	}
	return terms, nil
}

// missingTerms lists the terms not defined in the ontology.
func missingTerms(defined map[string]bool, terms []string) []string {
	var missing []string
	for _, term := range terms {
		if !defined[strings.TrimSpace(term)] {
			missing = append(missing, term)
		}
	}
	return missing
}
