// Copyright 2026 Candace Labs

package github_test

import (
	"context"
	"io"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/net/github"
)

const (
	hookToken       = "gho_spec"
	redeliveryURL   = "https://api.github.com/repos/o/r/hooks/501/deliveries/7/attempts"
	deliveriesLinks = `<https://api.github.com/repositories/1/hooks/501/deliveries?per_page=100&cursor=v1_12345>; rel="next", <https://api.github.com/repositories/1/hooks/501/deliveries?per_page=100>; rel="first"`
)

func answer(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"X-Ratelimit-Limit": {"5000"}, "X-Ratelimit-Remaining": {"4999"}}, Body: io.NopCloser(strings.NewReader(body))}
}

var _ = Describe("The GitHub protocol client's webhook deliveries", func() {
	var (
		transport *MockIHTTPClient
		client    *github.GitHubClient
	)

	BeforeEach(func() {
		transport = NewMockIHTTPClient(gomock.NewController(GinkgoT()))
		var err error
		client, err = github.NewGitHubClient(transport, hookToken)
		Expect(err).NotTo(HaveOccurred())
	})

	It("asks a redelivery with an authenticated POST and reads GitHub's 202 as done", func() {
		transport.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *http.Request) (*http.Response, error) {
			Expect(request.Method).To(Equal(http.MethodPost))
			Expect(request.URL.String()).To(Equal(redeliveryURL))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer " + hookToken))
			return answer(http.StatusAccepted, `{}`), nil
		})
		rate, err := client.RedeliverHookDelivery(context.Background(), "o", "r", 501, 7)
		Expect(err).NotTo(HaveOccurred())
		Expect(rate.Remaining).To(Equal(4999))
	})

	It("reports a redelivery GitHub refused with its status and body", func() {
		transport.EXPECT().Do(gomock.Any()).Return(answer(http.StatusUnprocessableEntity, `{"message":"too old"}`), nil)
		_, err := client.RedeliverHookDelivery(context.Background(), "o", "r", 501, 7)
		var refused *github.StatusError
		Expect(err).To(BeAssignableToTypeOf(refused))
		Expect(err.Error()).To(ContainSubstring("too old"))
	})

	It("reads the next page's cursor from the Link header, and none on the last page", func() {
		Expect(github.NextCursor(&http.Response{Header: http.Header{"Link": {deliveriesLinks}}})).To(Equal("v1_12345"))
		Expect(github.NextCursor(&http.Response{Header: http.Header{}})).To(BeEmpty())
		Expect(github.NextCursor(nil)).To(BeEmpty())
	})
})
