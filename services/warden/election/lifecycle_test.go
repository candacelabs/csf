package election

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/store"
	"github.com/candacelabs/csf/services/warden/testclock"
)

// newUnstartedManager builds a Manager without calling Run, so its loop is not
// consuming events. Useful for exercising the RPC-handler cancellation paths.
func newUnstartedManager(t iHarnessT) *Manager {
	t.Helper()
	clk := testclock.New(time.Unix(0, 0))
	cfg := Config{
		Self:  warden.Node{ID: "a", Addr: "a"},
		Peers: []warden.Node{{ID: "a", Addr: "a"}, {ID: "b", Addr: "b"}, {ID: "c", Addr: "c"}},
	}
	m, err := NewManager(cfg, stubTransport(), store.NewMemStore(), clk)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

var _ = Describe("manager lifecycle", func() {
	// TestHandlerReturnsOnContextCancel: a HandleVote whose reply never comes
	// (loop not running) unblocks when the caller's context is canceled, rather
	// than leaking the caller goroutine.
	It("returns from a handler when the caller's context is canceled", func() {
		m := newUnstartedManager(GinkgoT())

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan warden.VoteResponse, 1)
		go func() {
			defer GinkgoRecover()
			done <- m.HandleVote(ctx, warden.VoteRequest{Term: 1, CandidateID: "b"})
		}()

		cancel()

		select {
		case resp := <-done:
			Expect(resp.Granted).To(BeFalse(), "canceled handler should not report a granted vote: %+v", resp)
		case <-time.After(2 * time.Second):
			Fail("HandleVote did not return after context cancellation")
		}
	})

	// TestHandlerReturnsOnShutdown: a HandleHeartbeat in flight while the
	// Manager shuts down returns (via the done channel) instead of hanging.
	It("returns all in-flight handlers when the manager shuts down", func() {
		clk := testclock.New(time.Unix(0, 0))
		cfg := Config{
			Self:  warden.Node{ID: "a", Addr: "a"},
			Peers: []warden.Node{{ID: "a", Addr: "a"}, {ID: "b", Addr: "b"}, {ID: "c", Addr: "c"}},
		}
		m, err := NewManager(cfg, stubTransport(), store.NewMemStore(), clk)
		Expect(err).NotTo(HaveOccurred(), "NewManager")
		ctx, cancel := context.WithCancel(context.Background())
		runDone := make(chan struct{})
		go func() { _ = m.Run(ctx); close(runDone) }()

		// Fire many concurrent handlers, then shut the Manager down. Every one
		// must return.
		const n = 32
		results := make(chan struct{}, n)
		for i := 0; i < n; i++ {
			go func() {
				defer GinkgoRecover()
				m.HandleHeartbeat(context.Background(), warden.HeartbeatRequest{Term: 1, LeaderID: "b"})
				m.HandleVote(context.Background(), warden.VoteRequest{Term: 1, CandidateID: "b"})
				results <- struct{}{}
			}()
		}

		cancel()

		for i := 0; i < n; i++ {
			select {
			case <-results:
			case <-time.After(3 * time.Second):
				Fail("handler goroutine hung during shutdown")
			}
		}
		select {
		case <-runDone:
		case <-time.After(3 * time.Second):
			Fail("Run did not return after cancellation")
		}
	})

	// TestNoGoroutineLeakAfterShutdown: start a cluster, run a few elections and
	// failovers, stop it, and assert that no worker goroutines survive and the
	// process goroutine count returns to its pre-cluster baseline.
	It("leaks no goroutines or in-flight RPC workers after shutdown", func() {
		before := runtime.NumGoroutine()

		func() {
			c := newCluster(GinkgoT(), "n1", "n2", "n3", "n4", "n5")
			leader := c.electLeader()
			c.kill(leader)
			c.electLeader(c.others(leader)...)
			c.Advance(c.timings.ETMax * 2)

			// Every live Manager must have zero in-flight RPC workers once settled.
			for _, m := range c.aliveManagers() {
				Expect(m.rpc.count()).To(Equal(0), "manager %s has in-flight RPC workers while settled", m.self.ID)
			}
			c.stopAll()
			// After stop, killed and stopped alike must have zero workers.
			for _, m := range c.byID {
				Expect(m.m.rpc.count()).To(Equal(0), "manager %s has in-flight RPC workers after shutdown", m.m.self.ID)
			}
		}()

		// All Run goroutines and workers are gone; the count converges back
		// down. Allow the scheduler a bounded number of yields to reap them.
		leaked := true
		for i := 0; i < 200; i++ {
			if runtime.NumGoroutine() <= before {
				leaked = false
				break
			}
			runtime.Gosched()
		}
		Expect(leaked).To(BeFalse(), "goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
	})
})
