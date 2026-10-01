package widget_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/pkg/widget/internal/ir"
)

var _ = Describe("The fieldStation palette", func() {
	It("maps every one of the seven token names, which is what makes a document portable", func() {
		palette := widget.FieldStation()

		Expect(widget.TokenNames()).To(HaveLen(7))
		for _, name := range widget.TokenNames() {
			value, mapped := palette.Value(widget.Token(name))
			Expect(mapped).To(BeTrue(), "the palette maps %s", name)
			Expect(value).ToNot(BeEmpty())
		}
	})

	It("resolves the name a document's palette directive writes", func() {
		palette, known := widget.PaletteByName("fieldStation")

		Expect(known).To(BeTrue())
		Expect(palette.Name()).To(Equal(widget.FieldStationPalette))
		Expect(palette).To(Equal(widget.FieldStation()))
	})

	// The interpreter refuses a `palette` directive naming anything outside
	// ir.PaletteNames (W208), and this is the other half of that claim: a name
	// the interpreter accepts is a palette the SDK can actually resolve. A
	// palette named in one place and shipped in the other is a document that
	// validates and then panics at the host's first render.
	It("resolves every name the interpreter accepts", func() {
		Expect(ir.PaletteNames()).ToNot(BeEmpty())
		for _, name := range ir.PaletteNames() {
			palette, known := widget.PaletteByName(name)
			Expect(known).To(BeTrue(), "the interpreter accepts %s, so the SDK ships it", name)
			Expect(palette.Name()).To(Equal(name))
		}
	})

	It("refuses an unknown palette rather than falling back to a plausible one", func() {
		palette, known := widget.PaletteByName("fieldstation")

		Expect(known).To(BeFalse())
		Expect(palette.Name()).To(BeEmpty())
	})

	It("reports an unmapped token rather than resolving it to nothing", func() {
		value, mapped := widget.Palette{}.Value(widget.TokenAccent)

		Expect(mapped).To(BeFalse())
		Expect(value).To(BeEmpty())
	})
})

var _ = Describe("The widget stylesheet", func() {
	stylesheet := widget.Stylesheet(widget.FieldStation())

	It("declares one custom property per token, in palette order", func() {
		properties := []string{}
		for _, line := range strings.Split(stylesheet, "\n") {
			if name, _, isDeclaration := strings.Cut(strings.TrimSpace(line), ":"); isDeclaration &&
				strings.HasPrefix(name, "--widget-") {
				properties = append(properties, strings.TrimPrefix(name, "--widget-"))
			}
		}

		Expect(properties).To(Equal(widget.TokenNames()))
	})

	It("gives every token a class, so markup names a token and never a colour", func() {
		for _, name := range widget.TokenNames() {
			value, _ := widget.FieldStation().Value(widget.Token(name))
			Expect(stylesheet).To(ContainSubstring(".widget-token-" + name + " {"))
			Expect(stylesheet).To(ContainSubstring("var(--widget-" + name + ")"))
			Expect(stylesheet).To(ContainSubstring(value))
		}
	})

	It("writes every colour it has in the palette's own block and nowhere else", func() {
		root, rest, split := strings.Cut(stylesheet, "}")

		Expect(split).To(BeTrue())
		Expect(strings.Count(root, "#")).To(Equal(len(widget.TokenNames())))
		Expect(rest).ToNot(ContainSubstring("#"),
			"a colour outside the palette block is a colour a palette cannot change")
	})

	It("carries the motion invariants a host stylesheet could otherwise forget", func() {
		Expect(stylesheet).To(ContainSubstring(`@media (prefers-reduced-motion: reduce)`))
		Expect(stylesheet).To(ContainSubstring(`html[data-gotth-status="live"] .widget[data-motion="true"]`))
	})

	It("animates nothing outside the gate", func() {
		selector := ""
		armed := 0
		for _, line := range strings.Split(stylesheet, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasSuffix(trimmed, "{") {
				selector = trimmed
			}
			if !strings.HasPrefix(trimmed, "animation:") || strings.HasPrefix(trimmed, "animation: none") {
				continue
			}
			armed++
			By(selector)
			Expect(selector).To(HavePrefix(`html[data-gotth-status="live"] .widget[data-motion="true"]`))
		}

		Expect(armed).To(Equal(3), "one rule each for a forward pulse, a reverse pulse and an emphasis")
	})
})
