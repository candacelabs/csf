// Copyright 2026 Candace Labs

package csf

import (
	"context"
	"errors"

	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// ErrAgentSessionsUnavailable reports a harness operation on a host that
// mounted no agent harness.
var ErrAgentSessionsUnavailable = errors.New("agent harness is not mounted in this host")

// IAgentSessions is the agent harness capability behind the generated harness
// operations: the service in candace/services/harness that runs agent
// sessions in this process. The transport adapter delegates to it and owns no
// session of its own.
//
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=agent_sessions.go -destination=internal/mocks/agent_sessions.gen.go -package=csfmocks
type IAgentSessions interface {
	Submit(ctx context.Context, request *harnessv1.SubmitAgentSessionRequest) (*harnessv1.SubmitAgentSessionResponse, error)
	Send(ctx context.Context, request *harnessv1.SendAgentSessionMessageRequest) (*harnessv1.SendAgentSessionMessageResponse, error)
	List(ctx context.Context, request *harnessv1.ListAgentSessionsRequest) (*harnessv1.ListAgentSessionsResponse, error)
	Get(ctx context.Context, request *harnessv1.GetAgentSessionRequest) (*harnessv1.GetAgentSessionResponse, error)
	Cancel(ctx context.Context, request *harnessv1.CancelAgentSessionRequest) (*harnessv1.CancelAgentSessionResponse, error)
	Stop(ctx context.Context, request *harnessv1.StopHarnessRequest) (*harnessv1.StopHarnessResponse, error)
}

// WithAgentSessions mounts the agent harness behind the generated harness
// operations.
func WithAgentSessions(sessions IAgentSessions) Option {
	return func(service *Service) { service.agentSessions = sessions }
}

func (service *Service) agentSessionsOrUnavailable(ctx context.Context) (IAgentSessions, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if service.agentSessions == nil {
		return nil, ErrAgentSessionsUnavailable
	}
	return service.agentSessions, nil
}

// SubmitAgentSession admits one assignment recipe as a session of the mounted
// harness and returns its receipt.
func (service *Service) SubmitAgentSession(ctx context.Context, request *harnessv1.SubmitAgentSessionRequest) (*harnessv1.SubmitAgentSessionResponse, error) {
	sessions, err := service.agentSessionsOrUnavailable(ctx)
	if err != nil {
		return nil, err
	}
	return sessions.Submit(ctx, request)
}

// SendAgentSessionMessage queues one message for an open session's next turn.
func (service *Service) SendAgentSessionMessage(ctx context.Context, request *harnessv1.SendAgentSessionMessageRequest) (*harnessv1.SendAgentSessionMessageResponse, error) {
	sessions, err := service.agentSessionsOrUnavailable(ctx)
	if err != nil {
		return nil, err
	}
	if err := harnessv1.ValidateSendAgentSessionMessageRequest(request); err != nil {
		return nil, errors.Join(ErrInvalidRequest, err)
	}
	return sessions.Send(ctx, request)
}

// ListAgentSessions reports every session the harness holds.
func (service *Service) ListAgentSessions(ctx context.Context, request *harnessv1.ListAgentSessionsRequest) (*harnessv1.ListAgentSessionsResponse, error) {
	sessions, err := service.agentSessionsOrUnavailable(ctx)
	if err != nil {
		return nil, err
	}
	return sessions.List(ctx, request)
}

// GetAgentSession reports one session's state.
func (service *Service) GetAgentSession(ctx context.Context, request *harnessv1.GetAgentSessionRequest) (*harnessv1.GetAgentSessionResponse, error) {
	sessions, err := service.agentSessionsOrUnavailable(ctx)
	if err != nil {
		return nil, err
	}
	if err := harnessv1.ValidateGetAgentSessionRequest(request); err != nil {
		return nil, errors.Join(ErrInvalidRequest, err)
	}
	return sessions.Get(ctx, request)
}

// CancelAgentSession asks a session's owner to stop at its next safepoint.
func (service *Service) CancelAgentSession(ctx context.Context, request *harnessv1.CancelAgentSessionRequest) (*harnessv1.CancelAgentSessionResponse, error) {
	sessions, err := service.agentSessionsOrUnavailable(ctx)
	if err != nil {
		return nil, err
	}
	if err := harnessv1.ValidateCancelAgentSessionRequest(request); err != nil {
		return nil, errors.Join(ErrInvalidRequest, err)
	}
	return sessions.Cancel(ctx, request)
}

// StopHarness asks the harness process to shut down in order.
func (service *Service) StopHarness(ctx context.Context, request *harnessv1.StopHarnessRequest) (*harnessv1.StopHarnessResponse, error) {
	sessions, err := service.agentSessionsOrUnavailable(ctx)
	if err != nil {
		return nil, err
	}
	return sessions.Stop(ctx, request)
}
