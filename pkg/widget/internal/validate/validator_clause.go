package validate

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
	"github.com/candacelabs/csf/pkg/widget/internal/ir"
	"github.com/candacelabs/csf/pkg/widget/internal/lex"
	"github.com/candacelabs/csf/pkg/widget/internal/parse"
)

// The clause reader. Every construct in the dialect is a keyword followed by
// arguments, so one reader driven by the specs in spec.go produces every
// finding that is about the shape of a statement — a keyword the block does not
// accept, an argument of the wrong shape, a value outside a closed set, an
// integer outside its range, a clause written twice, a required clause absent —
// and every builder receives arguments already checked.
//
// It also owns the canonical-form group: a mermaid edge operator, a shape
// bracket, a dropped keyword, a colour literal, a unit-suffixed time and a
// string where a label reference belongs are each recognised here, and each
// suppresses the shape finding that would otherwise be reported in its place. A
// W5xx class always beats W004 and W013, because an author who wrote the
// mermaid spelling needs the replacement text rather than a rule number.

type argumentValue struct {
	token   lex.Token
	text    string
	integer int
	// sound is false when the argument was missing or already reported, so a
	// builder skips it rather than reporting a consequence of it.
	sound bool
	// canonical marks an argument written in a spelling the dialect dropped.
	// The rest of its clause is not read, because every later argument would
	// report a consequence of a mistake already explained.
	canonical bool
}

type clause struct {
	keyword   lex.Token
	values    []argumentValue
	statement parse.Statement
}

// value returns the clause's argument at an index, or an unsound zero value.
func (readClause clause) value(index int) argumentValue {
	if index >= len(readClause.values) {
		return argumentValue{}
	}
	return readClause.values[index]
}

type clauseSet struct {
	byKeyword map[string][]clause
	order     []clause
}

func (set clauseSet) all(keyword string) []clause {
	return set.byKeyword[keyword]
}

func (set clauseSet) first(keyword string) (clause, bool) {
	found := set.byKeyword[keyword]
	if len(found) == 0 {
		return clause{}, false
	}
	return found[0], true
}

// clauseContext describes what is being read, for the messages the reader
// produces.
type clauseContext struct {
	// container is the block or declaration a statement sits in, named the way
	// an author would name it: "the `roles` block", "role \"peer\"".
	container string
	// accepted lists every keyword legal here, including the keywords of nested
	// declarations, so W013 can enumerate them.
	accepted []string
	// duplicateClass is W107 inside a declaration and W302 for the singular
	// statements of a block.
	duplicateClass diag.Class
}

func (documentValidator *validator) readClauses(statements []parse.Statement, specs []clauseSpec, context clauseContext) clauseSet {
	set := clauseSet{byKeyword: map[string][]clause{}}
	for _, statement := range statements {
		if documentValidator.reportCanonicalForm(statement) {
			continue
		}
		spec, accepted := findClauseSpec(specs, statement.Keyword.Text)
		if !accepted {
			documentValidator.reportUnknownKeyword(statement, context)
			continue
		}
		read := documentValidator.readClause(statement, spec)
		if existing := set.byKeyword[spec.keyword]; len(existing) > 0 && !spec.repeatable {
			documentValidator.report(diag.New(
				context.duplicateClass,
				statement.At(),
				fmt.Sprintf("clause `%s` appears twice in %s, and it is written once", spec.keyword, context.container),
				fmt.Sprintf("delete one of the two `%s` lines", spec.keyword),
			).WithRelated(existing[0].keyword.At, fmt.Sprintf("`%s` appears here first", spec.keyword)))
			continue
		}
		set.byKeyword[spec.keyword] = append(set.byKeyword[spec.keyword], read)
		set.order = append(set.order, read)
	}
	return set
}

// checkRequired reports the clauses a declaration must carry and does not
// (W106), anchored at the declaration's opening line.
func (documentValidator *validator) checkRequired(set clauseSet, specs []clauseSpec, owner string, at diag.SourcePosition) {
	missing := make([]string, 0, len(specs))
	for _, spec := range specs {
		if spec.required && len(set.byKeyword[spec.keyword]) == 0 {
			missing = append(missing, fmt.Sprintf("`%s`", describeClause(spec)))
		}
	}
	if len(missing) == 0 {
		return
	}
	documentValidator.report(diag.New(
		diag.ClassClauseMissing,
		at,
		fmt.Sprintf("%s is missing %d required %s: %s", owner, len(missing), pluralize("clause", len(missing)), strings.Join(missing, ", ")),
		fmt.Sprintf("add %s to the declaration, one clause per line", strings.Join(missing, " and ")),
	))
}

func (documentValidator *validator) readClause(statement parse.Statement, spec clauseSpec) clause {
	read := clause{keyword: statement.Keyword, statement: statement}
	arguments := statement.Arguments
	for index, argument := range spec.arguments {
		if index >= len(arguments) {
			if !argument.optional {
				documentValidator.reportArgumentShape(spec, fmt.Sprintf("the %s argument is missing", argument.name), statement.At())
			}
			break
		}
		value := documentValidator.matchArgument(spec, argument, arguments[index])
		read.values = append(read.values, value)
		if value.canonical {
			return read
		}
	}
	if len(arguments) > len(spec.arguments) {
		extra := arguments[len(spec.arguments)]
		documentValidator.reportArgumentShape(spec, fmt.Sprintf("it carries %d arguments and takes %d", len(arguments), len(spec.arguments)), extra.At)
	}
	return read
}

func (documentValidator *validator) matchArgument(spec clauseSpec, argument argumentSpec, token lex.Token) argumentValue {
	switch argument.kind {
	case argumentKeyword:
		if token.Kind != lex.KindWord || token.Text != argument.name {
			documentValidator.reportArgumentShape(spec, fmt.Sprintf("the word `%s` is required at this position", argument.name), token.At)
			return argumentValue{token: token}
		}
		return argumentValue{token: token, text: token.Text, sound: true}
	case argumentString:
		if token.Kind != lex.KindString {
			documentValidator.reportArgumentShape(spec, fmt.Sprintf("the %s argument is a quoted string", argument.name), token.At)
			return argumentValue{token: token}
		}
		return argumentValue{token: token, text: token.Text, sound: true}
	case argumentInteger:
		return documentValidator.matchInteger(spec, argument, token)
	case argumentEnum:
		return documentValidator.matchEnumeration(spec, argument, token, diag.ClassValueNotEnumerated, argument.values)
	case argumentSignal:
		return documentValidator.matchEnumeration(spec, argument, token, diag.ClassSignalUnknown, signalNames)
	case argumentToken:
		return documentValidator.matchToken(spec, argument, token)
	default:
		return documentValidator.matchIdentifier(spec, argument, token)
	}
}

func (documentValidator *validator) matchInteger(spec clauseSpec, argument argumentSpec, token lex.Token) argumentValue {
	if token.Kind == lex.KindTimeLiteral {
		documentValidator.reportTimeUnitSigil(token)
		return argumentValue{token: token, canonical: true}
	}
	if token.Kind != lex.KindInteger {
		documentValidator.reportArgumentShape(spec, fmt.Sprintf("the %s argument is a whole number", argument.name), token.At)
		return argumentValue{token: token}
	}
	value, conversionError := strconv.Atoi(token.Text)
	if conversionError != nil || value < argument.minimum || value > argument.maximum {
		documentValidator.report(diag.New(
			diag.ClassIntegerOutOfRange,
			token.At,
			fmt.Sprintf("clause `%s` carries %s, and its %s argument is a whole number from %d to %d", spec.keyword, token.Text, argument.name, argument.minimum, argument.maximum),
			fmt.Sprintf("write a value inside the range, for example `%s %d`", spec.keyword, clampExample(argument)),
		))
		return argumentValue{token: token}
	}
	return argumentValue{token: token, text: token.Text, integer: value, sound: true}
}

func (documentValidator *validator) matchEnumeration(spec clauseSpec, argument argumentSpec, token lex.Token, class diag.Class, values []string) argumentValue {
	if token.Kind != lex.KindWord {
		documentValidator.reportArgumentShape(spec, fmt.Sprintf("the %s argument is one word", argument.name), token.At)
		return argumentValue{token: token}
	}
	for _, value := range values {
		if value == token.Text {
			return argumentValue{token: token, text: token.Text, sound: true}
		}
	}
	documentValidator.report(diag.New(
		class,
		token.At,
		fmt.Sprintf("%s %s is not one of the %d values `%s` takes", argument.name, diag.Quote(token.Text), len(values), spec.keyword),
		fmt.Sprintf("write one of %s", diag.Enumerate(values, token.Text)),
	))
	return argumentValue{token: token}
}

func (documentValidator *validator) matchToken(spec clauseSpec, argument argumentSpec, token lex.Token) argumentValue {
	_, namedColour := cssColourNames[strings.ToLower(token.Text)]
	if token.Kind == lex.KindColour || namedColour {
		documentValidator.report(diag.New(
			diag.ClassColourLiteral,
			token.At,
			fmt.Sprintf("colour value %s appears where a token name belongs, and a widget writes a token name only", diag.Quote(token.Text)),
			fmt.Sprintf("write one of the seven token names — %s — and let the palette own the value; there is no inline colour and no `var(--…)` escape hatch", diag.Enumerate(ir.TokenNames(), token.Text)),
		))
		return argumentValue{token: token, canonical: true}
	}
	return documentValidator.matchEnumeration(spec, argument, token, diag.ClassTokenNameUnknown, ir.TokenNames())
}

func (documentValidator *validator) matchIdentifier(spec clauseSpec, argument argumentSpec, token lex.Token) argumentValue {
	// A declaration's own name reaches this reader without passing the
	// statement-level canonical-form check, so the mermaid spelling an author is
	// most likely to reach for on a node is recognised here too.
	if token.Kind == lex.KindShapeBracket {
		documentValidator.reportShapeBracket(token)
		return argumentValue{token: token, canonical: true}
	}
	if token.Kind == lex.KindString {
		if argument.reference == kindLabel {
			documentValidator.reportLiteralWhereLabelExpected(spec, token)
			return argumentValue{token: token, canonical: true}
		}
		documentValidator.reportArgumentShape(spec, fmt.Sprintf("the %s argument is an identifier, not a quoted string", argument.name), token.At)
		return argumentValue{token: token}
	}
	if token.Kind != lex.KindWord {
		documentValidator.reportArgumentShape(spec, fmt.Sprintf("the %s argument is an identifier", argument.name), token.At)
		return argumentValue{token: token}
	}
	if !documentValidator.checkIdentifierCase(token, argument.nameCase) {
		return argumentValue{token: token}
	}
	return argumentValue{token: token, text: token.Text, sound: true}
}

func (documentValidator *validator) reportLiteralWhereLabelExpected(spec clauseSpec, token lex.Token) {
	suggested := suggestedLabelName(spec.keyword)
	documentValidator.report(diag.New(
		diag.ClassLiteralWhereLabelExpected,
		token.At,
		fmt.Sprintf("string literal %s appears where a label reference belongs, and text lives in the `labels` block and nowhere else", diag.Quote(token.Text)),
		fmt.Sprintf("add `label %s text %s` to the `labels` block and write `%s %s` here", suggested, diag.Quote(token.Text), spec.keyword, suggested),
	))
}

func (documentValidator *validator) reportTimeUnitSigil(token lex.Token) {
	documentValidator.report(diag.New(
		diag.ClassTimeUnitSigil,
		token.At,
		fmt.Sprintf("time value `%s` carries a unit sigil, and time carries its unit as a word", token.Text),
		fmt.Sprintf("write `%s milliseconds`", milliseconds(token.Text)),
	))
}

func (documentValidator *validator) reportArgumentShape(spec clauseSpec, wrong string, at diag.SourcePosition) {
	documentValidator.report(diag.New(
		diag.ClassClauseArgumentsMalformed,
		at,
		fmt.Sprintf("clause `%s` is written `%s`, and %s", spec.keyword, describeClause(spec), wrong),
		fmt.Sprintf("write the clause as `%s`", describeClause(spec)),
	))
}

func (documentValidator *validator) reportUnknownKeyword(statement parse.Statement, context clauseContext) {
	keyword := statement.Keyword.Text
	if replacement, dropped := parse.DroppedMermaidKeyword(keyword); dropped {
		documentValidator.report(diag.New(
			diag.ClassMermaidKeyword,
			statement.At(),
			fmt.Sprintf("mermaid keyword `%s` appears, and the dialect dropped it", keyword),
			fmt.Sprintf("write %s instead", replacement),
		))
		return
	}
	documentValidator.report(diag.New(
		diag.ClassStatementKeywordUnknown,
		statement.At(),
		fmt.Sprintf("statement keyword %s is not one %s accepts", diag.Quote(keyword), context.container),
		fmt.Sprintf("use one of the keywords %s accepts: %s", context.container, diag.Enumerate(context.accepted, keyword)),
	))
}

// reportCanonicalForm reports the mermaid spellings the dialect dropped, and
// tells the caller to stop reading a statement it has already explained.
func (documentValidator *validator) reportCanonicalForm(statement parse.Statement) bool {
	tokens := append([]lex.Token{statement.Keyword}, statement.Arguments...)
	for index, token := range tokens {
		switch token.Kind {
		case lex.KindMermaidEdge:
			documentValidator.reportMermaidEdge(tokens, index)
			return true
		case lex.KindShapeBracket:
			documentValidator.reportShapeBracket(token)
			return true
		}
	}
	return false
}

func (documentValidator *validator) reportShapeBracket(token lex.Token) {
	documentValidator.report(diag.New(
		diag.ClassMermaidShapeBracket,
		token.At,
		fmt.Sprintf("node shape bracket `%s` appears, and a node's appearance comes from its `role`", bracketOf(token.Text)),
		fmt.Sprintf("delete the bracket and write `node %s` with a `role` clause naming one of the declared roles: %s",
			identifierOf(token.Text), diag.Enumerate(documentValidator.symbols.namesOfKind(kindRole), identifierOf(token.Text))),
	))
}

func (documentValidator *validator) reportMermaidEdge(tokens []lex.Token, index int) {
	from, to := "nodeA", "nodeB"
	if index > 0 && tokens[index-1].Kind == lex.KindWord {
		from = tokens[index-1].Text
	}
	if index+1 < len(tokens) && tokens[index+1].Kind == lex.KindWord {
		to = tokens[index+1].Text
	}
	name := from + "To" + strings.ToUpper(to[:1]) + to[1:]
	documentValidator.report(diag.New(
		diag.ClassMermaidEdgeOperator,
		tokens[index].At,
		fmt.Sprintf("mermaid edge operator `%s` appears, and the dialect has one edge form, which carries an identifier", tokens[index].Text),
		fmt.Sprintf("write the edge as a block: `edge %s` / `from %s` / `to %s` / `carries <channel>` / `end`", name, from, to),
	))
}

func (documentValidator *validator) checkIdentifierCase(token lex.Token, rule caseRule) bool {
	if matchesCase(token.Text, rule) {
		return true
	}
	documentValidator.report(diag.New(
		diag.ClassIdentifierCase,
		token.At,
		fmt.Sprintf("identifier %s is not %s, which this position requires", diag.Quote(token.Text), caseName(rule)),
		fmt.Sprintf("rewrite it as `%s`", rewriteCase(token.Text, rule)),
	))
	return false
}

func findClauseSpec(specs []clauseSpec, keyword string) (clauseSpec, bool) {
	for _, spec := range specs {
		if spec.keyword == keyword {
			return spec, true
		}
	}
	return clauseSpec{}, false
}

func describeClause(spec clauseSpec) string {
	parts := make([]string, 0, len(spec.arguments)+1)
	parts = append(parts, spec.keyword)
	for _, argument := range spec.arguments {
		parts = append(parts, describeArgument(argument))
	}
	return strings.Join(parts, " ")
}

func describeArgument(argument argumentSpec) string {
	switch argument.kind {
	case argumentKeyword:
		return argument.name
	case argumentString:
		return fmt.Sprintf("\"<%s>\"", argument.name)
	case argumentInteger:
		return fmt.Sprintf("<%s>", argument.name)
	case argumentEnum:
		return "<" + strings.Join(argument.values, "|") + ">"
	case argumentSignal:
		return "<" + strings.Join(signalNames, "|") + ">"
	case argumentToken:
		return "<token>"
	default:
		return fmt.Sprintf("<%s>", strings.ReplaceAll(argument.name, " ", "-"))
	}
}

func clampExample(argument argumentSpec) int {
	if argument.minimum > 0 {
		return argument.minimum
	}
	if argument.maximum < 50 {
		return argument.maximum
	}
	return 50
}

func suggestedLabelName(keyword string) string {
	return keyword + "Label"
}

// milliseconds converts a unit-suffixed time value to the integer millisecond
// count the dialect spells, so W506's fix names the exact replacement.
func milliseconds(text string) string {
	digits := strings.TrimRight(text, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	unit := strings.ToLower(text[len(digits):])
	value, conversionError := strconv.ParseFloat(digits, 64)
	if conversionError != nil {
		return digits
	}
	if unit == "s" || unit == "sec" || unit == "secs" {
		value *= 1000
	}
	return strconv.FormatInt(int64(value+0.5), 10)
}

func bracketOf(text string) string {
	for index, character := range text {
		if strings.ContainsRune("[({>", character) {
			return text[index:]
		}
	}
	return text
}

func identifierOf(text string) string {
	for index, character := range text {
		if strings.ContainsRune("[({>", character) {
			return text[:index]
		}
	}
	return text
}

func pluralize(word string, count int) string {
	if count == 1 {
		return word
	}
	return word + "s"
}
