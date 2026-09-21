// Package config loads warden node configuration from built-in defaults, an
// optional YAML file, and environment overrides, in that precedence order
// (env beats file beats defaults). It is a pure, dependency-light package:
// stdlib, gopkg.in/yaml.v3, and the frozen warden contract package only. It
// contains zero concurrency constructs — a Config is an immutable value that
// callers pass by copy, which is what keeps the wiring in cmd/main.go and the
// whole service race-free by construction.
//
// Callers may rely on three things. Load never returns a half-resolved Config:
// it either applies the whole precedence chain or reports an error. Validate is
// the single gate a Config must pass before anything is wired from it, and it
// is total — every rule the rest of the service assumes is checked there, so no
// subsystem re-validates. And Redacted, which the Config's own String is built
// on, is how a Config reaches a log line or the dashboard: it is the package's
// promise that the SMTP password never leaves in cleartext, so no caller needs
// to remember to strip it. Resolution is pure: nothing here opens a socket,
// writes a file, or mutates the process environment.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/candacelabs/csf/services/warden"
)

// Built-in defaults. These are the values a node boots with when neither a
// YAML file nor an environment variable specifies otherwise.
const (
	defaultBind         = ":7717"
	defaultDataDir      = "/var/lib/warden"
	defaultLogLevel     = "info"
	defaultSMTPPort     = 587
	defaultMaxIncidents = 100

	defaultHeartbeatInterval  = 1 * time.Second
	defaultSuspectAfter       = 5 * time.Second
	defaultDeadAfter          = 15 * time.Second
	defaultElectionTimeoutMin = 1500 * time.Millisecond
	defaultElectionTimeoutMax = 3 * time.Second
	defaultRPCTimeout         = 500 * time.Millisecond
	defaultCooldown           = 10 * time.Minute

	defaultDiscoveryMode    = DiscoveryModeStatic
	defaultClusterID        = "candacenet"
	defaultJoinStability    = 30 * time.Second
	defaultRemoveAfter      = 0 * time.Second // 0 = never auto-remove
	defaultFilePollInterval = 2 * time.Second
	defaultTSSocket         = "/var/run/tailscale/tailscaled.sock"
	defaultTSPollInterval   = 15 * time.Second
)

// Config is the fully-resolved warden node configuration. It is safe to copy
// and to log via Redacted()/String() (which strip the SMTP password).
type Config struct {
	NodeID   string         `yaml:"node_id"`
	Bind     string         `yaml:"bind"`      // HTTP listen address, e.g. ":7717"
	DataDir  string         `yaml:"data_dir"`  // election state lives at <DataDir>/state.json
	Peers    []warden.Node  `yaml:"peers"`     // full member SEED (voter set to start from), including self
	Timing   TimingConfig   `yaml:"timing"`    // election/liveness durations
	Watchdog WatchdogConfig `yaml:"watchdog"`  // incident engine tuning
	Notify   NotifyConfig   `yaml:"notify"`    // operator notification delivery
	LogLevel string         `yaml:"log_level"` // debug|info|warn|error

	// Discovery configures membership autodiscovery (how a node learns which
	// other nodes are candidate members). Default mode "static" = peers only.
	Discovery DiscoveryConfig `yaml:"discovery"`
	// Advertise is this node's routable ip:port, used ONLY when it is a
	// discovery-mode joiner whose node_id is not yet in Peers: main.go then
	// synthesizes Self from it. Empty means "use Bind" (see AdvertiseAddr).
	// Env override: WARDEN_ADVERTISE_ADDR.
	Advertise string `yaml:"advertise_addr"`
}

// DiscoveryConfig configures membership autodiscovery. Discovery is advisory:
// it never changes the quorum denominator — quorum is always computed over the
// persisted voter set. The leader turns stable, identify-verified candidates
// into one-at-a-time voting-membership changes (see services/warden/election). In the
// dynamic modes (tailscale, file) the static Peers list is still REQUIRED as
// the membership seed.
type DiscoveryConfig struct {
	// Mode selects the discovery source: "static" (peers only, no dynamic
	// discovery), "tailscale" (poll the local tailscaled LocalAPI), or "file"
	// (poll a JSON roster file). Env WARDEN_DISCOVERY_MODE.
	Mode string `yaml:"mode"`
	// ClusterID names this cluster for the identify handshake; a discovered
	// node is treated as an observer candidate only when it reports the same
	// ClusterID. Env WARDEN_CLUSTER_ID.
	ClusterID string `yaml:"cluster_id"`
	// JoinStability is how long a discovered, identify-verified observer must
	// remain continuously present before the leader admits it as a voter.
	// Env WARDEN_JOIN_STABILITY.
	JoinStability time.Duration `yaml:"join_stability"`
	// RemoveAfter is how long a voter may be absent from the roster before the
	// leader commits its removal (0 = never auto-remove; removal is then a
	// manual config edit + rolling restart). Env WARDEN_REMOVE_AFTER.
	RemoveAfter time.Duration `yaml:"remove_after"`
	// File is the roster path for Mode "file". Env WARDEN_DISCOVERY_FILE.
	File string `yaml:"file"`
	// FilePollInterval is how often Mode "file" re-reads the roster file.
	// Env WARDEN_FILE_POLL_INTERVAL.
	FilePollInterval time.Duration `yaml:"file_poll_interval"`
	// Tailscale holds Mode "tailscale" settings.
	Tailscale TailscaleDiscoveryConfig `yaml:"tailscale"`
}

// TailscaleDiscoveryConfig configures the tailscale discovery source. A peer is
// selected when it advertises Tag OR its HostName matches HostPattern — either
// suffices when both are set.
type TailscaleDiscoveryConfig struct {
	// Socket is the tailscaled LocalAPI unix socket. Env WARDEN_TS_SOCKET.
	Socket string `yaml:"socket"`
	// Tag matches peers advertising this ACL tag, e.g. "tag:candacenet".
	// Env WARDEN_TS_TAG.
	Tag string `yaml:"tag"`
	// HostPattern is an RE2 pattern matched (anchored to the whole string)
	// against a peer's HostName. Env WARDEN_TS_HOST_PATTERN.
	HostPattern string `yaml:"host_pattern"`
	// PollInterval is how often tailscaled status is polled.
	// Env WARDEN_TS_POLL_INTERVAL.
	PollInterval time.Duration `yaml:"poll_interval"`
}

// TimingConfig holds the election and liveness durations. Its YAML form uses
// Go duration strings ("1s", "1500ms"); see UnmarshalYAML.
type TimingConfig struct {
	HeartbeatInterval  time.Duration `yaml:"heartbeat_interval"`
	SuspectAfter       time.Duration `yaml:"suspect_after"`
	DeadAfter          time.Duration `yaml:"dead_after"`
	ElectionTimeoutMin time.Duration `yaml:"election_timeout_min"`
	ElectionTimeoutMax time.Duration `yaml:"election_timeout_max"`
	RPCTimeout         time.Duration `yaml:"rpc_timeout"`
}

// WatchdogConfig tunes the leader-only incident engine. NotifyRecovery is a
// tri-state pointer so an unset value (nil) can default to true while an
// explicit "false" in YAML/env is honored.
type WatchdogConfig struct {
	Cooldown       time.Duration `yaml:"cooldown"`
	NotifyRecovery *bool         `yaml:"notify_recovery"`
	MaxIncidents   int           `yaml:"max_incidents"`
}

// NotifyConfig selects and configures the operator Notifier. SMTPPass is
// never read from YAML (tag "-"); it comes from the SMTP_PASS env var only,
// so the password never has to live in a config file on disk.
type NotifyConfig struct {
	Mode     string   `yaml:"mode"` // smtp|log|file
	File     string   `yaml:"file"` // path for mode=file
	SMTPHost string   `yaml:"smtp_host"`
	SMTPPort int      `yaml:"smtp_port"`
	SMTPUser string   `yaml:"smtp_user"`
	SMTPPass string   `yaml:"-"` // env SMTP_PASS only, never YAML
	SMTPFrom string   `yaml:"smtp_from"`
	SMTPTo   []string `yaml:"smtp_to"`
}

// defaultConfig returns the built-in defaults. Peers and SMTPTo have no
// built-in value: a warden binary must never carry one operator's fleet
// addresses or notification recipients, so both are supplied per deployment
// through YAML (WARDEN_CONFIG) or the environment and fail validation when
// they are required but absent.
func defaultConfig() Config {
	return Config{
		Bind:     defaultBind,
		DataDir:  defaultDataDir,
		LogLevel: defaultLogLevel,
		Timing: TimingConfig{
			HeartbeatInterval:  defaultHeartbeatInterval,
			SuspectAfter:       defaultSuspectAfter,
			DeadAfter:          defaultDeadAfter,
			ElectionTimeoutMin: defaultElectionTimeoutMin,
			ElectionTimeoutMax: defaultElectionTimeoutMax,
			RPCTimeout:         defaultRPCTimeout,
		},
		Watchdog: WatchdogConfig{
			Cooldown:       defaultCooldown,
			NotifyRecovery: nil, // resolved to true at end of Load
			MaxIncidents:   defaultMaxIncidents,
		},
		Notify: NotifyConfig{
			SMTPPort: defaultSMTPPort,
		},
		Discovery: DiscoveryConfig{
			Mode:             defaultDiscoveryMode,
			ClusterID:        defaultClusterID,
			JoinStability:    defaultJoinStability,
			RemoveAfter:      defaultRemoveAfter,
			FilePollInterval: defaultFilePollInterval,
			Tailscale: TailscaleDiscoveryConfig{
				Socket:       defaultTSSocket,
				PollInterval: defaultTSPollInterval,
			},
		},
	}
}

// Load resolves configuration from defaults, then the YAML file at path (if
// non-empty; an explicitly-given but unreadable/unparseable file is an
// error), then environment overrides read via getenv (injectable for tests;
// nil getenv is treated as "no env set"). It returns a fully-defaulted Config
// but does NOT validate it — call Validate separately.
func Load(path string, getenv func(name string) string) (Config, error) {
	if getenv == nil {
		getenv = func(name string) string { return "" }
	}

	cfg := defaultConfig()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("reading config file %q: %w", path, err)
		}
		// Unmarshal into the already-defaulted value: keys present in the
		// file override the corresponding defaults; absent keys are left as
		// defaults (the timing/watchdog custom decoders preserve them).
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parsing config file %q: %w", path, err)
		}
	}

	if err := applyEnv(&cfg, getenv); err != nil {
		return Config{}, err
	}

	// Resolve the tri-state notify mode last, so an override from either YAML
	// or env wins and only a genuinely-absent value falls back. Peers and
	// smtp_to have no fallback at all - see defaultConfig.
	if cfg.Notify.Mode == "" {
		if cfg.Notify.SMTPHost != "" {
			cfg.Notify.Mode = NotifyModeSMTP
		} else {
			cfg.Notify.Mode = NotifyModeLog
		}
	}
	if cfg.Watchdog.NotifyRecovery == nil {
		v := true
		cfg.Watchdog.NotifyRecovery = &v
	}
	if cfg.Watchdog.MaxIncidents <= 0 {
		cfg.Watchdog.MaxIncidents = defaultMaxIncidents
	}

	return cfg, nil
}

// Self returns the node entry matching NodeID and whether it was found.
func (c Config) Self() (warden.Node, bool) {
	for _, n := range c.Peers {
		if string(n.ID) == c.NodeID {
			return n, true
		}
	}
	return warden.Node{}, false
}

// AdvertiseAddr returns the address other nodes should reach this node at:
// the explicit advertise_addr when set, otherwise Bind. For a node present in
// Peers the peer entry is authoritative and this is unused; it matters only for
// a discovery-mode joiner whose node_id is not yet in the peer seed, where
// main.go synthesizes Self from it.
func (c Config) AdvertiseAddr() string {
	if strings.TrimSpace(c.Advertise) != "" {
		return c.Advertise
	}
	return c.Bind
}

// Validate reports the first configuration error, or nil if the Config is
// internally consistent and deployable. It deliberately does NOT require
// SMTPPass: some relays are IP-allowlisted and need no password.
func (c Config) Validate() error {
	if c.NodeID == "" {
		return errors.New("node_id must not be empty")
	}
	if len(c.Peers) == 0 {
		return errors.New("peers must not be empty: warden ships no built-in " +
			"fleet, so supply the cluster's member set through the YAML config " +
			"named by WARDEN_CONFIG or through WARDEN_PEERS")
	}

	seen := make(map[warden.NodeID]bool, len(c.Peers))
	selfFound := false
	for _, p := range c.Peers {
		if p.ID == "" {
			return errors.New("peers contains an entry with an empty id")
		}
		if seen[p.ID] {
			return fmt.Errorf("duplicate peer id %q", p.ID)
		}
		seen[p.ID] = true
		if _, _, err := net.SplitHostPort(p.Addr); err != nil {
			return fmt.Errorf("peer %q addr %q is not host:port: %w", p.ID, p.Addr, err)
		}
		if string(p.ID) == c.NodeID {
			selfFound = true
		}
	}
	if err := c.validateDiscovery(selfFound); err != nil {
		return err
	}

	t := c.Timing
	for _, d := range []struct {
		name string
		v    time.Duration
	}{
		{"heartbeat_interval", t.HeartbeatInterval},
		{"suspect_after", t.SuspectAfter},
		{"dead_after", t.DeadAfter},
		{"election_timeout_min", t.ElectionTimeoutMin},
		{"election_timeout_max", t.ElectionTimeoutMax},
		{"rpc_timeout", t.RPCTimeout},
	} {
		if d.v <= 0 {
			return fmt.Errorf("timing.%s must be > 0 (got %s)", d.name, d.v)
		}
	}
	if c.Watchdog.Cooldown <= 0 {
		return fmt.Errorf("watchdog.cooldown must be > 0 (got %s)", c.Watchdog.Cooldown)
	}
	if t.ElectionTimeoutMin >= t.ElectionTimeoutMax {
		return fmt.Errorf("election_timeout_min (%s) must be < election_timeout_max (%s)",
			t.ElectionTimeoutMin, t.ElectionTimeoutMax)
	}
	if !(t.HeartbeatInterval < t.SuspectAfter && t.SuspectAfter < t.DeadAfter) {
		return fmt.Errorf("must satisfy heartbeat_interval (%s) < suspect_after (%s) < dead_after (%s)",
			t.HeartbeatInterval, t.SuspectAfter, t.DeadAfter)
	}

	switch c.Notify.Mode {
	case NotifyModeSMTP:
		if c.Notify.SMTPHost == "" {
			return errors.New("notify.mode=smtp requires smtp_host")
		}
		if c.Notify.SMTPFrom == "" {
			return errors.New("notify.mode=smtp requires smtp_from")
		}
		if len(c.Notify.SMTPTo) == 0 {
			return errors.New("notify.mode=smtp requires smtp_to")
		}
	case NotifyModeFile:
		if c.Notify.File == "" {
			return errors.New("notify.mode=file requires file")
		}
	case NotifyModeLog:
		// no additional requirements
	default:
		return fmt.Errorf("notify.mode %q is invalid (want smtp|log|file)", c.Notify.Mode)
	}

	return nil
}

// validateDiscovery checks the discovery block and the mode-dependent
// membership rule. selfFound reports whether NodeID is present in Peers.
//
// In static mode NodeID MUST be in Peers (the classic rule). In a dynamic mode
// (tailscale|file) NodeID may be absent — that is exactly the joining-node case:
// the node seeds membership from Peers and runs as an observer until the leader
// admits it. When Self is absent, main.go synthesizes it from advertise_addr,
// which must then carry a routable host (not empty/0.0.0.0/::).
func (c Config) validateDiscovery(selfFound bool) error {
	switch c.Discovery.Mode {
	case DiscoveryModeStatic:
		if !selfFound {
			return fmt.Errorf("node_id %q is not present in peers", c.NodeID)
		}
	case DiscoveryModeTailscale, DiscoveryModeFile:
		if !selfFound {
			adv := c.AdvertiseAddr()
			host, _, err := net.SplitHostPort(adv)
			if err != nil {
				return fmt.Errorf("advertise_addr %q is not host:port: %w", adv, err)
			}
			if host == "" || host == "0.0.0.0" || host == "::" {
				return fmt.Errorf("node_id %q is not in peers and discovery.mode=%s needs a routable "+
					"advertise_addr (got %q); set advertise_addr / WARDEN_ADVERTISE_ADDR to this node's "+
					"tailnet ip:port", c.NodeID, c.Discovery.Mode, adv)
			}
		}
		if c.Discovery.Mode == DiscoveryModeTailscale &&
			c.Discovery.Tailscale.Tag == "" && c.Discovery.Tailscale.HostPattern == "" {
			return errors.New("discovery.mode=tailscale requires tailscale.tag or tailscale.host_pattern")
		}
		if c.Discovery.Mode == DiscoveryModeFile && strings.TrimSpace(c.Discovery.File) == "" {
			return errors.New("discovery.mode=file requires discovery.file")
		}
	default:
		return fmt.Errorf("discovery.mode %q is invalid (want static|tailscale|file)", c.Discovery.Mode)
	}

	if c.Discovery.ClusterID == "" {
		return errors.New("discovery.cluster_id must not be empty")
	}
	if c.Discovery.JoinStability <= 0 {
		return fmt.Errorf("discovery.join_stability must be > 0 (got %s)", c.Discovery.JoinStability)
	}
	if c.Discovery.RemoveAfter < 0 {
		return fmt.Errorf("discovery.remove_after must be >= 0 (got %s)", c.Discovery.RemoveAfter)
	}
	if c.Discovery.Tailscale.PollInterval <= 0 {
		return fmt.Errorf("discovery.tailscale.poll_interval must be > 0 (got %s)", c.Discovery.Tailscale.PollInterval)
	}
	if c.Discovery.FilePollInterval <= 0 {
		return fmt.Errorf("discovery.file_poll_interval must be > 0 (got %s)", c.Discovery.FilePollInterval)
	}
	if p := c.Discovery.Tailscale.HostPattern; p != "" {
		// Anchoring MUST match discovery.CompileHostPattern.
		if _, err := regexp.Compile(`\A(?:` + p + `)\z`); err != nil {
			return fmt.Errorf("discovery.tailscale.host_pattern %q is not a valid RE2 pattern: %w", p, err)
		}
	}
	return nil
}

// Redacted returns a copy of the Config with the SMTP password stripped and
// its slices deep-copied, safe to log or serialize.
func (c Config) Redacted() Config {
	r := c
	r.Notify.SMTPPass = ""
	r.Peers = append([]warden.Node(nil), c.Peers...)
	r.Notify.SMTPTo = append([]string(nil), c.Notify.SMTPTo...)
	if c.Watchdog.NotifyRecovery != nil {
		v := *c.Watchdog.NotifyRecovery
		r.Watchdog.NotifyRecovery = &v
	}
	return r
}

// String renders a single-line, password-free summary of the Config,
// suitable for a startup log line.
func (c Config) String() string {
	r := c.Redacted()
	recovery := "unset"
	if r.Watchdog.NotifyRecovery != nil {
		recovery = strconv.FormatBool(*r.Watchdog.NotifyRecovery)
	}
	return fmt.Sprintf(
		"node_id=%s bind=%s advertise_addr=%s data_dir=%s peers=%d log_level=%s "+
			"notify{mode=%s file=%q smtp_host=%s smtp_port=%d smtp_from=%s smtp_to=%v smtp_pass=<redacted>} "+
			"timing{heartbeat=%s suspect=%s dead=%s election=[%s,%s] rpc_timeout=%s} "+
			"watchdog{cooldown=%s notify_recovery=%s max_incidents=%d} "+
			"discovery{mode=%s cluster_id=%s join_stability=%s remove_after=%s file=%q file_poll=%s "+
			"ts{socket=%s tag=%s host_pattern=%q poll=%s}}",
		r.NodeID, r.Bind, r.AdvertiseAddr(), r.DataDir, len(r.Peers), r.LogLevel,
		r.Notify.Mode, r.Notify.File, r.Notify.SMTPHost, r.Notify.SMTPPort,
		r.Notify.SMTPFrom, r.Notify.SMTPTo,
		r.Timing.HeartbeatInterval, r.Timing.SuspectAfter, r.Timing.DeadAfter,
		r.Timing.ElectionTimeoutMin, r.Timing.ElectionTimeoutMax, r.Timing.RPCTimeout,
		r.Watchdog.Cooldown, recovery, r.Watchdog.MaxIncidents,
		r.Discovery.Mode, r.Discovery.ClusterID, r.Discovery.JoinStability, r.Discovery.RemoveAfter,
		r.Discovery.File, r.Discovery.FilePollInterval,
		r.Discovery.Tailscale.Socket, r.Discovery.Tailscale.Tag,
		r.Discovery.Tailscale.HostPattern, r.Discovery.Tailscale.PollInterval,
	)
}

// UnmarshalYAML decodes TimingConfig from Go duration strings. It mutates the
// receiver in place, overwriting only the keys present in the YAML node, so
// any defaults already set on the receiver survive a partial timing block.
func (t *TimingConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		HeartbeatInterval  string `yaml:"heartbeat_interval"`
		SuspectAfter       string `yaml:"suspect_after"`
		DeadAfter          string `yaml:"dead_after"`
		ElectionTimeoutMin string `yaml:"election_timeout_min"`
		ElectionTimeoutMax string `yaml:"election_timeout_max"`
		RPCTimeout         string `yaml:"rpc_timeout"`
	}
	if err := value.Decode(&raw); err != nil {
		return fmt.Errorf("timing: %w", err)
	}
	for _, f := range []struct {
		name string
		src  string
		dst  *time.Duration
	}{
		{"heartbeat_interval", raw.HeartbeatInterval, &t.HeartbeatInterval},
		{"suspect_after", raw.SuspectAfter, &t.SuspectAfter},
		{"dead_after", raw.DeadAfter, &t.DeadAfter},
		{"election_timeout_min", raw.ElectionTimeoutMin, &t.ElectionTimeoutMin},
		{"election_timeout_max", raw.ElectionTimeoutMax, &t.ElectionTimeoutMax},
		{"rpc_timeout", raw.RPCTimeout, &t.RPCTimeout},
	} {
		if f.src == "" {
			continue
		}
		d, err := time.ParseDuration(f.src)
		if err != nil {
			return fmt.Errorf("timing.%s %q: %w", f.name, f.src, err)
		}
		*f.dst = d
	}
	return nil
}

// UnmarshalYAML decodes WatchdogConfig, parsing Cooldown from a duration
// string and preserving defaults for keys absent from the YAML node.
func (w *WatchdogConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Cooldown       string `yaml:"cooldown"`
		NotifyRecovery *bool  `yaml:"notify_recovery"`
		MaxIncidents   *int   `yaml:"max_incidents"`
	}
	if err := value.Decode(&raw); err != nil {
		return fmt.Errorf("watchdog: %w", err)
	}
	if raw.Cooldown != "" {
		d, err := time.ParseDuration(raw.Cooldown)
		if err != nil {
			return fmt.Errorf("watchdog.cooldown %q: %w", raw.Cooldown, err)
		}
		w.Cooldown = d
	}
	if raw.NotifyRecovery != nil {
		w.NotifyRecovery = raw.NotifyRecovery
	}
	if raw.MaxIncidents != nil {
		w.MaxIncidents = *raw.MaxIncidents
	}
	return nil
}

// UnmarshalYAML decodes DiscoveryConfig, parsing its duration fields from Go
// duration strings and merging the nested tailscale block into the receiver, so
// defaults for keys absent from a partial discovery block survive.
func (d *DiscoveryConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Mode             string    `yaml:"mode"`
		ClusterID        string    `yaml:"cluster_id"`
		JoinStability    string    `yaml:"join_stability"`
		RemoveAfter      string    `yaml:"remove_after"`
		File             string    `yaml:"file"`
		FilePollInterval string    `yaml:"file_poll_interval"`
		Tailscale        yaml.Node `yaml:"tailscale"`
	}
	if err := value.Decode(&raw); err != nil {
		return fmt.Errorf("discovery: %w", err)
	}
	if raw.Mode != "" {
		d.Mode = raw.Mode
	}
	if raw.ClusterID != "" {
		d.ClusterID = raw.ClusterID
	}
	if raw.File != "" {
		d.File = raw.File
	}
	for _, f := range []struct {
		name string
		src  string
		dst  *time.Duration
	}{
		{"join_stability", raw.JoinStability, &d.JoinStability},
		{"remove_after", raw.RemoveAfter, &d.RemoveAfter},
		{"file_poll_interval", raw.FilePollInterval, &d.FilePollInterval},
	} {
		if f.src == "" {
			continue
		}
		dur, err := time.ParseDuration(f.src)
		if err != nil {
			return fmt.Errorf("discovery.%s %q: %w", f.name, f.src, err)
		}
		*f.dst = dur
	}
	// Decode the tailscale block INTO the existing (defaulted) receiver so a
	// partial tailscale: block keeps unspecified defaults (Kind != 0 means the
	// key was present).
	if raw.Tailscale.Kind != 0 {
		if err := raw.Tailscale.Decode(&d.Tailscale); err != nil {
			return err
		}
	}
	return nil
}

// UnmarshalYAML decodes TailscaleDiscoveryConfig, parsing PollInterval from a
// duration string and preserving defaults for keys absent from the YAML node.
func (t *TailscaleDiscoveryConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Socket       string `yaml:"socket"`
		Tag          string `yaml:"tag"`
		HostPattern  string `yaml:"host_pattern"`
		PollInterval string `yaml:"poll_interval"`
	}
	if err := value.Decode(&raw); err != nil {
		return fmt.Errorf("discovery.tailscale: %w", err)
	}
	if raw.Socket != "" {
		t.Socket = raw.Socket
	}
	if raw.Tag != "" {
		t.Tag = raw.Tag
	}
	if raw.HostPattern != "" {
		t.HostPattern = raw.HostPattern
	}
	if raw.PollInterval != "" {
		d, err := time.ParseDuration(raw.PollInterval)
		if err != nil {
			return fmt.Errorf("discovery.tailscale.poll_interval %q: %w", raw.PollInterval, err)
		}
		t.PollInterval = d
	}
	return nil
}

// applyEnv overlays environment overrides onto cfg. Every recognized variable
// that is set (non-empty after trimming) replaces the corresponding value.
// Malformed values (bad duration, non-integer port, non-bool flag, malformed
// peer list) are returned as errors rather than silently ignored.
func applyEnv(cfg *Config, getenv func(name string) string) error {
	envString(getenv, envNodeID, &cfg.NodeID)
	envString(getenv, envBind, &cfg.Bind)
	envString(getenv, envDataDir, &cfg.DataDir)
	envString(getenv, envLogLevel, &cfg.LogLevel)

	if v := strings.TrimSpace(getenv(envPeers)); v != "" {
		peers, err := parsePeers(v)
		if err != nil {
			return fmt.Errorf("env WARDEN_PEERS: %w", err)
		}
		cfg.Peers = peers
	}

	if err := envDuration(getenv, envHeartbeatInterval, &cfg.Timing.HeartbeatInterval); err != nil {
		return err
	}
	if err := envDuration(getenv, envSuspectAfter, &cfg.Timing.SuspectAfter); err != nil {
		return err
	}
	if err := envDuration(getenv, envDeadAfter, &cfg.Timing.DeadAfter); err != nil {
		return err
	}
	if err := envDuration(getenv, envElectionTimeoutMin, &cfg.Timing.ElectionTimeoutMin); err != nil {
		return err
	}
	if err := envDuration(getenv, envElectionTimeoutMax, &cfg.Timing.ElectionTimeoutMax); err != nil {
		return err
	}
	if err := envDuration(getenv, envRPCTimeout, &cfg.Timing.RPCTimeout); err != nil {
		return err
	}
	if err := envDuration(getenv, envCooldown, &cfg.Watchdog.Cooldown); err != nil {
		return err
	}

	if v := strings.TrimSpace(getenv(envNotifyRecovery)); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("env WARDEN_NOTIFY_RECOVERY %q: %w", v, err)
		}
		cfg.Watchdog.NotifyRecovery = &b
	}

	envString(getenv, envNotifyMode, &cfg.Notify.Mode)
	envString(getenv, envNotifyFile, &cfg.Notify.File)
	envString(getenv, envSMTPHost, &cfg.Notify.SMTPHost)
	if err := envInt(getenv, envSMTPPort, &cfg.Notify.SMTPPort); err != nil {
		return err
	}
	envString(getenv, envSMTPUser, &cfg.Notify.SMTPUser)
	envString(getenv, envSMTPPass, &cfg.Notify.SMTPPass)
	envString(getenv, envSMTPFrom, &cfg.Notify.SMTPFrom)

	if v := strings.TrimSpace(getenv(envSMTPTo)); v != "" {
		if to := splitList(v); len(to) > 0 {
			cfg.Notify.SMTPTo = to
		}
	}

	// --- discovery -------------------------------------------------------
	envString(getenv, envDiscoveryMode, &cfg.Discovery.Mode)
	envString(getenv, envClusterID, &cfg.Discovery.ClusterID)
	if err := envDuration(getenv, envJoinStability, &cfg.Discovery.JoinStability); err != nil {
		return err
	}
	if err := envDuration(getenv, envRemoveAfter, &cfg.Discovery.RemoveAfter); err != nil {
		return err
	}
	envString(getenv, envDiscoveryFile, &cfg.Discovery.File)
	if err := envDuration(getenv, envFilePollInterval, &cfg.Discovery.FilePollInterval); err != nil {
		return err
	}
	envString(getenv, envTSSocket, &cfg.Discovery.Tailscale.Socket)
	envString(getenv, envTSTag, &cfg.Discovery.Tailscale.Tag)
	envString(getenv, envTSHostPattern, &cfg.Discovery.Tailscale.HostPattern)
	if err := envDuration(getenv, envTSPollInterval, &cfg.Discovery.Tailscale.PollInterval); err != nil {
		return err
	}
	envString(getenv, envAdvertiseAddr, &cfg.Advertise)

	return nil
}

// envString overwrites *dst with the trimmed env value if it is non-empty.
func envString(getenv func(name string) string, key string, dst *string) {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		*dst = v
	}
}

// envDuration parses a Go duration string; empty is a no-op, malformed errors.
func envDuration(getenv func(name string) string, key string, dst *time.Duration) error {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("env %s %q: %w", key, v, err)
	}
	*dst = d
	return nil
}

// envInt parses a base-10 integer; empty is a no-op, malformed errors.
func envInt(getenv func(name string) string, key string, dst *int) error {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("env %s %q: %w", key, v, err)
	}
	*dst = n
	return nil
}

// splitList splits a comma-separated list, trimming each element and dropping
// empties.
func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parsePeers parses the WARDEN_PEERS format: "id=host:port,id=host:port". It
// rejects entries missing "=", empty ids/addrs, addrs that are not host:port,
// and duplicate ids.
func parsePeers(s string) ([]warden.Node, error) {
	var peers []warden.Node
	seen := make(map[warden.NodeID]bool)
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		idx := strings.Index(pair, "=")
		if idx < 0 {
			return nil, fmt.Errorf("peer %q missing '=' (want id=host:port)", pair)
		}
		id := strings.TrimSpace(pair[:idx])
		addr := strings.TrimSpace(pair[idx+1:])
		if id == "" {
			return nil, fmt.Errorf("peer %q has an empty id", pair)
		}
		if addr == "" {
			return nil, fmt.Errorf("peer %q has an empty addr", pair)
		}
		if _, _, err := net.SplitHostPort(addr); err != nil {
			return nil, fmt.Errorf("peer %q addr %q is not host:port: %w", id, addr, err)
		}
		nid := warden.NodeID(id)
		if seen[nid] {
			return nil, fmt.Errorf("duplicate peer id %q", id)
		}
		seen[nid] = true
		peers = append(peers, warden.Node{ID: nid, Addr: addr})
	}
	if len(peers) == 0 {
		return nil, fmt.Errorf("parsed no peers from %q", s)
	}
	return peers, nil
}
