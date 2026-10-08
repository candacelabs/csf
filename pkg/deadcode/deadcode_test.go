// Copyright 2026 Candace Labs

package deadcode_test

import (
	"go/token"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/deadcode"
)

var _ = Describe("Function", func() {
	DescribeTable("is removable only when no other package could ever call it",
		func(function deadcode.Function, removable bool) {
			Expect(function.Removable()).To(Equal(removable))
		},
		Entry("an unexported function of a library", deadcode.Function{Name: "helper"}, true),
		Entry("exported API of a library", deadcode.Function{Name: "WithOption", Exported: true}, false),
		Entry("anything in a main package", deadcode.Function{Name: "Run", Exported: true, InMain: true}, true),
		Entry("anything in a test file", deadcode.Function{Name: "StubFactory", Exported: true, InTest: true}, true),
		Entry("a test double's method an interface needs", deadcode.Function{Name: "stub.Subject", InTest: true, Satisfies: true}, false),
	)

	It("protects the remover's own package and its external tests, and nothing else", func() {
		Expect(deadcode.Function{Package: "github.com/candacelabs/csf/pkg/deadcode"}.Protected()).To(BeTrue())
		Expect(deadcode.Function{Package: "github.com/candacelabs/csf/pkg/deadcode_test"}.Protected()).To(BeTrue())
		Expect(deadcode.Function{Package: "github.com/candacelabs/csf/pkg/deadcodex"}.Protected()).To(BeFalse())
	})

	It("refuses to remove its own source, before reading any file", func() {
		own := deadcode.Function{Package: "github.com/candacelabs/csf/pkg/deadcode", Name: "Find",
			Position: token.Position{Filename: "deadcode.go", Line: 1, Column: 1}}
		rewritten, err := deadcode.Remove([]deadcode.Function{own})
		Expect(err).To(MatchError(deadcode.ErrProtected))
		Expect(rewritten).To(BeNil())
	})

	It("prints as path:line:column: name", func() {
		function := deadcode.Function{Position: token.Position{Filename: "a/b.go", Line: 3, Column: 6}, Name: "T.f"}
		Expect(function.String()).To(Equal("a/b.go:3:6: T.f"))
	})
})
