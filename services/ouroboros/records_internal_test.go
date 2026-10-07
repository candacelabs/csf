// Copyright 2026 Candace Labs

package ouroboros

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Records in the shapes the harness and the miners write, reduced to the
// fields the loop folds.
const (
	resultOne   = `{"time":"2026-10-03T02:00:00Z","event_type":"result","event":{"type":"result","total_cost_usd":0.7358225,"usage":{"input_tokens":10,"output_tokens":21752}}}`
	resultTwo   = `{"time":"2026-10-03T02:30:00Z","event_type":"result","event":{"type":"result","total_cost_usd":1.2065423,"usage":{"input_tokens":5,"output_tokens":9487}}}`
	resultReset = `{"time":"2026-10-03T03:00:00Z","event_type":"result","event":{"type":"result","total_cost_usd":0.25,"usage":{"input_tokens":1,"output_tokens":100}}}`
	startedLine = `{"time":"2026-10-03T01:45:00Z","event_type":"harness_run_started","assignment_id":"0caf2d51-1ee7-468a-882f-ac478a437640"}`
	findingLine = `{"miner":"draft-pr-late","rule":"invisible","subject":[{"text":"6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60"}],"severity":"SEVERITY_S3","proof":[{"relation":"knee","args":[{"number":468}],"span":{"source":"","line":0}}]}`
	mergeReport = "INFO: Build completed successfully\n== ontology score ratchet against origin/main\ncheck-merge: REFUSED: house-lint\nmerge-pr: pull request #15 is refused; see the report above\n"
)

var _ = Describe("Folding a fixer session's usage", func() {
	It("sums the maximum of each run of the executor's cumulative cost", func() {
		usage := FoldUsage([]byte(strings.Join([]string{startedLine, resultOne, resultTwo, resultReset}, "\n")))
		Expect(usage.CostUSDMicros).To(Equal(int64(1206542 + 250000)))
		Expect(usage.InputTokens).To(Equal(int64(16)))
		Expect(usage.OutputTokens).To(Equal(int64(31339)))
		Expect(usage.Seconds).To(Equal(int64(75 * 60)))
		Expect(usage.Results).To(Equal(3))
	})

	It("reads an empty log as nothing spent", func() {
		Expect(FoldUsage(nil)).To(Equal(Usage{}))
	})
})

var _ = Describe("Naming a miner package", func() {
	It("keeps the proposal's name a Bazel package name", func() {
		Expect(MinerName("no_self_merge")).To(Equal("no_self_merge"))
		Expect(MinerName("Draft PR-Late!")).To(Equal("draft_pr_late"))
	})
})

var _ = Describe("Reading a miner's findings", func() {
	It("names each finding by its subject and the item it came from", func() {
		at := time.Date(2026, time.October, 4, 4, 0, 0, 0, time.UTC)
		items := []string{"6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e59/events.jsonl", "6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60/events.jsonl"}
		findings, err := ParseFindings("_template", items, []byte(findingLine+"\n"), at)
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(HaveLen(1))
		Expect(findings[0].Miner).To(Equal("_template"))
		Expect(findings[0].Rule).To(Equal("invisible"))
		Expect(findings[0].Subject).To(Equal("6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60"))
		Expect(findings[0].Item).To(Equal(items[1]))
		Expect(findings[0].Severity).To(Equal("SEVERITY_S3"))
		Expect(findings[0].FoundAt).To(Equal(at))
		Expect(string(findings[0].Record)).To(Equal(findingLine))
	})

	It("refuses a line that is not a finding", func() {
		_, err := ParseFindings("_template", nil, []byte("not json\n"), time.Time{})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Reading a merge report's refusal", func() {
	It("quotes the lines that name the refusal", func() {
		Expect(Refusal(mergeReport)).To(Equal("check-merge: REFUSED: house-lint\nmerge-pr: pull request #15 is refused; see the report above"))
	})

	It("falls back to the report's tail", func() {
		Expect(Refusal("a\nb\nc\nd\ne\nf\ng\n")).To(Equal("c\nd\ne\nf\ng"))
	})
})

var _ = Describe("Reading the sessions that authored a pull request", func() {
	It("reads each distinct CSF-Session trailer once", func() {
		Expect(SessionTrailers("fix\n\nCSF-Session: a\nCSF-Model: m\n\nmore\n\nCSF-Session: a\nCSF-Session: b\n")).To(Equal([]string{"a", "b"}))
	})
})

var _ = Describe("Reading a repository slug", func() {
	It("reads owner/name from the https and ssh spellings", func() {
		Expect(RepositorySlug("https://github.com/candacelabs/csf_staging.git\n")).To(Equal("candacelabs/csf_staging"))
		Expect(RepositorySlug("git@github.com:candacelabs/csf_staging.git")).To(Equal("candacelabs/csf_staging"))
		Expect(RepositorySlug("https://github.com/candacelabs/csf_staging")).To(Equal("candacelabs/csf_staging"))
	})

	It("refuses what names no repository", func() {
		_, err := RepositorySlug("file:///tmp")
		Expect(err).To(MatchError(ErrNoRepositorySlug))
	})
})

var _ = Describe("Reading a mining ticket", func() {
	It("takes the severity label and skips language tickets", func() {
		ticket, ok := MiningTicket(249, "NO-SELF-MERGE", []string{"backlog", "S0"})
		Expect(ok).To(BeTrue())
		Expect(ticket.Severity).To(Equal("S0"))
		_, ok = MiningTicket(250, "LANG: terms", []string{"backlog", "lang", "S2"})
		Expect(ok).To(BeFalse())
		_, ok = MiningTicket(251, "plain", []string{"backlog"})
		Expect(ok).To(BeFalse())
	})
})
