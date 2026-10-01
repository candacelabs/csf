package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/config"
	"github.com/candacelabs/csf/services/candaceos/fleet"
	harnesssdk "github.com/candacelabs/csf/services/candaceos/harness"
)

type copilotHarness struct {
	config     *candaceosv1.CoreConfig
	controller *Controller
	runner     *harnesssdk.Runner[eventRecord]
}

var (
	errCopilotHarnessClosed      = errors.New("Copilot harness is closed")
	errCopilotHarnessStarted     = errors.New("Copilot harness is already started")
	errCopilotSessionUnavailable = errors.New("Copilot session is unavailable")
)

func newCopilotHarness(cfg *candaceosv1.CoreConfig, controller *Controller) *copilotHarness {
	harness := &copilotHarness{config: cfg, controller: controller}
	harness.runner = harnesssdk.NewRunner(
		harness.project,
		func(event eventRecord) string { return event.ID },
	)
	return harness
}

// project applies one normalized event to Controller. A panic in projection is
// contained and reported as a session error: the SDK recovers handler panics
// only to stdout, and an escaping panic would otherwise kill the lifecycle
// owner and hang every later turn.
func (h *copilotHarness) project(event eventRecord) {
	defer func() {
		if failure := recover(); failure != nil {
			h.reportProjectionFailure(event, failure)
		}
	}()
	h.controller.ingest(event)
}

// reportProjectionFailure surfaces a projection panic in the operator
// transcript. A second panic while reporting is dropped rather than escaping,
// because the report is the last resort.
func (h *copilotHarness) reportProjectionFailure(event eventRecord, failure any) {
	defer func() { _ = recover() }()
	h.controller.ingest(eventRecord{
		ID:        "copilot-projection-failure-" + event.ID,
		Type:      eventKindSessionError,
		Timestamp: time.Now().UTC(),
		Data: map[string]any{
			"message": fmt.Sprintf(
				"projecting Copilot %s event %q: %v", event.Type, event.ID, failure,
			),
		},
	})
}

func copilotRunnerError(err error) error {
	switch err {
	case harnesssdk.ErrRunnerClosed:
		return errCopilotHarnessClosed
	case harnesssdk.ErrRunnerStarted:
		return errCopilotHarnessStarted
	case harnesssdk.ErrRuntimeUnavailable:
		return errCopilotSessionUnavailable
	default:
		return err
	}
}

func closeCopilotSDK(disconnect func() error, stop func() error) error {
	disconnectErr := disconnect()
	return errors.Join(disconnectErr, stop())
}

// copilotExternalRuntime reports whether Core attaches to a separately managed
// Copilot runtime instead of spawning the CLI itself.
func copilotExternalRuntime(cfg *candaceosv1.CoreConfig) bool {
	return cfg.GetCopilotUrl() != ""
}

// copilotClientOptions builds the client for the configured transport. The
// GitHub credential is carried by whichever mechanism that transport accepts:
// a spawned CLI receives it as a client-level token, while an externally
// managed runtime rejects one and takes a per-session token instead. Supplying
// a client-level token alongside a URIConnection makes copilot.NewClient panic.
func copilotClientOptions(cfg *candaceosv1.CoreConfig) *copilot.ClientOptions {
	options := &copilot.ClientOptions{
		WorkingDirectory: cfg.GetWorkspace(),
		BaseDirectory:    config.CopilotHome(cfg),
		LogLevel:         "error",
		Mode:             copilot.ModeEmpty,
	}
	if copilotExternalRuntime(cfg) {
		options.Connection = copilot.URIConnection{
			URL:             cfg.GetCopilotUrl(),
			ConnectionToken: cfg.GetCopilotConnectionToken(),
		}
		return options
	}
	options.Connection = copilot.StdioConnection{Path: cfg.GetCopilotCli()}
	options.GitHubToken = cfg.GetGithubToken()
	return options
}

// copilotSessionGitHubToken returns the per-session credential for an
// externally managed runtime. A spawned CLI already authenticates with the
// client-level token, so it needs none.
func copilotSessionGitHubToken(cfg *candaceosv1.CoreConfig) string {
	if !copilotExternalRuntime(cfg) {
		return ""
	}
	return cfg.GetGithubToken()
}

func (h *copilotHarness) Start(ctx context.Context) (harnessStart, error) {
	if err := os.MkdirAll(config.CopilotHome(h.config), 0o700); err != nil {
		return harnessStart{}, fmt.Errorf("creating Copilot session directory: %w", err)
	}
	if h.runner == nil {
		return harnessStart{}, errors.New("Copilot harness state is unavailable")
	}
	if err := copilotRunnerError(h.runner.BeginStart()); err != nil {
		return harnessStart{}, fmt.Errorf("starting Copilot state owner: %w", err)
	}

	client := copilot.NewClient(copilotClientOptions(h.config))
	if err := client.Start(ctx); err != nil {
		return harnessStart{}, fmt.Errorf("starting Copilot runtime: %w", err)
	}

	lastID, err := client.GetLastSessionID(ctx)
	if err != nil {
		_ = client.Stop()
		return harnessStart{}, fmt.Errorf("reading last Copilot session: %w", err)
	}
	var session *copilot.Session
	if lastID != nil && *lastID != "" {
		session, err = client.ResumeSession(ctx, *lastID, h.resumeConfig())
	} else {
		session, err = client.CreateSession(ctx, h.sessionConfig())
	}
	if err != nil {
		_ = client.Stop()
		return harnessStart{}, fmt.Errorf("opening Copilot session: %w", err)
	}

	send := func(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error {
		mode, err := copilotDeliveryMode(prompt.GetDelivery())
		if err != nil {
			return err
		}
		_, sendErr := session.Send(ctx, copilot.MessageOptions{
			Prompt: prompt.GetContent(), Mode: mode, AgentMode: copilot.AgentModeAutopilot,
		})
		return sendErr
	}
	cleanup := func() error { return closeCopilotSDK(session.Disconnect, client.Stop) }
	if err := copilotRunnerError(h.runner.Install(send, session.Abort, cleanup)); err != nil {
		_ = cleanup()
		return harnessStart{}, fmt.Errorf("installing Copilot runtime: %w", err)
	}
	replay, err := copilotReplay(ctx, session)
	if err != nil {
		_ = h.Close()
		return harnessStart{}, fmt.Errorf("hydrating Copilot session: %w", err)
	}
	return harnessStart{
		SessionID: session.SessionID,
		Activate: func() error {
			h.runner.Activate(replay)
			return nil
		},
	}, nil
}

// copilotReplayLimit bounds startup hydration. A resumed session's persisted
// history has no upper bound, so replaying all of it would grow the transcript,
// and every later render of it, without limit.
const copilotReplayLimit = 200

// copilotReplay reads the newest persisted events for one session. The durable
// event log serves a bounded tail directly; a runtime that does not offer it
// falls back to the whole transcript, which is then trimmed to the same tail.
// Either way the result is chronological and excludes streaming deltas, which
// must never be replayed after the message they built.
func copilotReplay(ctx context.Context, session *copilot.Session) ([]eventRecord, error) {
	history, err := copilotEventLogTail(ctx, session)
	if err != nil {
		if history, err = session.GetEvents(ctx); err != nil {
			return nil, err
		}
	}
	replay := make([]eventRecord, 0, len(history))
	for _, event := range history {
		record := copilotEventRecord(event)
		if record.Ephemeral {
			continue
		}
		replay = append(replay, record)
	}
	if len(replay) > copilotReplayLimit {
		replay = replay[len(replay)-copilotReplayLimit:]
	}
	return replay, nil
}

// copilotEventLogTail returns the newest persisted events, oldest first. A
// backward read covers persisted history only, so it never resurrects an
// ephemeral event that its final counterpart already replaced.
func copilotEventLogTail(ctx context.Context, session *copilot.Session) ([]copilot.SessionEvent, error) {
	direction := rpc.EventsReadDirectionBackward
	limit := int64(copilotReplayLimit)
	result, err := session.RPC.EventLog.Read(ctx, &rpc.EventLogReadRequest{
		Direction: &direction,
		Max:       &limit,
	})
	if err != nil {
		return nil, err
	}
	return result.Events, nil
}

func (h *copilotHarness) Send(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error {
	if h.runner == nil {
		return errCopilotSessionUnavailable
	}
	return copilotRunnerError(h.runner.Send(ctx, prompt))
}

// copilotDeliveryMode names the runtime's send mode for one delivery. Immediate
// is true active-turn steering: the runtime injects the prompt into the
// in-flight run and reports it back as a "steering" user message, with no abort
// and no lost tool state. Enqueue defers the prompt to its own agentic loop,
// which the runtime drains before it reports the session idle, so enqueued
// guidance extends the run that admitted it.
func copilotDeliveryMode(delivery candaceosv1.HarnessDelivery) (string, error) {
	const (
		immediate = "immediate"
		enqueue   = "enqueue"
	)
	switch delivery {
	case candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE:
		return immediate, nil
	case candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE:
		return enqueue, nil
	default:
		return "", errors.New("unsupported Copilot prompt delivery")
	}
}

func (h *copilotHarness) Abort(ctx context.Context) error {
	if h.runner == nil {
		return errCopilotSessionUnavailable
	}
	return copilotRunnerError(h.runner.Abort(ctx))
}

func (h *copilotHarness) Close() error {
	if h.runner == nil {
		return nil
	}
	return h.runner.Close()
}

func (h *copilotHarness) sessionConfig() *copilot.SessionConfig {
	tools, available := h.tools()
	return &copilot.SessionConfig{
		ClientName:                     "candaceos-core",
		Model:                          h.config.CopilotModel,
		WorkingDirectory:               h.config.Workspace,
		GitHubToken:                    copilotSessionGitHubToken(h.config),
		Streaming:                      copilot.Bool(true),
		IncludeSubAgentStreamingEvents: copilot.Bool(true),
		EnableHostGitOperations:        copilot.Bool(true),
		EnableSessionStore:             copilot.Bool(true),
		EnableFileChangeTracking:       copilot.Bool(true),
		Tools:                          tools,
		AvailableTools:                 available,
		SystemMessage:                  &copilot.SystemMessageConfig{Content: copilotSystemInstructions},
		ManagedSettings:                managedSettings(),
		OnPermissionRequest:            h.handlePermission,
		OnEvent:                        h.ingestCopilotEvent,
	}
}

func (h *copilotHarness) resumeConfig() *copilot.ResumeSessionConfig {
	tools, available := h.tools()
	return &copilot.ResumeSessionConfig{
		ClientName:                     "candaceos-core",
		Model:                          h.config.CopilotModel,
		WorkingDirectory:               h.config.Workspace,
		GitHubToken:                    copilotSessionGitHubToken(h.config),
		Streaming:                      copilot.Bool(true),
		IncludeSubAgentStreamingEvents: copilot.Bool(true),
		EnableHostGitOperations:        copilot.Bool(true),
		EnableSessionStore:             copilot.Bool(true),
		EnableFileChangeTracking:       copilot.Bool(true),
		// Core restores durable run state explicitly at startup. Never let the
		// CLI continue an uncorrelated pre-crash tool call or permission request.
		ContinuePendingWork: copilot.Bool(false),
		Tools:               tools,
		AvailableTools:      available,
		SystemMessage:       &copilot.SystemMessageConfig{Content: copilotSystemInstructions},
		ManagedSettings:     managedSettings(),
		OnPermissionRequest: h.handlePermission,
		OnEvent:             h.ingestCopilotEvent,
	}
}

func (h *copilotHarness) tools() ([]copilot.Tool, []string) {
	fleetStatus := copilot.DefineTool("candace_fleet_status", "Read configured node roles and labels plus current Warden leader/quorum", func(_ struct{}, _ copilot.ToolInvocation) (fleet.ConfiguredSnapshot, error) {
		return h.controller.configuredFleetStatus()
	})
	fleetStatus.SkipPermission = true
	tools := []copilot.Tool{fleetStatus}
	available := copilot.NewToolSet().AddBuiltIn("*").AddCustom("candace_fleet_status")
	if h.controller.reconciler != nil {
		reconcile := copilot.DefineTool(
			"candace_reconcile_app",
			"Reconcile one immutable app revision through the fenced CandaceOS node agent; always requires operator approval",
			func(input reconcileToolInput, invocation copilot.ToolInvocation) (string, error) {
				ctx := invocation.TraceContext
				if ctx == nil {
					ctx = context.Background()
				}
				ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
				defer cancel()
				intent, err := reconcileIntent(input)
				if err != nil {
					return "", err
				}
				evidence, err := h.controller.reconcileApproved(ctx, intent, invocation.ToolCallID)
				if err != nil {
					return "", err
				}
				return copilotToolResult(evidence)
			},
		)
		// The zero value is intentional: the SDK routes every call through the
		// web approval callback before invoking the mutating handler.
		reconcile.SkipPermission = false
		tools = append(tools, reconcile)
		available.AddCustom("candace_reconcile_app")
	}
	return tools, available.ToSlice()
}

// copilotToolResult renders one protobuf tool result in canonical protobuf
// JSON. The SDK otherwise serializes the generated struct with encoding/json,
// which spells enums as integers and does not follow the protobuf JSON mapping,
// so the model would read a shape no other Candace surface produces.
func copilotToolResult(message proto.Message) (string, error) {
	encoded, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("encoding Copilot tool result: %w", err)
	}
	return string(encoded), nil
}

func managedSettings() *copilot.ManagedSettings {
	return &copilot.ManagedSettings{Permissions: &copilot.ManagedSettingsPermissions{
		DisableBypassPermissionsMode: copilot.DisableBypassPermissionsModeDisable,
		Deny: []string{
			"Shell(docker *)", "Shell(sudo *)", "Shell(iptables *)",
			"Shell(nft *)", "Shell(systemctl *)", "Read(**/.env)",
		},
		Ask: []string{"Shell(git push *)", "Shell(gh pr create *)"},
	}}
}

func (h *copilotHarness) handlePermission(request copilot.PermissionRequest, _ copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
	decision, err := h.controller.handlePermission(mapCopilotPermission(request))
	switch decision {
	case harnessPermissionApprove:
		return &rpc.PermissionDecisionApproveOnce{}, err
	case harnessPermissionReject:
		feedback := "The operator rejected this action. Continue with a safe alternative."
		return &rpc.PermissionDecisionReject{Feedback: &feedback}, err
	default:
		return &rpc.PermissionDecisionUserNotAvailable{}, err
	}
}

func (h *copilotHarness) safeToApprove(request copilot.PermissionRequest) bool {
	return h.controller.safeToApprove(mapCopilotPermission(request))
}

func mapCopilotPermission(request copilot.PermissionRequest) harnessPermission {
	mapped := harnessPermission{
		kind:                    string(request.Kind()),
		toolCallID:              toolCallID(request),
		title:                   permissionTitle(request),
		risk:                    permissionRisk(request),
		payload:                 permissionPayload(request),
		requiresManagedApproval: request.RequiresManagedApproval(),
	}
	switch value := request.(type) {
	case *copilot.PermissionRequestRead:
		mapped.requestSandboxBypass = truth(value.RequestSandboxBypass)
		mapped.path = value.Path
	case *copilot.PermissionRequestWrite:
		mapped.requestSandboxBypass = truth(value.RequestSandboxBypass)
		mapped.path = value.FileName
	case *copilot.PermissionRequestShell:
		mapped.requestSandboxBypass = truth(value.RequestSandboxBypass)
		mapped.hasWriteFileRedirection = value.HasWriteFileRedirection
		mapped.hasPossibleURLs = len(value.PossibleURLs) > 0
		mapped.possiblePaths = append([]string(nil), value.PossiblePaths...)
		mapped.commandSegments = make([]string, 0, len(value.CommandSegments))
		for _, segment := range value.CommandSegments {
			mapped.commandSegments = append(mapped.commandSegments, segment.FullCommandText)
		}
	case *copilot.PermissionRequestCustomTool:
		if value.ToolName == "candace_reconcile_app" {
			mapped.requiresFleetQuorum = true
			mapped.reconcileArgs = value.Args
		}
	}
	return mapped
}

func permissionPayload(request copilot.PermissionRequest) any {
	encoded, err := json.Marshal(request)
	if err != nil {
		return map[string]any{"kind": string(request.Kind())}
	}
	var payload any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return map[string]any{"kind": string(request.Kind())}
	}
	return payload
}

func permissionTitle(request copilot.PermissionRequest) string {
	switch value := request.(type) {
	case *copilot.PermissionRequestShell:
		return "Run: " + value.FullCommandText
	case *copilot.PermissionRequestWrite:
		return "Write " + value.FileName
	case *copilot.PermissionRequestRead:
		return "Read " + value.Path
	case *copilot.PermissionRequestURL:
		return "Open " + value.URL
	case *copilot.PermissionRequestCustomTool:
		return "Use " + value.ToolName
	default:
		return "Approve " + string(request.Kind())
	}
}

func permissionRisk(request copilot.PermissionRequest) string {
	switch request.(type) {
	case *copilot.PermissionRequestRead:
		return "low"
	case *copilot.PermissionRequestWrite, *copilot.PermissionRequestURL:
		return "medium"
	default:
		return "high"
	}
}

func toolCallID(request copilot.PermissionRequest) string {
	encoded, _ := json.Marshal(request)
	var value struct {
		ToolCallID *string `json:"toolCallId"`
	}
	_ = json.Unmarshal(encoded, &value)
	if value.ToolCallID == nil {
		return ""
	}
	return *value.ToolCallID
}

func truth(value *bool) bool { return value != nil && *value }

func (h *copilotHarness) ingestCopilotEvent(event copilot.SessionEvent) {
	if h.runner != nil {
		h.runner.Publish(copilotEventRecord(event))
	}
}

func copilotEventRecord(event copilot.SessionEvent) eventRecord {
	data := make(map[string]any)
	if event.Data != nil {
		encoded, err := json.Marshal(event.Data)
		if err == nil {
			_ = json.Unmarshal(encoded, &data)
		}
	}
	parentID := ""
	if event.ParentID != nil {
		parentID = *event.ParentID
	}
	return eventRecord{
		ID: event.ID, ParentID: parentID, Type: eventKind(event.Type()), Timestamp: event.Timestamp,
		Data: data, Ephemeral: event.Ephemeral != nil && *event.Ephemeral,
	}
}
