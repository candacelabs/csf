// Package csf composes the brain-spine harness's capabilities and their
// generated HTTP/MCP operations in one Go process. The low-level spine
// controller is external and ROS-side: CSF reaches it only through the
// ipc/ros capability and interprets no controller program itself. It makes
// no hard-real-time or physical-safety guarantee.
package csf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/candacelabs/csf/io/net/ros"
	"github.com/candacelabs/csf/pkg/liquidproto"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/services/jobs"
	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	maxAPIBytes           = 2 * 1024 * 1024
	mcpImplementationName = "csf"
)

// Input, identity and revision errors are stable transport classifications.
// Backend and encoding failures remain server errors at the HTTP boundary.
var (
	ErrInvalidRequest                = errors.New("invalid request")
	ErrUnauthorized                  = errors.New("agent identity is required")
	ErrNotFound                      = errors.New("not found")
	ErrConflict                      = errors.New("revision conflict")
	ErrAgentConfigurationUnavailable = errors.New("agent configuration capability unavailable")
	ErrMCPToolConflict               = errors.New("MCP tool name already registered")
	ErrInvalidMCPTool                = errors.New("invalid MCP tool registration")
)

var mcpToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// IHTTPDoer is the only network behavior the generated client consumes.
type IHTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

// Client uses generated method signatures with protobuf's JSON codec.
type Client struct {
	endpoint string
	http     IHTTPDoer
	warn     func(warning string)
}

// ClientOption configures a [Client].
type ClientOption func(client *Client)

// WithClientWarnings receives the client's warnings, such as a response from
// a newer host carrying fields this client does not know. Without it they are
// dropped.
func WithClientWarnings(warn func(warning string)) ClientOption {
	return func(client *Client) {
		if warn != nil {
			client.warn = warn
		}
	}
}

func NewClient(endpoint string, transport IHTTPDoer, options ...ClientOption) (*Client, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || transport == nil {
		return nil, fmt.Errorf("an HTTP(S) endpoint and transport are required")
	}
	client := &Client{endpoint: strings.TrimRight(endpoint, "/"), http: transport, warn: func(warning string) {}}
	for _, option := range options {
		if option != nil {
			option(client)
		}
	}
	return client, nil
}

func (client *Client) call(ctx context.Context, method string, path string, input proto.Message, output proto.Message) error {
	encoded, err := protojson.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, client.endpoint+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAPIBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxAPIBytes {
		return fmt.Errorf("response exceeds limit")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("adapter returned HTTP %d: %s", response.StatusCode, string(body))
	}
	strict := protojson.Unmarshal(body, output)
	if strict == nil {
		return nil
	}
	// A newer host answers with fields this client predates. The operation
	// succeeded, so the response is kept without them, and the warning names
	// both versions so the operator knows which binary to upgrade.
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, output); err != nil {
		return strict
	}
	host := response.Header.Get(VersionHeader)
	if host == "" {
		host = UnknownVersion
	}
	client.warn(fmt.Sprintf("this csf (%s) is older than the host (%s): ignored fields of %s it does not know (%v); upgrade this csf", Version(), host, path, strict))
	return nil
}

// Service composes CSF capabilities and their generated HTTP/MCP operations.
// It opens no listener and owns no process.
type Service struct {
	simulations         *Simulations
	store               IKnowledgeStore
	index               IKnowledgeIndex
	artifacts           *Artifacts
	wake                chan struct{}
	workersStarted      atomic.Bool
	dashboard           *Dashboard
	spine               ros.ISpine
	theme               *workbenchTheme
	agentConfigurations IAgentConfigurationStore
	agentSessions       IAgentSessions
	dispatch            IDispatch
	email               IEmailSender
	onboarding          *OnboardingConfig
	mcp                 *mcp.Server
	consumerMCPTools    []mcpToolRegistrar
	routes              []route
	mcpToolNames        map[string]struct{}
}

type Option func(service *Service)

type mcpToolRegistrar func(service *Service) error

type route struct {
	method  string
	path    string
	handler gin.HandlerFunc
}

func WithKnowledge(store IKnowledgeStore, index IKnowledgeIndex, artifacts *Artifacts) Option {
	return func(service *Service) { service.store = store; service.index = index; service.artifacts = artifacts }
}

func WithDashboard(dashboard *Dashboard) Option {
	return func(service *Service) { service.dashboard = dashboard }
}

// WithSpine grants the low-level spine controller capability. Without it the
// service holds ros.DisconnectedSpine and its views report "no spine
// connected".
func WithSpine(spine ros.ISpine) Option {
	return func(service *Service) { service.spine = spine }
}

func WithAgentConfigurations(store IAgentConfigurationStore) Option {
	return func(service *Service) { service.agentConfigurations = store }
}

func WithOnboarding(config OnboardingConfig) Option {
	return func(service *Service) { service.onboarding = &config }
}

// WithMCPTool adds one typed consumer-owned tool to the same MCP server as
// CSF's generated operations. The MCP SDK derives and validates the input and
// output schemas from In and Out; CSF only supplies registration ordering and
// collision checks.
func WithMCPTool[In, Out any](tool mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) Option {
	return func(service *Service) {
		service.consumerMCPTools = append(service.consumerMCPTools, func(service *Service) error {
			if !mcpToolNamePattern.MatchString(tool.Name) || handler == nil {
				return fmt.Errorf("%w: name and handler are required", ErrInvalidMCPTool)
			}
			if _, exists := service.mcpToolNames[tool.Name]; exists {
				return fmt.Errorf("%w: %q", ErrMCPToolConflict, tool.Name)
			}
			toolCopy := tool
			mcp.AddTool[In, Out](service.mcp, &toolCopy, handler)
			service.mcpToolNames[tool.Name] = struct{}{}
			return nil
		})
	}
}

func New(options ...Option) (*Service, error) {
	service := &Service{wake: make(chan struct{}, projectionWorkerCount), spine: ros.NewDisconnectedSpine(), theme: &workbenchTheme{}, mcpToolNames: make(map[string]struct{})}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	if (service.store != nil || service.index != nil || service.artifacts != nil) && (service.store == nil || service.index == nil || service.artifacts == nil) {
		return nil, fmt.Errorf("knowledge requires store, index and artifacts")
	}
	if err := service.theme.load(); err != nil {
		return nil, fmt.Errorf("load Workbench theme: %w", err)
	}
	service.mcp = mcp.NewServer(&mcp.Implementation{Name: mcpImplementationName, Version: "0.1.0"}, nil)
	service.registerOperations()
	for _, operation := range HumanOperations() {
		service.mcpToolNames[operation.Name] = struct{}{}
	}
	for _, register := range service.consumerMCPTools {
		if err := register(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (service *Service) Register(router gin.IRouter) {
	for _, route := range service.routes {
		router.Handle(route.method, route.path, route.handler)
	}
}

// Erasure is contained in this transport adapter. Capability authors consume
// generated protobuf types; MCP's SDK owns the heterogeneous tool collection.
func registerOperation[Q proto.Message, R proto.Message](service *Service, name string, method string, path string, humanDescription string, schema json.RawMessage, construct func() Q, call func(ctx context.Context, request Q) (R, error)) {
	invoke := func(ctx context.Context, raw []byte) ([]byte, error) {
		if len(raw) > maxAPIBytes {
			return nil, fmt.Errorf("%w: request exceeds limit", ErrInvalidRequest)
		}
		request := construct()
		if len(raw) == 0 {
			raw = []byte("{}")
		}
		if err := protojson.Unmarshal(raw, request); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
		}
		result, err := call(ctx, request)
		if err != nil {
			return nil, err
		}
		return protojson.Marshal(result)
	}
	service.routes = append(service.routes, route{method: method, path: path, handler: func(ctx *gin.Context) {
		raw, err := io.ReadAll(http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxAPIBytes))
		if err != nil {
			ctx.String(http.StatusBadRequest, "request exceeds limit")
			return
		}
		result, err := invoke(ctx.Request.Context(), raw)
		if err != nil {
			ctx.String(OperationErrorStatus(err), "%s", err.Error())
			return
		}
		ctx.Header(VersionHeader, Version())
		ctx.Data(http.StatusOK, "application/json", result)
	}})
	service.mcp.AddTool(&mcp.Tool{Name: name, Title: humanDescription, Description: humanDescription + " Technical operation: " + name + ".", InputSchema: schema}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := invoke(ctx, request.Params.Arguments)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(result)}}, StructuredContent: json.RawMessage(result)}, nil
	})
}

// OperationErrorStatus is the HTTP status an operation's error is served
// with: the stable classifications above, and 500 for everything else. A
// service serving its own typed operations over HTTP answers with it too.
func OperationErrorStatus(err error) int {
	var validation *liquidproto.Error
	switch {
	case errors.Is(err, ErrInvalidRequest), errors.As(err, &validation):
		return http.StatusBadRequest
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// MCPHandler supports legacy clients and per-request protocol metadata. Durable
// knowledge belongs to the service stores, so the HTTP transport is stateless.
func (service *Service) MCPHandler() *mcp.StreamableHTTPHandler {
	return mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server { return service.mcp }, &mcp.StreamableHTTPOptions{Stateless: true})
}

// ServeStdioMCP serves the same generated tools over the caller process's
// standard input and output. The application still owns the process lifetime
// and cancellation context.
func (service *Service) ServeStdioMCP(ctx context.Context) error {
	return service.mcp.Run(ctx, &mcp.StdioTransport{})
}

// RebuildSimulationTrace derives versioned spans from OpenSearch alone. The
// durable delivery ledger prevents repeating an accepted or ambiguous export.
func (service *Service) RebuildSimulationTrace(ctx context.Context, request *pb.RebuildSimulationTraceRequest) (*pb.RebuildSimulationTraceResponse, error) {
	simulations := service.simulations
	if simulations == nil || simulations.local == nil || simulations.traces == nil || simulations.logSearch == nil || simulations.local.config.TraceBaseUrl == "" || simulations.local.config.LogIndex == "" || request == nil || !simulationID.MatchString(request.RunId) {
		return nil, fmt.Errorf("simulation trace reconstruction unavailable or invalid run identity")
	}
	job, err := simulations.ledger.Get(ctx, request.RunId)
	if err != nil {
		return nil, err
	}
	if job.Executor != simulationExecutorLocal || !job.State.Terminal() || (job.Managed && !job.CleanupConfirmed) {
		return nil, fmt.Errorf("simulation is not terminal with confirmed cleanup")
	}
	deadline, cancel := context.WithTimeout(ctx, simulationTraceDeadline)
	defer cancel()
	record, err := simulations.logSearch.SimulationSource(deadline, simulations.local.config.LogIndex, request.RunId)
	if err != nil {
		return nil, err
	}
	if record.Simulation.Run.Simulator != simulatorOf(job.Kind) || int64(record.Simulation.Run.Steps) != job.TotalUnits {
		return nil, fmt.Errorf("archived simulation does not match admitted job")
	}
	spans, identity, err := simulations.local.projectTrace(record.Simulation)
	if err != nil {
		return nil, err
	}
	if err := simulations.deliverSimulationTrace(deadline, job, spans, identity); err != nil {
		retained := simulations.ledger.RecordTrace(ctx, job.ID, jobs.TraceProjection{URL: job.Trace.URL, ExportError: err.Error()})
		return nil, errors.Join(err, retained)
	}
	run, err := simulations.inspect(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	return &pb.RebuildSimulationTraceResponse{Run: run, SourceSha256: record.SourceSha256}, nil
}
