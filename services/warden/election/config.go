package election

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"time"

	"github.com/candacelabs/csf/pkg/core"
	"github.com/candacelabs/csf/services/warden"
)

// Package-level errors returned by NewManager.
var (
	ErrNoTransport = errors.New("election: transport is nil")
	ErrNoStore     = errors.New("election: store is nil")
	ErrNoClock     = errors.New("election: clock is nil")
	ErrNoSelf      = errors.New("election: Self.ID is empty")
	ErrNoPeers     = errors.New("election: Peers is empty")
	ErrSelfMissing = errors.New("election: Self is not a member of Peers")
	ErrDuplicate   = errors.New("election: duplicate node id in Peers")
)

// Config configures a Manager. Zero-valued durations receive documented
// defaults (see NewManager). Peers is the full, static cluster membership and
// must include Self.
type Config struct {
	Self  warden.Node
	Peers []warden.Node // full cluster member list INCLUDING Self

	HeartbeatInterval  time.Duration // default 1s
	SuspectAfter       time.Duration // default 5s
	DeadAfter          time.Duration // default 15s
	ElectionTimeoutMin time.Duration // default 1500ms
	ElectionTimeoutMax time.Duration // default 3s
	RPCTimeout         time.Duration // per-RPC timeout, default 500ms
	// ClusterID names the cluster for the identify handshake (default
	// "candacenet"). Discovery ignores nodes reporting a different id.
	ClusterID string
	// BuildVersion is reported in identify responses (informational).
	BuildVersion string
	// ViewFreshFor is how long a follower treats a cached leader view as
	// authoritative; default = DeadAfter.
	ViewFreshFor time.Duration

	// Discoverer supplies dynamic peer discovery. When nil, the Manager runs in
	// STATIC mode: effective membership is exactly Peers, the persisted
	// Membership is ignored, Version semantics are inert, and no admission or
	// removal ever happens (historical behavior, unchanged). When non-nil, the
	// Manager runs in DISCOVERY mode: it consumes roster snapshots, verifies
	// candidates via Transport.Identify, and — only as leader — grows/shrinks
	// the voting membership one node at a time.
	Discoverer warden.IPeerDiscoverer
	// JoinStability is how long a discovered node must be continuously present
	// in the roster AND identify-verified before it becomes admission-eligible.
	// Default 30s. Discovery mode only. It also serves as the grace period
	// before a vanished observer is dropped.
	JoinStability time.Duration
	// RemoveAfter enables leader-committed TTL removal of voters. 0 (default)
	// disables removal entirely — a voter is NEVER auto-removed. When > 0, the
	// leader may remove a voter that is absent from the roster and has been
	// StatusDead for at least this long, but only while it still observes a live
	// majority of the current voter set. Discovery mode only.
	RemoveAfter time.Duration
}

// applyDefaults fills zero-valued fields with their defaults and repairs an
// invalid election-timeout window.
func applyDefaults(cfg *Config) {
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = time.Second
	}
	if cfg.SuspectAfter <= 0 {
		cfg.SuspectAfter = 5 * time.Second
	}
	if cfg.DeadAfter <= 0 {
		cfg.DeadAfter = 15 * time.Second
	}
	if cfg.ElectionTimeoutMin <= 0 {
		cfg.ElectionTimeoutMin = 1500 * time.Millisecond
	}
	if cfg.ElectionTimeoutMax <= 0 {
		cfg.ElectionTimeoutMax = 3 * time.Second
	}
	if cfg.ElectionTimeoutMax <= cfg.ElectionTimeoutMin {
		// Keep a non-empty randomization window even under odd configuration.
		cfg.ElectionTimeoutMax = cfg.ElectionTimeoutMin + cfg.ElectionTimeoutMin/2
	}
	if cfg.RPCTimeout <= 0 {
		cfg.RPCTimeout = 500 * time.Millisecond
	}
	if cfg.ClusterID == "" {
		cfg.ClusterID = "candacenet"
	}
	if cfg.ViewFreshFor <= 0 {
		cfg.ViewFreshFor = cfg.DeadAfter
	}
	if cfg.JoinStability <= 0 {
		cfg.JoinStability = 30 * time.Second
	}
	// RemoveAfter's zero value is meaningful (removal disabled); never defaulted.
}

// validateConfig checks structural invariants that defaults cannot repair.
func validateConfig(cfg Config, tr warden.ITransport, st warden.IStore, clock warden.IClock) error {
	if tr == nil {
		return ErrNoTransport
	}
	if st == nil {
		return ErrNoStore
	}
	if clock == nil {
		return ErrNoClock
	}
	if cfg.Self.ID == "" {
		return ErrNoSelf
	}
	if len(cfg.Peers) == 0 {
		return ErrNoPeers
	}
	seen := make(map[warden.NodeID]bool, len(cfg.Peers))
	found := false
	for _, p := range cfg.Peers {
		if seen[p.ID] {
			return fmt.Errorf("%w: %q", ErrDuplicate, p.ID)
		}
		seen[p.ID] = true
		if p.ID == cfg.Self.ID {
			found = true
		}
	}
	// In STATIC mode Self must be one of the static peers. In DISCOVERY mode a
	// brand-new joiner ships the existing fleet's peer list as its membership
	// seed and is legitimately absent from it — it boots as a pure observer and
	// is admitted later — so Self ∉ Peers is allowed there.
	if !found && cfg.Discoverer == nil {
		return fmt.Errorf("%w: %q", ErrSelfMissing, cfg.Self.ID)
	}
	return nil
}

// NewManager constructs a Manager, loads any persisted term/vote from st, and
// resumes from that term. The returned Manager is inert until Run is called.
//
// Defaults for zero-valued Config durations: HeartbeatInterval 1s,
// SuspectAfter 5s, DeadAfter 15s, ElectionTimeoutMin 1500ms,
// ElectionTimeoutMax 3s, RPCTimeout 500ms, ViewFreshFor = DeadAfter.
func NewManager(cfg Config, tr warden.ITransport, st warden.IStore, clock warden.IClock) (*Manager, error) {
	if err := validateConfig(cfg, tr, st, clock); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)

	ps, ok, err := st.Load()
	if err != nil {
		return nil, fmt.Errorf("election: loading persistent state: %w", err)
	}

	peers := make([]warden.Node, len(cfg.Peers))
	copy(peers, cfg.Peers)
	warden.SortNodes(peers)

	discoveryMode := cfg.Discoverer != nil

	// Seed the effective voting membership.
	//   STATIC:    Version 0, Voters == Peers exactly (Version inert; any
	//              persisted Membership is ignored).
	//   DISCOVERY: the persisted Membership if present (a restart resumes the
	//              same quorum denominator), else Version 1 with Voters == Peers
	//              (the founding/seed voter set; a joiner's seed omits self).
	var membership warden.Membership
	switch {
	case discoveryMode && ok && ps.Membership != nil:
		membership = ps.Membership.Clone()
		warden.SortNodes(membership.Voters)
	case discoveryMode:
		membership = warden.Membership{Version: 1, Voters: append([]warden.Node(nil), peers...)}
	default:
		membership = warden.Membership{Version: 0, Voters: append([]warden.Node(nil), peers...)}
	}

	m := &Manager{
		cfg:             cfg,
		self:            cfg.Self,
		discoveryMode:   discoveryMode,
		membership:      membership,
		publishInterval: cfg.HeartbeatInterval,
		transport:       tr,
		store:           st,
		clock:           clock,
		log:             core.Logger,
		rng:             rand.New(rand.NewSource(seedFor(cfg.Self.ID) ^ clock.Now().UnixNano())),
		role:            warden.RoleFollower,
		lastContact:     make(map[warden.NodeID]time.Time),
		latencyMS:       make(map[warden.NodeID]float64),
		candidates:      make(map[warden.NodeID]*candidate),
		ackedVersion:    make(map[warden.NodeID]ackRef),
		subs:            make(map[int]chan warden.ClusterView),
		events:          make(chan any, eventBuffer),
		done:            make(chan struct{}),
		rpc:             newInflightTracker(),
	}
	if ok {
		m.currentTerm = ps.CurrentTerm
		m.votedFor = ps.VotedFor
	}
	return m, nil
}

// seedFor derives a PRNG seed component from a node ID so that different
// nodes pick different randomized election timeout sequences (avoiding
// perpetual split votes). The caller XORs it with clock.Now().UnixNano():
// under the real clock each boot gets a fresh, unpredictable schedule (a
// restarted node never replays its previous timeout sequence), while under
// testclock the fixed start time keeps tests fully deterministic and
// per-node distinct.
func seedFor(id warden.NodeID) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return int64(h.Sum64())
}
