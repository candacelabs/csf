// Copyright 2026 Candace Labs

package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"github.com/candacelabs/csf/io/kernel/clock"
	ionet "github.com/candacelabs/csf/io/net"
	"github.com/candacelabs/csf/pkg/atomicfile"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mocks/mock_services.go -package=mocks github.com/candacelabs/csf/io/ipc/docker IServices

const (
	// stateDigestLength names one container per state directory, so a second
	// host on the same machine owns its own.
	stateDigestLength = 12
	volumeSuffix      = "-data"
	healthHealthy     = "healthy"
	// ownedHealthPoll and OwnedHealthLimit bound the wait for the health
	// check. Measured 2026-10-05: a first PostgreSQL start, which initializes
	// its cluster, passed 2.8 s after creation; the limit leaves room for a
	// loaded host.
	ownedHealthPoll = 500 * time.Millisecond
	// OwnedHealthLimit is how long Ensure waits for the health check.
	OwnedHealthLimit   = 2 * time.Minute
	ownedRecordMode    = 0o600
	ownedDirectoryMode = 0o700
	networkTCP         = "tcp"
	anyPort            = "0"
)

var (
	// ErrServiceRecordLost reports a container with no record: what the record
	// held (a password) is gone, so the owner refuses to guess rather than
	// create a second one.
	ErrServiceRecordLost = errors.New("ipc/docker: the container exists but its record is missing")
	// ErrServiceNotHealthy reports a container whose health check did not pass
	// within [OwnedHealthLimit].
	ErrServiceNotHealthy = errors.New("ipc/docker: the health check did not pass")
	// ErrServiceNotOwned reports a state directory that owns no such
	// container.
	ErrServiceNotOwned = errors.New("ipc/docker: this state directory owns no such container")
)

// IServices is the part of the container capability an owned service uses.
// [ContainerHost] satisfies it.
type IServices interface {
	EnsureService(ctx context.Context, spec ServiceSpec) (ServiceState, error)
	InspectService(ctx context.Context, name string) (ServiceState, error)
	StopService(ctx context.Context, name string) error
}

var _ IServices = (*ContainerHost)(nil)

// OwnedLocation is where an owned container is, recorded on its first start
// and reused on every later one.
type OwnedLocation struct {
	Container string `json:"container"`
	Volume    string `json:"volume"`
	Image     string `json:"image"`
	Host      string `json:"host"`
	Port      uint16 `json:"port"`
}

// OwnedRecord is the record file: the location and the owner's own settings,
// such as a password, which only the record file holds.
type OwnedRecord[Settings any] struct {
	OwnedLocation
	Settings Settings `json:"settings"`
}

// OwnedStatus is what a lifecycle verb prints: the location and the container
// as the Engine sees it, never the owner's settings.
type OwnedStatus struct {
	Location OwnedLocation `json:"location"`
	State    ServiceState  `json:"state"`
}

// OwnedServiceDefinition is what an owner decides; [OwnedService] does the
// rest.
type OwnedServiceDefinition[Settings any] struct {
	// StateDirectory holds RecordFile, mode 0600; one container per state
	// directory.
	StateDirectory string
	RecordFile     string
	// NamePrefix and a digest of StateDirectory name the container; its
	// volume adds "-data" and is mounted at DataDirectory.
	NamePrefix    string
	DataDirectory string
	// Image is pinned by digest.
	Image string
	// Host is the address the port is published on: loopback, or a tailnet
	// address.
	Host netip.Addr
	// NewSettings fills the owner's settings on the first start only; nil
	// leaves them zero.
	NewSettings func() (Settings, error)
	// Spec is the container a record describes: environment, mounts, health
	// check and anything else of the owner's. Name, Image, HostAddress,
	// HostPort and the data volume are set from the record.
	Spec func(record OwnedRecord[Settings]) ServiceSpec
	// Validate checks the owner's settings each time the record is loaded,
	// naming a required field that is missing; nil checks none.
	Validate func(settings Settings) *RecordError
}

// OwnedServiceOption grants an [OwnedService] its capabilities.
type OwnedServiceOption func(capabilities *ownedCapabilities) error

type ownedCapabilities struct {
	services IServices
	listener ionet.IListener
	clock    clock.IClock
}

// WithServices grants the container capability. Required.
func WithServices(services IServices) OwnedServiceOption {
	return func(capabilities *ownedCapabilities) error {
		if services == nil {
			return fmt.Errorf("%w: nil services", ErrInvalidOption)
		}
		capabilities.services = services
		return nil
	}
}

// WithListener grants the network capability the first start probes a free
// port with. Required.
func WithListener(listener ionet.IListener) OwnedServiceOption {
	return func(capabilities *ownedCapabilities) error {
		if listener == nil {
			return fmt.Errorf("%w: nil listener", ErrInvalidOption)
		}
		capabilities.listener = listener
		return nil
	}
}

// WithClock replaces the host's clock, which paces the health wait.
func WithClock(source clock.IClock) OwnedServiceOption {
	return func(capabilities *ownedCapabilities) error {
		if source == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidOption)
		}
		capabilities.clock = source
		return nil
	}
}

// OwnedService is one long-lived container a state directory owns. Ensure
// creates it on the first start (a free port probed once and then bound for
// good, the owner's settings filled once, all recorded mode 0600) and
// reuses it on every later one; stopping the process that owns it leaves it
// running, because it holds state.
type OwnedService[Settings any] struct {
	definition OwnedServiceDefinition[Settings]
	ownedCapabilities
}

// NewOwnedService validates the definition and the capabilities.
func NewOwnedService[Settings any](definition OwnedServiceDefinition[Settings], options ...OwnedServiceOption) (*OwnedService[Settings], error) {
	owned := &OwnedService[Settings]{definition: definition, ownedCapabilities: ownedCapabilities{clock: clock.NewSystemClock()}}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(&owned.ownedCapabilities); err != nil {
			return nil, err
		}
	}
	switch {
	case !filepath.IsAbs(definition.StateDirectory):
		return nil, fmt.Errorf("%w: the state directory must be absolute, got %q", ErrInvalidOption, definition.StateDirectory)
	case definition.RecordFile == "" || definition.NamePrefix == "" || definition.Image == "" || definition.DataDirectory == "":
		return nil, fmt.Errorf("%w: the definition needs a record file, name prefix, image and data directory", ErrInvalidOption)
	case !definition.Host.IsValid() || definition.Spec == nil:
		return nil, fmt.Errorf("%w: the definition needs a host address and a spec", ErrInvalidOption)
	case owned.services == nil || owned.listener == nil:
		return nil, fmt.Errorf("%w: services and a listener are required", ErrInvalidOption)
	}
	return owned, nil
}

// Ensure brings the container up and returns its record, once its health
// check passes. The host paths it mounts are created first, so the Engine
// never creates them root-owned.
func (owned *OwnedService[Settings]) Ensure(ctx context.Context) (OwnedRecord[Settings], error) {
	record, previous, err := owned.load()
	if errors.Is(err, ErrServiceNotOwned) {
		record, err = owned.create(ctx)
	}
	if err != nil {
		return OwnedRecord[Settings]{}, err
	}
	if previous {
		// The previous shape loads; the current one is written back, so the
		// next binary reads one shape.
		if err := owned.write(record); err != nil {
			return OwnedRecord[Settings]{}, err
		}
	}
	spec := owned.spec(record)
	for _, bind := range spec.Mounts {
		if !filepath.IsAbs(bind.Source) {
			return OwnedRecord[Settings]{}, &RecordError{Path: owned.recordPath(), Field: bind.Target,
				Problem: fmt.Sprintf("names the host path %q, which is not absolute", bind.Source),
				Fix:     "set the field the mount at " + bind.Target + " comes from to an absolute path"}
		}
		if err := os.MkdirAll(bind.Source, ownedDirectoryMode); err != nil {
			return OwnedRecord[Settings]{}, err
		}
	}
	state, err := owned.services.EnsureService(ctx, spec)
	if err != nil {
		return OwnedRecord[Settings]{}, err
	}
	if err := owned.awaitHealthy(ctx, record.Container, state); err != nil {
		return OwnedRecord[Settings]{}, err
	}
	return record, nil
}

// Stop stops the container and keeps it and its volume.
func (owned *OwnedService[Settings]) Stop(ctx context.Context) error {
	record, err := owned.Record()
	if err != nil {
		return err
	}
	return owned.services.StopService(ctx, record.Container)
}

// Status reports the location and the container, without the settings.
func (owned *OwnedService[Settings]) Status(ctx context.Context) (OwnedStatus, error) {
	record, err := owned.Record()
	if err != nil {
		return OwnedStatus{}, err
	}
	state, err := owned.services.InspectService(ctx, record.Container)
	if err != nil && !errors.Is(err, ErrNoContainer) {
		return OwnedStatus{}, err
	}
	return OwnedStatus{Location: record.OwnedLocation, State: state}, nil
}

// Record reads the record, in the current or the previous shape, or
// reports [ErrServiceNotOwned] or a [RecordError] naming the file, the field
// and the fix.
func (owned *OwnedService[Settings]) Record() (OwnedRecord[Settings], error) {
	record, _, err := owned.load()
	return record, err
}

func (owned *OwnedService[Settings]) load() (OwnedRecord[Settings], bool, error) {
	content, err := os.ReadFile(owned.recordPath())
	if errors.Is(err, os.ErrNotExist) {
		return OwnedRecord[Settings]{}, false, fmt.Errorf("%w: %s", ErrServiceNotOwned, owned.recordPath())
	}
	if err != nil {
		return OwnedRecord[Settings]{}, false, err
	}
	return DecodeOwnedRecord(owned.recordPath(), content, owned.definition.Validate)
}

// create records a container this state directory has not yet created. A
// container that already has the name but no record is refused.
func (owned *OwnedService[Settings]) create(ctx context.Context) (OwnedRecord[Settings], error) {
	digest := sha256.Sum256([]byte(owned.definition.StateDirectory))
	name := owned.definition.NamePrefix + hex.EncodeToString(digest[:])[:stateDigestLength]
	_, err := owned.services.InspectService(ctx, name)
	if err == nil {
		return OwnedRecord[Settings]{}, fmt.Errorf("%w: %s", ErrServiceRecordLost, name)
	}
	if !errors.Is(err, ErrNoContainer) {
		return OwnedRecord[Settings]{}, err
	}
	port, err := owned.freePort(ctx)
	if err != nil {
		return OwnedRecord[Settings]{}, err
	}
	record := OwnedRecord[Settings]{OwnedLocation: OwnedLocation{
		Container: name, Volume: name + volumeSuffix, Image: owned.definition.Image,
		Host: owned.definition.Host.String(), Port: port,
	}}
	if owned.definition.NewSettings != nil {
		if record.Settings, err = owned.definition.NewSettings(); err != nil {
			return OwnedRecord[Settings]{}, err
		}
	}
	return record, owned.write(record)
}

// freePort is a port nothing listens on now on any address, so the
// container can be bound to it on loopback and a tailnet address alike.
func (owned *OwnedService[Settings]) freePort(ctx context.Context) (uint16, error) {
	probe, err := owned.listener.Listen(ctx, networkTCP, net.JoinHostPort(netip.IPv4Unspecified().String(), anyPort))
	if err != nil {
		return 0, fmt.Errorf("ipc/docker: probe a free port: %w", err)
	}
	address, err := netip.ParseAddrPort(probe.Addr().String())
	return address.Port(), errors.Join(err, probe.Close())
}

// awaitHealthy waits, bounded by OwnedHealthLimit on the clock, until the
// container runs and passes its health check.
func (owned *OwnedService[Settings]) awaitHealthy(ctx context.Context, container string, state ServiceState) error {
	deadline := owned.clock.Now().Add(OwnedHealthLimit)
	for !state.Running || state.Health != healthHealthy {
		if !owned.clock.Now().Before(deadline) {
			return fmt.Errorf("%w: %s is %s, health %q, after %s", ErrServiceNotHealthy, container, state.Status, state.Health, OwnedHealthLimit)
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-owned.clock.After(ownedHealthPoll):
		}
		var err error
		if state, err = owned.services.InspectService(ctx, container); err != nil {
			return err
		}
	}
	return nil
}

// spec is the owner's spec with what the record decides filled in.
func (owned *OwnedService[Settings]) spec(record OwnedRecord[Settings]) ServiceSpec {
	spec := owned.definition.Spec(record)
	spec.Name, spec.Image = record.Container, record.Image
	spec.HostAddress, spec.HostPort = owned.definition.Host, record.Port
	if spec.Volumes == nil {
		spec.Volumes = map[string]string{}
	}
	spec.Volumes[record.Volume] = owned.definition.DataDirectory
	return spec
}

func (owned *OwnedService[Settings]) recordPath() string {
	return filepath.Join(owned.definition.StateDirectory, owned.definition.RecordFile)
}

// write replaces the record atomically, mode 0600, so a reader never sees a
// half-written one and nobody else reads its settings.
func (owned *OwnedService[Settings]) write(record OwnedRecord[Settings]) error {
	content, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(owned.definition.StateDirectory, ownedDirectoryMode); err != nil {
		return err
	}
	path := owned.recordPath()
	return atomicfile.WriteFile(path, content, ownedRecordMode)
}
