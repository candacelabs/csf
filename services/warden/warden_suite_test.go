package warden_test

// Contract test suite for the frozen contract package
// (github.com/candacelabs/csf/services/warden).
//
// These specs live in the EXTERNAL test package (warden_test) on purpose: they
// may only touch the exported surface, which is exactly what makes them a
// contract suite. Every file here is named *_contract_test.go to distinguish it
// from ordinary implementation tests.
//
// The frozen contract package ships with NO tests of its own; this suite is the
// executable freeze of its wire types, path constants, and clock semantics.

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWardenContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "warden contract suite")
}

// fixedTime is a single, arbitrary, non-zero instant used across the golden
// JSON assertions. It is deliberately UTC with no sub-second component so its
// RFC 3339 rendering is stable and human-checkable ("2026-07-21T15:04:05Z").
var fixedTime = time.Date(2026, 7, 21, 15, 4, 5, 0, time.UTC)

// fixedTimeJSON is how fixedTime marshals inside a JSON document.
const fixedTimeJSON = "2026-07-21T15:04:05Z"

// zeroTimeJSON is how the zero time.Time marshals. time.Time is not omitempty
// on any contract field, so a zero timestamp appears verbatim in the wire form.
const zeroTimeJSON = "0001-01-01T00:00:00Z"
