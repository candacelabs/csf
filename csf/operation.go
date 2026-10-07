// Copyright 2026 Candace Labs

package csf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Operation is one typed operation, as every caller reaches it: Tool is the
// MCP tool for [New]; Method, Path and Handler are the HTTP route for the
// host's router; Name and Invoke call it in-process with its JSON input, for
// a CLI verb that holds the capability itself.
type Operation struct {
	Name    string
	Tool    Option
	Method  string
	Path    string
	Handler gin.HandlerFunc
	Invoke  func(ctx context.Context, input []byte) ([]byte, error)
}

// OperationTools is each operation's MCP tool, for [New].
func OperationTools(operations []Operation) []Option {
	tools := make([]Option, 0, len(operations))
	for _, operation := range operations {
		tools = append(tools, operation.Tool)
	}
	return tools
}

// RegisterOperations mounts each operation's HTTP route on router.
func RegisterOperations(router gin.IRouter, operations []Operation) {
	for _, operation := range operations {
		router.Handle(operation.Method, operation.Path, operation.Handler)
	}
}

// requestHeaderKey carries the calling request's header through an
// operation's context.
type requestHeaderKey struct{}

// RequestHeader is the header of the MCP or HTTP request that called the
// operation running under ctx; nil for a call made in-process.
func RequestHeader(ctx context.Context) http.Header {
	header, _ := ctx.Value(requestHeaderKey{}).(http.Header)
	return header
}

// NewOperation serves call as the MCP tool name and the HTTP route method
// path. The route reads In as a JSON body; a GET reads none. Both transports
// put the request's header in call's context, read with [RequestHeader].
func NewOperation[In, Out any](name string, description string, method string, path string, call func(ctx context.Context, input In) (Out, error)) Operation {
	return NewToolOperation(mcp.Tool{Name: name, Description: description}, method, path, call)
}

// NewToolOperation is [NewOperation] for a tool whose input or output schema
// the caller supplies rather than the MCP SDK deriving it from In and Out:
// one generated from an API's own description, say.
func NewToolOperation[In, Out any](tool mcp.Tool, method string, path string, call func(ctx context.Context, input In) (Out, error)) Operation {
	return Operation{
		Name: tool.Name,
		Invoke: func(ctx context.Context, raw []byte) ([]byte, error) {
			var input In
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, fmt.Errorf("%w: decode the input: %w", ErrInvalidRequest, err)
			}
			output, err := call(ctx, input)
			if err != nil {
				return nil, err
			}
			return json.Marshal(output)
		},
		Tool: WithMCPTool(tool, func(ctx context.Context, request *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
			if request != nil && request.Extra != nil {
				ctx = context.WithValue(ctx, requestHeaderKey{}, request.Extra.Header)
			}
			output, err := call(ctx, input)
			return nil, output, err
		}),
		Method: method,
		Path:   path,
		Handler: func(request *gin.Context) {
			var input In
			if method != http.MethodGet {
				if err := json.NewDecoder(http.MaxBytesReader(request.Writer, request.Request.Body, maxAPIBytes)).Decode(&input); err != nil {
					request.String(http.StatusBadRequest, "decode the request: %s", err.Error())
					return
				}
			}
			ctx := context.WithValue(request.Request.Context(), requestHeaderKey{}, request.Request.Header)
			output, err := call(ctx, input)
			if err != nil {
				request.String(OperationErrorStatus(err), "%s", err.Error())
				return
			}
			request.JSON(http.StatusOK, output)
		},
	}
}

// ErrUnknownOperation reports an in-process call of an operation that is
// not in the list.
var ErrUnknownOperation = errors.New("unknown operation")

// InvokeOperation calls the operation named name in-process with its JSON
// input and returns its JSON output.
func InvokeOperation(ctx context.Context, operations []Operation, name string, input []byte) ([]byte, error) {
	for _, operation := range operations {
		if operation.Name == name {
			return operation.Invoke(ctx, input)
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownOperation, name)
}
