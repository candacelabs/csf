package election

import (
	"time"

	"github.com/candacelabs/csf/services/warden"
)

// onHeartbeatTick fires on the heartbeat ticker (armed on every node). Every
// node in discovery mode uses the tick to maintain its discovery state
// (eligibility timers, dropping vanished observers, re-probing). Only a leader
// then fans out heartbeats and drives membership changes.
func (m *Manager) onHeartbeatTick() {
	if m.discoveryMode {
		m.maintainDiscovery(m.clock.Now())
	}
	if m.role != warden.RoleLeader {
		return
	}
	m.sendHeartbeats()
	if m.discoveryMode {
		m.maybeStartMembershipChange()
	}
}

// sendHeartbeats fans out a heartbeat carrying the leader's authoritative
// cluster view (and, in discovery mode, its effective membership) to every
// other voter AND every known observer, in parallel. Observers are heartbeated
// so they learn the leader, its views, and membership. Each worker measures the
// RTT and reports the result back to the loop.
func (m *Manager) sendHeartbeats() {
	term := m.currentTerm
	self := m.self.ID
	// One fresh authoritative view is shared (read-only) by all workers.
	view := m.snapshotView()

	// In discovery mode every heartbeat carries the current membership so
	// followers/observers persist-then-adopt it. One read-only copy is shared.
	var memPtr *warden.Membership
	mver, mterm := m.membership.Version, m.membership.CreatedInTerm
	if m.discoveryMode {
		mc := m.membership.Clone()
		memPtr = &mc
	}

	for _, p := range m.heartbeatTargets() {
		p := p
		m.spawnRPC(func() {
			ctx, cancel := m.rpcContext()
			defer cancel()
			vc := view
			sentAt := m.clock.Now()
			resp, err := m.transport.SendHeartbeat(ctx, p, warden.HeartbeatRequest{
				Term:       term,
				LeaderID:   self,
				View:       &vc,
				Membership: memPtr,
			})
			rtt := m.clock.Now().Sub(sentAt)
			m.deliver(heartbeatResultMsg{term: term, peer: p, resp: resp, err: err, sentAt: sentAt, rtt: rtt, mversion: mver, mterm: mterm})
		})
	}
}

// heartbeatTargets is the set of nodes the leader heartbeats: every other voter
// plus every verified observer. Observers are included so they stay in sync but
// they never affect quorum or the watchdog denominator.
func (m *Manager) heartbeatTargets() []warden.Node {
	out := make([]warden.Node, 0, len(m.membership.Voters)+len(m.candidates))
	out = append(out, m.voterPeers()...)
	if m.discoveryMode {
		for _, c := range m.candidates {
			if c.verified {
				out = append(out, c.node)
			}
		}
	}
	return out
}

// onHeartbeatResult records the outcome of a heartbeat. A higher term forces a
// step-down; an RPC error (unreachable/timeout) is left to age the target
// toward suspect/dead, since no response at all is the only real evidence of
// non-liveness. Any OTHER well-formed response — including OK:false — proves
// this peer is alive at the transport level and updates its liveness
// (lastContact/latencyMS for a voter, lastContact for an observer)
// regardless of OK.
//
// OK is deliberately NOT a liveness signal: it means only "the follower
// durably adopted the membership this heartbeat carried" (see onHeartbeat and
// the HeartbeatResponse doc comment). A follower can be fully reachable and
// heartbeating normally while persistently failing to persist a membership
// change (e.g. a failing disk) — conflating that with unreachability would
// let the watchdog raise a false peer_dead alert for a node that is actually
// up. So OK gates ONLY the settle/ack-version advance below (the
// one-change-at-a-time SETTLE rule), never liveness.
func (m *Manager) onHeartbeatResult(e heartbeatResultMsg) {
	if m.role != warden.RoleLeader || e.term != m.currentTerm {
		return
	}
	if e.err != nil {
		return // failed contact: lastContact is intentionally not updated
	}
	if e.resp.Term > m.currentTerm {
		m.stepDown(e.resp.Term)
		return
	}

	// A response arrived with an acceptable term: this peer IS alive,
	// independent of OK.
	if m.isVoter(e.peer.ID) {
		m.lastContact[e.peer.ID] = e.sentAt
		m.latencyMS[e.peer.ID] = float64(e.rtt) / float64(time.Millisecond)
	} else if c := m.candidates[e.peer.ID]; c != nil {
		// Observer liveness (never counted toward quorum).
		c.lastContact = e.sentAt
	}

	if !e.resp.OK {
		// Reachable, but the follower rejected this heartbeat (stale term
		// reply — already handled above — or, in discovery mode, a failed
		// membership-adoption Save). Liveness is already recorded; do not
		// count this as a settle ack.
		m.publish()
		return
	}

	if m.isVoter(e.peer.ID) && m.discoveryMode && m.ackedVersion[e.peer.ID].newerAck(e.mversion, e.mterm) {
		m.ackedVersion[e.peer.ID] = ackRef{version: e.mversion, term: e.mterm}
	}
	m.publish()

	// An ack may have just settled a pending change; try to make progress on
	// the next one.
	if m.discoveryMode {
		m.maybeStartMembershipChange()
	}
}

// onHeartbeat handles an inbound heartbeat. A heartbeat whose term is at least
// ours is accepted: we adopt any higher term, record the leader, cache its
// view, reset the election timer, and acknowledge. A stale term is rejected
// with our current (newer) term so the sender steps down.
func (m *Manager) onHeartbeat(req warden.HeartbeatRequest) warden.HeartbeatResponse {
	now := m.clock.Now()

	if req.Term < m.currentTerm {
		return warden.HeartbeatResponse{Term: m.currentTerm, OK: false, NodeID: m.self.ID}
	}

	if req.Term > m.currentTerm {
		// Adopt the newer term. votedFor resets for the new term. We only Save
		// when the term actually changes, so steady-state heartbeats never
		// fsync.
		if err := m.saveState(req.Term, ""); err != nil {
			m.log.Error().Err(err).
				Str("node", string(m.self.ID)).
				Uint64("term", uint64(req.Term)).
				Msg("warden: failed to persist adopted term on heartbeat; accepting leader anyway")
		}
		m.currentTerm = req.Term
		m.votedFor = ""
	}

	m.role = warden.RoleFollower
	m.leaderID = req.LeaderID
	m.lastLeaderContact = now
	if req.View != nil {
		vc := copyView(*req.View)
		m.cachedView = &vc
		m.cachedViewAt = now
	}

	// Membership adoption: from the leader we accept, persist-then-adopt any
	// strictly newer configuration (discovery mode only). A Save failure here
	// must surface as OK=false: the leader's settle accounting
	// (changeSettled/ackedVersion, membership.go) counts an ack ONLY from a
	// voter that durably stored the config, and OK is the only signal an
	// unmodified HeartbeatResponse can carry that with. Silently acking a
	// membership this node did not persist would let the leader declare a
	// one-at-a-time change "settled" on a non-durable quorum — under
	// coincident follower Save failures that lets followers drift more than
	// one config apart, reintroducing the split-brain the design otherwise
	// rules out by construction.
	acked := true
	if m.discoveryMode && req.Membership != nil && req.Membership.Supersedes(m.membership) {
		acked = m.adoptMembership(*req.Membership)
	}

	timeout := m.randTimeout()
	m.electionDeadline = now.Add(timeout)
	m.resetElectionTimer(timeout)

	m.publish()
	return warden.HeartbeatResponse{Term: m.currentTerm, OK: acked, NodeID: m.self.ID}
}
