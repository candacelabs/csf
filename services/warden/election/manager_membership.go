package election

import (
	"time"

	"github.com/candacelabs/csf/services/warden"
)

// candidate tracks one non-voter node reported by discovery. It lives only in
// discovery mode and is owned exclusively by the Run loop.
type candidate struct {
	node warden.Node
	// inRoster is whether the node is present in the latest roster snapshot.
	inRoster bool
	// absentSince is when the node left the roster (zero while present); a
	// candidate absent longer than the JoinStability grace window is dropped.
	absentSince time.Time
	// verified is whether the last identify probe confirmed the node is a
	// same-cluster warden with a matching ID. It is re-checked on every fresh
	// roster entry (see onRoster) so a stale identity cannot linger.
	verified bool
	// eligibleSince is when the node became continuously (inRoster && verified);
	// zero whenever either condition is false. Admission requires this to be at
	// least JoinStability in the past.
	eligibleSince time.Time
	// lastContact is the last successful contact (identify OK or, once an
	// observer, a heartbeat OK) — the leader's observer-liveness reference. It
	// NEVER affects quorum.
	lastContact time.Time
	// probing guards against issuing a second identify probe while one is in
	// flight.
	probing bool
}

// sortNodes sorts a Node slice by ID in place — the canonical order for a
// membership's Voters.
// onRoster consumes a discovery snapshot. It keeps the snapshot as the last
// known roster (channel silence keeps it — never treated as empty), reconciles
// the candidate set, and lets the leader act on any resulting change.
func (m *Manager) onRoster(r warden.Roster) {
	now := m.clock.Now()
	m.lastRoster = r
	m.haveRoster = true

	present := make(map[warden.NodeID]warden.Node, len(r.Nodes))
	for _, n := range r.Nodes {
		present[n.ID] = n
	}

	// Add/refresh candidates for non-voter, non-self roster entries.
	for id, node := range present {
		if id == m.self.ID || m.isVoter(id) {
			continue
		}
		c := m.candidates[id]
		if c == nil {
			c = &candidate{node: node}
			m.candidates[id] = c
		}
		c.node = node
		if !c.inRoster {
			// (Re)entered the roster: force a fresh identity verification and
			// (re)start the continuous-presence clock from scratch.
			c.inRoster = true
			c.verified = false
			c.eligibleSince = time.Time{}
		}
		c.absentSince = time.Time{}
	}

	// Mark tracked candidates that are no longer present.
	for id, c := range m.candidates {
		if _, ok := present[id]; !ok && c.inRoster {
			c.inRoster = false
			c.absentSince = now
			c.eligibleSince = time.Time{} // presence broke
		}
	}

	m.maintainDiscovery(now)
	if m.role == warden.RoleLeader {
		m.maybeStartMembershipChange()
	}
}

// maintainDiscovery is the periodic (and post-roster) reconciliation of the
// candidate set: drop anything that became a voter or has been absent past the
// grace window, refresh eligibility timers, and (re)probe unverified in-roster
// candidates. Called on every heartbeat tick and after each roster snapshot.
func (m *Manager) maintainDiscovery(now time.Time) {
	for id, c := range m.candidates {
		if id == m.self.ID || m.isVoter(id) {
			delete(m.candidates, id) // admitted or otherwise now a voter
			continue
		}
		if !c.inRoster && !c.absentSince.IsZero() && now.Sub(c.absentSince) >= m.cfg.JoinStability {
			delete(m.candidates, id) // vanished beyond the grace window
			continue
		}
		// Refresh the continuous (inRoster && verified) eligibility clock.
		if c.inRoster && c.verified {
			if c.eligibleSince.IsZero() {
				c.eligibleSince = now
			}
		} else {
			c.eligibleSince = time.Time{}
		}
	}
	m.probeCandidates()
}

// probeCandidates issues an identify probe for each in-roster candidate that is
// not yet verified and has no probe in flight. Verification is sticky (once
// confirmed it is not re-probed until the node leaves and re-enters the roster),
// which keeps probe volume tiny. Probes are loop-spawned, ctx-respecting,
// tracked worker goroutines that report back on the inbound channel.
func (m *Manager) probeCandidates() {
	for _, c := range m.candidates {
		if !c.inRoster || c.probing || c.verified {
			continue
		}
		c.probing = true
		node := c.node
		m.spawnRPC(func() {
			ctx, cancel := m.rpcContext()
			defer cancel()
			resp, err := m.transport.Identify(ctx, node)
			m.deliver(identifyResultMsg{node: node, resp: resp, err: err})
		})
	}
}

// onIdentifyResult folds an identify probe outcome back into the candidate. A
// match (cluster id + node id) marks the candidate verified and starts its
// eligibility clock; anything else clears verification.
func (m *Manager) onIdentifyResult(e identifyResultMsg) {
	c := m.candidates[e.node.ID]
	if c == nil {
		return // no longer tracked (left the roster, or admitted)
	}
	c.probing = false
	now := m.clock.Now()

	if e.err != nil || e.resp.ClusterID != m.cfg.ClusterID || e.resp.NodeID != e.node.ID {
		c.verified = false
		c.eligibleSince = time.Time{}
		return
	}

	c.verified = true
	c.lastContact = now
	if c.inRoster && c.eligibleSince.IsZero() {
		c.eligibleSince = now
	}
	// A newly verified, continuously-present candidate may already be eligible.
	if m.role == warden.RoleLeader {
		m.maybeStartMembershipChange()
	}
}

// candidateEligible reports whether c is admission-eligible right now:
// identify-verified AND continuously present in the roster for JoinStability.
func (m *Manager) candidateEligible(c *candidate, now time.Time) bool {
	return c.inRoster && c.verified && !c.eligibleSince.IsZero() &&
		now.Sub(c.eligibleSince) >= m.cfg.JoinStability
}

// changeSettled reports whether the current membership is committed on a
// majority of the CURRENT voters (each such voter having acked a heartbeat that
// carried at least this version). No new membership change may begin until the
// current one is settled — this is what keeps changes strictly one at a time.
func (m *Manager) changeSettled() bool {
	v, ct := m.membership.Version, m.membership.CreatedInTerm
	acked := 0
	for _, voter := range m.membership.Voters {
		a := m.ackedVersion[voter.ID]
		// An ack counts only for this exact config or a newer one: a voter
		// holding a divergent sibling (same version, older term) is NOT
		// committed to this configuration.
		if a.version > v || (a.version == v && a.term >= ct) {
			acked++
		}
	}
	return acked >= m.quorum()
}

// maybeStartMembershipChange is the leader's membership driver. It applies at
// most ONE single-node change (add or remove) and only when the previous change
// has settled. Admission (growing the cluster) is preferred over removal.
func (m *Manager) maybeStartMembershipChange() {
	if !m.discoveryMode || m.role != warden.RoleLeader {
		return
	}
	if !m.changeSettled() {
		return // a change is still in flight; strictly one at a time
	}

	// ADMISSION: add the lowest-ID eligible observer.
	if node, ok := m.nextAdmission(); ok {
		next := append(append([]warden.Node(nil), m.membership.Voters...), node)
		warden.SortNodes(next)
		m.applyMembershipChange(warden.Membership{Version: m.membership.Version + 1, CreatedInTerm: m.currentTerm, Voters: next}, "admit", node)
		return
	}

	// REMOVAL: only when enabled, and only a genuinely dead + roster-absent
	// voter, and only while we still see a live majority (isolation guard).
	if m.cfg.RemoveAfter > 0 {
		if node, ok := m.nextRemoval(); ok {
			next := make([]warden.Node, 0, len(m.membership.Voters)-1)
			for _, v := range m.membership.Voters {
				if v.ID != node.ID {
					next = append(next, v)
				}
			}
			m.applyMembershipChange(warden.Membership{Version: m.membership.Version + 1, CreatedInTerm: m.currentTerm, Voters: next}, "remove", node)
		}
	}
}

// nextAdmission returns the lowest-ID admission-eligible observer, if any
// (deterministic order: lowest ID first).
func (m *Manager) nextAdmission() (warden.Node, bool) {
	now := m.clock.Now()
	var best *candidate
	for _, c := range m.candidates {
		if !m.candidateEligible(c, now) {
			continue
		}
		if best == nil || c.node.ID < best.node.ID {
			best = c
		}
	}
	if best == nil {
		return warden.Node{}, false
	}
	return best.node, true
}

// nextRemoval returns the lowest-ID voter the leader may currently remove, if
// any. All of these must hold: the leader has observed at least one real
// discovery roster snapshot (never treat "discovery hasn't reported anything
// yet" as "roster confirms this voter is gone" — see haveRoster), the leader
// sees a live majority of the current voters (isolation guard — an isolated
// leader must never shrink the denominator), the target is not self, the
// target is absent from the latest roster, and the target is StatusDead with
// its last contact at least RemoveAfter in the past.
func (m *Manager) nextRemoval() (warden.Node, bool) {
	if !m.haveRoster {
		// lastRoster is still its zero value (Nodes == nil), which is
		// bit-for-bit indistinguishable from a discovery source that HAS
		// polled and found the fleet genuinely empty. Without this gate every
		// voter would look roster-absent from the moment this node becomes
		// leader — e.g. tailscaled not yet up, or the roster file not yet
		// read — turning mere discovery silence into automatic quorum shrink
		// once RemoveAfter elapses for any peer that also looks dead. Refuse
		// until onRoster has actually fired at least once.
		return warden.Node{}, false
	}

	now := m.clock.Now()
	if !m.leaderSeesLiveMajority(now) {
		return warden.Node{}, false
	}

	inRoster := make(map[warden.NodeID]bool, len(m.lastRoster.Nodes))
	for _, n := range m.lastRoster.Nodes {
		inRoster[n.ID] = true
	}

	var best *warden.Node
	for i := range m.membership.Voters {
		v := m.membership.Voters[i]
		if v.ID == m.self.ID || inRoster[v.ID] {
			continue
		}
		lc := m.lastContact[v.ID]
		if m.leaderPeerStatus(now, lc) != warden.StatusDead {
			continue
		}
		ref := m.becameLeaderAt
		if lc.After(ref) {
			ref = lc
		}
		if now.Sub(ref) < m.cfg.RemoveAfter {
			continue
		}
		if best == nil || v.ID < best.ID {
			vv := v
			best = &vv
		}
	}
	if best == nil {
		return warden.Node{}, false
	}
	return *best, true
}

// leaderSeesLiveMajority reports whether the leader currently observes a live
// majority of the CURRENT voter set (self plus StatusAlive voters). This is the
// removal-side analogue of the watchdog isolation guard.
func (m *Manager) leaderSeesLiveMajority(now time.Time) bool {
	live := 0
	for _, v := range m.membership.Voters {
		if v.ID == m.self.ID {
			live++
			continue
		}
		if m.leaderPeerStatus(now, m.lastContact[v.ID]) == warden.StatusAlive {
			live++
		}
	}
	return live >= m.quorum()
}

// applyMembershipChange commits newM: it persists FIRST (with the current term
// and vote) and only adopts in memory on success — a Save failure aborts the
// change so it is retried later, never applied non-durably. On success it
// immediately disseminates via a heartbeat round.
func (m *Manager) applyMembershipChange(newM warden.Membership, action string, target warden.Node) {
	mc := newM.Clone()
	if err := m.store.Save(warden.PersistentState{
		CurrentTerm: m.currentTerm,
		VotedFor:    m.votedFor,
		Membership:  &mc,
	}); err != nil {
		m.log.Error().Err(err).
			Str("node", string(m.self.ID)).
			Str("action", action).
			Str("target", string(target.ID)).
			Uint64("version", newM.Version).
			Msg("warden: failed to persist membership change; aborting (will retry)")
		return
	}

	m.membership = newM.Clone()
	// We trivially hold the new version; an admitted node is no longer a
	// discovery candidate.
	m.ackedVersion[m.self.ID] = ackRef{version: newM.Version, term: newM.CreatedInTerm}
	delete(m.candidates, target.ID)

	m.log.Info().
		Str("node", string(m.self.ID)).
		Str("action", action).
		Str("target", string(target.ID)).
		Uint64("version", newM.Version).
		Int("voters", len(newM.Voters)).
		Int("quorum", m.quorum()).
		Msg("warden: committed membership change")

	m.publish()
	m.sendHeartbeats()
}

// adoptMembership persist-then-adopts a strictly newer membership received from
// the accepted leader. A Save failure keeps the old membership (retry on the
// next heartbeat) and reports false; the caller (onHeartbeat) must fold this
// into the heartbeat's OK so the leader's settle accounting never counts a
// config this node has not durably stored — see the OK-semantics note on
// onHeartbeat. Adopting may change this node's own standing: losing
// voter-ship demotes it to a pure observer; gaining it enters the normal
// follower lifecycle.
func (m *Manager) adoptMembership(newM warden.Membership) bool {
	mc := newM.Clone()
	if err := m.store.Save(warden.PersistentState{
		CurrentTerm: m.currentTerm,
		VotedFor:    m.votedFor,
		Membership:  &mc,
	}); err != nil {
		m.log.Error().Err(err).
			Str("node", string(m.self.ID)).
			Uint64("version", newM.Version).
			Msg("warden: failed to persist adopted membership; keeping old (will retry)")
		return false
	}

	wasVoter := m.selfIsVoter()
	m.membership = newM.Clone()
	nowVoter := m.selfIsVoter()

	switch {
	case wasVoter && !nowVoter:
		// Removed from the voter set: become a pure observer. Abandon any
		// candidacy; the election timer keeps ticking but onElectionTimeout is a
		// no-op for observers, so it never stands for election. The leader
		// learned from the current heartbeat is kept as-is.
		m.role = warden.RoleFollower
		m.votesFrom = nil
		m.log.Info().
			Str("node", string(m.self.ID)).
			Uint64("version", newM.Version).
			Msg("warden: self removed from voters; demoting to observer")
	case !wasVoter && nowVoter:
		// Admitted: begin the normal follower lifecycle. We are already a
		// follower under this leader with a freshly reset election timer, so
		// there is nothing else to arm.
		m.log.Info().
			Str("node", string(m.self.ID)).
			Uint64("version", newM.Version).
			Msg("warden: self admitted to voters; entering follower lifecycle")
	}

	// Any candidate that is now a voter (or self) is no longer a candidate.
	for id := range m.candidates {
		if id == m.self.ID || m.isVoter(id) {
			delete(m.candidates, id)
		}
	}

	return true
}

// candidateRows renders the observer / discovered-but-unverified peer rows for a
// ClusterView. Verified candidates are MemberObserver (with observer liveness);
// unverified ones are MemberDiscovered (StatusUnknown). Returns nil in static
// mode.
func (m *Manager) candidateRows(now time.Time) []warden.PeerView {
	if !m.discoveryMode || len(m.candidates) == 0 {
		return nil
	}
	rows := make([]warden.PeerView, 0, len(m.candidates))
	for _, c := range m.candidates {
		if c.verified {
			rows = append(rows, warden.PeerView{
				Node:     c.node,
				Status:   m.observerStatus(now, c),
				LastSeen: c.lastContact,
				Member:   warden.MemberObserver,
			})
		} else {
			rows = append(rows, warden.PeerView{
				Node:   c.node,
				Status: warden.StatusUnknown,
				Member: warden.MemberDiscovered,
			})
		}
	}
	return rows
}

// observerStatus classifies an observer's liveness from the leader's last
// contact with it, aging alive -> suspect -> dead exactly like a voter. This is
// display-only and never affects quorum or the watchdog.
func (m *Manager) observerStatus(now time.Time, c *candidate) warden.PeerStatus {
	if c.lastContact.IsZero() {
		return warden.StatusUnknown
	}
	age := now.Sub(c.lastContact)
	switch {
	case age >= m.cfg.DeadAfter:
		return warden.StatusDead
	case age >= m.cfg.SuspectAfter:
		return warden.StatusSuspect
	default:
		return warden.StatusAlive
	}
}
