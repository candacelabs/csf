// Copyright 2026 Candace Labs

package net_test

import (
	"context"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ipcnet "github.com/candacelabs/csf/ipc/net"
)

const (
	tcp      = "tcp"
	loopback = "127.0.0.1:0"
	greeting = "hello over the kernel"
)

var _ = Describe("HostNetwork", func() {
	It("listens and dials through one capability", func(ctx SpecContext) {
		network := ipcnet.NewHostNetwork()
		var _ ipcnet.IListener = network
		var _ ipcnet.IDialer = network

		listener, err := network.Listen(ctx, tcp, loopback)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(listener.Close)

		client, err := network.DialContext(ctx, tcp, listener.Addr().String())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(client.Close)
		server, err := listener.Accept()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(server.Close)

		_, err = io.WriteString(client, greeting)
		Expect(err).NotTo(HaveOccurred())
		received := make([]byte, len(greeting))
		_, err = io.ReadFull(server, received)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(received)).To(Equal(greeting))
	})

	It("names the address in a failure", func(ctx SpecContext) {
		network := ipcnet.NewHostNetwork()
		_, err := network.Listen(ctx, tcp, "not-an-address")
		Expect(err).To(MatchError(ContainSubstring("ipc/net: listen tcp not-an-address")))
		_, err = network.DialContext(context.Background(), tcp, "127.0.0.1:1")
		Expect(err).To(MatchError(ContainSubstring("ipc/net: dial tcp 127.0.0.1:1")))
	})
})
