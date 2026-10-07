// Copyright 2026 Candace Labs

// Package relaytest holds the conformance specs every relay.IRegistry
// implementation must pass, and the messaging specs every relay.Relay[Body]
// must pass over such a registry. The in-memory registry runs them today; the
// durable csfpg-backed registry (slice A1) runs exactly the same specs, so the two
// cannot drift apart on the semantics written on relay.IRegistry. Both suites
// are generic: the registry suite over the registry's own type, the messaging
// suite over the message contract the relay carries.
//
// Ginkgo and Gomega are imported qualified because this is a library, not a
// test file: dot-importing an assertion vocabulary into a production
// namespace is what CS-11's library counterweight forbids.
package relaytest

import (
	"context"

	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/candacelabs/csf/io"
	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/io/net/model/copilot"
	"github.com/candacelabs/csf/services/relay"
)

const (
	conformanceAgent   relay.AgentID = "conformance-agent"
	conformanceOther   relay.AgentID = "conformance-other"
	conformanceInvalid relay.AgentID = "Not An Agent"
	conformanceSocket                = "unix:/run/csf/conformance.sock"
	conformanceAdapter               = "198.51.100.7:14111"
)

// conformanceSpec is one registry requirement: a name and a check run
// against a fresh, empty registry.
type conformanceSpec struct {
	name  string
	check func(ctx context.Context, registry relay.IRegistry)
}

// conformanceSpecs is the whole contract, in data, so a reader (or a spec)
// can enumerate it.
var conformanceSpecs = []conformanceSpec{
	{"resolves an agent and locates its address after registration", resolvesAfterRegistration},
	{"replaces an agent's address on re-registration and retires the old one at once", replacesOnReregistration},
	{"gives one address to at most one agent", oneAgentPerAddress},
	{"reports an agent or address it has never seen", reportsUnknown},
	{"refuses a registration without a valid agent or address", refusesInvalid},
	{"round-trips a Claude Code host address with its type and tier", roundTrips(hostAddress, io.TierIpc)},
	{"round-trips a Copilot in_process address with its type and tier", roundTrips(inProcessAddress, io.TierInProcess)},
	{"round-trips a Copilot network address with its type and tier", roundTrips(networkAddress, io.TierNet)},
	{"honors a canceled context", honorsCancellation},
}

// DescribeRegistryConformance registers the IRegistry conformance specs under
// name. newRegistry builds one empty registry per spec; it runs inside a
// BeforeEach, so it may call ginkgo.DeferCleanup to release a database or
// schema. Call it at package level in a Ginkgo suite:
//
//	var _ = relaytest.DescribeRegistryConformance("MemoryRegistry", relay.NewMemoryRegistry)
func DescribeRegistryConformance[Registry relay.IRegistry](name string, newRegistry func() Registry) bool {
	return ginkgo.Describe(name+" conforms to relay.IRegistry", func() {
		var registry Registry
		ginkgo.BeforeEach(func() { registry = newRegistry() })
		for _, spec := range conformanceSpecs {
			ginkgo.It(spec.name, func(ctx ginkgo.SpecContext) { spec.check(ctx, registry) })
		}
	})
}

func resolvesAfterRegistration(ctx context.Context, registry relay.IRegistry) {
	address := hostAddress()
	registration := relay.Registration{Agent: conformanceAgent, Address: address}
	gomega.Expect(registry.Register(ctx, registration)).To(gomega.Succeed())

	resolved, err := registry.Resolve(ctx, conformanceAgent)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(resolved).To(gomega.Equal(registration))
	located, err := registry.Locate(ctx, address.Key())
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(located).To(gomega.Equal(registration))
}

func replacesOnReregistration(ctx context.Context, registry relay.IRegistry) {
	before, after := hostAddress(), hostAddress()
	gomega.Expect(registry.Register(ctx, relay.Registration{Agent: conformanceAgent, Address: before})).To(gomega.Succeed())
	gomega.Expect(registry.Register(ctx, relay.Registration{Agent: conformanceAgent, Address: after})).To(gomega.Succeed())

	resolved, err := registry.Resolve(ctx, conformanceAgent)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(resolved.Address).To(gomega.Equal(after))
	_, err = registry.Locate(ctx, before.Key())
	gomega.Expect(err).To(gomega.MatchError(relay.ErrUnknownAddress))
	located, err := registry.Locate(ctx, after.Key())
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(located.Agent).To(gomega.Equal(conformanceAgent))
}

func oneAgentPerAddress(ctx context.Context, registry relay.IRegistry) {
	shared := hostAddress()
	gomega.Expect(registry.Register(ctx, relay.Registration{Agent: conformanceAgent, Address: shared})).To(gomega.Succeed())

	err := registry.Register(ctx, relay.Registration{Agent: conformanceOther, Address: shared})
	gomega.Expect(err).To(gomega.MatchError(relay.ErrAddressTaken))
	_, err = registry.Resolve(ctx, conformanceOther)
	gomega.Expect(err).To(gomega.MatchError(relay.ErrUnknownAgent), "a refused registration leaves no trace")
	gomega.Expect(registry.Register(ctx, relay.Registration{Agent: conformanceAgent, Address: shared})).To(gomega.Succeed(),
		"re-registering an agent at the address it already holds is an upsert, not a conflict")
}

func reportsUnknown(ctx context.Context, registry relay.IRegistry) {
	_, err := registry.Resolve(ctx, conformanceAgent)
	gomega.Expect(err).To(gomega.MatchError(relay.ErrUnknownAgent))
	_, err = registry.Locate(ctx, hostAddress().Key())
	gomega.Expect(err).To(gomega.MatchError(relay.ErrUnknownAddress))
}

func refusesInvalid(ctx context.Context, registry relay.IRegistry) {
	gomega.Expect(registry.Register(ctx, relay.Registration{Agent: conformanceInvalid, Address: hostAddress()})).
		To(gomega.MatchError(relay.ErrInvalidRegistration))
	gomega.Expect(registry.Register(ctx, relay.Registration{Agent: conformanceAgent})).
		To(gomega.MatchError(relay.ErrInvalidRegistration))
}

// roundTrips checks that storage returns the provider's own address type,
// equal to what was registered, so the tier survives storage.
func roundTrips[Address model.IAgentAddress](newAddress func() Address, tier io.Tier) func(ctx context.Context, registry relay.IRegistry) {
	return func(ctx context.Context, registry relay.IRegistry) {
		address := newAddress()
		gomega.Expect(registry.Register(ctx, relay.Registration{Agent: conformanceAgent, Address: address})).To(gomega.Succeed())

		resolved, err := registry.Resolve(ctx, conformanceAgent)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(resolved.Address).To(gomega.BeAssignableToTypeOf(address), "storage must return the provider's own type")
		gomega.Expect(resolved.Address).To(gomega.Equal(address))
		gomega.Expect(resolved.Kind()).To(gomega.Equal(tier))
	}
}

func honorsCancellation(ctx context.Context, registry relay.IRegistry) {
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	gomega.Expect(registry.Register(canceled, relay.Registration{Agent: conformanceAgent, Address: hostAddress()})).
		To(gomega.MatchError(context.Canceled))
	_, err := registry.Resolve(canceled, conformanceAgent)
	gomega.Expect(err).To(gomega.MatchError(context.Canceled))
	_, err = registry.Locate(canceled, hostAddress().Key())
	gomega.Expect(err).To(gomega.MatchError(context.Canceled))
}

func hostAddress() claudecode.HostAddress {
	address, err := claudecode.NewHostAddress(uuid.New(), conformanceSocket)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return address
}

func inProcessAddress() copilot.InProcessAddress {
	address, err := copilot.NewInProcessAddress(uuid.New())
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return address
}

func networkAddress() copilot.NetworkAddress {
	address, err := copilot.NewNetworkAddress(uuid.New(), conformanceAdapter)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return address
}
