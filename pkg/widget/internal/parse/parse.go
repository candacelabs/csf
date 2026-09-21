// Package parse turns positioned tokens into a widget document's block
// structure, and reports every structural finding of the catalogue's W0 group.
//
// It is deliberately shallow: it knows the fourteen blocks, their canonical
// order and which declaration keyword opens a nested block inside each, and it
// knows nothing about clauses, enumerations or references. That split is what
// keeps a malformed document producing structural findings rather than a
// cascade of semantic ones — and it is what lets the validator resolve against
// a shape that is always well-formed, whatever the source did.
//
// Recovery is explicit rather than accidental. A block name met inside an open
// block means the open block was never closed, so the parser closes it, reports
// W008 at its opening line, and re-dispatches at document scope. An unclosed
// group therefore never swallows the rest of the document.
package parse

import (
	"fmt"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
	"github.com/candacelabs/csf/pkg/widget/internal/lex"
)

// Statement is one significant line: its first token and the rest.
type Statement struct {
	Keyword   lex.Token
	Arguments []lex.Token
}

// At is the statement's anchor, at its first token (A1).
func (statement Statement) At() diag.SourcePosition {
	return statement.Keyword.At
}

// Span covers the whole statement.
func (statement Statement) Span() diag.SourceSpan {
	span := statement.Keyword.Span()
	if len(statement.Arguments) > 0 {
		last := statement.Arguments[len(statement.Arguments)-1]
		span.EndLine = last.At.Line
		span.EndColumn = last.EndColumn
	}
	return span
}

// Declaration is a named block nested inside one of the fourteen blocks: a
// role, a node, an event and their nine siblings.
type Declaration struct {
	Header     Statement
	Statements []Statement
	// EndAt anchors the `end` that closed the declaration, or the header when
	// the declaration was never closed.
	EndAt diag.SourcePosition
}

// Span covers the declaration from its header to its `end`.
func (declaration Declaration) Span() diag.SourceSpan {
	span := declaration.Header.Span()
	span.EndLine = declaration.EndAt.Line
	span.EndColumn = declaration.EndAt.Column
	return span
}

// Block is one of the fourteen blocks, with its member lines and its nested
// declarations each in declaration order.
type Block struct {
	Spec         BlockSpec
	Header       Statement
	Statements   []Statement
	Declarations []Declaration
	EndAt        diag.SourcePosition
}

// Span covers the block from its header to its `end`.
func (block Block) Span() diag.SourceSpan {
	span := block.Header.Span()
	span.EndLine = block.EndAt.Line
	span.EndColumn = block.EndAt.Column
	return span
}

// Document is a parsed widget document: the preamble directives in source
// order, and the blocks in source order — which is not necessarily the
// canonical order, because reporting an out-of-order block is the validator's
// job rather than a reason to rearrange what the author wrote.
type Document struct {
	File     string
	Preamble []Statement
	Blocks   []Block
}

// FirstStatement is the document's first significant line, which is where W001
// anchors.
func (document Document) FirstStatement() (Statement, bool) {
	if len(document.Preamble) > 0 {
		return document.Preamble[0], true
	}
	if len(document.Blocks) > 0 {
		return document.Blocks[0].Header, true
	}
	return Statement{}, false
}

// LastPreamblePosition anchors the findings that are about the preamble as a
// whole: a required block that is absent, or a directive that is missing.
func (document Document) LastPreamblePosition() diag.SourcePosition {
	if len(document.Preamble) == 0 {
		return diag.SourcePosition{File: document.File, Line: 1, Column: 1}
	}
	return document.Preamble[len(document.Preamble)-1].At()
}

// Parse scans and structures a document, reporting every lexical and structural
// finding. The returned document is always usable: recovery is total.
func Parse(file string, source []byte) (Document, []diag.Finding) {
	lines, findings := lex.Scan(file, source)
	documentParser := &parser{file: file, lines: lines, findings: findings}
	document := documentParser.parseDocument()
	documentParser.checkBlockPlacement(&document)
	return document, documentParser.findings
}

type parser struct {
	file     string
	lines    []lex.Line
	index    int
	findings []diag.Finding
}

func (documentParser *parser) parseDocument() Document {
	document := Document{File: documentParser.file}
	for documentParser.index < len(documentParser.lines) {
		statement := documentParser.statementAt(documentParser.index)
		keyword := statement.Keyword.Text
		spec, isBlock := LookupBlock(keyword)
		switch {
		case isBlock:
			documentParser.index++
			document.Blocks = append(document.Blocks, documentParser.parseBlock(spec, statement))
		case isPreambleKeyword(keyword) && len(document.Blocks) == 0:
			documentParser.index++
			document.Preamble = append(document.Preamble, statement)
		default:
			documentParser.index++
			documentParser.reportStrayStatement(statement)
		}
	}
	return document
}

// reportStrayStatement classifies a line that appears at document scope where
// only a block header may: a stray `end`, a dropped mermaid keyword, a
// statement that belongs inside a block, or an unknown block name.
func (documentParser *parser) reportStrayStatement(statement Statement) {
	keyword := statement.Keyword.Text
	switch replacement, dropped := DroppedMermaidKeyword(keyword); {
	case keyword == "end":
		documentParser.report(diag.New(
			diag.ClassEndWithoutBlock,
			statement.At(),
			"an `end` closes nothing here",
			"delete the `end`, or add the block opening line it was meant to close",
		))
	case dropped:
		documentParser.reportDroppedKeyword(statement, replacement)
	case IsStatementKeyword(keyword):
		documentParser.report(diag.New(
			diag.ClassStatementAtDocumentScope,
			statement.At(),
			fmt.Sprintf("statement `%s` appears at document scope, where only the four preamble directives and the fourteen block headers may", keyword),
			fmt.Sprintf("move the `%s` statement into the block that owns it, one of %s", keyword, diag.Enumerate(BlockNames(), keyword)),
		))
	default:
		documentParser.report(diag.New(
			diag.ClassBlockNameUnknown,
			statement.At(),
			fmt.Sprintf("block name %s is not one of the fourteen", diag.Quote(keyword)),
			fmt.Sprintf("use one of the fourteen block names: %s", diag.Enumerate(BlockNames(), keyword)),
		))
	}
}

func (documentParser *parser) reportDroppedKeyword(statement Statement, replacement string) {
	documentParser.report(diag.New(
		diag.ClassMermaidKeyword,
		statement.At(),
		fmt.Sprintf("mermaid keyword `%s` appears, and the dialect dropped it", statement.Keyword.Text),
		fmt.Sprintf("write %s instead", replacement),
	))
}

func (documentParser *parser) parseBlock(spec BlockSpec, header Statement) Block {
	block := Block{Spec: spec, Header: header, EndAt: header.At()}
	for documentParser.index < len(documentParser.lines) {
		statement := documentParser.statementAt(documentParser.index)
		keyword := statement.Keyword.Text
		switch {
		case keyword == "end":
			block.EndAt = statement.At()
			documentParser.index++
			return block
		case documentParser.isBlockHeader(keyword):
			documentParser.reportUnclosed(header, spec.Name)
			return block
		case spec.opens(keyword):
			documentParser.index++
			block.Declarations = append(block.Declarations, documentParser.parseDeclaration(spec, statement))
		default:
			documentParser.index++
			block.Statements = append(block.Statements, statement)
		}
	}
	documentParser.reportUnclosed(header, spec.Name)
	return block
}

func (documentParser *parser) parseDeclaration(parent BlockSpec, header Statement) Declaration {
	declaration := Declaration{Header: header, EndAt: header.At()}
	for documentParser.index < len(documentParser.lines) {
		statement := documentParser.statementAt(documentParser.index)
		keyword := statement.Keyword.Text
		switch {
		case keyword == "end":
			declaration.EndAt = statement.At()
			documentParser.index++
			return declaration
		case documentParser.isBlockHeader(keyword):
			documentParser.reportUnclosed(header, keyword)
			return declaration
		case parent.opens(keyword):
			documentParser.index++
			documentParser.skipNestedBlock(parent, header, statement)
		default:
			documentParser.index++
			declaration.Statements = append(declaration.Statements, statement)
		}
	}
	documentParser.reportUnclosed(header, header.Keyword.Text)
	return declaration
}

// skipNestedBlock consumes a declaration nested one level too deep and reports
// it. Nesting is container-then-declaration and never deeper, so the inner
// block's contents cannot be attached anywhere.
func (documentParser *parser) skipNestedBlock(parent BlockSpec, outer Statement, inner Statement) {
	documentParser.report(diag.New(
		diag.ClassBlockNestedTooDeep,
		inner.At(),
		fmt.Sprintf("declaration `%s` is nested inside declaration `%s`, and nesting is container-then-declaration and never deeper", inner.Keyword.Text, outer.Keyword.Text),
		fmt.Sprintf("move the `%s` declaration to the top level of the `%s` block", inner.Keyword.Text, parent.Name),
	))
	for documentParser.index < len(documentParser.lines) {
		statement := documentParser.statementAt(documentParser.index)
		if documentParser.isBlockHeader(statement.Keyword.Text) {
			return
		}
		documentParser.index++
		if statement.Keyword.Text == "end" {
			return
		}
	}
}

func (documentParser *parser) reportUnclosed(header Statement, name string) {
	documentParser.report(diag.New(
		diag.ClassBlockUnclosed,
		header.At(),
		fmt.Sprintf("block `%s` opened at line %d is never closed", name, header.At().Line),
		fmt.Sprintf("add `end` on its own line to close the `%s` block", name),
	))
}

// checkBlockPlacement reports the findings that are about a block's position
// rather than its contents: an unknown-free document may still repeat a block,
// misorder one, leave one empty, or omit a required one.
func (documentParser *parser) checkBlockPlacement(document *Document) {
	seen := map[string]Statement{}
	highest := BlockSpec{}
	for _, block := range document.Blocks {
		if first, repeated := seen[block.Spec.Name]; repeated {
			documentParser.report(diag.New(
				diag.ClassBlockDuplicated,
				block.Header.At(),
				fmt.Sprintf("block `%s` appears twice", block.Spec.Name),
				fmt.Sprintf("merge the two into one `%s` block at line %d", block.Spec.Name, first.At().Line),
			).WithRelated(first.At(), fmt.Sprintf("`%s` is declared here first", block.Spec.Name)))
		} else {
			seen[block.Spec.Name] = block.Header
		}
		if highest.Ordinal > block.Spec.Ordinal {
			documentParser.report(diag.New(
				diag.ClassBlockOutOfOrder,
				block.Header.At(),
				fmt.Sprintf("block `%s` is block %d of the canonical order and follows block `%s`, which is block %d", block.Spec.Name, block.Spec.Ordinal, highest.Name, highest.Ordinal),
				fmt.Sprintf("move the `%s` block above the `%s` block, so block %d precedes block %d", block.Spec.Name, highest.Name, block.Spec.Ordinal, highest.Ordinal),
			))
		}
		if block.Spec.Ordinal > highest.Ordinal {
			highest = block.Spec
		}
		if len(block.Statements) == 0 && len(block.Declarations) == 0 {
			documentParser.report(diag.New(
				diag.ClassBlockEmpty,
				block.Header.At(),
				fmt.Sprintf("block `%s` contains no declarations", block.Spec.Name),
				fmt.Sprintf("delete the `%s` block — an omitted optional block is the only spelling for \"none\"", block.Spec.Name),
			))
		}
	}
	for _, spec := range blockSpecs {
		if _, present := seen[spec.Name]; spec.Required && !present {
			documentParser.report(diag.New(
				diag.ClassRequiredBlockMissing,
				document.LastPreamblePosition(),
				fmt.Sprintf("required block `%s` is absent, and six blocks are required: %s", spec.Name, diag.Enumerate(requiredBlockNames(), spec.Name)),
				fmt.Sprintf("add the `%s` block, closed by `end`, in position %d of the canonical order", spec.Name, spec.Ordinal),
			))
		}
	}
}

func requiredBlockNames() []string {
	names := make([]string, 0, len(blockSpecs))
	for _, spec := range blockSpecs {
		if spec.Required {
			names = append(names, spec.Name)
		}
	}
	return names
}

func (documentParser *parser) isBlockHeader(keyword string) bool {
	_, isBlock := LookupBlock(keyword)
	return isBlock
}

func (documentParser *parser) statementAt(index int) Statement {
	line := documentParser.lines[index]
	return Statement{Keyword: line.Tokens[0], Arguments: line.Tokens[1:]}
}

func (documentParser *parser) report(finding diag.Finding) {
	documentParser.findings = append(documentParser.findings, finding)
}
