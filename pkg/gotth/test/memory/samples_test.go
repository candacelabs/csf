package memory_test

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/test/memory"
)

func TestMemoryHarness(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "G2 Memory Harness Suite")
}

// window builds a well-formed §3.6 window whose workload bytes are the values
// given, spaced at exactly 1 Hz.
func window(workload ...int64) []memory.Sample {
	out := make([]memory.Sample, len(workload))
	for i, w := range workload {
		out[i] = memory.Sample{
			UnixMilli: int64(1_700_000_000_000 + i*memory.SpecPeriodMS),
			// The split between current and file is arbitrary; what the
			// arithmetic must see is the difference.
			Current: w + 4096,
			File:    4096,
		}
	}
	return out
}

func flat(n int, workload int64) []memory.Sample {
	vals := make([]int64, n)
	for i := range vals {
		vals[i] = workload
	}
	return window(vals...)
}

var _ = Describe("Reading a sample file", func() {
	header := strings.Join([]string{
		"unix_ms", "memory_current", "file", "anon", "sock", "slab", "kernel",
	}, ",")

	It("parses every column measure.sh writes", func() {
		samples, err := memory.ParseCSV(strings.NewReader(
			header + "\n1700000000000,900,100,300,40,200,500\n"))

		Expect(err).NotTo(HaveOccurred())
		Expect(samples).To(HaveLen(1))
		Expect(samples[0]).To(Equal(memory.Sample{
			UnixMilli: 1700000000000, Current: 900, File: 100,
			Anon: 300, Sock: 40, Slab: 200, Kernel: 500,
		}))
	})

	It("subtracts page cache, because that is what M(x) is", func() {
		samples, err := memory.ParseCSV(strings.NewReader(
			header + "\n1700000000000,900,100,0,0,0,0\n"))

		Expect(err).NotTo(HaveOccurred())
		Expect(samples[0].Workload()).To(BeEquivalentTo(800))
	})

	// A positionally-read CSV whose columns moved is the failure mode that
	// produces a number nobody can tell is wrong, so the header is a contract
	// rather than a comment.
	It("refuses a file whose columns are in a different order", func() {
		_, err := memory.ParseCSV(strings.NewReader(
			"unix_ms,file,memory_current,anon,sock,slab,kernel\n1,2,3,4,5,6,7\n"))

		Expect(err).To(MatchError(ContainSubstring("unexpected header")))
	})

	It("refuses an empty file rather than reporting a window of nothing", func() {
		_, err := memory.ParseCSV(strings.NewReader(""))
		Expect(err).To(MatchError(ContainSubstring("empty")))
	})

	It("names the row and the column when a cell is not a number", func() {
		_, err := memory.ParseCSV(strings.NewReader(
			header + "\n1700000000000,900,100,0,0,0,0\n1700000001000,900,nine,0,0,0,0\n"))

		Expect(err).To(MatchError(ContainSubstring("row 2")))
		Expect(err).To(MatchError(ContainSubstring("file")))
	})
})

var _ = Describe("Checking that a window is §3.6's window", func() {
	It("accepts 60 samples at 1 Hz", func() {
		Expect(memory.CheckWindow(flat(memory.SpecSamples, 1_000_000))).To(Succeed())
	})

	// The failure this exists for: a sampler that died early, or a run that
	// was stopped by hand, leaves a short file that still medians perfectly
	// well. §3.6 fixes the count, so a short window is a rejected run.
	DescribeTable("rejects a window that is not 60 samples",
		func(n int) {
			err := memory.CheckWindow(flat(n, 1_000_000))
			Expect(err).To(MatchError(ContainSubstring(fmt.Sprintf("holds %d samples", n))))
		},
		Entry("one short", memory.SpecSamples-1),
		Entry("one long", memory.SpecSamples+1),
		Entry("half", memory.SpecSamples/2),
		Entry("none", 0),
	)

	It("rejects a window whose sampler missed a second", func() {
		samples := flat(memory.SpecSamples, 1_000_000)
		for i := 30; i < len(samples); i++ {
			samples[i].UnixMilli += 1000 // one whole missed tick, back-filled by the next
		}

		Expect(memory.CheckWindow(samples)).To(MatchError(ContainSubstring("outside 1 Hz")))
	})

	It("tolerates the scheduling jitter a shell loop on a shared host actually shows", func() {
		samples := flat(memory.SpecSamples, 1_000_000)
		for i := range samples {
			if i%2 == 1 {
				samples[i].UnixMilli += memory.PeriodToleranceMS - 1
			}
		}

		Expect(memory.CheckWindow(samples)).To(Succeed())
	})

	It("rejects a window that goes backwards in time", func() {
		samples := flat(memory.SpecSamples, 1_000_000)
		// One step backwards, inside the jitter tolerance in magnitude, so the
		// spec that fails is the monotonicity one and not the 1 Hz one.
		samples[11].UnixMilli = samples[10].UnixMilli - 50

		Expect(memory.CheckWindow(samples)).To(MatchError(ContainSubstring("not monotonic")))
	})
})

var _ = Describe("M(x) and the headline figure", func() {
	It("takes the mean of the two central readings, because 60 is even", func() {
		// Deliberately unsorted on input: the median is an order statistic,
		// and a sampler writes in time order, not in value order.
		m, err := memory.Median(window(40, 10, 30, 20))

		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(Equal(25.0))
	})

	It("takes the central reading when the count is odd", func() {
		m, err := memory.Median(window(40, 10, 30))
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(Equal(30.0))
	})

	It("is unmoved by an outlier, which is why §3.6 says median and not mean", func() {
		m, err := memory.Median(window(100, 100, 100, 100, 100, 1_000_000))
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(Equal(100.0))
	})

	It("divides the difference by N", func() {
		perSession, err := memory.PerSession(50_000_000, 95_000_000, 1000)

		Expect(err).NotTo(HaveOccurred())
		Expect(perSession).To(Equal(45_000.0))
	})

	// Clamping a negative to zero would turn "these two windows are not
	// comparable" into "sessions are free", which is the flattering direction.
	It("returns a negative figure rather than hiding an incomparable pair", func() {
		perSession, err := memory.PerSession(95_000_000, 50_000_000, 1000)

		Expect(err).NotTo(HaveOccurred())
		Expect(perSession).To(BeNumerically("<", 0))
	})

	It("refuses to divide by no sessions", func() {
		_, err := memory.PerSession(1, 2, 0)
		Expect(err).To(MatchError(ContainSubstring("without sessions")))
	})

	It("reports no samples as an error rather than as zero bytes", func() {
		_, err := memory.Median(nil)
		Expect(err).To(MatchError(ContainSubstring("no samples")))
	})
})
