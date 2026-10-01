// The JSONL adapter is a disposable simulator boundary. Go applications should
// compose csf.NewRuntime directly and keep calls in-process.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"

	"github.com/gin-gonic/gin"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/guregu/null/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	dockerclient "github.com/moby/moby/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/httpserver"
	brainspinev1 "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	"github.com/candacelabs/csf/services/copilot-adapter/copilotbridge"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	copilotv1 "github.com/candacelabs/csf/services/copilot-adapter/proto/candace/copilot/v1"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
	"github.com/candacelabs/csf/services/copilot-adapter/workbench"
)

const maxRequestBytes = 256 * 1024
const watchRootFlag = "watch-root"
const agentMCPKeyFileFlag = "agent-mcp-key-file"
const watchReceiptFile = "receipt.json"
const watchReceiptTemporary = "receipt.json.tmp"
const httpServiceName = "csf"

const copilotHistorySessionsToolName = "ListCopilotHistorySessions"

type copilotHistorySessionLister func(ctx context.Context, filter *copilot.SessionListFilter) ([]copilot.SessionMetadata, error)

type copilotHistorySessionsOutput struct {
	Sessions []copilot.SessionMetadata `json:"sessions"`
}

func withCopilotHistorySessionsTool(list copilotHistorySessionLister) csf.Option {
	closedWorld := false
	return csf.WithMCPTool[copilot.SessionListFilter, copilotHistorySessionsOutput](mcp.Tool{
		Name:        copilotHistorySessionsToolName,
		Title:       "List Copilot history sessions",
		Description: "Read-only metadata listing for the copied native Copilot history. It does not resume sessions, read events, send prompts or execute tools.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, filter copilot.SessionListFilter) (*mcp.CallToolResult, copilotHistorySessionsOutput, error) {
		sessions, err := list(ctx, &filter)
		if sessions == nil {
			sessions = []copilot.SessionMetadata{}
		}
		return nil, copilotHistorySessionsOutput{Sessions: sessions}, err
	})
}

// The release build supplies the operator's reachable dashboard URL. Source
// archives retain a loopback default and do not embed a deployment identity.
var dashboardURL = "http://127.0.0.1:14111/"

func main() {
	if len(os.Args) > 1 && os.Args[1] == linksCommand {
		if err := links(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "link" {
		if _, err := fmt.Fprintln(os.Stdout, dashboardURL+"ui/"); err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "call" {
		if err := call(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "serve" || os.Args[1] == "mcp" || os.Args[1] == "initialize") {
		if err := serve(os.Args[1], os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// All operation names, request types and dispatch are generated from the same
// annotated API service as HTTP/MCP. Only process I/O lives in this composition.
func call(arguments []string) error {
	return callWithStreams(arguments, os.Stdin, os.Stdout)
}

func callWithStreams(arguments []string, input io.Reader, output io.Writer) error {
	flags := flag.NewFlagSet("call", flag.ContinueOnError)
	endpoint := flags.String("endpoint", "http://127.0.0.1:14111", "shared host address")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("choose one operation: %v", csf.CLIOperations())
	}
	request, err := io.ReadAll(io.LimitReader(input, maxRequestBytes+1))
	if err != nil {
		return err
	}
	if len(request) > maxRequestBytes {
		return fmt.Errorf("request exceeds limit")
	}
	if len(request) == 0 {
		request = []byte("{}")
	}
	client, err := csf.NewClient(*endpoint, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	response, err := client.CallOperation(context.Background(), flags.Arg(0), request)
	if err != nil {
		return err
	}
	_, err = output.Write(append(response, '\n'))
	return err
}

func run() error {
	return runWithStreams(os.Stdin, os.Stdout)
}

func runWithStreams(input io.Reader, output io.Writer) error {
	runtime := csf.NewRuntime()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), maxRequestBytes)
	writer := bufio.NewWriter(output)
	for scanner.Scan() {
		request := &brainspinev1.RuntimeRequest{}
		var response *brainspinev1.RuntimeResponse
		if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(scanner.Bytes(), request); err != nil {
			response = &brainspinev1.RuntimeResponse{Error: err.Error()}
		} else {
			response = runtime.Handle(request)
		}
		encoded, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(response)
		if err != nil {
			return err
		}
		if _, err := writer.Write(append(encoded, '\n')); err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func serve(mode string, arguments []string) error {
	settings, err := parseServeConfig(mode, arguments, os.LookupEnv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var agentMCPAuthenticator *csf.AgentMCPAuthenticator
	if settings.agentMCPKeyFile != "" {
		key, err := os.ReadFile(settings.agentMCPKeyFile)
		if err != nil {
			return fmt.Errorf("read agent MCP key: %w", err)
		}
		agentMCPAuthenticator, err = csf.NewAgentMCPAuthenticator([]byte(strings.TrimSpace(string(key))))
		if err != nil {
			return err
		}
	}
	onboardingConfig := csf.OnboardingConfig{ConsumerRoot: settings.consumerRoot, ConsumerRevision: settings.consumerRevision}
	options := []csf.Option{csf.WithDashboard(csf.NewDashboard(settings.events))}
	registry := prometheus.NewRegistry()
	if settings.emailConfiguration != "" {
		if agentMCPAuthenticator == nil {
			return fmt.Errorf("operator email requires the agent MCP signing key")
		}
		mailer, err := configuredOperatorEmail(settings.emailConfiguration, registry)
		if err != nil {
			return err
		}
		options = append(options, csf.WithEmail(mailer))
	}
	if settings.workbenchThemeDirectory == "" && settings.workbenchUI != "" && settings.work != "" {
		settings.workbenchThemeDirectory = filepath.Dir(settings.work)
	}
	if settings.workbenchThemeDirectory != "" {
		options = append(options, csf.WithWorkbenchThemeDirectory(settings.workbenchThemeDirectory))
	}
	var simulations *csf.Simulations
	if settings.databaseConfig != "" {
		content, err := os.ReadFile(settings.databaseConfig)
		if err != nil {
			return err
		}
		var config struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(content, &config); err != nil {
			return err
		}
		pool, err := pgxpool.New(ctx, config.URL)
		if err != nil {
			return fmt.Errorf("invalid database configuration")
		}
		defer pool.Close()
		if err := pool.Ping(ctx); err != nil {
			return fmt.Errorf("database connection failed")
		}
		store, err := csf.NewPostgres(pool)
		if err != nil {
			return err
		}
		if mode == "initialize" {
			return store.Initialize(ctx)
		}
		if err := store.Migrate(ctx); err != nil {
			return err
		}
		artifacts, err := csf.NewArtifacts(settings.artifactsPath)
		if err != nil {
			return err
		}
		defer func() { _ = artifacts.Close() }()
		index, err := csf.ConnectOpenSearch(settings.searchURL, settings.searchIndex, settings.embeddingModel, &http.Client{Timeout: 30 * time.Second})
		if err != nil {
			return err
		}
		defer func() { _ = index.Close() }()
		options = append(options, csf.WithKnowledge(store, index, artifacts), csf.WithAgentConfigurations(store))
		var policy *brainspinev1.SimulationConfig
		var provider csf.IBatch
		var logs csf.ISimulationLogs
		if settings.simulationConfig != "" {
			content, err := os.ReadFile(settings.simulationConfig)
			if err != nil {
				return err
			}
			policy = &brainspinev1.SimulationConfig{}
			if err := protojson.Unmarshal(content, policy); err != nil {
				return err
			}
			loadOptions := []func(options *awsconfig.LoadOptions) error{awsconfig.WithRegion(policy.Region)}
			if policy.AwsProfile != "" {
				loadOptions = append(loadOptions, awsconfig.WithSharedConfigProfile(policy.AwsProfile))
			}
			config, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
			if err != nil {
				return err
			}
			provider = batch.NewFromConfig(config)
			logs = cloudwatchlogs.NewFromConfig(config)
		}
		simulationOptions := []csf.SimulationOption{}
		if settings.localSimulationConfig != "" {
			content, err := os.ReadFile(settings.localSimulationConfig)
			if err != nil {
				return err
			}
			localConfig := &brainspinev1.LocalSimulationConfig{}
			if err := protojson.Unmarshal(content, localConfig); err != nil {
				return err
			}
			docker, err := dockerclient.New(dockerclient.WithHost(localConfig.DockerHost), dockerclient.WithAPIVersionNegotiation())
			if err != nil {
				return err
			}
			defer func() { _ = docker.Close() }()
			local, err := csf.NewLocalSimulations(docker, localConfig)
			if err != nil {
				return err
			}
			simulationOptions = append(simulationOptions, csf.WithLocalSimulations(local), csf.WithSimulationLogSearch(index))
			if settings.workbenchTraceConfig != "" {
				document, err := os.ReadFile(settings.workbenchTraceConfig)
				if err != nil {
					return err
				}
				config := &copilotv1.TraceExportConfig{}
				if err := protojson.Unmarshal(document, config); err != nil {
					return err
				}
				if err := copilotv1.ValidateTraceExportConfig(config); err != nil {
					return err
				}
				client := otlptracehttp.NewClient(otlptracehttp.WithEndpointURL(config.EndpointUrl),
					otlptracehttp.WithHeaders(map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(config.PublicKey+":"+config.SecretKey)), "x-langfuse-ingestion-version": "4"}),
					otlptracehttp.WithTimeout(10*time.Second), otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}), otlptracehttp.WithMaxRequestSize(1048576))
				simulationOptions = append(simulationOptions, csf.WithSimulationTraces(client))
			}
		}
		simulations, err = csf.NewSimulations(store, artifacts, policy, provider, logs, simulationOptions...)
		if err != nil {
			return err
		}
		options = append(options, csf.WithSimulations(simulations))
	} else if mode == "initialize" {
		return fmt.Errorf("database-config required")
	}
	var copilotHistoryBridge *copilotbridge.CopilotBridge
	if settings.copilotHistorySource != "" {
		copilotHistoryBridge, err = copilotbridge.NewCopilotBridge(ctx, copilotbridge.Config{
			HistorySourceDirectory: settings.copilotHistorySource,
			Logger:                 slog.Default(),
			ShutdownTimeout:        time.Duration(copilotadapter.DefaultAdapterConfig().GetDurableTransitionTimeoutMillis()) * time.Millisecond,
		})
		if err != nil {
			return err
		}
		onboardingConfig.CopilotHistoryReader = copilotHistoryBridge.ReadSessionHistory
		defer func() { _ = copilotHistoryBridge.Close() }()
	}
	options = append(options, csf.WithOnboarding(onboardingConfig))
	if copilotHistoryBridge != nil {
		options = append(options, withCopilotHistorySessionsTool(copilotHistoryBridge.ListHistorySessions))
	}
	service, err := csf.New(options...)
	if err != nil {
		return err
	}
	workers, err := service.StartProjectionWorkers(ctx)
	if err != nil {
		return err
	}
	defer workers.Close()
	if settings.watchRoot != "" {
		watch, err := csf.NewSourceWatch(settings.watchRoot, csf.ImportantSourcePaths(), time.Second, func(checkContext context.Context, request csf.CheckRequest) error {
			receipt, checkErr := csf.CheckSourceSnapshot(checkContext, request)
			content, err := protojson.Marshal(receipt)
			if err != nil {
				return err
			}
			directory := filepath.Join(settings.receipts, receipt.ReceiptId)
			if err := os.MkdirAll(directory, 0700); err != nil {
				return err
			}
			temporary := filepath.Join(directory, watchReceiptTemporary)
			if err := os.WriteFile(temporary, content, 0600); err != nil {
				return err
			}
			if err := os.Rename(temporary, filepath.Join(directory, watchReceiptFile)); err != nil {
				return err
			}
			return checkErr
		})
		if err != nil {
			return err
		}
		results, err := watch.Start(ctx)
		if err != nil {
			return err
		}
		go func() {
			for result := range results {
				if result.Err != nil {
					fmt.Fprintln(os.Stderr, result.Err)
				}
			}
		}()
	}
	if mode == "mcp" {
		workerContext, workerCancel := context.WithCancel(ctx)
		defer workerCancel()
		jobs, jobsContext := errgroup.WithContext(workerContext)
		jobs.Go(func() error { defer workerCancel(); return service.ServeStdioMCP(jobsContext) })
		if simulations != nil {
			jobs.Go(func() error { return simulations.Work(jobsContext) })
		}
		return jobs.Wait()
	}
	engine := httpserver.NewEngine(httpServiceName, httpserver.WithRequestLogging())
	service.Register(engine)
	group, groupContext := errgroup.WithContext(ctx)
	if simulations != nil {
		group.Go(func() error { return simulations.Work(groupContext) })
	}
	farmOptions := []csf.FarmOption{csf.WithFarmWorkPath(settings.work)}
	var copilotWorkbench *workbench.Workbench
	if settings.workbenchDatabase != "" {
		content, err := os.ReadFile(settings.workbenchDatabase)
		if err != nil {
			return err
		}
		var databaseConfig struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(content, &databaseConfig); err != nil {
			return err
		}
		database, err := sql.Open("pgx", databaseConfig.URL)
		if err != nil {
			return fmt.Errorf("workbench database configuration invalid")
		}
		defer func() { _ = database.Close() }()
		token := ""
		if settings.workbenchToken != "" {
			secret, err := os.ReadFile(settings.workbenchToken)
			if err != nil {
				return err
			}
			token = strings.TrimSpace(string(secret))
		}
		var traceConfig *copilotv1.TraceExportConfig
		if settings.workbenchTraceConfig != "" {
			document, err := os.ReadFile(settings.workbenchTraceConfig)
			if err != nil {
				return err
			}
			traceConfig = &copilotv1.TraceExportConfig{}
			if err := protojson.Unmarshal(document, traceConfig); err != nil {
				return fmt.Errorf("invalid Workbench trace configuration")
			}
		}
		mcpServers, err := workbenchMCPServers(settings.origin, traceConfig)
		if err != nil {
			return err
		}
		bridgeConfig := copilotbridge.Config{
			GitHubToken: token, WorkingDirectory: settings.workbenchRepository, Logger: slog.Default(),
			ShutdownTimeout: time.Duration(copilotadapter.DefaultAdapterConfig().GetDurableTransitionTimeoutMillis()) * time.Millisecond,
			MCPServers:      mcpServers,
		}
		if agentMCPAuthenticator != nil {
			bridgeConfig.MCPServerResolver = workbenchMCPServerResolver(settings.origin, traceConfig, agentMCPAuthenticator)
		}
		bridge, err := copilotbridge.NewCopilotBridge(ctx, bridgeConfig)
		if err != nil {
			return err
		}
		defer func() { _ = bridge.Close() }()
		tasks, err := workbenchTasks(ctx, token, os.Stdout)
		if err != nil {
			return err
		}
		copilotWorkbench, err = workbench.NewWorkbench(ctx, database, workbench.WithBridge(bridge),
			workbench.WithRepository(settings.workbenchRepository), workbench.WithWorktrees(settings.workbenchWorktrees),
			workbench.WithTaskContinuity(tasks), workbench.WithKanbanOrigins(strings.TrimRight(settings.origin, "/")))
		if err != nil {
			return err
		}
		defer func() {
			closeContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := copilotWorkbench.Close(closeContext); err != nil {
				slog.Warn("close Workbench", "error", err)
			}
		}()
		if err := copilotWorkbench.Register(engine); err != nil {
			return err
		}
		if settings.workbenchUI != "" {
			workbench.MountUI(engine, settings.workbenchUI)
		}
		if traceConfig != nil {
			exporter, err := copilotadapter.NewTraceExporter(copilotWorkbench.Store, traceConfig)
			if err != nil {
				return err
			}
			if err := exporter.Start(groupContext); err != nil {
				return err
			}
			defer func() {
				closeContext, stop := context.WithTimeout(context.Background(), time.Duration(traceConfig.GetRequestTimeoutMillis())*time.Millisecond)
				defer stop()
				if err := exporter.Close(closeContext); err != nil {
					slog.Warn("close Workbench trace exporter", "error", err)
				}
			}()
		}
		farmOptions = append(farmOptions, csf.WithFarmAgentSource(func(now time.Time) ([]csf.FarmAgent, error) {
			queryContext, cancel := context.WithTimeout(groupContext, time.Second)
			defer cancel()
			var sessions []storedb.Session
			for _, status := range []api.SessionStatus{api.SessionStatusRunning, api.SessionStatusStarting, api.SessionStatusIdle} {
				rows, err := copilotWorkbench.Store.ListSessions(queryContext, storedb.ListSessionsParams{RowLimit: 8, Status: null.StringFrom(string(status))})
				if err != nil {
					return nil, err
				}
				sessions = append(sessions, rows...)
			}
			telemetry, err := copilotWorkbench.Adapter.Telemetry(queryContext)
			if err != nil {
				return nil, err
			}
			measurements := make(map[string]api.SessionTelemetry, len(telemetry.Sessions))
			for _, row := range telemetry.Sessions {
				measurements[row.SessionId.String()] = row
			}
			agents := make([]csf.FarmAgent, 0, len(sessions))
			for _, session := range sessions {
				agent := csf.FarmAgent{Name: session.DisplayName, Role: "Copilot", State: session.Status,
					SessionID: session.ID.String(), Worktree: session.WorkingDirectory, Model: session.Model,
					Task: "Workbench session", ObservedAt: now.UTC().Format(time.RFC3339), StartedAt: session.CreatedAt.UTC().Format(time.RFC3339),
					DurationLabel: "session age", Duration: now.Sub(session.CreatedAt).Truncate(time.Second).String(), TicketURL: "/ui/#/sessions/" + session.ID.String()}
				if measured, found := measurements[session.ID.String()]; found {
					agent.PremiumRequests = measured.PremiumRequests
					if measured.ObservedModelCallCount > 0 {
						agent.Tokens = &api.UsageObservation{Kind: api.ModelCall, InputTokens: measured.InputTokens, OutputTokens: measured.OutputTokens,
							CacheReadTokens: measured.CacheReadTokens, CacheWriteTokens: measured.CacheWriteTokens, ReasoningTokens: measured.ReasoningTokens, ApiDurationMs: measured.ApiDurationMs}
					}
					if measured.LatestStartedAt != nil {
						agent.StartedAt = measured.LatestStartedAt.UTC().Format(time.RFC3339Nano)
						end := now
						agent.DurationLabel = "turn elapsed"
						if measured.LatestCompletedAt != nil {
							end = *measured.LatestCompletedAt
							agent.FinishedAt = end.UTC().Format(time.RFC3339Nano)
							agent.DurationLabel = "latest turn execution"
						}
						if !end.Before(*measured.LatestStartedAt) {
							agent.Duration = end.Sub(*measured.LatestStartedAt).Truncate(time.Second).String()
						}
					}
				}
				agents = append(agents, agent)
			}
			return agents, nil
		}))
	}
	inspectionOptions := []csf.InspectionOption{csf.WithInspectionRegistry(registry), csf.WithInspectionReceipts(settings.receipts), csf.WithInspectionProjectionWorkers(workers)}
	if simulations != nil {
		inspectionOptions = append(inspectionOptions, csf.WithInspectionSimulations(simulations))
	}
	if copilotWorkbench != nil {
		inspectionOptions = append(inspectionOptions, csf.WithInspectionTelemetry(copilotWorkbench.Adapter.Telemetry))
	}
	if settings.work != "" {
		farm, err := csf.NewFarmDashboard([]string{settings.origin, "http://127.0.0.1:14111", "http://localhost:14111"}, farmOptions...)
		if err != nil {
			return err
		}
		go farm.Observe(ctx)
		defer func() {
			closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = farm.Close(closeContext)
		}()
		farm.Register(engine)
		inspectionOptions = append(inspectionOptions, csf.WithInspectionSnapshot(farm.Snapshot), csf.WithInspectionBrowserConnections(farm.ActiveConnections))
	} else {
		csf.NewDashboard(settings.events).Register(engine)
	}
	csf.NewInspection(inspectionOptions...).Register(engine)
	engine.Any("/mcp", gin.WrapH(service.MCPHandler()))
	if agentMCPAuthenticator != nil {
		engine.Any(workbenchAgentMCPPath, gin.WrapH(service.AgentMCPHandler(agentMCPAuthenticator)))
	}
	server := httpserver.NewStreamingServer(settings.listen, engine)
	ready := make(chan struct{})
	server.BaseContext = func(listener net.Listener) context.Context { close(ready); return groupContext }
	// Start HTTP before restoring sessions so their shared MCP transport is reachable.
	group.Go(func() error { return httpserver.Serve(groupContext, server) })
	if copilotWorkbench != nil {
		group.Go(func() error {
			select {
			case <-ready:
			case <-groupContext.Done():
				return groupContext.Err()
			}
			if err := copilotWorkbench.Restore(groupContext); err != nil {
				return err
			}
			return copilotWorkbench.Adapter.RunSchedules(groupContext)
		})
	}
	fmt.Fprintln(os.Stderr, "CSF HTTP, MCP and optional Workbench listening on", settings.listen)
	return group.Wait()
}
