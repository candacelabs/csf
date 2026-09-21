package notify

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LogNotifier", func() {
	// TestLogNotifierNotify
	It("returns nil for dead and recovery incidents", func() {
		n := NewLogNotifier()
		Expect(n.Notify(context.Background(), deadIncident("node-a"))).To(Succeed())
		Expect(n.Notify(context.Background(), recoveryIncident("node-a"))).To(Succeed())
	})

	// TestLogNotifierConcurrent
	It("is safe under concurrent Notify calls", func() {
		n := NewLogNotifier()
		ctx := context.Background()
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				Expect(n.Notify(ctx, deadIncident("node-a"))).To(Succeed())
			}()
		}
		wg.Wait()
	})
})
