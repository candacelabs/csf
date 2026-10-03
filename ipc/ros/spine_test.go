// Copyright 2026 Candace Labs

package ros_test

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/candacelabs/csf/ipc/ros"
	brainspinev1 "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

// Integration specs: only the exported API, with ISpine's generated mock
// standing in for a ROS transport.
var _ = Describe("DisconnectedSpine", func() {
	var spine *ros.DisconnectedSpine

	BeforeEach(func() { spine = ros.NewDisconnectedSpine() })

	It("satisfies ISpine as a value a consumer can hold", func() {
		var granted ros.ISpine = spine
		Expect(granted).NotTo(BeNil())
	})

	It("answers a submitted action with a typed not-connected error", func() {
		err := spine.Submit(context.Background(), &brainspinev1.Action{Steering: 1})
		var failure *ros.NotConnectedError
		Expect(errors.As(err, &failure)).To(BeTrue())
		Expect(failure.Operation).To(Equal(ros.OperationSubmit))
		Expect(err).To(MatchError(HavePrefix(ros.NotConnectedStatus)))
	})

	It("answers an observation with no value and a typed not-connected error", func() {
		observation, err := spine.Observe(context.Background())
		Expect(observation).To(BeNil())
		var failure *ros.NotConnectedError
		Expect(errors.As(err, &failure)).To(BeTrue())
		Expect(failure.Operation).To(Equal(ros.OperationObserve))
	})

	It("treats a nil action as not connected rather than panicking", func() {
		Expect(ros.IsNotConnected(spine.Submit(context.Background(), nil))).To(BeTrue())
	})

	It("returns the context's error, not not-connected, once the context is done", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(spine.Submit(ctx, &brainspinev1.Action{})).To(MatchError(context.Canceled))
		observation, err := spine.Observe(ctx)
		Expect(observation).To(BeNil())
		Expect(err).To(MatchError(context.Canceled))
		Expect(ros.IsNotConnected(err)).To(BeFalse())
	})

	It("is safe to share between goroutines", func() {
		var group sync.WaitGroup
		failures := make(chan error, 16)
		for range 16 {
			group.Go(func() { failures <- spine.Submit(context.Background(), &brainspinev1.Action{}) })
		}
		group.Wait()
		close(failures)
		for err := range failures {
			Expect(ros.IsNotConnected(err)).To(BeTrue())
		}
	})
})

var _ = Describe("IsNotConnected", func() {
	It("recognizes a wrapped not-connected error and rejects every other error", func() {
		err := ros.NewDisconnectedSpine().Submit(context.Background(), nil)
		Expect(ros.IsNotConnected(fmt.Errorf("step: %w", err))).To(BeTrue())
		Expect(ros.IsNotConnected(context.Canceled)).To(BeFalse())
		Expect(ros.IsNotConnected(nil)).To(BeFalse())
	})
})

var _ = Describe("Status", func() {
	var spine *MockISpine

	BeforeEach(func() { spine = NewMockISpine(gomock.NewController(GinkgoT())) })

	It("renders nothing when a connected spine answers an observation", func() {
		spine.EXPECT().Observe(gomock.Any()).Return(&brainspinev1.Observation{Sequence: 3}, nil)
		Expect(ros.Status(context.Background(), spine)).To(BeEmpty())
	})

	It("renders no spine connected for a wrapped not-connected error", func() {
		spine.EXPECT().Observe(gomock.Any()).Return(nil, fmt.Errorf("transport: %w", &ros.NotConnectedError{Operation: ros.OperationObserve}))
		Expect(ros.Status(context.Background(), spine)).To(Equal(ros.NotConnectedStatus))
	})

	It("renders any other observation failure with its cause instead of hiding it", func() {
		spine.EXPECT().Observe(gomock.Any()).Return(nil, errors.New("ros master unreachable"))
		Expect(ros.Status(context.Background(), spine)).To(Equal("spine observation failed: ros master unreachable"))
	})

	It("observes exactly once and never submits", func() {
		spine.EXPECT().Observe(gomock.Any()).Return(nil, &ros.NotConnectedError{Operation: ros.OperationObserve}).Times(1)
		spine.EXPECT().Submit(gomock.Any(), gomock.Any()).Times(0)
		Expect(ros.Status(context.Background(), spine)).To(Equal(ros.NotConnectedStatus))
	})

	It("renders no spine connected for a nil spine and the stub", func() {
		Expect(ros.Status(context.Background(), nil)).To(Equal(ros.NotConnectedStatus))
		Expect(ros.Status(context.Background(), ros.NewDisconnectedSpine())).To(Equal(ros.NotConnectedStatus))
	})

	It("renders a done context as a failure, not as a missing spine", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(ros.Status(ctx, ros.NewDisconnectedSpine())).To(Equal("spine observation failed: context canceled"))
	})
})
