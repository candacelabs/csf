// Copyright 2026 Candace Labs

package codes_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/ollama"
	"github.com/candacelabs/csf/io/net/model/stub"
	"github.com/candacelabs/csf/services/ouroboros/codes"
)

func cannedCoder(content string, options ...codes.ComplaintCoderOption) *codes.ComplaintCoder {
	brain := stub.NewCannedBrain[*ollama.Prompt, ollama.Answer](ollama.Answer{Content: content})
	coder, err := codes.NewComplaintCoder(append([]codes.ComplaintCoderOption{codes.WithBrain(brain)}, options...)...)
	Expect(err).NotTo(HaveOccurred())
	return coder
}

var sent = time.Date(2026, 10, 5, 2, 19, 0, 0, time.UTC)

var complaints = []codes.Complaint{
	{Source: "s", Ref: "a", At: sent, Text: "you said it passed but you never ran the tests"},
	{Source: "s", Ref: "b", At: sent, Text: "wait what you wrote this in python"},
	{Source: "s", Ref: "c", At: sent, Text: "no that's wrong"},
	{Source: "s", Ref: "d", At: sent, Text: "stop"},
}

const answer = `{"codes": [
 {"index": 1, "code": "mast_no_verification", "quote": "never ran  the TESTS", "name": "", "definition": "", "edge_from": "agent", "edge_to": "session_gate", "side": "harness"},
 {"index": 2, "code": "none", "quote": "wrote this in python", "name": "Bypass CSF", "definition": "The agent works around CSF's own tools.", "edge_from": "agent", "edge_to": "harness", "side": "harness"},
 {"index": 3, "code": "mast_task_derailment", "quote": "that is not what I said", "name": "", "definition": "", "edge_from": "agent", "edge_to": "assignment", "side": "model"}
]}`

var _ = Describe("The catalogue", func() {
	It("seeds MAST's 14 modes, each with an edge between two known components and a routed side", func() {
		Expect(codes.Seed()).To(HaveLen(14))
		for _, code := range codes.Catalogue {
			Expect(codes.Components).To(HaveKey(code.Edge.From), code.ID)
			Expect(codes.Components).To(HaveKey(code.Edge.To), code.ID)
			Expect(code.Side.Route()).NotTo(BeEmpty(), code.ID)
		}
		for _, code := range codes.Seed() {
			Expect(code.MAST).To(MatchRegexp(`^FM-[1-3]\.[1-6]$`), code.ID)
		}
		found, ok := codes.Lookup("mast_no_verification")
		Expect(ok).To(BeTrue())
		Expect(found.MAST).To(Equal("FM-3.2"))
		_, ok = codes.Lookup("absent")
		Expect(ok).To(BeFalse())
	})

	It("routes each side to its repair", func() {
		Expect(codes.SideHarness.Route()).To(Equal("a session gate or a miner"))
		Expect(codes.SideProvider.Route()).To(Equal("capacity, not a gate"))
		Expect(codes.Side("user").Route()).To(BeEmpty())
	})
})

var _ = Describe("ComplaintCoder", func() {
	It("keeps a code whose quote is in the complaint, a proposal when none fits, and rejects the rest", func() {
		coded, err := cannedCoder(answer).Code(context.Background(), complaints)
		Expect(err).NotTo(HaveOccurred())
		Expect(coded).To(HaveLen(4))
		Expect(coded[0].Code).To(Equal("mast_no_verification"), "case and runs of whitespace are folded")
		Expect(coded[0].Rejected).To(BeEmpty())
		Expect(coded[1].Code).To(BeEmpty())
		Expect(coded[1].Proposal).To(Equal(&codes.Proposal{Name: "Bypass CSF", Definition: "The agent works around CSF's own tools.",
			Edge: codes.Edge{From: "agent", To: "harness"}, Side: codes.SideHarness}))
		Expect(coded[2].Rejected).To(Equal(codes.RejectedNoQuote))
		Expect(coded[3].Rejected).To(Equal(codes.RejectedUnanswered))
	})

	It("attaches each answer to the complaint its quote is in when the model numbers from 0", func() {
		fromZero := `{"codes": [
 {"index": 0, "code": "mast_no_verification", "quote": "never ran the tests", "name": "", "definition": "", "edge_from": "agent", "edge_to": "session_gate", "side": "harness"},
 {"index": 1, "code": "mast_disobey_role", "quote": "wrote this in python", "name": "", "definition": "", "edge_from": "harness", "edge_to": "agent", "side": "model"}
]}`
		coded, err := cannedCoder(fromZero).Code(context.Background(), complaints[:2])
		Expect(err).NotTo(HaveOccurred())
		Expect([]string{coded[0].Code, coded[1].Code}).To(Equal([]string{"mast_no_verification", "mast_disobey_role"}))
	})

	It("rejects a code the catalogue does not hold", func() {
		coded, err := cannedCoder(answer, codes.WithCatalogue(codes.Seed()[:1])).Code(context.Background(), complaints[:1])
		Expect(err).NotTo(HaveOccurred())
		Expect(coded[0].Rejected).To(Equal(codes.RejectedUnknownCode))
	})

	It("asks once per batch", func() {
		coded, err := cannedCoder(answer, codes.WithBatch(2)).Code(context.Background(), complaints)
		Expect(err).NotTo(HaveOccurred())
		Expect(coded).To(HaveLen(4))
		Expect(coded[0].Code).To(Equal("mast_no_verification"))
		Expect(coded[2].Rejected).To(Equal(codes.RejectedNoQuote), "the second batch's first complaint is index 1 again, and its quote is not in it")
		Expect(coded[2].Code).To(BeEmpty())
	})

	It("reports a model that answers nothing or answers malformed JSON", func() {
		empty, err := codes.NewComplaintCoder(codes.WithBrain(stub.NewCannedBrain[*ollama.Prompt, ollama.Answer]()))
		Expect(err).NotTo(HaveOccurred())
		_, err = empty.Code(context.Background(), complaints)
		Expect(err).To(MatchError(model.ErrNoProposal))
		_, err = cannedCoder("not json").Code(context.Background(), complaints)
		Expect(err).To(MatchError(ContainSubstring("decode the model's answer")))
	})

	It("requires a brain and refuses options it cannot use", func() {
		_, err := codes.NewComplaintCoder()
		Expect(err).To(MatchError(codes.ErrNoBrain))
		_, err = codes.NewComplaintCoder(nil)
		Expect(err).To(MatchError(codes.ErrInvalidOption))
		_, err = codes.NewComplaintCoder(codes.WithBatch(0))
		Expect(err).To(MatchError(codes.ErrInvalidOption))
		_, err = codes.NewComplaintCoder(codes.WithCatalogue(nil))
		Expect(err).To(MatchError(codes.ErrInvalidOption))
	})
})

var _ = Describe("Induce", func() {
	It("reads each induced code's members from its quotes, keeping a code whose quotes reproduce nowhere", func() {
		induced := `{"codes": [
 {"name": "BYPASS-CSF", "definition": "The agent works around CSF.", "edge_from": "agent", "edge_to": "harness", "side": "harness", "quotes": ["wrote this in PYTHON", "operator: never ran the tests"]},
 {"name": "NOTHING", "definition": "Covers nothing.", "edge_from": "agent", "edge_to": "human", "side": "model", "quotes": ["not in any complaint"]}
]}`
		codesInduced, err := cannedCoder(induced).Induce(context.Background(), complaints)
		Expect(err).NotTo(HaveOccurred())
		Expect(codesInduced).To(HaveLen(2))
		Expect(codesInduced[0].Members).To(Equal([]string{"a", "b"}))
		Expect(codesInduced[0].Proposal).To(Equal(codes.Proposal{Name: "BYPASS-CSF", Definition: "The agent works around CSF.",
			Edge: codes.Edge{From: "agent", To: "harness"}, Side: codes.SideHarness}))
		Expect(codesInduced[1].Members).To(BeEmpty())
		Expect(codesInduced[1].Quotes).To(Equal([]string{"not in any complaint"}))
		_, err = cannedCoder("[").Induce(context.Background(), complaints)
		Expect(err).To(MatchError(ContainSubstring("decode the model's induction")))
	})
})

var _ = Describe("Tally", func() {
	coded := []codes.Coded{
		{Complaint: complaints[0], Code: "mast_no_verification"},
		{Complaint: complaints[1], Proposal: &codes.Proposal{Name: "Bypass CSF", Edge: codes.Edge{From: "agent", To: "harness"}, Side: codes.SideHarness}},
		{Complaint: complaints[2], Proposal: &codes.Proposal{Name: "bypass-csf!", Edge: codes.Edge{From: "agent", To: "harness"}, Side: codes.SideHarness}},
		{Complaint: complaints[3], Rejected: codes.RejectedNoQuote},
	}

	It("counts per code, proposal, edge and side, most first", func() {
		summary := codes.Tally(coded, codes.Catalogue)
		Expect(summary.Complaints).To(Equal(4))
		Expect([]int{summary.Coded, summary.Proposed, summary.Rejected}).To(Equal([]int{1, 2, 1}))
		Expect(summary.Codes).To(Equal([]codes.CodeCount{{Code: "mast_no_verification", Name: "No or incomplete verification",
			Edge: codes.Edge{From: "agent", To: "session_gate"}, Side: codes.SideHarness, Route: "a session gate or a miner", Count: 1}}))
		Expect(summary.Proposals).To(HaveLen(1), "names that differ in case and punctuation count together")
		Expect(summary.Proposals[0].Name).To(Equal("bypass-csf"))
		Expect(summary.Proposals[0].Count).To(Equal(2))
		Expect(summary.Edges).To(Equal([]codes.Count{{Key: "agent→harness", Count: 2}, {Key: "agent→session_gate", Count: 1}}))
		Expect(summary.Sides).To(Equal([]codes.Count{{Key: "harness", Count: 3}}))
	})

	It("clusters kept complaints and measures them against a reference", func() {
		clusters := codes.Clusters(coded)
		Expect(clusters).To(Equal(map[string]string{"a": "mast_no_verification", "b": "proposed:bypass-csf", "c": "proposed:bypass-csf"}))
		agreement := codes.Agree(clusters, map[string]string{"a": "SILENT-WAIT", "b": "BYPASS-CSF", "c": "SHAPE-DRIFT", "z": "ORPHAN-WORK"})
		Expect(agreement.Complaints).To(Equal(3))
		Expect([]int{agreement.Clusters, agreement.Classes}).To(Equal([]int{2, 3}))
		Expect(agreement.Purity).To(BeNumerically("~", 2.0/3, 1e-9))
		Expect(agreement.InversePurity).To(BeNumerically("~", 1, 1e-9))
		Expect(agreement.F).To(BeNumerically("~", 0.8, 1e-9))
		Expect(codes.Agree(clusters, nil)).To(Equal(codes.ClusterAgreement{}))
	})

	It("reads each kept complaint's side", func() {
		Expect(codes.SidesOf(coded, codes.Catalogue)).To(Equal(map[string]string{"a": "harness", "b": "harness", "c": "harness"}))
	})
})

var _ = Describe("Kappa", func() {
	It("is 1 for identical labelings, 0 by chance and below 0 against it", func() {
		labels := map[string]string{"a": "harness", "b": "model", "c": "harness", "d": "model"}
		Expect(codes.Kappa(labels, labels)).To(BeNumerically("~", 1, 1e-9))
		Expect(codes.Kappa(labels, map[string]string{"a": "harness", "b": "harness", "c": "model", "d": "model"})).To(BeNumerically("~", 0, 1e-9))
		Expect(codes.Kappa(labels, map[string]string{"a": "model", "b": "harness", "c": "model", "d": "harness"})).To(BeNumerically("~", -1, 1e-9))
		Expect(codes.Kappa(labels, map[string]string{"x": "harness"})).To(BeZero())
		Expect(codes.Kappa(map[string]string{"a": "harness"}, map[string]string{"a": "harness"})).To(BeNumerically("==", 1))
	})

	It("leaves the plain matching share to read a one-label reference", func() {
		share, shared := codes.Matching(map[string]string{"a": "harness", "b": "model", "x": "model"}, map[string]string{"a": "harness", "b": "harness"})
		Expect(shared).To(Equal(2))
		Expect(share).To(BeNumerically("~", 0.5, 1e-9))
		share, shared = codes.Matching(nil, nil)
		Expect([]any{share, shared}).To(Equal([]any{0.0, 0}))
	})
})

var _ = Describe("ReadTranscript", func() {
	transcript := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Done: the tests pass."},{"type":"tool_use","name":"Bash"}]}}`,
		`{"type":"user","uuid":"u1","timestamp":"2026-10-05T02:19:00Z","origin":{"kind":"human"},"message":{"content":"no you never ran them"}}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-10-05T02:20:00Z","origin":{"kind":"human"},"message":{"content":"thanks, ship it"}}`,
		`{"type":"user","uuid":"u3","timestamp":"2026-10-05T02:21:00Z","origin":{"kind":"task-notification"},"message":{"content":"no output"}}`,
		`{"type":"user","uuid":"u4","timestamp":"2026-10-05T02:22:00Z","message":{"content":[{"type":"tool_result","content":"no such file"}]}}`,
		`not json`,
		`{"type":"user","uuid":"u5","timestamp":"2026-10-05T09:00:00Z","origin":{"kind":"human"},"message":{"content":[{"type":"text","text":"why did you stop"}]}}`,
	}, "\n")

	It("reads the operator's messages in the window, marks the corrections and keeps the agent's last text", func() {
		read, err := codes.ReadTranscript("session", strings.NewReader(transcript), sent, sent.Add(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(read).To(Equal([]codes.Complaint{
			{Source: "session", Ref: "u1", At: sent, Text: "no you never ran them", Context: "Done: the tests pass.", Correction: true},
			{Source: "session", Ref: "u2", At: sent.Add(time.Minute), Text: "thanks, ship it", Context: "Done: the tests pass."},
		}))
	})

	It("selects the corrections and the messages a reference labels, with the reader's recall on it", func() {
		read, err := codes.ReadTranscript("session", strings.NewReader(transcript), time.Time{}, time.Time{})
		Expect(err).NotTo(HaveOccurred())
		selected, recall := codes.Select(read, nil)
		Expect(selected).To(HaveLen(2), "u1 and u5 are corrections")
		Expect(recall).To(Equal(codes.ReaderRecall{}))
		selected, recall = codes.Select(read, map[string]string{"u2": "PRAISE", "u5": "SILENT-WAIT", "absent": "X"})
		Expect(selected).To(HaveLen(3))
		Expect(recall).To(Equal(codes.ReaderRecall{Labeled: 2, Corrections: 1}))
	})

	It("leaves the window open-ended with a zero end", func() {
		read, err := codes.ReadTranscript("session", strings.NewReader(transcript), time.Time{}, time.Time{})
		Expect(err).NotTo(HaveOccurred())
		Expect(read).To(HaveLen(3))
		Expect(read[2].Ref).To(Equal("u5"))
		Expect(read[2].Correction).To(BeTrue())
	})
})

var _ = Describe("Records", func() {
	It("records a code's edge and side, and reads back the latest coding of each complaint", func() {
		coded := codes.Coded{Complaint: complaints[0], Code: "mast_no_verification", Quote: "never ran"}
		record := codes.NewRecord(coded, codes.Catalogue, "ollama/qwen3:8b")
		Expect(record.Edge).To(Equal(codes.Edge{From: "agent", To: "session_gate"}))
		Expect(record.Side).To(Equal(codes.SideHarness))
		proposed := codes.NewRecord(codes.Coded{Complaint: complaints[1], Proposal: &codes.Proposal{Name: "Bypass CSF", Side: codes.SideHarness}}, codes.Catalogue, "m")
		Expect(proposed.Side).To(Equal(codes.SideHarness))
		lines := strings.Join([]string{
			`{"source":"s","ref":"a","code":"mast_task_derailment"}`,
			`{"source":"s","ref":"b","code":""}`,
			`{"source":"s","ref":"a","code":"mast_no_verification"}`,
		}, "\n")
		read, err := codes.ReadRecords(strings.NewReader(lines))
		Expect(err).NotTo(HaveOccurred())
		Expect(read).To(HaveLen(2))
		Expect(read[0].Code).To(Equal("mast_no_verification"))
		_, err = codes.ReadRecords(strings.NewReader("{"))
		Expect(err).To(MatchError(ContainSubstring(codes.RecordFile + " line 1")))
	})

	It("reads a reference clustering and refuses a malformed line", func() {
		classes, sides, err := codes.ReadReference(strings.NewReader("# ref\tclass\tside\n\na\tBYPASS-CSF\tharness\nb\tPROXY-METRIC\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(classes).To(Equal(map[string]string{"a": "BYPASS-CSF", "b": "PROXY-METRIC"}))
		Expect(sides).To(Equal(map[string]string{"a": "harness"}))
		_, _, err = codes.ReadReference(strings.NewReader("only-a-ref\n"))
		Expect(err).To(MatchError(ContainSubstring("reference line 1")))
	})
})
