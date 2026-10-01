package operator

import (
	"context"
	"errors"
	"fmt"
	"math"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/config"
	"github.com/candacelabs/csf/services/candaceos/fleet"
	harnesssdk "github.com/candacelabs/csf/services/candaceos/harness"
)

const maxHarnessStructuredDetailBytes = 256 * 1024

// harnessAdapter adapts a public Runtime to Controller's private lifecycle.
// The adapter, rather than the provider, owns run correlation and the internal
// UI event representation.
type harnessAdapter struct {
	runtime     harnesssdk.IRuntime
	controller  *Controller
	description string
}

func newHarnessAdapter(
	cfg *candaceosv1.CoreConfig,
	controller *Controller,
	factory harnesssdk.IFactory,
	expectedBackend candaceosv1.HarnessBackend,
) (*harnessAdapter, *candaceosv1.HarnessRuntimeIdentity, error) {
	description := config.HarnessBackendName(expectedBackend)
	if factory == nil {
		return nil, nil, fmt.Errorf("%s harness factory is required", description)
	}
	harnessContext := &candaceosv1.HarnessContext{Workspace: cfg.GetWorkspace()}
	if err := candaceosv1.ValidateHarnessContext(harnessContext); err != nil {
		return nil, nil, fmt.Errorf("%s harness context: %w", description, err)
	}
	instance, err := factory.New(
		harnessContext,
		&harnessHost{controller: controller},
	)
	if err != nil {
		rejection := fmt.Errorf("constructing %s harness: %w", description, err)
		return nil, nil, closeRejectedHarnessInstance(instance, description, rejection)
	}
	if instance == nil || instance.Runtime == nil {
		return nil, nil, fmt.Errorf("%s harness factory returned no runtime", description)
	}
	if instance.Identity == nil {
		rejection := fmt.Errorf("%s harness factory returned no identity", description)
		return nil, nil, closeRejectedHarnessInstance(instance, description, rejection)
	}
	identity := proto.Clone(instance.Identity).(*candaceosv1.HarnessRuntimeIdentity)
	if err := candaceosv1.ValidateHarnessRuntimeIdentity(identity); err != nil {
		rejection := fmt.Errorf("%s harness identity: %w", description, err)
		return nil, nil, closeRejectedHarnessInstance(instance, description, rejection)
	}
	if identity.GetBackend() != expectedBackend {
		rejection := fmt.Errorf("%s harness identity must use %s", description, expectedBackend)
		return nil, nil, closeRejectedHarnessInstance(instance, description, rejection)
	}
	return &harnessAdapter{
		runtime: instance.Runtime, controller: controller, description: description,
	}, identity, nil
}

func closeRejectedHarnessInstance(instance *harnesssdk.Instance, description string, rejection error) error {
	if instance == nil || instance.Runtime == nil {
		return rejection
	}
	if err := instance.Runtime.Close(); err != nil {
		return errors.Join(rejection, fmt.Errorf("closing rejected %s harness: %w", description, err))
	}
	return rejection
}

func (h *harnessAdapter) Start(ctx context.Context) (harnessStart, error) {
	session, err := h.runtime.Start(ctx)
	if err != nil {
		return harnessStart{}, err
	}
	var ownedSession *candaceosv1.HarnessSession
	if session != nil {
		ownedSession = proto.Clone(session).(*candaceosv1.HarnessSession)
	}
	if err := candaceosv1.ValidateHarnessSession(ownedSession); err != nil {
		return harnessStart{}, fmt.Errorf("%s harness session: %w", h.description, err)
	}
	return harnessStart{
		SessionID: ownedSession.GetId(),
		Activate:  func() error { return h.runtime.Activate(ctx) },
	}, nil
}

func (h *harnessAdapter) Send(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error {
	if err := candaceosv1.ValidateHarnessPrompt(prompt); err != nil {
		return fmt.Errorf("%s harness prompt: %w", h.description, err)
	}
	return h.runtime.Send(ctx, proto.Clone(prompt).(*candaceosv1.HarnessPrompt))
}

func (h *harnessAdapter) Abort(ctx context.Context) error { return h.runtime.Abort(ctx) }

func (h *harnessAdapter) Close() error { return h.runtime.Close() }

// harnessHost is the only authority a public harness receives. Every
// method preserves the same Controller policy used by the built-in adapters.
type harnessHost struct {
	controller *Controller
}

func (h *harnessHost) Publish(ctx context.Context, event *candaceosv1.HarnessEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var ownedEvent *candaceosv1.HarnessEvent
	if event != nil {
		ownedEvent = proto.Clone(event).(*candaceosv1.HarnessEvent)
	}
	if err := validateHarnessEvent(ownedEvent); err != nil {
		return fmt.Errorf("publishing harness event: %w", err)
	}
	if ownedEvent.GetAt() == nil {
		return errors.New("publishing harness event: at is required")
	}
	if err := ownedEvent.GetAt().CheckValid(); err != nil {
		return fmt.Errorf("publishing harness event timestamp: %w", err)
	}
	if ownedEvent.GetSessionStarted() != nil {
		if ownedEvent.GetRunId() != "" {
			return errors.New("publishing session-started event: run_id must be empty")
		}
	}
	return h.controller.publishHarnessEvent(ownedEvent)
}

// harnessEventRecord is the single projection from the generated public event
// contract into Controller's existing private event store and timeline.
func harnessEventRecord(event *candaceosv1.HarnessEvent) eventRecord {
	record := eventRecord{
		ID: event.GetId(), Timestamp: event.GetAt().AsTime(), Data: make(map[string]any),
	}
	switch payload := event.GetPayload().(type) {
	case *candaceosv1.HarnessEvent_SessionStarted:
		record.Type = eventKindSessionStart
		record.Data["message"] = payload.SessionStarted.GetMessage()
	case *candaceosv1.HarnessEvent_UserMessage:
		record.Type = eventKindUserMessage
		record.Data["content"] = payload.UserMessage.GetContent()
	case *candaceosv1.HarnessEvent_AssistantDelta:
		record.Type = eventKindAssistantDelta
		record.Ephemeral = true
		record.Data["messageId"] = payload.AssistantDelta.GetMessageId()
		record.Data["deltaContent"] = payload.AssistantDelta.GetContent()
	case *candaceosv1.HarnessEvent_AssistantMessage:
		record.Type = eventKindAssistantMessage
		record.Data["messageId"] = payload.AssistantMessage.GetMessageId()
		record.Data["content"] = payload.AssistantMessage.GetContent()
	case *candaceosv1.HarnessEvent_ToolStarted:
		record.Type = eventKindToolExecutionStart
		record.Data["toolCallId"] = payload.ToolStarted.GetToolCallId()
		record.Data["toolName"] = payload.ToolStarted.GetToolName()
		record.Data["arguments"] = harnessValue(payload.ToolStarted.GetArguments())
	case *candaceosv1.HarnessEvent_ToolCompleted:
		record.Type = eventKindToolExecutionComplete
		record.Data["toolCallId"] = payload.ToolCompleted.GetToolCallId()
		record.Data["toolName"] = payload.ToolCompleted.GetToolName()
		if succeeded := payload.ToolCompleted.GetSucceeded(); succeeded != nil {
			record.Data["result"] = harnessValue(succeeded.GetResult())
		} else {
			record.Data["error"] = payload.ToolCompleted.GetFailed().GetMessage()
		}
	case *candaceosv1.HarnessEvent_Idle:
		record.Type = eventKindSessionIdle
	case *candaceosv1.HarnessEvent_Error:
		record.Type = eventKindSessionError
		record.Data["message"] = payload.Error.GetMessage()
	}
	return record
}

func harnessValue(value *structpb.Value) any {
	if value == nil {
		return nil
	}
	return value.AsInterface()
}

func (h *harnessHost) FleetStatus(ctx context.Context) (*candaceosv1.HarnessFleetStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot, err := h.controller.configuredFleetStatus()
	if err != nil {
		return nil, err
	}
	return harnessFleetStatus(snapshot), nil
}

func (h *harnessHost) Reconcile(
	ctx context.Context,
	request *candaceosv1.HarnessReconcileRequest,
) (*candaceosv1.ReconcileEvidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var ownedRequest *candaceosv1.HarnessReconcileRequest
	if request != nil {
		ownedRequest = proto.Clone(request).(*candaceosv1.HarnessReconcileRequest)
	}
	if err := candaceosv1.ValidateHarnessReconcileRequest(ownedRequest); err != nil {
		return nil, fmt.Errorf("reconciling from embedded harness: %w", err)
	}
	if err := candaceosv1.ValidateReconcileIntent(ownedRequest.GetIntent()); err != nil {
		return nil, fmt.Errorf("reconciling from embedded harness intent: %w", err)
	}
	input := proto.Clone(ownedRequest.GetIntent()).(*candaceosv1.ReconcileIntent)
	decision, err := h.controller.handlePermissionContext(ctx, harnessPermission{
		kind: "custom_tool", toolCallID: ownedRequest.GetToolCallId(),
		title: "Use candace_reconcile_app", risk: "high",
		payload: ownedRequest, requiresFleetQuorum: true, reconcileArgs: input,
	})
	if err != nil {
		return nil, err
	}
	switch decision {
	case harnessPermissionApprove:
		result, err := h.controller.reconcileApproved(ctx, input, ownedRequest.GetToolCallId())
		if err != nil {
			return nil, err
		}
		return result, nil
	case harnessPermissionReject:
		return nil, errors.New("the operator rejected this reconcile action")
	default:
		return nil, errors.New("the operator is unavailable to approve this reconcile action")
	}
}

func validateHarnessEvent(event *candaceosv1.HarnessEvent) error {
	if err := candaceosv1.ValidateHarnessEvent(event); err != nil {
		return err
	}
	switch payload := event.GetPayload().(type) {
	case *candaceosv1.HarnessEvent_SessionStarted:
		return candaceosv1.ValidateHarnessSessionStarted(payload.SessionStarted)
	case *candaceosv1.HarnessEvent_UserMessage:
		return candaceosv1.ValidateHarnessUserMessage(payload.UserMessage)
	case *candaceosv1.HarnessEvent_AssistantDelta:
		return candaceosv1.ValidateHarnessAssistantDelta(payload.AssistantDelta)
	case *candaceosv1.HarnessEvent_AssistantMessage:
		return candaceosv1.ValidateHarnessAssistantMessage(payload.AssistantMessage)
	case *candaceosv1.HarnessEvent_ToolStarted:
		if err := candaceosv1.ValidateHarnessToolStarted(payload.ToolStarted); err != nil {
			return err
		}
		return validateStructuredHarnessDetail(payload.ToolStarted.GetArguments())
	case *candaceosv1.HarnessEvent_ToolCompleted:
		if err := candaceosv1.ValidateHarnessToolCompleted(payload.ToolCompleted); err != nil {
			return err
		}
		switch outcome := payload.ToolCompleted.GetOutcome().(type) {
		case *candaceosv1.HarnessToolCompleted_Succeeded:
			if outcome.Succeeded == nil || outcome.Succeeded.GetResult() == nil {
				return errors.New("successful tool result is required")
			}
			return validateStructuredHarnessDetail(outcome.Succeeded.GetResult())
		case *candaceosv1.HarnessToolCompleted_Failed:
			return candaceosv1.ValidateHarnessToolFailed(outcome.Failed)
		default:
			return errors.New("tool outcome is required")
		}
	case *candaceosv1.HarnessEvent_Idle:
		if payload.Idle == nil {
			return errors.New("idle payload is required")
		}
		return nil
	case *candaceosv1.HarnessEvent_Error:
		return candaceosv1.ValidateHarnessError(payload.Error)
	default:
		return errors.New("payload is required")
	}
}

func validateStructuredHarnessDetail(value *structpb.Value) error {
	if value == nil {
		return nil
	}
	if err := validateFiniteHarnessDetail(value); err != nil {
		return err
	}
	encoded, err := protojson.Marshal(value)
	if err != nil {
		return fmt.Errorf("structured detail: %w", err)
	}
	if len(encoded) > maxHarnessStructuredDetailBytes {
		return fmt.Errorf("structured detail exceeds %d bytes", maxHarnessStructuredDetailBytes)
	}
	return nil
}

func validateFiniteHarnessDetail(value *structpb.Value) error {
	switch kind := value.GetKind().(type) {
	case *structpb.Value_NullValue, *structpb.Value_StringValue, *structpb.Value_BoolValue:
		return nil
	case *structpb.Value_NumberValue:
		if math.IsNaN(kind.NumberValue) || math.IsInf(kind.NumberValue, 0) {
			return errors.New("structured detail contains a non-finite number")
		}
		return nil
	case *structpb.Value_StructValue:
		if kind.StructValue == nil {
			return errors.New("structured detail contains a nil object")
		}
		for _, field := range kind.StructValue.GetFields() {
			if field == nil {
				return errors.New("structured detail contains a nil field")
			}
			if err := validateFiniteHarnessDetail(field); err != nil {
				return err
			}
		}
		return nil
	case *structpb.Value_ListValue:
		if kind.ListValue == nil {
			return errors.New("structured detail contains a nil list")
		}
		for _, element := range kind.ListValue.GetValues() {
			if element == nil {
				return errors.New("structured detail contains a nil element")
			}
			if err := validateFiniteHarnessDetail(element); err != nil {
				return err
			}
		}
		return nil
	default:
		return errors.New("structured detail kind is required")
	}
}

func harnessFleetStatus(snapshot fleet.ConfiguredSnapshot) *candaceosv1.HarnessFleetStatus {
	nodes := make([]*candaceosv1.HarnessFleetNode, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodes = append(nodes, &candaceosv1.HarnessFleetNode{
			Id: node.ID, Name: node.Name, Role: node.Role,
			Labels: cloneStringMap(node.Labels), Status: node.Status, Address: node.Address,
			LastSeen: timestamppb.New(node.LastSeen),
		})
	}
	return &candaceosv1.HarnessFleetStatus{
		Self: snapshot.Self, LeaderId: snapshot.LeaderID, Term: snapshot.Term,
		Authoritative: snapshot.Authoritative, HasQuorum: snapshot.HasQuorum,
		Online: uint32(snapshot.Online), Required: uint32(snapshot.Required), Nodes: nodes,
		UpdatedAt: timestamppb.New(snapshot.UpdatedAt), Error: snapshot.Error,
	}
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

var _ iHarnessImplementation = (*harnessAdapter)(nil)
var _ harnesssdk.IHost = (*harnessHost)(nil)
