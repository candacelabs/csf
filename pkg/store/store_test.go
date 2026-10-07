package store

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Store Suite")
}

var _ = Describe("MemoryStore", func() {
	var ctx context.Context
	var store *MemoryStore[string]

	BeforeEach(func() {
		ctx = context.Background()
		store = NewMemoryStore[string]()
	})

	Describe("Load", func() {
		It("returns zero value initially", func() {
			val, err := store.Load(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(""))
		})

		It("returns saved value", func() {
			_ = store.Save(ctx, "hello")
			val, err := store.Load(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal("hello"))
		})
	})

	Describe("Save", func() {
		It("saves a value", func() {
			err := store.Save(ctx, "world")
			Expect(err).NotTo(HaveOccurred())
			val, _ := store.Load(ctx)
			Expect(val).To(Equal("world"))
		})

		It("overwrites previous value", func() {
			_ = store.Save(ctx, "first")
			_ = store.Save(ctx, "second")
			val, _ := store.Load(ctx)
			Expect(val).To(Equal("second"))
		})
	})

	Describe("Saved", func() {
		It("returns false initially", func() {
			Expect(store.Saved()).To(BeFalse())
		})

		It("returns true after Save", func() {
			_ = store.Save(ctx, "value")
			Expect(store.Saved()).To(BeTrue())
		})
	})
})
