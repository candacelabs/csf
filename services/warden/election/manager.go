// Package election implements Raft-style leader election (terms and votes, no
// log replication) over a static peer set, plus the peer-liveness tracking
// that feeds the cluster ClusterView.
//
// A Manager is both the warden.IRPCHandler a node answers cluster RPCs with and
// the warden.IViewSource its dashboard, metrics, and watchdog read. Callers may
// rely on the two safety properties that make the rest of the service simple:
// quorum is always computed over the persisted voter set (Membership.Voters),
// never over a discovery roster or over "peers we can currently reach", so a
// quiet or broken discovery source cannot shrink the denominator; and only a
// leader admits or removes a member, one change at a time. Every view handed
// out is an immutable snapshot the caller may keep. Durable term-and-vote state
// goes through warden.IStore before a vote is granted, so a restart cannot vote
// twice in one term.
//
// # Concurrency model
//
// A Manager is a CSP actor: Run(ctx) is a single event loop that exclusively
// owns all election, role, and liveness state. Nothing else ever reads or
// mutates that state. Every external interaction is a typed message placed on
// the loop's inbound channel:
//
//   - HandleVote / HandleHeartbeat (the warden.IRPCHandler methods, invoked
//     from HTTP handler goroutines) wrap the request with a per-request reply
//     channel and wait for the loop to answer, honoring both the caller ctx
//     and loop shutdown so a handler never leaks or hangs.
//   - View / Subscribe (the warden.IViewSource methods) are queries into the
//     loop returning immutable snapshots.
//   - Outbound vote/heartbeat RPCs run in short-lived worker goroutines the
//     loop spawns; they never touch state, reporting their results back to the
//     loop as messages. They are tracked so Run does not return until every
//     one has exited.
//
// Timers and tickers come from warden.IClock and are composed directly into the
// loop's select, which keeps the whole machine testable with a simulated
// clock.
package election

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/candacelabs/csf/services/warden"
)

// eventBuffer sizes the loop inbound channel. RPC volume is tiny (a handful of
// peers), but a buffer keeps outbound-result delivery from blocking the worker
// goroutines while the loop is momentarily busy.
const eventBuffer = 256

// HandleIdentify implements warden.IRPCHandler. It reads only immutable
// construction-time configuration, so it answers directly without a loop
// round-trip.
func (m *Manager) HandleIdentify(ctx context.Context) warden.IdentifyResponse {
	return warden.IdentifyResponse{
		ClusterID: m.cfg.ClusterID,
		NodeID:    m.self.ID,
		Version:   m.cfg.BuildVersion,
	}
}

// ackRef is the (Version, CreatedInTerm) identity of a membership as acked by
// a voter. See Manager.ackedVersion.
type ackRef struct {
	version uint64
	term    warden.Term
}

// newerAck reports whether the (v, t) pair is lexicographically newer than a.
func (a ackRef) newerAck(v uint64, t warden.Term) bool {
	if v != a.version {
		return v > a.version
	}
	return t > a.term
}

// interface assertions.
var (
	_ warden.IRPCHandler = (*Manager)(nil)
	_ warden.IViewSource = (*Manager)(nil)
)

// Manager runs the election state machine for one node. Construct with
// NewManager; drive with Run.
type Manager struct {
	// Immutable after construction.
	cfg             Config
	self            warden.Node
	discoveryMode   bool // cfg.Discoverer != nil
	publishInterval time.Duration
	transport       warden.ITransport
	store           warden.IStore
	clock           warden.IClock
	log             *zerolog.Logger

	// baseCtx is the Run context; outbound RPC contexts derive from it.
	baseCtx context.Context

	// ---- Loop-owned state (touched only by the Run goroutine) ----
	rng *rand.Rand // seeded per-node; only the loop calls it

	// membership is the effective voting configuration — the single source of
	// truth for quorum and for who may vote/lead. In static mode it is
	// {Version 0, Voters == cfg.Peers} and never changes. In discovery mode it
	// is seeded (from persisted state or cfg.Peers), grown/shrunk one node at a
	// time by the leader, and adopted from leader heartbeats. Quorum is ALWAYS
	// warden.Quorum(len(membership.Voters)); unreachability never changes it.
	membership warden.Membership

	currentTerm      warden.Term
	votedFor         warden.NodeID
	role             warden.Role
	leaderID         warden.NodeID
	electionDeadline time.Time
	electionsStarted uint64
	votesFrom        map[warden.NodeID]bool

	becameLeaderAt time.Time
	lastContact    map[warden.NodeID]time.Time // leader: last successful heartbeat response
	latencyMS      map[warden.NodeID]float64   // leader: last heartbeat RTT

	// ---- Discovery / dynamic-membership state (discovery mode only) ----
	// lastRoster is the most recent discovery snapshot; channel silence keeps
	// it (never treated as an empty roster).
	lastRoster warden.Roster
	// haveRoster is set the first time onRoster ever fires. Before that, a
	// voter's absence from lastRoster.Nodes (whose zero value is indistinguishable
	// from a real, explicitly-empty roster) must NOT be read as "confirmed gone" —
	// nextRemoval gates on this so a leader that has not yet heard from discovery
	// at all (source not yet up, or still on its first poll) can never treat
	// every voter as roster-absent and remove a genuinely-alive-but-undiscovered
	// peer. See nextRemoval.
	haveRoster bool
	// candidates tracks non-voter nodes reported by discovery, keyed by ID.
	candidates map[warden.NodeID]*candidate
	// ackedVersion is the leader-only per-voter highest-acked membership
	// identity, keyed by the (Version, CreatedInTerm) pair — never the bare
	// version number, so an ack for a divergent sibling config (same Version
	// minted by a deposed leader in an older term) can never count toward
	// settling the current config.
	// acked via a HeartbeatResponse OK. It backs the one-change-at-a-time
	// SETTLE rule (a change settles when a majority of the NEW voters ack it).
	ackedVersion map[warden.NodeID]ackRef
	// rosterCh is the live discovery channel consumed by the loop; nil in
	// static mode or once the discoverer closes it.
	rosterCh <-chan warden.Roster

	cachedView        *warden.ClusterView // follower: last leader view received
	cachedViewAt      time.Time
	lastLeaderContact time.Time // follower: last accepted heartbeat receipt

	electionTimer   warden.Timer
	heartbeatTicker warden.Ticker
	publishTicker   warden.Ticker

	subs      map[int]chan warden.ClusterView
	nextSubID int

	// ---- Cross-goroutine plumbing ----
	events chan any
	done   chan struct{}
	rpc    *inflightTracker

	// activity counts state-changing events processed by the loop. It exists
	// only so the deterministic test harness can detect quiescence; it is an
	// atomic because the harness reads it from another goroutine.
	activity atomic.Uint64
}

// ---- Inbound message types ----

type voteMsg struct {
	req   warden.VoteRequest
	reply chan warden.VoteResponse
}

type heartbeatMsg struct {
	req   warden.HeartbeatRequest
	reply chan warden.HeartbeatResponse
}

type voteResultMsg struct {
	term warden.Term // candidate term when the request was sent
	from warden.NodeID
	resp warden.VoteResponse
	err  error
}

type heartbeatResultMsg struct {
	term     warden.Term // leader term when the heartbeat was sent
	peer     warden.Node
	resp     warden.HeartbeatResponse
	err      error
	sentAt   time.Time
	rtt      time.Duration
	mversion uint64      // membership version carried in the heartbeat (for the ack tracker)
	mterm    warden.Term // CreatedInTerm of that membership (ack identity is the pair)
}

// identifyResultMsg reports the outcome of an identify probe of a discovered
// node back to the loop.
type identifyResultMsg struct {
	node warden.Node
	resp warden.IdentifyResponse
	err  error
}

type viewMsg struct {
	reply chan warden.ClusterView
}

type subscribeMsg struct {
	ch    chan warden.ClusterView
	reply chan int
}

type unsubscribeMsg struct {
	id int
}

// barrierMsg is a test-only synchronization primitive: the loop drains every
// currently-ready timer/message before replying, giving the harness a
// deterministic "the loop has caught up" signal.
type barrierMsg struct {
	done chan struct{}
}

// Run is the blocking main loop. It owns all state until ctx is done, at which
// point it stops its timers, waits for every worker goroutine it spawned to
// exit, closes all subscriber channels, and returns nil.
func (m *Manager) Run(ctx context.Context) error {
	m.baseCtx = ctx

	// Discovery mode: open the roster channel and consume it in the select. A
	// discoverer error is non-fatal — the node runs on its seed/persisted
	// membership until discovery recovers (consumers never fall back to an
	// empty set).
	if m.discoveryMode {
		ch, err := m.cfg.Discoverer.Discover(ctx)
		if err != nil {
			m.log.Error().Err(err).Str("node", string(m.self.ID)).
				Msg("warden: peer discovery unavailable at start; running on seed membership")
		} else {
			m.rosterCh = ch
		}
	}

	now := m.clock.Now()
	timeout := m.randTimeout()
	m.electionDeadline = now.Add(timeout)
	m.electionTimer = m.clock.NewTimer(timeout)
	m.heartbeatTicker = m.clock.NewTicker(m.cfg.HeartbeatInterval)
	m.publishTicker = m.clock.NewTicker(m.publishInterval)

	defer m.shutdown()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-m.events:
			m.handleEvent(ev)
		case r, ok := <-m.rosterCh:
			// A nil m.rosterCh (static mode / closed) never selects.
			if !ok {
				m.rosterCh = nil
				continue
			}
			m.onRoster(r)
			m.activity.Add(1)
		case <-m.electionTimer.C:
			m.onElectionTimeout()
			m.activity.Add(1)
		case <-m.heartbeatTicker.C:
			m.onHeartbeatTick()
			m.activity.Add(1)
		case <-m.publishTicker.C:
			m.publish()
			m.activity.Add(1)
		}
	}
}

// shutdown runs (deferred) in the loop goroutine when Run returns. Closing
// done first unblocks any handler waiting on a reply and any worker trying to
// deliver a result; only then do we wait for the workers to exit.
func (m *Manager) shutdown() {
	close(m.done)
	m.rpc.wait()
	// The guard is on the function field rather than on the value: a Manager
	// that never reached the arming block in Run holds zero Timers/Tickers,
	// whose Stop is nil.
	if m.electionTimer.Stop != nil {
		m.electionTimer.Stop()
	}
	if m.heartbeatTicker.Stop != nil {
		m.heartbeatTicker.Stop()
	}
	if m.publishTicker.Stop != nil {
		m.publishTicker.Stop()
	}
	for id, ch := range m.subs {
		delete(m.subs, id)
		close(ch)
	}
}

// handleEvent dispatches one inbound message. State-changing messages bump the
// activity counter; pure queries (view/subscribe) do not.
func (m *Manager) handleEvent(ev any) {
	switch e := ev.(type) {
	case voteMsg:
		e.reply <- m.onVote(e.req)
		m.activity.Add(1)
	case heartbeatMsg:
		e.reply <- m.onHeartbeat(e.req)
		m.activity.Add(1)
	case voteResultMsg:
		m.onVoteResult(e)
		m.activity.Add(1)
	case heartbeatResultMsg:
		m.onHeartbeatResult(e)
		m.activity.Add(1)
	case identifyResultMsg:
		m.onIdentifyResult(e)
		m.activity.Add(1)
	case viewMsg:
		e.reply <- m.snapshotView()
	case subscribeMsg:
		m.onSubscribe(e)
	case unsubscribeMsg:
		m.onUnsubscribe(e)
	case barrierMsg:
		m.drainReady()
		close(e.done)
	}
}

// drainReady processes every timer fire and inbound message that is ready
// right now, then returns once nothing more is immediately available. It backs
// the test barrier and guarantees an idle loop after Advance has delivered its
// fires.
func (m *Manager) drainReady() {
	for {
		select {
		case ev := <-m.events:
			m.handleEvent(ev)
		case r, ok := <-m.rosterCh:
			if !ok {
				m.rosterCh = nil
				continue
			}
			m.onRoster(r)
			m.activity.Add(1)
		case <-m.electionTimer.C:
			m.onElectionTimeout()
			m.activity.Add(1)
		case <-m.heartbeatTicker.C:
			m.onHeartbeatTick()
			m.activity.Add(1)
		case <-m.publishTicker.C:
			m.publish()
			m.activity.Add(1)
		default:
			return
		}
	}
}

// ---- warden.IRPCHandler ----

func waitForReply[T any](ctx context.Context, done <-chan struct{}, reply <-chan T, fallback T) T {
	select {
	case response := <-reply:
		return response
	case <-ctx.Done():
		return fallback
	case <-done:
		return fallback
	}
}

// HandleVote answers a VoteRequest by round-tripping it through the loop. It
// returns a safe "not granted" response if the caller ctx is canceled or the
// Manager shuts down before the loop replies.
func (m *Manager) HandleVote(ctx context.Context, req warden.VoteRequest) warden.VoteResponse {
	fallback := warden.VoteResponse{Granted: false, VoterID: m.self.ID}
	reply := make(chan warden.VoteResponse, 1)
	select {
	case m.events <- voteMsg{req: req, reply: reply}:
	case <-ctx.Done():
		return fallback
	case <-m.done:
		return fallback
	}
	return waitForReply(ctx, m.done, reply, fallback)
}

// HandleHeartbeat answers a HeartbeatRequest by round-tripping it through the
// loop. It returns OK=false if the caller ctx is canceled or the Manager shuts
// down before the loop replies.
func (m *Manager) HandleHeartbeat(ctx context.Context, req warden.HeartbeatRequest) warden.HeartbeatResponse {
	fallback := warden.HeartbeatResponse{OK: false, NodeID: m.self.ID}
	reply := make(chan warden.HeartbeatResponse, 1)
	select {
	case m.events <- heartbeatMsg{req: req, reply: reply}:
	case <-ctx.Done():
		return fallback
	case <-m.done:
		return fallback
	}
	return waitForReply(ctx, m.done, reply, fallback)
}

// ---- warden.IViewSource ----

// View returns the current cluster view snapshot. The returned value is a copy
// the caller may keep and mutate.
func (m *Manager) View() warden.ClusterView {
	reply := make(chan warden.ClusterView, 1)
	select {
	case m.events <- viewMsg{reply: reply}:
	case <-m.done:
		return m.offlineView()
	}
	select {
	case v := <-reply:
		return v
	case <-m.done:
		return m.offlineView()
	}
}

// offlineView is returned when the loop is not running. It exposes only the
// immutable identity of the node and the last-known membership (safe to read:
// the loop closes m.done after its final mutation, establishing happens-before
// for any goroutine that observes the close before calling this).
func (m *Manager) offlineView() warden.ClusterView {
	return warden.ClusterView{
		Self:       m.self.ID,
		Role:       warden.RoleFollower,
		Source:     m.self.ID,
		Membership: m.membership.Clone(),
	}
}

// Subscribe registers a buffered channel that receives view snapshots after
// state changes and on the periodic publish tick. Delivery is best-effort
// (dropped when the channel is full). cancel unsubscribes and closes the
// channel; it is safe to call multiple times.
func (m *Manager) Subscribe(buf int) (<-chan warden.ClusterView, func()) {
	if buf < 0 {
		buf = 0
	}
	ch := make(chan warden.ClusterView, buf)
	reply := make(chan int, 1)
	select {
	case m.events <- subscribeMsg{ch: ch, reply: reply}:
	case <-m.done:
		close(ch)
		return ch, func() {}
	}

	var id int
	select {
	case id = <-reply:
	case <-m.done:
		// The loop shut down before registering; it never took ownership of
		// ch, so we close it here.
		close(ch)
		return ch, func() {}
	}

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			select {
			case m.events <- unsubscribeMsg{id: id}:
			case <-m.done:
			}
		})
	}
	return ch, cancel
}

// ---- helpers ----

// deliver posts a worker result to the loop, bailing out if the Manager has
// shut down so no worker goroutine can leak.
func (m *Manager) deliver(ev any) {
	select {
	case m.events <- ev:
	case <-m.done:
	}
}

// spawnRPC runs f in a tracked worker goroutine.
func (m *Manager) spawnRPC(f func()) {
	m.rpc.add(1)
	go func() {
		defer m.rpc.done()
		f()
	}()
}

// rpcContext derives a per-RPC context from the Run context with the
// configured timeout.
func (m *Manager) rpcContext() (context.Context, context.CancelFunc) {
	base := m.baseCtx
	if base == nil {
		base = context.Background()
	}
	return context.WithTimeout(base, m.cfg.RPCTimeout)
}

// quorum is the majority threshold over the CURRENT voter set. It is recomputed
// from membership on every call so a membership change immediately moves the
// bar; unreachability never enters into it.
func (m *Manager) quorum() int { return warden.Quorum(len(m.membership.Voters)) }

// isVoter reports whether id is in the current voting configuration.
func (m *Manager) isVoter(id warden.NodeID) bool { return m.membership.HasVoter(id) }

// selfIsVoter reports whether this node is currently a voter.
func (m *Manager) selfIsVoter() bool { return m.membership.HasVoter(m.self.ID) }

// isObserver reports whether this node is a pure observer: discovery mode and
// not (yet / any longer) a voter. Observers run no election timer, never become
// candidates, and never grant votes.
func (m *Manager) isObserver() bool { return m.discoveryMode && !m.membership.HasVoter(m.self.ID) }

// voterPeers returns the current voters excluding self — the peers a candidate
// solicits votes from and a leader must reach for quorum.
func (m *Manager) voterPeers() []warden.Node {
	out := make([]warden.Node, 0, len(m.membership.Voters))
	for _, v := range m.membership.Voters {
		if v.ID != m.self.ID {
			out = append(out, v)
		}
	}
	return out
}

// saveState persists (term, votedFor) together with the current effective
// membership (discovery mode only; nil in static mode so behavior and on-disk
// format are unchanged). Every Save routes through here so a term/vote write
// can never silently drop the persisted membership.
func (m *Manager) saveState(term warden.Term, votedFor warden.NodeID) error {
	ps := warden.PersistentState{CurrentTerm: term, VotedFor: votedFor}
	if m.discoveryMode {
		mc := m.membership.Clone()
		ps.Membership = &mc
	}
	return m.store.Save(ps)
}

// randTimeout returns a randomized election timeout in
// [ElectionTimeoutMin, ElectionTimeoutMax). Only the loop calls it.
func (m *Manager) randTimeout() time.Duration {
	span := m.cfg.ElectionTimeoutMax - m.cfg.ElectionTimeoutMin
	if span <= 0 {
		return m.cfg.ElectionTimeoutMin
	}
	return m.cfg.ElectionTimeoutMin + time.Duration(m.rng.Int63n(int64(span)))
}

// resetElectionTimer safely re-arms the election timer to duration d. Only the
// loop calls this, so the stop/drain/reset sequence has no cross-goroutine
// race.
func (m *Manager) resetElectionTimer(d time.Duration) {
	if d < 0 {
		d = 0
	}
	if !m.electionTimer.Stop() {
		select {
		case <-m.electionTimer.C:
		default:
		}
	}
	m.electionTimer.Reset(d)
}

// barrier is a test-only helper: it blocks until the loop has drained every
// currently-ready timer/message.
func (m *Manager) barrier() {
	done := make(chan struct{})
	select {
	case m.events <- barrierMsg{done: done}:
	case <-m.done:
		return
	}
	select {
	case <-done:
	case <-m.done:
	}
}
