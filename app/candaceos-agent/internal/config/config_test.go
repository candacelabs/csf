package config_test

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/app/candaceos-agent/internal/config"
)

func TestConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "candaceos-agent config suite")
}

var _ = Describe("Config", func() {
	It("uses loopback and production execution defaults", func() {
		cfg, err := config.Load(func(key string) string { return "" })
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Bind).To(Equal("127.0.0.1:8094"))
		Expect(cfg.DryRun).To(BeFalse())
		Expect(cfg.Workspace).To(Equal("/var/lib/candaceos/apps"))
		Expect(cfg.RevisionRoot).To(Equal("/var/lib/candaceos-agent/revisions"))
		Expect(cfg.RevisionMaxEntries).To(Equal(int64(128)))
		Expect(cfg.RevisionMaxBytes).To(Equal(int64(4 << 30)))
		Expect(cfg.SourceRemote).To(BeEmpty())
		Expect(cfg.SourceRepository).To(Equal("/var/lib/candaceos-agent/source.git"))
		Expect(cfg.SourceFetchTimeout).To(Equal(30 * time.Second))
	})

	It("parses explicit revision cache quotas", func() {
		values := map[string]string{
			"CANDACEOS_AGENT_REVISION_MAX_ENTRIES": "7",
			"CANDACEOS_AGENT_REVISION_MAX_BYTES":   "1048576",
		}
		cfg, err := config.Load(func(key string) string { return values[key] })
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.RevisionMaxEntries).To(Equal(int64(7)))
		Expect(cfg.RevisionMaxBytes).To(Equal(int64(1048576)))
	})

	It("parses an optional source remote and bounded fetch timeout", func() {
		values := map[string]string{
			"CANDACEOS_AGENT_SOURCE_REMOTE":        " origin ",
			"CANDACEOS_AGENT_SOURCE_REPOSITORY":    "/srv/candaceos/source.git",
			"CANDACEOS_AGENT_SOURCE_FETCH_TIMEOUT": "45s",
		}
		cfg, err := config.Load(func(key string) string { return values[key] })
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.SourceRemote).To(Equal("origin"))
		Expect(cfg.SourceRepository).To(Equal("/srv/candaceos/source.git"))
		Expect(cfg.SourceFetchTimeout).To(Equal(45 * time.Second))
	})

	DescribeTable("rejects invalid revision cache quotas",
		func(key, value string) {
			_, err := config.Load(func(candidate string) string {
				if candidate == key {
					return value
				}
				return ""
			})
			Expect(err).To(MatchError(ContainSubstring(key)))
		},
		Entry("zero entries", "CANDACEOS_AGENT_REVISION_MAX_ENTRIES", "0"),
		Entry("negative bytes", "CANDACEOS_AGENT_REVISION_MAX_BYTES", "-1"),
		Entry("non-numeric entries", "CANDACEOS_AGENT_REVISION_MAX_ENTRIES", "many"),
	)

	DescribeTable("rejects invalid source synchronization configuration",
		func(key, value string) {
			_, err := config.Load(func(candidate string) string {
				if candidate == key {
					return value
				}
				return ""
			})
			Expect(err).To(MatchError(ContainSubstring(key)))
		},
		Entry("zero fetch timeout", "CANDACEOS_AGENT_SOURCE_FETCH_TIMEOUT", "0s"),
		Entry("malformed fetch timeout", "CANDACEOS_AGENT_SOURCE_FETCH_TIMEOUT", "eventually"),
		Entry("option-like remote", "CANDACEOS_AGENT_SOURCE_REMOTE", "--upload-pack=evil"),
		Entry("relative repository", "CANDACEOS_AGENT_SOURCE_REPOSITORY", "source.git"),
	)

	It("requires a bearer token on a non-loopback bind", func() {
		values := map[string]string{"CANDACEOS_AGENT_BIND": "0.0.0.0:8094"}
		_, err := config.Load(func(key string) string { return values[key] })
		Expect(err).To(MatchError(ContainSubstring("CANDACEOS_AGENT_TOKEN is required")))

		values["CANDACEOS_AGENT_TOKEN"] = "secret"
		_, err = config.Load(func(key string) string { return values[key] })
		Expect(err).NotTo(HaveOccurred())
	})

	It("rejects malformed dry-run values", func() {
		_, err := config.Load(func(key string) string {
			if key == "CANDACEOS_AGENT_DRY_RUN" {
				return "sometimes"
			}
			return ""
		})
		Expect(err).To(HaveOccurred())
	})

	It("rejects node IDs that cannot satisfy the protobuf response contract", func() {
		_, err := config.Load(func(key string) string {
			if key == "CANDACEOS_AGENT_NODE_ID" {
				return "not a valid node"
			}
			return ""
		})
		Expect(err).To(MatchError(ContainSubstring("candace.candaceos.v1.AgentStatus.node_id")))
	})
})
