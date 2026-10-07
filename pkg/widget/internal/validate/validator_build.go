package validate

import (
	"fmt"
	"math"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
	"github.com/candacelabs/csf/pkg/widget/internal/ir"
	"github.com/candacelabs/csf/pkg/widget/internal/lex"
	"github.com/candacelabs/csf/pkg/widget/internal/parse"
)

// The second pass: the fourteen blocks, read in canonical order, each producing
// its IR records with every reference resolved to a handle.
//
// Two blocks are read out of their canonical position on purpose. Events are
// read before controls, because a control names an event and the canonical
// order puts controls first — the one forward reference the language requires,
// and the reason W411 exists rather than a reference finding. Predicates are
// read in two sweeps, because a composition may name a composition declared
// later in its own block and it is W408, not a reference finding, that owns the
// cycle such a reference can make.

func (documentValidator *validator) build() {
	documentValidator.buildState()
	documentValidator.buildPredicates()
	documentValidator.buildBindings()
	documentValidator.buildLabels()
	documentValidator.buildChrome()
	documentValidator.buildRoles()
	documentValidator.buildChannels()
	documentValidator.buildPlacements()
	documentValidator.buildScene()
	documentValidator.buildMotion()
	documentValidator.buildIndicators()
	documentValidator.buildEvents()
	documentValidator.buildControls()
	documentValidator.buildStreams()
	documentValidator.computeProjections()
}

// readBlockMembers splits a block into its line declarations and its own
// clauses, and reports every statement that is neither.
func (documentValidator *validator) readBlockMembers(block *parse.Block, lineSpecs []declarationSpec, clauses []clauseSpec) (map[string][]namedDeclaration, clauseSet) {
	accepted := make([]string, 0, len(lineSpecs)+len(clauses)+len(block.Spec.Openers))
	byKeyword := map[string]declarationSpec{}
	for _, spec := range lineSpecs {
		byKeyword[spec.keyword] = spec
		accepted = append(accepted, spec.keyword)
	}
	for _, spec := range clauses {
		accepted = append(accepted, spec.keyword)
	}
	accepted = append(accepted, block.Spec.Openers...)

	declarations := map[string][]namedDeclaration{}
	remaining := make([]parse.Statement, 0, len(block.Statements))
	for _, statement := range block.Statements {
		spec, isDeclaration := byKeyword[statement.Keyword.Text]
		if !isDeclaration {
			remaining = append(remaining, statement)
			continue
		}
		declarations[spec.keyword] = append(declarations[spec.keyword], documentValidator.readLineDeclaration(statement, spec))
	}
	context := clauseContext{
		container:      fmt.Sprintf("the `%s` block", block.Spec.Name),
		accepted:       accepted,
		duplicateClass: diag.ClassSingularStatementRepeated,
	}
	return declarations, documentValidator.readClauses(remaining, clauses, context)
}

// namedDeclaration is one read construct: its identifier, its clauses and the
// statement that opened it. Both declaration forms produce one — a line
// declaration whose clauses follow on the same line, and a block declaration
// whose clauses are its member lines — because everything downstream of the
// clause reader treats them alike.
type namedDeclaration struct {
	name      argumentValue
	clauses   clauseSet
	statement parse.Statement
}

func (documentValidator *validator) readLineDeclaration(statement parse.Statement, spec declarationSpec) namedDeclaration {
	name := documentValidator.readDeclarationName(statement, spec)
	inline := splitInlineClauses(statement.Arguments[minimumInt(1, len(statement.Arguments)):], spec.clauses)
	owner := describeDeclaration(spec, name)
	set := documentValidator.readClauses(inline, spec.clauses, clauseContext{
		container:      owner,
		accepted:       spec.keywords(),
		duplicateClass: diag.ClassClauseDuplicated,
	})
	documentValidator.checkRequired(set, spec.clauses, owner, statement.At())
	return namedDeclaration{name: name, clauses: set, statement: statement}
}

func (documentValidator *validator) readBlockDeclaration(declaration parse.Declaration, spec declarationSpec) namedDeclaration {
	name := documentValidator.readDeclarationName(declaration.Header, spec)
	owner := describeDeclaration(spec, name)
	set := documentValidator.readClauses(declaration.Statements, spec.clauses, clauseContext{
		container:      owner,
		accepted:       spec.keywords(),
		duplicateClass: diag.ClassClauseDuplicated,
	})
	documentValidator.checkRequired(set, spec.clauses, owner, declaration.Header.At())
	return namedDeclaration{name: name, clauses: set, statement: declaration.Header}
}

// readDeclarationName reads the identifier a declaration's header carries, with
// the case rule its kind requires.
func (documentValidator *validator) readDeclarationName(header parse.Statement, spec declarationSpec) argumentValue {
	arguments := header.Arguments
	if len(arguments) > 1 {
		arguments = arguments[:1]
	}
	headerSpec := clauseSpec{
		keyword:   spec.keyword,
		arguments: []argumentSpec{{kind: argumentIdentifier, name: "id", nameCase: spec.nameCase}},
	}
	return documentValidator.readClause(parse.Statement{Keyword: header.Keyword, Arguments: arguments}, headerSpec).value(0)
}

// splitInlineClauses turns the tail of a line declaration into the clause
// statements the reader consumes, so one clause reader serves both the line
// form and the block form.
func splitInlineClauses(tokens []lex.Token, specs []clauseSpec) []parse.Statement {
	statements := []parse.Statement{}
	for _, token := range tokens {
		_, opensClause := findClauseSpec(specs, token.Text)
		if opensClause && token.Kind == lex.KindWord || len(statements) == 0 {
			statements = append(statements, parse.Statement{Keyword: token})
			continue
		}
		last := &statements[len(statements)-1]
		last.Arguments = append(last.Arguments, token)
	}
	return statements
}

func describeDeclaration(spec declarationSpec, name argumentValue) string {
	if !name.sound {
		return fmt.Sprintf("the `%s` declaration", spec.keyword)
	}
	return fmt.Sprintf("%s %s", spec.keyword, diag.Quote(name.text))
}

// bind attaches an IR record to the identifier that declared it, so a later
// reference resolves to a handle rather than to a name.
func (documentValidator *validator) bind(name argumentValue, record any) bool {
	if !name.sound {
		return false
	}
	found, declared := documentValidator.symbols.lookup(name.text)
	if !declared || found.at != name.token.At {
		return false
	}
	found.record = record
	return true
}

// reference resolves an identifier argument, marking the declaration as used so
// the whole-document pass can report vocabulary nothing names.
func (documentValidator *validator) reference(value argumentValue, kind symbolKind, options referenceOptions) (*symbol, bool) {
	if !value.sound {
		return nil, false
	}
	found, resolved := documentValidator.resolve(value.token, kind, options)
	if resolved && !options.secondary {
		documentValidator.referenced[found.name] = true
	}
	return found, resolved
}

func (documentValidator *validator) referenceLabel(value argumentValue) *ir.Label {
	found, resolved := documentValidator.reference(value, kindLabel, referenceOptions{})
	label, isLabel := recordOf[*ir.Label](found, resolved)
	if !isLabel {
		return nil
	}
	return label
}

func (documentValidator *validator) referencePredicate(value argumentValue, options referenceOptions) *ir.Predicate {
	found, resolved := documentValidator.reference(value, kindPredicate, options)
	if !resolved {
		return nil
	}
	if field, isField := recordOf[*ir.StateField](found, resolved); isField {
		return documentValidator.atomics[field.Name]
	}
	predicate, isPredicate := recordOf[*ir.Predicate](found, resolved)
	if !isPredicate {
		return nil
	}
	return predicate
}

func (documentValidator *validator) referenceField(value argumentValue) *ir.StateField {
	found, resolved := documentValidator.reference(value, kindStateField, referenceOptions{})
	field, isField := recordOf[*ir.StateField](found, resolved)
	if !isField {
		return nil
	}
	return field
}

// buildState reads the state block. Every field is typed, and a field bound to
// a runtime signal takes that signal as one of its writers.
func (documentValidator *validator) buildState() {
	block, present := documentValidator.block("state")
	if !present {
		return
	}
	declarations, _ := documentValidator.readBlockMembers(block, []declarationSpec{fieldDeclaration}, nil)
	for _, declaration := range declarations["field"] {
		field := &ir.StateField{Name: declaration.name.text, Span: declaration.statement.Span()}
		typeClause, typed := declaration.clauses.first("type")
		typed = typed && typeClause.value(0).sound
		if typed {
			field.Type = ir.FieldType(typeClause.value(0).text)
		}
		if signalClause, signalled := declaration.clauses.first("signal"); signalled && signalClause.value(0).sound {
			// The writer is recorded even when the type is wrong. The author
			// wrote a writer, and reporting W409 as well would report a
			// consequence of the finding above as if it were a second mistake.
			documentValidator.checkSignalType(field, typed, signalClause)
			field.Signal = signalClause.value(0).text
			field.Writers = append(field.Writers, ir.Writer{
				Kind:   ir.WriterSignal,
				Signal: field.Signal,
				Span:   signalClause.keyword.Span(),
			})
		}
		if !documentValidator.bind(declaration.name, field) {
			continue
		}
		documentValidator.document.StateFields = append(documentValidator.document.StateFields, field)
		if field.Type == ir.FieldFlag {
			atomic := &ir.Predicate{Name: field.Name, Kind: ir.PredicateAtomic, Field: field, Span: field.Span}
			documentValidator.atomics[field.Name] = atomic
			documentValidator.document.Predicates = append(documentValidator.document.Predicates, atomic)
		}
	}
}

// checkSignalType reports a signal bound to a field that cannot hold what the
// signal writes. The runtime writes the signal as true and false, so the field
// is a `flag`, and this is the check the P2 audit found missing: `field beats
// type counter signal slowClient` validated clean, generated, and produced Go
// that assigns an untyped bool constant to a uint64.
//
// It is the same shape as W406, W416, W417 and W418 with one difference, and the
// difference is why it carries no secondary anchor: those four name a field
// declared elsewhere in the document, while a `signal` clause is part of the
// field's own declaration, so the type it disagrees with is on the line the
// finding already points at.
//
// A field whose `type` clause was itself unsound is left alone: its type is
// unknown rather than wrong, and a finding derived from an unresolved value
// reports a consequence of the first one as if it were a second.
func (documentValidator *validator) checkSignalType(field *ir.StateField, typed bool, signalClause clause) {
	if !typed || field.Type == ir.FieldFlag {
		return
	}
	documentValidator.report(diag.New(
		diag.ClassSignalFieldNotFlag,
		signalClause.keyword.At,
		fmt.Sprintf("state field %s is a `%s`, and a runtime signal writes a `%s`", diag.Quote(field.Name), field.Type, ir.FieldFlag),
		fmt.Sprintf("write `field %s type %s signal %s`, or drop the `signal` clause and write the value from an event with `field <wire_name> writes %s`",
			field.Name, ir.FieldFlag, signalClause.value(0).text, field.Name),
	))
}

// buildPredicates reads the compositions in two sweeps: every record first, so
// a composition may name one declared later in the same block, then every
// clause. The cycle such a reference can make is W408's to report.
func (documentValidator *validator) buildPredicates() {
	block, present := documentValidator.block("predicates")
	if !present {
		return
	}
	records := make([]*ir.Predicate, 0, len(block.Declarations))
	for _, declaration := range block.Declarations {
		name, named := declaredName(declaration.Header)
		if !named {
			records = append(records, nil)
			continue
		}
		predicate := &ir.Predicate{Name: name.text, Kind: ir.PredicateComposed, Span: declaration.Span()}
		if !documentValidator.bind(name, predicate) {
			records = append(records, nil)
			continue
		}
		records = append(records, predicate)
		documentValidator.document.Predicates = append(documentValidator.document.Predicates, predicate)
	}
	for index, declaration := range block.Declarations {
		read := documentValidator.readBlockDeclaration(declaration, predicateDeclaration)
		if records[index] == nil {
			continue
		}
		documentValidator.fillPredicate(records[index], read, declaration)
	}
	documentValidator.reportStrayStatements(block)
}

func (documentValidator *validator) fillPredicate(predicate *ir.Predicate, read namedDeclaration, declaration parse.Declaration) {
	forward := referenceOptions{allowForward: true}
	for _, clause := range read.clauses.order {
		switch clause.keyword.Text {
		case "requires":
			documentValidator.fillRequires(predicate, clause, forward)
		case "forbids":
			if forbidden := documentValidator.referencePredicate(clause.value(0), forward); forbidden != nil {
				predicate.Forbids = append(predicate.Forbids, forbidden)
			}
		}
	}
	if len(read.clauses.order) == 0 {
		documentValidator.report(diag.New(
			diag.ClassContainerUnderfilled,
			declaration.Header.At(),
			fmt.Sprintf("predicate %s composes 0 clauses, and a composition takes at least 1", diag.Quote(predicate.Name)),
			"add a `requires <predicate>`, a `forbids <predicate>` or a `requires <numericField> atLeast <n>` clause; an empty composition is vacuously true, which is a way of writing \"always\" that does not look like one",
		))
	}
}

// fillRequires reads the two forms of a `requires` clause: naming a predicate,
// and bounding a numeric field.
func (documentValidator *validator) fillRequires(predicate *ir.Predicate, read clause, options referenceOptions) {
	if len(read.values) < 3 {
		if required := documentValidator.referencePredicate(read.value(0), options); required != nil {
			predicate.Requires = append(predicate.Requires, required)
		}
		return
	}
	field := documentValidator.referenceField(read.value(0))
	if field == nil {
		return
	}
	if !field.Numeric() {
		documentValidator.report(diag.New(
			diag.ClassBoundFieldNotNumeric,
			read.keyword.At,
			fmt.Sprintf("state field %s is a `%s`, and a numeric bound compares a `counter` or a `count`", diag.Quote(field.Name), field.Type),
			fmt.Sprintf("bound a numeric field, or write `requires %s` to use the flag directly as a predicate", field.Name),
		).WithRelated(field.Span.Start(), fmt.Sprintf("%s is declared here as a `%s`", diag.Quote(field.Name), field.Type)))
		return
	}
	if !read.value(1).sound || !read.value(2).sound {
		return
	}
	predicate.Bounds = append(predicate.Bounds, ir.NumericBound{
		Field:      field,
		Comparison: ir.Comparison(read.value(1).text),
		Value:      read.value(2).integer,
		Span:       read.statement.Span(),
	})
}

// buildBindings reads the ordered, total decisions from state to text.
func (documentValidator *validator) buildBindings() {
	block, present := documentValidator.block("bindings")
	if !present {
		return
	}
	for _, declaration := range block.Declarations {
		read := documentValidator.readBlockDeclaration(declaration, bindingDeclaration)
		binding := &ir.Binding{Name: read.name.text, Span: declaration.Span()}
		for _, clause := range read.clauses.order {
			documentValidator.fillBindingClause(binding, clause)
		}
		documentValidator.checkBindingTotality(binding, read, declaration)
		if documentValidator.bind(read.name, binding) {
			documentValidator.document.Bindings = append(documentValidator.document.Bindings, binding)
		}
	}
	documentValidator.reportStrayStatements(block)
}

func (documentValidator *validator) fillBindingClause(binding *ir.Binding, read clause) {
	if read.keyword.Text == "otherwise" {
		if read.value(0).sound {
			binding.Otherwise = documentValidator.readLiteral(read.value(0).token)
		}
		return
	}
	guard := &ir.BindingClause{
		Polarity: ir.GuardPolarity(read.keyword.Text),
		Span:     read.statement.Span(),
	}
	guard.Predicate = documentValidator.referencePredicate(read.value(0), referenceOptions{})
	if read.value(2).sound {
		guard.Template = documentValidator.readLiteral(read.value(2).token)
	}
	binding.Clauses = append(binding.Clauses, guard)
}

// readLiteral turns an author's quoted text into a template, after checking
// that it carries no identifier a widget document may not name.
func (documentValidator *validator) readLiteral(token lex.Token) *ir.TextTemplate {
	documentValidator.checkPortability(token)
	return documentValidator.parseTemplate(token)
}

func (documentValidator *validator) checkBindingTotality(binding *ir.Binding, read namedDeclaration, declaration parse.Declaration) {
	if binding.Otherwise == nil {
		documentValidator.report(diag.New(
			diag.ClassBindingNotTotal,
			declaration.Header.At(),
			fmt.Sprintf("binding %s has no `otherwise` clause, so it produces no value when no guard matches", diag.Quote(binding.Name)),
			"add `otherwise \"<text>\"` as the last line of the binding",
		))
	}
	if len(binding.Clauses) == 0 && len(read.clauses.order) > 0 {
		documentValidator.report(diag.New(
			diag.ClassContainerUnderfilled,
			declaration.Header.At(),
			fmt.Sprintf("binding %s carries 0 guard clauses, and a binding takes at least 1", diag.Quote(binding.Name)),
			"add a `when <predicate> then \"<text>\"` clause; a decision with no guard is a label, and a literal label is how a value with no decision around it is written",
		))
	}
}

// buildLabels reads the one place free text enters the language.
func (documentValidator *validator) buildLabels() {
	block, present := documentValidator.block("labels")
	if !present {
		return
	}
	declarations, _ := documentValidator.readBlockMembers(block, []declarationSpec{labelDeclaration}, nil)
	for _, declaration := range declarations["label"] {
		label := &ir.Label{Name: declaration.name.text, Span: declaration.statement.Span()}
		text, literal := declaration.clauses.first("text")
		binds, bound := declaration.clauses.first("binds")
		switch {
		case literal && bound, !literal && !bound:
			documentValidator.reportLabelSources(declaration, literal, bound)
			if bound {
				label.Binding = documentValidator.referenceBinding(binds.value(0))
			}
			if literal && text.value(0).sound {
				label.Literal = documentValidator.readLiteral(text.value(0).token)
			}
		case literal:
			label.SourceKind = ir.LabelLiteral
			if text.value(0).sound {
				label.Literal = documentValidator.readLiteral(text.value(0).token)
			}
		default:
			label.SourceKind = ir.LabelBound
			label.Binding = documentValidator.referenceBinding(binds.value(0))
		}
		if documentValidator.bind(declaration.name, label) {
			documentValidator.document.Labels = append(documentValidator.document.Labels, label)
		}
	}
}

func (documentValidator *validator) referenceBinding(value argumentValue) *ir.Binding {
	found, resolved := documentValidator.reference(value, kindBinding, referenceOptions{})
	binding, isBinding := recordOf[*ir.Binding](found, resolved)
	if !isBinding {
		return nil
	}
	return binding
}

func (documentValidator *validator) reportLabelSources(declaration namedDeclaration, literal bool, bound bool) {
	present := "neither source"
	fix := "give the label exactly one source: `text \"<text>\"` or `binds <binding>`"
	if literal && bound {
		present = "both `text` and `binds`"
		fix = "delete one of the two: keep `text \"<text>\"` for literal text, or `binds <binding>` for a decided value"
	}
	documentValidator.report(diag.New(
		diag.ClassLabelSourceCount,
		declaration.statement.At(),
		fmt.Sprintf("label %s carries %s, and a label carries exactly one", diag.Quote(declaration.name.text), present),
		fix,
	))
}

// buildChrome fills the closed slot set.
func (documentValidator *validator) buildChrome() {
	block, present := documentValidator.block("chrome")
	if !present {
		return
	}
	_, clauses := documentValidator.readBlockMembers(block, nil, chromeClauses)
	title, titled := clauses.first("title")
	if !titled {
		documentValidator.report(diag.New(
			diag.ClassTitleSlotMissing,
			block.Header.At(),
			"the `chrome` block has no `title` line, and a widget with no accessible name is a defect no browser reports",
			fmt.Sprintf("add `title <label>`, naming one of the declared labels: %s", diag.Enumerate(documentValidator.symbols.namesOfKind(kindLabel), "title")),
		))
	} else {
		documentValidator.appendSlot(ir.SlotTitle, title, 0)
	}
	if source, sourced := clauses.first("source"); sourced {
		documentValidator.appendSlot(ir.SlotSource, source, 0)
	}
	for ordinal, stat := range clauses.all("stat") {
		documentValidator.appendSlot(ir.SlotStat, stat, ordinal)
	}
}

func (documentValidator *validator) appendSlot(kind ir.SlotKind, read clause, ordinal int) *ir.Slot {
	slot := &ir.Slot{
		Kind:    kind,
		Label:   documentValidator.referenceLabel(read.value(0)),
		Ordinal: ordinal,
		Span:    read.statement.Span(),
	}
	documentValidator.document.Slots = append(documentValidator.document.Slots, slot)
	return slot
}

// buildRoles reads the node kinds. A role fixes appearance and animation
// eligibility for every node it classifies.
func (documentValidator *validator) buildRoles() {
	block, present := documentValidator.block("roles")
	if !present {
		return
	}
	for _, declaration := range block.Declarations {
		read := documentValidator.readBlockDeclaration(declaration, roleDeclaration)
		role := &ir.Role{Name: read.name.text, Span: declaration.Span()}
		if token, tokened := read.clauses.first("token"); tokened && token.value(0).sound {
			role.Token = ir.Token(token.value(0).text)
		}
		if marker, marked := read.clauses.first("marker"); marked && marker.value(0).sound {
			role.Marker = ir.Marker(marker.value(0).text)
		}
		if emphasis, emphasised := read.clauses.first("emphasis"); emphasised && emphasis.value(0).sound {
			role.EmphasisAllowed = emphasis.value(0).text == "allowed"
		}
		if documentValidator.bind(read.name, role) {
			documentValidator.document.Roles = append(documentValidator.document.Roles, role)
		}
	}
	documentValidator.reportStrayStatements(block)
}

// buildChannels reads the kinds of traffic an edge can carry.
func (documentValidator *validator) buildChannels() {
	block, present := documentValidator.block("channels")
	if !present {
		return
	}
	for _, declaration := range block.Declarations {
		read := documentValidator.readBlockDeclaration(declaration, channelDeclaration)
		channel := &ir.Channel{Name: read.name.text, Span: declaration.Span()}
		if direction, directed := read.clauses.first("direction"); directed && direction.value(0).sound {
			channel.Direction = ir.Direction(direction.value(0).text)
		}
		if token, tokened := read.clauses.first("token"); tokened && token.value(0).sound {
			channel.Token = ir.Token(token.value(0).text)
		}
		if legend, legended := read.clauses.first("legend"); legended {
			channel.LegendLabel = documentValidator.referenceLabel(legend.value(0))
		}
		if documentValidator.bind(read.name, channel) {
			documentValidator.document.Channels = append(documentValidator.document.Channels, channel)
		}
	}
	documentValidator.reportStrayStatements(block)
}

// buildPlacements reads the named points of the normalized scene box.
func (documentValidator *validator) buildPlacements() {
	block, present := documentValidator.block("placements")
	if !present {
		return
	}
	declarations, _ := documentValidator.readBlockMembers(block, []declarationSpec{placementDeclaration}, nil)
	for _, declaration := range declarations["placement"] {
		placement := &ir.Placement{Name: declaration.name.text, Span: declaration.statement.Span()}
		if left, positioned := declaration.clauses.first("left"); positioned {
			placement.Left = left.value(0).integer
		}
		if top, positioned := declaration.clauses.first("top"); positioned {
			placement.Top = top.value(0).integer
		}
		if documentValidator.bind(declaration.name, placement) {
			documentValidator.document.Placements = append(documentValidator.document.Placements, placement)
		}
	}
}

// buildScene reads the widget's one drawing area: its description, its
// decorative orbits, its nodes and the edges between them.
func (documentValidator *validator) buildScene() {
	block, present := documentValidator.block("scene")
	if !present {
		return
	}
	name := documentValidator.readDeclarationName(block.Header, declarationSpec{keyword: "scene", kind: kindScene})
	scene := &ir.Scene{Name: name.text, Span: block.Span()}
	documentValidator.document.Scene = scene
	documentValidator.bind(name, scene)

	declarations, clauses := documentValidator.readBlockMembers(block, []declarationSpec{orbitDeclaration}, sceneClauses)
	if description, described := clauses.first("description"); described {
		scene.DescriptionSlot = documentValidator.appendSlot(ir.SlotDescription, description, 0)
	} else {
		documentValidator.report(diag.New(
			diag.ClassDescriptionSlotMissing,
			block.Header.At(),
			fmt.Sprintf("scene %s has no `description` line, and the scene is the widget's single accessibility boundary", diag.Quote(scene.Name)),
			fmt.Sprintf("add `description <label>`, naming one of the declared labels: %s; an absent description degrades to no accessibility rather than to less", diag.Enumerate(documentValidator.symbols.namesOfKind(kindLabel), "description")),
		))
	}
	for _, declaration := range declarations["orbit"] {
		orbit := &ir.Orbit{Name: declaration.name.text, Span: declaration.statement.Span()}
		if token, tokened := declaration.clauses.first("token"); tokened && token.value(0).sound {
			orbit.Token = ir.Token(token.value(0).text)
		}
		if documentValidator.bind(declaration.name, orbit) {
			scene.Orbits = append(scene.Orbits, orbit)
		}
	}
	documentValidator.buildNodes(block, scene)
	documentValidator.buildEdges(block, scene)
}

func (documentValidator *validator) buildNodes(block *parse.Block, scene *ir.Scene) {
	occupied := map[string]*ir.Node{}
	for _, declaration := range block.Declarations {
		if declaration.Header.Keyword.Text != "node" {
			continue
		}
		read := documentValidator.readBlockDeclaration(declaration, nodeDeclaration)
		node := &ir.Node{Name: read.name.text, Span: declaration.Span()}
		if role, classified := read.clauses.first("role"); classified {
			node.Role = documentValidator.referenceRole(role.value(0))
		}
		if title, titled := read.clauses.first("title"); titled {
			node.TitleLabel = documentValidator.referenceLabel(title.value(0))
		}
		if caption, captioned := read.clauses.first("caption"); captioned {
			node.CaptionLabel = documentValidator.referenceLabel(caption.value(0))
		}
		if at, placed := read.clauses.first("at"); placed {
			node.Placement = documentValidator.placeNode(node, at, occupied)
		}
		if documentValidator.bind(read.name, node) {
			scene.Nodes = append(scene.Nodes, node)
		}
	}
}

// placeNode resolves a node's placement and reports the second node to name one
// point: one point holds one node, so a scene never renders two nodes on top of
// each other.
func (documentValidator *validator) placeNode(node *ir.Node, at clause, occupied map[string]*ir.Node) *ir.Placement {
	found, resolved := documentValidator.reference(at.value(0), kindPlacement, referenceOptions{})
	placement, isPlacement := recordOf[*ir.Placement](found, resolved)
	if !isPlacement {
		return nil
	}
	if first, taken := occupied[placement.Name]; taken {
		documentValidator.report(diag.New(
			diag.ClassPlacementShared,
			at.keyword.At,
			fmt.Sprintf("placement %s already holds node %s, and one point holds one node", diag.Quote(placement.Name), diag.Quote(first.Name)),
			fmt.Sprintf("add a second placement to the `placements` block and name it here, for example `placement %sTwo left 78 top 50`", placement.Name),
		).WithRelated(first.Span.Start(), fmt.Sprintf("%s is used here by node %s", diag.Quote(placement.Name), diag.Quote(first.Name))))
		return placement
	}
	occupied[placement.Name] = node
	return placement
}

func (documentValidator *validator) referenceRole(value argumentValue) *ir.Role {
	found, resolved := documentValidator.reference(value, kindRole, referenceOptions{})
	role, isRole := recordOf[*ir.Role](found, resolved)
	if !isRole {
		return nil
	}
	return role
}

func (documentValidator *validator) buildEdges(block *parse.Block, scene *ir.Scene) {
	joined := map[string]*ir.Edge{}
	for _, declaration := range block.Declarations {
		if declaration.Header.Keyword.Text != "edge" {
			continue
		}
		read := documentValidator.readBlockDeclaration(declaration, edgeDeclaration)
		edge := &ir.Edge{Name: read.name.text, Span: declaration.Span()}
		if from, sourced := read.clauses.first("from"); sourced {
			edge.From = documentValidator.referenceNode(from.value(0))
		}
		if to, targeted := read.clauses.first("to"); targeted {
			edge.To = documentValidator.referenceNode(to.value(0))
			documentValidator.checkEdgeEndpoints(edge, to, joined)
		}
		for _, carries := range read.clauses.all("carries") {
			if channel := documentValidator.referenceChannel(carries.value(0)); channel != nil {
				edge.Channels = append(edge.Channels, channel)
			}
		}
		edge.Geometry = geometryOf(edge)
		if documentValidator.bind(read.name, edge) {
			scene.Edges = append(scene.Edges, edge)
		}
	}
}

// checkEdgeEndpoints reports a self-edge and a second edge on one unordered
// pair. Direction belongs to the channel, not to a second edge.
func (documentValidator *validator) checkEdgeEndpoints(edge *ir.Edge, to clause, joined map[string]*ir.Edge) {
	if edge.From == nil || edge.To == nil {
		return
	}
	if edge.From == edge.To {
		documentValidator.report(diag.New(
			diag.ClassEdgeSelfLoop,
			to.keyword.At,
			fmt.Sprintf("edge %s joins node %s to itself, and `from` and `to` are distinct", diag.Quote(edge.Name), diag.Quote(edge.To.Name)),
			"point `to` at a different node; a self-edge has no derivable geometry in a point-placement scene",
		))
		return
	}
	key := unorderedPair(edge.From.Name, edge.To.Name)
	if first, taken := joined[key]; taken {
		documentValidator.report(diag.New(
			diag.ClassEdgePairDuplicated,
			edge.Span.Start(),
			fmt.Sprintf("edge %s joins nodes %s and %s, which edge %s already joins", diag.Quote(edge.Name), diag.Quote(edge.From.Name), diag.Quote(edge.To.Name), diag.Quote(first.Name)),
			fmt.Sprintf("merge the two into one edge carrying both channels, and delete `edge %s`; direction belongs to the channel, not to a second edge", edge.Name),
		).WithRelated(first.Span.Start(), fmt.Sprintf("edge %s joins the same pair here", diag.Quote(first.Name))))
		return
	}
	joined[key] = edge
}

func (documentValidator *validator) referenceNode(value argumentValue) *ir.Node {
	found, resolved := documentValidator.reference(value, kindNode, referenceOptions{})
	node, isNode := recordOf[*ir.Node](found, resolved)
	if !isNode {
		return nil
	}
	return node
}

func (documentValidator *validator) referenceChannel(value argumentValue) *ir.Channel {
	return documentValidator.referenceChannelAs(value, referenceOptions{})
}

func (documentValidator *validator) referenceChannelAs(value argumentValue, options referenceOptions) *ir.Channel {
	found, resolved := documentValidator.reference(value, kindChannel, options)
	channel, isChannel := recordOf[*ir.Channel](found, resolved)
	if !isChannel {
		return nil
	}
	return channel
}

func unorderedPair(first string, second string) string {
	if first < second {
		return first + "\x00" + second
	}
	return second + "\x00" + first
}

// geometryOf derives an edge's length and angle from its endpoints. It is
// computed, never parsed: the shipped card hand-wrote both, which is a copy of
// the endpoints that silently goes stale when a node moves.
func geometryOf(edge *ir.Edge) ir.EdgeGeometry {
	if edge.From == nil || edge.To == nil || edge.From.Placement == nil || edge.To.Placement == nil {
		return ir.EdgeGeometry{}
	}
	across := float64(edge.To.Placement.Left - edge.From.Placement.Left)
	down := float64(edge.To.Placement.Top - edge.From.Placement.Top)
	return ir.EdgeGeometry{
		LengthPercent: math.Hypot(across, down),
		AngleDegrees:  math.Atan2(down, across) * 180 / math.Pi,
	}
}

// buildMotion reads the one gate every animation runs under.
func (documentValidator *validator) buildMotion() {
	block, present := documentValidator.block("motion")
	if !present {
		return
	}
	_, clauses := documentValidator.readBlockMembers(block, nil, motionClauses)
	motion := &ir.Motion{HostStatusGate: true, Span: block.Span()}
	documentValidator.document.Motion = motion
	for _, required := range clauses.all("requires") {
		if predicate := documentValidator.referencePredicate(required.value(0), referenceOptions{}); predicate != nil {
			motion.Requires = append(motion.Requires, predicate)
		}
	}
	for _, forbidden := range clauses.all("forbids") {
		if predicate := documentValidator.referencePredicate(forbidden.value(0), referenceOptions{}); predicate != nil {
			motion.Forbids = append(motion.Forbids, predicate)
		}
	}
	documentValidator.buildPulses(block, motion)
	documentValidator.buildEmphases(block, motion)
	documentValidator.checkTick(block, motion, clauses)
}

func (documentValidator *validator) buildPulses(block *parse.Block, motion *ir.Motion) {
	travelling := map[string]*ir.Pulse{}
	for _, declaration := range block.Declarations {
		if declaration.Header.Keyword.Text != "pulse" {
			continue
		}
		read := documentValidator.readBlockDeclaration(declaration, pulseDeclaration)
		pulse := &ir.Pulse{Name: read.name.text, Span: declaration.Span()}
		if edge, travels := read.clauses.first("edge"); travels {
			pulse.Edge = documentValidator.referenceEdge(edge.value(0))
		}
		channelClause, carried := read.clauses.first("channel")
		if carried {
			pulse.Channel = documentValidator.referenceChannelAs(channelClause.value(0), referenceOptions{secondary: true})
		}
		pulse.DurationMilliseconds, pulse.DelayMilliseconds = readTiming(read)
		documentValidator.checkPulseChannel(pulse, channelClause, carried)
		documentValidator.checkPulseUniqueness(pulse, travelling)
		if documentValidator.bind(read.name, pulse) {
			motion.Pulses = append(motion.Pulses, pulse)
		}
	}
}

// checkPulseChannel reports a pulse naming a channel its edge does not carry.
// It is skipped when either reference failed, so an invariant checked against
// an unresolved name never reports a consequence of the first finding.
func (documentValidator *validator) checkPulseChannel(pulse *ir.Pulse, channelClause clause, carried bool) {
	if !carried || pulse.Edge == nil || pulse.Channel == nil {
		return
	}
	for _, channel := range pulse.Edge.Channels {
		if channel == pulse.Channel {
			return
		}
	}
	documentValidator.report(diag.New(
		diag.ClassPulseChannelNotCarried,
		channelClause.keyword.At,
		fmt.Sprintf("pulse %s travels channel %s on edge %s, which carries %s", diag.Quote(pulse.Name), diag.Quote(pulse.Channel.Name), diag.Quote(pulse.Edge.Name), diag.Enumerate(channelNames(pulse.Edge), pulse.Channel.Name)),
		fmt.Sprintf("add `carries %s` to edge `%s`, or name a channel the edge already carries", pulse.Channel.Name, pulse.Edge.Name),
	).WithRelated(pulse.Edge.Span.Start(), fmt.Sprintf("edge %s is declared here", diag.Quote(pulse.Edge.Name))))
}

func (documentValidator *validator) checkPulseUniqueness(pulse *ir.Pulse, travelling map[string]*ir.Pulse) {
	if pulse.Edge == nil || pulse.Channel == nil {
		return
	}
	key := pulse.Edge.Name + "\x00" + pulse.Channel.Name
	if first, taken := travelling[key]; taken {
		documentValidator.report(diag.New(
			diag.ClassPulseDuplicated,
			pulse.Span.Start(),
			fmt.Sprintf("pulse %s travels channel %s on edge %s, which pulse %s already travels", diag.Quote(pulse.Name), diag.Quote(pulse.Channel.Name), diag.Quote(pulse.Edge.Name), diag.Quote(first.Name)),
			fmt.Sprintf("delete `pulse %s`, or move it to another channel; a second pulse on one pair is a duplicate, not a second particle", pulse.Name),
		).WithRelated(first.Span.Start(), fmt.Sprintf("pulse %s travels the same pair here", diag.Quote(first.Name))))
		return
	}
	travelling[key] = pulse
}

func channelNames(edge *ir.Edge) []string {
	names := make([]string, 0, len(edge.Channels))
	for _, channel := range edge.Channels {
		names = append(names, channel.Name)
	}
	return names
}

func (documentValidator *validator) buildEmphases(block *parse.Block, motion *ir.Motion) {
	emphasised := map[string]*ir.Emphasis{}
	for _, declaration := range block.Declarations {
		if declaration.Header.Keyword.Text != "emphasis" {
			continue
		}
		read := documentValidator.readBlockDeclaration(declaration, emphasisDeclaration)
		emphasis := &ir.Emphasis{Name: read.name.text, Span: declaration.Span()}
		nodeClause, attached := read.clauses.first("node")
		if attached {
			emphasis.Node = documentValidator.referenceNode(nodeClause.value(0))
		}
		emphasis.DurationMilliseconds, emphasis.DelayMilliseconds = readTiming(read)
		documentValidator.checkEmphasisNode(emphasis, nodeClause, emphasised)
		if documentValidator.bind(read.name, emphasis) {
			motion.Emphases = append(motion.Emphases, emphasis)
		}
	}
}

func (documentValidator *validator) checkEmphasisNode(emphasis *ir.Emphasis, nodeClause clause, emphasised map[string]*ir.Emphasis) {
	if emphasis.Node == nil {
		return
	}
	if first, taken := emphasised[emphasis.Node.Name]; taken {
		documentValidator.report(diag.New(
			diag.ClassEmphasisDuplicated,
			nodeClause.keyword.At,
			fmt.Sprintf("node %s already carries emphasis %s, and a node carries at most one", diag.Quote(emphasis.Node.Name), diag.Quote(first.Name)),
			fmt.Sprintf("delete `emphasis %s`, or emphasise another node", emphasis.Name),
		).WithRelated(first.Span.Start(), fmt.Sprintf("emphasis %s names the same node here", diag.Quote(first.Name))))
	} else {
		emphasised[emphasis.Node.Name] = emphasis
	}
	if emphasis.Node.Role == nil || emphasis.Node.Role.EmphasisAllowed {
		return
	}
	documentValidator.report(diag.New(
		diag.ClassEmphasisForbiddenByRole,
		nodeClause.keyword.At,
		fmt.Sprintf("node %s is classified by role %s, which declares `emphasis forbidden`", diag.Quote(emphasis.Node.Name), diag.Quote(emphasis.Node.Role.Name)),
		fmt.Sprintf("write `emphasis allowed` in role `%s`, or emphasise a node whose role allows it; there is no per-emphasis override", emphasis.Node.Role.Name),
	).WithRelated(emphasis.Node.Role.Span.Start(), fmt.Sprintf("role %s declares `emphasis forbidden` here", diag.Quote(emphasis.Node.Role.Name))))
}

// readTiming materialises the timing defaults, so the IR says the same thing
// whether or not the author wrote the delay.
func readTiming(read namedDeclaration) (int, int) {
	duration, delay := 0, 0
	if clause, timed := read.clauses.first("duration"); timed {
		duration = clause.value(0).integer
	}
	if clause, delayed := read.clauses.first("delay"); delayed {
		delay = clause.value(0).integer
	}
	return duration, delay
}

// checkTick reports the three ways a motion block and its tick disagree.
func (documentValidator *validator) checkTick(block *parse.Block, motion *ir.Motion, clauses clauseSet) {
	restart, declared := clauses.first("restartOn")
	animated := len(motion.Pulses) + len(motion.Emphases)
	if !declared {
		if animated > 0 {
			documentValidator.report(diag.New(
				diag.ClassMotionTickMissing,
				block.Header.At(),
				fmt.Sprintf("the `motion` block holds %d finite %s and declares no `restartOn`, so nothing re-arms them", animated, pluralize("animation", animated)),
				fmt.Sprintf("add `restartOn <counterField>`, naming one of the declared counters: %s", diag.Enumerate(documentValidator.counterNames(), "restartOn")),
			))
		}
		return
	}
	if animated == 0 {
		documentValidator.report(diag.New(
			diag.ClassTickWithoutAnimation,
			restart.keyword.At,
			"the `motion` block declares `restartOn` and holds no pulse and no emphasis, so there is nothing to re-arm",
			"delete the `restartOn` line, or add the pulse or emphasis it was meant to re-arm",
		))
	}
	field := documentValidator.referenceField(restart.value(0))
	if field == nil {
		return
	}
	if field.Type != ir.FieldCounter {
		documentValidator.report(diag.New(
			diag.ClassTickNotCounter,
			restart.keyword.At,
			fmt.Sprintf("state field %s is a `%s`, and a tick is a `counter`", diag.Quote(field.Name), field.Type),
			fmt.Sprintf("name a declared counter: %s", diag.Enumerate(documentValidator.counterNames(), field.Name)),
		).WithRelated(field.Span.Start(), fmt.Sprintf("%s is declared here as a `%s`", diag.Quote(field.Name), field.Type)))
		return
	}
	motion.RestartOn = field
}

func (documentValidator *validator) counterNames() []string {
	names := []string{}
	for _, field := range documentValidator.document.StateFields {
		if field.Type == ir.FieldCounter {
			names = append(names, field.Name)
		}
	}
	return names
}

// buildIndicators reads the binary status marks.
func (documentValidator *validator) buildIndicators() {
	block, present := documentValidator.block("indicators")
	if !present {
		return
	}
	for _, declaration := range block.Declarations {
		read := documentValidator.readBlockDeclaration(declaration, indicatorDeclaration)
		indicator := &ir.Indicator{Name: read.name.text, Span: declaration.Span()}
		labelClause, labelled := read.clauses.first("label")
		if labelled {
			indicator.Label = documentValidator.referenceLabel(labelClause.value(0))
			documentValidator.checkIndicatorLabel(indicator, labelClause)
		}
		if positive, toned := read.clauses.first("positiveWhen"); toned {
			indicator.Predicate = documentValidator.referencePredicate(positive.value(0), referenceOptions{})
		}
		if documentValidator.bind(read.name, indicator) {
			documentValidator.document.Indicators = append(documentValidator.document.Indicators, indicator)
		}
	}
	documentValidator.reportStrayStatements(block)
}

func (documentValidator *validator) checkIndicatorLabel(indicator *ir.Indicator, labelClause clause) {
	if indicator.Label == nil || !emptyLabel(indicator.Label) {
		return
	}
	documentValidator.report(diag.New(
		diag.ClassIndicatorLabelEmpty,
		labelClause.keyword.At,
		fmt.Sprintf("indicator %s names label %s, which resolves to empty text", diag.Quote(indicator.Name), diag.Quote(indicator.Label.Name)),
		fmt.Sprintf("give label `%s` text; colour alone is not a signal every viewer receives, so the label is the indicator's accessible carrier", indicator.Label.Name),
	).WithRelated(indicator.Label.Span.Start(), fmt.Sprintf("label %s is declared here", diag.Quote(indicator.Label.Name))))
}

func emptyLabel(label *ir.Label) bool {
	if label.SourceKind == ir.LabelLiteral {
		return label.Literal.Empty()
	}
	if label.Binding == nil {
		return false
	}
	if label.Binding.Otherwise.Empty() {
		return true
	}
	for _, clause := range label.Binding.Clauses {
		if clause.Template.Empty() {
			return true
		}
	}
	return false
}

// buildEvents reads the exhaustive, default-deny accept list. It runs before
// the controls block so a control resolves to an event record.
func (documentValidator *validator) buildEvents() {
	block, present := documentValidator.block("events")
	if !present {
		return
	}
	for _, declaration := range block.Declarations {
		read := documentValidator.readBlockDeclaration(declaration, eventDeclaration)
		event := &ir.EventDeclaration{Name: read.name.text, Span: declaration.Span()}
		if wire, wired := read.clauses.first("wire"); wired && wire.value(0).sound && documentValidator.checkWireName(wire.keyword.At, wire.value(0).token) {
			event.Wire = wire.value(0).text
			documentValidator.checkPortability(wire.value(0).token)
		}
		if toggles, toggled := read.clauses.first("toggles"); toggled {
			event.Toggles = documentValidator.readToggle(event, toggles)
		}
		for _, fieldClause := range read.clauses.all("field") {
			documentValidator.readEventField(event, fieldClause)
		}
		if documentValidator.bind(read.name, event) {
			documentValidator.document.Events = append(documentValidator.document.Events, event)
		}
	}
	documentValidator.reportStrayStatements(block)
}

func (documentValidator *validator) readToggle(event *ir.EventDeclaration, toggles clause) *ir.StateField {
	field := documentValidator.referenceField(toggles.value(0))
	if field == nil {
		return nil
	}
	if field.Type != ir.FieldFlag {
		documentValidator.report(diag.New(
			diag.ClassToggleFieldNotFlag,
			toggles.keyword.At,
			fmt.Sprintf("state field %s is a `%s`, and `toggles` flips a `flag`", diag.Quote(field.Name), field.Type),
			fmt.Sprintf("name a `flag` field, or write `field <wire_name> writes %s` to assign a value instead", field.Name),
		).WithRelated(field.Span.Start(), fmt.Sprintf("%s is declared here as a `%s`", diag.Quote(field.Name), field.Type)))
		return nil
	}
	field.Writers = append(field.Writers, ir.Writer{Kind: ir.WriterEventToggle, Event: event, Span: toggles.statement.Span()})
	return field
}

func (documentValidator *validator) readEventField(event *ir.EventDeclaration, fieldClause clause) {
	written := documentValidator.referenceField(fieldClause.value(2))
	if !fieldClause.value(0).sound || written == nil {
		return
	}
	eventField := &ir.EventField{
		WireName: fieldClause.value(0).text,
		Writes:   written,
		Type:     written.Type,
		Span:     fieldClause.statement.Span(),
	}
	event.Fields = append(event.Fields, eventField)
	written.Writers = append(written.Writers, ir.Writer{
		Kind:  ir.WriterEventField,
		Event: event,
		Field: eventField,
		Span:  fieldClause.statement.Span(),
	})
}

// buildControls reads the interactive elements. A control names an event, which
// the canonical block order places after it: the one forward reference the
// language requires, and the reason an undeclared event is W411 rather than a
// reference finding.
func (documentValidator *validator) buildControls() {
	block, present := documentValidator.block("controls")
	if !present {
		return
	}
	for _, declaration := range block.Declarations {
		read := documentValidator.readBlockDeclaration(declaration, controlDeclaration)
		control := &ir.Control{Name: read.name.text, Span: declaration.Span()}
		if caption, captioned := read.clauses.first("caption"); captioned {
			control.CaptionLabel = documentValidator.referenceLabel(caption.value(0))
		}
		if trigger, triggered := read.clauses.first("trigger"); triggered && trigger.value(0).sound {
			control.Trigger = ir.Trigger(trigger.value(0).text)
		}
		if emits, emitting := read.clauses.first("emits"); emitting {
			control.Event = documentValidator.readEmittedEvent(emits)
		}
		if pressed, pressable := read.clauses.first("pressedWhen"); pressable {
			control.PressedWhen = documentValidator.referencePredicate(pressed.value(0), referenceOptions{})
		}
		if documentValidator.bind(read.name, control) {
			documentValidator.document.Controls = append(documentValidator.document.Controls, control)
		}
	}
	documentValidator.reportStrayStatements(block)
}

func (documentValidator *validator) readEmittedEvent(emits clause) *ir.EventDeclaration {
	value := emits.value(0)
	if !value.sound {
		return nil
	}
	found, declared := documentValidator.symbols.lookup(value.text)
	event, isEvent := recordOf[*ir.EventDeclaration](found, declared)
	if !isEvent {
		documentValidator.report(diag.New(
			diag.ClassControlEventUndeclared,
			emits.keyword.At,
			fmt.Sprintf("event %s is not declared in the `events` block, and a widget declares every event it emits", diag.Quote(value.text)),
			fmt.Sprintf("declare it as `event %s` with a `wire` name, or emit one of the declared events: %s; the runtime is default-deny and would refuse an undeclared name as UNKNOWN_EVENT at run time", value.text, diag.Enumerate(documentValidator.symbols.namesOfKind(kindEvent), value.text)),
		))
		return nil
	}
	documentValidator.referenced[value.text] = true
	return event
}

// buildStreams reads the declared subscriptions. A stream carries a source name
// and nothing else — no address, no credential.
func (documentValidator *validator) buildStreams() {
	block, present := documentValidator.block("data")
	if !present {
		return
	}
	for _, declaration := range block.Declarations {
		read := documentValidator.readBlockDeclaration(declaration, streamDeclaration)
		stream := &ir.Stream{Name: read.name.text, Span: declaration.Span()}
		if source, sourced := read.clauses.first("source"); sourced && source.value(0).sound {
			stream.Source = source.value(0).text
			documentValidator.checkPortability(source.value(0).token)
		}
		if delivers, delivering := read.clauses.first("delivers"); delivering {
			stream.Delivers = documentValidator.referenceEvent(delivers.value(0))
		}
		if ordering, ordered := read.clauses.first("ordering"); ordered {
			stream.Ordering = documentValidator.readOrdering(ordering)
		}
		if documentValidator.bind(read.name, stream) {
			documentValidator.document.Streams = append(documentValidator.document.Streams, stream)
		}
	}
	documentValidator.reportStrayStatements(block)
}

func (documentValidator *validator) readOrdering(ordering clause) *ir.StateField {
	field := documentValidator.referenceField(ordering.value(0))
	if field == nil {
		return nil
	}
	if field.Type != ir.FieldCounter {
		documentValidator.report(diag.New(
			diag.ClassOrderingFieldNotCounter,
			ordering.keyword.At,
			fmt.Sprintf("state field %s is a `%s`, and a stream's `ordering` names a `counter`", diag.Quote(field.Name), field.Type),
			fmt.Sprintf("name a declared counter — %s — or drop the clause if the stream delivers no ordering", diag.Enumerate(documentValidator.counterNames(), field.Name)),
		).WithRelated(field.Span.Start(), fmt.Sprintf("%s is declared here as a `%s`", diag.Quote(field.Name), field.Type)))
		return nil
	}
	return field
}

func (documentValidator *validator) referenceEvent(value argumentValue) *ir.EventDeclaration {
	found, resolved := documentValidator.reference(value, kindEvent, referenceOptions{})
	event, isEvent := recordOf[*ir.EventDeclaration](found, resolved)
	if !isEvent {
		return nil
	}
	return event
}

func (documentValidator *validator) referenceEdge(value argumentValue) *ir.Edge {
	found, resolved := documentValidator.reference(value, kindEdge, referenceOptions{})
	edge, isEdge := recordOf[*ir.Edge](found, resolved)
	if !isEdge {
		return nil
	}
	return edge
}

func minimumInt(first int, second int) int {
	if first < second {
		return first
	}
	return second
}

// declaredName reads a declaration's identifier without reporting anything, for
// the sweep that registers records before their clauses are read.
func declaredName(header parse.Statement) (argumentValue, bool) {
	if len(header.Arguments) == 0 || header.Arguments[0].Kind != lex.KindWord {
		return argumentValue{}, false
	}
	token := header.Arguments[0]
	return argumentValue{token: token, text: token.Text, sound: true}, true
}

// reportStrayStatements reports every line of a block that is not one of its
// declarations, for the blocks whose members are all nested declarations.
func (documentValidator *validator) reportStrayStatements(block *parse.Block) {
	documentValidator.readBlockMembers(block, nil, nil)
}
