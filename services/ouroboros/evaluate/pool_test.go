// Copyright 2026 Candace Labs

package evaluate_test

import (
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/ouroboros/evaluate"
)

var _ = Describe("The pool and the hiding", func() {
	// corpus holds two runs of ticket 41 (the longer is the original), one of
	// ticket 42, one of a ticket never merged, and a stray file.
	corpus := fstest.MapFS{
		"run-a/recipe.json":  {Data: recipe(41)},
		"run-a/events.jsonl": {Data: eventLog(10)},
		"run-b/recipe.json":  {Data: recipe(41)},
		"run-b/events.jsonl": {Data: eventLog(30, 3)},
		"run-c/recipe.json":  {Data: recipe(42)},
		"run-c/events.jsonl": {Data: eventLog(5)},
		"run-d/recipe.json":  {Data: recipe(43)},
		"run-d/events.jsonl": {Data: eventLog(5)},
		"harness.json":       {Data: []byte("{}")},
	}
	pulls := []evaluate.MergedPull{
		{Number: 7, Title: "ONE (#41): first", MergeCommit: "m7", BaseCommit: "b7", Files: []string{"z.go", "a.go"}},
		{Number: 9, Title: "ONE (#41): follow-up", MergeCommit: "m9", BaseCommit: "b9", Files: []string{"c.go"}},
		{Number: 8, Title: "TWO (#42): second", MergeCommit: "m8", BaseCommit: "b8"},
		{Number: 10, Title: "no ticket named", MergeCommit: "m10", BaseCommit: "b10"},
	}

	It("holds each merged ticket once, by its last pull request, with its longest run as the original", func() {
		pool, err := evaluate.Pool(corpus, pulls)
		Expect(err).NotTo(HaveOccurred())
		Expect(pool).To(HaveLen(2))
		Expect(pool[0].Number).To(Equal(int64(41)))
		Expect(pool[0].PullRequest).To(Equal(int64(9)))
		Expect(pool[0].BaseCommit).To(Equal("b9"))
		Expect(pool[0].Assignment).To(Equal("run-b"))
		Expect(pool[0].ToolCalls).To(Equal(int64(30)))
		Expect(pool[0].Episodes).To(Equal(int64(1)))
		Expect(pool[1].Number).To(Equal(int64(42)))
	})

	It("reads the ticket a pull request title names", func() {
		Expect(evaluate.TicketOfTitle("EVAL-SUITE (#416): scored")).To(Equal(int64(416)))
		Expect(evaluate.TicketOfTitle("no ticket")).To(BeZero())
	})

	It("hides every run of a hidden ticket, originals and replays alike", func() {
		hidden, err := evaluate.HiddenAssignments(corpus, map[int64]bool{41: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(hidden).To(Equal(map[string]bool{"run-a": true, "run-b": true}))
	})

	It("reads no ticket off a run without a recipe", func() {
		number, err := evaluate.TicketOf(corpus, "run-missing")
		Expect(err).NotTo(HaveOccurred())
		Expect(number).To(BeZero())
	})
})
