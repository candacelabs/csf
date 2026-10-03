// Copyright 2026 Candace Labs

package dispatch_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/inproc"
	"github.com/candacelabs/csf/pkg/listsched"
	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
	"github.com/candacelabs/csf/services/dispatch"
)

// The proof fixture: eight slices, two chains that join, two independents,
// and contends edges from shared hotspots. Capacity 3.
//
//	ontology ─┐
//	          ├─> compiler ─> checks ─┐
//	schema ───┘                      ├─> release
//	queries ─────> store ────────────┘
//	docs, kit: independent
//
// Hotspots: ontology and compiler both touch architecture.csf; schema and
// queries both touch 001_init.sql; docs and kit both touch README.md.
type fixtureSlice struct {
	id       string
	deps     []string
	hotspots []string
	duration time.Duration
}

var fixture = []fixtureSlice{
	{id: "ontology", hotspots: []string{"architecture.csf"}, duration: 2 * time.Second},
	{id: "schema", hotspots: []string{"001_init.sql"}, duration: 1 * time.Second},
	{id: "queries", hotspots: []string{"001_init.sql"}, duration: 2 * time.Second},
	{id: "compiler", deps: []string{"ontology", "schema"}, hotspots: []string{"architecture.csf"}, duration: 1 * time.Second},
	{id: "store", deps: []string{"queries"}, hotspots: []string{"csfpg"}, duration: 2 * time.Second},
	{id: "checks", deps: []string{"compiler"}, hotspots: []string{"house_lint"}, duration: 1 * time.Second},
	{id: "docs", hotspots: []string{"README.md"}, duration: 1 * time.Second},
	{id: "kit", hotspots: []string{"README.md"}, duration: 3 * time.Second},
	{id: "release", deps: []string{"checks", "store"}, hotspots: []string{"Candacefile"}, duration: 1 * time.Second},
}

const proofCapacity = 3

// fixturePlan is the list-scheduling plan of the fixture, ranked the way the
// service ranks: upward critical path (unit weights), then enqueue order.
func fixturePlan() listsched.Plan[string, dispatch.Relation] {
	order, err := inproc.NewGraph[string](map[dispatch.Relation]inproc.Relation{dispatch.RelationDependsOn: {Directed: true, Acyclic: true}})
	Expect(err).NotTo(HaveOccurred())
	index := map[string]int{}
	hotspots := map[string][]string{}
	durations := map[string]time.Duration{}
	for position, slice := range fixture {
		Expect(order.AddNode(slice.id)).To(Succeed())
		index[slice.id] = position
		hotspots[slice.id] = slice.hotspots
		durations[slice.id] = slice.duration
	}
	for _, slice := range fixture {
		for _, dep := range slice.deps {
			Expect(order.AddEdge(dispatch.RelationDependsOn, dep, slice.id)).To(Succeed())
		}
	}
	ranks := order.UpwardRank(dispatch.RelationDependsOn, func(id string) float64 { return 1 }, nil)
	return listsched.Plan[string, dispatch.Relation]{
		Order:    order,
		Precedes: dispatch.RelationDependsOn,
		Duration: func(id string) time.Duration { return durations[id] },
		Conflicts: func(a string, b string) bool {
			for _, hotspot := range hotspots[a] {
				if slices.Contains(hotspots[b], hotspot) {
					return true
				}
			}
			return false
		},
		Capacity: proofCapacity,
		Less: func(a string, b string) bool {
			if ranks[a] != ranks[b] {
				return ranks[a] > ranks[b]
			}
			return index[a] < index[b]
		},
	}
}

// render draws a schedule as one row per slice over time, one column per
// second.
func render(schedule listsched.Schedule[string]) string {
	var out strings.Builder
	width := int(schedule.Makespan / time.Second)
	fmt.Fprintf(&out, "%-10s", "slice")
	for second := range width {
		fmt.Fprintf(&out, "%2d", second)
	}
	out.WriteString("\n")
	for _, interval := range schedule.Intervals {
		fmt.Fprintf(&out, "%-10s", interval.Task)
		for second := range width {
			at := time.Duration(second) * time.Second
			if at >= interval.Start && at < interval.End {
				out.WriteString(" #")
			} else {
				out.WriteString(" .")
			}
		}
		out.WriteString("\n")
	}
	fmt.Fprintf(&out, "%-10s", "running")
	for second := range width {
		at := time.Duration(second) * time.Second
		running := 0
		for _, interval := range schedule.Intervals {
			if at >= interval.Start && at < interval.End {
				running++
			}
		}
		fmt.Fprintf(&out, "%2d", running)
	}
	out.WriteString("\n")
	return out.String()
}

var _ = Describe("proof: the fixture slice graph", func() {
	var (
		ctx        context.Context
		controller *gomock.Controller
		harness    *harnessDouble
	)

	BeforeEach(func() {
		ctx = context.Background()
		controller = gomock.NewController(GinkgoT())
		harness = newHarnessDouble(controller)
		harness.workerCap = proofCapacity
	})

	It("is dispatched in the list-scheduling order and within the Graham bound of the optimum", func() {
		plan := fixturePlan()
		predicted, err := listsched.Simulate(plan)
		Expect(err).NotTo(HaveOccurred())
		lower, err := listsched.LowerBound(plan)
		Expect(err).NotTo(HaveOccurred())
		bound := listsched.GrahamBound(proofCapacity, lower)

		// The exact optimum over every priority order: with nine slices,
		// 9! list schedules, each a few hundred operations.
		best := predicted.Makespan
		ids := plan.Order.Nodes()
		permute(ids, func(priority []string) {
			position := map[string]int{}
			for index, id := range priority {
				position[id] = index
			}
			candidate := plan
			candidate.Less = func(a string, b string) bool { return position[a] < position[b] }
			schedule, err := listsched.Simulate(candidate)
			Expect(err).NotTo(HaveOccurred())
			best = min(best, schedule.Makespan)
		})

		fmt.Fprintf(GinkgoWriter, "\nPredicted list schedule (capacity %d):\n%s", proofCapacity, render(predicted))
		fmt.Fprintf(GinkgoWriter, "makespan %s; lower bound %s; Graham bound (2-1/m)*LB %s; best list schedule over all %d orders %s; peak concurrency %d\n",
			predicted.Makespan, lower, bound, factorial(len(ids)), best, predicted.Peak)
		Expect(predicted.Makespan).To(BeNumerically("<=", bound), "within the Graham bound of the optimum")
		Expect(predicted.Makespan).To(Equal(best), "the critical-path order is as good as any order on this graph")
		Expect(predicted.Peak).To(BeNumerically("<=", proofCapacity))

		// Now the service, driven through the same completions: at every
		// predicted end time the slices ending then are merged, and the
		// sessions the service submits are compared with the prediction.
		service, _ := started(ctx, dispatch.WithSessions(harness.sessions), dispatch.WithHostMeasures(measuresFor(controller, proofCapacity+1, 1)), dispatch.WithClock(fakeClock{}))
		for _, slice := range fixture {
			_, err := service.Enqueue(ctx, sliceRequest(slice.id, dependsOn(slice.deps...), slice.hotspots...))
			Expect(err).NotTo(HaveOccurred())
		}
		var actual []string
		var concurrency []int
		startedAt := map[string]time.Duration{}
		endsAt := map[string]time.Duration{}
		for _, interval := range predicted.Intervals {
			startedAt[interval.Task] = interval.Start
			endsAt[interval.Task] = interval.End
		}
		var instants []time.Duration
		for _, sample := range predicted.Concurrency {
			instants = append(instants, sample.At)
		}
		for _, now := range instants {
			var expected []string
			for _, interval := range predicted.Intervals {
				if interval.Start == now {
					expected = append(expected, assignmentOf(interval.Task))
				}
			}
			if len(expected) > 0 {
				submitted := harness.submittedSet(len(expected))
				Expect(submitted).To(ConsistOf(expected), "at %s the service starts what the prediction starts", now)
				for _, assignment := range submitted {
					for _, slice := range fixture {
						if assignmentOf(slice.id) == assignment {
							actual = append(actual, slice.id)
						}
					}
				}
			}
			harness.nothingSubmitted()
			listed, err := service.List(ctx, &dispatchv1.ListSlicesRequest{})
			Expect(err).NotTo(HaveOccurred())
			concurrency = append(concurrency, int(listed.GetRunning()))
			for _, interval := range predicted.Intervals {
				if interval.End == now+step(instants, now) {
					_, err := service.MarkMerged(ctx, &dispatchv1.MarkSliceMergedRequest{SliceId: interval.Task})
					Expect(err).NotTo(HaveOccurred())
				}
			}
		}
		fmt.Fprintf(GinkgoWriter, "service dispatched: %s\nconcurrency at each instant %v: %v\n", strings.Join(actual, " "), instants, concurrency)
		Expect(byStart(actual, predicted)).To(BeTrue(), "the service's dispatch order is the predicted order up to ties within one instant")
		var predictedConcurrency []int
		for _, sample := range predicted.Concurrency {
			predictedConcurrency = append(predictedConcurrency, sample.Running)
		}
		Expect(concurrency).To(Equal(predictedConcurrency))
		Expect(slices.Max(concurrency)).To(BeNumerically("<=", proofCapacity))
	})
})

// step is the time from now to the next instant.
func step(instants []time.Duration, now time.Duration) time.Duration {
	for _, instant := range instants {
		if instant > now {
			return instant - now
		}
	}
	return 0
}

// byStart reports whether actual lists the predicted tasks in start order,
// allowing any order among tasks that start at the same instant.
func byStart(actual []string, predicted listsched.Schedule[string]) bool {
	if len(actual) != len(predicted.Intervals) {
		return false
	}
	position := 0
	for position < len(actual) {
		start := predicted.Intervals[position].Start
		var batch []string
		for _, interval := range predicted.Intervals {
			if interval.Start == start {
				batch = append(batch, interval.Task)
			}
		}
		slot := slices.Clone(actual[position : position+len(batch)])
		slices.Sort(slot)
		slices.Sort(batch)
		if !slices.Equal(slot, batch) {
			return false
		}
		position += len(batch)
	}
	return true
}

func factorial(n int) int {
	result := 1
	for i := 2; i <= n; i++ {
		result *= i
	}
	return result
}

// permute calls visit with every ordering of ids (Heap's algorithm).
func permute(ids []string, visit func(order []string)) {
	order := slices.Clone(ids)
	counters := make([]int, len(order))
	visit(order)
	for i := 0; i < len(order); {
		if counters[i] < i {
			if i%2 == 0 {
				order[0], order[i] = order[i], order[0]
			} else {
				order[counters[i]], order[i] = order[i], order[counters[i]]
			}
			visit(order)
			counters[i]++
			i = 0
		} else {
			counters[i] = 0
			i++
		}
	}
}
