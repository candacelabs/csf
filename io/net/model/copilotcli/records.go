// Copyright 2026 Candace Labs

package copilotcli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// copilotAssistantMessage is the part of Copilot's assistant.message record
// the translation reads.
type copilotAssistantMessage struct {
	Data struct {
		Model        string `json:"model"`
		Content      string `json:"content"`
		ToolRequests []struct {
			ToolCallID string `json:"toolCallId"`
			Name       string `json:"name"`
			// Arguments is the tool's input, whose shape each tool chooses.
			Arguments map[string]json.RawMessage `json:"arguments"`
		} `json:"toolRequests"`
	} `json:"data"`
}

// The Claude Code stream-json shapes the translation writes.
type (
	streamAssistant struct {
		Type      string          `json:"type"`
		SessionID string          `json:"session_id"`
		Message   streamMessage   `json:"message"`
		Copilot   json.RawMessage `json:"copilot"`
	}
	streamMessage struct {
		Role    string        `json:"role"`
		Model   string        `json:"model,omitempty"`
		Content []streamBlock `json:"content"`
	}
	streamBlock struct {
		Type  string                     `json:"type"`
		Text  string                     `json:"text,omitempty"`
		ID    string                     `json:"id,omitempty"`
		Name  string                     `json:"name,omitempty"`
		Input map[string]json.RawMessage `json:"input,omitempty"`
	}
	streamResult struct {
		Type         string          `json:"type"`
		Subtype      string          `json:"subtype"`
		IsError      bool            `json:"is_error"`
		SessionID    string          `json:"session_id"`
		DurationMS   int64           `json:"duration_ms"`
		Usage        *streamUsage    `json:"usage,omitempty"`
		Copilot      json.RawMessage `json:"copilot"`
		CopilotUsage json.RawMessage `json:"copilot_usage,omitempty"`
	}
	streamUsage struct {
		InputTokens         int64 `json:"input_tokens"`
		OutputTokens        int64 `json:"output_tokens"`
		CacheReadTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
	}
)

// assistantEvent translates an assistant.message record: its text is one
// text block, and each tool request a tool_use block under the Claude Code
// tool name, with a file tool's arguments named as Claude Code names them
// (argumentNames), so an edit reads the same from either executor.
func assistantEvent(session uuid.UUID, record []byte) (Event, error) {
	var message copilotAssistantMessage
	if err := json.Unmarshal(record, &message); err != nil {
		return Event{}, err
	}
	blocks := []streamBlock{}
	if message.Data.Content != "" {
		blocks = append(blocks, streamBlock{Type: blockText, Text: message.Data.Content})
	}
	for _, request := range message.Data.ToolRequests {
		input := map[string]json.RawMessage{}
		for key, value := range request.Arguments {
			if claude, renamed := argumentNames[key]; renamed {
				key = claude
			}
			input[key] = value
		}
		blocks = append(blocks, streamBlock{Type: blockToolUse, ID: request.ToolCallID, Name: claudeToolName(request.Name), Input: input})
	}
	raw, err := json.Marshal(streamAssistant{
		Type: EventTypeAssistant, SessionID: session.String(),
		Message: streamMessage{Role: roleAssistant, Model: message.Data.Model, Content: blocks},
		Copilot: record,
	})
	if err != nil {
		return Event{}, err
	}
	return Event{Type: EventTypeAssistant, Raw: raw}, nil
}

// copilotToolComplete is the part of Copilot's tool.execution_complete record
// the translation reads, as the Copilot SDK's session events spell it: the
// call it completes, whether it succeeded, and its result or error.
type copilotToolComplete struct {
	Data struct {
		ToolCallID string `json:"toolCallId"`
		Success    *bool  `json:"success"`
		Result     struct {
			Content         string `json:"content"`
			DetailedContent string `json:"detailedContent"`
		} `json:"result"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"data"`
}

// The Claude Code stream-json tool result the translation writes.
type (
	streamToolResult struct {
		Type      string           `json:"type"`
		SessionID string           `json:"session_id"`
		Message   streamResultBody `json:"message"`
		Copilot   json.RawMessage  `json:"copilot"`
	}
	streamResultBody struct {
		Role    string              `json:"role"`
		Content []streamResultBlock `json:"content"`
	}
	streamResultBlock struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
		Content   string `json:"content"`
		IsError   bool   `json:"is_error"`
	}
)

// toolResultEvent translates a tool.execution_complete record into the user
// message carrying one tool_result block that Claude Code writes for a
// finished call: the call's identifier, the result's detailed content (or its
// content), or the error's message when the call failed.
func toolResultEvent(session uuid.UUID, record []byte) (Event, error) {
	var complete copilotToolComplete
	if err := json.Unmarshal(record, &complete); err != nil {
		return Event{}, err
	}
	data := complete.Data
	if data.ToolCallID == "" {
		return Event{}, errors.New("tool completion has no toolCallId")
	}
	failed := data.Success != nil && !*data.Success || data.Error.Message != ""
	content := data.Result.DetailedContent
	if content == "" {
		content = data.Result.Content
	}
	if failed && data.Error.Message != "" {
		content = data.Error.Message
	}
	raw, err := json.Marshal(streamToolResult{
		Type: EventTypeUser, SessionID: session.String(),
		Message: streamResultBody{Role: roleUser, Content: []streamResultBlock{{Type: blockResult, ToolUseID: data.ToolCallID, Content: content, IsError: failed}}},
		Copilot: record,
	})
	if err != nil {
		return Event{}, err
	}
	return Event{Type: EventTypeUser, Raw: raw}, nil
}

// copilotResult is the part of Copilot's result record the translation reads.
type copilotResult struct {
	SessionID string `json:"sessionId"`
	ExitCode  int    `json:"exitCode"`
	Usage     struct {
		SessionDurationMS int64 `json:"sessionDurationMs"`
	} `json:"usage"`
}

// copilotUsageFile is the part of the usage file the translation reads: the
// session's token counts by kind.
type copilotUsageFile struct {
	TokenDetails struct {
		Input      copilotTokenCount `json:"input"`
		Output     copilotTokenCount `json:"output"`
		CacheRead  copilotTokenCount `json:"cache_read"`
		CacheWrite copilotTokenCount `json:"cache_write"`
	} `json:"tokenDetails"`
}

type copilotTokenCount struct {
	TokenCount int64 `json:"tokenCount"`
}

// translatedResult is a result event and what the executor checks in it.
type translatedResult struct {
	event    Event
	session  string
	exitCode int
}

// resultEvent translates the result record: Copilot's exit code becomes the
// subtype and is_error, and the usage file, when there is one, the token and
// cache usage. The file itself is kept under "copilot_usage", with the AI
// credits and premium requests the turn cost.
func resultEvent(record []byte, usage json.RawMessage) (translatedResult, error) {
	var result copilotResult
	if err := json.Unmarshal(record, &result); err != nil {
		return translatedResult{}, err
	}
	translated := streamResult{
		Type: EventTypeResult, Subtype: subtypeSuccess, SessionID: result.SessionID,
		DurationMS: result.Usage.SessionDurationMS, Copilot: record, CopilotUsage: usage,
	}
	if result.ExitCode != 0 {
		translated.Subtype, translated.IsError = subtypeError, true
	}
	if usage != nil {
		var counts copilotUsageFile
		if err := json.Unmarshal(usage, &counts); err != nil {
			return translatedResult{}, fmt.Errorf("usage file: %w", err)
		}
		details := counts.TokenDetails
		translated.Usage = &streamUsage{
			InputTokens: details.Input.TokenCount, OutputTokens: details.Output.TokenCount,
			CacheReadTokens: details.CacheRead.TokenCount, CacheCreationTokens: details.CacheWrite.TokenCount,
		}
	}
	raw, err := json.Marshal(translated)
	if err != nil {
		return translatedResult{}, err
	}
	return translatedResult{event: Event{Type: EventTypeResult, Raw: raw}, session: result.SessionID, exitCode: result.ExitCode}, nil
}
