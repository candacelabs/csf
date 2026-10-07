// Copyright 2026 Candace Labs

package sessiongate_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/sessiongate"
)

// rules is the rule of each finding, in order.
func rules(findings []sessiongate.ShellFinding) []sessiongate.Rule {
	found := []sessiongate.Rule{}
	for _, finding := range findings {
		found = append(found, finding.Rule)
	}
	return found
}

var _ = Describe("FindShellWaits", func() {
	DescribeTable("rejects a command that waits on nothing",
		func(command string, expected ...sessiongate.Rule) {
			findings, err := sessiongate.FindShellWaits(command)
			Expect(err).NotTo(HaveOccurred())
			Expect(rules(findings)).To(Equal(expected))
			for _, finding := range findings {
				Expect(finding.Message()).To(ContainSubstring(finding.Replacement()))
			}
		},
		// The two loops earlier agents left running forever, verbatim.
		Entry("an until loop on pgrep -f",
			`until ! pgrep -f s5final.sh >/dev/null; do sleep 10; done; cat x.log`,
			sessiongate.RulePollLoop, sessiongate.RuleSelfMatchingPgrep),
		Entry("a while loop on pgrep -f",
			`while pgrep -f "check-house-lint.sh" >/dev/null; do sleep 10; done`,
			sessiongate.RulePollLoop, sessiongate.RuleSelfMatchingPgrep),
		Entry("a foreground sleep", `sleep 1`, sessiongate.RuleForegroundSleep),
		Entry("a sleep between commands", `go test ./... && sleep 3 && cat out.txt`, sessiongate.RuleForegroundSleep),
		Entry("a sleep in a command substitution", `x=$(sleep 2; echo hi)`, sessiongate.RuleForegroundSleep),
		Entry("a for loop that polls", `for i in $(seq 30); do curl -s localhost:8080 && break; sleep 2; done`, sessiongate.RulePollLoop),
		Entry("an until loop with a quoted sleep", `until test -f done.txt; do "sleep" 1; done`, sessiongate.RulePollLoop),
		Entry("a loop inside bash -c", `bash -c 'while true; do sleep 5; done'`, sessiongate.RulePollLoop),
		Entry("a sleep behind a wrapper", `command sleep 5`, sessiongate.RuleForegroundSleep),
		Entry("a bundled pgrep -f flag", `pgrep -fl node`, sessiongate.RuleSelfMatchingPgrep),
		Entry("pgrep --full", `pgrep --full "go run"`, sessiongate.RuleSelfMatchingPgrep),
		Entry("a backgrounded poll loop, which still runs forever",
			`while true; do sleep 60; done &`, sessiongate.RulePollLoop),
	)

	DescribeTable("allows a command that does not wait on nothing",
		func(command string) {
			findings, err := sessiongate.FindShellWaits(command)
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(BeEmpty())
		},
		Entry("go test", `go test ./...`),
		Entry("a commit from a message file", `git commit -F msg`),
		Entry("a heredoc whose text says sleep", "cat > notes.txt <<'EOF'\nwhile true; do sleep 10; done\nsleep 5\nEOF"),
		Entry("an unquoted heredoc whose text says sleep", "cat <<EOF\nthen sleep 5 seconds\nEOF"),
		Entry("the word sleep inside a string", `echo "never sleep 10 in a loop"`),
		Entry("grep for sleep", `grep -rn "sleep" .`),
		Entry("a backgrounded subshell that sleeps", `(sleep 5; kill 123) &`),
		Entry("pgrep without -f", `pgrep node`),
		Entry("a loop that does not sleep", `for f in *.go; do gofmt -l "$f"; done`),
	)

	It("returns the offending source text", func() {
		findings, err := sessiongate.FindShellWaits(`echo start; sleep 30`)
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(ConsistOf(sessiongate.ShellFinding{Rule: sessiongate.RuleForegroundSleep, Snippet: "sleep 30"}))
	})

	It("rejects the loop the operator flagged on 2026-10-05 and names csf await url-status", func() {
		findings, err := sessiongate.FindShellWaits(`for i in $(seq 1 40); do c=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:14120/); if [ "$c" = "200" ]; then echo up; exit 0; fi; sleep 3; done`)
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(HaveLen(1))
		Expect(findings[0].Rule).To(Equal(sessiongate.RulePollLoop))
		Expect(findings[0].Message()).To(HaveSuffix("wait with `csf await url-status -url URL -status 200 -deadline DURATION`, " +
			"which returns once the condition holds or fails at the deadline"))
	})

	DescribeTable("names the csf await condition matching what a poll loop polls",
		func(command string, condition string) {
			findings, err := sessiongate.FindShellWaits(command)
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).NotTo(BeEmpty())
			Expect(findings[0].Rule).To(Equal(sessiongate.RulePollLoop))
			Expect(findings[0].Replacement()).To(ContainSubstring("`csf await " + condition + " -deadline DURATION`"))
		},
		Entry("a process exit", `for i in $(seq 1 120); do if ! kill -0 4242 2>/dev/null; then exit 0; fi; sleep 5; done`, "harness-stopped -pid PID"),
		Entry("a pull request", `until gh pr view 7 --json state | grep -q MERGED; do sleep 30; done`, "pull-request-merged -pull-request URL"),
		Entry("a session", `while csf get -assignment x | grep -q RUNNING; do sleep 15; done`, "turn-finished -assignment ID"),
		Entry("the load", `while uptime | grep -q 'load average: 6'; do sleep 60; done`, "load-below -load LEVEL"),
		Entry("anything else", `until test -f done.txt; do sleep 1; done`, "CONDITION"),
	)

	It("names csf await and every condition for a foreground sleep, and no executor tool", func() {
		findings, err := sessiongate.FindShellWaits(`go test ./... && sleep 3 && cat out.txt`)
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(HaveLen(1))
		Expect(findings[0].Replacement()).To(Equal("wait with `csf await CONDITION -deadline DURATION`, which returns once the condition " +
			"holds or fails at the deadline; the conditions are harness-ready, harness-stopped, load-below, " +
			"pull-request-merged, session-phase, turn-finished, url-status"))
		Expect(findings[0].Replacement()).NotTo(Or(ContainSubstring("run_in_background"), ContainSubstring("Monitor")))
	})

	It("reports a command it cannot parse with the defined error", func() {
		_, err := sessiongate.FindShellWaits(`while true; do`)
		Expect(err).To(MatchError(sessiongate.ErrUnparsedCommand))
	})
})

var _ = Describe("RunsGitCommit", func() {
	DescribeTable("finds git commit",
		func(command string, expected bool) {
			committed, err := sessiongate.RunsGitCommit(command)
			Expect(err).NotTo(HaveOccurred())
			Expect(committed).To(Equal(expected))
		},
		Entry("from a message file", `git commit -F msg`, true),
		Entry("with global options", `git -C /work -c user.name=x commit -m y`, true),
		Entry("after git add", `git add notes.txt && git commit -m "add notes"`, true),
		Entry("inside bash -c", `bash -c "git commit -m x"`, true),
		Entry("git log", `git log --oneline`, false),
		Entry("the words in an echo", `echo git commit`, false),
		Entry("git commit-tree", `git commit-tree HEAD^{tree}`, false),
	)

	It("reports a command it cannot parse with the defined error", func() {
		_, err := sessiongate.RunsGitCommit(`git commit -m "unterminated`)
		Expect(err).To(MatchError(sessiongate.ErrUnparsedCommand))
	})
})
