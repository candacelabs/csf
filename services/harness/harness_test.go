package harness_test

import (
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	deployv1 "github.com/candacelabs/csf/proto/candace/deploy/v1"
	"github.com/candacelabs/csf/services/harness"
)

func TestHarness(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Deploy Harness SDK Suite")
}

var _ = Describe("FactoryFunc", func() {
	It("preserves the configured factory boundary", func() {
		harnessContext := &deployv1.HarnessContext{Workspace: "/workspace"}
		expected := &harness.Instance{Identity: &deployv1.HarnessRuntimeIdentity{
			Backend:        deployv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
			Implementation: "example",
		}}
		factory := harness.FactoryFunc(func(received *deployv1.HarnessContext, host harness.IHost) (*harness.Instance, error) {
			Expect(received).To(BeIdenticalTo(harnessContext))
			Expect(host).To(BeNil())
			return expected, nil
		})
		var contract harness.IFactory = factory

		actual, err := contract.New(harnessContext, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(actual).To(BeIdenticalTo(expected))
	})

	It("returns the implementation error unchanged", func() {
		expected := errors.New("constructing custom harness")
		factory := harness.FactoryFunc(func(harnessContext *deployv1.HarnessContext, host harness.IHost) (*harness.Instance, error) {
			return nil, expected
		})

		instance, err := factory.New(&deployv1.HarnessContext{Workspace: "/workspace"}, nil)

		Expect(instance).To(BeNil())
		Expect(err).To(MatchError(expected))
	})
})
