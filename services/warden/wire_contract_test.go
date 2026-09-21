package warden_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
)

// Freezes the RETIRED HTTP/JSON wire encoding: the (now-dead) HTTP route path
// constants and the exact encoding/json of every request/response message.
// This is NOT the live node-to-node wire — that's protobuf over gRPC now,
// frozen separately by services/warden/proto/warden/v1/schema_contract_test.go. What this
// suite guarantees is the legacy-compat surface: services/warden/store's read path
// must keep accepting this exact encoding/json shape (in addition to the
// protojson the store now writes) so a pre-migration state.json upgrades
// losslessly. The PersistentState assertions below freeze that legacy READ
// encoding, not the current on-disk format (the store now WRITES protojson —
// see services/warden/wireconv and services/warden/store).

var _ = Describe("Wire protocol path constants", func() {
	DescribeTable("hold their frozen value",
		func(got, want string) { Expect(got).To(Equal(want)) },
		Entry("PathVote", warden.PathVote, "/warden/v1/vote"),
		Entry("PathHeartbeat", warden.PathHeartbeat, "/warden/v1/heartbeat"),
		Entry("PathIdentify", warden.PathIdentify, "/warden/v1/identify"),
		Entry("PathAPIStatus", warden.PathAPIStatus, "/api/status"),
		Entry("PathMetrics", warden.PathMetrics, "/metrics"),
		Entry("PathDashboard", warden.PathDashboard, "/"),
		Entry("PathClusterPartial", warden.PathClusterPartial, "/partials/cluster"),
	)

	It("versions the cluster RPCs under /warden/v1/", func() {
		Expect(warden.PathVote).To(HavePrefix("/warden/v1/"))
		Expect(warden.PathHeartbeat).To(HavePrefix("/warden/v1/"))
		Expect(warden.PathIdentify).To(HavePrefix("/warden/v1/"))
	})
})

var _ = Describe("Wire message JSON", func() {
	Describe("VoteRequest", func() {
		It("marshals term,candidate_id", func() {
			b, err := json.Marshal(warden.VoteRequest{Term: 7, CandidateID: "node-d"})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal(`{"term":7,"candidate_id":"node-d"}`))
		})
		It("marshals the zero value with no omitted fields", func() {
			b, _ := json.Marshal(warden.VoteRequest{})
			Expect(string(b)).To(Equal(`{"term":0,"candidate_id":""}`))
		})
		It("round-trips", func() {
			in := warden.VoteRequest{Term: 99, CandidateID: "n3"}
			var out warden.VoteRequest
			Expect(json.Unmarshal([]byte(`{"term":99,"candidate_id":"n3"}`), &out)).To(Succeed())
			Expect(out).To(Equal(in))
		})
	})

	Describe("VoteResponse", func() {
		It("marshals term,granted,voter_id", func() {
			b, _ := json.Marshal(warden.VoteResponse{Term: 7, Granted: true, VoterID: "node-a"})
			Expect(string(b)).To(Equal(`{"term":7,"granted":true,"voter_id":"node-a"}`))
		})
		It("marshals the zero value (granted:false present)", func() {
			b, _ := json.Marshal(warden.VoteResponse{})
			Expect(string(b)).To(Equal(`{"term":0,"granted":false,"voter_id":""}`))
		})
	})

	Describe("HeartbeatRequest", func() {
		It("omits view when nil (view is the only omitempty field in the contract)", func() {
			b, err := json.Marshal(warden.HeartbeatRequest{Term: 7, LeaderID: "node-d"})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal(`{"term":7,"leader_id":"node-d"}`))
			Expect(string(b)).NotTo(ContainSubstring("view"))
		})

		It("includes view when present, nested as the full ClusterView", func() {
			cv := warden.ClusterView{
				Self: "node-d", Role: warden.RoleLeader, Term: 7, LeaderID: "node-d",
				Source: "node-d", Authoritative: true, UpdatedAt: fixedTime,
				Peers: []warden.PeerView{
					{Node: warden.Node{ID: "node-d", Addr: "203.0.113.24:7717"}, Status: warden.StatusAlive, LastSeen: fixedTime},
				},
				ElectionsStarted: 3,
			}
			b, err := json.Marshal(warden.HeartbeatRequest{Term: 7, LeaderID: "node-d", View: &cv})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal(
				`{"term":7,"leader_id":"node-d","view":{"self":"node-d","role":"leader","term":7,` +
					`"leader_id":"node-d","source":"node-d","authoritative":true,"updated_at":"` + fixedTimeJSON + `",` +
					`"peers":[{"node":{"id":"node-d","addr":"203.0.113.24:7717"},"status":"alive","last_seen":"` +
					fixedTimeJSON + `","latency_ms":0}],"elections_started":3,` +
					`"membership":{"version":0,"created_in_term":0,"voters":null}}}`))
		})

		It("omits membership when nil (membership is omitempty on the wire)", func() {
			b, err := json.Marshal(warden.HeartbeatRequest{Term: 7, LeaderID: "node-d"})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring("membership"))
		})

		It("includes membership when present (leader disseminating a config change)", func() {
			m := &warden.Membership{Version: 2, CreatedInTerm: 5, Voters: []warden.Node{{ID: "a", Addr: "1:1"}}}
			b, err := json.Marshal(warden.HeartbeatRequest{Term: 7, LeaderID: "node-d", Membership: m})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal(
				`{"term":7,"leader_id":"node-d","membership":{"version":2,"created_in_term":5,"voters":[{"id":"a","addr":"1:1"}]}}`))
		})

		It("round-trips a nil-view heartbeat back to a nil view pointer", func() {
			var out warden.HeartbeatRequest
			Expect(json.Unmarshal([]byte(`{"term":7,"leader_id":"node-d"}`), &out)).To(Succeed())
			Expect(out.View).To(BeNil())
			Expect(out.Term).To(Equal(warden.Term(7)))
			Expect(out.LeaderID).To(Equal(warden.NodeID("node-d")))
		})
	})

	Describe("HeartbeatResponse", func() {
		It("marshals term,ok,node_id", func() {
			b, _ := json.Marshal(warden.HeartbeatResponse{Term: 7, OK: true, NodeID: "node-a"})
			Expect(string(b)).To(Equal(`{"term":7,"ok":true,"node_id":"node-a"}`))
		})
		It("marshals the zero value (ok:false present)", func() {
			b, _ := json.Marshal(warden.HeartbeatResponse{})
			Expect(string(b)).To(Equal(`{"term":0,"ok":false,"node_id":""}`))
		})
	})

	Describe("PersistentState", func() {
		// This is the LEGACY on-disk state-file encoding (encoding/json), which
		// the store's read path must keep accepting so a pre-migration state.json
		// upgrades losslessly (proto3 JSON parses a uint64 field from either a
		// number or a string). It is not what the store currently WRITES — that's
		// protojson (UseProtoNames, current_term rendered as a string) via
		// services/warden/wireconv; see services/warden/proto/warden/v1/schema_contract_test.go for that
		// live on-disk format.
		It("marshals current_term,voted_for", func() {
			b, _ := json.Marshal(warden.PersistentState{CurrentTerm: 7, VotedFor: "node-d"})
			Expect(string(b)).To(Equal(`{"current_term":7,"voted_for":"node-d"}`))
		})
		It("marshals the zero value (fresh node, no vote)", func() {
			b, _ := json.Marshal(warden.PersistentState{})
			Expect(string(b)).To(Equal(`{"current_term":0,"voted_for":""}`))
		})
		It("round-trips", func() {
			in := warden.PersistentState{CurrentTerm: 5, VotedFor: "n2"}
			var out warden.PersistentState
			Expect(json.Unmarshal([]byte(`{"current_term":5,"voted_for":"n2"}`), &out)).To(Succeed())
			Expect(out).To(Equal(in))
		})

		It("omits membership when nil (state files predating membership support)", func() {
			b, _ := json.Marshal(warden.PersistentState{CurrentTerm: 7, VotedFor: "node-d"})
			Expect(string(b)).NotTo(ContainSubstring("membership"))
		})

		It("persists membership when present, including created_in_term", func() {
			b, _ := json.Marshal(warden.PersistentState{
				CurrentTerm: 7, VotedFor: "node-d",
				Membership: &warden.Membership{Version: 2, CreatedInTerm: 5, Voters: []warden.Node{{ID: "a", Addr: "1:1"}}},
			})
			Expect(string(b)).To(Equal(
				`{"current_term":7,"voted_for":"node-d","membership":{"version":2,"created_in_term":5,"voters":[{"id":"a","addr":"1:1"}]}}`))
		})
	})

	Describe("IdentifyResponse", func() {
		It("marshals cluster_id,node_id,version", func() {
			b, err := json.Marshal(warden.IdentifyResponse{ClusterID: "candacenet", NodeID: "node-d", Version: "v1.2.3"})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal(`{"cluster_id":"candacenet","node_id":"node-d","version":"v1.2.3"}`))
		})
		It("marshals the zero value with no omitted fields", func() {
			b, _ := json.Marshal(warden.IdentifyResponse{})
			Expect(string(b)).To(Equal(`{"cluster_id":"","node_id":"","version":""}`))
		})
	})
})
