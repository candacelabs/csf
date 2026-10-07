// Copyright 2026 Candace Labs

package database_test

import (
	"context"
	"net"
	"net/netip"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/io/ipc/docker/mocks"
	"github.com/candacelabs/csf/services/database"
)

const (
	probedPort = 15999
	backupUser = "1000:1000"
)

// probe is the listener the first Ensure opens to find a free port.
type probe struct{ net.Listener }

func (probe) Addr() net.Addr {
	return net.TCPAddrFromAddrPort(netip.AddrPortFrom(netip.IPv4Unspecified(), probedPort))
}
func (probe) Close() error { return nil }

var _ = Describe("NewOwnedDatabase", func() {
	It("provisions PostgreSQL on loopback with a generated password, the backup mount and a TCP health check, and names its csfpg settings", func(ctx SpecContext) {
		controller := gomock.NewController(GinkgoT())
		services, listener := mocks.NewMockIServices(controller), NewMockIListener(controller)
		state := GinkgoT().TempDir()
		services.EXPECT().InspectService(gomock.Any(), gomock.Any()).Return(docker.ServiceState{}, docker.ErrNoContainer)
		listener.EXPECT().Listen(gomock.Any(), "tcp", "0.0.0.0:0").Return(probe{}, nil)
		var spec docker.ServiceSpec
		services.EXPECT().EnsureService(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, given docker.ServiceSpec) (docker.ServiceState, error) {
				spec = given
				return docker.ServiceState{Running: true, Health: "healthy", HostPort: probedPort}, nil
			})
		owned, err := database.NewOwnedDatabase(state, backupUser, docker.WithServices(services), docker.WithListener(listener))
		Expect(err).NotTo(HaveOccurred())

		record, err := owned.Ensure(ctx)
		Expect(err).NotTo(HaveOccurred())

		Expect(record.Container).To(MatchRegexp(`^csf-postgres-[0-9a-f]{12}$`))
		Expect(record.Image).To(Equal(database.Image))
		Expect(record.Host).To(Equal("127.0.0.1"))
		access := record.Settings
		Expect(access.User).To(Equal("csf"))
		Expect(access.Database).To(Equal("csf"))
		Expect(access.Password).To(HaveLen(26))
		Expect(access.BackupDirectory).To(Equal(filepath.Join(state, "backups")))
		Expect(access.BackupDirectory).To(BeADirectory())
		Expect(access.BackupUser).To(Equal(backupUser))
		Expect(database.Settings(record).URL).To(Equal("postgres://csf:" + access.Password + "@127.0.0.1:15999/csf?sslmode=disable"))

		Expect(spec.Environment).To(ConsistOf("POSTGRES_USER=csf", "POSTGRES_PASSWORD="+access.Password, "POSTGRES_DB=csf"))
		Expect(spec.Volumes).To(Equal(map[string]string{record.Volume: "/var/lib/postgresql"}))
		Expect(spec.Mounts).To(Equal([]docker.Mount{{Source: access.BackupDirectory, Target: "/backups"}}))
		Expect(spec.HostAddress).To(Equal(netip.MustParseAddr("127.0.0.1")))
		Expect(spec.Port).To(Equal("5432/tcp"))
		Expect(spec.HealthCheck).To(Equal([]string{"pg_isready", "--host", "127.0.0.1", "--username", "csf", "--dbname", "csf"}))
	})
})
