// Copyright 2026 Candace Labs

package labeler

import (
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/net/model/ollama"
	ouroborosv1 "github.com/candacelabs/csf/proto/candace/ouroboros/v1"
)

// Unit specs over the prefilter, the derivations and the acceptance rule's
// two judgments. The proposal text is ticket #105's, as the overnight loop
// posted it.
const proposalComment = "Miner proposal from the overnight ticket loop.\n\n| field | value |\n|---|---|\n| miner | `pgrep_self_match_escape` |\n| severity | S0 |\n| corpus | jsonl |\n\nPredicate: $x$\n\nFacts:\n- `gated(R) from session_gate_decision event type`\n- `until_loop(R) from tool_call matching 'until !' pattern`\n- `pgrep_command(R) from command containing 'pgrep'`\n- `self_matches_session(R) from command containing own session_id in pattern`\n\n```datalog\nescape(R) :- until_loop(R).\n```\n"

var _ = Describe("Parsing a mining ticket", func() {
	It("reads the proposal's miner, corpus and fact lines and the body's instances", func() {
		request := ParseTicket("example/repo", 105, "S0 GATE ESCAPE", "The run 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60 looped on `until ! pgrep`.",
			[]TicketComment{{ID: "IC_1", Body: "unrelated comment"}, {ID: "IC_2", Body: proposalComment}})
		Expect(request.GetProposal()).To(Equal("IC_2"))
		Expect(request.GetMiner()).To(Equal("pgrep_self_match_escape"))
		Expect(request.GetCorpus()).To(Equal([]string{"jsonl"}))
		Expect(request.GetFacts()).To(Equal([]string{
			"gated(R) from session_gate_decision event type",
			"until_loop(R) from tool_call matching 'until !' pattern",
			"pgrep_command(R) from command containing 'pgrep'",
			"self_matches_session(R) from command containing own session_id in pattern",
		}))
		Expect(request.GetInstances()).To(Equal([]string{"6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60"}))
		Expect(request.GetPredicate()).To(HavePrefix("S0 GATE ESCAPE\nThe run"))
	})

	It("extracts the corpus vocabulary, never the relation names", func() {
		request := ParseTicket("example/repo", 105, "S0 GATE ESCAPE", "looped on `until ! pgrep`", []TicketComment{{ID: "IC_2", Body: proposalComment}})
		var spelled []string
		for _, searched := range extractTerms(request) {
			spelled = append(spelled, searched.String())
		}
		Expect(spelled).To(Equal([]string{"session_gate_decision", "until !", "tool_call", "pgrep", "session_id", "until ! pgrep"}))
	})

	It("compiles a quoted regular expression and keeps a literal literal", func() {
		request := &ouroborosv1.LabelRequest{Facts: []string{"timeout_wraps_events(R) from tool_call command matching 'timeout [0-9]+ (harness|csf) events'"}}
		terms := extractTerms(request)
		Expect(terms[0].pattern).NotTo(BeNil())
		Expect(terms[0].matches("", "ran timeout 1800 harness events -assignment x")).To(BeTrue())
		Expect(terms[0].matches("", "ran timeout harness events")).To(BeFalse())
		Expect(terms[1].literal).To(Equal("tool_call"))
	})
})

var _ = Describe("Ranking candidates", func() {
	It("weights rare terms over common ones and interleaves instances, named ones first", func() {
		terms := []term{{literal: "common"}, {literal: "rare"}}
		scan := newCorpusScan(terms)
		scan.read("a", "a/events.jsonl", 1, "common line", "x")
		scan.read("a", "a/events.jsonl", 2, "common and rare line", "x")
		scan.read("b", "b/events.jsonl", 1, "common line", "x")
		scan.read("c", "c/events.jsonl", 1, "nothing here", "x")
		scan.read("c", "c/events.jsonl", 2, "common line", "x")
		ranked := scan.ranked([]string{"c"})
		var order []string
		for _, found := range ranked {
			order = append(order, found.source+":"+strings.Repeat("", 0)+string(rune('0'+found.line)))
		}
		Expect(order).To(Equal([]string{"c/events.jsonl:2", "a/events.jsonl:2", "b/events.jsonl:1", "a/events.jsonl:1"}))
		Expect(ranked[1].score).To(BeNumerically(">", ranked[2].score))
	})

	It("centres the excerpt on the rarest matched term, under the event header", func() {
		terms := []term{{literal: "session_id"}, {literal: "needle"}}
		scan := newCorpusScan(terms)
		text := `{"time":"2026-10-02T02:48:40Z","event_type":"session_gate_decision","msg":"session gate decision","session_id":"t","command":"` + strings.Repeat("x", 500) + `needle` + strings.Repeat("y", 500) + `"}`
		scan.read("a", "a/events.jsonl", 1, text, "session_gate_decision")
		scan.read("a", "a/events.jsonl", 2, `{"session_id":"t"}`, "x")
		found := scan.ranked(nil)[0]
		Expect(found.focus).To(Equal(strings.Index(text, "needle")))
		excerpt := found.excerpt(100)
		Expect(excerpt).To(HavePrefix("2026-10-02T02:48:40Z session_gate_decision session gate decision\n…"))
		Expect(excerpt).To(ContainSubstring("needle"))
		Expect(excerpt).To(HaveSuffix("…"))
		Expect(len(excerpt)).To(BeNumerically("<", 200))
	})
})

var _ = Describe("The derivations", func() {
	It("sizes a batch from the context window, the fixed prompt and the measured per-item costs", func() {
		// 16384 less 3000/2.1 and the envelope, over 265 + 57 per candidate.
		Expect(batchSize(16384, 2.1, 3000, 265, 57)).To(Equal(46))
		Expect(batchSize(16384, 2.1, 3000, 265, 57)).To(BeNumerically(">", batchSize(16384, 2.1, 9000, 265, 57)))
		Expect(batchSize(16384, 2.1, 3000, 222, 57)).To(BeNumerically(">", batchSize(16384, 2.1, 3000, 265, 57)))
		Expect(batchSize(16384, 2.1, 3000, 265, 39)).To(BeNumerically(">", batchSize(16384, 2.1, 3000, 265, 57)))
		Expect(batchSize(answerTokens+1, 2.1, 100000, 265, 57)).To(Equal(1))
	})

	It("keeps the running maximum of the measured per-item costs", func() {
		subject := &Labeler{measured: measurements{charsPerToken: initialCharsPerToken, tokensPerCandidate: 100, answerPerLabel: 10}}
		decision := &ollama.Prompt{System: strings.Repeat("s", 1000), User: strings.Repeat("u", 9000)}
		subject.measure(decision, ollama.Usage{PromptTokens: 5000, Tokens: 1200}, 20, 20)
		Expect(subject.measured.charsPerToken).To(Equal(2.0))
		Expect(subject.measured.tokensPerCandidate).To(BeNumerically("~", (5000-float64(1000+len(answerSchema))/2.0)/20, 0.01))
		Expect(subject.measured.answerPerLabel).To(Equal(60.0))
		subject.measure(decision, ollama.Usage{PromptTokens: 2500, Tokens: 100}, 20, 20)
		Expect(subject.measured.answerPerLabel).To(Equal(60.0), "a cheaper batch does not lower the maximum")
	})

	It("derives the keep-alive from the batch gaps and the load", func() {
		Expect(keepAliveFor(nil, 0)).To(BeZero())
		Expect(keepAliveFor(nil, 5*time.Second)).To(Equal(10 * time.Second))
		Expect(keepAliveFor([]time.Duration{10 * time.Second, 30 * time.Second, 20 * time.Second}, 5*time.Second)).To(Equal(40 * time.Second))
		Expect(keepAliveFor([]time.Duration{time.Second}, 5*time.Second)).To(Equal(10 * time.Second))
	})

	It("reads a container's creation for the GPU", func() {
		Expect(holdsGPU(nil)).To(BeFalse())
		Expect(holdsGPU(&container.HostConfig{})).To(BeFalse())
		Expect(holdsGPU(&container.HostConfig{Runtime: "nvidia"})).To(BeTrue())
		Expect(holdsGPU(&container.HostConfig{Resources: container.Resources{DeviceRequests: []container.DeviceRequest{{Capabilities: [][]string{{"gpu"}}}}}})).To(BeTrue())
		Expect(holdsGPU(&container.HostConfig{Resources: container.Resources{DeviceRequests: []container.DeviceRequest{{Driver: "nvidia", DeviceIDs: []string{"GPU-1"}}}}})).To(BeTrue())
	})
})

var _ = Describe("The acceptance rule", func() {
	It("keeps a positive whose quote is verbatim on the line and rejects the rest", func() {
		acceptance, rejection := judgeSpan("until ! pgrep -f x; do sleep 1; done", "until ! pgrep")
		Expect(acceptance).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_REPRODUCED_SPAN))
		Expect(rejection).To(BeEmpty())
		acceptance, rejection = judgeSpan("git push", "until ! pgrep")
		Expect(acceptance).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_REJECTED))
		Expect(rejection).To(Equal(rejectionQuoteAbsent))
		acceptance, rejection = judgeSpan("git push", "")
		Expect(acceptance).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_REJECTED))
		Expect(rejection).To(Equal(rejectionEmptyQuote))
	})

	It("strips the excerpt's own cut marks and whitespace from a quote", func() {
		Expect(quoted("…pgrep -f abc …")).To(Equal("pgrep -f abc"))
		Expect(quoted("  ")).To(BeEmpty())
		Expect(quoted("tool_us…")).To(Equal("tool_us"))
	})

	It("reads a quote across the cut mark as fragments in order, and a JSON line decoded", func() {
		line := `{"command":"until ! pgrep -f 'bazel.*build' > /dev/null; do sleep 2; done && echo \"Build complete\"","body":"stop admitting\n- interrupt turns"}`
		accepted := func(quote string) ouroborosv1.Acceptance {
			acceptance, _ := judgeSpan(line, quote)
			return acceptance
		}
		Expect(accepted("until ! pgrep … do sleep 2; done")).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_REPRODUCED_SPAN))
		Expect(accepted("do sleep 2 … until ! pgrep")).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_REJECTED), "fragments out of order")
		Expect(accepted(`done && echo "Build complete"`)).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_REPRODUCED_SPAN), "the JSON escapes decoded")
		Expect(accepted("stop admitting\n- interrupt turns")).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_REPRODUCED_SPAN))
		Expect(accepted("timeout 1800 harness events … REDEPLOY DUE")).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_REJECTED), "a quote from the ticket, not the line")
	})

	It("keeps a positive the miner extracts", func() {
		acceptance, _ := judgeFact(map[string]bool{"a": true}, "a")
		Expect(acceptance).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_REPRODUCED_FACT))
		acceptance, rejection := judgeFact(map[string]bool{"a": true}, "b")
		Expect(acceptance).To(Equal(ouroborosv1.Acceptance_ACCEPTANCE_REJECTED))
		Expect(rejection).To(Equal(rejectionNotExtracted))
	})

	It("refuses an answer that is not the labels object", func() {
		_, err := parseAnswer("not json")
		Expect(err).To(MatchError(ErrMalformedAnswer))
		labels, err := parseAnswer(`{"labels":[{"candidate":1,"label":"+","quote":"q","reason":"r"}]}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(labels).To(Equal([]answerLabel{{Candidate: 1, Label: "+", Quote: "q", Reason: "r"}}))
	})
})
