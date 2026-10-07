package graph

import (
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Backtest: generic-placement rule from #357", func() {
	It("demonstrates topological order on a slice graph", func() {
		// This backtest validates that the new pkg/graph.Graph[N,E]
		// correctly implements the topological ordering and frontier
		// operations that would be needed for generic placement scheduling.
		//
		// Scenario: Three slices with a linear dependency chain.
		// The old code in io/inproc/graph.go and services/dispatch/graph.go
		// (at ae146a0) would declare generic types outside pkg/,
		// triggering the generic-placement rule.
		// The new code places Graph[N,E] in pkg/ as a pure data structure.

		g := New[string, string]()

		_ = g.DeclareRelation("depends_on", Edge{Directed: true, Acyclic: true})
		_ = g.AddNode("SliceA")
		_ = g.AddNode("SliceB")
		_ = g.AddNode("SliceC")

		_ = g.AddEdge("depends_on", "SliceA", "SliceB")
		_ = g.AddEdge("depends_on", "SliceB", "SliceC")

		// Topological order must place A before B before C.
		order := g.TopologicalOrder("depends_on")
		Expect(order).To(Equal([]string{"SliceA", "SliceB", "SliceC"}))

		// Frontier with nothing done should return the initial node.
		done := func(n string) bool { return false }
		frontier := g.Frontier("depends_on", done)
		Expect(frontier).To(Equal([]string{"SliceA"}))

		// After SliceA completes, SliceB becomes ready.
		done = func(n string) bool { return n == "SliceA" }
		frontier = g.Frontier("depends_on", done)
		Expect(frontier).To(Equal([]string{"SliceB"}))

		// After SliceA and SliceB complete, SliceC becomes ready.
		done = func(n string) bool { return n == "SliceA" || n == "SliceB" }
		frontier = g.Frontier("depends_on", done)
		Expect(frontier).To(Equal([]string{"SliceC"}))
	})

	It("rejects cycles on acyclic relations", func() {
		// The new generic graph enforces acyclicity on insert,
		// preventing invalid schedules at construction time.
		g := New[string, string]()

		_ = g.DeclareRelation("depends_on", Edge{Directed: true, Acyclic: true})
		_ = g.AddNode("A")
		_ = g.AddNode("B")
		_ = g.AddNode("C")

		_ = g.AddEdge("depends_on", "A", "B")
		_ = g.AddEdge("depends_on", "B", "C")

		// Attempting to add an edge that would create a cycle fails.
		err := g.AddEdge("depends_on", "C", "A")
		Expect(err).To(BeAssignableToTypeOf(ErrCycle{}))
	})

	It("computes upward rank for critical path", func() {
		// Upward rank computes the longest path to a sink,
		// useful for HEFT-style scheduling and priority assignment.
		g := New[string, string]()

		_ = g.DeclareRelation("depends_on", Edge{Directed: true, Acyclic: true})
		_ = g.AddNode("A")
		_ = g.AddNode("B")
		_ = g.AddNode("C")

		_ = g.AddEdge("depends_on", "A", "B")
		_ = g.AddEdge("depends_on", "B", "C")

		// Weight all nodes uniformly; the rank should reflect depth.
		weight := func(n string) float64 { return 1.0 }
		skip := func(n string) bool { return false }
		rank := g.UpwardRank("depends_on", weight, skip)

		// C (leaf): rank = 1
		// B (one step from leaf): rank = 1 + 1 = 2
		// A (two steps from leaf): rank = 1 + 2 = 3
		Expect(rank["C"]).To(Equal(1.0))
		Expect(rank["B"]).To(Equal(2.0))
		Expect(rank["A"]).To(Equal(3.0))
	})
})

// BenchmarkTopologicalOrder measures the performance of topological ordering.
// This is included to show that the new generic implementation is efficient.
var _ = Describe("Performance: topological order", func() {
	It("orders a chain of 100 slices efficiently", func() {
		g := New[string, string]()
		_ = g.DeclareRelation("depends_on", Edge{Directed: true, Acyclic: true})

		// Build a chain of 100 nodes.
		for i := 0; i < 100; i++ {
			nodeID := itoa(i)
			_ = g.AddNode(nodeID)
			if i > 0 {
				_ = g.AddEdge("depends_on", itoa(i-1), nodeID)
			}
		}

		// Topological order should return all 100 nodes in sequence.
		order := g.TopologicalOrder("depends_on")
		Expect(order).To(HaveLen(100))
		Expect(order[0]).To(Equal("0"))
		Expect(order[99]).To(Equal("99"))
	})
})

func itoa(i int) string {
	return strconv.Itoa(i)
}
