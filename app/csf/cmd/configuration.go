package main

import (
	"flag"
	"fmt"
)

const (
	databaseConfigFlag                 = "database-config"
	artifactsFlag                      = "artifacts"
	eventsFlag                         = "events"
	listenFlag                         = "listen"
	workFlag                           = "work-state"
	receiptsFlag                       = "receipts"
	originFlag                         = "origin"
	searchURLFlag                      = "search-url"
	searchIndexFlag                    = "search-index"
	embeddingModelFlag                 = "embedding-model"
	consumerRootFlag                   = "consumer-root"
	consumerRevisionFlag               = "consumer-revision"
	copilotHistorySourceFlag           = "copilot-history-source"
	workbenchDatabaseFlag              = "workbench-database-config"
	workbenchRepositoryFlag            = "workbench-repository"
	workbenchWorktreesFlag             = "workbench-worktrees"
	workbenchUIFlag                    = "workbench-ui"
	workbenchThemeDirectoryFlag        = "workbench-theme-dir"
	workbenchTraceConfigFlag           = "workbench-trace-config"
	workbenchTokenFlag                 = "workbench-token-file"
	localSimulationConfigFlag          = "local-simulation-config"
	simulationConfigFlag               = "simulation-config"
	environmentWatchRoot               = "CSF_WATCH_ROOT"
	environmentDatabaseConfig          = "CSF_DATABASE_CONFIG"
	environmentArtifacts               = "CSF_ARTIFACTS"
	environmentEvents                  = "CSF_EVENTS"
	environmentListen                  = "CSF_LISTEN"
	environmentWork                    = "CSF_WORK_STATE"
	environmentReceipts                = "CSF_RECEIPTS"
	environmentOrigin                  = "CSF_ORIGIN"
	environmentSearchURL               = "CSF_SEARCH_URL"
	environmentSearchIndex             = "CSF_SEARCH_INDEX"
	environmentEmbeddingModel          = "CSF_EMBEDDING_MODEL"
	environmentConsumerRoot            = "CSF_CONSUMER_ROOT"
	environmentConsumerRevision        = "CSF_CONSUMER_REVISION"
	environmentCopilotHistorySource    = "CSF_COPILOT_HISTORY_SOURCE"
	environmentWorkbenchDatabase       = "CSF_WORKBENCH_DATABASE_CONFIG"
	environmentWorkbenchRepository     = "CSF_WORKBENCH_REPOSITORY"
	environmentWorkbenchWorktrees      = "CSF_WORKBENCH_WORKTREES"
	environmentWorkbenchUI             = "CSF_WORKBENCH_UI"
	environmentWorkbenchThemeDirectory = "CSF_WORKBENCH_THEME_DIR"
	environmentWorkbenchTraceConfig    = "CSF_WORKBENCH_TRACE_CONFIG"
	environmentWorkbenchToken          = "CSF_WORKBENCH_TOKEN_FILE"
	environmentAgentMCPKeyFile         = "CSF_AGENT_MCP_KEY_FILE"
	environmentOperatorEmailConfig     = "CSF_OPERATOR_EMAIL_CONFIG"
	environmentLocalSimulationConfig   = "CSF_LOCAL_SIMULATION_CONFIG"
	environmentSimulationConfig        = "CSF_SIMULATION_CONFIG"
	defaultServeArtifacts              = "artifacts/cas"
	defaultServeEvents                 = "events.jsonl"
	defaultServeListen                 = "127.0.0.1:14111"
	defaultServeOrigin                 = "http://127.0.0.1:14111"
	defaultServeSearchURL              = "http://127.0.0.1:19200"
	defaultServeSearchIndex            = "brain-knowledge"
)

type serveConfig struct {
	watchRoot               string
	databaseConfig          string
	artifactsPath           string
	events                  string
	listen                  string
	work                    string
	receipts                string
	origin                  string
	searchURL               string
	searchIndex             string
	embeddingModel          string
	consumerRoot            string
	consumerRevision        string
	copilotHistorySource    string
	workbenchDatabase       string
	workbenchRepository     string
	workbenchWorktrees      string
	workbenchUI             string
	workbenchThemeDirectory string
	workbenchTraceConfig    string
	workbenchToken          string
	agentMCPKeyFile         string
	emailConfiguration      string
	localSimulationConfig   string
	simulationConfig        string
}

type serveEnvironmentValue struct {
	name   string
	target *string
}

func newServeConfig(lookup func(name string) (value string, found bool)) serveConfig {
	config := serveConfig{
		artifactsPath: defaultServeArtifacts,
		events:        defaultServeEvents,
		listen:        defaultServeListen,
		origin:        defaultServeOrigin,
		searchURL:     defaultServeSearchURL,
		searchIndex:   defaultServeSearchIndex,
	}
	for _, environment := range config.environmentValues() {
		if value, found := lookup(environment.name); found {
			*environment.target = value
		}
	}
	return config
}

func (config *serveConfig) environmentValues() []serveEnvironmentValue {
	return []serveEnvironmentValue{
		{name: environmentWatchRoot, target: &config.watchRoot},
		{name: environmentDatabaseConfig, target: &config.databaseConfig},
		{name: environmentArtifacts, target: &config.artifactsPath},
		{name: environmentEvents, target: &config.events},
		{name: environmentListen, target: &config.listen},
		{name: environmentWork, target: &config.work},
		{name: environmentReceipts, target: &config.receipts},
		{name: environmentOrigin, target: &config.origin},
		{name: environmentSearchURL, target: &config.searchURL},
		{name: environmentSearchIndex, target: &config.searchIndex},
		{name: environmentEmbeddingModel, target: &config.embeddingModel},
		{name: environmentConsumerRoot, target: &config.consumerRoot},
		{name: environmentConsumerRevision, target: &config.consumerRevision},
		{name: environmentCopilotHistorySource, target: &config.copilotHistorySource},
		{name: environmentWorkbenchDatabase, target: &config.workbenchDatabase},
		{name: environmentWorkbenchRepository, target: &config.workbenchRepository},
		{name: environmentWorkbenchWorktrees, target: &config.workbenchWorktrees},
		{name: environmentWorkbenchUI, target: &config.workbenchUI},
		{name: environmentWorkbenchThemeDirectory, target: &config.workbenchThemeDirectory},
		{name: environmentWorkbenchTraceConfig, target: &config.workbenchTraceConfig},
		{name: environmentWorkbenchToken, target: &config.workbenchToken},
		{name: environmentAgentMCPKeyFile, target: &config.agentMCPKeyFile},
		{name: environmentOperatorEmailConfig, target: &config.emailConfiguration},
		{name: environmentLocalSimulationConfig, target: &config.localSimulationConfig},
		{name: environmentSimulationConfig, target: &config.simulationConfig},
	}
}

func (config *serveConfig) bindFlags(flags *flag.FlagSet) {
	flags.StringVar(&config.watchRoot, watchRootFlag, config.watchRoot, "optional repository root for bounded in-process source checks")
	flags.StringVar(&config.databaseConfig, databaseConfigFlag, config.databaseConfig, "private JSON file containing database url")
	flags.StringVar(&config.artifactsPath, artifactsFlag, config.artifactsPath, "content-addressed artifact directory")
	flags.StringVar(&config.events, eventsFlag, config.events, "event log for dashboard")
	flags.StringVar(&config.listen, listenFlag, config.listen, "HTTP listen address")
	flags.StringVar(&config.work, workFlag, config.work, "reported work state for live board")
	flags.StringVar(&config.receipts, receiptsFlag, config.receipts, "retained command receipt directory for inspection")
	flags.StringVar(&config.origin, originFlag, config.origin, "operator browser origin")
	flags.StringVar(&config.searchURL, searchURLFlag, config.searchURL, "OpenSearch URL")
	flags.StringVar(&config.searchIndex, searchIndexFlag, config.searchIndex, "OpenSearch document index")
	flags.StringVar(&config.embeddingModel, embeddingModelFlag, config.embeddingModel, "ML Commons model identity; empty means lexical")
	flags.StringVar(&config.consumerRoot, consumerRootFlag, config.consumerRoot, "authorized checkout for explicitly selected onboarding sources")
	flags.StringVar(&config.consumerRevision, consumerRevisionFlag, config.consumerRevision, "optional consumer source revision; defaults to each file's content hash")
	flags.StringVar(&config.copilotHistorySource, copilotHistorySourceFlag, config.copilotHistorySource, "read-only native Copilot history source copied into a disposable SDK home")
	flags.StringVar(&config.workbenchDatabase, workbenchDatabaseFlag, config.workbenchDatabase, "private JSON database URL for the optional Copilot Workbench")
	flags.StringVar(&config.workbenchRepository, workbenchRepositoryFlag, config.workbenchRepository, "repository root used by Workbench sessions")
	flags.StringVar(&config.workbenchWorktrees, workbenchWorktreesFlag, config.workbenchWorktrees, "Workbench git worktree directory")
	flags.StringVar(&config.workbenchUI, workbenchUIFlag, config.workbenchUI, "built Workbench UI directory")
	flags.StringVar(&config.workbenchThemeDirectory, workbenchThemeDirectoryFlag, config.workbenchThemeDirectory, "directory containing workbench-theme.css; defaults beside work-state when Workbench is mounted")
	flags.StringVar(&config.workbenchTraceConfig, workbenchTraceConfigFlag, config.workbenchTraceConfig, "private generated OTLP configuration for retained Workbench traces")
	flags.StringVar(&config.workbenchToken, workbenchTokenFlag, config.workbenchToken, "optional private Copilot token file")
	flags.StringVar(&config.agentMCPKeyFile, agentMCPKeyFileFlag, config.agentMCPKeyFile, "private bearer key for agent MCP sessions")
	flags.StringVar(&config.emailConfiguration, operatorEmailConfigFlag, config.emailConfiguration, "private protobuf JSON operator email configuration; requires authenticated agent MCP")
	flags.StringVar(&config.localSimulationConfig, localSimulationConfigFlag, config.localSimulationConfig, "operator-owned local Docker profiles; launch in the shared Go worker")
	flags.StringVar(&config.simulationConfig, simulationConfigFlag, config.simulationConfig, "operator-owned protobuf JSON AWS Batch profiles; absent allows local observations only")
}

func parseServeConfig(mode string, arguments []string, lookup func(name string) (value string, found bool)) (serveConfig, error) {
	config := newServeConfig(lookup)
	flags := flag.NewFlagSet(mode, flag.ContinueOnError)
	config.bindFlags(flags)
	if err := flags.Parse(arguments); err != nil {
		return serveConfig{}, err
	}
	knowledgeConfigured := config.artifactsPath != defaultServeArtifacts ||
		config.searchURL != defaultServeSearchURL ||
		config.searchIndex != defaultServeSearchIndex || config.embeddingModel != ""
	if config.databaseConfig == "" && knowledgeConfigured {
		return serveConfig{}, fmt.Errorf("knowledge settings require %s", environmentDatabaseConfig)
	}
	if config.consumerRevision != "" && config.consumerRoot == "" {
		return serveConfig{}, fmt.Errorf("%s requires %s", environmentConsumerRevision, environmentConsumerRoot)
	}
	if config.watchRoot != "" && config.receipts == "" {
		return serveConfig{}, fmt.Errorf("%s requires %s", environmentWatchRoot, environmentReceipts)
	}
	if config.workbenchDatabase != "" && config.workbenchRepository == "" {
		return serveConfig{}, fmt.Errorf("%s requires %s", environmentWorkbenchDatabase, environmentWorkbenchRepository)
	}
	if config.workbenchDatabase == "" && (config.workbenchRepository != "" || config.workbenchWorktrees != "" || config.workbenchUI != "" || config.workbenchToken != "") {
		return serveConfig{}, fmt.Errorf("Workbench settings require %s", environmentWorkbenchDatabase)
	}
	if config.databaseConfig == "" && (config.localSimulationConfig != "" || config.simulationConfig != "") {
		return serveConfig{}, fmt.Errorf("simulation settings require %s", environmentDatabaseConfig)
	}
	if config.workbenchDatabase == "" && config.workbenchTraceConfig != "" && config.localSimulationConfig == "" {
		return serveConfig{}, fmt.Errorf("%s requires %s or %s", environmentWorkbenchTraceConfig, environmentWorkbenchDatabase, environmentLocalSimulationConfig)
	}
	return config, nil
}
