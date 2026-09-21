package config

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
)

// envMap turns a map into a getenv func; missing keys return "".
func envMap(m map[string]string) func(name string) string {
	return func(k string) string { return m[k] }
}

// writeTemp writes contents to a temp file and returns its path.
func writeTemp(name, contents string) string {
	GinkgoHelper()
	p := filepath.Join(GinkgoT().TempDir(), name)
	Expect(os.WriteFile(p, []byte(contents), 0o600)).To(Succeed(), "writing temp config")
	return p
}

func boolPtr(b bool) *bool { return &b }

var _ = Describe("config Load", func() {
	// TestLoadDefaultsOnly
	It("loads the built-in defaults when no path or env is given", func() {
		cfg, err := Load("", envMap(nil))
		Expect(err).NotTo(HaveOccurred())

		Expect(cfg.Bind).To(Equal(":7717"))
		Expect(cfg.DataDir).To(Equal("/var/lib/warden"))
		Expect(cfg.LogLevel).To(Equal("info"))

		want := TimingConfig{
			HeartbeatInterval:  1 * time.Second,
			SuspectAfter:       5 * time.Second,
			DeadAfter:          15 * time.Second,
			ElectionTimeoutMin: 1500 * time.Millisecond,
			ElectionTimeoutMax: 3 * time.Second,
			RPCTimeout:         500 * time.Millisecond,
		}
		Expect(cfg.Timing).To(Equal(want))

		Expect(cfg.Watchdog.Cooldown).To(Equal(10 * time.Minute))
		Expect(cfg.Watchdog.MaxIncidents).To(Equal(100))
		Expect(cfg.Watchdog.NotifyRecovery).NotTo(BeNil())
		Expect(*cfg.Watchdog.NotifyRecovery).To(BeTrue())

		// default notify mode: no SMTP host => log
		Expect(cfg.Notify.Mode).To(Equal("log"))
		Expect(cfg.Notify.SMTPPort).To(Equal(587))
		Expect(cfg.Notify.SMTPTo).To(BeEmpty())

		// No fleet and no notification recipient is compiled into the binary;
		// both are per-deployment configuration and fail validation when the
		// deployment requires them.
		Expect(cfg.Peers).To(BeEmpty())
	})

	// TestLoadYAMLOverrides
	It("applies YAML overrides on top of defaults", func() {
		yamlBody := `
node_id: node-c
bind: "0.0.0.0:9999"
data_dir: /data/warden
log_level: debug
peers:
  - id: node-c
    addr: 10.0.0.1:7717
  - id: node-b
    addr: 10.0.0.2:7717
timing:
  heartbeat_interval: 2s
  dead_after: 20s
watchdog:
  cooldown: 30m
  max_incidents: 50
notify:
  mode: file
  file: /var/log/warden/incidents.jsonl
`
		p := writeTemp("warden.yaml", yamlBody)
		cfg, err := Load(p, envMap(nil))
		Expect(err).NotTo(HaveOccurred())

		Expect(cfg.NodeID).To(Equal("node-c"))
		Expect(cfg.Bind).To(Equal("0.0.0.0:9999"))
		Expect(cfg.DataDir).To(Equal("/data/warden"))
		Expect(cfg.LogLevel).To(Equal("debug"))
		Expect(cfg.Peers).To(HaveLen(2), "YAML supplies the whole member set")
		// partial timing block: overridden keys change, others keep defaults
		Expect(cfg.Timing.HeartbeatInterval).To(Equal(2 * time.Second))
		Expect(cfg.Timing.DeadAfter).To(Equal(20 * time.Second))
		Expect(cfg.Timing.SuspectAfter).To(Equal(5*time.Second), "default preserved")
		Expect(cfg.Timing.RPCTimeout).To(Equal(500*time.Millisecond), "default preserved")
		Expect(cfg.Watchdog.Cooldown).To(Equal(30 * time.Minute))
		Expect(cfg.Watchdog.MaxIncidents).To(Equal(50))
		Expect(cfg.Notify.Mode).To(Equal("file"))
		Expect(cfg.Notify.File).To(Equal("/var/log/warden/incidents.jsonl"))
	})

	// TestLoadEnvBeatsYAML
	It("lets env override YAML", func() {
		yamlBody := `
node_id: node-c
bind: "203.0.113.24:1111"
log_level: warn
timing:
  heartbeat_interval: 2s
notify:
  mode: log
`
		p := writeTemp("warden.yaml", yamlBody)
		env := envMap(map[string]string{
			envNodeID:            "node-b",
			envBind:              "203.0.113.28:2222",
			envLogLevel:          "error",
			envHeartbeatInterval: "3s",
			envNotifyMode:        "smtp",
			envSMTPHost:          "smtp.gmail.com",
			envSMTPFrom:          "warden@example.invalid",
		})
		cfg, err := Load(p, env)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.NodeID).To(Equal("node-b"))
		Expect(cfg.Bind).To(Equal("203.0.113.28:2222"))
		Expect(cfg.LogLevel).To(Equal("error"))
		Expect(cfg.Timing.HeartbeatInterval).To(Equal(3 * time.Second))
		Expect(cfg.Notify.Mode).To(Equal("smtp"))
	})

	// TestParsePeersEnv
	It("parses and trims WARDEN_PEERS", func() {
		env := envMap(map[string]string{
			envPeers: " a=10.0.0.1:7717 , b=10.0.0.2:7717 ,c=[::1]:7717 ",
		})
		cfg, err := Load("", env)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Peers).To(HaveLen(3))
		got := map[warden.NodeID]string{}
		for _, p := range cfg.Peers {
			got[p.ID] = p.Addr
		}
		Expect(got["a"]).To(Equal("10.0.0.1:7717"))
		Expect(got["b"]).To(Equal("10.0.0.2:7717"))
		Expect(got["c"]).To(Equal("[::1]:7717"))
	})

	// TestParsePeersMalformed
	DescribeTable("rejects malformed WARDEN_PEERS",
		func(val, wantSub string) {
			_, err := Load("", envMap(map[string]string{envPeers: val}))
			Expect(err).To(HaveOccurred(), "Load(%q) should error", val)
			Expect(err.Error()).To(ContainSubstring(wantSub))
		},
		Entry("no equals", "a10.0.0.1:7717", "missing '='"),
		Entry("empty id", "=10.0.0.1:7717", "empty id"),
		Entry("empty addr", "a=", "empty addr"),
		Entry("bad addr no port", "a=10.0.0.1", "host:port"),
		Entry("duplicate id", "a=10.0.0.1:7717,a=10.0.0.2:7717", "duplicate peer id"),
		Entry("only junk", "  ,  ", "parsed no peers"),
	)

	// TestDurationParseErrors
	DescribeTable("reports a bad env duration and names the offending key",
		func(k string) {
			_, err := Load("", envMap(map[string]string{k: "not-a-duration"}))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(k))
		},
		Entry(envHeartbeatInterval, envHeartbeatInterval),
		Entry(envSuspectAfter, envSuspectAfter),
		Entry(envDeadAfter, envDeadAfter),
		Entry(envElectionTimeoutMin, envElectionTimeoutMin),
		Entry(envElectionTimeoutMax, envElectionTimeoutMax),
		Entry(envRPCTimeout, envRPCTimeout),
		Entry(envCooldown, envCooldown),
	)

	// TestYAMLDurationParseError
	It("errors on a malformed YAML duration", func() {
		p := writeTemp("bad.yaml", "timing:\n  heartbeat_interval: 5 potatoes\n")
		_, err := Load(p, envMap(nil))
		Expect(err).To(HaveOccurred())
	})

	// TestSMTPPortAndBoolParseErrors
	It("errors on non-integer SMTP_PORT and non-bool NOTIFY_RECOVERY", func() {
		_, err := Load("", envMap(map[string]string{envSMTPPort: "abc"}))
		Expect(err).To(HaveOccurred(), "non-integer SMTP_PORT")
		_, err = Load("", envMap(map[string]string{envNotifyRecovery: "maybe"}))
		Expect(err).To(HaveOccurred(), "non-bool WARDEN_NOTIFY_RECOVERY")
	})

	// TestMissingExplicitFileErrors
	It("errors on a missing explicit file but not on an empty path", func() {
		_, err := Load("/nonexistent/warden/does-not-exist.yaml", envMap(nil))
		Expect(err).To(HaveOccurred(), "missing explicit file should error")
		// empty path must not error (defaults/env only)
		_, err = Load("", envMap(nil))
		Expect(err).NotTo(HaveOccurred())
	})

	// TestNotifyModeDefaulting
	DescribeTable("defaults the notify mode",
		func(env map[string]string, want string) {
			cfg, err := Load("", envMap(env))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Notify.Mode).To(Equal(want))
		},
		Entry("no host => log", map[string]string(nil), "log"),
		Entry("host set => smtp", map[string]string{envSMTPHost: "smtp.gmail.com"}, "smtp"),
		Entry("explicit mode wins over host", map[string]string{envSMTPHost: "smtp.gmail.com", envNotifyMode: "log"}, "log"),
		Entry("explicit file mode", map[string]string{envNotifyMode: "file"}, "file"),
	)

	// TestSMTPToSplitting
	It("splits and trims SMTP_TO, dropping empties", func() {
		cfg, err := Load("", envMap(map[string]string{
			envSMTPTo: " a@x.com , b@y.com ,, c@z.com ",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Notify.SMTPTo).To(Equal([]string{"a@x.com", "b@y.com", "c@z.com"}))
	})

	// TestNotifyRecoveryTriState
	It("resolves NotifyRecovery as a tri-state (unset/YAML/env)", func() {
		// unset => true
		cfg, err := Load("", envMap(nil))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Watchdog.NotifyRecovery).NotTo(BeNil())
		Expect(*cfg.Watchdog.NotifyRecovery).To(BeTrue(), "unset => true")

		// YAML false => false
		p := writeTemp("nr.yaml", "watchdog:\n  notify_recovery: false\n")
		cfg, err = Load(p, envMap(nil))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Watchdog.NotifyRecovery).NotTo(BeNil())
		Expect(*cfg.Watchdog.NotifyRecovery).To(BeFalse(), "YAML false => false")

		// env overrides YAML false => true
		cfg, err = Load(p, envMap(map[string]string{envNotifyRecovery: "true"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Watchdog.NotifyRecovery).NotTo(BeNil())
		Expect(*cfg.Watchdog.NotifyRecovery).To(BeTrue(), "env override => true")

		// env false with no YAML => false
		cfg, err = Load("", envMap(map[string]string{envNotifyRecovery: "false"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Watchdog.NotifyRecovery).NotTo(BeNil())
		Expect(*cfg.Watchdog.NotifyRecovery).To(BeFalse(), "env false => false")
	})

	// TestSelf
	It("resolves Self by node id and reports absence", func() {
		cfg, err := Load("", envMap(map[string]string{
			envNodeID: "node-b",
			envPeers:  testPeers,
		}))
		Expect(err).NotTo(HaveOccurred())
		self, ok := cfg.Self()
		Expect(ok).To(BeTrue(), "Self() not found for node-b")
		Expect(self.ID).To(Equal(warden.NodeID("node-b")))
		Expect(self.Addr).To(Equal("203.0.113.12:7717"))
		// unknown node id
		cfg.NodeID = "ghost"
		_, ok = cfg.Self()
		Expect(ok).To(BeFalse(), "Self() should be false for unknown node id")
	})
})

// testPeers is an explicit two-node member set. warden compiles in no fleet,
// so every test that needs a valid config supplies one the way a deployment
// must - through WARDEN_PEERS or a WARDEN_CONFIG file.
const testPeers = "node-a=203.0.113.11:7717,node-b=203.0.113.12:7717"

// baseValidConfig loads a minimal valid config with node-a as self.
func baseValidConfig() Config {
	cfg, _ := Load("", envMap(map[string]string{
		envNodeID: "node-a",
		envPeers:  testPeers,
	}))
	return cfg
}

var _ = Describe("config Validate", func() {
	// TestValidateHappyPath
	It("accepts a base valid config", func() {
		Expect(baseValidConfig().Validate()).To(Succeed())
	})

	// TestValidateRules
	DescribeTable("rejects each invalid config with a clear message",
		func(mutate func(cfg *Config), wantSub string) {
			cfg := baseValidConfig()
			mutate(&cfg)
			err := cfg.Validate()
			Expect(err).To(HaveOccurred(), "want error containing %q", wantSub)
			Expect(err.Error()).To(ContainSubstring(wantSub))
		},
		Entry("empty node id", func(c *Config) { c.NodeID = "" }, "node_id must not be empty"),
		Entry("node id not in peers", func(c *Config) { c.NodeID = "nope" }, "not present in peers"),
		Entry("empty peers", func(c *Config) { c.Peers = nil }, "peers must not be empty"),
		Entry("empty peer id", func(c *Config) {
			c.Peers = []warden.Node{{ID: "", Addr: "203.0.113.24:7717"}}
			c.NodeID = "node-a"
		}, "empty id"),
		Entry("duplicate peer id", func(c *Config) {
			c.Peers = []warden.Node{
				{ID: "node-a", Addr: "203.0.113.24:7717"},
				{ID: "node-a", Addr: "203.0.113.25:7717"},
			}
		}, "duplicate peer id"),
		Entry("bad peer addr", func(c *Config) {
			c.Peers = []warden.Node{{ID: "node-a", Addr: "not-host-port"}}
		}, "host:port"),
		Entry("nonpositive heartbeat", func(c *Config) { c.Timing.HeartbeatInterval = 0 }, "heartbeat_interval must be > 0"),
		Entry("nonpositive rpc timeout", func(c *Config) { c.Timing.RPCTimeout = -1 }, "rpc_timeout must be > 0"),
		Entry("nonpositive cooldown", func(c *Config) { c.Watchdog.Cooldown = 0 }, "cooldown must be > 0"),
		Entry("election min>=max", func(c *Config) {
			c.Timing.ElectionTimeoutMin = 5 * time.Second
			c.Timing.ElectionTimeoutMax = 3 * time.Second
		}, "election_timeout_min"),
		Entry("heartbeat>=suspect", func(c *Config) {
			c.Timing.HeartbeatInterval = 10 * time.Second // > suspect(5s)
		}, "suspect_after"),
		Entry("suspect>=dead", func(c *Config) {
			c.Timing.SuspectAfter = 20 * time.Second // > dead(15s)
		}, "dead_after"),
		Entry("smtp missing host", func(c *Config) {
			c.Notify.Mode = "smtp"
			c.Notify.SMTPHost = ""
			c.Notify.SMTPFrom = "a@b.com"
			c.Notify.SMTPTo = []string{"c@d.com"}
		}, "smtp_host"),
		Entry("smtp missing from", func(c *Config) {
			c.Notify.Mode = "smtp"
			c.Notify.SMTPHost = "smtp.gmail.com"
			c.Notify.SMTPFrom = ""
			c.Notify.SMTPTo = []string{"c@d.com"}
		}, "smtp_from"),
		Entry("smtp missing to", func(c *Config) {
			c.Notify.Mode = "smtp"
			c.Notify.SMTPHost = "smtp.gmail.com"
			c.Notify.SMTPFrom = "a@b.com"
			c.Notify.SMTPTo = nil
		}, "smtp_to"),
		Entry("file missing file", func(c *Config) {
			c.Notify.Mode = "file"
			c.Notify.File = ""
		}, "requires file"),
		Entry("invalid mode", func(c *Config) { c.Notify.Mode = "carrier-pigeon" }, "invalid"),
	)

	// TestValidateDoesNotRequirePassword
	It("does not require SMTP_PASS in smtp mode", func() {
		cfg, err := Load("", envMap(map[string]string{
			envNodeID:     "node-a",
			envPeers:      testPeers,
			envNotifyMode: "smtp",
			envSMTPHost:   "relay.internal",
			envSMTPFrom:   "warden@example.invalid",
			envSMTPTo:     "ops@example.invalid",
			// no SMTP_PASS
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Validate()).To(Succeed(), "password not required")
	})
})

var _ = Describe("config Redacted/String", func() {
	// TestRedactedStripsPassword
	It("strips the SMTP password from Redacted and String without mutating the original", func() {
		cfg, err := Load("", envMap(map[string]string{
			envNodeID:   "node-a",
			envPeers:    testPeers,
			envSMTPHost: "smtp.gmail.com",
			envSMTPFrom: "warden@example.invalid",
			envSMTPPass: "super-secret-app-password",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Notify.SMTPPass).To(Equal("super-secret-app-password"), "SMTP_PASS should be read from env")

		r := cfg.Redacted()
		Expect(r.Notify.SMTPPass).To(BeEmpty())
		// original untouched
		Expect(cfg.Notify.SMTPPass).NotTo(BeEmpty(), "Redacted must not mutate the original Config")
		// String never leaks the password
		s := cfg.String()
		Expect(s).NotTo(ContainSubstring("super-secret-app-password"), "String() leaked the password")
		Expect(s).To(ContainSubstring("<redacted>"))
	})
})
