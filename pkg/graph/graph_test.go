package graph

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGraph(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Graph Suite")
}

var _ = Describe("Graph", func() {
	var g *Graph[string, string]

	BeforeEach(func() {
		g = New[string, string]()
	})

	Describe("DeclareRelation", func() {
		It("declares an edge kind", func() {
			err := g.DeclareRelation("depends", Edge{Directed: true, Acyclic: true})
			Expect(err).NotTo(HaveOccurred())
		})

		It("rejects duplicate declarations", func() {
			_ = g.DeclareRelation("depends", Edge{Directed: true, Acyclic: true})
			err := g.DeclareRelation("depends", Edge{Directed: true, Acyclic: true})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("AddNode", func() {
		It("adds a node", func() {
			err := g.AddNode("A")
			Expect(err).NotTo(HaveOccurred())
			Expect(g.Has("A")).To(BeTrue())
		})

		It("rejects duplicate nodes", func() {
			_ = g.AddNode("A")
			err := g.AddNode("A")
			Expect(err).To(BeAssignableToTypeOf(ErrDuplicateNode{}))
		})

		It("maintains insertion order", func() {
			_ = g.AddNode("C")
			_ = g.AddNode("A")
			_ = g.AddNode("B")
			Expect(g.Nodes()).To(Equal([]string{"C", "A", "B"}))
		})

		It("returns Len correctly", func() {
			_ = g.AddNode("A")
			_ = g.AddNode("B")
			Expect(g.Len()).To(Equal(2))
		})
	})

	Describe("AddEdge", func() {
		BeforeEach(func() {
			_ = g.DeclareRelation("depends", Edge{Directed: true, Acyclic: true})
			_ = g.AddNode("A")
			_ = g.AddNode("B")
			_ = g.AddNode("C")
		})

		It("adds a directed edge", func() {
			err := g.AddEdge("depends", "A", "B")
			Expect(err).NotTo(HaveOccurred())
			Expect(g.HasEdge("depends", "A", "B")).To(BeTrue())
			Expect(g.HasEdge("depends", "B", "A")).To(BeFalse())
		})

		It("rejects duplicate edges", func() {
			_ = g.AddEdge("depends", "A", "B")
			err := g.AddEdge("depends", "A", "B")
			Expect(err).To(BeAssignableToTypeOf(ErrDuplicateEdge{}))
		})

		It("rejects edges with unknown nodes", func() {
			err := g.AddEdge("depends", "A", "Unknown")
			Expect(err).To(BeAssignableToTypeOf(ErrUnknownNode{}))
		})

		It("rejects edges with unknown relations", func() {
			err := g.AddEdge("unknown", "A", "B")
			Expect(err).To(BeAssignableToTypeOf(ErrUnknownRelation{}))
		})

		It("detects cycles in acyclic relations", func() {
			_ = g.AddEdge("depends", "A", "B")
			_ = g.AddEdge("depends", "B", "C")
			err := g.AddEdge("depends", "C", "A")
			Expect(err).To(BeAssignableToTypeOf(ErrCycle{}))
		})

		It("computes successors", func() {
			_ = g.AddEdge("depends", "A", "B")
			_ = g.AddEdge("depends", "A", "C")
			Expect(g.Out("depends", "A")).To(ConsistOf("B", "C"))
		})

		It("computes predecessors", func() {
			_ = g.AddEdge("depends", "A", "B")
			_ = g.AddEdge("depends", "C", "B")
			Expect(g.In("depends", "B")).To(ConsistOf("A", "C"))
		})

		It("checks reachability", func() {
			_ = g.AddEdge("depends", "A", "B")
			_ = g.AddEdge("depends", "B", "C")
			Expect(g.Reaches("depends", "A", "C")).To(BeTrue())
			Expect(g.Reaches("depends", "C", "A")).To(BeFalse())
		})
	})

	Describe("TopologicalOrder", func() {
		BeforeEach(func() {
			_ = g.DeclareRelation("depends", Edge{Directed: true, Acyclic: true})
			_ = g.AddNode("A")
			_ = g.AddNode("B")
			_ = g.AddNode("C")
			_ = g.AddEdge("depends", "A", "B")
			_ = g.AddEdge("depends", "B", "C")
		})

		It("returns a valid topological order", func() {
			order := g.TopologicalOrder("depends")
			Expect(order).To(Equal([]string{"A", "B", "C"}))
		})

		It("returns nil for unknown relation", func() {
			order := g.TopologicalOrder("unknown")
			Expect(order).To(BeNil())
		})
	})

	Describe("Frontier", func() {
		BeforeEach(func() {
			_ = g.DeclareRelation("depends", Edge{Directed: true, Acyclic: true})
			_ = g.AddNode("A")
			_ = g.AddNode("B")
			_ = g.AddNode("C")
			_ = g.AddEdge("depends", "A", "B")
			_ = g.AddEdge("depends", "B", "C")
		})

		It("returns nodes with all predecessors done", func() {
			done := func(n string) bool { return false }
			frontier := g.Frontier("depends", done)
			Expect(frontier).To(Equal([]string{"A"}))
		})

		It("updates frontier as nodes complete", func() {
			done := func(n string) bool { return n == "A" }
			frontier := g.Frontier("depends", done)
			Expect(frontier).To(Equal([]string{"B"}))
		})

		It("returns nodes in insertion order", func() {
			_ = g.AddNode("D")
			_ = g.AddEdge("depends", "A", "D")
			done := func(n string) bool { return false }
			frontier := g.Frontier("depends", done)
			Expect(frontier).To(Equal([]string{"A"}))
		})
	})

	Describe("UpwardRank", func() {
		BeforeEach(func() {
			_ = g.DeclareRelation("depends", Edge{Directed: true, Acyclic: true})
			_ = g.AddNode("A")
			_ = g.AddNode("B")
			_ = g.AddNode("C")
			_ = g.AddEdge("depends", "A", "B")
			_ = g.AddEdge("depends", "B", "C")
		})

		It("computes upward rank", func() {
			weight := func(n string) float64 { return 1.0 }
			skip := func(n string) bool { return false }
			rank := g.UpwardRank("depends", weight, skip)
			Expect(rank["C"]).To(Equal(1.0))
			Expect(rank["B"]).To(Equal(2.0))
			Expect(rank["A"]).To(Equal(3.0))
		})

		It("skips nodes", func() {
			weight := func(n string) float64 { return 1.0 }
			skip := func(n string) bool { return n == "C" }
			rank := g.UpwardRank("depends", weight, skip)
			Expect(rank["B"]).To(Equal(1.0))
			Expect(rank["A"]).To(Equal(2.0))
		})
	})
})
