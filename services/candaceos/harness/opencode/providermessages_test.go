package opencode

import (
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	opencodesdk "github.com/sst/opencode-sdk-go"
	"github.com/sst/opencode-sdk-go/shared"
)

// Fixture transcripts are built from the OpenCode SDK's own generated types and
// then serialized and decoded back through the SDK's real unmarshaling path.
//
// Both halves are load-bearing. Building from SDK types makes provider schema
// drift a compile error here rather than a silent fixture lie, so no
// hand-rolled mirror of Message, Part, ToolState, or the error union exists in
// this suite. Decoding is not stylistic either: the SDK models each of those as
// a discriminated union whose variant lives in an unexported field that only
// UnmarshalJSON populates, so a hand-constructed Message has a nil AsUnion and
// would project nothing at all.
//
// Raw JSON survives in exactly one place - the SSE envelope and the two
// endpoints the pinned SDK has no types for - and each is named as an SDK gap
// where it is declared.

const fixtureSessionID = "ses_exact"

// fixtureModel is the model every fixture prompt is submitted with.
var fixtureModel = promptModel{ProviderID: "openrouter", ModelID: "openai/gpt-5.4-nano"}

func fixtureMillis() float64 { return float64(time.Now().UnixMilli()) }

// decodeMessage marshals one SDK-typed message and decodes it the way the
// transport would, so its unions are populated. It reports an error rather than
// asserting, because the scripted provider builds messages on the runtime's own
// goroutine where a Ginkgo failure has no spec to land on.
func decodeMessage(info any, parts ...any) (providerMessage, error) {
	if parts == nil {
		parts = []any{}
	}
	encoded, err := json.Marshal(map[string]any{"info": info, "parts": parts})
	if err != nil {
		return providerMessage{}, fmt.Errorf("encoding fixture message: %w", err)
	}
	var message providerMessage
	if err := json.Unmarshal(encoded, &message); err != nil {
		return providerMessage{}, fmt.Errorf("decoding fixture message: %w", err)
	}
	return message, nil
}

// transcriptMessage unwraps a fixture message inside a spec, failing the spec if
// the fixture itself is malformed. Spec goroutines only.
func transcriptMessage(message providerMessage, err error) providerMessage {
	GinkgoHelper()
	Expect(err).NotTo(HaveOccurred())
	return message
}

// textPart builds one visible text part of a message.
func textPart(messageID, content string) opencodesdk.TextPart {
	return opencodesdk.TextPart{
		ID: "prt_" + messageID, MessageID: messageID, SessionID: fixtureSessionID,
		Type: opencodesdk.TextPartTypeText, Text: content,
	}
}

// completedToolPart builds one tool call the provider ran to completion.
func completedToolPart(partID, messageID, callID, output string, at float64) opencodesdk.ToolPart {
	return opencodesdk.ToolPart{
		ID: partID, CallID: callID, MessageID: messageID, SessionID: fixtureSessionID,
		Type: opencodesdk.ToolPartTypeTool, Tool: "inspect",
		State: opencodesdk.ToolPartState{
			Status: opencodesdk.ToolPartStateStatusCompleted,
			Input:  map[string]any{"path": "README.md"},
			Output: output, Title: "Inspect workspace",
			Metadata: map[string]any{},
			Time:     opencodesdk.ToolStateCompletedTime{Start: at - 1, End: at},
		},
	}
}

// interruptedToolPart builds one tool call the operator's abort cut short.
func interruptedToolPart(messageID string) opencodesdk.ToolPart {
	return opencodesdk.ToolPart{
		ID: "prt_interrupted_tool", CallID: "call_interrupted", MessageID: messageID,
		SessionID: fixtureSessionID, Type: opencodesdk.ToolPartTypeTool, Tool: "bash",
		State: opencodesdk.ToolPartState{
			Status: opencodesdk.ToolPartStateStatusError,
			Error:  "Tool execution aborted",
			Input:  map[string]any{},
			Metadata: map[string]any{
				"interrupted": true, "output": "Interrupted by operator",
			},
			Time: opencodesdk.ToolStateErrorTime{Start: fixtureMillis() - 1, End: fixtureMillis()},
		},
	}
}

// providerFailure builds the SDK's error union for one failure name, choosing
// the typed data variant that name carries on the wire.
func providerFailure(name, message string) *opencodesdk.AssistantMessageError {
	failure := &opencodesdk.AssistantMessageError{
		Name: opencodesdk.AssistantMessageErrorName(name),
	}
	switch failure.Name {
	case opencodesdk.AssistantMessageErrorNameProviderAuthError:
		failure.Data = shared.ProviderAuthErrorData{Message: message, ProviderID: "openrouter"}
	case opencodesdk.AssistantMessageErrorNameMessageAbortedError:
		failure.Data = shared.MessageAbortedErrorData{Message: message}
	default:
		failure.Data = shared.UnknownErrorData{Message: message}
	}
	return failure
}

// userMessage builds one operator prompt as it appears in the transcript.
func userMessage(id, content string) (providerMessage, error) {
	return decodeMessage(
		opencodesdk.UserMessage{
			ID: id, SessionID: fixtureSessionID, Role: opencodesdk.UserMessageRoleUser,
			Time: opencodesdk.UserMessageTime{Created: fixtureMillis()},
		},
		textPart(id, content),
	)
}

// assistantMessage builds one assistant reply, in progress when completed is
// nil and terminal when it carries a completion time or a failure.
func assistantMessage(
	id, parentID, content string,
	completed *float64,
	failure *opencodesdk.AssistantMessageError,
) (providerMessage, error) {
	return assistantMessageWithParts(id, parentID, completed, failure, textPart(id, content))
}

// assistantMessageWithParts builds one assistant reply carrying arbitrary
// parts, so a tool call decodes inside the same message as its text.
func assistantMessageWithParts(
	id, parentID string,
	completed *float64,
	failure *opencodesdk.AssistantMessageError,
	parts ...any,
) (providerMessage, error) {
	info := opencodesdk.AssistantMessage{
		ID: id, ParentID: parentID, SessionID: fixtureSessionID,
		Role:       opencodesdk.AssistantMessageRoleAssistant,
		ProviderID: fixtureModel.ProviderID, ModelID: fixtureModel.ModelID,
		Time: opencodesdk.AssistantMessageTime{Created: fixtureMillis()},
	}
	if completed != nil {
		info.Time.Completed = *completed
	}
	if failure != nil {
		info.Error = *failure
	}
	return decodeMessage(info, parts...)
}

// toolAssistantMessage builds one terminal assistant reply whose work was a
// single completed tool call.
func toolAssistantMessage(id, parentID, callID, output string, at float64) (providerMessage, error) {
	return assistantMessageWithParts(
		id, parentID, &at, nil,
		textPart(id, ""),
		completedToolPart("prt_"+callID, id, callID, output, at),
	)
}
