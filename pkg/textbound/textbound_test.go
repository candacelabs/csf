// Copyright 2026 Candace Labs

package textbound_test

import (
	"testing"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/textbound"
)

func TestTextbound(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/textbound Suite")
}

var _ = Describe("Prefix", func() {
	It("leaves text within the bound alone", func() {
		Expect(textbound.Prefix("short", 10)).To(Equal("short"))
	})

	It("cuts on a character boundary, never inside one", func() {
		// "é" is two bytes: a cut at 2 would land inside it.
		cut := textbound.Prefix("aé tail", 2)
		Expect(cut).To(Equal("a…"))
		Expect(utf8.ValidString(cut)).To(BeTrue())
	})

	It("replaces invalid UTF-8 already in the text", func() {
		Expect(utf8.ValidString(textbound.Prefix("bad \xff byte", 100))).To(BeTrue())
	})

	It("cuts everything for a bound below one", func() {
		Expect(textbound.Prefix("text", 0)).To(Equal("…"))
		Expect(textbound.Prefix("text", -3)).To(Equal("…"))
	})
})
