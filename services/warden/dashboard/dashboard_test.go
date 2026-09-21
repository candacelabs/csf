package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/httpserver"
	"github.com/candacelabs/csf/services/warden/internal/mocks"
)

// --- test doubles -----------------------------------------------------------
//
// The dashboard reads immutable snapshots via View()/Incidents(). These gomock
// stubs are primed once and return the same snapshot on every call (the real
// implementations return copies from a single-owner event loop), so they stand
// in for the "already a snapshot" contract. The dashboard never calls Subscribe.

func mockView(v warden.ClusterView) *mocks.MockIViewSource {
	GinkgoHelper()
	ctrl := gomock.NewController(GinkgoT())
	mv := mocks.NewMockIViewSource(ctrl)
	mv.EXPECT().View().Return(v).AnyTimes()
	return mv
}

func mockIncidents(incs []warden.Incident) *mocks.MockIIncidentLog {
	GinkgoHelper()
	ctrl := gomock.NewController(GinkgoT())
	ml := mocks.NewMockIIncidentLog(ctrl)
	ml.EXPECT().Incidents().Return(incs).AnyTimes()
	return ml
}

// --- fixtures ---------------------------------------------------------------

// richView returns a healthy multi-peer view with one peer per liveness
// status, relative to now. UTC times keep JSON round-tripping stable.
func richView(now time.Time) warden.ClusterView {
	return warden.ClusterView{
		Self:             "node-c",
		Role:             warden.RoleLeader,
		Term:             7,
		LeaderID:         "node-c",
		Source:           "node-c",
		Authoritative:    true,
		UpdatedAt:        now.Add(-3 * time.Second),
		ElectionsStarted: 2,
		Peers: []warden.PeerView{
			{Node: warden.Node{ID: "node-b", Addr: "203.0.113.12:7717"}, Status: warden.StatusSuspect, LastSeen: now.Add(-90 * time.Second), LatencyMS: 12.5},
			{Node: warden.Node{ID: "node-c", Addr: "203.0.113.13:7717"}, Status: warden.StatusAlive, LastSeen: now.Add(-1 * time.Second), LatencyMS: 1.2},
			{Node: warden.Node{ID: "candace-edge", Addr: "203.0.113.14:7717"}, Status: warden.StatusDead, LastSeen: now.Add(-2 * time.Hour), LatencyMS: 0},
			{Node: warden.Node{ID: "zeta-cold", Addr: "100.64.0.9:7717"}, Status: warden.StatusUnknown, LastSeen: time.Time{}, LatencyMS: 0},
		},
	}
}

func richIncidents(now time.Time) []warden.Incident {
	return []warden.Incident{
		{
			ID:         "peer_dead/candace-edge/1",
			Type:       warden.IncidentPeerDead,
			Peer:       warden.Node{ID: "candace-edge", Addr: "203.0.113.14:7717"},
			Term:       7,
			ReportedBy: "node-c",
			DetectedAt: now.Add(-30 * time.Second),
			LastSeen:   now.Add(-2 * time.Hour),
			Message:    "candace-edge stopped responding to heartbeats",
		},
		{
			ID:         "peer_recovered/node-b/2",
			Type:       warden.IncidentPeerRecovered,
			Peer:       warden.Node{ID: "node-b", Addr: "203.0.113.12:7717"},
			Term:       7,
			ReportedBy: "node-c",
			DetectedAt: now.Add(-5 * time.Minute),
			Message:    "node-b is alive again",
		},
	}
}

// membershipView returns a dynamic-membership view: an explicit voting
// configuration plus one peer of each membership kind, including a memberless
// (legacy) row that must render as a voter for backward compatibility.
func membershipView(now time.Time) warden.ClusterView {
	return warden.ClusterView{
		Self:             "node-c",
		Role:             warden.RoleLeader,
		Term:             9,
		LeaderID:         "node-c",
		Source:           "node-c",
		Authoritative:    true,
		UpdatedAt:        now.Add(-2 * time.Second),
		ElectionsStarted: 1,
		Membership: warden.Membership{
			Version: 4,
			Voters: []warden.Node{
				{ID: "node-c", Addr: "203.0.113.13:7717"},
				{ID: "voter-two", Addr: "100.64.0.2:7717"},
				{ID: "voter-three", Addr: "100.64.0.3:7717"},
			},
		},
		Peers: []warden.PeerView{
			{Node: warden.Node{ID: "node-c", Addr: "203.0.113.13:7717"}, Status: warden.StatusAlive, LatencyMS: 1.0, Member: warden.MemberVoter},
			{Node: warden.Node{ID: "disc-node", Addr: "100.64.0.9:7717"}, Status: warden.StatusUnknown, Member: warden.MemberDiscovered},
			{Node: warden.Node{ID: "legacy-node", Addr: "100.64.0.10:7717"}, Status: warden.StatusAlive, LatencyMS: 2.0}, // memberless -> voter
			{Node: warden.Node{ID: "obs-node", Addr: "100.64.0.8:7717"}, Status: warden.StatusAlive, LatencyMS: 3.0, Member: warden.MemberObserver},
		},
	}
}

// newTestMux builds the dashboard router exactly as production does
// (httpserver.NewEngine), so the tests exercise the real middleware, 405, and
// 404 behavior. It returns an http.Handler (the underlying *gin.Engine).
func newTestMux(view warden.IViewSource, incs warden.IIncidentLog) http.Handler {
	GinkgoHelper()
	d, err := New(view, incs, "test-1.2.3")
	Expect(err).NotTo(HaveOccurred())
	engine := httpserver.NewEngine()
	d.Register(engine)
	return engine
}

func doGET(mux http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

var _ = Describe("dashboard HTTP surface", func() {
	// TestIndexPage
	It("renders the full index page with peers, incidents, and offline assets", func() {
		now := time.Now().UTC()
		mux := newTestMux(mockView(richView(now)), mockIncidents(richIncidents(now)))

		rec := doGET(mux, "/")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Content-Type")).To(HavePrefix("text/html"))
		body := rec.Body.String()

		// Full HTML shell present.
		Expect(body).To(ContainSubstring("<html"))
		// Embedded (offline-capable) assets: stylesheet + htmx served from the
		// binary, never from a CDN. warden must render with the WAN down.
		for _, want := range []string{`href="/assets/warden.css"`, `src="/assets/htmx.min.js"`} {
			Expect(body).To(ContainSubstring(want), "missing embedded asset reference")
		}
		for _, banned := range []string{"https://", "http://", "cdn.", "unpkg.com"} {
			Expect(body).NotTo(ContainSubstring(banned), "dashboard must be fully offline-capable")
		}
		// noscript meta-refresh fallback.
		Expect(body).To(ContainSubstring(`http-equiv="refresh"`))
		// HTMX polling wrapper.
		for _, want := range []string{`hx-get="/partials/cluster"`, `hx-trigger="every 2s"`, `hx-swap="innerHTML"`} {
			Expect(body).To(ContainSubstring(want), "missing HTMX attribute")
		}
		// Node identity, term, leader id.
		Expect(body).To(ContainSubstring("node-c"), "missing node identity/leader id")
		Expect(body).To(ContainSubstring(">7<"), "missing term value 7")
		// Every peer id renders.
		for _, id := range []string{"node-c", "node-b", "candace-edge", "zeta-cold"} {
			Expect(body).To(ContainSubstring(id), "missing peer id")
		}
		// Self marker.
		Expect(body).To(ContainSubstring("(this node)"))
		// Leader tag.
		Expect(body).To(ContainSubstring("[leader]"))
		// Status pill classes: one per status color (defined in warden.css).
		for _, want := range []string{"pill-green", "pill-amber", "pill-red", "pill-slate"} {
			Expect(body).To(ContainSubstring(want), "missing status class")
		}
		// Latency formatting and dash for unknown.
		Expect(body).To(ContainSubstring("1.2 ms"), "missing formatted latency")
		Expect(body).To(ContainSubstring("—"), "missing em-dash for unknown latency/last-seen")
		// Incident content.
		Expect(body).To(ContainSubstring("candace-edge stopped responding to heartbeats"))
		Expect(body).To(ContainSubstring("DEAD"))
		Expect(body).To(ContainSubstring("RECOVERED"))
		// Footer version.
		Expect(body).To(ContainSubstring("test-1.2.3"))
		// Healthy view must NOT show the leaderless/stale banners.
		Expect(body).NotTo(ContainSubstring("NO LEADER"))
		Expect(body).NotTo(ContainSubstring("local view — may be stale/incomplete"))
	})

	// TestIndexLeaderlessBanner
	It("renders the NO LEADER banner for a leaderless view", func() {
		now := time.Now().UTC()
		v := richView(now)
		v.LeaderID = ""
		v.Role = warden.RoleCandidate
		mux := newTestMux(mockView(v), mockIncidents(nil))

		body := doGET(mux, "/").Body.String()
		Expect(body).To(ContainSubstring("NO LEADER"))
	})

	// TestIndexStaleWarning
	It("renders the stale warning for a non-authoritative view", func() {
		now := time.Now().UTC()
		v := richView(now)
		v.Authoritative = false
		v.Source = "node-b"
		mux := newTestMux(mockView(v), mockIncidents(nil))

		body := doGET(mux, "/").Body.String()
		Expect(body).To(ContainSubstring("local view — may be stale/incomplete"))
	})

	// TestIndexEmptyIncidents
	It("renders the empty state for an empty incident log", func() {
		now := time.Now().UTC()
		mux := newTestMux(mockView(richView(now)), mockIncidents(nil))

		body := doGET(mux, "/").Body.String()
		Expect(body).To(ContainSubstring("No incidents"))
	})

	// TestEmbeddedAssets verifies the vendored assets serve from the binary so
	// the dashboard renders with no network egress at all.
	It("serves the vendored assets from the binary and 404s unknown assets", func() {
		now := time.Now().UTC()
		mux := newTestMux(mockView(richView(now)), mockIncidents(nil))

		css := doGET(mux, "/assets/warden.css")
		Expect(css.Code).To(Equal(http.StatusOK))
		Expect(css.Body.String()).To(ContainSubstring(".pill-green"))

		js := doGET(mux, "/assets/htmx.min.js")
		Expect(js.Code).To(Equal(http.StatusOK))
		Expect(js.Body.Len()).To(BeNumerically(">=", 10_000), "vendored asset suspiciously small")

		Expect(doGET(mux, "/assets/nope.js").Code).To(Equal(http.StatusNotFound))
	})

	// TestUnmatchedPath404
	It("404s an unmatched path (root must not swallow subpaths)", func() {
		now := time.Now().UTC()
		mux := newTestMux(mockView(richView(now)), mockIncidents(nil))
		Expect(doGET(mux, "/nonexistent").Code).To(Equal(http.StatusNotFound))
	})

	// TestMethodNotAllowed
	It("405s POST on GET-only routes", func() {
		now := time.Now().UTC()
		mux := newTestMux(mockView(richView(now)), mockIncidents(nil))

		for _, path := range []string{"/", "/partials/cluster", "/api/status"} {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
			Expect(rec.Code).To(Equal(http.StatusMethodNotAllowed), "POST %s", path)
		}
	})

	// TestPartial
	It("renders the cluster partial as a shell-less fragment", func() {
		now := time.Now().UTC()
		mux := newTestMux(mockView(richView(now)), mockIncidents(richIncidents(now)))

		rec := doGET(mux, "/partials/cluster")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Content-Type")).To(HavePrefix("text/html"))
		body := rec.Body.String()

		// The partial is a fragment: no HTML document shell.
		Expect(body).NotTo(ContainSubstring("<html"))
		Expect(body).NotTo(ContainSubstring("<!DOCTYPE"))
		// Cluster rows.
		for _, id := range []string{"node-c", "node-b", "candace-edge", "zeta-cold"} {
			Expect(body).To(ContainSubstring(id), "missing peer row")
		}
		// Leadership summary strip travels with the partial so polling reflects
		// leadership changes.
		Expect(body).To(ContainSubstring("LEADER"), "missing leadership summary strip")
		// Incident entries.
		Expect(body).To(ContainSubstring("candace-edge stopped responding to heartbeats"))
	})

	// TestAPIStatus
	It("serves /api/status as pretty-printed JSON that round-trips the view and incidents", func() {
		now := time.Now().UTC()
		view := richView(now)
		incs := richIncidents(now)
		mux := newTestMux(mockView(view), mockIncidents(incs))

		rec := doGET(mux, "/api/status")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Content-Type")).To(Equal("application/json"))
		raw := rec.Body.String()

		// Pretty-printed with two-space indent.
		Expect(raw).To(ContainSubstring("\n  "), "status response is not pretty-printed")

		var got StatusResponse
		Expect(json.Unmarshal([]byte(raw), &got)).To(Succeed(), "body:\n%s", raw)

		// View scalar fields round-trip.
		Expect(got.View.Self).To(Equal(view.Self))
		Expect(got.View.Role).To(Equal(view.Role))
		Expect(got.View.Term).To(Equal(view.Term))
		Expect(got.View.LeaderID).To(Equal(view.LeaderID))
		Expect(got.View.Source).To(Equal(view.Source))
		Expect(got.View.Authoritative).To(Equal(view.Authoritative))
		Expect(got.View.ElectionsStarted).To(Equal(view.ElectionsStarted))
		Expect(got.View.UpdatedAt).To(BeTemporally("==", view.UpdatedAt))

		// Peers round-trip.
		Expect(got.View.Peers).To(HaveLen(len(view.Peers)))
		for i, p := range view.Peers {
			g := got.View.Peers[i]
			Expect(g.Node).To(Equal(p.Node), "peer[%d]", i)
			Expect(g.Status).To(Equal(p.Status), "peer[%d]", i)
			Expect(g.LatencyMS).To(Equal(p.LatencyMS), "peer[%d]", i)
			Expect(g.LastSeen).To(BeTemporally("==", p.LastSeen), "peer[%d]", i)
		}

		// Incidents round-trip.
		Expect(got.Incidents).To(HaveLen(len(incs)))
		for i, in := range incs {
			g := got.Incidents[i]
			Expect(g.ID).To(Equal(in.ID), "incident[%d]", i)
			Expect(g.Type).To(Equal(in.Type), "incident[%d]", i)
			Expect(g.Peer).To(Equal(in.Peer), "incident[%d]", i)
			Expect(g.Term).To(Equal(in.Term), "incident[%d]", i)
			Expect(g.ReportedBy).To(Equal(in.ReportedBy), "incident[%d]", i)
			Expect(g.Message).To(Equal(in.Message), "incident[%d]", i)
			Expect(g.DetectedAt).To(BeTemporally("==", in.DetectedAt), "incident[%d]", i)
		}
	})

	// TestAPIStatusEmptyIncidents
	It("marshals empty incidents as [] and never null", func() {
		now := time.Now().UTC()
		mux := newTestMux(mockView(richView(now)), mockIncidents(nil))

		raw := doGET(mux, "/api/status").Body.String()
		Expect(raw).To(ContainSubstring(`"incidents": []`), "empty incidents must marshal as []")
		Expect(raw).NotTo(ContainSubstring(`"incidents": null`))
	})

	// TestXSSEscaping
	It("HTML-escapes hostile peer/incident content (no XSS)", func() {
		now := time.Now().UTC()
		const payload = `<script>alert(1)</script>`
		v := warden.ClusterView{
			Self:          "safe-node",
			Role:          warden.RoleLeader,
			Term:          1,
			LeaderID:      "safe-node",
			Source:        "safe-node",
			Authoritative: true,
			UpdatedAt:     now,
			Peers: []warden.PeerView{
				{Node: warden.Node{ID: warden.NodeID(payload), Addr: payload}, Status: warden.StatusAlive, LastSeen: now, LatencyMS: 1},
			},
		}
		incs := []warden.Incident{
			{ID: "x", Type: warden.IncidentPeerDead, Peer: warden.Node{ID: warden.NodeID(payload)}, Message: payload, DetectedAt: now},
		}
		mux := newTestMux(mockView(v), mockIncidents(incs))

		body := doGET(mux, "/").Body.String()
		Expect(body).NotTo(ContainSubstring(payload), "raw <script> payload leaked into HTML (XSS)")
		Expect(body).To(ContainSubstring("&lt;script&gt;alert(1)&lt;/script&gt;"), "expected HTML-escaped payload")
	})

	// TestMemberBadges verifies the per-row Member column renders one badge per
	// membership kind with the correct warden.css pill class and label, on both the
	// full page and the polled partial. A memberless (legacy) row renders as a
	// voter for backward compatibility.
	It("renders per-row member badges on the page and the partial", func() {
		now := time.Now().UTC()
		mux := newTestMux(mockView(membershipView(now)), mockIncidents(nil))

		for _, path := range []string{"/", "/partials/cluster"} {
			body := doGET(mux, path).Body.String()

			// A "Member" column header is present.
			Expect(body).To(ContainSubstring(">Member<"), "%s: missing Member column header", path)
			// Each kind renders its class+label together. pill-sky and pill-outline
			// are unique to the member column (status pills never use them), so a
			// class+label fragment uniquely proves the badge rendered.
			wants := []string{
				`pill-sky">OBSERVER<`,       // observer -> sky pill
				`pill-outline">DISCOVERED<`, // discovered -> dashed outline pill
				`pill-slate">VOTER<`,        // explicit voter + memberless -> slate pill
			}
			for _, w := range wants {
				Expect(body).To(ContainSubstring(w), "%s: missing member badge fragment", path)
			}
			// The memberless legacy row still appears and is labeled a voter.
			Expect(body).To(ContainSubstring("legacy-node"), "%s: memberless legacy row missing", path)
		}
	})

	// TestMembershipCard verifies the summary strip's Membership stat card shows the
	// version and voter count from the view's Membership.
	It("renders the Membership stat card with version and voter count", func() {
		now := time.Now().UTC()
		mux := newTestMux(mockView(membershipView(now)), mockIncidents(nil))

		body := doGET(mux, "/").Body.String()
		Expect(body).To(ContainSubstring(">Membership<"), "missing Membership stat card label")
		Expect(body).To(ContainSubstring("v4 · 3 voters"), "missing membership summary")
		// The partial carries the same strip so a membership change shows on poll.
		partial := doGET(mux, "/partials/cluster").Body.String()
		Expect(partial).To(ContainSubstring("v4 · 3 voters"), "partial missing membership summary")
	})

	// TestMemberlessBackCompat verifies a view with no membership and memberless
	// peers still renders: the Membership card shows "static" and every row is a
	// voter.
	It("renders a memberless view as static with voter rows only", func() {
		now := time.Now().UTC()
		mux := newTestMux(mockView(richView(now)), mockIncidents(nil))

		body := doGET(mux, "/").Body.String()
		Expect(body).To(ContainSubstring(">Membership<"), "memberless view missing Membership stat card")
		Expect(body).To(ContainSubstring("static"), "memberless view should render Membership card as 'static'")
		// Memberless peers render as voters.
		Expect(body).To(ContainSubstring(`pill-slate">VOTER<`), "memberless peers should render a VOTER badge")
		// No observer/discovered styling should appear for a memberless view.
		Expect(body).NotTo(ContainSubstring("pill-sky"))
		Expect(body).NotTo(ContainSubstring("pill-outline"))
		// Offline guard: still no external references.
		for _, banned := range []string{"https://", "http://", "cdn.", "unpkg.com"} {
			Expect(body).NotTo(ContainSubstring(banned), "memberless view references external resource")
		}
	})

	// TestAPIStatusMembershipRoundTrip asserts the new membership and per-peer
	// member fields survive the JSON round-trip through /api/status.
	It("round-trips membership and per-peer member fields through /api/status", func() {
		now := time.Now().UTC()
		view := membershipView(now)
		mux := newTestMux(mockView(view), mockIncidents(nil))

		raw := doGET(mux, "/api/status").Body.String()
		var got StatusResponse
		Expect(json.Unmarshal([]byte(raw), &got)).To(Succeed(), "body:\n%s", raw)

		// Membership version + voters round-trip.
		Expect(got.View.Membership.Version).To(Equal(view.Membership.Version))
		Expect(got.View.Membership.Voters).To(HaveLen(len(view.Membership.Voters)))
		for i, v := range view.Membership.Voters {
			Expect(got.View.Membership.Voters[i]).To(Equal(v), "voter[%d]", i)
		}
		// Per-peer Member field round-trips (including the memberless -> "" case).
		Expect(got.View.Peers).To(HaveLen(len(view.Peers)))
		for i, p := range view.Peers {
			Expect(got.View.Peers[i].Member).To(Equal(p.Member), "peer[%d] Member", i)
		}
	})
})
