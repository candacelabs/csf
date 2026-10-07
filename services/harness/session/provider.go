// Copyright 2026 Candace Labs

package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/services/harness/provider"
)

// The inference provider's records on a session's event log.
const (
	// EventTypeProviderApplied records the provider a session was launched
	// through and the spelling its model resolved to, with no credential.
	EventTypeProviderApplied = "harness_provider_applied"
	// EventTypeProviderFallback records a turn the provider's first spelling
	// could not serve, retried on the next one.
	EventTypeProviderFallback = "harness_provider_fallback"
	// KeyProviderBaseURL is the provider's endpoint.
	KeyProviderBaseURL = "provider_base_url"
	// KeyProviderModel is the provider spelling the session's model resolved
	// to, and KeyProviderNextModel the spelling a fallback moved to.
	KeyProviderModel     = "provider_model"
	KeyProviderNextModel = "provider_next_model"
	// KeyProviderReason is why the provider could not serve the spelling, as
	// the executor reported it.
	KeyProviderReason = "provider_reason"
	// KeyModel is the CSF model name the recipe asked for.
	KeyModel = "model"
)

// terminalAPIError is the executor's terminal reason for a request the
// provider refused or could not serve: measured on 2026-10-05 against an
// unavailable upstream, which answers 503 and ends the turn with this reason
// on an is_error result.
const terminalAPIError = "api_error"

// ErrProviderUnavailable reports a turn the provider could not serve on the
// spelling it was asked for: the fallback retries it on the next spelling, and
// a turn with none left fails with this error.
var ErrProviderUnavailable = errors.New("harness session: the provider could not serve the model")

// recordProvider records the provider a session is launched through and the
// spelling its model resolved to at attempt. The record carries the endpoint
// and the spelling, never the key. Only a Claude Code session reaches the
// provider this way: a Copilot session runs its own configured provider.
func (runner *AgentSessionRunner) recordProvider(ctx context.Context, log *EventLog, state *RunState, plan *pb.AgentAssignmentPlan, attempt int) {
	if runner.provider == nil || ExecutorOf(plan.GetRecipe()) != ExecutorClaudeCode {
		return
	}
	model := plan.GetRecipe().GetModel()
	spelling, served := runner.providerModel(model, attempt)
	if !served {
		return
	}
	log.Record(ctx, state.SessionID, state.Turns, EventTypeProviderApplied, "session launched through the inference provider",
		slog.String(KeyProviderBaseURL, runner.provider.BaseURL()),
		slog.String(KeyModel, model), slog.String(KeyProviderModel, spelling))
}

// fallBack moves the open session to the next provider spelling of its model
// after the provider could not serve the current one: it closes the turn
// executor, opens it again on the next spelling, resumed on the same
// conversation, and records the move. It reports whether the session moved;
// one with no spelling left does not, and the caller keeps the original
// failure. A fallback that cannot open the executor returns its error.
func (open *OpenSession) fallBack(ctx context.Context, cause error) (bool, error) {
	runner := open.runner
	if runner.provider == nil || ExecutorOf(open.plan.GetRecipe()) != ExecutorClaudeCode {
		return false, nil
	}
	model := open.plan.GetRecipe().GetModel()
	next := open.attempt + 1
	spelling, served := runner.providerModel(model, next)
	if !served {
		return false, nil
	}
	previous, _ := runner.providerModel(model, open.attempt)
	arguments, environment, err := runner.launchAttempt(open.plan, open.Directory(), open.settings, next)
	if err != nil {
		return false, err
	}
	if err := open.executor.Close(ctx); err != nil {
		open.log.Failure(open.ctx, open.state.SessionID, open.state.Turns, EventTypeProviderFallback,
			"provider fallback: the turn executor did not close", err)
	}
	open.executor = nil
	spec := open.spec
	spec.Arguments = arguments
	// The provider's environment is the executor's own, so the attempt's is
	// put in place of the one the previous attempt was opened with.
	spec.Environment = append(withoutProviderEnvironment(spec.Environment), environment...)
	spec.Resume = open.state.Turns > 1
	executor, err := runner.openExecutors(ctx, spec)
	if err != nil {
		return false, err
	}
	open.executor, open.spec, open.attempt = executor, spec, next
	open.log.Record(open.ctx, open.state.SessionID, open.state.Turns, EventTypeProviderFallback,
		"provider fallback: the turn is retried on the next spelling",
		slog.String(KeyModel, model), slog.String(KeyProviderModel, previous),
		slog.String(KeyProviderNextModel, spelling), slog.String(KeyProviderReason, cause.Error()))
	return true, nil
}

// withoutProviderEnvironment is environment with every variable the provider
// owns removed, so an attempt's environment replaces the previous attempt's
// rather than being shadowed by it.
func withoutProviderEnvironment(environment []string) []string {
	owned := []string{
		provider.EnvironmentBaseURL + "=",
		provider.EnvironmentAPIKey + "=",
		provider.EnvironmentHaikuModel + "=",
		provider.EnvironmentConfigDirectory + "=",
		provider.EnvironmentMaxRetries + "=",
	}
	kept := make([]string, 0, len(environment))
	for _, variable := range environment {
		if !slices.ContainsFunc(owned, func(prefix string) bool { return strings.HasPrefix(variable, prefix) }) {
			kept = append(kept, variable)
		}
	}
	return kept
}

// WithProvider launches every session through router, the host's inference
// provider: each turn executor is given the provider's environment, its
// --model is the provider's spelling of the recipe's CSF model, and a turn the
// first spelling cannot serve is retried on the next.
func WithProvider(router *provider.Router) AgentSessionRunnerOption {
	return func(runner *AgentSessionRunner) error {
		if router == nil {
			return fmt.Errorf("%w: nil provider router", ErrInvalidOption)
		}
		runner.provider = router
		return nil
	}
}

// providerModel is the provider spelling the session's model resolves to at
// attempt, counted from zero, and whether the provider has that many
// spellings. A runner with no provider serves the recipe's own model name.
func (runner *AgentSessionRunner) providerModel(model string, attempt int) (string, bool) {
	if runner.provider == nil {
		return model, attempt == 0
	}
	spellings := runner.provider.Spellings(model)
	if attempt >= len(spellings) {
		return "", false
	}
	return spellings[attempt], true
}

// providerEnvironment is the provider's launch environment, read now: it
// carries the API key, so it goes straight into the executor's process
// environment and is never recorded. A runner with no provider adds nothing.
func (runner *AgentSessionRunner) providerEnvironment() ([]string, error) {
	if runner.provider == nil {
		return nil, nil
	}
	return runner.provider.Launch()
}

// providerResult is the part of the executor's result event a fallback reads.
type providerResult struct {
	IsError bool `json:"is_error"`
	// TerminalReason is why the executor ended the turn.
	TerminalReason string `json:"terminal_reason"`
	// Result is the executor's own message, which names the gateway and the
	// status; it carries no credential.
	Result string `json:"result"`
}

// ProviderUnavailable reports whether the provider could not serve the
// spelling the turn asked for, which is what a fallback retries, and the
// reason the executor gave. The executor reports it either way: as the result
// event of a proposal it completed, or inside the [claudecode.TurnError] of
// one it did not. A turn that failed for any other cause is not a fallback:
// the model answered and the session keeps its turn.
func ProviderUnavailable(proposal *model.Proposal[claudecode.Event], err error) (string, bool) {
	var events []claudecode.Event
	var turnErr *claudecode.TurnError
	switch {
	case errors.As(err, &turnErr):
		events = turnErr.Events
	case proposal != nil:
		events = proposal.Actions
	}
	for _, event := range events {
		if event.Type != EventTypeResult {
			continue
		}
		var result providerResult
		if json.Unmarshal(event.Raw, &result) != nil {
			continue
		}
		if result.IsError && result.TerminalReason == terminalAPIError {
			return result.Result, true
		}
	}
	return "", false
}
