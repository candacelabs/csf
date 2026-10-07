// Copyright 2026 Candace Labs

package views_test

import (
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/services/views"
	"github.com/candacelabs/csf/services/views/mocks"
)

// probe is a listener a free-port probe returns: it only reports its port.
type probe struct{ port int }

func (probe) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (probe) Close() error              { return nil }
func (listener probe) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4zero, Port: listener.port}
}

var (
	loopback = netip.MustParseAddr("127.0.0.1")
	tailnet  = netip.MustParseAddr("192.0.2.7")
	healthy  = docker.ServiceState{Running: true, Health: "healthy"}
)

const freeDisk = 88_000_000_000

func newStack(state string, containers views.IContainers, listener *MockIListener, extra ...views.StackOption) (*views.Stack, error) {
	return views.NewStack(append([]views.StackOption{
		views.WithStateDirectory(state), views.WithContainers(containers), views.WithListener(listener),
		views.WithFreeBytes(func(path string) (uint64, error) { return freeDisk, nil }),
		views.WithPublishedAddresses(loopback, tailnet), views.WithScrapeTarget("192.0.2.7:14120"),
	}, extra...)...)
}

// expectCreated expects both containers created on their first start, on
// the probed ports, healthy at once, and Prometheus told to reread its new
// configuration; it returns the specs as they are passed.
func expectCreated(containers *mocks.MockIContainers, listener *MockIListener) *[]docker.ServiceSpec {
	var specs []docker.ServiceSpec
	record := func(_ context.Context, spec docker.ServiceSpec) (docker.ServiceState, error) {
		specs = append(specs, spec)
		return healthy, nil
	}
	gomock.InOrder(
		containers.EXPECT().InspectService(gomock.Any(), gomock.Any()).Return(docker.ServiceState{}, docker.ErrNoContainer),
		listener.EXPECT().Listen(gomock.Any(), "tcp", "0.0.0.0:0").Return(probe{port: 41001}, nil),
		containers.EXPECT().EnsureService(gomock.Any(), gomock.Any()).DoAndReturn(record),
		containers.EXPECT().InspectService(gomock.Any(), gomock.Any()).Return(docker.ServiceState{}, docker.ErrNoContainer),
		listener.EXPECT().Listen(gomock.Any(), "tcp", "0.0.0.0:0").Return(probe{port: 41002}, nil),
		containers.EXPECT().EnsureService(gomock.Any(), gomock.Any()).DoAndReturn(record),
		containers.EXPECT().SignalService(gomock.Any(), gomock.Any(), "HUP").Return(nil),
	)
	return &specs
}

var _ = Describe("Stack", func() {
	var (
		state      string
		containers *mocks.MockIContainers
		listener   *MockIListener
	)

	BeforeEach(func() {
		state = GinkgoT().TempDir()
		controller := gomock.NewController(GinkgoT())
		containers, listener = mocks.NewMockIContainers(controller), NewMockIListener(controller)
	})

	It("creates Prometheus and Grafana on free ports, both addresses and a retention derived from free disk, then reuses them", func(ctx SpecContext) {
		specs := expectCreated(containers, listener)
		stack, err := newStack(state, containers, listener)
		Expect(err).NotTo(HaveOccurred())

		record, err := stack.Ensure(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(record.Prometheus.Port).To(Equal(uint16(41001)))
		Expect(record.Grafana.Port).To(Equal(uint16(41002)))
		Expect(record.Addresses).To(Equal([]string{"127.0.0.1", "192.0.2.7"}))
		Expect(record.Prometheus.Settings.Bytes).To(Equal(uint64(freeDisk / views.RetentionShare)))
		Expect(record.Prometheus.Settings.Derivation).To(HavePrefix("1/100 of the 88000000000 bytes free"))
		Expect(record.Grafana.Settings.Password).NotTo(BeEmpty())
		Expect(record.GrafanaURL("192.0.2.7")).To(Equal("http://192.0.2.7:41002"))

		prometheus, grafana := (*specs)[0], (*specs)[1]
		Expect(prometheus.Name).To(HavePrefix("csf-prometheus-"))
		Expect(prometheus.Image).To(Equal(views.PrometheusImage))
		Expect(prometheus.HostAddress).To(Equal(loopback))
		Expect(prometheus.ExtraHostAddresses).To(Equal([]netip.Addr{tailnet}))
		Expect(prometheus.Command).To(ContainElement("--storage.tsdb.retention.size=880000000B"))
		Expect(grafana.Name).To(HavePrefix("csf-grafana-"))
		Expect(grafana.HostPort).To(Equal(uint16(41002)))
		Expect(grafana.Environment).To(ContainElement("GF_SECURITY_ADMIN_PASSWORD=" + record.Grafana.Settings.Password))

		configuration, err := os.ReadFile(filepath.Join(state, views.Directory, "prometheus", "prometheus.yml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(configuration)).To(ContainSubstring(`targets: ["192.0.2.7:14120"]`))
		datasource, err := os.ReadFile(filepath.Join(state, views.Directory, "grafana", "datasources", "csf.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(datasource)).To(ContainSubstring("url: http://192.0.2.7:41001"))
		dashboard, err := os.ReadFile(filepath.Join(state, views.Directory, "dashboards", "csf.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(dashboard).To(Equal(views.DashboardDocument()))
		info, err := os.Stat(filepath.Join(state, views.Directory, "prometheus"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o755)))

		containers.EXPECT().EnsureService(gomock.Any(), gomock.Any()).Return(healthy, nil).Times(2)
		again, err := stack.Ensure(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(record))
		shown, err := stack.Record()
		Expect(err).NotTo(HaveOccurred())
		Expect(shown.Grafana.Settings.Password).To(BeEmpty())
		Expect(shown.Prometheus.Port).To(Equal(uint16(41001)))
	})

	It("refuses a container that exists without its record rather than guess its password", func(ctx SpecContext) {
		containers.EXPECT().InspectService(gomock.Any(), gomock.Any()).Return(healthy, nil)
		stack, err := newStack(state, containers, listener)
		Expect(err).NotTo(HaveOccurred())
		_, err = stack.Ensure(ctx)
		Expect(err).To(MatchError(docker.ErrServiceRecordLost))
	})

	It("reports a stack it has not provisioned", func() {
		stack, err := newStack(state, containers, listener)
		Expect(err).NotTo(HaveOccurred())
		_, err = stack.Record()
		Expect(err).To(MatchError(docker.ErrServiceNotOwned))
	})

	It("needs every capability and an address a container reaches the host on", func() {
		_, err := views.NewStack(views.WithStateDirectory(state))
		Expect(err).To(MatchError(views.ErrMissingCapability))
		_, err = newStack(state, containers, listener, views.WithPublishedAddresses(loopback))
		Expect(err).To(MatchError(views.ErrMissingCapability))
		_, err = newStack(state, containers, listener, views.WithStateDirectory("relative"))
		Expect(err).To(MatchError(views.ErrInvalidOption))
		_, err = newStack(state, containers, listener, nil)
		Expect(err).To(MatchError(views.ErrInvalidOption))
	})

	Describe("Backfill", func() {
		var (
			stack  *views.Stack
			record views.StackRecord
		)

		BeforeEach(func(ctx SpecContext) {
			expectCreated(containers, listener)
			var err error
			stack, err = newStack(state, containers, listener)
			Expect(err).NotTo(HaveOccurred())
			record, err = stack.Ensure(ctx)
			Expect(err).NotTo(HaveOccurred())
		})

		It("has promtool turn the history into blocks in Prometheus's data directory, records how far it reached and removes the file", func(ctx SpecContext) {
			var command []string
			containers.EXPECT().Exec(gomock.Any(), record.Prometheus.Container, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, spec docker.ExecSpec) (docker.ExecResult, error) {
					command = spec.Command
					_, err := os.Stat(filepath.Join(state, views.Directory, "prometheus", views.BackfillFile))
					Expect(err).NotTo(HaveOccurred())
					return docker.ExecResult{}, nil
				})

			samples, err := stack.Backfill(ctx, record, fixtureHistory(), at(12, 30))
			Expect(err).NotTo(HaveOccurred())
			Expect(samples).To(BeNumerically(">", 0))
			Expect(strings.Join(command, " ")).To(Equal("promtool tsdb create-blocks-from openmetrics /etc/csf-prometheus/backfill.om /prometheus"))
			after, err := stack.Record()
			Expect(err).NotTo(HaveOccurred())
			Expect(after.BackfilledThrough).To(Equal(at(12, 30)))
			_, err = os.Stat(filepath.Join(state, views.Directory, "prometheus", views.BackfillFile))
			Expect(err).To(MatchError(os.ErrNotExist))
		})

		It("reports promtool's failure and keeps the mark where it was", func(ctx SpecContext) {
			containers.EXPECT().Exec(gomock.Any(), gomock.Any(), gomock.Any()).Return(docker.ExecResult{ExitCode: 1, Stderr: []byte("out of bounds")}, nil)
			_, err := stack.Backfill(ctx, record, fixtureHistory(), at(12, 30))
			Expect(err).To(MatchError(ContainSubstring("out of bounds")))
			after, err := stack.Record()
			Expect(err).NotTo(HaveOccurred())
			Expect(after.BackfilledThrough).To(Equal(time.Time{}))
		})
	})
})
