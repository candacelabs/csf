package widget_test

import (
	"bytes"
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/widget"
)

// switchRequest is the operation a verified button in these specs runs.
type switchRequest struct{ Target string }

var _ = Describe("A verified action", func() {
	ctx := context.Background()
	check := func(_ context.Context, request switchRequest) (widget.Expectation, error) {
		if request.Target == "gone" {
			return widget.Expectation{}, errors.New("no such switch")
		}
		return widget.Expectation{Result: "turns " + request.Target + " off", Confirm: []string{request.Target}}, nil
	}

	It("keeps each check's verdict, in order, with the expected result or the refusal", func() {
		verified := widget.VerifyActions(ctx, []widget.VerifiedAction[switchRequest]{
			{ID: "off/lamp", Label: "Off", Request: switchRequest{Target: "lamp"}},
			{ID: "off/gone", Label: "Off", Request: switchRequest{Target: "gone"}},
		}, check)
		Expect(verified).To(HaveLen(2))
		Expect(verified[0].Enabled()).To(BeTrue())
		Expect(verified[0].Expectation).To(Equal(widget.Expectation{Result: "turns lamp off", Confirm: []string{"lamp"}}))
		Expect(verified[1].Enabled()).To(BeFalse())
		Expect(verified[1].Refusal).To(Equal("no such switch"))
		found, ok := widget.FindVerified(verified, "off/gone")
		Expect(ok).To(BeTrue())
		Expect(found.Request.Target).To(Equal("gone"))
		_, ok = widget.FindVerified(verified, "missing")
		Expect(ok).To(BeFalse())
	})

	It("draws an enabled button bound to its event and a refused one disabled with the reason", func() {
		verified := widget.VerifyActions(ctx, []widget.VerifiedAction[switchRequest]{
			{ID: "off/lamp", Label: "Off", Request: switchRequest{Target: "lamp"}},
			{ID: "off/gone", Label: "Off", Request: switchRequest{Target: "gone"}},
		}, check)
		var enabled, refused bytes.Buffer
		Expect(widget.VerifiedButton(verified[0], "panel.press", "action", "act").Render(ctx, &enabled)).To(Succeed())
		Expect(widget.VerifiedButton(verified[1], "panel.press", "action", "act").Render(ctx, &refused)).To(Succeed())
		Expect(enabled.String()).To(ContainSubstring(`data-verified="off/lamp"`))
		Expect(enabled.String()).To(ContainSubstring(`title="turns lamp off"`))
		Expect(enabled.String()).To(ContainSubstring(`data-gotth-on="click:panel.press`))
		Expect(enabled.String()).NotTo(ContainSubstring("disabled"))
		Expect(refused.String()).To(ContainSubstring(" disabled>Off</button>"))
		Expect(refused.String()).To(ContainSubstring(`<small class="refusal" data-verified-refusal="off/gone">no such switch</small>`))
		Expect(refused.String()).NotTo(ContainSubstring("data-gotth-on"))
	})
})
