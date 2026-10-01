package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/candacelabs/csf/pkg/privatefile"
	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
	"github.com/candacelabs/csf/services/warden"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	maxEmailProvenanceBytes = 1 << 20
	maxEmailProvenanceLinks = 64
	buildVersionDevel       = "(devel)"
	buildRevisionSetting    = "vcs.revision"

	unavailableContainerObserver = "container observer unavailable"
	unavailableCSFVersion        = "csf version unavailable"
	unavailableSourceRevision    = "source revision unavailable"
	unavailableHostname          = "hostname unavailable"
)

// wardenEmailProvenance supplies host-owned notification facts. It never
// observes containers or sessions: a Warden alert reports a local watchdog
// observation, not a claim about the host's other workloads.
type wardenEmailProvenance struct {
	reportingNode  *provenancev1.NodeIdentity
	csfVersion     string
	sourceRevision string
	links          []*provenancev1.EvidenceLink
	unavailable    []string
}

// newWardenEmailProvenance obtains host identity and build metadata once at
// process construction. Snapshot only returns immutable copies of those facts.
func newWardenEmailProvenance(self warden.Node, base *provenancev1.ReceiptMetadata) *wardenEmailProvenance {
	hostname, hostnameErr := os.Hostname()
	buildInfo, buildInfoOK := debug.ReadBuildInfo()
	return newWardenEmailProvenanceFromFacts(self, hostname, hostnameErr, buildInfo, buildInfoOK, base)
}

func newWardenEmailProvenanceFromFacts(
	self warden.Node,
	hostname string,
	hostnameErr error,
	buildInfo *debug.BuildInfo,
	buildInfoOK bool,
	base *provenancev1.ReceiptMetadata,
) *wardenEmailProvenance {
	csfVersion, sourceRevision := buildProvenanceMetadata(buildInfo, buildInfoOK)
	if csfVersion == "" && base != nil {
		csfVersion = base.GetCsfVersion()
	}
	if sourceRevision == "" && base != nil {
		sourceRevision = base.GetSourceRevision()
	}

	unavailable := []string{unavailableContainerObserver}
	if csfVersion == "" {
		unavailable = append(unavailable, unavailableCSFVersion)
	}
	if sourceRevision == "" {
		unavailable = append(unavailable, unavailableSourceRevision)
	}
	if hostnameErr != nil {
		unavailable = append(unavailable, unavailableHostname)
	}

	provenance := &wardenEmailProvenance{
		reportingNode: &provenancev1.NodeIdentity{
			NodeId:   string(self.ID),
			Hostname: hostname,
			Address:  self.Addr,
		},
		csfVersion:     csfVersion,
		sourceRevision: sourceRevision,
		unavailable:    unavailable,
	}
	if base != nil {
		provenance.links = make([]*provenancev1.EvidenceLink, 0, len(base.GetLinks()))
		for _, link := range base.GetLinks() {
			provenance.links = append(provenance.links, proto.CloneOf(link))
		}
	}
	return provenance
}

func buildProvenanceMetadata(buildInfo *debug.BuildInfo, buildInfoOK bool) (string, string) {
	if !buildInfoOK || buildInfo == nil {
		return "", ""
	}
	csfVersion := buildInfo.Main.Version
	if csfVersion == buildVersionDevel {
		csfVersion = ""
	}
	for _, setting := range buildInfo.Settings {
		if setting.Key == buildRevisionSetting && setting.Value != "" {
			return csfVersion, setting.Value
		}
	}
	return csfVersion, ""
}

// Snapshot returns only facts Warden owns: actual runtime node identity,
// build/deployment identity, and explicitly configured evidence links.
func (provenance *wardenEmailProvenance) Snapshot(ctx context.Context) (*provenancev1.ReceiptMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	metadata := &provenancev1.ReceiptMetadata{
		ReportingNode:  proto.CloneOf(provenance.reportingNode),
		CsfVersion:     provenance.csfVersion,
		SourceRevision: provenance.sourceRevision,
		Unavailable:    append([]string(nil), provenance.unavailable...),
	}
	for _, link := range provenance.links {
		metadata.Links = append(metadata.Links, proto.CloneOf(link))
	}
	return metadata, nil
}

// loadEmailProvenance reads an optional private protobuf-JSON deployment file.
// Only its version, revision, and evidence links are subsequently used; runtime
// node identity and all session/container observations are intentionally ignored.
func loadEmailProvenance(path string) (*provenancev1.ReceiptMetadata, error) {
	if path == "" {
		return nil, nil
	}
	content, err := privatefile.Read(path, maxEmailProvenanceBytes)
	if err != nil {
		return nil, err
	}
	base := &provenancev1.ReceiptMetadata{}
	if err := protojson.Unmarshal(content, base); err != nil {
		return nil, fmt.Errorf("invalid email provenance protobuf JSON: %w", err)
	}
	if err := validateEmailProvenanceBase(base); err != nil {
		return nil, err
	}
	return base, nil
}

func validateEmailProvenanceBase(base *provenancev1.ReceiptMetadata) error {
	if len(base.GetLinks()) > maxEmailProvenanceLinks {
		return fmt.Errorf("email provenance evidence links exceed %d", maxEmailProvenanceLinks)
	}
	validation := &provenancev1.ReceiptMetadata{
		SchemaVersion:  1,
		ReceiptId:      "deployment-provenance",
		CsfVersion:     base.GetCsfVersion(),
		SourceRevision: base.GetSourceRevision(),
	}
	if err := provenancev1.ValidateReceiptMetadata(validation); err != nil {
		return fmt.Errorf("invalid email provenance metadata: %w", err)
	}
	for _, link := range base.GetLinks() {
		if err := provenancev1.ValidateEvidenceLink(link); err != nil {
			return fmt.Errorf("invalid email provenance evidence link: %w", err)
		}
	}
	return nil
}
