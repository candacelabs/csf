package widget_test

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/widget"
)

// The golden suite. docs/examples/ is the contract: three documents that must
// resolve to IR, and one that must produce exactly the findings its own
// comments annotate — twenty-eight faults across twenty-two classes, each named
// on the line above the statement that carries it.

const (
	clusterExample  = "docs/examples/01-cluster-heartbeats.widget"
	nodeExample     = "docs/examples/02-node-status.widget"
	pipelineExample = "docs/examples/03-relay-pipeline.widget"
	wrongExample    = "docs/examples/04-wrong-on-purpose.widget"
)

func interpretExample(path string) *widget.Document {
	document, findings, readError := widget.InterpretFile(path)
	ExpectWithOffset(1, readError).ToNot(HaveOccurred())
	ExpectWithOffset(1, renderClasses(findings)).To(BeEmpty())
	return document
}

func renderClasses(findings []widget.Finding) []string {
	rendered := make([]string, 0, len(findings))
	for _, finding := range findings {
		rendered = append(rendered, fmt.Sprintf("%d:%d: %s", finding.At.Line, finding.At.Column, finding.Class))
	}
	return rendered
}

var _ = Describe("The exemplars that validate", func() {
	It("resolves the flagship cluster document", func() {
		document := interpretExample(clusterExample)
		Expect(document.Name).To(Equal("ClusterHeartbeats"))
		Expect(document.DialectVersion).To(Equal(widget.DialectVersion))
		Expect(document.Region).To(Equal("widget.cluster-heartbeats"))
		Expect(document.Palette).To(Equal("fieldStation"))
		Expect(document.StateFields).To(HaveLen(10))
		Expect(document.Bindings).To(HaveLen(8))
		Expect(document.Labels).To(HaveLen(16))
		Expect(document.Roles).To(HaveLen(2))
		Expect(document.Channels).To(HaveLen(2))
		Expect(document.Scene.Nodes).To(HaveLen(3))
		Expect(document.Scene.Edges).To(HaveLen(2))
		Expect(document.Scene.Orbits).To(HaveLen(2))
		Expect(document.Motion.Pulses).To(HaveLen(4))
		Expect(document.Motion.Emphases).To(HaveLen(1))
		Expect(document.Indicators).To(HaveLen(1))
		Expect(document.Controls).To(HaveLen(1))
		Expect(document.Events).To(HaveLen(2))
		Expect(document.Streams).To(HaveLen(1))
	})

	It("resolves the smallest legal document, which omits five blocks", func() {
		document := interpretExample(nodeExample)
		Expect(document.Name).To(Equal("NodeStatus"))
		Expect(document.StateFields).To(HaveLen(1))
		Expect(document.Motion).To(BeNil())
		Expect(document.Channels).To(BeEmpty())
		Expect(document.Indicators).To(BeEmpty())
		Expect(document.Controls).To(BeEmpty())
		Expect(document.Scene.Nodes).To(HaveLen(1))
		Expect(document.Scene.Edges).To(BeEmpty())
		Expect(document.Streams[0].Ordering).To(BeNil())
	})

	It("resolves a document whose shape is a chain rather than a star", func() {
		document := interpretExample(pipelineExample)
		Expect(document.Name).To(Equal("RelayPipeline"))
		Expect(document.Roles).To(HaveLen(1))
		Expect(document.Scene.Nodes).To(HaveLen(3))
		for _, node := range document.Scene.Nodes {
			Expect(node.Role).To(Equal(document.Roles[0]))
		}
		Expect(document.Motion.Forbids).To(BeEmpty())
		Expect(document.Motion.Requires).To(HaveLen(1))
	})
})

var _ = Describe("The exemplar that does not validate", func() {
	// Every pair below is annotated in the document itself, on the line above
	// the statement it is about. Changing this list without changing those
	// comments is how the two drift apart.
	It("produces exactly its annotated findings, in order", func() {
		_, findings, readError := widget.InterpretFile(wrongExample)
		Expect(readError).ToNot(HaveOccurred())
		Expect(renderClasses(findings)).To(Equal([]string{
			"23:1: W002",   // the `dialect` directive is missing
			"27:1: W108",   // the region identity contains a space
			"32:9: W208",   // no palette is named `midnightNeon`
			"41:28: W419",  // a `signal` binds `beats`, which is a counter
			"42:3: W409",   // nothing writes `reachable`
			"45:3: W409",   // nothing writes `orphan`
			"52:3: W408",   // `up` and `down` require each other
			"65:3: W401",   // `statusText` has no `otherwise`
			"72:10: W201",  // `upAndReachable` is not declared
			"81:5: W415",   // clause 2 refines clause 1, so it is dead
			"86:3: W410",   // no label binds `neverUsedText`
			"102:3: W402",  // `statusLabel` has two sources
			"106:24: W605", // a literal address in a label
			"114:11: W505", // an inline colour where a token belongs
			"124:1: W005",  // `chrome` is block 5 and follows block 6
			"141:3: W410",  // `ghost` is carried by no edge
			"144:15: W105", // `direction sideways`
			"155:3: W410",  // no node is placed `there`
			"155:24: W104", // `left 120` is outside [0, 100]
			"171:10: W201", // `missingRole` is not declared
			"173:5: W306",  // `here` already holds nodeA
			"176:11: W507", // a literal where a label reference belongs
			"182:9: W501",  // a mermaid edge operator
			"188:5: W403",  // a self-edge
			"196:1: W405",  // a pulse with no `restartOn`
			"202:5: W404",  // `loop` does not carry `ghost`
			"205:14: W506", // `820ms` carries a unit sigil
			"215:13: W105", // `trigger hover`
			"218:5: W411",  // `togglePause` is not declared
		}))
	})

	It("covers twenty-four classes with twenty-nine faults", func() {
		_, findings, _ := widget.InterpretFile(wrongExample)
		classes := map[widget.Class]int{}
		for _, finding := range findings {
			classes[finding.Class]++
		}
		Expect(findings).To(HaveLen(29))
		Expect(classes).To(HaveLen(24))
	})

	It("still returns a document, because recovery is total", func() {
		document, findings, _ := widget.InterpretFile(wrongExample)
		Expect(findings).ToNot(BeEmpty())
		Expect(document).ToNot(BeNil())
		Expect(document.Name).To(Equal("BrokenCard"))
	})
})

var _ = Describe("The IR's five properties", func() {
	It("resolves every reference to a handle", func() {
		document := interpretExample(clusterExample)
		for _, label := range document.Labels {
			if label.SourceKind == widget.LabelBound {
				Expect(label.Binding).ToNot(BeNil())
			}
		}
		for _, slot := range document.Slots {
			Expect(slot.Label).ToNot(BeNil())
		}
		for _, node := range document.Scene.Nodes {
			Expect(node.Role).ToNot(BeNil())
			Expect(node.Placement).ToNot(BeNil())
			Expect(node.TitleLabel).ToNot(BeNil())
		}
		for _, edge := range document.Scene.Edges {
			Expect(edge.From).ToNot(BeNil())
			Expect(edge.To).ToNot(BeNil())
			Expect(edge.Channels).ToNot(BeEmpty())
		}
		for _, pulse := range document.Motion.Pulses {
			Expect(pulse.Edge).ToNot(BeNil())
			Expect(pulse.Channel).ToNot(BeNil())
		}
		Expect(document.Motion.RestartOn).To(Equal(document.StateFields[0]))
		Expect(document.Controls[0].Event).To(Equal(document.Events[0]))
		Expect(document.Streams[0].Delivers).To(Equal(document.Events[1]))
	})

	It("materialises a default rather than leaving a field to be worked out", func() {
		document := interpretExample(pipelineExample)
		Expect(document.Motion.HostStatusGate).To(BeTrue())
		for _, binding := range document.Bindings {
			Expect(binding.Otherwise).ToNot(BeNil())
		}
	})

	It("orders every collection in declaration order", func() {
		document := interpretExample(clusterExample)
		Expect(document.StateFields[0].Name).To(Equal("sequence"))
		Expect(document.StateFields[9].Name).To(Equal("degraded"))
		Expect(document.Channels[0].Name).To(Equal("heartbeat"))
		Expect(document.Channels[1].Name).To(Equal("ack"))
		Expect(document.Scene.Nodes[0].Name).To(Equal("nodeA"))
		Expect(document.Scene.Nodes[2].Name).To(Equal("nodeC"))
		stats := []string{}
		for _, slot := range document.Slots {
			if slot.Kind == widget.SlotStat {
				stats = append(stats, slot.Label.Name)
			}
		}
		Expect(stats).To(Equal([]string{"termStatLabel", "quorumStatLabel", "telemetryStatLabel"}))
	})

	It("finds a filled slot by kind, and reports an unfilled one as absent", func() {
		document := interpretExample(nodeExample)
		title, filled := document.Slot(widget.SlotTitle)
		Expect(filled).To(BeTrue())
		Expect(title.Label.Name).To(Equal("titleLabel"))
		_, filled = document.Slot(widget.SlotSource)
		Expect(filled).To(BeFalse())
	})

	It("anchors every record at the statement that produced it", func() {
		document := interpretExample(nodeExample)
		Expect(document.StateFields[0].Span.StartLine).To(Equal(27))
		Expect(document.StateFields[0].Span.File).To(Equal(nodeExample))
		Expect(document.Scene.Span.StartLine).To(Equal(89))
		Expect(document.Scene.Span.EndLine).To(Equal(98))
	})

	It("names no path, host, address or credential", func() {
		document := interpretExample(clusterExample)
		Expect(document.Streams[0].Source).To(Equal("widget.cluster.watch"))
		Expect(document.Events[0].Wire).To(Equal("widget.cluster.motion-toggle"))
	})
})

var _ = Describe("The three computed records", func() {
	It("derives an edge's length and angle from its endpoints", func() {
		document := interpretExample(clusterExample)
		// linkWest runs from the centre (50, 50) to the west point (21, 17).
		west := document.Scene.Edges[0]
		Expect(west.Name).To(Equal("linkWest"))
		Expect(west.Geometry.LengthPercent).To(BeNumerically("~", math.Hypot(29, 33), 0.000001))
		Expect(west.Geometry.AngleDegrees).To(BeNumerically("~", math.Atan2(-33, -29)*180/math.Pi, 0.000001))
	})

	It("projects the legend from the declared channels, in order", func() {
		document := interpretExample(clusterExample)
		Expect(document.Legend.Entries).To(HaveLen(2))
		Expect(document.Legend.Entries[0].Channel.Name).To(Equal("heartbeat"))
		Expect(document.Legend.Entries[0].Label.Name).To(Equal("heartbeatLegendLabel"))
		Expect(document.Legend.Entries[1].Channel.Name).To(Equal("ack"))
	})

	It("projects the dirty set from what the widget's text and predicates read", func() {
		document := interpretExample(pipelineExample)
		names := []string{}
		for _, field := range document.DirtyProjection.Fields {
			names = append(names, field.Name)
		}
		// `cursor` is read by a literal label's template, `backlog` by a bound
		// one's, and the four health fields by the two predicates.
		Expect(names).To(Equal([]string{"cursor", "ingressUp", "relayUp", "sinkUp", "backlog", "slow"}))
	})

	It("counts the tick motion re-arms on, which the markup reads and no text does", func() {
		document := interpretExample(clusterExample)
		names := []string{}
		for _, field := range document.DirtyProjection.Fields {
			names = append(names, field.Name)
		}
		// `sequence` appears in no binding, no template and no predicate: it is
		// in the projection because the generated view carries it as a per-tick
		// identity, so a transition that moves only the tick moves the markup.
		Expect(document.Motion.RestartOn.Name).To(Equal("sequence"))
		Expect(names).To(ContainElement("sequence"))
		Expect(names[0]).To(Equal("sequence"), "the projection is in state-declaration order")
	})

	It("leaves an edge with unresolved endpoints without geometry rather than guessing", func() {
		document, findings := widget.Interpret("fixture.widget", []byte("widget Broken\ndialect 0\nregion \"widget.broken\"\npalette fieldStation\n\nscene solo\n  edge link\n    from missingA\n    to missingB\n    carries missing\n  end\nend\n"))
		Expect(findings).ToNot(BeEmpty())
		Expect(document.Scene.Edges[0].Geometry).To(Equal(widget.EdgeGeometry{}))
	})
})

var _ = Describe("Writers", func() {
	It("records the three kinds of writer a state field can have", func() {
		document := interpretExample(clusterExample)
		byName := map[string]*widget.StateField{}
		for _, field := range document.StateFields {
			byName[field.Name] = field
		}
		Expect(byName["sequence"].Writers[0].Kind).To(Equal(widget.WriterEventField))
		Expect(byName["paused"].Writers[0].Kind).To(Equal(widget.WriterEventToggle))
		Expect(byName["degraded"].Writers[0].Kind).To(Equal(widget.WriterSignal))
		Expect(byName["degraded"].Writers[0].Signal).To(Equal(widget.SignalSlowClient))
	})
})

var _ = Describe("Reading a document from disk", func() {
	It("uses the path the caller gave, unmodified, in every anchor", func() {
		_, findings, readError := widget.InterpretFile(wrongExample)
		Expect(readError).ToNot(HaveOccurred())
		for _, finding := range findings {
			Expect(finding.At.File).To(Equal(wrongExample))
		}
	})

	It("returns an error, and no findings, when the document cannot be read", func() {
		document, findings, readError := widget.InterpretFile(filepath.Join(GinkgoT().TempDir(), "absent.widget"))
		Expect(readError).To(HaveOccurred())
		Expect(document).To(BeNil())
		Expect(findings).To(BeNil())
	})

	It("interprets a document written to a temporary file", func() {
		path := filepath.Join(GinkgoT().TempDir(), "empty.widget")
		Expect(os.WriteFile(path, []byte("%% a document of nothing but a comment\n"), 0o600)).To(Succeed())
		document, findings, readError := widget.InterpretFile(path)
		Expect(readError).ToNot(HaveOccurred())
		Expect(document.Name).To(BeEmpty())
		Expect(renderClasses(findings)).ToNot(BeEmpty())
	})
})
