// Copyright 2026 Candace Labs

package docker_test

import (
	"bufio"
	"context"
	"io"
	"iter"
	"net"
	"net/netip"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/docker"
)

const (
	serviceName  = "csf-postgres-0123456789ab"
	serviceImage = "example.invalid/postgres@sha256:00"
	execID       = "exec-1"
)

var serviceSpec = docker.ServiceSpec{
	Name:           serviceName,
	Image:          serviceImage,
	Environment:    []string{"POSTGRES_USER=csf"},
	Volumes:        map[string]string{serviceName + "-data": "/var/lib/postgresql"},
	Mounts:         []docker.Mount{{Source: "/state/backups", Target: "/backups"}},
	Port:           "5432/tcp",
	HostAddress:    netip.MustParseAddr("127.0.0.1"),
	HostPort:       15999,
	HealthCheck:    []string{"pg_isready"},
	HealthInterval: 2 * time.Second,
}

// inspected is the Engine's view of the service container.
func inspected(running bool, health container.HealthStatus) client.ContainerInspectResult {
	status := container.StateExited
	if running {
		status = container.StateRunning
	}
	return client.ContainerInspectResult{Container: container.InspectResponse{
		ID:    containerID,
		State: &container.State{Status: status, Running: running, Health: &container.Health{Status: health}},
		NetworkSettings: &container.NetworkSettings{Ports: network.PortMap{
			network.MustParsePort("5432/tcp"): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "15999"}},
		}},
	}}
}

// pulled is an image pull that has finished.
type pulled struct{ io.ReadCloser }

func (pulled) JSONMessages(ctx context.Context) iter.Seq2[jsonstream.Message, error] {
	return func(yield func(message jsonstream.Message, err error) bool) {}
}
func (pulled) Wait(ctx context.Context) error { return nil }

// streamConn is a hijacked exec connection that replays one output stream.
type streamConn struct {
	net.Conn
	stream io.Reader
}

func (conn streamConn) Read(content []byte) (int, error) { return conn.stream.Read(content) }
func (streamConn) Close() error                          { return nil }

var _ = Describe("ContainerHost.EnsureService", func() {
	var api *MockIContainerAPI

	BeforeEach(func() { api = NewMockIContainerAPI(gomock.NewController(GinkgoT())) })

	It("creates a missing container on the loopback port it is given, with its volume, mount, health check and restart policy, then starts it", func(ctx SpecContext) {
		var created client.ContainerCreateOptions
		gomock.InOrder(
			api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(client.ContainerInspectResult{}, cerrdefs.ErrNotFound),
			api.EXPECT().ContainerCreate(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
					created = options
					return client.ContainerCreateResult{ID: containerID}, nil
				}),
			api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(inspected(false, ""), nil),
			api.EXPECT().ContainerStart(gomock.Any(), containerID, gomock.Any()).Return(client.ContainerStartResult{}, nil),
			api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(inspected(true, container.Starting), nil),
		)

		state, err := newHost(api).EnsureService(ctx, serviceSpec)
		Expect(err).NotTo(HaveOccurred())
		Expect(state).To(Equal(docker.ServiceState{ID: containerID, Status: "running", Running: true, Health: "starting", HostPort: 15999}))

		Expect(created.Name).To(Equal(serviceName))
		Expect(created.Config.Image).To(Equal(serviceImage))
		Expect(created.Config.Healthcheck.Test).To(Equal([]string{"CMD", "pg_isready"}))
		Expect(created.Config.Healthcheck.Interval).To(Equal(2 * time.Second))
		port := network.MustParsePort("5432/tcp")
		Expect(created.HostConfig.PortBindings).To(Equal(network.PortMap{port: {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "15999"}}}))
		Expect(created.HostConfig.RestartPolicy.Name).To(Equal(container.RestartPolicyUnlessStopped))
		Expect(created.HostConfig.Mounts).To(ConsistOf(
			mount.Mount{Type: mount.TypeVolume, Source: serviceName + "-data", Target: "/var/lib/postgresql"},
			mount.Mount{Type: mount.TypeBind, Source: "/state/backups", Target: "/backups"},
		))
	})

	It("pulls the image when the Engine lacks it, then creates the container", func(ctx SpecContext) {
		gomock.InOrder(
			api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(client.ContainerInspectResult{}, cerrdefs.ErrNotFound),
			api.EXPECT().ContainerCreate(gomock.Any(), gomock.Any()).Return(client.ContainerCreateResult{}, cerrdefs.ErrNotFound),
			api.EXPECT().ImagePull(gomock.Any(), serviceImage, gomock.Any()).Return(pulled{io.NopCloser(strings.NewReader(""))}, nil),
			api.EXPECT().ContainerCreate(gomock.Any(), gomock.Any()).Return(client.ContainerCreateResult{ID: containerID}, nil),
			api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(inspected(true, container.Healthy), nil),
		)

		state, err := newHost(api).EnsureService(ctx, serviceSpec)
		Expect(err).NotTo(HaveOccurred())
		Expect(state.Health).To(Equal("healthy"))
	})

	It("starts a stopped container without recreating it, and leaves a running one alone", func(ctx SpecContext) {
		gomock.InOrder(
			api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(inspected(false, container.Unhealthy), nil),
			api.EXPECT().ContainerStart(gomock.Any(), containerID, gomock.Any()).Return(client.ContainerStartResult{}, nil),
			api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(inspected(true, container.Starting), nil),
			api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(inspected(true, container.Healthy), nil),
		)
		host := newHost(api)

		_, err := host.EnsureService(ctx, serviceSpec)
		Expect(err).NotTo(HaveOccurred())
		state, err := host.EnsureService(ctx, serviceSpec)
		Expect(err).NotTo(HaveOccurred())
		Expect(state.Running).To(BeTrue())
	})

	It("publishes the one host port on every extra address and passes the command", func(ctx SpecContext) {
		var created client.ContainerCreateOptions
		gomock.InOrder(
			api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(client.ContainerInspectResult{}, cerrdefs.ErrNotFound),
			api.EXPECT().ContainerCreate(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
					created = options
					return client.ContainerCreateResult{ID: containerID}, nil
				}),
			api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(inspected(true, container.Healthy), nil),
		)
		spec := serviceSpec
		spec.ExtraHostAddresses = []netip.Addr{netip.MustParseAddr("192.0.2.7")}
		spec.Command = []string{"--config.file=/etc/csf/prometheus.yml"}

		_, err := newHost(api).EnsureService(ctx, spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Config.Cmd).To(Equal([]string{"--config.file=/etc/csf/prometheus.yml"}))
		Expect(created.HostConfig.PortBindings).To(Equal(network.PortMap{network.MustParsePort("5432/tcp"): {
			{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "15999"},
			{HostIP: netip.MustParseAddr("192.0.2.7"), HostPort: "15999"},
		}}))
	})

	It("grants the GPUs through the nvidia driver only when the spec asks", func(ctx SpecContext) {
		var created []client.ContainerCreateOptions
		for range 2 {
			gomock.InOrder(
				api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(client.ContainerInspectResult{}, cerrdefs.ErrNotFound),
				api.EXPECT().ContainerCreate(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
						created = append(created, options)
						return client.ContainerCreateResult{ID: containerID}, nil
					}),
				api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(inspected(true, container.Healthy), nil),
			)
		}
		spec := serviceSpec
		spec.GPU = true
		_, err := newHost(api).EnsureService(ctx, spec)
		Expect(err).NotTo(HaveOccurred())
		_, err = newHost(api).EnsureService(ctx, serviceSpec)
		Expect(err).NotTo(HaveOccurred())
		Expect(created[0].HostConfig.DeviceRequests).To(Equal([]container.DeviceRequest{{Driver: "nvidia", Count: -1, Capabilities: [][]string{{"gpu"}}}}))
		Expect(created[1].HostConfig.DeviceRequests).To(BeEmpty())
	})

	It("refuses extra addresses without a fixed host port, since each would get its own", func(ctx SpecContext) {
		api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(client.ContainerInspectResult{}, cerrdefs.ErrNotFound)
		spec := serviceSpec
		spec.HostPort = 0
		spec.ExtraHostAddresses = []netip.Addr{netip.MustParseAddr("192.0.2.7")}
		_, err := newHost(api).EnsureService(ctx, spec)
		Expect(err).To(MatchError(docker.ErrInvalidOption))
	})

	It("refuses a port it cannot parse and creates nothing", func(ctx SpecContext) {
		api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(client.ContainerInspectResult{}, cerrdefs.ErrNotFound)
		spec := serviceSpec
		spec.Port = "postgres"
		_, err := newHost(api).EnsureService(ctx, spec)
		Expect(err).To(MatchError(docker.ErrInvalidOption))
	})
})

var _ = Describe("ContainerHost.StopService", func() {
	It("stops the container and reports one the Engine does not have", func(ctx SpecContext) {
		api := NewMockIContainerAPI(gomock.NewController(GinkgoT()))
		gomock.InOrder(
			api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(inspected(true, container.Healthy), nil),
			api.EXPECT().ContainerStop(gomock.Any(), serviceName, gomock.Any()).Return(client.ContainerStopResult{}, nil),
			api.EXPECT().ContainerInspect(gomock.Any(), serviceName, gomock.Any()).Return(client.ContainerInspectResult{}, cerrdefs.ErrNotFound),
		)
		host := newHost(api)

		Expect(host.StopService(ctx, serviceName)).To(Succeed())
		Expect(host.StopService(ctx, serviceName)).To(MatchError(docker.ErrNoContainer))
	})
})

var _ = Describe("ContainerHost.SignalService", func() {
	It("sends the signal to the named container", func(ctx SpecContext) {
		api := NewMockIContainerAPI(gomock.NewController(GinkgoT()))
		api.EXPECT().ContainerKill(gomock.Any(), serviceName, client.ContainerKillOptions{Signal: "HUP"}).Return(client.ContainerKillResult{}, nil)
		Expect(newHost(api).SignalService(ctx, serviceName, "HUP")).To(Succeed())
	})
})

var _ = Describe("ContainerHost.Exec", func() {
	It("runs the command as the user given and returns both outputs and the exit code", func(ctx SpecContext) {
		api := NewMockIContainerAPI(gomock.NewController(GinkgoT()))
		var options client.ExecCreateOptions
		conn := streamConn{stream: multiplexed("dumped\n", "warning\n")}
		gomock.InOrder(
			api.EXPECT().ExecCreate(gomock.Any(), serviceName, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, given client.ExecCreateOptions) (client.ExecCreateResult, error) {
					options = given
					return client.ExecCreateResult{ID: execID}, nil
				}),
			api.EXPECT().ExecAttach(gomock.Any(), execID, gomock.Any()).Return(
				client.ExecAttachResult{HijackedResponse: client.HijackedResponse{Conn: conn, Reader: bufio.NewReader(conn)}}, nil),
			api.EXPECT().ExecInspect(gomock.Any(), execID, gomock.Any()).Return(client.ExecInspectResult{ExitCode: 3}, nil),
		)

		result, err := newHost(api).Exec(ctx, serviceName, docker.ExecSpec{Command: []string{"pg_dump"}, User: "1000:1000"})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(docker.ExecResult{ExitCode: 3, Stdout: []byte("dumped\n"), Stderr: []byte("warning\n")}))
		Expect(options.Cmd).To(Equal([]string{"pg_dump"}))
		Expect(options.User).To(Equal("1000:1000"))
		Expect(options.AttachStdout && options.AttachStderr).To(BeTrue())
	})
})
