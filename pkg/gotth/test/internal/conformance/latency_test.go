package conformance_test

import (
	"fmt"
	"runtime"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// Protocol-level event→patch latency.
//
// What this measures, stated precisely so it cannot be quoted as something
// else: the interval from the moment the client hands an encoded Event frame
// to the socket, to the moment the corresponding Patch frame has been read and
// decoded by the client. It spans refine → authorize → reduce → render →
// encode → send → decode, over a real WebSocket, through the real handler.
//
// What it does NOT measure, and what therefore may not be compared with G1's
// ≤50ms p50 / ≤150ms p99 gate:
//
//   - the browser's morph and paint, which is the other half of "event→paint";
//   - a real network, since both ends are loopback in one process;
//   - a LAN round trip, which G1 specifies.
//
// G1 is a Phase 5 gate measured by QA-2 against equivalence-spec §3.6. This is
// a Phase 1 datum, published so checkpoint 1 has a measured number and a
// method rather than an estimate, and it is a floor: the real figure is this
// plus a network round trip plus paint.
//
// Run it with:
//
//	GOTTHLIVE_SOAK=1 go test ./test/... -v -args -ginkgo.label-filter=latency
// ---------------------------------------------------------------------------

// interactions is the sample count. The PRD asks for percentiles rather than
// means, and a p99 over fewer than a few hundred samples is one sample.
const interactions = 300

var _ = Describe("Event to patch, measured", Label("latency"), func() {
	It("reports p50, p95 and p99 over a real connection", func() {
		soakOnly()

		// The inbound rate limit is lifted for the run, and this is a
		// disclosure rather than a convenience. At the default 50 events/s a
		// loop that measures round-trip latency outruns the bucket after the
		// burst and then measures the limiter instead of the stack. The limit
		// is a policy bound that sits *before* the interval being timed — the
		// token is taken at ingress, and a refused event produces an Error
		// rather than a slow Patch — so removing it does not shorten anything
		// this spec reports. Every other limit is left at its default.
		d := dial(func(c *live.Config[tally, qaUser]) {
			c.Limits.MaxEventsPerSecond = 1_000_000
			c.Limits.EventBurst = 1_000_000
		})

		// Warm up: the first interactions pay for lazily built render state and
		// for the Go runtime's own warm-up, and a percentile that includes them
		// describes the warm-up rather than the steady state.
		for i := 0; i < 30; i++ {
			d.event("qa.increment", d.highestSeq())
			d.nextPatch()
			d.ack(d.highestSeq())
		}

		samples := make([]time.Duration, 0, interactions)
		for i := 0; i < interactions; i++ {
			seq := d.highestSeq()

			start := time.Now()
			d.event("qa.increment", seq)
			d.nextPatch()
			samples = append(samples, time.Since(start))

			d.ack(d.highestSeq())
		}

		Expect(samples).To(HaveLen(interactions))
		report(samples)

		// Not a gate. A sanity bound only: if a loopback round trip through
		// this stack takes more than a tenth of a second at p50, something is
		// wrong that a percentile table would merely document.
		Expect(percentile(samples, 50)).To(BeNumerically("<", 100*time.Millisecond),
			"p50 of %s on loopback indicates a defect, not a slow network", percentile(samples, 50))
	})
})

func report(samples []time.Duration) {
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total time.Duration
	for _, s := range sorted {
		total += s
	}

	AddReportEntry("event→patch latency (protocol level, loopback)", fmt.Sprintf(`
  samples      %d
  goos/goarch  %s/%s
  cpus         %d
  go           %s

  min          %s
  p50          %s
  p95          %s
  p99          %s
  max          %s
  mean         %s

  Method: time from handing an encoded Event frame to the socket until the
  corresponding Patch frame is read and decoded, client side, over a real
  WebSocket to a real handler on loopback. Excludes browser morph and paint,
  and excludes any network. NOT comparable with PRD G1, which is event→paint
  on a LAN and is measured in Phase 5.`,
		len(sorted), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version(),
		sorted[0],
		percentile(sorted, 50), percentile(sorted, 95), percentile(sorted, 99),
		sorted[len(sorted)-1],
		total/time.Duration(len(sorted)),
	))
}

// percentile returns the nearest-rank percentile, which is the definition that
// does not invent a sample that was never measured.
func percentile(samples []time.Duration, p int) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	rank := (p*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}
