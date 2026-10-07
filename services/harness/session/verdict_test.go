// Copyright 2026 Candace Labs

package session_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/session"
)

const (
	passingVerdict = "The record holds.\n\n```verdict\n{\"pass\": true, \"defects\": []}\n```\n"
	failingVerdict = "```verdict\n{\"pass\": false, \"defects\": [{\"statement\": \"go test ./... passes\", \"field\": \"acceptance[0]\", \"evidence\": [\"services/a/a.go:12\"]}]}\n```"
	// verifierReply is an assistant record whose final text carries a verdict.
	verifierReply = `{"event_type":"assistant","turn":1,"direction":"out","event":{"type":"assistant","message":{"content":[{"type":"text","text":"` +
		"```verdict\\n{\\\"pass\\\": true, \\\"defects\\\": []}\\n```" + `"}]}}}`
	toolOnly = `{"event_type":"assistant","turn":1,"direction":"out","event":{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{}}]}}}`
)

var _ = Describe("the verifier station's verdict", func() {
	It("reads a passing verdict as complete", func() {
		verdict, found, err := session.ParseVerdict(passingVerdict)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(*verdict.Pass).To(BeTrue())
		Expect(verdict.Incomplete()).To(BeEmpty())
	})

	It("reads a failing verdict's defect with its field and evidence", func() {
		verdict, found, err := session.ParseVerdict(failingVerdict)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(verdict.Defects).To(Equal([]session.Defect{{
			Statement: "go test ./... passes", Field: "acceptance[0]", Evidence: []string{"services/a/a.go:12"},
		}}))
		Expect(verdict.Incomplete()).To(BeEmpty())
	})

	It("reads the last verdict block when a reply carries several", func() {
		verdict, _, err := session.ParseVerdict(passingVerdict + failingVerdict)
		Expect(err).NotTo(HaveOccurred())
		Expect(verdict.Defects).To(HaveLen(1))
	})

	It("finds no verdict in a reply without one", func() {
		_, found, err := session.ParseVerdict("looks good to me")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())
	})

	It("names a block that is not one JSON object", func() {
		_, found, err := session.ParseVerdict("```verdict\nlooks good\n```")
		Expect(found).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("the verdict block is not a JSON object")))
	})

	DescribeTable("names what an incomplete verdict leaves missing",
		func(reply string, missing []string) {
			verdict, _, err := session.ParseVerdict(reply)
			Expect(err).NotTo(HaveOccurred())
			Expect(verdict.Incomplete()).To(Equal(missing))
		},
		Entry("no pass", "```verdict\n{\"defects\": []}\n```", []string{session.VerdictFieldPass}),
		Entry("no defects list", "```verdict\n{\"pass\": true}\n```", []string{session.VerdictFieldDefects}),
		Entry("pass with a defect", "```verdict\n{\"pass\": true, \"defects\": [{\"statement\": \"s\", \"field\": \"f\", \"evidence\": [\"e\"]}]}\n```",
			[]string{session.VerdictFieldPass}),
		Entry("fail with no defect", "```verdict\n{\"pass\": false, \"defects\": []}\n```", []string{session.VerdictFieldPass}),
		Entry("a defect with no evidence", "```verdict\n{\"pass\": false, \"defects\": [{\"statement\": \"s\", \"field\": \"f\", \"evidence\": [\" \"]}]}\n```",
			[]string{"defects[0]"}),
	)

	It("reads a session's final message from its event log", func() {
		directory := filepath.Join(GinkgoT().TempDir(), siblingAssignment)
		writeLog(directory, turnRequested, verifierReply, toolOnly)
		records, err := session.ReadRecords(directory, func(_ *session.Record) bool { return true })
		Expect(err).NotTo(HaveOccurred())
		verdict, found, err := session.ParseVerdict(session.LastReply(records))
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(verdict.Incomplete()).To(BeEmpty())
	})
})
