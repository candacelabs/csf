package memory_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/test/memory"
)

func runAt(id string, n int, perSession float64) memory.Run {
	return memory.Run{ID: id, N: n, PerSession: perSession}
}

var _ = Describe("Pooling a cell's runs", func() {
	It("pools with a median, so one contended run does not move the number", func() {
		cell, err := memory.Summarize([]memory.Run{
			runAt("r1", 1000, 40_000),
			runAt("r2", 1000, 41_000),
			runAt("r3", 1000, 40_500),
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(cell.PooledPerSession).To(Equal(40_500.0))
		Expect(cell.MinPerSession).To(Equal(40_000.0))
		Expect(cell.MaxPerSession).To(Equal(41_000.0))
	})

	It("marks a cell unstable when the per-run spread exceeds 20 %", func() {
		cell, err := memory.Summarize([]memory.Run{
			runAt("r1", 1000, 40_000),
			runAt("r2", 1000, 50_000),
			runAt("r3", 1000, 45_000),
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(cell.Spread).To(BeNumerically("~", 10_000.0/45_000.0, 1e-9))
		Expect(cell.Unstable).To(BeTrue())
	})

	It("leaves a tight cell stable", func() {
		cell, err := memory.Summarize([]memory.Run{
			runAt("r1", 1000, 40_000),
			runAt("r2", 1000, 41_000),
			runAt("r3", 1000, 40_500),
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(cell.Unstable).To(BeFalse())
	})

	// Pooling runs at different N would produce a number that is about neither
	// concurrency, and the sub-linearity check would then compare a cell with
	// itself.
	It("refuses to pool runs at different concurrencies", func() {
		_, err := memory.Summarize([]memory.Run{
			runAt("r1", 1000, 40_000),
			runAt("r2", 100, 40_000),
		})

		Expect(err).To(MatchError(ContainSubstring("not one cell")))
	})

	It("refuses a cell with no runs", func() {
		_, err := memory.Summarize(nil)
		Expect(err).To(MatchError(ContainSubstring("no runs")))
	})
})

var _ = Describe("RFC-0001 §6.3's sub-linearity check", func() {
	cellAt := func(n int, perSession float64) memory.Cell {
		c, err := memory.Summarize([]memory.Run{runAt("r1", n, perSession)})
		Expect(err).NotTo(HaveOccurred())
		return c
	}

	It("passes when N=1000 is within 15 % of N=100", func() {
		rel, ok, err := memory.SubLinear(cellAt(100, 40_000), cellAt(1000, 44_000))

		Expect(err).NotTo(HaveOccurred())
		Expect(rel).To(BeNumerically("~", 0.10, 1e-9))
		Expect(ok).To(BeTrue())
	})

	It("fails when per-session memory grows with concurrency", func() {
		_, ok, err := memory.SubLinear(cellAt(100, 40_000), cellAt(1000, 48_000))

		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	// A 20 % FALL is as much a sign that the two cells are not measuring the
	// same thing as a 20 % rise, so the bound is two-sided even though the
	// design defect §6.3 names is growth.
	It("fails when per-session memory falls by more than 15 %", func() {
		_, ok, err := memory.SubLinear(cellAt(100, 40_000), cellAt(1000, 30_000))

		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	It("refuses to divide by an N=100 cell with no figure", func() {
		_, _, err := memory.SubLinear(memory.Cell{}, cellAt(1000, 40_000))
		Expect(err).To(MatchError(ContainSubstring("no figure")))
	})
})
