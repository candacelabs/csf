// Copyright 2026 Candace Labs

package inproc_test

import (
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/inproc"
)

var _ = Describe("Recent", func() {
	It("refuses a capacity that holds nothing", func() {
		_, err := inproc.NewRecent[string, int](0)
		Expect(err).To(MatchError(inproc.ErrNoCapacity))
	})

	It("returns what was stored and reports what was not", func() {
		recent, err := inproc.NewRecent[string, int](2)
		Expect(err).NotTo(HaveOccurred())
		recent.Store("a", 1)
		value, found := recent.Load("a")
		Expect(found).To(BeTrue())
		Expect(value).To(Equal(1))
		_, found = recent.Load("b")
		Expect(found).To(BeFalse())
	})

	It("forgets the key stored longest ago once full, counting a re-store as recent", func() {
		recent, err := inproc.NewRecent[string, int](2)
		Expect(err).NotTo(HaveOccurred())
		recent.Store("a", 1)
		recent.Store("b", 2)
		recent.Store("a", 3)
		recent.Store("c", 4)
		_, found := recent.Load("b")
		Expect(found).To(BeFalse(), "b was stored longest ago")
		value, _ := recent.Load("a")
		Expect(value).To(Equal(3))
		Expect(recent.Len()).To(Equal(2))
	})

	It("is safe for concurrent stores and loads", func() {
		recent, err := inproc.NewRecent[string, int](8)
		Expect(err).NotTo(HaveOccurred())
		var group sync.WaitGroup
		for writer := range 4 {
			group.Add(1)
			go func() {
				defer group.Done()
				for index := range 100 {
					recent.Store(fmt.Sprint(writer, index%10), index)
					recent.Load(fmt.Sprint(writer, index%10))
				}
			}()
		}
		group.Wait()
		Expect(recent.Len()).To(Equal(8))
	})
})
