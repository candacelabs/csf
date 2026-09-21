// Package copilotadapter is the HTTP adapter that fronts a Copilot CLI
// session with the contract in openapi.yaml. It is a library (CS-10): the
// binary in cmd/ reads flags and environment, this package never does.
package copilotadapter

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

// ErrNoActiveTurn means an abort cannot select a turn without guessing.
var ErrNoActiveTurn = errors.New("copilot adapter: no active turn")

// ErrAbortPending means an earlier abort RPC still awaits its terminal SDK
// event. A second abort must not replace that event's exact target.
var ErrAbortPending = errors.New("copilot adapter: an earlier abort is still pending")

// ErrAbortTargetMismatch means the caller's durable expected turn is no
// longer the CLI's foreground turn. The bridge must not guess and abort a
// successor.
var ErrAbortTargetMismatch = errors.New("copilot adapter: active turn does not match the abort target")

// ErrInvalidWorktree means a persisted path definitively falls outside the
// configured repository boundary. Callers may quarantine only this class;
// git, filesystem and cancellation failures leave persisted state untouched.
var ErrInvalidWorktree = errors.New("copilot adapter: invalid worktree")

// ErrBridgeSessionMissing means the Copilot SDK has proved that a persisted
// adapter session no longer exists in its durable session store. Only this
// class may terminalize that session during restart; resume dependency errors
// must leave it retryable.
var ErrBridgeSessionMissing = errors.New("copilot adapter: bridge session missing")

// BridgeEventKind names what a BridgeEvent reports. It mirrors the contract's
// SessionEventKind without importing the generated HTTP types into the seam.
type BridgeEventKind string

const (
	// BridgeEventUsage preserves provider measurements even after turn completion.
	BridgeEventUsage BridgeEventKind = "usage"
	// BridgeEventAssistantDelta is a token delta on the assistant's reply.
	BridgeEventAssistantDelta BridgeEventKind = "assistantDelta"
	// BridgeEventAssistantMessage is one completed assistant message.
	BridgeEventAssistantMessage BridgeEventKind = "assistantMessage"
	// BridgeEventToolCall is the CLI invoking a tool.
	BridgeEventToolCall BridgeEventKind = "toolCall"
	// BridgeEventToolResult is a tool's output coming back.
	BridgeEventToolResult BridgeEventKind = "toolResult"
	// BridgeEventTurnStarted marks the exact FIFO turn the CLI began.
	BridgeEventTurnStarted BridgeEventKind = "turnStarted"
	// BridgeEventTurnCompleted closes the turn in flight.
	BridgeEventTurnCompleted BridgeEventKind = "turnCompleted"
	// BridgeEventTurnAborted closes the exact foreground turn canceled by the CLI.
	BridgeEventTurnAborted BridgeEventKind = "turnAborted"
	// BridgeEventTurnFailed closes the exact foreground turn whose SDK request failed.
	BridgeEventTurnFailed BridgeEventKind = "turnFailed"
	// BridgeEventRequestOpened raises one exact-identity permission request.
	BridgeEventRequestOpened BridgeEventKind = "requestOpened"
	// BridgeEventRequestCompleted reports the exact permission result observed
	// from the SDK, including decisions made by another attached client.
	BridgeEventRequestCompleted BridgeEventKind = "requestCompleted"
	// BridgeEventSubagentStarted opens one actual SDK subagent lifecycle.
	BridgeEventSubagentStarted BridgeEventKind = "subagentStarted"
	// BridgeEventSubagentCompleted closes a successful SDK subagent lifecycle.
	BridgeEventSubagentCompleted BridgeEventKind = "subagentCompleted"
	// BridgeEventSubagentFailed closes a failed SDK subagent lifecycle.
	BridgeEventSubagentFailed BridgeEventKind = "subagentFailed"
	// BridgeEventSubagentMessage is one completed subagent progress message.
	BridgeEventSubagentMessage BridgeEventKind = "subagentMessage"
	// BridgeEventSubagentProgress is the SDK's live assistant.intent update for
	// one identified subagent.
	BridgeEventSubagentProgress BridgeEventKind = "subagentProgress"
	// BridgeEventSubagentToolCall is a tool invoked by a subagent.
	BridgeEventSubagentToolCall BridgeEventKind = "subagentToolCall"
	// BridgeEventSubagentToolResult is a subagent tool's output.
	BridgeEventSubagentToolResult BridgeEventKind = "subagentToolResult"
	// BridgeEventFailed is reserved for an unrecoverable CLI session failure.
	BridgeEventFailed BridgeEventKind = "failed"
)

// BridgeRequestKind names the kind of decision the CLI is asking for.
type BridgeRequestKind string

const (
	// BridgeRequestPermission is the CLI asking to run a tool.
	BridgeRequestPermission BridgeRequestKind = "permission"
)

// ToolName identifies a tool the CLI can invoke. The protocol leaves the set
// open (MCP servers and custom tools register names at run time) and the SDK
// exports no enumeration of the built-ins, so this is a typed identifier
// rather than an enum; the names the SDK does export are re-exported typed
// from copilotbridge.
type ToolName string

// ToolCallID is the CLI's own identifier for one tool invocation; it pairs a
// result with the call that produced it.
type ToolCallID string

// BridgeEvent is one thing the CLI told the adapter. It is data, not
// behaviour, so it is a struct rather than an interface (CS-8's data-shaped
// test).
type BridgeEvent struct {
	// ID is the source event's stable identifier. The concrete SDK supplies its
	// event UUID; callback-originated events use the callback request UUID.
	ID         string
	Kind       BridgeEventKind
	OccurredAt time.Time
	TurnID     *uuid.UUID
	Text       string
	ToolName   ToolName
	// ToolCallID is the CLI's own identifier for one tool invocation. It
	// pairs a toolResult with the toolCall that produced it even when two
	// calls to the same tool overlap.
	ToolCallID  ToolCallID
	Author      string
	RequestID   *uuid.UUID
	RequestKind BridgeRequestKind
	// ResolutionDecision is approve or deny on RequestCompleted.
	ResolutionDecision string
	AgentID            string
	DisplayName        string
	SessionBusy        bool
	Usage              *api.UsageObservation
	UsagePayload       json.RawMessage
}

// BridgeSessionSpec is what the adapter asks the bridge to start.
type BridgeSessionSpec struct {
	SessionID uuid.UUID
	// AgentID is a host-scoped durable identity. The adapter carries it without
	// interpreting permissions or service-specific configuration.
	AgentID            string
	Model              string
	WorkingDirectory   string
	SystemInstructions string
	RestoredTurns      []BridgeRestoredTurn
}

// BridgeRestoredTurn preserves the scheduling lane a durable turn occupied
// before the adapter process restarted.
type BridgeRestoredTurn struct {
	ID              uuid.UUID
	Mode            api.PromptMode
	Status          api.TurnStatus
	DeliveryUnknown bool
}

// BridgeResolution is a decision routed back into a pending CLI request.
type BridgeResolution struct {
	RequestID uuid.UUID
	Decision  string
}

// BridgePrompt is one durable adapter turn handed to the CLI. TurnID is
// queued before Send crosses the external boundary, then paired with the
// SDK's assistant.turn_start identifier by the concrete bridge.
type BridgePrompt struct {
	TurnID uuid.UUID
	Text   string
	Mode   string
	Author string
}

// BridgePromptDelivery states what the bridge knows about one Send call after
// it returns. An error after crossing the SDK boundary is deliberately
// Unknown: the adapter keeps the durable turn in its distinct unknown state
// until a retry or later SDK event proves acceptance instead of guessing that
// delivery failed.
type BridgePromptDelivery uint8

const (
	// BridgePromptDeliveryUnknown means the bridge cannot prove whether the SDK
	// accepted the prompt.
	BridgePromptDeliveryUnknown BridgePromptDelivery = iota
	// BridgePromptDeliveryAccepted means the SDK accepted the prompt.
	BridgePromptDeliveryAccepted
	// BridgePromptDeliveryRejected means the prompt definitely did not cross the
	// SDK boundary and may be failed or retried safely.
	BridgePromptDeliveryRejected
)

// BridgeSession is the live handle on one CLI session. Every capability is a
// function value and the stream is a channel, so there is no behaviour left to
// abstract and no interface to exempt (CS-8's data-shaped test).
type BridgeSession struct {
	Events                <-chan BridgeEvent
	UsageHistory          func(ctx context.Context) ([]BridgeEvent, error)
	Send                  func(ctx context.Context, prompt BridgePrompt) (BridgePromptDelivery, error)
	AcknowledgeDelivery   func(turnID uuid.UUID)
	ActiveTurn            func() (uuid.UUID, bool)
	Abort                 func(ctx context.Context, expectedTurnID uuid.UUID) (uuid.UUID, error)
	SetModel              func(ctx context.Context, model string) error
	Resolve               func(ctx context.Context, resolution BridgeResolution) error
	AcknowledgeResolution func(requestID uuid.UUID)
	AbandonResolution     func(requestID uuid.UUID)
	Close                 func(ctx context.Context) error
}

// BridgeModel is one model the CLI reports.
type BridgeModel struct {
	ID           string
	DisplayName  string
	Capabilities []api.ModelCapability
}

// Repository describes one configured root the browser is allowed to select.
type Repository struct {
	ID          string
	DisplayName string
	Root        string
	DefaultRef  string
}

// PreparedWorktree is a validated working directory ready for a CLI session.
type PreparedWorktree struct {
	Repository Repository
	Path       string
	BaseRef    string
	Managed    bool
}

// WorktreeRequest asks the concrete adapter to reuse a configured root or
// create an isolated git worktree. No browser-supplied path crosses the seam.
type WorktreeRequest struct {
	RepositoryID string
	Mode         string
	BaseRef      string
	SessionID    uuid.UUID
}

// WorktreeSnapshot is current git state read from disk.
type WorktreeSnapshot struct {
	Branch  string
	HeadSHA string
	Clean   bool
	State   string
}

// WorktreeChange is one porcelain-v2 status entry.
type WorktreeChange struct {
	Path          string
	PreviousPath  string
	IndexState    string
	WorktreeState string
}

// WorktreeChanges is the bounded current diff plus file states.
type WorktreeChanges struct {
	HeadSHA   string
	Clean     bool
	Files     []WorktreeChange
	Patch     string
	Truncated bool
	Captured  time.Time
}

// IWorktreeManager owns the git/filesystem boundary for configured roots.
type IWorktreeManager interface {
	Repositories() []Repository
	Prepare(ctx context.Context, request WorktreeRequest) (PreparedWorktree, error)
	Reuse(ctx context.Context, repositoryID string, path string) (PreparedWorktree, error)
	Inspect(ctx context.Context, path string) (WorktreeSnapshot, error)
	Changes(ctx context.Context, path string) (WorktreeChanges, error)
	Release(ctx context.Context, worktree PreparedWorktree) error
}

// TerminalSpec is a process-owned shell request in one persisted worktree.
type TerminalSpec struct {
	WorktreeID uuid.UUID
	Directory  string
	Rows       int32
	Columns    int32
}

// TerminalSnapshot is the lifecycle state returned by the terminal manager.
type TerminalSnapshot struct {
	ID         uuid.UUID
	WorktreeID uuid.UUID
	Rows       int32
	Columns    int32
	Shell      string
	Status     string
	ExitCode   *int32
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// TerminalOutput is one bounded replay event.
type TerminalOutput struct {
	Seq             int64
	TerminalID      uuid.UUID
	Kind            string
	Data            string
	ExitCode        *int32
	OccurredAt      time.Time
	ReplayTruncated bool
}

// TerminalEventReplay is one atomic view of a terminal's current lifecycle
// state and every retained event after a cursor. The snapshot and events must
// come from the same read so a stream cannot miss the terminal event while
// deciding that an exited process is already caught up.
type TerminalEventReplay struct {
	// Changed closes when newer output or a lifecycle event is published. It is
	// captured with Events so a subscriber cannot miss a write before waiting.
	Changed  <-chan struct{}
	Snapshot TerminalSnapshot
	Events   []TerminalOutput
}

// ITerminalManager owns PTYs and their bounded in-memory replay.
type ITerminalManager interface {
	List(worktreeID uuid.UUID) []TerminalSnapshot
	Create(ctx context.Context, spec TerminalSpec) (TerminalSnapshot, error)
	Get(identifier uuid.UUID) (TerminalSnapshot, bool)
	Resize(identifier uuid.UUID, rows int32, columns int32) (TerminalSnapshot, error)
	Write(identifier uuid.UUID, data string) (TerminalSnapshot, error)
	Stop(identifier uuid.UUID) (TerminalSnapshot, error)
	EventsAfter(identifier uuid.UUID, afterSeq int64) (TerminalEventReplay, bool)
	Close() error
}

// ICopilotBridge is the seam over the Copilot CLI. Everything above it is
// testable with a generated mock; the concrete implementation needs a CLI on
// the host.
type ICopilotBridge interface {
	CreateSession(ctx context.Context, spec BridgeSessionSpec) (BridgeSession, error)
	ResumeSession(ctx context.Context, spec BridgeSessionSpec) (BridgeSession, error)
	ListModels(ctx context.Context) ([]BridgeModel, error)
}

// StoreTransaction is one atomic mutation against a transaction-bound SQLC
// query set. The callback must not retain queries after it returns.
type StoreTransaction func(queries storedb.Querier) error

// IStore is the persistence seam. Its method set is sqlc's own generated
// Querier (emit_interface in store/sqlc.yaml), plus the one transaction
// capability SQLC deliberately does not generate. The concrete store binds a
// callback to *sql.Tx so related domain facts and their event pointer commit
// together.
type IStore interface {
	storedb.Querier
	Transact(ctx context.Context, transaction StoreTransaction) error
}
