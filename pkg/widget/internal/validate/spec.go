package validate

import "github.com/candacelabs/csf/pkg/widget/internal/ir"

// The semantic half of the grammar: what each declaration is spelled with.
// parse owns the block table; this table owns clauses, their argument shapes,
// their enumerations and their ranges. Everything the clause reader reports —
// an unknown keyword, a missing clause, a repeated one, a value outside a
// closed set, an integer outside its range — is decided from this data, so a
// construct is added by adding a row rather than by writing another loop.

type argumentKind int

const (
	// argumentIdentifier is a name: a declaration's own, or a reference to one.
	argumentIdentifier argumentKind = iota
	// argumentString is a quoted literal.
	argumentString
	// argumentInteger is a decimal integer, checked against a range.
	argumentInteger
	// argumentEnum is one word from a closed set.
	argumentEnum
	// argumentToken is one of the seven semantic token names.
	argumentToken
	// argumentSignal is one of the runtime-minted signal names.
	argumentSignal
	// argumentKeyword is a fixed word inside a clause, such as `then`.
	argumentKeyword
)

type caseRule int

const (
	caseLowerCamel caseRule = iota
	casePascal
	caseLowerSnake
)

type argumentSpec struct {
	kind argumentKind
	// name labels the argument in a message: "left", "duration", "then".
	name string
	// values enumerates an argumentEnum's members.
	values []string
	// minimum and maximum bound an argumentInteger, inclusive.
	minimum int
	maximum int
	// reference names the kind an argumentIdentifier must resolve to. An empty
	// reference marks the identifier as a declaration's own name.
	reference symbolKind
	// nameCase is the convention an argumentIdentifier's spelling must obey.
	nameCase caseRule
	// optional marks a trailing argument group: absent is legal, and present
	// obliges every later argument of the clause.
	optional bool
}

type clauseSpec struct {
	keyword    string
	arguments  []argumentSpec
	required   bool
	repeatable bool
}

// declarationSpec is one named construct: a line declaration whose clauses
// follow on the same line, or a block declaration whose clauses are its member
// lines.
type declarationSpec struct {
	keyword  string
	kind     symbolKind
	nameCase caseRule
	clauses  []clauseSpec
}

func (spec declarationSpec) keywords() []string {
	names := make([]string, 0, len(spec.clauses))
	for _, clause := range spec.clauses {
		names = append(names, clause.keyword)
	}
	return names
}

const unboundedInteger = 1 << 30

func identifierArgument(name string, reference symbolKind) argumentSpec {
	return argumentSpec{kind: argumentIdentifier, name: name, reference: reference}
}

func enumArgument(name string, values []string) argumentSpec {
	return argumentSpec{kind: argumentEnum, name: name, values: values}
}

func integerArgument(name string, minimum int, maximum int) argumentSpec {
	return argumentSpec{kind: argumentInteger, name: name, minimum: minimum, maximum: maximum}
}

func keywordArgument(name string) argumentSpec {
	return argumentSpec{kind: argumentKeyword, name: name}
}

func millisecondsClause(keyword string, required bool, minimum int) clauseSpec {
	return clauseSpec{
		keyword:  keyword,
		required: required,
		arguments: []argumentSpec{
			integerArgument(keyword, minimum, unboundedInteger),
			keywordArgument("milliseconds"),
		},
	}
}

// The line declarations: a name and its clauses on one line.
var (
	fieldDeclaration = declarationSpec{
		keyword: "field", kind: kindStateField,
		clauses: []clauseSpec{
			{keyword: "type", required: true, arguments: []argumentSpec{enumArgument("type", ir.FieldTypes())}},
			{keyword: "signal", arguments: []argumentSpec{{kind: argumentSignal, name: "signal"}}},
		},
	}
	labelDeclaration = declarationSpec{
		keyword: "label", kind: kindLabel,
		clauses: []clauseSpec{
			{keyword: "text", arguments: []argumentSpec{{kind: argumentString, name: "text"}}},
			{keyword: "binds", arguments: []argumentSpec{identifierArgument("binding", kindBinding)}},
		},
	}
	placementDeclaration = declarationSpec{
		keyword: "placement", kind: kindPlacement,
		clauses: []clauseSpec{
			{keyword: "left", required: true, arguments: []argumentSpec{integerArgument("left", 0, 100)}},
			{keyword: "top", required: true, arguments: []argumentSpec{integerArgument("top", 0, 100)}},
		},
	}
	orbitDeclaration = declarationSpec{
		keyword: "orbit", kind: kindOrbit,
		clauses: []clauseSpec{
			{keyword: "token", required: true, arguments: []argumentSpec{{kind: argumentToken, name: "token"}}},
		},
	}
)

// The block declarations: a header naming the construct, then its clauses.
var (
	predicateDeclaration = declarationSpec{
		keyword: "predicate", kind: kindPredicate,
		clauses: []clauseSpec{
			{keyword: "requires", repeatable: true, arguments: []argumentSpec{
				identifierArgument("predicate", kindPredicate),
				{kind: argumentEnum, name: "comparison", values: comparisonNames, optional: true},
				{kind: argumentInteger, name: "bound", minimum: -unboundedInteger, maximum: unboundedInteger, optional: true},
			}},
			{keyword: "forbids", repeatable: true, arguments: []argumentSpec{identifierArgument("predicate", kindPredicate)}},
		},
	}
	bindingDeclaration = declarationSpec{
		keyword: "binding", kind: kindBinding,
		clauses: []clauseSpec{
			{keyword: "when", repeatable: true, arguments: guardArguments},
			{keyword: "whenNot", repeatable: true, arguments: guardArguments},
			{keyword: "otherwise", arguments: []argumentSpec{{kind: argumentString, name: "text"}}},
		},
	}
	roleDeclaration = declarationSpec{
		keyword: "role", kind: kindRole,
		clauses: []clauseSpec{
			{keyword: "token", required: true, arguments: []argumentSpec{{kind: argumentToken, name: "token"}}},
			{keyword: "marker", required: true, arguments: []argumentSpec{enumArgument("marker", markerNames)}},
			{keyword: "emphasis", required: true, arguments: []argumentSpec{enumArgument("emphasis", emphasisNames)}},
		},
	}
	channelDeclaration = declarationSpec{
		keyword: "channel", kind: kindChannel,
		clauses: []clauseSpec{
			{keyword: "direction", required: true, arguments: []argumentSpec{enumArgument("direction", directionNames)}},
			{keyword: "token", required: true, arguments: []argumentSpec{{kind: argumentToken, name: "token"}}},
			{keyword: "legend", required: true, arguments: []argumentSpec{identifierArgument("label", kindLabel)}},
		},
	}
	nodeDeclaration = declarationSpec{
		keyword: "node", kind: kindNode,
		clauses: []clauseSpec{
			{keyword: "role", required: true, arguments: []argumentSpec{identifierArgument("role", kindRole)}},
			{keyword: "at", required: true, arguments: []argumentSpec{identifierArgument("placement", kindPlacement)}},
			{keyword: "title", required: true, arguments: []argumentSpec{identifierArgument("label", kindLabel)}},
			{keyword: "caption", arguments: []argumentSpec{identifierArgument("label", kindLabel)}},
		},
	}
	edgeDeclaration = declarationSpec{
		keyword: "edge", kind: kindEdge,
		clauses: []clauseSpec{
			{keyword: "from", required: true, arguments: []argumentSpec{identifierArgument("node", kindNode)}},
			{keyword: "to", required: true, arguments: []argumentSpec{identifierArgument("node", kindNode)}},
			{keyword: "carries", required: true, repeatable: true, arguments: []argumentSpec{identifierArgument("channel", kindChannel)}},
		},
	}
	pulseDeclaration = declarationSpec{
		keyword: "pulse", kind: kindPulse,
		clauses: []clauseSpec{
			{keyword: "edge", required: true, arguments: []argumentSpec{identifierArgument("edge", kindEdge)}},
			{keyword: "channel", required: true, arguments: []argumentSpec{identifierArgument("channel", kindChannel)}},
			millisecondsClause("duration", true, 1),
			millisecondsClause("delay", false, 0),
		},
	}
	emphasisDeclaration = declarationSpec{
		keyword: "emphasis", kind: kindEmphasis,
		clauses: []clauseSpec{
			{keyword: "node", required: true, arguments: []argumentSpec{identifierArgument("node", kindNode)}},
			millisecondsClause("duration", true, 1),
			millisecondsClause("delay", false, 0),
		},
	}
	indicatorDeclaration = declarationSpec{
		keyword: "indicator", kind: kindIndicator,
		clauses: []clauseSpec{
			{keyword: "label", required: true, arguments: []argumentSpec{identifierArgument("label", kindLabel)}},
			{keyword: "positiveWhen", required: true, arguments: []argumentSpec{identifierArgument("predicate", kindPredicate)}},
		},
	}
	controlDeclaration = declarationSpec{
		keyword: "control", kind: kindControl,
		clauses: []clauseSpec{
			{keyword: "caption", required: true, arguments: []argumentSpec{identifierArgument("label", kindLabel)}},
			{keyword: "trigger", required: true, arguments: []argumentSpec{enumArgument("trigger", ir.Triggers())}},
			{keyword: "emits", required: true, arguments: []argumentSpec{identifierArgument("event", kindEvent)}},
			{keyword: "pressedWhen", arguments: []argumentSpec{identifierArgument("predicate", kindPredicate)}},
		},
	}
	eventDeclaration = declarationSpec{
		keyword: "event", kind: kindEvent,
		clauses: []clauseSpec{
			{keyword: "wire", required: true, arguments: []argumentSpec{{kind: argumentString, name: "wire"}}},
			{keyword: "toggles", arguments: []argumentSpec{identifierArgument("state field", kindStateField)}},
			{keyword: "field", repeatable: true, arguments: []argumentSpec{
				{kind: argumentIdentifier, name: "wire field", nameCase: caseLowerSnake},
				keywordArgument("writes"),
				identifierArgument("state field", kindStateField),
			}},
		},
	}
	streamDeclaration = declarationSpec{
		keyword: "stream", kind: kindStream,
		clauses: []clauseSpec{
			{keyword: "source", required: true, arguments: []argumentSpec{{kind: argumentString, name: "source"}}},
			{keyword: "delivers", required: true, arguments: []argumentSpec{identifierArgument("event", kindEvent)}},
			{keyword: "ordering", arguments: []argumentSpec{identifierArgument("state field", kindStateField)}},
		},
	}
)

// The clauses that belong to a block rather than to a declaration.
var (
	chromeClauses = []clauseSpec{
		{keyword: "title", arguments: []argumentSpec{identifierArgument("label", kindLabel)}},
		{keyword: "source", arguments: []argumentSpec{identifierArgument("label", kindLabel)}},
		{keyword: "stat", repeatable: true, arguments: []argumentSpec{identifierArgument("label", kindLabel)}},
	}
	sceneClauses = []clauseSpec{
		{keyword: "description", arguments: []argumentSpec{identifierArgument("label", kindLabel)}},
	}
	motionClauses = []clauseSpec{
		{keyword: "requires", repeatable: true, arguments: []argumentSpec{identifierArgument("predicate", kindPredicate)}},
		{keyword: "forbids", repeatable: true, arguments: []argumentSpec{identifierArgument("predicate", kindPredicate)}},
		{keyword: "restartOn", arguments: []argumentSpec{identifierArgument("state field", kindStateField)}},
	}
)

var guardArguments = []argumentSpec{
	identifierArgument("predicate", kindPredicate),
	keywordArgument("then"),
	{kind: argumentString, name: "text"},
}

var (
	comparisonNames = []string{string(ir.ComparisonAtLeast), string(ir.ComparisonAtMost)}
	markerNames     = []string{string(ir.MarkerLarge), string(ir.MarkerSmall)}
	emphasisNames   = []string{"allowed", "forbidden"}
	directionNames  = []string{string(ir.DirectionForward), string(ir.DirectionReverse)}
	signalNames     = []string{ir.SignalSlowClient}
)

// runtimeMintedEvents are the wire names the live runtime synthesizes itself.
// None is browser-sendable, so none may be declared: a widget reaches the
// slow-client signal through a `signal` field instead.
var runtimeMintedEvents = []string{"gotth.effect_failed", "timer:slow_client", "timer:client_recovered"}

// cssColourNames are the colour words most likely to be reached for where a
// token name belongs. The list only has to be good enough to recognise the
// mistake: an unrecognised colour word still fails as an unknown token name.
var cssColourNames = map[string]struct{}{
	"aqua": {}, "black": {}, "blue": {}, "brown": {}, "coral": {}, "crimson": {},
	"cyan": {}, "fuchsia": {}, "gold": {}, "gray": {}, "green": {}, "grey": {},
	"indigo": {}, "ivory": {}, "khaki": {}, "lime": {}, "magenta": {},
	"maroon": {}, "navy": {}, "olive": {}, "orange": {}, "orchid": {},
	"pink": {}, "plum": {}, "purple": {}, "red": {}, "salmon": {}, "silver": {},
	"tan": {}, "teal": {}, "tomato": {}, "violet": {}, "white": {}, "yellow": {},
}
