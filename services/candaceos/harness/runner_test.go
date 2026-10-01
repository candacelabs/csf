package harness_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/harness"
)

type runnerEvent struct {
	ID   string
	Type string
}

func newTestRunner(publish func(event runnerEvent)) *harness.Runner[runnerEvent] {
	return harness.NewRunner(publish, func(event runnerEvent) string { return event.ID })
}

var _ = Describe("Runner", func() {
	It("survives a host projection panic during replay and stays usable", func(ctx SpecContext) {
		received := make(chan runnerEvent, 8)
		runner := newTestRunner(func(event runnerEvent) {
			if event.Type == "poison" {
				panic("projection failed")
			}
			received <- event
		})
		DeferCleanup(runner.Close)

		Expect(runner.BeginStart()).To(Succeed())
		var sent atomic.Int64
		Expect(runner.Install(
			func(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error {
				sent.Add(1)
				return nil
			}, nil, nil,
		)).To(Succeed())
		runner.Activate([]runnerEvent{
			{ID: "poison", Type: "poison"},
			{ID: "after", Type: "replay"},
		})

		Eventually(received).Should(Receive(Equal(runnerEvent{ID: "after", Type: "replay"})))

		// A panic in the injected callback must not take the lifecycle owner
		// with it, or every later operation would block forever.
		runner.Publish(runnerEvent{ID: "poison-live", Type: "poison"})
		runner.Publish(runnerEvent{ID: "live", Type: "live"})
		Eventually(received).Should(Receive(Equal(runnerEvent{ID: "live", Type: "live"})))
		Expect(runner.Send(ctx, &candaceosv1.HarnessPrompt{
			RunId: "run-1", Content: "still accepted",
			Delivery: candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE,
		})).To(Succeed())
		Expect(sent.Load()).To(Equal(int64(1)))
	})

	It("activates replay before buffered callbacks and deduplicates nonempty IDs", func() {
		received := make(chan runnerEvent, 8)
		runner := newTestRunner(func(event runnerEvent) { received <- event })
		DeferCleanup(runner.Close)

		Expect(runner.BeginStart()).To(Succeed())
		Expect(runner.Install(nil, nil, nil)).To(Succeed())
		runner.Publish(runnerEvent{ID: "shared", Type: "buffered-duplicate"})
		runner.Publish(runnerEvent{ID: "early", Type: "buffered"})
		runner.Publish(runnerEvent{Type: "buffered-without-id"})
		runner.Activate([]runnerEvent{
			{ID: "history", Type: "replay"},
			{ID: "shared", Type: "replay-wins"},
			{Type: "replay-without-id"},
		})

		var activated []runnerEvent
		for range 5 {
			var event runnerEvent
			Eventually(received).Should(Receive(&event))
			activated = append(activated, event)
		}
		Expect(activated).To(Equal([]runnerEvent{
			{ID: "history", Type: "replay"},
			{ID: "shared", Type: "replay-wins"},
			{Type: "replay-without-id"},
			{ID: "early", Type: "buffered"},
			{Type: "buffered-without-id"},
		}))

		runner.Publish(runnerEvent{ID: "live", Type: "live"})
		runner.Activate(nil)
		Eventually(received).Should(Receive(Equal(runnerEvent{ID: "live", Type: "live"})))
		Consistently(received).ShouldNot(Receive())
	})

	It("reserves startup once without dropping accepted callbacks", func() {
		received := make(chan runnerEvent, 1)
		runner := newTestRunner(func(event runnerEvent) { received <- event })
		DeferCleanup(runner.Close)

		Expect(runner.BeginStart()).To(Succeed())
		runner.Publish(runnerEvent{ID: "early", Type: "callback"})
		Expect(runner.BeginStart()).To(MatchError(harness.ErrRunnerStarted))
		Expect(runner.Install(nil, nil, nil)).To(Succeed())
		runner.Activate(nil)

		Eventually(received).Should(Receive(Equal(runnerEvent{ID: "early", Type: "callback"})))
	})

	It("safely disables optional event hooks", func() {
		runner := harness.NewRunner[runnerEvent](nil, nil)
		Expect(runner.BeginStart()).To(Succeed())
		runner.Publish(runnerEvent{ID: "ignored"})
		runner.Activate([]runnerEvent{{ID: "also-ignored"}})
		Expect(runner.Close()).To(Succeed())
	})

	It("does not lose concurrently published live events", func() {
		const eventCount = 512
		received := make(chan runnerEvent, eventCount)
		runner := newTestRunner(func(event runnerEvent) { received <- event })
		DeferCleanup(runner.Close)

		Expect(runner.BeginStart()).To(Succeed())
		Expect(runner.Install(nil, nil, nil)).To(Succeed())
		runner.Activate(nil)

		var publishers sync.WaitGroup
		publishers.Add(eventCount)
		for index := range eventCount {
			go func() {
				defer publishers.Done()
				runner.Publish(runnerEvent{ID: fmt.Sprintf("event-%03d", index)})
			}()
		}
		publishers.Wait()
		runner.Activate(nil)

		seen := make(map[string]struct{}, eventCount)
		for range eventCount {
			var event runnerEvent
			Eventually(received).Should(Receive(&event))
			seen[event.ID] = struct{}{}
		}
		Expect(seen).To(HaveLen(eventCount))
		Consistently(received).ShouldNot(Receive())
	})

	It("receives a synchronous provider callback while Send is running", func() {
		received := make(chan runnerEvent, 1)
		runner := newTestRunner(func(event runnerEvent) { received <- event })
		DeferCleanup(runner.Close)
		Expect(runner.BeginStart()).To(Succeed())
		Expect(runner.Install(
			func(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error {
				runner.Publish(runnerEvent{ID: prompt.GetRunId(), Type: "callback"})
				return nil
			},
			nil,
			nil,
		)).To(Succeed())
		runner.Activate(nil)

		Expect(runner.Send(context.Background(), &candaceosv1.HarnessPrompt{RunId: "run-1"})).To(Succeed())
		Eventually(received).Should(Receive(Equal(runnerEvent{ID: "run-1", Type: "callback"})))
	})

	It("preserves accepted operations while close runs cleanup exactly once", func() {
		sendErr := errors.New("send result")
		abortErr := errors.New("abort result")
		closeErr := errors.New("close result")
		sendStarted := make(chan struct{})
		releaseSend := make(chan struct{})
		var releaseOnce sync.Once
		var closeCalls atomic.Int32

		runner := newTestRunner(func(event runnerEvent) {})
		DeferCleanup(func() { _ = runner.Close() })
		Expect(runner.Send(context.Background(), nil)).To(MatchError(harness.ErrRuntimeUnavailable))
		Expect(runner.Abort(context.Background())).To(MatchError(harness.ErrRuntimeUnavailable))
		Expect(runner.BeginStart()).To(Succeed())
		Expect(runner.Install(
			func(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error {
				close(sendStarted)
				<-releaseSend
				return sendErr
			},
			func(ctx context.Context) error { return abortErr },
			func() error {
				closeCalls.Add(1)
				releaseOnce.Do(func() { close(releaseSend) })
				return closeErr
			},
		)).To(Succeed())
		Expect(runner.Abort(context.Background())).To(MatchError(abortErr))

		sendResult := make(chan error, 1)
		go func() { sendResult <- runner.Send(context.Background(), nil) }()
		Eventually(sendStarted).Should(BeClosed())

		const closerCount = 16
		closeResults := make(chan error, closerCount)
		for range closerCount {
			go func() { closeResults <- runner.Close() }()
		}

		Eventually(sendResult).Should(Receive(MatchError(sendErr)))
		for range closerCount {
			Eventually(closeResults).Should(Receive(MatchError(closeErr)))
		}
		Expect(closeCalls.Load()).To(Equal(int32(1)))
		Expect(runner.Send(context.Background(), nil)).To(MatchError(harness.ErrRuntimeUnavailable))
		Expect(runner.Abort(context.Background())).To(MatchError(harness.ErrRuntimeUnavailable))
		Expect(runner.BeginStart()).To(MatchError(harness.ErrRunnerClosed))
	})

	It("rejects canceled calls before invoking provider operations", func() {
		var sendCalls atomic.Int32
		var abortCalls atomic.Int32
		runner := newTestRunner(nil)
		DeferCleanup(runner.Close)
		Expect(runner.BeginStart()).To(Succeed())
		Expect(runner.Install(
			func(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error {
				sendCalls.Add(1)
				return nil
			},
			func(ctx context.Context) error {
				abortCalls.Add(1)
				return nil
			},
			nil,
		)).To(Succeed())

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(runner.Send(ctx, nil)).To(MatchError(context.Canceled))
		Expect(runner.Abort(ctx)).To(MatchError(context.Canceled))
		Expect(sendCalls.Load()).To(BeZero())
		Expect(abortCalls.Load()).To(BeZero())
	})

	It("cancels accepted operations before waiting for close", func() {
		started := make(chan struct{})
		runner := newTestRunner(nil)
		Expect(runner.BeginStart()).To(Succeed())
		Expect(runner.Install(
			func(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			},
			nil,
			nil,
		)).To(Succeed())

		sendResult := make(chan error, 1)
		go func() { sendResult <- runner.Send(context.Background(), nil) }()
		Eventually(started).Should(BeClosed())
		closeResult := make(chan error, 1)
		go func() { closeResult <- runner.Close() }()

		Eventually(sendResult).Should(Receive(MatchError(context.Canceled)))
		Eventually(closeResult).Should(Receive(Succeed()))
	})

	It("drains accepted operations across a concurrent close race", func() {
		const operationCount = 64
		runner := newTestRunner(nil)
		Expect(runner.BeginStart()).To(Succeed())
		waitForClose := func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}
		Expect(runner.Install(
			func(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error {
				return waitForClose(ctx)
			},
			waitForClose,
			nil,
		)).To(Succeed())

		results := make(chan error, operationCount)
		for index := range operationCount {
			go func() {
				if index%2 == 0 {
					results <- runner.Send(context.Background(), nil)
					return
				}
				results <- runner.Abort(context.Background())
			}()
		}
		closeResult := make(chan error, 1)
		go func() { closeResult <- runner.Close() }()

		for range operationCount {
			var err error
			Eventually(results).Should(Receive(&err))
			Expect(errors.Is(err, context.Canceled) || errors.Is(err, harness.ErrRuntimeUnavailable)).To(BeTrue())
		}
		Eventually(closeResult).Should(Receive(Succeed()))
	})
})
