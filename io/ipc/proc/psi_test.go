// Copyright 2026 Candace Labs

package proc_test

import (
	"context"
	"errors"
	"testing/fstest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/ipc/proc/mocks"
)

// The text of this host's /proc/pressure/cpu, read 2026-10-05 ~07:30Z.
const cpuPressure = "some avg10=66.32 avg60=63.81 avg300=35.44 total=5303695890\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"

var oneSecondTrigger = proc.PressureTrigger{Kind: proc.PressureSome, Stall: 150 * time.Millisecond, Window: 2 * time.Second}

var _ = Describe("ReadPressure", func() {
	It("reads both lines of a resource's pressure file from the process table", func() {
		processes := fstest.MapFS{"pressure/cpu": &fstest.MapFile{Data: []byte(cpuPressure)}}

		pressure, err := proc.ReadPressure(processes, proc.PressureCPU)
		Expect(err).NotTo(HaveOccurred())
		Expect(pressure).To(Equal(proc.Pressure{
			Some: proc.PressureLine{Avg10: 66.32, Avg60: 63.81, Avg300: 35.44, Total: 5303695890 * time.Microsecond},
		}))
	})

	It("reads a file with no full line, as a kernel before 5.13 writes for CPU", func() {
		pressure, err := proc.ParsePressure("some avg10=1.00 avg60=2.00 avg300=3.00 total=4\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(pressure.Some.Avg60).To(Equal(2.0))
	})

	DescribeTable("refuses what it cannot read",
		func(processes fstest.MapFS, resource proc.PressureResource, refusal error) {
			_, err := proc.ReadPressure(processes, resource)
			Expect(err).To(MatchError(refusal))
		},
		Entry("an unknown resource", fstest.MapFS{}, proc.PressureResource("gpu"), proc.ErrUnknownPressure),
		Entry("a garbled line", fstest.MapFS{"pressure/io": &fstest.MapFile{Data: []byte("some avg10 total\n")}}, proc.PressureIO, proc.ErrPressureFormat),
		Entry("a file with no some line", fstest.MapFS{"pressure/io": &fstest.MapFile{Data: []byte("full avg10=0 avg60=0 avg300=0 total=0\n")}}, proc.PressureIO, proc.ErrPressureFormat),
	)

	It("reports an unreadable pressure file as the file error", func() {
		_, err := proc.ReadPressure(fstest.MapFS{}, proc.PressureMemory)
		Expect(err).To(HaveOccurred())
		Expect(err).NotTo(MatchError(proc.ErrPressureFormat))
	})
})

var _ = Describe("PressureTrigger", func() {
	It("is written as the kernel reads it, in microseconds", func() {
		Expect(oneSecondTrigger.String()).To(Equal("some 150000 2000000"))
	})

	DescribeTable("refuses a trigger the kernel would",
		func(trigger proc.PressureTrigger) {
			Expect(trigger.Validate()).To(MatchError(proc.ErrInvalidTrigger))
		},
		Entry("an unknown kind", proc.PressureTrigger{Kind: "most", Stall: time.Millisecond, Window: time.Second}),
		Entry("a window below 500 ms", proc.PressureTrigger{Kind: proc.PressureSome, Stall: time.Millisecond, Window: 100 * time.Millisecond}),
		Entry("a window above 10 s", proc.PressureTrigger{Kind: proc.PressureSome, Stall: time.Second, Window: 11 * time.Second}),
		Entry("a stall above its window", proc.PressureTrigger{Kind: proc.PressureFull, Stall: 3 * time.Second, Window: 2 * time.Second}),
		Entry("no stall", proc.PressureTrigger{Kind: proc.PressureFull, Window: 2 * time.Second}),
	)
})

var _ = Describe("WatchPressureSource", func() {
	var source *mocks.MockIPressureSource

	BeforeEach(func() {
		source = mocks.NewMockIPressureSource(gomock.NewController(GinkgoT()))
	})

	It("arms the trigger and delivers one value per kernel event, closing the source when the context ends", func() {
		ctx, cancel := context.WithCancel(context.Background())
		fired := make(chan struct{})
		gomock.InOrder(
			source.EXPECT().Arm("some 150000 2000000").Return(nil),
			source.EXPECT().Wait(gomock.Any()).Return(nil),
			source.EXPECT().Wait(gomock.Any()).DoAndReturn(func(ctx context.Context) error {
				close(fired)
				<-ctx.Done()
				return ctx.Err()
			}),
			source.EXPECT().Close().Return(nil),
		)

		events, err := proc.WatchPressureSource(ctx, source, oneSecondTrigger)
		Expect(err).NotTo(HaveOccurred())
		Eventually(events).Should(Receive())
		Eventually(fired).Should(BeClosed())
		cancel()
		Eventually(events).Should(BeClosed())
	})

	It("closes the stream when the pressure file fails", func() {
		gomock.InOrder(
			source.EXPECT().Arm(gomock.Any()).Return(nil),
			source.EXPECT().Wait(gomock.Any()).Return(errors.New("POLLERR")),
			source.EXPECT().Close().Return(nil),
		)

		events, err := proc.WatchPressureSource(context.Background(), source, oneSecondTrigger)
		Expect(err).NotTo(HaveOccurred())
		Eventually(events).Should(BeClosed())
	})

	It("returns the kernel's refusal of the trigger write and closes the source", func() {
		refused := errors.Join(proc.ErrInvalidTrigger, errors.New("EINVAL"))
		source.EXPECT().Arm(gomock.Any()).Return(refused)
		source.EXPECT().Close().Return(nil)

		_, err := proc.WatchPressureSource(context.Background(), source, oneSecondTrigger)
		Expect(err).To(MatchError(proc.ErrInvalidTrigger))
	})

	It("refuses an invalid trigger without arming it", func() {
		source.EXPECT().Close().Return(nil)

		_, err := proc.WatchPressureSource(context.Background(), source, proc.PressureTrigger{Kind: proc.PressureSome})
		Expect(err).To(MatchError(proc.ErrInvalidTrigger))
	})

	It("refuses an unknown resource before opening anything", func() {
		_, err := proc.WatchPressure(context.Background(), "gpu", oneSecondTrigger)
		Expect(err).To(MatchError(proc.ErrUnknownPressure))
	})
})
