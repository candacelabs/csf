package election

import (
	"time"

	"github.com/candacelabs/csf/services/warden"
)

// onElectionTimeout fires when the election timer elapses. Leaders keep the
// timer armed but ignore it. Followers/candidates start a new election, unless
// the logical deadline was pushed forward by a reset that raced the fire, in
// which case the timer is simply re-armed for the remaining time.
func (m *Manager) onElectionTimeout() {
	now := m.clock.Now()
	if m.role == warden.RoleLeader {
		m.resetElectionTimer(m.randTimeout())
		return
	}
	if m.isObserver() {
		// Pure observers never run for election. Keep the timer harmlessly
		// re-armed (equivalent to having no election timer) so a later
		// admission that makes us a voter finds a live, ticking loop.
		m.resetElectionTimer(m.randTimeout())
		return
	}
	if now.Before(m.electionDeadline) {
		m.resetElectionTimer(m.electionDeadline.Sub(now))
		return
	}
	m.startElection()
}

// startElection advances to the next term, votes for self, persists that
// decision BEFORE proceeding (so a crash can never let the node vote twice in
// one term), and fans out RequestVote to every other peer in parallel.
func (m *Manager) startElection() {
	// Defensive: an observer (self ∉ voters) must never stand for election.
	if m.isObserver() {
		m.resetElectionTimer(m.randTimeout())
		return
	}
	now := m.clock.Now()
	newTerm := m.currentTerm + 1

	// SAFETY: persist (term, self-vote) before self-voting. If it fails we do
	// not start the election; the timer will retry.
	if err := m.saveState(newTerm, m.self.ID); err != nil {
		m.log.Error().Err(err).
			Str("node", string(m.self.ID)).
			Uint64("term", uint64(newTerm)).
			Msg("warden: failed to persist state at election start; not starting election")
		m.resetElectionTimer(m.randTimeout())
		return
	}

	m.currentTerm = newTerm
	m.votedFor = m.self.ID
	m.role = warden.RoleCandidate
	m.leaderID = ""
	m.electionsStarted++
	// Track granted votes as a set keyed by voter so a duplicate or retried
	// reply from the same peer can never double-count toward a false majority.
	m.votesFrom = map[warden.NodeID]bool{m.self.ID: true} // vote for self

	timeout := m.randTimeout()
	m.electionDeadline = now.Add(timeout)
	m.resetElectionTimer(timeout)

	m.log.Info().
		Str("node", string(m.self.ID)).
		Uint64("term", uint64(newTerm)).
		Int("quorum", m.quorum()).
		Msg("warden: starting election")

	m.publish()

	term := m.currentTerm
	self := m.self.ID
	// Solicit votes ONLY from other voters in the current membership.
	for _, p := range m.voterPeers() {
		p := p
		m.spawnRPC(func() {
			ctx, cancel := m.rpcContext()
			defer cancel()
			resp, err := m.transport.RequestVote(ctx, p, warden.VoteRequest{Term: term, CandidateID: self})
			m.deliver(voteResultMsg{term: term, from: p.ID, resp: resp, err: err})
		})
	}

	// A single-node cluster (or any cluster whose quorum is 1) wins outright.
	if len(m.votesFrom) >= m.quorum() {
		m.becomeLeader()
	}
}

// onVoteResult tallies a RequestVote reply. Stale replies (wrong term or no
// longer a candidate) are ignored; a higher term forces a step-down; a
// majority promotes to leader.
func (m *Manager) onVoteResult(e voteResultMsg) {
	if m.role != warden.RoleCandidate || e.term != m.currentTerm {
		return
	}
	if e.err != nil {
		return // unreachable/malformed: counts as a vote not granted
	}
	if e.resp.Term > m.currentTerm {
		m.stepDown(e.resp.Term)
		return
	}
	if !e.resp.Granted {
		return
	}
	// Count a grant ONLY from a node that is a voter in the CURRENT membership.
	// A vote from a node that has since been removed (or was never a voter)
	// must never contribute to quorum.
	if !m.isVoter(e.from) {
		return
	}
	m.votesFrom[e.from] = true
	if len(m.votesFrom) >= m.quorum() {
		m.becomeLeader()
	}
}

// becomeLeader transitions to leader and immediately heartbeats. The term and
// vote were already persisted at candidacy, so no additional Save is needed.
func (m *Manager) becomeLeader() {
	now := m.clock.Now()
	m.role = warden.RoleLeader
	m.leaderID = m.self.ID
	m.becameLeaderAt = now
	// Reset liveness: peers start Unknown and are promoted to Alive as their
	// first heartbeat responses arrive.
	m.lastContact = make(map[warden.NodeID]time.Time)
	m.latencyMS = make(map[warden.NodeID]float64)
	if m.discoveryMode {
		// Fresh ack ledger for the one-change-at-a-time SETTLE rule: we trivially
		// hold our own current membership version.
		m.ackedVersion = map[warden.NodeID]ackRef{m.self.ID: {version: m.membership.Version, term: m.membership.CreatedInTerm}}
	}

	m.log.Info().
		Str("node", string(m.self.ID)).
		Uint64("term", uint64(m.currentTerm)).
		Msg("warden: became leader")

	m.publish()
	m.sendHeartbeats()
	// A newly elected leader may already have an admission/removal to make.
	if m.discoveryMode {
		m.maybeStartMembershipChange()
	}
}

// stepDown adopts a strictly higher term seen on an RPC reply and reverts to
// follower, persisting the new term (with the vote cleared) first.
func (m *Manager) stepDown(newTerm warden.Term) {
	now := m.clock.Now()
	if err := m.saveState(newTerm, ""); err != nil {
		m.log.Error().Err(err).
			Str("node", string(m.self.ID)).
			Uint64("term", uint64(newTerm)).
			Msg("warden: failed to persist state on step-down; stepping down anyway")
	}
	m.currentTerm = newTerm
	m.votedFor = ""
	m.role = warden.RoleFollower
	m.leaderID = ""

	timeout := m.randTimeout()
	m.electionDeadline = now.Add(timeout)
	m.resetElectionTimer(timeout)

	m.log.Info().
		Str("node", string(m.self.ID)).
		Uint64("term", uint64(newTerm)).
		Msg("warden: stepped down to follower")

	m.publish()
}

// onVote decides a RequestVote. It grants at most one vote per term, only to
// a candidate in the CURRENT voter set, and persists (term, vote) BEFORE
// granting, so the promise survives a restart.
func (m *Manager) onVote(req warden.VoteRequest) warden.VoteResponse {
	now := m.clock.Now()

	// A pure observer (self ∉ voters) never grants a vote. It plays no part in
	// quorum and must not influence any election.
	if m.isObserver() {
		return warden.VoteResponse{Term: m.currentTerm, Granted: false, VoterID: m.self.ID}
	}

	if req.Term < m.currentTerm {
		return warden.VoteResponse{Term: m.currentTerm, Granted: false, VoterID: m.self.ID}
	}

	// A request for a newer term resets our vote for that term.
	termToUse := m.currentTerm
	effVotedFor := m.votedFor
	if req.Term > m.currentTerm {
		termToUse = req.Term
		effVotedFor = ""
	}

	// A candidate outside the CURRENT voter set (an observer, a not-yet-admitted
	// joiner, or a since-removed voter) can never legitimately reach quorum —
	// voterPeers()/onVoteResult already restrict candidacy and grant-counting to
	// current voters — so granting it a vote would burn this voter's one
	// vote-per-term on a candidacy that can never win, potentially blocking a
	// legitimate candidate for the rest of the term. Refuse outright; the branch
	// below still adopts a higher term (Raft term monotonicity) without
	// recording a vote, so the slot stays free for a real candidate.
	grant := m.isVoter(req.CandidateID) && (effVotedFor == "" || effVotedFor == req.CandidateID)
	if grant {
		if err := m.saveState(termToUse, req.CandidateID); err != nil {
			m.log.Error().Err(err).
				Str("node", string(m.self.ID)).
				Str("candidate", string(req.CandidateID)).
				Uint64("term", uint64(termToUse)).
				Msg("warden: failed to persist vote; refusing")
			return warden.VoteResponse{Term: m.currentTerm, Granted: false, VoterID: m.self.ID}
		}
		m.currentTerm = termToUse
		m.votedFor = req.CandidateID
		m.role = warden.RoleFollower
		m.leaderID = ""

		timeout := m.randTimeout()
		m.electionDeadline = now.Add(timeout)
		m.resetElectionTimer(timeout)

		m.log.Debug().
			Str("node", string(m.self.ID)).
			Str("candidate", string(req.CandidateID)).
			Uint64("term", uint64(termToUse)).
			Msg("warden: granted vote")

		m.publish()
		return warden.VoteResponse{Term: m.currentTerm, Granted: true, VoterID: m.self.ID}
	}

	// Not granting. If the request nonetheless carried a higher term we must
	// still adopt it and step down (Raft term rule) — WITHOUT recording a vote,
	// so the term's vote slot remains free for a legitimate candidate. This
	// branch is reached whenever a higher-term request is refused only because
	// its candidate is not a current voter (the isVoter check above); it is
	// otherwise unreachable, since a higher term clears effVotedFor.
	if req.Term > m.currentTerm {
		if err := m.saveState(req.Term, ""); err != nil {
			m.log.Error().Err(err).
				Str("node", string(m.self.ID)).
				Uint64("term", uint64(req.Term)).
				Msg("warden: failed to persist adopted term")
			return warden.VoteResponse{Term: m.currentTerm, Granted: false, VoterID: m.self.ID}
		}
		m.currentTerm = req.Term
		m.votedFor = ""
		m.role = warden.RoleFollower
		m.leaderID = ""

		timeout := m.randTimeout()
		m.electionDeadline = now.Add(timeout)
		m.resetElectionTimer(timeout)
		m.publish()
	}

	return warden.VoteResponse{Term: m.currentTerm, Granted: false, VoterID: m.self.ID}
}
