package longturns_test

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/longturns"
)

func TestLongTurns(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Long Turn Detector")
}

var _ = Describe("Long Turn Detector", func() {
	It("detects turns exceeding the p99 threshold", func() {
		threshold := 100 * time.Millisecond
		detector := longturns.NewDetectorWithThreshold(threshold)

		obs := detector.StartTurn("session-1", 1)
		time.Sleep(150 * time.Millisecond)
		detector.EndTurn(obs)

		Expect(obs.IsLong).To(BeTrue())
		Expect(detector.LongTurnCount()).To(Equal(1))
	})

	It("does not flag turns under the threshold", func() {
		threshold := 100 * time.Millisecond
		detector := longturns.NewDetectorWithThreshold(threshold)

		obs := detector.StartTurn("session-1", 1)
		time.Sleep(50 * time.Millisecond)
		detector.EndTurn(obs)

		Expect(obs.IsLong).To(BeFalse())
		Expect(detector.LongTurnCount()).To(Equal(0))
	})

	It("tracks multiple sessions independently", func() {
		threshold := 100 * time.Millisecond
		detector := longturns.NewDetectorWithThreshold(threshold)

		obs1 := detector.StartTurn("session-1", 1)
		obs2 := detector.StartTurn("session-2", 1)

		time.Sleep(150 * time.Millisecond)

		detector.EndTurn(obs1)
		detector.EndTurn(obs2)

		Expect(detector.LongTurnCount()).To(Equal(2))
		longs := detector.GetLongTurns()
		Expect(longs).To(HaveLen(2))
	})

	It("reports current turn elapsed duration while running", func() {
		detector := longturns.NewDetector()

		detector.StartTurn("session-1", 1)
		time.Sleep(50 * time.Millisecond)

		elapsed := detector.GetCurrentTurnDuration("session-1")
		Expect(elapsed).To(BeNumerically(">=", 50*time.Millisecond))
		Expect(elapsed).To(BeNumerically("<", 200*time.Millisecond))
	})

	It("detects if a running turn is already long", func() {
		threshold := 100 * time.Millisecond
		detector := longturns.NewDetectorWithThreshold(threshold)

		detector.StartTurn("session-1", 1)
		time.Sleep(50 * time.Millisecond)
		Expect(detector.IsCurrentTurnLong("session-1")).To(BeFalse())

		time.Sleep(100 * time.Millisecond)
		Expect(detector.IsCurrentTurnLong("session-1")).To(BeTrue())
	})

	It("stores turn metadata in completed observations", func() {
		detector := longturns.NewDetector()

		obs := detector.StartTurn("session-1", 5)
		obs.QueueDepth = 3
		time.Sleep(10 * time.Millisecond)
		detector.EndTurn(obs)

		Expect(obs.SessionID).To(Equal("session-1"))
		Expect(obs.TurnNumber).To(Equal(5))
		Expect(obs.QueueDepth).To(Equal(3))
		Expect(obs.Duration).To(BeNumerically(">=", 10*time.Millisecond))
		Expect(obs.StartTime).NotTo(BeZero())
		Expect(obs.EndTime).NotTo(BeZero())
	})

	It("handles nil observer in EndTurn gracefully", func() {
		detector := longturns.NewDetector()
		Expect(func() { detector.EndTurn(nil) }).NotTo(Panic())
	})

	It("returns zero duration for unknown sessions", func() {
		detector := longturns.NewDetector()
		Expect(detector.GetCurrentTurnDuration("unknown")).To(Equal(time.Duration(0)))
	})

	It("returns false for IsCurrentTurnLong on unknown sessions", func() {
		detector := longturns.NewDetector()
		Expect(detector.IsCurrentTurnLong("unknown")).To(BeFalse())
	})
})
