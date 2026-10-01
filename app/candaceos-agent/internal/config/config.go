// Package config loads and validates candaceos-agent process configuration.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

const (
	defaultBind               = "127.0.0.1:8094"
	defaultWorkspace          = "/var/lib/candaceos/apps"
	defaultRevisionRoot       = "/var/lib/candaceos-agent/revisions"
	defaultRevisionMaxEntries = 128
	defaultRevisionMaxBytes   = int64(4 << 30)
	defaultSourceRepository   = "/var/lib/candaceos-agent/source.git"
	defaultSourceFetchTimeout = 30 * time.Second
	defaultStateFile          = "/var/lib/candaceos-agent/state.json"
	defaultDockerBin          = "docker"
	defaultNodeID             = "candaceos-node"
	envBind                   = "CANDACEOS_AGENT_BIND"
	envToken                  = "CANDACEOS_AGENT_TOKEN"
	envNodeID                 = "CANDACEOS_AGENT_NODE_ID"
	envWorkspace              = "CANDACEOS_AGENT_WORKSPACE"
	envRevisionRoot           = "CANDACEOS_AGENT_REVISION_ROOT"
	envRevisionMaxEntries     = "CANDACEOS_AGENT_REVISION_MAX_ENTRIES"
	envRevisionMaxBytes       = "CANDACEOS_AGENT_REVISION_MAX_BYTES"
	envSourceRemote           = "CANDACEOS_AGENT_SOURCE_REMOTE"
	envSourceRepository       = "CANDACEOS_AGENT_SOURCE_REPOSITORY"
	envSourceFetchTimeout     = "CANDACEOS_AGENT_SOURCE_FETCH_TIMEOUT"
	envStateFile              = "CANDACEOS_AGENT_STATE_FILE"
	envDockerBin              = "CANDACEOS_AGENT_DOCKER_BIN"
	envDryRun                 = "CANDACEOS_AGENT_DRY_RUN"
)

// Config is the complete runtime configuration.
type Config struct {
	Bind               string
	Token              string
	NodeID             string
	Workspace          string
	RevisionRoot       string
	RevisionMaxEntries int64
	RevisionMaxBytes   int64
	SourceRemote       string
	SourceRepository   string
	SourceFetchTimeout time.Duration
	StateFile          string
	DockerBin          string
	DryRun             bool
}

// Load reads configuration from getenv and applies safe defaults.
func Load(getenv func(key string) string) (Config, error) {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = defaultNodeID
	}

	cfg := Config{
		Bind:               valueOr(getenv(envBind), defaultBind),
		Token:              strings.TrimSpace(getenv(envToken)),
		NodeID:             valueOr(getenv(envNodeID), hostname),
		Workspace:          valueOr(getenv(envWorkspace), defaultWorkspace),
		RevisionRoot:       valueOr(getenv(envRevisionRoot), defaultRevisionRoot),
		RevisionMaxEntries: defaultRevisionMaxEntries,
		RevisionMaxBytes:   defaultRevisionMaxBytes,
		SourceRemote:       strings.TrimSpace(getenv(envSourceRemote)),
		SourceRepository:   valueOr(getenv(envSourceRepository), defaultSourceRepository),
		SourceFetchTimeout: defaultSourceFetchTimeout,
		StateFile:          valueOr(getenv(envStateFile), defaultStateFile),
		DockerBin:          valueOr(getenv(envDockerBin), defaultDockerBin),
	}
	if raw := strings.TrimSpace(getenv(envRevisionMaxEntries)); raw != "" {
		cfg.RevisionMaxEntries, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("parsing %s: %w", envRevisionMaxEntries, err)
		}
	}
	if raw := strings.TrimSpace(getenv(envRevisionMaxBytes)); raw != "" {
		cfg.RevisionMaxBytes, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("parsing %s: %w", envRevisionMaxBytes, err)
		}
	}
	if raw := strings.TrimSpace(getenv(envSourceFetchTimeout)); raw != "" {
		cfg.SourceFetchTimeout, err = time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("parsing %s: %w", envSourceFetchTimeout, err)
		}
	}

	if raw := strings.TrimSpace(getenv(envDryRun)); raw != "" {
		cfg.DryRun, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("parsing %s: %w", envDryRun, err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate rejects unsafe network and filesystem configuration.
func (c Config) Validate() error {
	host, port, err := net.SplitHostPort(c.Bind)
	if err != nil {
		return fmt.Errorf("invalid bind address %q: %w", c.Bind, err)
	}
	if port == "" {
		return fmt.Errorf("bind address %q has no port", c.Bind)
	}
	loopback := false
	if strings.EqualFold(host, "localhost") {
		loopback = true
	} else if ip := net.ParseIP(host); ip != nil {
		loopback = ip.IsLoopback()
	}
	if !loopback && strings.TrimSpace(c.Token) == "" {
		return fmt.Errorf("%s is required for non-loopback bind %q", envToken, c.Bind)
	}
	if !filepath.IsAbs(c.Workspace) {
		return fmt.Errorf("%s must be an absolute path", envWorkspace)
	}
	if !filepath.IsAbs(c.RevisionRoot) {
		return fmt.Errorf("%s must be an absolute path", envRevisionRoot)
	}
	if err := candaceosv1.ValidateRevisionLimits(&candaceosv1.RevisionLimits{
		MaxEntries: c.RevisionMaxEntries,
		MaxBytes:   c.RevisionMaxBytes,
	}); err != nil {
		return fmt.Errorf("%s or %s violates the protobuf contract: %w", envRevisionMaxEntries, envRevisionMaxBytes, err)
	}
	if err := candaceosv1.ValidateSourceSync(&candaceosv1.SourceSync{
		Remote: c.SourceRemote, Repository: c.SourceRepository,
		FetchTimeoutNanoseconds: int64(c.SourceFetchTimeout),
	}); err != nil {
		return fmt.Errorf("%s, %s, or %s violates the protobuf contract: %w", envSourceRemote, envSourceRepository, envSourceFetchTimeout, err)
	}
	if !filepath.IsAbs(c.SourceRepository) {
		return fmt.Errorf("%s must be an absolute path", envSourceRepository)
	}
	if err := candaceosv1.ValidateAgentStatus(&candaceosv1.AgentStatus{
		NodeId:    c.NodeID,
		Workspace: c.Workspace,
	}); err != nil {
		return fmt.Errorf("%s or %s violates the protobuf response contract: %w", envNodeID, envWorkspace, err)
	}
	if !filepath.IsAbs(c.StateFile) {
		return fmt.Errorf("%s must be an absolute path", envStateFile)
	}
	if strings.TrimSpace(c.DockerBin) == "" {
		return fmt.Errorf("%s must not be empty", envDockerBin)
	}
	return nil
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
