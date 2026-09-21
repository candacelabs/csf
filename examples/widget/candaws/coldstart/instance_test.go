package coldstart

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The temperature ladder is a table of pure functions, so every rule below is
// driven from a literal. Not one specification in this file starts a goroutine,
// which is the point: an instance standing up to reach a temperature is an
// instance whose start-up budget has to be waited out.

var _ = Describe("The temperature ladder", func() {
	// A map can be enumerated and a method set cannot, which is the whole
	// reason this is a table: a sixth lifecycle event is an entry here, and
	// without this assertion it would be one nothing ever applies.
	It("has exactly one rule per lifecycle event, and no rule for anything else", func() {
		events := []lifecycleEvent{eventSpawn, eventWarmed, eventInvoked, eventIdled, eventFrozen}
		Expect(temperatureRules).To(HaveLen(len(events)))
		for _, happened := range events {
			Expect(temperatureRules).To(HaveKey(happened), "no rule applies event %d", uint8(happened))
		}
	})

	It("names every temperature it reports", func() {
		for _, heat := range []temperature{
			temperatureCold, temperatureWarming, temperatureWarm, temperatureIdle,
		} {
			Expect(heat.String()).ToNot(ContainSubstring("temperature "),
				"a temperature that prints as a number says nothing in a failure")
		}
	})

	It("climbs cold to warming to warm to idle, and never skips warming", func() {
		heat, moved := advance(temperatureCold, eventSpawn)
		Expect(moved).To(BeTrue())
		Expect(heat).To(Equal(temperatureWarming))

		heat, moved = advance(heat, eventWarmed)
		Expect(moved).To(BeTrue())
		Expect(heat).To(Equal(temperatureWarm))

		heat, moved = advance(heat, eventIdled)
		Expect(moved).To(BeTrue())
		Expect(heat).To(Equal(temperatureIdle))

		// The rung that cannot be skipped. Nothing reaches warm except from
		// warming, so no instance ever serves without having paid the budget
		// the customer is billed for.
		refused, legal := advance(temperatureCold, eventWarmed)
		Expect(legal).To(BeFalse())
		Expect(refused).To(Equal(temperatureCold),
			"an illegal move leaves the temperature where it was; a temperature that "+
				"can be dragged sideways is a rumour rather than a report")
	})

	It("lets an idle instance serve again without another start-up", func() {
		heat, moved := advance(temperatureIdle, eventInvoked)
		Expect(moved).To(BeTrue())
		Expect(heat).To(Equal(temperatureWarm),
			"that an instance is already there is the whole difference between the "+
				"premium tier and the other one")
	})

	It("refuses to invoke an instance that has not warmed", func() {
		_, legal := advance(temperatureWarming, eventInvoked)
		Expect(legal).To(BeFalse())
	})

	It("freezes anything that exists and nothing that does not", func() {
		frozen, legal := advance(temperatureIdle, eventFrozen)
		Expect(legal).To(BeTrue())
		Expect(frozen).To(Equal(temperatureCold))

		_, illegal := advance(temperatureCold, eventFrozen)
		Expect(illegal).To(BeFalse(),
			"there is no frozen state to be in: a frozen instance is a goroutine that returned")
	})
})

var _ = Describe("The published view", func() {
	It("holds of a pool that could exist, and not of one that could not", func() {
		Expect(PoolView{WarmInstances: 1, LiveInstances: 2}.Sound()).To(BeTrue())
		Expect(PoolView{WarmInstances: 3, LiveInstances: 2}.Sound()).To(BeFalse(),
			"an instance cannot have paid a start-up it was never spawned for")
		Expect(PoolView{}.ScaledToZero()).To(BeTrue())
		Expect(PoolView{LiveInstances: 1}.ScaledToZero()).To(BeFalse())
	})
})
