package live

import (
	"testing"
	"unsafe"
)

// comparableState is what BR-7 turns on, and it is not the same question as
// reflect.Type.Comparable: a pointer, map, slice, channel, function or
// interface is comparable in Go's sense, and == over one of them asks whether
// two handles name the same object rather than whether two states are equal.
// A reducer that mutates in place and returns the same pointer answers that
// question "yes" for a change that happened, which froze state_version and made
// P4 false on the wire.
//
// It is an in-package test because the function is unexported and its whole job
// is to be exactly right about a set of kinds. Driving it through New would
// assert the same thing at ten times the distance.
//
// The table is written as a standard-library test rather than a Ginkgo spec:
// it is a pure function over a fixed table with no behaviour in it, which is
// the case the testing conventions leave to the standard library.
func TestComparableState(t *testing.T) {
	type plain struct{ N int }
	type withPointer struct{ At *plain }
	type withSlice struct{ Xs []int }

	cases := []struct {
		name string
		got  bool
		want bool
	}{
		// The ordinary case, and the one the fast path exists for.
		{"struct of scalars", comparableState[plain](), true},
		{"scalar", comparableState[int](), true},
		{"string", comparableState[string](), true},
		{"array of scalars", comparableState[[4]int](), true},

		// Comparable in Go's sense, but by identity. These are BR-7.
		{"pointer", comparableState[*plain](), false},
		{"map", comparableState[map[string]int](), false},
		{"slice", comparableState[[]int](), false},
		{"channel", comparableState[chan int](), false},
		{"function", comparableState[func()](), false},
		{"interface", comparableState[any](), false},
		{"unsafe pointer", comparableState[unsafe.Pointer](), false},

		// Not comparable at all: == would panic, and the safe answer is that
		// the transition changed something.
		{"struct with a slice field", comparableState[withSlice](), false},

		// Deliberately true. A struct holding a pointer compares the pointer,
		// so mutating through it is invisible here too — but that is the purity
		// rule rather than a type-level property, and descending would report
		// "changed" for every transition of any state holding a *time.Location,
		// which breaks P4 in the other direction.
		{"struct with a pointer field", comparableState[withPointer](), true},
	}

	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("comparableState for a %s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}
