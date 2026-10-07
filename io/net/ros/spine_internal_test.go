// Copyright 2026 Candace Labs

package ros

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Unit specs of the error's own rendering; the boundary's usage patterns are
// the external specs in spine_test.go.
var _ = Describe("NotConnectedError", func() {
	It("leads with the stable status and names the operation", func() {
		failure := &NotConnectedError{Operation: OperationObserve}
		Expect(failure.Error()).To(Equal("no spine connected: observe requires the external ROS-side controller"))
	})

	It("keeps the status and reason vocabularies distinct", func() {
		Expect(NotConnectedStatus).NotTo(Equal(NotConnectedReason))
		Expect([]Operation{OperationSubmit, OperationObserve}).To(HaveEach(Not(BeEmpty())))
	})
})
