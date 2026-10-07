package warden

import "sort"

// Node is a member of the static cluster peer set.
type Node struct {
	ID NodeID `json:"id" yaml:"id"`
	// Addr is the host:port the node's warden HTTP server listens on,
	// reachable over the tailnet (e.g. "203.0.113.10:7717").
	Addr string `json:"addr" yaml:"addr"`
}

// SortNodes sorts a Node slice by ID, the canonical order for
// Membership.Voters and Roster.Nodes.
func SortNodes(ns []Node) {
	sort.Slice(ns, func(i, j int) bool { return ns[i].ID < ns[j].ID })
}
