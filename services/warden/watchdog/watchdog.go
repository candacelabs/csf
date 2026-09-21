package watchdog

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/candacelabs/csf/pkg/core"
	"github.com/candacelabs/csf/services/warden"
)

// Default configuration values, applied by New when a field is left zero.
const (
	defaultCooldown      = 10 * time.Minute
	defaultMaxIncidents  = 100
	defaultCheckInterval = time.Second
)

const (
	// subscribeBuffer is the ViewSource subscription buffer. View delivery is
	// best-effort; a full buffer drops intermediate updates and the ticker
	// re-evaluation still catches the latest state.
	subscribeBuffer = 16
	// notifyResultBuffer lets finished delivery goroutines report without
	// blocking the loop under a burst of concurrent notifications.
	notifyResultBuffer = 64
)

// Config tunes the watchdog. The zero value is valid; New substitutes
// defaults for any zero field.
type Config struct {
	// Cooldown is the minimum interval between notifications for the same
	// (peer, incident type). Defaults to 10m when zero. The incident is still
	// recorded in the log while suppressed; only the Notify call is skipped.
	Cooldown time.Duration
	// NotifyRecovery enables peer_recovered notifications. Recovery incidents
	// are always recorded in the log regardless of this flag.
	NotifyRecovery bool
	// MaxIncidents is the ring-buffer capacity for the dashboard incident log.
	// Defaults to 100 when zero or negative.
	MaxIncidents int
	// CheckInterval is the periodic re-evaluation tick. Defaults to 1s when
	// zero. It also sets the retry cadence for failed notifications.
	CheckInterval time.Duration
}

// Watchdog is the leader-only incident engine. Construct it with New and drive
// it with Run. It implements warden.IIncidentLog.
type Watchdog struct {
	cfg      Config
	src      warden.IViewSource
	notifier warden.INotifier
	clock    warden.IClock

	// ring is the only state shared beyond the Run loop; see incidentRing.
	ring *incidentRing
}

var _ warden.IIncidentLog = (*Watchdog)(nil)

// New builds a Watchdog. src supplies cluster views, notifier delivers
// incidents, and clock drives timing (inject a fake clock in tests).
func New(cfg Config, src warden.IViewSource, notifier warden.INotifier, clock warden.IClock) *Watchdog {
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = defaultCooldown
	}
	if cfg.MaxIncidents <= 0 {
		cfg.MaxIncidents = defaultMaxIncidents
	}
	if cfg.CheckInterval <= 0 {
		cfg.CheckInterval = defaultCheckInterval
	}
	return &Watchdog{
		cfg:      cfg,
		src:      src,
		notifier: notifier,
		clock:    clock,
		ring:     newIncidentRing(cfg.MaxIncidents),
	}
}

// Incidents returns a defensive copy of the recorded incident log, most
// recent first. It is safe to call from any goroutine at any time — before
// Run starts, while it runs, or after it exits — and never blocks on the loop.
func (w *Watchdog) Incidents() []warden.Incident {
	return w.ring.snapshot()
}

// cooldownKey identifies a notification-dedup bucket: one per affected peer
// and incident type.
type cooldownKey struct {
	peer warden.NodeID
	typ  warden.IncidentType
}

// episode is an open (unrecovered) dead episode for a peer.
type episode struct {
	peer     warden.Node
	openedAt time.Time // when the leader detected death (clock time)
	lastSeen time.Time // leader's last contact before death
	term     warden.Term
}

// loopState holds all watchdog decision state. It is created and mutated
// exclusively by the Run loop (and, in white-box tests, by the test goroutine
// driving evaluate/handleResult directly) — never concurrently.
type loopState struct {
	// open tracks peers with an unrecovered dead episode.
	open map[warden.NodeID]*episode
	// cooldowns records the time of the last successful notification per
	// (peer, type). Survives leadership epoch changes so a flapping process
	// does not re-alert.
	cooldowns map[cooldownKey]time.Time
	// pending holds incidents that should be delivered but have not yet been
	// successfully sent; failed deliveries stay here for retry.
	pending []warden.Incident
	// inFlight guards against dispatching a second delivery goroutine for an
	// incident whose first attempt has not yet reported a result.
	inFlight map[string]bool
	// wasLeader is the previous evaluation's leadership gate result, used to
	// detect leadership transitions.
	wasLeader bool
	// quorumLost records that the isolation guard is currently suppressing
	// evaluation (used to log the transition once instead of every tick).
	quorumLost bool
}

func newLoopState() *loopState {
	return &loopState{
		open:      make(map[warden.NodeID]*episode),
		cooldowns: make(map[cooldownKey]time.Time),
		inFlight:  make(map[string]bool),
	}
}

// notifyResult is the outcome of one delivery goroutine.
type notifyResult struct {
	inc warden.Incident
	err error
}

// Run is the watchdog event loop. It blocks until ctx is cancelled and then
// returns ctx.Err() after joining every delivery goroutine it spawned.
func (w *Watchdog) Run(ctx context.Context) error {
	ch, cancel := w.src.Subscribe(subscribeBuffer)
	defer cancel()

	ticker := w.clock.NewTicker(w.cfg.CheckInterval)
	defer ticker.Stop()

	st := newLoopState()
	results := make(chan notifyResult, notifyResultBuffer)

	// wg tracks in-flight delivery goroutines. Joining it before returning
	// guarantees no goroutine outlives Run. It must be the last deferred call
	// so it runs while ctx is already cancelled and the goroutines can exit
	// via their ctx.Done() branch.
	var wg sync.WaitGroup
	defer wg.Wait()

	// Evaluate once at startup so a node that boots as (or immediately
	// becomes) leader acts without waiting for the first tick or view change.
	w.evaluate(ctx, st, &wg, results)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-ch:
			if !ok {
				// Subscription closed; keep re-evaluating on the ticker.
				ch = nil
				continue
			}
			w.evaluate(ctx, st, &wg, results)
		case <-ticker.C:
			w.evaluate(ctx, st, &wg, results)
		case r := <-results:
			w.handleResult(st, r)
		}
	}
}

// isActingLeader is the gate: the watchdog acts only when this node is the
// authoritative leader that produced the view.
func isActingLeader(v warden.ClusterView) bool {
	return v.Role == warden.RoleLeader && v.Authoritative && v.Source == v.Self
}

// isVoter reports whether a peer's membership kind makes it eligible for
// incident alerting and quorum counting. An empty Member is treated as
// MemberVoter for backward compatibility with pre-membership views, so
// legacy views (no Member set) behave exactly as before. Observers and
// discovered nodes are never voters: a candidate node that vanishes before
// admission is not an operator emergency and must neither help nor hurt the
// leader's live-quorum evidence.
func isVoter(m warden.MemberKind) bool {
	return m == "" || m == warden.MemberVoter
}

// liveQuorum computes the isolation-guard denominator and the live-alive count
// for a leader view.
//
// When the view carries a non-empty voting configuration, the cluster size is
// the number of voters and only voting members contribute to the alive count:
// self counts when self is itself a voter, and a peer counts only when it is a
// voter (Member voter/""), is present in the voting set, and is StatusAlive.
// Observers and discovered nodes are ignored entirely, so they can neither
// restore nor erode quorum.
//
// When membership is absent (pre-membership views), it falls back to the
// historical peers-based count: cluster size is len(Peers), self is trivially
// alive, and any StatusAlive peer counts — with the same omitted-self
// tolerance (a view that omits self still counts self as one live node).
func liveQuorum(v warden.ClusterView) (clusterSize, alive int) {
	if len(v.Membership.Voters) > 0 {
		clusterSize = len(v.Membership.Voters)
		if v.Membership.HasVoter(v.Self) {
			alive++ // self is trivially alive from its own perspective
		}
		for _, p := range v.Peers {
			if p.Node.ID == v.Self {
				continue // self already accounted for above
			}
			if !isVoter(p.Member) {
				continue // observers/discovered never count toward quorum
			}
			if !v.Membership.HasVoter(p.Node.ID) {
				continue // not part of the voting set
			}
			if p.Status == warden.StatusAlive {
				alive++
			}
		}
		return clusterSize, alive
	}

	// Back-compat: peers-based count for pre-membership views.
	clusterSize = len(v.Peers)
	selfListed := false
	for _, p := range v.Peers {
		if p.Node.ID == v.Self {
			selfListed = true
			alive++ // self is trivially alive from its own perspective
			continue
		}
		if p.Status == warden.StatusAlive {
			alive++
		}
	}
	if !selfListed {
		// Tolerate views that omit self from Peers (the contract includes
		// self, but degrade safely rather than miscount the cluster size).
		clusterSize++
		alive++
	}
	return clusterSize, alive
}

// evaluate reads the current view and, when acting as leader, opens/closes
// episodes, records incidents, and dispatches any pending notifications. It
// mutates only loop-owned state.
func (w *Watchdog) evaluate(ctx context.Context, st *loopState, wg *sync.WaitGroup, results chan<- notifyResult) {
	v := w.src.View()

	if !isActingLeader(v) {
		// Not leader: raise nothing, evaluate nothing.
		st.wasLeader = false
		return
	}

	if !st.wasLeader {
		// Gained leadership: adopt a fresh perspective. Reset open-episode
		// tracking and drop stale pending deliveries from the previous epoch,
		// but keep cooldown timestamps so a rapidly flapping leader process
		// does not re-alert. In-flight goroutines from the prior epoch are
		// left to report normally; handleResult applies their outcome
		// harmlessly (removePending is a no-op for a dropped incident).
		st.wasLeader = true
		st.open = make(map[warden.NodeID]*episode)
		st.pending = nil
	}

	// Isolation guard: a leader that cannot see a live majority is more
	// likely the isolated node than a witness to fleet-wide death. Without
	// this gate, a partitioned ex-leader (which keeps RoleLeader until a
	// higher term reaches it) would age every peer to StatusDead and page the
	// operator that the whole fleet died. While the live quorum is below
	// warden.Quorum, ALL episode evaluation and delivery is frozen — the
	// leader cannot distinguish "they died" from "I am cut off", so it stays
	// silent; evaluation resumes as soon as a majority is visible again (or a
	// higher term deposes this node).
	//
	// The denominator is the VOTING set, never the raw peer list: quorum is
	// always computed over Membership.Voters when membership is present, so
	// observers and discovered nodes can neither help nor hurt the leader's
	// quorum evidence. Alive = self (when self is a voter) plus every peer
	// that is a voter, in the voting set, and StatusAlive. When membership is
	// absent (pre-membership views), fall back to the historical peers-based
	// count so legacy behavior is unchanged.
	clusterSize, alive := liveQuorum(v)
	if alive < warden.Quorum(clusterSize) {
		if !st.quorumLost {
			st.quorumLost = true
			core.Logger.Warn().
				Str("node", string(v.Self)).
				Int("alive", alive).
				Int("quorum", warden.Quorum(clusterSize)).
				Msg("warden watchdog: leader lost live peer quorum; suppressing death alerts (this node may be the isolated one)")
		}
		return
	}
	if st.quorumLost {
		st.quorumLost = false
		core.Logger.Info().
			Str("node", string(v.Self)).
			Msg("warden watchdog: live peer quorum restored; resuming evaluation")
	}

	now := w.clock.Now()
	for _, p := range v.Peers {
		if p.Node.ID == v.Self {
			continue // self never generates an incident
		}
		if !isVoter(p.Member) {
			// Observers and discovered nodes never open or close episodes:
			// a candidate node that goes away pre-admission is not an
			// operator emergency.
			continue
		}
		switch p.Status {
		case warden.StatusDead:
			if _, open := st.open[p.Node.ID]; !open {
				st.open[p.Node.ID] = &episode{
					peer:     p.Node,
					openedAt: now,
					lastSeen: p.LastSeen,
					term:     v.Term,
				}
				inc := buildDeadIncident(v, p, now)
				w.ring.append(inc)
				w.queueOrSuppress(st, inc, now)
			}
		case warden.StatusAlive:
			if ep, open := st.open[p.Node.ID]; open {
				delete(st.open, p.Node.ID)
				inc := buildRecoveryIncident(v, p, ep, now)
				w.ring.append(inc)
				if w.cfg.NotifyRecovery {
					w.queueOrSuppress(st, inc, now)
				}
			}
		default:
			// StatusSuspect and StatusUnknown are not dead: no incident, and a
			// dead->suspect transition does not close an open episode.
		}
	}

	w.dispatchPending(ctx, st, wg, results)
}

// queueOrSuppress records the delivery decision for a freshly recorded
// incident. If a successful notification for the same (peer, type) happened
// within Cooldown the incident is suppressed (logged, not delivered);
// otherwise it is queued for delivery.
func (w *Watchdog) queueOrSuppress(st *loopState, inc warden.Incident, now time.Time) {
	key := cooldownKey{peer: inc.Peer.ID, typ: inc.Type}
	if last, ok := st.cooldowns[key]; ok && now.Sub(last) < w.cfg.Cooldown {
		core.Logger.Info().
			Str("incident_id", inc.ID).
			Str("type", string(inc.Type)).
			Str("peer_id", string(inc.Peer.ID)).
			Dur("cooldown", w.cfg.Cooldown).
			Msg("warden watchdog: notification suppressed by cooldown")
		return
	}
	st.pending = append(st.pending, inc)
}

// dispatchPending spawns a delivery goroutine for each pending incident that
// does not already have one in flight. Failed deliveries remain pending and
// are retried on the next evaluation (driven by the CheckInterval ticker), so
// a persistently failing notifier cannot spin a hot loop.
func (w *Watchdog) dispatchPending(ctx context.Context, st *loopState, wg *sync.WaitGroup, results chan<- notifyResult) {
	for _, inc := range st.pending {
		if st.inFlight[inc.ID] {
			continue
		}
		st.inFlight[inc.ID] = true
		wg.Add(1)
		go func(inc warden.Incident) {
			defer wg.Done()
			err := w.notifier.Notify(ctx, inc)
			select {
			case results <- notifyResult{inc: inc, err: err}:
			case <-ctx.Done():
			}
		}(inc)
	}
}

// handleResult applies the outcome of one delivery goroutine to loop-owned
// bookkeeping. On success it stamps the cooldown and removes the incident from
// the pending queue (dedup: the episode is now notified); on failure it leaves
// the incident pending for a later retry.
func (w *Watchdog) handleResult(st *loopState, r notifyResult) {
	delete(st.inFlight, r.inc.ID)
	if r.err != nil {
		core.Logger.Error().
			Err(r.err).
			Str("incident_id", r.inc.ID).
			Str("type", string(r.inc.Type)).
			Str("peer_id", string(r.inc.Peer.ID)).
			Msg("warden watchdog: notification failed; will retry")
		return
	}
	st.cooldowns[cooldownKey{peer: r.inc.Peer.ID, typ: r.inc.Type}] = w.clock.Now()
	removePending(st, r.inc.ID)
}

func removePending(st *loopState, id string) {
	for i := range st.pending {
		if st.pending[i].ID == id {
			st.pending = append(st.pending[:i], st.pending[i+1:]...)
			return
		}
	}
}

// buildDeadIncident constructs a peer_dead incident from a dead peer view.
func buildDeadIncident(v warden.ClusterView, p warden.PeerView, now time.Time) warden.Incident {
	msg := fmt.Sprintf(
		"peer %s (%s) declared dead by leader %s (term %d); last seen %s",
		p.Node.ID, p.Node.Addr, v.Self, v.Term, core.FormatTimeOrNever(p.LastSeen),
	)
	return warden.Incident{
		ID:         warden.NewIncidentID(warden.IncidentPeerDead, p.Node.ID, now),
		Type:       warden.IncidentPeerDead,
		Peer:       p.Node,
		Term:       v.Term,
		ReportedBy: v.Self,
		DetectedAt: now,
		LastSeen:   p.LastSeen,
		Message:    msg,
	}
}

// buildRecoveryIncident constructs a peer_recovered incident when an open dead
// episode closes, including the outage duration.
func buildRecoveryIncident(v warden.ClusterView, p warden.PeerView, ep *episode, now time.Time) warden.Incident {
	outage := now.Sub(ep.openedAt)
	msg := fmt.Sprintf(
		"peer %s (%s) recovered (reported alive) by leader %s (term %d); outage lasted %s (dead detected %s)",
		p.Node.ID, p.Node.Addr, v.Self, v.Term, outage, core.FormatTimeOrNever(ep.openedAt),
	)
	return warden.Incident{
		ID:         warden.NewIncidentID(warden.IncidentPeerRecovered, p.Node.ID, now),
		Type:       warden.IncidentPeerRecovered,
		Peer:       p.Node,
		Term:       v.Term,
		ReportedBy: v.Self,
		DetectedAt: now,
		LastSeen:   p.LastSeen,
		Message:    msg,
	}
}

// incidentRing is the append-only incident log. It is the sole watchdog state
// shared beyond the Run loop.
//
// Incidents() is called by dashboard HTTP handler goroutines whose lifecycle
// is independent of Run: they may query before Run starts, while it runs, or
// after it exits, and must never hang. Routing every read through the loop's
// select would force reasoning about not-yet-started / busy / already-exited
// states and could let a slow delivery stall the dashboard. Instead we keep
// JUST this append-only log behind a tiny mutex: the Run loop is the only
// writer (append) and external callers only ever take a defensive copy
// (snapshot). All watchdog decision state remains exclusively loop-owned with
// no locks — this is the one deliberate, well-scoped exception.
type incidentRing struct {
	mu  sync.Mutex
	max int
	buf []warden.Incident
}

func newIncidentRing(max int) *incidentRing {
	return &incidentRing{max: max}
}

func (r *incidentRing) append(inc warden.Incident) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, inc)
	if overflow := len(r.buf) - r.max; overflow > 0 {
		// Drop the oldest entries, reusing the backing array.
		r.buf = append(r.buf[:0], r.buf[overflow:]...)
	}
}

func (r *incidentRing) snapshot() []warden.Incident {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.buf)
	out := make([]warden.Incident, n)
	for i := 0; i < n; i++ {
		out[i] = r.buf[n-1-i] // most recent first
	}
	return out
}
