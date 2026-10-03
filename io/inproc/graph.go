// Copyright 2026 Candace Labs

package inproc

import (
	"errors"
	"fmt"
	"slices"
	"sync"
)

var (
	// ErrCycle reports an edge that would make an acyclic relation cyclic.
	ErrCycle = errors.New("inproc: edge would close a cycle")
	// ErrUnknownNode reports an edge end that was never added.
	ErrUnknownNode = errors.New("inproc: unknown node")
	// ErrDuplicateNode reports a node added twice.
	ErrDuplicateNode = errors.New("inproc: node already present")
	// ErrUnknownRelation reports an edge kind the graph was not built with.
	ErrUnknownRelation = errors.New("inproc: unknown relation")
	// ErrNoRelations reports a graph built with no edge kinds.
	ErrNoRelations = errors.New("inproc: a graph needs at least one relation")
)

// Relation describes one edge kind of a [Graph]: whether its edges have a
// direction, and, when they do, whether the kind must stay acyclic.
type Relation struct {
	Directed bool
	Acyclic  bool
}

// Graph is a store of nodes N joined by edges of kinds E, each kind with its
// own [Relation]. It is the one graph shape in-process stores are built on: a
// slice graph's depends_on (directed, acyclic) and contends (undirected) are
// two kinds on one graph. Nodes keep insertion order, which is the tie-break
// every query uses, so results are deterministic.
//
// The lock guards the adjacency and every mutation is a single step that
// calls nothing it does not control, so it is a leaf (house rule CS-5).
// Queries that take a caller's function run it on a snapshot, outside the
// lock.
type Graph[N comparable, E comparable] struct {
	relations map[E]Relation
	guard     sync.RWMutex
	nodes     []N
	// out[kind][n] are the nodes n has a kind edge to; for an undirected
	// kind both ends list each other.
	out map[E]map[N][]N
	in  map[E]map[N][]N
}

// NewGraph returns an empty graph with the given edge kinds.
func NewGraph[N comparable, E comparable](relations map[E]Relation) (*Graph[N, E], error) {
	if len(relations) == 0 {
		return nil, ErrNoRelations
	}
	graph := &Graph[N, E]{relations: map[E]Relation{}, out: map[E]map[N][]N{}, in: map[E]map[N][]N{}}
	for kind, relation := range relations {
		graph.relations[kind] = relation
		graph.out[kind] = map[N][]N{}
		graph.in[kind] = map[N][]N{}
	}
	return graph, nil
}

// AddNode adds n with no edges.
func (graph *Graph[N, E]) AddNode(n N) error {
	graph.guard.Lock()
	defer graph.guard.Unlock()
	if slices.Contains(graph.nodes, n) {
		return fmt.Errorf("%w: %v", ErrDuplicateNode, n)
	}
	graph.nodes = append(graph.nodes, n)
	return nil
}

// Has reports whether n is a node.
func (graph *Graph[N, E]) Has(n N) bool {
	graph.guard.RLock()
	defer graph.guard.RUnlock()
	return slices.Contains(graph.nodes, n)
}

// Len is the number of nodes.
func (graph *Graph[N, E]) Len() int {
	graph.guard.RLock()
	defer graph.guard.RUnlock()
	return len(graph.nodes)
}

// Nodes returns every node in insertion order.
func (graph *Graph[N, E]) Nodes() []N {
	graph.guard.RLock()
	defer graph.guard.RUnlock()
	return slices.Clone(graph.nodes)
}

// AddEdge joins from and to with an edge of kind. A directed kind reads
// from -> to; an undirected kind joins both ways. It rejects an unknown kind
// or end, a self-edge, and, for an acyclic kind, an edge that would close a
// cycle; a repeated edge is accepted once.
func (graph *Graph[N, E]) AddEdge(kind E, from N, to N) error {
	graph.guard.Lock()
	defer graph.guard.Unlock()
	relation, known := graph.relations[kind]
	if !known {
		return fmt.Errorf("%w: %v", ErrUnknownRelation, kind)
	}
	for _, end := range []N{from, to} {
		if !slices.Contains(graph.nodes, end) {
			return fmt.Errorf("%w: %v", ErrUnknownNode, end)
		}
	}
	if from == to {
		return fmt.Errorf("%w: %v -> %v", ErrCycle, from, to)
	}
	if relation.Directed && relation.Acyclic && graph.reaches(kind, to, from) {
		return fmt.Errorf("%w: %v -> %v", ErrCycle, from, to)
	}
	if slices.Contains(graph.out[kind][from], to) {
		return nil
	}
	graph.out[kind][from] = append(graph.out[kind][from], to)
	graph.in[kind][to] = append(graph.in[kind][to], from)
	if !relation.Directed {
		graph.out[kind][to] = append(graph.out[kind][to], from)
		graph.in[kind][from] = append(graph.in[kind][from], to)
	}
	return nil
}

// HasEdge reports an edge of kind from -> to; for an undirected kind, that
// the two are joined.
func (graph *Graph[N, E]) HasEdge(kind E, from N, to N) bool {
	graph.guard.RLock()
	defer graph.guard.RUnlock()
	return slices.Contains(graph.out[kind][from], to)
}

// Out returns the nodes n has a kind edge to: its successors, or for an
// undirected kind its neighbors, in edge order.
func (graph *Graph[N, E]) Out(kind E, n N) []N {
	graph.guard.RLock()
	defer graph.guard.RUnlock()
	return slices.Clone(graph.out[kind][n])
}

// In returns the nodes with a kind edge to n: its predecessors, or for an
// undirected kind its neighbors, in edge order.
func (graph *Graph[N, E]) In(kind E, n N) []N {
	graph.guard.RLock()
	defer graph.guard.RUnlock()
	return slices.Clone(graph.in[kind][n])
}

// Reaches reports whether to is reachable from from along one or more kind
// edges: for an acyclic kind, whether an edge to -> from would close a cycle.
func (graph *Graph[N, E]) Reaches(kind E, from N, to N) bool {
	graph.guard.RLock()
	defer graph.guard.RUnlock()
	return graph.reaches(kind, from, to)
}

// reaches is Reaches under the lock.
func (graph *Graph[N, E]) reaches(kind E, start N, target N) bool {
	visited := map[N]bool{start: true}
	stack := []N{start}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, next := range graph.out[kind][current] {
			if next == target {
				return true
			}
			if !visited[next] {
				visited[next] = true
				stack = append(stack, next)
			}
		}
	}
	return false
}

// view is a consistent copy of one kind's adjacency, taken under the read
// lock so a query can call the caller's functions outside it.
type view[N comparable] struct {
	nodes []N
	out   map[N][]N
	in    map[N][]N
}

func (graph *Graph[N, E]) snapshot(kind E) view[N] {
	graph.guard.RLock()
	defer graph.guard.RUnlock()
	copied := view[N]{nodes: slices.Clone(graph.nodes), out: make(map[N][]N, len(graph.nodes)), in: make(map[N][]N, len(graph.nodes))}
	for n, edges := range graph.out[kind] {
		copied.out[n] = slices.Clone(edges)
	}
	for n, edges := range graph.in[kind] {
		copied.in[n] = slices.Clone(edges)
	}
	return copied
}

// Frontier is the ready set of a directed kind: every node that is not done
// and whose kind predecessors are all done, in insertion order. It is a
// query over the store, not a copy of it.
func (graph *Graph[N, E]) Frontier(kind E, done func(n N) bool) []N {
	snapshot := graph.snapshot(kind)
	var ready []N
	for _, n := range snapshot.nodes {
		if done(n) {
			continue
		}
		blocked := false
		for _, predecessor := range snapshot.in[n] {
			if !done(predecessor) {
				blocked = true
				break
			}
		}
		if !blocked {
			ready = append(ready, n)
		}
	}
	return ready
}

// TopologicalOrder returns every node after all of its kind predecessors,
// taking nodes in insertion order whenever more than one is available
// (Kahn's algorithm). It is defined for an acyclic kind.
func (graph *Graph[N, E]) TopologicalOrder(kind E) []N {
	return topologicalOrder(graph.snapshot(kind))
}

func topologicalOrder[N comparable](snapshot view[N]) []N {
	remaining := make(map[N]int, len(snapshot.nodes))
	for _, n := range snapshot.nodes {
		remaining[n] = len(snapshot.in[n])
	}
	order := make([]N, 0, len(snapshot.nodes))
	placed := map[N]bool{}
	for len(order) < len(snapshot.nodes) {
		progressed := false
		for _, n := range snapshot.nodes {
			if placed[n] || remaining[n] != 0 {
				continue
			}
			placed[n] = true
			progressed = true
			order = append(order, n)
			for _, successor := range snapshot.out[n] {
				remaining[successor]--
			}
		}
		if !progressed {
			// A cyclic kind: the rest has no order.
			break
		}
	}
	return order
}

// UpwardRank is the longest path from each node to the end of a directed
// acyclic kind, counting the node's own weight and every successor along
// the heaviest chain: the upward rank of list scheduling (Topcuoglu, Hariri
// and Wu, HEFT, 2002) with no communication cost. Nodes excluded by skip
// contribute nothing and are not ranked; a weight that is not positive
// counts as zero.
func (graph *Graph[N, E]) UpwardRank(kind E, weight func(n N) float64, skip func(n N) bool) map[N]float64 {
	snapshot := graph.snapshot(kind)
	order := topologicalOrder(snapshot)
	ranks := make(map[N]float64, len(order))
	for index := len(order) - 1; index >= 0; index-- {
		n := order[index]
		if skip != nil && skip(n) {
			continue
		}
		best := 0.0
		for _, successor := range snapshot.out[n] {
			if rank, ranked := ranks[successor]; ranked {
				best = max(best, rank)
			}
		}
		ranks[n] = max(0, weight(n)) + best
	}
	return ranks
}
