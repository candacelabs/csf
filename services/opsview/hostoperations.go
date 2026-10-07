// Copyright 2026 Candace Labs

package opsview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdfs "io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/io/ipc/docker"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/pkg/atomicfile"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/services/harness"
)

// The host operations: what runs on the machine, and the operator's controls
// over it. Each is one typed operation, served over HTTP here and over MCP by
// the host app, and the Workbench's host panel is a client of the same calls.
const (
	GetHostOperation           = "get_host"
	ControlContainersOperation = "control_containers"
	ApplyHostProfileOperation  = "apply_host_profile"
	UndoHostProfileOperation   = "undo_host_profile"
	PutHostProfileOperation    = "put_host_profile"
	ProtectContainersOperation = "protect_containers"

	// HostSettingsFile holds the operator's host profiles and protected
	// containers, under the state directory.
	HostSettingsFile = "host.json"
	// HostLedgerFile is every applied profile and undo, one record per line.
	HostLedgerFile = "host.jsonl"
	// OnlyCSFWork is the profile every host starts with: stop everything that
	// is not CSF work.
	OnlyCSFWork = "only CSF work"

	hostOperationsPath = "/api/host/"
	hostFileMode       = 0o600
	profileNameLimit   = 64
)

var (
	// ErrUnconfirmed reports a change that takes something away, or changes
	// more than one container, without the confirm naming exactly what it
	// changes on the host now.
	ErrUnconfirmed = errors.New("confirm by naming exactly the containers it changes")
	// ErrNoProfile reports a profile name the settings do not hold.
	ErrNoProfile = errors.New("no host profile has that name")
	// ErrNothingToUndo reports an undo with no applied profile left to undo.
	ErrNothingToUndo = errors.New("no applied profile is left to undo")
	// ErrInvalidProfile reports a profile record that cannot be kept.
	ErrInvalidProfile = errors.New("a host profile needs a one-line name of at most 64 characters, and container names that are not empty")
	// ErrAcceptance reports a change the host did not end up in.
	ErrAcceptance = errors.New("acceptance check failed")
)

// HostProfile is one operator profile: a named set of containers to keep
// running, started when stopped, and whether everything else stops.
type HostProfile struct {
	Name string `json:"name" jsonschema:"the profile's name, as the panel shows it"`
	// Keep names containers to keep running: each that is stopped starts.
	Keep []string `json:"keep,omitempty" jsonschema:"container names to keep running; each one that is stopped starts"`
	// KeepCSFWork leaves CSF work running when the profile stops the rest.
	KeepCSFWork bool `json:"keep_csf_work,omitempty" jsonschema:"leave the harness's sessions and their build containers running"`
	// StopOthers stops every running or paused container the profile does not
	// keep. Protected containers are never stopped.
	StopOthers bool `json:"stop_others,omitempty" jsonschema:"stop every running or paused container the profile does not keep; protected ones never stop"`
}

// HostSettings is the operator's host record: the profiles and the
// containers nothing on the panel may stop or pause.
type HostSettings struct {
	Profiles  []HostProfile `json:"profiles"`
	Protected []string      `json:"protected"`
}

// defaultHostSettings is a host with no record yet.
func defaultHostSettings() HostSettings {
	return HostSettings{Profiles: []HostProfile{{Name: OnlyCSFWork, KeepCSFWork: true, StopOthers: true}}, Protected: []string{}}
}

// AppliedProfile is one ledger record: a profile applied, or an undo of one,
// with exactly the changes made.
type AppliedProfile struct {
	At      time.Time         `json:"at"`
	Profile string            `json:"profile"`
	Changes []ContainerChange `json:"changes"`
	// Undoes is the time of the record this one undid; empty on an apply.
	Undoes *time.Time `json:"undoes,omitempty"`
}

// HostOperation names what a host request does.
type HostOperation string

const (
	OperationControl HostOperation = "control"
	OperationProfile HostOperation = "profile"
	OperationUndo    HostOperation = "undo"
	OperationProtect HostOperation = "protect"
	OperationHold    HostOperation = "hold"
)

// HostRequest is one host operation as the panel's buttons carry it: the
// exact request each button's check dry-runs and its press runs.
type HostRequest struct {
	Operation HostOperation   `json:"operation"`
	Action    ContainerAction `json:"action,omitempty"`
	Names     []string        `json:"names,omitempty"`
	Group     HostGroup       `json:"group,omitempty"`
	Profile   string          `json:"profile,omitempty"`
	Protected bool            `json:"protected,omitempty"`
}

// GetHostInput takes nothing.
type GetHostInput struct{}

// HostReport is the machine now: its gauges and containers, the operator's
// settings, and the applied profile an undo would reverse.
type HostReport struct {
	HostSnapshot
	Settings HostSettings `json:"settings"`
	// Undoable is the applied profile an undo reverses, when there is one.
	Undoable *AppliedProfile `json:"undoable,omitempty"`
	// Holds is true when sessions can be held from the panel.
	Holds bool `json:"holds"`
}

// ControlContainersInput is one action on named containers, or on every
// container of a group it applies to.
type ControlContainersInput struct {
	Action  ContainerAction `json:"action" jsonschema:"start, stop, pause or unpause"`
	Names   []string        `json:"names,omitempty" jsonschema:"container names; or give group"`
	Group   HostGroup       `json:"group,omitempty" jsonschema:"csf or other: every container of the group the action applies to, protected ones left out of a stop or pause"`
	Confirm []string        `json:"confirm,omitempty" jsonschema:"the containers it changes, exactly as a dry run listed them; required for a stop or pause, or a change to more than one container"`
	DryRun  bool            `json:"dry_run,omitempty" jsonschema:"plan against the live host and change nothing"`
}

// ApplyHostProfileInput applies one profile.
type ApplyHostProfileInput struct {
	Name    string   `json:"name" jsonschema:"the profile's name"`
	Confirm []string `json:"confirm,omitempty" jsonschema:"the containers it changes, exactly as a dry run listed them"`
	DryRun  bool     `json:"dry_run,omitempty" jsonschema:"show the diff and change nothing"`
}

// UndoHostProfileInput undoes the last applied profile not yet undone.
type UndoHostProfileInput struct {
	Confirm []string `json:"confirm,omitempty" jsonschema:"the containers it changes, exactly as a dry run listed them"`
	DryRun  bool     `json:"dry_run,omitempty" jsonschema:"show the diff and change nothing"`
}

// HostChangeOutput is a change set: planned on a dry run, made otherwise.
type HostChangeOutput struct {
	DryRun  bool              `json:"dry_run"`
	Changes []ContainerChange `json:"changes"`
	Stop    []string          `json:"stop"`
	Start   []string          `json:"start"`
	// Result is the change set in words: the expected result.
	Result string `json:"result"`
	// Confirm is what the change asks to be confirmed; empty when none.
	Confirm []string `json:"confirm"`
}

// PutHostProfileInput adds, replaces or removes one profile.
type PutHostProfileInput struct {
	Profile HostProfile `json:"profile"`
	Remove  bool        `json:"remove,omitempty" jsonschema:"remove the profile of this name instead"`
}

// ProtectContainersInput marks containers protected, or clears the mark.
type ProtectContainersInput struct {
	Names     []string `json:"names"`
	Protected bool     `json:"protected" jsonschema:"true protects them; false clears the mark"`
}

// HostOperations is the host operations over the capabilities they need.
type HostOperations struct {
	containers IHostContainers
	processes  iofs.IFiles
	state      *iofs.HostFiles
	clock      clock.IClock
	hold       func(ctx context.Context, reason string) error
	// samples is the last usage reading per container, published whole so a
	// reading needs no lock: cpu is the difference between two readings.
	samples atomic.Pointer[map[string]docker.ContainerSample]
}

// HostOperationsOption configures [HostOperations].
type HostOperationsOption func(operations *HostOperations) error

// WithHostContainers grants the container capability. Required.
func WithHostContainers(containers IHostContainers) HostOperationsOption {
	return func(operations *HostOperations) error {
		operations.containers = containers
		return nil
	}
}

// WithProcessTable grants /proc, which the load and memory gauges read.
func WithProcessTable(processes iofs.IFiles) HostOperationsOption {
	return func(operations *HostOperations) error {
		operations.processes = processes
		return nil
	}
}

// WithHostState grants the harness state directory: its run directories are
// the records CSF work is recognized from, its filesystem is the disk gauge,
// and it holds the operator's settings and the ledger.
func WithHostState(state *iofs.HostFiles) HostOperationsOption {
	return func(operations *HostOperations) error {
		operations.state = state
		return nil
	}
}

// WithHostClock grants the clock ledger records are stamped by.
func WithHostClock(source clock.IClock) HostOperationsOption {
	return func(operations *HostOperations) error {
		operations.clock = source
		return nil
	}
}

// WithSessionHold grants the hold the load guard offers: hold, with a reason,
// stops new sessions being launched.
func WithSessionHold(hold func(ctx context.Context, reason string) error) HostOperationsOption {
	return func(operations *HostOperations) error {
		operations.hold = hold
		return nil
	}
}

// NewHostOperations validates the option set before building the operations.
func NewHostOperations(options ...HostOperationsOption) (*HostOperations, error) {
	operations := &HostOperations{clock: clock.NewSystemClock()}
	for _, option := range options {
		if option == nil {
			return nil, ErrNoHostContainers
		}
		if err := option(operations); err != nil {
			return nil, err
		}
	}
	if operations.containers == nil {
		return nil, ErrNoHostContainers
	}
	return operations, nil
}

// Host reads the machine now.
func (operations *HostOperations) Host(ctx context.Context, input GetHostInput) (HostReport, error) {
	snapshot, settings, err := operations.snapshot(ctx)
	if err != nil {
		return HostReport{}, err
	}
	report := HostReport{HostSnapshot: snapshot, Settings: settings, Holds: operations.hold != nil}
	if applied, found := undoable(operations.ledger()); found {
		report.Undoable = &applied
	}
	return report, nil
}

// Control starts, stops, pauses or unpauses containers.
func (operations *HostOperations) Control(ctx context.Context, input ControlContainersInput) (HostChangeOutput, error) {
	return operations.change(ctx, HostRequest{Operation: OperationControl, Action: input.Action, Names: input.Names, Group: input.Group}, input.Confirm, input.DryRun)
}

// ApplyProfile makes the host match one profile and records what it changed.
func (operations *HostOperations) ApplyProfile(ctx context.Context, input ApplyHostProfileInput) (HostChangeOutput, error) {
	return operations.change(ctx, HostRequest{Operation: OperationProfile, Profile: input.Name}, input.Confirm, input.DryRun)
}

// Undo reverses the last applied profile not yet undone, and records that.
func (operations *HostOperations) Undo(ctx context.Context, input UndoHostProfileInput) (HostChangeOutput, error) {
	return operations.change(ctx, HostRequest{Operation: OperationUndo}, input.Confirm, input.DryRun)
}

// PutProfile adds or replaces the profile of its name, or removes it.
func (operations *HostOperations) PutProfile(ctx context.Context, input PutHostProfileInput) (HostSettings, error) {
	profile := input.Profile
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.Name == "" || len(profile.Name) > profileNameLimit || strings.ContainsAny(profile.Name, "\n\r") || slices.Contains(profile.Keep, "") {
		return HostSettings{}, ErrInvalidProfile
	}
	settings := operations.settings()
	settings.Profiles = slices.DeleteFunc(settings.Profiles, func(existing HostProfile) bool { return existing.Name == profile.Name })
	if !input.Remove {
		settings.Profiles = append(settings.Profiles, profile)
	}
	return settings, operations.writeSettings(settings)
}

// Protect marks containers protected, or clears the mark.
func (operations *HostOperations) Protect(ctx context.Context, input ProtectContainersInput) (HostSettings, error) {
	if len(input.Names) == 0 || slices.Contains(input.Names, "") {
		return HostSettings{}, errors.New("host operations: name the containers to protect")
	}
	settings := operations.settings()
	settings.Protected = slices.DeleteFunc(settings.Protected, func(name string) bool { return slices.Contains(input.Names, name) })
	if input.Protected {
		settings.Protected = append(settings.Protected, input.Names...)
	}
	slices.Sort(settings.Protected)
	return settings, operations.writeSettings(settings)
}

// Check is a request's acceptance check: the exact operation dry-run against
// the live host, which is what each of the panel's buttons is verified by.
func (operations *HostOperations) Check(ctx context.Context, request HostRequest) (widget.Expectation, error) {
	snapshot, settings, err := operations.snapshot(ctx)
	if err != nil {
		return widget.Expectation{}, err
	}
	return operations.expect(snapshot, settings, operations.ledger(), request)
}

// expect dry-runs one request against one reading.
func (operations *HostOperations) expect(snapshot HostSnapshot, settings HostSettings, ledger []AppliedProfile, request HostRequest) (widget.Expectation, error) {
	switch request.Operation {
	case OperationProtect:
		for _, name := range request.Names {
			if _, exists := snapshot.container(name); !exists {
				return widget.Expectation{}, fmt.Errorf("no container is named %q", name)
			}
		}
		if operations.state == nil {
			return widget.Expectation{}, errors.New("no state directory was granted to keep the mark in")
		}
		verb := "Clears the protection of "
		if request.Protected {
			verb = "Protects "
		}
		return widget.Expectation{Result: verb + strings.Join(request.Names, ", ") + "."}, nil
	case OperationHold:
		if operations.hold == nil {
			return widget.Expectation{}, errors.New("no slice dispatcher is mounted to hold sessions")
		}
		return widget.Expectation{Result: "Holds new sessions: the dispatcher launches nothing until it is resumed."}, nil
	}
	changes, err := operations.plan(snapshot, settings, ledger, request)
	if err != nil {
		return widget.Expectation{}, err
	}
	if len(changes) == 0 && request.Operation != OperationProfile {
		return widget.Expectation{}, errors.New("nothing to change")
	}
	return widget.Expectation{Result: describe(changes), Confirm: confirmFor(request, changes)}, nil
}

// Press runs one request as the panel's button does: confirm is what the
// operator was shown and confirmed, and the operation refuses if the host
// has moved since.
func (operations *HostOperations) Press(ctx context.Context, request HostRequest, confirm []string) (string, error) {
	switch request.Operation {
	case OperationProtect:
		_, err := operations.Protect(ctx, ProtectContainersInput{Names: request.Names, Protected: request.Protected})
		return "Saved.", err
	case OperationHold:
		if operations.hold == nil {
			return "", errors.New("no slice dispatcher is mounted to hold sessions")
		}
		return "New sessions are held.", operations.hold(ctx, "held from the Workbench host panel under load")
	}
	output, err := operations.change(ctx, request, confirm, false)
	return output.Result, err
}

// change plans a request against the live host and, unless it is a dry run,
// makes it once the confirm matches, checks the host ended up as planned,
// and records a profile or undo in the ledger.
func (operations *HostOperations) change(ctx context.Context, request HostRequest, confirm []string, dryRun bool) (HostChangeOutput, error) {
	snapshot, settings, err := operations.snapshot(ctx)
	if err != nil {
		return HostChangeOutput{}, err
	}
	ledger := operations.ledger()
	changes, err := operations.plan(snapshot, settings, ledger, request)
	if err != nil {
		return HostChangeOutput{}, err
	}
	output := HostChangeOutput{DryRun: dryRun, Changes: changes, Result: describe(changes), Confirm: confirmFor(request, changes), Stop: []string{}, Start: []string{}}
	for _, change := range changes {
		if transitions[change.Action].destructive {
			output.Stop = append(output.Stop, change.Name)
		} else {
			output.Start = append(output.Start, change.Name)
		}
	}
	if output.Confirm == nil {
		output.Confirm = []string{}
	}
	if dryRun {
		return output, nil
	}
	if len(output.Confirm) > 0 && !sameNames(confirm, output.Confirm) {
		return output, fmt.Errorf("%w: %s", ErrUnconfirmed, strings.Join(output.Confirm, ", "))
	}
	if err := operations.apply(ctx, changes); err != nil {
		return output, err
	}
	// A profile that changed nothing is not recorded, so it never stands
	// between the operator and the undo of one that did.
	if request.Operation == OperationControl || (request.Operation == OperationProfile && len(changes) == 0) {
		return output, nil
	}
	record := AppliedProfile{At: operations.clock.Now().UTC(), Profile: request.Profile, Changes: changes}
	if request.Operation == OperationUndo {
		last, _ := undoable(ledger)
		record.Profile, record.Undoes = last.Profile, &last.At
	}
	return output, operations.appendLedger(record)
}

// plan is the change set one request makes on one reading.
func (operations *HostOperations) plan(snapshot HostSnapshot, settings HostSettings, ledger []AppliedProfile, request HostRequest) ([]ContainerChange, error) {
	switch request.Operation {
	case OperationControl:
		names := request.Names
		if request.Group != "" {
			if len(names) > 0 {
				return nil, errors.New("give names or a group, not both")
			}
			if names = groupNames(snapshot, request.Group, request.Action); len(names) == 0 {
				return nil, fmt.Errorf("no container in %s can %s", request.Group, request.Action)
			}
		}
		return planControl(snapshot, request.Action, names)
	case OperationProfile:
		for _, profile := range settings.Profiles {
			if profile.Name == request.Profile {
				return planProfile(snapshot, profile), nil
			}
		}
		return nil, fmt.Errorf("%w: %q", ErrNoProfile, request.Profile)
	case OperationUndo:
		last, found := undoable(ledger)
		if !found {
			return nil, ErrNothingToUndo
		}
		return planUndo(snapshot, last.Changes), nil
	}
	return nil, fmt.Errorf("host operations: %q is not an operation", request.Operation)
}

// apply makes every change at once, so stopping forty containers waits for
// the slowest rather than for all of them in turn, then reads the host back:
// each changed container must be where its action leaves it.
func (operations *HostOperations) apply(ctx context.Context, changes []ContainerChange) error {
	failures := make([]error, len(changes))
	var group sync.WaitGroup
	for index, change := range changes {
		group.Go(func() { failures[index] = operations.act(ctx, change) })
	}
	group.Wait()
	if err := errors.Join(failures...); err != nil {
		return err
	}
	after, _, err := operations.snapshot(ctx)
	if err != nil {
		return err
	}
	var wrong []string
	for _, change := range changes {
		found, exists := after.container(change.Name)
		if want := transitions[change.Action].to; !exists || container.ContainerState(found.State) != want {
			wrong = append(wrong, fmt.Sprintf("%s is %s, expected %s", change.Name, found.State, want))
		}
	}
	if len(wrong) > 0 {
		return fmt.Errorf("%w: %s", ErrAcceptance, strings.Join(wrong, "; "))
	}
	return nil
}

// act makes one change through the Engine.
func (operations *HostOperations) act(ctx context.Context, change ContainerChange) error {
	var err error
	switch change.Action {
	case ActionStart:
		_, err = operations.containers.ContainerStart(ctx, change.Name, client.ContainerStartOptions{})
	case ActionStop:
		_, err = operations.containers.ContainerStop(ctx, change.Name, client.ContainerStopOptions{})
	case ActionPause:
		_, err = operations.containers.ContainerPause(ctx, change.Name, client.ContainerPauseOptions{})
	case ActionUnpause:
		_, err = operations.containers.ContainerUnpause(ctx, change.Name, client.ContainerUnpauseOptions{})
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w", change.Action, change.Name, err)
	}
	return nil
}

// snapshot reads the machine: every container, grouped and sampled, and the
// gauges.
func (operations *HostOperations) snapshot(ctx context.Context) (HostSnapshot, HostSettings, error) {
	settings := operations.settings()
	listed, err := operations.containers.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return HostSnapshot{}, settings, fmt.Errorf("host operations: list containers: %w", err)
	}
	protected := map[string]bool{}
	for _, name := range settings.Protected {
		protected[name] = true
	}
	directory := ""
	var runs map[string]bool
	if operations.state != nil {
		directory, runs = operations.state.Directory(), runDirectories(operations.state)
	}
	previous := map[string]docker.ContainerSample{}
	if last := operations.samples.Load(); last != nil {
		previous = *last
	}
	samples := map[string]docker.ContainerSample{}
	snapshot := HostSnapshot{Containers: make([]HostContainer, 0, len(listed.Items))}
	for _, summary := range listed.Items {
		found := classify(summary, directory, runs, protected)
		if summary.State == container.StateRunning {
			if sample, err := operations.containers.SampleContainer(ctx, summary.ID); err == nil {
				samples[summary.ID] = sample
				found.MemoryBytes = sample.MemoryBytes
				if before, seen := previous[summary.ID]; seen {
					found.CPUPercent = cpuPercent(before, sample)
				}
			}
		}
		snapshot.Containers = append(snapshot.Containers, found)
	}
	operations.samples.Store(&samples)
	orderContainers(snapshot.Containers)
	var disk IHostDisk
	if operations.state != nil {
		disk = operations.state
	}
	snapshot.Gauges = readGauges(operations.processes, disk)
	if operations.state != nil {
		if content, err := operations.state.ReadFile(harness.ResumeQueueFile); err == nil {
			var queue harness.ResumeQueue
			if json.Unmarshal(content, &queue) == nil {
				snapshot.Resumes = &queue
			}
		}
	}
	return snapshot, settings, nil
}

// settings reads the operator's record; a host with none has the defaults.
func (operations *HostOperations) settings() HostSettings {
	if operations.state == nil {
		return defaultHostSettings()
	}
	content, err := operations.state.ReadFile(HostSettingsFile)
	if err != nil {
		return defaultHostSettings()
	}
	settings := HostSettings{}
	if json.Unmarshal(content, &settings) != nil {
		return defaultHostSettings()
	}
	if settings.Protected == nil {
		settings.Protected = []string{}
	}
	return settings
}

// writeSettings replaces the operator's record whole.
func (operations *HostOperations) writeSettings(settings HostSettings) error {
	if operations.state == nil {
		return errors.New("host operations: no state directory was granted to keep settings in")
	}
	content, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(operations.state.Directory(), HostSettingsFile)
	if err := atomicfile.WriteFile(target, append(content, '\n'), hostFileMode); err != nil {
		return fmt.Errorf("host operations: write settings: %w", err)
	}
	return nil
}

// ledger reads every applied profile and undo, oldest first.
func (operations *HostOperations) ledger() []AppliedProfile {
	if operations.state == nil {
		return nil
	}
	content, err := operations.state.ReadFile(HostLedgerFile)
	if err != nil && !errors.Is(err, stdfs.ErrNotExist) {
		return nil
	}
	var records []AppliedProfile
	for _, line := range bytes.Split(content, []byte("\n")) {
		var record AppliedProfile
		if json.Unmarshal(line, &record) == nil {
			records = append(records, record)
		}
	}
	return records
}

// appendLedger appends one record.
func (operations *HostOperations) appendLedger(record AppliedProfile) error {
	if operations.state == nil {
		return errors.New("host operations: no state directory was granted to keep the ledger in")
	}
	content, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(operations.state.Directory(), HostLedgerFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, hostFileMode)
	if err != nil {
		return fmt.Errorf("host operations: open the ledger: %w", err)
	}
	_, err = file.Write(append(content, '\n'))
	return errors.Join(err, file.Close())
}

// undoable is the last applied profile no later record undid.
func undoable(ledger []AppliedProfile) (AppliedProfile, bool) {
	undone := map[time.Time]bool{}
	for _, record := range ledger {
		if record.Undoes != nil {
			undone[*record.Undoes] = true
		}
	}
	for index := len(ledger) - 1; index >= 0; index-- {
		if record := ledger[index]; record.Undoes == nil && !undone[record.At] {
			return record, true
		}
	}
	return AppliedProfile{}, false
}

// confirmFor is what a request asks the operator to confirm: a profile or an
// undo always shows its whole diff first, and a control asks when it takes
// something away or changes more than one container.
func confirmFor(request HostRequest, changes []ContainerChange) []string {
	if request.Operation == OperationControl {
		return needsConfirm(changes)
	}
	if len(changes) == 0 {
		return nil
	}
	return changeNames(changes)
}

// sameNames reports whether two name lists hold the same names.
func sameNames(given []string, want []string) bool {
	sorted := slices.Clone(given)
	slices.Sort(sorted)
	return slices.Equal(slices.Compact(sorted), want)
}

// Register serves the host operations over HTTP, each a POST of its input
// as JSON answered with its output.
func (operations *HostOperations) Register(router gin.IRouter) {
	router.POST(hostOperationsPath+GetHostOperation, handle(operations.Host))
	router.POST(hostOperationsPath+ControlContainersOperation, handle(operations.Control))
	router.POST(hostOperationsPath+ApplyHostProfileOperation, handle(operations.ApplyProfile))
	router.POST(hostOperationsPath+UndoHostProfileOperation, handle(operations.Undo))
	router.POST(hostOperationsPath+PutHostProfileOperation, handle(operations.PutProfile))
	router.POST(hostOperationsPath+ProtectContainersOperation, handle(operations.Protect))
}

// Panel reads the host and verifies every button the panel draws against
// that same reading: each button's request is dry-run and its expected
// result kept, or its refusal.
func (operations *HostOperations) Panel(ctx context.Context) HostPanel {
	snapshot, settings, err := operations.snapshot(ctx)
	if err != nil {
		return HostPanel{Error: err.Error()}
	}
	ledger := operations.ledger()
	report := HostReport{HostSnapshot: snapshot, Settings: settings, Holds: operations.hold != nil}
	if applied, found := undoable(ledger); found {
		report.Undoable = &applied
	}
	actions := widget.VerifyActions(ctx, hostActions(report), func(_ context.Context, request HostRequest) (widget.Expectation, error) {
		return operations.expect(snapshot, settings, ledger, request)
	})
	return HostPanel{Report: report, Actions: actions}
}
