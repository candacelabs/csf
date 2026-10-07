// Copyright 2026 Candace Labs

package github_test

import (
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/net/github"
)

// The handwritten half of the protocol client; the generated operations are
// specified through csf/githubtools, which calls every one of them.
var _ = Describe("The GitHub protocol client", func() {
	DescribeTable("reads a push remote's repository",
		func(remote string) {
			parsed, err := github.ParseRemote(remote)
			Expect(err).NotTo(HaveOccurred())
			Expect(parsed).To(Equal(github.Remote{Owner: "o", Name: "r"}))
		},
		Entry("an https remote", "https://github.com/o/r.git"),
		Entry("an SSH remote", "git@github.com:o/r.git"),
		Entry("owner/name", "o/r"),
		Entry("a browser URL", "https://github.com/o/r/\n"),
	)

	DescribeTable("refuses a remote that is not a github.com repository",
		func(remote string) {
			_, err := github.ParseRemote(remote)
			Expect(err).To(MatchError(github.ErrInvalidRepository))
		},
		Entry("another host", "https://gitlab.com/o/r.git"),
		Entry("no name", "o"),
		Entry("a path below a repository", "o/r/issues"),
	)

	It("reads the active account's token from gh's hosts.yml", func() {
		token, err := github.TokenFromGhHosts([]byte("github.com:\n    users:\n        someone:\n            oauth_token: gho_one\n    git_protocol: https\n    user: someone\n    oauth_token: gho_one\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(token).To(Equal("gho_one"))
	})

	It("reports a hosts.yml that holds no token, as gh writes it when a keyring holds it", func() {
		_, err := github.TokenFromGhHosts([]byte("github.com:\n    git_protocol: https\n    user: someone\n"))
		Expect(err).To(MatchError(github.ErrNoToken))
		_, err = github.TokenFromGhHosts([]byte("{"))
		Expect(err).To(HaveOccurred())
	})

	It("reads the rate limit a response reports, and none from no response", func() {
		response := &http.Response{Header: http.Header{"X-Ratelimit-Limit": {"5000"}, "X-Ratelimit-Remaining": {"4321"}, "X-Ratelimit-Reset": {"1791200000"}}}
		Expect(github.RateLimitOf(response)).To(Equal(github.RateLimit{Limit: 5000, Remaining: 4321, Reset: time.Unix(1791200000, 0).UTC()}))
		Expect(github.RateLimitOf(nil)).To(Equal(github.RateLimit{}))
	})
})
