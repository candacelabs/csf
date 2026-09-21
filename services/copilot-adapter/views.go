package copilotadapter

//go:generate go run github.com/jmattheis/goverter/cmd/goverter@v1.10.0 gen ./

import (
	"time"

	"github.com/guregu/null/v5"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

// iViewConverter is the projection from sqlc's rows to the contract's models.
// goverter generates the implementation (views_gen.go) from this declaration,
// so the mapping between two generated types is itself generated. The
// handwritten conversions below normalize timestamps; nullable SQL text
// carries absence through null.String until the API boundary.
//
// goverter:converter
// goverter:output:file ./views_gen.go
// goverter:output:package github.com/candacelabs/csf/services/copilot-adapter
// goverter:matchIgnoreCase
// goverter:extend utcTime utcTimePointer nullTimePointer
// goverter:extend github.com/guregu/null/v5:IntFromPtr github.com/guregu/null/v5:FloatFromPtr github.com/guregu/null/v5:StringFromPtr
type iViewConverter interface {
	WorkspaceWorktree(row storedb.Worktree) api.WorkspaceWorktree
	WorkspaceTaskLink(row storedb.SessionTask) api.WorkspaceTaskLink
	TraceDelivery(row storedb.ClaimTraceDeliveryRow) storedb.TraceDelivery

	// goverter:map DurationSeconds.Ptr DurationSeconds
	// goverter:map InputTokens.Ptr InputTokens
	// goverter:map OutputTokens.Ptr OutputTokens
	// goverter:map CacheReadTokens.Ptr CacheReadTokens
	// goverter:map CacheWriteTokens.Ptr CacheWriteTokens
	// goverter:map ReasoningTokens.Ptr ReasoningTokens
	// goverter:map ApiDurationMs.Ptr ApiDurationMs
	// goverter:map PremiumRequests.Ptr PremiumRequests
	// goverter:map LatestTurnStatus.Ptr LatestTurnStatus
	// goverter:map LatestTurnStartedAt LatestStartedAt
	// goverter:map LatestTurnCompletedAt LatestCompletedAt
	SessionTelemetry(row storedb.ListSessionTelemetryRow) api.SessionTelemetry

	// goverter:ignore SessionID EventID TurnID OccurredAt ProviderEvent
	UsageInsert(row api.UsageObservation) storedb.InsertProviderUsageEventParams
	// goverter:map Status State
	TraceCount(row storedb.CountTraceDeliveriesRow) api.TraceDeliveryCount

	Turn(row storedb.Turn) api.Turn
	TurnEventVersion(row storedb.TurnEventVersion) api.Turn
	// goverter:map Body Text
	// goverter:map Author.Ptr Author
	// goverter:map ToolName.Ptr ToolName
	// goverter:map ToolCallID.Ptr ToolCallId
	TranscriptItem(row storedb.TranscriptItem) api.TranscriptItem
	// goverter:map ToolName.Ptr ToolName
	SessionRequest(row storedb.PendingRequest) api.SessionRequest
	// goverter:map ToolName.Ptr ToolName
	SessionRequestEventVersion(row storedb.RequestEventVersion) api.SessionRequest
	// goverter:ignore TurnCount
	Session(row storedb.Session) api.Session
	SessionEventVersion(row storedb.SessionEventVersion) api.Session
	// goverter:map Summary.Ptr Summary
	SubagentEventVersion(row storedb.SubagentEventVersion) api.Subagent
}

// views is the generated converter, held at the one seam that uses it; the
// tagged views_seam.go assigns it, because that file is what goverter hides
// from itself while it regenerates.
var views iViewConverter

func utcTime(value time.Time) time.Time { return value.UTC() }

func utcTimePointer(value time.Time) *time.Time {
	utc := value.UTC()
	return &utc
}

func nullTimePointer(value null.Time) *time.Time {
	if !value.Valid {
		return nil
	}
	utc := value.Time.UTC()
	return &utc
}
