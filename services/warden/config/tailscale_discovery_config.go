package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

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
