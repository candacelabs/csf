package externalconsumer_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/candaceos/component"

	"example.com/candace-external-consumer/composition"
	"example.com/candace-external-consumer/identity"
	"example.com/candace-external-consumer/noteboard"
)

// The composition root is a library precisely so these specs can assert on the
// values the binary is linked with, rather than on a second copy of them
// written to be asserted on.

var _ = Describe("this repository's composition", func() {
	It("passes the checks the composition root applies before infrastructure is opened", func() {
		// These are the exact validations bootstrap.WithBrand and
		// bootstrap.WithNavItem run, so a palette value or a label this
		// repository got wrong fails here rather than at an operator's first
		// startup.
		Expect(identity.Brand().Validate()).To(Succeed())
		Expect(noteboard.NavItem().Validate()).To(Succeed())
	})

	It("wires one option per seam", func() {
		product, err := composition.New()
		Expect(err).NotTo(HaveOccurred())

		// Brand, overlay, nav item, HTTP service, three components, harness
		// factory. Every seam this repository documents must still be wired.
		Expect(product.Options).To(HaveLen(8))
		for index, option := range product.Options {
			Expect(option).NotTo(BeNil(), "option %d is nil", index+1)
		}
	})

	It("orders the service it added after the components it depends on", func() {
		product, err := composition.New()
		Expect(err).NotTo(HaveOccurred())

		resolved, err := component.Order(product.Components...)
		Expect(err).NotTo(HaveOccurred())
		names := make([]string, 0, len(resolved))
		for _, definition := range resolved {
			names = append(names, definition.Name())
		}
		Expect(names).To(Equal([]string{"steering-store", "steering-service", noteboard.ComponentName}))
	})

	It("keeps that order however the components are registered", func() {
		product, err := composition.New()
		Expect(err).NotTo(HaveOccurred())
		Expect(product.Components).To(HaveLen(3))

		reversed := []*component.Definition{
			product.Components[2], product.Components[1], product.Components[0],
		}
		resolved, err := component.Order(reversed...)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved[0].Name()).To(Equal("steering-store"))
		Expect(resolved[2].Name()).To(Equal(noteboard.ComponentName))
	})

	It("names the missing requirement when the graph is registered incomplete", func() {
		product, err := composition.New()
		Expect(err).NotTo(HaveOccurred())

		_, err = component.Order(product.Components[2])
		Expect(err).To(MatchError(component.ErrMissingRequirement))
		Expect(err.Error()).To(ContainSubstring(noteboard.ComponentName))
		Expect(err.Error()).To(ContainSubstring("steering-service"))
	})
})
