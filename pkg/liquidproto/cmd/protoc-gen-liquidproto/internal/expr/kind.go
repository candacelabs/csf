package expr

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
