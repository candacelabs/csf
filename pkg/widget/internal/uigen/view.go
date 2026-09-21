package uigen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/candacelabs/csf/pkg/widget/internal/ir"
)

// The scene's coordinate system. A placement is a percentage of the normalized
// scene box, so a viewBox of a hundred units square makes one percent one user
// unit and the emitted coordinate the authored number, unscaled.
const sceneExtent = 100

// The two marker sizes as radii, and the offsets that keep a node's title above
// its marker and its caption below. They are here rather than in CSS because an
// SVG geometry attribute is not a style: a stylesheet that failed to load would
// otherwise collapse every node to a point.
const (
	largeRadius        = 9
	smallRadius        = 5
	largeTitleOffset   = -14
	largeCaptionOffset = 20
	smallTitleOffset   = -10
	smallCaptionOffset = 15
)

// A travelling pulse's radius, and how far outside its node's marker an
// emphasis ring starts. Same argument as the marker radii: geometry, not style.
const (
	pulseRadius    = 2
	emphasisMargin = 2
)

// An orbit's geometry, derived from its ordinal.
//
// An orbit is decorative and the document declares no size for one — there is
// no spelling for it, deliberately, because a scene with authored ellipse
// dimensions is a scene whose author is drawing rather than declaring. So the
// generator derives one: concentric ellipses, each pair a step smaller than the
// last, alternating which axis is the long one and turned a further step, with a
// floor so that a document declaring many orbits never reaches a radius of zero.
const (
	firstOrbitLongRadius  = 41
	firstOrbitShortRadius = 29
	orbitRadiusStep       = 7
	smallestOrbitRadius   = 8
	firstOrbitRotation    = -9
	orbitRotationStep     = 56
)

// emitView writes the templ half of a widget: its chrome, its scene, its
// projected legend, its stats, its indicators, its controls, and nothing that is
// not a pure read of state.
func emitView(document *ir.Document, identifiers *names, options Options) []byte {
	view := &strings.Builder{}

	fmt.Fprintf(view, "%s\n\n", generatedBy(document))
	fmt.Fprintf(view, "package %s\n\n", options.Package)
	view.WriteString("import \"github.com/candacelabs/csf/pkg/gotth/live\"\n\n")
	fmt.Fprintf(view, "// %s draws the %s widget's default live region.\n", identifiers.viewFunc, document.Name)
	fmt.Fprintf(view, "templ %s(state %s) {\n\t@%s(state, %s)\n}\n\n",
		identifiers.viewFunc, identifiers.stateType, identifiers.viewAtFunc, identifiers.regionConst)
	fmt.Fprintf(view, "// %s draws one instance in its host-assigned live region.\n", identifiers.viewAtFunc)
	view.WriteString("//\n")
	view.WriteString("// Every value in it is a pure read of state and region: equal inputs render\n")
	view.WriteString("// byte-identical markup: that comparison is what suppresses a patch nobody needs.\n")
	fmt.Fprintf(view, "templ %s(state %s, region string) {\n", identifiers.viewAtFunc, identifiers.stateType)
	fmt.Fprintf(view, "\t{{ titleID := region + %s }}\n", strconv.Quote(titleIDSuffix))

	// A landmark, not a bare container. The element is <aside> and it carries an
	// accessible name from its own title, which is what makes it a complementary
	// landmark a screen-reader user can jump to and skip. Both halves are
	// required: HTML-AAM maps a nameless <aside> inside sectioning content to
	// `generic`, so the name is not decoration on the role — it is the role.
	// The redundant role="complementary" is deliberately not emitted; the
	// element already carries it, and ARIA that restates HTML is one more thing
	// that can disagree with it.
	fmt.Fprintf(view, "\t<aside { live.Region(region)... } class=\"widget\" aria-labelledby={ titleID }"+
		" data-widget=%s data-palette=%s",
		strconv.Quote(document.Name), strconv.Quote(document.Palette))
	if document.Motion != nil {
		// The widget's own half of the motion gate. The host's connection status
		// is the other half and is not this widget's to write: the runtime puts
		// it on the document element, and the stylesheet reads both.
		fmt.Fprintf(view, " data-motion={ %s }", stateCall(identifiers.motionActiveText))
	}
	view.WriteString(">\n")

	writeChrome(view, document, identifiers)
	writeScene(view, document, identifiers)
	writeLegend(view, document, identifiers)
	writeStats(view, document, identifiers)
	writeIndicators(view, document, identifiers)
	writeControls(view, document, identifiers)
	view.WriteString("\t</aside>\n}\n")

	return []byte(view.String())
}

// writeChrome emits the widget's header: its source line if it declared one,
// then its title.
//
// The source comes first because that is the order it is read in — it is an
// eyebrow above the heading, and every design that shows one shows it there.
// The generator emitted the heading first and let a host stylesheet reverse the
// visual order, which is a DOM order and a reading order that disagree: a
// screen reader, a reader-mode extension and a stylesheet that failed to load
// all follow the DOM, and all three then heard the eyebrow after the heading it
// introduces. Reading order is settled in the markup, so no host has to patch
// it in CSS and no host can forget to.
//
// The title carries the id the landmark above is labelled by, which is why it
// is derived once from the region rather than repeated: two spellings of one
// identifier is an aria-labelledby that points at nothing.
func writeChrome(view *strings.Builder, document *ir.Document, identifiers *names) {
	view.WriteString("\t\t<header class=\"widget-chrome\">\n")
	if origin, filled := document.Slot(ir.SlotSource); filled {
		fmt.Fprintf(view, "\t\t\t<p class=\"widget-source widget-token-muted\">{ %s }</p>\n",
			labelCall(origin.Label, identifiers))
	}
	if title, filled := document.Slot(ir.SlotTitle); filled {
		fmt.Fprintf(view, "\t\t\t<h2 class=\"widget-title widget-token-ink\" id={ titleID }>{ %s }</h2>\n",
			labelCall(title.Label, identifiers))
	}
	view.WriteString("\t\t</header>\n")
}

// writeScene emits the widget's one drawing area and one accessibility
// boundary: an image with a text alternative, and nothing inside it
// contributing an accessible name of its own.
//
// A widget whose motion names a tick gives the scene a per-tick identity, so a
// finished animation is a new element and starts again. That is the tick's whole
// job — the picture moves exactly as often as the data does — and it is why the
// computed dirty projection counts the tick as read.
func writeScene(view *strings.Builder, document *ir.Document, identifiers *names) {
	view.WriteString("\t\t<svg class=\"widget-scene\"")
	if tick(document) != nil {
		fmt.Fprintf(view, " id={ state.%s(region) }", identifiers.motionTickAtFunc)
	}
	fmt.Fprintf(view, " viewBox=\"0 0 %d %d\" preserveAspectRatio=\"xMidYMid meet\""+
		" role=\"img\" aria-label={ %s }>\n",
		sceneExtent, sceneExtent, labelCall(document.Scene.DescriptionSlot.Label, identifiers))

	for index, orbit := range document.Scene.Orbits {
		writeOrbit(view, orbit, index)
	}
	for _, edge := range document.Scene.Edges {
		writeEdge(view, document, edge, identifiers)
	}
	for _, node := range document.Scene.Nodes {
		writeNode(view, document, node, identifiers)
	}
	view.WriteString("\t\t</svg>\n")
}

// writeOrbit emits one decorative ambient ellipse. It is hidden from the
// accessibility tree by the scene's own boundary — the scene is one image with
// one text alternative — so an orbit needs no attribute of its own to be silent.
func writeOrbit(view *strings.Builder, orbit *ir.Orbit, index int) {
	radiusX, radiusY, rotation := orbitGeometry(index)
	centre := sceneExtent / 2
	fmt.Fprintf(view, "\t\t\t<ellipse class=\"widget-orbit widget-token-%s\""+
		" cx=\"%d\" cy=\"%d\" rx=\"%d\" ry=\"%d\" transform=\"rotate(%d %d %d)\"></ellipse>\n",
		orbit.Token, centre, centre, radiusX, radiusY, rotation, centre, centre)
}

// orbitGeometry derives one orbit's ellipse from its ordinal. See the constants
// above for why an orbit's size is derived rather than authored.
func orbitGeometry(index int) (int, int, int) {
	long := firstOrbitLongRadius - (index/2)*orbitRadiusStep
	short := firstOrbitShortRadius - (index/2)*orbitRadiusStep
	if long < smallestOrbitRadius {
		long = smallestOrbitRadius
	}
	if short < smallestOrbitRadius {
		short = smallestOrbitRadius
	}
	if index%2 == 1 {
		long, short = short, long
	}
	return long, short, firstOrbitRotation + index*orbitRotationStep
}

// writeEdge emits one edge and the pulses that travel it.
//
// The group is placed at the `from` endpoint and turned by the edge's angle, so
// the line runs along its own local x axis and a pulse travels it by moving
// along that axis alone. Both numbers are the interpreter's computed geometry:
// the shipped card hand-wrote them, which is a copy of the endpoints that
// silently goes stale when a node moves.
func writeEdge(view *strings.Builder, document *ir.Document, edge *ir.Edge, identifiers *names) {
	length := formatNumber(edge.Geometry.LengthPercent)
	fmt.Fprintf(view, "\t\t\t<g class=\"widget-edge\" transform=\"translate(%d,%d) rotate(%s)\""+
		" style=\"--widget-edge-length: %spx\">\n",
		edge.From.Placement.Left, edge.From.Placement.Top,
		formatNumber(edge.Geometry.AngleDegrees), length)
	fmt.Fprintf(view, "\t\t\t\t<line class=\"widget-edge-line widget-token-rule\""+
		" x1=\"0\" y1=\"0\" x2=\"%s\" y2=\"0\"></line>\n", length)
	for _, pulse := range pulsesOn(document, edge) {
		writePulse(view, pulse)
	}
	view.WriteString("\t\t\t</g>\n")
}

// writePulse emits one finite animation of one channel on one edge.
//
// Travel direction is the channel's, never the pulse's: an author cannot make an
// acknowledgement travel forwards. The duration and the delay are the document's
// numbers and travel as custom properties, so the stylesheet holds the gate and
// the keyframes while the document holds the timing.
func writePulse(view *strings.Builder, pulse *ir.Pulse) {
	fmt.Fprintf(view, "\t\t\t\t<circle class=\"widget-pulse widget-pulse-%s widget-token-%s\" r=\"%d\""+
		" style=\"--widget-pulse-duration: %dms; --widget-pulse-delay: %dms\"></circle>\n",
		pulse.Channel.Direction, pulse.Channel.Token, pulseRadius,
		pulse.DurationMilliseconds, pulse.DelayMilliseconds)
}

// writeNode emits one participant: its emphasis ring if it has one, a marker at
// its placement, its title above and its caption below.
func writeNode(view *strings.Builder, document *ir.Document, node *ir.Node, identifiers *names) {
	radius, titleOffset, captionOffset := largeRadius, largeTitleOffset, largeCaptionOffset
	if node.Role.Marker == ir.MarkerSmall {
		radius, titleOffset, captionOffset = smallRadius, smallTitleOffset, smallCaptionOffset
	}

	fmt.Fprintf(view, "\t\t\t<g class=\"widget-node widget-role-%s\" transform=\"translate(%d,%d)\">\n",
		node.Role.Name, node.Placement.Left, node.Placement.Top)
	if emphasis := emphasisOn(document, node); emphasis != nil {
		// Behind the marker, so the ring expands out from under it rather than
		// over it. At most one per node, which the interpreter enforces.
		fmt.Fprintf(view, "\t\t\t\t<circle class=\"widget-emphasis widget-token-%s\" r=\"%d\""+
			" style=\"--widget-emphasis-duration: %dms; --widget-emphasis-delay: %dms\"></circle>\n",
			node.Role.Token, radius+emphasisMargin,
			emphasis.DurationMilliseconds, emphasis.DelayMilliseconds)
	}
	fmt.Fprintf(view, "\t\t\t\t<circle class=\"widget-marker widget-marker-%s widget-token-%s\" r=\"%d\"></circle>\n",
		node.Role.Marker, node.Role.Token, radius)
	if node.TitleLabel != nil {
		fmt.Fprintf(view, "\t\t\t\t<text class=\"widget-node-title widget-token-ink\" y=\"%d\""+
			" text-anchor=\"middle\">{ %s }</text>\n",
			titleOffset, labelCall(node.TitleLabel, identifiers))
	}
	if node.CaptionLabel != nil {
		fmt.Fprintf(view, "\t\t\t\t<text class=\"widget-node-caption widget-token-muted\" y=\"%d\""+
			" text-anchor=\"middle\">{ %s }</text>\n",
			captionOffset, labelCall(node.CaptionLabel, identifiers))
	}
	view.WriteString("\t\t\t</g>\n")
}

// writeLegend emits the footer legend, which is the interpreter's projection of
// the declared channels in declaration order rather than anything an author
// wrote — so it cannot disagree with the picture.
func writeLegend(view *strings.Builder, document *ir.Document, identifiers *names) {
	if len(document.Legend.Entries) == 0 {
		return
	}
	view.WriteString("\t\t<ul class=\"widget-legend\">\n")
	for _, entry := range document.Legend.Entries {
		fmt.Fprintf(view, "\t\t\t<li class=\"widget-legend-entry widget-token-muted\">"+
			"<span class=\"widget-swatch widget-token-%s\" aria-hidden=\"true\"></span>{ %s }</li>\n",
			entry.Channel.Token, labelCall(entry.Label, identifiers))
	}
	view.WriteString("\t\t</ul>\n")
}

// writeStats emits the stat slots, in declaration order, or nothing at all when
// the widget declared none.
func writeStats(view *strings.Builder, document *ir.Document, identifiers *names) {
	stats := make([]*ir.Slot, 0, len(document.Slots))
	for _, slot := range document.Slots {
		if slot.Kind == ir.SlotStat {
			stats = append(stats, slot)
		}
	}
	if len(stats) == 0 {
		return
	}
	view.WriteString("\t\t<ul class=\"widget-stats\">\n")
	for _, stat := range stats {
		fmt.Fprintf(view, "\t\t\t<li class=\"widget-stat widget-token-ink\">{ %s }</li>\n",
			labelCall(stat.Label, identifiers))
	}
	view.WriteString("\t\t</ul>\n")
}

// writeIndicators emits each status mark: a tone the predicate selects, and a
// label beside it.
//
// The label is emitted whatever the tone, because an indicator is never the only
// carrier of a status: colour alone is not a signal a colour-blind or
// screen-reader user receives, and the language has no label-less indicator.
func writeIndicators(view *strings.Builder, document *ir.Document, identifiers *names) {
	if len(document.Indicators) == 0 {
		return
	}
	view.WriteString("\t\t<ul class=\"widget-indicators\">\n")
	for _, indicator := range document.Indicators {
		fmt.Fprintf(view, "\t\t\t<li class=\"widget-indicator widget-token-muted\" data-tone={ %s }>"+
			"<span class=\"widget-indicator-dot\" aria-hidden=\"true\"></span>{ %s }</li>\n",
			stateCall(identifiers.indicatorTones[indicator]), labelCall(indicator.Label, identifiers))
	}
	view.WriteString("\t\t</ul>\n")
}

// writeControls emits each control: one DOM interaction bound to one declared
// event, captioned by its label.
//
// A control with a pressed-state predicate renders aria-pressed and one without
// does not, because there is no "sometimes pressed" control.
func writeControls(view *strings.Builder, document *ir.Document, identifiers *names) {
	if len(document.Controls) == 0 {
		return
	}
	view.WriteString("\t\t<div class=\"widget-controls\">\n")
	for _, control := range document.Controls {
		view.WriteString("\t\t\t<button class=\"widget-control\" type=\"button\"")
		if control.PressedWhen != nil {
			fmt.Fprintf(view, " aria-pressed={ %s }", stateCall(identifiers.controlPressed[control]))
		}
		fmt.Fprintf(view, " { live.On(%s, %s)... }>{ %s }</button>\n",
			strconv.Quote(string(control.Trigger)), identifiers.events[control.Event],
			labelCall(control.CaptionLabel, identifiers))
	}
	view.WriteString("\t\t</div>\n")
}

// pulsesOn returns the pulses travelling one edge, in motion declaration order.
func pulsesOn(document *ir.Document, edge *ir.Edge) []*ir.Pulse {
	if document.Motion == nil {
		return nil
	}
	travelling := make([]*ir.Pulse, 0, len(document.Motion.Pulses))
	for _, pulse := range document.Motion.Pulses {
		if pulse.Edge == edge {
			travelling = append(travelling, pulse)
		}
	}
	return travelling
}

// emphasisOn returns one node's emphasis, or nil. There is at most one per node,
// which the interpreter enforces.
func emphasisOn(document *ir.Document, node *ir.Node) *ir.Emphasis {
	if document.Motion == nil {
		return nil
	}
	for _, emphasis := range document.Motion.Emphases {
		if emphasis.Node == node {
			return emphasis
		}
	}
	return nil
}

// tick returns the counter a widget's motion re-arms on, or nil when it has no
// motion or declares none.
func tick(document *ir.Document) *ir.StateField {
	if document.Motion == nil {
		return nil
	}
	return document.Motion.RestartOn
}

// formatNumber renders one computed geometry number with a fixed two decimal
// places and no trailing zeros, so that the same document emits the same bytes
// on every machine.
func formatNumber(value float64) string {
	rendered := strconv.FormatFloat(value, 'f', 2, 64)
	rendered = strings.TrimRight(rendered, "0")
	return strings.TrimSuffix(rendered, ".")
}

// labelCall is the Go expression that reads one label's text.
func labelCall(label *ir.Label, identifiers *names) string {
	return stateCall(identifiers.labels[label])
}

// stateCall is the Go expression that calls one derivation on the widget's own
// state.
func stateCall(method string) string {
	return "state." + method + "()"
}
