// Package config resolves CandaceOS Core's environment into its canonical
// Liquid Proto contract.
//
// The package owns the environment vocabulary, the validation that rejects an
// unusable process environment outright, and the small accessors that project
// the resolved contract into the Go values the rest of Core consumes. The
// variable names and defaults in environment_generated.go are generated from
// the repository-root Candacefile by tools/candace_environment.py and are not
// written by hand.
//
// Callers may rely on Load and LoadForHarness returning a fully validated
// *candaceosv1.CoreConfig or an error, never a partially populated one, and on
// Loader keeping the environment source replaceable for embedding and tests
// without widening Core's configuration vocabulary. Resolution is pure: the
// package opens no connections, starts nothing, and never mutates the process
// environment.
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	configlib "github.com/candacelabs/csf/pkg/config"
	"google.golang.org/protobuf/reflect/protoreflect"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/harness"
)

const copilotStateDirectory = "copilot"

// Loader resolves one environment source into Core's canonical Liquid Proto
// contract. The concrete loader keeps process input replaceable for embedding
// and tests without expanding Core's configuration vocabulary.
type Loader struct {
	environment configlib.Environment
}

// NewLoader constructs a Core configuration loader over environment.
func NewLoader(environment configlib.Environment) Loader {
	return Loader{environment: environment}
}

// Load resolves the current process environment.
func Load() (*candaceosv1.CoreConfig, error) {
	return NewLoader(configlib.OSEnvironment()).Load()
}

// LoadForHarness resolves the current process environment for an implementation
// selected by the embedding binary. Provider-specific environment is loaded
// only for built-in harnesses selected through Load.
func LoadForHarness(backend candaceosv1.HarnessBackend) (*candaceosv1.CoreConfig, error) {
	return NewLoader(configlib.OSEnvironment()).LoadForHarness(backend)
}

// Load resolves the loader's environment-selected built-in harness.
func (loader Loader) Load() (*candaceosv1.CoreConfig, error) {
	backend, err := loader.harnessBackend()
	if err != nil {
		return nil, err
	}
	return loader.load(backend)
}

// LoadForHarness resolves the loader's environment around a compiled-in
// implementation. Environment cannot replace the supplied backend.
func (loader Loader) LoadForHarness(backend candaceosv1.HarnessBackend) (*candaceosv1.CoreConfig, error) {
	return loader.load(backend)
}

func (loader Loader) load(backend candaceosv1.HarnessBackend) (*candaceosv1.CoreConfig, error) {
	agentPort, err := loader.integer(EnvironmentCoreAgentPort, DefaultCoreAgentPort)
	if err != nil {
		return nil, err
	}
	approvalTimeout, err := loader.duration(EnvironmentCoreApprovalTimeout, DefaultCoreApprovalTimeout)
	if err != nil {
		return nil, err
	}
	fleetPollInterval, err := loader.duration(EnvironmentCoreFleetPollInterval, DefaultCoreFleetPollInterval)
	if err != nil {
		return nil, err
	}
	labels, err := loader.nodeLabels()
	if err != nil {
		return nil, err
	}

	resolved := &candaceosv1.CoreConfig{
		HarnessBackend:  backend,
		Bind:            loader.environment.String(EnvironmentCoreBind, DefaultCoreBind),
		DataDir:         loader.environment.String(EnvironmentCoreDataDir, DefaultCoreDataDir),
		Workspace:       loader.environment.String(EnvironmentCoreWorkspace, DefaultCoreWorkspace),
		DatabaseUrl:     loader.environment.String(EnvironmentCoreDatabaseURL, DefaultCoreDatabaseURL),
		WardenUrl:       loader.environment.String(EnvironmentCoreWardenURL, DefaultCoreWardenURL),
		AgentUrl:        loader.environment.String(EnvironmentCoreAgentURL, DefaultCoreAgentURL),
		AgentPort:       agentPort,
		AgentToken:      loader.environment.Raw(EnvironmentAgentToken),
		NodeLabels:      labels,
		ApprovalTimeout: int64(approvalTimeout),
		FleetPollInterval: &candaceosv1.PersistenceTiming{
			FleetPollIntervalNanoseconds: int64(fleetPollInterval),
		},
	}

	switch backend {
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI:
		loader.loadCopilot(resolved)
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA:
		resolved.Ollama, err = loader.loadOllama()
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_OPENCODE:
		resolved.Opencode, err = loader.loadOpenCode()
	}
	if err != nil {
		return nil, err
	}
	if err := Validate(resolved); err != nil {
		return nil, err
	}
	return resolved, nil
}

func (loader Loader) loadCopilot(resolved *candaceosv1.CoreConfig) {
	resolved.CopilotCli = loader.environment.String(EnvironmentCopilotCLI, DefaultCopilotCLI)
	resolved.CopilotUrl = loader.environment.String(EnvironmentCopilotURL, DefaultCopilotURL)
	resolved.CopilotConnectionToken = loader.environment.Raw(EnvironmentCopilotConnectionToken)
	resolved.CopilotModel = loader.environment.String(EnvironmentCopilotModel, DefaultCopilotModel)
	resolved.GithubToken = loader.environment.First(
		EnvironmentCopilotGitHubToken,
		EnvironmentGHToken,
		EnvironmentGitHubToken,
	)
}

func (loader Loader) loadOllama() (*candaceosv1.OllamaConfig, error) {
	contextTokens, err := loader.integer(EnvironmentOllamaContextTokens, DefaultOllamaContextTokens)
	if err != nil {
		return nil, err
	}
	maxToolCalls, err := loader.integer(EnvironmentOllamaMaxToolCalls, DefaultOllamaMaxToolCalls)
	if err != nil {
		return nil, err
	}
	turnTimeout, err := loader.duration(EnvironmentOllamaTurnTimeout, DefaultOllamaTurnTimeout)
	if err != nil {
		return nil, err
	}
	return &candaceosv1.OllamaConfig{
		Url:           loader.environment.String(EnvironmentOllamaURL, DefaultOllamaURL),
		Model:         loader.environment.String(EnvironmentOllamaModel, DefaultOllamaModel),
		ModelDigest:   loader.environment.String(EnvironmentOllamaModelDigest, DefaultOllamaModelDigest),
		ContextTokens: contextTokens,
		MaxToolCalls:  maxToolCalls,
		TurnTimeout:   int64(turnTimeout),
	}, nil
}

func (loader Loader) loadOpenCode() (*candaceosv1.OpenCodeConfig, error) {
	requestTimeout, err := loader.duration(EnvironmentOpenCodeRequestTimeout, DefaultOpenCodeRequestTimeout)
	if err != nil {
		return nil, err
	}
	pollInterval, err := loader.duration(EnvironmentOpenCodePollInterval, DefaultOpenCodePollInterval)
	if err != nil {
		return nil, err
	}
	queueCapacity, err := loader.integer(EnvironmentOpenCodeQueueCapacity, DefaultOpenCodeQueueCapacity)
	if err != nil {
		return nil, err
	}
	return &candaceosv1.OpenCodeConfig{
		Url:            loader.environment.String(EnvironmentOpenCodeURL, DefaultOpenCodeURL),
		Username:       loader.environment.String(EnvironmentOpenCodeUsername, DefaultOpenCodeUsername),
		Password:       loader.environment.Raw(EnvironmentOpenCodePassword),
		SessionId:      loader.environment.String(EnvironmentOpenCodeSessionID, DefaultOpenCodeSessionID),
		RequestTimeout: int64(requestTimeout),
		PollInterval:   int64(pollInterval),
		QueueCapacity:  queueCapacity,
		Model:          loader.environment.String(EnvironmentOpenCodeModel, DefaultOpenCodeModel),
	}, nil
}

func (loader Loader) duration(name, fallback string) (time.Duration, error) {
	defaultValue, err := time.ParseDuration(fallback)
	if err != nil {
		return 0, fmt.Errorf("Candacefile default for %s: %w", name, err)
	}
	return loader.environment.Duration(name, defaultValue)
}

func (loader Loader) integer(name, fallback string) (int32, error) {
	defaultValue, err := strconv.ParseInt(fallback, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("Candacefile default for %s: %w", name, err)
	}
	return loader.environment.Int32(name, int32(defaultValue))
}

func (loader Loader) nodeLabels() ([]*candaceosv1.Node, error) {
	raw := loader.environment.String(EnvironmentCoreNodeLabels, DefaultCoreNodeLabels)
	var labelsByNode map[string]map[string]string
	if err := json.Unmarshal([]byte(raw), &labelsByNode); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object of node label objects: %w", EnvironmentCoreNodeLabels, err)
	}
	nodeIDs := make([]string, 0, len(labelsByNode))
	for nodeID := range labelsByNode {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	nodes := make([]*candaceosv1.Node, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		nodes = append(nodes, &candaceosv1.Node{Id: nodeID, Labels: labelsByNode[nodeID]})
	}
	return nodes, nil
}

// Validate applies generated Liquid predicates first, then the OS, URL, and
// cross-field checks that the refinement grammar intentionally does not own.
func Validate(resolved *candaceosv1.CoreConfig) error {
	if err := candaceosv1.ValidateCoreConfig(resolved); err != nil {
		return labelRefinement(resolved, coreEnvironmentNames, err)
	}
	if !filepath.IsAbs(resolved.GetDataDir()) {
		return fmt.Errorf("%s must be an absolute path", EnvironmentCoreDataDir)
	}
	if !filepath.IsAbs(resolved.GetWorkspace()) {
		return fmt.Errorf("%s must be an absolute path", EnvironmentCoreWorkspace)
	}
	if _, err := url.ParseRequestURI(resolved.GetWardenUrl()); err != nil {
		return fmt.Errorf("%s: %w", EnvironmentCoreWardenURL, err)
	}
	if resolved.GetAgentUrl() != "" {
		if _, err := url.ParseRequestURI(resolved.GetAgentUrl()); err != nil {
			return fmt.Errorf("%s: %w", EnvironmentCoreAgentURL, err)
		}
	}
	if err := candaceosv1.ValidatePersistenceTiming(resolved.GetFleetPollInterval()); err != nil {
		return fmt.Errorf("%s: %w", EnvironmentCoreFleetPollInterval, err)
	}
	if err := validateNodeLabels(resolved.GetNodeLabels()); err != nil {
		return err
	}
	if _, _, err := net.SplitHostPort(resolved.GetBind()); err != nil {
		return fmt.Errorf("%s must be host:port: %w", EnvironmentCoreBind, err)
	}

	switch resolved.GetHarnessBackend() {
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI:
		return validateCopilot(resolved)
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA:
		return validateOllama(resolved.GetOllama())
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_OPENCODE:
		return validateOpenCode(resolved.GetOpencode())
	default:
		return nil
	}
}

func validateNodeLabels(nodes []*candaceosv1.Node) error {
	seenNodeIDs := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if err := candaceosv1.ValidateNode(node); err != nil {
			return fmt.Errorf("%s: %w", EnvironmentCoreNodeLabels, err)
		}
		if len(node.GetLabels()) == 0 {
			return fmt.Errorf("%s must give every node at least one label", EnvironmentCoreNodeLabels)
		}
		if _, duplicate := seenNodeIDs[node.GetId()]; duplicate {
			return fmt.Errorf("%s contains duplicate node %q", EnvironmentCoreNodeLabels, node.GetId())
		}
		seenNodeIDs[node.GetId()] = struct{}{}
	}
	return nil
}

func validateCopilot(resolved *candaceosv1.CoreConfig) error {
	if resolved.GetCopilotUrl() == "" && resolved.GetCopilotCli() == "" {
		return fmt.Errorf("%s or %s is required for the %s harness",
			EnvironmentCopilotCLI, EnvironmentCopilotURL, HarnessBackendName(resolved.GetHarnessBackend()))
	}
	if resolved.GetCopilotUrl() == "" {
		return nil
	}
	if _, err := url.ParseRequestURI(resolved.GetCopilotUrl()); err != nil {
		return fmt.Errorf("%s: %w", EnvironmentCopilotURL, err)
	}
	if resolved.GetCopilotConnectionToken() == "" {
		return fmt.Errorf("%s is required with %s", EnvironmentCopilotConnectionToken, EnvironmentCopilotURL)
	}
	return nil
}

func validateOllama(resolved *candaceosv1.OllamaConfig) error {
	if resolved == nil {
		return fmt.Errorf("%s is required for the %s harness",
			EnvironmentOllamaURL, HarnessBackendName(candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA))
	}
	if err := candaceosv1.ValidateOllamaConfig(resolved); err != nil {
		return labelRefinement(resolved, ollamaEnvironmentNames, err)
	}
	if _, err := url.ParseRequestURI(resolved.GetUrl()); err != nil {
		return fmt.Errorf("%s: %w", EnvironmentOllamaURL, err)
	}
	return nil
}

func validateOpenCode(resolved *candaceosv1.OpenCodeConfig) error {
	if resolved == nil {
		return fmt.Errorf("%s is required for the %s harness",
			EnvironmentOpenCodeURL, HarnessBackendName(candaceosv1.HarnessBackend_HARNESS_BACKEND_OPENCODE))
	}
	if err := candaceosv1.ValidateOpenCodeConfig(resolved); err != nil {
		return labelRefinement(resolved, opencodeEnvironmentNames, err)
	}
	if err := configlib.ValidatePrivateOrigin(resolved.GetUrl()); err != nil {
		return fmt.Errorf("%s: %w", EnvironmentOpenCodeURL, err)
	}
	if _, err := configlib.ParseProviderModel(resolved.GetModel()); err != nil {
		return fmt.Errorf("%s: %w", EnvironmentOpenCodeModel, err)
	}
	return nil
}

func labelRefinement(
	message protoreflect.ProtoMessage,
	names map[protoreflect.Name]string,
	err error,
) error {
	return configlib.LabelRefinementError(message, func(field protoreflect.FieldDescriptor) string {
		if name := names[field.Name()]; name != "" {
			return name
		}
		return string(field.FullName())
	}, err)
}

// NodeLabels projects the canonical node contracts into the map consumed by
// the current fleet and reconciliation adapters. The returned maps are copies.
func NodeLabels(resolved *candaceosv1.CoreConfig) map[string]map[string]string {
	labelsByNode := make(map[string]map[string]string, len(resolved.GetNodeLabels()))
	for _, node := range resolved.GetNodeLabels() {
		labels := make(map[string]string, len(node.GetLabels()))
		for key, value := range node.GetLabels() {
			labels[key] = value
		}
		labelsByNode[node.GetId()] = labels
	}
	return labelsByNode
}

// CopilotHome is the Copilot adapter's private state directory inside the
// resolved data directory. It is never shared with another harness backend.
func CopilotHome(resolved *candaceosv1.CoreConfig) string {
	return filepath.Join(resolved.GetDataDir(), copilotStateDirectory)
}

// ApprovalTimeout is how long a pending approval may wait before the queue
// fails it closed.
func ApprovalTimeout(resolved *candaceosv1.CoreConfig) time.Duration {
	return time.Duration(resolved.GetApprovalTimeout())
}

// FleetPollInterval is how often Core refreshes its Warden membership view.
func FleetPollInterval(resolved *candaceosv1.CoreConfig) time.Duration {
	return time.Duration(resolved.GetFleetPollInterval().GetFleetPollIntervalNanoseconds())
}

// HarnessBackendName is the environment spelling of a harness backend enum,
// the value CANDACEOS_HARNESS_BACKEND accepts.
func HarnessBackendName(backend candaceosv1.HarnessBackend) string {
	return harness.BackendName(backend)
}

func (loader Loader) harnessBackend() (candaceosv1.HarnessBackend, error) {
	canonical := loader.environment.Raw(EnvironmentHarnessBackend)
	legacy := loader.environment.Raw(EnvironmentLegacyMode)
	if canonical == "" && legacy == "" {
		canonical = DefaultHarnessBackend
	}
	if canonical == "" {
		return parseLegacyHarnessBackend(legacy)
	}
	backend, err := parseHarnessBackend(canonical)
	if err != nil {
		return candaceosv1.HarnessBackend_HARNESS_BACKEND_UNSPECIFIED,
			fmt.Errorf("%s must be one of %s", EnvironmentHarnessBackend, strings.Join(harnessBackendChoices(), ", "))
	}
	if legacy == "" {
		return backend, nil
	}
	legacyBackend, err := parseLegacyHarnessBackend(legacy)
	if err != nil {
		return candaceosv1.HarnessBackend_HARNESS_BACKEND_UNSPECIFIED, err
	}
	if legacyBackend != backend {
		return candaceosv1.HarnessBackend_HARNESS_BACKEND_UNSPECIFIED,
			fmt.Errorf("%s conflicts with legacy %s", EnvironmentHarnessBackend, EnvironmentLegacyMode)
	}
	return backend, nil
}

func parseHarnessBackend(value string) (candaceosv1.HarnessBackend, error) {
	name := protoreflect.Name("HARNESS_BACKEND_" + strings.ToUpper(strings.ReplaceAll(value, "-", "_")))
	descriptor := candaceosv1.HarnessBackend(0).Descriptor().Values().ByName(name)
	if descriptor == nil || descriptor.Number() == 0 {
		return candaceosv1.HarnessBackend_HARNESS_BACKEND_UNSPECIFIED, fmt.Errorf("unknown harness backend %q", value)
	}
	backend := candaceosv1.HarnessBackend(descriptor.Number())
	if backend == candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED {
		return candaceosv1.HarnessBackend_HARNESS_BACKEND_UNSPECIFIED, fmt.Errorf("unknown harness backend %q", value)
	}
	return backend, nil
}

func harnessBackendChoices() []string {
	values := candaceosv1.HarnessBackend(0).Descriptor().Values()
	choices := make([]string, 0, values.Len()-2)
	for index := range values.Len() {
		backend := candaceosv1.HarnessBackend(values.Get(index).Number())
		if backend == candaceosv1.HarnessBackend_HARNESS_BACKEND_UNSPECIFIED ||
			backend == candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED {
			continue
		}
		choices = append(choices, strconv.Quote(HarnessBackendName(backend)))
	}
	sort.Strings(choices)
	return choices
}

func parseLegacyHarnessBackend(value string) (candaceosv1.HarnessBackend, error) {
	switch value {
	case ProfileDemo:
		return candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, nil
	case ProfileCopilot:
		return candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI, nil
	default:
		return candaceosv1.HarnessBackend_HARNESS_BACKEND_UNSPECIFIED,
			fmt.Errorf("%s must be %q or %q", EnvironmentLegacyMode, ProfileDemo, ProfileCopilot)
	}
}
