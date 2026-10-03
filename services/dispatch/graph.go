// Copyright 2026 Candace Labs

package dispatch

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/io/inproc"
	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
)

// Relation is an edge kind of the slice graph; its spelling is the relation
// column of csf_slice_edges.
type Relation string

const (
	// RelationDependsOn is the one serial relation: directed and acyclic,
	// from the slice that must merge first to the slice that waits.
	RelationDependsOn Relation = "depends_on"
	// RelationContends is mutual exclusion: undirected; the two never run at
	// once.
	RelationContends Relation = "contends"
)

// relations is how the store treats each kind.
var relations = map[Relation]inproc.Relation{
	RelationDependsOn: {Directed: true, Acyclic: true},
	RelationContends:  {Directed: false},
}

// node is one slice as the store holds it: the served record and its
// enqueue sequence.
type node struct {
	proto    *dispatchv1.SliceNode
	sequence uint64
}

// graph is the in-process slice graph, owned by the service's one goroutine:
// the edges in one inproc.Graph store, the records beside it by identifier.
type graph struct {
	edges *inproc.Graph[string, Relation]
	nodes map[string]*node
	// intents is every declared intent by identifier, attached or not.
	intents map[string]*dispatchv1.Intent
	// running maps a session's assignment to the slice it runs.
	running  map[string]string
	sequence uint64
}

func newGraph() *graph {
	edges, err := inproc.NewGraph[string](relations)
	if err != nil {
		// The relations are a constant of this package.
		panic(err)
	}
	return &graph{
		edges:   edges,
		nodes:   map[string]*node{},
		intents: map[string]*dispatchv1.Intent{},
		running: map[string]string{},
	}
}

// add places a node with no declared edges; the contends edges its touch-set
// shares with every existing node are computed here.
func (store *graph) add(record *dispatchv1.SliceNode, sequence uint64) error {
	id := record.GetSlice().GetSliceId()
	others := store.edges.Nodes()
	if err := store.edges.AddNode(id); err != nil {
		return fmt.Errorf("%w: %s", ErrSliceExists, id)
	}
	store.nodes[id] = &node{proto: record, sequence: sequence}
	store.sequence = max(store.sequence, sequence)
	for _, other := range others {
		if overlaps(record.GetTouchSet(), store.nodes[other].proto.GetTouchSet()) {
			store.exclude(id, other)
		}
	}
	return nil
}

// order is every slice in enqueue order.
func (store *graph) order() []string { return store.edges.Nodes() }

// precede records that predecessor must merge before successor. It reports
// ErrCycle when the depends_on relation would stop being acyclic.
func (store *graph) precede(predecessor string, successor string) error {
	if err := store.edges.AddEdge(RelationDependsOn, predecessor, successor); err != nil {
		if errors.Is(err, inproc.ErrCycle) {
			return fmt.Errorf("%w: %w", ErrCycle, err)
		}
		return err
	}
	return nil
}

// exclude records that a and b contend.
func (store *graph) exclude(a string, b string) {
	// Both ends exist and a != b wherever this is called; the store has no
	// other refusal for an undirected kind.
	_ = store.edges.AddEdge(RelationContends, a, b)
}

// conflicts is the contends relation as list scheduling reads it.
func (store *graph) conflicts(a string, b string) bool {
	return store.edges.HasEdge(RelationContends, a, b)
}

// has reports whether id is a slice of the graph.
func (store *graph) has(id string) bool { return store.edges.Has(id) }

func (store *graph) state(id string) dispatchv1.SliceState {
	return store.nodes[id].proto.GetState()
}

func (store *graph) merged(id string) bool {
	return store.state(id) == dispatchv1.SliceState_SLICE_STATE_MERGED
}

// terminal reports a state no session will run again: out of the ranking.
func terminal(state dispatchv1.SliceState) bool {
	return state == dispatchv1.SliceState_SLICE_STATE_MERGED || state == dispatchv1.SliceState_SLICE_STATE_CANCELED
}

// occupied reports a state holding a session, so counting against the cap.
func occupied(state dispatchv1.SliceState) bool {
	return state == dispatchv1.SliceState_SLICE_STATE_RUNNING || state == dispatchv1.SliceState_SLICE_STATE_PREEMPTED
}

// ranks is the upward critical-path length of every unfinished slice, in
// slices: unit weights until durations have been measured.
func (store *graph) ranks() map[string]float64 {
	unit := func(id string) float64 { return 1 }
	skip := func(id string) bool { return terminal(store.state(id)) }
	return store.edges.UpwardRank(RelationDependsOn, unit, skip)
}

// urgency is the strongest urgency among the intents attached to id.
func (store *graph) urgency(id string) dispatchv1.Urgency {
	strongest := dispatchv1.Urgency_URGENCY_UNSPECIFIED
	for _, intent := range store.nodes[id].proto.GetIntents() {
		strongest = max(strongest, intent.GetUrgency())
	}
	return strongest
}

// less is the dispatch priority: longer critical path first, then stronger
// urgency, then earlier enqueue.
func (store *graph) less(ranks map[string]float64) func(a string, b string) bool {
	return func(a string, b string) bool {
		if ranks[a] != ranks[b] {
			return ranks[a] > ranks[b]
		}
		if urgencyA, urgencyB := store.urgency(a), store.urgency(b); urgencyA != urgencyB {
			return urgencyA > urgencyB
		}
		return store.nodes[a].sequence < store.nodes[b].sequence
	}
}

// frontier is every queued slice whose depends_on predecessors have merged,
// in dispatch order: a query over the store.
func (store *graph) frontier(ranks map[string]float64) []string {
	ready := store.edges.Frontier(RelationDependsOn, store.merged)
	ready = slices.DeleteFunc(ready, func(id string) bool { return store.state(id) != dispatchv1.SliceState_SLICE_STATE_QUEUED })
	less := store.less(ranks)
	sort.SliceStable(ready, func(i int, j int) bool { return less(ready[i], ready[j]) })
	return ready
}

// runningIDs is every slice holding a session, in enqueue order.
func (store *graph) runningIDs() []string {
	var running []string
	for _, id := range store.order() {
		if occupied(store.state(id)) {
			running = append(running, id)
		}
	}
	return running
}

// view is the served copy of a node with its priority breakdown.
func (store *graph) view(id string, ranks map[string]float64, frontier []string) *dispatchv1.SliceNode {
	record := store.nodes[id]
	served := proto.Clone(record.proto).(*dispatchv1.SliceNode)
	served.Priority = &dispatchv1.PriorityBreakdown{
		CriticalPath:  uint32(ranks[id]),
		Urgency:       store.urgency(id),
		DeclaredOrder: record.sequence,
	}
	if position := slices.Index(frontier, id); position >= 0 {
		served.Priority.Rank = uint32(position + 1)
	}
	return served
}

// views serves several nodes under one ranking.
func (store *graph) views(ids []string) []*dispatchv1.SliceNode {
	ranks := store.ranks()
	frontier := store.frontier(ranks)
	served := make([]*dispatchv1.SliceNode, 0, len(ids))
	for _, id := range ids {
		served = append(served, store.view(id, ranks, frontier))
	}
	return served
}

// attach puts intent on the slice, replacing an earlier copy of the same
// intent.
func (store *graph) attach(intent *dispatchv1.Intent, id string) {
	record := store.nodes[id].proto
	record.Intents = slices.DeleteFunc(record.Intents, func(existing *dispatchv1.Intent) bool {
		return existing.GetIntentId() == intent.GetIntentId()
	})
	record.Intents = append(record.Intents, proto.Clone(intent).(*dispatchv1.Intent))
}

// detach removes the intent from every slice and reports which held it.
func (store *graph) detach(intentID string) []string {
	var held []string
	for _, id := range store.order() {
		record := store.nodes[id].proto
		before := len(record.Intents)
		record.Intents = slices.DeleteFunc(record.Intents, func(existing *dispatchv1.Intent) bool {
			return existing.GetIntentId() == intentID
		})
		if len(record.Intents) != before {
			held = append(held, id)
		}
	}
	return held
}

// targets routes an intent: the slice its scope names, or every slice whose
// touch-set covers the most of its terms, earliest enqueued first. Merged
// slices are included, so a caller can answer already done.
func (store *graph) targets(intent *dispatchv1.Intent) []string {
	if scope := intent.GetScope(); store.has(scope) {
		return []string{scope}
	}
	best := 0
	var covering []string
	for _, id := range store.order() {
		score := coverage(store.nodes[id].proto.GetTouchSet(), intent.GetTerms())
		switch {
		case score == 0:
		case score > best:
			best, covering = score, []string{id}
		case score == best:
			covering = append(covering, id)
		}
	}
	return covering
}
