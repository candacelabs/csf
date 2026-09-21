// Package validate resolves a parsed widget document into the typed IR and
// reports every finding of the error catalogue.
//
// Two passes, and both run to completion. The first declares every identifier,
// so a reference can be told apart from a name that is merely written later;
// the second builds the IR in canonical block order, resolving each reference
// against the declaration table and reporting the invariants that can be
// decided locally. A third, whole-document sweep reports the relations no
// single statement can be wrong about on its own — an unreferenced role, a
// channel carried by no edge, a state field written by nothing, a cycle in the
// predicate graph.
//
// Nothing here stops at the first finding. An author who fixes one error and
// re-runs to discover the next learns that the tool tells them a fraction of
// the truth, and starts guessing ahead of it.
package validate

import (
	"fmt"
	"strconv"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
	"github.com/candacelabs/csf/pkg/widget/internal/ir"
	"github.com/candacelabs/csf/pkg/widget/internal/lex"
	"github.com/candacelabs/csf/pkg/widget/internal/parse"
)

// DialectVersion is the one dialect version this interpreter implements. A
// document declaring any other version is refused rather than guessed at.
const DialectVersion = 0

type validator struct {
	file       string
	symbols    *symbolTable
	document   *ir.Document
	findings   []diag.Finding
	blocks     map[string]*parse.Block
	referenced map[string]bool
	atomics    map[string]*ir.Predicate
}

// Document parses and validates one widget document. It always returns an IR
// value; the document is sound only when no finding is returned, and a caller
// that generates from an unsound document is generating from a guess.
func Document(file string, source []byte) (*ir.Document, []diag.Finding) {
	parsed, findings := parse.Parse(file, source)
	documentValidator := &validator{
		file:       file,
		symbols:    newSymbolTable(),
		document:   &ir.Document{DialectVersion: DialectVersion},
		findings:   findings,
		blocks:     map[string]*parse.Block{},
		referenced: map[string]bool{},
		atomics:    map[string]*ir.Predicate{},
	}
	documentValidator.readPreamble(parsed)
	documentValidator.indexBlocks(parsed)
	documentValidator.declareSymbols(parsed)
	documentValidator.build()
	documentValidator.checkInvariants()
	diag.Sort(documentValidator.findings)
	return documentValidator.document, documentValidator.findings
}

func (documentValidator *validator) report(finding diag.Finding) {
	documentValidator.findings = append(documentValidator.findings, finding)
}

// readPreamble reads the four document-scope directives and reports the three
// ways a preamble goes wrong: not opening with `widget`, omitting a directive,
// and declaring a dialect version this parser does not implement.
func (documentValidator *validator) readPreamble(parsed parse.Document) {
	document := documentValidator.document
	document.Span = diag.SourceSpan{File: documentValidator.file, StartLine: 1, StartColumn: 1}
	first, present := parsed.FirstStatement()
	if present && first.Keyword.Text != parse.PreambleKeywords[0] {
		documentValidator.report(diag.New(
			diag.ClassPreambleFirstStatement,
			first.At(),
			fmt.Sprintf("the document opens with `%s`, and the first statement of a document is `widget <Name>`", first.Keyword.Text),
			"make `widget <Name>` the first statement; only whole-line `%% ` comments may precede it",
		))
	}
	directives := map[string]parse.Statement{}
	for _, statement := range parsed.Preamble {
		if _, repeated := directives[statement.Keyword.Text]; !repeated {
			directives[statement.Keyword.Text] = statement
		}
	}
	anchor := parsed.LastPreamblePosition()
	if opening, declared := directives[parse.PreambleKeywords[0]]; declared {
		anchor = opening.At()
	}
	for _, keyword := range parse.PreambleKeywords {
		statement, declared := directives[keyword]
		if !declared {
			documentValidator.reportMissingDirective(keyword, anchor)
			continue
		}
		documentValidator.readDirective(keyword, statement)
	}
}

func (documentValidator *validator) reportMissingDirective(keyword string, anchor diag.SourcePosition) {
	documentValidator.report(diag.New(
		diag.ClassPreambleDirectiveMissing,
		anchor,
		fmt.Sprintf("preamble directive `%s` is absent, and all four are required in the order %s", keyword, diag.Sequence(parse.PreambleKeywords)),
		fmt.Sprintf("add `%s` to the preamble, in position %d", preambleForms[keyword], preambleOrdinal(keyword)),
	))
}

var preambleForms = map[string]string{
	"widget":  "widget <Name>",
	"dialect": "dialect 0",
	"region":  "region \"<identity>\"",
	"palette": "palette <name>",
}

func preambleOrdinal(keyword string) int {
	for index, directive := range parse.PreambleKeywords {
		if directive == keyword {
			return index + 1
		}
	}
	return 0
}

func (documentValidator *validator) readDirective(keyword string, statement parse.Statement) {
	spec := preambleSpecs[keyword]
	read := documentValidator.readClause(statement, spec)
	value := read.value(0)
	if !value.sound {
		return
	}
	switch keyword {
	case "widget":
		documentValidator.document.Name = value.text
	case "dialect":
		documentValidator.readDialectVersion(value)
	case "region":
		if documentValidator.checkRegionIdentity(statement.At(), value.token) {
			documentValidator.document.Region = value.text
		}
		documentValidator.checkPortability(value.token)
	case "palette":
		if documentValidator.checkPaletteName(value) {
			documentValidator.document.Palette = value.text
		}
	}
}

// checkPaletteName reports a `palette` directive naming a palette that does not
// exist.
//
// The interpreter does not resolve a palette — colour values belong to the
// design system, and a widget that could mint one could leave it — but the set
// of names is closed and known, so a typo is refusable here rather than at run
// time. The P2 audit's H6: `palette midnightNeon` validated clean, generated,
// compiled, and surfaced as a package-init panic in the host that mounted it.
// Fail-fast is what that host intends; the finding is that the document was
// called sound.
//
// The document's Palette is left unset when the name is refused, for the reason
// every reference class exists: a generator reads a resolved IR, and a name
// nothing can resolve is not a value to carry forward.
func (documentValidator *validator) checkPaletteName(value argumentValue) bool {
	for _, name := range ir.PaletteNames() {
		if name == value.text {
			return true
		}
	}
	documentValidator.report(diag.New(
		diag.ClassPaletteUnknown,
		value.token.At,
		fmt.Sprintf("palette %s is not a palette this SDK ships, and a widget renders through a palette rather than through colour values of its own", diag.Quote(value.text)),
		fmt.Sprintf("name one of the palettes that exist: %s; a palette maps the seven token names to values, and the design system owns both the mapping and the set", diag.Enumerate(ir.PaletteNames(), value.text)),
	))
	return false
}

func (documentValidator *validator) readDialectVersion(value argumentValue) {
	version, conversionError := strconv.Atoi(value.text)
	if conversionError != nil || version != DialectVersion {
		documentValidator.report(diag.New(
			diag.ClassDialectVersionUnsupported,
			value.token.At,
			fmt.Sprintf("the document declares dialect version %s, and this interpreter implements version %d", diag.Quote(value.text), DialectVersion),
			fmt.Sprintf("write `dialect %d`, or read the document with an interpreter that implements version %s — a version is refused rather than guessed at", DialectVersion, diag.Quote(value.text)),
		))
		return
	}
	documentValidator.document.DialectVersion = version
}

var preambleSpecs = map[string]clauseSpec{
	"widget":  {keyword: "widget", arguments: []argumentSpec{{kind: argumentIdentifier, name: "Name", nameCase: casePascal}}},
	"dialect": {keyword: "dialect", arguments: []argumentSpec{integerArgument("version", 0, unboundedInteger)}},
	"region":  {keyword: "region", arguments: []argumentSpec{{kind: argumentString, name: "identity"}}},
	"palette": {keyword: "palette", arguments: []argumentSpec{{kind: argumentIdentifier, name: "name"}}},
}

// indexBlocks keeps the first occurrence of each block. A repeated block is
// already reported by the parser; reading only the first keeps every later
// finding about the document the author meant to write.
func (documentValidator *validator) indexBlocks(parsed parse.Document) {
	for index := range parsed.Blocks {
		block := &parsed.Blocks[index]
		if _, seen := documentValidator.blocks[block.Spec.Name]; !seen {
			documentValidator.blocks[block.Spec.Name] = block
		}
	}
}

func (documentValidator *validator) block(name string) (*parse.Block, bool) {
	block, present := documentValidator.blocks[name]
	return block, present
}

// declareSymbols is the first pass: every identifier the document declares,
// with its kind and its position. It runs before any reference is resolved, so
// a name written before its declaration is reported as the ordering mistake it
// is rather than as an unknown name.
func (documentValidator *validator) declareSymbols(parsed parse.Document) {
	for index := range parsed.Blocks {
		block := &parsed.Blocks[index]
		documentValidator.declareBlockSymbols(block)
	}
}

func (documentValidator *validator) declareBlockSymbols(block *parse.Block) {
	if block.Spec.Named {
		documentValidator.declareFrom(block.Header, kindScene)
	}
	for _, statement := range block.Statements {
		if spec, isLine := namedDeclarationSpecs[statement.Keyword.Text]; isLine && spec.blocks[block.Spec.Name] {
			documentValidator.declareFrom(statement, spec.spec.kind)
		}
	}
	for _, declaration := range block.Declarations {
		if spec, isDeclaration := blockDeclarationSpecs[declaration.Header.Keyword.Text]; isDeclaration {
			documentValidator.declareFrom(declaration.Header, spec.kind)
		}
	}
}

func (documentValidator *validator) declareFrom(statement parse.Statement, kind symbolKind) {
	if len(statement.Arguments) == 0 || statement.Arguments[0].Kind != lex.KindWord {
		return
	}
	token := statement.Arguments[0]
	existing := documentValidator.symbols.declare(token.Text, kind, token.At, statement.At())
	if existing == nil {
		return
	}
	documentValidator.report(diag.New(
		diag.ClassIdentifierDuplicated,
		token.At,
		fmt.Sprintf("identifier %s is declared twice, as a %s and as a %s, and every identifier is unique across the whole document", diag.Quote(token.Text), existing.kind, kind),
		fmt.Sprintf("rename one of the two, for example `%s`; the one deliberate sharing is that a `flag` field is already a predicate under its own name", token.Text+"Two"),
	).WithRelated(existing.at, fmt.Sprintf("%s is declared here as a %s", diag.Quote(token.Text), existing.kind)))
}

// namedDeclarationSpecs and blockDeclarationSpecs index the declaration table by
// keyword, so the symbol pass and the build pass agree on what a keyword
// declares without either restating the grammar.
var namedDeclarationSpecs = map[string]struct {
	spec   declarationSpec
	blocks map[string]bool
}{
	"field":     {fieldDeclaration, map[string]bool{"state": true}},
	"label":     {labelDeclaration, map[string]bool{"labels": true}},
	"placement": {placementDeclaration, map[string]bool{"placements": true}},
	"orbit":     {orbitDeclaration, map[string]bool{"scene": true}},
}

var blockDeclarationSpecs = map[string]declarationSpec{
	"predicate": predicateDeclaration,
	"binding":   bindingDeclaration,
	"role":      roleDeclaration,
	"channel":   channelDeclaration,
	"node":      nodeDeclaration,
	"edge":      edgeDeclaration,
	"pulse":     pulseDeclaration,
	"emphasis":  emphasisDeclaration,
	"indicator": indicatorDeclaration,
	"control":   controlDeclaration,
	"event":     eventDeclaration,
	"stream":    streamDeclaration,
}

func recordOf[Record any](found *symbol, declared bool) (Record, bool) {
	var zero Record
	if !declared || found == nil {
		return zero, false
	}
	record, isRecord := found.record.(Record)
	return record, isRecord
}
