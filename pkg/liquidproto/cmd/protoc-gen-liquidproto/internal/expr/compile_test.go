package expr

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestCompile(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Liquid Proto expression compiler")
}

var _ = Describe("Compile", func() {
	DescribeTable("accepted predicates",
		func(kind protoreflect.Kind, source, want string) {
			program, err := Compile(source, mustType(kind), "v", "_testRe")
			Expect(err).NotTo(HaveOccurred())
			Expect(program.Expr).To(Equal(want))
		},
		Entry("bounded int", protoreflect.Int32Kind, "this >= 0 && this < 150", "v >= 0 && v < 150"),
		Entry("signed bounds", protoreflect.Sint64Kind, "this >= -1000000000 && this <= 1000000000", "v >= -1000000000 && v <= 1000000000"),
		Entry("minimum signed integer", protoreflect.Int64Kind, "this >= -9223372036854775808", "v >= -9223372036854775808"),
		Entry("enum values", protoreflect.EnumKind, "this == 1 || this == 2", "v == 1 || v == 2"),
		Entry("string length", protoreflect.StringKind, "len(this) >= 1", "len(v) >= 1"),
		Entry("bytes length", protoreflect.BytesKind, "len(this) == 32", "len(v) == 32"),
		Entry("boolean", protoreflect.BoolKind, "!this || this", "!v || v"),
	)

	It("hoists a validated regular expression", func() {
		program, err := Compile(
			"matches(this, `^[a-z]+$`)",
			mustType(protoreflect.StringKind),
			"value",
			"_subjectRe",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(program.Expr).To(Equal("_subjectRe0.MatchString(value)"))
		Expect(program.Regexps).To(HaveLen(1))
		Expect(program.Regexps[0].Pattern).To(Equal("^[a-z]+$"))
	})

	DescribeTable("unsafe or ill-typed predicates",
		func(kind protoreflect.Kind, source, want string) {
			_, err := Compile(source, mustType(kind), "v", "re")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(want))
		},
		Entry("selector injection", protoreflect.StringKind, "this.String() == `x`", "only the builtins"),
		Entry("unknown identifier", protoreflect.Int32Kind, "other > 0", "unknown identifier"),
		Entry("wrong builtin type", protoreflect.Int32Kind, "len(this) > 0", "requires a string or bytes"),
		Entry("bad regexp", protoreflect.StringKind, "matches(this, `(`)", "invalid RE2 pattern"),
		Entry("arithmetic", protoreflect.Int32Kind, "this + 1 > 2", "operator + is not part"),
		Entry("remainder", protoreflect.Uint64Kind, "this % 5 == 0", "operator % is not part"),
		Entry("division", protoreflect.Int32Kind, "100 / this > 1", "operator / is not part"),
		Entry("float literal", protoreflect.Int32Kind, "this > 1.0", "float literals are not part"),
		Entry("overflow", protoreflect.Uint32Kind, "this > 4294967296", "overflows uint32"),
		Entry("negative unsigned", protoreflect.Uint64Kind, "this > -1", "overflows uint64"),
		Entry("negative overflow", protoreflect.Int64Kind, "this > -9223372036854775809", "overflows int64"),
		Entry("negated field", protoreflect.Int64Kind, "-this > 0", "requires an integer literal"),
		Entry("negated boolean", protoreflect.BoolKind, "-this", "requires an integer literal"),
		Entry("not boolean", protoreflect.Int64Kind, "this", "must evaluate to bool"),
	)
})

func mustType(kind protoreflect.Kind) Type {
	GinkgoHelper()
	typeOf, ok := FromProtoKind(kind)
	Expect(ok).To(BeTrue(), "FromProtoKind(%s) rejected a test scalar", kind)
	return typeOf
}
