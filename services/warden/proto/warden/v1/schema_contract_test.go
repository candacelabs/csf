package wardenv1_test

// Schema-level contract suite for the generated candacenet.warden.v1 protobuf
// bindings. These specs freeze the protobuf-JSON (protojson) rendering of every
// wire message and the forward/back-compatibility semantics a future gRPC
// transport worker will rely on:
//
//   - canonical protojson goldens per message (regenerated from ACTUAL output,
//     never hand-typed — see the explorer note below),
//   - protojson round-trip fidelity per message (marshal -> unmarshal -> equal),
//   - unknown-field tolerance under UnmarshalOptions{DiscardUnknown: true},
//   - zero-value omission semantics (and that omission is lossless),
//   - Membership.Supersedes ordering reproduced from proto-derived values.
//
// These are ADDITIVE to (and independent of) the existing JSON wire goldens in
// services/warden, which continue to freeze the current HTTP/JSON
// transport. This
// suite freezes the proto source of truth for the gRPC plane.
//
// Determinism: protojson deliberately injects randomized insignificant
// whitespace, so raw Marshal output is not byte-stable. canonicalJSON compacts
// it through encoding/json to yield stable, comparable bytes; field ORDER is
// already deterministic (protojson emits in field-number/declaration order).

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/candacelabs/csf/services/warden"
	wardenv1 "github.com/candacelabs/csf/services/warden/proto/warden/v1"
)

func TestWardenSchemaContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "warden proto schema contract suite")
}

// fixedTimestamp is the single non-zero instant used across the goldens, chosen
// UTC with no sub-second component so its RFC 3339 rendering is stable and
// human-checkable. It mirrors the fixedTime in services/warden so the
// two suites tell the same story.
var fixedTimestamp = timestamppb.New(time.Date(2026, 7, 21, 15, 4, 5, 0, time.UTC))

// fixedTimestampJSON is how fixedTimestamp renders inside protojson output.
const fixedTimestampJSON = "2026-07-21T15:04:05Z"

// emptyObjectJSON is the canonical rendering of any message whose set fields are
// all proto3 zero values: every field is omitted.
const emptyObjectJSON = "{}"

// Golden fragments, composed so nested messages render identically wherever they
// appear (no duplicated literal blocks). Each fragment is the exact canonical
// protojson of the correspondingly-named sample* builder below.
const (
	nodeCandaceJSON = `{"id":"node-d","addr":"203.0.113.24:7717"}`
	nodeAJSON       = `{"id":"a","addr":"1:1"}`

	membershipJSON = `{"version":"1","created_in_term":"2","voters":[` + nodeCandaceJSON + `]}`

	// The peer as embedded in a ClusterView: latency_ms and member are zero and
	// therefore omitted.
	peerInViewJSON = `{"node":` + nodeCandaceJSON + `,"status":"alive","last_seen":"` + fixedTimestampJSON + `"}`

	clusterViewJSON = `{"self":"node-d","role":"leader","term":"7","leader_id":"node-d",` +
		`"source":"node-d","authoritative":true,"updated_at":"` + fixedTimestampJSON + `",` +
		`"peers":[` + peerInViewJSON + `],"elections_started":"3","membership":` + membershipJSON + `}`

	cursorJSON = `{"membership_version":"1","membership_created_in_term":"2","view_updated_at":"` + fixedTimestampJSON + `"}`
)

// canonicalJSON renders m the way the wire contract fixes it: proto field names
// (snake_case, via UseProtoNames) and deterministic compacted whitespace. This
// is the single renderer every golden assertion goes through.
func canonicalJSON(m proto.Message) string {
	raw, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(m)
	Expect(err).NotTo(HaveOccurred())
	var compact bytes.Buffer
	Expect(json.Compact(&compact, raw)).To(Succeed())
	return compact.String()
}

// emptyLike returns a fresh, zero-valued message of the same concrete type as m,
// the target for a round-trip decode.
func emptyLike(m proto.Message) proto.Message {
	return m.ProtoReflect().New().Interface()
}

// domainMembership projects the identity fields of a proto Membership onto the
// Go contract type, so Supersedes can be exercised on proto-derived values.
func domainMembership(m *wardenv1.Membership) warden.Membership {
	out := warden.Membership{
		Version:       m.GetVersion(),
		CreatedInTerm: warden.Term(m.GetCreatedInTerm()),
	}
	for _, v := range m.GetVoters() {
		out.Voters = append(out.Voters, warden.Node{ID: warden.NodeID(v.GetId()), Addr: v.GetAddr()})
	}
	return out
}

// --- Sample builders: one canonical populated value per message. Shared by the
// golden and round-trip tables so a single fixture drives both. ---------------

func sampleNode() *wardenv1.Node { return &wardenv1.Node{Id: "node-d", Addr: "203.0.113.24:7717"} }

func sampleMembership() *wardenv1.Membership {
	return &wardenv1.Membership{Version: 1, CreatedInTerm: 2, Voters: []*wardenv1.Node{sampleNode()}}
}

func sampleClusterView() *wardenv1.ClusterView {
	return &wardenv1.ClusterView{
		Self: "node-d", Role: "leader", Term: 7, LeaderId: "node-d", Source: "node-d",
		Authoritative: true, UpdatedAt: fixedTimestamp,
		Peers:            []*wardenv1.PeerView{{Node: sampleNode(), Status: "alive", LastSeen: fixedTimestamp}},
		ElectionsStarted: 3,
		Membership:       sampleMembership(),
	}
}

func sampleCursor() *wardenv1.ClusterViewCursor {
	return &wardenv1.ClusterViewCursor{MembershipVersion: 1, MembershipCreatedInTerm: 2, ViewUpdatedAt: fixedTimestamp}
}

var _ = Describe("protojson canonical goldens", func() {
	// The exact wire bytes each message renders to. Regenerated from real
	// protojson output; a one-character perturbation of any want string must turn
	// this table RED (the counterfactual tripwire).
	DescribeTable("render to their frozen canonical form",
		func(m proto.Message, want string) { Expect(canonicalJSON(m)).To(Equal(want)) },
		Entry("VoteRequest", &wardenv1.VoteRequest{Term: 7, CandidateId: "node-d"},
			`{"term":"7","candidate_id":"node-d"}`),
		Entry("VoteResponse", &wardenv1.VoteResponse{Term: 7, Granted: true, VoterId: "node-a"},
			`{"term":"7","granted":true,"voter_id":"node-a"}`),
		Entry("HeartbeatResponse", &wardenv1.HeartbeatResponse{Term: 7, Ok: true, NodeId: "node-a"},
			`{"term":"7","ok":true,"node_id":"node-a"}`),
		Entry("IdentifyResponse", &wardenv1.IdentifyResponse{ClusterId: "candacenet", NodeId: "node-d", Version: "v1.2.3"},
			`{"cluster_id":"candacenet","node_id":"node-d","version":"v1.2.3"}`),
		Entry("Node", sampleNode(), nodeCandaceJSON),
		Entry("Membership", sampleMembership(), membershipJSON),
		Entry("PeerView (all fields)", &wardenv1.PeerView{Node: sampleNode(), Status: "alive", LastSeen: fixedTimestamp, LatencyMs: 1.5, Member: "observer"},
			`{"node":`+nodeCandaceJSON+`,"status":"alive","last_seen":"`+fixedTimestampJSON+`","latency_ms":1.5,"member":"observer"}`),
		Entry("ClusterView", sampleClusterView(), clusterViewJSON),
		Entry("PersistentState", &wardenv1.PersistentState{CurrentTerm: 7, VotedFor: "node-d", Membership: &wardenv1.Membership{Version: 2, CreatedInTerm: 5, Voters: []*wardenv1.Node{{Id: "a", Addr: "1:1"}}}},
			`{"current_term":"7","voted_for":"node-d","membership":{"version":"2","created_in_term":"5","voters":[`+nodeAJSON+`]}}`),
		Entry("HeartbeatRequest (view + membership)", &wardenv1.HeartbeatRequest{Term: 7, LeaderId: "node-d", View: sampleClusterView(), Membership: sampleMembership()},
			`{"term":"7","leader_id":"node-d","view":`+clusterViewJSON+`,"membership":`+membershipJSON+`}`),
		Entry("ClusterViewCursor", sampleCursor(), cursorJSON),
		Entry("ClusterViewUpdate", &wardenv1.ClusterViewUpdate{View: sampleClusterView(), Cursor: sampleCursor()},
			`{"view":`+clusterViewJSON+`,"cursor":`+cursorJSON+`}`),
		Entry("WatchClusterRequest (since cursor)", &wardenv1.WatchClusterRequest{Since: &wardenv1.ClusterViewCursor{MembershipVersion: 4}},
			`{"since":{"membership_version":"4"}}`),
	)
})

var _ = Describe("protojson round-trip fidelity", func() {
	// Every message survives marshal -> unmarshal unchanged (proto.Equal). This
	// covers each message the schema defines, including the deep-nested ones.
	DescribeTable("marshal then unmarshal reproduces the message",
		func(m proto.Message) {
			raw, err := protojson.Marshal(m)
			Expect(err).NotTo(HaveOccurred())
			got := emptyLike(m)
			Expect(protojson.Unmarshal(raw, got)).To(Succeed())
			Expect(proto.Equal(got, m)).To(BeTrue(), "round-trip changed the message:\n got=%s\nwant=%s", canonicalJSON(got), canonicalJSON(m))
		},
		Entry("VoteRequest", &wardenv1.VoteRequest{Term: 99, CandidateId: "n3"}),
		Entry("VoteResponse", &wardenv1.VoteResponse{Term: 3, Granted: false, VoterId: "n2"}),
		Entry("HeartbeatRequest (bare)", &wardenv1.HeartbeatRequest{Term: 7, LeaderId: "node-d"}),
		Entry("HeartbeatRequest (view + membership)", &wardenv1.HeartbeatRequest{Term: 7, LeaderId: "node-d", View: sampleClusterView(), Membership: sampleMembership()}),
		Entry("HeartbeatResponse", &wardenv1.HeartbeatResponse{Term: 7, Ok: true, NodeId: "node-a"}),
		Entry("IdentifyRequest (empty)", &wardenv1.IdentifyRequest{}),
		Entry("IdentifyResponse", &wardenv1.IdentifyResponse{ClusterId: "candacenet", NodeId: "node-d", Version: "v1"}),
		Entry("Node", sampleNode()),
		Entry("Membership", sampleMembership()),
		Entry("PeerView", &wardenv1.PeerView{Node: sampleNode(), Status: "suspect", LastSeen: fixedTimestamp, LatencyMs: 2.25, Member: "discovered"}),
		Entry("ClusterView", sampleClusterView()),
		Entry("PersistentState", &wardenv1.PersistentState{CurrentTerm: 5, VotedFor: "n2", Membership: sampleMembership()}),
		Entry("ClusterViewCursor", sampleCursor()),
		Entry("WatchClusterRequest", &wardenv1.WatchClusterRequest{Since: sampleCursor()}),
		Entry("ClusterViewUpdate", &wardenv1.ClusterViewUpdate{View: sampleClusterView(), Cursor: sampleCursor()}),
	)
})

var _ = Describe("unknown-field tolerance", func() {
	// Forward compatibility: an older node must accept a newer node's message that
	// carries fields it does not know. protojson grants this ONLY under
	// UnmarshalOptions{DiscardUnknown: true}; strict decoding rejects unknowns.
	// The gRPC transport worker MUST decode with DiscardUnknown for this reason.
	const withSurplusField = `{"term":"7","candidate_id":"c","surprise":true,"future_nested":{"x":1}}`

	It("accepts unknown fields with DiscardUnknown and keeps the known ones", func() {
		var got wardenv1.VoteRequest
		Expect((protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(withSurplusField), &got)).To(Succeed())
		Expect(got.GetTerm()).To(Equal(uint64(7)))
		Expect(got.GetCandidateId()).To(Equal("c"))
	})

	It("rejects unknown fields under strict (default) decoding", func() {
		var got wardenv1.VoteRequest
		err := protojson.Unmarshal([]byte(withSurplusField), &got)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown field"))
	})

	It("also accepts the lowerCamelCase JSON name on input (name equivalence)", func() {
		var got wardenv1.VoteRequest
		Expect(protojson.Unmarshal([]byte(`{"candidateId":"z","term":"9"}`), &got)).To(Succeed())
		Expect(got.GetCandidateId()).To(Equal("z"))
		Expect(got.GetTerm()).To(Equal(uint64(9)))
	})
})

var _ = Describe("zero-value omission semantics", func() {
	// Canonical protojson omits proto3 zero-valued scalar fields. This differs
	// from the historical encoding/json wire (which emits "term":0 etc.), and is
	// an accepted, documented delta — omission is lossless: a reader reconstructs
	// the zero value.
	DescribeTable("a message with only zero-valued fields renders as {}",
		func(m proto.Message) { Expect(canonicalJSON(m)).To(Equal(emptyObjectJSON)) },
		Entry("VoteRequest", &wardenv1.VoteRequest{}),
		Entry("VoteResponse", &wardenv1.VoteResponse{}),
		Entry("HeartbeatRequest", &wardenv1.HeartbeatRequest{}),
		Entry("HeartbeatResponse", &wardenv1.HeartbeatResponse{}),
		Entry("IdentifyRequest", &wardenv1.IdentifyRequest{}),
		Entry("IdentifyResponse", &wardenv1.IdentifyResponse{}),
		Entry("Node", &wardenv1.Node{}),
		Entry("Membership", &wardenv1.Membership{}),
		Entry("PeerView", &wardenv1.PeerView{}),
		Entry("ClusterView", &wardenv1.ClusterView{}),
		Entry("PersistentState", &wardenv1.PersistentState{}),
		Entry("WatchClusterRequest", &wardenv1.WatchClusterRequest{}),
		Entry("ClusterViewUpdate", &wardenv1.ClusterViewUpdate{}),
	)

	It("omits an unset optional message field but keeps set ones", func() {
		// view unset -> omitted; membership set -> present.
		hb := &wardenv1.HeartbeatRequest{Term: 7, LeaderId: "node-d", Membership: sampleMembership()}
		Expect(canonicalJSON(hb)).To(Equal(`{"term":"7","leader_id":"node-d","membership":` + membershipJSON + `}`))
	})

	It("reconstructs zero values from an omitted-field document (omission is lossless)", func() {
		var got wardenv1.VoteResponse
		Expect(protojson.Unmarshal([]byte(emptyObjectJSON), &got)).To(Succeed())
		Expect(got.GetTerm()).To(Equal(uint64(0)))
		Expect(got.GetGranted()).To(BeFalse())
		Expect(got.GetVoterId()).To(BeEmpty())
	})

	It("treats an empty PeerView.member as the voter default (matches JSON omitempty)", func() {
		pv := &wardenv1.PeerView{Node: &wardenv1.Node{Id: "a", Addr: "x:1"}, Status: "alive"}
		Expect(canonicalJSON(pv)).NotTo(ContainSubstring("member"))
	})
})

var _ = Describe("proto field-name choice", func() {
	// The wire keeps snake_case field names, which requires UseProtoNames on
	// marshal. Left at the protojson default, the same message renders lowerCamel.
	It("emits snake_case under UseProtoNames and lowerCamel by default", func() {
		msg := &wardenv1.VoteRequest{Term: 7, CandidateId: "node-d"}
		Expect(canonicalJSON(msg)).To(ContainSubstring(`"candidate_id"`))

		byDefault, err := protojson.Marshal(msg)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(byDefault)).To(ContainSubstring(`"candidateId"`))
		Expect(string(byDefault)).NotTo(ContainSubstring(`"candidate_id"`))
	})
})

var _ = Describe("Membership.Supersedes on proto-derived values", func() {
	// The (Version, CreatedInTerm) lexicographic identity ordering must hold
	// unchanged when the operands are projected from proto Membership messages —
	// the split-brain guard survives the proto representation.
	DescribeTable("orders proto-derived memberships identically to the domain contract",
		func(a, b *wardenv1.Membership, want bool) {
			Expect(domainMembership(a).Supersedes(domainMembership(b))).To(Equal(want))
		},
		Entry("equal version, higher term supersedes",
			&wardenv1.Membership{Version: 1, CreatedInTerm: 5}, &wardenv1.Membership{Version: 1, CreatedInTerm: 3}, true),
		Entry("equal version, lower term does not",
			&wardenv1.Membership{Version: 1, CreatedInTerm: 3}, &wardenv1.Membership{Version: 1, CreatedInTerm: 5}, false),
		Entry("higher version wins even with a lower term",
			&wardenv1.Membership{Version: 2, CreatedInTerm: 1}, &wardenv1.Membership{Version: 1, CreatedInTerm: 9}, true),
		Entry("lower version loses even with a higher term",
			&wardenv1.Membership{Version: 1, CreatedInTerm: 9}, &wardenv1.Membership{Version: 2, CreatedInTerm: 1}, false),
		Entry("identical pair does not supersede itself",
			&wardenv1.Membership{Version: 3, CreatedInTerm: 4}, &wardenv1.Membership{Version: 3, CreatedInTerm: 4}, false),
		Entry("zero value never supersedes a minted config",
			&wardenv1.Membership{}, &wardenv1.Membership{Version: 1, CreatedInTerm: 0}, false),
	)
})
