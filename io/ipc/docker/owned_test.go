// Copyright 2026 Candace Labs

package docker_test

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/io/ipc/docker/mocks"
)

const probedPort = 15999

var ownedStart = time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)

// token is an owner's own settings: a secret generated once.
type token struct {
	Secret string `json:"secret"`
}

// probe is the listener the first Ensure opens to find a free port.
type probe struct{ net.Listener }

func (probe) Addr() net.Addr {
	return net.TCPAddrFromAddrPort(netip.AddrPortFrom(netip.IPv4Unspecified(), probedPort))
}
func (probe) Close() error { return nil }

var healthy = docker.ServiceState{ID: containerID, Status: "running", Running: true, Health: "healthy", HostPort: probedPort}

// owner is the host the specs grant: a temporary state directory, the
// services and listener doubles, a manual clock and the owner's definition.
type owner struct {
	state    string
	services *mocks.MockIServices
	listener *MockIListener
	clock    *clock.ManualClock
	secrets  int
}

func newOwner() *owner {
	controller := gomock.NewController(GinkgoT())
	return &owner{
		state:    GinkgoT().TempDir(),
		services: mocks.NewMockIServices(controller),
		listener: NewMockIListener(controller),
		clock:    clock.NewManualClock(ownedStart),
	}
}

func (host *owner) definition() docker.OwnedServiceDefinition[token] {
	return docker.OwnedServiceDefinition[token]{
		StateDirectory: host.state,
		RecordFile:     "metrics.json",
		NamePrefix:     "csf-metrics-",
		DataDirectory:  "/data",
		Image:          serviceImage,
		Host:           netip.MustParseAddr("127.0.0.1"),
		NewSettings: func() (token, error) {
			host.secrets++
			return token{Secret: "generated"}, nil
		},
		Spec: func(record docker.OwnedRecord[token]) docker.ServiceSpec {
			return docker.ServiceSpec{
				Environment: []string{"SECRET=" + record.Settings.Secret},
				Mounts:      []docker.Mount{{Source: filepath.Join(host.state, "exports"), Target: "/exports"}},
				Port:        "9090/tcp",
				HealthCheck: []string{"probe"},
			}
		},
	}
}

func (host *owner) owned() *docker.OwnedService[token] {
	GinkgoHelper()
	owned, err := docker.NewOwnedService(host.definition(),
		docker.WithServices(host.services), docker.WithListener(host.listener), docker.WithClock(host.clock))
	Expect(err).NotTo(HaveOccurred())
	return owned
}

// expectFirstCreation is the Engine of a host that has never run the
// container: none by the name, one free port.
func (host *owner) expectFirstCreation() {
	host.services.EXPECT().InspectService(gomock.Any(), gomock.Any()).Return(docker.ServiceState{}, docker.ErrNoContainer)
	host.listener.EXPECT().Listen(gomock.Any(), "tcp", "0.0.0.0:0").Return(probe{}, nil)
}

var _ = Describe("OwnedService.Ensure", func() {
	var host *owner

	BeforeEach(func() { host = newOwner() })

	It("creates the container on a probed port, records it with the owner's settings mode 0600, creates its mounts, and returns once healthy", func(ctx SpecContext) {
		host.expectFirstCreation()
		var spec docker.ServiceSpec
		host.services.EXPECT().EnsureService(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, given docker.ServiceSpec) (docker.ServiceState, error) {
				spec = given
				return healthy, nil
			})

		record, err := host.owned().Ensure(ctx)
		Expect(err).NotTo(HaveOccurred())

		Expect(record.Container).To(MatchRegexp(`^csf-metrics-[0-9a-f]{12}$`))
		Expect(record.OwnedLocation).To(Equal(docker.OwnedLocation{
			Container: record.Container, Volume: record.Container + "-data", Image: serviceImage, Host: "127.0.0.1", Port: probedPort}))
		Expect(record.Settings).To(Equal(token{Secret: "generated"}))

		content, err := os.ReadFile(filepath.Join(host.state, "metrics.json"))
		Expect(err).NotTo(HaveOccurred())
		var recorded docker.OwnedRecord[token]
		Expect(json.Unmarshal(content, &recorded)).To(Succeed())
		Expect(recorded).To(Equal(record))
		Expect(string(content)).To(ContainSubstring(`"container": "` + record.Container + `"`))
		info, err := os.Stat(filepath.Join(host.state, "metrics.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		Expect(filepath.Join(host.state, "exports")).To(BeADirectory())

		Expect(spec.Name).To(Equal(record.Container))
		Expect(spec.Image).To(Equal(serviceImage))
		Expect(spec.HostAddress).To(Equal(netip.MustParseAddr("127.0.0.1")))
		Expect(spec.HostPort).To(Equal(uint16(probedPort)))
		Expect(spec.Volumes).To(Equal(map[string]string{record.Volume: "/data"}))
		Expect(spec.Environment).To(Equal([]string{"SECRET=generated"}))
	})

	It("reuses the record on every later start: same container, port and settings, no new probe", func(ctx SpecContext) {
		host.expectFirstCreation()
		host.services.EXPECT().EnsureService(gomock.Any(), gomock.Any()).Return(healthy, nil).Times(2)
		first, err := host.owned().Ensure(ctx)
		Expect(err).NotTo(HaveOccurred())

		second, err := host.owned().Ensure(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(Equal(first))
		Expect(host.secrets).To(Equal(1))
	})

	It("waits for the health check to pass before returning", func(ctx SpecContext) {
		host.expectFirstCreation()
		starting := healthy
		starting.Health = "starting"
		host.services.EXPECT().EnsureService(gomock.Any(), gomock.Any()).Return(starting, nil)
		host.services.EXPECT().InspectService(gomock.Any(), gomock.Any()).Return(healthy, nil)
		done := make(chan error, 1)
		go func() {
			_, err := host.owned().Ensure(ctx)
			done <- err
		}()

		Eventually(host.clock.Waiting).Should(Equal(1))
		Consistently(done).ShouldNot(Receive())
		host.clock.Advance(time.Second)
		Eventually(done).Should(Receive(BeNil()))
	})

	It("fails when the health check has not passed within its limit", func(ctx SpecContext) {
		host.expectFirstCreation()
		starting := healthy
		starting.Health = "starting"
		host.services.EXPECT().EnsureService(gomock.Any(), gomock.Any()).Return(starting, nil)
		host.services.EXPECT().InspectService(gomock.Any(), gomock.Any()).Return(starting, nil)
		done := make(chan error, 1)
		go func() {
			_, err := host.owned().Ensure(ctx)
			done <- err
		}()

		Eventually(host.clock.Waiting).Should(Equal(1))
		host.clock.Advance(docker.OwnedHealthLimit + time.Second)
		Eventually(done).Should(Receive(MatchError(docker.ErrServiceNotHealthy)))
	})

	It("refuses a container that exists without a record, rather than guess its settings", func(ctx SpecContext) {
		host.services.EXPECT().InspectService(gomock.Any(), gomock.Any()).Return(healthy, nil)

		_, err := host.owned().Ensure(ctx)
		Expect(err).To(MatchError(docker.ErrServiceRecordLost))
		Expect(filepath.Join(host.state, "metrics.json")).NotTo(BeAnExistingFile())
	})
})

var _ = Describe("OwnedService.Stop and Status", func() {
	var host *owner

	BeforeEach(func() { host = newOwner() })

	It("reports a state directory that owns no such container", func(ctx SpecContext) {
		Expect(host.owned().Stop(ctx)).To(MatchError(docker.ErrServiceNotOwned))
		_, err := host.owned().Status(ctx)
		Expect(err).To(MatchError(docker.ErrServiceNotOwned))
	})

	It("stops the recorded container and reports its location and state, never its settings", func(ctx SpecContext) {
		host.expectFirstCreation()
		host.services.EXPECT().EnsureService(gomock.Any(), gomock.Any()).Return(healthy, nil)
		record, err := host.owned().Ensure(ctx)
		Expect(err).NotTo(HaveOccurred())

		host.services.EXPECT().StopService(gomock.Any(), record.Container).Return(nil)
		Expect(host.owned().Stop(ctx)).To(Succeed())

		stopped := healthy
		stopped.Running, stopped.Status = false, "exited"
		host.services.EXPECT().InspectService(gomock.Any(), record.Container).Return(stopped, nil)
		status, err := host.owned().Status(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(status).To(Equal(docker.OwnedStatus{Location: record.OwnedLocation, State: stopped}))
		printed, err := json.Marshal(status)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(printed)).NotTo(ContainSubstring("generated"))
	})
})

var _ = Describe("NewOwnedService", func() {
	It("refuses missing capabilities, a nil option and an incomplete or relative definition", func() {
		host := newOwner()
		capabilities := []docker.OwnedServiceOption{docker.WithServices(host.services), docker.WithListener(host.listener)}

		_, err := docker.NewOwnedService(host.definition())
		Expect(err).To(MatchError(docker.ErrInvalidOption))
		_, err = docker.NewOwnedService(host.definition(), nil)
		Expect(err).To(MatchError(docker.ErrInvalidOption))
		_, err = docker.NewOwnedService(host.definition(), docker.WithServices(nil))
		Expect(err).To(MatchError(docker.ErrInvalidOption))

		relative := host.definition()
		relative.StateDirectory = "state"
		_, err = docker.NewOwnedService(relative, capabilities...)
		Expect(err).To(MatchError(docker.ErrInvalidOption))
		incomplete := host.definition()
		incomplete.Spec = nil
		_, err = docker.NewOwnedService(incomplete, capabilities...)
		Expect(err).To(MatchError(docker.ErrInvalidOption))
	})
})
