// Code generated from Candacefile by tools/candace_environment.py; DO NOT EDIT.

package config

import "google.golang.org/protobuf/reflect/protoreflect"

const (
	EnvironmentPostgresDatabase        = "POSTGRES_DB"
	DefaultPostgresDatabase            = "deploy"
	EnvironmentPostgresUser            = "POSTGRES_USER"
	DefaultPostgresUser                = "deploy"
	EnvironmentPostgresPassword        = "POSTGRES_PASSWORD"
	EnvironmentStateRoot               = "DEPLOY_STATE_ROOT"
	EnvironmentUID                     = "DEPLOY_UID"
	EnvironmentGID                     = "DEPLOY_GID"
	EnvironmentHostWorkspace           = "DEPLOY_HOST_WORKSPACE"
	EnvironmentAgentToken              = "NODEEXEC_TOKEN"
	EnvironmentCopilotConnectionToken  = "DEPLOY_COPILOT_CONNECTION_TOKEN"
	EnvironmentOpenCodePassword        = "DEPLOY_OPENCODE_PASSWORD"
	EnvironmentCopilotGitHubToken      = "COPILOT_GITHUB_TOKEN"
	DefaultCopilotGitHubToken          = ""
	EnvironmentOpenCodeModel           = "DEPLOY_OPENCODE_MODEL"
	DefaultOpenCodeModel               = ""
	EnvironmentAgentRevisionMaxEntries = "NODEEXEC_REVISION_MAX_ENTRIES"
	DefaultAgentRevisionMaxEntries     = "128"
	EnvironmentAgentRevisionMaxBytes   = "NODEEXEC_REVISION_MAX_BYTES"
	DefaultAgentRevisionMaxBytes       = "4294967296"
	EnvironmentCopilotBin              = "DEPLOY_COPILOT_BIN"
	EnvironmentCopilotSHA256           = "DEPLOY_COPILOT_SHA256"
	EnvironmentGHToken                 = "GH_TOKEN"
	EnvironmentGitHubToken             = "GITHUB_TOKEN"
	EnvironmentLiveConfirm             = "DEPLOY_LIVE_CONFIRM"
	EnvironmentLegacyMode              = "DEPLOY_MODE"
	EnvironmentOpenAIAPIKey            = "OPENAI_API_KEY"
	EnvironmentAnthropicAPIKey         = "ANTHROPIC_API_KEY"
	EnvironmentOpenRouterAPIKey        = "OPENROUTER_API_KEY"
	EnvironmentAgentBind               = "NODEEXEC_BIND"
	DefaultAgentBind                   = "0.0.0.0:8094"
	EnvironmentAgentNodeID             = "NODEEXEC_NODE_ID"
	DefaultAgentNodeID                 = "deploy-demo"
	EnvironmentAgentStateFile          = "NODEEXEC_STATE_FILE"
	DefaultAgentStateFile              = "/var/lib/nodeexec/state.json"
	EnvironmentAgentRevisionRoot       = "NODEEXEC_REVISION_ROOT"
	DefaultAgentRevisionRoot           = "/var/lib/nodeexec/revisions"
	EnvironmentDockerConfig            = "DOCKER_CONFIG"
	DefaultDockerConfig                = "/tmp/docker-config"
	EnvironmentAgentDryWorkspace       = "NODEEXEC_DRY_WORKSPACE"
	DefaultAgentDryWorkspace           = "/workspace"
	EnvironmentAgentLiveWorkspace      = "NODEEXEC_LIVE_WORKSPACE"
	DefaultAgentLiveWorkspace          = "/workspace"
	EnvironmentAgentDryRunEnabled      = "NODEEXEC_DRY_RUN_ENABLED"
	DefaultAgentDryRunEnabled          = "true"
	EnvironmentAgentDryRunDisabled     = "NODEEXEC_DRY_RUN_DISABLED"
	DefaultAgentDryRunDisabled         = "false"
	EnvironmentWardenConfig            = "WARDEN_CONFIG"
	DefaultWardenConfig                = "/etc/warden/warden.yaml"
	EnvironmentWardenLogFormat         = "WARDEN_LOG_FORMAT"
	DefaultWardenLogFormat             = "console"
	EnvironmentCopilotHome             = "DEPLOY_COPILOT_HOME"
	DefaultCopilotHome                 = "/var/lib/copilot"
	EnvironmentHarnessBackend          = "DEPLOY_HARNESS_BACKEND"
	DefaultHarnessBackend              = "copilot-cli"
	EnvironmentCoreBind                = "DEPLOY_BIND"
	DefaultCoreBind                    = "0.0.0.0:7780"
	EnvironmentCoreDataDir             = "DEPLOY_DATA_DIR"
	DefaultCoreDataDir                 = "/var/lib/deploy"
	EnvironmentCoreWorkspace           = "DEPLOY_WORKSPACE"
	DefaultCoreWorkspace               = "/workspace"
	EnvironmentCoreDatabaseURL         = "DEPLOY_DATABASE_URL"
	DefaultCoreDatabaseURL             = ""
	EnvironmentCoreWardenURL           = "DEPLOY_WARDEN_URL"
	DefaultCoreWardenURL               = "http://127.0.0.1:7717"
	EnvironmentCoreAgentURL            = "NODEEXEC_URL"
	DefaultCoreAgentURL                = ""
	EnvironmentCoreAgentPort           = "NODEEXEC_PORT"
	DefaultCoreAgentPort               = "8094"
	EnvironmentCoreNodeLabels          = "DEPLOY_NODE_LABELS"
	DefaultCoreNodeLabels              = "{}"
	EnvironmentCoreApprovalTimeout     = "DEPLOY_APPROVAL_TIMEOUT"
	DefaultCoreApprovalTimeout         = "15m"
	EnvironmentCoreFleetPollInterval   = "DEPLOY_FLEET_POLL_INTERVAL"
	DefaultCoreFleetPollInterval       = "2s"
	EnvironmentCopilotCLI              = "DEPLOY_COPILOT_CLI"
	DefaultCopilotCLI                  = "/usr/local/bin/copilot"
	EnvironmentCopilotURL              = "DEPLOY_COPILOT_URL"
	DefaultCopilotURL                  = ""
	EnvironmentCopilotModel            = "DEPLOY_COPILOT_MODEL"
	DefaultCopilotModel                = "gpt-5.4"
	EnvironmentOllamaURL               = "DEPLOY_OLLAMA_URL"
	DefaultOllamaURL                   = ""
	EnvironmentOllamaModel             = "DEPLOY_OLLAMA_MODEL"
	DefaultOllamaModel                 = ""
	EnvironmentOllamaModelDigest       = "DEPLOY_OLLAMA_MODEL_DIGEST"
	DefaultOllamaModelDigest           = ""
	EnvironmentOllamaContextTokens     = "DEPLOY_OLLAMA_CONTEXT_TOKENS"
	DefaultOllamaContextTokens         = "16384"
	EnvironmentOllamaMaxToolCalls      = "DEPLOY_OLLAMA_MAX_TOOL_CALLS"
	DefaultOllamaMaxToolCalls          = "16"
	EnvironmentOllamaTurnTimeout       = "DEPLOY_OLLAMA_TURN_TIMEOUT"
	DefaultOllamaTurnTimeout           = "10m"
	EnvironmentOpenCodeURL             = "DEPLOY_OPENCODE_URL"
	DefaultOpenCodeURL                 = "http://127.0.0.1:4096"
	EnvironmentOpenCodeUsername        = "DEPLOY_OPENCODE_USERNAME"
	DefaultOpenCodeUsername            = "opencode"
	EnvironmentOpenCodeSessionID       = "DEPLOY_OPENCODE_SESSION_ID"
	DefaultOpenCodeSessionID           = ""
	EnvironmentOpenCodeRequestTimeout  = "DEPLOY_OPENCODE_REQUEST_TIMEOUT"
	DefaultOpenCodeRequestTimeout      = "10s"
	EnvironmentOpenCodePollInterval    = "DEPLOY_OPENCODE_POLL_INTERVAL"
	DefaultOpenCodePollInterval        = "1s"
	EnvironmentOpenCodeQueueCapacity   = "DEPLOY_OPENCODE_QUEUE_CAPACITY"
	DefaultOpenCodeQueueCapacity       = "32"
	EnvironmentLiveConfirmPhrase       = "DEPLOY_LIVE_CONFIRM_PHRASE"
	DefaultLiveConfirmPhrase           = "I_UNDERSTAND_DOCKER_SOCKET_IS_ROOT"
	ProfileLocal                       = "local"
	ProfileLocalAgentRevisionRoot      = "{{state_root}}/revisions"
	ProfileLocalAgentLiveWorkspace     = "{{host_workspace}}"
	ProfileLocalCoreDatabaseURL        = "postgres://deploy:{{postgres_password}}@postgres:5432/deploy?sslmode=disable"
	ProfileLocalCoreWardenURL          = "http://warden:7717"
	ProfileLocalCoreAgentURL           = "http://nodeexec:8094"
	ProfileLocalCoreNodeLabels         = "{\"deploy-demo\":{\"environment\":\"prototype\",\"runtime\":\"compose\"}}"
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

// EnvironmentNames lists every environment variable the deploy
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
	"approval_timeout":         "DEPLOY_APPROVAL_TIMEOUT",
	"bind":                     "DEPLOY_BIND",
	"copilot_cli":              "DEPLOY_COPILOT_CLI",
	"copilot_connection_token": "DEPLOY_COPILOT_CONNECTION_TOKEN",
	"copilot_model":            "DEPLOY_COPILOT_MODEL",
	"copilot_url":              "DEPLOY_COPILOT_URL",
	"data_dir":                 "DEPLOY_DATA_DIR",
	"database_url":             "DEPLOY_DATABASE_URL",
	"fleet_poll_interval":      "DEPLOY_FLEET_POLL_INTERVAL",
	"harness_backend":          "DEPLOY_HARNESS_BACKEND",
	"node_labels":              "DEPLOY_NODE_LABELS",
	"nodeexec_port":            "NODEEXEC_PORT",
	"nodeexec_token":           "NODEEXEC_TOKEN",
	"nodeexec_url":             "NODEEXEC_URL",
	"warden_url":               "DEPLOY_WARDEN_URL",
	"workspace":                "DEPLOY_WORKSPACE",
}

var ollamaEnvironmentNames = map[protoreflect.Name]string{
	"context_tokens": "DEPLOY_OLLAMA_CONTEXT_TOKENS",
	"max_tool_calls": "DEPLOY_OLLAMA_MAX_TOOL_CALLS",
	"model":          "DEPLOY_OLLAMA_MODEL",
	"model_digest":   "DEPLOY_OLLAMA_MODEL_DIGEST",
	"turn_timeout":   "DEPLOY_OLLAMA_TURN_TIMEOUT",
	"url":            "DEPLOY_OLLAMA_URL",
}

var opencodeEnvironmentNames = map[protoreflect.Name]string{
	"model":           "DEPLOY_OPENCODE_MODEL",
	"password":        "DEPLOY_OPENCODE_PASSWORD",
	"poll_interval":   "DEPLOY_OPENCODE_POLL_INTERVAL",
	"queue_capacity":  "DEPLOY_OPENCODE_QUEUE_CAPACITY",
	"request_timeout": "DEPLOY_OPENCODE_REQUEST_TIMEOUT",
	"session_id":      "DEPLOY_OPENCODE_SESSION_ID",
	"url":             "DEPLOY_OPENCODE_URL",
	"username":        "DEPLOY_OPENCODE_USERNAME",
}
