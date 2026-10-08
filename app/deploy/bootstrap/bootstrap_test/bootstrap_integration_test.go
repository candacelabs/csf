package bootstrap_test

import (
	"testing"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/app/deploy/bootstrap"
	"github.com/candacelabs/csf/web/deploy/webui"
)

const testHTTPServicePath = "/test-service"

type testHTTPService struct{}

func TestBootstrapIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Deploy Bootstrap Integration Suite")
}

var _ = Describe("Core lifecycle", func() {
	It("rejects a nil optional HTTP service before loading infrastructure", func(ctx SpecContext) {
		core, err := bootstrap.AssembleCore(ctx, "test", bootstrap.WithHTTPService(nil))
		Expect(core).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("Deploy HTTP service is required")))
	})

	It("rejects a brand that could smuggle CSS into the operator page", func(ctx SpecContext) {
		core, err := bootstrap.AssembleCore(ctx, "test", bootstrap.WithBrand(webui.Brand{
			ProductName: "Atlas",
			Palette:     webui.Palette{Canvas: "#fff; position: fixed"},
		}))
		Expect(core).To(BeNil())
		Expect(err).To(MatchError(webui.ErrInvalidPaletteValue))
	})

	It("rejects a sidebar entry that cannot be rendered as one labeled link", func(ctx SpecContext) {
		core, err := bootstrap.AssembleCore(ctx, "test", bootstrap.WithNavItem(webui.NavItem{
			Href: "/reports",
		}))
		Expect(core).To(BeNil())
		Expect(err).To(MatchError(webui.ErrInvalidNavItem))
	})

	It("rejects a missing or duplicated UI overlay before loading infrastructure", func(ctx SpecContext) {
		core, err := bootstrap.AssembleCore(ctx, "test", bootstrap.WithUIOverlay(nil))
		Expect(core).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("Deploy UI overlay is required")))

		core, err = bootstrap.AssembleCore(ctx, "test",
			bootstrap.WithUIOverlay(fstest.MapFS{}),
			bootstrap.WithUIOverlay(fstest.MapFS{}),
		)
		Expect(core).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("only one UI overlay")))
	})

})
