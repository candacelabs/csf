// Code generated from Candacefile by tools/candace_environment.py; DO NOT EDIT.

package config

import "google.golang.org/protobuf/reflect/protoreflect"

const (
	EnvironmentPostgresDatabase        = "POSTGRES_DB"
	DefaultPostgresDatabase            = "candaceos"
	EnvironmentPostgresUser            = "POSTGRES_USER"
	DefaultPostgresUser                = "candaceos"
	EnvironmentPostgresPassword        = "POSTGRES_PASSWORD"
	EnvironmentStateRoot               = "CANDACEOS_STATE_ROOT"
	EnvironmentUID                     = "CANDACEOS_UID"
	EnvironmentGID                     = "CANDACEOS_GID"
	EnvironmentHostWorkspace           = "CANDACEOS_HOST_WORKSPACE"
	EnvironmentAgentToken              = "CANDACEOS_AGENT_TOKEN"
	EnvironmentCopilotConnectionToken  = "CANDACEOS_COPILOT_CONNECTION_TOKEN"
	EnvironmentOpenCodePassword        = "CANDACEOS_OPENCODE_PASSWORD"
	EnvironmentCopilotGitHubToken      = "COPILOT_GITHUB_TOKEN"
	DefaultCopilotGitHubToken          = ""
	EnvironmentOpenCodeModel           = "CANDACEOS_OPENCODE_MODEL"
	DefaultOpenCodeModel               = ""
	EnvironmentAgentRevisionMaxEntries = "CANDACEOS_AGENT_REVISION_MAX_ENTRIES"
	DefaultAgentRevisionMaxEntries     = "128"
	EnvironmentAgentRevisionMaxBytes   = "CANDACEOS_AGENT_REVISION_MAX_BYTES"
	DefaultAgentRevisionMaxBytes       = "4294967296"
	EnvironmentCopilotBin              = "CANDACEOS_COPILOT_BIN"
	EnvironmentCopilotSHA256           = "CANDACEOS_COPILOT_SHA256"
	EnvironmentGHToken                 = "GH_TOKEN"
	EnvironmentGitHubToken             = "GITHUB_TOKEN"
	EnvironmentLiveConfirm             = "CANDACEOS_LIVE_CONFIRM"
	EnvironmentLegacyMode              = "CANDACEOS_MODE"
	EnvironmentOpenAIAPIKey            = "OPENAI_API_KEY"
	EnvironmentAnthropicAPIKey         = "ANTHROPIC_API_KEY"
	EnvironmentOpenRouterAPIKey        = "OPENROUTER_API_KEY"
	EnvironmentAgentBind               = "CANDACEOS_AGENT_BIND"
	DefaultAgentBind                   = "0.0.0.0:8094"
	EnvironmentAgentNodeID             = "CANDACEOS_AGENT_NODE_ID"
	DefaultAgentNodeID                 = "candaceos-demo"
	EnvironmentAgentStateFile          = "CANDACEOS_AGENT_STATE_FILE"
	DefaultAgentStateFile              = "/var/lib/candaceos-agent/state.json"
	EnvironmentAgentRevisionRoot       = "CANDACEOS_AGENT_REVISION_ROOT"
	DefaultAgentRevisionRoot           = "/var/lib/candaceos-agent/revisions"
	EnvironmentDockerConfig            = "DOCKER_CONFIG"
	DefaultDockerConfig                = "/tmp/docker-config"
	EnvironmentAgentDryWorkspace       = "CANDACEOS_AGENT_DRY_WORKSPACE"
	DefaultAgentDryWorkspace           = "/workspace"
	EnvironmentAgentLiveWorkspace      = "CANDACEOS_AGENT_LIVE_WORKSPACE"
	DefaultAgentLiveWorkspace          = "/workspace"
	EnvironmentAgentDryRunEnabled      = "CANDACEOS_AGENT_DRY_RUN_ENABLED"
	DefaultAgentDryRunEnabled          = "true"
	EnvironmentAgentDryRunDisabled     = "CANDACEOS_AGENT_DRY_RUN_DISABLED"
	DefaultAgentDryRunDisabled         = "false"
	EnvironmentWardenConfig            = "WARDEN_CONFIG"
	DefaultWardenConfig                = "/etc/warden/warden.yaml"
	EnvironmentWardenLogFormat         = "WARDEN_LOG_FORMAT"
	DefaultWardenLogFormat             = "console"
	EnvironmentCopilotHome             = "CANDACEOS_COPILOT_HOME"
	DefaultCopilotHome                 = "/var/lib/copilot"
	EnvironmentHarnessBackend          = "CANDACEOS_HARNESS_BACKEND"
	DefaultHarnessBackend              = "copilot-cli"
	EnvironmentCoreBind                = "CANDACEOS_BIND"
	DefaultCoreBind                    = "0.0.0.0:7780"
	EnvironmentCoreDataDir             = "CANDACEOS_DATA_DIR"
	DefaultCoreDataDir                 = "/var/lib/candaceos"
	EnvironmentCoreWorkspace           = "CANDACEOS_WORKSPACE"
	DefaultCoreWorkspace               = "/workspace"
	EnvironmentCoreDatabaseURL         = "CANDACEOS_DATABASE_URL"
	DefaultCoreDatabaseURL             = ""
	EnvironmentCoreWardenURL           = "CANDACEOS_WARDEN_URL"
	DefaultCoreWardenURL               = "http://127.0.0.1:7717"
	EnvironmentCoreAgentURL            = "CANDACEOS_AGENT_URL"
	DefaultCoreAgentURL                = ""
	EnvironmentCoreAgentPort           = "CANDACEOS_AGENT_PORT"
	DefaultCoreAgentPort               = "8094"
	EnvironmentCoreNodeLabels          = "CANDACEOS_NODE_LABELS"
	DefaultCoreNodeLabels              = "{}"
	EnvironmentCoreApprovalTimeout     = "CANDACEOS_APPROVAL_TIMEOUT"
	DefaultCoreApprovalTimeout         = "15m"
	EnvironmentCoreFleetPollInterval   = "CANDACEOS_FLEET_POLL_INTERVAL"
	DefaultCoreFleetPollInterval       = "2s"
	EnvironmentCopilotCLI              = "CANDACEOS_COPILOT_CLI"
	DefaultCopilotCLI                  = "/usr/local/bin/copilot"
	EnvironmentCopilotURL              = "CANDACEOS_COPILOT_URL"
	DefaultCopilotURL                  = ""
	EnvironmentCopilotModel            = "CANDACEOS_COPILOT_MODEL"
	DefaultCopilotModel                = "gpt-5.4"
	EnvironmentOllamaURL               = "CANDACEOS_OLLAMA_URL"
	DefaultOllamaURL                   = ""
	EnvironmentOllamaModel             = "CANDACEOS_OLLAMA_MODEL"
	DefaultOllamaModel                 = ""
	EnvironmentOllamaModelDigest       = "CANDACEOS_OLLAMA_MODEL_DIGEST"
	DefaultOllamaModelDigest           = ""
	EnvironmentOllamaContextTokens     = "CANDACEOS_OLLAMA_CONTEXT_TOKENS"
	DefaultOllamaContextTokens         = "16384"
	EnvironmentOllamaMaxToolCalls      = "CANDACEOS_OLLAMA_MAX_TOOL_CALLS"
	DefaultOllamaMaxToolCalls          = "16"
	EnvironmentOllamaTurnTimeout       = "CANDACEOS_OLLAMA_TURN_TIMEOUT"
	DefaultOllamaTurnTimeout           = "10m"
	EnvironmentOpenCodeURL             = "CANDACEOS_OPENCODE_URL"
	DefaultOpenCodeURL                 = "http://127.0.0.1:4096"
	EnvironmentOpenCodeUsername        = "CANDACEOS_OPENCODE_USERNAME"
	DefaultOpenCodeUsername            = "opencode"
	EnvironmentOpenCodeSessionID       = "CANDACEOS_OPENCODE_SESSION_ID"
	DefaultOpenCodeSessionID           = ""
	EnvironmentOpenCodeRequestTimeout  = "CANDACEOS_OPENCODE_REQUEST_TIMEOUT"
	DefaultOpenCodeRequestTimeout      = "10s"
	EnvironmentOpenCodePollInterval    = "CANDACEOS_OPENCODE_POLL_INTERVAL"
	DefaultOpenCodePollInterval        = "1s"
	EnvironmentOpenCodeQueueCapacity   = "CANDACEOS_OPENCODE_QUEUE_CAPACITY"
	DefaultOpenCodeQueueCapacity       = "32"
	EnvironmentLiveConfirmPhrase       = "CANDACEOS_LIVE_CONFIRM_PHRASE"
	DefaultLiveConfirmPhrase           = "I_UNDERSTAND_DOCKER_SOCKET_IS_ROOT"
	ProfileLocal                       = "local"
	ProfileLocalAgentRevisionRoot      = "{{state_root}}/revisions"
	ProfileLocalAgentLiveWorkspace     = "{{host_workspace}}"
	ProfileLocalCoreDatabaseURL        = "postgres://candaceos:{{postgres_password}}@postgres:5432/candaceos?sslmode=disable"
	ProfileLocalCoreWardenURL          = "http://warden:7717"
	ProfileLocalCoreAgentURL           = "http://candaceos-agent:8094"
	ProfileLocalCoreNodeLabels         = "{\"candaceos-demo\":{\"environment\":\"prototype\",\"runtime\":\"compose\"}}"
	ProfileDemo                        = "demo"
	ProfileDemoHarnessBackend          = "demo"
	ProfileDemoCopilotURL              = ""
	ProfileDemoOpenCodeURL             = ""
	ProfileCopilot                     = "copilot"
	ProfileCopilotHarnessBackend       = "copilot-cli"
	ProfileCopilotCopilotURL           = "http://copilot:4321"
	ProfileCopilotOpenCodeURL          = ""
	ProfileOpenCode                    = "opencode"
	ProfileOpenCodeHarnessBackend      = "opencode"
	ProfileOpenCodeCopilotURL          = ""
	ProfileOpenCodeOpenCodeURL         = "http://opencode:4096"
)

// EnvironmentNames lists every environment variable the CandaceOS
// environment declares, in Candacefile order.
var EnvironmentNames = [...]string{
	EnvironmentPostgresDatabase,
	EnvironmentPostgresUser,
	EnvironmentPostgresPassword,
	EnvironmentStateRoot,
	EnvironmentUID,
	EnvironmentGID,
	EnvironmentHostWorkspace,
	EnvironmentAgentToken,
	EnvironmentCopilotConnectionToken,
	EnvironmentOpenCodePassword,
	EnvironmentCopilotGitHubToken,
	EnvironmentOpenCodeModel,
	EnvironmentAgentRevisionMaxEntries,
	EnvironmentAgentRevisionMaxBytes,
	EnvironmentCopilotBin,
	EnvironmentCopilotSHA256,
	EnvironmentGHToken,
	EnvironmentGitHubToken,
	EnvironmentLiveConfirm,
	EnvironmentLegacyMode,
	EnvironmentOpenAIAPIKey,
	EnvironmentAnthropicAPIKey,
	EnvironmentOpenRouterAPIKey,
	EnvironmentAgentBind,
	EnvironmentAgentNodeID,
	EnvironmentAgentStateFile,
	EnvironmentAgentRevisionRoot,
	EnvironmentDockerConfig,
	EnvironmentAgentDryWorkspace,
	EnvironmentAgentLiveWorkspace,
	EnvironmentAgentDryRunEnabled,
	EnvironmentAgentDryRunDisabled,
	EnvironmentWardenConfig,
	EnvironmentWardenLogFormat,
	EnvironmentCopilotHome,
	EnvironmentHarnessBackend,
	EnvironmentCoreBind,
	EnvironmentCoreDataDir,
	EnvironmentCoreWorkspace,
	EnvironmentCoreDatabaseURL,
	EnvironmentCoreWardenURL,
	EnvironmentCoreAgentURL,
	EnvironmentCoreAgentPort,
	EnvironmentCoreNodeLabels,
	EnvironmentCoreApprovalTimeout,
	EnvironmentCoreFleetPollInterval,
	EnvironmentCopilotCLI,
	EnvironmentCopilotURL,
	EnvironmentCopilotModel,
	EnvironmentOllamaURL,
	EnvironmentOllamaModel,
	EnvironmentOllamaModelDigest,
	EnvironmentOllamaContextTokens,
	EnvironmentOllamaMaxToolCalls,
	EnvironmentOllamaTurnTimeout,
	EnvironmentOpenCodeURL,
	EnvironmentOpenCodeUsername,
	EnvironmentOpenCodeSessionID,
	EnvironmentOpenCodeRequestTimeout,
	EnvironmentOpenCodePollInterval,
	EnvironmentOpenCodeQueueCapacity,
	EnvironmentLiveConfirmPhrase,
}

var coreEnvironmentNames = map[protoreflect.Name]string{
	"agent_port":               "CANDACEOS_AGENT_PORT",
	"agent_token":              "CANDACEOS_AGENT_TOKEN",
	"agent_url":                "CANDACEOS_AGENT_URL",
	"approval_timeout":         "CANDACEOS_APPROVAL_TIMEOUT",
	"bind":                     "CANDACEOS_BIND",
	"copilot_cli":              "CANDACEOS_COPILOT_CLI",
	"copilot_connection_token": "CANDACEOS_COPILOT_CONNECTION_TOKEN",
	"copilot_model":            "CANDACEOS_COPILOT_MODEL",
	"copilot_url":              "CANDACEOS_COPILOT_URL",
	"data_dir":                 "CANDACEOS_DATA_DIR",
	"database_url":             "CANDACEOS_DATABASE_URL",
	"fleet_poll_interval":      "CANDACEOS_FLEET_POLL_INTERVAL",
	"harness_backend":          "CANDACEOS_HARNESS_BACKEND",
	"node_labels":              "CANDACEOS_NODE_LABELS",
	"warden_url":               "CANDACEOS_WARDEN_URL",
	"workspace":                "CANDACEOS_WORKSPACE",
}

var ollamaEnvironmentNames = map[protoreflect.Name]string{
	"context_tokens": "CANDACEOS_OLLAMA_CONTEXT_TOKENS",
	"max_tool_calls": "CANDACEOS_OLLAMA_MAX_TOOL_CALLS",
	"model":          "CANDACEOS_OLLAMA_MODEL",
	"model_digest":   "CANDACEOS_OLLAMA_MODEL_DIGEST",
	"turn_timeout":   "CANDACEOS_OLLAMA_TURN_TIMEOUT",
	"url":            "CANDACEOS_OLLAMA_URL",
}

var opencodeEnvironmentNames = map[protoreflect.Name]string{
	"model":           "CANDACEOS_OPENCODE_MODEL",
	"password":        "CANDACEOS_OPENCODE_PASSWORD",
	"poll_interval":   "CANDACEOS_OPENCODE_POLL_INTERVAL",
	"queue_capacity":  "CANDACEOS_OPENCODE_QUEUE_CAPACITY",
	"request_timeout": "CANDACEOS_OPENCODE_REQUEST_TIMEOUT",
	"session_id":      "CANDACEOS_OPENCODE_SESSION_ID",
	"url":             "CANDACEOS_OPENCODE_URL",
	"username":        "CANDACEOS_OPENCODE_USERNAME",
}
