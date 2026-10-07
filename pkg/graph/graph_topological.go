package graph

// TopologicalOrder returns the nodes of the graph in topological order for the
// given edge kind, using insertion order as a tie-breaker. It returns nil if
// the edge kind is unknown or if a cycle exists (which should not happen if
// the graph enforces acyclicity).
func (g *Graph[N, E]) TopologicalOrder(kind E) []N {
	if _, exists := g.edges[kind]; !exists {
		return nil
	}

	// Kahn's algorithm: compute in-degree for all nodes, then repeatedly
	// emit nodes with in-degree 0, decrementing neighbors' in-degrees.
	inDegree := make(map[N]int)
	for _, node := range g.nodes {
		inDegree[node] = len(g.inEdges[kind][node])
	}

	var queue []N
	for _, node := range g.nodes {
		if inDegree[node] == 0 {
			queue = append(queue, node)
		}
	}

	var result []N
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)

		for succ := range g.adj[kind][node] {
			inDegree[succ]--
			if inDegree[succ] == 0 {
				queue = append(queue, succ)
			}
		}
	}

	return result
}

// UpwardRank computes the longest path to a sink for each node under the given
// edge kind, scaled by the node weight, and with nodes skipped by the skip
// predicate excluded. This is useful for critical-path and HEFT-style
// scheduling. The weight function should return non-negative values; negative
// weights are treated as 0.
func (g *Graph[N, E]) UpwardRank(kind E, weight func(node N) float64, skip func(node N) bool) map[N]float64 {
	if _, exists := g.edges[kind]; !exists {
		return nil
	}

	rank := make(map[N]float64)
	visited := make(map[N]bool)

	var dfs func(node N) float64
	dfs = func(node N) float64 {
		if visited[node] {
			return rank[node]
		}
		visited[node] = true

		w := weight(node)
		if w < 0 {
			w = 0
		}

		maxChild := 0.0
		for child := range g.adj[kind][node] {
			if !skip(child) {
				childRank := dfs(child)
				if childRank > maxChild {
					maxChild = childRank
				}
			}
		}

		rank[node] = w + maxChild
		return rank[node]
	}

	for _, node := range g.nodes {
		if !skip(node) {
			dfs(node)
		}
	}

	return rank
}

// Frontier computes the ready set (frontier) of the graph: nodes that are not
// done and whose predecessors are all done. Nodes are returned in insertion
// order.
func (g *Graph[N, E]) Frontier(kind E, done func(node N) bool) []N {
	if _, exists := g.edges[kind]; !exists {
		return nil
	}

	var result []N
	for _, node := range g.nodes {
		if done(node) {
			continue
		}
		allDone := true
		for pred := range g.inEdges[kind][node] {
			if !done(pred) {
				allDone = false
				break
			}
		}
		if allDone {
			result = append(result, node)
		}
	}
	return result
}
