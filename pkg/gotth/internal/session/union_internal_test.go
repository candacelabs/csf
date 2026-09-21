package session

import (
	"math/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
)

// The specs are in package session rather than session_test because
// unionReaches and unionEdges are unexported and the property under test is
// that they agree — which is a statement about the two functions, not about
// anything a frame shows.
//
// U-4. actor.go states the requirement precisely — "the exact half must agree
// with unionEdges exactly, so it applies the same three rules" — and then
// implements those rules twice, sixty lines apart: the same seeding of `seen`
// with the origin's own identifier, the same zero skip, the same first-seen
// deduplication, the same traversal order over origin.Contributing, then
// pendingIDs, then the deferred origin's identifier, then its Contributing.
// They do agree today. Nothing would have noticed if they stopped, and the cost
// of a divergence is a frame: unionReaches decides whether to flush, and
// unionEdges decides what the flushed frame carries.
var _ = Describe("The contributing union's two implementations", func() {
	// Identifiers are drawn from a pool smaller than the lists, so duplicates
	// across the four sources are the common case rather than the corner: the
	// deduplication rule is the one most likely to drift, and a pool of
	// distinct identifiers would never exercise it.
	const pool = 12

	var rng *rand.Rand

	BeforeEach(func() {
		// Seeded from Ginkgo's own seed, which it prints, so a failure is
		// reproducible with --seed.
		rng = rand.New(rand.NewSource(GinkgoRandomSeed()))
	})

	ids := func(n int) []uint64 {
		out := make([]uint64, 0, n)
		for i := 0; i < n; i++ {
			// Zero is drawn deliberately: it is not an identifier and both
			// implementations must skip it.
			out = append(out, uint64(rng.Intn(pool+1)))
		}
		return out
	}

	anOrigin := func() protocol.Origin {
		return protocol.Origin{
			EventID:      uint64(rng.Intn(pool + 1)),
			Contributing: ids(rng.Intn(6)),
		}
	}

	It("agree about every threshold, over randomized deferred state", func() {
		for trial := 0; trial < 300; trial++ {
			emitting := anOrigin()
			pending := ids(rng.Intn(8))

			var deferred *protocol.Origin
			if rng.Intn(2) == 0 {
				o := anOrigin()
				deferred = &o
			}

			build := func() *Actor[testSubject] {
				a := &Actor[testSubject]{pendingIDs: append([]uint64(nil), pending...)}
				if deferred != nil {
					o := *deferred
					a.pendingOrig = &o
				}
				return a
			}

			// The exact set, through the path an emission takes.
			taken := build()
			_, contributing := taken.takePending(emitting)
			exact := len(unionEdges(emitting, contributing))

			// The predicate, at every threshold either side of it. unionReaches
			// does not mutate, so one actor answers them all — and the
			// thresholds below the sum-of-parts short circuit and above it are
			// both covered, because exact is never more than that sum.
			asking := build()
			for n := 0; n <= exact+3; n++ {
				Expect(asking.unionReaches(emitting, n)).To(Equal(exact >= n),
					"trial %d: unionReaches(%d) disagreed with an exact union of %d "+
						"(origin %+v, pending %v, deferred %+v)",
					trial, n, exact, emitting, pending, deferred)
			}
		}
	})
})
