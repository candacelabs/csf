// Copyright 2026 Candace Labs

package grpc_test

import (
	"context"
	stdnet "net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	upstream "google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"

	ipcgrpc "github.com/candacelabs/csf/ipc/net/grpc"
)

const (
	peerAddress = "warden.test:7717"
	target      = "passthrough:///" + peerAddress
	bufferSize  = 1 << 20
)

var _ = Describe("ClientConnector", func() {
	It("dials every connection through the granted dialer capability", func(ctx SpecContext) {
		pipe := bufconn.Listen(bufferSize)
		server := upstream.NewServer()
		healthv1.RegisterHealthServer(server, health.NewServer())
		served := make(chan error, 1)
		go func() { served <- server.Serve(pipe) }()
		DeferCleanup(func() {
			server.Stop()
			Eventually(served).Should(Receive())
		})

		dialer := NewMockIDialer(gomock.NewController(GinkgoT()))
		dialer.EXPECT().DialContext(gomock.Any(), "tcp", peerAddress).DoAndReturn(
			func(ctx context.Context, _ string, _ string) (stdnet.Conn, error) { return pipe.DialContext(ctx) },
		).MinTimes(1)

		connector, err := ipcgrpc.NewPlaintextClientConnector(dialer)
		Expect(err).NotTo(HaveOccurred())
		var _ ipcgrpc.IClientConnector = connector
		connection, err := connector.NewClient(target)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(connection.Close)

		response, err := healthv1.NewHealthClient(connection).Check(ctx, &healthv1.HealthCheckRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.GetStatus()).To(Equal(healthv1.HealthCheckResponse_SERVING))
	})

	It("requires a dialer and reports an unusable target", func() {
		_, err := ipcgrpc.NewPlaintextClientConnector(nil)
		Expect(err).To(HaveOccurred())

		connector, err := ipcgrpc.NewPlaintextClientConnector(NewMockIDialer(gomock.NewController(GinkgoT())))
		Expect(err).NotTo(HaveOccurred())
		_, err = connector.NewClient("unknown-scheme://%%")
		Expect(err).To(MatchError(ContainSubstring("ipc/net/grpc: open client")))
	})
})
