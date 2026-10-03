// Copyright 2026 Candace Labs

package inproc_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/inproc"
)

// The two kinds a slice graph has.
type relation int

const (
	precedes relation = iota
	excludes
)

var relations = map[relation]inproc.Relation{
	precedes: {Directed: true, Acyclic: true},
	excludes: {Directed: false},
}

// diamond builds a -> b, a -> c, b -> d, c -> d, with b and c excluding each
// other.
func diamond() *inproc.Graph[string, relation] {
	graph, err := inproc.NewGraph[string](relations)
	Expect(err).NotTo(HaveOccurred())
	for _, id := range []string{"a", "b", "c", "d"} {
		Expect(graph.AddNode(id)).To(Succeed())
	}
	Expect(graph.AddEdge(precedes, "a", "b")).To(Succeed())
	Expect(graph.AddEdge(precedes, "a", "c")).To(Succeed())
	Expect(graph.AddEdge(precedes, "b", "d")).To(Succeed())
	Expect(graph.AddEdge(precedes, "c", "d")).To(Succeed())
	Expect(graph.AddEdge(excludes, "b", "c")).To(Succeed())
	return graph
}

var _ = Describe("Graph", func() {
	It("needs at least one relation", func() {
		_, err := inproc.NewGraph[string, relation](nil)
		Expect(err).To(MatchError(inproc.ErrNoRelations))
	})

	It("rejects a duplicate node, an unknown kind or end, a self-edge and a cycle of an acyclic kind", func() {
		graph := diamond()
		Expect(graph.AddNode("a")).To(MatchError(inproc.ErrDuplicateNode))
		Expect(graph.AddEdge(relation(9), "a", "b")).To(MatchError(inproc.ErrUnknownRelation))
		Expect(graph.AddEdge(precedes, "a", "z")).To(MatchError(inproc.ErrUnknownNode))
		Expect(graph.AddEdge(precedes, "z", "a")).To(MatchError(inproc.ErrUnknownNode))
		Expect(graph.AddEdge(precedes, "a", "a")).To(MatchError(inproc.ErrCycle))
		Expect(graph.AddEdge(precedes, "d", "a")).To(MatchError(inproc.ErrCycle), "d reaches back to a through b")
		Expect(graph.AddEdge(precedes, "a", "b")).To(Succeed(), "a repeated edge is accepted once")
		Expect(graph.Out(precedes, "a")).To(Equal([]string{"b", "c"}))
		Expect(graph.In(precedes, "d")).To(Equal([]string{"b", "c"}))
		Expect(graph.Reaches(precedes, "a", "d")).To(BeTrue())
		Expect(graph.Reaches(precedes, "d", "a")).To(BeFalse())
		Expect(graph.Has("d")).To(BeTrue())
		Expect(graph.Has("z")).To(BeFalse())
		Expect(graph.Len()).To(Equal(4))
	})

	It("joins an undirected kind both ways and lets it carry cycles", func() {
		graph := diamond()
		Expect(graph.HasEdge(excludes, "b", "c")).To(BeTrue())
		Expect(graph.HasEdge(excludes, "c", "b")).To(BeTrue())
		Expect(graph.HasEdge(excludes, "a", "b")).To(BeFalse())
		Expect(graph.AddEdge(excludes, "c", "d")).To(Succeed())
		Expect(graph.AddEdge(excludes, "d", "b")).To(Succeed(), "b-c-d-b is a cycle, which an undirected kind allows")
		Expect(graph.Out(excludes, "c")).To(Equal([]string{"b", "d"}))
		Expect(graph.AddEdge(excludes, "c", "b")).To(Succeed(), "the reverse spelling of an existing edge adds nothing")
		Expect(graph.Out(excludes, "c")).To(Equal([]string{"b", "d"}))
	})

	It("answers the frontier of a directed kind given what is done", func() {
		graph := diamond()
		none := func(id string) bool { return false }
		Expect(graph.Frontier(precedes, none)).To(Equal([]string{"a"}))
		doneA := func(id string) bool { return id == "a" }
		Expect(graph.Frontier(precedes, doneA)).To(Equal([]string{"b", "c"}))
		doneAB := func(id string) bool { return id == "a" || id == "b" }
		Expect(graph.Frontier(precedes, doneAB)).To(Equal([]string{"c"}), "d still waits for c")
		all := func(id string) bool { return true }
		Expect(graph.Frontier(precedes, all)).To(BeEmpty())
	})

	It("orders nodes topologically in insertion order among the available", func() {
		graph := diamond()
		Expect(graph.TopologicalOrder(precedes)).To(Equal([]string{"a", "b", "c", "d"}))
		Expect(graph.Nodes()).To(Equal([]string{"a", "b", "c", "d"}))
	})

	It("ranks by the longest path ahead, skipping finished nodes", func() {
		graph := diamond()
		unit := func(id string) float64 { return 1 }
		Expect(graph.UpwardRank(precedes, unit, nil)).To(Equal(map[string]float64{"a": 3, "b": 2, "c": 2, "d": 1}))
		weighted := func(id string) float64 {
			if id == "c" {
				return 5
			}
			return 1
		}
		Expect(graph.UpwardRank(precedes, weighted, nil)["a"]).To(Equal(7.0), "a -> c -> d is the heaviest chain")
		skipDone := func(id string) bool { return id == "a" }
		Expect(graph.UpwardRank(precedes, unit, skipDone)).NotTo(HaveKey("a"))
	})
})
