// Copyright 2026 Candace Labs

package sessiongate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/candacelabs/csf/pkg/textbound"
	"github.com/candacelabs/csf/services/harness/session"
)

// RuleSearchChain is a turn's third grep or find call in a row: the agent is
// finding code by hand where the knowledge index answers in one call.
const RuleSearchChain Rule = "search_chain"

const (
	// searchChainLimit is the chain length the search gate denies: the call
	// that would be the third search in a row.
	searchChainLimit = 3
	// searchSnippetBytes bounds a command quoted in the rejection.
	searchSnippetBytes = 80
	csfVerbSearch      = "search"
	searchReplacement  = `ask the index instead: csf search "<what you are looking for>" prints ranked path:line hits with why`
	// sessionGateRejection opens a wait or search gate rejection.
	sessionGateRejection = "Rejected by the CSF session gate. "
)

// searchCommands are the programs that search files by name or content.
var searchCommands = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true, "ack": true, "find": true, "fd": true}

// IsSearchCall reports a tool call the search gate counts: a Grep or Glob
// call, or a Bash command that runs grep, egrep, fgrep, rg, ag, ack, find or
// fd anywhere in it, including in a pipeline, a list or sh -c. A command that
// runs csf search asks the index and is not one.
func IsSearchCall(tool session.ToolUse) bool {
	_, ok := describeToolUse(tool)
	return ok
}

// describeToolUse is [SearchCall] over a logged tool call. An input that is
// not a JSON object describes no search.
func describeToolUse(tool session.ToolUse) (string, bool) {
	var input ToolInput
	if json.Unmarshal(tool.Input, &input) != nil {
		return "", false
	}
	return SearchCall(tool.Name, input)
}

// SearchCall describes a tool call [IsSearchCall] reports, as the search
// gate's rejection quotes it; ok is false for every other call.
func SearchCall(tool string, input ToolInput) (described string, ok bool) {
	switch tool {
	case session.ToolGrep, session.ToolGlob:
		described = fmt.Sprintf("%s %q", tool, input.Pattern)
		if input.Path != "" {
			described += fmt.Sprintf(" in %q", input.Path)
		}
		return described, true
	case session.ToolBash:
		if !runsSearch(input.Command) {
			return "", false
		}
		return fmt.Sprintf("%s %q", tool, textbound.Prefix(firstLine(input.Command), searchSnippetBytes)), true
	}
	return "", false
}

// runsSearch reports a command that runs a search program and does not ask
// the index. A command the shell parser cannot read is not one.
func runsSearch(command string) bool {
	searches, err := runsCommand(command, func(name string, _ []string) bool { return searchCommands[name] })
	if err != nil || !searches {
		return false
	}
	asks, err := runsCommand(command, isCSFSearch)
	return err == nil && !asks
}

func isCSFSearch(name string, arguments []string) bool {
	return name == commandCSF && len(arguments) > 0 && arguments[0] == csfVerbSearch
}

// SearchChain is the chain of search calls the call being judged would end:
// the consecutive searches that end earlier, the turn's calls before it, then
// the call itself, each described as [SearchCall] describes it. Any other call
// breaks a chain. A call that is not a search ends none, and the chain is nil.
func SearchChain(earlier []session.ToolUse, tool string, input ToolInput) []string {
	current, ok := SearchCall(tool, input)
	if !ok {
		return nil
	}
	chain := []string{current}
	for index := len(earlier) - 1; index >= 0; index-- {
		described, ok := describeToolUse(earlier[index])
		if !ok {
			break
		}
		chain = append(chain, described)
	}
	slices.Reverse(chain)
	return chain
}

// callsBefore is the tool calls of the turn's records before the one with id
// toolUseID. The turn executor usually logs a call before its hook runs, but
// not always: when the log does not hold it yet, every call is before it.
func callsBefore(records []session.Record, toolUseID string) []session.ToolUse {
	calls := []session.ToolUse{}
	for index := range records {
		record := &records[index]
		if record.Direction != session.DirectionOut {
			continue
		}
		_, tools := record.Assistant()
		calls = append(calls, tools...)
	}
	if toolUseID == "" {
		return calls
	}
	if index := slices.IndexFunc(calls, func(call session.ToolUse) bool { return call.ID == toolUseID }); index >= 0 {
		return calls[:index]
	}
	return calls
}

// search is the PreToolUse gate on Grep, Glob and Bash that stops a chain of
// searches: the third search call in a row of the turn is denied, naming the
// chain and csf search, and the denial is logged. An event log that cannot
// be read allows the call and logs why: the gate never blocks on its own
// failure.
func (call *gateCall) search(ctx context.Context) *HookOutput {
	if _, ok := SearchCall(call.hook.ToolName, call.hook.ToolInput); !ok {
		return nil
	}
	records, err := session.ReadTurnRecords(call.gate.directory)
	if err != nil {
		call.failure(ctx, GateSearch, DecisionAllow, err)
		return nil
	}
	chain := SearchChain(callsBefore(records, call.hook.ToolUseID), call.hook.ToolName, call.hook.ToolInput)
	if len(chain) < searchChainLimit {
		return nil
	}
	reason := sessionGateRejection + searchChainMessage(chain)
	call.record(ctx, GateSearch, DecisionDeny, slog.Any(keyRules, []string{string(RuleSearchChain)}), slog.String(keyReason, reason))
	return &HookOutput{HookSpecificOutput: &HookSpecificOutput{
		HookEventName:            session.HookPreToolUse,
		PermissionDecision:       permissionDeny,
		PermissionDecisionReason: reason,
	}}
}

func searchChainMessage(chain []string) string {
	return fmt.Sprintf("%s: %d grep/find calls in a row (%s); %s", RuleSearchChain, len(chain), strings.Join(chain, listSeparator), searchReplacement)
}
