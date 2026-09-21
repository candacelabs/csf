package election

import (
	"context"
	"errors"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/store"
	"github.com/candacelabs/csf/services/warden/testclock"
)

// TestMain silences the shared structured logger so the deterministic election
// tests do not flood test output with heartbeat/vote log lines.
func TestMain(m *testing.M) {
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(m.Run())
}

// errUnreachable is what the fake transport returns for a killed peer or a
// message crossing a partition boundary.
var errUnreachable = errors.New("harness: peer unreachable")

// harnessTimings are small, fixed simulated durations chosen so the important
// ordering relationships hold: heartbeat << election-timeout (a leader's
// heartbeats reset followers before they time out) and suspect < dead. Because
// the clock is simulated, absolute magnitudes never cost real time.
type harnessTimings struct {
	Heartbeat    time.Duration
	Suspect      time.Duration
	Dead         time.Duration
	ETMin        time.Duration
	ETMax        time.Duration
	RPCTimeout   time.Duration
	ViewFreshFor time.Duration
}

func defaultTimings() harnessTimings {
	return harnessTimings{
		Heartbeat:    100 * time.Millisecond,
		Suspect:      1 * time.Second,
		Dead:         3 * time.Second,
		ETMin:        300 * time.Millisecond,
		ETMax:        600 * time.Millisecond,
		RPCTimeout:   50 * time.Millisecond,
		ViewFreshFor: 3 * time.Second,
	}
}

// node bundles a running Manager with its lifecycle handles and its store (so
// a restart can reuse the same persisted state).
type node struct {
	id      warden.NodeID
	m       *Manager
	store   warden.IStore
	cancel  context.CancelFunc
	runDone chan struct{}
}

// cluster is a deterministic in-memory harness. It is the shared
// warden.ITransport for every node and models partitions and node death. All
// nodes share one testclock so the whole fleet observes the same simulated
// time.
type cluster struct {
	t       iHarnessT
	clock   *testclock.Clock
	timings harnessTimings
	ids     []warden.NodeID
	nodes   []warden.Node

	mu      sync.Mutex
	byID    map[warden.NodeID]*node
	killed  map[warden.NodeID]bool
	part    map[warden.NodeID]int // partition group id; same id == reachable
	startAt time.Time

	// ---- discovery-mode configuration (nil/false in static clusters) ----
	discovery     bool
	clusterID     string
	joinStability time.Duration
	removeAfter   time.Duration
	disco         map[warden.NodeID]*fakeDiscoverer
	// seedPeers overrides a node's membership seed (cfg.Peers). Absent = the
	// founder set (c.nodes). A joiner supplies a seed that omits itself.
	seedPeers map[warden.NodeID][]warden.Node
}

var _ warden.ITransport = (*cluster)(nil)

// newCluster builds and starts an n-node cluster with default timings.
func newCluster(t iHarnessT, ids ...warden.NodeID) *cluster {
	return newClusterWithTimings(t, defaultTimings(), ids...)
}

func newClusterWithTimings(t iHarnessT, tim harnessTimings, ids ...warden.NodeID) *cluster {
	t.Helper()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := &cluster{
		t:       t,
		clock:   testclock.New(start),
		timings: tim,
		ids:     append([]warden.NodeID(nil), ids...),
		byID:    make(map[warden.NodeID]*node),
		killed:  make(map[warden.NodeID]bool),
		part:    make(map[warden.NodeID]int),
		startAt: start,
	}
	for _, id := range ids {
		c.nodes = append(c.nodes, warden.Node{ID: id, Addr: string(id)})
	}
	for _, id := range ids {
		c.start(id, store.NewMemStore())
	}
	t.Cleanup(c.stopAll)
	return c
}

// configFor builds the election Config for a node. Every harness node uses the
// convention Addr == string(ID); routing is by ID so the Addr is only cosmetic.
func (c *cluster) configFor(id warden.NodeID) Config {
	self := warden.Node{ID: id, Addr: string(id)}
	peers := c.seedPeers[id]
	if peers == nil {
		peers = append([]warden.Node(nil), c.nodes...)
	}
	cfg := Config{
		Self:               self,
		Peers:              peers,
		HeartbeatInterval:  c.timings.Heartbeat,
		SuspectAfter:       c.timings.Suspect,
		DeadAfter:          c.timings.Dead,
		ElectionTimeoutMin: c.timings.ETMin,
		ElectionTimeoutMax: c.timings.ETMax,
		RPCTimeout:         c.timings.RPCTimeout,
		ViewFreshFor:       c.timings.ViewFreshFor,
	}
	if c.discovery {
		cfg.Discoverer = c.disco[id]
		cfg.ClusterID = c.clusterID
		cfg.BuildVersion = "test"
		cfg.JoinStability = c.joinStability
		cfg.RemoveAfter = c.removeAfter
	}
	return cfg
}

// start constructs and runs a Manager for id backed by st.
func (c *cluster) start(id warden.NodeID, st warden.IStore) {
	c.t.Helper()
	if c.discovery {
		c.mu.Lock()
		if c.disco[id] == nil {
			c.disco[id] = newFakeDiscoverer()
		}
		c.mu.Unlock()
	}
	m, err := NewManager(c.configFor(id), c, st, c.clock)
	if err != nil {
		c.t.Fatalf("NewManager(%s): %v", id, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		_ = m.Run(ctx)
		close(runDone)
	}()
	c.mu.Lock()
	c.byID[id] = &node{id: id, m: m, store: st, cancel: cancel, runDone: runDone}
	c.killed[id] = false
	c.mu.Unlock()
}

// ---- discovery harness ----

// fakeDiscoverer is a channel-driven warden.IPeerDiscoverer. Tests push Roster
// snapshots into it; Discover hands the Manager the receive end. It spawns no
// goroutine of its own (so it can never leak) and never blocks a push once the
// Run context is done.
type fakeDiscoverer struct {
	mu  sync.Mutex
	ch  chan warden.Roster
	ctx context.Context
}

func newFakeDiscoverer() *fakeDiscoverer {
	return &fakeDiscoverer{ch: make(chan warden.Roster, 64)}
}

func (f *fakeDiscoverer) Discover(ctx context.Context) (<-chan warden.Roster, error) {
	f.mu.Lock()
	f.ctx = ctx
	ch := f.ch
	f.mu.Unlock()
	return ch, nil
}

func (f *fakeDiscoverer) push(r warden.Roster) {
	f.mu.Lock()
	ch, ctx := f.ch, f.ctx
	f.mu.Unlock()
	if ctx == nil {
		// Run has not called Discover yet; the buffered channel absorbs it.
		ch <- r
		return
	}
	select {
	case ch <- r:
	case <-ctx.Done():
	}
}

var _ warden.IPeerDiscoverer = (*fakeDiscoverer)(nil)

// newDiscoveryCluster builds and starts an n-node discovery-mode cluster. The
// founder ids seed the initial voter set; join and remove configure
// JoinStability and RemoveAfter.
func newDiscoveryCluster(t iHarnessT, tim harnessTimings, join, remove time.Duration, ids ...warden.NodeID) *cluster {
	t.Helper()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := &cluster{
		t:             t,
		clock:         testclock.New(start),
		timings:       tim,
		ids:           append([]warden.NodeID(nil), ids...),
		byID:          make(map[warden.NodeID]*node),
		killed:        make(map[warden.NodeID]bool),
		part:          make(map[warden.NodeID]int),
		startAt:       start,
		discovery:     true,
		clusterID:     "candacenet-test",
		joinStability: join,
		removeAfter:   remove,
		disco:         make(map[warden.NodeID]*fakeDiscoverer),
		seedPeers:     make(map[warden.NodeID][]warden.Node),
	}
	for _, id := range ids {
		c.nodes = append(c.nodes, warden.Node{ID: id, Addr: string(id)})
	}
	for _, id := range ids {
		c.start(id, store.NewMemStore())
	}
	t.Cleanup(c.stopAll)
	return c
}

// addObserver starts a brand-new joiner node whose membership seed is the given
// voter set (which must NOT contain id — that is the whole point of a joiner).
// It boots as a pure observer until a leader admits it. Returns after settling.
func (c *cluster) addObserver(id warden.NodeID, seed []warden.Node) {
	c.t.Helper()
	c.mu.Lock()
	c.seedPeers[id] = append([]warden.Node(nil), seed...)
	c.ids = append(c.ids, id)
	c.mu.Unlock()
	c.start(id, store.NewMemStore())
	c.Settle()
}

// rosterNodes builds a Roster node slice from ids (Addr == string(id)).
func rosterNodes(ids ...warden.NodeID) []warden.Node {
	out := make([]warden.Node, 0, len(ids))
	for _, id := range ids {
		out = append(out, warden.Node{ID: id, Addr: string(id)})
	}
	return out
}

// pushRoster delivers a roster of `ids` to the discoverers of `targets` (all
// live discoverers when targets is empty), then settles.
func (c *cluster) pushRoster(targets []warden.NodeID, ids ...warden.NodeID) {
	c.mu.Lock()
	var discos []*fakeDiscoverer
	if len(targets) == 0 {
		for _, d := range c.disco {
			discos = append(discos, d)
		}
	} else {
		for _, id := range targets {
			if d := c.disco[id]; d != nil {
				discos = append(discos, d)
			}
		}
	}
	c.mu.Unlock()
	r := warden.Roster{Nodes: rosterNodes(ids...)}
	for _, d := range discos {
		d.push(r)
	}
	c.Settle()
}

// ---- discovery / membership observation helpers ----

// voterIDs returns the sorted voter IDs from id's current membership view.
func (c *cluster) voterIDs(id warden.NodeID) []warden.NodeID {
	v := c.view(id)
	out := make([]warden.NodeID, 0, len(v.Membership.Voters))
	for _, n := range v.Membership.Voters {
		out = append(out, n.ID)
	}
	return out
}

// membershipVersion returns the effective membership Version in id's view.
func (c *cluster) membershipVersion(id warden.NodeID) uint64 {
	return c.view(id).Membership.Version
}

// memberKind returns the MemberKind of target as seen in viewer's view (or ""
// if target is absent).
func (c *cluster) memberKind(viewer, target warden.NodeID) warden.MemberKind {
	for _, p := range c.view(viewer).Peers {
		if p.Node.ID == target {
			return p.Member
		}
	}
	return ""
}

// agreedLeaderWithin reports the leader if EXACTLY one node in scope is a leader
// and every other node in scope follows it. Unlike singleAgreedLeader it ignores
// nodes outside scope — e.g. a stale leader stranded in another partition — so
// it can wait for one side of a partition to elect.
func (c *cluster) agreedLeaderWithin(scope ...warden.NodeID) (warden.NodeID, bool) {
	var leader warden.NodeID
	count := 0
	for _, id := range scope {
		if c.role(id) == warden.RoleLeader {
			leader = id
			count++
		}
	}
	if count != 1 {
		return "", false
	}
	for _, id := range scope {
		if id == leader {
			continue
		}
		v := c.view(id)
		if v.Role != warden.RoleFollower || v.LeaderID != leader {
			return "", false
		}
	}
	return leader, true
}

// electWithin advances until scope has a single agreed leader among itself,
// ignoring any leader outside scope.
func (c *cluster) electWithin(scope ...warden.NodeID) warden.NodeID {
	c.t.Helper()
	for i := 0; i < 600; i++ {
		if id, ok := c.agreedLeaderWithin(scope...); ok {
			return id
		}
		c.Advance(c.step())
	}
	c.t.Fatalf("no agreed leader within %v; leaders=%v", scope, c.leaders())
	return ""
}

// equalIDs reports whether two ID slices are element-wise equal.
func equalIDs(a, b []warden.NodeID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sortedIDs returns a sorted copy of ids.
func sortedIDs(ids []warden.NodeID) []warden.NodeID {
	out := append([]warden.NodeID(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// persistedVoters returns the voter IDs recorded in id's store, or nil if no
// membership has been persisted.
func (c *cluster) persistedVoters(id warden.NodeID) []warden.NodeID {
	c.mu.Lock()
	n := c.byID[id]
	c.mu.Unlock()
	ps, ok, err := n.store.Load()
	if err != nil {
		c.t.Fatalf("load store %s: %v", id, err)
	}
	if !ok || ps.Membership == nil {
		return nil
	}
	out := make([]warden.NodeID, 0, len(ps.Membership.Voters))
	for _, v := range ps.Membership.Voters {
		out = append(out, v.ID)
	}
	return out
}

// ---- warden.ITransport ----

func (c *cluster) RequestVote(ctx context.Context, peer warden.Node, req warden.VoteRequest) (warden.VoteResponse, error) {
	tgt, ok := c.route(req.CandidateID, peer.ID)
	if !ok {
		return warden.VoteResponse{}, errUnreachable
	}
	return tgt.HandleVote(ctx, req), nil
}

func (c *cluster) SendHeartbeat(ctx context.Context, peer warden.Node, req warden.HeartbeatRequest) (warden.HeartbeatResponse, error) {
	tgt, ok := c.route(req.LeaderID, peer.ID)
	if !ok {
		return warden.HeartbeatResponse{}, errUnreachable
	}
	return tgt.HandleHeartbeat(ctx, req), nil
}

// Identify implements warden.ITransport for the harness. Reachability follows
// the same partition/kill rules; src is unknown for identify, so it uses the
// destination's own group (identify is only issued by nodes probing peers
// they can reach; tests that need partition-aware identify drive route
// directly).
func (c *cluster) Identify(ctx context.Context, peer warden.Node) (warden.IdentifyResponse, error) {
	c.mu.Lock()
	n, ok := c.byID[peer.ID]
	killed := c.killed[peer.ID]
	c.mu.Unlock()
	if !ok || killed {
		return warden.IdentifyResponse{}, errUnreachable
	}
	return n.m.HandleIdentify(ctx), nil
}

// route resolves the target Manager if src can reach dst right now. The lookup
// is done under the harness mutex; the returned handler is invoked by the
// caller outside the lock.
//
// It returns the concrete *Manager rather than warden.IRPCHandler: the harness
// only ever holds Managers, so the interface here was a widening on the way
// out, which is the CS-8 defect spelled as a method. The `ok` bool is the
// guard, so the failure arm's nil is never dereferenced.
func (c *cluster) route(src, dst warden.NodeID) (*Manager, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.killed[src] || c.killed[dst] {
		return nil, false
	}
	if c.part[src] != c.part[dst] {
		return nil, false
	}
	n, ok := c.byID[dst]
	if !ok {
		return nil, false
	}
	return n.m, true
}

// ---- fault injection ----

// partition assigns nodes to reachability groups. Nodes in the same group can
// talk; nodes in different groups cannot. Groups must cover every node.
func (c *cluster) partition(groups ...[]warden.NodeID) {
	c.mu.Lock()
	for gi, g := range groups {
		for _, id := range g {
			c.part[id] = gi + 1
		}
	}
	c.mu.Unlock()
	c.Settle()
}

// heal restores full connectivity.
func (c *cluster) heal() {
	c.mu.Lock()
	for id := range c.part {
		c.part[id] = 0
	}
	c.mu.Unlock()
	c.Settle()
}

// kill stops a node's loop and makes it unreachable.
func (c *cluster) kill(id warden.NodeID) {
	c.mu.Lock()
	n := c.byID[id]
	c.killed[id] = true
	c.mu.Unlock()
	if n == nil {
		return
	}
	n.cancel()
	<-n.runDone
	c.Settle()
}

// restart brings a killed node back up reusing its persisted state.
func (c *cluster) restart(id warden.NodeID) {
	c.mu.Lock()
	n := c.byID[id]
	st := n.store
	c.mu.Unlock()
	c.start(id, st)
	c.Settle()
}

// stopAll cancels every node and waits for all loops to return.
func (c *cluster) stopAll() {
	c.mu.Lock()
	nodes := make([]*node, 0, len(c.byID))
	for _, n := range c.byID {
		nodes = append(nodes, n)
	}
	c.mu.Unlock()
	for _, n := range nodes {
		n.cancel()
	}
	for _, n := range nodes {
		<-n.runDone
	}
}

// ---- deterministic advancement ----

// aliveManagers returns the currently-running managers.
func (c *cluster) aliveManagers() []*Manager {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*Manager, 0, len(c.byID))
	for id, n := range c.byID {
		if !c.killed[id] {
			out = append(out, n.m)
		}
	}
	return out
}

// Settle drives all loops and outbound RPC workers to a fixpoint without
// advancing time. It alternates draining each loop (via a barrier that
// consumes every ready timer fire and message) with waiting for outbound RPC
// workers, and returns once a full pass produces no state-changing activity —
// which can only happen when nothing is pending anywhere.
func (c *cluster) Settle() {
	for {
		ms := c.aliveManagers()
		before := sumActivity(ms)
		for _, m := range ms {
			m.barrier()
		}
		for _, m := range ms {
			m.rpc.wait()
		}
		for _, m := range ms {
			m.barrier()
		}
		for _, m := range ms {
			m.rpc.wait()
		}
		if sumActivity(ms) == before {
			return
		}
	}
}

func sumActivity(ms []*Manager) uint64 {
	var total uint64
	for _, m := range ms {
		total += m.activity.Load()
	}
	return total
}

// step is the granularity Advance uses. It is at most half the heartbeat
// interval so that, in every advancement, a leader's heartbeats are processed
// and reset connected followers' election timers before those followers could
// reach their (longer) election deadline. This is what makes multi-node
// advancement deterministic: a well-connected follower never spuriously times
// out just because time jumped.
func (c *cluster) step() time.Duration {
	s := c.timings.Heartbeat / 2
	if s <= 0 {
		s = 25 * time.Millisecond
	}
	return s
}

// Advance moves simulated time forward by d in small steps, settling the
// cluster after each, and lands exactly on now+d.
func (c *cluster) Advance(d time.Duration) {
	step := c.step()
	for d > 0 {
		s := step
		if s > d {
			s = d
		}
		c.clock.Advance(s)
		c.Settle()
		d -= s
	}
}

// advanceTo advances simulated time to exactly the target instant.
func (c *cluster) advanceTo(target time.Time) {
	d := target.Sub(c.clock.Now())
	if d > 0 {
		c.Advance(d)
	}
}

// ---- observation helpers ----

func (c *cluster) manager(id warden.NodeID) *Manager {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byID[id].m
}

func (c *cluster) view(id warden.NodeID) warden.ClusterView {
	return c.manager(id).View()
}

func (c *cluster) role(id warden.NodeID) warden.Role {
	return c.view(id).Role
}

// leaders returns the ids of all nodes that currently believe they are leader.
func (c *cluster) leaders() []warden.NodeID {
	var out []warden.NodeID
	c.mu.Lock()
	ids := make([]warden.NodeID, 0, len(c.byID))
	for id := range c.byID {
		if !c.killed[id] {
			ids = append(ids, id)
		}
	}
	c.mu.Unlock()
	for _, id := range ids {
		if c.role(id) == warden.RoleLeader {
			out = append(out, id)
		}
	}
	return out
}

// electLeader advances time in small steps until exactly one leader exists and
// every other live node agrees on it, or fails the test. Small steps let the
// earliest-timing node win and heartbeat the rest before they also time out,
// yielding a clean single leader; occasional split votes self-heal on the next
// randomized timeout.
func (c *cluster) electLeader(among ...warden.NodeID) warden.NodeID {
	c.t.Helper()
	for i := 0; i < 600; i++ {
		if id, ok := c.singleAgreedLeader(among...); ok {
			return id
		}
		c.Advance(c.step())
	}
	c.t.Fatalf("no single agreed leader after advancing; leaders=%v", c.leaders())
	return ""
}

// singleAgreedLeader reports the unique leader if there is exactly one and all
// live nodes in `among` (default: all live nodes) point at it.
func (c *cluster) singleAgreedLeader(among ...warden.NodeID) (warden.NodeID, bool) {
	ls := c.leaders()
	if len(ls) != 1 {
		return "", false
	}
	leader := ls[0]
	scope := among
	if len(scope) == 0 {
		c.mu.Lock()
		for id := range c.byID {
			if !c.killed[id] {
				scope = append(scope, id)
			}
		}
		c.mu.Unlock()
	}
	for _, id := range scope {
		v := c.view(id)
		if id == leader {
			if v.Role != warden.RoleLeader {
				return "", false
			}
			continue
		}
		if v.Role != warden.RoleFollower || v.LeaderID != leader {
			return "", false
		}
	}
	return leader, true
}

// term returns a node's current term as reported by its view.
func (c *cluster) term(id warden.NodeID) warden.Term {
	return c.view(id).Term
}

// peerStatus returns the status of peerID as seen in viewerID's cluster view.
func (c *cluster) peerStatus(viewerID, peerID warden.NodeID) warden.PeerStatus {
	v := c.view(viewerID)
	for _, p := range v.Peers {
		if p.Node.ID == peerID {
			return p.Status
		}
	}
	c.t.Fatalf("%s not present in %s's view", peerID, viewerID)
	return ""
}

// peerLastSeen returns the LastSeen timestamp of peerID in viewerID's view.
func (c *cluster) peerLastSeen(viewerID, peerID warden.NodeID) time.Time {
	v := c.view(viewerID)
	for _, p := range v.Peers {
		if p.Node.ID == peerID {
			return p.LastSeen
		}
	}
	c.t.Fatalf("%s not present in %s's view", peerID, viewerID)
	return time.Time{}
}

// others returns every id except the given ones.
func (c *cluster) others(exclude ...warden.NodeID) []warden.NodeID {
	ex := make(map[warden.NodeID]bool, len(exclude))
	for _, e := range exclude {
		ex[e] = true
	}
	var out []warden.NodeID
	for _, id := range c.ids {
		if !ex[id] {
			out = append(out, id)
		}
	}
	return out
}

// sinceStart is the simulated elapsed time since the cluster booted.
func (c *cluster) sinceStart() time.Duration {
	return c.clock.Now().Sub(c.startAt)
}
