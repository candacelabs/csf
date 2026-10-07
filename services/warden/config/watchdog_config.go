package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// WatchdogConfig tunes the leader-only incident engine. NotifyRecovery is a
// tri-state pointer so an unset value (nil) can default to true while an
// explicit "false" in YAML/env is honored.
type WatchdogConfig struct {
	Cooldown       time.Duration `yaml:"cooldown"`
	NotifyRecovery *bool         `yaml:"notify_recovery"`
	MaxIncidents   int           `yaml:"max_incidents"`
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
