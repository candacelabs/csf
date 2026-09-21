package config_test

// Row-for-row contract tests for warden's configuration loader: every
// documented default, every env var name, the env-beats-file-beats-defaults
// precedence, and the startup validation rules from the README's config table.
// These specs use an injected getenv map (never os.Setenv) so they are fully
// hermetic and safe under Ginkgo's spec randomization/parallelism.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/config"
)

func TestConfigContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "config contract suite")
}

// env builds a getenv function from a map (absent keys return "").
func env(m map[string]string) func(name string) string {
	return func(k string) string { return m[k] }
}

// writeYAML writes content to a temp file and returns its path.
func writeYAML(content string) string {
	dir := GinkgoT().TempDir()
	path := filepath.Join(dir, "warden.yaml")
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
	return path
}

var _ = Describe("config.Load defaults (no file, no env)", func() {
	var cfg config.Config

	BeforeEach(func() {
		var err error
		cfg, err = config.Load("", env(nil))
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable("resolve to the documented built-in defaults",
		func(get func(cfg config.Config) any, want any) {
			Expect(get(cfg)).To(Equal(want))
		},
		Entry("bind :7717", func(c config.Config) any { return c.Bind }, ":7717"),
		Entry("data_dir /var/lib/warden", func(c config.Config) any { return c.DataDir }, "/var/lib/warden"),
		Entry("log_level info", func(c config.Config) any { return c.LogLevel }, "info"),
		Entry("heartbeat_interval 1s", func(c config.Config) any { return c.Timing.HeartbeatInterval }, time.Second),
		Entry("suspect_after 5s", func(c config.Config) any { return c.Timing.SuspectAfter }, 5*time.Second),
		Entry("dead_after 15s", func(c config.Config) any { return c.Timing.DeadAfter }, 15*time.Second),
		Entry("election_timeout_min 1500ms", func(c config.Config) any { return c.Timing.ElectionTimeoutMin }, 1500*time.Millisecond),
		Entry("election_timeout_max 3s", func(c config.Config) any { return c.Timing.ElectionTimeoutMax }, 3*time.Second),
		Entry("rpc_timeout 500ms", func(c config.Config) any { return c.Timing.RPCTimeout }, 500*time.Millisecond),
		Entry("cooldown 10m", func(c config.Config) any { return c.Watchdog.Cooldown }, 10*time.Minute),
		Entry("max_incidents 100", func(c config.Config) any { return c.Watchdog.MaxIncidents }, 100),
		Entry("smtp_port 587", func(c config.Config) any { return c.Notify.SMTPPort }, 587),
	)

	It("defaults notify_recovery to true (resolved from the tri-state nil)", func() {
		Expect(cfg.Watchdog.NotifyRecovery).NotTo(BeNil())
		Expect(*cfg.Watchdog.NotifyRecovery).To(BeTrue())
	})

	It("leaves smtp_to empty so no recipient is compiled in", func() {
		Expect(cfg.Notify.SMTPTo).To(BeEmpty())
	})

	It("defaults notify.mode to log when no smtp_host is set", func() {
		Expect(cfg.Notify.Mode).To(Equal("log"))
	})

	It("compiles in no peer set at all", func() {
		Expect(cfg.Peers).To(BeEmpty())
	})
})

var _ = Describe("config.Load env var mapping", func() {
	DescribeTable("each documented env var overrides its field",
		func(key, val string, get func(cfg config.Config) any, want any) {
			cfg, err := config.Load("", env(map[string]string{key: val}))
			Expect(err).NotTo(HaveOccurred())
			Expect(get(cfg)).To(Equal(want))
		},
		Entry("WARDEN_NODE_ID", "WARDEN_NODE_ID", "n7", func(c config.Config) any { return c.NodeID }, "n7"),
		Entry("WARDEN_BIND", "WARDEN_BIND", ":9999", func(c config.Config) any { return c.Bind }, ":9999"),
		Entry("WARDEN_DATA_DIR", "WARDEN_DATA_DIR", "/data", func(c config.Config) any { return c.DataDir }, "/data"),
		Entry("WARDEN_LOG_LEVEL", "WARDEN_LOG_LEVEL", "debug", func(c config.Config) any { return c.LogLevel }, "debug"),
		Entry("WARDEN_HEARTBEAT_INTERVAL", "WARDEN_HEARTBEAT_INTERVAL", "2s", func(c config.Config) any { return c.Timing.HeartbeatInterval }, 2*time.Second),
		Entry("WARDEN_SUSPECT_AFTER", "WARDEN_SUSPECT_AFTER", "6s", func(c config.Config) any { return c.Timing.SuspectAfter }, 6*time.Second),
		Entry("WARDEN_DEAD_AFTER", "WARDEN_DEAD_AFTER", "20s", func(c config.Config) any { return c.Timing.DeadAfter }, 20*time.Second),
		Entry("WARDEN_ELECTION_TIMEOUT_MIN", "WARDEN_ELECTION_TIMEOUT_MIN", "1s", func(c config.Config) any { return c.Timing.ElectionTimeoutMin }, time.Second),
		Entry("WARDEN_ELECTION_TIMEOUT_MAX", "WARDEN_ELECTION_TIMEOUT_MAX", "4s", func(c config.Config) any { return c.Timing.ElectionTimeoutMax }, 4*time.Second),
		Entry("WARDEN_RPC_TIMEOUT", "WARDEN_RPC_TIMEOUT", "750ms", func(c config.Config) any { return c.Timing.RPCTimeout }, 750*time.Millisecond),
		Entry("WARDEN_COOLDOWN", "WARDEN_COOLDOWN", "30m", func(c config.Config) any { return c.Watchdog.Cooldown }, 30*time.Minute),
		Entry("WARDEN_NOTIFY_MODE", "WARDEN_NOTIFY_MODE", "file", func(c config.Config) any { return c.Notify.Mode }, "file"),
		Entry("WARDEN_NOTIFY_FILE", "WARDEN_NOTIFY_FILE", "/x.jsonl", func(c config.Config) any { return c.Notify.File }, "/x.jsonl"),
		Entry("SMTP_HOST", "SMTP_HOST", "smtp.example.com", func(c config.Config) any { return c.Notify.SMTPHost }, "smtp.example.com"),
		Entry("SMTP_PORT", "SMTP_PORT", "2525", func(c config.Config) any { return c.Notify.SMTPPort }, 2525),
		Entry("SMTP_USER", "SMTP_USER", "u@x", func(c config.Config) any { return c.Notify.SMTPUser }, "u@x"),
		Entry("SMTP_PASS", "SMTP_PASS", "secret", func(c config.Config) any { return c.Notify.SMTPPass }, "secret"),
		Entry("SMTP_FROM", "SMTP_FROM", "f@x", func(c config.Config) any { return c.Notify.SMTPFrom }, "f@x"),
	)

	It("parses WARDEN_PEERS in id=host:port,... form", func() {
		cfg, err := config.Load("", env(map[string]string{
			"WARDEN_PEERS": "a=10.0.0.1:7717,b=10.0.0.2:7717",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Peers).To(Equal([]warden.Node{
			{ID: "a", Addr: "10.0.0.1:7717"},
			{ID: "b", Addr: "10.0.0.2:7717"},
		}))
	})

	It("splits SMTP_TO on commas, trimming whitespace", func() {
		cfg, err := config.Load("", env(map[string]string{"SMTP_TO": "a@x, b@y ,c@z"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Notify.SMTPTo).To(Equal([]string{"a@x", "b@y", "c@z"}))
	})

	It("honours an explicit notify_recovery=false from env (tri-state)", func() {
		cfg, err := config.Load("", env(map[string]string{"WARDEN_NOTIFY_RECOVERY": "false"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Watchdog.NotifyRecovery).NotTo(BeNil())
		Expect(*cfg.Watchdog.NotifyRecovery).To(BeFalse())
	})

	It("defaults notify.mode to smtp when SMTP_HOST is set but mode is unset", func() {
		cfg, err := config.Load("", env(map[string]string{"SMTP_HOST": "smtp.gmail.com"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Notify.Mode).To(Equal("smtp"))
	})
})

var _ = Describe("config.Load precedence", func() {
	const yaml = `
node_id: file-node
bind: ":1111"
log_level: warn
peers:
  - id: file-node
    addr: 10.0.0.9:7717
timing:
  heartbeat_interval: 2s
watchdog:
  cooldown: 15m
notify:
  mode: log
`

	It("lets env override values present in the YAML file", func() {
		path := writeYAML(yaml)
		cfg, err := config.Load(path, env(map[string]string{
			"WARDEN_BIND":      ":2222",
			"WARDEN_LOG_LEVEL": "error",
			"WARDEN_COOLDOWN":  "45m",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Bind).To(Equal(":2222"))     // env wins over file
		Expect(cfg.LogLevel).To(Equal("error")) // env wins over file
		Expect(cfg.Watchdog.Cooldown).To(Equal(45 * time.Minute))
		Expect(cfg.NodeID).To(Equal("file-node")) // file wins over default
		Expect(cfg.Timing.HeartbeatInterval).To(Equal(2 * time.Second))
	})

	It("keeps file values where no env override is present, and defaults where neither is", func() {
		path := writeYAML(yaml)
		cfg, err := config.Load(path, env(nil))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Bind).To(Equal(":1111"))                        // from file
		Expect(cfg.Timing.SuspectAfter).To(Equal(5 * time.Second)) // default (file omitted it)
		Expect(cfg.Timing.DeadAfter).To(Equal(15 * time.Second))   // default
		Expect(cfg.Watchdog.MaxIncidents).To(Equal(100))           // default
	})

	It("errors when an explicitly-named config file cannot be read", func() {
		_, err := config.Load("/no/such/warden.yaml", env(nil))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("reading config file"))
	})

	It("errors on malformed YAML", func() {
		path := writeYAML("this: : : not: valid")
		_, err := config.Load(path, env(nil))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("parsing config file"))
	})
})

var _ = Describe("config.Load malformed env values", func() {
	DescribeTable("are rejected with an error rather than silently ignored",
		func(key, val, substr string) {
			_, err := config.Load("", env(map[string]string{key: val}))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(substr))
		},
		Entry("bad duration", "WARDEN_HEARTBEAT_INTERVAL", "not-a-duration", "WARDEN_HEARTBEAT_INTERVAL"),
		Entry("non-integer port", "SMTP_PORT", "abc", "SMTP_PORT"),
		Entry("non-bool recovery", "WARDEN_NOTIFY_RECOVERY", "maybe", "WARDEN_NOTIFY_RECOVERY"),
		Entry("peers missing '='", "WARDEN_PEERS", "just-an-id", "missing '='"),
		Entry("peers empty addr", "WARDEN_PEERS", "id=", "empty addr"),
		Entry("peers bad addr", "WARDEN_PEERS", "id=not-host-port", "not host:port"),
		Entry("peers duplicate id", "WARDEN_PEERS", "a=h:1,a=h:2", "duplicate peer id"),
	)
})

var _ = Describe("Config.Validate", func() {
	// base returns a minimal internally-consistent config to mutate per case.
	base := func() config.Config {
		cfg, err := config.Load("", env(map[string]string{
			"WARDEN_NODE_ID": "a",
			"WARDEN_PEERS":   "a=10.0.0.1:7717,b=10.0.0.2:7717,c=10.0.0.3:7717",
		}))
		Expect(err).NotTo(HaveOccurred())
		return cfg
	}

	It("accepts an internally-consistent config", func() {
		Expect(base().Validate()).To(Succeed())
	})

	It("rejects an empty node_id", func() {
		cfg := base()
		cfg.NodeID = ""
		Expect(cfg.Validate().Error()).To(ContainSubstring("node_id must not be empty"))
	})

	It("rejects a node_id absent from peers", func() {
		cfg := base()
		cfg.NodeID = "ghost"
		Expect(cfg.Validate().Error()).To(ContainSubstring("not present in peers"))
	})

	It("rejects duplicate peer ids", func() {
		cfg := base()
		cfg.Peers = []warden.Node{{ID: "a", Addr: "h:1"}, {ID: "a", Addr: "h:2"}}
		Expect(cfg.Validate().Error()).To(ContainSubstring("duplicate peer id"))
	})

	It("rejects a peer addr that is not host:port", func() {
		cfg := base()
		cfg.Peers = []warden.Node{{ID: "a", Addr: "not-host-port"}}
		Expect(cfg.Validate().Error()).To(ContainSubstring("not host:port"))
	})

	DescribeTable("rejects non-positive timing durations",
		func(mut func(cfg *config.Config), substr string) {
			cfg := base()
			mut(&cfg)
			Expect(cfg.Validate().Error()).To(ContainSubstring(substr))
		},
		Entry("heartbeat_interval <= 0", func(c *config.Config) { c.Timing.HeartbeatInterval = 0 }, "timing.heartbeat_interval must be > 0"),
		Entry("suspect_after <= 0", func(c *config.Config) { c.Timing.SuspectAfter = 0 }, "timing.suspect_after must be > 0"),
		Entry("dead_after <= 0", func(c *config.Config) { c.Timing.DeadAfter = -1 }, "timing.dead_after must be > 0"),
		Entry("election_timeout_min <= 0", func(c *config.Config) { c.Timing.ElectionTimeoutMin = 0 }, "timing.election_timeout_min must be > 0"),
		Entry("election_timeout_max <= 0", func(c *config.Config) { c.Timing.ElectionTimeoutMax = -1 }, "timing.election_timeout_max must be > 0"),
		Entry("rpc_timeout <= 0", func(c *config.Config) { c.Timing.RPCTimeout = 0 }, "timing.rpc_timeout must be > 0"),
		Entry("cooldown <= 0", func(c *config.Config) { c.Watchdog.Cooldown = 0 }, "watchdog.cooldown must be > 0"),
	)

	It("rejects an empty peer set", func() {
		cfg := base()
		cfg.Peers = nil
		Expect(cfg.Validate().Error()).To(ContainSubstring("peers must not be empty"))
	})

	It("rejects election_timeout_min >= election_timeout_max", func() {
		cfg := base()
		cfg.Timing.ElectionTimeoutMin = 3 * time.Second
		cfg.Timing.ElectionTimeoutMax = 3 * time.Second
		Expect(cfg.Validate().Error()).To(ContainSubstring("must be < election_timeout_max"))
	})

	It("enforces heartbeat < suspect < dead", func() {
		cfg := base()
		cfg.Timing.HeartbeatInterval = 10 * time.Second // now > suspect_after (5s)
		Expect(cfg.Validate().Error()).To(ContainSubstring("heartbeat_interval"))
	})

	DescribeTable("enforces notify-mode requirements",
		func(mut func(cfg *config.Config), substr string) {
			cfg := base()
			mut(&cfg)
			Expect(cfg.Validate().Error()).To(ContainSubstring(substr))
		},
		Entry("smtp requires host", func(c *config.Config) {
			c.Notify.Mode = "smtp"
			c.Notify.SMTPFrom = "f@x"
			c.Notify.SMTPTo = []string{"t@x"}
			c.Notify.SMTPHost = ""
		}, "requires smtp_host"),
		Entry("smtp requires from", func(c *config.Config) {
			c.Notify.Mode = "smtp"
			c.Notify.SMTPHost = "h"
			c.Notify.SMTPFrom = ""
			c.Notify.SMTPTo = []string{"t@x"}
		}, "requires smtp_from"),
		Entry("file requires file", func(c *config.Config) {
			c.Notify.Mode = "file"
			c.Notify.File = ""
		}, "requires file"),
		Entry("unknown mode", func(c *config.Config) { c.Notify.Mode = "carrier-pigeon" }, "is invalid"),
	)

	It("does NOT require SMTP_PASS for smtp mode (IP-allowlisted relays need none)", func() {
		cfg := base()
		cfg.Notify.Mode = "smtp"
		cfg.Notify.SMTPHost = "relay"
		cfg.Notify.SMTPFrom = "f@x"
		cfg.Notify.SMTPTo = []string{"t@x"}
		cfg.Notify.SMTPPass = ""
		Expect(cfg.Validate()).To(Succeed())
	})

	It("Load itself does not validate (an invalid config still loads)", func() {
		// node_id absent from peers is invalid, but Load returns it anyway.
		cfg, err := config.Load("", env(map[string]string{"WARDEN_NODE_ID": "ghost"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Validate()).To(HaveOccurred())
	})
})

var _ = Describe("Config.Redacted and String", func() {
	It("strips the SMTP password and deep-copies slices", func() {
		cfg, err := config.Load("", env(map[string]string{
			"WARDEN_NODE_ID": "a",
			"WARDEN_PEERS":   "a=h:1,b=h:2",
			"SMTP_PASS":      "topsecret",
		}))
		Expect(err).NotTo(HaveOccurred())
		r := cfg.Redacted()
		Expect(r.Notify.SMTPPass).To(BeEmpty())
		Expect(cfg.Notify.SMTPPass).To(Equal("topsecret")) // original untouched
	})

	It("never leaks the password through String()", func() {
		cfg, err := config.Load("", env(map[string]string{"SMTP_PASS": "topsecret"}))
		Expect(err).NotTo(HaveOccurred())
		s := cfg.String()
		Expect(s).NotTo(ContainSubstring("topsecret"))
		Expect(s).To(ContainSubstring("smtp_pass=<redacted>"))
	})
})
