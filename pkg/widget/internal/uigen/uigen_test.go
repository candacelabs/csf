package uigen_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/widget/internal/ir"
	"github.com/candacelabs/csf/pkg/widget/internal/uigen"
)

// generatedMarker is the line every gate in this repository recognises a
// generated file by: the coverage denominator, the Go reuse check and the style
// census all read it. A generated file without it is measured as if somebody had
// written it.
const generatedMarker = `^// Code generated .* DO NOT EDIT\.$`

// options are what every spec here generates with; only the collision and
// package-name specs vary them.
var options = uigen.Options{Package: "nodestatus"}

// literal builds a text template of one literal run.
func literal(text string) *ir.TextTemplate {
	return &ir.TextTemplate{Segments: []ir.TemplateSegment{{Literal: text}}}
}

// builder assembles an IR document the way the interpreter would, one piece at a
// time, so a spec can vary exactly the piece it is about.
//
// The IR is built here rather than interpreted from a document on disk because
// what these specs are about is emission: the interpreter has its own suite and
// its own golden examples, and reaching through it would make an emission
// failure look like a parse failure.
type builder struct {
	document *ir.Document
}

// newBuilder returns the minimal sound document: one flag field, two bindings,
// four labels, one role, one placement, one node, one event and one stream. It
// is the node-status widget, which is the smallest legal document the dialect
// has.
func newBuilder() *builder {
	reachable := &ir.StateField{Name: "reachable", Type: ir.FieldFlag}
	predicate := &ir.Predicate{Name: "reachable", Kind: ir.PredicateAtomic, Field: reachable}

	statusText := &ir.Binding{
		Name: "statusText",
		Clauses: []*ir.BindingClause{{
			Polarity: ir.GuardWhen, Predicate: predicate, Template: literal("reachable"),
		}},
		Otherwise: literal("unreachable"),
	}
	descriptionText := &ir.Binding{
		Name: "sceneDescriptionText",
		Clauses: []*ir.BindingClause{{
			Polarity: ir.GuardWhen, Predicate: predicate, Template: literal("One node; passing."),
		}},
		Otherwise: literal("One node; failing."),
	}

	titleLabel := &ir.Label{Name: "titleLabel", SourceKind: ir.LabelLiteral, Literal: literal("Node status")}
	descriptionLabel := &ir.Label{
		Name: "sceneDescriptionLabel", SourceKind: ir.LabelBound, Binding: descriptionText,
	}
	nodeLabel := &ir.Label{Name: "nodeALabel", SourceKind: ir.LabelLiteral, Literal: literal("node-a")}
	statusLabel := &ir.Label{Name: "statusLabel", SourceKind: ir.LabelBound, Binding: statusText}

	role := &ir.Role{Name: "node", Token: ir.TokenAccent, Marker: ir.MarkerLarge}
	placement := &ir.Placement{Name: "centre", Left: 50, Top: 50}
	node := &ir.Node{
		Name: "nodeA", Role: role, Placement: placement,
		TitleLabel: nodeLabel, CaptionLabel: statusLabel,
	}

	titleSlot := &ir.Slot{Kind: ir.SlotTitle, Label: titleLabel}
	descriptionSlot := &ir.Slot{Kind: ir.SlotDescription, Label: descriptionLabel}

	health := &ir.EventDeclaration{
		Name: "health", Wire: "widget.node-status.health",
		Fields: []*ir.EventField{{WireName: "reachable", Writes: reachable, Type: ir.FieldFlag}},
	}

	return &builder{document: &ir.Document{
		Name:           "NodeStatus",
		DialectVersion: 0,
		Region:         "widget.node-status",
		Palette:        "fieldStation",
		StateFields:    []*ir.StateField{reachable},
		Predicates:     []*ir.Predicate{predicate},
		Bindings:       []*ir.Binding{statusText, descriptionText},
		Labels:         []*ir.Label{titleLabel, descriptionLabel, nodeLabel, statusLabel},
		Slots:          []*ir.Slot{titleSlot, descriptionSlot},
		Roles:          []*ir.Role{role},
		Placements:     []*ir.Placement{placement},
		Scene: &ir.Scene{
			Name: "solo", DescriptionSlot: descriptionSlot, Nodes: []*ir.Node{node},
		},
		Events:  []*ir.EventDeclaration{health},
		Streams: []*ir.Stream{{Name: "healthWatch", Source: "widget.node-status.watch", Delivers: health}},
		// Computed by the interpreter, never authored: the one field both
		// bindings read.
		DirtyProjection: ir.DirtyProjection{Fields: []*ir.StateField{reachable}},
	}}
}

func (documentBuilder *builder) build() *ir.Document { return documentBuilder.document }

// with applies one edit and returns the document, so a spec reads as "the
// minimal document, except …".
func (documentBuilder *builder) with(edit func(document *ir.Document)) *ir.Document {
	edit(documentBuilder.document)
	return documentBuilder.document
}

// withFullScene adds everything the minimal document leaves out: a second node,
// an orbit, a channel with its legend, an edge carrying it, a motion block with
// one pulse and one emphasis, an indicator and a control. It is the flagship
// document's shape at the smallest size that still reaches every emission path.
//
// The two computed records are filled the way the interpreter fills them — the
// legend from the declared channel, the dirty projection from what the widget
// reads — because a generator that re-derived either would be contradicting the
// thing it was handed.
func (documentBuilder *builder) withFullScene() *ir.Document {
	document := documentBuilder.document
	reachable := document.StateFields[0]
	reachablePredicate := document.Predicates[0]
	nodeLabel, statusLabel := document.Labels[2], document.Labels[3]

	sequence := &ir.StateField{Name: "sequence", Type: ir.FieldCounter}
	paused := &ir.StateField{Name: "paused", Type: ir.FieldFlag}
	pausedPredicate := &ir.Predicate{Name: "paused", Kind: ir.PredicateAtomic, Field: paused}
	document.StateFields = append(document.StateFields, sequence, paused)
	document.Predicates = append(document.Predicates, pausedPredicate)

	legendLabel := &ir.Label{Name: "beatLegendLabel", SourceKind: ir.LabelLiteral, Literal: literal("beat")}
	captionLabel := &ir.Label{Name: "toggleCaptionLabel", SourceKind: ir.LabelLiteral, Literal: literal("Pause")}
	document.Labels = append(document.Labels, legendLabel, captionLabel)

	channel := &ir.Channel{
		Name: "beat", Direction: ir.DirectionForward, Token: ir.TokenAccent, LegendLabel: legendLabel,
	}
	document.Channels = append(document.Channels, channel)
	document.Legend.Entries = append(document.Legend.Entries,
		ir.LegendEntry{Channel: channel, Label: legendLabel})

	peerRole := &ir.Role{Name: "peer", Token: ir.TokenPositive, Marker: ir.MarkerSmall}
	east := &ir.Placement{Name: "east", Left: 80, Top: 20}
	peer := &ir.Node{Name: "nodeB", Role: peerRole, Placement: east, TitleLabel: nodeLabel}
	document.Roles = append(document.Roles, peerRole)
	document.Placements = append(document.Placements, east)
	document.Scene.Orbits = append(document.Scene.Orbits, &ir.Orbit{Name: "ring", Token: ir.TokenRule})
	document.Scene.Nodes = append(document.Scene.Nodes, peer)

	// The geometry of (50,50) → (80,20), which is what the interpreter computes
	// from the two placements and what no author may write.
	edge := &ir.Edge{
		Name: "link", From: document.Scene.Nodes[0], To: peer, Channels: []*ir.Channel{channel},
		Geometry: ir.EdgeGeometry{LengthPercent: 42.42640687, AngleDegrees: -45},
	}
	document.Scene.Edges = append(document.Scene.Edges, edge)

	toggle := &ir.EventDeclaration{
		Name: "toggleMotion", Wire: "widget.node-status.toggle", Toggles: paused,
	}
	document.Events = append(document.Events, toggle)

	document.Motion = &ir.Motion{
		HostStatusGate: true,
		Requires:       []*ir.Predicate{reachablePredicate},
		Forbids:        []*ir.Predicate{pausedPredicate},
		RestartOn:      sequence,
		Pulses: []*ir.Pulse{{
			Name: "beatOut", Edge: edge, Channel: channel,
			DurationMilliseconds: 820, DelayMilliseconds: 180,
		}},
		Emphases: []*ir.Emphasis{{
			Name: "coreRing", Node: document.Scene.Nodes[0], DurationMilliseconds: 720,
		}},
	}
	document.Indicators = append(document.Indicators, &ir.Indicator{
		Name: "connection", Label: statusLabel, Predicate: reachablePredicate,
	})
	document.Controls = append(document.Controls, &ir.Control{
		Name: "motionToggle", CaptionLabel: captionLabel, Trigger: ir.TriggerClick,
		Event: toggle, PressedWhen: pausedPredicate,
	})
	document.DirtyProjection.Fields = []*ir.StateField{reachable, sequence, paused}
	return document
}

// generate is Generate with the failure asserted away, returning the two
// artifacts as text keyed by path.
func generate(document *ir.Document) map[string]string {
	artifacts, generateError := uigen.Generate(document, options)
	ExpectWithOffset(1, generateError).ToNot(HaveOccurred())
	files := map[string]string{}
	for _, artifact := range artifacts {
		files[artifact.Path] = string(artifact.Data)
	}
	return files
}

func scaffoldOf(document *ir.Document) string { return generate(document)["widget.gen.go"] }
func viewOf(document *ir.Document) string     { return generate(document)["view.templ"] }

var _ = Describe("Generate", func() {
	It("emits the view and the scaffold, view first", func() {
		artifacts, generateError := uigen.Generate(newBuilder().build(), options)

		Expect(generateError).ToNot(HaveOccurred())
		Expect(artifacts).To(HaveLen(2))
		Expect(artifacts[0].Path).To(Equal("view.templ"))
		Expect(artifacts[1].Path).To(Equal("widget.gen.go"))
	})

	It("opens every file with the marker every gate in this repository reads", func() {
		for path, contents := range generate(newBuilder().build()) {
			By(path)
			Expect(firstLine(contents)).To(MatchRegexp(generatedMarker))
		}
	})

	It("produces byte-identical output twice, which is the whole reason it returns bytes", func() {
		first, firstError := uigen.Generate(newBuilder().build(), options)
		second, secondError := uigen.Generate(newBuilder().build(), options)

		Expect(firstError).ToNot(HaveOccurred())
		Expect(secondError).ToNot(HaveOccurred())
		Expect(second).To(Equal(first))
	})

	It("writes nothing of its own, so a caller decides when the filesystem changes", func() {
		directory := GinkgoT().TempDir()
		_, generateError := uigen.Generate(newBuilder().build(), options)

		Expect(generateError).ToNot(HaveOccurred())
		Expect(os.ReadDir(directory)).To(BeEmpty())
	})
})

var _ = Describe("The generated scaffold", func() {
	It("declares the package the caller asked for", func() {
		Expect(scaffoldOf(newBuilder().build())).To(ContainSubstring("package nodestatus\n"))
	})

	It("names the widget, its region and its palette as constants, because each is a contract in two places", func() {
		scaffold := scaffoldOf(newBuilder().build())

		Expect(scaffold).To(ContainSubstring(`NodeStatusName    = "NodeStatus"`))
		Expect(scaffold).To(ContainSubstring(`NodeStatusRegion  = "widget.node-status"`))
		Expect(scaffold).To(ContainSubstring(`NodeStatusPalette = "fieldStation"`))
		Expect(scaffold).To(ContainSubstring(`NodeStatusTitleID = "widget.node-status-title"`))
		Expect(scaffold).To(ContainSubstring(`NodeStatusEventHealth = "widget.node-status.health"`))
	})

	It("asserts at compile time that it satisfies the SDK contract, and declares its own dirty test", func() {
		scaffold := scaffoldOf(newBuilder().build())

		// The contract assertion instantiates on live.AnonymousIdentity: a
		// generated widget is generic in the HOST's identity type since
		// 2026-09-03, so a compile-time assertion has to pick one, and the
		// identity for a host with no accounts is the honest pick. Nothing in
		// the generated body branches on it.
		Expect(scaffold).To(ContainSubstring(
			"_ widget.IWidget[NodeStatusState, live.AnonymousIdentity] = (*NodeStatus[live.AnonymousIdentity])(nil)"))
		Expect(scaffold).To(ContainSubstring(
			"_ widget.IDirtyDeclarer[NodeStatusState]                  = (*NodeStatus[live.AnonymousIdentity])(nil)"))
	})

	It("retains the default constructor and routes an assigned instance region through registration and render", func() {
		scaffold := scaffoldOf(newBuilder().build())

		Expect(scaffold).To(ContainSubstring("return NewNodeStatusAt[I](NodeStatusRegion)"))
		Expect(scaffold).To(ContainSubstring("func NewNodeStatusAt[I live.IIdentity](region string) *NodeStatus[I]"))
		Expect(scaffold).To(ContainSubstring("return &NodeStatus[I]{region: region}"))
		Expect(scaffold).To(ContainSubstring("Region:   instance.region,"))
		Expect(scaffold).To(ContainSubstring("return NodeStatusViewAt(state, instance.region)"))
	})

	It("carries the declared stream into the registration, for the host to resolve", func() {
		Expect(scaffoldOf(newBuilder().build())).To(ContainSubstring(
			`{Name: "healthWatch", Source: "widget.node-status.watch", Delivers: NodeStatusEventHealth},`))
	})

	Describe("the browser-sendable split", func() {
		// The minimal document's one event is delivered by its one stream, so
		// it is the whole of the Internal side and there is no Events side at
		// all — which is the shape the P2 audit's forgery needed and did not
		// get.
		It("puts a stream-delivered event on the internal side and registers nothing", func() {
			scaffold := scaffoldOf(newBuilder().build())

			Expect(scaffold).To(ContainSubstring("Internal: []string{NodeStatusEventHealth},"))
			Expect(scaffold).ToNot(ContainSubstring("Events:"))
		})

		It("puts an event no stream delivers on the browser-sendable side", func() {
			scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
				document.Streams = nil
			}))

			Expect(scaffold).To(ContainSubstring("Events: []string{NodeStatusEventHealth},"))
			Expect(scaffold).ToNot(ContainSubstring("Internal:"))
		})

		It("splits a document that has one of each, in declaration order", func() {
			scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
				document.Events = append(document.Events, &ir.EventDeclaration{
					Name: "acknowledge", Wire: "widget.node-status.ack",
				})
			}))

			Expect(scaffold).To(ContainSubstring("Events:   []string{NodeStatusEventAcknowledge},"))
			Expect(scaffold).To(ContainSubstring("Internal: []string{NodeStatusEventHealth},"))
		})
	})

	Describe("the payload field names", func() {
		It("emits a constant per declared field, named under its own event", func() {
			scaffold := scaffoldOf(newBuilder().build())

			Expect(scaffold).To(ContainSubstring(`NodeStatusEventHealthFieldReachable = "reachable"`))
		})

		It("spells a lower_snake_case wire name as one Go identifier", func() {
			scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
				document.Events[0].Fields[0].WireName = "is_reachable"
			}))

			Expect(scaffold).To(ContainSubstring(`NodeStatusEventHealthFieldIsReachable = "is_reachable"`))
		})

		It("reads the wire through the constant rather than through a second literal", func() {
			Expect(scaffoldOf(newBuilder().build())).To(ContainSubstring(
				"if raw, present := event.Fields.Lookup(NodeStatusEventHealthFieldReachable); present {"))
		})

		It("carries the same names into the registration, for a host that cannot name them at compile time", func() {
			Expect(scaffoldOf(newBuilder().build())).To(ContainSubstring(
				"Payloads: []widget.EventPayload{\n" +
					"\t\t\t{Event: NodeStatusEventHealth, Fields: []string{\n" +
					"\t\t\t\tNodeStatusEventHealthFieldReachable,\n" +
					"\t\t\t}},\n\t\t},"))
		})

		It("emits no payload at all for a document whose events carry no fields", func() {
			scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
				document.Events[0].Fields = nil
			}))

			Expect(scaffold).ToNot(ContainSubstring("Payloads:"))
			Expect(scaffold).ToNot(ContainSubstring("NodeStatusEventHealthField"))
		})

		It("refuses a wire name with no exported Go spelling rather than emitting one that will not compile", func() {
			_, generateError := uigen.Generate(newBuilder().with(func(document *ir.Document) {
				document.Events[0].Fields[0].WireName = "_reachable"
			}), options)

			Expect(generateError).To(MatchError(uigen.ErrUnexportable))
		})

		It("reports two wire names that collide in Go, naming both", func() {
			_, generateError := uigen.Generate(newBuilder().with(func(document *ir.Document) {
				document.Events[0].Fields = append(document.Events[0].Fields, &ir.EventField{
					WireName: "Reachable",
					Writes:   document.StateFields[0],
					Type:     ir.FieldFlag,
				})
			}), options)

			Expect(generateError).To(MatchError(uigen.ErrNameCollision))
			Expect(generateError.Error()).To(ContainSubstring("Reachable and reachable"))
		})
	})

	DescribeTable("gives each state field its Go type",
		func(fieldType ir.FieldType, expected string) {
			scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
				document.StateFields[0].Type = fieldType
			}))

			Expect(scaffold).To(ContainSubstring("Reachable " + expected))
		},
		Entry("a flag is a bool", ir.FieldFlag, "bool"),
		Entry("a counter is unsigned, because it is monotonic", ir.FieldCounter, "uint64"),
		Entry("a count is an ordinary signed number", ir.FieldCount, "int64"),
		Entry("text is a string", ir.FieldText, "string"),
	)

	DescribeTable("reads each field type off the wire through the SDK's own reader",
		func(fieldType ir.FieldType, expected string) {
			scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
				document.StateFields[0].Type = fieldType
				document.Events[0].Fields[0].Type = fieldType
			}))

			Expect(scaffold).To(ContainSubstring("current.Reachable = " + expected))
		},
		Entry("a flag", ir.FieldFlag, "widget.ParseFlag(raw, current.Reachable)"),
		Entry("a counter", ir.FieldCounter, "widget.ParseCounter(raw, current.Reachable)"),
		Entry("a count", ir.FieldCount, "widget.ParseCount(raw, current.Reachable)"),
		Entry("text, which needs no reader at all", ir.FieldText, "raw"),
	)

	DescribeTable("renders each field type into a snapshot",
		func(fieldType ir.FieldType, expected string) {
			scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
				document.StateFields[0].Type = fieldType
			}))

			Expect(scaffold).To(ContainSubstring(`{Name: "reachable", Value: ` + expected + `}`))
		},
		Entry("a flag", ir.FieldFlag, "strconv.FormatBool(state.Reachable)"),
		Entry("a counter", ir.FieldCounter, "strconv.FormatUint(state.Reachable, 10)"),
		Entry("a count", ir.FieldCount, "strconv.FormatInt(state.Reachable, 10)"),
		Entry("text", ir.FieldText, "state.Reachable"),
	)

	It("imports strconv only when something is formatted through it", func() {
		Expect(scaffoldOf(newBuilder().build())).To(ContainSubstring(`"strconv"`))

		textOnly := scaffoldOf(newBuilder().with(func(document *ir.Document) {
			document.StateFields[0].Type = ir.FieldText
			document.Events[0].Fields[0].Type = ir.FieldText
		}))
		Expect(textOnly).ToNot(ContainSubstring(`"strconv"`))
	})

	It("emits a toggle as a negation rather than as a payload read", func() {
		scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
			document.Events[0].Toggles = document.StateFields[0]
			document.Events[0].Fields = nil
		}))

		Expect(scaffold).To(ContainSubstring("current.Reachable = !current.Reachable"))
		Expect(scaffold).ToNot(ContainSubstring("event.Fields.Lookup"))
	})

	It("says so when an event writes nothing, rather than emitting an empty case", func() {
		scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
			document.Events[0].Fields = nil
		}))

		Expect(scaffold).To(ContainSubstring("This event carries no payload and writes no field."))
	})

	It("handles the two runtime signals when a state field carries one", func() {
		scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
			document.StateFields[0].Signal = ir.SignalSlowClient
		}))

		Expect(scaffold).To(ContainSubstring("case live.SlowClientEvent:\n\t\tcurrent.Reachable = true"))
		Expect(scaffold).To(ContainSubstring("case live.ClientRecoveredEvent:\n\t\tcurrent.Reachable = false"))
	})

	It("emits no switch at all for a widget with no event and no signal", func() {
		scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
			document.Events = nil
			document.Streams = nil
		}))

		Expect(scaffold).ToNot(ContainSubstring("switch event.Name"))
		Expect(scaffold).ToNot(ContainSubstring("Events: []string{"))
	})

	Describe("what a document's motion, indicators and controls derive", func() {
		var scaffold string

		BeforeEach(func() { scaffold = scaffoldOf(newBuilder().withFullScene()) })

		It("compiles the motion gate into one predicate over state", func() {
			Expect(scaffold).To(ContainSubstring(
				"func (state NodeStatusState) MotionActive() bool {\n" +
					"\treturn state.Reachable && !state.Paused\n}"))
			Expect(scaffold).To(ContainSubstring(
				"func (state NodeStatusState) MotionActiveText() string {\n" +
					"\treturn strconv.FormatBool(state.MotionActive())\n}"))
		})

		It("makes the tick an identity, because a finished animation restarts by being a new element", func() {
			Expect(scaffold).To(ContainSubstring(
				"func (state NodeStatusState) MotionTickID() string {\n" +
					"\treturn state.MotionTickIDAt(NodeStatusRegion)\n}"))
			Expect(scaffold).To(ContainSubstring(
				"func (state NodeStatusState) MotionTickIDAt(region string) string {\n" +
					"\treturn region + \"-tick-\" + strconv.FormatUint(state.Sequence, 10)\n}"))
		})

		It("selects an indicator's tone between the two tokens the ontology fixes", func() {
			Expect(scaffold).To(ContainSubstring(
				"func (state NodeStatusState) ConnectionTone() string {\n" +
					"\tif state.Reachable {\n\t\treturn \"positive\"\n\t}\n\treturn \"warning\"\n}"))
		})

		It("renders a control's pressed state as the attribute value it becomes", func() {
			Expect(scaffold).To(ContainSubstring(
				"func (state NodeStatusState) MotionTogglePressedText() string {\n" +
					"\treturn strconv.FormatBool(state.Paused)\n}"))
		})

		It("emits a gate over nothing as true rather than as an empty expression", func() {
			document := newBuilder().withFullScene()
			document.Motion.Requires, document.Motion.Forbids = nil, nil

			Expect(scaffoldOf(document)).To(ContainSubstring(
				"func (state NodeStatusState) MotionActive() bool {\n\treturn true\n}"))
		})

		It("emits no pressed method for a control that declared no pressed state", func() {
			document := newBuilder().withFullScene()
			document.Controls[0].PressedWhen = nil

			Expect(scaffoldOf(document)).ToNot(ContainSubstring("MotionTogglePressedText"))
		})
	})

	Describe("the dirty declaration", func() {
		It("compares exactly the fields of the document's computed projection", func() {
			scaffold := scaffoldOf(newBuilder().withFullScene())

			Expect(scaffold).To(ContainSubstring(
				"_ widget.IDirtyDeclarer[NodeStatusState]                  = (*NodeStatus[live.AnonymousIdentity])(nil)"))
			Expect(scaffold).To(ContainSubstring(
				"func (instance *NodeStatus[I]) Dirty(previous NodeStatusState, next NodeStatusState) bool {\n" +
					"\treturn previous.Reachable != next.Reachable ||\n" +
					"\t\tprevious.Sequence != next.Sequence ||\n" +
					"\t\tprevious.Paused != next.Paused\n}"))
		})

		It("declares a widget whose markup reads no state clean, rather than declaring nothing", func() {
			document := newBuilder().build()
			document.DirtyProjection.Fields = nil

			Expect(scaffoldOf(document)).To(ContainSubstring(
				"func (instance *NodeStatus[I]) Dirty(previous NodeStatusState, next NodeStatusState) bool {\n" +
					"\t// Nothing this widget renders reads state, so no transition can move it.\n" +
					"\treturn false\n}"))
		})
	})

	Describe("the pure derivations the view reads", func() {
		It("emits a binding as its clauses in order, with the otherwise arm last", func() {
			Expect(scaffoldOf(newBuilder().build())).To(ContainSubstring(
				"func (state NodeStatusState) StatusText() string {\n" +
					"\tif state.Reachable {\n\t\treturn \"reachable\"\n\t}\n\treturn \"unreachable\"\n}"))
		})

		It("negates a whenNot clause, because the two polarities are two constructs", func() {
			scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
				document.Bindings[0].Clauses[0].Polarity = ir.GuardWhenNot
			}))

			Expect(scaffold).To(ContainSubstring("if !state.Reachable {"))
		})

		It("interpolates a field into a template as a concatenation", func() {
			scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
				document.StateFields[0].Type = ir.FieldCount
				document.Bindings[0].Otherwise = &ir.TextTemplate{Segments: []ir.TemplateSegment{
					{Literal: "seen "},
					{Field: document.StateFields[0]},
					{Literal: " times"},
				}}
			}))

			Expect(scaffold).To(ContainSubstring(
				`return "seen " + strconv.FormatInt(state.Reachable, 10) + " times"`))
		})

		It("emits an empty template as the empty string rather than as nothing", func() {
			scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
				document.Bindings[0].Otherwise = &ir.TextTemplate{}
			}))

			Expect(scaffold).To(ContainSubstring("\treturn \"\"\n}"))
		})

		It("emits a literal label as its text and a bound one as a call", func() {
			scaffold := scaffoldOf(newBuilder().build())

			Expect(scaffold).To(ContainSubstring(
				"func (state NodeStatusState) TitleLabel() string {\n\treturn \"Node status\"\n}"))
			Expect(scaffold).To(ContainSubstring(
				"func (state NodeStatusState) StatusLabel() string {\n\treturn state.StatusText()\n}"))
		})

		It("emits no method for an atomic predicate, which is already the struct field", func() {
			Expect(scaffoldOf(newBuilder().build())).
				ToNot(ContainSubstring("func (state NodeStatusState) Reachable()"))
		})

		It("emits a composed predicate as everything it requires, nothing it forbids, and its bounds", func() {
			scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
				count := &ir.StateField{Name: "seen", Type: ir.FieldCount}
				degraded := &ir.StateField{Name: "degraded", Type: ir.FieldFlag}
				degradedPredicate := &ir.Predicate{
					Name: "degraded", Kind: ir.PredicateAtomic, Field: degraded,
				}
				document.StateFields = append(document.StateFields, count, degraded)
				document.Predicates = append(document.Predicates, degradedPredicate, &ir.Predicate{
					Name:     "healthy",
					Kind:     ir.PredicateComposed,
					Requires: []*ir.Predicate{document.Predicates[0]},
					Forbids:  []*ir.Predicate{degradedPredicate},
					Bounds: []ir.NumericBound{
						{Field: count, Comparison: ir.ComparisonAtLeast, Value: 3},
						{Field: count, Comparison: ir.ComparisonAtMost, Value: 9},
					},
				})
			}))

			Expect(scaffold).To(ContainSubstring(
				"return state.Reachable && !state.Degraded && state.Seen >= 3 && state.Seen <= 9"))
		})

		It("emits a composition with no terms as true, rather than as an empty expression", func() {
			scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
				document.Predicates = append(document.Predicates,
					&ir.Predicate{Name: "always", Kind: ir.PredicateComposed})
			}))

			Expect(scaffold).To(ContainSubstring(
				"func (state NodeStatusState) Always() bool {\n\treturn true\n}"))
		})

		It("calls a composed predicate as a method when a clause guards on it", func() {
			scaffold := scaffoldOf(newBuilder().with(func(document *ir.Document) {
				composed := &ir.Predicate{
					Name: "healthy", Kind: ir.PredicateComposed,
					Requires: []*ir.Predicate{document.Predicates[0]},
				}
				document.Predicates = append(document.Predicates, composed)
				document.Bindings[0].Clauses[0].Predicate = composed
			}))

			Expect(scaffold).To(ContainSubstring("if state.Healthy() {"))
		})
	})
})

var _ = Describe("The generated view", func() {
	It("keeps the default view and accepts a host-assigned instance region", func() {
		view := viewOf(newBuilder().build())
		Expect(view).To(ContainSubstring("templ NodeStatusView(state NodeStatusState) {\n" +
			"\t@NodeStatusViewAt(state, NodeStatusRegion)\n}"))
		Expect(view).To(ContainSubstring("templ NodeStatusViewAt(state NodeStatusState, region string) {"))
		Expect(view).To(ContainSubstring(
			`<aside { live.Region(region)... } class="widget"` +
				` aria-labelledby={ titleID }` +
				` data-widget="NodeStatus" data-palette="fieldStation">`))
	})

	Describe("the widget's landmark", func() {
		// A widget is a complementary landmark, which is what lets a screen
		// reader user jump to it and — more to the point — skip it. Both halves
		// are load-bearing: HTML-AAM maps a nameless <aside> inside sectioning
		// content to `generic`, so a landmark with no accessible name is not a
		// landmark at all. The hand-written card this generator replaced was an
		// <aside aria-labelledby>, and the generated one was a bare <section>
		// with no name, which is the regression these two specs are about.
		It("is an aside labelled by its own title", func() {
			view := viewOf(newBuilder().build())

			Expect(view).To(ContainSubstring(`<aside { live.Region(region)... }`))
			Expect(view).To(ContainSubstring(`aria-labelledby={ titleID }`))
			Expect(view).To(ContainSubstring(`<h2 class="widget-title widget-token-ink" id={ titleID }>`))
		})

		It("derives the label and labelled element once from the instance region", func() {
			// Two spellings of one identifier is an aria-labelledby pointing at
			// nothing, which reads to a screen reader exactly like no name.
			view := viewOf(newBuilder().build())

			Expect(view).To(ContainSubstring(`{{ titleID := region + "-title" }}`))
			Expect(strings.Count(view, "{ titleID }")).To(Equal(2))
		})
	})

	Describe("the chrome's reading order", func() {
		It("emits the source line before the title, because that is the order it is read in", func() {
			view := viewOf(newBuilder().with(func(document *ir.Document) {
				document.Slots = append(document.Slots, &ir.Slot{
					Kind:  ir.SlotSource,
					Label: document.Labels[2],
				})
			}))

			source := strings.Index(view, `class="widget-source`)
			title := strings.Index(view, `class="widget-title`)
			Expect(source).To(BeNumerically(">", 0))
			Expect(source).To(BeNumerically("<", title),
				"the generator emitted the heading first and let a host stylesheet reverse it, "+
					"which left DOM order and reading order disagreeing for everything that follows the DOM")
		})
	})

	It("puts the scene's description on the image as its text alternative", func() {
		Expect(viewOf(newBuilder().build())).To(ContainSubstring(
			`role="img" aria-label={ state.SceneDescriptionLabel() }`))
	})

	It("places a node at its declared percentage of the normalized box", func() {
		view := viewOf(newBuilder().with(func(document *ir.Document) {
			document.Placements[0].Left, document.Placements[0].Top = 20, 80
		}))

		Expect(view).To(ContainSubstring(`transform="translate(20,80)"`))
		Expect(view).To(ContainSubstring(`viewBox="0 0 100 100"`))
	})

	It("writes the role's token and marker as classes, so no widget owns a colour", func() {
		Expect(viewOf(newBuilder().build())).To(ContainSubstring(
			`class="widget-marker widget-marker-large widget-token-accent" r="9"`))
	})

	It("draws a small marker smaller, and moves its labels in with it", func() {
		view := viewOf(newBuilder().with(func(document *ir.Document) {
			document.Roles[0].Marker = ir.MarkerSmall
		}))

		Expect(view).To(ContainSubstring(`widget-marker-small widget-token-accent" r="5"`))
		Expect(view).To(ContainSubstring(`y="-10"`))
		Expect(view).To(ContainSubstring(`y="15"`))
	})

	It("omits a node's title and caption when it declared neither", func() {
		view := viewOf(newBuilder().with(func(document *ir.Document) {
			document.Scene.Nodes[0].TitleLabel = nil
			document.Scene.Nodes[0].CaptionLabel = nil
		}))

		Expect(view).ToNot(ContainSubstring("widget-node-title"))
		Expect(view).ToNot(ContainSubstring("widget-node-caption"))
	})

	It("emits the source line only when the widget filled that slot", func() {
		Expect(viewOf(newBuilder().build())).ToNot(ContainSubstring("widget-source"))

		view := viewOf(newBuilder().with(func(document *ir.Document) {
			document.Slots = append(document.Slots,
				&ir.Slot{Kind: ir.SlotSource, Label: document.Labels[2]})
		}))
		Expect(view).To(ContainSubstring(`<p class="widget-source widget-token-muted">{ state.NodeALabel() }</p>`))
	})

	Describe("a scene with everything in it", func() {
		var view string

		BeforeEach(func() { view = viewOf(newBuilder().withFullScene()) })

		It("draws an orbit whose geometry it derived, because a document declares none", func() {
			Expect(view).To(ContainSubstring(
				`<ellipse class="widget-orbit widget-token-rule" cx="50" cy="50" rx="41" ry="29"` +
					` transform="rotate(-9 50 50)"></ellipse>`))
		})

		It("places an edge at its from endpoint and turns it by the computed angle", func() {
			Expect(view).To(ContainSubstring(
				`<g class="widget-edge" transform="translate(50,50) rotate(-45)"` +
					` style="--widget-edge-length: 42.43px">`))
			Expect(view).To(ContainSubstring(
				`<line class="widget-edge-line widget-token-rule" x1="0" y1="0" x2="42.43" y2="0"></line>`))
		})

		It("gives a pulse its channel's direction and token, and the document's own timing", func() {
			Expect(view).To(ContainSubstring(
				`<circle class="widget-pulse widget-pulse-forward widget-token-accent" r="2"` +
					` style="--widget-pulse-duration: 820ms; --widget-pulse-delay: 180ms"></circle>`))
		})

		It("draws an emphasis ring behind its node's marker, in the role's token", func() {
			Expect(view).To(ContainSubstring(
				`<circle class="widget-emphasis widget-token-accent" r="11"` +
					` style="--widget-emphasis-duration: 720ms; --widget-emphasis-delay: 0ms"></circle>` + "\n" +
					"\t\t\t\t" + `<circle class="widget-marker widget-marker-large widget-token-accent"`))
		})

		It("renders the legend the interpreter projected, rather than one an author wrote", func() {
			Expect(view).To(ContainSubstring(
				`<li class="widget-legend-entry widget-token-muted">` +
					`<span class="widget-swatch widget-token-accent" aria-hidden="true"></span>` +
					`{ state.BeatLegendLabel() }</li>`))
		})

		It("carries the widget's own half of the motion gate, and the tick that re-arms it", func() {
			Expect(view).To(ContainSubstring(`data-motion={ state.MotionActiveText() }>`))
			Expect(view).To(ContainSubstring(`<svg class="widget-scene" id={ state.MotionTickIDAt(region) } viewBox=`))
		})

		It("selects an indicator's tone with a predicate and keeps its label beside it", func() {
			Expect(view).To(ContainSubstring(
				`<li class="widget-indicator widget-token-muted" data-tone={ state.ConnectionTone() }>` +
					`<span class="widget-indicator-dot" aria-hidden="true"></span>` +
					`{ state.StatusLabel() }</li>`))
		})

		It("binds a control to the one event it emits, with its pressed state", func() {
			Expect(view).To(ContainSubstring(
				`<button class="widget-control" type="button"` +
					` aria-pressed={ state.MotionTogglePressedText() }` +
					` { live.On("click", NodeStatusEventToggleMotion)... }>` +
					`{ state.ToggleCaptionLabel() }</button>`))
		})
	})

	It("gives a control with no pressed-state predicate no aria-pressed at all", func() {
		document := newBuilder().withFullScene()
		document.Controls[0].PressedWhen = nil

		view := viewOf(document)

		Expect(view).To(ContainSubstring(`<button class="widget-control" type="button" { live.On(`))
		Expect(view).ToNot(ContainSubstring("aria-pressed"))
	})

	It("gives a widget with no motion no gate attribute and no tick identity", func() {
		view := viewOf(newBuilder().build())

		Expect(view).ToNot(ContainSubstring("data-motion"))
		Expect(view).To(ContainSubstring(`<svg class="widget-scene" viewBox=`))
	})

	It("emits no legend, no indicators and no controls for a widget that declares none", func() {
		view := viewOf(newBuilder().build())

		Expect(view).ToNot(ContainSubstring("widget-legend"))
		Expect(view).ToNot(ContainSubstring("widget-indicator"))
		Expect(view).ToNot(ContainSubstring("widget-control"))
	})

	It("emits the stats in declaration order, or no list at all", func() {
		Expect(viewOf(newBuilder().build())).ToNot(ContainSubstring("widget-stats"))

		view := viewOf(newBuilder().with(func(document *ir.Document) {
			document.Slots = append(document.Slots,
				&ir.Slot{Kind: ir.SlotStat, Label: document.Labels[2], Ordinal: 0},
				&ir.Slot{Kind: ir.SlotStat, Label: document.Labels[3], Ordinal: 1})
		}))
		Expect(view).To(ContainSubstring("<ul class=\"widget-stats\">\n" +
			"\t\t\t<li class=\"widget-stat widget-token-ink\">{ state.NodeALabel() }</li>\n" +
			"\t\t\t<li class=\"widget-stat widget-token-ink\">{ state.StatusLabel() }</li>\n" +
			"\t\t</ul>"))
	})
})

var _ = Describe("What Generate refuses", func() {
	It("refuses three constructs and no others", func() {
		Expect(uigen.Refusals()).To(Equal([]string{
			"a control with a change trigger",
			"a control with an input trigger",
			"a control with a submit trigger",
		}))
	})

	It("emits the widest document the dialect has, so the list above is the whole of it", func() {
		_, generateError := uigen.Generate(newBuilder().withFullScene(), options)

		Expect(generateError).ToNot(HaveOccurred())
	})

	DescribeTable("names each construct it does not emit",
		func(trigger ir.Trigger, construct string) {
			_, generateError := uigen.Generate(newBuilder().with(func(document *ir.Document) {
				document.Controls = []*ir.Control{{
					Name: "retry", Trigger: trigger,
					CaptionLabel: document.Labels[2], Event: document.Events[0],
				}}
			}), options)

			var unsupportedError *uigen.UnsupportedError
			Expect(errors.As(generateError, &unsupportedError)).To(BeTrue())
			Expect(unsupportedError.Widget).To(Equal("NodeStatus"))
			Expect(unsupportedError.Constructs).To(ConsistOf(construct))
			Expect(generateError.Error()).To(ContainSubstring(construct))
		},
		Entry("a change trigger has no element to bind to", ir.TriggerChange, "a control with a change trigger"),
		Entry("nor does an input trigger", ir.TriggerInput, "a control with an input trigger"),
		Entry("nor does a submit trigger, which needs a form", ir.TriggerSubmit, "a control with a submit trigger"),
	)

	It("names every unsupported construct rather than the first, in the list's own order", func() {
		_, generateError := uigen.Generate(newBuilder().with(func(document *ir.Document) {
			document.Controls = []*ir.Control{
				{Name: "submit", Trigger: ir.TriggerSubmit, CaptionLabel: document.Labels[2], Event: document.Events[0]},
				{Name: "typed", Trigger: ir.TriggerInput, CaptionLabel: document.Labels[2], Event: document.Events[0]},
			}
		}), options)

		var unsupportedError *uigen.UnsupportedError
		Expect(errors.As(generateError, &unsupportedError)).To(BeTrue())
		Expect(unsupportedError.Constructs).To(Equal([]string{
			"a control with an input trigger", "a control with a submit trigger",
		}))
	})

	DescribeTable("refuses an output package Go could not compile",
		func(packageName string) {
			_, generateError := uigen.Generate(newBuilder().build(), uigen.Options{Package: packageName})

			Expect(generateError).To(MatchError(uigen.ErrPackage))
		},
		Entry("empty", ""),
		Entry("a path", "examples/widget"),
		Entry("a keyword", "range"),
		Entry("a digit first", "2status"),
	)

	DescribeTable("refuses a document the interpreter would not have called sound",
		func(what string, edit func(document *ir.Document)) {
			_, generateError := uigen.Generate(newBuilder().with(edit), options)

			Expect(generateError).To(MatchError(uigen.ErrUnsound))
			Expect(generateError.Error()).To(ContainSubstring(what))
		},
		Entry("no widget name", "no widget name", func(document *ir.Document) { document.Name = "" }),
		Entry("no region", "declares no region", func(document *ir.Document) { document.Region = "" }),
		Entry("no state", "declares no state", func(document *ir.Document) { document.StateFields = nil }),
		Entry("no scene", "declares no scene", func(document *ir.Document) { document.Scene = nil }),
		Entry("no description", "scene description unfilled", func(document *ir.Document) {
			document.Scene.DescriptionSlot = nil
		}),
		Entry("an unlabelled description", "scene description unfilled", func(document *ir.Document) {
			document.Scene.DescriptionSlot.Label = nil
		}),
		Entry("no title slot", "fills no title slot", func(document *ir.Document) {
			document.Slots = document.Slots[1:]
		}),
	)

	It("refuses a document identifier with no exported Go spelling", func() {
		_, generateError := uigen.Generate(newBuilder().with(func(document *ir.Document) {
			document.StateFields[0].Name = "_hidden"
		}), options)

		Expect(generateError).To(MatchError(uigen.ErrUnexportable))
	})

	It("refuses two document identifiers that emit one Go identifier, naming both", func() {
		_, generateError := uigen.Generate(newBuilder().with(func(document *ir.Document) {
			document.Bindings[1].Name = "StatusText"
		}), options)

		Expect(generateError).To(MatchError(uigen.ErrNameCollision))
		Expect(generateError.Error()).To(ContainSubstring("StatusText and statusText"))
		Expect(generateError.Error()).To(ContainSubstring("the state type"))
	})

	It("refuses an event whose constant collides with the widget's own identity", func() {
		_, generateError := uigen.Generate(newBuilder().with(func(document *ir.Document) {
			document.Events[0].Name = "health"
			document.Events = append(document.Events, &ir.EventDeclaration{
				Name: "Health", Wire: "widget.node-status.health2",
			})
		}), options)

		Expect(generateError).To(MatchError(uigen.ErrNameCollision))
		Expect(generateError.Error()).To(ContainSubstring("the generated package"))
	})

	It("refuses a state field colliding with the instance tick derivation", func() {
		document := newBuilder().withFullScene()
		document.StateFields = append(document.StateFields, &ir.StateField{Name: "motionTickIDAt", Type: ir.FieldText})
		_, generateError := uigen.Generate(document, options)

		Expect(generateError).To(MatchError(uigen.ErrNameCollision))
		Expect(generateError.Error()).To(ContainSubstring("MotionTickIDAt"))
	})
})

var _ = Describe("Write", func() {
	It("writes every artifact under a directory it creates", func() {
		directory := filepath.Join(GinkgoT().TempDir(), "nested", "nodestatus")
		artifacts, generateError := uigen.Generate(newBuilder().build(), options)
		Expect(generateError).ToNot(HaveOccurred())

		Expect(uigen.Write(directory, artifacts)).To(Succeed())

		for _, artifact := range artifacts {
			written, readError := os.ReadFile(filepath.Join(directory, artifact.Path))
			Expect(readError).ToNot(HaveOccurred())
			Expect(written).To(Equal(artifact.Data))
		}
	})

	It("reports a directory it cannot create rather than losing the output", func() {
		blocked := filepath.Join(GinkgoT().TempDir(), "file")
		Expect(os.WriteFile(blocked, []byte("not a directory"), 0o644)).To(Succeed())

		Expect(uigen.Write(blocked, []Artifact{{Path: "view.templ", Data: []byte("x")}})).To(HaveOccurred())
	})
})

// Artifact is aliased so the spec above can build one without importing the
// package under test twice over.
type Artifact = uigen.Artifact

// firstLine returns everything before the first newline.
func firstLine(contents string) string {
	for index := range len(contents) {
		if contents[index] == '\n' {
			return contents[:index]
		}
	}
	return contents
}
