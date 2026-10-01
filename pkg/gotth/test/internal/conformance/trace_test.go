package conformance_test

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/trace"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
	"github.com/candacelabs/csf/pkg/gotth/internal/obstest"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// FR-36 — the event path, asserted on the edges that actually carry it.
//
// live/instrumentation_test.go asserts the span set, the attributes, and the
// link from the client's morph back to the encode span. Those are real, and one
// of them found a real bug.
//
// What it does not assert is the thing its own "one trace, not four" line reads
// as. obstest stamps every span it records with the same TraceID
// unconditionally, so a set-of-trace-ids assertion has cardinality one whatever
// the library does. QA-1 confirmed it by mutation: reparenting the encode span
// onto context.Background() leaves that suite green. Recorded as QA-1 defect
// D-11 — a vacuous assertion is worse than a missing one, because it is
// counted as evidence.
//
// The edges that do carry the claim are the parent pointer and the link, and
// obstest records both faithfully. This asserts over them.
// ---------------------------------------------------------------------------

// pathSpans names the spans the FR-36 path is made of — all eight of the
// server-side phases plus the client's morph. Five of them were drawn by the
// design and started by nothing until FR-36 clause 4's change; a list of four
// could not distinguish the tracer that shipped from the tracer that was
// specified.
var pathSpans = []string{
	obs.SpanParse,
	obs.SpanAuthorize,
	obs.SpanEvent,
	obs.SpanReduce,
	obs.SpanRender,
	obs.SpanRenderFragment,
	obs.SpanEncode,
	obs.SpanSend,
	obs.SpanClientMorph,
}

// serverPathSpans is pathSpans without the morph: the spans FR-36 clause 4
// requires to share one sampling decision. The morph is excluded by the
// requirement itself, which names it as a deliberate second decision.
var serverPathSpans = []string{
	obs.SpanParse,
	obs.SpanAuthorize,
	obs.SpanEvent,
	obs.SpanReduce,
	obs.SpanRender,
	obs.SpanRenderFragment,
	obs.SpanEncode,
	obs.SpanSend,
}

// tracedInteraction drives one full interaction, including the client's
// telemetry report, and returns everything recorded.
func tracedInteraction() *obstest.Traces {
	GinkgoHelper()

	traces := obstest.NewTraces()
	d := dial(func(c *live.Config[tally, qaUser]) { c.Tracer = traces })

	d.event("qa.increment", d.highestSeq())
	patch := d.nextPatch()

	// The half of the path that arrives after the server span for it closed.
	Expect(d.writeFrame(d.envelope(&pb.ClientTelemetry{
		PatchId: patch.GetPatchId(), MorphMicros: 1200, ApplyMicros: 1500,
	}))).To(Succeed())

	Eventually(traces.Names, 5*time.Second).Should(ContainElements(toAny(pathSpans)...),
		func() string { return "recorded spans:\n" + traces.Describe() })

	return traces
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

var _ = Describe("FR-36 — the event path is walkable end to end", func() {

	// The property that is actually true of this design, and the one an
	// operator depends on: from any span on the path you can reach every other
	// one by following parents and links. Two of the edges cannot be parent
	// edges — authorization runs on the read pump before a transition span
	// exists, and the morph happens in a browser — so the walk has to admit
	// links, and FR-36's own wording admits one of them ("with the client morph
	// attached via the causal ID carried in the frame").
	It("connects every span on the path by a parent or a link", func() {
		traces := tracedInteraction()
		spans := traces.Spans()

		byID := map[trace.SpanID]obstest.Span{}
		for _, s := range spans {
			byID[s.SpanID] = s
		}

		// Undirected adjacency over both edge kinds.
		adj := map[trace.SpanID][]trace.SpanID{}
		join := func(a, b trace.SpanID) {
			adj[a] = append(adj[a], b)
			adj[b] = append(adj[b], a)
		}
		for _, s := range spans {
			if _, ok := byID[s.ParentID]; ok {
				join(s.SpanID, s.ParentID)
			}
			for _, l := range s.Links {
				if _, ok := byID[l.SpanID()]; ok {
					join(s.SpanID, l.SpanID())
				}
			}
		}

		// Walk from the transition span, which is the one an operator starts
		// from when asking "what did this event do".
		event := traces.Named(obs.SpanEvent)
		Expect(event).NotTo(BeEmpty())

		seen := map[trace.SpanID]bool{}
		queue := []trace.SpanID{event[0].SpanID}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if seen[cur] {
				continue
			}
			seen[cur] = true
			queue = append(queue, adj[cur]...)
		}

		var unreachable []string
		for _, want := range pathSpans {
			found := false
			for _, s := range traces.Named(want) {
				if seen[s.SpanID] {
					found = true
				}
			}
			if !found {
				unreachable = append(unreachable, want)
			}
		}

		Expect(unreachable).To(BeEmpty(),
			"these spans cannot be reached from the transition span by any parent or link, "+
				"so the causal chain is broken at them: %v\n%s", unreachable, traces.Describe())
	})

	// The parent half, on the one boundary where a parent is possible. This is
	// the assertion that fails when the encode span is reparented, which is the
	// mutation the existing suite survives.
	It("puts the encode span under the transition that produced the patch", func() {
		traces := obstest.NewTraces()
		d := dial(func(c *live.Config[tally, qaUser]) { c.Tracer = traces })

		d.event("qa.increment", d.highestSeq())
		patch := d.nextPatch()

		Eventually(traces.Names, 5*time.Second).Should(ContainElement(obs.SpanEncode))

		spans := traces.Spans()
		byID := map[trace.SpanID]obstest.Span{}
		for _, s := range spans {
			byID[s.SpanID] = s
		}

		var encode obstest.Span
		for _, s := range spans {
			if s.Name == obs.SpanEncode && s.Attrs[obs.AttrPatchID] == fmt.Sprint(patch.GetPatchId()) {
				encode = s
			}
		}
		Expect(encode.Name).To(Equal(obs.SpanEncode),
			"no encode span carries patch %d:\n%s", patch.GetPatchId(), traces.Describe())

		var chain []string
		cur := encode
		for i := 0; i < 16; i++ {
			chain = append(chain, cur.Name)
			parent, ok := byID[cur.ParentID]
			if !ok {
				break
			}
			cur = parent
		}

		Expect(chain).To(ContainElement(obs.SpanEvent),
			"the encode span's ancestry is %s, which never reaches the transition span:\n%s",
			strings.Join(chain, " → "), traces.Describe())
	})

	// FR-36 clause 4, as far as obstest can carry it.
	//
	// The recorder stamps one hard-coded TraceID on every span (D-11), so it
	// cannot express a sampling decision and the falsifier that can lives in
	// test/sampling against a real SDK sampler. What obstest records
	// faithfully is the parent pointer, and "one sampling decision" is a
	// property of the parent forest: every server-side span on the path must
	// reach ONE root by parent edges alone. This is the structural half, and
	// it fails on the same mutation the sampling spec fails on.
	It("roots one interaction's whole server-side path at one span, by parent edges alone", func() {
		traces := tracedInteraction()

		byID := map[trace.SpanID]obstest.Span{}
		for _, s := range traces.Spans() {
			byID[s.SpanID] = s
		}

		// The root a span reaches by walking parents only. Links are
		// deliberately not followed: ParentBased does not follow them either,
		// which is the whole of C-30.
		rootOf := func(s obstest.Span) obstest.Span {
			for i := 0; i < 32; i++ {
				parent, ok := byID[s.ParentID]
				if !ok {
					return s
				}
				s = parent
			}
			Fail("a parent chain longer than 32 spans: the graph has a cycle\n" + traces.Describe())
			return s
		}

		// One interaction, named by the transition the client's event caused.
		// The mount's own transition and the telemetry frame are separate
		// graphs and are not this interaction's; scoping to the transition is
		// what makes "the whole path or none of it" a statement about an
		// interaction rather than about a process.
		event := traces.Named(obs.SpanEvent)
		Expect(event).To(HaveLen(1), "expected exactly one transition:\n%s", traces.Describe())
		root := rootOf(event[0])

		Expect(root.Name).To(Equal(obs.SpanParse),
			"the interaction's parent chain reaches %q; FR-36's path begins at "+
				"receive/parse, so that is where its single sampling decision belongs:\n%s",
			root.Name, traces.Describe())
		Expect(root.Attr(obs.AttrFrameKind)).To(Equal("event"),
			"the interaction is rooted at the parse of a %q frame:\n%s",
			root.Attr(obs.AttrFrameKind), traces.Describe())

		// Everything the root encloses, and the names it must cover. A span on
		// the path that roots anywhere else is a second sampling decision, and
		// under ParentBased that is a partial graph waiting to happen.
		var covered []string
		for _, s := range traces.Spans() {
			if rootOf(s).SpanID == root.SpanID {
				covered = append(covered, s.Name)
			}
		}

		AddReportEntry("FR-36 clause 4 — one interaction's span tree, measured", fmt.Sprintf(
			"root: %s (frame kind %q)\nspans under it: %d %v\n\n"+
				"One root is one ParentBased decision. Before clause 4 this interaction had\n"+
				"three roots — parse, authorize and event — and at instrumentation §3.5's\n"+
				"documented default 0 of 300 interactions recorded two of them together\n"+
				"(L9-1, C-30). The rate spec that can fail on this is test/sampling.\n\n%s",
			root.Name, root.Attr(obs.AttrFrameKind), len(covered), covered, traces.Describe()))

		Expect(covered).To(ContainElements(toAny(serverPathSpans)...),
			"these server-side spans do not descend from the interaction's root, so "+
				"they sample independently of it:\n%s", traces.Describe())
	})

	// The morph is the second decision, and FR-36 says so rather than leaving
	// it to be discovered. A spec holds it because "deliberate" and "forgotten"
	// look identical in a trace.
	It("leaves the client morph a root, linked and not parented", func() {
		traces := tracedInteraction()

		byID := map[trace.SpanID]obstest.Span{}
		for _, s := range traces.Spans() {
			byID[s.SpanID] = s
		}

		morph := traces.Named(obs.SpanClientMorph)
		Expect(morph).To(HaveLen(1))
		_, hasParent := byID[morph[0].ParentID]
		Expect(hasParent).To(BeFalse(),
			"the morph span has a parent, which asserts an enclosure over a start "+
				"timestamp instrumentation §3.3 states is derived:\n%s", traces.Describe())
		Expect(morph[0].Links).To(HaveLen(1),
			"the morph span is neither parented nor linked, so the patch it timed is "+
				"unreachable from it:\n%s", traces.Describe())
	})
})
