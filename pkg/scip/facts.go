// Copyright 2026 Candace Labs

package scip

import (
	"fmt"
	"strconv"
)

// DeclaredLanguages is the language set the mine stage covers: the indexers
// that exist for the bootstrap loop, one per language a foreign repository is
// most likely to be written in. A repository whose index reports a language
// outside this set is refused rather than silently half-mined.
var DeclaredLanguages = []string{"go", "typescript", "python", "java", "rust"}

// Facts projects a decoded index into the fixed fact schema s0. languages is
// the declared coverage set: a document whose language is not in it is refused,
// so an index built by an indexer this stage does not know fails loudly instead
// of contributing a partial fact set.
//
// Facts is deterministic: the same index yields the same facts in the same
// order, so two runs over one repository compare equal, which is what the
// reorientation stage relies on.
func Facts(index Index, languages []string) ([]Fact, error) {
	allowed := make(map[string]bool, len(languages))
	for _, language := range languages {
		allowed[language] = true
	}
	facts := []Fact{{Relation: "schema", Args: []string{SchemaS0Name, SchemaS0Version}}}
	emittedLanguage := make(map[string]bool, len(languages))
	for _, document := range index.Documents {
		if document.Language == "" {
			return nil, fmt.Errorf("scip: document %q declares no language", document.RelativePath)
		}
		if !allowed[document.Language] {
			return nil, fmt.Errorf("scip: document %q is %q, outside the declared languages %v", document.RelativePath, document.Language, languages)
		}
		if !emittedLanguage[document.Language] {
			emittedLanguage[document.Language] = true
			facts = append(facts, Fact{Relation: "language", Args: []string{document.Language}})
		}
		facts = append(facts, Fact{
			Relation: "document",
			Args:     []string{document.RelativePath},
			Span:     SourceSpan{Document: document.RelativePath},
		})
		for _, symbol := range document.Symbols {
			facts = append(facts, Fact{
				Relation: "symbol",
				Args:     []string{document.RelativePath, symbol.Symbol, SymbolSuffix(symbol.Symbol), document.Language},
				Span:     SourceSpan{Document: document.RelativePath},
			})
			for _, relationship := range symbol.Relationships {
				for _, relation := range relationshipRelations(relationship) {
					facts = append(facts, Fact{
						Relation: "relationship",
						Args:     []string{document.RelativePath, symbol.Symbol, relation, relationship.Symbol},
						Span:     SourceSpan{Document: document.RelativePath},
					})
				}
			}
		}
		for _, occurrence := range document.Occurrences {
			if occurrence.Symbol == "" {
				continue
			}
			facts = append(facts, Fact{
				Relation: "occurrence",
				Args: []string{
					document.RelativePath,
					occurrence.Symbol,
					occurrenceRole(occurrence),
					strconv.Itoa(int(occurrence.Range.StartLine)),
				},
				Span: SourceSpan{Document: document.RelativePath, Line: int(occurrence.Range.StartLine)},
			})
		}
	}
	return facts, nil
}

// occurrenceRole names an occurrence's role. A definition wins over everything;
// an import is neither a definition nor an ordinary reference.
func occurrenceRole(occurrence Occurrence) string {
	switch {
	case occurrence.HasRole(RoleDefinition):
		return "definition"
	case occurrence.HasRole(RoleImport):
		return "import"
	default:
		return "reference"
	}
}

// relationshipRelations names each relation a relationship asserts, in a fixed
// order so the fact stream is deterministic.
func relationshipRelations(relationship Relationship) []string {
	var relations []string
	if relationship.IsDefinition {
		relations = append(relations, "definition")
	}
	if relationship.IsReference {
		relations = append(relations, "reference")
	}
	if relationship.IsImplementation {
		relations = append(relations, "implementation")
	}
	if relationship.IsTypeDefinition {
		relations = append(relations, "type_definition")
	}
	return relations
}

// SymbolSuffix is the grammar suffix of a symbol's last descriptor, or
// "unspecified" when the symbol does not parse. It derives the symbol's kind
// from the symbol string alone, so it needs no per-language kind table.
func SymbolSuffix(symbol string) string {
	parsed, err := ParseSymbol(symbol)
	if err != nil || len(parsed.Descriptors) == 0 {
		return SuffixUnspecified.String()
	}
	return parsed.Descriptors[len(parsed.Descriptors)-1].Suffix.String()
}
