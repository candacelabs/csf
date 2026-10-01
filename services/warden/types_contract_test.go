package warden_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
)

// These specs freeze the JSON wire form of every exported contract type. The
// assertions are on the EXACT marshaled byte string (not just round-trip
// equality) because two warden nodes of different builds exchange these types
// over HTTP: field names, field order, omitempty behaviour, and zero-value
// rendering are all part of the cross-version compatibility contract.

var _ = Describe("Contract type JSON serialization", func() {

	Describe("string-enum constants", func() {
		// These string values travel on the wire (inside ClusterView / Incident)
		// and are matched verbatim by peers and by operator tooling. A rename is
		// a breaking protocol change.
		DescribeTable("hold their documented string value",
			func(got, want string) { Expect(got).To(Equal(want)) },
			Entry("RoleFollower", string(warden.RoleFollower), "follower"),
			Entry("RoleCandidate", string(warden.RoleCandidate), "candidate"),
			Entry("RoleLeader", string(warden.RoleLeader), "leader"),
			Entry("StatusUnknown", string(warden.StatusUnknown), "unknown"),
			Entry("StatusAlive", string(warden.StatusAlive), "alive"),
			Entry("StatusSuspect", string(warden.StatusSuspect), "suspect"),
			Entry("StatusDead", string(warden.StatusDead), "dead"),
			Entry("IncidentPeerDead", string(warden.IncidentPeerDead), "peer_dead"),
			Entry("IncidentPeerRecovered", string(warden.IncidentPeerRecovered), "peer_recovered"),
		)
	})

	Describe("Node", func() {
		It("marshals populated values with the id,addr field order", func() {
			b, err := json.Marshal(warden.Node{ID: "node-c", Addr: "203.0.113.13:7717"})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal(`{"id":"node-c","addr":"203.0.113.13:7717"}`))
		})

		It("marshals the zero value with empty (non-omitted) fields", func() {
			b, err := json.Marshal(warden.Node{})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal(`{"id":"","addr":""}`))
		})

		It("round-trips exactly", func() {
			in := warden.Node{ID: "node-d", Addr: "203.0.113.14:7717"}
			var out warden.Node
			Expect(json.Unmarshal([]byte(`{"id":"node-d","addr":"203.0.113.14:7717"}`), &out)).To(Succeed())
			Expect(out).To(Equal(in))
		})
	})

	Describe("PeerView", func() {
		It("marshals with node,status,last_seen,latency_ms and never omits", func() {
			b, err := json.Marshal(warden.PeerView{
				Node:      warden.Node{ID: "node-d", Addr: "203.0.113.14:7717"},
				Status:    warden.StatusAlive,
				LastSeen:  fixedTime,
				LatencyMS: 1.5,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal(
				`{"node":{"id":"node-d","addr":"203.0.113.14:7717"},"status":"alive","last_seen":"` +
					fixedTimeJSON + `","latency_ms":1.5}`))
		})

		It("renders the zero value with a zero timestamp and latency_ms 0", func() {
			b, err := json.Marshal(warden.PeerView{})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal(
				`{"node":{"id":"","addr":""},"status":"","last_seen":"` + zeroTimeJSON + `","latency_ms":0}`))
		})
	})

	Describe("ClusterView", func() {
		It("marshals the fully-populated view in the frozen field order", func() {
			cv := warden.ClusterView{
				Self:          "node-d",
				Role:          warden.RoleLeader,
				Term:          7,
				LeaderID:      "node-d",
				Source:        "node-d",
				Authoritative: true,
				UpdatedAt:     fixedTime,
				Peers: []warden.PeerView{
					{Node: warden.Node{ID: "node-d", Addr: "203.0.113.24:7717"}, Status: warden.StatusAlive, LastSeen: fixedTime, LatencyMS: 0},
				},
				ElectionsStarted: 3,
				Membership: warden.Membership{
					Version: 1, CreatedInTerm: 2,
					Voters: []warden.Node{{ID: "node-d", Addr: "203.0.113.24:7717"}},
				},
			}
			b, err := json.Marshal(cv)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal(
				`{"self":"node-d","role":"leader","term":7,"leader_id":"node-d","source":"node-d",` +
					`"authoritative":true,"updated_at":"` + fixedTimeJSON + `",` +
					`"peers":[{"node":{"id":"node-d","addr":"203.0.113.24:7717"},"status":"alive",` +
					`"last_seen":"` + fixedTimeJSON + `","latency_ms":0}],"elections_started":3,` +
					`"membership":{"version":1,"created_in_term":2,"voters":[{"id":"node-d","addr":"203.0.113.24:7717"}]}}`))
		})

		It("renders a nil Peers slice as JSON null and always emits membership", func() {
			// Peers is NOT omitempty (nil -> null); the dashboard handler is what
			// guarantees incidents:[] elsewhere. Membership is also NOT omitempty,
			// so the zero membership is always present with a null voters slice.
			b, err := json.Marshal(warden.ClusterView{})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal(
				`{"self":"","role":"","term":0,"leader_id":"","source":"","authoritative":false,` +
					`"updated_at":"` + zeroTimeJSON + `","peers":null,"elections_started":0,` +
					`"membership":{"version":0,"created_in_term":0,"voters":null}}`))
		})

		It("round-trips a populated view without loss", func() {
			cv := warden.ClusterView{
				Self: "n1", Role: warden.RoleFollower, Term: 42, LeaderID: "n2",
				Source: "n2", Authoritative: false, UpdatedAt: fixedTime,
				Peers:            []warden.PeerView{{Node: warden.Node{ID: "n2", Addr: "a:1"}, Status: warden.StatusSuspect, LastSeen: fixedTime}},
				ElectionsStarted: 9,
			}
			data, err := json.Marshal(cv)
			Expect(err).NotTo(HaveOccurred())
			var out warden.ClusterView
			Expect(json.Unmarshal(data, &out)).To(Succeed())
			Expect(out).To(Equal(cv))
		})
	})

	Describe("Incident", func() {
		It("marshals in the frozen field order", func() {
			inc := warden.Incident{
				ID:         "peer_dead/node-a/1784646245",
				Type:       warden.IncidentPeerDead,
				Peer:       warden.Node{ID: "node-a", Addr: "203.0.113.11:7717"},
				Term:       7,
				ReportedBy: "node-d",
				DetectedAt: fixedTime,
				LastSeen:   fixedTime,
				Message:    "peer node-a died",
			}
			b, err := json.Marshal(inc)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal(
				`{"id":"peer_dead/node-a/1784646245","type":"peer_dead",` +
					`"peer":{"id":"node-a","addr":"203.0.113.11:7717"},"term":7,` +
					`"reported_by":"node-d","detected_at":"` + fixedTimeJSON + `",` +
					`"last_seen":"` + fixedTimeJSON + `","message":"peer node-a died"}`))
		})

		It("renders the zero value with both timestamps present", func() {
			b, err := json.Marshal(warden.Incident{})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal(
				`{"id":"","type":"","peer":{"id":"","addr":""},"term":0,"reported_by":"",` +
					`"detected_at":"` + zeroTimeJSON + `","last_seen":"` + zeroTimeJSON + `","message":""}`))
		})
	})
})

var _ = Describe("Quorum arithmetic", func() {
	// The majority threshold is the safety linchpin: two majorities of the same
	// set must overlap, which is what forbids two leaders per term. These values
	// are frozen for clusters up to the real fleet size and beyond.
	DescribeTable("returns n/2+1 (strict majority, counting self)",
		func(n, want int) { Expect(warden.Quorum(n)).To(Equal(want)) },
		Entry("n=1 (trivial)", 1, 1),
		Entry("n=2", 2, 2),
		Entry("n=3", 3, 2),
		Entry("n=4 (even: tolerates 1 loss, needs 3)", 4, 3),
		Entry("n=5", 5, 3),
		Entry("n=6 (even: needs 4)", 6, 4),
		Entry("n=7", 7, 4),
	)

	It("guarantees two quorums of an n-node cluster always intersect", func() {
		// 2*Quorum(n) > n  <=>  any two quorums share at least one member.
		for n := 1; n <= 33; n++ {
			Expect(2*warden.Quorum(n)).To(BeNumerically(">", n),
				"two quorums must overlap for n=%d", n)
		}
	})
})

var _ = Describe("SortPeers", func() {
	It("orders peers by node ID ascending, the canonical ClusterView order", func() {
		peers := []warden.PeerView{
			{Node: warden.Node{ID: "node-d"}},
			{Node: warden.Node{ID: "node-a"}},
			{Node: warden.Node{ID: "node-c"}},
			{Node: warden.Node{ID: "node-b"}},
		}
		warden.SortPeers(peers)
		got := []warden.NodeID{}
		for _, p := range peers {
			got = append(got, p.Node.ID)
		}
		Expect(got).To(Equal([]warden.NodeID{"node-a", "node-b", "node-c", "node-d"}))
	})

	It("is a no-op on an empty slice", func() {
		var peers []warden.PeerView
		Expect(func() { warden.SortPeers(peers) }).NotTo(Panic())
		Expect(peers).To(BeEmpty())
	})
})

var _ = Describe("NewIncidentID", func() {
	It("builds the canonical type/peer/unix-seconds id", func() {
		// 1784646245 is fixedTime.Unix(); the id embeds whole seconds.
		Expect(warden.NewIncidentID(warden.IncidentPeerDead, "node-a", fixedTime)).
			To(Equal("peer_dead/node-a/1784646245"))
		Expect(warden.NewIncidentID(warden.IncidentPeerRecovered, "node-d", fixedTime)).
			To(Equal("peer_recovered/node-d/1784646245"))
	})
})

var _ = Describe("Contract type field tags", func() {
	// Pins every ClusterView json tag AND the field order by asserting the exact
	// marshaled bytes of a fully-populated value under distinct sentinel fields —
	// a literal golden-marshal check (no reflection). An accidental tag rename or
	// field reorder fails here just as a reflection walk of the tags would, and it
	// additionally freezes value rendering and field order.
	It("marshals every field under its frozen json name, in order", func() {
		cv := warden.ClusterView{
			Self: "s", Role: warden.RoleCandidate, Term: 11, LeaderID: "ld",
			Source: "src", Authoritative: true, UpdatedAt: fixedTime,
			Peers: []warden.PeerView{
				{Node: warden.Node{ID: "p", Addr: "h:1"}, Status: warden.StatusSuspect, LastSeen: fixedTime, LatencyMS: 4},
			},
			ElectionsStarted: 6,
			Membership:       warden.Membership{Version: 5, CreatedInTerm: 8, Voters: []warden.Node{{ID: "p", Addr: "h:1"}}},
		}
		b, err := json.Marshal(cv)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal(
			`{"self":"s","role":"candidate","term":11,"leader_id":"ld","source":"src",` +
				`"authoritative":true,"updated_at":"` + fixedTimeJSON + `",` +
				`"peers":[{"node":{"id":"p","addr":"h:1"},"status":"suspect",` +
				`"last_seen":"` + fixedTimeJSON + `","latency_ms":4}],"elections_started":6,` +
				`"membership":{"version":5,"created_in_term":8,"voters":[{"id":"p","addr":"h:1"}]}}`))
	})
})

// --- Dynamic membership contract (added on the candacenet-warden merge) -------

var _ = Describe("MemberKind constants", func() {
	DescribeTable("hold their documented string value",
		func(got, want string) { Expect(got).To(Equal(want)) },
		Entry("MemberVoter", string(warden.MemberVoter), "voter"),
		Entry("MemberObserver", string(warden.MemberObserver), "observer"),
		Entry("MemberDiscovered", string(warden.MemberDiscovered), "discovered"),
	)
})

var _ = Describe("PeerView.Member", func() {
	It("omits member for a voter (empty MemberKind => voter, omitempty)", func() {
		b, err := json.Marshal(warden.PeerView{
			Node: warden.Node{ID: "a", Addr: "x:1"}, Status: warden.StatusAlive,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal(
			`{"node":{"id":"a","addr":"x:1"},"status":"alive","last_seen":"` + zeroTimeJSON + `","latency_ms":0}`))
		Expect(string(b)).NotTo(ContainSubstring("member"))
	})

	It("emits member for an observer/discovered peer", func() {
		b, _ := json.Marshal(warden.PeerView{
			Node: warden.Node{ID: "a", Addr: "x:1"}, Status: warden.StatusAlive, Member: warden.MemberObserver,
		})
		Expect(string(b)).To(Equal(
			`{"node":{"id":"a","addr":"x:1"},"status":"alive","last_seen":"` + zeroTimeJSON + `","latency_ms":0,"member":"observer"}`))
	})
})

var _ = Describe("Membership JSON", func() {
	It("marshals version,created_in_term,voters", func() {
		b, err := json.Marshal(warden.Membership{
			Version: 3, CreatedInTerm: 7, Voters: []warden.Node{{ID: "a", Addr: "1:1"}},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal(`{"version":3,"created_in_term":7,"voters":[{"id":"a","addr":"1:1"}]}`))
	})

	It("renders the zero value with a null voters slice (voters is NOT omitempty)", func() {
		b, _ := json.Marshal(warden.Membership{})
		Expect(string(b)).To(Equal(`{"version":0,"created_in_term":0,"voters":null}`))
	})

	It("HasVoter reports voting-set membership", func() {
		m := warden.Membership{Voters: []warden.Node{{ID: "a"}, {ID: "b"}}}
		Expect(m.HasVoter("a")).To(BeTrue())
		Expect(m.HasVoter("z")).To(BeFalse())
	})
})

var _ = Describe("Membership.Supersedes", func() {
	// Identity is the lexicographic (Version, CreatedInTerm) pair — never a bare
	// version compare. This is the split-brain guard for membership configs: a
	// deposed leader's persisted-but-undisseminated config shares a Version with
	// the next leader's, and only the higher CreatedInTerm wins.
	DescribeTable("orders by (Version, CreatedInTerm) lexicographically",
		func(a, b warden.Membership, want bool) { Expect(a.Supersedes(b)).To(Equal(want)) },
		Entry("equal version, higher term supersedes",
			warden.Membership{Version: 1, CreatedInTerm: 5}, warden.Membership{Version: 1, CreatedInTerm: 3}, true),
		Entry("equal version, lower term does not",
			warden.Membership{Version: 1, CreatedInTerm: 3}, warden.Membership{Version: 1, CreatedInTerm: 5}, false),
		Entry("higher version wins even with a lower term (version dominates)",
			warden.Membership{Version: 2, CreatedInTerm: 1}, warden.Membership{Version: 1, CreatedInTerm: 9}, true),
		Entry("lower version loses even with a higher term",
			warden.Membership{Version: 1, CreatedInTerm: 9}, warden.Membership{Version: 2, CreatedInTerm: 1}, false),
		Entry("an identical pair does not supersede itself",
			warden.Membership{Version: 3, CreatedInTerm: 4}, warden.Membership{Version: 3, CreatedInTerm: 4}, false),
		Entry("the zero value never supersedes a minted config",
			warden.Membership{}, warden.Membership{Version: 1, CreatedInTerm: 0}, false),
	)
})

var _ = Describe("Membership.Clone", func() {
	It("deep-copies the Voters slice (no aliasing)", func() {
		orig := warden.Membership{Version: 2, Voters: []warden.Node{{ID: "a"}}}
		cp := orig.Clone()
		cp.Voters[0].ID = "mutated"
		Expect(orig.Voters[0].ID).To(Equal(warden.NodeID("a")))
	})

	It("preserves the full (Version, CreatedInTerm) identity", func() {
		orig := warden.Membership{Version: 4, CreatedInTerm: 9, Voters: []warden.Node{{ID: "a"}}}
		cp := orig.Clone()
		Expect(cp.CreatedInTerm).To(Equal(warden.Term(9)))
		Expect(cp.Version).To(Equal(uint64(4)))
	})
})
