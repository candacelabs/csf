// Copyright 2026 Candace Labs

//go:build acceptance

package proc_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/ipc/proc"
)

// The suite crosses into the host's kernel, so it runs on the host: Docker
// masks /proc/pressure read-only inside a container. Build the test binary in
// the pinned Go image (go test -c -tags acceptance) and run it on the host;
// on 2026-10-05 it fired 1.88 s after arming, as the unprivileged user.

// acceptanceTriggerBudget is five of the trigger's 2 s windows: a host
// under any CPU pressure at all stalls 50 ms in one of them.
const acceptanceTriggerBudget = 10 * time.Second

var _ = Describe("WatchPressure on this host's kernel", Label("acceptance"), func() {
	It("arms an unprivileged CPU trigger and receives its event", func() {
		ctx, cancel := context.WithTimeout(context.Background(), acceptanceTriggerBudget)
		defer cancel()
		armed := time.Now()

		events, err := proc.WatchPressure(ctx, proc.PressureCPU,
			proc.PressureTrigger{Kind: proc.PressureSome, Stall: 50 * time.Millisecond, Window: 2 * time.Second})
		Expect(err).NotTo(HaveOccurred())
		Eventually(events, acceptanceTriggerBudget).Should(Receive())
		GinkgoWriter.Printf("CPU trigger fired %s after arming\n", time.Since(armed))
		cancel()
		Eventually(events, acceptanceTriggerBudget).Should(BeClosed())
	})
})
