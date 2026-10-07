package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
	"github.com/candacelabs/csf/pkg/widget/internal/ir"
	"github.com/candacelabs/csf/pkg/widget/internal/lex"
)

// symbolKind names what an identifier was declared as. The value is the phrase
// a message uses, because every reference diagnostic has to say both what a
// name is and what was required in its place.
type symbolKind string

const (
	kindStateField symbolKind = "state field"
	kindPredicate  symbolKind = "predicate"
	kindBinding    symbolKind = "binding"
	kindLabel      symbolKind = "label"
	kindRole       symbolKind = "role"
	kindChannel    symbolKind = "channel"
	kindPlacement  symbolKind = "placement"
	kindScene      symbolKind = "scene"
	kindOrbit      symbolKind = "orbit"
	kindNode       symbolKind = "node"
	kindEdge       symbolKind = "edge"
	kindPulse      symbolKind = "pulse"
	kindEmphasis   symbolKind = "emphasis"
	kindIndicator  symbolKind = "indicator"
	kindControl    symbolKind = "control"
	kindEvent      symbolKind = "event"
	kindStream     symbolKind = "stream"
)

// declarationForm names where a declaration of each kind goes and how it is
// spelled, so an "is not declared" message can hand the author the line to
// write rather than a rule to look up.
type declarationForm struct {
	block string
	form  string
}

var declarationForms = map[symbolKind]declarationForm{
	kindStateField: {"state", "field <id> type <flag|counter|count|text>"},
	kindPredicate:  {"predicates", "predicate <id>"},
	kindBinding:    {"bindings", "binding <id>"},
	kindLabel:      {"labels", "label <id> text \"<text>\""},
	kindRole:       {"roles", "role <id>"},
	kindChannel:    {"channels", "channel <id>"},
	kindPlacement:  {"placements", "placement <id> left <n> top <n>"},
	kindScene:      {"scene", "scene <id>"},
	kindOrbit:      {"scene", "orbit <id> token <token>"},
	kindNode:       {"scene", "node <id>"},
	kindEdge:       {"scene", "edge <id>"},
	kindPulse:      {"motion", "pulse <id>"},
	kindEmphasis:   {"motion", "emphasis <id>"},
	kindIndicator:  {"indicators", "indicator <id>"},
	kindControl:    {"controls", "control <id>"},
	kindEvent:      {"events", "event <id>"},
	kindStream:     {"data", "stream <id>"},
}

// symbol is one declared identifier. Record holds the IR record the name
// resolves to, once the builder has made it.
type symbol struct {
	name string
	kind symbolKind
	// at anchors the identifier itself, which is where a duplicate and a
	// reference finding point (A2, A4).
	at diag.SourcePosition
	// lineAt anchors the declaration's first token, which is where a
	// whole-document invariant about the declaration points (A3).
	lineAt diag.SourcePosition
	record any
}

type symbolTable struct {
	byName map[string]*symbol
	order  []*symbol
}

func newSymbolTable() *symbolTable {
	return &symbolTable{byName: map[string]*symbol{}}
}

// declare registers a name and returns nothing. When the name is already taken
// it returns the first declaration instead, which is where W301 anchors its
// secondary location and what tells the caller the name was not registered.
func (table *symbolTable) declare(name string, kind symbolKind, at diag.SourcePosition, lineAt diag.SourcePosition) *symbol {
	if existing, taken := table.byName[name]; taken {
		return existing
	}
	table.byName[name] = &symbol{name: name, kind: kind, at: at, lineAt: lineAt}
	table.order = append(table.order, table.byName[name])
	return nil
}

func (table *symbolTable) lookup(name string) (*symbol, bool) {
	found, declared := table.byName[name]
	return found, declared
}

// namesOfKind lists the declared names of one kind, sorted, for a message that
// enumerates the alternatives.
func (table *symbolTable) namesOfKind(kind symbolKind) []string {
	names := make([]string, 0, len(table.order))
	for _, declared := range table.order {
		if declared.kind == kind {
			names = append(names, declared.name)
		}
	}
	sort.Strings(names)
	return names
}

// referenceOptions carry the two exemptions to the one-pass resolution order.
type referenceOptions struct {
	// allowForward exempts a reference the canonical block order requires to
	// point forwards: a control naming an event, and a predicate naming a
	// predicate declared later in its own block, whose cycles W408 owns.
	allowForward bool
	// secondary marks a reference that does not satisfy the declaration's own
	// use invariant. A pulse names a channel, but only an edge carries one, so a
	// channel every pulse names and no edge carries is still an error.
	secondary bool
}

// resolve looks a reference up and reports the reference findings: not
// declared, declared later, or declared as something else. It returns the
// symbol only when the reference is sound, so a caller can skip the invariants
// that would otherwise report a consequence of this finding as a finding of
// their own.
func (documentValidator *validator) resolve(token lex.Token, required symbolKind, options referenceOptions) (*symbol, bool) {
	name := token.Text
	found, declared := documentValidator.symbols.lookup(name)
	if !declared {
		documentValidator.reportUndeclared(token, required)
		return nil, false
	}
	if !kindSatisfies(found, required) {
		documentValidator.report(diag.New(
			diag.ClassIdentifierWrongKind,
			token.At,
			fmt.Sprintf("%s %s is declared as a %s, and a %s is required here", required, diag.Quote(name), found.kind, required),
			fmt.Sprintf("name a declared %s: %s", required, diag.Enumerate(documentValidator.symbols.namesOfKind(required), name)),
		).WithRelated(found.at, fmt.Sprintf("%s is declared here as a %s", diag.Quote(name), found.kind)))
		return nil, false
	}
	if !options.allowForward && after(found.at, token.At) {
		documentValidator.report(diag.New(
			diag.ClassForwardReference,
			token.At,
			fmt.Sprintf("%s %s is used at line %d and declared at line %d, and a reference resolves against a declaration already seen", found.kind, diag.Quote(name), token.At.Line, found.at.Line),
			fmt.Sprintf("move the `%s` declaration into the `%s` block, which the canonical block order places earlier", name, declarationForms[found.kind].block),
		).WithRelated(found.at, fmt.Sprintf("%s is declared here as a %s", diag.Quote(name), found.kind)))
		return nil, false
	}
	return found, true
}

func (documentValidator *validator) reportUndeclared(token lex.Token, required symbolKind) {
	form := declarationForms[required]
	documentValidator.report(diag.New(
		diag.ClassIdentifierUndeclared,
		token.At,
		fmt.Sprintf("%s %s is not declared", required, diag.Quote(token.Text)),
		fmt.Sprintf("add `%s` to the `%s` block, or name one of the declared %ss: %s",
			strings.Replace(form.form, "<id>", token.Text, 1), form.block, required,
			diag.Enumerate(documentValidator.symbols.namesOfKind(required), token.Text)),
	))
}

// kindSatisfies applies the one deliberate namespace sharing: a flag state
// field is a predicate under its own name, so a reader never has to ask which
// kind a name in a guard is.
func kindSatisfies(found *symbol, required symbolKind) bool {
	if found.kind == required {
		return true
	}
	if required != kindPredicate || found.kind != kindStateField {
		return false
	}
	field, isField := found.record.(*ir.StateField)
	return isField && field.Type == ir.FieldFlag
}

func after(first diag.SourcePosition, second diag.SourcePosition) bool {
	if first.Line != second.Line {
		return first.Line > second.Line
	}
	return first.Column > second.Column
}
