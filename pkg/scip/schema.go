// Copyright 2026 Candace Labs

package scip

import (
	"fmt"
	"strings"
)

// A Fact is one relation from the fixed schema s0 applied to arguments. The
// span records where the fact came from, so a checker can point at the source
// line that produced a bad fact rather than only at the fact.
type Fact struct {
	Relation string
	Args     []string
	// Span is the document and 0-based line the fact was extracted from. It is
	// empty for facts about the index itself (schema, the language list).
	Span SourceSpan
}

// SourceSpan names the document and 0-based line a fact was extracted from.
type SourceSpan struct {
	Document string
	Line     int
}

// String renders a fact the way the bootstrap archive stores it, one fact per
// line: the relation, then each argument separated by tabs, then the source.
func (f Fact) String() string {
	parts := make([]string, 0, len(f.Args)+2)
	parts = append(parts, f.Relation)
	parts = append(parts, f.Args...)
	if f.Span.Document != "" {
		parts = append(parts, fmt.Sprintf("%s:%d", f.Span.Document, f.Span.Line))
	}
	return strings.Join(parts, "\t")
}

// SchemaS0 is the fixed fact schema the mine stage emits. It is fixed so that
// every stage downstream of the mine stage reads the same relations no matter
// which language a foreign repository is written in: the reorientation stage
// (#538) compares two repositories only through these names.
//
// The relation set is the smallest one that still carries the two things the
// induce stage needs: what the repository declares (its public surface) and
// what it uses. A symbol's suffix comes from the SCIP symbol grammar, not from
// a language-specific kind, so the same relation serves all five languages.
var SchemaS0 = map[string]int{
	"schema":       2, // (name, version): s0, 1
	"language":     1, // the index covers a document written in this language
	"document":     1, // a source document in the index
	"symbol":       4, // (document, symbol, suffix, language)
	"occurrence":   4, // (document, symbol, role, line); role is definition|reference|import
	"relationship": 4, // (document, symbol, relation, target)
}

// SchemaS0Name and SchemaS0Version name the schema the facts carry.
const (
	SchemaS0Name    = "s0"
	SchemaS0Version = "1"
)

// Check verifies that a fact stream conforms to SchemaS0: every relation is one
// the schema declares, every relation has its declared arity, and the stream is
// not empty. It returns the first offending fact. An empty fact stream is a
// finding, not a pass: a gate over zero facts would report clean without
// looking.
func Check(facts []Fact) error {
	if len(facts) == 0 {
		return fmt.Errorf("schema s0: no facts")
	}
	for _, f := range facts {
		arity, ok := SchemaS0[f.Relation]
		if !ok {
			return fmt.Errorf("schema s0: unknown relation %q in %s", f.Relation, f)
		}
		if len(f.Args) != arity {
			return fmt.Errorf("schema s0: relation %s wants %d args, got %d in %s", f.Relation, arity, len(f.Args), f)
		}
		for _, arg := range f.Args {
			if arg == "" {
				return fmt.Errorf("schema s0: relation %s has an empty argument in %s", f.Relation, f)
			}
		}
	}
	return nil
}
