// Copyright 2026 Candace Labs

package dispatch

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
)

func touch(hotspots []string, paths ...string) *dispatchv1.TouchSet {
	return &dispatchv1.TouchSet{Hotspots: hotspots, Paths: paths}
}

var _ = Describe("touch-set overlap", func() {
	It("contends on a shared hotspot, an equal or matching path, or a path under a glob", func() {
		Expect(overlaps(touch([]string{"schema"}), touch([]string{"schema"}))).To(BeTrue())
		Expect(overlaps(touch([]string{"schema"}), touch([]string{"ontology"}))).To(BeFalse())
		Expect(overlaps(touch(nil, "services/harness/sessions.go"), touch(nil, "services/harness/sessions.go"))).To(BeTrue())
		Expect(overlaps(touch(nil, "services/harness/*.go"), touch(nil, "services/harness/sessions.go"))).To(BeTrue())
		Expect(overlaps(touch(nil, "services/harness/**"), touch(nil, "services/harness/chat/chat.go"))).To(BeTrue())
		Expect(overlaps(touch(nil, "services/harness/**"), touch(nil, "services/dispatch/dispatch.go"))).To(BeFalse())
		Expect(overlaps(touch(nil, "services/harness"), touch(nil, "services/harnesses/x.go"))).To(BeFalse(), "a prefix is not a directory")
	})

	It("counts the terms a touch-set covers, case-insensitively", func() {
		set := touch([]string{"architecture.csf"}, "services/dispatch/**")
		Expect(coverage(set, []string{"dispatch", "Architecture.csf", "warden", " "})).To(Equal(2))
		Expect(coverage(nil, []string{"dispatch"})).To(BeZero())
	})
})

var _ = Describe("ontology terms", func() {
	It("reads term identifiers from an architecture.csf and reports the missing ones", func() {
		source := filepath.Join(GinkgoT().TempDir(), "architecture.csf")
		Expect(os.WriteFile(source, []byte("# comment\nterm dispatch \"Dispatch\" \"...\";\nterm intent \"Intent\" \"...\"\n  forms \"intents\";\nretired engine \"Engine\" \"...\";\n"), 0o600)).To(Succeed())
		terms, err := ontologyTerms(source)
		Expect(err).NotTo(HaveOccurred())
		Expect(terms).To(Equal(map[string]bool{"dispatch": true, "intent": true}))
		Expect(missingTerms(terms, []string{"dispatch", "frontier", "intent"})).To(Equal([]string{"frontier"}))
		_, err = ontologyTerms(filepath.Join(GinkgoT().TempDir(), "absent.csf"))
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("the in-process graph", func() {
	enqueue := func(store *graph, id string, hotspots ...string) {
		record := &dispatchv1.SliceNode{
			Slice: &dispatchv1.Slice{SliceId: id}, Edges: &dispatchv1.SliceEdges{}, TouchSet: touch(hotspots),
			State: dispatchv1.SliceState_SLICE_STATE_QUEUED,
		}
		Expect(store.add(record, store.sequence+1)).To(Succeed())
	}

	It("ranks by critical path, then urgency, then enqueue order, and derives contends from touch-sets", func() {
		store := newGraph()
		enqueue(store, "a", "schema")
		enqueue(store, "b", "schema")
		enqueue(store, "c")
		enqueue(store, "d")
		Expect(store.precede("a", "c")).To(Succeed())
		Expect(store.precede("c", "d")).To(Succeed())
		Expect(store.precede("d", "a")).To(MatchError(ErrCycle))
		Expect(store.conflicts("a", "b")).To(BeTrue())
		Expect(store.conflicts("b", "a")).To(BeTrue())
		Expect(store.conflicts("a", "c")).To(BeFalse())

		ranks := store.ranks()
		Expect(ranks).To(Equal(map[string]float64{"a": 3, "b": 1, "c": 2, "d": 1}))
		Expect(store.frontier(ranks)).To(Equal([]string{"a", "b"}), "c and d wait for a")
		store.attach(&dispatchv1.Intent{IntentId: "hurry", Urgency: dispatchv1.Urgency_URGENCY_URGENT}, "b")
		Expect(store.frontier(ranks)).To(Equal([]string{"a", "b"}), "critical path outranks urgency")
		store.nodes["a"].proto.State = dispatchv1.SliceState_SLICE_STATE_MERGED
		ranks = store.ranks()
		Expect(ranks).NotTo(HaveKey("a"))
		Expect(store.frontier(ranks)).To(Equal([]string{"c", "b"}), "c has the longer path; b and d tie and b is urgent")
		view := store.view("b", ranks, store.frontier(ranks))
		Expect(view.GetPriority().GetRank()).To(Equal(uint32(2)))
		Expect(view.GetPriority().GetUrgency()).To(Equal(dispatchv1.Urgency_URGENCY_URGENT))
		Expect(view.GetPriority().GetCriticalPath()).To(Equal(uint32(1)))
		Expect(store.detach("hurry")).To(Equal([]string{"b"}))
		Expect(store.urgency("b")).To(Equal(dispatchv1.Urgency_URGENCY_UNSPECIFIED))
	})

	It("routes an intent by scope, then by the best term coverage, earliest first", func() {
		store := newGraph()
		enqueue(store, "schema-slice", "schema", "csfpg")
		enqueue(store, "ontology-slice", "architecture.csf")
		enqueue(store, "both", "schema", "architecture.csf")
		Expect(store.targets(&dispatchv1.Intent{Scope: "both"})).To(Equal([]string{"both"}))
		Expect(store.targets(&dispatchv1.Intent{Terms: []string{"schema", "csfpg"}})).To(Equal([]string{"schema-slice"}))
		Expect(store.targets(&dispatchv1.Intent{Terms: []string{"schema"}})).To(Equal([]string{"schema-slice", "both"}), "ties keep enqueue order")
		Expect(store.targets(&dispatchv1.Intent{Terms: []string{"warden"}})).To(BeEmpty())
	})
})
