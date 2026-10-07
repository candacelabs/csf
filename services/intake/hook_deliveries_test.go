// Copyright 2026 Candace Labs

package intake_test

import (
	"context"
	"io"
	stdhttp "net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/net/github"
	"github.com/candacelabs/csf/services/intake"
)

// GitHub's answers, reduced to the fields recovery reads.
const (
	hooksAnswer      = `[{"id":501,"active":true,"events":["push"],"name":"web","config":{"url":"https://hooks.example.invalid/api/intake/github","content_type":"json"},"created_at":"2026-10-05T06:00:00Z","updated_at":"2026-10-05T06:00:00Z","last_response":{},"ping_url":"","test_url":"","type":"Repository","url":""}]`
	deliveriesAnswer = `[{"id":9001,"guid":"ae506274-a3b1-11f0-8b2e-1a2b3c4d5e6f","delivered_at":"2026-10-05T07:30:00Z","redelivery":false,"duration":0.1,"status":"failed to connect","status_code":0,"event":"issue_comment","action":"created"}]`
	deliveriesLink   = `<https://api.github.com/repositories/1/hooks/501/deliveries?per_page=100&cursor=v1_9000>; rel="next"`
)

func githubAnswer(status int, body string, header stdhttp.Header) *stdhttp.Response {
	if header == nil {
		header = stdhttp.Header{}
	}
	header.Set("Content-Type", "application/json")
	return &stdhttp.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

var _ = Describe("recovery over the GitHub protocol client", func() {
	var (
		ctx       context.Context
		transport *MockIHTTPClient
		hooks     *intake.GitHubHookDeliveries
	)

	BeforeEach(func() {
		ctx = context.Background()
		transport = NewMockIHTTPClient(gomock.NewController(GinkgoT()))
		client, err := github.NewGitHubClient(transport, "gho_spec")
		Expect(err).NotTo(HaveOccurred())
		hooks, err = intake.NewGitHubHookDeliveries(client)
		Expect(err).NotTo(HaveOccurred())
	})

	It("lists a repository's hooks with the URL each delivers to", func() {
		transport.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
			Expect(request.URL.Path).To(Equal("/repos/candacelabs/csf/hooks"))
			return githubAnswer(stdhttp.StatusOK, hooksAnswer, nil), nil
		})
		listed, err := hooks.ListHooks(ctx, hookRepository)
		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(Equal([]intake.Hook{{ID: hookID, URL: "https://hooks.example.invalid" + intake.WebhookPath}}))
	})

	It("lists a page of a hook's deliveries and the cursor of the next", func() {
		transport.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
			Expect(request.URL.Path).To(Equal("/repos/candacelabs/csf/hooks/501/deliveries"))
			Expect(request.URL.Query().Get("cursor")).To(Equal("v1_9500"))
			return githubAnswer(stdhttp.StatusOK, deliveriesAnswer, stdhttp.Header{"Link": {deliveriesLink}}), nil
		})
		page, next, err := hooks.ListHookDeliveries(ctx, hookRepository, hookID, "v1_9500")
		Expect(err).NotTo(HaveOccurred())
		Expect(next).To(Equal("v1_9000"))
		Expect(page).To(Equal([]intake.HookDelivery{{ID: 9001, GUID: missedDelivery, Event: intake.WebhookIssueComment, DeliveredAt: time.Date(2026, 10, 5, 7, 30, 0, 0, time.UTC)}}))
	})

	It("asks a redelivery of one attempt", func() {
		transport.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
			Expect(request.Method).To(Equal(stdhttp.MethodPost))
			Expect(request.URL.Path).To(Equal("/repos/candacelabs/csf/hooks/501/deliveries/9001/attempts"))
			return githubAnswer(stdhttp.StatusAccepted, `{}`, nil), nil
		})
		Expect(hooks.RedeliverHookDelivery(ctx, hookRepository, hookID, 9001)).To(Succeed())
	})

	It("reports GitHub's refusal, and refuses a repository that is not owner/name", func() {
		transport.EXPECT().Do(gomock.Any()).Return(githubAnswer(stdhttp.StatusNotFound, `{"message":"Not Found"}`, nil), nil)
		_, err := hooks.ListHooks(ctx, hookRepository)
		Expect(err).To(MatchError(ContainSubstring("HTTP 404")))
		_, err = hooks.ListHooks(ctx, "not-a-repository")
		Expect(err).To(HaveOccurred())
		_, err = intake.NewGitHubHookDeliveries(nil)
		Expect(err).To(HaveOccurred())
	})
})
