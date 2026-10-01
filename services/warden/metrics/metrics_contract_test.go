package metrics_test

// Contract tests for the Prometheus /metrics surface, asserted through a real
// HTTP scrape of the registered handler. These freeze the metric names, types,
// HELP strings, label sets, the one-hot peer-status matrix, the latency
// omit-at-zero rule, and the collect-on-scrape freshness guarantee. Operators'
// dashboards and alerts are wired to these exact names and labels.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/httpserver"
	"github.com/candacelabs/csf/services/warden/metrics"
)

func TestMetricsContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "metrics contract suite")
}

// stubSource is a warden.IViewSource that returns a settable view. Its viewReads
// counter proves the collector reads a fresh snapshot on every scrape.
type stubSource struct {
	mu        sync.Mutex
	view      warden.ClusterView
	viewReads int
}

func (s *stubSource) set(v warden.ClusterView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.view = v
}

func (s *stubSource) View() warden.ClusterView {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.viewReads++
	return s.view
}

func (s *stubSource) reads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.viewReads
}

func (s *stubSource) Subscribe(buf int) (<-chan warden.ClusterView, func()) {
	ch := make(chan warden.ClusterView, buf)
	return ch, func() {}
}

// newMetricsServer isolates the concrete-mux dependency: the eventual Gin port
// changes this one helper, and the scrape assertions below stay unchanged.
func newMetricsServer(src warden.IViewSource) *httptest.Server {
	engine := httpserver.NewEngine()
	metrics.New(src).Register(engine)
	return httptest.NewServer(engine)
}

func scrape(server *httptest.Server) string {
	resp, err := http.Get(server.URL + warden.PathMetrics)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	Expect(resp.StatusCode).To(Equal(http.StatusOK))
	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return string(body)
}

// metricLine finds a single exposition line "<name>{labels} <value>" or
// "<name> <value>" and returns the value string, or "" if absent.
func metricLine(scrapeText, name string, labels string) string {
	prefix := name + labels + " "
	for _, line := range strings.Split(scrapeText, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func leaderView() warden.ClusterView {
	return warden.ClusterView{
		Self: "node-d", Role: warden.RoleLeader, Term: 7, LeaderID: "node-d",
		Source: "node-d", Authoritative: true, ElectionsStarted: 4,
		Peers: []warden.PeerView{
			{Node: warden.Node{ID: "node-d", Addr: "203.0.113.21:7717"}, Status: warden.StatusAlive},
			{Node: warden.Node{ID: "node-a", Addr: "203.0.113.22:7717"}, Status: warden.StatusDead, LatencyMS: 0},
			{Node: warden.Node{ID: "node-b", Addr: "203.0.113.23:7717"}, Status: warden.StatusAlive, LatencyMS: 12.5},
		},
	}
}

var _ = Describe("Metrics endpoint", func() {
	var (
		src    *stubSource
		server *httptest.Server
	)

	BeforeEach(func() {
		src = &stubSource{}
		src.set(leaderView())
		server = newMetricsServer(src)
	})

	AfterEach(func() { server.Close() })

	Describe("HELP and TYPE metadata", func() {
		DescribeTable("declares the frozen metric type",
			func(name, typ string) {
				out := scrape(server)
				Expect(out).To(ContainSubstring("# TYPE " + name + " " + typ))
			},
			Entry("warden_is_leader is a gauge", "warden_is_leader", "gauge"),
			Entry("warden_term is a gauge", "warden_term", "gauge"),
			Entry("warden_elections_total is a counter", "warden_elections_total", "counter"),
			Entry("warden_view_authoritative is a gauge", "warden_view_authoritative", "gauge"),
			Entry("warden_membership_version is a gauge", "warden_membership_version", "gauge"),
			Entry("warden_voters is a gauge", "warden_voters", "gauge"),
			Entry("warden_observers is a gauge", "warden_observers", "gauge"),
			Entry("warden_discovered is a gauge", "warden_discovered", "gauge"),
			Entry("warden_peer_up is a gauge", "warden_peer_up", "gauge"),
			Entry("warden_peer_status is a gauge", "warden_peer_status", "gauge"),
			Entry("warden_peer_latency_ms is a gauge", "warden_peer_latency_ms", "gauge"),
			Entry("warden_peer_member is a gauge", "warden_peer_member", "gauge"),
		)

		DescribeTable("declares the frozen HELP string",
			func(help string) {
				out := scrape(server)
				Expect(out).To(ContainSubstring(help))
			},
			Entry("is_leader", "# HELP warden_is_leader 1 if this node is the current cluster leader, otherwise 0."),
			Entry("term", "# HELP warden_term Current Raft-style election term as observed by this node."),
			Entry("elections", "# HELP warden_elections_total Total number of elections this node has started since boot."),
			Entry("authoritative", "# HELP warden_view_authoritative 1 if the cluster view is sourced from the current leader (authoritative), otherwise 0."),
			Entry("peer_up", "# HELP warden_peer_up 1 if the peer is currently alive, otherwise 0."),
			Entry("peer_status", "# HELP warden_peer_status One-hot peer liveness status: exactly one of status={alive,suspect,dead,unknown} is 1 per peer."),
			Entry("peer_latency", "# HELP warden_peer_latency_ms Most recent heartbeat round-trip time to the peer in milliseconds (omitted when unknown)."),
		)
	})

	Describe("node-scoped scalar metrics", func() {
		It("labels is_leader/term/elections with node and reports leader state", func() {
			out := scrape(server)
			Expect(metricLine(out, metrics.MetricIsLeader, `{node="node-d"}`)).To(Equal("1"))
			Expect(metricLine(out, metrics.MetricTerm, `{node="node-d"}`)).To(Equal("7"))
			Expect(metricLine(out, metrics.MetricElectionsTotal, `{node="node-d"}`)).To(Equal("4"))
		})

		It("emits warden_view_authoritative with NO labels", func() {
			out := scrape(server)
			Expect(metricLine(out, metrics.MetricViewAuthoritative, "")).To(Equal("1"))
		})

		It("reports is_leader=0 and authoritative=0 for a non-leader local view", func() {
			src.set(warden.ClusterView{
				Self: "n2", Role: warden.RoleFollower, Term: 3, LeaderID: "node-d",
				Source: "n2", Authoritative: false,
				Peers: []warden.PeerView{{Node: warden.Node{ID: "n2"}, Status: warden.StatusAlive}},
			})
			out := scrape(server)
			Expect(metricLine(out, metrics.MetricIsLeader, `{node="n2"}`)).To(Equal("0"))
			Expect(metricLine(out, metrics.MetricViewAuthoritative, "")).To(Equal("0"))
		})
	})

	Describe("per-peer metrics", func() {
		It("sets warden_peer_up=1 only for alive peers", func() {
			out := scrape(server)
			Expect(metricLine(out, metrics.MetricPeerUp, `{peer="node-d"}`)).To(Equal("1"))
			Expect(metricLine(out, metrics.MetricPeerUp, `{peer="node-a"}`)).To(Equal("0"))
			Expect(metricLine(out, metrics.MetricPeerUp, `{peer="node-b"}`)).To(Equal("1"))
		})

		It("emits a strict one-hot warden_peer_status across {alive,suspect,dead,unknown}", func() {
			out := scrape(server)
			// node-a is dead: exactly the dead series is 1, the rest are 0.
			Expect(metricLine(out, metrics.MetricPeerStatus, `{peer="node-a",status="dead"}`)).To(Equal("1"))
			Expect(metricLine(out, metrics.MetricPeerStatus, `{peer="node-a",status="alive"}`)).To(Equal("0"))
			Expect(metricLine(out, metrics.MetricPeerStatus, `{peer="node-a",status="suspect"}`)).To(Equal("0"))
			Expect(metricLine(out, metrics.MetricPeerStatus, `{peer="node-a",status="unknown"}`)).To(Equal("0"))

			// Every peer emits all four status series, summing to exactly 1.
			for _, peer := range []string{"node-d", "node-a", "node-b"} {
				sum := 0.0
				for _, s := range []string{"alive", "suspect", "dead", "unknown"} {
					v := metricLine(out, metrics.MetricPeerStatus, `{peer="`+peer+`",status="`+s+`"}`)
					Expect(v).NotTo(BeEmpty(), "missing status series peer=%s status=%s", peer, s)
					if v == "1" {
						sum++
					}
				}
				Expect(sum).To(Equal(1.0), "peer %s is not one-hot", peer)
			}
		})

		It("omits warden_peer_latency_ms entirely when the RTT is 0, emits it otherwise", func() {
			out := scrape(server)
			// node-a has LatencyMS 0 -> no series at all.
			Expect(metricLine(out, metrics.MetricPeerLatencyMS, `{peer="node-a"}`)).To(BeEmpty())
			Expect(metricLine(out, metrics.MetricPeerLatencyMS, `{peer="node-d"}`)).To(BeEmpty())
			// node-b has 12.5 -> present.
			Expect(metricLine(out, metrics.MetricPeerLatencyMS, `{peer="node-b"}`)).To(Equal("12.5"))
		})
	})

	Describe("membership metrics", func() {
		It("reports version/voters/observers/discovered (unlabeled) and one-hot peer_member", func() {
			src.set(warden.ClusterView{
				Self: "node-d", Role: warden.RoleLeader, Term: 7, LeaderID: "node-d",
				Source: "node-d", Authoritative: true,
				Membership: warden.Membership{Version: 4, CreatedInTerm: 6, Voters: []warden.Node{{ID: "node-d"}, {ID: "node-a"}}},
				Peers: []warden.PeerView{
					{Node: warden.Node{ID: "node-d"}, Status: warden.StatusAlive}, // empty Member => voter
					{Node: warden.Node{ID: "node-a"}, Status: warden.StatusAlive, Member: warden.MemberVoter},
					{Node: warden.Node{ID: "obs1"}, Status: warden.StatusAlive, Member: warden.MemberObserver},
					{Node: warden.Node{ID: "disc1"}, Status: warden.StatusUnknown, Member: warden.MemberDiscovered},
				},
			})
			out := scrape(server)
			Expect(metricLine(out, metrics.MetricMembershipVersion, "")).To(Equal("4"))
			Expect(metricLine(out, metrics.MetricVoters, "")).To(Equal("2"))     // len(Membership.Voters)
			Expect(metricLine(out, metrics.MetricObservers, "")).To(Equal("1"))  // peers with Member==observer
			Expect(metricLine(out, metrics.MetricDiscovered, "")).To(Equal("1")) // peers with Member==discovered

			// One-hot warden_peer_member{peer,member} over {voter,observer,discovered}.
			Expect(metricLine(out, metrics.MetricPeerMember, `{member="observer",peer="obs1"}`)).To(Equal("1"))
			Expect(metricLine(out, metrics.MetricPeerMember, `{member="voter",peer="obs1"}`)).To(Equal("0"))
			Expect(metricLine(out, metrics.MetricPeerMember, `{member="discovered",peer="disc1"}`)).To(Equal("1"))
			// Empty Member is reported as voter.
			Expect(metricLine(out, metrics.MetricPeerMember, `{member="voter",peer="node-d"}`)).To(Equal("1"))

			for _, peer := range []string{"node-d", "node-a", "obs1", "disc1"} {
				sum := 0.0
				for _, m := range []string{"voter", "observer", "discovered"} {
					v := metricLine(out, metrics.MetricPeerMember, `{member="`+m+`",peer="`+peer+`"}`)
					Expect(v).NotTo(BeEmpty(), "missing peer_member series peer=%s member=%s", peer, m)
					if v == "1" {
						sum++
					}
				}
				Expect(sum).To(Equal(1.0), "peer %s is not one-hot on member", peer)
			}
		})

		It("reports zero membership gauges for a static / pre-membership view", func() {
			// The default leaderView() carries no Membership and all-voter peers.
			out := scrape(server)
			Expect(metricLine(out, metrics.MetricMembershipVersion, "")).To(Equal("0"))
			Expect(metricLine(out, metrics.MetricVoters, "")).To(Equal("0"))
			Expect(metricLine(out, metrics.MetricObservers, "")).To(Equal("0"))
			Expect(metricLine(out, metrics.MetricDiscovered, "")).To(Equal("0"))
		})
	})

	Describe("collect-on-scrape freshness", func() {
		It("reads a fresh view on every scrape (no cached state)", func() {
			before := src.reads()
			_ = scrape(server)
			_ = scrape(server)
			Expect(src.reads()).To(Equal(before + 2))
		})

		It("reflects a changed view on the very next scrape", func() {
			out1 := scrape(server)
			Expect(metricLine(out1, metrics.MetricTerm, `{node="node-d"}`)).To(Equal("7"))

			v := leaderView()
			v.Term = 8
			src.set(v)

			out2 := scrape(server)
			Expect(metricLine(out2, metrics.MetricTerm, `{node="node-d"}`)).To(Equal("8"))
		})
	})

	Describe("scrape hygiene", func() {
		It("exposes exactly the frozen warden metric families and nothing misspelled", func() {
			out := scrape(server)
			families := regexp.MustCompile(`(?m)^# TYPE (warden_[a-z_]+) `).FindAllStringSubmatch(out, -1)
			got := map[string]bool{}
			for _, m := range families {
				got[m[1]] = true
			}
			for _, want := range []string{
				"warden_is_leader", "warden_term", "warden_elections_total",
				"warden_view_authoritative",
				"warden_membership_version", "warden_voters", "warden_observers", "warden_discovered",
				"warden_peer_up", "warden_peer_status", "warden_peer_latency_ms", "warden_peer_member",
			} {
				Expect(got).To(HaveKey(want))
			}
			// Exactly twelve warden_* families — no more, no fewer. This catches a
			// spuriously-added or misspelled family, not just a missing one.
			Expect(got).To(HaveLen(12))
		})
	})
})
