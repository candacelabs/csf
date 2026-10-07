// Copyright 2026 Candace Labs

// Package housekeeping is the housekeeping service: the default cron
// triggers that keep a harness host's disk and state healthy. Each trigger is
// one operation of a [Housekeeper], mounted with the cron service, and every
// operation obeys one ownership rule: it deletes only what the harness
// records as created by a session that has ended, plus the Docker classes
// the docker trigger names (exited runner containers, dangling images, the
// build cache) and the database dumps the backup trigger itself wrote beyond
// their derived retention. Paths are never matched by pattern; anything the
// harness cannot attribute to an ended session is reported as a finding with
// its size and survives. Volumes and running containers are never touched.
// Two triggers keep state rather than reclaim disk: database_backup dumps
// the database the host owns, and live_checkout fast-forwards the Workbench's
// widgets checkout, never resetting anything.
//
// The service crosses no boundary itself: it reads through the file
// capability (the state directory and the process table), runs programs
// through the process capability and reaches the Docker Engine through the
// container capability. Every deletion is logged with its path, size and
// owning session before it happens and recorded afterwards as a typed
// [Record] through the sink the binary grants.
package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	// Triggers declares its schedules in ScheduleLocation, an IANA zone that
	// Go loads by name from the host's time zone database. A host without
	// one, such as the pinned Bazel image or a scratch container, answers
	// "unknown time zone" and Triggers fails. time/tzdata embeds the
	// database as a fallback that is consulted only when the host has no
	// copy, at a cost of about 450 KB in every binary that mounts the
	// service.
	_ "time/tzdata"

	"github.com/moby/moby/client"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/io/ipc/docker"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/ipc/proc"
	grammar "github.com/candacelabs/csf/pkg/cron"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	cronservice "github.com/candacelabs/csf/services/cron"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mocks/mock_housekeeping.go -package=mocks github.com/candacelabs/csf/services/housekeeping IContainers,IAdmission,IProcessTable

// The default triggers, as csfcron declares them.
const (
	// TriggerDiskFloor measures free disk every five minutes and, below the
	// derived floor, reclaims until it is above it.
	TriggerDiskFloor = "housekeeping.disk_floor"
	// TriggerSessions reclaims what ended sessions left, every 15 minutes.
	TriggerSessions = "housekeeping.sessions"
	// TriggerDocker removes the Docker leftovers it names, hourly.
	TriggerDocker = "housekeeping.docker"
	// TriggerSharedCache trims the shared Bazel disk cache daily at 04:30.
	TriggerSharedCache = "housekeeping.shared_cache"
	// TriggerDatabaseBackup dumps the database the host owns, hourly.
	TriggerDatabaseBackup = "housekeeping.database_backup"
	// TriggerLiveCheckout fast-forwards the live checkout every minute.
	TriggerLiveCheckout = "housekeeping.live_checkout"
	// ScheduleLocation is the time zone the triggers are declared in.
	ScheduleLocation = "America/Los_Angeles"

	diskFloorInterval  = 5 * time.Minute
	sessionsInterval   = 15 * time.Minute
	dockerInterval     = time.Hour
	backupInterval     = time.Hour
	checkoutInterval   = time.Minute
	sharedCacheHour    = 4
	sharedCacheMinute  = 30
	manualOccurrence   = "manual"
	defaultSandboxName = "busybox:1.37.0"
)

var (
	// ErrInvalidOption reports a nil option or a value the service cannot use.
	ErrInvalidOption = errors.New("housekeeping: invalid option")
	// ErrMissingCapability reports a housekeeper built without a required
	// capability.
	ErrMissingCapability = errors.New("housekeeping: a required capability is missing")
	// ErrUnknownTrigger reports a Run of a trigger this service does not
	// declare.
	ErrUnknownTrigger = errors.New("housekeeping: unknown trigger")
	// ErrBelowFloor reports a disk_floor occurrence that ended with free disk
	// still below the floor.
	ErrBelowFloor = errors.New("housekeeping: free disk is below the floor")
)

// SessionList reports the harness's sessions and the process they run in:
// the session service's List in the host, its client elsewhere.
type SessionList func(ctx context.Context) (*harnessv1.ListAgentSessionsResponse, error)

// RecordSink receives every record an operation writes.
type RecordSink func(record Record) error

// IContainers is the part of the container capability housekeeping uses.
// It has no volume operation, so no trigger can remove a volume.
type IContainers interface {
	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerRemove(ctx context.Context, id string, options client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
	ImageList(ctx context.Context, options client.ImageListOptions) (client.ImageListResult, error)
	ImagePrune(ctx context.Context, options client.ImagePruneOptions) (client.ImagePruneResult, error)
	BuildCachePrune(ctx context.Context, options client.BuildCachePruneOptions) (client.BuildCachePruneResult, error)
	DiskUsage(ctx context.Context, options client.DiskUsageOptions) (client.DiskUsageResult, error)
	RunSandboxed(ctx context.Context, spec docker.SandboxSpec) (docker.SandboxResult, error)
	Exec(ctx context.Context, name string, spec docker.ExecSpec) (docker.ExecResult, error)
}

// IAdmission is the harness's session admission: held while free disk is
// below the floor, so no new session starts on a full disk.
type IAdmission interface {
	HoldAdmission(reason string)
	ReleaseAdmission()
}

// IProcessTable is the host's process table, granted as /proc.
type IProcessTable interface {
	iofs.IFiles
	ReadLink(name string) (string, error)
}

var _ IContainers = (*docker.ContainerHost)(nil)
var _ IProcessTable = (*iofs.HostFiles)(nil)

// Housekeeper runs the housekeeping operations. It holds no state between
// occurrences: each one measures the host again.
type Housekeeper struct {
	stateDirectory   string
	state            iofs.IFiles
	scratchDirectory string
	scratch          iofs.IFiles
	sessions         SessionList
	launcher         proc.ILauncher
	containers       IContainers
	processes        IProcessTable
	sink             RecordSink
	admission        IAdmission
	clock            clock.IClock
	logger           *slog.Logger
	dryRun           bool
	sandboxImage     string
	liveCheckout     string
	operations       map[string]cronservice.Operation
}

// HousekeeperOption configures a [Housekeeper].
type HousekeeperOption func(housekeeper *Housekeeper) error

// WithState grants the harness state directory: its absolute path, for the
// programs that measure and remove, and read access to it.
func WithState(directory string, files iofs.IFiles) HousekeeperOption {
	return func(housekeeper *Housekeeper) error {
		if directory == "" || files == nil {
			return fmt.Errorf("%w: the state directory needs a path and its files", ErrInvalidOption)
		}
		housekeeper.stateDirectory, housekeeper.state = directory, files
		return nil
	}
}

// WithScratch grants a scratch directory whose entries the sessions trigger
// reports, with their sizes, as findings. Nothing in it is ever deleted.
func WithScratch(directory string, files iofs.IFiles) HousekeeperOption {
	return func(housekeeper *Housekeeper) error {
		if directory == "" || files == nil {
			return fmt.Errorf("%w: the scratch directory needs a path and its files", ErrInvalidOption)
		}
		housekeeper.scratchDirectory, housekeeper.scratch = directory, files
		return nil
	}
}

// WithSessions grants the harness's session list. Required.
func WithSessions(list SessionList) HousekeeperOption {
	return func(housekeeper *Housekeeper) error {
		if list == nil {
			return fmt.Errorf("%w: nil session list", ErrInvalidOption)
		}
		housekeeper.sessions = list
		return nil
	}
}

// WithLauncher grants the process capability. Required.
func WithLauncher(launcher proc.ILauncher) HousekeeperOption {
	return func(housekeeper *Housekeeper) error {
		if launcher == nil {
			return fmt.Errorf("%w: nil launcher", ErrInvalidOption)
		}
		housekeeper.launcher = launcher
		return nil
	}
}

// WithContainers grants the container capability. Required.
func WithContainers(containers IContainers) HousekeeperOption {
	return func(housekeeper *Housekeeper) error {
		if containers == nil {
			return fmt.Errorf("%w: nil containers", ErrInvalidOption)
		}
		housekeeper.containers = containers
		return nil
	}
}

// WithProcessTable grants the process table. Required.
func WithProcessTable(processes IProcessTable) HousekeeperOption {
	return func(housekeeper *Housekeeper) error {
		if processes == nil {
			return fmt.Errorf("%w: nil process table", ErrInvalidOption)
		}
		housekeeper.processes = processes
		return nil
	}
}

// WithRecordSink grants where records go. Required.
func WithRecordSink(sink RecordSink) HousekeeperOption {
	return func(housekeeper *Housekeeper) error {
		if sink == nil {
			return fmt.Errorf("%w: nil record sink", ErrInvalidOption)
		}
		housekeeper.sink = sink
		return nil
	}
}

// WithAdmission grants the harness's admission, which disk_floor holds
// while free disk is below the floor.
func WithAdmission(admission IAdmission) HousekeeperOption {
	return func(housekeeper *Housekeeper) error {
		if admission == nil {
			return fmt.Errorf("%w: nil admission", ErrInvalidOption)
		}
		housekeeper.admission = admission
		return nil
	}
}

// WithClock replaces the host's clock, which stamps every record.
func WithClock(source clock.IClock) HousekeeperOption {
	return func(housekeeper *Housekeeper) error {
		if source == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidOption)
		}
		housekeeper.clock = source
		return nil
	}
}

// WithLogger receives the line logged before every deletion.
func WithLogger(logger *slog.Logger) HousekeeperOption {
	return func(housekeeper *Housekeeper) error {
		if logger == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidOption)
		}
		housekeeper.logger = logger
		return nil
	}
}

// WithDryRun measures and records the plan without deleting anything or
// holding admission.
func WithDryRun() HousekeeperOption {
	return func(housekeeper *Housekeeper) error {
		housekeeper.dryRun = true
		return nil
	}
}

// WithSandboxImage names the image the root-owned remainder of an output
// base is removed in, through a container that mounts only that path.
func WithSandboxImage(image string) HousekeeperOption {
	return func(housekeeper *Housekeeper) error {
		if image == "" {
			return fmt.Errorf("%w: empty sandbox image", ErrInvalidOption)
		}
		housekeeper.sandboxImage = image
		return nil
	}
}

// WithLiveCheckout names a directory of the checkout live_checkout keeps
// fast-forwarded to origin/main; without it that trigger is not declared.
func WithLiveCheckout(directory string) HousekeeperOption {
	return func(housekeeper *Housekeeper) error {
		if directory == "" {
			return fmt.Errorf("%w: empty live checkout", ErrInvalidOption)
		}
		housekeeper.liveCheckout = directory
		return nil
	}
}

// NewHousekeeper validates the whole option set and returns the service.
// The state directory, session list, launcher, containers, process table
// and record sink are required.
func NewHousekeeper(options ...HousekeeperOption) (*Housekeeper, error) {
	housekeeper := &Housekeeper{
		clock:        clock.NewSystemClock(),
		logger:       slog.New(slog.DiscardHandler),
		sandboxImage: defaultSandboxName,
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(housekeeper); err != nil {
			return nil, err
		}
	}
	if housekeeper.state == nil || housekeeper.sessions == nil || housekeeper.launcher == nil ||
		housekeeper.containers == nil || housekeeper.processes == nil || housekeeper.sink == nil {
		return nil, ErrMissingCapability
	}
	housekeeper.operations = map[string]cronservice.Operation{
		TriggerDiskFloor:      housekeeper.DiskFloor,
		TriggerSessions:       housekeeper.Sessions,
		TriggerDocker:         housekeeper.Docker,
		TriggerSharedCache:    housekeeper.SharedCache,
		TriggerDatabaseBackup: housekeeper.DatabaseBackup,
	}
	if housekeeper.liveCheckout != "" {
		housekeeper.operations[TriggerLiveCheckout] = housekeeper.LiveCheckout
	}
	return housekeeper, nil
}

// Triggers declares the default triggers for the cron service, in
// [ScheduleLocation]: live_checkout only with [WithLiveCheckout].
func (housekeeper *Housekeeper) Triggers() ([]cronservice.Option, error) {
	location, err := time.LoadLocation(ScheduleLocation)
	if err != nil {
		return nil, fmt.Errorf("housekeeping: load %s: %w", ScheduleLocation, err)
	}
	triggers := []cronservice.Option{
		cronservice.WithTrigger(TriggerDiskFloor, grammar.Spec(grammar.Every(diskFloorInterval)).In(location), housekeeper.DiskFloor),
		cronservice.WithTrigger(TriggerSessions, grammar.Spec(grammar.Every(sessionsInterval)).In(location), housekeeper.Sessions),
		cronservice.WithTrigger(TriggerDocker, grammar.Spec(grammar.Every(dockerInterval)).In(location), housekeeper.Docker),
		cronservice.WithTrigger(TriggerSharedCache,
			grammar.Spec(grammar.Daily(grammar.At(sharedCacheHour, sharedCacheMinute).AM())).In(location), housekeeper.SharedCache),
		cronservice.WithTrigger(TriggerDatabaseBackup, grammar.Spec(grammar.Every(backupInterval)).In(location), housekeeper.DatabaseBackup),
	}
	if housekeeper.liveCheckout != "" {
		triggers = append(triggers,
			cronservice.WithTrigger(TriggerLiveCheckout, grammar.Spec(grammar.Every(checkoutInterval)).In(location), housekeeper.LiveCheckout))
	}
	return triggers, nil
}

// Run invokes one trigger's operation once, outside the scheduler, as a
// manual occurrence.
func (housekeeper *Housekeeper) Run(ctx context.Context, trigger string) error {
	operation, exists := housekeeper.operations[trigger]
	if !exists {
		return fmt.Errorf("%w: %q", ErrUnknownTrigger, trigger)
	}
	now := housekeeper.clock.Now().UTC()
	return operation(ctx, cronservice.Occurrence{
		ID:          fmt.Sprintf("%s/%s/%s", manualOccurrence, trigger, now.Format(time.RFC3339)),
		TriggerName: trigger,
		ScheduledAt: now,
		StartedAt:   now,
		Attempt:     1,
	})
}
