package parse

// The block table. It is the structural half of the grammar — the fourteen
// block names, the canonical order that is also the resolution order, which
// blocks are required, and which declaration keyword opens a nested block
// inside each. The semantic half — clauses, enumerations, ranges and reference
// kinds — belongs to the validator, which reads this table rather than
// restating it.

// BlockSpec describes one of the fourteen blocks.
type BlockSpec struct {
	// Name is the block keyword.
	Name string
	// Ordinal is the block's position in the canonical order, from 1.
	Ordinal int
	// Required is true for the six blocks every document declares.
	Required bool
	// Named is true for the one block whose header carries an identifier.
	Named bool
	// Openers are the declaration keywords that open a nested block here.
	Openers []string
}

var blockSpecs = []BlockSpec{
	{Name: "state", Ordinal: 1, Required: true},
	{Name: "predicates", Ordinal: 2, Openers: []string{"predicate"}},
	{Name: "bindings", Ordinal: 3, Openers: []string{"binding"}},
	{Name: "labels", Ordinal: 4, Required: true},
	{Name: "chrome", Ordinal: 5, Required: true},
	{Name: "roles", Ordinal: 6, Required: true, Openers: []string{"role"}},
	{Name: "channels", Ordinal: 7, Openers: []string{"channel"}},
	{Name: "placements", Ordinal: 8, Required: true},
	{Name: "scene", Ordinal: 9, Required: true, Named: true, Openers: []string{"node", "edge"}},
	{Name: "motion", Ordinal: 10, Openers: []string{"pulse", "emphasis"}},
	{Name: "indicators", Ordinal: 11, Openers: []string{"indicator"}},
	{Name: "controls", Ordinal: 12, Openers: []string{"control"}},
	{Name: "events", Ordinal: 13, Openers: []string{"event"}},
	{Name: "data", Ordinal: 14, Openers: []string{"stream"}},
}

// BlockSpecs returns the fourteen blocks in canonical order.
func BlockSpecs() []BlockSpec {
	specs := make([]BlockSpec, len(blockSpecs))
	copy(specs, blockSpecs)
	return specs
}

// BlockNames returns the fourteen block names in canonical order, for a message
// that has to enumerate them.
func BlockNames() []string {
	names := make([]string, 0, len(blockSpecs))
	for _, spec := range blockSpecs {
		names = append(names, spec.Name)
	}
	return names
}

// LookupBlock returns the spec for a block name.
func LookupBlock(name string) (BlockSpec, bool) {
	for _, spec := range blockSpecs {
		if spec.Name == name {
			return spec, true
		}
	}
	return BlockSpec{}, false
}

func (spec BlockSpec) opens(keyword string) bool {
	for _, opener := range spec.Openers {
		if opener == keyword {
			return true
		}
	}
	return false
}

// PreambleKeywords are the four document-scope directives, in their required
// order.
var PreambleKeywords = []string{"widget", "dialect", "region", "palette"}

func isPreambleKeyword(keyword string) bool {
	for _, directive := range PreambleKeywords {
		if directive == keyword {
			return true
		}
	}
	return false
}

// droppedMermaidKeywords maps each mermaid keyword the dialect dropped to the
// construct that replaced it. W503's message is built from this table, so a
// keyword cannot be recognised without also naming its replacement.
var droppedMermaidKeywords = map[string]string{
	"direction": "nothing — the scene is absolutely placed, so a direction would be a keyword that does nothing",
	"classDef":  "a `role` or a `channel`, which own appearance",
	"class":     "a `role` or a `channel`, which own appearance",
	"style":     "a `role` or a `channel`, which own appearance",
	"subgraph":  "the `scene` block, closed by `end`",
	"linkStyle": "a `channel`, which owns an edge's appearance",
}

// DroppedMermaidKeyword reports whether a keyword is one of the mermaid
// keywords the dialect dropped, and names the construct that replaced it.
func DroppedMermaidKeyword(keyword string) (string, bool) {
	replacement, dropped := droppedMermaidKeywords[keyword]
	return replacement, dropped
}

// statementKeywords is every keyword that begins a statement inside some block.
// It exists so a statement written at document scope is reported as misplaced
// (W010) rather than as an unknown block name (W004).
var statementKeywords = map[string]struct{}{
	"field": {}, "predicate": {}, "binding": {}, "label": {}, "title": {},
	"source": {}, "stat": {}, "description": {}, "role": {}, "channel": {},
	"placement": {}, "orbit": {}, "node": {}, "edge": {}, "pulse": {},
	"emphasis": {}, "indicator": {}, "control": {}, "event": {}, "stream": {},
	"type": {}, "signal": {}, "requires": {}, "forbids": {}, "when": {},
	"whenNot": {}, "otherwise": {}, "text": {}, "binds": {}, "token": {},
	"marker": {}, "legend": {}, "at": {}, "caption": {}, "from": {}, "to": {},
	"carries": {}, "restartOn": {}, "duration": {}, "delay": {},
	"positiveWhen": {}, "pressedWhen": {}, "trigger": {}, "emits": {},
	"wire": {}, "toggles": {}, "writes": {}, "delivers": {}, "ordering": {},
}

// IsStatementKeyword reports whether a keyword begins a statement inside some
// block of the dialect.
func IsStatementKeyword(keyword string) bool {
	_, known := statementKeywords[keyword]
	return known
}
