// Copyright 2026 Candace Labs

package opsview_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/opsview"
)

// The sentences a card shows for a tool call and a failure, from inputs
// shaped like the ones sessions on a host logged on 2026-10-05.
const (
	// someoneHome is a Linux home directory spelled from its two segments: the
	// scrubber under test recognises the shape, and no account path is written
	// out in this file.
	someoneHome      = "/home" + "/someone"
	activityWorktree = someoneHome + "/.local/state/csf/harness/abc/worktree"
)

var _ = Describe("Activity lines", func() {
	DescribeTable("say what a tool call does, never the raw command or a home path",
		func(tool string, input string, sentence string) {
			Expect(opsview.Activity(tool, json.RawMessage(input), activityWorktree)).To(Equal(sentence))
		},
		Entry("an edit, relative to the worktree", "Edit", `{"file_path":"`+activityWorktree+`/ipc/docker/containers.go"}`, "Editing ipc/docker/containers.go"),
		Entry("a read outside it, home shown as ~", "Read", `{"file_path":"`+someoneHome+`/notes/x.md"}`, "Reading ~/notes/x.md"),
		Entry("Bazel tests, by target count", "Bash", `{"command":"tools/bazel.sh test -- //services/opsview:all //io/inproc:all //services/harness:all --test_output=errors"}`, "Running Bazel tests (3 targets)"),
		Entry("Go tests, by package", "Bash", `{"command":"go test ./services/opsview/ -run X"}`, "Running Go tests in services/opsview"),
		Entry("past cd and a pipe", "Bash", `{"command":"cd `+someoneHome+`/x && git diff HEAD~1 HEAD --name-only | grep go"}`, "Reviewing its changes"),
		Entry("a pull request by number", "Bash", `{"command":"gh pr view 259 --json state"}`, "Checking pull request #259"),
		Entry("a repository script past its wrappers", "Bash", `{"command":"timeout 300 bash tools/check-merge.sh 2>&1 | tail -50"}`, "Running the merge checks"),
		Entry("a quoted alternation as words", "Bash", `{"command":"grep -n \"admission\\|limit\" services/harness/sessions.go"}`, "Searching for “admission” or “limit” in services/harness/sessions.go"),
		Entry("the merge path", "Bash", `{"command":"bash tools/merge-pr.sh 341"}`, "Merging pull request #341"),
		Entry("the session's own description first", "Bash", `{"command":"x","description":"Run the gate check"}`, "Run the gate check"),
		Entry("a search expression as words", "Grep", `{"pattern":"admission.*limit","path":"`+activityWorktree+`/services"}`, "Searching for “admission limit” in services"),
		Entry("a page by its host", "WebFetch", `{"url":"https://pkg.go.dev/x","prompt":"p"}`, "Reading pkg.go.dev"),
		Entry("an MCP operation in words", "mcp__csf__ListAgentSessions", `{}`, "CSF: list agent sessions"),
		Entry("a wait by its reason", "ScheduleWakeup", `{"reason":"Watching CI run"}`, "Waiting: watching CI run"),
		Entry("an unknown tool by its name", "Unknown", `{}`, "Unknown"),
		Entry("a scratch directory, whose name spells a home path", "Write", `{"file_path":"/tmp/claude-1000/-home-someone--local-state-x/scratchpad/notes.md"}`, "Writing scratch/scratchpad/notes.md"),
	)

	It("keeps the raw call for the raw view, without home paths", func() {
		raw := opsview.RawActivity("Read", json.RawMessage(`{"file_path":"`+activityWorktree+`/README.md"}`), activityWorktree)
		Expect(raw).To(Equal("Read: README.md"))
	})

	DescribeTable("word failures as what happened, with the turn",
		func(raw string, wording string) {
			Expect(opsview.HumanError(raw, "")).To(Equal(wording))
		},
		Entry("a canceled turn", "claudecode brain: turn 1: ipc/proc: claude: terminated signal received", "Turn 1: Stopped: the session was canceled."),
		Entry("an abandoned turn", "claudecode brain: turn 1: claudecode session: the turn was abandoned before its result: context canceled", "Turn 1: Stopped: the session was canceled."),
		Entry("a timeout", "claudecode session: the Claude Code process is closed: ipc/proc: "+someoneHome+"/.local/bin/claude: context deadline exceeded", "Timed out."),
		Entry("no commits for a pull request", "session gate: could not open the draft pull request: ipc/proc: gh: exit status 1: pull request create failed: GraphQL: No commits between main and dev", "No pull request yet: the branch has no commits."),
		Entry("anything else as its innermost message", "harness session: create hooks directory: mkdir "+someoneHome+"/x/.git: not a directory", "Create hooks directory: mkdir ~/x/.git: not a directory"),
		Entry("nothing", "", ""),
	)
})
