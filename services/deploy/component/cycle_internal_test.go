package component

// Pointer-identity requirements make a cycle unconstructible through New: a
// requirement must already exist when its dependent is built. These specs wire
// the defensive detector directly so its reported path stays covered.

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func cyclic(name string) *Definition {
	return &Definition{
		name:     name,
		assemble: func(ctx context.Context, capabilities ICapabilities) error { return nil },
	}
}

var _ = Describe("Order cycle reporting", func() {
	It("names every component on the cycle it traverses", func() {
		alpha, beta, gamma := cyclic("a"), cyclic("b"), cyclic("c")
		alpha.requirements = []*Definition{beta}
		beta.requirements = []*Definition{gamma}
		gamma.requirements = []*Definition{alpha}

		resolved, err := Order(alpha, beta, gamma)
		Expect(resolved).To(BeNil())
		Expect(err).To(MatchError(ErrDependencyCycle))
		Expect(err).To(MatchError("component: dependency cycle: a -> b -> c -> a"))
	})

	It("walks into a cycle that excludes the first unresolved component", func() {
		entry, alpha, beta := cyclic("entry"), cyclic("a"), cyclic("b")
		entry.requirements = []*Definition{alpha}
		alpha.requirements = []*Definition{beta}
		beta.requirements = []*Definition{alpha}

		resolved, err := Order(entry, alpha, beta)
		Expect(resolved).To(BeNil())
		Expect(err).To(MatchError("component: dependency cycle: a -> b -> a"))
	})
})
