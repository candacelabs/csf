// Package lex turns widget source into positioned tokens, one slice per
// significant line.
//
// The dialect is line-oriented and its whole punctuation inventory is `%%`,
// `"`, `{` and `}`, so the scanner is a whitespace splitter that knows about
// quoted text. It classifies each token far enough for the parser and the
// validator to report a canonical-form finding rather than a shape error: a
// mermaid edge operator, a node shape bracket, a colour literal and a
// unit-suffixed time value are each their own kind, because each has a
// catalogued rewrite.
//
// Comments are dropped here. A `%%` that opens a line is a comment; a `%%`
// after content on the same line is W508 and the rest of that line is dropped,
// which is what makes a `%%` inside quoted text unambiguously text.
package lex

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
)

// Kind classifies one token.
type Kind int

const (
	// KindWord is an identifier or a keyword: the dialect reserves nothing, so
	// `node` is a legal role name and the parser decides by position.
	KindWord Kind = iota
	// KindInteger is a decimal integer, optionally signed.
	KindInteger
	// KindString is a double-quoted literal. Text holds its contents.
	KindString
	// KindTimeLiteral is a number carrying a unit sigil, such as `820ms`.
	KindTimeLiteral
	// KindMermaidEdge is one of mermaid's ten edge operators.
	KindMermaidEdge
	// KindShapeBracket is an identifier carrying a mermaid node shape bracket.
	KindShapeBracket
	// KindColour is a colour value: a hex literal, or an `rgb(`/`var(` form.
	KindColour
	// KindOther is a token that matches no other kind.
	KindOther
)

// Token is one lexical unit with its anchor.
type Token struct {
	Kind Kind
	// Text is the token's value: the run exactly as written for every kind
	// except KindString, where it is the literal's contents without its
	// delimiters. A span recovers what the delimiters were.
	Text string
	At   diag.SourcePosition
	// EndColumn is one past the token's last code point, so a span can be
	// closed without re-measuring the text.
	EndColumn int
}

// Span is the token's extent, for an IR record that is exactly one token wide.
func (token Token) Span() diag.SourceSpan {
	return diag.SourceSpan{
		File:        token.At.File,
		StartLine:   token.At.Line,
		StartColumn: token.At.Column,
		EndLine:     token.At.Line,
		EndColumn:   token.EndColumn,
	}
}

// Line is one significant line: a statement's tokens, in order. Blank lines and
// whole-line comments produce no Line.
type Line struct {
	Number int
	Tokens []Token
}

var (
	integerPattern      = regexp.MustCompile(`^[+-]?[0-9]+$`)
	timeLiteralPattern  = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[A-Za-z]+$`)
	identifierPattern   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	shapeBracketPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*[\[({>].*$`)
	hexColourPattern    = regexp.MustCompile(`^#[0-9A-Fa-f]{3,8}$`)
	colourCallPattern   = regexp.MustCompile(`^(?i:rgba?|hsla?|var|color)\(.*$`)
	mermaidEdgePattern  = regexp.MustCompile(`^(<?[-=]{2,3}[->ox]?|<?-\.-+>?|~{3,}|[-=]{2,}[->]?\|.*\|?)$`)
)

// byteOrderMark is the UTF-8 encoding of U+FEFF, which some editors write at
// the head of a file. It is an encoding artifact rather than content — the same
// kind of thing as the CRLF stripped on the next line — so it is removed before
// anything is tokenised. Left in place it glues itself to the first token: a
// document whose first line is a `%%` comment stops being a comment, becomes a
// block name nobody wrote, and swallows the rest of the document into a block
// that does not exist. The author is then told their comment is not one of the
// fourteen block names, which is true and useless.
//
// Stripping it rather than reporting it also keeps column 1 meaning column 1 on
// the first line, which is where a mark can appear and nowhere else.
const byteOrderMark = "\uFEFF"

// Scan splits source into significant lines of positioned tokens and reports
// every lexical finding it can decide without the grammar: an unterminated
// string, a trailing comment, and a mermaid init directive.
func Scan(file string, source []byte) ([]Line, []diag.Finding) {
	scanner := &scanner{file: file}
	text := strings.TrimPrefix(string(source), byteOrderMark)
	for number, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		scanner.scanLine(number+1, line)
	}
	return scanner.lines, scanner.findings
}

type scanner struct {
	file     string
	lines    []Line
	findings []diag.Finding
}

func (currentScanner *scanner) scanLine(number int, text string) {
	runes := []rune(text)
	tokens := make([]Token, 0, 6)
	column := 1
	for column <= len(runes) {
		if unicode.IsSpace(runes[column-1]) {
			column++
			continue
		}
		if isCommentStart(runes, column) {
			currentScanner.closeLineAtComment(number, runes, column, len(tokens))
			break
		}
		token, next := currentScanner.scanToken(number, runes, column)
		tokens = append(tokens, token)
		column = next
	}
	if len(tokens) > 0 {
		currentScanner.lines = append(currentScanner.lines, Line{Number: number, Tokens: tokens})
	}
}

// closeLineAtComment handles the three things a `%%` can mean: a whole-line
// comment, an init directive the dialect dropped, and a trailing comment.
func (currentScanner *scanner) closeLineAtComment(number int, runes []rune, column int, tokensSoFar int) {
	at := diag.SourcePosition{File: currentScanner.file, Line: number, Column: column}
	switch {
	case column+2 <= len(runes) && runes[column+1] == '{':
		currentScanner.findings = append(currentScanner.findings, diag.New(
			diag.ClassMermaidInitDirective,
			at,
			"a `%%{ … }%%` init directive appears, and the dialect replaced init directives with preamble directives",
			"delete the directive and write the setting as one of the four preamble directives: `widget <Name>`, `dialect 0`, `region \"<identity>\"` or `palette <name>`",
		))
	case tokensSoFar > 0:
		currentScanner.findings = append(currentScanner.findings, diag.New(
			diag.ClassTrailingComment,
			at,
			"a `%%` comment follows content on the same line, and a comment occupies a whole line",
			"move the comment to its own line above the statement, starting that line with `%%`",
		))
	}
}

func (currentScanner *scanner) scanToken(number int, runes []rune, column int) (Token, int) {
	at := diag.SourcePosition{File: currentScanner.file, Line: number, Column: column}
	if runes[column-1] == '"' {
		return currentScanner.scanString(at, runes, column)
	}
	end := column
	for end <= len(runes) && !unicode.IsSpace(runes[end-1]) {
		end++
	}
	raw := string(runes[column-1 : end-1])
	return Token{Kind: classify(raw), Text: raw, At: at, EndColumn: end}, end
}

func (currentScanner *scanner) scanString(at diag.SourcePosition, runes []rune, column int) (Token, int) {
	end := column + 1
	for end <= len(runes) && runes[end-1] != '"' {
		end++
	}
	if end > len(runes) {
		currentScanner.findings = append(currentScanner.findings, diag.New(
			diag.ClassStringUnterminated,
			at,
			"a string literal is not closed before the end of the line",
			"close the string with `\"` on the same line; a literal never spans lines",
		))
		return Token{
			Kind:      KindString,
			Text:      string(runes[column:]),
			At:        at,
			EndColumn: len(runes) + 1,
		}, len(runes) + 1
	}
	return Token{
		Kind:      KindString,
		Text:      string(runes[column : end-1]),
		At:        at,
		EndColumn: end + 1,
	}, end + 1
}

func isCommentStart(runes []rune, column int) bool {
	return runes[column-1] == '%' && column < len(runes) && runes[column] == '%'
}

func classify(raw string) Kind {
	switch {
	case identifierPattern.MatchString(raw):
		return KindWord
	case integerPattern.MatchString(raw):
		return KindInteger
	case timeLiteralPattern.MatchString(raw):
		return KindTimeLiteral
	case mermaidEdgePattern.MatchString(raw):
		return KindMermaidEdge
	case hexColourPattern.MatchString(raw), colourCallPattern.MatchString(raw):
		return KindColour
	case shapeBracketPattern.MatchString(raw):
		return KindShapeBracket
	default:
		return KindOther
	}
}
