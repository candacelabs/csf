// Copyright 2026 Candace Labs

package opsview

import (
	"encoding/json"
	"fmt"
	"html/template"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/services/harness"
)

// The host panel's identity on the wire: one fragment at the top of the
// Workbench, fed by an effect that reads the host and verifies every button.
const (
	// notAvailable is a gauge the kernel does not report.
	notAvailable = "n/a"
	HostRegion   = "opsview.host"
	// EventHost carries the panel, as JSON, in FieldHost. It is internal.
	EventHost = "opsview.host"
	// EventHostDone carries a press's outcome in FieldNotice. It is internal.
	EventHostDone = "opsview.host.done"
	// EventHostPress is a verified button: FieldAction names it.
	EventHostPress = "opsview.host.press"
	// EventHostConfirm runs the action awaiting its confirm, FieldAction.
	EventHostConfirm = "opsview.host.confirm"
	// EventHostDismiss closes the confirm without acting.
	EventHostDismiss = "opsview.host.dismiss"
	// EventHostProfile is the profile editor's submission.
	EventHostProfile = "opsview.host.profile"

	FieldHost        = "host"
	FieldAction      = "action"
	FieldProfileName = "name"
	FieldProfileKeep = "keep"
	FieldKeepCSF     = "keep_csf_work"
	FieldStopOthers  = "stop_others"
	FieldRemove      = "remove"

	sourceHost      = "opsview.host"
	sourceHostPress = "opsview.host.press"
	hostTemplate    = "host"
	// hostRefresh is how often the panel reads the host and re-verifies its
	// buttons; a press reads it again at once. Docker's own stats view
	// samples every second; five keeps forty containers' samples well under
	// a percent of one core while the cpu column stays current to a glance.
	hostRefresh = 5 * time.Second
	checkboxOn  = "on"
	gibibyte    = 1 << 30

	actionUndo  = "undo"
	actionHold  = "hold"
	idSeparator = "/"
)

// HostPanel is what the effect delivers: the host read, and every button
// with its check's verdict.
type HostPanel struct {
	Report  HostReport                     `json:"report"`
	Actions []widget.Verified[HostRequest] `json:"actions"`
	Error   string                         `json:"error,omitempty"`
}

// hostState is the panel in one connection: the delivered panel, the action
// awaiting its confirm, and what the last press reported.
type hostState struct {
	Panel      HostPanel
	encoded    string
	Confirming string
	Pending    bool
	Notice     string
}

// WithHostOperations gives the Workbench its host panel, a client of
// operations.
func WithHostOperations(operations *HostOperations) Option {
	return func(view *OpsView) error {
		if operations == nil {
			return ErrNoHostContainers
		}
		view.host = operations
		return nil
	}
}

// hostActions is every button the panel draws for one report.
func hostActions(report HostReport) []widget.VerifiedAction[HostRequest] {
	var actions []widget.VerifiedAction[HostRequest]
	for _, profile := range report.Settings.Profiles {
		actions = append(actions, widget.VerifiedAction[HostRequest]{ID: string(OperationProfile) + idSeparator + profile.Name, Label: "Apply " + profile.Name,
			Request: HostRequest{Operation: OperationProfile, Profile: profile.Name}})
	}
	actions = append(actions,
		widget.VerifiedAction[HostRequest]{ID: actionUndo, Label: "Undo last profile", Request: HostRequest{Operation: OperationUndo}},
		widget.VerifiedAction[HostRequest]{ID: actionHold, Label: "Hold new sessions", Request: HostRequest{Operation: OperationHold}})
	for _, group := range HostGroups {
		for _, action := range []ContainerAction{ActionStop, ActionPause, ActionUnpause, ActionStart} {
			actions = append(actions, widget.VerifiedAction[HostRequest]{ID: groupActionID(group, action), Label: titleCase(string(action)) + " all",
				Request: HostRequest{Operation: OperationControl, Action: action, Group: group}})
		}
	}
	for _, found := range report.Containers {
		for _, action := range containerActions(found.State) {
			actions = append(actions, widget.VerifiedAction[HostRequest]{ID: containerActionID(string(action), found.Name), Label: titleCase(string(action)),
				Request: HostRequest{Operation: OperationControl, Action: action, Names: []string{found.Name}}})
		}
		label, verb := "Protect", string(OperationProtect)
		if found.Protected {
			label, verb = "Unprotect", "un"+string(OperationProtect)
		}
		actions = append(actions, widget.VerifiedAction[HostRequest]{ID: containerActionID(verb, found.Name), Label: label,
			Request: HostRequest{Operation: OperationProtect, Names: []string{found.Name}, Protected: !found.Protected}})
	}
	return actions
}

// containerActions are the buttons a container in one state shows.
func containerActions(state string) []ContainerAction {
	switch container.ContainerState(state) {
	case container.StateRunning:
		return []ContainerAction{ActionStop, ActionPause}
	case container.StatePaused:
		return []ContainerAction{ActionUnpause, ActionStop}
	case container.StateRestarting:
		return []ContainerAction{ActionStop}
	case container.StateExited, container.StateCreated:
		return []ContainerAction{ActionStart}
	}
	return nil
}

func containerActionID(verb string, name string) string { return verb + idSeparator + name }

func groupActionID(group HostGroup, action ContainerAction) string {
	return string(group) + idSeparator + string(action)
}

func titleCase(word string) string {
	if word == "" {
		return word
	}
	return strings.ToUpper(word[:1]) + word[1:]
}

// HostEvent is the event the effect emits with a fresh panel.
func HostEvent(panel HostPanel) (live.Event, error) {
	encoded, err := json.Marshal(panel)
	if err != nil {
		return live.Event{}, err
	}
	return live.Event{Name: EventHost, FragmentID: HostRegion, Fields: live.NewFields(map[string]string{FieldHost: string(encoded)})}, nil
}

// splitNames reads a comma- or line-separated list of names.
func splitNames(text string) []string {
	var names []string
	for _, name := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func hostChanged(previous viewState, next viewState) bool {
	before, after := previous.host, next.host
	return before.encoded != after.encoded || before.Confirming != after.Confirming || before.Pending != after.Pending || before.Notice != after.Notice
}

// hostView is the panel region's data, every value already rendered.
type hostView struct {
	Region     string
	Operable   bool
	Present    bool
	Error      string
	Notice     string
	Pending    bool
	Load       string
	Memory     string
	Disk       string
	Pressure   string
	Resumes    *harness.ResumeQueue
	Overloaded bool
	Findings   []string
	Confirm    *hostConfirmView
	Guard      []template.HTML
	Profiles   []hostProfileView
	Undo       template.HTML
	Undoable   string
	Groups     []hostGroupView
	OnProfile  template.HTMLAttr
}

type hostConfirmView struct {
	ID, Label, Result string
	Names             []string
	OnConfirm         template.HTMLAttr
	OnDismiss         template.HTMLAttr
}

type hostProfileView struct {
	HostProfile
	KeepText string
	Button   template.HTML
}

type hostGroupView struct {
	Group   HostGroup
	Title   string
	Live    []hostRowView
	Stopped []hostRowView
	Buttons []template.HTML
}

type hostRowView struct {
	HostContainer
	CPU     string
	Memory  string
	Buttons []template.HTML
}

// pressureText is one pressure gauge as the panel shows it: a percent, or
// "n/a" where the kernel reports none.
func pressureText(percent float64) string {
	if percent < 0 {
		return notAvailable
	}
	return fmt.Sprintf("%.1f%%", percent)
}

func groupTitle(group HostGroup) string {
	if group == GroupCSF {
		return "CSF work"
	}
	return "Everything else"
}

// hostEvents are the browser events the panel registers.
var hostEvents = []string{EventHostPress, EventHostConfirm, EventHostDismiss, EventHostProfile}

// isHostEvent reports whether the panel's reducer owns event.
func isHostEvent(name string) bool {
	return name == EventHost || name == EventHostDone || slices.Contains(hostEvents, name)
}
