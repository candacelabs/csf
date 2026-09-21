package expr

import "google.golang.org/protobuf/reflect/protoreflect"

// Kind classifies a predicate expression's type.
type Kind int

const (
	KindInvalid Kind = iota
	KindUntypedInt
	KindBool
	KindInt
	KindUint
	KindString
	KindBytes
)

func (k Kind) untyped() bool { return k == KindUntypedInt }

func (k Kind) ordered() bool {
	return k == KindUntypedInt || k == KindInt || k == KindUint || k == KindString
}

func (k Kind) comparable() bool { return k.ordered() || k == KindBool }

// Type is a concrete or untyped type in the predicate language.
type Type struct {
	Kind Kind
	// Go is the Go spelling of a concrete type. It is empty for untyped
	// constants.
	Go string
	// Bits is the width of a concrete numeric type.
	Bits int
}

func (t Type) String() string {
	switch t.Kind {
	case KindUntypedInt:
		return "untyped int"
	case KindInvalid:
		return "invalid"
	default:
		return t.Go
	}
}

var (
	typeBool       = Type{Kind: KindBool, Go: "bool"}
	typeString     = Type{Kind: KindString, Go: "string"}
	typeBytes      = Type{Kind: KindBytes, Go: "[]byte"}
	typeInt        = Type{Kind: KindInt, Go: "int", Bits: 64}
	typeUntypedInt = Type{Kind: KindUntypedInt}
)

// FromProtoKind maps a singular protobuf scalar or enum kind to the Go type
// used to type-check its refinement predicate. Proto enums have an int32 wire
// representation and compare directly with untyped integer constants in Go.
func FromProtoKind(kind protoreflect.Kind) (Type, bool) {
	switch kind {
	case protoreflect.BoolKind:
		return typeBool, true
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return Type{Kind: KindInt, Go: "int32", Bits: 32}, true
	case protoreflect.EnumKind:
		return Type{Kind: KindInt, Go: "int32", Bits: 32}, true
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return Type{Kind: KindInt, Go: "int64", Bits: 64}, true
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return Type{Kind: KindUint, Go: "uint32", Bits: 32}, true
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return Type{Kind: KindUint, Go: "uint64", Bits: 64}, true
	case protoreflect.StringKind:
		return typeString, true
	case protoreflect.BytesKind:
		return typeBytes, true
	default:
		return Type{}, false
	}
}
