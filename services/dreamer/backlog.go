// Copyright 2026 Candace Labs

package dreamer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/candacelabs/csf/io/ipc/proc"
)

// Issue is one open issue of CSF's own repository.
type Issue struct {
	Number int64
	Title  string
	URL    string
	Body   string
	Labels []string
}

// The gh invocations the backlog makes.
const (
	ghExecutable  = "gh"
	ghIssue       = "issue"
	ghList        = "list"
	ghCreate      = "create"
	ghRepo        = "--repo"
	ghState       = "--state"
	ghStateOpen   = "open"
	ghLimit       = "--limit"
	ghListLimit   = "500"
	ghJSON        = "--json"
	ghIssueFields = "number,title,url,body,labels"
	ghTitle       = "--title"
	ghBodyFile    = "--body-file"
	ghStdin       = "-"
)

// GitHubBacklog is CSF's own repository's issues over gh, run through the
// process capability.
type GitHubBacklog struct {
	launcher   proc.ILauncher
	repository string
}

var _ IBacklog = (*GitHubBacklog)(nil)

// NewGitHubBacklog grants one repository's issues; repository is its
// owner/name slug.
func NewGitHubBacklog(launcher proc.ILauncher, repository string) (*GitHubBacklog, error) {
	if launcher == nil || repository == "" {
		return nil, fmt.Errorf("%w: the backlog needs a launcher and a repository", ErrInvalidOption)
	}
	return &GitHubBacklog{launcher: launcher, repository: repository}, nil
}

type issueRow struct {
	Number int64  `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Body   string `json:"body"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// OpenIssues lists the repository's open issues.
func (backlog *GitHubBacklog) OpenIssues(ctx context.Context) ([]Issue, error) {
	result, err := backlog.launcher.Run(ctx, proc.Command{Executable: ghExecutable, Arguments: []string{
		ghIssue, ghList, ghRepo, backlog.repository, ghState, ghStateOpen, ghLimit, ghListLimit, ghJSON, ghIssueFields,
	}})
	if err != nil {
		return nil, fmt.Errorf("dreamer: list the open issues: %w", err)
	}
	var rows []issueRow
	if err := json.Unmarshal(result.Stdout, &rows); err != nil {
		return nil, fmt.Errorf("dreamer: decode the issue list: %w", err)
	}
	issues := make([]Issue, 0, len(rows))
	for _, row := range rows {
		issue := Issue{Number: row.Number, Title: row.Title, URL: row.URL, Body: row.Body}
		for _, label := range row.Labels {
			issue.Labels = append(issue.Labels, label.Name)
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

// OpenIssue opens one issue and returns its URL.
func (backlog *GitHubBacklog) OpenIssue(ctx context.Context, title string, body string) (string, error) {
	result, err := backlog.launcher.Run(ctx, proc.Command{
		Executable: ghExecutable,
		Arguments:  []string{ghIssue, ghCreate, ghRepo, backlog.repository, ghTitle, title, ghBodyFile, ghStdin},
		Stdin:      strings.NewReader(body),
	})
	if err != nil {
		return "", fmt.Errorf("dreamer: open an issue: %w", err)
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}
