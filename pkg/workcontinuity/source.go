package workcontinuity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	workv1 "github.com/candacelabs/csf/proto/candace/work/v1"
)

// ISource is the issue authority used to read and append task checkpoints.
type ISource interface {
	Load(ctx context.Context, taskURL string) (*workv1.SourceSnapshot, error)
	Append(ctx context.Context, taskURL, body string) (*workv1.SourceComment, error)
}

// Command runs an argument vector, never a shell command from task content.
type Command func(ctx context.Context, args ...string) ([]byte, error)

// GitHubSource uses the operator's existing gh authentication. The application
// does not read, copy, print or persist the credential itself.
type GitHubSource struct{ command Command }

const (
	githubExecutable       = "gh"
	githubAPICommand       = "api"
	githubScheme           = "https"
	githubHost             = "github.com"
	githubRepositoryPrefix = "repos"
	githubCommentsPath     = "/comments"
	githubCommentPageQuery = "?per_page=100"
	githubPaginateFlag     = "--paginate"
	githubMethodFlag       = "--method"
	githubRawFieldFlag     = "--raw-field"
	githubBodyField        = "body="
	githubCommentFragment  = "#issuecomment-"
)

func NewGitHubSource(command Command) *GitHubSource { return &GitHubSource{command: command} }

func RunGitHub(ctx context.Context, args ...string) ([]byte, error) {
	result, err := exec.CommandContext(ctx, githubExecutable, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("GitHub CLI request failed: %w", err)
	}
	return result, nil
}

var taskPath = regexp.MustCompile(`^/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/issues/[1-9][0-9]*$`)

// ValidateTaskURL accepts exactly the issue identities the continuity source
// can read and append to; consumers use it before retaining an association.
func ValidateTaskURL(taskURL string) error {
	_, err := issueEndpoint(taskURL)
	return err
}

func issueEndpoint(taskURL string) (string, error) {
	parsed, err := url.Parse(taskURL)
	if err != nil || parsed.Scheme != githubScheme || parsed.Host != githubHost || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery || parsed.RawPath != "" ||
		parsed.String() != taskURL || !taskPath.MatchString(parsed.Path) || path.Clean(parsed.Path) != parsed.Path {
		return "", fmt.Errorf("invalid GitHub issue URL")
	}
	if _, err := strconv.ParseInt(parsed.Path[strings.LastIndexByte(parsed.Path, '/')+1:], 10, 64); err != nil {
		return "", fmt.Errorf("invalid GitHub issue number: %w", err)
	}
	return githubRepositoryPrefix + parsed.Path, nil
}

func (source *GitHubSource) Load(ctx context.Context, taskURL string) (*workv1.SourceSnapshot, error) {
	endpoint, err := issueEndpoint(taskURL)
	if err != nil {
		return nil, err
	}
	data, err := source.command(ctx, githubAPICommand, endpoint)
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
	data, err = source.command(ctx, githubAPICommand, endpoint+githubCommentsPath+githubCommentPageQuery, githubPaginateFlag)
	if err != nil {
		return nil, err
	}
	// Raw JSON is confined to the upstream paginated envelope. Each item is
	// decoded into the generated adapter projection, not a handwritten DTO.
	snapshot := &workv1.SourceSnapshot{Issue: issue}
	decoder := json.NewDecoder(bytes.NewReader(data))
	pages := 0
	for {
		var page []json.RawMessage
		if err := decoder.Decode(&page); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode source comment page: %w", err)
		}
		pages++
		for _, item := range page {
			comment := &workv1.SourceComment{}
			if err := upstream.Unmarshal(item, comment); err != nil {
				return nil, fmt.Errorf("decode source comment: %w", err)
			}
			snapshot.Comments = append(snapshot.Comments, comment)
		}
	}
	if pages == 0 {
		return nil, fmt.Errorf("source returned no comment pages")
	}
	return snapshot, nil
}

func (source *GitHubSource) Append(ctx context.Context, taskURL, body string) (*workv1.SourceComment, error) {
	endpoint, err := issueEndpoint(taskURL)
	if err != nil {
		return nil, err
	}
	data, err := source.command(ctx, githubAPICommand, endpoint+githubCommentsPath, githubMethodFlag, http.MethodPost, githubRawFieldFlag, githubBodyField+body)
	if err != nil {
		return nil, err
	}
	comment := &workv1.SourceComment{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, comment); err != nil {
		return nil, fmt.Errorf("decode appended comment: %w", err)
	}
	if comment.Body != body || !strings.HasPrefix(comment.HtmlUrl, taskURL+githubCommentFragment) {
		return nil, fmt.Errorf("appended comment identity/content mismatch; inspect source before retry")
	}
	return comment, nil
}
