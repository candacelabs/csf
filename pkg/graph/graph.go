// Package graph provides a typed, generic directed or acyclic graph with
// multiple edge kinds. Each kind declares whether edges are directed and
// acyclic through its type; cycles are rejected on insert. Graph is a pure
// data structure with no locks, goroutines, or I/O; synchronization belongs
// in a store that places the graph.
package graph

import (
	"fmt"
)

// Edge describes the properties of one kind of edge.
type Edge struct {
	Directed bool
	Acyclic  bool
}

// Graph[N, E] is a typed, generic directed or acyclic graph where N is the
// node type and E is the edge kind type. Nodes carry payloads of type N;
// edge kinds declare their properties (directed, acyclic) through Edge.
type Graph[N comparable, E comparable] struct {
	edges   map[E]Edge
	nodes   []N
	nodeSet map[N]bool
	adj     map[E]map[N]map[N]bool
	inEdges map[E]map[N]map[N]bool
}

// ErrCycle indicates that adding an edge would create a cycle in an acyclic relation.
type ErrCycle struct {
	Kind   interface{}
	From   interface{}
	To     interface{}
	Reason string
}

func (err ErrCycle) Error() string {
	return fmt.Sprintf("cycle in acyclic edge kind %v: %v → %v (%s)", err.Kind, err.From, err.To, err.Reason)
}

// ErrDuplicateNode indicates that the node is already in the graph.
type ErrDuplicateNode struct {
	Node interface{}
}

func (err ErrDuplicateNode) Error() string {
	return fmt.Sprintf("node already exists: %v", err.Node)
}

// ErrUnknownNode indicates that the node is not in the graph.
type ErrUnknownNode struct {
	Node interface{}
}

func (err ErrUnknownNode) Error() string {
	return fmt.Sprintf("node not found: %v", err.Node)
}

// ErrUnknownRelation indicates that the edge kind is not declared.
type ErrUnknownRelation struct {
	Kind interface{}
}

func (err ErrUnknownRelation) Error() string {
	return fmt.Sprintf("edge kind not found: %v", err.Kind)
}

// ErrDuplicateEdge indicates that the edge already exists.
type ErrDuplicateEdge struct {
	Kind interface{}
	From interface{}
	To   interface{}
}

func (err ErrDuplicateEdge) Error() string {
	return fmt.Sprintf("edge already exists: %v → %v (kind %v)", err.From, err.To, err.Kind)
}

// New creates a new empty graph.
func New[N comparable, E comparable]() *Graph[N, E] {
	return &Graph[N, E]{
		edges:   make(map[E]Edge),
		nodes:   make([]N, 0),
		nodeSet: make(map[N]bool),
		adj:     make(map[E]map[N]map[N]bool),
		inEdges: make(map[E]map[N]map[N]bool),
	}
}

// DeclareRelation declares an edge kind with its properties. It must be called
// before any edges of that kind are added.
func (g *Graph[N, E]) DeclareRelation(kind E, edge Edge) error {
	if _, exists := g.edges[kind]; exists {
		return fmt.Errorf("relation already declared: %v", kind)
	}
	g.edges[kind] = edge
	g.adj[kind] = make(map[N]map[N]bool)
	g.inEdges[kind] = make(map[N]map[N]bool)
	return nil
}

// AddNode adds a node to the graph. It returns ErrDuplicateNode if the node
// already exists.
func (g *Graph[N, E]) AddNode(node N) error {
	if g.nodeSet[node] {
		return ErrDuplicateNode{Node: node}
	}
	g.nodes = append(g.nodes, node)
	g.nodeSet[node] = true
	for kind := range g.edges {
		g.adj[kind][node] = make(map[N]bool)
		g.inEdges[kind][node] = make(map[N]bool)
	}
	return nil
}

// AddEdge adds a directed edge from `from` to `to` with the given kind.
// It returns an error if:
//   - the edge kind is not declared
//   - either endpoint is not in the graph
//   - the edge already exists
//   - the edge is not directed and both directions already exist
//   - the edge would create a cycle in an acyclic relation
func (g *Graph[N, E]) AddEdge(kind E, from N, to N) error {
	edge, exists := g.edges[kind]
	if !exists {
		return ErrUnknownRelation{Kind: kind}
	}

	if !g.nodeSet[from] {
		return ErrUnknownNode{Node: from}
	}
	if !g.nodeSet[to] {
		return ErrUnknownNode{Node: to}
	}

	if g.adj[kind][from][to] {
		return ErrDuplicateEdge{Kind: kind, From: from, To: to}
	}

	if edge.Acyclic && g.reaches(kind, to, from) {
		return ErrCycle{Kind: kind, From: from, To: to, Reason: "to reaches from"}
	}

	g.adj[kind][from][to] = true
	g.inEdges[kind][to][from] = true

	if !edge.Directed && from != to {
		if g.adj[kind][to][from] {
			return fmt.Errorf("reverse edge already exists: %v → %v (kind %v)", to, from, kind)
		}
		if edge.Acyclic && g.reaches(kind, from, to) {
			return ErrCycle{Kind: kind, From: to, To: from, Reason: "from reaches to"}
		}
		g.adj[kind][to][from] = true
		g.inEdges[kind][from][to] = true
	}

	return nil
}

// Has reports whether node is in the graph.
func (g *Graph[N, E]) Has(node N) bool {
	return g.nodeSet[node]
}

// HasEdge reports whether a directed edge exists from `from` to `to` with the given kind.
func (g *Graph[N, E]) HasEdge(kind E, from N, to N) bool {
	if _, exists := g.edges[kind]; !exists {
		return false
	}
	return g.adj[kind][from][to]
}

// Out returns the successors of node for the given edge kind, in insertion order.
func (g *Graph[N, E]) Out(kind E, node N) []N {
	if _, exists := g.edges[kind]; !exists {
		return nil
	}
	if _, exists := g.nodeSet[node]; !exists {
		return nil
	}
	var result []N
	for _, n := range g.nodes {
		if g.adj[kind][node][n] {
			result = append(result, n)
		}
	}
	return result
}

// In returns the predecessors of node for the given edge kind, in insertion order.
func (g *Graph[N, E]) In(kind E, node N) []N {
	if _, exists := g.edges[kind]; !exists {
		return nil
	}
	if _, exists := g.nodeSet[node]; !exists {
		return nil
	}
	var result []N
	for _, n := range g.nodes {
		if g.inEdges[kind][node][n] {
			result = append(result, n)
		}
	}
	return result
}

// Reaches reports whether there is a directed path from `from` to `to` for the given edge kind.
func (g *Graph[N, E]) Reaches(kind E, from N, to N) bool {
	return g.reaches(kind, from, to)
}

// reaches checks reachability using depth-first search.
func (g *Graph[N, E]) reaches(kind E, from N, to N) bool {
	if from == to {
		return true
	}
	if _, exists := g.edges[kind]; !exists {
		return false
	}
	visited := make(map[N]bool)
	var dfs func(node N) bool
	dfs = func(node N) bool {
		if visited[node] {
			return false
		}
		visited[node] = true
		if node == to {
			return true
		}
		for succ := range g.adj[kind][node] {
			if dfs(succ) {
				return true
			}
		}
		return false
	}
	return dfs(from)
}

// Nodes returns all nodes in the graph in insertion order.
func (g *Graph[N, E]) Nodes() []N {
	return append([]N(nil), g.nodes...)
}

// Len returns the number of nodes in the graph.
func (g *Graph[N, E]) Len() int {
	return len(g.nodes)
}
