// Copyright 2026 Candace Labs

package collections_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/collections"
)

func TestCollections(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/collections suite")
}

var _ = Describe("Set", func() {
	It("removes a present value", func() {
		set := collections.Set[int]{1, 2, 3}
		Expect(set.Toggle(2)).To(Equal(collections.Set[int]{1, 3}))
	})

	It("adds an absent value", func() {
		set := collections.Set[int]{1, 2}
		Expect(set.Toggle(3)).To(Equal(collections.Set[int]{1, 2, 3}))
	})

	It("leaves the receiver unchanged", func() {
		set := collections.Set[int]{1, 2}
		_ = set.Toggle(2)
		Expect(set).To(Equal(collections.Set[int]{1, 2}))
	})

	It("removes a present value and reports it", func() {
		set := collections.Set[int]{1, 2, 3}
		next, removed := set.Remove(2)
		Expect(removed).To(BeTrue())
		Expect(next).To(Equal(collections.Set[int]{1, 3}))
	})

	It("leaves an absent value and reports it absent", func() {
		set := collections.Set[int]{1, 2}
		next, removed := set.Remove(3)
		Expect(removed).To(BeFalse())
		Expect(next).To(Equal(collections.Set[int]{1, 2}))
	})
})

var _ = Describe("KeyedList", func() {
	type row struct{ seq int }

	list := func() collections.KeyedList[int, row] {
		return collections.NewKeyedList(
			func(each row) int { return each.seq },
			[]row{{seq: 1}, {seq: 2}, {seq: 3}},
		)
	}

	It("returns a present key's value", func() {
		got, ok := list().Get(2)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(row{seq: 2}))
	})

	It("returns zero and false for an absent key", func() {
		got, ok := list().Get(4)
		Expect(ok).To(BeFalse())
		Expect(got).To(Equal(row{}))
	})
})
