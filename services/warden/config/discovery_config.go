package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

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
