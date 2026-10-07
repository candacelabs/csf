// Copyright 2026 Candace Labs

package intake

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
)

// The X-GitHub-Event names of the webhook deliveries the receiver turns into
// events. A delivery of any other name is acknowledged and recorded as
// ignored.
const (
	WebhookPullRequest              = "pull_request"
	WebhookPullRequestReview        = "pull_request_review"
	WebhookPullRequestReviewComment = "pull_request_review_comment"
	WebhookIssueComment             = "issue_comment"
	WebhookIssues                   = "issues"
	WebhookCheckRun                 = "check_run"
	WebhookCheckSuite               = "check_suite"
	WebhookPush                     = "push"
	WebhookRelease                  = "release"
	// WebhookPing is the delivery GitHub sends when a hook is created.
	WebhookPing = "ping"
)

// The webhook actions the receiver acts on.
const (
	actionOpened    = "opened"
	actionClosed    = "closed"
	actionSubmitted = "submitted"
	actionCompleted = "completed"
	actionPublished = "published"
	branchRefPrefix = "refs/heads/"
)

// Event identifier prefixes of the kinds only webhooks carry; comments,
// reviews and review comments share the poller's.
const (
	pullRequestIdentifier = "github/pull_request/"
	issueIdentifier       = "github/issue/"
	checkRunIdentifier    = "github/check_run/"
	checkSuiteIdentifier  = "github/check_suite/"
	pushIdentifier        = "github/push/"
	releaseIdentifier     = "github/release/"
	identifierSeparator   = "/"
)

// The webhook JSON shapes below are projections of the fields the receiver
// reads; every other field of GitHub's payload is discarded here and kept
// only in the delivery's raw copy.

type webhookRepository struct {
	FullName string `json:"full_name"`
}

type webhookRef struct {
	Ref string `json:"ref"`
}

type webhookPullRequest struct {
	ID        int64      `json:"id"`
	Number    uint64     `json:"number"`
	Title     string     `json:"title"`
	HTMLURL   string     `json:"html_url"`
	Merged    bool       `json:"merged"`
	Head      webhookRef `json:"head"`
	Base      webhookRef `json:"base"`
	User      githubUser `json:"user"`
	CreatedAt time.Time  `json:"created_at"`
	ClosedAt  time.Time  `json:"closed_at"`
	MergedAt  time.Time  `json:"merged_at"`
}

type webhookComment struct {
	ID        int64      `json:"id"`
	HTMLURL   string     `json:"html_url"`
	Body      string     `json:"body"`
	User      githubUser `json:"user"`
	CreatedAt time.Time  `json:"created_at"`
}

type webhookReview struct {
	ID          int64      `json:"id"`
	State       string     `json:"state"`
	Body        string     `json:"body"`
	HTMLURL     string     `json:"html_url"`
	User        githubUser `json:"user"`
	SubmittedAt time.Time  `json:"submitted_at"`
}

type webhookIssue struct {
	ID          int64           `json:"id"`
	Number      uint64          `json:"number"`
	Title       string          `json:"title"`
	HTMLURL     string          `json:"html_url"`
	PullRequest json.RawMessage `json:"pull_request"`
	User        githubUser      `json:"user"`
	CreatedAt   time.Time       `json:"created_at"`
	ClosedAt    time.Time       `json:"closed_at"`
}

type webhookCheckSuite struct {
	ID           int64                `json:"id"`
	HeadBranch   string               `json:"head_branch"`
	Conclusion   string               `json:"conclusion"`
	UpdatedAt    time.Time            `json:"updated_at"`
	PullRequests []webhookPullRequest `json:"pull_requests"`
	App          struct {
		Name string `json:"name"`
	} `json:"app"`
}

type webhookCheckRun struct {
	ID           int64                `json:"id"`
	Name         string               `json:"name"`
	Conclusion   string               `json:"conclusion"`
	HTMLURL      string               `json:"html_url"`
	CompletedAt  time.Time            `json:"completed_at"`
	CheckSuite   webhookCheckSuite    `json:"check_suite"`
	PullRequests []webhookPullRequest `json:"pull_requests"`
}

type webhookCommit struct {
	ID        string    `json:"id"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

type webhookRelease struct {
	ID          int64      `json:"id"`
	TagName     string     `json:"tag_name"`
	Name        string     `json:"name"`
	HTMLURL     string     `json:"html_url"`
	Author      githubUser `json:"author"`
	PublishedAt time.Time  `json:"published_at"`
}

type webhookPayload struct {
	Action      string             `json:"action"`
	Repository  webhookRepository  `json:"repository"`
	Sender      githubUser         `json:"sender"`
	PullRequest webhookPullRequest `json:"pull_request"`
	Comment     webhookComment     `json:"comment"`
	Review      webhookReview      `json:"review"`
	Issue       webhookIssue       `json:"issue"`
	CheckRun    webhookCheckRun    `json:"check_run"`
	CheckSuite  webhookCheckSuite  `json:"check_suite"`
	Release     webhookRelease     `json:"release"`
	// A push's fields.
	Ref        string        `json:"ref"`
	After      string        `json:"after"`
	Compare    string        `json:"compare"`
	HeadCommit webhookCommit `json:"head_commit"`
}

// webhookNormalizers turns one delivery of a known X-GitHub-Event name into
// zero or more events: zero when the action is not one CSF acts on. A name
// with no entry is ignored.
var webhookNormalizers = map[string]func(payload webhookPayload) []*intakev1.Event{
	WebhookPullRequest:              normalizePullRequestDelivery,
	WebhookPullRequestReview:        normalizeReviewDelivery,
	WebhookPullRequestReviewComment: normalizeReviewCommentDelivery,
	WebhookIssueComment:             normalizeIssueCommentDelivery,
	WebhookIssues:                   normalizeIssueDelivery,
	WebhookCheckRun:                 normalizeCheckRunDelivery,
	WebhookCheckSuite:               normalizeCheckSuiteDelivery,
	WebhookPush:                     normalizePushDelivery,
	WebhookRelease:                  normalizeReleaseDelivery,
}

// NormalizeDelivery turns one webhook delivery into its typed events, each
// stamped with the delivery's identifier. handled is false for an event name
// the receiver does not act on.
func NormalizeDelivery(name string, delivery string, body []byte) (events []*intakev1.Event, handled bool, err error) {
	normalize, handled := webhookNormalizers[name]
	if !handled {
		return nil, false, nil
	}
	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, true, fmt.Errorf("intake: decode %s delivery %s: %w", name, delivery, err)
	}
	events = normalize(payload)
	for _, event := range events {
		event.Delivery = delivery
	}
	return events, true, nil
}

func normalizePullRequestDelivery(payload webhookPayload) []*intakev1.Event {
	pull := payload.PullRequest
	event := &intakev1.Event{
		Subject: pullRequestSubject(payload.Repository.FullName, pull.Number),
		Actor:   payload.Sender.Login,
		Url:     pull.HTMLURL,
		Summary: excerpt(pull.Title),
		Branch:  pull.Head.Ref,
		Base:    pull.Base.Ref,
	}
	switch {
	case payload.Action == actionOpened:
		event.Kind, event.OccurredAt = intakev1.EventKind_EVENT_KIND_PULL_REQUEST_OPENED, timestamppb.New(pull.CreatedAt)
	case payload.Action == actionClosed && pull.Merged:
		event.Kind, event.OccurredAt = intakev1.EventKind_EVENT_KIND_PULL_REQUEST_MERGED, timestamppb.New(pull.MergedAt)
	case payload.Action == actionClosed:
		event.Kind, event.OccurredAt = intakev1.EventKind_EVENT_KIND_PULL_REQUEST_CLOSED, timestamppb.New(pull.ClosedAt)
	default:
		return nil
	}
	event.Id = pullRequestIdentifier + strconv.FormatInt(pull.ID, 10) + identifierSeparator + kindWord(event.Kind)
	return []*intakev1.Event{event}
}

func normalizeReviewDelivery(payload webhookPayload) []*intakev1.Event {
	if payload.Action != actionSubmitted {
		return nil
	}
	review := payload.Review
	summary := strings.ToLower(review.State)
	if review.Body != "" {
		summary += ": " + review.Body
	}
	return []*intakev1.Event{{
		Id:         reviewIdentifier + strconv.FormatInt(review.ID, 10),
		Kind:       intakev1.EventKind_EVENT_KIND_REVIEW,
		Subject:    pullRequestSubject(payload.Repository.FullName, payload.PullRequest.Number),
		Actor:      firstNonEmpty(review.User.Login, payload.Sender.Login),
		Url:        review.HTMLURL,
		Summary:    excerpt(summary),
		OccurredAt: timestamppb.New(review.SubmittedAt),
		Branch:     payload.PullRequest.Head.Ref,
		Base:       payload.PullRequest.Base.Ref,
		Conclusion: strings.ToLower(review.State),
	}}
}

func normalizeReviewCommentDelivery(payload webhookPayload) []*intakev1.Event {
	if payload.Action != createdAction {
		return nil
	}
	comment := payload.Comment
	return []*intakev1.Event{{
		Id:         reviewCommentIdentifier + strconv.FormatInt(comment.ID, 10),
		Kind:       intakev1.EventKind_EVENT_KIND_REVIEW_COMMENT,
		Subject:    pullRequestSubject(payload.Repository.FullName, payload.PullRequest.Number),
		Actor:      firstNonEmpty(comment.User.Login, payload.Sender.Login),
		Url:        comment.HTMLURL,
		Summary:    excerpt(comment.Body),
		OccurredAt: timestamppb.New(comment.CreatedAt),
		Branch:     payload.PullRequest.Head.Ref,
		Base:       payload.PullRequest.Base.Ref,
	}}
}

// normalizeIssueCommentDelivery covers a comment on an issue and on a pull
// request's conversation; GitHub sends both as issue_comment, and the
// payload carries no head branch, so routing a pull request's comment keys on
// its number.
func normalizeIssueCommentDelivery(payload webhookPayload) []*intakev1.Event {
	if payload.Action != createdAction {
		return nil
	}
	comment := payload.Comment
	return []*intakev1.Event{{
		Id:         commentIdentifier + strconv.FormatInt(comment.ID, 10),
		Kind:       intakev1.EventKind_EVENT_KIND_COMMENT,
		Subject:    issueSubject(payload.Repository.FullName, payload.Issue),
		Actor:      firstNonEmpty(comment.User.Login, payload.Sender.Login),
		Url:        comment.HTMLURL,
		Summary:    excerpt(comment.Body),
		OccurredAt: timestamppb.New(comment.CreatedAt),
	}}
}

func normalizeIssueDelivery(payload webhookPayload) []*intakev1.Event {
	issue := payload.Issue
	event := &intakev1.Event{
		Subject: issueSubject(payload.Repository.FullName, issue),
		Actor:   payload.Sender.Login,
		Url:     issue.HTMLURL,
		Summary: excerpt(issue.Title),
	}
	switch payload.Action {
	case actionOpened:
		event.Kind, event.OccurredAt = intakev1.EventKind_EVENT_KIND_ISSUE_OPENED, timestamppb.New(issue.CreatedAt)
	case actionClosed:
		event.Kind, event.OccurredAt = intakev1.EventKind_EVENT_KIND_ISSUE_CLOSED, timestamppb.New(issue.ClosedAt)
	default:
		return nil
	}
	event.Id = issueIdentifier + strconv.FormatInt(issue.ID, 10) + identifierSeparator + kindWord(event.Kind)
	return []*intakev1.Event{event}
}

func normalizeCheckRunDelivery(payload webhookPayload) []*intakev1.Event {
	if payload.Action != actionCompleted {
		return nil
	}
	run := payload.CheckRun
	return []*intakev1.Event{{
		Id:         checkRunIdentifier + strconv.FormatInt(run.ID, 10),
		Kind:       intakev1.EventKind_EVENT_KIND_CHECK_RUN_COMPLETED,
		Subject:    checkSubject(payload.Repository.FullName, run.PullRequests),
		Actor:      payload.Sender.Login,
		Url:        run.HTMLURL,
		Summary:    excerpt(fmt.Sprintf("%s %s on %s", run.Name, run.Conclusion, run.CheckSuite.HeadBranch)),
		OccurredAt: timestamppb.New(run.CompletedAt),
		Branch:     run.CheckSuite.HeadBranch,
		Conclusion: run.Conclusion,
	}}
}

func normalizeCheckSuiteDelivery(payload webhookPayload) []*intakev1.Event {
	if payload.Action != actionCompleted {
		return nil
	}
	suite := payload.CheckSuite
	return []*intakev1.Event{{
		Id:         checkSuiteIdentifier + strconv.FormatInt(suite.ID, 10),
		Kind:       intakev1.EventKind_EVENT_KIND_CHECK_SUITE_COMPLETED,
		Subject:    checkSubject(payload.Repository.FullName, suite.PullRequests),
		Actor:      payload.Sender.Login,
		Summary:    excerpt(fmt.Sprintf("%s %s on %s", suite.App.Name, suite.Conclusion, suite.HeadBranch)),
		OccurredAt: timestamppb.New(suite.UpdatedAt),
		Branch:     suite.HeadBranch,
		Conclusion: suite.Conclusion,
	}}
}

// normalizePushDelivery covers a push to a branch; a tag push or a branch
// deletion is not one.
func normalizePushDelivery(payload webhookPayload) []*intakev1.Event {
	branch, isBranch := strings.CutPrefix(payload.Ref, branchRefPrefix)
	if !isBranch || payload.HeadCommit.ID == "" {
		return nil
	}
	return []*intakev1.Event{{
		Id:         pushIdentifier + payload.Repository.FullName + identifierSeparator + branch + identifierSeparator + payload.After,
		Kind:       intakev1.EventKind_EVENT_KIND_PUSH,
		Subject:    &intakev1.Subject{Repository: payload.Repository.FullName, Kind: intakev1.SubjectKind_SUBJECT_KIND_REPOSITORY},
		Actor:      payload.Sender.Login,
		Url:        payload.Compare,
		Summary:    excerpt(firstLine(payload.HeadCommit.Message)),
		OccurredAt: timestamppb.New(payload.HeadCommit.Timestamp),
		Branch:     branch,
	}}
}

func normalizeReleaseDelivery(payload webhookPayload) []*intakev1.Event {
	if payload.Action != actionPublished {
		return nil
	}
	release := payload.Release
	return []*intakev1.Event{{
		Id:         releaseIdentifier + strconv.FormatInt(release.ID, 10),
		Kind:       intakev1.EventKind_EVENT_KIND_RELEASE_PUBLISHED,
		Subject:    &intakev1.Subject{Repository: payload.Repository.FullName, Kind: intakev1.SubjectKind_SUBJECT_KIND_REPOSITORY},
		Actor:      firstNonEmpty(release.Author.Login, payload.Sender.Login),
		Url:        release.HTMLURL,
		Summary:    excerpt(release.TagName),
		OccurredAt: timestamppb.New(release.PublishedAt),
	}}
}

func issueSubject(repository string, issue webhookIssue) *intakev1.Subject {
	kind := intakev1.SubjectKind_SUBJECT_KIND_ISSUE
	if len(issue.PullRequest) > 0 && string(issue.PullRequest) != jsonNull {
		kind = intakev1.SubjectKind_SUBJECT_KIND_PULL_REQUEST
	}
	return &intakev1.Subject{Repository: repository, Kind: kind, Number: issue.Number}
}

// checkSubject is the first pull request a check ran for, or the repository
// when it ran on a branch with none.
func checkSubject(repository string, pulls []webhookPullRequest) *intakev1.Subject {
	if len(pulls) > 0 {
		return pullRequestSubject(repository, pulls[0].Number)
	}
	return &intakev1.Subject{Repository: repository, Kind: intakev1.SubjectKind_SUBJECT_KIND_REPOSITORY}
}

// kindWord is an event kind's name without its enum prefix, lower case:
// "pull_request_merged".
func kindWord(kind intakev1.EventKind) string {
	return strings.ToLower(strings.TrimPrefix(kind.String(), eventKindPrefix))
}

const eventKindPrefix = "EVENT_KIND_"

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}
