// Copyright 2026 Candace Labs

package relayhook

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/candacelabs/csf/csf"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// ISender is the interface for sending messages to the orchestrator and performing operations.
type ISender interface {
	// Send posts a message to the target assignment and returns the answer
	// from the target's last result, if any.
	Send(orchestratorID string, message string) (string, error)

	// Get returns the orchestrator session's last result without sending a new message.
	Get(orchestratorID string) (string, error)

	// Cancel requests that the target assignment stop at its next safepoint.
	Cancel(orchestratorID string) error
}

// ClientSender sends messages to the orchestrator via the harness API.
type ClientSender struct {
	endpoint string
	client   *csf.Client
	timeout  time.Duration
}

// NewClientSender creates a Sender that uses the harness API.
func NewClientSender(endpoint string, timeout time.Duration) (*ClientSender, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	httpClient := &http.Client{Timeout: timeout}
	client, err := csf.NewClient(endpoint, httpClient)
	if err != nil {
		return nil, fmt.Errorf("create harness client: %w", err)
	}
	return &ClientSender{
		endpoint: endpoint,
		client:   client,
		timeout:  timeout,
	}, nil
}

// Send posts a message to the orchestrator and returns its last answer.
func (cs *ClientSender) Send(orchestratorID string, message string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cs.timeout)
	defer cancel()

	// Send the message to the orchestrator.
	_, err := cs.client.SendAgentSessionMessage(ctx, &harnessv1.SendAgentSessionMessageRequest{
		AssignmentId: orchestratorID,
		Message:      message,
	})
	if err != nil {
		return "", fmt.Errorf("send message: %w", err)
	}

	// The harness API acknowledges the queued message with a turn ID but does
	// not return the orchestrator's answer text, so there is none to surface.
	return "", nil
}

// Get returns the orchestrator session's last result without sending a new message.
func (cs *ClientSender) Get(orchestratorID string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cs.timeout)
	defer cancel()

	_, err := cs.client.GetAgentSession(ctx, &harnessv1.GetAgentSessionRequest{
		AssignmentId: orchestratorID,
	})
	if err != nil {
		return "", fmt.Errorf("get orchestrator session: %w", err)
	}

	// The session state carries phase and turn counts, not the text of the
	// orchestrator's last answer, so there is no answer text to return.
	return "", nil
}

// Cancel requests that the orchestrator session stop at its next safepoint.
func (cs *ClientSender) Cancel(orchestratorID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cs.timeout)
	defer cancel()

	_, err := cs.client.CancelAgentSession(ctx, &harnessv1.CancelAgentSessionRequest{
		AssignmentId: orchestratorID,
	})
	if err != nil {
		return fmt.Errorf("cancel orchestrator session: %w", err)
	}

	return nil
}
