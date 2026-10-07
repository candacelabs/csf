package warden

// Roster is a discovery snapshot: candidate cluster nodes as reported by an
// IPeerDiscoverer. It is advisory only — it never directly changes voting
// membership.
type Roster struct {
	Nodes []Node `json:"nodes"`
}
