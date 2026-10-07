// Copyright 2026 Candace Labs

package await

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/candacelabs/csf/csf"
)

// The await operation: one MCP tool on the CSF service and one HTTP route on
// the host's router; csf await is a client of the route.
const (
	AwaitTool = "Await"
	AwaitPath = "/api/await"
)

const awaitDescription = "Wait for one condition, up to a required deadline, and report the condition, the outcome " +
	"(met, deadline, or unreachable when it can no longer hold), the elapsed time and the last observation. " +
	"Conditions: harness-ready, harness-stopped (pid), session-phase (assignment, phase), turn-finished " +
	"(assignment, turn), pull-request-merged (pull_request, repository), url-status (url, status), " +
	"load-below (load). This is CSF's wait: use it instead of a shell loop that sleeps."

// operation answers a misuse as an invalid request, so it is served 400.
func (awaiter *Awaiter) operation(ctx context.Context, request Request) (Result, error) {
	result, err := awaiter.Await(ctx, request)
	for _, misuse := range []error{ErrUnknownCondition, ErrNoDeadline, ErrMissingOperand, ErrUnavailable} {
		if errors.Is(err, misuse) {
			return result, fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
		}
	}
	return result, err
}

// operations is every typed operation of the awaiter.
func (awaiter *Awaiter) operations() []csf.Operation {
	return []csf.Operation{csf.NewOperation(AwaitTool, awaitDescription, http.MethodPost, AwaitPath, awaiter.operation)}
}

// Tools is every operation as an MCP tool for the CSF service:
// csf.New(append(options, awaiter.Tools()...)...).
func (awaiter *Awaiter) Tools() []csf.Option { return csf.OperationTools(awaiter.operations()) }

// Register mounts every operation's HTTP route on the caller's router.
func (awaiter *Awaiter) Register(router gin.IRouter) {
	csf.RegisterOperations(router, awaiter.operations())
}
