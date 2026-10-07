package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

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
