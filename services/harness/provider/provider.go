// Copyright 2026 Candace Labs

// Package provider is the inference provider every session this host launches
// reaches its model through. It is structural rather than environment surgery:
// the operator records one router entry in providers.json under the state
// directory, and serve applies it on every start, so a restart or an upgrade
// keeps the switch.
//
// The entry names the provider's base URL, the file its API key is read from,
// the Haiku model its small-model calls run on, and a model map from CSF model
// names to the ordered provider spellings that serve them. A session's recipe
// keeps naming the CSF model, so the host's model policy and every recipe are
// unchanged by a provider switch; the mapping happens at launch.
//
// The key is read from its file at launch and reaches the turn executor as one
// environment variable of its process. It is never held in a record, a
// receipt, an event or a log line: [Router] carries the key's path, never its
// value, and only [Router.Launch] reads it.
package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/candacelabs/csf/pkg/privatefile"
)

// ProvidersFile is the operator's provider file under the harness state
// directory, shared with the other readers of its own top-level keys.
const ProvidersFile = "providers.json"

// The environment a session's turn executor reaches the provider through.
const (
	// EnvironmentBaseURL is the provider's endpoint.
	EnvironmentBaseURL = "ANTHROPIC_BASE_URL"
	// EnvironmentAPIKey is the key read from the entry's key file at launch.
	EnvironmentAPIKey = "ANTHROPIC_API_KEY"
	// EnvironmentHaikuModel is the provider's spelling of the small model the
	// executor runs its own cheap calls on.
	EnvironmentHaikuModel = "ANTHROPIC_DEFAULT_HAIKU_MODEL"
	// EnvironmentConfigDirectory points the executor at the harness-owned
	// configuration directory: a directory carrying no provider login, since a
	// login otherwise overrides the key.
	EnvironmentConfigDirectory = "CLAUDE_CONFIG_DIR"
	// EnvironmentMaxRetries bounds the executor's own retries of one request,
	// so an unavailable spelling reaches the fallback in seconds instead of
	// after the executor's ten escalating retries. Measured on 2026-10-05: the
	// default ten take about ten minutes to exhaust, one takes about two
	// seconds.
	EnvironmentMaxRetries = "CLAUDE_CODE_MAX_RETRIES"
)

// ConfigDirectory is the harness-owned executor configuration directory, under
// the state directory. Its projects/ is linked to the real history, so a
// conversation started before the switch resumes.
const ConfigDirectory = "router-claude-config"

// ProjectsDirectory is the conversation history inside an executor
// configuration directory.
const ProjectsDirectory = "projects"

// executorRetries is the bound [EnvironmentMaxRetries] carries: one attempt
// per spelling, because the fallback is CSF's retry.
const executorRetries = "1"

// maxProvidersBytes and maxKeyBytes bound the two files read.
const (
	maxProvidersBytes = 1 << 20
	maxKeyBytes       = 1 << 12
)

var (
	// ErrNoRouter reports a state directory recording no router entry, which
	// is not an error: the host runs on the executor's own configured
	// provider. Readers distinguish it with errors.Is.
	ErrNoRouter = errors.New("harness provider: no router entry is recorded")
	// ErrInvalidRouter reports a router entry that cannot be used as one.
	ErrInvalidRouter = errors.New("harness provider: the router entry is invalid")
	// ErrNoKey reports a key file the provider cannot read at launch.
	ErrNoKey = errors.New("harness provider: the router's key file cannot be read")
)

// RouterEntry is the "router" object of providers.json. Other top-level keys
// belong to other readers and are left untouched.
type RouterEntry struct {
	// BaseURL is the provider's endpoint, such as https://api.example.invalid.
	BaseURL string `json:"base_url"`
	// KeyFile holds the API key, owner-readable only. Relative to the state
	// directory unless absolute, so the operator's own path stays out of a
	// tracked file.
	KeyFile string `json:"key_file"`
	// HaikuModel is the provider's spelling of the small model.
	HaikuModel string `json:"haiku_model"`
	// ModelMap maps a CSF model name to the provider spellings that serve it,
	// in the order they are tried: the first that answers serves the turn, and
	// a later one is a fallback.
	ModelMap map[string][]string `json:"model_map"`
}

// providers is providers.json as this package reads it; every other key is
// another reader's.
type providers struct {
	Router *RouterEntry `json:"router"`
}

// Router is one host's inference provider, built from the recorded entry. It
// holds the key's path and never its value.
type Router struct {
	baseURL        string
	keyFile        string
	haikuModel     string
	modelMap       map[string][]string
	stateDirectory string
}

// ReadRouter reads the router entry recorded under stateDirectory. A state
// directory with no providers.json, or one whose providers.json carries no
// router entry, returns [ErrNoRouter]: the host then launches sessions on the
// executor's own provider, exactly as before this slice.
func ReadRouter(stateDirectory string) (*Router, error) {
	path := filepath.Join(stateDirectory, ProvidersFile)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: no %s", ErrNoRouter, ProvidersFile)
	}
	content, err := privatefile.Read(path, maxProvidersBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %w", ErrInvalidRouter, ProvidersFile, err)
	}
	var read providers
	if err := json.Unmarshal(content, &read); err != nil {
		return nil, fmt.Errorf("%w: decode %s: %w", ErrInvalidRouter, ProvidersFile, err)
	}
	if read.Router == nil {
		return nil, fmt.Errorf("%w: %s carries no router entry", ErrNoRouter, ProvidersFile)
	}
	return NewRouter(*read.Router, stateDirectory)
}

// NewRouter builds the router one entry describes. It checks the entry names
// an endpoint, a key file and at least one mapped model, so a host that cannot
// serve a turn says so at start rather than at the first launch.
func NewRouter(entry RouterEntry, stateDirectory string) (*Router, error) {
	switch {
	case strings.TrimSpace(entry.BaseURL) == "":
		return nil, fmt.Errorf("%w: router.base_url is empty", ErrInvalidRouter)
	case strings.TrimSpace(entry.KeyFile) == "":
		return nil, fmt.Errorf("%w: router.key_file is empty", ErrInvalidRouter)
	case len(entry.ModelMap) == 0:
		return nil, fmt.Errorf("%w: router.model_map is empty", ErrInvalidRouter)
	}
	mapped := make(map[string][]string, len(entry.ModelMap))
	for model, spellings := range entry.ModelMap {
		kept := make([]string, 0, len(spellings))
		for _, spelling := range spellings {
			if trimmed := strings.TrimSpace(spelling); trimmed != "" {
				kept = append(kept, trimmed)
			}
		}
		if len(kept) == 0 {
			return nil, fmt.Errorf("%w: router.model_map[%q] names no provider spelling", ErrInvalidRouter, model)
		}
		mapped[model] = kept
	}
	return &Router{
		baseURL:        strings.TrimSpace(entry.BaseURL),
		keyFile:        entry.KeyFile,
		haikuModel:     strings.TrimSpace(entry.HaikuModel),
		modelMap:       mapped,
		stateDirectory: stateDirectory,
	}, nil
}

// BaseURL is the provider's endpoint.
func (router *Router) BaseURL() string { return router.baseURL }

// Spellings are the provider spellings serving the CSF model name, in the
// order they are tried. A model the map does not name is served by its own
// name, so a host need only map the models whose spelling differs.
func (router *Router) Spellings(model string) []string {
	if mapped, found := router.modelMap[model]; found {
		return mapped
	}
	return []string{model}
}

// Attempts is how many spellings serve model: how many times a turn may be
// tried before the provider has nothing left to fall back to.
func (router *Router) Attempts(model string) int { return len(router.Spellings(model)) }

// keyPath is the key file's absolute path: as given when absolute, otherwise
// under the state directory, so no operator path is recorded in the entry.
func (router *Router) keyPath() string {
	if filepath.IsAbs(router.keyFile) {
		return router.keyFile
	}
	return filepath.Join(router.stateDirectory, router.keyFile)
}

// ConfigDirectoryPath is the harness-owned executor configuration directory.
func (router *Router) ConfigDirectoryPath() string {
	return filepath.Join(router.stateDirectory, ConfigDirectory)
}

// Launch is the environment one session's turn executor reaches the provider
// through, read at launch: the endpoint, the key read from its file now, the
// small model, the harness-owned configuration directory and the executor's
// retry bound. The key is in the returned slice and nowhere else; a caller
// that logs an environment logs the key, so no caller does.
func (router *Router) Launch() ([]string, error) {
	key, err := privatefile.Read(router.keyPath(), maxKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoKey, err)
	}
	trimmed := strings.TrimSpace(string(key))
	if trimmed == "" {
		return nil, fmt.Errorf("%w: the key file is empty", ErrNoKey)
	}
	environment := []string{
		EnvironmentBaseURL + "=" + router.baseURL,
		EnvironmentAPIKey + "=" + trimmed,
		EnvironmentConfigDirectory + "=" + router.ConfigDirectoryPath(),
		EnvironmentMaxRetries + "=" + executorRetries,
	}
	if router.haikuModel != "" {
		environment = append(environment, EnvironmentHaikuModel+"="+router.haikuModel)
	}
	return environment, nil
}
