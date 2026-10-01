package component_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/candaceos/component"
)

func TestComponent(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CandaceOS Component Suite")
}

func noop(ctx context.Context, capabilities component.ICapabilities) error { return nil }

func define(name string, requirements ...*component.Definition) *component.Definition {
	GinkgoHelper()
	definition, err := component.New(
		name,
		component.WithAssemble(noop),
		component.WithRequires(requirements...),
	)
	Expect(err).NotTo(HaveOccurred())
	return definition
}

func names(definitions []*component.Definition) []string {
	resolved := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		resolved = append(resolved, definition.Name())
	}
	return resolved
}

var _ = Describe("New", func() {
	It("rejects a nil option by its 1-based position", func() {
		definition, err := component.New("alpha", component.WithAssemble(noop), nil)
		Expect(definition).To(BeNil())
		Expect(err).To(MatchError(component.ErrNilOption))
		Expect(err.Error()).To(ContainSubstring(`component "alpha" option 2`))
	})

	DescribeTable(
		"rejects a name outside the grammar or byte bound",
		func(name string) {
			definition, err := component.New(name, component.WithAssemble(noop))
			Expect(definition).To(BeNil())
			Expect(err).To(MatchError(component.ErrName))
		},
		Entry("empty", ""),
		Entry("leading digit", "1alpha"),
		Entry("leading dash", "-alpha"),
		Entry("uppercase", "Alpha"),
		Entry("underscore", "alpha_beta"),
		Entry("dot", "alpha.beta"),
		Entry("slash", "alpha/beta"),
		Entry("trailing space", "alpha "),
		Entry("one byte past the bound", "a"+strings.Repeat("b", component.MaxNameBytes)),
	)

	It("accepts a name exactly at the byte bound", func() {
		name := "a" + strings.Repeat("b", component.MaxNameBytes-1)
		Expect(name).To(HaveLen(component.MaxNameBytes))
		definition, err := component.New(name, component.WithAssemble(noop))
		Expect(err).NotTo(HaveOccurred())
		Expect(definition.Name()).To(Equal(name))
	})

	It("accepts the full grammar", func() {
		definition, err := component.New("a0-b9", component.WithAssemble(noop))
		Expect(err).NotTo(HaveOccurred())
		Expect(definition.Name()).To(Equal("a0-b9"))
	})

	It("requires an assemble function", func() {
		definition, err := component.New("alpha")
		Expect(definition).To(BeNil())
		Expect(err).To(MatchError(component.ErrMissingAssemble))
	})

	It("rejects a nil assemble function", func() {
		definition, err := component.New("alpha", component.WithAssemble(nil))
		Expect(definition).To(BeNil())
		Expect(err).To(MatchError(component.ErrMissingAssemble))
	})

	It("rejects a nil requirement", func() {
		definition, err := component.New(
			"alpha",
			component.WithAssemble(noop),
			component.WithRequires(define("beta"), nil),
		)
		Expect(definition).To(BeNil())
		Expect(err).To(MatchError(component.ErrNilDefinition))
		Expect(err.Error()).To(ContainSubstring("requirement 2"))
	})

	It("rejects a requirement naming the component itself", func() {
		definition, err := component.New(
			"alpha",
			component.WithAssemble(noop),
			component.WithRequires(define("alpha")),
		)
		Expect(definition).To(BeNil())
		Expect(err).To(MatchError(component.ErrRequirement))
		Expect(err.Error()).To(ContainSubstring("requires itself"))
	})

	It("rejects a repeated requirement across separate options", func() {
		beta := define("beta")
		definition, err := component.New(
			"alpha",
			component.WithAssemble(noop),
			component.WithRequires(beta),
			component.WithRequires(beta),
		)
		Expect(definition).To(BeNil())
		Expect(err).To(MatchError(component.ErrRequirement))
		Expect(err.Error()).To(ContainSubstring(`requires "beta" more than once`))
	})

	It("reports an option failure with its 1-based position", func() {
		definition, err := component.New(
			"alpha",
			component.WithAssemble(noop),
			component.WithStart(nil),
		)
		Expect(definition).To(BeNil())
		Expect(err).To(MatchError(component.ErrRequirement))
		Expect(err.Error()).To(ContainSubstring(`component "alpha" option 2`))
	})

	It("treats start and stop as optional no-ops", func(ctx SpecContext) {
		definition := define("alpha")
		Expect(definition.Start(ctx)).To(Succeed())
		Expect(definition.Stop(ctx)).To(Succeed())
	})
})

var _ = Describe("Order", func() {
	It("breaks ties by registration order rather than by name or map order", func() {
		zulu := define("zulu")
		alpha := define("alpha")
		consumer := define("consumer", zulu, alpha)

		registered, err := component.Order(zulu, alpha, consumer)
		Expect(err).NotTo(HaveOccurred())
		Expect(names(registered)).To(Equal([]string{"zulu", "alpha", "consumer"}))

		reversed, err := component.Order(alpha, zulu, consumer)
		Expect(err).NotTo(HaveOccurred())
		Expect(names(reversed)).To(Equal([]string{"alpha", "zulu", "consumer"}))
	})

	It("places a requirement before a dependent registered earlier", func() {
		late := define("late")
		early := define("early", late)

		resolved, err := component.Order(early, late)
		Expect(err).NotTo(HaveOccurred())
		Expect(names(resolved)).To(Equal([]string{"late", "early"}))
	})

	It("resolves an empty set", func() {
		resolved, err := component.Order()
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(BeEmpty())
	})

	It("rejects a nil definition by its 1-based position", func() {
		resolved, err := component.Order(define("alpha"), nil)
		Expect(resolved).To(BeNil())
		Expect(err).To(MatchError(component.ErrNilDefinition))
		Expect(err.Error()).To(ContainSubstring("definition 2"))
	})

	It("rejects two definitions sharing one name", func() {
		resolved, err := component.Order(define("alpha"), define("beta"), define("alpha"))
		Expect(resolved).To(BeNil())
		Expect(err).To(MatchError(component.ErrDuplicateName))
		Expect(err.Error()).To(ContainSubstring(`"alpha" at positions 1 and 3`))
	})

	It("rejects a requirement outside the set, naming both components", func() {
		absent := define("absent")
		dependent := define("dependent", absent)

		resolved, err := component.Order(dependent)
		Expect(resolved).To(BeNil())
		Expect(err).To(MatchError(component.ErrMissingRequirement))
		Expect(err.Error()).To(ContainSubstring(`"dependent" requires "absent"`))
	})

	It("rejects a requirement satisfied only by a same-named impostor", func() {
		declared := define("shared")
		impostor := define("shared")
		dependent := define("dependent", declared)

		resolved, err := component.Order(impostor, dependent)
		Expect(resolved).To(BeNil())
		Expect(err).To(MatchError(component.ErrMissingRequirement))
	})

	It("never invokes a definition function", func(ctx SpecContext) {
		invocations := 0
		record := func() error {
			invocations++
			return nil
		}
		first, err := component.New(
			"first",
			component.WithAssemble(func(ctx context.Context, capabilities component.ICapabilities) error {
				return record()
			}),
			component.WithStart(func(ctx context.Context) error { return record() }),
			component.WithStop(func(ctx context.Context) error { return record() }),
		)
		Expect(err).NotTo(HaveOccurred())
		second := define("second", first)

		resolved, err := component.Order(second, first)
		Expect(err).NotTo(HaveOccurred())
		Expect(names(resolved)).To(Equal([]string{"first", "second"}))
		Expect(invocations).To(Equal(0))

		Expect(first.Assemble(ctx, nil)).To(Succeed())
		Expect(first.Start(ctx)).To(Succeed())
		Expect(first.Stop(ctx)).To(Succeed())
		Expect(invocations).To(Equal(3))
	})
})

var _ = Describe("Sentinels", func() {
	DescribeTable(
		"are distinct and errors.Is-matchable through a wrapped detail",
		func(sentinel error) {
			wrapped := fmt.Errorf("bringing up CandaceOS: %w", sentinel)
			Expect(errors.Is(wrapped, sentinel)).To(BeTrue())
			Expect(sentinel.Error()).To(HavePrefix("component: "))
			for _, other := range sentinels() {
				if other == sentinel {
					continue
				}
				Expect(errors.Is(wrapped, other)).To(BeFalse())
			}
		},
		Entry("nil option", component.ErrNilOption),
		Entry("nil definition", component.ErrNilDefinition),
		Entry("name", component.ErrName),
		Entry("missing assemble", component.ErrMissingAssemble),
		Entry("requirement", component.ErrRequirement),
		Entry("duplicate name", component.ErrDuplicateName),
		Entry("missing requirement", component.ErrMissingRequirement),
		Entry("dependency cycle", component.ErrDependencyCycle),
	)
})

func sentinels() []error {
	return []error{
		component.ErrNilOption,
		component.ErrNilDefinition,
		component.ErrName,
		component.ErrMissingAssemble,
		component.ErrRequirement,
		component.ErrDuplicateName,
		component.ErrMissingRequirement,
		component.ErrDependencyCycle,
	}
}
