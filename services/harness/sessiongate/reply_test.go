// Copyright 2026 Candace Labs

package sessiongate_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/affect"
	"github.com/candacelabs/csf/pkg/terms"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/sessiongate"
)

// check is one complete research check for term, fenced as a reply carries it.
func check(term string) string {
	encoded, err := json.Marshal(session.ResearchCheck{
		Term: term, WhatItIs: "a kernel resource controller", WhatItDoesNotDo: "isolate a filesystem or a network namespace",
		FitsGoal: session.FitsGoalPartly, Why: "it caps and kills a session's processes; isolation needs more", LikelySource: session.LikelySourcePerson,
	})
	Expect(err).NotTo(HaveOccurred())
	return "```" + session.ResearchCheckFence + "\n" + string(encoded) + "\n```\n"
}

func bash(command string) session.ToolUse {
	input, err := json.Marshal(map[string]string{"command": command})
	Expect(err).NotTo(HaveOccurred())
	return session.ToolUse{Name: "Bash", Input: input}
}

func edit(tool string, path string) session.ToolUse {
	input, err := json.Marshal(map[string]string{"file_path": path, "old_string": "a", "new_string": "b"})
	Expect(err).NotTo(HaveOccurred())
	return session.ToolUse{Name: tool, Input: input}
}

var _ = Describe("research checks", func() {
	It("parses one object or an array per fenced block and names a block that is neither", func() {
		reply := "Agreed in part.\n\n" + check("Cgroups") +
			"```research-check\n[{\"term\": \"sandboxing\", \"what_it_is\": \"x\"}, {\"term\": \"landlock\"}]\n```\n" +
			"```research-check\nnot json\n```\n```go\nfmt.Println(\"research-check\")\n```"
		checks, malformed := sessiongate.ParseResearchChecks(reply)
		Expect(checks).To(HaveLen(3))
		Expect(checks[0].Term).To(Equal("Cgroups"))
		Expect(checks[1].Term).To(Equal("sandboxing"))
		Expect(checks[2].Term).To(Equal("landlock"))
		Expect(malformed).To(HaveLen(1))
		Expect(malformed[0].Error()).To(HavePrefix("research-check block 3 is not a JSON object or array"))
	})

	It("misses the terms no complete check covers, matching by stem", func() {
		checks, _ := sessiongate.ParseResearchChecks(check("Cgroups") + "```research-check\n{\"term\": \"sandboxing\", \"what_it_is\": \"x\"}\n```")
		Expect(sessiongate.MissingResearchChecks([]terms.Term{"cgroup", "sandboxing", "initramfs"}, checks)).To(Equal(
			[]terms.Term{"sandboxing", "initramfs"}))
		Expect(sessiongate.MissingResearchChecks(nil, checks)).To(BeEmpty())
		Expect(sessiongate.MissingResearchChecks([]terms.Term{"cgroup"}, nil)).To(Equal([]terms.Term{"cgroup"}))
	})
})

var _ = Describe("commitments", func() {
	DescribeTable("finds the markers #47 lists, in any case, and quotes their sentence",
		func(reply string, marker string, sentence string) {
			found := sessiongate.FindCommitments(reply)
			Expect(found).To(HaveLen(1))
			Expect(found[0].Marker).To(Equal(marker))
			Expect(found[0].Sentence).To(Equal(sentence))
		},
		Entry("I'll", "Saved. From here I'll flag any analogy that is wrong. Next step is yours.", "i'll", "From here I'll flag any analogy that is wrong"),
		Entry("a curly apostrophe", "I’ll say so when I use one.", "i’ll", "I’ll say so when I use one"),
		Entry("from now on", "Noted: from now on every table is a disk scan.\nMore text.", "from now on", "Noted: from now on every table is a disk scan"),
		Entry("going forward", "Going forward the proxy prints its source line.", "going forward", "Going forward the proxy prints its source line"),
		Entry("until then I will", "Until then I will say so!", "until then i will", "Until then I will say so"),
		Entry("two markers in one sentence are one commitment", "Saved: from now on I'll flag it.", "from now on", "Saved: from now on I'll flag it"),
		Entry("every time", "This fires every time a message arrives.", "every time", "This fires every time a message arrives"),
	)

	It("finds nothing in a reply without a marker, inside code, or in a word that only contains one", func() {
		Expect(sessiongate.FindCommitments("The gate refuses the reply. `csf list` prints its source line.")).To(BeEmpty())
		Expect(sessiongate.FindCommitments("```\nfrom now on I'll\n```\nand `going forward`.")).To(BeEmpty())
		Expect(sessiongate.FindCommitments("The hill climbs; everytime is one word.")).To(BeEmpty())
	})

	It("finds a marker per sentence and bounds a long sentence", func() {
		long := "I'll keep this " + repeat("and on ", 40) + "end. Going forward too."
		found := sessiongate.FindCommitments(long)
		Expect(found).To(HaveLen(2))
		Expect(found[0].Marker).To(Equal("i'll"))
		Expect(len(found[0].Sentence)).To(Equal(160 + len("…")))
		Expect(found[0].Sentence).To(HaveSuffix("…"))
		Expect(found[1]).To(Equal(sessiongate.Commitment{Marker: "going forward", Sentence: "Going forward too"}))
	})
})

var _ = Describe("waits", func() {
	DescribeTable("finds the ways a reply waits on a background result",
		func(reply string, marker string) {
			found := sessiongate.FindWaits(reply)
			Expect(found).To(HaveLen(1))
			Expect(found[0].Marker).To(Equal(marker))
		},
		Entry("waiting on", "The proof run is on its first tickets. I'm waiting on those two completions.", "waiting on"),
		Entry("waiting for", "Waiting for the harness suite rerun before the squash-merge.", "waiting for"),
		Entry("will follow", "The commit, ratchets and merge will follow from it.", "will follow"),
		Entry("once the run finishes", "I'll patch the body once the ontology score finishes.", "once the ontology score finishes"),
		Entry("when it completes", "House lint is running; I act when the bar completes.", "when the bar completes"),
		Entry("a completion notification", "Nothing else is outstanding, so its completion notification decides the rest.", "completion notification"),
	)

	It("finds nothing in a reply that does not wait, or inside code", func() {
		Expect(sessiongate.FindWaits("Merged as #333; the branch is deleted.")).To(BeEmpty())
		Expect(sessiongate.FindWaits("```\nwaiting on the run\n```\nand `will follow`.")).To(BeEmpty())
	})
})

func repeat(text string, count int) string {
	out := ""
	for range count {
		out += text
	}
	return out
}

var _ = Describe("enforcing artifacts", func() {
	It("recognizes a commit, a gate or hook change and a ticket item with an owner, and nothing else", func() {
		artifacts := sessiongate.EnforcingArtifacts([]session.ToolUse{
			bash("git add -A && git commit -F /tmp/msg"),
			edit("Edit", "/work/services/harness/sessiongate/gate_call_reply.go"),
			edit("Write", "/work/.claude/settings.json"),
			edit("Write", "/srv/someone/.claude/projects/x/memory/analogies.md"),
			bash("gh issue create --repo o/r --title t --body b --assignee operator"),
			bash("gh issue edit 47 --add-assignee operator"),
			bash("gh issue comment 47 --body 'I will do it'"),
			bash("git status"),
			{Name: "Read", Input: json.RawMessage(`{"file_path": "/work/tools/check-merge.sh"}`)},
			{Name: "Bash", Input: json.RawMessage(`not json`)},
		})
		Expect(artifacts).To(Equal([]sessiongate.Artifact{
			{Kind: sessiongate.ArtifactCommit, Detail: "git add -A && git commit -F /tmp/msg"},
			{Kind: sessiongate.ArtifactGateOrHook, Detail: "/work/services/harness/sessiongate/gate_call_reply.go"},
			{Kind: sessiongate.ArtifactGateOrHook, Detail: "/work/.claude/settings.json"},
			{Kind: sessiongate.ArtifactTicketItem, Detail: "gh issue create --repo o/r --title t --body b --assignee operator"},
			{Kind: sessiongate.ArtifactTicketItem, Detail: "gh issue edit 47 --add-assignee operator"},
		}))
		Expect(sessiongate.EnforcingArtifacts(nil)).To(BeEmpty())
	})

	It("recognizes an owned ticket item made through the GitHub tools, and not one with no owner", func() {
		artifacts := sessiongate.EnforcingArtifacts([]session.ToolUse{
			{Name: "mcp__csf__IssuesCreate", Input: json.RawMessage(`{"owner":"o","repo":"r","body":{"title":"t","assignees":["operator"]}}`)},
			{Name: "mcp__csf__IssuesUpdate", Input: json.RawMessage(`{"owner":"o","repo":"r","issue_number":47,"body":{"assignee":"operator"}}`)},
			{Name: "mcp__csf__IssuesCreate", Input: json.RawMessage(`{"owner":"o","repo":"r","body":{"title":"t"}}`)},
			{Name: "mcp__csf__IssuesCreateComment", Input: json.RawMessage(`{"owner":"o","repo":"r","issue_number":47,"body":{"body":"I will"}}`)},
		})
		Expect(artifacts).To(Equal([]sessiongate.Artifact{
			{Kind: sessiongate.ArtifactTicketItem, Detail: "IssuesCreate o/r"},
			{Kind: sessiongate.ArtifactTicketItem, Detail: "IssuesUpdate o/r"},
		}))
	})

	DescribeTable("reads an owned ticket item out of a command",
		func(command string, expected bool) {
			owned, err := sessiongate.RunsOwnedTicketItem(command)
			Expect(err).NotTo(HaveOccurred())
			Expect(owned).To(Equal(expected))
		},
		Entry("create with a short assignee flag", `gh issue create -t title -a me`, true),
		Entry("inside sh -c", `sh -c 'gh issue create --assignee me --title t'`, true),
		Entry("create with no owner", `gh issue create --title t --body b`, false),
		Entry("a pull request", `gh pr create --assignee me`, false),
		Entry("an issue listing", `gh issue list --assignee me`, false),
	)
})

var _ = Describe("JudgeReply", func() {
	const agreeing = "**Verdict:** Agreed. Cgroups plus the kernel's own sandboxing features fit better than containers here."

	It("refuses a reply to unvetted terms without a research check per term, with the skeleton to fill", func() {
		findings := sessiongate.JudgeReply(agreeing, []terms.Term{"cgroup", "sandboxing"}, nil, nil, affect.StrainLow)
		Expect(findings).To(HaveLen(1))
		Expect(findings[0].Rule).To(Equal(sessiongate.RuleResearchCheck))
		Expect(findings[0].Message).To(And(
			HavePrefix("research_check: no complete research check for the unvetted term(s) cgroup, sandboxing."),
			ContainSubstring(session.ResearchCheckSkeleton("cgroup"))))
	})

	It("passes the same reply once it carries a complete check for each term", func() {
		Expect(sessiongate.JudgeReply(agreeing+"\n\n"+check("cgroup")+check("sandboxing"), []terms.Term{"cgroup", "sandboxing"}, nil, nil, affect.StrainLow)).To(BeEmpty())
	})

	It("refuses a first-person future commitment whose only artifact is a memory file, and passes it with a commit or without the commitment", func() {
		reply := "I've also saved your standing request: from now on I'll flag any analogy that's wrong in a way that matters."
		memory := []session.ToolUse{edit("Write", "/srv/someone/.claude/projects/x/memory/analogies.md")}
		findings := sessiongate.JudgeReply(reply, nil, memory, nil, affect.StrainLow)
		Expect(findings).To(HaveLen(1))
		Expect(findings[0].Rule).To(Equal(sessiongate.RuleCommitment))
		Expect(findings[0].Message).To(And(
			HavePrefix(`commitment: "I've also saved your standing request: from now on I'll flag any analogy that's wrong in a way that matters" commits your future behaviour`),
			ContainSubstring("a commit, a gate or hook change, or a ticket item with an owner")))
		Expect(sessiongate.JudgeReply(reply, nil, append(memory, bash(`git commit -m "reply gate: refuse analogies without a check"`)), nil, affect.StrainLow)).To(BeEmpty())
		Expect(sessiongate.JudgeReply("Saved the request as ticket #47 with an owner; the gate enforces it.", nil, memory, nil, affect.StrainLow)).To(BeEmpty())
	})

	It("refuses a wait on a background result while no background task runs, and passes it while one does", func() {
		const reply = "Everything is staged. Waiting for the merge gate's completion message before marking it ready."
		findings := sessiongate.JudgeReply(reply, nil, nil, nil, affect.StrainLow)
		Expect(findings).To(HaveLen(1))
		Expect(findings[0].Rule).To(Equal(sessiongate.RuleWaitWithoutChild))
		Expect(findings[0].Message).To(Equal(`wait_without_child: "Waiting for the merge gate's completion message before marking it ready" waits on a background result, ` +
			"but this session runs no background task, so no completion will wake it. Do the work in this turn, start it with run_in_background before ending the turn, or remove the wait"))
		running := []session.BackgroundTask{{TaskID: "b1", TaskType: "local_bash", Description: "Run the merge gate"}}
		Expect(sessiongate.JudgeReply(reply, nil, nil, running, affect.StrainLow)).To(BeEmpty())
	})

	It("reports both rules on one reply", func() {
		findings := sessiongate.JudgeReply(agreeing+" Going forward I'll check first.", []terms.Term{"cgroup"}, nil, nil, affect.StrainLow)
		Expect(findings).To(HaveLen(2))
		Expect(findings[0].Rule).To(Equal(sessiongate.RuleResearchCheck))
		Expect(findings[1].Rule).To(Equal(sessiongate.RuleCommitment))
	})

	It("bounds a reply to a strained operator at the measured length, and only then", func() {
		long := repeat("word ", affect.ReplyWordLimit+1)
		findings := sessiongate.JudgeReply(long, nil, nil, nil, affect.StrainHigh)
		Expect(findings).To(HaveLen(1))
		Expect(findings[0].Rule).To(Equal(sessiongate.RuleStrain))
		Expect(findings[0].Message).To(Equal("strain: the operator's strain reads high and the reply runs 801 words; answer in at most 800"))
		Expect(sessiongate.JudgeReply(long, nil, nil, nil, affect.StrainLow)).To(BeEmpty())
		Expect(sessiongate.JudgeReply(repeat("word ", affect.ReplyWordLimit), nil, nil, nil, affect.StrainHigh)).To(BeEmpty())
	})
})
