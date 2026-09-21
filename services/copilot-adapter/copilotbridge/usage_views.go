package copilotbridge

//go:generate go run github.com/jmattheis/goverter/cmd/goverter@v1.10.0 gen ./

import (
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/github/copilot-sdk/go/rpc"
)

// iUsageConverter derives measurement projections between upstream generated
// SDK payloads and our OpenAPI contract. Only the event discriminator is policy.
// goverter:converter
// goverter:output:file ./usage_views_gen.go
// goverter:output:package github.com/candacelabs/csf/services/copilot-adapter/copilotbridge
// goverter:matchIgnoreCase
type iUsageConverter interface {
	// goverter:map Duration ApiDurationMs
	// goverter:map Cost BillingMultiplier
	// goverter:ignore Kind PremiumRequests NanoAiu
	ModelCall(row rpc.AssistantUsageData) api.UsageObservation
	// goverter:map TotalPremiumRequests PremiumRequests
	// goverter:map TotalNanoAiu NanoAiu
	// goverter:ignore Kind Model ApiCallId ProviderCallId ServiceRequestId InputTokens OutputTokens CacheReadTokens CacheWriteTokens ReasoningTokens ApiDurationMs BillingMultiplier
	Checkpoint(row rpc.SessionUsageCheckpointData) api.UsageObservation
	// goverter:map TotalPremiumRequests PremiumRequests
	// goverter:map TotalNanoAiu NanoAiu
	// goverter:ignore Kind Model ApiCallId ProviderCallId ServiceRequestId InputTokens OutputTokens CacheReadTokens CacheWriteTokens ReasoningTokens ApiDurationMs BillingMultiplier
	Shutdown(row rpc.SessionShutdownData) api.UsageObservation
}

var usageViews iUsageConverter
