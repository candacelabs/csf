// Copyright 2026 Candace Labs

package claudecode_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/net/model/claudecode"
)

var _ = Describe("ParseLocalSocket", func() {
	DescribeTable("accepts a socket only this machine can reach",
		func(endpoint string, network string, address string) {
			socket, err := claudecode.ParseLocalSocket(endpoint)
			Expect(err).NotTo(HaveOccurred())
			Expect(socket).To(Equal(claudecode.LocalSocket{Network: network, Address: address}))
		},
		Entry("a unix socket with its scheme", "unix:/run/csf/agents.sock", claudecode.SocketUnix, "/run/csf/agents.sock"),
		Entry("a bare absolute unix path", "/run/csf/agents.sock", claudecode.SocketUnix, "/run/csf/agents.sock"),
		Entry("IPv4 loopback", "127.0.0.1:14111", claudecode.SocketLoopback, "127.0.0.1:14111"),
		Entry("IPv6 loopback", "[::1]:14111", claudecode.SocketLoopback, "[::1]:14111"),
		Entry("localhost", "localhost:14111", claudecode.SocketLoopback, "localhost:14111"),
		Entry("localhost in any case", "LocalHost:14111", claudecode.SocketLoopback, "LocalHost:14111"),
	)

	DescribeTable("rejects anything another machine could reach",
		func(endpoint string) {
			_, err := claudecode.ParseLocalSocket(endpoint)
			Expect(err).To(MatchError(claudecode.ErrNotLocal))
		},
		Entry("a private address", "10.0.0.5:14111"),
		Entry("a documentation-range public address", "198.51.100.7:14111"),
		Entry("the unspecified address", "0.0.0.0:14111"),
		Entry("a hostname", "agents.example.invalid:14111"),
		Entry("a name that merely starts with localhost", "localhost.example.invalid:14111"),
		Entry("a relative path", "run/agents.sock"),
		Entry("no port", "localhost"),
	)
})
