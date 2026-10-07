// Copyright 2026 Candace Labs

package opsview_test

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/opsview"
)

// githubStreamRecords are stream records as the receiver writes them, reduced
// to the keys the panel reads: a merge routed to the dispatcher, a csf
// release, a forged delivery and a comment routed to a session.
var githubStreamRecords = strings.Join([]string{
	`{"time":"2026-10-05T07:00:00Z","event_type":"github_delivery","delivery":"d1","github_event":"pull_request","outcome":"accepted"}`,
	`{"time":"2026-10-05T07:00:00Z","event_type":"github_event","delivery":"d1","kind":"pull_request_merged","event_id":"github/pull_request/1/pull_request_merged","repository":"candacelabs/csf","number":412,"branch":"dev/github-events","actor":"octocat","url":"https://github.com/candacelabs/csf/pull/412","summary":"GITHUB-EVENTS"}`,
	`{"time":"2026-10-05T07:00:00Z","event_type":"github_event_routed","event_id":"github/pull_request/1/pull_request_merged","target":"dispatcher","detail":"github-events"}`,
	`{"time":"2026-10-05T07:01:00Z","event_type":"github_delivery","delivery":"d2","github_event":"release","outcome":"accepted"}`,
	`{"time":"2026-10-05T07:01:00Z","event_type":"github_event","kind":"release_published","event_id":"github/release/77","repository":"candacelabs/csf","actor":"octocat","summary":"csf-4ed91b355e21"}`,
	`{"time":"2026-10-05T07:02:00Z","event_type":"github_delivery","delivery":"d3","github_event":"pull_request","outcome":"refused","refusal":"bad_signature"}`,
	`not a record`,
	`{"time":"2026-10-05T07:03:00Z","event_type":"github_event","kind":"comment","event_id":"github/comment/9","repository":"candacelabs/csf","number":412,"actor":"octocat","summary":"Please rename\nthe constant"}`,
	`{"time":"2026-10-05T07:03:00Z","event_type":"github_event_routed","event_id":"github/comment/9","target":"session","detail":"1f0e2d3c-4b5a-4968-8776-655443322110"}`,
}, "\n")

var _ = Describe("the GitHub panel", func() {
	It("lists the newest events first with where each was routed, and the refused deliveries", func() {
		panel := opsview.ReadGitHubPanel([]byte(githubStreamRecords), time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC))
		Expect(panel.Recorded).To(BeTrue())
		Expect(panel.Accepted).To(Equal(2))
		Expect(panel.Refused).To(Equal(1))
		Expect(panel.Events).To(HaveLen(3))
		Expect(panel.Events[0].Kind).To(Equal("comment"))
		Expect(panel.Events[0].Summary).To(Equal("Please rename"))
		Expect(panel.Events[0].Routed).To(Equal("session 1f0e2d3c"))
		Expect(panel.Events[2].Where).To(Equal("candacelabs/csf#412 · dev/github-events"))
		Expect(panel.Events[2].Routed).To(Equal("dispatcher github-events"))
		Expect(panel.Failures).To(ConsistOf(opsview.GitHubRefusal{Time: "07:02:00", Event: "pull_request", Delivery: "d3", Outcome: "refused", Why: "bad_signature"}))
		Expect(panel.Notice).To(BeEmpty(), "the release was published before this host started")
	})

	It("says csf upgrade installs a csf release published after this host started", func() {
		panel := opsview.ReadGitHubPanel([]byte(githubStreamRecords), time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC))
		Expect(panel.Notice).To(Equal("csf release csf-4ed91b355e21 was published at 07:01:00 UTC, after this host started: csf upgrade installs it."))
	})

	It("reads an empty stream as recorded and quiet", func() {
		panel := opsview.ReadGitHubPanel(nil, time.Time{})
		Expect(panel.Recorded).To(BeTrue())
		Expect(panel.Events).To(BeEmpty())
	})
})
