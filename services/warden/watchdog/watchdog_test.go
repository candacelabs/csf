package watchdog

import (
	"context"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/services/warden"
)

const peerAddr = "203.0.113.11:7717"

// driver drives the watchdog state machine synchronously for deterministic
// white-box tests: step() runs one evaluation and then settles all delivery
// goroutines it spawned, applying each result to loop-owned state. Only the
// test goroutine mutates loopState (evaluate + handleResult); the delivery
// goroutines touch only the notifier and the results channel, so there is no
// data race on decision state. This synchronous harness is a simulator.
type driver struct {
	w       *Watchdog
	st      *loopState
	results chan notifyResult
	wg      sync.WaitGroup
	ctx     context.Context
}

func newDriver(cfg Config) (*driver, *fakeSource, *notifyRecorder, *fakeClock) {
	GinkgoHelper()
	src := newFakeSource()
	ctrl := gomock.NewController(GinkgoT())
	mock, rec := recordingNotifier(ctrl)
	clk := newFakeClock(baseTime)
	d := &driver{
		w:       New(cfg, src, mock, clk),
		st:      newLoopState(),
		results: make(chan notifyResult, 64),
		ctx:     context.Background(),
	}
	return d, src, rec, clk
}

// newDriverFailN builds a driver whose notifier fails its first n deliveries
// then records, for the retry spec.
func newDriverFailN(cfg Config, n int) (*driver, *fakeSource, *notifyRecorder, *fakeClock) {
	GinkgoHelper()
	src := newFakeSource()
	ctrl := gomock.NewController(GinkgoT())
	mock, rec := failThenRecordNotifier(ctrl, n)
	clk := newFakeClock(baseTime)
	d := &driver{
		w:       New(cfg, src, mock, clk),
		st:      newLoopState(),
		results: make(chan notifyResult, 64),
		ctx:     context.Background(),
	}
	return d, src, rec, clk
}

func (d *driver) step() {
	GinkgoHelper()
	d.w.evaluate(d.ctx, d.st, &d.wg, d.results)
	d.settle()
}

// settle drains a result for every currently in-flight delivery and applies
// it, then joins the finished goroutines.
func (d *driver) settle() {
	GinkgoHelper()
	for len(d.st.inFlight) > 0 {
		select {
		case r := <-d.results:
			d.w.handleResult(d.st, r)
		case <-time.After(2 * time.Second):
			Fail(fmt.Sprintf("timed out waiting for %d notify result(s)", len(d.st.inFlight)))
		}
	}
	d.wg.Wait()
}

var _ = Describe("Watchdog (synchronous driver)", func() {
	// TestDeadPeerNotifiedOnce
	It("notifies a dead peer once; repeated evaluations do not re-notify", func() {
		d, src, rec, _ := newDriver(Config{})
		seen := baseTime.Add(-time.Minute)
		src.set(leaderView(7, peer("node-a", peerAddr, warden.StatusDead, seen)))

		d.step()
		d.step()
		d.step()

		sent := rec.Sent()
		Expect(sent).To(HaveLen(1), "want 1 notification: %+v", sent)
		inc := sent[0]
		Expect(inc.Type).To(Equal(warden.IncidentPeerDead))
		Expect(inc.Peer.ID).To(Equal(warden.NodeID("node-a")))
		Expect(inc.ReportedBy).To(Equal(selfID))
		Expect(inc.Term).To(Equal(warden.Term(7)))
		Expect(inc.LastSeen).To(BeTemporally("==", seen))
		Expect(d.w.Incidents()).To(HaveLen(1))
		wantMsg := "peer node-a (203.0.113.11:7717) declared dead by leader node-c (term 7); last seen "
		Expect(inc.Message).To(HavePrefix(wantMsg))
	})

	// TestRecoveryNotification (enabled)
	It("notifies recovery when enabled, after a dead episode closes", func() {
		d, src, rec, clk := newDriver(Config{NotifyRecovery: true})
		seen := baseTime.Add(-time.Minute)
		src.set(leaderView(7, peer("node-a", peerAddr, warden.StatusDead, seen)))
		d.step()
		clk.Advance(90 * time.Second)
		src.set(leaderView(7, peer("node-a", peerAddr, warden.StatusAlive, clk.Now())))
		d.step()

		sent := rec.Sent()
		Expect(sent).To(HaveLen(2), "dead+recovered: %+v", sent)
		Expect(sent[1].Type).To(Equal(warden.IncidentPeerRecovered))
		Expect(sent[1].Message).To(ContainSubstring("outage lasted 1m30s"))
		Expect(d.w.Incidents()).To(HaveLen(2))
	})

	// TestRecoveryNotification (disabled)
	It("does not notify recovery when disabled, but still records it", func() {
		d, src, rec, clk := newDriver(Config{NotifyRecovery: false})
		seen := baseTime.Add(-time.Minute)
		src.set(leaderView(7, peer("node-a", peerAddr, warden.StatusDead, seen)))
		d.step()
		clk.Advance(90 * time.Second)
		src.set(leaderView(7, peer("node-a", peerAddr, warden.StatusAlive, clk.Now())))
		d.step()

		sent := rec.Sent()
		log := d.w.Incidents()
		Expect(sent).To(HaveLen(1), "dead only: %+v", sent)
		Expect(sent[0].Type).To(Equal(warden.IncidentPeerDead))
		// The recovery is still recorded in the log.
		Expect(log).To(HaveLen(2), "dead+recovered recorded")
		Expect(log[0].Type).To(Equal(warden.IncidentPeerRecovered))
	})

	// TestFlapCooldown
	It("suppresses a flap within cooldown, records it, and re-notifies after cooldown", func() {
		d, src, rec, clk := newDriver(Config{Cooldown: 30 * time.Second})
		seen := baseTime.Add(-time.Minute)
		dead := func() { src.set(leaderView(7, peer("node-a", peerAddr, warden.StatusDead, seen))) }
		alive := func() { src.set(leaderView(7, peer("node-a", peerAddr, warden.StatusAlive, clk.Now()))) }

		dead()
		d.step() // dead #1 -> notified, cooldown stamped at baseTime
		Expect(rec.Sent()).To(HaveLen(1), "after first dead")

		clk.Advance(5 * time.Second)
		alive()
		d.step() // recovery closes episode (not notified)

		clk.Advance(5 * time.Second) // t+10s, within 30s cooldown
		dead()
		d.step() // dead #2 -> suppressed by cooldown
		Expect(rec.Sent()).To(HaveLen(1), "dead within cooldown must be suppressed")

		clk.Advance(25 * time.Second) // t+35s, past cooldown
		alive()
		d.step()                 // recovery closes the suppressed episode
		clk.Advance(time.Second) // t+36s
		dead()
		d.step() // dead #3 -> new episode, cooldown elapsed -> notified
		Expect(rec.Sent()).To(HaveLen(2), "after cooldown a new episode must notify")
		for _, inc := range rec.Sent() {
			Expect(inc.Type).To(Equal(warden.IncidentPeerDead), "all notifications should be dead type")
		}
	})

	// TestNewLeaderSeesAlreadyDeadPeer
	It("as a new leader notifies an already-dead peer once; as a follower does nothing", func() {
		d, src, rec, _ := newDriver(Config{})
		seen := baseTime.Add(-time.Minute)

		src.set(followerView(7, "other-node", peer("node-a", peerAddr, warden.StatusDead, seen)))
		d.step()
		Expect(rec.Sent()).To(HaveLen(0), "follower must not notify")
		Expect(d.w.Incidents()).To(HaveLen(0), "follower must record nothing")

		src.set(leaderView(8, peer("node-a", peerAddr, warden.StatusDead, seen)))
		d.step()
		d.step()
		Expect(rec.Sent()).To(HaveLen(1), "new leader must notify the already-dead peer once")
		Expect(rec.Sent()[0].Term).To(Equal(warden.Term(8)), "new leader term")
	})

	// TestFollowerAndGateStopEvaluation
	Describe("stops evaluation when not the acting authoritative leader", func() {
		seen := baseTime.Add(-time.Minute)
		deadPeer := peer("node-a", peerAddr, warden.StatusDead, seen)

		It("steps down to follower", func() {
			d, src, rec, _ := newDriver(Config{})
			src.set(leaderView(7, peer("node-a", peerAddr, warden.StatusAlive, seen)))
			d.step() // leader, peer alive -> nothing
			src.set(followerView(7, "other-node", deadPeer))
			d.step() // stepped down; dead peer must be ignored
			Expect(rec.Sent()).To(HaveLen(0), "follower must not notify")
			Expect(d.w.Incidents()).To(HaveLen(0), "follower must record nothing")
		})

		It("ignores a non-authoritative leader view", func() {
			d, src, rec, _ := newDriver(Config{})
			v := leaderView(7, deadPeer)
			v.Authoritative = false
			src.set(v)
			d.step()
			Expect(len(rec.Sent())+len(d.w.Incidents())).To(Equal(0), "non-authoritative view must not act")
		})

		It("ignores a view sourced elsewhere", func() {
			d, src, rec, _ := newDriver(Config{})
			v := leaderView(7, deadPeer)
			v.Source = "other-node"
			src.set(v)
			d.step()
			Expect(len(rec.Sent())+len(d.w.Incidents())).To(Equal(0), "view sourced elsewhere must not act")
		})
	})

	// TestNotifyRetryUntilSuccess
	It("retries a failing Notify until success, with one delivery and no duplicate incident", func() {
		d, src, rec, _ := newDriverFailN(Config{}, 2)
		seen := baseTime.Add(-time.Minute)
		src.set(leaderView(7, peer("node-a", peerAddr, warden.StatusDead, seen)))

		d.step() // create + attempt #1 (fail)
		Expect(rec.Sent()).To(HaveLen(0), "after fail #1")
		d.step() // retry #2 (fail)
		Expect(rec.Sent()).To(HaveLen(0), "after fail #2")
		d.step() // retry #3 (success)
		Expect(rec.Sent()).To(HaveLen(1), "after success")
		d.step() // nothing new
		Expect(rec.Sent()).To(HaveLen(1), "no duplicate after success")
		Expect(d.w.Incidents()).To(HaveLen(1), "incident recorded once")
	})

	// TestSuspectUnknownProduceNothing
	It("produces nothing for suspect and unknown peers", func() {
		d, src, rec, _ := newDriver(Config{})
		seen := baseTime.Add(-time.Minute)
		src.set(leaderView(7,
			peer("s", "10.0.0.1:1", warden.StatusSuspect, seen),
			peer("u", "10.0.0.2:2", warden.StatusUnknown, time.Time{}),
		))
		d.step()
		Expect(len(rec.Sent())+len(d.w.Incidents())).To(Equal(0), "suspect/unknown must produce nothing")
	})

	// TestDeadSuspectDoesNotCloseEpisode
	It("keeps an episode open across dead->suspect->dead, closing only on alive", func() {
		d, src, rec, _ := newDriver(Config{NotifyRecovery: true})
		seen := baseTime.Add(-time.Minute)
		set := func(status warden.PeerStatus) {
			src.set(leaderView(7, peer("node-a", peerAddr, status, seen)))
		}

		set(warden.StatusDead)
		d.step()
		set(warden.StatusSuspect)
		d.step() // suspect: episode stays open
		set(warden.StatusDead)
		d.step() // still open -> no new dead incident
		set(warden.StatusAlive)
		d.step() // now closes -> exactly one recovery

		var deadCount, recoveredCount int
		for _, inc := range rec.Sent() {
			switch inc.Type {
			case warden.IncidentPeerDead:
				deadCount++
			case warden.IncidentPeerRecovered:
				recoveredCount++
			}
		}
		Expect(deadCount).To(Equal(1), "suspect must not reopen")
		Expect(recoveredCount).To(Equal(1), "suspect must not close early")
	})

	// TestRingBufferEviction
	It("caps the incident log at MaxIncidents, evicting oldest", func() {
		d, src, _, _ := newDriver(Config{MaxIncidents: 3})
		seen := baseTime.Add(-time.Minute)
		// Five dead episodes opened across successive views, one dead peer each,
		// so every view keeps a live majority (self + anchors) and the isolation
		// guard does not freeze evaluation.
		for i, id := range []string{"a", "b", "c", "d", "e"} {
			src.set(leaderView(7, peer(id, "10.0.0.1:1", warden.StatusDead, seen.Add(time.Duration(i)*time.Second))))
			d.step()
		}

		log := d.w.Incidents()
		Expect(log).To(HaveLen(3), "capped")
		// Episodes opened in order a..e; ring keeps c,d,e; most recent first.
		want := []warden.NodeID{"e", "d", "c"}
		for i, id := range want {
			Expect(log[i].Peer.ID).To(Equal(id), "log[%d].peer", i)
		}
	})

	// TestIncidentsOrderingAndCopy
	It("returns incidents most-recent-first as a defensive copy", func() {
		d, src, _, _ := newDriver(Config{})
		seen := baseTime.Add(-time.Minute)
		src.set(leaderView(7,
			peer("x", "10.0.0.1:1", warden.StatusDead, seen),
			peer("y", "10.0.0.2:2", warden.StatusDead, seen),
		))
		d.step()

		log := d.w.Incidents()
		Expect(log).To(HaveLen(2))
		Expect(log[0].Peer.ID).To(Equal(warden.NodeID("y")))
		Expect(log[1].Peer.ID).To(Equal(warden.NodeID("x")))
		// Mutate the returned copy; internal state must be unaffected.
		log[0].Message = "tampered"
		log[0].Peer.ID = "tampered"
		again := d.w.Incidents()
		Expect(again[0].Message).NotTo(Equal("tampered"))
		Expect(again[0].Peer.ID).NotTo(Equal(warden.NodeID("tampered")))
	})

	// TestSelfNeverGeneratesIncident
	It("never generates an incident for self, but does for other dead peers", func() {
		d, src, rec, _ := newDriver(Config{})
		seen := baseTime.Add(-time.Minute)
		src.set(leaderView(7,
			peer(string(selfID), selfAddr, warden.StatusDead, seen),
			peer("node-a", peerAddr, warden.StatusDead, seen),
		))
		d.step()

		sent := rec.Sent()
		Expect(sent).To(HaveLen(1), "peer only, not self: %+v", sent)
		Expect(sent[0].Peer.ID).To(Equal(warden.NodeID("node-a")), "self must be skipped")
	})

	// TestCooldownSurvivesLeadershipFlap
	It("keeps cooldown timestamps across a leadership epoch reset", func() {
		d, src, rec, clk := newDriver(Config{Cooldown: 30 * time.Second})
		seen := baseTime.Add(-time.Minute)

		src.set(leaderView(7, peer("node-a", peerAddr, warden.StatusDead, seen)))
		d.step() // dead notified, cooldown stamped at baseTime
		Expect(rec.Sent()).To(HaveLen(1), "initial dead")

		// Step down (resets open episodes on the next leadership gain).
		src.set(followerView(7, "other-node", peer("node-a", peerAddr, warden.StatusDead, seen)))
		d.step()

		// Regain leadership within the cooldown, peer still dead.
		clk.Advance(10 * time.Second)
		src.set(leaderView(8, peer("node-a", peerAddr, warden.StatusDead, seen)))
		d.step()

		Expect(rec.Sent()).To(HaveLen(1), "cooldown must survive the epoch reset")
		// A fresh incident is still recorded (fresh perspective), just suppressed.
		Expect(d.w.Incidents()).To(HaveLen(2), "re-detected incident should be recorded")
	})

	// TestIsolatedLeaderSuppressesAlerts
	It("suppresses all death alerts while isolated, then alerts once quorum returns", func() {
		d, src, rec, _ := newDriver(Config{})
		seen := baseTime.Add(-time.Minute)

		// Built directly (no anchors): a 4-node cluster where self is the only
		// live member. alive=1 < quorum(4)=3 -> everything is frozen.
		isolated := warden.ClusterView{
			Self:          selfID,
			Role:          warden.RoleLeader,
			Term:          7,
			LeaderID:      selfID,
			Source:        selfID,
			Authoritative: true,
			Peers: []warden.PeerView{
				peer(string(selfID), selfAddr, warden.StatusAlive, baseTime),
				peer("node-a", peerAddr, warden.StatusDead, seen),
				peer("node-d", "203.0.113.14:7717", warden.StatusDead, seen),
				peer("node-b", "203.0.113.12:7717", warden.StatusDead, seen),
			},
		}
		src.set(isolated)
		d.step()
		d.step()
		Expect(len(rec.Sent())+len(d.w.Incidents())).To(Equal(0), "isolated leader must suppress all death alerts")

		// Contact with a majority returns (self + two alive = quorum 3 of 4);
		// the one genuinely dead peer now alerts exactly once.
		quorate := isolated
		quorate.Peers = []warden.PeerView{
			peer(string(selfID), selfAddr, warden.StatusAlive, baseTime),
			peer("node-a", peerAddr, warden.StatusDead, seen),
			peer("node-d", "203.0.113.14:7717", warden.StatusAlive, baseTime),
			peer("node-b", "203.0.113.12:7717", warden.StatusAlive, baseTime),
		}
		src.set(quorate)
		d.step()
		d.step()
		sent := rec.Sent()
		Expect(sent).To(HaveLen(1), "after quorum restored: %+v", sent)
		Expect(sent[0].Peer.ID).To(Equal(warden.NodeID("node-a")))
		Expect(sent[0].Type).To(Equal(warden.IncidentPeerDead))
	})

	// TestVotersOnlyAlerting
	DescribeTable("only voting members generate incidents",
		func(kind warden.MemberKind, wantSent, wantLogged int) {
			d, src, rec, _ := newDriver(Config{})
			seen := baseTime.Add(-time.Minute)
			src.set(leaderView(7, memberPeer("cand", peerAddr, warden.StatusDead, seen, kind)))
			d.step()
			d.step()

			Expect(rec.Sent()).To(HaveLen(wantSent), "notifications")
			Expect(d.w.Incidents()).To(HaveLen(wantLogged), "incident log")
			if wantSent == 1 {
				Expect(rec.Sent()[0].Peer.ID).To(Equal(warden.NodeID("cand")))
			}
		},
		Entry("dead observer raises nothing", warden.MemberObserver, 0, 0),
		Entry("dead discovered raises nothing", warden.MemberDiscovered, 0, 0),
		Entry("dead voter raises one incident", warden.MemberVoter, 1, 1),
	)

	// TestVotersOnlyAlertingMixedView
	It("in a mixed view alerts only for the dead voter, ignoring observers/discovered", func() {
		d, src, rec, _ := newDriver(Config{})
		seen := baseTime.Add(-time.Minute)

		// Membership: self + a live voter (keeps quorum) + a voter that dies. The
		// dead observers and discovered node are noise: they must not affect
		// alerting or quorum.
		m := membership(1, string(selfID), "voter-live", "voter-x")
		src.set(membershipLeaderView(7, m,
			peer(string(selfID), selfAddr, warden.StatusAlive, baseTime),
			memberPeer("voter-live", "10.0.0.9:9", warden.StatusAlive, baseTime, warden.MemberVoter),
			memberPeer("voter-x", peerAddr, warden.StatusDead, seen, warden.MemberVoter),
			memberPeer("obs-1", "10.0.0.1:1", warden.StatusDead, seen, warden.MemberObserver),
			memberPeer("obs-2", "10.0.0.2:2", warden.StatusDead, seen, warden.MemberObserver),
			memberPeer("disc-1", "10.0.0.3:3", warden.StatusDead, seen, warden.MemberDiscovered),
		))
		d.step()
		d.step()

		sent := rec.Sent()
		Expect(sent).To(HaveLen(1), "voter only: %+v", sent)
		Expect(sent[0].Peer.ID).To(Equal(warden.NodeID("voter-x")))
		Expect(sent[0].Type).To(Equal(warden.IncidentPeerDead))
		Expect(d.w.Incidents()).To(HaveLen(1), "voter only")
	})

	// TestIsolationGuardCountsOnlyVoters
	Describe("isolation guard counts only voters", func() {
		seen := baseTime.Add(-time.Minute)

		It("dead observers do not erode quorum", func() {
			// 3 voters (self + voter-a alive + voter-b dead): alive voters = 2,
			// quorum(3) = 2 -> quorate. Five dead observers must not tip the leader
			// into the isolation guard, and the one dead voter alerts.
			d, src, rec, _ := newDriver(Config{})
			m := membership(2, string(selfID), "voter-a", "voter-b")
			peers := []warden.PeerView{
				peer(string(selfID), selfAddr, warden.StatusAlive, baseTime),
				memberPeer("voter-a", "10.0.1.1:1", warden.StatusAlive, baseTime, warden.MemberVoter),
				memberPeer("voter-b", "10.0.1.2:2", warden.StatusDead, seen, warden.MemberVoter),
			}
			for i, id := range []string{"o1", "o2", "o3", "o4", "o5"} {
				peers = append(peers, memberPeer(id, "10.0.2."+string(rune('1'+i))+":9", warden.StatusDead, seen, warden.MemberObserver))
			}
			src.set(membershipLeaderView(9, m, peers...))
			d.step()
			d.step()

			sent := rec.Sent()
			Expect(sent).To(HaveLen(1), "exactly voter-b: %+v", sent)
			Expect(sent[0].Peer.ID).To(Equal(warden.NodeID("voter-b")))
			Expect(sent[0].Type).To(Equal(warden.IncidentPeerDead))
		})

		It("alive observers cannot restore quorum", func() {
			// 4 voters, only self alive (voter-a/b/c dead): alive voters = 1,
			// quorum(4) = 3 -> NOT quorate. Three alive observers must not rescue
			// the leader, so all death alerts stay suppressed.
			d, src, rec, _ := newDriver(Config{})
			m := membership(3, string(selfID), "voter-a", "voter-b", "voter-c")
			peers := []warden.PeerView{
				peer(string(selfID), selfAddr, warden.StatusAlive, baseTime),
				memberPeer("voter-a", "10.0.1.1:1", warden.StatusDead, seen, warden.MemberVoter),
				memberPeer("voter-b", "10.0.1.2:2", warden.StatusDead, seen, warden.MemberVoter),
				memberPeer("voter-c", "10.0.1.3:3", warden.StatusDead, seen, warden.MemberVoter),
				memberPeer("o1", "10.0.2.1:9", warden.StatusAlive, baseTime, warden.MemberObserver),
				memberPeer("o2", "10.0.2.2:9", warden.StatusAlive, baseTime, warden.MemberObserver),
				memberPeer("o3", "10.0.2.3:9", warden.StatusAlive, baseTime, warden.MemberObserver),
			}
			src.set(membershipLeaderView(9, m, peers...))
			d.step()
			d.step()

			Expect(len(rec.Sent())+len(d.w.Incidents())).To(Equal(0),
				"isolated leader must suppress all alerts (observers cannot restore quorum)")
		})
	})
})
