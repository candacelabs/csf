// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/services/ouroboros"
)

// The merges tile and list on the wire. Both are internal events: the follow
// effect asks for a refresh, and the refresh effect delivers what the forge
// reports.
const (
	// EventRefreshMerges asks for the merges on main to be read again: when
	// the page connects, and whenever a session ends a turn, closes or has its
	// pull request merged, which is when a merge can have happened.
	EventRefreshMerges = "opsview.refresh_merges"
	// EventMerges carries the pull requests merged into main today, as JSON
	// in FieldMerges.
	EventMerges = "opsview.merges"
	FieldMerges = "merges"
	// EventToday carries the UTC day by the view's clock, in FieldDay.
	EventToday = "opsview.today"
	FieldDay   = "day"

	sourceMerges   = "opsview.merges"
	programGitHub  = "gh"
	githubHost     = "github.com"
	mainBranch     = "main"
	mergedLimit    = "200"
	mergedFields   = "number,title,url,mergedAt"
	mergedSearch   = "merged:>="
	pullPathMarker = "pull"
)

// ErrNoLauncher reports WithGitHub given no process capability.
var ErrNoLauncher = errors.New("ops view: the process capability is required to read merges")

// MergedPullRequest is one pull request merged into main, as gh lists it.
type MergedPullRequest struct {
	Number   int       `json:"number"`
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	MergedAt time.Time `json:"mergedAt"`
}

// WithGitHub lets the page read the pull requests merged into main today from
// the forge, through gh started by launcher, for every repository a session's
// pull request lives in. Without it the merges tile says it has no source.
func WithGitHub(launcher proc.ILauncher) Option {
	return func(view *OpsView) error {
		if launcher == nil {
			return ErrNoLauncher
		}
		view.github = launcher
		return nil
	}
}

// WithClock replaces the clock that says which UTC day is today.
func WithClock(source clock.IClock) Option {
	return func(view *OpsView) error {
		if source == nil {
			return ErrNoClock
		}
		view.clock = source
		return nil
	}
}

// WithHostStart tells the page when the host serving it started, so a
// session whose log still says running but whose last record is older reads
// as stalled.
func WithHostStart(startedAt time.Time) Option {
	return func(view *OpsView) error {
		view.hostStart = startedAt
		return nil
	}
}

// ErrNoClock reports WithClock given no clock.
var ErrNoClock = errors.New("ops view: the clock is required")

// TodayEvent tells the page which UTC day is today.
func TodayEvent(day string) live.Event {
	return live.Event{Name: EventToday, FragmentID: SummaryRegion, Fields: live.NewFields(map[string]string{FieldDay: day})}
}

// repositoryOf is the owner/name of a GitHub pull request URL, or empty.
func repositoryOf(pullRequest string) string {
	parsed, err := url.Parse(pullRequest)
	if err != nil || parsed.Host != githubHost {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != pullPathMarker {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// repositoriesOf are the repositories the sessions' pull requests live in,
// sorted.
func repositoriesOf(cards []SessionCard) []string {
	var repositories []string
	for _, card := range cards {
		if repository := repositoryOf(card.PullRequestURL); repository != "" && !slices.Contains(repositories, repository) {
			repositories = append(repositories, repository)
		}
	}
	slices.Sort(repositories)
	return repositories
}

// The open pull requests on the wire, and what gh is asked for them.
const (
	// EventPulls carries the sessions' open pull requests' diff stat and
	// checks, as JSON in FieldPulls.
	EventPulls  = "opsview.pulls"
	FieldPulls  = "pulls"
	sourcePulls = "opsview.pulls"
	openLimit   = "200"
	openFields  = "url,additions,deletions,changedFiles,statusCheckRollup"
	stateOpen   = "open"
)

// The checks of a pull request, rolled up as a row shows them.
const (
	ChecksPassing = "passing"
	ChecksFailing = "failing"
	ChecksPending = "pending"
)

// OpenPullRequest is one open pull request's diff stat and checks.
type OpenPullRequest struct {
	URL          string `json:"url"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	ChangedFiles int    `json:"changedFiles"`
	// Checks is the rollup: failing if any check failed, pending if any has
	// not finished, passing if all passed, empty with no checks.
	Checks string `json:"checks"`
}

// statusCheck is one entry of gh's statusCheckRollup: a check run carries a
// status and a conclusion, a commit status a state.
type statusCheck struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// The check outcomes the rollup reads, as GitHub spells them.
var (
	failedOutcomes  = []string{"FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE", "ERROR"}
	pendingOutcomes = []string{"PENDING", "EXPECTED", "QUEUED", "IN_PROGRESS", "WAITING", "REQUESTED"}
)

// rollup is a pull request's checks in a word.
func rollup(checks []statusCheck) string {
	if len(checks) == 0 {
		return ""
	}
	verdict := ChecksPassing
	for _, check := range checks {
		switch {
		case slices.Contains(failedOutcomes, check.Conclusion) || slices.Contains(failedOutcomes, check.State):
			return ChecksFailing
		case slices.Contains(pendingOutcomes, check.Status) || slices.Contains(pendingOutcomes, check.State):
			verdict = ChecksPending
		}
	}
	return verdict
}

// readPulls is the effect that lists the open pull requests in each
// repository and delivers their diff stat and checks. A repository gh cannot
// list is logged and left out.
func (view *OpsView) readPulls(repositories []string) live.Effect[ViewerIdentity] {
	return live.Effect[ViewerIdentity]{Source: sourcePulls, Run: func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
		var open []OpenPullRequest
		for _, repository := range repositories {
			result, err := view.github.Run(ctx, proc.Command{Executable: programGitHub, Arguments: []string{
				"pr", "list", "--repo", repository, "--state", stateOpen, "--limit", openLimit, "--json", openFields,
			}})
			var listed []struct {
				OpenPullRequest
				StatusCheckRollup []statusCheck `json:"statusCheckRollup"`
			}
			if err == nil {
				err = json.Unmarshal(result.Stdout, &listed)
			}
			if err != nil {
				view.logger.Warn("ops view: open pull requests not read", "repository", repository, "error", err)
				continue
			}
			for _, pull := range listed {
				pull.Checks = rollup(pull.StatusCheckRollup)
				open = append(open, pull.OpenPullRequest)
			}
		}
		encoded, err := json.Marshal(open)
		if err != nil {
			return err
		}
		return emit(live.Event{Name: EventPulls, FragmentID: SummaryRegion, Fields: live.NewFields(map[string]string{FieldPulls: string(encoded)})})
	}}
}

// readMerges is the effect that lists the pull requests merged into main on
// day in each repository and delivers them, newest first. A repository gh
// cannot list is logged and left out; the tile counts what could be read.
func (view *OpsView) readMerges(repositories []string, day string) live.Effect[ViewerIdentity] {
	return live.Effect[ViewerIdentity]{Source: sourceMerges, Run: func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
		var merged []MergedPullRequest
		for _, repository := range repositories {
			result, err := view.github.Run(ctx, proc.Command{Executable: programGitHub, Arguments: []string{
				"pr", "list", "--repo", repository, "--base", mainBranch, "--state", "merged",
				"--search", mergedSearch + day, "--limit", mergedLimit, "--json", mergedFields,
			}})
			var listed []MergedPullRequest
			if err == nil {
				err = json.Unmarshal(result.Stdout, &listed)
			}
			if err != nil {
				view.logger.Warn("ops view: merges not read", "repository", repository, "error", err)
				continue
			}
			for _, pull := range listed {
				if pull.MergedAt.UTC().Format(time.DateOnly) == day {
					merged = append(merged, pull)
				}
			}
		}
		slices.SortFunc(merged, func(a, b MergedPullRequest) int { return b.MergedAt.Compare(a.MergedAt) })
		encoded, err := json.Marshal(merged)
		if err != nil {
			return err
		}
		return emit(live.Event{Name: EventMerges, FragmentID: SummaryRegion, Fields: live.NewFields(map[string]string{FieldMerges: string(encoded), FieldDay: day})})
	}}
}

// mergesState is what the page knows of today's merges: present once the
// forge answered for the day it was asked about.
type mergesState struct {
	Present bool
	Day     string
	Pulls   []MergedPullRequest
}

// reduceMerges is the merges' transition: a refresh starts the read when the
// page has a forge to read, and the delivery replaces the list and marks the
// cards whose pull requests landed.
func (view *OpsView) reduceMerges(state viewState, event live.Event) (viewState, []live.Effect[ViewerIdentity]) {
	switch event.Name {
	case EventToday:
		state.today = event.Fields.Get(FieldDay)
	case EventRefreshMerges:
		if view.github == nil || state.today == "" {
			return state, nil
		}
		var cards []SessionCard
		for _, item := range state.cards.Items() {
			cards = append(cards, item.State)
		}
		if repositories := repositoriesOf(cards); len(repositories) > 0 {
			return state, []live.Effect[ViewerIdentity]{view.readMerges(repositories, state.today), view.readPulls(repositories)}
		}
	case EventPulls:
		var pulls []OpenPullRequest
		if json.Unmarshal([]byte(event.Fields.Get(FieldPulls)), &pulls) != nil {
			return state, nil
		}
		for _, item := range state.cards.Items() {
			index := slices.IndexFunc(pulls, func(pull OpenPullRequest) bool { return pull.URL == item.State.PullRequestURL })
			if index < 0 || item.State.Forge == pulls[index] {
				continue
			}
			card := item.State
			card.Forge = pulls[index]
			if next, err := state.cards.Upsert(item.Key, card); err == nil {
				state.cards = next
			}
		}
	case EventMerges:
		var pulls []MergedPullRequest
		if json.Unmarshal([]byte(event.Fields.Get(FieldMerges)), &pulls) != nil {
			return state, nil
		}
		state.merges = mergesState{Present: true, Day: event.Fields.Get(FieldDay), Pulls: pulls}
		for _, item := range state.cards.Items() {
			landed := slices.ContainsFunc(pulls, func(pull MergedPullRequest) bool { return pull.URL == item.State.PullRequestURL })
			if landed != item.State.Landed {
				card := item.State
				card.Landed = landed
				if next, err := state.cards.Upsert(item.Key, card); err == nil {
					state.cards = next
				}
			}
		}
	}
	return state, nil
}

// isFixer is whether a card is a fixer session the mining loop launched,
// whose spend counts against the loop's daily cap.
func isFixer(card SessionCard) bool { return card.Agent == ouroboros.FixerAgentID }
