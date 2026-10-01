package wireconv_test

// Contract suite for the domain <-> proto conversion boundary. It freezes the
// two properties the whole gRPC plane leans on:
//
//   - Round-trip losslessness in BOTH directions over full-population fixtures:
//     domain -> proto -> domain reproduces the domain value (reflect.DeepEqual),
//     and proto -> domain -> proto reproduces the proto message (proto.Equal).
//     The fixtures populate EVERY field, including Membership.CreatedInTerm — the
//     field a past Clone regression silently zeroed. The dedicated
//     "created_in_term survives" spec is the tripwire: drop CreatedInTerm from
//     MembershipToProto/FromProto and both the round-trip tables and that spec go
//     red.
//   - Totality: nil proto inputs and zero domain inputs convert without panicking
//     to the zero value on the other side, and the *Ptr* converters preserve nil
//     ("field absent") while the value converters never return nil.
//
// It is additive to the schema suite (services/warden/proto/warden/v1/schema_contract_test.go),
// which freezes the protojson wire form; this suite freezes the Go-type mapping.

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/candacelabs/csf/services/warden"
	wardenv1 "github.com/candacelabs/csf/services/warden/proto/warden/v1"
	"github.com/candacelabs/csf/services/warden/wireconv"
)

func TestWireconv(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "wireconv contract suite")
}

// fixedTime is the single non-zero instant used across fixtures: UTC with a
// sub-second component so both the timestamp and its fractional part are
// exercised. UTC guarantees timestamppb.New(t).AsTime() round-trips under
// reflect.DeepEqual (AsTime always returns UTC).
var fixedTime = time.Date(2026, 7, 21, 15, 4, 5, 123456789, time.UTC)

// --- full-population domain fixtures ---------------------------------------

func fullMembership() warden.Membership {
	return warden.Membership{
		Version:       9,
		CreatedInTerm: 4,
		Voters: []warden.Node{
			{ID: "node-b", Addr: "203.0.113.12:7717"},
			{ID: "node-d", Addr: "203.0.113.14:7717"},
		},
	}
}

func fullClusterView() warden.ClusterView {
	return warden.ClusterView{
		Self:          "node-d",
		Role:          warden.RoleLeader,
		Term:          7,
		LeaderID:      "node-d",
		Source:        "node-d",
		Authoritative: true,
		UpdatedAt:     fixedTime,
		Peers: []warden.PeerView{
			{Node: warden.Node{ID: "node-d", Addr: "203.0.113.24:7717"}, Status: warden.StatusAlive, LastSeen: fixedTime, LatencyMS: 1.5, Member: warden.MemberVoter},
			{Node: warden.Node{ID: "joiner", Addr: "203.0.113.28:7717"}, Status: warden.StatusUnknown, Member: warden.MemberObserver},
		},
		ElectionsStarted: 3,
		Membership:       fullMembership(),
	}
}

func fullHeartbeatRequest() warden.HeartbeatRequest {
	cv := fullClusterView()
	m := fullMembership()
	return warden.HeartbeatRequest{Term: 7, LeaderID: "node-d", View: &cv, Membership: &m}
}

var _ = Describe("domain -> proto -> domain is lossless", func() {
	DescribeTable("reproduces the domain value over a full-population fixture",
		func(roundTrip func() (got, want any)) {
			got, want := roundTrip()
			Expect(got).To(Equal(want))
		},
		Entry("Node", func() (any, any) {
			in := warden.Node{ID: "node-d", Addr: "203.0.113.24:7717"}
			return wireconv.NodeFromProto(wireconv.NodeToProto(in)), in
		}),
		Entry("Membership", func() (any, any) {
			in := fullMembership()
			return wireconv.MembershipFromProto(wireconv.MembershipToProto(in)), in
		}),
		Entry("PeerView", func() (any, any) {
			in := warden.PeerView{Node: warden.Node{ID: "n", Addr: "a:1"}, Status: warden.StatusSuspect, LastSeen: fixedTime, LatencyMS: 2.25, Member: warden.MemberDiscovered}
			return wireconv.PeerViewFromProto(wireconv.PeerViewToProto(in)), in
		}),
		Entry("ClusterView", func() (any, any) {
			in := fullClusterView()
			return wireconv.ClusterViewFromProto(wireconv.ClusterViewToProto(in)), in
		}),
		Entry("PersistentState (with membership)", func() (any, any) {
			m := fullMembership()
			in := warden.PersistentState{CurrentTerm: 5, VotedFor: "n2", Membership: &m}
			return wireconv.PersistentStateFromProto(wireconv.PersistentStateToProto(in)), in
		}),
		Entry("VoteRequest", func() (any, any) {
			in := warden.VoteRequest{Term: 99, CandidateID: "n3"}
			return wireconv.VoteRequestFromProto(wireconv.VoteRequestToProto(in)), in
		}),
		Entry("VoteResponse", func() (any, any) {
			in := warden.VoteResponse{Term: 3, Granted: true, VoterID: "n2"}
			return wireconv.VoteResponseFromProto(wireconv.VoteResponseToProto(in)), in
		}),
		Entry("HeartbeatRequest (view + membership)", func() (any, any) {
			in := fullHeartbeatRequest()
			return wireconv.HeartbeatRequestFromProto(wireconv.HeartbeatRequestToProto(in)), in
		}),
		Entry("HeartbeatResponse", func() (any, any) {
			in := warden.HeartbeatResponse{Term: 7, OK: true, NodeID: "node-a"}
			return wireconv.HeartbeatResponseFromProto(wireconv.HeartbeatResponseToProto(in)), in
		}),
		Entry("IdentifyResponse", func() (any, any) {
			in := warden.IdentifyResponse{ClusterID: "candacenet", NodeID: "node-d", Version: "v1.2.3"}
			return wireconv.IdentifyResponseFromProto(wireconv.IdentifyResponseToProto(in)), in
		}),
	)
})

var _ = Describe("proto -> domain -> proto is lossless", func() {
	DescribeTable("reproduces the proto message (proto.Equal)",
		func(in proto.Message, back func() proto.Message) {
			Expect(proto.Equal(back(), in)).To(BeTrue(), "round-trip changed the message")
		},
		Entry("Node", &wardenv1.Node{Id: "node-d", Addr: "203.0.113.24:7717"},
			func() proto.Message {
				return wireconv.NodeToProto(wireconv.NodeFromProto(&wardenv1.Node{Id: "node-d", Addr: "203.0.113.24:7717"}))
			}),
		Entry("Membership", wireconv.MembershipToProto(fullMembership()),
			func() proto.Message {
				return wireconv.MembershipToProto(wireconv.MembershipFromProto(wireconv.MembershipToProto(fullMembership())))
			}),
		Entry("ClusterView", wireconv.ClusterViewToProto(fullClusterView()),
			func() proto.Message {
				return wireconv.ClusterViewToProto(wireconv.ClusterViewFromProto(wireconv.ClusterViewToProto(fullClusterView())))
			}),
		Entry("PersistentState", wireconv.PersistentStateToProto(warden.PersistentState{CurrentTerm: 5, VotedFor: "n2", Membership: ptrMembership(fullMembership())}),
			func() proto.Message {
				p := wireconv.PersistentStateToProto(warden.PersistentState{CurrentTerm: 5, VotedFor: "n2", Membership: ptrMembership(fullMembership())})
				return wireconv.PersistentStateToProto(wireconv.PersistentStateFromProto(p))
			}),
		Entry("HeartbeatRequest", wireconv.HeartbeatRequestToProto(fullHeartbeatRequest()),
			func() proto.Message {
				p := wireconv.HeartbeatRequestToProto(fullHeartbeatRequest())
				return wireconv.HeartbeatRequestToProto(wireconv.HeartbeatRequestFromProto(p))
			}),
	)
})

var _ = Describe("Membership.CreatedInTerm survives conversion (Clone-regression tripwire)", func() {
	It("preserves a non-zero CreatedInTerm both ways", func() {
		in := warden.Membership{Version: 1, CreatedInTerm: 42, Voters: []warden.Node{{ID: "a", Addr: "x:1"}}}
		p := wireconv.MembershipToProto(in)
		Expect(p.GetCreatedInTerm()).To(Equal(uint64(42)))
		out := wireconv.MembershipFromProto(p)
		Expect(out.CreatedInTerm).To(Equal(warden.Term(42)))
	})

	It("carries CreatedInTerm through a nested ClusterView", func() {
		v := fullClusterView()
		v.Membership.CreatedInTerm = 77
		got := wireconv.ClusterViewFromProto(wireconv.ClusterViewToProto(v))
		Expect(got.Membership.CreatedInTerm).To(Equal(warden.Term(77)))
	})
})

var _ = Describe("totality and nil discipline", func() {
	It("maps nil proto messages to the zero domain value without panicking", func() {
		Expect(wireconv.NodeFromProto(nil)).To(Equal(warden.Node{}))
		Expect(wireconv.MembershipFromProto(nil)).To(Equal(warden.Membership{}))
		Expect(wireconv.PeerViewFromProto(nil)).To(Equal(warden.PeerView{}))
		Expect(wireconv.ClusterViewFromProto(nil)).To(Equal(warden.ClusterView{}))
		Expect(wireconv.PersistentStateFromProto(nil)).To(Equal(warden.PersistentState{}))
		Expect(wireconv.VoteRequestFromProto(nil)).To(Equal(warden.VoteRequest{}))
		Expect(wireconv.VoteResponseFromProto(nil)).To(Equal(warden.VoteResponse{}))
		Expect(wireconv.HeartbeatRequestFromProto(nil)).To(Equal(warden.HeartbeatRequest{}))
		Expect(wireconv.HeartbeatResponseFromProto(nil)).To(Equal(warden.HeartbeatResponse{}))
		Expect(wireconv.IdentifyResponseFromProto(nil)).To(Equal(warden.IdentifyResponse{}))
	})

	It("value converters never return nil, even for the zero value", func() {
		Expect(wireconv.NodeToProto(warden.Node{})).NotTo(BeNil())
		Expect(wireconv.MembershipToProto(warden.Membership{})).NotTo(BeNil())
		Expect(wireconv.ClusterViewToProto(warden.ClusterView{})).NotTo(BeNil())
		Expect(wireconv.ClusterViewToProto(warden.ClusterView{}).GetMembership()).NotTo(BeNil())
	})

	It("Ptr converters preserve nil in both directions", func() {
		Expect(wireconv.MembershipPtrToProto(nil)).To(BeNil())
		Expect(wireconv.MembershipPtrFromProto(nil)).To(BeNil())
		Expect(wireconv.ClusterViewPtrToProto(nil)).To(BeNil())
		Expect(wireconv.ClusterViewPtrFromProto(nil)).To(BeNil())
	})

	It("the zero time maps to an omitted (nil) timestamp and back to the zero time", func() {
		p := wireconv.PeerViewToProto(warden.PeerView{Node: warden.Node{ID: "a"}, Status: warden.StatusUnknown})
		Expect(p.GetLastSeen()).To(BeNil())
		Expect(wireconv.PeerViewFromProto(p).LastSeen.IsZero()).To(BeTrue())
	})

	It("an empty Voters slice round-trips as nil (proto nil/empty equivalence)", func() {
		in := warden.Membership{Version: 2}
		Expect(wireconv.MembershipFromProto(wireconv.MembershipToProto(in)).Voters).To(BeNil())
	})
})

var _ = Describe("cluster-watch cursor derivation", func() {
	It("derives the (version, created_in_term, updated_at) triple from a view", func() {
		v := wireconv.ClusterViewToProto(fullClusterView())
		c := wireconv.CursorOf(v)
		Expect(c.GetMembershipVersion()).To(Equal(uint64(9)))
		Expect(c.GetMembershipCreatedInTerm()).To(Equal(uint64(4)))
		Expect(c.GetViewUpdatedAt().AsTime().Equal(fixedTime)).To(BeTrue())
	})

	It("CursorOf is non-nil even for a nil view", func() {
		Expect(wireconv.CursorOf(nil)).NotTo(BeNil())
	})

	It("CursorEqual treats nil as the zero cursor and compares by value", func() {
		a := &wardenv1.ClusterViewCursor{MembershipVersion: 1, MembershipCreatedInTerm: 2, ViewUpdatedAt: timestamppb.New(fixedTime)}
		b := &wardenv1.ClusterViewCursor{MembershipVersion: 1, MembershipCreatedInTerm: 2, ViewUpdatedAt: timestamppb.New(fixedTime)}
		Expect(wireconv.CursorEqual(a, b)).To(BeTrue())
		Expect(wireconv.CursorEqual(a, &wardenv1.ClusterViewCursor{MembershipVersion: 1})).To(BeFalse())
		Expect(wireconv.CursorEqual(nil, &wardenv1.ClusterViewCursor{})).To(BeTrue())
		Expect(wireconv.CursorEqual(nil, a)).To(BeFalse())
	})
})

// ptrMembership is a fixture helper returning a heap copy of m.
func ptrMembership(m warden.Membership) *warden.Membership { return &m }
