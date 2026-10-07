// Copyright 2026 Candace Labs

package opsview

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/upgrade"
	"github.com/candacelabs/csf/services/intake"
)

// The GitHub panel: what CSF heard from GitHub. The latest typed events with
// where each was routed, the deliveries refused or failed, and, when a csf
// release was published after this host started, the notice that csf upgrade
// installs it. It follows the GitHub event stream, the events.jsonl of the
// run directory intake.StreamAssignment.
const (
	GitHubRegion = "opsview.github"
	// EventGitHub carries the panel, as JSON in FieldPanel. It is internal:
	// the follow effect emits it, and a browser may not.
	EventGitHub = "opsview.github"
	FieldPanel  = "panel"

	githubTemplate    = "github"
	githubTitleNone   = "GitHub: nothing heard yet"
	githubTitleFormat = "GitHub: %d events, %d refused or failed"
	githubEventsShown = 10
	githubFailedShown = 5
	githubTimeFormat  = "15:04:05"
	releaseNotice     = "csf release %s was published at %s UTC, after this host started: csf upgrade installs it."
	sessionPrefix     = 8
)

// GitHubPanel is the panel's data, read from the end of the stream.
type GitHubPanel struct {
	Recorded bool              `json:"recorded"`
	Events   []GitHubEventRow  `json:"events"`
	Failures []GitHubRefusal   `json:"failures"`
	Accepted int               `json:"accepted"`
	Refused  int               `json:"refused"`
	Release  *GitHubEventRow   `json:"release,omitempty"`
	Notice   string            `json:"notice,omitempty"`
	routes   map[string]string `json:"-"`
}

// GitHubEventRow is one typed event as the panel shows it.
type GitHubEventRow struct {
	At      time.Time `json:"at"`
	Time    string    `json:"time"`
	EventID string    `json:"event_id"`
	Kind    string    `json:"kind"`
	Where   string    `json:"where"`
	Actor   string    `json:"actor"`
	Summary string    `json:"summary"`
	URL     string    `json:"url"`
	Routed  string    `json:"routed"`
}

// GitHubRefusal is one delivery the receiver refused or failed.
type GitHubRefusal struct {
	Time     string `json:"time"`
	Event    string `json:"event"`
	Delivery string `json:"delivery"`
	Outcome  string `json:"outcome"`
	Why      string `json:"why"`
}

// streamRecord is the part of a stream record the panel reads.
type streamRecord struct {
	Time       time.Time `json:"time"`
	EventType  string    `json:"event_type"`
	Delivery   string    `json:"delivery"`
	Event      string    `json:"github_event"`
	Outcome    string    `json:"outcome"`
	Refusal    string    `json:"refusal"`
	Error      string    `json:"error"`
	Kind       string    `json:"kind"`
	EventID    string    `json:"event_id"`
	Repository string    `json:"repository"`
	Number     uint64    `json:"number"`
	Branch     string    `json:"branch"`
	Actor      string    `json:"actor"`
	URL        string    `json:"url"`
	Summary    string    `json:"summary"`
	Target     string    `json:"target"`
	Detail     string    `json:"detail"`
}

const releasePublished = "release_published"

// ReadGitHubPanel folds the stream's records, oldest first, into the panel;
// hostStart decides whether the newest csf release is news.
func ReadGitHubPanel(records []byte, hostStart time.Time) GitHubPanel {
	panel := GitHubPanel{Recorded: true, routes: map[string]string{}}
	scanner := bufio.NewScanner(strings.NewReader(string(records)))
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		var record streamRecord
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			continue
		}
		panel.fold(record)
	}
	for index := range panel.Events {
		panel.Events[index].Routed = panel.routes[panel.Events[index].EventID]
	}
	slices.Reverse(panel.Events)
	slices.Reverse(panel.Failures)
	panel.Events = panel.Events[:min(githubEventsShown, len(panel.Events))]
	panel.Failures = panel.Failures[:min(githubFailedShown, len(panel.Failures))]
	if panel.Release != nil && panel.Release.At.After(hostStart) {
		panel.Notice = fmt.Sprintf(releaseNotice, panel.Release.Summary, panel.Release.Time)
	}
	return panel
}

func (panel *GitHubPanel) fold(record streamRecord) {
	switch record.EventType {
	case intake.EventTypeDelivery:
		switch intake.Outcome(record.Outcome) {
		case intake.OutcomeAccepted, intake.OutcomeIgnored, intake.OutcomeDuplicate:
			panel.Accepted++
		case intake.OutcomeRefused, intake.OutcomeFailed:
			panel.Refused++
			panel.Failures = append(panel.Failures, GitHubRefusal{
				Time: record.Time.UTC().Format(githubTimeFormat), Event: record.Event, Delivery: record.Delivery,
				Outcome: record.Outcome, Why: firstNonEmpty(record.Refusal, firstLine(record.Error)),
			})
		}
	case intake.EventTypeEvent:
		where := record.Repository
		if record.Number != 0 {
			where = fmt.Sprintf("%s#%d", where, record.Number)
		}
		if record.Branch != "" {
			where += " · " + record.Branch
		}
		row := GitHubEventRow{
			At: record.Time, Time: record.Time.UTC().Format(githubTimeFormat), EventID: record.EventID, Kind: record.Kind,
			Where: where, Actor: record.Actor, Summary: firstLine(record.Summary), URL: record.URL,
		}
		panel.Events = append(panel.Events, row)
		if record.Kind == releasePublished && csfRelease(record.Summary) {
			release := row
			panel.Release = &release
		}
	case intake.EventTypeRouted:
		routed := record.Target + " " + shortAssignment(record.Detail)
		if record.Error != "" {
			routed += " (failed: " + firstLine(record.Error) + ")"
		}
		panel.routes[record.EventID] = routed
	}
}

// csfRelease is whether a release tag is one csf upgrade installs.
func csfRelease(tag string) bool {
	return tag == upgrade.LatestTag || strings.HasPrefix(tag, upgrade.CommitTagPrefix)
}

func shortAssignment(detail string) string {
	if len(detail) > sessionPrefix && strings.Count(detail, "-") == 4 {
		return detail[:sessionPrefix]
	}
	return detail
}

func firstNonEmpty(preferred string, fallback string) string {
	if preferred != "" {
		return preferred
	}
	return fallback
}

// GitHubEvent is the event the follow effect emits when the stream grew.
func GitHubEvent(panel GitHubPanel) (live.Event, error) {
	encoded, err := json.Marshal(panel)
	if err != nil {
		return live.Event{}, err
	}
	return live.Event{Name: EventGitHub, FragmentID: GitHubRegion, Fields: live.NewFields(map[string]string{FieldPanel: string(encoded)})}, nil
}

// githubView is the panel's template data.
type githubView struct {
	GitHubPanel
	Region string
}

func renderGitHub(state viewState) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, writer io.Writer) error {
		return views.ExecuteTemplate(writer, githubTemplate, githubView{GitHubPanel: state.github, Region: GitHubRegion})
	})
}

func githubChanged(previous viewState, next viewState) bool {
	before, _ := json.Marshal(previous.github)
	after, _ := json.Marshal(next.github)
	return string(before) != string(after)
}

// githubTitle is the folded panel's line; a csf release to install shows on
// it, so the notice is seen without unfolding.
func githubTitle(state viewState) string {
	switch {
	case state.github.Notice != "":
		return state.github.Notice
	case !state.github.Recorded:
		return githubTitleNone
	}
	return fmt.Sprintf(githubTitleFormat, state.github.Accepted, state.github.Refused)
}

func githubFragment() live.Fragment[viewState] {
	return live.Fragment[viewState]{ID: GitHubRegion, Render: githubPanel, Dirty: foldedDirty(sectionGitHub, githubChanged)}
}

func githubPanel(state viewState) templ.Component {
	return foldedPanel(GitHubRegion, sectionGitHub, githubTitle(state), renderGitHub)(state)
}

// githubStream is the stream's event log under the state directory.
var githubStream = path.Join(intake.StreamAssignment, session.EventsFile)

// deliverGitHub emits the panel read from the end of the stream, when there
// is a stream.
func (view *OpsView) deliverGitHub(emit live.Emitter) error {
	records, err := view.readStreamEnd()
	if errors.Is(err, stdfs.ErrNotExist) {
		return nil
	}
	if err != nil {
		view.logger.Warn("ops view: GitHub event stream not read", "error", err)
		return nil
	}
	event, err := GitHubEvent(ReadGitHubPanel(records, view.hostStart))
	if err != nil {
		return err
	}
	return emit(event)
}

// readStreamEnd reads the stream's last tailWindow bytes, from the first
// whole record in them.
func (view *OpsView) readStreamEnd() ([]byte, error) {
	file, err := view.files.Open(githubStream)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	seeker, seekable := file.(io.Seeker)
	if !seekable || info.Size() <= tailWindow {
		return io.ReadAll(file)
	}
	if _, err := seeker.Seek(info.Size()-tailWindow, io.SeekStart); err != nil {
		return nil, err
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	if _, rest, found := strings.Cut(string(content), "\n"); found {
		return []byte(rest), nil
	}
	return nil, nil
}
