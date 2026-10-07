package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
	"github.com/candacelabs/csf/pkg/widget/internal/ir"
)

// The whole-document pass: the relations no single statement can be wrong about
// on its own. Each anchors at the declaration of the thing it is about — never
// at the end of the file, never at a block header — because that is where the
// author has to go.
//
// The pass is a registry rather than a call list. Each check is a named
// function value that takes the whole document as one read-only value and
// returns its findings; wholeDocumentInvariants is the family as data, each row
// carrying the error classes its check reports; the sweep iterates the rows and
// appends. A call list of sibling methods is correct on the day it is written
// and nothing ever says when it stops being, because a method set cannot be
// enumerated by a spec: no test can ask whether every invariant is still wired
// in, or whether two of them claim one class. invariants_internal_test.go asks
// the registry both.

// declaredSymbol is one declaration as an invariant sees it: what it was
// declared as, where its line starts, whether the build pass made a record for
// it, and whether anything named it. It is a copy taken when the sweep starts,
// so a check cannot reach back into the symbol table, and cannot change what a
// later check reads.
type declaredSymbol struct {
	name string
	kind symbolKind
	// lineAt anchors the declaration's first token, which is where a
	// whole-document invariant about the declaration points (A3).
	lineAt diag.SourcePosition
	// resolved is whether the build pass made an IR record for the declaration.
	// A declaration that resolved to nothing is already reported by the pass
	// that failed to build it, and reporting it again would report a
	// consequence of that finding as a finding of its own.
	resolved   bool
	referenced bool
}

// documentView is the whole document as the sweep reads it: the built IR and a
// snapshot of the declaration table. It carries nothing that reports, mutates
// or accumulates, which is what lets every check be a pure function of it.
type documentView struct {
	document     *ir.Document
	declarations []declaredSymbol
}

// invariantCheck is one whole-document check: the view in, its findings out,
// nothing shared with the validator or with another check.
type invariantCheck func(view documentView) []diag.Finding

// registeredInvariant is one row of the sweep: the check, the name a spec
// failure prints, and the error classes the check reports — the metadata a
// method had nowhere to carry.
type registeredInvariant struct {
	name  string
	owns  []diag.Class
	check invariantCheck
}

// wholeDocumentInvariants is the sweep. Adding an invariant means adding a row;
// a row that claims a class its check never reports, or one another row already
// owns, fails a spec rather than becoming a bug filed months later.
//
// owns names what the check reports, not who else in the interpreter may report
// the same class. W303 is also reported per block by the build pass, which is
// the same class read of a different container; the sweep's reading of it is
// the scene, whose nodes and edges are only whole once the document is.
var wholeDocumentInvariants = []registeredInvariant{
	{
		name:  "state-field-writers",
		owns:  []diag.Class{diag.ClassStateFieldUnwritten},
		check: checkStateFieldWriters,
	},
	{
		name:  "declaration-references",
		owns:  []diag.Class{diag.ClassDeclarationUnreferenced},
		check: checkUnreferencedDeclarations,
	},
	{
		name:  "predicate-cycles",
		owns:  []diag.Class{diag.ClassPredicateCycle},
		check: checkPredicateCycles,
	},
	{
		name:  "binding-clause-reachability",
		owns:  []diag.Class{diag.ClassBindingClauseUnreachable},
		check: checkBindingReachability,
	},
	{
		name:  "scene-cardinality",
		owns:  []diag.Class{diag.ClassContainerUnderfilled},
		check: checkSceneCardinality,
	},
	{
		name:  "scene-description-bound",
		owns:  []diag.Class{diag.ClassDescriptionNotBound},
		check: checkDescriptionIsBound,
	},
}

// sweepClasses is every error class the registry claims, in registration order.
// It is what the completeness spec asserts against, and it is the thing a
// method set has no equivalent of.
func sweepClasses() []diag.Class {
	classes := make([]diag.Class, 0, len(wholeDocumentInvariants))
	for _, registered := range wholeDocumentInvariants {
		classes = append(classes, registered.owns...)
	}
	return classes
}

// checkInvariants runs the registry over one snapshot of the document. It is
// the only place the sweep meets the validator: the view is taken once, and
// every finding arrives as a return value.
func (documentValidator *validator) checkInvariants() {
	view := documentValidator.view()
	for _, registered := range wholeDocumentInvariants {
		for _, finding := range registered.check(view) {
			documentValidator.report(finding)
		}
	}
}

// view snapshots exactly what the sweep is allowed to read.
func (documentValidator *validator) view() documentView {
	declarations := make([]declaredSymbol, 0, len(documentValidator.symbols.order))
	for _, declared := range documentValidator.symbols.order {
		declarations = append(declarations, declaredSymbol{
			name:       declared.name,
			kind:       declared.kind,
			lineAt:     declared.lineAt,
			resolved:   declared.record != nil,
			referenced: documentValidator.referenced[declared.name],
		})
	}
	return documentView{document: documentValidator.document, declarations: declarations}
}

// checkStateFieldWriters reports state nothing can reach: a field written by
// nothing renders one value forever.
func checkStateFieldWriters(view documentView) []diag.Finding {
	findings := make([]diag.Finding, 0, len(view.document.StateFields))
	for _, field := range view.document.StateFields {
		if len(field.Writers) > 0 {
			continue
		}
		findings = append(findings, diag.New(
			diag.ClassStateFieldUnwritten,
			field.Span.Start(),
			fmt.Sprintf("state field %s has no writer, and a field written by nothing renders one value forever", diag.Quote(field.Name)),
			fmt.Sprintf("write it from an event with `field <wire_name> writes %s`, flip it with `toggles %s` in an event, or bind it to the runtime with `signal %s` — which is right depends on where the value comes from", field.Name, field.Name, ir.SignalSlowClient),
		))
	}
	return findings
}

// referenceSites names, for each kind that must be used, what using it means
// and where the reference goes — so W410 says "carried by no edge" rather than
// "unused", and its fix names the line to write.
type referenceSite struct {
	unused string
	site   string
}

var referenceSites = map[symbolKind]referenceSite{
	kindLabel:     {"fills no slot", "a `title`, `source`, `stat` or `description` line, a node's `title` or `caption`, a channel's `legend`, a control's `caption`, or an indicator's `label`"},
	kindBinding:   {"is named by no label", "a `label <id> binds <binding>` line"},
	kindRole:      {"classifies no node", "a node's `role` clause"},
	kindChannel:   {"is carried by no edge", "an edge's `carries` clause"},
	kindPlacement: {"is named by no node", "a node's `at` clause"},
	kindEvent:     {"is named by no control and no stream", "a control's `emits` clause or a stream's `delivers` clause"},
	kindPredicate: {"is referenced by nothing", "a binding guard, the `motion` gate, an indicator's `positiveWhen`, or a control's `pressedWhen`"},
}

// checkUnreferencedDeclarations reports vocabulary nothing names. Unreferenced
// vocabulary is the shape drift arrives in.
func checkUnreferencedDeclarations(view documentView) []diag.Finding {
	findings := make([]diag.Finding, 0, len(view.declarations))
	for _, declared := range view.declarations {
		site, checked := referenceSites[declared.kind]
		if !checked || declared.referenced {
			continue
		}
		if !declared.resolved {
			continue
		}
		findings = append(findings, diag.New(
			diag.ClassDeclarationUnreferenced,
			declared.lineAt,
			fmt.Sprintf("%s %s %s", declared.kind, diag.Quote(declared.name), site.unused),
			fmt.Sprintf("name it from %s, or delete the declaration; unreferenced vocabulary is the shape drift arrives in", site.site),
		))
	}
	return findings
}

// checkPredicateCycles reports every cycle of the predicate reference graph
// once, anchored at the declaration of its lexically first member — the member
// whose declaration comes first in the document — and listing the cycle from
// that member onwards, so one cycle always reports in one place.
func checkPredicateCycles(view documentView) []diag.Finding {
	walk := &predicateWalk{
		onPath:   map[*ir.Predicate]bool{},
		settled:  map[*ir.Predicate]bool{},
		reported: map[string]bool{},
	}
	findings := []diag.Finding{}
	for _, predicate := range view.document.Predicates {
		if predicate.Kind != ir.PredicateComposed {
			continue
		}
		found, _ := walk.visit(predicate, []*ir.Predicate{})
		findings = append(findings, found...)
	}
	return findings
}

// predicateWalk is one search over one document's predicate graph: the path it
// is currently on, the subgraphs it has finished and found clean, and the
// cycles it has already reported. It is made where the walk starts and shared
// with nothing.
type predicateWalk struct {
	// onPath marks the predicates between the search's root and where it is
	// now. A reference back into that set is a cycle.
	onPath map[*ir.Predicate]bool
	// settled marks a predicate from which no cycle is reachable at all. It is
	// what keeps the walk linear: without it the search enumerates every simple
	// path, which on a cycle-free graph where each composition names two others
	// is 2^depth — measured at 18 s for 27 levels, and the document that shape
	// describes is 340 lines a person could write.
	settled map[*ir.Predicate]bool
	// reported keys each cycle by its rotated member names, so one cycle
	// reached through two entry points is reported once.
	reported map[string]bool
}

// visit walks one predicate's subgraph. It returns that subgraph's findings and
// whether the subgraph is clean — whether the search reached no cycle anywhere
// below this predicate.
//
// Only a clean predicate is settled, and that is what makes the memo report
// exactly the cycles the exhaustive walk reported. A cycle reachable from a
// predicate must close on the path the search is standing on, so a predicate
// that reached no such closure can reach no cycle from any other path either,
// and skipping it a second time loses nothing. A predicate that DID reach one
// is never settled, so every path through it is still enumerated.
func (walk *predicateWalk) visit(predicate *ir.Predicate, path []*ir.Predicate) ([]diag.Finding, bool) {
	if walk.onPath[predicate] {
		return cycleFindings(cycleFrom(path, predicate), walk.reported), false
	}
	if walk.settled[predicate] {
		return nil, true
	}
	walk.onPath[predicate] = true
	path = append(path, predicate)
	findings := []diag.Finding{}
	clean := true
	for _, referenced := range referencedPredicates(predicate) {
		if referenced.Kind != ir.PredicateComposed {
			continue
		}
		found, subgraphClean := walk.visit(referenced, path)
		findings = append(findings, found...)
		clean = clean && subgraphClean
	}
	walk.onPath[predicate] = false
	if clean {
		walk.settled[predicate] = true
	}
	return findings, clean
}

// referencedPredicates is what one composition names, requires before forbids —
// the order the walk descends in, and therefore the order a cycle is listed in.
func referencedPredicates(predicate *ir.Predicate) []*ir.Predicate {
	referenced := make([]*ir.Predicate, 0, len(predicate.Requires)+len(predicate.Forbids))
	referenced = append(referenced, predicate.Requires...)
	return append(referenced, predicate.Forbids...)
}

func cycleFrom(path []*ir.Predicate, closing *ir.Predicate) []*ir.Predicate {
	for index, member := range path {
		if member == closing {
			return append([]*ir.Predicate{}, path[index:]...)
		}
	}
	return append(append([]*ir.Predicate{}, path...), closing)
}

// cycleFindings reports one cycle, or nothing when the same cycle has already
// been reported through another entry point. reported is the walk's own memo,
// made where the walk starts and never shared with another check.
func cycleFindings(cycle []*ir.Predicate, reported map[string]bool) []diag.Finding {
	rotated := rotateToLexicallyFirst(cycle)
	names := make([]string, 0, len(rotated))
	for _, member := range rotated {
		names = append(names, member.Name)
	}
	key := strings.Join(names, "\x00")
	if reported[key] {
		return nil
	}
	reported[key] = true
	return []diag.Finding{diag.New(
		diag.ClassPredicateCycle,
		rotated[0].Span.Start(),
		fmt.Sprintf("the predicate reference graph has a cycle: %s, and the graph is acyclic", strings.Join(append(names, names[0]), " → ")),
		fmt.Sprintf("break the cycle by removing one clause, for example the clause of predicate `%s` that names `%s`", names[len(names)-1], names[0]),
	)}
}

func rotateToLexicallyFirst(cycle []*ir.Predicate) []*ir.Predicate {
	first := 0
	for index, member := range cycle {
		if before(member.Span.Start(), cycle[first].Span.Start()) {
			first = index
		}
	}
	return append(append([]*ir.Predicate{}, cycle[first:]...), cycle[:first]...)
}

func before(first diag.SourcePosition, second diag.SourcePosition) bool {
	if first.Line != second.Line {
		return first.Line < second.Line
	}
	return first.Column < second.Column
}

// checkBindingReachability reports a clause no state can reach: a guard a
// clause above it already covers. The check is the decidable subset — a
// duplicate guard, or a guard whose requires and forbids sets are supersets of
// an earlier guard's — and no general satisfiability is attempted.
func checkBindingReachability(view documentView) []diag.Finding {
	findings := []diag.Finding{}
	for _, binding := range view.document.Bindings {
		constraints := make([][]string, len(binding.Clauses))
		for index, clause := range binding.Clauses {
			constraints[index] = guardConstraints(clause)
		}
		for later := range binding.Clauses {
			findings = append(findings, clauseReachability(binding, constraints, later)...)
		}
	}
	return findings
}

// clauseReachability reports the first earlier clause that covers this one, and
// stops there: a clause subsumed by two earlier guards is one dead clause, not
// two findings.
func clauseReachability(binding *ir.Binding, constraints [][]string, later int) []diag.Finding {
	if constraints[later] == nil {
		return nil
	}
	for earlier := 0; earlier < later; earlier++ {
		if constraints[earlier] == nil || !covers(constraints[earlier], constraints[later]) {
			continue
		}
		return []diag.Finding{diag.New(
			diag.ClassBindingClauseUnreachable,
			binding.Clauses[later].Span.Start(),
			fmt.Sprintf("clause %d of binding %s guards on `%s`, which clause %d's guard `%s` already covers, so it is dead text",
				later+1, diag.Quote(binding.Name), guardName(binding.Clauses[later]), earlier+1, guardName(binding.Clauses[earlier])),
			fmt.Sprintf("reorder the two clauses so `%s` is tested first, or weaken the later guard", guardName(binding.Clauses[later])),
		).WithRelated(binding.Clauses[earlier].Span.Start(), fmt.Sprintf("clause %d guards on `%s` here", earlier+1, guardName(binding.Clauses[earlier])))}
	}
	return nil
}

func guardName(clause *ir.BindingClause) string {
	if clause.Predicate == nil {
		return "?"
	}
	if clause.Polarity == ir.GuardWhenNot {
		return "whenNot " + clause.Predicate.Name
	}
	return clause.Predicate.Name
}

// guardConstraints renders a guard as the set of atoms every matching state
// satisfies. A negated composition is a disjunction, which this representation
// cannot carry, so it returns nil and the clause makes no claim.
func guardConstraints(clause *ir.BindingClause) []string {
	predicate := clause.Predicate
	if predicate == nil {
		return nil
	}
	if predicate.Kind == ir.PredicateAtomic {
		return []string{sign(clause.Polarity == ir.GuardWhen) + predicate.Name}
	}
	if clause.Polarity == ir.GuardWhenNot {
		return nil
	}
	atoms := []string{}
	for _, required := range predicate.Requires {
		atoms = append(atoms, "+"+required.Name)
	}
	for _, forbidden := range predicate.Forbids {
		atoms = append(atoms, "-"+forbidden.Name)
	}
	for _, bound := range predicate.Bounds {
		atoms = append(atoms, fmt.Sprintf("%s %s %d", bound.Field.Name, bound.Comparison, bound.Value))
	}
	if len(atoms) == 0 {
		return nil
	}
	sort.Strings(atoms)
	return atoms
}

func sign(positive bool) string {
	if positive {
		return "+"
	}
	return "-"
}

// covers reports whether every state matching the earlier guard's atoms also
// matches the later guard's: the later set is a superset, so the later clause
// can never be the first match.
func covers(earlier []string, later []string) bool {
	present := make(map[string]bool, len(later))
	for _, atom := range later {
		present[atom] = true
	}
	for _, atom := range earlier {
		if !present[atom] {
			return false
		}
	}
	return true
}

// checkSceneCardinality reports a scene whose edges have too few nodes to join.
func checkSceneCardinality(view documentView) []diag.Finding {
	scene := view.document.Scene
	if scene == nil || len(scene.Edges) == 0 || len(scene.Nodes) >= 2 {
		return nil
	}
	return []diag.Finding{diag.New(
		diag.ClassContainerUnderfilled,
		scene.Span.Start(),
		fmt.Sprintf("scene %s carries %d %s and %d %s, and a scene containing an edge contains at least 2 nodes",
			diag.Quote(scene.Name), len(scene.Edges), pluralize("edge", len(scene.Edges)), len(scene.Nodes), pluralize("node", len(scene.Nodes))),
		"add the missing `node <id>` declaration, with its `role`, `at` and `title` clauses, or delete the edge",
	)}
}

// checkDescriptionIsBound reports a fixed sentence describing a picture that
// changes: it is wrong the moment it changes.
func checkDescriptionIsBound(view documentView) []diag.Finding {
	scene := view.document.Scene
	if scene == nil || scene.DescriptionSlot == nil || scene.DescriptionSlot.Label == nil {
		return nil
	}
	label := scene.DescriptionSlot.Label
	if label.SourceKind != ir.LabelLiteral || len(view.document.Predicates) == 0 {
		return nil
	}
	return []diag.Finding{diag.New(
		diag.ClassDescriptionNotBound,
		scene.DescriptionSlot.Span.Start(),
		fmt.Sprintf("the scene's description names literal label %s while the widget declares %d %s, and a fixed sentence describing a picture that changes is wrong the moment it changes",
			diag.Quote(label.Name), len(view.document.Predicates), pluralize("predicate", len(view.document.Predicates))),
		fmt.Sprintf("bind the description: add `binding %sText` with one guarded clause per state and an `otherwise`, then write `label %s binds %sText`", label.Name, label.Name, label.Name),
	)}
}

// computeProjections derives the three records that are in the IR so a
// generator need not re-derive them, and absent from the grammar so an author
// cannot contradict them. Edge geometry is computed as each edge is built; the
// legend and the dirty projection are computed here, once the document is whole
// — the dirty projection last of all, because it reads the motion block.
func (documentValidator *validator) computeProjections() {
	document := documentValidator.document
	for _, channel := range document.Channels {
		document.Legend.Entries = append(document.Legend.Entries, ir.LegendEntry{Channel: channel, Label: channel.LegendLabel})
	}
	read := map[*ir.StateField]bool{}
	for _, predicate := range document.Predicates {
		collectPredicateFields(predicate, read, map[*ir.Predicate]bool{})
	}
	for _, binding := range document.Bindings {
		for _, clause := range binding.Clauses {
			collectTemplateFields(clause.Template, read)
		}
		collectTemplateFields(binding.Otherwise, read)
	}
	for _, label := range document.Labels {
		collectTemplateFields(label.Literal, read)
	}
	// The tick is read by the rendered output rather than by any text: a widget
	// with motion carries a per-tick identity in its markup, so that a finished
	// animation is a new element and starts again. A transition that moves only
	// the tick therefore moves the markup, and a projection that omitted it
	// would under-declare — the one direction that is a correctness bug rather
	// than a wasted render.
	if document.Motion != nil && document.Motion.RestartOn != nil {
		read[document.Motion.RestartOn] = true
	}
	for _, field := range document.StateFields {
		if read[field] {
			document.DirtyProjection.Fields = append(document.DirtyProjection.Fields, field)
		}
	}
}

func collectPredicateFields(predicate *ir.Predicate, read map[*ir.StateField]bool, visited map[*ir.Predicate]bool) {
	if predicate == nil || visited[predicate] {
		return
	}
	visited[predicate] = true
	if predicate.Field != nil {
		read[predicate.Field] = true
	}
	for _, required := range predicate.Requires {
		collectPredicateFields(required, read, visited)
	}
	for _, forbidden := range predicate.Forbids {
		collectPredicateFields(forbidden, read, visited)
	}
	for _, bound := range predicate.Bounds {
		read[bound.Field] = true
	}
}

func collectTemplateFields(template *ir.TextTemplate, read map[*ir.StateField]bool) {
	if template == nil {
		return
	}
	for _, segment := range template.Segments {
		if segment.Field != nil {
			read[segment.Field] = true
		}
	}
}
