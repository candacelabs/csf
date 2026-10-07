// Copyright 2026 Candace Labs

package opsview

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness"
)

// One sample as the harness writes it: 56 MiB for itself, 1200 MiB for its
// executors, the bound of this host's first derivation.
const residentSample = `{"at":"2026-10-04T12:00:00Z","harness_rss_bytes":58720256,"executors_rss_bytes":1258291200,"open_sessions":5,"executors_alive":4,"resumes":1,"resume_ttft_ms":3497,"idle_bound_s":661,"idle_bound_quantile":0.829,"gaps":486}` + "\n"

var _ = Describe("the resident panel", func() {
	It("shows the latest sample's numbers and the bound with its derivation", func() {
		view := residentViewOf(harness.ReadResidentSeries([]byte(residentSample)))
		Expect(view.Latest).To(Equal(residentPoint{Time: "10-04 12:00", Harness: "56 MB", Executors: "1200 MB", Alive: 4}))
		Expect(view.Points).To(HaveLen(1))
		Expect(view.OpenSessions).To(Equal(5))
		Expect(view.ExecutorsAlive).To(Equal(4))
		Expect(view.Resumes).To(Equal(1))
		Expect(view.ResumeTTFT).To(Equal("3.5s"))
		Expect(view.IdleBound).To(Equal("11m1s (q0.83 of 486 gaps)"))
	})

	It("is empty with no bound before a sample exists, and dirty only when the series changed", func() {
		empty := residentViewOf(nil)
		Expect(empty.Points).To(BeEmpty())
		Expect(empty.IdleBound).To(Equal(residentNoBoundYet))
		before := viewState{resident: harness.ReadResidentSeries([]byte(residentSample))}
		Expect(residentChanged(before, before)).To(BeFalse())
		Expect(residentChanged(viewState{}, before)).To(BeTrue())
		Expect(residentEqual(nil, harness.ReadResidentSeries(nil))).To(BeTrue())
	})
})
