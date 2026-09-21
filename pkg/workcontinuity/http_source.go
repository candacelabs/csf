package workcontinuity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	workv1 "github.com/candacelabs/csf/proto/candace/work/v1"
)

const (
	githubAPIBase           = "https://api.github.com/"
	githubAccept            = "application/vnd.github+json"
	githubAcceptHeader      = "Accept"
	githubContentTypeHeader = "Content-Type"
	githubContentType       = "application/json"
	githubPageQuery         = "&page="
	githubPageSize          = 100
	githubMaxPages          = 100
	githubResponseLimit     = 16 << 20
)

// HTTPGitHubSource performs continuity I/O through the caller's authenticated
// client in the existing process. The host owns credentials and client lifetime.
// Load reads at most 100 comment pages and 16 MiB of response bodies in total;
// exceeding either bound fails rather than returning an incomplete history.
type HTTPGitHubSource struct{ client *http.Client }

func NewHTTPGitHubSource(client *http.Client) (*HTTPGitHubSource, error) {
	if client == nil {
		return nil, fmt.Errorf("GitHub source requires an HTTP client")
	}
	return &HTTPGitHubSource{client: client}, nil
}

func (source *HTTPGitHubSource) Load(ctx context.Context, taskURL string) (*workv1.SourceSnapshot, error) {
	endpoint, err := issueEndpoint(taskURL)
	if err != nil {
		return nil, err
	}
	data, err := source.request(ctx, http.MethodGet, endpoint, nil, githubResponseLimit)
	if err != nil {
		return nil, err
	}
	issue := &workv1.SourceIssue{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, issue); err != nil {
		return nil, fmt.Errorf("decode source issue: %w", err)
	}
	if issue.HtmlUrl != taskURL || issue.Number <= 0 ||
		!strings.HasSuffix(endpoint, "/"+strconv.FormatInt(issue.Number, 10)) ||
		(issue.State != IssueOpen && issue.State != IssueClosed) {
		return nil, fmt.Errorf("source issue identity/state mismatch")
	}
	comments, err := source.loadComments(ctx, taskURL, endpoint, githubResponseLimit-len(data))
	if err != nil {
		return nil, err
	}
	return &workv1.SourceSnapshot{Issue: issue, Comments: comments}, nil
}

func (source *HTTPGitHubSource) loadComments(ctx context.Context, taskURL, endpoint string, budget int) ([]*workv1.SourceComment, error) {
	var comments []*workv1.SourceComment
	seen := make(map[int64]bool)
	for page := 1; page <= githubMaxPages; page++ {
		data, err := source.request(ctx, http.MethodGet, endpoint+githubCommentsPath+githubCommentPageQuery+githubPageQuery+strconv.Itoa(page), nil, budget)
		if err != nil {
			return nil, err
		}
		budget -= len(data)
		items, err := decodeSourceComments(data, taskURL)
		if err != nil {
			return nil, err
		}
		for _, comment := range items {
			if seen[comment.Id] {
				return nil, fmt.Errorf("source comment identity repeated across history")
			}
			seen[comment.Id] = true
		}
		comments = append(comments, items...)
		if len(items) < githubPageSize {
			return comments, nil
		}
	}
	return nil, fmt.Errorf("source history exceeds the bounded comment page budget")
}

func decodeSourceComments(data []byte, taskURL string) ([]*workv1.SourceComment, error) {
	// Only the upstream array is untyped; records use generated projections.
	var page []json.RawMessage
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("decode source comments: %w", err)
	}
	if page == nil || len(page) > githubPageSize {
		return nil, fmt.Errorf("source comment page is null or exceeds the page size")
	}
	comments := make([]*workv1.SourceComment, 0, len(page))
	decoder := protojson.UnmarshalOptions{DiscardUnknown: true}
	for _, item := range page {
		comment := &workv1.SourceComment{}
		if err := decoder.Unmarshal(item, comment); err != nil {
			return nil, fmt.Errorf("decode source comment: %w", err)
		}
		if !sourceCommentIdentity(comment, taskURL) {
			return nil, fmt.Errorf("source comment identity mismatch")
		}
		comments = append(comments, comment)
	}
	return comments, nil
}

func sourceCommentIdentity(comment *workv1.SourceComment, taskURL string) bool {
	return comment.Id > 0 && comment.HtmlUrl == taskURL+githubCommentFragment+strconv.FormatInt(comment.Id, 10)
}

// Append validates the returned identity and body but never retries a write.
// An error can follow a committed write; inspect the issue before retrying. The
// continuity service separately reloads history to verify its checkpoint chain.
func (source *HTTPGitHubSource) Append(ctx context.Context, taskURL, body string) (*workv1.SourceComment, error) {
	endpoint, err := issueEndpoint(taskURL)
	if err != nil {
		return nil, err
	}
	data, err := source.request(ctx, http.MethodPost, endpoint+githubCommentsPath, &workv1.SourceComment{Body: body}, githubResponseLimit)
	if err != nil {
		return nil, err
	}
	comment := &workv1.SourceComment{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, comment); err != nil {
		return nil, fmt.Errorf("decode appended comment: %w", err)
	}
	if !sourceCommentIdentity(comment, taskURL) || comment.Body != body {
		return nil, fmt.Errorf("appended comment identity/content mismatch; inspect source before retry")
	}
	return comment, nil
}

func (source *HTTPGitHubSource) request(ctx context.Context, method, endpoint string, message proto.Message, budget int) ([]byte, error) {
	if budget <= 0 {
		return nil, fmt.Errorf("GitHub source response exceeds the byte budget")
	}
	var body []byte
	var err error
	if message != nil {
		body, err = protojson.Marshal(message)
		if err != nil {
			return nil, fmt.Errorf("encode GitHub source request: %w", err)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, githubAPIBase+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set(githubAcceptHeader, githubAccept)
	request.Header.Set(githubContentTypeHeader, githubContentType)
	response, err := source.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GitHub source request failed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	expectedStatus := http.StatusOK
	if method == http.MethodPost {
		expectedStatus = http.StatusCreated
	}
	if response.StatusCode != expectedStatus {
		return nil, fmt.Errorf("GitHub source returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(budget)+1))
	if err != nil {
		return nil, fmt.Errorf("read GitHub source response: %w", err)
	}
	if len(data) > budget {
		return nil, fmt.Errorf("GitHub source response exceeds the byte budget")
	}
	return data, nil
}
