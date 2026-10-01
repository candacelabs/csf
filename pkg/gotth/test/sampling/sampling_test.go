package sampling

import (
	"fmt"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
)

// serverPath is the set FR-36 clause 4 binds together: every server-side span
// of one interaction, from the frame arriving to the bytes leaving.
//
// gotthlive.client.morph is deliberately absent. Clause 4 names it as a second
// sampling decision and says why: its start timestamp is derived, so a parent
// edge would assert an enclosure the design does not observe. Its rate is
// measured below rather than asserted, because what independent sampling costs
// there is attribution and not measurement.
var serverPath = []string{
	obs.SpanParse,
	obs.SpanAuthorize,
	obs.SpanEvent,
	obs.SpanReduce,
	obs.SpanRender,
	obs.SpanRenderFragment,
	obs.SpanEncode,
	obs.SpanSend,
}

// graph is one recorded trace, grouped by the trace identifier the SDK
// assigned. An unsampled interaction contributes no graph at all: a
// non-recording span never reaches a span processor, which is exactly what
// makes "all or nothing" checkable by counting.
type graph struct {
	id    trace.TraceID
	root  sdktrace.ReadOnlySpan
	names map[string]int
}

func (g graph) has(name string) bool { return g.names[name] > 0 }

func (g graph) missing() []string {
	var out []string
	for _, want := range serverPath {
		if !g.has(want) {
			out = append(out, want)
		}
	}
	return out
}

func (g graph) sorted() []string {
	out := make([]string, 0, len(g.names))
	for n, c := range g.names {
		out = append(out, fmt.Sprintf("%s×%d", n, c))
	}
	sort.Strings(out)
	return out
}

// graphsOf groups the recorded spans into traces and finds each one's root.
func graphsOf(sr *tracetest.SpanRecorder) []graph {
	byTrace := map[trace.TraceID][]sdktrace.ReadOnlySpan{}
	for _, s := range sr.Ended() {
		id := s.SpanContext().TraceID()
		byTrace[id] = append(byTrace[id], s)
	}

	out := make([]graph, 0, len(byTrace))
	for id, spans := range byTrace {
		g := graph{id: id, names: map[string]int{}}
		ids := map[trace.SpanID]bool{}
		for _, s := range spans {
			ids[s.SpanContext().SpanID()] = true
		}
		for _, s := range spans {
			g.names[s.Name()]++
			if !ids[s.Parent().SpanID()] {
				g.root = s
			}
		}
		out = append(out, g)
	}
	return out
}

// attr reads one string attribute off a recorded span.
func attr(s sdktrace.ReadOnlySpan, key string) string {
	if s == nil {
		return ""
	}
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsString()
		}
	}
	return ""
}

// interactionGraphs keeps the traces an inbound EVENT frame rooted. An ack's
// parse span is a root too and its graph is complete at one span; counting it
// as a partial event graph would be a spec failing on the wrong subject.
func interactionGraphs(all []graph) []graph {
	var out []graph
	for _, g := range all {
		if g.root != nil && g.root.Name() == obs.SpanParse && attr(g.root, obs.AttrFrameKind) == "event" {
			out = append(out, g)
		}
	}
	return out
}

var _ = Describe("FR-36 clause 4 — the server-side event path is one sampling decision", func() {

	// The falsifier PM-1 attached to clause 4 at the checkpoint-2 gate: over N
	// interactions at any 0 < p < 1, the number of PARTIAL server-side graphs
	// must be 0.
	//
	// The three rates are not decoration. 0.05 is instrumentation §3.5's
	// STATED DEFAULT and L9-1's exact C-30 configuration, so the row that
	// replaces "0 of 300" is measured against the same sampler at the same
	// rate. The other two are the "any 0 < p < 1" half: a property that held
	// only at the rate someone happened to try is not the property.
	//
	// Two assertions, and the second is what stops the first being vacuous.
	// "Zero partial graphs" is trivially true of a run that recorded nothing,
	// and a spec that can pass by recording nothing is the defect class this
	// project keeps finding. So the sampler must be shown to have said yes to
	// some interactions and no to others in the same run.
	DescribeTable("records the whole path or none of it",
		func(rate float64, n int) {
			tp, sr := recorder(rate)
			d := dial(tp)

			for i := 0; i < n; i++ {
				d.interact()
			}
			d.stop()

			all := graphsOf(sr)
			interactions := interactionGraphs(all)

			var complete, partial int
			var evidence []string
			for _, g := range interactions {
				missing := g.missing()
				switch {
				case len(missing) == 0:
					complete++
				default:
					partial++
					if len(evidence) < 5 {
						evidence = append(evidence, fmt.Sprintf(
							"  trace %s: recorded %v, missing %v",
							g.id, g.sorted(), missing))
					}
				}
			}

			AddReportEntry(fmt.Sprintf("FR-36 clause 4 — measured at p=%g over %d interactions", rate, n),
				fmt.Sprintf(
					"interactions driven:        %d\n"+
						"server-side graphs recorded: %d (%.2f %%)\n"+
						"  complete:                  %d\n"+
						"  PARTIAL:                   %d\n"+
						"traces recorded in total:    %d (an ack's parse span is its own root)\n",
					n, len(interactions), 100*float64(len(interactions))/float64(n),
					complete, partial, len(all)))

			Expect(partial).To(BeZero(),
				"%d of %d recorded interactions are PARTIAL server-side graphs at p=%g, so an "+
					"unreachable span and an unsampled one are indistinguishable and FR-36 "+
					"clause 1 asserts a distinction the design cannot make:\n%s",
				partial, len(interactions), rate, strings.Join(evidence, "\n"))

			// The anti-vacuity half. Both outcomes must occur, or "zero
			// partial" is a statement about an empty set. At the worst of the
			// three rates — p=0.05, n=300 — an all-or-nothing run has
			// probability about 2e-7, which is orders of magnitude below this
			// suite's other failure modes.
			Expect(complete).To(BeNumerically(">", 0),
				"no interaction was sampled at all at p=%g over %d, so 'zero partial graphs' "+
					"is true of nothing and this spec asserted nothing", rate, n)
			Expect(complete).To(BeNumerically("<", n),
				"every interaction was sampled at p=%g, so the sampler never declined and this "+
					"spec never exercised the case it exists for", rate, n)
		},
		Entry("at instrumentation §3.5's documented default", 0.05, 300),
		Entry("at a quarter", 0.25, 300),
		Entry("at a half", 0.5, 300),
	)

	// The consequence clause 4 books honestly, published as a number rather
	// than left to be rediscovered a fourth time.
	//
	// This is a measurement with one assertion on it, and the assertion is the
	// weaker of the two things that could be said: the morph never appears in
	// an interaction's own trace. That is the observable form of "it is a
	// second decision". The joint rate is reported and not asserted, because
	// what independent sampling costs is attribution — the same duration is
	// also a ClientTelemetry frame and an unsampled histogram (FR-29, FR-34).
	It("leaves the client morph a second decision, and says what that costs", func() {
		const rate, n = 0.5, 200

		tp, sr := recorder(rate)
		d := dial(tp)
		d.reportTiming = true

		for i := 0; i < n; i++ {
			d.interact()
		}
		d.stop()

		all := graphsOf(sr)
		interactions := interactionGraphs(all)

		var morphTraces int
		for _, g := range all {
			if g.has(obs.SpanClientMorph) {
				morphTraces++
				Expect(g.names).To(HaveLen(1),
					"a morph span shares a trace with %v: it was parented rather than "+
						"linked, which asserts an enclosure over a derived start timestamp",
					g.sorted())
			}
		}
		for _, g := range interactions {
			Expect(g.has(obs.SpanClientMorph)).To(BeFalse(),
				"an interaction's own trace contains the morph span, so it is not the "+
					"independent decision FR-36 clause 4 says it is")
		}

		AddReportEntry(fmt.Sprintf("FR-36 clause 4 — the morph's second decision at p=%g over %d", rate, n),
			fmt.Sprintf(
				"interactions driven:          %d\n"+
					"server-side graphs recorded:  %d\n"+
					"morph spans recorded:         %d\n"+
					"expected joint rate p²=%.4f → about %.0f of %d interactions would have both\n\n"+
					"What that loses is the per-event link, not the latency:\n"+
					"gotthlive_client_morph_duration_seconds is unsampled (FR-29, FR-34).\n",
				n, len(interactions), morphTraces, rate*rate, rate*rate*float64(n), n))

		Expect(morphTraces).To(BeNumerically(">", 0),
			"no morph span was recorded at all, so this measurement measured nothing")
	})
})
