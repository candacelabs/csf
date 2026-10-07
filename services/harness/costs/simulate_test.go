// Copyright 2026 Candace Labs

package costs_test

import (
	"math"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/costs"
)

// The hand-computed ski-rental case. Prices are powers of two so every
// dollar is exact: a prefix of 2^20 tokens read at 2^-20 $/token costs 1 $
// per keep-alive request, and written again at 20 * 2^-20 $/token costs a
// rebuild premium of 20 - 1 = 19 $. The break-even rule keeps alive up to
// floor(19 / 1) = 19 requests.
const (
	model          = "m"
	prefix         = 1 << 20
	requestUSD     = 1.0
	rebuildUSD     = 19.0
	breakEven      = 19
	lifetime       = time.Hour
	exactTolerance = 1e-12
)

var price = costs.Price{Read: math.Ldexp(1, -20), Write: 20 * math.Ldexp(1, -20)}

func gap(length time.Duration, returned bool) costs.Gap {
	return costs.Gap{Day: "2026-10-05", Model: model, Length: length, Returned: returned, Prefix: prefix}
}

var _ = Describe("keep-alive as ski rental", func() {
	keepAlive := costs.KeepAlive{Lifetime: lifetime, Prices: map[string]costs.Price{model: price}}
	// Three returns: inside the lifetime, after 3.5 hours (3 requests bridge
	// it) and after 30 hours (29 requests would).
	short, middle, long := gap(30*time.Minute, true), gap(210*time.Minute, true), gap(30*time.Hour, true)

	It("prices one request, one rebuild and the break-even limit exactly", func() {
		Expect(keepAlive.Request(middle)).To(Equal(requestUSD))
		Expect(keepAlive.Rebuild(middle)).To(Equal(rebuildUSD))
		Expect(keepAlive.BreakEven(middle)).To(Equal(breakEven))
		Expect([]int{keepAlive.Requests(short), keepAlive.Requests(middle), keepAlive.Requests(long)}).To(Equal([]int{0, 3, 29}))
	})

	It("charges requests while they last and the rebuild when they run out", func() {
		Expect(keepAlive.Cost(short, 0)).To(Equal(0.0))
		Expect(keepAlive.Cost(middle, 0)).To(Equal(19.0))
		Expect(keepAlive.Cost(middle, 3)).To(Equal(3.0))
		Expect(keepAlive.Cost(long, 3)).To(Equal(22.0))
		Expect(keepAlive.Cost(long, 29)).To(Equal(29.0))
	})

	It("charges a session that never returns only the requests sent", func() {
		tail := gap(150*time.Minute, false)
		Expect(keepAlive.Cost(tail, 0)).To(Equal(0.0))
		Expect(keepAlive.Cost(tail, 19)).To(Equal(2.0))
		Expect(keepAlive.Offline(tail)).To(Equal(0.0))
	})

	It("reproduces the hand-computed replay of every policy", func() {
		// never: 0 + 19 + 19 = 38. fixed k: g2 costs 3 once k >= 3, else
		// k + 19; g3 costs k + 19 below 29. break-even (19): 0 + 3 + 38 = 41.
		// The expected-cost minimum over k in 0..29 is k = 3: 0 + 3 + 22 = 25.
		// The offline optimum is 0 + min(3, 19) + min(29, 19) = 22, and the
		// break-even rule's 41 is within twice it.
		outcomes, parameter, _ := costs.KeepAlivePolicies(keepAlive, []costs.Gap{short, middle, long}, 1)
		type row struct {
			Policy    string
			Parameter float64
			USD       float64
			Saving    float64
		}
		rows := []row{}
		for _, outcome := range outcomes {
			rows = append(rows, row{outcome.GetPolicy(), outcome.GetParameter(), outcome.GetUsdPerDay(), outcome.GetSavingUsdPerDay()})
		}
		Expect(rows).To(Equal([]row{
			{"never", 0, 38, 0},
			{"fixed", 1, 40, -2},
			{"fixed", 2, 42, -4},
			{"fixed", 4, 26, 12},
			{"fixed", 8, 30, 8},
			{"break-even rule", -1, 41, -3},
			{"expected-cost minimum", 3, 25, 13},
			{"offline optimum (lower bound)", -1, 22, 16},
		}))
		Expect(41.0).To(BeNumerically("<=", 2*22.0))
		By("keeping today's limit: three gaps cannot tell the saving from zero")
		Expect(parameter.GetName()).To(Equal(costs.ParameterKeepAliveRequests))
		Expect(parameter.GetValue()).To(Equal(0.0))
		Expect(parameter.GetChosen()).To(BeFalse())
		Expect(outcomes[0].GetChosen()).To(BeTrue())
	})

	It("replays an empty record to zero rather than failing", func() {
		outcomes, parameter, saving := costs.KeepAlivePolicies(keepAlive, nil, 1)
		Expect(outcomes).NotTo(BeEmpty())
		for _, outcome := range outcomes {
			Expect(outcome.GetUsdPerDay()).To(Equal(0.0))
		}
		Expect(parameter.GetChosen()).To(BeFalse())
		Expect(saving).To(BeEmpty())
	})
})

var _ = Describe("idle close", func() {
	idle := costs.IdleClose{Lifetime: lifetime, ResidentBytes: 1e9, ReopenPremium: 0.5, ReopenLatency: 2 * time.Second}
	returnedSoon, returnedLate, tail := gap(10*time.Minute, true), gap(2*time.Hour, true), gap(30*time.Minute, false)

	It("holds memory until the threshold and pays a reopen on every later return", func() {
		Expect(idle.Cost(returnedSoon, 5*time.Minute)).To(Equal(costs.IdleCost{USD: 0.5, ResidentGBHours: 5.0 / 60, Latency: 2 * time.Second}))
		By("charging no cache premium past the lifetime, where the cache is lost either way")
		Expect(idle.Cost(returnedLate, 5*time.Minute)).To(Equal(costs.IdleCost{ResidentGBHours: 5.0 / 60, Latency: 2 * time.Second}))
		By("never reopening a session that does not return")
		Expect(idle.Cost(tail, 5*time.Minute)).To(Equal(costs.IdleCost{ResidentGBHours: 5.0 / 60}))
	})

	It("holds the whole gap when it never closes", func() {
		held := 0.0
		for _, each := range []costs.Gap{returnedSoon, returnedLate, tail} {
			cost := idle.Cost(each, -1)
			Expect(cost.USD).To(Equal(0.0))
			Expect(cost.Latency).To(BeZero())
			held += cost.ResidentGBHours
		}
		Expect(held).To(BeNumerically("~", 160.0/60, exactTolerance))
	})
})

var _ = Describe("placement by shared prefix", func() {
	It("moves the unread part of the shared prefix from written to read", func() {
		binds := []costs.Bind{
			{Day: "2026-10-05", Model: model, First: costs.Tokens{Read: 25_000, Write: 3_000}},
			{Day: "2026-10-05", Model: model, First: costs.Tokens{Write: 30_000}},
			{Day: "2026-10-05", Model: model, First: costs.Tokens{Read: 10_000, Write: 4_000}},
		}
		placement := costs.NewPlacement(binds, map[string]costs.Price{model: price})
		Expect(placement.Shared).To(Equal(map[string]int64{model: 25_000}))
		Expect([]int64{placement.Shift(binds[0]), placement.Shift(binds[1]), placement.Shift(binds[2])}).To(Equal([]int64{0, 25_000, 4_000}))
		Expect(placement.Saving(binds[1])).To(Equal(25_000 * 19 * math.Ldexp(1, -20)))
	})
})
