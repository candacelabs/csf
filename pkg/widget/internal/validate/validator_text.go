package validate

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
	"github.com/candacelabs/csf/pkg/widget/internal/ir"
	"github.com/candacelabs/csf/pkg/widget/internal/lex"
)

// Everything about the text an author writes: the case conventions identifiers
// obey, the template grammar a label and a guard share, the region identity's
// pattern, the wire grammar the runtime imposes, and the classes of identifier
// a widget document may not carry.

var (
	lowerCamelPattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
	pascalPattern     = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	lowerSnakePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	regionPattern     = regexp.MustCompile(`^[A-Za-z0-9_:.-]{1,64}$`)
)

func matchesCase(text string, rule caseRule) bool {
	switch rule {
	case casePascal:
		return pascalPattern.MatchString(text)
	case caseLowerSnake:
		return lowerSnakePattern.MatchString(text)
	default:
		return lowerCamelPattern.MatchString(text)
	}
}

func caseName(rule caseRule) string {
	switch rule {
	case casePascal:
		return "PascalCase"
	case caseLowerSnake:
		return "lower_snake_case"
	default:
		return "lowerCamelCase"
	}
}

// rewriteCase renders the identifier the author wrote in the convention its
// position requires, so W101's fix shows the corrected spelling rather than
// naming the convention and stopping.
func rewriteCase(text string, rule caseRule) string {
	words := splitIdentifierWords(text)
	if len(words) == 0 {
		return text
	}
	switch rule {
	case caseLowerSnake:
		return strings.Join(words, "_")
	case casePascal:
		return joinCamel(words, true)
	default:
		return joinCamel(words, false)
	}
}

func splitIdentifierWords(text string) []string {
	words := []string{}
	current := &strings.Builder{}
	flush := func() {
		if current.Len() > 0 {
			words = append(words, strings.ToLower(current.String()))
			current.Reset()
		}
	}
	for _, character := range text {
		switch {
		case character == '_' || character == '-' || unicode.IsSpace(character):
			flush()
		case unicode.IsUpper(character):
			flush()
			current.WriteRune(character)
		case unicode.IsLetter(character) || unicode.IsDigit(character):
			current.WriteRune(character)
		}
	}
	flush()
	return words
}

func joinCamel(words []string, leadingUpper bool) string {
	builder := &strings.Builder{}
	for index, word := range words {
		if index > 0 || leadingUpper {
			builder.WriteString(strings.ToUpper(word[:1]) + word[1:])
			continue
		}
		builder.WriteString(word)
	}
	return builder.String()
}

// parseTemplate turns a literal into an IR text template, resolving every
// interpolation against the state fields and reporting the three ways an
// interpolation can be wrong: malformed, naming something that is not a state
// field, and naming a flag.
func (documentValidator *validator) parseTemplate(token lex.Token) *ir.TextTemplate {
	template := &ir.TextTemplate{Span: token.Span()}
	literal := []rune(token.Text)
	pending := &strings.Builder{}
	for index := 0; index < len(literal); {
		character := literal[index]
		if character == '{' && index+1 < len(literal) && literal[index+1] == '{' {
			pending.WriteRune('{')
			index += 2
			continue
		}
		if character != '{' {
			pending.WriteRune(character)
			index++
			continue
		}
		closing := indexOfRune(literal, '}', index+1)
		name := ""
		if closing > 0 {
			name = string(literal[index+1 : closing])
		}
		if closing < 0 || name == "" {
			documentValidator.report(diag.New(
				diag.ClassInterpolationMalformed,
				insideLiteral(token, index),
				fmt.Sprintf("interpolation %s is unbalanced or empty", diag.Quote(string(literal[index:]))),
				"close the interpolation as `{fieldName}`, naming a state field, or write `{{` for a literal brace",
			))
			pending.WriteRune(character)
			index++
			continue
		}
		template.Segments = appendLiteral(template.Segments, pending)
		template.Segments = append(template.Segments, ir.TemplateSegment{Field: documentValidator.interpolatedField(token, index, name)})
		index = closing + 1
	}
	template.Segments = appendLiteral(template.Segments, pending)
	return template
}

func (documentValidator *validator) interpolatedField(token lex.Token, offset int, name string) *ir.StateField {
	at := insideLiteral(token, offset)
	found, declared := documentValidator.symbols.lookup(name)
	field, isField := recordOf[*ir.StateField](found, declared)
	if !isField {
		documentValidator.report(diag.New(
			diag.ClassInterpolationNotStateField,
			at,
			fmt.Sprintf("interpolation %s does not name a state field", diag.Quote("{"+name+"}")),
			fmt.Sprintf("interpolate a declared `counter`, `count` or `text` field: %s", diag.Enumerate(documentValidator.interpolableFieldNames(), name)),
		))
		return nil
	}
	if !field.Interpolable() {
		documentValidator.report(diag.New(
			diag.ClassInterpolationOfFlag,
			at,
			fmt.Sprintf("interpolation %s names a `flag` field, and a flag renders as text only where the author decides how", diag.Quote("{"+name+"}")),
			fmt.Sprintf("replace the interpolation with a guard: `when %s then \"<text>\"`", name),
		))
		return nil
	}
	return field
}

func (documentValidator *validator) interpolableFieldNames() []string {
	names := make([]string, 0, len(documentValidator.document.StateFields))
	for _, field := range documentValidator.document.StateFields {
		if field.Interpolable() {
			names = append(names, field.Name)
		}
	}
	return names
}

func appendLiteral(segments []ir.TemplateSegment, pending *strings.Builder) []ir.TemplateSegment {
	if pending.Len() == 0 {
		return segments
	}
	segments = append(segments, ir.TemplateSegment{Literal: pending.String()})
	pending.Reset()
	return segments
}

// insideLiteral anchors a finding at a position counted inside a string
// literal, past its opening delimiter (A8).
func insideLiteral(token lex.Token, offset int) diag.SourcePosition {
	return diag.SourcePosition{File: token.At.File, Line: token.At.Line, Column: token.At.Column + 1 + offset}
}

func indexOfRune(runes []rune, wanted rune, from int) int {
	for index := from; index < len(runes); index++ {
		if runes[index] == wanted {
			return index
		}
	}
	return -1
}

// The classes of identifier a widget document may not carry. A document is
// portable by construction, which is also what makes it publishable — and the
// finding names the class rather than the value, because a validator that
// echoed the identifier would copy it into every transcript its output reaches.
var identifierClasses = []struct {
	class   string
	pattern *regexp.Regexp
}{
	{"a network address", regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)},
	{"a network address", regexp.MustCompile(`\b[0-9a-fA-F]{1,4}(:[0-9a-fA-F]{1,4}){3,}\b`)},
	{"a mailbox", regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)},
	{"a host name", regexp.MustCompile(`(?i)\b[a-z0-9][a-z0-9-]*\.(com|net|org|io|dev|cloud|ai|sh|xyz|local|internal|lan|home|arpa|ts\.net)\b`)},
	{"a URL", regexp.MustCompile(`(?i)\b(https?|ssh|ftp)://`)},
	{"a filesystem path", regexp.MustCompile(`(^|[\s"(])(~|\.{0,2})/[A-Za-z0-9._-]+/`)},
	{"a filesystem path", regexp.MustCompile(`(?i)\b[a-z]:\\`)},
}

// checkPortability reports a literal carrying a host name, a network address, a
// filesystem path or an account identifier.
func (documentValidator *validator) checkPortability(token lex.Token) {
	for _, candidate := range identifierClasses {
		if !candidate.pattern.MatchString(token.Text) {
			continue
		}
		documentValidator.report(diag.New(
			diag.ClassLiteralCarriesIdentifier,
			token.At,
			fmt.Sprintf("a literal carries %s, and a widget document names no host, address, path or account", candidate.class),
			"replace it with a role name or a generic node name such as `node-a`, and for a genuine example use a documentation-range address; a widget document is portable by construction, which is also what makes it publishable",
		))
		return
	}
}

// checkRegionIdentity reports a region identity outside the pattern every patch
// on the wire has to carry.
func (documentValidator *validator) checkRegionIdentity(at diag.SourcePosition, token lex.Token) bool {
	if regionPattern.MatchString(token.Text) {
		return true
	}
	documentValidator.report(diag.New(
		diag.ClassRegionIdentityMalformed,
		at,
		fmt.Sprintf("region identity %s does not match `^[A-Za-z0-9_:.-]{1,64}$`, and %s", diag.Quote(token.Text), regionFault(token.Text)),
		"rewrite the identity within the pattern, for example `widget.node-status`; a deployed identity is named by every patch on the wire, so changing one a client already knows is a client-visible change",
	))
	return false
}

func regionFault(text string) string {
	if length := len([]rune(text)); length == 0 || length > 64 {
		return fmt.Sprintf("it is %d code points long", length)
	}
	for _, character := range text {
		if !strings.ContainsRune("_:.-", character) && !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			return fmt.Sprintf("it contains %s", describeCharacter(character))
		}
	}
	return "it contains a character outside the pattern"
}

func describeCharacter(character rune) string {
	if unicode.IsSpace(character) {
		return "a space"
	}
	return fmt.Sprintf("`%c`", character)
}

// checkWireName reports the two spellings the runtime's binding grammar cannot
// carry, and the runtime-minted names a document may not declare.
func (documentValidator *validator) checkWireName(at diag.SourcePosition, token lex.Token) bool {
	for _, minted := range runtimeMintedEvents {
		if token.Text == minted {
			documentValidator.report(diag.New(
				diag.ClassRuntimeMintedEvent,
				token.At,
				fmt.Sprintf("wire name %s is minted by the runtime, and a runtime-minted event is never browser-sendable", diag.Quote(token.Text)),
				fmt.Sprintf("delete the declaration; the minted set is %s, and a widget reaches the slow-client signal through a `signal` field instead", diag.Enumerate(runtimeMintedEvents, token.Text)),
			))
			return false
		}
	}
	if token.Text == "" {
		documentValidator.report(diag.New(
			diag.ClassWireNameMalformed,
			at,
			"the wire name is empty, and an empty name silences every binding behind it on the same DOM event",
			"give the event a wire name, for example `wire \"widget.node-status.health\"`; the runtime panics rather than degrading",
		))
		return false
	}
	if index := strings.IndexAny(token.Text, ":;"); index >= 0 {
		documentValidator.report(diag.New(
			diag.ClassWireNameMalformed,
			at,
			fmt.Sprintf("wire name %s contains `%c`, which is structural in the runtime's binding grammar", diag.Quote(token.Text), token.Text[index]),
			fmt.Sprintf("remove the `%c` — a stray one shifts every component behind it and turns a declared debounce into a throttle, and the runtime panics rather than degrading", token.Text[index]),
		))
		return false
	}
	return true
}
