package config

import (
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("config discovery", func() {
	// TestExampleYAMLLoadsAndValidates guards that the shipped warden.example.yaml
	// stays loadable and deployable (its node_id is in the seed => static mode is
	// valid). Path is relative to this package dir.
	It("loads and validates the shipped warden.example.yaml", func() {
		const path = "../../../app/warden/warden.example.yaml"
		if _, err := os.Stat(path); err != nil {
			Skip(fmt.Sprintf("example yaml not found at %s: %v", path, err))
		}
		cfg, err := Load(path, envMap(nil))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Validate()).To(Succeed(), "example config does not validate")
		Expect(cfg.Discovery.Mode).To(Equal("static"), "fail-safe default")
		Expect(cfg.Discovery.ClusterID).To(Equal("candacenet"))
	})

	// TestDiscoveryDefaults
	It("applies discovery defaults", func() {
		cfg, err := Load("", envMap(nil))
		Expect(err).NotTo(HaveOccurred())
		d := cfg.Discovery
		Expect(d.Mode).To(Equal("static"))
		Expect(d.ClusterID).To(Equal("candacenet"))
		Expect(d.JoinStability).To(Equal(30 * time.Second))
		Expect(d.RemoveAfter).To(Equal(time.Duration(0)))
		Expect(d.FilePollInterval).To(Equal(2 * time.Second))
		Expect(d.Tailscale.Socket).To(Equal("/var/run/tailscale/tailscaled.sock"))
		Expect(d.Tailscale.PollInterval).To(Equal(15 * time.Second))
		Expect(d.Tailscale.Tag).To(BeEmpty())
		Expect(d.Tailscale.HostPattern).To(BeEmpty())
		Expect(cfg.Advertise).To(BeEmpty())
		// AdvertiseAddr falls back to Bind when unset.
		Expect(cfg.AdvertiseAddr()).To(Equal(cfg.Bind))
	})

	// TestDiscoveryYAMLOverrides
	It("applies discovery YAML overrides", func() {
		yamlBody := `
node_id: node-c
discovery:
  mode: tailscale
  cluster_id: mycluster
  join_stability: 45s
  remove_after: 10m
  file: /var/lib/warden/roster.json
  file_poll_interval: 3s
  tailscale:
    socket: /run/tailscale/ts.sock
    tag: tag:mywarden
    host_pattern: "warden-.*"
    poll_interval: 20s
advertise_addr: 100.64.0.29:7717
`
		p := writeTemp("warden.yaml", yamlBody)
		cfg, err := Load(p, envMap(nil))
		Expect(err).NotTo(HaveOccurred())
		d := cfg.Discovery
		Expect(d.Mode).To(Equal("tailscale"))
		Expect(d.ClusterID).To(Equal("mycluster"))
		Expect(d.JoinStability).To(Equal(45 * time.Second))
		Expect(d.RemoveAfter).To(Equal(10 * time.Minute))
		Expect(d.File).To(Equal("/var/lib/warden/roster.json"))
		Expect(d.FilePollInterval).To(Equal(3 * time.Second))
		Expect(d.Tailscale.Socket).To(Equal("/run/tailscale/ts.sock"))
		Expect(d.Tailscale.Tag).To(Equal("tag:mywarden"))
		Expect(d.Tailscale.HostPattern).To(Equal("warden-.*"))
		Expect(d.Tailscale.PollInterval).To(Equal(20 * time.Second))
		Expect(cfg.Advertise).To(Equal("100.64.0.29:7717"))
		Expect(cfg.AdvertiseAddr()).To(Equal("100.64.0.29:7717"))
	})

	// TestDiscoveryPartialTailscaleBlock
	It("keeps tailscale defaults for absent keys in a partial block", func() {
		p := writeTemp("warden.yaml", "discovery:\n  tailscale:\n    tag: tag:x\n")
		cfg, err := Load(p, envMap(nil))
		Expect(err).NotTo(HaveOccurred())
		d := cfg.Discovery.Tailscale
		Expect(d.Tag).To(Equal("tag:x"))
		Expect(d.Socket).To(Equal("/var/run/tailscale/tailscaled.sock"), "default preserved")
		Expect(d.PollInterval).To(Equal(15*time.Second), "default preserved")
		// A partial discovery block leaves mode at its default too.
		Expect(cfg.Discovery.Mode).To(Equal("static"), "default preserved")
	})

	// TestDiscoveryEnvOverrides
	It("applies discovery env overrides", func() {
		env := envMap(map[string]string{
			envDiscoveryMode:    "file",
			envClusterID:        "envcluster",
			envJoinStability:    "12s",
			envRemoveAfter:      "5m",
			envDiscoveryFile:    "/tmp/roster.json",
			envFilePollInterval: "4s",
			envTSSocket:         "/tmp/ts.sock",
			envTSTag:            "tag:env",
			envTSHostPattern:    "host-.*",
			envTSPollInterval:   "25s",
			envAdvertiseAddr:    "100.64.0.25:7717",
		})
		cfg, err := Load("", env)
		Expect(err).NotTo(HaveOccurred())
		d := cfg.Discovery
		Expect(d.Mode).To(Equal("file"))
		Expect(d.ClusterID).To(Equal("envcluster"))
		Expect(d.JoinStability).To(Equal(12 * time.Second))
		Expect(d.RemoveAfter).To(Equal(5 * time.Minute))
		Expect(d.File).To(Equal("/tmp/roster.json"))
		Expect(d.FilePollInterval).To(Equal(4 * time.Second))
		Expect(d.Tailscale.Socket).To(Equal("/tmp/ts.sock"))
		Expect(d.Tailscale.Tag).To(Equal("tag:env"))
		Expect(d.Tailscale.HostPattern).To(Equal("host-.*"))
		Expect(d.Tailscale.PollInterval).To(Equal(25 * time.Second))
		Expect(cfg.Advertise).To(Equal("100.64.0.25:7717"))
	})

	// TestDiscoveryEnvBeatsYAML
	It("lets env override YAML for discovery keys", func() {
		p := writeTemp("warden.yaml", "discovery:\n  mode: tailscale\n  cluster_id: fromyaml\n")
		cfg, err := Load(p, envMap(map[string]string{
			envDiscoveryMode: "file",
			envClusterID:     "fromenv",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Discovery.Mode).To(Equal("file"), "env wins")
		Expect(cfg.Discovery.ClusterID).To(Equal("fromenv"), "env wins")
	})

	// TestDiscoveryDurationParseErrors
	DescribeTable("reports a bad discovery env duration and names the key",
		func(k string) {
			_, err := Load("", envMap(map[string]string{k: "not-a-duration"}))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(k))
		},
		Entry(envJoinStability, envJoinStability),
		Entry(envRemoveAfter, envRemoveAfter),
		Entry(envFilePollInterval, envFilePollInterval),
		Entry(envTSPollInterval, envTSPollInterval),
	)

	// TestDiscoveryYAMLDurationParseError
	It("errors on malformed discovery/tailscale YAML durations", func() {
		p := writeTemp("bad.yaml", "discovery:\n  join_stability: 5 bananas\n")
		_, err := Load(p, envMap(nil))
		Expect(err).To(HaveOccurred(), "malformed discovery YAML duration")
		p = writeTemp("bad2.yaml", "discovery:\n  tailscale:\n    poll_interval: 9 bananas\n")
		_, err = Load(p, envMap(nil))
		Expect(err).To(HaveOccurred(), "malformed tailscale.poll_interval")
	})

	// TestValidateDiscoveryRules
	DescribeTable("rejects each invalid discovery config with a clear message",
		func(mutate func(cfg *Config), wantSub string) {
			cfg := baseValidConfig()
			mutate(&cfg)
			err := cfg.Validate()
			Expect(err).To(HaveOccurred(), "want error containing %q", wantSub)
			Expect(err.Error()).To(ContainSubstring(wantSub))
		},
		Entry("invalid mode", func(c *Config) { c.Discovery.Mode = "carrier-pigeon" }, "discovery.mode"),
		Entry("empty cluster_id", func(c *Config) { c.Discovery.ClusterID = "" }, "cluster_id must not be empty"),
		Entry("join_stability zero", func(c *Config) { c.Discovery.JoinStability = 0 }, "join_stability must be > 0"),
		Entry("remove_after negative", func(c *Config) { c.Discovery.RemoveAfter = -1 }, "remove_after must be >= 0"),
		Entry("ts poll zero", func(c *Config) { c.Discovery.Tailscale.PollInterval = 0 }, "tailscale.poll_interval must be > 0"),
		Entry("file poll zero", func(c *Config) { c.Discovery.FilePollInterval = 0 }, "file_poll_interval must be > 0"),
		Entry("tailscale needs tag or pattern", func(c *Config) {
			c.Discovery.Mode = "tailscale"
			c.Discovery.Tailscale.Tag = ""
			c.Discovery.Tailscale.HostPattern = ""
		}, "requires tailscale.tag or tailscale.host_pattern"),
		Entry("file needs file path", func(c *Config) {
			c.Discovery.Mode = "file"
			c.Discovery.File = ""
		}, "requires discovery.file"),
		Entry("bad host_pattern", func(c *Config) {
			c.Discovery.Mode = "tailscale"
			c.Discovery.Tailscale.HostPattern = "("
		}, "not a valid RE2 pattern"),
	)

	// TestValidateTailscaleModeHappy
	It("validates a tailscale-mode config with a tag when self is in peers", func() {
		cfg, err := Load("", envMap(map[string]string{
			envNodeID:        "node-a",
			envPeers:         testPeers,
			envDiscoveryMode: "tailscale",
			envTSTag:         "tag:candacenet",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Validate()).To(Succeed())
	})

	// TestValidateJoinerWithAdvertiseAddr
	It("accepts a joiner (self not in peers) with a routable advertise_addr", func() {
		cfg, err := Load("", envMap(map[string]string{
			envNodeID:        "newnode", // deliberately not in the peer seed
			envPeers:         testPeers,
			envDiscoveryMode: "tailscale",
			envTSTag:         "tag:candacenet",
			envAdvertiseAddr: "203.0.113.55:7717",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Validate()).To(Succeed(), "joiner with advertise_addr is valid")
	})

	// TestValidateJoinerMissingAdvertiseAddr
	It("rejects a joiner with no routable advertise_addr", func() {
		cfg, err := Load("", envMap(map[string]string{
			envNodeID:        "newnode",
			envPeers:         testPeers,
			envDiscoveryMode: "file",
			envDiscoveryFile: "/tmp/roster.json",
			// no advertise_addr; Bind default ":7717" has an empty host
		}))
		Expect(err).NotTo(HaveOccurred())
		err = cfg.Validate()
		Expect(err).To(HaveOccurred(), "joiner without routable advertise_addr")
		Expect(err.Error()).To(ContainSubstring("advertise_addr"))
	})

	// TestValidateJoinerWildcardAdvertiseRejected
	It("rejects 0.0.0.0 as an advertise host", func() {
		cfg, err := Load("", envMap(map[string]string{
			envNodeID:        "newnode",
			envPeers:         testPeers,
			envDiscoveryMode: "tailscale",
			envTSTag:         "tag:candacenet",
			envAdvertiseAddr: "0.0.0.0:7717",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Validate()).NotTo(Succeed(), "0.0.0.0 advertise host")
	})

	// TestValidateStaticSelfMustBeInPeers
	It("in static mode requires node_id to be in peers", func() {
		cfg, err := Load("", envMap(map[string]string{
			envNodeID: "ghost", // static mode default, not in peers
			envPeers:  testPeers,
		}))
		Expect(err).NotTo(HaveOccurred())
		err = cfg.Validate()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not present in peers"))
	})

	// TestAdvertiseAddr
	It("falls back AdvertiseAddr to Bind, else uses the explicit addr", func() {
		cfg := Config{Bind: ":7717"}
		Expect(cfg.AdvertiseAddr()).To(Equal(":7717"), "falls back to Bind")
		cfg.Advertise = "100.64.0.25:7717"
		Expect(cfg.AdvertiseAddr()).To(Equal("100.64.0.25:7717"))
	})

	// TestStringIncludesDiscovery
	It("includes the discovery summary in String()", func() {
		cfg, err := Load("", envMap(map[string]string{
			envNodeID: "node-a",
			envPeers:  testPeers,
		}))
		Expect(err).NotTo(HaveOccurred())
		s := cfg.String()
		for _, want := range []string{"discovery{", "mode=static", "cluster_id=candacenet", "advertise_addr="} {
			Expect(s).To(ContainSubstring(want))
		}
	})
})
