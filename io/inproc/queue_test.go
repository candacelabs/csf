// Copyright 2026 Candace Labs

package inproc_test

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/inproc"
	"github.com/candacelabs/csf/pkg/eventually"
)

// popBudget bounds a spec's wait for a consumer to see what a producer did.
var popBudget = eventually.Budget{Within: 5 * time.Second}

var _ = Describe("Queue", func() {
	var (
		ctx   context.Context
		queue *inproc.Queue[int]
	)

	BeforeEach(func() {
		ctx = context.Background()
		queue = inproc.NewQueue[int]()
	})

	It("delivers items in push order and reports its length", func() {
		Expect(queue.Push(1)).To(Succeed())
		Expect(queue.Push(2)).To(Succeed())
		Expect(queue.Len()).To(Equal(2))
		first, err := queue.Pop(ctx)
		Expect(err).NotTo(HaveOccurred())
		second, err := queue.Pop(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect([]int{first, second}).To(Equal([]int{1, 2}))
		Expect(queue.Len()).To(BeZero())
	})

	It("blocks an empty pop until a push arrives", func() {
		popped := make(chan int, 1)
		var consumer sync.WaitGroup
		consumer.Add(1)
		go func() {
			defer consumer.Done()
			item, err := queue.Pop(ctx)
			if err == nil {
				popped <- item
			}
		}()
		Consistently(popped, 50*time.Millisecond).ShouldNot(Receive())
		Expect(queue.Push(7)).To(Succeed())
		Eventually(popped, popBudget.Within).Should(Receive(Equal(7)))
		consumer.Wait()
	})

	It("returns the context's cause when the context ends first", func() {
		bounded, cancel := context.WithCancelCause(ctx)
		cause := errors.New("spec gave up")
		cancel(cause)
		_, err := queue.Pop(bounded)
		Expect(err).To(MatchError(cause))
	})

	It("drains queued items after Close, then reports closed", func() {
		Expect(queue.Push(1)).To(Succeed())
		Expect(queue.Push(2)).To(Succeed())
		queue.Close()
		queue.Close()
		Expect(queue.Push(3)).To(MatchError(inproc.ErrQueueClosed))
		first, err := queue.Pop(ctx)
		Expect(err).NotTo(HaveOccurred())
		second, err := queue.Pop(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect([]int{first, second}).To(Equal([]int{1, 2}))
		_, err = queue.Pop(ctx)
		Expect(err).To(MatchError(inproc.ErrQueueClosed))
	})

	It("wakes a consumer blocked on an empty queue when it closes", func() {
		result := make(chan error, 1)
		var consumer sync.WaitGroup
		consumer.Add(1)
		go func() {
			defer consumer.Done()
			_, err := queue.Pop(ctx)
			result <- err
		}()
		Consistently(result, 50*time.Millisecond).ShouldNot(Receive())
		queue.Close()
		Eventually(result, popBudget.Within).Should(Receive(MatchError(inproc.ErrQueueClosed)))
		consumer.Wait()
	})

	It("hands every item to exactly one of several consumers", func() {
		const items, consumers = 200, 4
		seen := make(chan int, items)
		var group sync.WaitGroup
		for range consumers {
			group.Add(1)
			go func() {
				defer group.Done()
				for {
					item, err := queue.Pop(ctx)
					if err != nil {
						return
					}
					seen <- item
				}
			}()
		}
		for item := range items {
			Expect(queue.Push(item)).To(Succeed())
		}
		queue.Close()
		group.Wait()
		close(seen)
		counts := map[int]int{}
		for item := range seen {
			counts[item]++
		}
		Expect(counts).To(HaveLen(items))
		for item, count := range counts {
			Expect(count).To(Equal(1), "item %d delivered once", item)
		}
	})
})
