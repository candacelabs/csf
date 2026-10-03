package opencode

import (
	"encoding/json"
	"strings"
	"time"

	opencodesdk "github.com/sst/opencode-sdk-go"
	"google.golang.org/protobuf/types/known/structpb"
)

// Normalization of provider-authored text, timestamps, and errors into the
// bounded, well-typed values a HarnessEvent may carry.

// maxProjectedTextBytes bounds any provider-authored text copied into an event.
const maxProjectedTextBytes = 64 << 10

const truncationNotice = "\n[OpenCode output truncated]"

func assistantErrorMessage(providerError opencodesdk.AssistantMessageError) string {
	switch value := providerError.AsUnion().(type) {
	case opencodesdk.ProviderAuthError:
		return value.Data.Message
	case opencodesdk.UnknownError:
		return value.Data.Message
	case opencodesdk.MessageAbortedError:
		return value.Data.Message
	case opencodesdk.AssistantMessageErrorAPIError:
		return value.Data.Message
	default:
		return string(providerError.Name)
	}
}

func normalizedAssistantError(providerError opencodesdk.AssistantMessageError) string {
	failure := boundedText(assistantErrorMessage(providerError))
	if failure == "" {
		return "OpenCode provider failed"
	}
	return failure
}

// partsText concatenates the visible text of one message. Parts the provider
// marked ignored are internal reasoning scaffolding and are not operator text.
func partsText(parts []providerPart) string {
	var content strings.Builder
	for _, entry := range parts {
		if text, isText := entry.AsUnion().(opencodesdk.TextPart); isText && !textPartIgnored(text) {
			content.WriteString(text.Text)
		}
	}
	return content.String()
}

// textPartIgnored reads the one field OpenCode 1.18.21 added after the pinned
// SDK's schema snapshot. This single-field overlay is the only message-part
// compatibility escape hatch in the package.
func textPartIgnored(part opencodesdk.TextPart) bool {
	raw := part.JSON.RawJSON()
	if raw == "" {
		return false
	}
	var extra struct {
		Ignored bool `json:"ignored"`
	}
	return json.Unmarshal([]byte(raw), &extra) == nil && extra.Ignored
}

func structValue(value any) *structpb.Value {
	structured, err := structpb.NewValue(value)
	if err != nil {
		return nil
	}
	return structured
}

// timestampAt converts provider milliseconds to a wall-clock instant, falling
// back to now when the provider left the field unset.
func timestampAt(milliseconds int64) time.Time {
	return timestampOr(milliseconds, time.Now().UTC())
}

func timestampOr(milliseconds int64, fallback time.Time) time.Time {
	if milliseconds <= 0 {
		return fallback
	}
	return time.UnixMilli(milliseconds).UTC()
}

// boundedText caps provider-authored text so one runaway tool output cannot
// dominate an event stream Core must persist and render.
func boundedText(value string) string {
	if len(value) <= maxProjectedTextBytes {
		return value
	}
	return value[:maxProjectedTextBytes-len(truncationNotice)] + truncationNotice
}
