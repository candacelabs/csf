// Copyright 2026 Candace Labs

package githubtools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/csf/githubtools"
	"github.com/candacelabs/csf/csf/prod"
	"github.com/candacelabs/csf/io/net/github"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/services/harness/session"
)

// Integration specs: the tools through MCP and HTTP over the generated
// client and a gomock http capability. Bodies are trimmed from what
// api.github.com returns.
const (
	specToken      = "ghs_spec"
	specAssignment = "6f2c1a10-3b7e-4d6a-9e51-0a1b2c3d4e5f"
	fixtureHead    = "6dcb09b5b57875f334f61aebed695e2e4193db5e"
	fixtureNode    = "MDExOlB1bGxSZXF1ZXN0MQ=="
)

// fixture is one of GitHub's own response examples, copied from the
// description's examples into testdata with its account URLs' /users/ path
// spelled /accounts/: the identifier check reads /users/<name>/ as a home
// directory, and the schema asks only for a URI.
func fixture(name string) string {
	GinkgoHelper()
	content, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	Expect(err).NotTo(HaveOccurred())
	return string(content)
}

// draftPull is the pull request example as a draft.
func draftPull() string {
	GinkgoHelper()
	var pull map[string]any
	Expect(json.Unmarshal([]byte(fixture("pull")), &pull)).To(Succeed())
	pull["draft"] = true
	content, err := json.Marshal(pull)
	Expect(err).NotTo(HaveOccurred())
	return string(content)
}

// heldBranch and freeBranch are the head branches the guard spec names: one
// the merge guard reports held, one it leaves free.
const (
	heldBranch = "p1/held-branch"
	freeBranch = "p1/free-branch"
)

// pullFrom is the pull request example with its head branch renamed, so a
// spec can name the branch a merge guard is asked about.
func pullFrom(branch string) string {
	GinkgoHelper()
	var pull map[string]any
	Expect(json.Unmarshal([]byte(fixture("pull")), &pull)).To(Succeed())
	head, ok := pull["head"].(map[string]any)
	Expect(ok).To(BeTrue(), "the pull fixture carries a head object")
	head["ref"] = branch
	content, err := json.Marshal(pull)
	Expect(err).NotTo(HaveOccurred())
	return string(content)
}

// pullShas is the pull request example with its base and head SHAs renamed, so
// a spec can name the main revision and the merge result the golden metrics
// compare.
func pullShas(base, head string) string {
	GinkgoHelper()
	var pull map[string]any
	Expect(json.Unmarshal([]byte(fixture("pull")), &pull)).To(Succeed())
	baseObject, ok := pull["base"].(map[string]any)
	Expect(ok).To(BeTrue(), "the pull fixture carries a base object")
	headObject, ok := pull["head"].(map[string]any)
	Expect(ok).To(BeTrue(), "the pull fixture carries a head object")
	baseObject["sha"], headObject["sha"] = base, head
	content, err := json.Marshal(pull)
	Expect(err).NotTo(HaveOccurred())
	return string(content)
}

// fixedClock stamps every record with one instant.
type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time                             { return clock.now }
func (clock fixedClock) After(delay time.Duration) <-chan time.Time { return nil }

// inProcess serves a request on a handler without a socket.
type inProcess struct {
	handler http.Handler
	header  http.Header
}

func (transport inProcess) RoundTrip(request *http.Request) (*http.Response, error) {
	for name, values := range transport.header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

// reply is one answer of the GitHub double: the request it expects and
// what it returns.
type reply struct {
	method string
	path   string
	status int
	body   string
}

// answer makes the double answer the replies in order and returns each
// request's body as it was sent.
func answer(client *MockIHTTPClient, replies ...reply) *[]string {
	sent := &[]string{}
	calls := make([]any, 0, len(replies))
	for _, step := range replies {
		calls = append(calls, client.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *http.Request) (*http.Response, error) {
			defer GinkgoRecover()
			Expect(request.Method + " " + request.URL.Path).To(Equal(step.method + " " + step.path))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer " + specToken))
			Expect(request.Header.Get("X-GitHub-Api-Version")).To(Equal(github.APIVersion))
			content := []byte{}
			if request.Body != nil {
				var err error
				content, err = io.ReadAll(request.Body)
				Expect(err).NotTo(HaveOccurred())
			}
			*sent = append(*sent, string(content))
			header := http.Header{"Content-Type": {"application/json"}, "X-Ratelimit-Limit": {"5000"}, "X-Ratelimit-Remaining": {"4990"}}
			return &http.Response{StatusCode: step.status, Header: header, Body: io.NopCloser(strings.NewReader(step.body)), Request: request}, nil
		}))
	}
	gomock.InOrder(calls...)
	return sent
}

// readLog is the host's GitHub log under state.
func readLog(state string) []githubtools.CallRecord {
	GinkgoHelper()
	content, err := os.ReadFile(filepath.Join(state, githubtools.LogFile))
	Expect(err).NotTo(HaveOccurred())
	var records []githubtools.CallRecord
	for _, line := range bytes.Split(bytes.TrimSpace(content), []byte("\n")) {
		var record githubtools.CallRecord
		Expect(json.Unmarshal(line, &record)).To(Succeed())
		records = append(records, record)
	}
	return records
}

var _ = Describe("The GitHub tools", func() {
	var (
		httpClient *MockIHTTPClient
		tools      *githubtools.GitHubTools
		router     *gin.Engine
		state      string
		now        = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
		ctx        = context.Background()
	)

	BeforeEach(func() {
		httpClient = NewMockIHTTPClient(gomock.NewController(GinkgoT()))
		client, err := github.NewGitHubClient(httpClient, specToken)
		Expect(err).NotTo(HaveOccurred())
		state = GinkgoT().TempDir()
		tools, err = githubtools.NewGitHubTools(githubtools.WithClient(client), githubtools.WithStateDirectory(state), githubtools.WithClock(fixedClock{now: now}))
		Expect(err).NotTo(HaveOccurred())
		api, err := csf.New(tools.Tools()...)
		Expect(err).NotTo(HaveOccurred())
		router = httpserver.NewEngine("githubtools-spec")
		tools.Register(router)
		router.Any("/mcp", gin.WrapH(api.MCPHandler()))
	})

	// connect opens an MCP session whose every request carries header.
	connect := func(header http.Header) *mcp.ClientSession {
		GinkgoHelper()
		transport := &mcp.StreamableClientTransport{Endpoint: "http://csf.invalid/mcp", HTTPClient: &http.Client{Transport: inProcess{handler: router, header: header}}, DisableStandaloneSSE: true}
		connected, err := mcp.NewClient(&mcp.Implementation{Name: "githubtools-spec", Version: "1"}, nil).Connect(ctx, transport, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(connected.Close)
		return connected
	}

	// post calls a tool's HTTP route as the operator.
	post := func(route string, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, githubtools.RoutePrefix+route, strings.NewReader(body)))
		return recorder
	}

	It("serves every allowlisted operation and the ready tool over MCP, described from GitHub's description", func() {
		listed, err := connect(nil).ListTools(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		descriptions := map[string]string{}
		for _, tool := range listed.Tools {
			descriptions[tool.Name] = tool.Description
		}
		for _, name := range []string{
			githubtools.ToolIssuesCreate, githubtools.ToolIssuesCreateComment, githubtools.ToolIssuesUpdate, githubtools.ToolIssuesListForRepo,
			githubtools.ToolIssuesGet, githubtools.ToolIssuesListComments, githubtools.ToolPullsCreate, githubtools.ToolPullsMerge,
			githubtools.ToolPullsList, githubtools.ToolPullsGet, githubtools.ToolPullsUpdate, githubtools.ToolChecksListForRef,
			githubtools.ToolGitGetRef, githubtools.ToolReposGetCommit, githubtools.ToolMarkPullRequestReady,
		} {
			Expect(descriptions).To(HaveKey(name))
		}
		Expect(descriptions).To(HaveKeyWithValue(githubtools.ToolIssuesCreate, HavePrefix("Create an issue.")))
		Expect(descriptions).To(HaveKeyWithValue(githubtools.ToolPullsMerge, HaveSuffix("GitHub REST operation pulls/merge.")))
		Expect(descriptions).To(HaveKey(githubtools.ToolMarkPullRequestReady))
	})

	It("files an issue for a virtual session and records the call on the host log and the session", func() {
		run := session.RunDirectory(state, specAssignment)
		Expect(os.MkdirAll(run, 0o700)).To(Succeed())
		Expect(session.WriteRunState(run, &session.RunState{AssignmentID: specAssignment, SessionID: "s", TraceID: "0af7651916cd43dd8448eb211c80319c", SpanID: "b7ad6b7169203331"})).To(Succeed())
		sent := answer(httpClient, reply{method: http.MethodPost, path: "/repos/o/r/issues", status: http.StatusCreated, body: fixture("issue")})

		result, err := connect(http.Header{githubtools.AssignmentHeader: {specAssignment}}).CallTool(ctx, &mcp.CallToolParams{
			Name: githubtools.ToolIssuesCreate, Arguments: map[string]any{"owner": "o", "repo": "r", "body": map[string]any{"title": "T", "body": "B"}},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeFalse(), "%s", result.Content[0].(*mcp.TextContent).Text)
		Expect(result.StructuredContent).To(HaveKeyWithValue("number", BeNumerically("==", 1347)))
		Expect((*sent)[0]).To(MatchJSON(`{"title":"T","body":"B"}`))

		Expect(readLog(state)).To(Equal([]githubtools.CallRecord{{
			Time: now, Operation: githubtools.ToolIssuesCreate, Actor: githubtools.ActorVirtualSession, Assignment: specAssignment,
			Repository: "o/r", Outcome: githubtools.OutcomeOK, RateLimit: 5000, RateRemaining: 4990,
		}}))
		records, err := session.ReadRecords(run, func(record *session.Record) bool { return record.EventType == session.EventTypeGitHubCall })
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(1))
		Expect(records[0].Message).To(Equal("github call: IssuesCreate"))
	})

	It("merges with the policy's method whatever the caller asked, and records the ruling", func() {
		sent := answer(httpClient, reply{method: http.MethodPut, path: "/repos/o/r/pulls/9/merge", status: http.StatusOK, body: fixture("merged")})
		recorder := post("pulls/merge", `{"owner":"o","repo":"r","pull_number":9,"body":{"merge_method":"merge","sha":"`+fixtureHead+`"}}`)
		Expect(recorder.Code).To(Equal(http.StatusOK), recorder.Body.String())
		Expect(recorder.Body.String()).To(MatchJSON(fixture("merged")))
		Expect((*sent)[0]).To(MatchJSON(`{"merge_method":"squash","sha":"` + fixtureHead + `"}`))
		records := readLog(state)
		Expect(records).To(HaveLen(1))
		Expect(records[0].Actor).To(Equal(githubtools.ActorOperator))
		Expect(records[0].Number).To(Equal(9))
		Expect(records[0].Policy).To(Equal(&githubtools.DefaultMergePolicy))
		Expect(records[0].Policy.Ruling).To(ContainSubstring("NO CHECKS"))
	})

	It("refuses a merge a policy that requires checks finds a failing check for", func() {
		client, err := github.NewGitHubClient(httpClient, specToken)
		Expect(err).NotTo(HaveOccurred())
		strict := githubtools.DefaultMergePolicy
		strict.RequireChecks = true
		tools, err = githubtools.NewGitHubTools(githubtools.WithClient(client), githubtools.WithStateDirectory(state), githubtools.WithMergePolicy(strict))
		Expect(err).NotTo(HaveOccurred())
		router = httpserver.NewEngine("githubtools-strict")
		tools.Register(router)
		answer(httpClient,
			reply{method: http.MethodGet, path: "/repos/o/r/pulls/9", status: http.StatusOK, body: fixture("pull")},
			reply{method: http.MethodGet, path: "/repos/o/r/commits/" + fixtureHead + "/check-runs", status: http.StatusOK, body: `{"total_count":1,"check_runs":[{"name":"ci","status":"completed","conclusion":"failure"}]}`})
		recorder := post("pulls/merge", `{"owner":"o","repo":"r","pull_number":9,"body":{}}`)
		Expect(recorder.Code).To(Equal(http.StatusInternalServerError))
		Expect(recorder.Body.String()).To(ContainSubstring("ci is completed"))
		Expect(readLog(state)[0].Outcome).To(Equal(githubtools.OutcomeFailed))
	})

	It("refuses a merge of a branch the merge guard reports held, and allows one it leaves free", func() {
		client, err := github.NewGitHubClient(httpClient, specToken)
		Expect(err).NotTo(HaveOccurred())
		guard := func(_ context.Context, branch string) (string, bool, error) {
			if branch == heldBranch {
				return "waits for a ruling", true, nil
			}
			return "", false, nil
		}
		tools, err = githubtools.NewGitHubTools(githubtools.WithClient(client), githubtools.WithStateDirectory(state), githubtools.WithMergeGuard(guard))
		Expect(err).NotTo(HaveOccurred())
		router = httpserver.NewEngine("githubtools-guard")
		tools.Register(router)

		answer(httpClient, reply{method: http.MethodGet, path: "/repos/o/r/pulls/9", status: http.StatusOK, body: pullFrom(heldBranch)})
		held := post("pulls/merge", `{"owner":"o","repo":"r","pull_number":9,"body":{}}`)
		Expect(held.Code).To(Equal(http.StatusInternalServerError))
		Expect(held.Body.String()).To(ContainSubstring("this branch is held for waits for a ruling; push to it, do not merge it"))
		Expect(readLog(state)[0].Outcome).To(Equal(githubtools.OutcomeFailed))

		answer(httpClient,
			reply{method: http.MethodGet, path: "/repos/o/r/pulls/9", status: http.StatusOK, body: pullFrom(freeBranch)},
			reply{method: http.MethodPut, path: "/repos/o/r/pulls/9/merge", status: http.StatusOK, body: fixture("merged")})
		free := post("pulls/merge", `{"owner":"o","repo":"r","pull_number":9,"body":{}}`)
		Expect(free.Code).To(Equal(http.StatusOK), free.Body.String())
		Expect(free.Body.String()).To(MatchJSON(fixture("merged")))
	})

	It("refuses a merge the golden metrics refuse, and allows one they pass", func() {
		client, err := github.NewGitHubClient(httpClient, specToken)
		Expect(err).NotTo(HaveOccurred())
		const (
			baseSha = "1111111111111111111111111111111111111111"
			headSha = "2222222222222222222222222222222222222222"
		)
		readings := map[string]prod.Reading{
			baseSha: {Chief: map[string]map[string]int{"a": {}}},
			headSha: {Chief: map[string]map[string]int{"a": {"cli_purpose_declared": 1}}},
		}
		measurer := func(_ context.Context, _ string, _ string, revision string) (prod.Reading, error) {
			return readings[revision], nil
		}
		tools, err = githubtools.NewGitHubTools(githubtools.WithClient(client), githubtools.WithStateDirectory(state), githubtools.WithGoldenMetrics(measurer))
		Expect(err).NotTo(HaveOccurred())
		router = httpserver.NewEngine("githubtools-golden")
		tools.Register(router)

		answer(httpClient, reply{method: http.MethodGet, path: "/repos/o/r/pulls/9", status: http.StatusOK, body: pullShas(baseSha, headSha)})
		refused := post("pulls/merge", `{"owner":"o","repo":"r","pull_number":9,"body":{}}`)
		Expect(refused.Code).To(Equal(http.StatusInternalServerError))
		Expect(refused.Body.String()).To(ContainSubstring("the golden metrics refuse the merge"))
		Expect(readLog(state)[0].Outcome).To(Equal(githubtools.OutcomeFailed))

		// The verdict is written either way, for the ops view panel.
		content, err := os.ReadFile(filepath.Join(state, prod.FileName))
		Expect(err).NotTo(HaveOccurred())
		var snapshot prod.Snapshot
		Expect(json.Unmarshal(content, &snapshot)).To(Succeed())
		Expect(snapshot).To(And(
			HaveField("Refused", BeTrue()),
			HaveField("Reasons", ContainElement(prod.MergeGateSeedBreaks)),
		))

		// A merge the metrics pass proceeds, and its clean verdict is written.
		readings[headSha] = prod.Reading{Chief: map[string]map[string]int{"a": {}, "b": {}}}
		answer(httpClient,
			reply{method: http.MethodGet, path: "/repos/o/r/pulls/9", status: http.StatusOK, body: pullShas(baseSha, headSha)},
			reply{method: http.MethodPut, path: "/repos/o/r/pulls/9/merge", status: http.StatusOK, body: fixture("merged")})
		merged := post("pulls/merge", `{"owner":"o","repo":"r","pull_number":9,"body":{}}`)
		Expect(merged.Code).To(Equal(http.StatusOK), merged.Body.String())
		Expect(merged.Body.String()).To(MatchJSON(fixture("merged")))
		content, err = os.ReadFile(filepath.Join(state, prod.FileName))
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(content, &snapshot)).To(Succeed())
		Expect(snapshot).To(And(
			HaveField("Refused", BeFalse()),
			HaveField("Reasons", BeEmpty()),
			HaveField("SeedFilesMerge", Equal(2)),
		))
	})

	It("marks a draft ready through GraphQL", func() {
		sent := answer(httpClient,
			reply{method: http.MethodGet, path: "/repos/o/r/pulls/9", status: http.StatusOK, body: draftPull()},
			reply{method: http.MethodPost, path: "/graphql", status: http.StatusOK, body: `{"data":{"markPullRequestReadyForReview":{"pullRequest":{"isDraft":false}}}}`})
		recorder := post("pulls/ready", `{"owner":"o","repo":"r","pull_number":9}`)
		Expect(recorder.Code).To(Equal(http.StatusOK), recorder.Body.String())
		Expect(recorder.Body.String()).To(MatchJSON(`{"number":9,"url":"https://github.com/candacelabs/Hello-World/pull/1347","draft":false}`))
		Expect((*sent)[1]).To(ContainSubstring(`"variables":{"id":"` + fixtureNode + `"}`))
	})

	It("reports a GraphQL error as a failed call", func() {
		answer(httpClient,
			reply{method: http.MethodGet, path: "/repos/o/r/pulls/9", status: http.StatusOK, body: draftPull()},
			reply{method: http.MethodPost, path: "/graphql", status: http.StatusOK, body: `{"errors":[{"message":"Resource not accessible by integration"}]}`})
		recorder := post("pulls/ready", `{"owner":"o","repo":"r","pull_number":9}`)
		Expect(recorder.Body.String()).To(ContainSubstring("Resource not accessible by integration"))
		Expect(readLog(state)[0].Outcome).To(Equal(githubtools.OutcomeFailed))
	})

	It("answers GitHub's not found as not found, records it failed, and an MCP caller reads the error", func() {
		answer(httpClient, reply{method: http.MethodGet, path: "/repos/o/r/issues/404", status: http.StatusNotFound, body: `{"message":"Not Found"}`})
		result, err := connect(nil).CallTool(ctx, &mcp.CallToolParams{Name: githubtools.ToolIssuesGet, Arguments: map[string]any{"owner": "o", "repo": "r", "issue_number": 404}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeTrue())
		Expect(readLog(state)[0]).To(And(
			HaveField("Outcome", githubtools.OutcomeFailed),
			HaveField("Error", ContainSubstring("HTTP 404")),
		))
		answer(httpClient, reply{method: http.MethodGet, path: "/repos/o/r/issues/404", status: http.StatusNotFound, body: `{"message":"Not Found"}`})
		Expect(post("issues/get", `{"owner":"o","repo":"r","issue_number":404}`).Code).To(Equal(http.StatusNotFound))
	})

	It("wraps a list result in an object and passes the query parameters", func() {
		var query string
		httpClient.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *http.Request) (*http.Response, error) {
			query = request.URL.RawQuery
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(fixture("issues"))), Request: request}, nil
		})
		recorder := post("issues/list-for-repo", `{"owner":"o","repo":"r","params":{"state":"all","labels":"bug"}}`)
		Expect(recorder.Code).To(Equal(http.StatusOK), recorder.Body.String())
		var listed struct {
			Items []map[string]any `json:"items"`
		}
		Expect(json.Unmarshal(recorder.Body.Bytes(), &listed)).To(Succeed())
		Expect(listed.Items).To(HaveLen(1))
		Expect(query).To(And(ContainSubstring("state=all"), ContainSubstring("labels=bug")))
	})

	It("refuses a call that names no repository without reaching GitHub", func() {
		recorder := post("pulls/get", `{"pull_number":9}`)
		Expect(recorder.Code).To(Equal(http.StatusBadRequest))
		Expect(readLog(state)[0].Outcome).To(Equal(githubtools.OutcomeFailed))
	})

	It("counts the calls by operation, actor and outcome with the latest rate limit", func() {
		answer(httpClient, reply{method: http.MethodPut, path: "/repos/o/r/pulls/9/merge", status: http.StatusOK, body: fixture("merged")})
		post("pulls/merge", `{"owner":"o","repo":"r","pull_number":9,"body":{}}`)
		post("pulls/get", `{}`)
		counts, err := githubtools.CountCalls(state)
		Expect(err).NotTo(HaveOccurred())
		Expect(counts).To(Equal(githubtools.CallCounts{
			Calls: map[githubtools.CallKey]int{
				{Operation: githubtools.ToolPullsMerge, Actor: githubtools.ActorOperator, Outcome: githubtools.OutcomeOK}:   1,
				{Operation: githubtools.ToolPullsGet, Actor: githubtools.ActorOperator, Outcome: githubtools.OutcomeFailed}: 1,
			},
			RateLimit: 5000, RateRemaining: 4990,
		}))
		empty, err := githubtools.CountCalls(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		Expect(empty.Calls).To(BeEmpty())
	})

	Describe("construction", func() {
		It("requires the client and the state directory, and a policy with a method and its ruling", func() {
			client, err := github.NewGitHubClient(httpClient, specToken)
			Expect(err).NotTo(HaveOccurred())
			_, err = githubtools.NewGitHubTools(githubtools.WithStateDirectory(state))
			Expect(err).To(MatchError(githubtools.ErrNoClient))
			_, err = githubtools.NewGitHubTools(githubtools.WithClient(client))
			Expect(err).To(MatchError(githubtools.ErrNoStateDirectory))
			_, err = githubtools.NewGitHubTools(githubtools.WithClient(client), githubtools.WithStateDirectory(state), nil)
			Expect(err).To(MatchError(githubtools.ErrInvalidOption))
			_, err = githubtools.NewGitHubTools(githubtools.WithClient(client), githubtools.WithStateDirectory(state), githubtools.WithMergePolicy(githubtools.MergePolicy{Method: "squash"}))
			Expect(err).To(MatchError(githubtools.ErrInvalidOption))
			_, err = githubtools.NewGitHubTools(githubtools.WithClient(client), githubtools.WithStateDirectory(state), githubtools.WithMergeGuard(nil))
			Expect(err).To(MatchError(githubtools.ErrInvalidOption))
			_, err = githubtools.NewGitHubTools(githubtools.WithClient(client), githubtools.WithStateDirectory(state), githubtools.WithGoldenMetrics(nil))
			Expect(err).To(MatchError(githubtools.ErrInvalidOption))
		})

		It("builds the protocol client only with the http capability and a token", func() {
			_, err := github.NewGitHubClient(nil, specToken)
			Expect(err).To(MatchError(github.ErrNoHTTPClient))
			_, err = github.NewGitHubClient(httpClient, " ")
			Expect(err).To(MatchError(github.ErrNoToken))
		})
	})
})
