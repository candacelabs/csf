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
	"strings"
	"sync/atomic"

	"github.com/candacelabs/csf/pkg/liquidproto"
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
)

// IHTTPDoer is the only network behavior the generated client consumes.
type IHTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

// Client uses generated method signatures with protobuf's JSON codec.
type Client struct {
	endpoint string
	http     IHTTPDoer
}

func NewClient(endpoint string, transport IHTTPDoer) (*Client, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || transport == nil {
		return nil, fmt.Errorf("an HTTP(S) endpoint and transport are required")
	}
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), http: transport}, nil
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
	return protojson.Unmarshal(body, output)
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
	theme               *workbenchTheme
	agentConfigurations IAgentConfigurationStore
	mcp                 *mcp.Server
	routes              []route
}

type Option func(service *Service)

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

func WithAgentConfigurations(store IAgentConfigurationStore) Option {
	return func(service *Service) { service.agentConfigurations = store }
}

func New(options ...Option) (*Service, error) {
	service := &Service{wake: make(chan struct{}, projectionWorkerCount), theme: &workbenchTheme{}}
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
			ctx.String(operationErrorStatus(err), "%s", err.Error())
			return
		}
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

func operationErrorStatus(err error) int {
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
