package workcontinuity_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/workcontinuity"
	workv1 "github.com/candacelabs/csf/proto/candace/work/v1"
)

const (
	httpSourceBudget = 10 * time.Second
	httpSourceBytes  = 16 << 20
	httpIssuePath    = "/repos/example/project/issues/1"
	httpCommentPath  = httpIssuePath + "/comments"
)

// The caller owns authentication and transport. Only the origin is rewritten;
// Clone retains the context and every path/query the source actually requested.
type httpSourceTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (transport *httpSourceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || request.URL.Host != "api.github.com" {
		return nil, fmt.Errorf("unexpected GitHub API origin: %s", request.URL)
	}
	local := request.Clone(request.Context())
	local.URL.Scheme, local.URL.Host = transport.target.Scheme, transport.target.Host
	local.Host = transport.target.Host
	local.Header.Set("Authorization", "Bearer caller-owned-token")
	return transport.base.RoundTrip(local)
}

func newHTTPSourceFixture(handler http.HandlerFunc) *workcontinuity.HTTPGitHubSource {
	GinkgoHelper()
	server := httptest.NewServer(handler)
	DeferCleanup(server.Close)
	target, err := url.Parse(server.URL)
	Expect(err).NotTo(HaveOccurred())
	client := &http.Client{
		Transport: &httpSourceTransport{target: target, base: server.Client().Transport},
		Timeout:   httpSourceBudget,
	}
	DeferCleanup(client.CloseIdleConnections)
	source, err := workcontinuity.NewHTTPGitHubSource(client)
	Expect(err).NotTo(HaveOccurred())
	return source
}

func httpIssueJSON(number int64, address, state string) string {
	return fmt.Sprintf(`{"number":%d,"html_url":%q,"title":"Task","body":"scope","state":%q,"updated_at":"2026-09-17T00:00:00Z","labels":[]}`, number, address, state)
}

func httpCommentJSON(id int64, address, body string) string {
	return fmt.Sprintf(`{"id":%d,"html_url":%q,"body":%q,"user":{"login":"maintainer","id":42},"author_association":"OWNER","created_at":"2026-09-17T00:00:00Z","updated_at":"2026-09-17T01:00:00Z"}`, id, address, body)
}

func httpCommentPage(first, count int) string {
	comments := make([]string, count)
	for index := range comments {
		id := first + index
		comments[index] = httpCommentJSON(int64(id), taskURL+"#issuecomment-"+strconv.Itoa(id), "comment "+strconv.Itoa(id))
	}
	return "[" + strings.Join(comments, ",") + "]"
}

func writeHTTPJSON(writer http.ResponseWriter, status int, body string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = io.WriteString(writer, body)
}

func loadHTTPPayloads(issue, comments string) (*workv1.SourceSnapshot, error) {
	GinkgoHelper()
	source := newHTTPSourceFixture(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == httpIssuePath {
			writeHTTPJSON(writer, http.StatusOK, issue)
			return
		}
		writeHTTPJSON(writer, http.StatusOK, comments)
	})
	return source.Load(context.Background(), taskURL)
}

var _ = Describe("HTTP GitHub source", func() {
	It("requires a caller-owned HTTP client", func() {
		_, err := workcontinuity.NewHTTPGitHubSource(nil)
		Expect(err).To(HaveOccurred())
	})

	It("rejects invalid canonical URLs before either source operation performs I/O", func() {
		var calls atomic.Int32
		source := newHTTPSourceFixture(func(writer http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			writer.WriteHeader(http.StatusInternalServerError)
		})
		_, err := source.Load(context.Background(), taskURL+"?")
		Expect(err).To(HaveOccurred())
		_, err = source.Append(context.Background(), taskURL+"?", "body")
		Expect(err).To(HaveOccurred())
		Expect(calls.Load()).To(BeZero())
	})

	It("loads the issue and every comment page through caller authentication", func() {
		var calls atomic.Int32
		source := newHTTPSourceFixture(func(writer http.ResponseWriter, request *http.Request) {
			defer GinkgoRecover()
			calls.Add(1)
			Expect(request.Method).To(Equal(http.MethodGet))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer caller-owned-token"))
			Expect(request.Header.Get("Accept")).To(Equal("application/vnd.github+json"))
			if request.URL.Path == httpIssuePath {
				Expect(request.URL.RawQuery).To(BeEmpty())
				writeHTTPJSON(writer, http.StatusOK, httpIssueJSON(1, taskURL, "open"))
				return
			}
			Expect(request.URL.Path).To(Equal(httpCommentPath))
			Expect(request.URL.Query().Get("per_page")).To(Equal("100"))
			switch request.URL.Query().Get("page") {
			case "1":
				writeHTTPJSON(writer, http.StatusOK, httpCommentPage(1, 100))
			case "2":
				writeHTTPJSON(writer, http.StatusOK, httpCommentPage(101, 1))
			default:
				Fail("unexpected comments page")
			}
		})
		snapshot, err := source.Load(context.Background(), taskURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(calls.Load()).To(Equal(int32(3)))
		Expect(snapshot.Issue.Number).To(Equal(int64(1)))
		Expect(snapshot.Issue.HtmlUrl).To(Equal(taskURL))
		Expect(snapshot.Issue.Title).To(Equal("Task"))
		Expect(snapshot.Issue.Body).To(Equal("scope"))
		Expect(snapshot.Issue.UpdatedAt.AsTime().UTC().Format(time.RFC3339)).To(Equal("2026-09-17T00:00:00Z"))
		Expect(snapshot.Comments).To(HaveLen(101))
		last := snapshot.Comments[100]
		Expect(last.Id).To(Equal(int64(101)))
		Expect(last.HtmlUrl).To(Equal(taskURL + "#issuecomment-101"))
		Expect(last.Body).To(Equal("comment 101"))
		Expect(last.User.Login).To(Equal("maintainer"))
		Expect(last.AuthorAssociation).To(Equal("OWNER"))
		Expect(last.CreatedAt.AsTime().UTC().Format(time.RFC3339)).To(Equal("2026-09-17T00:00:00Z"))
		Expect(last.UpdatedAt.AsTime().UTC().Format(time.RFC3339)).To(Equal("2026-09-17T01:00:00Z"))
	})

	DescribeTable("rejects invalid issue projections", func(issue string) {
		snapshot, err := loadHTTPPayloads(issue, "[]")
		Expect(err).To(HaveOccurred())
		Expect(snapshot).To(BeNil())
	},
		Entry("malformed JSON", "{"),
		Entry("null issue", "null"),
		Entry("zero number", httpIssueJSON(0, taskURL, "open")),
		Entry("number differs from requested issue", httpIssueJSON(2, taskURL, "open")),
		Entry("URL differs from requested issue", httpIssueJSON(1, taskURL+"0", "open")),
		Entry("unknown state", httpIssueJSON(1, taskURL, "unknown")),
	)

	DescribeTable("rejects malformed or noncanonical comment records and pages", func(comments string) {
		snapshot, err := loadHTTPPayloads(httpIssueJSON(1, taskURL, "closed"), comments)
		Expect(err).To(HaveOccurred())
		Expect(snapshot).To(BeNil())
	},
		Entry("malformed page", "["),
		Entry("null page", "null"),
		Entry("object page", "{}"),
		Entry("trailing second page", "[][]"),
		Entry("null comment", "[null]"),
		Entry("empty comment", "[{}]"),
		Entry("zero ID", "["+httpCommentJSON(0, taskURL+"#issuecomment-0", "body")+"]"),
		Entry("negative ID", "["+httpCommentJSON(-1, taskURL+"#issuecomment--1", "body")+"]"),
		Entry("missing URL", "["+httpCommentJSON(1, "", "body")+"]"),
		Entry("different issue URL", "["+httpCommentJSON(1, taskURL+"0#issuecomment-1", "body")+"]"),
		Entry("URL has a different comment ID", "["+httpCommentJSON(1, taskURL+"#issuecomment-2", "body")+"]"),
		Entry("noncanonical comment fragment", "["+httpCommentJSON(1, taskURL+"#issuecomment-01", "body")+"]"),
		Entry("101 comments in one page", httpCommentPage(1, 101)),
		Entry("duplicate ID and URL in one page", "["+httpCommentJSON(1, taskURL+"#issuecomment-1", "one")+","+httpCommentJSON(1, taskURL+"#issuecomment-1", "two")+"]"),
		Entry("different IDs sharing one URL", "["+httpCommentJSON(1, taskURL+"#issuecomment-1", "one")+","+httpCommentJSON(2, taskURL+"#issuecomment-1", "two")+"]"),
	)

	It("rejects a comment repeated across pages", func() {
		var calls atomic.Int32
		source := newHTTPSourceFixture(func(writer http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			body := httpIssueJSON(1, taskURL, "open")
			if request.URL.Path == httpCommentPath {
				body = httpCommentPage(1, 100)
				if request.URL.Query().Get("page") == "2" {
					body = httpCommentPage(100, 1)
				}
			}
			writeHTTPJSON(writer, http.StatusOK, body)
		})
		snapshot, err := source.Load(context.Background(), taskURL)
		Expect(err).To(HaveOccurred())
		Expect(snapshot).To(BeNil())
		Expect(calls.Load()).To(Equal(int32(3)))
	})
})

var _ = Describe("HTTP GitHub source resource boundaries", func() {
	It("accepts exactly the total byte budget", func() {
		issue := httpIssueJSON(1, taskURL, "open")
		issue += strings.Repeat(" ", httpSourceBytes-len(issue)-len("[]"))
		snapshot, err := loadHTTPPayloads(issue, "[]")
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Comments).To(BeEmpty())
	})

	DescribeTable("rejects responses exceeding the per-load byte budget", func(cumulative bool) {
		issue := httpIssueJSON(1, taskURL, "open")
		comments := "[]"
		if cumulative {
			issue += strings.Repeat(" ", httpSourceBytes/2-len(issue))
			comments += strings.Repeat(" ", httpSourceBytes/2)
		} else {
			issue += strings.Repeat(" ", httpSourceBytes-len(issue)+1)
		}
		snapshot, err := loadHTTPPayloads(issue, comments)
		Expect(err).To(HaveOccurred())
		Expect(snapshot).To(BeNil())
	}, Entry("one oversized response", false), Entry("multiple individually valid responses", true))

	DescribeTable("bounds comment pagination at 100 pages", func(terminates bool) {
		var calls atomic.Int32
		source := newHTTPSourceFixture(func(writer http.ResponseWriter, request *http.Request) {
			defer GinkgoRecover()
			calls.Add(1)
			if request.URL.Path == httpIssuePath {
				writeHTTPJSON(writer, http.StatusOK, httpIssueJSON(1, taskURL, "open"))
				return
			}
			page, err := strconv.Atoi(request.URL.Query().Get("page"))
			Expect(err).NotTo(HaveOccurred())
			Expect(page).To(BeNumerically("<=", 100))
			count := 100
			if terminates && page == 100 {
				count = 0
			}
			writeHTTPJSON(writer, http.StatusOK, httpCommentPage((page-1)*100+1, count))
		})
		snapshot, err := source.Load(context.Background(), taskURL)
		Expect(calls.Load()).To(Equal(int32(101)))
		if terminates {
			Expect(err).NotTo(HaveOccurred())
			Expect(snapshot.Comments).To(HaveLen(9900))
		} else {
			Expect(err).To(HaveOccurred())
			Expect(snapshot).To(BeNil())
		}
	}, Entry("last allowed page terminates", true), Entry("never asks for page 101", false))

	DescribeTable("propagates cancellation through caller transport to the active request", func(appendComment bool) {
		ctx, cancel := context.WithCancel(context.Background())
		received, canceled := make(chan struct{}), make(chan struct{})
		shutdown := make(chan struct{})
		result := make(chan error, 1)
		source := newHTTPSourceFixture(func(writer http.ResponseWriter, request *http.Request) {
			defer GinkgoRecover()
			_, err := io.Copy(io.Discard, request.Body)
			Expect(err).NotTo(HaveOccurred())
			close(received)
			select {
			case <-request.Context().Done():
				close(canceled)
			case <-shutdown:
			}
		})
		DeferCleanup(func() {
			close(shutdown)
			cancel()
		})
		go func() {
			if appendComment {
				_, err := source.Append(ctx, taskURL, "body")
				result <- err
				return
			}
			_, err := source.Load(ctx, taskURL)
			result <- err
		}()
		Eventually(received, httpSourceBudget).Should(BeClosed())
		cancel()
		Eventually(canceled, httpSourceBudget).Should(BeClosed())
		var err error
		Eventually(result, httpSourceBudget).Should(Receive(&err))
		Expect(err).To(MatchError(context.Canceled))
	}, Entry("load", false), Entry("append", true))
})

var _ = Describe("HTTP GitHub source HTTP failures", func() {
	DescribeTable("requires method-specific success status and never retries an append", func(stage string, status int) {
		var calls atomic.Int32
		source := newHTTPSourceFixture(func(writer http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			if stage == "comments" && request.URL.Path == httpIssuePath {
				writeHTTPJSON(writer, http.StatusOK, httpIssueJSON(1, taskURL, "open"))
				return
			}
			body := httpIssueJSON(1, taskURL, "open")
			if stage == "comments" {
				body = "[]"
			} else if stage == "append" {
				body = httpCommentJSON(1, taskURL+"#issuecomment-1", "body")
			}
			writeHTTPJSON(writer, status, body)
		})
		var err error
		if stage == "append" {
			_, err = source.Append(context.Background(), taskURL, "body")
		} else {
			_, err = source.Load(context.Background(), taskURL)
		}
		Expect(err).To(HaveOccurred())
		expectedCalls := int32(1)
		if stage == "comments" {
			expectedCalls = 2
		}
		Expect(calls.Load()).To(Equal(expectedCalls))
	},
		Entry("issue GET rejects 201", "issue", http.StatusCreated),
		Entry("comments GET rejects 201", "comments", http.StatusCreated),
		Entry("POST rejects 200", "append", http.StatusOK),
		Entry("issue not found", "issue", http.StatusNotFound),
		Entry("comments rate limited", "comments", http.StatusTooManyRequests),
		Entry("append unauthorized", "append", http.StatusUnauthorized),
		Entry("append server failure", "append", http.StatusInternalServerError),
	)

	DescribeTable("returns body read failures without retrying", func(appendComment bool) {
		var calls atomic.Int32
		source := newHTTPSourceFixture(func(writer http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			writer.Header().Set("Content-Length", "32")
			status := http.StatusOK
			if appendComment {
				status = http.StatusCreated
			}
			writer.WriteHeader(status)
			_, _ = io.WriteString(writer, "{")
		})
		var err error
		if appendComment {
			_, err = source.Append(context.Background(), taskURL, "body")
		} else {
			_, err = source.Load(context.Background(), taskURL)
		}
		Expect(err).To(MatchError(io.ErrUnexpectedEOF))
		Expect(calls.Load()).To(Equal(int32(1)))
	}, Entry("load", false), Entry("append", true))
})

var _ = Describe("HTTP GitHub source append verification", func() {
	It("sends exactly the requested body once and accepts the canonical receipt", func() {
		body := "line one\n\"quoted\" \u2602 <literal>"
		var calls atomic.Int32
		source := newHTTPSourceFixture(func(writer http.ResponseWriter, request *http.Request) {
			defer GinkgoRecover()
			calls.Add(1)
			Expect(request.Method).To(Equal(http.MethodPost))
			Expect(request.URL.Path).To(Equal(httpCommentPath))
			Expect(request.URL.RawQuery).To(BeEmpty())
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer caller-owned-token"))
			Expect(request.Header.Get("Content-Type")).To(Equal("application/json"))
			var sent map[string]json.RawMessage
			Expect(json.NewDecoder(request.Body).Decode(&sent)).To(Succeed())
			Expect(sent).To(HaveLen(1))
			var content string
			Expect(json.Unmarshal(sent["body"], &content)).To(Succeed())
			Expect(content).To(Equal(body))
			writeHTTPJSON(writer, http.StatusCreated, httpCommentJSON(123, taskURL+"#issuecomment-123", body))
		})
		comment, err := source.Append(context.Background(), taskURL, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(calls.Load()).To(Equal(int32(1)))
		Expect(comment.Id).To(Equal(int64(123)))
		Expect(comment.HtmlUrl).To(Equal(taskURL + "#issuecomment-123"))
		Expect(comment.Body).To(Equal(body))
		Expect(comment.User.Login).To(Equal("maintainer"))
	})

	DescribeTable("refuses unverifiable receipts without automatically posting again", func(response string) {
		var calls atomic.Int32
		source := newHTTPSourceFixture(func(writer http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			writeHTTPJSON(writer, http.StatusCreated, response)
		})
		comment, err := source.Append(context.Background(), taskURL, "body")
		Expect(err).To(HaveOccurred())
		Expect(comment).To(BeNil())
		Expect(calls.Load()).To(Equal(int32(1)))
	},
		Entry("malformed JSON", "{"),
		Entry("null receipt", "null"),
		Entry("zero ID", httpCommentJSON(0, taskURL+"#issuecomment-0", "body")),
		Entry("negative ID", httpCommentJSON(-1, taskURL+"#issuecomment--1", "body")),
		Entry("body mismatch", httpCommentJSON(1, taskURL+"#issuecomment-1", "different")),
		Entry("missing URL", httpCommentJSON(1, "", "body")),
		Entry("different issue", httpCommentJSON(1, taskURL+"0#issuecomment-1", "body")),
		Entry("different ID in URL", httpCommentJSON(1, taskURL+"#issuecomment-2", "body")),
		Entry("leading zero in URL ID", httpCommentJSON(1, taskURL+"#issuecomment-01", "body")),
		Entry("oversized response", httpCommentJSON(1, taskURL+"#issuecomment-1", "body")+strings.Repeat(" ", httpSourceBytes)),
	)
})

var _ = Describe("Canonical task URL validation", func() {
	It("accepts canonical positive issue identities including the largest int64", func() {
		Expect(workcontinuity.ValidateTaskURL(taskURL)).To(Succeed())
		Expect(workcontinuity.ValidateTaskURL("https://github.com/example/project/issues/9223372036854775807")).To(Succeed())
	})

	DescribeTable("rejects noncanonical issue destinations", func(address string) {
		Expect(workcontinuity.ValidateTaskURL(address)).To(HaveOccurred())
	},
		Entry("encoded owner", "https://github.com/%65xample/project/issues/1"),
		Entry("encoded issue digit", "https://github.com/example/project/issues/%31"),
		Entry("encoded separator", "https://github.com/example%2fproject/issues/1"),
		Entry("encoded dot segment", "https://github.com/example/%2e%2e/issues/1"),
		Entry("empty query", taskURL+"?"),
		Entry("nonempty query", taskURL+"?x=1"),
		Entry("empty fragment", taskURL+"#"),
		Entry("nonempty fragment", taskURL+"#issuecomment-1"),
		Entry("dot owner", "https://github.com/./project/issues/1"),
		Entry("parent owner", "https://github.com/../project/issues/1"),
		Entry("dot repository", "https://github.com/example/./issues/1"),
		Entry("parent repository", "https://github.com/example/../issues/1"),
		Entry("insecure scheme", "http://github.com/example/project/issues/1"),
		Entry("wrong scheme", "ftp://github.com/example/project/issues/1"),
		Entry("wrong host", "https://example.invalid/example/project/issues/1"),
		Entry("API host", "https://api.github.com/example/project/issues/1"),
		Entry("noncanonical host case", "https://GitHub.com/example/project/issues/1"),
		Entry("explicit port", "https://github.com:443/example/project/issues/1"),
		Entry("credentials", "https://user:password@github.com/example/project/issues/1"),
		Entry("empty user credentials", "https://@github.com/example/project/issues/1"),
		Entry("overflow int64", "https://github.com/example/project/issues/9223372036854775808"),
		Entry("huge issue number", "https://github.com/example/project/issues/184467440737095516160"),
		Entry("zero issue number", "https://github.com/example/project/issues/0"),
		Entry("negative issue number", "https://github.com/example/project/issues/-1"),
		Entry("leading zero", "https://github.com/example/project/issues/01"),
		Entry("trailing slash", taskURL+"/"),
	)
})
