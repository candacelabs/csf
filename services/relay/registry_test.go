// Copyright 2026 Candace Labs

package relay_test

import (
	"strconv"

	"github.com/candacelabs/csf/services/relay"
	"github.com/candacelabs/csf/services/relay/relaytest"
)

// The in-memory registry runs the same conformance specs the durable csfpg-backed
// registry will.
var _ = relaytest.DescribeRegistryConformance("MemoryRegistry", relay.NewMemoryRegistry)

// conformanceMessagePrefix makes the plain-string bodies of the messaging
// conformance run distinct.
const conformanceMessagePrefix = "conformance message "

// A relay of plain strings runs the generic messaging conformance specs; csf
// runs the same specs over its agent message contract.
var _ = relaytest.DescribeRelayConformance("Relay[string] over MemoryRegistry", relay.NewMemoryRegistry,
	func(sequence int) string { return conformanceMessagePrefix + strconv.Itoa(sequence) })
