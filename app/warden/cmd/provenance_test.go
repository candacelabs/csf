package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
	"github.com/candacelabs/csf/services/warden"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestWardenEmailProvenance(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "warden email provenance suite")
}

var _ = Describe("Warden email provenance", func() {
	It("keeps runtime node identity while limiting static provenance to deployment facts", func() {
		path := filepath.Join(GinkgoT().TempDir(), "provenance.json")
		static := &provenancev1.ReceiptMetadata{
			ReportingNode:  &provenancev1.NodeIdentity{NodeId: "forged-node", Hostname: "forged-host"},
			CsfVersion:     "configured-version",
			SourceRevision: "configured-revision",
			Sessions: []*provenancev1.SessionReference{{
				Provider: "forged", SessionId: "forged-session",
			}},
			Containers: []*provenancev1.ContainerObservation{{Name: "forged-container"}},
			Links: []*provenancev1.EvidenceLink{{
				Label: "release evidence", Url: "https://example.invalid/release",
			}},
		}
		content, err := protojson.Marshal(static)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, content, 0o600)).To(Succeed())

		base, err := loadEmailProvenance(path)
		Expect(err).NotTo(HaveOccurred())
		provenance := newWardenEmailProvenanceFromFacts(
			warden.Node{ID: "warden-a", Addr: "203.0.113.8:7717"},
			"warden-host", nil,
			&debug.BuildInfo{
				Main:     debug.Module{Version: "v1.2.3"},
				Settings: []debug.BuildSetting{{Key: buildRevisionSetting, Value: "actual-revision"}},
			}, true, base,
		)

		metadata, err := provenance.Snapshot(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(metadata.GetReportingNode().GetNodeId()).To(Equal("warden-a"))
		Expect(metadata.GetReportingNode().GetHostname()).To(Equal("warden-host"))
		Expect(metadata.GetReportingNode().GetAddress()).To(Equal("203.0.113.8:7717"))
		Expect(metadata.GetSessions()).To(BeEmpty())
		Expect(metadata.GetContainers()).To(BeEmpty())
		Expect(metadata.GetCsfVersion()).To(Equal("v1.2.3"))
		Expect(metadata.GetSourceRevision()).To(Equal("actual-revision"))
		Expect(metadata.GetLinks()).To(HaveLen(1))
		Expect(metadata.GetUnavailable()).To(ContainElement(unavailableContainerObserver))
	})

	It("marks unavailable build facts instead of inferring them from main.version", func() {
		provenance := newWardenEmailProvenanceFromFacts(
			warden.Node{ID: "warden-a"}, "warden-host", nil, nil, false, nil,
		)

		metadata, err := provenance.Snapshot(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(metadata.GetCsfVersion()).To(BeEmpty())
		Expect(metadata.GetSourceRevision()).To(BeEmpty())
		Expect(metadata.GetUnavailable()).To(ConsistOf(
			unavailableContainerObserver,
			unavailableCSFVersion,
			unavailableSourceRevision,
		))
	})

	It("rejects deployment provenance with more evidence links than a receipt accepts", func() {
		base := &provenancev1.ReceiptMetadata{}
		for range maxEmailProvenanceLinks + 1 {
			base.Links = append(base.Links, &provenancev1.EvidenceLink{
				Label: "release evidence", Url: "https://example.invalid/release",
			})
		}

		Expect(validateEmailProvenanceBase(base)).To(MatchError(
			"email provenance evidence links exceed 64",
		))
	})
})
