// Copyright 2026 Candace Labs

package intake

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
)

// The targets the routes record.
const (
	TargetSession    = "session"
	TargetDispatcher = "dispatcher"
)

// paragraphBreak separates a session message's parts.
const paragraphBreak = "\n\n"

// MainBranch is the branch whose merges move the dispatcher's frontier.
const MainBranch = "main"

// failedConclusions are the check conclusions that mean the branch's owner
// has work to do.
var failedConclusions = map[string]bool{"failure": true, "timed_out": true, "startup_failure": true}

// pullRequestURLPattern reads owner/name and the number out of a pull
// request's URL as a session records it.
var pullRequestURLPattern = regexp.MustCompile(`github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/([0-9]+)`)

// ISessionMessenger is the part of the harness that lists the virtual
// sessions and queues a message into one; the agent session service
// satisfies it.
type ISessionMessenger interface {
	List(ctx context.Context, request *harnessv1.ListAgentSessionsRequest) (*harnessv1.ListAgentSessionsResponse, error)
	Send(ctx context.Context, request *harnessv1.SendAgentSessionMessageRequest) (*harnessv1.SendAgentSessionMessageResponse, error)
}

// SessionRoute queues a comment, a review, an inline review comment or a
// failed check on a pull request as a message in the virtual session that
// owns it: the live session whose branch is the event's head branch or whose
// recorded pull request is the event's subject.
func SessionRoute(sessions ISessionMessenger) EventRoute {
	return func(ctx context.Context, event *intakev1.Event) (Routed, error) {
		if !forOwner(event) {
			return Routed{}, nil
		}
		listed, err := sessions.List(ctx, &harnessv1.ListAgentSessionsRequest{})
		if err != nil {
			return Routed{}, fmt.Errorf("intake: list sessions: %w", err)
		}
		owner := owningSession(listed.GetSessions(), event)
		if owner == "" {
			return Routed{}, nil
		}
		_, err = sessions.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: owner, Message: SessionMessage(event)})
		if err != nil {
			return Routed{Target: TargetSession, Detail: owner}, fmt.Errorf("intake: queue the event in session %s: %w", owner, err)
		}
		return Routed{Target: TargetSession, Detail: owner}, nil
	}
}

// forOwner is whether an event is work for the session that owns its pull
// request or branch.
func forOwner(event *intakev1.Event) bool {
	switch event.GetKind() {
	case intakev1.EventKind_EVENT_KIND_COMMENT, intakev1.EventKind_EVENT_KIND_REVIEW, intakev1.EventKind_EVENT_KIND_REVIEW_COMMENT:
		return event.GetSubject().GetKind() == intakev1.SubjectKind_SUBJECT_KIND_PULL_REQUEST
	case intakev1.EventKind_EVENT_KIND_CHECK_RUN_COMPLETED, intakev1.EventKind_EVENT_KIND_CHECK_SUITE_COMPLETED:
		return failedConclusions[event.GetConclusion()]
	case intakev1.EventKind_EVENT_KIND_CHECK_FAILURE:
		return true
	}
	return false
}

// owningSession is the live session that owns the event's branch or pull
// request; "" when none does.
func owningSession(sessions []*harnessv1.AgentSessionState, event *intakev1.Event) string {
	subject := event.GetSubject()
	for _, state := range sessions {
		if !live(state.GetPhase()) {
			continue
		}
		if event.GetBranch() != "" && state.GetBranch() == event.GetBranch() {
			return state.GetAssignmentId()
		}
		if subject.GetKind() == intakev1.SubjectKind_SUBJECT_KIND_PULL_REQUEST && names(state.GetPullRequestUrl(), subject) {
			return state.GetAssignmentId()
		}
	}
	return ""
}

func live(phase harnessv1.AgentSessionPhase) bool {
	switch phase {
	case harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_STARTING,
		harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING,
		harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN:
		return true
	}
	return false
}

// names is whether a pull request URL is the subject's.
func names(url string, subject *intakev1.Subject) bool {
	match := pullRequestURLPattern.FindStringSubmatch(url)
	if match == nil {
		return false
	}
	number, err := strconv.ParseUint(match[2], 10, 64)
	return err == nil && strings.EqualFold(match[1], subject.GetRepository()) && number == subject.GetNumber()
}

// SessionMessage is the message a session is sent for an event.
func SessionMessage(event *intakev1.Event) string {
	message := "GitHub event on your pull request, recorded on the GitHub event stream: " + EventLine(event)
	if event.GetSummary() != "" && strings.Contains(event.GetSummary(), "\n") {
		message += paragraphBreak + event.GetSummary()
	}
	if event.GetUrl() != "" {
		message += paragraphBreak + event.GetUrl()
	}
	return message
}

// MergeRoute records a pull request merged into main on the dispatcher's
// frontier: merged marks merged every slice that recorded the pull request
// and returns their identifiers.
func MergeRoute(merged func(ctx context.Context, pullRequestURL string) ([]string, error)) EventRoute {
	return func(ctx context.Context, event *intakev1.Event) (Routed, error) {
		if event.GetKind() != intakev1.EventKind_EVENT_KIND_PULL_REQUEST_MERGED || event.GetBase() != MainBranch {
			return Routed{}, nil
		}
		slices, err := merged(ctx, event.GetUrl())
		if err != nil {
			return Routed{Target: TargetDispatcher}, fmt.Errorf("intake: mark the merge on the dispatcher: %w", err)
		}
		if len(slices) == 0 {
			return Routed{}, nil
		}
		return Routed{Target: TargetDispatcher, Detail: strings.Join(slices, ",")}, nil
	}
}
