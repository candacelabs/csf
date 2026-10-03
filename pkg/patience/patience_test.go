// Copyright 2026 Candace Labs

package patience_test

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/patience"
)

func TestPatience(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "patience Suite")
}

var _ = Describe("Await", func() {
	It("receives immediately when the channel is ready", func() {
		ch := make(chan string, 1)
		ch <- "hello"
		ctx := context.Background()
		val, err := patience.Await(ctx, ch, "should receive")
		Expect(err).NotTo(HaveOccurred())
		Expect(val).To(Equal("hello"))
	})

	It("returns an error when the context times out", func() {
		ch := make(chan string)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		_, err := patience.Await(ctx, ch, "timeout test")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("timeout test"))
	})

	It("returns an error when the context is canceled", func() {
		ch := make(chan string)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := patience.Await(ctx, ch, "canceled test")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("canceled test"))
	})

	It("returns the zero value on error", func() {
		ch := make(chan int)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		val, _ := patience.Await(ctx, ch, "")
		Expect(val).To(Equal(0))
	})
})

var _ = Describe("AwaitWithTimeout", func() {
	It("receives immediately when the channel is ready", func() {
		ch := make(chan string, 1)
		ch <- "hello"
		ctx := context.Background()
		val, err := patience.AwaitWithTimeout(ctx, ch, 100*time.Millisecond, "should receive")
		Expect(err).NotTo(HaveOccurred())
		Expect(val).To(Equal("hello"))
	})

	It("returns an error when the internal timeout fires", func() {
		ch := make(chan string)
		ctx := context.Background()
		_, err := patience.AwaitWithTimeout(ctx, ch, 10*time.Millisecond, "internal timeout")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("internal timeout"))
	})

	It("respects the context deadline if it is shorter", func() {
		ch := make(chan string)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		_, err := patience.AwaitWithTimeout(ctx, ch, 1*time.Second, "context shorter")
		Expect(err).To(HaveOccurred())
	})
})
