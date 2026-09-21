package dashboard_test

// Contract tests for the dashboard/API surface, asserted through real HTTP.
// These freeze: the /api/status JSON shape (pretty-printed, incidents never
// null), route status codes (exact-root match, GET-only, embedded assets),
// offline self-containment (no external URLs in rendered HTML), HTML escaping,
// and the HTMX / noscript / banner behaviours operators rely on.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/dashboard"
	"github.com/candacelabs/csf/services/warden/httpserver"
)

func TestDashboardContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "dashboard contract suite")
}

var fixedTime = time.Date(2026, 7, 21, 15, 4, 5, 0, time.UTC)

// stubView is a warden.IViewSource returning a fixed view.
type stubView struct {
	mu sync.Mutex
	v  warden.ClusterView
}

func (s *stubView) set(v warden.ClusterView) { s.mu.Lock(); s.v = v; s.mu.Unlock() }
func (s *stubView) View() warden.ClusterView { s.mu.Lock(); defer s.mu.Unlock(); return s.v }
func (s *stubView) Subscribe(buf int) (<-chan warden.ClusterView, func()) {
	return make(chan warden.ClusterView, buf), func() {}
}

// stubLog is a warden.IIncidentLog returning a fixed (possibly nil) slice.
type stubLog struct{ incidents []warden.Incident }

func (s *stubLog) Incidents() []warden.Incident { return s.incidents }

// newDashboardServer isolates the concrete-mux dependency (Register(mux)). The
// eventual Gin port changes this one helper; every HTTP assertion below stays.
func newDashboardServer(v warden.IViewSource, log warden.IIncidentLog, version string) *httptest.Server {
	d, err := dashboard.New(v, log, version)
	Expect(err).NotTo(HaveOccurred())
	engine := httpserver.NewEngine()
	d.Register(engine)
	return httptest.NewServer(engine)
}

func healthyView() warden.ClusterView {
	return warden.ClusterView{
		Self: "node-d", Role: warden.RoleLeader, Term: 7, LeaderID: "node-d",
		Source: "node-d", Authoritative: true, UpdatedAt: fixedTime, ElectionsStarted: 2,
		Peers: []warden.PeerView{
			{Node: warden.Node{ID: "node-d", Addr: "203.0.113.14:7717"}, Status: warden.StatusAlive, LastSeen: fixedTime, LatencyMS: 1.2},
			{Node: warden.Node{ID: "node-a", Addr: "203.0.113.11:7717"}, Status: warden.StatusDead},
		},
	}
}

func get(server *httptest.Server, path string) (*http.Response, string) {
	resp, err := http.Get(server.URL + path)
	Expect(err).NotTo(HaveOccurred())
	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	resp.Body.Close()
	return resp, string(body)
}

var _ = Describe("GET /api/status", func() {
	It("returns application/json, pretty-printed, with the full StatusResponse shape", func() {
		view := &stubView{}
		view.set(healthyView())
		inc := warden.Incident{
			ID: "peer_dead/node-a/1784646245", Type: warden.IncidentPeerDead,
			Peer: warden.Node{ID: "node-a", Addr: "203.0.113.11:7717"}, Term: 7,
			ReportedBy: "node-d", DetectedAt: fixedTime, LastSeen: fixedTime,
			Message: "peer node-a declared dead",
		}
		server := newDashboardServer(view, &stubLog{incidents: []warden.Incident{inc}}, "v1.2.3")
		defer server.Close()

		resp, body := get(server, warden.PathAPIStatus)
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
		// Pretty-printed with a two-space indent (operators curl this).
		Expect(body).To(ContainSubstring("\n  \"view\": {"))

		Expect(body).To(MatchJSON(`{
			"view": {
				"self": "node-d", "role": "leader", "term": 7, "leader_id": "node-d",
				"source": "node-d", "authoritative": true, "updated_at": "2026-07-21T15:04:05Z",
				"peers": [
					{"node":{"id":"node-d","addr":"203.0.113.14:7717"},"status":"alive","last_seen":"2026-07-21T15:04:05Z","latency_ms":1.2},
					{"node":{"id":"node-a","addr":"203.0.113.11:7717"},"status":"dead","last_seen":"0001-01-01T00:00:00Z","latency_ms":0}
				],
				"elections_started": 2,
				"membership": {"version":0,"created_in_term":0,"voters":null}
			},
			"incidents": [
				{"id":"peer_dead/node-a/1784646245","type":"peer_dead","peer":{"id":"node-a","addr":"203.0.113.11:7717"},"term":7,"reported_by":"node-d","detected_at":"2026-07-21T15:04:05Z","last_seen":"2026-07-21T15:04:05Z","message":"peer node-a declared dead"}
			]
		}`))
	})

	It("renders incidents as [] (never null) when the log is empty", func() {
		view := &stubView{}
		view.set(healthyView())
		// stubLog with a nil slice: the handler must still emit [].
		server := newDashboardServer(view, &stubLog{incidents: nil}, "v1")
		defer server.Close()

		_, body := get(server, warden.PathAPIStatus)
		Expect(body).To(ContainSubstring(`"incidents": []`))
		Expect(body).NotTo(ContainSubstring(`"incidents": null`))

		var parsed dashboard.StatusResponse
		Expect(json.Unmarshal([]byte(body), &parsed)).To(Succeed())
		Expect(parsed.Incidents).NotTo(BeNil())
		Expect(parsed.Incidents).To(BeEmpty())
	})
})

var _ = Describe("Route status codes", func() {
	var server *httptest.Server

	BeforeEach(func() {
		view := &stubView{}
		view.set(healthyView())
		server = newDashboardServer(view, &stubLog{}, "v1")
	})
	AfterEach(func() { server.Close() })

	It("serves the dashboard at the EXACT root path", func() {
		resp, body := get(server, "/")
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Type")).To(Equal("text/html; charset=utf-8"))
		Expect(body).To(ContainSubstring("candacenet"))
	})

	It("404s unmatched paths ({$} exact-match keeps the root from swallowing them)", func() {
		resp, _ := get(server, "/definitely-not-a-route")
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})

	It("serves the HTMX cluster partial", func() {
		resp, _ := get(server, warden.PathClusterPartial)
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Type")).To(Equal("text/html; charset=utf-8"))
	})

	It("serves embedded assets and 404s unknown asset paths", func() {
		resp, body := get(server, "/assets/warden.css")
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(body).NotTo(BeEmpty())

		resp2, _ := get(server, "/assets/nope.css")
		Expect(resp2.StatusCode).To(Equal(http.StatusNotFound))
	})

	DescribeTable("rejects non-GET methods with 405 (routes are GET-only)",
		func(method, path string) {
			req, err := http.NewRequest(method, server.URL+path, nil)
			Expect(err).NotTo(HaveOccurred())
			resp, err := http.DefaultClient.Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
		},
		Entry("POST /", http.MethodPost, "/"),
		Entry("POST /api/status", http.MethodPost, warden.PathAPIStatus),
		Entry("POST /partials/cluster", http.MethodPost, warden.PathClusterPartial),
		Entry("PUT /api/status", http.MethodPut, warden.PathAPIStatus),
		Entry("DELETE /", http.MethodDelete, "/"),
	)
})

var _ = Describe("Offline self-containment", func() {
	// warden is a fault-tolerance tool: the page must render with the WAN down,
	// so the rendered HTML must reference no external host.
	externalURL := regexp.MustCompile(`(?i)(https?:)?//[a-z0-9.\-]+`)

	It("emits no external URLs in the dashboard HTML (only local /assets/ links)", func() {
		view := &stubView{}
		view.set(healthyView())
		server := newDashboardServer(view, &stubLog{}, "v1")
		defer server.Close()

		for _, path := range []string{"/", warden.PathClusterPartial} {
			_, body := get(server, path)
			matches := externalURL.FindAllString(body, -1)
			Expect(matches).To(BeEmpty(),
				"rendered %s references an external URL: %v", path, matches)
			// Asset links are root-relative, not protocol-relative or absolute.
			Expect(body).NotTo(ContainSubstring(`src="//`))
			Expect(body).NotTo(ContainSubstring(`href="//`))
		}
	})

	It("references its assets by root-relative /assets/ paths", func() {
		view := &stubView{}
		view.set(healthyView())
		server := newDashboardServer(view, &stubLog{}, "v1")
		defer server.Close()
		_, body := get(server, "/")
		Expect(body).To(ContainSubstring(`href="/assets/warden.css"`))
		Expect(body).To(ContainSubstring(`src="/assets/htmx.min.js"`))
	})

	It("ships embedded assets that themselves reference no external host", func() {
		// The offline contract also depends on the CSS/JS not pulling in a CDN
		// font, @import, or url() from the network.
		view := &stubView{}
		view.set(healthyView())
		server := newDashboardServer(view, &stubLog{}, "v1")
		defer server.Close()
		for _, asset := range []string{"/assets/warden.css", "/assets/htmx.min.js"} {
			resp, body := get(server, asset)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(externalURL.FindAllString(body, -1)).To(BeEmpty(),
				"embedded asset %s references an external URL", asset)
		}
	})
})

var _ = Describe("HTML escaping (XSS defense)", func() {
	It("escapes a node id containing HTML metacharacters", func() {
		view := &stubView{}
		view.set(warden.ClusterView{
			Self: `<script>alert(1)</script>`, Role: warden.RoleFollower, Term: 1,
			LeaderID: "node-d", Source: "node-d", Authoritative: true, UpdatedAt: fixedTime,
			Peers: []warden.PeerView{
				{Node: warden.Node{ID: `<script>alert(1)</script>`, Addr: "x:1"}, Status: warden.StatusAlive, LastSeen: fixedTime},
			},
		})
		server := newDashboardServer(view, &stubLog{}, "v1")
		defer server.Close()
		_, body := get(server, "/")
		Expect(body).NotTo(ContainSubstring("<script>alert(1)</script>"))
		Expect(body).To(ContainSubstring("&lt;script&gt;"))
	})
})

var _ = Describe("Progressive-enhancement markers", func() {
	var body string

	BeforeEach(func() {
		view := &stubView{}
		view.set(healthyView())
		server := newDashboardServer(view, &stubLog{}, "v1")
		DeferCleanup(server.Close)
		_, body = get(server, "/")
	})

	It("polls the cluster partial via HTMX every 2s, swapping innerHTML", func() {
		Expect(body).To(ContainSubstring(`hx-get="/partials/cluster"`))
		Expect(body).To(ContainSubstring(`hx-trigger="every 2s"`))
		Expect(body).To(ContainSubstring(`hx-swap="innerHTML"`))
	})

	It("provides a noscript meta-refresh fallback", func() {
		Expect(body).To(ContainSubstring("<noscript>"))
		Expect(body).To(MatchRegexp(`(?i)<noscript><meta http-equiv="refresh" content="5"`))
	})
})

var _ = Describe("Dashboard banners and incident feed", func() {
	render := func(v warden.ClusterView, incidents []warden.Incident) string {
		view := &stubView{}
		view.set(v)
		server := newDashboardServer(view, &stubLog{incidents: incidents}, "v1")
		DeferCleanup(server.Close)
		_, body := get(server, warden.PathClusterPartial)
		return body
	}

	It("shows the NO LEADER banner when leaderless, and hides it when a leader is known", func() {
		leaderless := healthyView()
		leaderless.LeaderID = ""
		Expect(render(leaderless, nil)).To(ContainSubstring("NO LEADER"))

		Expect(render(healthyView(), nil)).NotTo(ContainSubstring(
			"NO LEADER — the cluster is currently leaderless"))
	})

	It("shows the local/stale-view banner when the view is not authoritative", func() {
		stale := healthyView()
		stale.Authoritative = false
		body := render(stale, nil)
		Expect(body).To(ContainSubstring("local view — may be stale/incomplete"))

		Expect(render(healthyView(), nil)).NotTo(ContainSubstring(
			"local view — may be stale/incomplete"))
	})

	It("renders incidents with their type badge label and message", func() {
		// Use a RECOVERED incident: the label "RECOVERED" is emitted ONLY by the
		// incident badge (incidentLabel), never by a status pill or role badge, so
		// asserting it isolates incident rendering. (A dead incident's "DEAD" label
		// would collide with a dead peer's status pill.)
		recovered := warden.Incident{
			ID: "peer_recovered/node-a/1", Type: warden.IncidentPeerRecovered,
			Peer: warden.Node{ID: "node-a"}, Term: 7, ReportedBy: "node-d",
			DetectedAt: fixedTime, Message: "peer node-a recovered after outage",
		}
		body := render(healthyView(), []warden.Incident{recovered})
		Expect(body).To(ContainSubstring("RECOVERED"))
		Expect(body).To(ContainSubstring("peer node-a recovered after outage"))
		Expect(body).NotTo(ContainSubstring("No incidents"))
	})

	It("shows the empty-state when there are no incidents", func() {
		Expect(render(healthyView(), nil)).To(ContainSubstring("No incidents"))
	})
})

var _ = Describe("Membership dashboard elements", func() {
	renderPartial := func(v warden.ClusterView) string {
		view := &stubView{}
		view.set(v)
		server := newDashboardServer(view, &stubLog{}, "v1")
		DeferCleanup(server.Close)
		_, body := get(server, warden.PathClusterPartial)
		return body
	}

	It("renders a Member column header in the cluster table", func() {
		Expect(renderPartial(healthyView())).To(ContainSubstring(`<th scope="col">Member</th>`))
	})

	It("renders member pills with the class and label for each kind", func() {
		v := warden.ClusterView{
			Self: "node-d", Role: warden.RoleLeader, Term: 7, LeaderID: "node-d",
			Source: "node-d", Authoritative: true, UpdatedAt: fixedTime,
			Membership: warden.Membership{Version: 3, Voters: []warden.Node{{ID: "node-d"}, {ID: "node-a"}}},
			Peers: []warden.PeerView{
				{Node: warden.Node{ID: "node-d", Addr: "a:1"}, Status: warden.StatusAlive}, // empty Member => voter
				{Node: warden.Node{ID: "obs1", Addr: "b:1"}, Status: warden.StatusAlive, Member: warden.MemberObserver},
				{Node: warden.Node{ID: "disc1", Addr: "c:1"}, Status: warden.StatusUnknown, Member: warden.MemberDiscovered},
			},
		}
		body := renderPartial(v)
		Expect(body).To(ContainSubstring("pill-sky"))     // observer color
		Expect(body).To(ContainSubstring("OBSERVER"))     // observer label
		Expect(body).To(ContainSubstring("pill-outline")) // discovered color
		Expect(body).To(ContainSubstring("DISCOVERED"))   // discovered label
		Expect(body).To(ContainSubstring("VOTER"))        // empty Member renders as VOTER
	})

	It("shows the membership stat card as 'v<version> · <n> voters' when a config is in effect", func() {
		v := healthyView()
		v.Membership = warden.Membership{Version: 3, CreatedInTerm: 4, Voters: []warden.Node{{ID: "node-d"}, {ID: "node-a"}}}
		body := renderPartial(v)
		Expect(body).To(ContainSubstring("Membership"))
		Expect(body).To(ContainSubstring("v3 · 2 voters"))
	})

	It("shows the membership stat card as 'static' when there are no voters", func() {
		// healthyView carries no membership => the summary renders as static.
		Expect(renderPartial(healthyView())).To(ContainSubstring(">static<"))
	})
})
