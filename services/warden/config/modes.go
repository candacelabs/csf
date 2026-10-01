package config

// Discovery source modes for Config.Discovery.Mode. These name the three
// recognized values that config.Load defaults, Validate accepts, and
// cmd/main.go switches on to build the PeerDiscoverer. The YAML/env value is
// still carried as a raw string; these constants are the single source of
// truth for what the accepted strings ARE.
const (
	DiscoveryModeStatic    = "static"    // peers seed only, no dynamic discovery (default, fail-safe)
	DiscoveryModeTailscale = "tailscale" // poll the local tailscaled LocalAPI
	DiscoveryModeFile      = "file"      // poll a JSON roster file (also a manual dynamic mode)
)

// Operator-notification delivery modes for Config.Notify.Mode. Recognized by
// Validate and switched on by cmd/main.go to build the Notifier. NotifyModeFile
// shares the "file" spelling of DiscoveryModeFile but is a distinct concept (an
// incident sink, not a discovery source).
const (
	NotifyModeSMTP = "smtp" // email over SMTP + STARTTLS
	NotifyModeLog  = "log"  // structured log lines (default when no smtp_host)
	NotifyModeFile = "file" // append incidents to a JSONL sink file
)
