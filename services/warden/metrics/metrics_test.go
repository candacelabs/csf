package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/httpserver"
	"github.com/candacelabs/csf/services/warden/internal/mocks"
)

// loopView is a channel-based, single-owner fake ViewSource. It models the
// real election manager: all reads and updates funnel through one goroutine so
// there is no shared mutable state and no lock, and every View() caller gets an
// immutable snapshot. Close stops the owner goroutine. It is a behavioral
// concurrency simulator (kept for the mutate-between-scrapes and concurrent
// scrape tests), not a mock.
type loopView struct {
	reads  chan chan warden.ClusterView
	writes chan warden.ClusterView
	done   chan struct{}
}

func newLoopView(initial warden.ClusterView) *loopView {
	lv := &loopView{
		reads:  make(chan chan warden.ClusterView),
		writes: make(chan warden.ClusterView),
		done:   make(chan struct{}),
	}
	go lv.run(initial)
	return lv
}

func (lv *loopView) run(cur warden.ClusterView) {
	for {
		select {
		case reply := <-lv.reads:
			reply <- cur
		case cur = <-lv.writes:
		case <-lv.done:
			return
		}
	}
}

func (lv *loopView) View() warden.ClusterView {
	reply := make(chan warden.ClusterView, 1)
	lv.reads <- reply
	return <-reply
}

func (lv *loopView) set(v warden.ClusterView) { lv.writes <- v }

func (lv *loopView) Subscribe(buf int) (<-chan warden.ClusterView, func()) {
	return nil, func() {} // unused by metrics
}

func (lv *loopView) Close() { close(lv.done) }

// mockView returns a MockIViewSource whose View() always yields the fixed
// snapshot v (metrics never calls Subscribe). Used for the static-view specs.
func mockView(v warden.ClusterView) *mocks.MockIViewSource {
	GinkgoHelper()
	ctrl := gomock.NewController(GinkgoT())
	mv := mocks.NewMockIViewSource(ctrl)
	mv.EXPECT().View().Return(v).AnyTimes()
	return mv
}

// onePerStatusView returns a leader view with exactly one peer in each of the
// four liveness statuses. The alive and suspect peers carry latency; the dead
// and unknown peers have none (so their latency series must be omitted).
func onePerStatusView() warden.ClusterView {
	return warden.ClusterView{
		Self:             "n1",
		Role:             warden.RoleLeader,
		Term:             5,
		LeaderID:         "n1",
		Source:           "n1",
		Authoritative:    true,
		ElectionsStarted: 3,
		Peers: []warden.PeerView{
			{Node: warden.Node{ID: "p-alive"}, Status: warden.StatusAlive, LatencyMS: 2.5},
			{Node: warden.Node{ID: "p-suspect"}, Status: warden.StatusSuspect, LatencyMS: 8.0},
			{Node: warden.Node{ID: "p-dead"}, Status: warden.StatusDead, LatencyMS: 0},
			{Node: warden.Node{ID: "p-unknown"}, Status: warden.StatusUnknown, LatencyMS: 0},
		},
	}
}

// oneOfEachMemberView returns a leader view carrying an explicit voting
// membership plus one peer of each membership kind and one memberless (legacy)
// peer that must be reported as a voter.
func oneOfEachMemberView() warden.ClusterView {
	return warden.ClusterView{
		Self:          "n1",
		Role:          warden.RoleLeader,
		Term:          5,
		LeaderID:      "n1",
		Source:        "n1",
		Authoritative: true,
		Membership: warden.Membership{
			Version: 4,
			Voters: []warden.Node{
				{ID: "n1"}, {ID: "v2"}, {ID: "v3"},
			},
		},
		Peers: []warden.PeerView{
			{Node: warden.Node{ID: "p-voter"}, Status: warden.StatusAlive, Member: warden.MemberVoter},
			{Node: warden.Node{ID: "p-observer"}, Status: warden.StatusAlive, Member: warden.MemberObserver},
			{Node: warden.Node{ID: "p-discovered"}, Status: warden.StatusUnknown, Member: warden.MemberDiscovered},
			{Node: warden.Node{ID: "p-legacy"}, Status: warden.StatusAlive}, // memberless -> voter
		},
	}
}

func scrape(mux http.Handler) string {
	GinkgoHelper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	Expect(rec.Code).To(Equal(http.StatusOK), "scrape status")
	return rec.Body.String()
}

// hasLine reports whether body has a metric line exactly equal to want. Exact
// line matching avoids false positives from value/label prefixes (e.g. term 5
// vs 50).
func hasLine(body, want string) bool {
	for _, line := range strings.Split(body, "\n") {
		if line == want {
			return true
		}
	}
	return false
}

func mustLines(body string, wants ...string) {
	GinkgoHelper()
	for _, w := range wants {
		Expect(hasLine(body, w)).To(BeTrue(), "missing metric line:\n  %s", w)
	}
}

var _ = Describe("Metrics collector", func() {
	// TestScrapeValues
	It("emits leader/term/peer/latency series with correct values", func() {
		mux := httpserver.NewEngine()
		New(mockView(onePerStatusView())).Register(mux)

		body := scrape(mux)

		// Leader/term/elections/authoritative.
		mustLines(body,
			`warden_is_leader{node="n1"} 1`,
			`warden_term{node="n1"} 5`,
			`warden_elections_total{node="n1"} 3`,
			`warden_view_authoritative 1`,
		)

		// Counter TYPE line for elections.
		Expect(body).To(ContainSubstring("# TYPE warden_elections_total counter"))

		// peer_up: 1 only for the alive peer.
		mustLines(body,
			`warden_peer_up{peer="p-alive"} 1`,
			`warden_peer_up{peer="p-suspect"} 0`,
			`warden_peer_up{peer="p-dead"} 0`,
			`warden_peer_up{peer="p-unknown"} 0`,
		)

		// peer_status one-hot correctness for each peer.
		peers := map[string]warden.PeerStatus{
			"p-alive":   warden.StatusAlive,
			"p-suspect": warden.StatusSuspect,
			"p-dead":    warden.StatusDead,
			"p-unknown": warden.StatusUnknown,
		}
		statuses := []warden.PeerStatus{warden.StatusAlive, warden.StatusSuspect, warden.StatusDead, warden.StatusUnknown}
		for peer, active := range peers {
			for _, s := range statuses {
				want := "0"
				if s == active {
					want = "1"
				}
				line := `warden_peer_status{peer="` + peer + `",status="` + string(s) + `"} ` + want
				Expect(hasLine(body, line)).To(BeTrue(), "missing one-hot status line:\n  %s", line)
			}
		}

		// Latency present for peers with a measured RTT, omitted for the others.
		mustLines(body,
			`warden_peer_latency_ms{peer="p-alive"} 2.5`,
			`warden_peer_latency_ms{peer="p-suspect"} 8`,
		)
		Expect(body).NotTo(ContainSubstring(`warden_peer_latency_ms{peer="p-dead"}`),
			"latency series must be omitted for 0-latency peer p-dead")
		Expect(body).NotTo(ContainSubstring(`warden_peer_latency_ms{peer="p-unknown"}`),
			"latency series must be omitted for 0-latency peer p-unknown")

		// Standard Go/process collectors present on the same registry.
		Expect(body).To(ContainSubstring("go_goroutines"))
	})

	// TestCollectOnScrape verifies the collector reads the view at scrape time:
	// mutating the view source between scrapes changes the emitted values with no
	// restart or re-registration.
	It("reads the view at scrape time so values track mutations", func() {
		lv := newLoopView(onePerStatusView())
		defer lv.Close()

		mux := httpserver.NewEngine()
		New(lv).Register(mux)

		body1 := scrape(mux)
		mustLines(body1,
			`warden_is_leader{node="n1"} 1`,
			`warden_term{node="n1"} 5`,
			`warden_view_authoritative 1`,
			`warden_elections_total{node="n1"} 3`,
		)

		// Step down: new leader elsewhere, higher term, non-authoritative view.
		mutated := onePerStatusView()
		mutated.Role = warden.RoleFollower
		mutated.Term = 6
		mutated.LeaderID = "n2"
		mutated.Authoritative = false
		mutated.ElectionsStarted = 4
		mutated.Peers[0].Status = warden.StatusDead // p-alive died
		lv.set(mutated)

		body2 := scrape(mux)
		mustLines(body2,
			`warden_is_leader{node="n1"} 0`,
			`warden_term{node="n1"} 6`,
			`warden_view_authoritative 0`,
			`warden_elections_total{node="n1"} 4`,
			`warden_peer_up{peer="p-alive"} 0`,
			`warden_peer_status{peer="p-alive",status="alive"} 0`,
			`warden_peer_status{peer="p-alive",status="dead"} 1`,
		)
	})

	// TestConcurrentScrapes stresses the collector under many simultaneous scrapes
	// while the view is concurrently mutated. Run with -race to catch data races.
	It("is race-free under concurrent scrapes with a concurrent mutator", func() {
		lv := newLoopView(onePerStatusView())
		defer lv.Close()

		mux := httpserver.NewEngine()
		New(lv).Register(mux)

		// Concurrent mutator: bumps only Term (never peer elements) so any race on
		// the collector's read path surfaces under -race.
		stop := make(chan struct{})
		mutDone := make(chan struct{})
		go func() {
			defer close(mutDone)
			v := onePerStatusView()
			for term := warden.Term(5); ; term++ {
				select {
				case <-stop:
					return
				default:
				}
				v.Term = term
				lv.set(v)
			}
		}()

		// Many concurrent scrapers.
		var scrapers sync.WaitGroup
		for i := 0; i < 24; i++ {
			scrapers.Add(1)
			go func() {
				defer scrapers.Done()
				defer GinkgoRecover()
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
				Expect(rec.Code).To(Equal(http.StatusOK), "concurrent scrape status")
			}()
		}

		scrapers.Wait()
		close(stop)
		<-mutDone
	})

	// TestMembershipGauges verifies the membership-level gauges and the one-hot
	// warden_peer_member series for a view with one peer of each membership kind
	// plus a memberless (legacy) peer.
	It("emits membership gauges and one-hot member series", func() {
		mux := httpserver.NewEngine()
		New(mockView(oneOfEachMemberView())).Register(mux)

		body := scrape(mux)

		// Membership-level gauges.
		mustLines(body,
			`warden_membership_version 4`,
			`warden_voters 3`,
			`warden_observers 1`,
			`warden_discovered 1`,
		)

		// warden_peer_member one-hot: exactly one kind is 1 per peer. The legacy
		// (memberless) peer reports as a voter.
		peers := map[string]warden.MemberKind{
			"p-voter":      warden.MemberVoter,
			"p-observer":   warden.MemberObserver,
			"p-discovered": warden.MemberDiscovered,
			"p-legacy":     warden.MemberVoter, // empty Member normalizes to voter
		}
		kinds := []warden.MemberKind{warden.MemberVoter, warden.MemberObserver, warden.MemberDiscovered}
		for peer, active := range peers {
			for _, k := range kinds {
				want := "0"
				if k == active {
					want = "1"
				}
				line := `warden_peer_member{member="` + string(k) + `",peer="` + peer + `"} ` + want
				Expect(hasLine(body, line)).To(BeTrue(), "missing one-hot member line:\n  %s", line)
			}
		}

		// peer_up/peer_status still emitted for every peer regardless of kind: the
		// observer and discovered peers must have their status series present.
		mustLines(body,
			`warden_peer_up{peer="p-observer"} 1`,
			`warden_peer_status{peer="p-discovered",status="unknown"} 1`,
		)
	})

	// TestMemberlessMetricsBackCompat verifies a pre-membership view (no Membership,
	// memberless peers): the voters gauge is 0 and every peer reports as a voter.
	It("reports memberless peers as voters with zeroed membership gauges", func() {
		mux := httpserver.NewEngine()
		New(mockView(onePerStatusView())).Register(mux) // no Membership, memberless peers

		body := scrape(mux)

		mustLines(body,
			`warden_membership_version 0`,
			`warden_voters 0`,
			`warden_observers 0`,
			`warden_discovered 0`,
		)

		// Every peer reports as a voter (one-hot), with observer/discovered at 0.
		for _, peer := range []string{"p-alive", "p-suspect", "p-dead", "p-unknown"} {
			mustLines(body,
				`warden_peer_member{member="voter",peer="`+peer+`"} 1`,
				`warden_peer_member{member="observer",peer="`+peer+`"} 0`,
				`warden_peer_member{member="discovered",peer="`+peer+`"} 0`,
			)
		}
	})

	// TestMetricsMethodNotAllowed
	It("returns 405 for POST /metrics", func() {
		mux := httpserver.NewEngine()
		New(mockView(onePerStatusView())).Register(mux)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/metrics", nil))
		Expect(rec.Code).To(Equal(http.StatusMethodNotAllowed))
	})
})
