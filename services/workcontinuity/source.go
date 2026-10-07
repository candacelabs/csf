package workcontinuity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/io/net/github"
	workv1 "github.com/candacelabs/csf/proto/candace/work/v1"
)

// ISource is the issue authority used to read and append task checkpoints.
type ISource interface {
	Load(ctx context.Context, taskURL string) (*workv1.SourceSnapshot, error)
	Append(ctx context.Context, taskURL, body string) (*workv1.SourceComment, error)
}

// GitHubSource reads and writes tasks through the GitHub protocol client the
// binary granted; it never reads, copies or persists the token itself.
type GitHubSource struct{ client *github.GitHubClient }

const (
	githubScheme    = "https"
	githubHost      = "github.com"
	commentFragment = "#issuecomment-"
	commentsPerPage = 100
	// maxCommentPages bounds one Load: a longer history fails rather than
	// returning incomplete.
	maxCommentPages   = 100
	pathSeparator     = "/"
	issueTargetFields = 4
)

func NewGitHubSource(client *github.GitHubClient) *GitHubSource { return &GitHubSource{client: client} }

var taskPath = regexp.MustCompile(`^/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/issues/[1-9][0-9]*$`)

// issueTarget is the repository and number an issue URL names.
type issueTarget struct {
	owner  string
	repo   string
	number int
}

// ValidateTaskURL accepts exactly the issue identities the continuity source
// can read and append to; consumers use it before retaining an association.
func ValidateTaskURL(taskURL string) error {
	_, err := issueTargetOf(taskURL)
	return err
}

func issueTargetOf(taskURL string) (issueTarget, error) {
	parsed, err := url.Parse(taskURL)
	if err != nil || parsed.Scheme != githubScheme || parsed.Host != githubHost || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery || parsed.RawPath != "" ||
		parsed.String() != taskURL || !taskPath.MatchString(parsed.Path) || path.Clean(parsed.Path) != parsed.Path {
		return issueTarget{}, fmt.Errorf("invalid GitHub issue URL")
	}
	parts := strings.Split(strings.TrimPrefix(parsed.Path, pathSeparator), pathSeparator)
	if len(parts) != issueTargetFields {
		return issueTarget{}, fmt.Errorf("invalid GitHub issue URL")
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil {
		return issueTarget{}, fmt.Errorf("invalid GitHub issue number: %w", err)
	}
	return issueTarget{owner: parts[0], repo: parts[1], number: number}, nil
}

// answered is a generated call's raw body when it succeeded, GitHub's
// refusal otherwise.
func answered(status int, body []byte, succeeded bool) ([]byte, error) {
	if !succeeded {
		return nil, &github.StatusError{Status: status, Message: strings.TrimSpace(string(body))}
	}
	return body, nil
}

// getIssue is an issue's raw JSON.
func (source *GitHubSource) getIssue(ctx context.Context, target issueTarget) ([]byte, error) {
	response, err := source.client.IssuesgetWithResponse(ctx, target.owner, target.repo, target.number)
	if err != nil {
		return nil, err
	}
	return answered(response.StatusCode(), response.Body, response.JSON200 != nil)
}

func (source *GitHubSource) Load(ctx context.Context, taskURL string) (*workv1.SourceSnapshot, error) {
	target, err := issueTargetOf(taskURL)
	if err != nil {
		return nil, err
	}
	data, err := source.getIssue(ctx, target)
	if err != nil {
		return nil, err
	}
	issue := &workv1.SourceIssue{}
	upstream := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := upstream.Unmarshal(data, issue); err != nil {
		return nil, fmt.Errorf("decode source issue: %w", err)
	}
	if issue.HtmlUrl != taskURL || (issue.State != IssueOpen && issue.State != IssueClosed) || issue.Number <= 0 {
		return nil, fmt.Errorf("source issue identity/state mismatch")
	}
	// Each comment is decoded from GitHub's own JSON into the generated
	// adapter projection, not a handwritten DTO; pages run until one is short.
	snapshot := &workv1.SourceSnapshot{Issue: issue}
	perPage := commentsPerPage
	for page := 1; page <= maxCommentPages; page++ {
		response, err := source.client.IssueslistCommentsWithResponse(ctx, target.owner, target.repo, target.number,
			&github.IssueslistCommentsParams{PerPage: &perPage, Page: &page})
		if err != nil {
			return nil, err
		}
		data, err := answered(response.StatusCode(), response.Body, response.JSON200 != nil)
		if err != nil {
			return nil, err
		}
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, fmt.Errorf("decode source comment page: %w", err)
		}
		for _, item := range items {
			comment := &workv1.SourceComment{}
			if err := upstream.Unmarshal(item, comment); err != nil {
				return nil, fmt.Errorf("decode source comment: %w", err)
			}
			snapshot.Comments = append(snapshot.Comments, comment)
		}
		if len(items) < perPage {
			return snapshot, nil
		}
	}
	return nil, fmt.Errorf("source issue has more than %d comment pages", maxCommentPages)
}

func (source *GitHubSource) Append(ctx context.Context, taskURL, body string) (*workv1.SourceComment, error) {
	target, err := issueTargetOf(taskURL)
	if err != nil {
		return nil, err
	}
	response, err := source.client.IssuescreateCommentWithResponse(ctx, target.owner, target.repo, target.number, github.IssuescreateCommentJSONRequestBody{Body: body})
	if err != nil {
		return nil, err
	}
	data, err := answered(response.StatusCode(), response.Body, response.JSON201 != nil)
	if err != nil {
		return nil, err
	}
	comment := &workv1.SourceComment{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, comment); err != nil {
		return nil, fmt.Errorf("decode appended comment: %w", err)
	}
	if comment.Body != body || !strings.HasPrefix(comment.HtmlUrl, taskURL+commentFragment) {
		return nil, fmt.Errorf("appended comment identity/content mismatch; inspect source before retry")
	}
	return comment, nil
}
