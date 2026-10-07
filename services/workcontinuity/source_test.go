package workcontinuity_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/net/github"
	"github.com/candacelabs/csf/services/workcontinuity"
)

// githubCall is one request the GitHub protocol client sent.
type githubCall struct {
	Method string
	Path   string
	Query  url.Values
	Body   string
}

// githubSource is the source over the protocol client and a gomock http
// capability whose every request answer answers with a status and body.
func githubSource(answer func(call githubCall) (int, string)) *workcontinuity.GitHubSource {
	GinkgoHelper()
	httpClient := NewMockIHTTPClient(gomock.NewController(GinkgoT()))
	httpClient.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *http.Request) (*http.Response, error) {
		defer GinkgoRecover()
		body := ""
		if request.Body != nil {
			content, err := io.ReadAll(request.Body)
			Expect(err).NotTo(HaveOccurred())
			body = string(content)
		}
		status, reply := answer(githubCall{Method: request.Method, Path: request.URL.Path, Query: request.URL.Query(), Body: body})
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(reply)), Request: request}, nil
	}).AnyTimes()
	client, err := github.NewGitHubClient(httpClient, "ghs_spec")
	Expect(err).NotTo(HaveOccurred())
	return workcontinuity.NewGitHubSource(client)
}

// unreachable is a GitHub the spec must not contact.
func unreachable(message string) func(call githubCall) (int, string) {
	return func(call githubCall) (int, string) {
		Fail(message + ": " + call.Method + " " + call.Path)
		return 0, ""
	}
}

var _ = Describe("GitHub source boundary", func() {
	It("reads the issue and every comment page through the protocol client into the generated projection", func() {
		var calls []string
		source := githubSource(func(call githubCall) (int, string) {
			calls = append(calls, call.Method+" "+call.Path+" "+call.Query.Get("page"))
			switch {
			case call.Path == "/repos/candacelabs/project/issues/1":
				return http.StatusOK, fmt.Sprintf(`{"number":1,"html_url":%q,"title":"Task","body":"scope","state":"open","labels":[]}`, taskURL)
			case call.Query.Get("page") == "1":
				Expect(call.Query.Get("per_page")).To(Equal("100"))
				page := make([]string, 0, 100)
				page = append(page, `{"id":1,"body":"first","user":{"login":"maintainer","id":42},"author_association":"OWNER"}`)
				for len(page) < 100 {
					page = append(page, `{"id":3,"body":"filler","author_association":"NONE"}`)
				}
				return http.StatusOK, "[" + strings.Join(page, ",") + "]"
			}
			return http.StatusOK, `[{"id":2,"body":"second","author_association":"NONE"}]`
		})
		snapshot, err := source.Load(context.Background(), taskURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Comments).To(HaveLen(101))
		Expect(snapshot.Comments[0].User.Login).To(Equal("maintainer"))
		Expect(snapshot.Comments[0].AuthorAssociation).To(Equal("OWNER"))
		Expect(snapshot.Comments[100].Body).To(Equal("second"))
		Expect(calls).To(Equal([]string{
			"GET /repos/candacelabs/project/issues/1 ",
			"GET /repos/candacelabs/project/issues/1/comments 1",
			"GET /repos/candacelabs/project/issues/1/comments 2",
		}))
	})

	It("appends a checkpoint comment and checks it is the one GitHub stored", func() {
		source := githubSource(func(call githubCall) (int, string) {
			Expect(call.Method + " " + call.Path).To(Equal("POST /repos/candacelabs/project/issues/1/comments"))
			Expect(call.Body).To(MatchJSON(`{"body":"checkpoint"}`))
			return http.StatusCreated, fmt.Sprintf(`{"id":9,"body":"checkpoint","html_url":"%s#issuecomment-9"}`, taskURL)
		})
		comment, err := source.Append(context.Background(), taskURL, "checkpoint")
		Expect(err).NotTo(HaveOccurred())
		Expect(comment.Body).To(Equal("checkpoint"))
	})

	It("reports GitHub's refusal as the source's error", func() {
		source := githubSource(func(call githubCall) (int, string) { return http.StatusNotFound, `{"message":"Not Found"}` })
		_, err := source.Load(context.Background(), taskURL)
		Expect(err).To(MatchError(ContainSubstring("HTTP 404")))
	})

	DescribeTable("rejects invalid task destinations without contacting GitHub", func(task string) {
		source := githubSource(unreachable("must not contact GitHub"))
		_, err := source.Load(context.Background(), task)
		Expect(err).To(HaveOccurred())
	}, Entry("other host", "https://example.invalid/org/repo/issues/1"), Entry("query", taskURL+"?x=1"), Entry("credentials", "https://user@github.com/candacelabs/project/issues/1"), Entry("shell", "$(touch /tmp/not-executed)"))
})
