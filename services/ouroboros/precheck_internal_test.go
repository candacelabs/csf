// Copyright 2026 Candace Labs

package ouroboros

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// proposalComment is the proposal the overnight loop posted on ticket #249,
// in the shape every mining ticket carries (the predicate and rules trimmed).
const proposalComment = "Miner proposal from the overnight ticket loop (one stateless Haiku call per batch; not backtested, not verified).\n\n" +
	"| field | value |\n|---|---|\n| miner | `no_self_merge` |\n| severity | S0 |\n| corpus | pull_request, jsonl |\n| needs a repo edit | True |\n\n" +
	"Facts:\n- `pr_id(P) from pull_request number`\n\n```datalog\nrefused(P) :- pr_author(P, S), merging_session(S), not(has_independent_verdict(P)).\n```\n\n" +
	"| labeled instance | sign | source |\n|---|---|---|\n| `orchestrator authored #226, #245; merged via merge-pr.sh` | + | ticket #249 finding section |\n| `6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60` | - | ticket #249 |\n\n" +
	"Status: proposal. FN/TP are unmeasured until the walk-forward backtest runs on the corpus (`services/ouroboros` contract)."

var _ = Describe("Reading a mining proposal", func() {
	It("reads the miner, the corpus kinds and every labeled instance", func() {
		proposal, ok := ParseProposal(Comment{ID: "IC_1", Body: proposalComment})
		Expect(ok).To(BeTrue())
		Expect(proposal.ID).To(Equal("IC_1"))
		Expect(proposal.Miner).To(Equal("no_self_merge"))
		Expect(proposal.Corpus).To(Equal([]string{"pull_request", "jsonl"}))
		Expect(proposal.Instances).To(Equal([]string{
			"orchestrator authored #226, #245; merged via merge-pr.sh",
			"6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60",
		}))
	})

	It("is not a proposal without the labeled-instance table", func() {
		_, ok := ParseProposal(Comment{ID: "IC_2", Body: "Claimed by CSF session 9027f6f1 (branch dev/x)."})
		Expect(ok).To(BeFalse())
	})

	It("takes the newest proposal of a ticket", func() {
		older := Comment{ID: "IC_old", Body: proposalComment}
		newer := Comment{ID: "IC_new", Body: proposalComment}
		proposal, ok := LatestProposal([]Comment{older, {ID: "IC_claim", Body: "Claimed."}, newer, {ID: "IC_after", Body: "later note"}})
		Expect(ok).To(BeTrue())
		Expect(proposal.ID).To(Equal("IC_new"))
	})
})

var _ = Describe("Reading the references a labeled instance names", func() {
	It("reads pull request and issue numbers out of prose", func() {
		Expect(References("orchestrator authored #226, #245; merged via merge-pr.sh")).To(Equal([]Reference{
			{Kind: InstanceIssue, Value: "226"}, {Kind: InstanceIssue, Value: "245"},
		}))
	})

	It("reads an assignment identifier as a run and nothing else", func() {
		Expect(References("…6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60 (COPILOT-EXEC)")).To(Equal([]Reference{
			{Kind: InstanceRun, Value: "6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60"},
		}))
	})

	It("reads a bare number as a reference and a hex revision as a commit", func() {
		Expect(References("330")).To(Equal([]Reference{{Kind: InstanceIssue, Value: "330"}}))
		Expect(References("5e5955aa2d8a")).To(Equal([]Reference{{Kind: InstanceCommit, Value: "5e5955aa2d8a"}}))
	})

	It("reads nothing from a synthetic label", func() {
		Expect(References("test-drain-ok-001")).To(BeEmpty())
		Expect(References("day with struggle rate growth 1.019× over baseline")).To(BeEmpty())
	})
})
