// Copyright 2026 Candace Labs

package intake

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/types/known/timestamppb"

	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
)

// Feed names label status and log records; they are stable.
const (
	repositoryEventsFeed = "repository_events"
	failedRunsFeed       = "failed_runs"
)

// GitHub repository event types this intake acts on. Every other type the
// events API reports (pushes, stars, forks, branch creation…) is discarded.
const (
	issueCommentEvent             = "IssueCommentEvent"
	pullRequestReviewEvent        = "PullRequestReviewEvent"
	pullRequestReviewCommentEvent = "PullRequestReviewCommentEvent"
	createdAction                 = "created"
	failureConclusion             = "failure"
)

// Event identifier prefixes. An identifier is the prefix plus GitHub's own
// identifier for the comment, review or run attempt, so it is stable across
// polls and across feeds that report the same object.
const (
	commentIdentifier       = "github/comment/"
	reviewIdentifier        = "github/review/"
	reviewCommentIdentifier = "github/review_comment/"
	runIdentifier           = "github/run/"
	attemptSeparator        = "/attempt/"
)

// jsonNull is how an absent pull_request link can still be spelled.
const jsonNull = "null"

// maxSummaryBytes bounds the excerpt an event carries; the reader follows the
// URL for the rest.
const maxSummaryBytes = 1024

// feed is one GitHub REST resource the poller reads per repository: where it
// is and how a response becomes events. Feeds are registered in [feeds]; the
// poller iterates the registry, so adding a source is adding an entry.
type feed struct {
	name      string
	path      func(repository string) string
	normalize func(repository string, body []byte) ([]*intakev1.Event, error)
}

// feeds is every source the poller reads, in poll order.
var feeds = []feed{
	{name: repositoryEventsFeed, path: repositoryEventsPath, normalize: normalizeRepositoryEvents},
	{name: failedRunsFeed, path: failedRunsPath, normalize: normalizeFailedRuns},
}

// GitHub REST paths, around the owner/name repository.
const (
	repositoriesPath   = "/repos/"
	eventsResource     = "/events?per_page=100"
	failedRunsResource = "/actions/runs?status=failure&per_page=30"
)

// repositoryEventsPath is the repository's public activity feed: comments,
// reviews and review comments, newest first. It supports ETags and
// X-Poll-Interval.
func repositoryEventsPath(repository string) string {
	return repositoriesPath + repository + eventsResource
}

// failedRunsPath lists the repository's workflow runs that concluded in
// failure, newest first.
func failedRunsPath(repository string) string {
	return repositoriesPath + repository + failedRunsResource
}

// The GitHub JSON shapes below are projections of the fields this intake
// reads; unknown upstream fields are discarded.

type githubUser struct {
	Login string `json:"login"`
}

type githubEvent struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Actor     githubUser      `json:"actor"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type githubComment struct {
	ID      int64      `json:"id"`
	HTMLURL string     `json:"html_url"`
	Body    string     `json:"body"`
	User    githubUser `json:"user"`
}

type githubIssue struct {
	Number      uint64          `json:"number"`
	PullRequest json.RawMessage `json:"pull_request"`
}

type githubPullRequest struct {
	Number uint64 `json:"number"`
}

type githubReview struct {
	ID      int64      `json:"id"`
	State   string     `json:"state"`
	Body    string     `json:"body"`
	HTMLURL string     `json:"html_url"`
	User    githubUser `json:"user"`
}

type githubEventPayload struct {
	Action      string            `json:"action"`
	Issue       githubIssue       `json:"issue"`
	Comment     githubComment     `json:"comment"`
	Review      githubReview      `json:"review"`
	PullRequest githubPullRequest `json:"pull_request"`
}

type githubWorkflowRun struct {
	ID           int64               `json:"id"`
	Name         string              `json:"name"`
	RunAttempt   int64               `json:"run_attempt"`
	Conclusion   string              `json:"conclusion"`
	HTMLURL      string              `json:"html_url"`
	HeadBranch   string              `json:"head_branch"`
	UpdatedAt    time.Time           `json:"updated_at"`
	Actor        githubUser          `json:"actor"`
	PullRequests []githubPullRequest `json:"pull_requests"`
}

type githubWorkflowRuns struct {
	WorkflowRuns []githubWorkflowRun `json:"workflow_runs"`
}

// eventNormalizers turns one repository event of a known type into an event;
// a type with no entry is not actionable.
var eventNormalizers = map[string]func(repository string, event githubEvent, payload githubEventPayload) *intakev1.Event{
	issueCommentEvent:             normalizeIssueComment,
	pullRequestReviewEvent:        normalizeReview,
	pullRequestReviewCommentEvent: normalizeReviewComment,
}

func normalizeRepositoryEvents(repository string, body []byte) ([]*intakev1.Event, error) {
	var listed []githubEvent
	if err := json.Unmarshal(body, &listed); err != nil {
		return nil, fmt.Errorf("intake: %s: decode repository events: %w", repository, err)
	}
	events := make([]*intakev1.Event, 0, len(listed))
	for _, event := range listed {
		normalize, actionable := eventNormalizers[event.Type]
		if !actionable {
			continue
		}
		var payload githubEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil, fmt.Errorf("intake: %s: decode %s %s: %w", repository, event.Type, event.ID, err)
		}
		if payload.Action != createdAction {
			continue
		}
		events = append(events, normalize(repository, event, payload))
	}
	return events, nil
}

func normalizeIssueComment(repository string, event githubEvent, payload githubEventPayload) *intakev1.Event {
	kind := intakev1.SubjectKind_SUBJECT_KIND_ISSUE
	if len(payload.Issue.PullRequest) > 0 && string(payload.Issue.PullRequest) != jsonNull {
		kind = intakev1.SubjectKind_SUBJECT_KIND_PULL_REQUEST
	}
	return &intakev1.Event{
		Id:         commentIdentifier + strconv.FormatInt(payload.Comment.ID, 10),
		Kind:       intakev1.EventKind_EVENT_KIND_COMMENT,
		Subject:    &intakev1.Subject{Repository: repository, Kind: kind, Number: payload.Issue.Number},
		Actor:      firstNonEmpty(payload.Comment.User.Login, event.Actor.Login),
		Url:        payload.Comment.HTMLURL,
		Summary:    excerpt(payload.Comment.Body),
		OccurredAt: timestamppb.New(event.CreatedAt),
	}
}

func normalizeReview(repository string, event githubEvent, payload githubEventPayload) *intakev1.Event {
	summary := strings.ToLower(payload.Review.State)
	if payload.Review.Body != "" {
		summary += ": " + payload.Review.Body
	}
	return &intakev1.Event{
		Id:         reviewIdentifier + strconv.FormatInt(payload.Review.ID, 10),
		Kind:       intakev1.EventKind_EVENT_KIND_REVIEW,
		Subject:    pullRequestSubject(repository, payload.PullRequest.Number),
		Actor:      firstNonEmpty(payload.Review.User.Login, event.Actor.Login),
		Url:        payload.Review.HTMLURL,
		Summary:    excerpt(summary),
		OccurredAt: timestamppb.New(event.CreatedAt),
	}
}

func normalizeReviewComment(repository string, event githubEvent, payload githubEventPayload) *intakev1.Event {
	return &intakev1.Event{
		Id:         reviewCommentIdentifier + strconv.FormatInt(payload.Comment.ID, 10),
		Kind:       intakev1.EventKind_EVENT_KIND_REVIEW_COMMENT,
		Subject:    pullRequestSubject(repository, payload.PullRequest.Number),
		Actor:      firstNonEmpty(payload.Comment.User.Login, event.Actor.Login),
		Url:        payload.Comment.HTMLURL,
		Summary:    excerpt(payload.Comment.Body),
		OccurredAt: timestamppb.New(event.CreatedAt),
	}
}

func normalizeFailedRuns(repository string, body []byte) ([]*intakev1.Event, error) {
	var listed githubWorkflowRuns
	if err := json.Unmarshal(body, &listed); err != nil {
		return nil, fmt.Errorf("intake: %s: decode workflow runs: %w", repository, err)
	}
	events := make([]*intakev1.Event, 0, len(listed.WorkflowRuns))
	for _, run := range listed.WorkflowRuns {
		if run.Conclusion != failureConclusion {
			continue
		}
		subject := &intakev1.Subject{Repository: repository, Kind: intakev1.SubjectKind_SUBJECT_KIND_REPOSITORY}
		if len(run.PullRequests) > 0 {
			subject = pullRequestSubject(repository, run.PullRequests[0].Number)
		}
		events = append(events, &intakev1.Event{
			Id:         runIdentifier + strconv.FormatInt(run.ID, 10) + attemptSeparator + strconv.FormatInt(run.RunAttempt, 10),
			Kind:       intakev1.EventKind_EVENT_KIND_CHECK_FAILURE,
			Subject:    subject,
			Actor:      run.Actor.Login,
			Url:        run.HTMLURL,
			Summary:    excerpt(fmt.Sprintf("%s failed on %s (attempt %d)", run.Name, run.HeadBranch, run.RunAttempt)),
			OccurredAt: timestamppb.New(run.UpdatedAt),
		})
	}
	return events, nil
}

func pullRequestSubject(repository string, number uint64) *intakev1.Subject {
	return &intakev1.Subject{Repository: repository, Kind: intakev1.SubjectKind_SUBJECT_KIND_PULL_REQUEST, Number: number}
}

func firstNonEmpty(preferred string, fallback string) string {
	if preferred != "" {
		return preferred
	}
	return fallback
}

// excerpt cuts text to at most maxSummaryBytes without splitting a rune.
func excerpt(text string) string {
	if len(text) <= maxSummaryBytes {
		return text
	}
	cut := maxSummaryBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
