package session_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
)

var _ = Describe("Resync", func() {
	var (
		app *testApp
		h   *harness
	)

	BeforeEach(func() {
		app = newTestApp()
		h = newHarness(app, session.DefaultLimits())
		h.start()
		DeferCleanup(h.stop)
	})

	It("answers a request that describes no gap with an acknowledgement, not a snapshot", func() {
		h.sendResync(1)

		Eventually(func() int {
			n := 0
			for _, f := range h.sink.all() {
				if f.GetAck() != nil {
					n++
				}
			}
			return n
		}).Should(Equal(1))
		Expect(h.sink.snapshots()).To(HaveLen(1), "a spurious resync cost a full re-render")
	})

	It("re-renders every fragment and names the range it supersedes", func() {
		for i := 0; i < 3; i++ {
			h.sendEvent("counter.increment")
		}
		Eventually(h.sink.patches).Should(HaveLen(3))

		h.sendResync(1)

		Eventually(h.sink.snapshots).Should(HaveLen(2))
		s := h.sink.snapshots()[1]

		Expect(s.GetOrigin().GetKind()).To(Equal(pb.OriginKind_RESYNC))
		Expect(s.GetOrigin().GetSource()).To(Equal(protocol.SourceResync))
		Expect(s.GetOrigin().GetEventId()).NotTo(BeZero(),
			"a resync snapshot lost the identifier of the request that caused it")
		Expect(s.GetSupersededFromSeq()).To(Equal(uint64(2)))
		Expect(s.GetSupersededThroughSeq()).To(Equal(uint64(4)))
		Expect(s.GetServerSeq()).To(Equal(uint64(5)))
		Expect(s.GetUpdates()).To(HaveLen(1))
		Expect(s.GetUpdates()[0].GetHtml()).To(Equal("<b>hits 3</b>"),
			"the snapshot did not come from current state")
	})

	// Exit criterion E13. Fifty requests a second from one authenticated
	// client must not become fifty full-state renders: a resync is the only
	// operation whose cost a client can trigger directly, so an unbudgeted one
	// is a self-service denial of service.
	It("bounds the amplification a client can cause", func() {
		lim := session.DefaultLimits()
		lim.MinResyncInterval = time.Second
		lim.ResyncBurst = 3

		amp := newTestApp()
		ah := newHarness(amp, lim)
		ah.start()
		defer ah.stop()

		// Give the session something to be behind on, so every request
		// describes a real gap and none takes the free short circuit.
		for i := 0; i < 3; i++ {
			ah.sendEvent("counter.increment")
		}
		Eventually(ah.sink.patches).Should(HaveLen(3))

		for i := 0; i < 50; i++ {
			ah.sendResync(1)
		}

		Consistently(func() int { return len(ah.sink.snapshots()) }, 300*time.Millisecond).
			Should(BeNumerically("<=", 1+lim.ResyncBurst),
				"fifty requests in one second produced more than the burst in full re-renders")

		Eventually(ah.sink.errors).ShouldNot(BeEmpty())
		Expect(ah.sink.errors()[0].GetCode()).To(Equal(pb.ErrorCode_RATE_LIMITED))
	})

	// BR-6. The arm the spec above cannot reach, because it counts renders and
	// this path performs none. H-14's first clause is about the FRAME KIND
	// having a budget, and the no-op short circuit used to return before the
	// bucket was consulted — so a request describing no gap was charged to no
	// bucket at all, this one or the event one, while still minting an event
	// identifier, running the application's Authorize hook on the read pump,
	// occupying a mailbox slot and producing an outbound Ack.
	It("charges a request that describes no gap to the same budget", func() {
		lim := session.DefaultLimits()
		lim.MinResyncInterval = time.Second
		lim.ResyncBurst = 3

		noop := newTestApp()
		nh := newHarness(noop, lim)
		nh.start()
		defer nh.stop()

		// Every one of these takes the free short circuit: the mount left
		// server_seq at 1 and the request says it has applied 1.
		for i := 0; i < 40; i++ {
			nh.sendResync(1)
		}

		answered := func() int {
			n := 0
			for _, f := range nh.sink.all() {
				if f.GetAck() != nil {
					n++
				}
			}
			return n
		}

		Eventually(nh.sink.errors).ShouldNot(BeEmpty(),
			"forty resync requests in one second were all answered and none was refused")
		Expect(nh.sink.errors()[0].GetCode()).To(Equal(pb.ErrorCode_RATE_LIMITED))
		Consistently(answered, 200*time.Millisecond).Should(BeNumerically("<=", lim.ResyncBurst),
			"a whole frame kind was answered off-budget")
		Expect(nh.metrics.Total("gotthlive_resync_requests_total")).To(BeNumerically(">", 0))
	})

	It("refills the budget as time passes, so a legitimate client is not locked out", func() {
		lim := session.DefaultLimits()
		lim.MinResyncInterval = time.Second
		lim.ResyncBurst = 1

		slow := newTestApp()
		sh := newHarness(slow, lim)
		sh.start()
		defer sh.stop()

		sh.sendEvent("counter.increment")
		Eventually(sh.sink.patches).Should(HaveLen(1))

		sh.sendResync(1)
		Eventually(sh.sink.snapshots).Should(HaveLen(2))

		sh.sendEvent("counter.increment")
		Eventually(sh.sink.patches).Should(HaveLen(2))

		sh.clock.advance(2 * time.Second)
		sh.sendResync(1)

		Eventually(sh.sink.snapshots).Should(HaveLen(3))
	})

	It("closes a connection that keeps asking after being refused", func() {
		lim := session.DefaultLimits()
		lim.ResyncBurst = 1

		flood := newTestApp()
		fh := newHarness(flood, lim)
		fh.start()
		defer fh.stop()

		fh.sendEvent("counter.increment")
		Eventually(fh.sink.patches).Should(HaveLen(1))

		for i := 0; i < 20; i++ {
			fh.sendResync(1)
		}

		Eventually(fh.closeRecords).Should(ContainElement(HaveField("Code", protocol.CloseRateLimited)))
	})

	// BR-9. P7 says the supersession ranges are contiguous and non-overlapping
	// per session, and the lower bound used to be taken straight from
	// ResyncRequest.last_applied_seq — untrusted input that nothing requires to
	// be non-decreasing, and which H-8's seen_server_seq check never sees
	// because a resync routes past transition. A client that always says 1
	// produced [2, S1], [2, S2], … and the property failed through no server
	// fault. The shipped client now closes 4002 on exactly that overlap.
	Describe("the range it says it superseded", func() {
		// The interleaving that reaches production: a client with a latched gap
		// asks, is refused, and re-arms, so the SAME cursor is answered twice
		// with a real snapshot in between — and the retry outruns the
		// acknowledgement of the first answer. That last part is what the
		// acknowledgement floor alone cannot see, and it is the case the
		// shipped client punishes: it enforces H-13's range clauses in
		// applied() and closes 4002 on an overlap.
		It("does not overlap a previous range when the same cursor is answered twice", func() {
			lim := session.DefaultLimits()
			lim.MinResyncInterval = time.Second
			lim.ResyncBurst = 3

			twice := newTestApp()
			th := newHarness(twice, lim)
			th.start()
			defer th.stop()

			for i := 0; i < 3; i++ {
				th.sendEvent("counter.increment")
			}
			Eventually(th.sink.patches).Should(HaveLen(3))

			// The client's cursor is stuck at 1 — it never acknowledged
			// anything, which is exactly what a client with a latched gap and a
			// re-armed retry looks like.
			th.sendResync(1)
			Eventually(th.sink.snapshots).Should(HaveLen(2))

			th.sendEvent("counter.increment")
			Eventually(th.sink.patches).Should(HaveLen(4))

			th.sendResync(1)
			Eventually(th.sink.snapshots).Should(HaveLen(3))

			// P7 over the whole session: no range reaches back into one already
			// superseded, and none reaches the snapshot that superseded it.
			var superseded uint64
			for i, s := range th.sink.snapshots() {
				from, through := s.GetSupersededFromSeq(), s.GetSupersededThroughSeq()
				if from == 0 {
					// The mount snapshot supersedes nothing, and is itself the
					// first sequence a later range may not reach back past.
					superseded = s.GetServerSeq()
					continue
				}
				Expect(from).To(BeNumerically(">", superseded),
					"snapshot %d superseded [%d, %d], which reaches back past sequence %d, already replaced",
					i, from, through, superseded)
				Expect(from).To(BeNumerically("<=", through))
				Expect(through).To(BeNumerically("<", s.GetServerSeq()))
				superseded = s.GetServerSeq()
			}
			Expect(th.closeRecords()).To(BeEmpty())
		})

		It("clamps a request claiming to have applied less than it acknowledged", func() {
			for i := 0; i < 3; i++ {
				h.sendEvent("counter.increment")
			}
			Eventually(h.sink.patches).Should(HaveLen(3))
			h.sendAck(3)
			// The acknowledgement has to have been applied before the request is
			// judged against it; both reach the actor on different channels.
			Eventually(func() int { return len(h.logs.provenance()) }).Should(BeNumerically(">=", 4))

			h.sendResync(1)

			Eventually(h.sink.snapshots).Should(HaveLen(2))
			Expect(h.sink.snapshots()[1].GetSupersededFromSeq()).To(Equal(uint64(4)),
				"the range began below the client's own acknowledged high-water mark")
			Eventually(h.logs.warnings).Should(ContainElement(
				ContainSubstring("claimed to have applied less than it had already acknowledged")))
		})

		It("leaves an honest request's range exactly where the client put it", func() {
			for i := 0; i < 3; i++ {
				h.sendEvent("counter.increment")
			}
			Eventually(h.sink.patches).Should(HaveLen(3))

			h.sendResync(2)

			Eventually(h.sink.snapshots).Should(HaveLen(2))
			Expect(h.sink.snapshots()[1].GetSupersededFromSeq()).To(Equal(uint64(3)))
			Expect(h.logs.warnings()).NotTo(ContainElement(
				ContainSubstring("claimed to have applied less")))
		})

		// The clamp decides which requests are answered at all, and not only
		// where a range begins — because it is applied BEFORE the no-op short
		// circuit, and has to be: clamping only the lower bound while judging
		// the gap on the raw claim emits from > through, which H-13 forbids and
		// validateSnapshot refuses.
		//
		// Every other spec in this Describe leaves the client at least one
		// sequence behind the server, so the clamped value never reaches
		// server_seq and this arm is never taken. That is exactly the gap this
		// covers, and it is not hypothetical: it is the arm the dashboard
		// example's resync measurement ran into, three modules away, after
		// BR-9 landed green here.
		It("answers an understated request with an acknowledgement once its own snapshot is the session's high-water mark", func() {
			for i := 0; i < 3; i++ {
				h.sendEvent("counter.increment")
			}
			Eventually(h.sink.patches).Should(HaveLen(3))

			h.sendResync(1)
			Eventually(h.sink.snapshots).Should(HaveLen(2))

			// Nothing has happened since, so server_seq IS the sequence of that
			// snapshot, and the lastSnapshotSeq floor is what sees it: a client
			// repeating the stale cursor is not describing a gap this server can
			// still fill, it is describing one the snapshot in flight already
			// filled. Against the acknowledgement floor alone the answer would
			// be a second snapshot superseding [2, 5] — a range covering the
			// first snapshot's own sequence as well as the patches it already
			// replaced, which is P7's non-overlap failing on the wire and which
			// the shipped client closes 4002 on.
			h.sendResync(1)

			acks := func() int {
				n := 0
				for _, f := range h.sink.all() {
					if f.GetAck() != nil {
						n++
					}
				}
				return n
			}
			Eventually(acks).Should(Equal(1))
			Consistently(h.sink.snapshots).Should(HaveLen(2),
				"a request the server had already answered was answered again, with a range "+
					"reaching back over the answer")
		})
	})

	It("draws on a budget independent of the event bucket", func() {
		lim := session.DefaultLimits()
		lim.MaxEventsPerSecond = 1
		lim.EventBurst = 1

		tight := newTestApp()
		th := newHarness(tight, lim)
		th.start()
		defer th.stop()

		// Spend the whole event budget, stopping short of the run of refusals
		// that would close the connection: the point is the budget, not the
		// flood response, which has its own spec.
		for i := 0; i < 3; i++ {
			th.sendEvent("counter.increment")
		}
		Eventually(th.sink.patches).Should(HaveLen(1))
		Eventually(th.sink.errors).ShouldNot(BeEmpty())

		// A resync still gets through, because it never drew on that bucket.
		th.sendResync(1)

		Eventually(th.sink.snapshots).Should(HaveLen(2))
		Expect(th.closeRecords()).To(BeEmpty())
	})
})
