// Copyright 2026 Candace Labs

package listsched_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/inproc"
	"github.com/candacelabs/csf/pkg/listsched"
)

// precedes is the one edge kind these plans order by.
type kind int

const precedes kind = iota

func newOrder(ids ...string) *inproc.Graph[string, kind] {
	graph, err := inproc.NewGraph[string](map[kind]inproc.Relation{precedes: {Directed: true, Acyclic: true}})
	Expect(err).NotTo(HaveOccurred())
	for _, id := range ids {
		Expect(graph.AddNode(id)).To(Succeed())
	}
	return graph
}

// chain builds a -> b -> c plus an independent d.
func chain() *inproc.Graph[string, kind] {
	graph := newOrder("a", "b", "c", "d")
	Expect(graph.AddEdge(precedes, "a", "b")).To(Succeed())
	Expect(graph.AddEdge(precedes, "b", "c")).To(Succeed())
	return graph
}

// byRank orders by upward rank, longest chain first, then by name.
func byRank(graph *inproc.Graph[string, kind]) func(a string, b string) bool {
	ranks := graph.UpwardRank(precedes, func(id string) float64 { return 1 }, nil)
	return func(a string, b string) bool {
		if ranks[a] != ranks[b] {
			return ranks[a] > ranks[b]
		}
		return a < b
	}
}

func byName(a string, b string) bool { return a < b }

var _ = Describe("Pick", func() {
	It("takes ready tasks in priority order up to the free capacity", func() {
		Expect(listsched.Pick([]string{"c", "a", "b"}, nil, 2, nil, byName)).To(Equal([]string{"a", "b"}))
		Expect(listsched.Pick([]string{"c", "a"}, []string{"x", "y"}, 2, nil, byName)).To(BeEmpty(), "no free machine")
		Expect(listsched.Pick(nil, nil, 2, nil, byName)).To(BeEmpty())
	})

	It("skips a task that conflicts with what runs or with an earlier pick, without holding the rest back", func() {
		conflicts := func(a string, b string) bool {
			return (a == "a" && b == "x") || (a == "x" && b == "a") || (a == "b" && b == "c") || (a == "c" && b == "b")
		}
		Expect(listsched.Pick([]string{"a", "b", "c", "d"}, []string{"x"}, 4, conflicts, byName)).To(Equal([]string{"b", "d"}),
			"a conflicts with the running x, c with the picked b; d runs")
	})
})

var _ = Describe("Simulate", func() {
	It("rejects a plan without an order, a priority or capacity", func() {
		_, err := listsched.Simulate(listsched.Plan[string, kind]{})
		Expect(err).To(MatchError(listsched.ErrInvalidPlan))
		_, err = listsched.Simulate(listsched.Plan[string, kind]{Order: chain(), Precedes: precedes, Less: byName})
		Expect(err).To(MatchError(listsched.ErrInvalidPlan))
	})

	It("runs a chain beside an independent task and reports the makespan and concurrency", func() {
		graph := chain()
		plan := listsched.Plan[string, kind]{Order: graph, Precedes: precedes, Capacity: 2, Less: byRank(graph)}
		schedule, err := listsched.Simulate(plan)
		Expect(err).NotTo(HaveOccurred())
		Expect(schedule.Order()).To(Equal([]string{"a", "d", "b", "c"}), "a ranks above d; b and c follow the chain")
		Expect(schedule.Makespan).To(Equal(3 * time.Second))
		Expect(schedule.Peak).To(Equal(2))
		Expect(schedule.Concurrency).To(Equal([]listsched.Sample{
			{At: 0, Running: 2}, {At: time.Second, Running: 1}, {At: 2 * time.Second, Running: 1}, {At: 3 * time.Second, Running: 0},
		}))
		bound, err := listsched.LowerBound(plan)
		Expect(err).NotTo(HaveOccurred())
		Expect(bound).To(Equal(3*time.Second), "the critical path a -> b -> c")
		Expect(schedule.Makespan).To(BeNumerically("<=", listsched.GrahamBound(2, bound)))
	})

	It("never runs two conflicting tasks at once", func() {
		graph := newOrder("p", "q", "r")
		conflicts := func(a string, b string) bool { return a != b && a != "r" && b != "r" }
		plan := listsched.Plan[string, kind]{Order: graph, Precedes: precedes, Capacity: 3, Conflicts: conflicts, Less: byName}
		schedule, err := listsched.Simulate(plan)
		Expect(err).NotTo(HaveOccurred())
		Expect(schedule.Order()).To(Equal([]string{"p", "r", "q"}))
		Expect(schedule.Peak).To(Equal(2))
		Expect(schedule.Makespan).To(Equal(2 * time.Second))
	})

	It("uses the plan's durations and the lower bound follows the total work", func() {
		graph := newOrder("long", "short1", "short2")
		duration := func(id string) time.Duration {
			if id == "long" {
				return 4 * time.Second
			}
			return time.Second
		}
		plan := listsched.Plan[string, kind]{Order: graph, Precedes: precedes, Capacity: 2, Duration: duration, Less: byName}
		schedule, err := listsched.Simulate(plan)
		Expect(err).NotTo(HaveOccurred())
		Expect(schedule.Makespan).To(Equal(4 * time.Second))
		bound, err := listsched.LowerBound(plan)
		Expect(err).NotTo(HaveOccurred())
		Expect(bound).To(Equal(4 * time.Second))
		Expect(listsched.GrahamBound(2, 4*time.Second)).To(Equal(6 * time.Second))
	})
})
