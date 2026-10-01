// Package wireconv provides total, composable conversions between the frozen
// warden domain types (services/warden) and the generated candacenet.warden.v1
// protobuf messages (services/warden/proto/warden/v1). It is the single boundary the gRPC plane
// (server, client, and protojson persistence) crosses to move between the
// in-memory domain model and the wire representation.
//
// Every conversion is:
//
//   - Total: a nil or zero input yields a well-defined output; no converter
//     panics and none returns an error (structural validity is guaranteed by
//     the generated types, semantic validation lives at the RPC boundary).
//   - Lossless: for a fully-populated value the round trip
//     domain -> proto -> domain (and proto -> domain -> proto) reproduces the
//     input exactly. This is property-tested in wireconv_contract_test.go over
//     full-population fixtures INCLUDING Membership.CreatedInTerm — the field a
//     past Clone regression silently zeroed.
//
// Nil discipline. The proto model uses pointers where the domain uses either a
// value (ClusterView.Membership, PeerView) or an optional pointer
// (HeartbeatRequest.View/Membership, PersistentState.Membership). Value-to-proto
// converters therefore always return a non-nil message; the *Ptr* variants
// preserve nil to model "field absent" on the wire.
package wireconv

import (
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/candacelabs/csf/services/warden"
	wardenv1 "github.com/candacelabs/csf/services/warden/proto/warden/v1"
)

// ---- time ------------------------------------------------------------------

// timeToProto maps a domain instant to google.protobuf.Timestamp semantics: the
// zero time (never-seen / unset) becomes a nil message so it is omitted on the
// wire, mirroring the JSON omitempty behaviour of the historical encoding.
func timeToProto(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// timeFromProto inverts timeToProto: a nil (omitted) timestamp is the zero time.
// AsTime always yields a UTC instant, so a UTC domain fixture round-trips under
// reflect.DeepEqual.
func timeFromProto(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime()
}

// ---- Node ------------------------------------------------------------------

// NodeToProto converts a domain Node to its proto message (always non-nil).
func NodeToProto(n warden.Node) *wardenv1.Node {
	return &wardenv1.Node{Id: string(n.ID), Addr: n.Addr}
}

// NodeFromProto converts a proto Node to the domain type; a nil message is the
// zero Node.
func NodeFromProto(p *wardenv1.Node) warden.Node {
	if p == nil {
		return warden.Node{}
	}
	return warden.Node{ID: warden.NodeID(p.GetId()), Addr: p.GetAddr()}
}

// nodesToProto maps a node slice; a nil/empty input stays nil so it round-trips
// with nodesFromProto (proto treats nil and empty repeated fields as equal).
func nodesToProto(ns []warden.Node) []*wardenv1.Node {
	if len(ns) == 0 {
		return nil
	}
	out := make([]*wardenv1.Node, len(ns))
	for i, n := range ns {
		out[i] = NodeToProto(n)
	}
	return out
}

func nodesFromProto(ps []*wardenv1.Node) []warden.Node {
	if len(ps) == 0 {
		return nil
	}
	out := make([]warden.Node, len(ps))
	for i, p := range ps {
		out[i] = NodeFromProto(p)
	}
	return out
}

// ---- Membership ------------------------------------------------------------

// MembershipToProto converts a domain Membership value to a non-nil proto
// message. Use MembershipPtrToProto where nil must survive as "no change".
func MembershipToProto(m warden.Membership) *wardenv1.Membership {
	return &wardenv1.Membership{
		Version:       m.Version,
		CreatedInTerm: uint64(m.CreatedInTerm),
		Voters:        nodesToProto(m.Voters),
	}
}

// MembershipFromProto converts a proto Membership to the domain value; a nil
// message is the zero Membership.
func MembershipFromProto(p *wardenv1.Membership) warden.Membership {
	if p == nil {
		return warden.Membership{}
	}
	return warden.Membership{
		Version:       p.GetVersion(),
		CreatedInTerm: warden.Term(p.GetCreatedInTerm()),
		Voters:        nodesFromProto(p.GetVoters()),
	}
}

// MembershipPtrToProto preserves nil (the "no membership conveyed" signal used
// by HeartbeatRequest and PersistentState).
func MembershipPtrToProto(m *warden.Membership) *wardenv1.Membership {
	if m == nil {
		return nil
	}
	return MembershipToProto(*m)
}

// MembershipPtrFromProto preserves nil, inverting MembershipPtrToProto.
func MembershipPtrFromProto(p *wardenv1.Membership) *warden.Membership {
	if p == nil {
		return nil
	}
	m := MembershipFromProto(p)
	return &m
}

// ---- PeerView --------------------------------------------------------------

// PeerViewToProto converts a domain PeerView (always non-nil). Member is copied
// verbatim: an empty MemberKind stays empty (the proto reader treats empty as
// the voter default, matching the JSON omitempty contract) so the round trip is
// exact rather than normalising.
func PeerViewToProto(pv warden.PeerView) *wardenv1.PeerView {
	return &wardenv1.PeerView{
		Node:      NodeToProto(pv.Node),
		Status:    string(pv.Status),
		LastSeen:  timeToProto(pv.LastSeen),
		LatencyMs: pv.LatencyMS,
		Member:    string(pv.Member),
	}
}

// PeerViewFromProto converts a proto PeerView; a nil message is the zero value.
func PeerViewFromProto(p *wardenv1.PeerView) warden.PeerView {
	if p == nil {
		return warden.PeerView{}
	}
	return warden.PeerView{
		Node:      NodeFromProto(p.GetNode()),
		Status:    warden.PeerStatus(p.GetStatus()),
		LastSeen:  timeFromProto(p.GetLastSeen()),
		LatencyMS: p.GetLatencyMs(),
		Member:    warden.MemberKind(p.GetMember()),
	}
}

func peersToProto(pvs []warden.PeerView) []*wardenv1.PeerView {
	if len(pvs) == 0 {
		return nil
	}
	out := make([]*wardenv1.PeerView, len(pvs))
	for i, pv := range pvs {
		out[i] = PeerViewToProto(pv)
	}
	return out
}

func peersFromProto(ps []*wardenv1.PeerView) []warden.PeerView {
	if len(ps) == 0 {
		return nil
	}
	out := make([]warden.PeerView, len(ps))
	for i, p := range ps {
		out[i] = PeerViewFromProto(p)
	}
	return out
}

// ---- ClusterView -----------------------------------------------------------

// ClusterViewToProto converts a domain ClusterView value (always non-nil).
func ClusterViewToProto(v warden.ClusterView) *wardenv1.ClusterView {
	return &wardenv1.ClusterView{
		Self:             string(v.Self),
		Role:             string(v.Role),
		Term:             uint64(v.Term),
		LeaderId:         string(v.LeaderID),
		Source:           string(v.Source),
		Authoritative:    v.Authoritative,
		UpdatedAt:        timeToProto(v.UpdatedAt),
		Peers:            peersToProto(v.Peers),
		ElectionsStarted: v.ElectionsStarted,
		Membership:       MembershipToProto(v.Membership),
	}
}

// ClusterViewFromProto converts a proto ClusterView; a nil message is the zero
// value.
func ClusterViewFromProto(p *wardenv1.ClusterView) warden.ClusterView {
	if p == nil {
		return warden.ClusterView{}
	}
	return warden.ClusterView{
		Self:             warden.NodeID(p.GetSelf()),
		Role:             warden.Role(p.GetRole()),
		Term:             warden.Term(p.GetTerm()),
		LeaderID:         warden.NodeID(p.GetLeaderId()),
		Source:           warden.NodeID(p.GetSource()),
		Authoritative:    p.GetAuthoritative(),
		UpdatedAt:        timeFromProto(p.GetUpdatedAt()),
		Peers:            peersFromProto(p.GetPeers()),
		ElectionsStarted: p.GetElectionsStarted(),
		Membership:       MembershipFromProto(p.GetMembership()),
	}
}

// ClusterViewPtrToProto preserves nil (HeartbeatRequest.View is optional).
func ClusterViewPtrToProto(v *warden.ClusterView) *wardenv1.ClusterView {
	if v == nil {
		return nil
	}
	return ClusterViewToProto(*v)
}

// ClusterViewPtrFromProto preserves nil, inverting ClusterViewPtrToProto.
func ClusterViewPtrFromProto(p *wardenv1.ClusterView) *warden.ClusterView {
	if p == nil {
		return nil
	}
	v := ClusterViewFromProto(p)
	return &v
}

// ---- PersistentState -------------------------------------------------------

// PersistentStateToProto converts the durable election record (always non-nil).
func PersistentStateToProto(st warden.PersistentState) *wardenv1.PersistentState {
	return &wardenv1.PersistentState{
		CurrentTerm: uint64(st.CurrentTerm),
		VotedFor:    string(st.VotedFor),
		Membership:  MembershipPtrToProto(st.Membership),
	}
}

// PersistentStateFromProto converts a proto PersistentState; a nil message is
// the zero state.
func PersistentStateFromProto(p *wardenv1.PersistentState) warden.PersistentState {
	if p == nil {
		return warden.PersistentState{}
	}
	return warden.PersistentState{
		CurrentTerm: warden.Term(p.GetCurrentTerm()),
		VotedFor:    warden.NodeID(p.GetVotedFor()),
		Membership:  MembershipPtrFromProto(p.GetMembership()),
	}
}

// ---- Vote ------------------------------------------------------------------

// VoteRequestToProto converts a domain VoteRequest (always non-nil).
func VoteRequestToProto(r warden.VoteRequest) *wardenv1.VoteRequest {
	return &wardenv1.VoteRequest{Term: uint64(r.Term), CandidateId: string(r.CandidateID)}
}

// VoteRequestFromProto converts a proto VoteRequest; a nil message is the zero
// request.
func VoteRequestFromProto(p *wardenv1.VoteRequest) warden.VoteRequest {
	if p == nil {
		return warden.VoteRequest{}
	}
	return warden.VoteRequest{Term: warden.Term(p.GetTerm()), CandidateID: warden.NodeID(p.GetCandidateId())}
}

// VoteResponseToProto converts a domain VoteResponse (always non-nil).
func VoteResponseToProto(r warden.VoteResponse) *wardenv1.VoteResponse {
	return &wardenv1.VoteResponse{Term: uint64(r.Term), Granted: r.Granted, VoterId: string(r.VoterID)}
}

// VoteResponseFromProto converts a proto VoteResponse; a nil message is the zero
// response.
func VoteResponseFromProto(p *wardenv1.VoteResponse) warden.VoteResponse {
	if p == nil {
		return warden.VoteResponse{}
	}
	return warden.VoteResponse{Term: warden.Term(p.GetTerm()), Granted: p.GetGranted(), VoterID: warden.NodeID(p.GetVoterId())}
}

// ---- Heartbeat -------------------------------------------------------------

// HeartbeatRequestToProto converts a domain HeartbeatRequest (always non-nil);
// its optional View and Membership preserve nil.
func HeartbeatRequestToProto(r warden.HeartbeatRequest) *wardenv1.HeartbeatRequest {
	return &wardenv1.HeartbeatRequest{
		Term:       uint64(r.Term),
		LeaderId:   string(r.LeaderID),
		View:       ClusterViewPtrToProto(r.View),
		Membership: MembershipPtrToProto(r.Membership),
	}
}

// HeartbeatRequestFromProto converts a proto HeartbeatRequest; a nil message is
// the zero request.
func HeartbeatRequestFromProto(p *wardenv1.HeartbeatRequest) warden.HeartbeatRequest {
	if p == nil {
		return warden.HeartbeatRequest{}
	}
	return warden.HeartbeatRequest{
		Term:       warden.Term(p.GetTerm()),
		LeaderID:   warden.NodeID(p.GetLeaderId()),
		View:       ClusterViewPtrFromProto(p.GetView()),
		Membership: MembershipPtrFromProto(p.GetMembership()),
	}
}

// HeartbeatResponseToProto converts a domain HeartbeatResponse (always non-nil).
func HeartbeatResponseToProto(r warden.HeartbeatResponse) *wardenv1.HeartbeatResponse {
	return &wardenv1.HeartbeatResponse{Term: uint64(r.Term), Ok: r.OK, NodeId: string(r.NodeID)}
}

// HeartbeatResponseFromProto converts a proto HeartbeatResponse; a nil message
// is the zero response.
func HeartbeatResponseFromProto(p *wardenv1.HeartbeatResponse) warden.HeartbeatResponse {
	if p == nil {
		return warden.HeartbeatResponse{}
	}
	return warden.HeartbeatResponse{Term: warden.Term(p.GetTerm()), OK: p.GetOk(), NodeID: warden.NodeID(p.GetNodeId())}
}

// ---- Identify --------------------------------------------------------------

// IdentifyResponseToProto converts a domain IdentifyResponse (always non-nil).
func IdentifyResponseToProto(r warden.IdentifyResponse) *wardenv1.IdentifyResponse {
	return &wardenv1.IdentifyResponse{ClusterId: r.ClusterID, NodeId: string(r.NodeID), Version: r.Version}
}

// IdentifyResponseFromProto converts a proto IdentifyResponse; a nil message is
// the zero response.
func IdentifyResponseFromProto(p *wardenv1.IdentifyResponse) warden.IdentifyResponse {
	if p == nil {
		return warden.IdentifyResponse{}
	}
	return warden.IdentifyResponse{ClusterID: p.GetClusterId(), NodeID: warden.NodeID(p.GetNodeId()), Version: p.GetVersion()}
}

// ---- Cluster-watch cursor --------------------------------------------------

// CursorOf derives the dedup/resume cursor from a proto ClusterView: the
// (membership.version, membership.created_in_term, view.updated_at) triple the
// schema fixes as the identity of an observable cluster state. It is always
// non-nil; a nil view yields the zero cursor.
func CursorOf(v *wardenv1.ClusterView) *wardenv1.ClusterViewCursor {
	c := &wardenv1.ClusterViewCursor{ViewUpdatedAt: v.GetUpdatedAt()}
	if m := v.GetMembership(); m != nil {
		c.MembershipVersion = m.GetVersion()
		c.MembershipCreatedInTerm = m.GetCreatedInTerm()
	}
	return c
}

// CursorEqual reports whether two cursors denote the same observable state. A
// nil cursor is treated as the zero cursor, so a client that opens a watch
// without a `since` cursor never accidentally matches a populated one.
func CursorEqual(a, b *wardenv1.ClusterViewCursor) bool {
	if a == nil {
		a = &wardenv1.ClusterViewCursor{}
	}
	if b == nil {
		b = &wardenv1.ClusterViewCursor{}
	}
	return proto.Equal(a, b)
}
