package election

import (
	"time"

	"github.com/candacelabs/csf/services/warden"
)

// snapshotView builds the current ClusterView. A leader produces an
// authoritative view from its own liveness tracking; other roles return a
// cached leader view (if fresh) or a local, non-authoritative fallback.
func (m *Manager) snapshotView() warden.ClusterView {
	now := m.clock.Now()
	if m.role == warden.RoleLeader {
		return m.leaderView(now)
	}
	return m.followerView(now)
}

// leaderView is the authoritative view. Self (a voter) is always Alive; every
// other voter is classified from the leader's last-contact tracking; observers
// and discovered-but-unverified nodes are appended with their MemberKind.
func (m *Manager) leaderView(now time.Time) warden.ClusterView {
	peers := make([]warden.PeerView, 0, len(m.membership.Voters)+len(m.candidates))
	for _, p := range m.membership.Voters {
		if p.ID == m.self.ID {
			peers = append(peers, warden.PeerView{
				Node:     p,
				Status:   warden.StatusAlive,
				LastSeen: now,
				Member:   warden.MemberVoter,
			})
			continue
		}
		lc := m.lastContact[p.ID]
		peers = append(peers, warden.PeerView{
			Node:      p,
			Status:    m.leaderPeerStatus(now, lc),
			LastSeen:  lc,
			LatencyMS: m.latencyMS[p.ID],
			Member:    warden.MemberVoter,
		})
	}
	peers = append(peers, m.candidateRows(now)...)
	warden.SortPeers(peers)
	return warden.ClusterView{
		Self:             m.self.ID,
		Role:             warden.RoleLeader,
		Term:             m.currentTerm,
		LeaderID:         m.self.ID,
		Source:           m.self.ID,
		Authoritative:    true,
		UpdatedAt:        now,
		Peers:            peers,
		ElectionsStarted: m.electionsStarted,
		Membership:       m.membership.Clone(),
	}
}

// leaderPeerStatus classifies a peer using max(lastContact, becameLeaderAt) as
// the reference time. A peer never contacted since this leader took over is
// Unknown only while less than SuspectAfter has elapsed since it took over;
// after that it ages through Suspect and Dead exactly as a lapsed peer would.
// This guarantees a fresh leader marks an already-down peer Dead within
// DeadAfter of taking over.
func (m *Manager) leaderPeerStatus(now time.Time, lastContact time.Time) warden.PeerStatus {
	if lastContact.IsZero() && now.Sub(m.becameLeaderAt) < m.cfg.SuspectAfter {
		return warden.StatusUnknown
	}
	ref := m.becameLeaderAt
	if lastContact.After(ref) {
		ref = lastContact
	}
	age := now.Sub(ref)
	switch {
	case age >= m.cfg.DeadAfter:
		return warden.StatusDead
	case age >= m.cfg.SuspectAfter:
		return warden.StatusSuspect
	default:
		return warden.StatusAlive
	}
}

// followerView returns the cached authoritative leader view when it is fresh
// and still names the current leader, adapted to this node's identity;
// otherwise it returns a local, non-authoritative view built from this node's
// own heartbeat receipts.
func (m *Manager) followerView(now time.Time) warden.ClusterView {
	if m.cachedView != nil && m.leaderID != "" &&
		m.cachedView.LeaderID == m.leaderID &&
		now.Sub(m.cachedViewAt) < m.cfg.ViewFreshFor {
		v := copyView(*m.cachedView)
		v.Self = m.self.ID
		v.Role = m.role
		v.Term = m.currentTerm
		v.ElectionsStarted = m.electionsStarted
		// Authoritative, Source (leader), LeaderID, Peers, UpdatedAt are kept
		// from the leader's snapshot (which already labels every Member). Report
		// the membership this node is actually operating under.
		v.Membership = m.membership.Clone()
		return v
	}

	peers := make([]warden.PeerView, 0, len(m.membership.Voters)+len(m.candidates)+1)
	for _, p := range m.membership.Voters {
		switch {
		case p.ID == m.self.ID:
			peers = append(peers, warden.PeerView{Node: p, Status: warden.StatusAlive, LastSeen: now, Member: warden.MemberVoter})
		case m.leaderID != "" && p.ID == m.leaderID:
			peers = append(peers, warden.PeerView{
				Node:     p,
				Status:   m.followerLeaderStatus(now),
				LastSeen: m.lastLeaderContact,
				Member:   warden.MemberVoter,
			})
		default:
			peers = append(peers, warden.PeerView{Node: p, Status: warden.StatusUnknown, Member: warden.MemberVoter})
		}
	}
	// An observer is not in Voters; include it as its own MemberObserver row.
	if !m.selfIsVoter() {
		peers = append(peers, warden.PeerView{Node: m.self, Status: warden.StatusAlive, LastSeen: now, Member: warden.MemberObserver})
	}
	peers = append(peers, m.candidateRows(now)...)
	warden.SortPeers(peers)
	return warden.ClusterView{
		Self:             m.self.ID,
		Role:             m.role,
		Term:             m.currentTerm,
		LeaderID:         m.leaderID,
		Source:           m.self.ID,
		Authoritative:    false,
		UpdatedAt:        now,
		Peers:            peers,
		ElectionsStarted: m.electionsStarted,
		Membership:       m.membership.Clone(),
	}
}

// followerLeaderStatus classifies the leader from this follower's own
// heartbeat-receipt timestamps.
func (m *Manager) followerLeaderStatus(now time.Time) warden.PeerStatus {
	if m.lastLeaderContact.IsZero() {
		return warden.StatusUnknown
	}
	age := now.Sub(m.lastLeaderContact)
	switch {
	case age >= m.cfg.DeadAfter:
		return warden.StatusDead
	case age >= m.cfg.SuspectAfter:
		return warden.StatusSuspect
	default:
		return warden.StatusAlive
	}
}

// copyView returns a deep copy of v (its Peers slice is duplicated) so the
// snapshot is safe to hand out and to cache.
func copyView(v warden.ClusterView) warden.ClusterView {
	cp := v
	cp.Peers = make([]warden.PeerView, len(v.Peers))
	copy(cp.Peers, v.Peers)
	return cp
}

// publish sends the current snapshot to every subscriber with a non-blocking
// send, so a slow subscriber never stalls the loop. All subscribers receive
// the same snapshot value and therefore share one backing Peers array; the
// ViewSource contract makes snapshots read-only, so this sharing is safe.
func (m *Manager) publish() {
	if len(m.subs) == 0 {
		return
	}
	view := m.snapshotView()
	for _, ch := range m.subs {
		select {
		case ch <- view:
		default:
		}
	}
}

// onSubscribe registers a new subscriber and immediately sends it a snapshot.
// The snapshot is enqueued before the id is returned so that, for a buffered
// channel, the initial snapshot is guaranteed visible by the time Subscribe
// returns.
func (m *Manager) onSubscribe(e subscribeMsg) {
	m.nextSubID++
	id := m.nextSubID
	m.subs[id] = e.ch
	select {
	case e.ch <- m.snapshotView():
	default:
	}
	e.reply <- id
}

// onUnsubscribe removes a subscriber and closes its channel.
func (m *Manager) onUnsubscribe(e unsubscribeMsg) {
	if ch, ok := m.subs[e.id]; ok {
		delete(m.subs, e.id)
		close(ch)
	}
}
