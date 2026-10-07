// Copyright 2026 Candace Labs

package ouroboros

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/ipc/proc"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=tickets.go -destination=mocks/mock_tickets.go -package=mocks

// ReferenceKind is what a number names in the repository's issue space.
type ReferenceKind string

// The kinds a number resolves to.
const (
	ReferenceIssue       ReferenceKind = "issue"
	ReferencePullRequest ReferenceKind = "pull_request"
	ReferenceNone        ReferenceKind = "none"
)

// Ticket is one open mining ticket: an issue carrying a severity label.
type Ticket struct {
	Number   int64
	Title    string
	Labels   []string
	Severity string
}

// Comment is one comment on a ticket.
type Comment struct {
	ID        string
	Body      string
	CreatedAt time.Time
}

// PullRequest is one pull request as the merge train and the fixer ledger
// read it.
type PullRequest struct {
	Number    int64
	Title     string
	URL       string
	HeadSHA   string
	Draft     bool
	State     string
	CreatedAt time.Time
}

// Merged reports whether the pull request has merged.
func (pull PullRequest) Merged() bool { return pull.State == stateMerged }

// pullRequestURLExpression reads the owner/name slug and the number from a
// GitHub pull request URL.
var pullRequestURLExpression = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/([0-9]+)$`)

// ErrNotPullRequestURL reports a URL that names no GitHub pull request.
var ErrNotPullRequestURL = errors.New("ouroboros: not a GitHub pull request URL")

// PullRequestOf reads the repository slug and the number of the pull request
// a GitHub URL names.
func PullRequestOf(url string) (string, int64, error) {
	match := pullRequestURLExpression.FindStringSubmatch(url)
	if match == nil {
		return "", 0, fmt.Errorf("%w: %q", ErrNotPullRequestURL, url)
	}
	number, err := strconv.ParseInt(match[2], referenceNumberBase, 64)
	if err != nil {
		return "", 0, fmt.Errorf("%w: %q: %w", ErrNotPullRequestURL, url, err)
	}
	return match[1], number, nil
}

// ITickets is the ticket capability: the repository's issues and pull
// requests, reached through gh in the binary and a double in specs.
type ITickets interface {
	// Repository is the owner/name slug the tickets live in.
	Repository() string
	// ListMiningTickets lists the open tickets carrying a severity label
	// and no lang label, the mining subset, lowest number first.
	ListMiningTickets(ctx context.Context) ([]Ticket, error)
	// TicketComments lists a ticket's comments, oldest first.
	TicketComments(ctx context.Context, number int64) ([]Comment, error)
	// Reference reports whether a number is an issue, a pull request or
	// nothing in the repository.
	Reference(ctx context.Context, number int64) (ReferenceKind, error)
	// ListReadyPullRequests lists the open pull requests that are not drafts,
	// oldest first.
	ListReadyPullRequests(ctx context.Context) ([]PullRequest, error)
	// PullRequest reads one pull request.
	PullRequest(ctx context.Context, number int64) (PullRequest, error)
	// PullRequestSessions lists the CSF-Session trailers of a pull request's
	// commits: the sessions that authored it.
	PullRequestSessions(ctx context.Context, number int64) ([]string, error)
	// Comment posts one comment on a pull request.
	Comment(ctx context.Context, number int64, body string) error
}

// The gh invocations and the labels the mining subset is read by.
const (
	ghExecutable      = "gh"
	ghIssue           = "issue"
	ghPullRequest     = "pr"
	ghAPI             = "api"
	ghList            = "list"
	ghView            = "view"
	ghCommentVerb     = "comment"
	ghRepo            = "--repo"
	ghState           = "--state"
	ghStateOpen       = "open"
	ghLimit           = "--limit"
	ghListLimit       = "500"
	ghJSON            = "--json"
	ghJQ              = "--jq"
	ghBodyFile        = "--body-file"
	ghStdin           = "-"
	ghIssueFields     = "number,title,labels"
	ghCommentFields   = "comments"
	ghPullFields      = "number,title,url,headRefOid,isDraft,state,createdAt"
	ghCommitFields    = "commits"
	ghCommitBodies    = "[.commits[].messageBody] | join(\"\\n\")"
	ghIssueKindQuery  = "if .pull_request then \"pull_request\" else \"issue\" end"
	ghIssuesAPIFormat = "repos/%s/issues/%d"
	labelLanguage     = "lang"
	// SessionTrailer is the commit trailer every harness session leaves on
	// its commits; the merge train reads the author from it.
	SessionTrailer = "CSF-Session:"
)

// severityLabels are the labels that make an issue a mining ticket, in
// severity order (candace-server#373: S0 is a gate escape, S3 an offense
// found only by mining).
var severityLabels = []string{"S0", "S1", "S2", "S3"}

// slugExpression is an owner/name GitHub repository slug.
var slugExpression = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// ErrNoRepositorySlug reports an origin URL no GitHub owner/name can be read from.
var ErrNoRepositorySlug = errors.New("ouroboros: the origin URL names no GitHub repository")

// gitHubHost is the host a remote URL must name for its last two path
// segments to be an owner/name slug; gitSuffix is what a clone URL may end
// in.
const (
	gitHubHost = "github.com"
	gitSuffix  = ".git"
)

// RepositorySlug reads owner/name from a GitHub remote URL in either the
// https or the ssh spelling.
func RepositorySlug(origin string) (string, error) {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(origin), "/"), gitSuffix)
	parts := strings.FieldsFunc(trimmed, func(r rune) bool { return r == '/' || r == ':' })
	if len(parts) < 3 || !strings.HasSuffix(parts[len(parts)-3], gitHubHost) {
		return "", fmt.Errorf("%w: %q", ErrNoRepositorySlug, origin)
	}
	slug := parts[len(parts)-2] + "/" + parts[len(parts)-1]
	if !slugExpression.MatchString(slug) {
		return "", fmt.Errorf("%w: %q", ErrNoRepositorySlug, origin)
	}
	return slug, nil
}

// GitHubTickets is the ticket capability over gh, run through the process
// capability against one repository.
type GitHubTickets struct {
	launcher   proc.ILauncher
	repository string
}

var _ ITickets = (*GitHubTickets)(nil)

// NewGitHubTickets grants the repository's tickets through gh. repository is
// the owner/name slug.
func NewGitHubTickets(launcher proc.ILauncher, repository string) (*GitHubTickets, error) {
	if launcher == nil {
		return nil, fmt.Errorf("%w: nil launcher", ErrInvalidOption)
	}
	if !slugExpression.MatchString(repository) {
		return nil, fmt.Errorf("%w: %q", ErrNoRepositorySlug, repository)
	}
	return &GitHubTickets{launcher: launcher, repository: repository}, nil
}

// issueRow is the part of gh's issue JSON the loop reads.
type issueRow struct {
	Number int64  `json:"number"`
	Title  string `json:"title"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

type commentRow struct {
	ID        string    `json:"id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

type pullRow struct {
	Number    int64     `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	HeadSHA   string    `json:"headRefOid"`
	Draft     bool      `json:"isDraft"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"createdAt"`
}

// Repository is the owner/name slug.
func (tickets *GitHubTickets) Repository() string { return tickets.repository }

func (tickets *GitHubTickets) run(ctx context.Context, arguments ...string) ([]byte, error) {
	result, err := tickets.launcher.Run(ctx, proc.Command{Executable: ghExecutable, Arguments: arguments})
	if err != nil {
		return nil, fmt.Errorf("ouroboros: %s %s: %w", ghExecutable, strings.Join(arguments[:2], " "), err)
	}
	return result.Stdout, nil
}

// ListMiningTickets lists the open severity-labeled, non-lang issues.
func (tickets *GitHubTickets) ListMiningTickets(ctx context.Context) ([]Ticket, error) {
	output, err := tickets.run(ctx, ghIssue, ghList, ghRepo, tickets.repository, ghState, ghStateOpen, ghLimit, ghListLimit, ghJSON, ghIssueFields)
	if err != nil {
		return nil, err
	}
	var rows []issueRow
	if err := json.Unmarshal(output, &rows); err != nil {
		return nil, fmt.Errorf("ouroboros: decode the issue list: %w", err)
	}
	mining := make([]Ticket, 0, len(rows))
	for _, row := range rows {
		labels := make([]string, 0, len(row.Labels))
		for _, label := range row.Labels {
			labels = append(labels, label.Name)
		}
		if ticket, ok := MiningTicket(row.Number, row.Title, labels); ok {
			mining = append(mining, ticket)
		}
	}
	slices.SortFunc(mining, func(a, b Ticket) int { return int(a.Number - b.Number) })
	return mining, nil
}

// MiningTicket reads an issue as a mining ticket: it carries a severity
// label and no lang label, which marks the operator-only language tickets.
func MiningTicket(number int64, title string, labels []string) (Ticket, bool) {
	if slices.Contains(labels, labelLanguage) {
		return Ticket{}, false
	}
	for _, severity := range severityLabels {
		if slices.Contains(labels, severity) {
			return Ticket{Number: number, Title: title, Labels: labels, Severity: severity}, true
		}
	}
	return Ticket{}, false
}

// TicketComments lists a ticket's comments, oldest first.
func (tickets *GitHubTickets) TicketComments(ctx context.Context, number int64) ([]Comment, error) {
	output, err := tickets.run(ctx, ghIssue, ghView, strconv.FormatInt(number, 10), ghRepo, tickets.repository, ghJSON, ghCommentFields)
	if err != nil {
		return nil, err
	}
	var view struct {
		Comments []commentRow `json:"comments"`
	}
	if err := json.Unmarshal(output, &view); err != nil {
		return nil, fmt.Errorf("ouroboros: decode the comments of #%d: %w", number, err)
	}
	comments := make([]Comment, 0, len(view.Comments))
	for _, row := range view.Comments {
		comments = append(comments, Comment{ID: row.ID, Body: row.Body, CreatedAt: row.CreatedAt})
	}
	slices.SortStableFunc(comments, func(a, b Comment) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return comments, nil
}

// Reference asks the issues API what a number is; a number gh cannot find
// is nothing.
func (tickets *GitHubTickets) Reference(ctx context.Context, number int64) (ReferenceKind, error) {
	result, err := tickets.launcher.Run(ctx, proc.Command{Executable: ghExecutable, Arguments: []string{
		ghAPI, fmt.Sprintf(ghIssuesAPIFormat, tickets.repository, number), ghJQ, ghIssueKindQuery,
	}})
	var exit *proc.ExitError
	if errors.As(err, &exit) {
		return ReferenceNone, nil
	}
	if err != nil {
		return ReferenceNone, fmt.Errorf("ouroboros: %s %s #%d: %w", ghExecutable, ghAPI, number, err)
	}
	switch kind := ReferenceKind(strings.TrimSpace(string(result.Stdout))); kind {
	case ReferenceIssue, ReferencePullRequest:
		return kind, nil
	}
	return ReferenceNone, nil
}

// ListReadyPullRequests lists the open pull requests that are not drafts,
// oldest first.
func (tickets *GitHubTickets) ListReadyPullRequests(ctx context.Context) ([]PullRequest, error) {
	output, err := tickets.run(ctx, ghPullRequest, ghList, ghRepo, tickets.repository, ghState, ghStateOpen, ghLimit, ghListLimit, ghJSON, ghPullFields)
	if err != nil {
		return nil, err
	}
	var rows []pullRow
	if err := json.Unmarshal(output, &rows); err != nil {
		return nil, fmt.Errorf("ouroboros: decode the pull request list: %w", err)
	}
	ready := make([]PullRequest, 0, len(rows))
	for _, row := range rows {
		if !row.Draft {
			ready = append(ready, pullRequestRecord(row))
		}
	}
	slices.SortStableFunc(ready, func(a, b PullRequest) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return ready, nil
}

// PullRequest reads one pull request.
func (tickets *GitHubTickets) PullRequest(ctx context.Context, number int64) (PullRequest, error) {
	output, err := tickets.run(ctx, ghPullRequest, ghView, strconv.FormatInt(number, 10), ghRepo, tickets.repository, ghJSON, ghPullFields)
	if err != nil {
		return PullRequest{}, err
	}
	var row pullRow
	if err := json.Unmarshal(output, &row); err != nil {
		return PullRequest{}, fmt.Errorf("ouroboros: decode pull request #%d: %w", number, err)
	}
	return pullRequestRecord(row), nil
}

func pullRequestRecord(row pullRow) PullRequest {
	return PullRequest{Number: row.Number, Title: row.Title, URL: row.URL, HeadSHA: row.HeadSHA, Draft: row.Draft, State: row.State, CreatedAt: row.CreatedAt}
}

// PullRequestSessions reads the CSF-Session trailers of a pull request's
// commit messages.
func (tickets *GitHubTickets) PullRequestSessions(ctx context.Context, number int64) ([]string, error) {
	output, err := tickets.run(ctx, ghPullRequest, ghView, strconv.FormatInt(number, 10), ghRepo, tickets.repository, ghJSON, ghCommitFields, ghJQ, ghCommitBodies)
	if err != nil {
		return nil, err
	}
	return SessionTrailers(string(output)), nil
}

// SessionTrailers reads every distinct CSF-Session trailer value in text, in
// first-seen order.
func SessionTrailers(text string) []string {
	var sessions []string
	for _, line := range strings.Split(text, "\n") {
		value, found := strings.CutPrefix(strings.TrimSpace(line), SessionTrailer)
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		if value != "" && !slices.Contains(sessions, value) {
			sessions = append(sessions, value)
		}
	}
	return sessions
}

// Comment posts one comment on a pull request, read from standard input so
// its body never crosses an argument vector.
func (tickets *GitHubTickets) Comment(ctx context.Context, number int64, body string) error {
	_, err := tickets.launcher.Run(ctx, proc.Command{
		Executable: ghExecutable,
		Arguments:  []string{ghPullRequest, ghCommentVerb, strconv.FormatInt(number, 10), ghRepo, tickets.repository, ghBodyFile, ghStdin},
		Stdin:      strings.NewReader(body),
	})
	if err != nil {
		return fmt.Errorf("ouroboros: comment on #%d: %w", number, err)
	}
	return nil
}
