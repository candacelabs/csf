package chaos_test

import (
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/session"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// Checkpoint 3, re-verification. The two conditions QA-2 held checkpoint 3 on
// have landed — D-23 in 8b428390/7533a1bb and D-29 in c3a91af8 — and this file
// is what re-verifying them rather than believing them looks like.
//
// It holds two things the landed work does not already hold itself:
//
//  1. the SERVER half of D-29's closure. The re-arm is client-side and
//     client/test/resync.test.mjs holds fourteen specs for the schedule, the
//     latch and the acks. What no JS harness can answer is whether the REAL
//     server's resync bucket, its consecutive-denial counter and RFC §7.4's
//     slow-client eviction admit that schedule — whether the retry is served,
//     refused into 4008, or evicted anyway. §1 below.
//
//  2. D-30, the adversarial case D-23's closure does not cover. The three
//     fields are now checked one at a time against the protocol's ranges, and
//     thoroughly: both endpoints, sub-millisecond narrowing, uint32 wraparound,
//     the error's wording, and a reflection property that mounts every
//     configuration New accepts. What nothing checks is the three against the
//     fields they only make sense NEXT to — and one such pair reproduces D-23's
//     whole failure mode from values every one of those checks admits. §2.

// ---------------------------------------------------------------------------
// 1. D-29, re-verified: the server serves the landed retry
// ---------------------------------------------------------------------------

// D-29 was "a refused resync freezes the page for about thirty seconds and is
// rescued only by the slow-client eviction". c3a91af8 closed the client half:
// Error{RATE_LIMITED} re-arms the request, one retry at a time, equal jitter
// over a doubling bound, and a patch discarded because of the gap is still
// acknowledged at the sequence the client holds.
//
// That fix is a claim ABOUT THE SERVER as much as about the client. It says the
// server will serve the retry — that a bucket refilling one token per
// MinResyncInterval grants the request that lands in [500, 1000) ms, or the one
// after it; that two refusals per gap stay far below the
// consecutiveDenialsBeforeClose x ResyncBurst that closes 4008; and that a
// client acknowledging at the sequence it holds is no longer the silent one
// whose window fills and whose session RFC §7.4 evicts with 4009.
//
// Every one of those is a property of code in this module, and none of them is
// checked anywhere else. The spec below puts the fixed client's behaviour in
// front of the real server and measures what a user would now feel: the longest
// interval in which the applied sequence did not move.
var _ = Describe("A legitimate client refused by the resync budget, with the landed re-arm (D-29 re-verified)", func() {

	It("retries, is served on the same connection, and is never evicted", func() {
		// The grace is five seconds rather than the default thirty for the same
		// reason the pre-fix spec shortens it: the arithmetic is what is under
		// test, and a shorter grace makes the OLD failure the FASTER of the two
		// outcomes. If the fix did not work, this spec is closed with 4009 in
		// about five seconds; the pass has to survive fifteen.
		const (
			grace  = 5 * time.Second
			window = 15 * time.Second
		)
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Logger = nil
			// The DEFAULT resync budget, deliberately. c3a91af8's claim is
			// stated at the defaults — "the first retry lands in [500, 1000) ms
			// against a bucket refilling a token a second" — so the defaults
			// are what has to be in front of it. Refusals are produced by
			// driving gaps faster than the budget, not by shrinking the budget.
			cfg.Limits.MinResyncInterval = time.Second
			cfg.Limits.ResyncBurst = 3
			cfg.Limits.AckWindow = 16
			cfg.Limits.SlowClientGrace = grace
			cfg.Limits.HeartbeatInterval = time.Second
			cfg.Limits.HeartbeatTimeout = 5 * time.Minute
			cfg.Limits.IdleTimeout = 5 * time.Minute
		})

		w := dialWire(s.addr(), wireOpts{
			acks:                 ackAuto,
			resyncOnGap:          true,
			resyncRetryOnRefusal: true,
			lossPercent:          50,
		})
		w.startTicks(40*time.Millisecond, 5000, false)

		// The premise, asserted before the conclusion: a run in which the
		// budget never refused anything measures nothing about a refusal.
		Eventually(func() int64 {
			_, _, _, limited := w.counters()
			return limited
		}, 30*time.Second).Should(BeNumerically(">", 0),
			"the resync budget never refused anything, so there is no refusal to recover from and "+
				"this run says nothing about D-29")

		refusedAt := time.Now()
		appliedAtRefusal := w.appliedSeq()
		retriesAtRefusal := w.retryCount()

		// The user-visible cost, sampled: the longest interval in which the
		// applied sequence did not move. Before c3a91af8 this ran to the
		// eviction and the re-mount behind it; the question this spec exists to
		// answer is what it is now.
		var (
			longestStall time.Duration
			lastMove     = time.Now()
			lastSeq      = appliedAtRefusal
			samples      int
		)
		deadline := time.Now().Add(window)
		for time.Now().Before(deadline) {
			// CS-9 keep: a sampler, not an await. Nothing here is waiting for
			// a condition — the loop runs the whole window on purpose and the
			// interval is the sampling resolution the stall figure is reported
			// at. Stopping early at a match would be measuring something else.
			time.Sleep(25 * time.Millisecond)
			samples++
			if w.isClosed() {
				break
			}
			if got := w.appliedSeq(); got != lastSeq {
				if d := time.Since(lastMove); d > longestStall {
					longestStall = d
				}
				lastSeq = got
				lastMove = time.Now()
			}
		}
		if d := time.Since(lastMove); d > longestStall {
			longestStall = d
		}

		_, _, _, limited := w.counters()
		retries := w.retryCount() - retriesAtRefusal
		advanced := w.appliedSeq() - appliedAtRefusal

		AddReportEntry("D-29 re-verified", fmt.Sprintf(
			"defaults (MinResyncInterval 1s, ResyncBurst 3), 50%% patch loss at 25 updates/s, "+
				"slow_client_grace %s, observed %s after the first refusal: %d refusals, %d armed "+
				"retries, applied sequence advanced by %d (%d -> %d), %d resync Snapshots, longest "+
				"stall %s, closed=%v code=%d",
			grace, time.Since(refusedAt).Round(time.Millisecond), limited, retries, advanced,
			appliedAtRefusal, w.appliedSeq(), len(w.snapshots()), longestStall.Round(time.Millisecond),
			w.isClosed(), w.code()))

		// The re-arm reached the wire. Before c3a91af8 this was zero for ever:
		// the latch was cleared only by a Patch or a Snapshot, and a refusal is
		// neither.
		Expect(retries).To(BeNumerically(">", 0),
			"the client was refused %d times and never asked again, so the D-29 re-arm did not reach "+
				"the wire and the page is back to waiting for the eviction", limited)

		// The server served it. This is the half no JS harness can assert: the
		// real bucket, refilling one token per MinResyncInterval, grants the
		// request the schedule puts in front of it.
		Expect(advanced).To(BeNumerically(">", 0),
			"the applied sequence did not move once after the first refusal, so the retries were made "+
				"and refused and the freeze D-29 describes survives its own fix")

		// And the recovery is no longer the eviction. RFC §7.4's third stage is
		// the thing D-29 called "a job nobody assigned it"; the session has now
		// outlived three graces without it firing.
		Expect(w.isClosed()).To(BeFalse(),
			"the session was closed with %d after %s, against slow_client_grace of %s. If this is 4009 "+
				"the recovery is still the slow-client eviction and D-29 is not closed; if it is 4008 "+
				"the retry schedule is tripping the resync flood close, which is a NEW defect in the fix",
			w.code(), time.Since(refusedAt).Round(time.Millisecond), grace)

		// The stall a user would feel, bounded by the thing that used to be the
		// recovery. Anything at or above the grace would have been evicted, so
		// this is the assertion that says the number above is the schedule's and
		// not the eviction's.
		Expect(longestStall).To(BeNumerically("<", grace),
			"the longest interval with no applied frame was %s against a grace of %s: the retry "+
				"schedule is slower than the eviction it replaced", longestStall, grace)
	})
})

// ---------------------------------------------------------------------------
// 2. D-30 — CLOSED in 985b5f61, and this is the closure on the wire
// ---------------------------------------------------------------------------

// D-23's closure validates each of the three fields the mount Snapshot carries
// against the protocol's own refinement, and it validates them well: both
// endpoints, a sub-millisecond duration that narrows to zero, a value that is
// only in range once truncated to uint32, the error's wording and its
// authority, and a reflection property that MOUNTS every configuration New
// accepts and requires the first frame to be a Snapshot.
//
// That property holds the class D-23 was — "a configuration live.New accepts
// must produce a mount Snapshot this library can encode" — and D-30 was the
// class one step out: nothing compared the three fields with the fields they
// only make sense NEXT to.
//
// THE DEFECT, as this file measured it. `Actor.onTick` samples the liveness
// deadline on a ticker whose period is `HeartbeatInterval`
// (internal/wsx/conn.go: `time.NewTicker(h.opts.Limits.HeartbeatInterval)`), and
// a quiet session's only inbound frame is the echo of the heartbeat a tick
// carries. So a `HeartbeatTimeout` at or below one interval could never be met
// by any client at all: the first tick found the deadline already past and
// closed 4010 HEARTBEAT_TIMEOUT, whose reason blames the peer for a value the
// operator set. Measured here at 2 s against 1 s — both accepted by `live.New`,
// both inside every range D-23 checks — as **closed 4010 after ZERO
// heartbeats**. It was reachable from the value D-23's own error message
// recommends: the 5 m ceiling for the interval against the 50 s default for the
// timeout, which the operator never mentions, leaving the deadline 4m10s past
// due on the first tick.
//
// CLOSED, 985b5f61 (DEV-1). `Limits.validate` gained a relational check —
// `HeartbeatTimeout >= 2 * HeartbeatInterval`, compared on EFFECTIVE values so
// that the reachable case (one field set, the other silently defaulted) is the
// case it catches, with the error naming which of the two came from the
// defaults. Two intervals rather than one: one is the bare correctness bound
// and is satisfiable-but-useless, because a single dropped echo then closes a
// healthy client and G9 is "survives bad networks". Two leaves a client exactly
// one solicitation it may lose.
//
// So these specs are inverted, per the instruction the previous version
// carried: they assert the closure. What they are NOT is a second copy of
// live/limits_test.go, which holds six refusal entries including one a
// nanosecond inside the boundary and is the exhaustive home for the check. This
// suite's contribution is the half a unit suite cannot have — a real client on
// a real socket — so the second spec below takes the TIGHTEST pair `live.New`
// still admits, discovered by asking the library rather than by hard-coding
// two, and shows that the failure D-30 was is unreachable at the closest point
// to it that is still legal.
//
// ON THE onTick ORDERING, since the record should not be left implying a second
// open defect. It is NOT independently wrong and it was correctly left alone.
// The deadline is on INBOUND frames, so no echo can arrive within the tick that
// solicits it, and reversing the two would emit one more heartbeat on a session
// that closes anyway and change nothing else — it would in fact make the
// outcome depend on a round trip racing a function body, which is worse. The
// sampling it produces is a specified and asserted property already:
// case6_partition_test.go states and measures that dead-peer detection costs at
// most HeartbeatTimeout + HeartbeatInterval precisely BECAUSE the deadline is
// sampled on the tick. D-30 was the missing constraint that makes that property
// satisfiable at all. The same quantisation applies to the idle timeout and to
// slow_client_grace, and both are already stated where they are measured —
// case 4 asserts the eviction bound as "grace plus one tick" — so neither is a
// further finding either.
var _ = Describe("The heartbeat pair, each inside its range and fatal together (D-30, closed)", func() {

	// The construction half. Deliberately two entries and not six: the
	// exhaustive table is live/limits_test.go's, and duplicating it here would
	// be a second copy of somebody else's claim. These two are the ones THIS
	// file measured dying on the wire, so they are the two whose closure this
	// file should be the one to assert.
	DescribeTable("refuses at construction the pair that used to kill every quiet session",
		func(mutate func(limits *live.Limits), wantSubstrings ...string) {
			cfg := chaosConfig(newLedger())
			cfg.Logger = nil
			mutate(&cfg.Limits)

			_, err := live.New(cfg)

			var cfgErr *live.ConfigError
			Expect(errors.As(err, &cfgErr)).To(BeTrue(),
				"live.New accepted a heartbeat pair whose timeout no client can ever satisfy, so "+
					"every quiet session on it is closed 4010 after zero heartbeats: D-30 has "+
					"regressed; got %v", err)
			Expect(cfgErr.Field).To(Equal("Limits.HeartbeatTimeout"),
				"the rejection named %q. Of the two fields it must name the timeout: raising it is "+
					"always available, while lowering the interval is not — the interval leaves this "+
					"process as a refined session parameter in the mount Snapshot", cfgErr.Field)
			for _, want := range wantSubstrings {
				Expect(err).To(MatchError(ContainSubstring(want)))
			}
		},

		// What this file actually watched happen: closed 4010 after zero
		// heartbeats, with a client echoing every one it was sent.
		Entry("QA-2's measured pair, 2 s against 1 s",
			func(l *live.Limits) {
				l.HeartbeatInterval = 2 * time.Second
				l.HeartbeatTimeout = time.Second
			},
			"1s", "2s", "4010"),

		// The headline, and the reason D-30 was HIGH rather than a curiosity:
		// the interval is the top of the range D-23's OWN error message
		// recommends, and the timeout is a default the operator never mentions.
		// The error has to say so, or it quotes a number back at somebody who
		// did not type it — which is the diagnosis problem D-23 was about.
		Entry("the 5 m ceiling D-23's message recommends, against the default the operator never wrote",
			func(l *live.Limits) { l.HeartbeatInterval = 300 * time.Second },
			"5m0s", "HeartbeatTimeout is not set here and took its default of 50s"),
	)

	// The wire half, and the reason this spec lives in the chaos suite rather
	// than beside the other six.
	//
	// The pair is not hard-coded. It is DISCOVERED, in tenth-of-an-interval
	// steps, by asking live.New what it will accept and taking the smallest — so
	// the spec follows the constant instead of restating it. The step matters:
	// a search in whole intervals could never find a timeout BELOW one, which is
	// exactly the region D-30 was about, and a spec that cannot reach the
	// dangerous values is this project's recurring defect wearing a new hat.
	//
	// Two clients run on whatever it finds, and the second is the one that
	// justifies the constant being TWO. At one interval the check is
	// satisfiable and useless: a client that never loses anything survives by a
	// hair — measured, and it does — and a client that loses a single echo does
	// not. Nothing else in this repository asserts the reason for the second
	// interval, so it is asserted here, on the wire, where losing an echo is a
	// thing that can actually happen.
	DescribeTable("admits no pair a faithful client cannot survive, at the tightest one it does admit",
		func(dropEchoAt int, what string) {
			const interval = time.Second

			var tightest time.Duration
			for step := 1; step <= 30; step++ {
				candidate := time.Duration(step) * interval / 10
				cfg := chaosConfig(newLedger())
				cfg.Logger = nil
				cfg.Limits.HeartbeatInterval = interval
				cfg.Limits.HeartbeatTimeout = candidate
				if _, err := live.New(cfg); err == nil {
					tightest = candidate
					break
				}
			}
			Expect(tightest).NotTo(BeZero(),
				"live.New refused every HeartbeatTimeout from a tenth of an interval to three "+
					"intervals, so either the relational check has become unsatisfiable or the "+
					"interval itself is being refused")

			s := serve(func(cfg *live.Config[board, chaosUser]) {
				cfg.Logger = nil
				cfg.Limits.HeartbeatInterval = interval
				cfg.Limits.HeartbeatTimeout = tightest
				cfg.Limits.IdleTimeout = 5 * time.Minute
			})

			// silent is OFF: this client echoes the heartbeats it is sent, which
			// is what the shipped runtime does. Under D-30 a client exactly like
			// it was closed 4010 having never been sent one.
			w := dialWire(s.addr(), wireOpts{acks: ackAuto, dropHeartbeatEchoAt: dropEchoAt})

			Consistently(w.isClosed, 5*interval, interval/10).Should(BeFalse(),
				"%s was closed on the TIGHTEST pair live.New admits (interval %s, timeout %s, "+
					"%.1f intervals). That is D-30 back: the check admits a configuration on which "+
					"the liveness deadline cannot be met, and every quiet session on it dies 4010 "+
					"for ever", what, interval, tightest, float64(tightest)/float64(interval))

			var heartbeats int
			for _, f := range w.captured() {
				if f.GetHeartbeat() != nil {
					heartbeats++
				}
			}
			Expect(heartbeats).To(BeNumerically(">", 1),
				"the session survived five intervals having been sent %d heartbeats, so it did not "+
					"survive BECAUSE the liveness path works — this spec would pass against a "+
					"server that never checks anything", heartbeats)

			AddReportEntry("D-30 closed, on the wire", fmt.Sprintf(
				"HeartbeatInterval=%s; smallest HeartbeatTimeout live.New admits = %s (%.1f "+
					"intervals); %s survived %s and was sent %d heartbeats, where the pre-fix pair "+
					"closed a faithful client 4010 after 0.",
				interval, tightest, float64(tightest)/float64(interval), what, 5*interval, heartbeats))
		},

		Entry("a client that echoes every heartbeat", 0, "a client echoing every heartbeat"),
		// The second interval, earning itself. Losing one echo costs the
		// deadline one whole interval, so this entry is exactly the margin the
		// constant buys and it goes red the moment that margin is spent.
		Entry("a client that loses exactly one echo", 2, "a client that lost the echo of heartbeat 2"),
	)

	// The defaults, which is the entry that would catch the check being made
	// too strong. Every application that never mentions Limits is on this pair.
	It("still admits the documented defaults, which no application mentions", func() {
		d := session.DefaultLimits()

		cfg := chaosConfig(newLedger())
		cfg.Logger = nil
		_, err := live.New(cfg)

		Expect(err).NotTo(HaveOccurred(),
			"live.New refuses its OWN defaults (%s interval, %s timeout), so every application that "+
				"never mentions Limits fails to start", d.HeartbeatInterval, d.HeartbeatTimeout)
		Expect(d.HeartbeatTimeout).To(BeNumerically(">=", 2*d.HeartbeatInterval),
			"the defaults are %s against %s and no longer clear the constraint the library now "+
				"enforces on everybody else", d.HeartbeatTimeout, d.HeartbeatInterval)
	})
})
