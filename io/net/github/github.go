// Copyright 2026 Candace Labs

// Package github is CSF's GitHub protocol client: a library compiled into
// the binary that uses it, granted the http capability, which never dials
// on its own and starts no goroutine. client.gen.go is oapi-codegen's client
// for the operations operations.txt names, generated from GitHub's own
// OpenAPI description (api.github.com.json, filtered from the pinned
// upstream by generate.sh); csf/githubtools serves the same operations as
// MCP tools, generated from the same file.
//
// This file is the handwritten rest: the client every request of which is
// authenticated with the token the binary read through the config
// capability, the rate limit a response reports, and marking a draft ready,
// which GitHub offers in GraphQL only.
//
//go:generate bash generate.sh write
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	iohttp "github.com/candacelabs/csf/io/net/http"
)

const (
	// Server is GitHub's REST endpoint.
	Server = "https://api.github.com"
	// APIVersion is the REST API version api.github.com.json describes.
	APIVersion = "2026-03-10"

	graphQLPath     = "/graphql"
	readyMutation   = "mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { isDraft } } }"
	variableID      = "id"
	headerAuth      = "Authorization"
	headerAccept    = "Accept"
	headerVersion   = "X-GitHub-Api-Version"
	headerType      = "Content-Type"
	headerLimit     = "X-Ratelimit-Limit"
	headerRemaining = "X-Ratelimit-Remaining"
	headerReset     = "X-Ratelimit-Reset"
	bearer          = "Bearer "
	mediaGitHub     = "application/vnd.github+json"
	mediaJSON       = "application/json"
	maxAnswerBytes  = 1 << 20
)

var (
	// ErrNoHTTPClient reports a client built without the http capability.
	ErrNoHTTPClient = errors.New("github: an HTTP client capability is required")
	// ErrNoToken reports a client built without a token.
	ErrNoToken = errors.New("github: a token is required")
)

// GitHubClient is the generated client, authenticated, plus the operations
// the REST description lacks.
type GitHubClient struct {
	*ClientWithResponses
	http  iohttp.IHTTPClient
	token string
}

// NewGitHubClient builds the client whose every request httpClient sends,
// authenticated with token, against [Server].
func NewGitHubClient(httpClient iohttp.IHTTPClient, token string) (*GitHubClient, error) {
	if httpClient == nil {
		return nil, ErrNoHTTPClient
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrNoToken
	}
	client := &GitHubClient{http: httpClient, token: token}
	generated, err := NewClientWithResponses(Server, WithHTTPClient(httpClient), WithRequestEditorFn(client.authenticate))
	if err != nil {
		return nil, fmt.Errorf("github: build the client: %w", err)
	}
	client.ClientWithResponses = generated
	return client, nil
}

func (client *GitHubClient) authenticate(_ context.Context, request *http.Request) error {
	request.Header.Set(headerAuth, bearer+client.token)
	request.Header.Set(headerAccept, mediaGitHub)
	request.Header.Set(headerVersion, APIVersion)
	return nil
}

// RateLimit is the core rate limit a response reported.
type RateLimit struct {
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	Reset     time.Time `json:"reset"`
}

// RateLimitOf reads the rate limit a response's headers carry; zero when
// they carry none.
func RateLimitOf(response *http.Response) RateLimit {
	if response == nil {
		return RateLimit{}
	}
	limit, _ := strconv.Atoi(response.Header.Get(headerLimit))
	remaining, _ := strconv.Atoi(response.Header.Get(headerRemaining))
	reset, _ := strconv.ParseInt(response.Header.Get(headerReset), 10, 64)
	rate := RateLimit{Limit: limit, Remaining: remaining}
	if reset > 0 {
		rate.Reset = time.Unix(reset, 0).UTC()
	}
	return rate
}

// StatusError is a response GitHub answered with a status the operation
// does not succeed with; Message is GitHub's own body.
type StatusError struct {
	Status  int
	Message string
}

func (failure *StatusError) Error() string {
	return fmt.Sprintf("github: HTTP %d: %s", failure.Status, failure.Message)
}

// ReadyPullRequest is a pull request after MarkPullRequestReady.
type ReadyPullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Draft  bool   `json:"draft"`
}

// MarkPullRequestReady takes a draft pull request out of draft through
// GraphQL's markPullRequestReadyForReview, and returns it with the rate
// limit the last response reported. A pull request that is not a draft is
// left as it is.
func (client *GitHubClient) MarkPullRequestReady(ctx context.Context, owner string, repo string, number int) (ReadyPullRequest, RateLimit, error) {
	pull, err := client.PullsgetWithResponse(ctx, owner, repo, number)
	if err != nil {
		return ReadyPullRequest{}, RateLimit{}, fmt.Errorf("github: read pull request %d: %w", number, err)
	}
	rate := RateLimitOf(pull.HTTPResponse)
	if pull.JSON200 == nil {
		return ReadyPullRequest{}, rate, &StatusError{Status: pull.StatusCode(), Message: string(pull.Body)}
	}
	ready := ReadyPullRequest{Number: number, URL: pull.JSON200.HtmlUrl}
	if pull.JSON200.Draft == nil || !*pull.JSON200.Draft {
		return ready, rate, nil
	}
	body, err := json.Marshal(graphQLRequest{Query: readyMutation, Variables: map[string]string{variableID: pull.JSON200.NodeId}})
	if err != nil {
		return ReadyPullRequest{}, rate, fmt.Errorf("github: encode the ready mutation: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, Server+graphQLPath, bytes.NewReader(body))
	if err != nil {
		return ReadyPullRequest{}, rate, fmt.Errorf("github: mark pull request %d ready: %w", number, err)
	}
	if err := client.authenticate(ctx, request); err != nil {
		return ReadyPullRequest{}, rate, err
	}
	request.Header.Set(headerType, mediaJSON)
	response, err := client.http.Do(request)
	if err != nil {
		return ReadyPullRequest{}, rate, fmt.Errorf("github: mark pull request %d ready: %w", number, err)
	}
	defer func() { _ = response.Body.Close() }()
	content, err := io.ReadAll(io.LimitReader(response.Body, maxAnswerBytes))
	if err != nil {
		return ReadyPullRequest{}, rate, fmt.Errorf("github: read the ready answer: %w", err)
	}
	if limit := RateLimitOf(response); limit.Limit > 0 {
		rate = limit
	}
	if response.StatusCode != http.StatusOK {
		return ReadyPullRequest{}, rate, &StatusError{Status: response.StatusCode, Message: string(content)}
	}
	var answer graphQLResponse
	if err := json.Unmarshal(content, &answer); err != nil {
		return ReadyPullRequest{}, rate, fmt.Errorf("github: decode the ready answer: %w", err)
	}
	if len(answer.Errors) > 0 {
		return ReadyPullRequest{}, rate, fmt.Errorf("github: mark pull request %d ready: %s", number, answer.Errors[0].Message)
	}
	return ready, rate, nil
}

// graphQLRequest and graphQLResponse are GraphQL's request and the part of
// its answer read here.
type graphQLRequest struct {
	Query     string            `json:"query"`
	Variables map[string]string `json:"variables"`
}

type graphQLResponse struct {
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}
