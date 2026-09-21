// Package metrics exposes a warden node's cluster state as Prometheus metrics.
//
// Metrics are collected on scrape: a custom prometheus.Collector reads a fresh
// ClusterView snapshot via ViewSource.View() each time /metrics is scraped, so
// there is no background goroutine and no cached state to keep in sync. The
// collector holds only immutable metric descriptors, making it safe for
// concurrent scrapes without any locking of its own.
//
// A private *prometheus.Registry is used rather than the global default
// registry to avoid collisions with anything else linked into the binary. The
// Go runtime and process collectors are registered on that same private
// registry, which is also why a host embedding warden alongside its own
// instrumentation cannot have its default registry disturbed by this package.
//
// The metric and label names in names.go are the scrape contract. They are
// what an operator's dashboards and alert rules bind to, so they are treated
// like any other wire name: additive growth is fine, renaming one is a
// breaking change to every downstream query.
package metrics

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	core "github.com/candacelabs/csf/pkg/core"
	"github.com/candacelabs/csf/services/warden"
)

// allStatuses is the fixed set of peer statuses emitted one-hot by
// warden_peer_status, in a stable order.
var allStatuses = []warden.PeerStatus{
	warden.StatusAlive,
	warden.StatusSuspect,
	warden.StatusDead,
	warden.StatusUnknown,
}

// allMembers is the fixed set of membership kinds emitted one-hot by
// warden_peer_member, in a stable order. An empty PeerView.Member is normalized
// to MemberVoter for backward compatibility before matching against this set.
var allMembers = []warden.MemberKind{
	warden.MemberVoter,
	warden.MemberObserver,
	warden.MemberDiscovered,
}

// memberOrVoter normalizes a peer's membership kind, treating an empty value as
// MemberVoter (pre-membership views).
func memberOrVoter(m warden.MemberKind) warden.MemberKind {
	if m == "" {
		return warden.MemberVoter
	}
	return m
}

// Metrics serves the warden Prometheus endpoint from a private registry.
type Metrics struct {
	registry *prometheus.Registry
	handler  http.Handler
}

// New builds a Metrics backed by a private registry containing the warden view
// collector plus the standard Go runtime and process collectors.
func New(view warden.IViewSource) *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		newViewCollector(view),
	)
	return &Metrics{
		registry: reg,
		handler:  promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
	}
}

// Register mounts the metrics endpoint (GET only) on r. The promhttp handler is
// adapted with gin.WrapH so its response (including its own Content-Type) is
// served unchanged; the engine's HandleMethodNotAllowed answers other methods
// with 405.
func (m *Metrics) Register(r gin.IRouter) {
	r.GET(warden.PathMetrics, gin.WrapH(m.handler))
}

// viewCollector emits warden cluster metrics derived from a live view. It owns
// only immutable *prometheus.Desc values, so Collect is safe under concurrent
// scrapes; the only mutable state read is the snapshot returned by view.View(),
// which is itself an immutable copy.
type viewCollector struct {
	view warden.IViewSource

	isLeader          *prometheus.Desc
	term              *prometheus.Desc
	elections         *prometheus.Desc
	authoritative     *prometheus.Desc
	membershipVersion *prometheus.Desc
	voters            *prometheus.Desc
	observers         *prometheus.Desc
	discovered        *prometheus.Desc
	peerUp            *prometheus.Desc
	peerStatus        *prometheus.Desc
	peerLatency       *prometheus.Desc
	peerMember        *prometheus.Desc
}

func newViewCollector(view warden.IViewSource) *viewCollector {
	return &viewCollector{
		view: view,
		isLeader: prometheus.NewDesc(
			MetricIsLeader,
			"1 if this node is the current cluster leader, otherwise 0.",
			[]string{LabelNode}, nil,
		),
		term: prometheus.NewDesc(
			MetricTerm,
			"Current Raft-style election term as observed by this node.",
			[]string{LabelNode}, nil,
		),
		elections: prometheus.NewDesc(
			MetricElectionsTotal,
			"Total number of elections this node has started since boot.",
			[]string{LabelNode}, nil,
		),
		authoritative: prometheus.NewDesc(
			MetricViewAuthoritative,
			"1 if the cluster view is sourced from the current leader (authoritative), otherwise 0.",
			nil, nil,
		),
		membershipVersion: prometheus.NewDesc(
			MetricMembershipVersion,
			"Version of the effective voting membership configuration this view was rendered under (0 in static/pre-membership views).",
			nil, nil,
		),
		voters: prometheus.NewDesc(
			MetricVoters,
			"Number of voting members in the effective membership configuration (0 in static/pre-membership views, where quorum falls back to the peer set).",
			nil, nil,
		),
		observers: prometheus.NewDesc(
			MetricObservers,
			"Number of peers currently classified as observers (identify-verified nodes awaiting admission; never counted toward quorum).",
			nil, nil,
		),
		discovered: prometheus.NewDesc(
			MetricDiscovered,
			"Number of peers currently classified as discovered (reported by discovery, not yet identify-verified).",
			nil, nil,
		),
		peerUp: prometheus.NewDesc(
			MetricPeerUp,
			"1 if the peer is currently alive, otherwise 0.",
			[]string{LabelPeer}, nil,
		),
		peerStatus: prometheus.NewDesc(
			MetricPeerStatus,
			"One-hot peer liveness status: exactly one of status={alive,suspect,dead,unknown} is 1 per peer.",
			[]string{LabelPeer, LabelStatus}, nil,
		),
		peerLatency: prometheus.NewDesc(
			MetricPeerLatencyMS,
			"Most recent heartbeat round-trip time to the peer in milliseconds (omitted when unknown).",
			[]string{LabelPeer}, nil,
		),
		peerMember: prometheus.NewDesc(
			MetricPeerMember,
			"One-hot peer membership kind: exactly one of member={voter,observer,discovered} is 1 per peer (empty Member is reported as voter).",
			[]string{LabelPeer, LabelMember}, nil,
		),
	}
}

// Describe implements prometheus.Collector. It is a checked collector: every
// descriptor a Collect call can emit is announced here.
func (c *viewCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.isLeader
	ch <- c.term
	ch <- c.elections
	ch <- c.authoritative
	ch <- c.membershipVersion
	ch <- c.voters
	ch <- c.observers
	ch <- c.discovered
	ch <- c.peerUp
	ch <- c.peerStatus
	ch <- c.peerLatency
	ch <- c.peerMember
}

// Collect implements prometheus.Collector by reading one fresh view snapshot
// and emitting const metrics from it.
func (c *viewCollector) Collect(ch chan<- prometheus.Metric) {
	v := c.view.View()
	node := string(v.Self)

	ch <- prometheus.MustNewConstMetric(c.isLeader, prometheus.GaugeValue, core.BoolToFloat64(v.Role == warden.RoleLeader), node)
	ch <- prometheus.MustNewConstMetric(c.term, prometheus.GaugeValue, float64(v.Term), node)
	ch <- prometheus.MustNewConstMetric(c.elections, prometheus.CounterValue, float64(v.ElectionsStarted), node)
	ch <- prometheus.MustNewConstMetric(c.authoritative, prometheus.GaugeValue, core.BoolToFloat64(v.Authoritative))

	// Membership-level gauges. Observer/discovered counts are derived from the
	// peer set (they never appear in Membership.Voters).
	var observerCount, discoveredCount int
	for _, p := range v.Peers {
		switch p.Member {
		case warden.MemberObserver:
			observerCount++
		case warden.MemberDiscovered:
			discoveredCount++
		}
	}
	ch <- prometheus.MustNewConstMetric(c.membershipVersion, prometheus.GaugeValue, float64(v.Membership.Version))
	ch <- prometheus.MustNewConstMetric(c.voters, prometheus.GaugeValue, float64(len(v.Membership.Voters)))
	ch <- prometheus.MustNewConstMetric(c.observers, prometheus.GaugeValue, float64(observerCount))
	ch <- prometheus.MustNewConstMetric(c.discovered, prometheus.GaugeValue, float64(discoveredCount))

	for _, p := range v.Peers {
		peer := string(p.Node.ID)
		ch <- prometheus.MustNewConstMetric(c.peerUp, prometheus.GaugeValue, core.BoolToFloat64(p.Status == warden.StatusAlive), peer)
		for _, s := range allStatuses {
			ch <- prometheus.MustNewConstMetric(c.peerStatus, prometheus.GaugeValue, core.BoolToFloat64(p.Status == s), peer, string(s))
		}
		// One-hot membership kind (empty Member reported as voter). Emitted for
		// every peer regardless of kind, matching peer_up/peer_status.
		member := memberOrVoter(p.Member)
		for _, m := range allMembers {
			ch <- prometheus.MustNewConstMetric(c.peerMember, prometheus.GaugeValue, core.BoolToFloat64(member == m), peer, string(m))
		}
		// Omit the latency series entirely when the RTT is unknown (0).
		if p.LatencyMS > 0 {
			ch <- prometheus.MustNewConstMetric(c.peerLatency, prometheus.GaugeValue, p.LatencyMS, peer)
		}
	}
}
