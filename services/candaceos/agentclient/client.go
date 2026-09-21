// Package agentclient is CandaceOS Core's transport to one node agent.
//
// The package owns a single hop: turning a prepared assignment into an
// authenticated HTTP call against one node and decoding the typed reply.
// Callers may rely on Reconcile proving the endpoint's node identity against
// the Warden-selected node before it mutates anything, on a reply being
// rejected unless it echoes the requested assignment and fence and carries a
// valid completion timestamp, and on every response body being size-bounded so
// a misbehaving node cannot exhaust Core. Placement, persistence, retry, and
// approval policy all stay with the reconciler that calls this package.
package agentclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const maxResponseBytes = 1 << 20

// Client routes an assignment either to one explicit prototype agent or to
// the standard agent port on the Warden-advertised node address.
type Client struct {
	overrideURL string
	token       string
	agentPort   string
	httpClient  *http.Client
}

// NewNodeAgentClient validates an optional fixed endpoint. An empty endpoint
// enables per-node address derivation for a real fleet.
func NewNodeAgentClient(overrideURL, token string, agentPort int, httpClient *http.Client) (*Client, error) {
	overrideURL = strings.TrimRight(strings.TrimSpace(overrideURL), "/")
	if overrideURL != "" {
		parsed, err := url.Parse(overrideURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("invalid CandaceOS agent URL %q", overrideURL)
		}
	}
	if agentPort < 1 || agentPort > 65535 {
		return nil, fmt.Errorf("invalid CandaceOS agent port %d", agentPort)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Minute}
	}
	return &Client{
		overrideURL: overrideURL,
		token:       strings.TrimSpace(token),
		agentPort:   fmt.Sprint(agentPort),
		httpClient:  httpClient,
	}, nil
}

// Reconcile verifies the endpoint's node identity before sending a mutation.
func (c *Client) Reconcile(
	ctx context.Context,
	nodeID string,
	wardenAddress string,
	request *candaceosv1.ReconcileRequest,
) (*candaceosv1.ReconcileResponse, error) {
	baseURL, err := c.baseURL(wardenAddress)
	if err != nil {
		return nil, err
	}
	health, err := c.health(ctx, baseURL)
	if err != nil {
		return nil, err
	}
	if health.GetNodeId() != nodeID {
		return nil, fmt.Errorf("agent identity mismatch: Warden selected %q but endpoint reports %q", nodeID, health.GetNodeId())
	}

	payload, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encoding agent reconciliation: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPut, baseURL+"/v1/assignment", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("building agent reconciliation request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	c.authorize(httpRequest)
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("calling agent %q: %w", nodeID, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, responseError("agent reconciliation", response)
	}
	body, err := readBounded(response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading agent reconciliation: %w", err)
	}
	result := new(candaceosv1.ReconcileResponse)
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, result); err != nil {
		return nil, fmt.Errorf("decoding agent reconciliation: %w", err)
	}
	if result.GetFence() == nil || result.GetAssignment() == nil || !proto.Equal(result.GetFence(), request.GetFence()) || !proto.Equal(result.GetAssignment(), request.GetAssignment()) {
		return nil, fmt.Errorf("agent %q returned a response for a different assignment or fence", nodeID)
	}
	if result.GetUpdatedAt() == nil || result.GetUpdatedAt().CheckValid() != nil {
		return nil, fmt.Errorf("agent %q returned no valid completion timestamp", nodeID)
	}
	return result, nil
}

func (c *Client) health(ctx context.Context, baseURL string) (*candaceosv1.HealthResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
	if err != nil {
		return nil, fmt.Errorf("building agent health request: %w", err)
	}
	c.authorize(request)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("calling agent health: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, responseError("agent health", response)
	}
	body, err := readBounded(response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading agent health: %w", err)
	}
	health := new(candaceosv1.HealthResponse)
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, health); err != nil {
		return nil, fmt.Errorf("decoding agent health: %w", err)
	}
	if err := candaceosv1.ValidateHealthResponse(health); err != nil {
		return nil, fmt.Errorf("validating agent health: %w", err)
	}
	return health, nil
}

func (c *Client) baseURL(wardenAddress string) (string, error) {
	if c.overrideURL != "" {
		return c.overrideURL, nil
	}
	host, _, err := net.SplitHostPort(wardenAddress)
	if err != nil {
		return "", fmt.Errorf("deriving agent endpoint from Warden address %q: %w", wardenAddress, err)
	}
	return "http://" + net.JoinHostPort(host, c.agentPort), nil
}

func (c *Client) authorize(request *http.Request) {
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
}

func responseError(operation string, response *http.Response) error {
	body, err := readBounded(response.Body)
	if err != nil {
		return fmt.Errorf("%s returned %s", operation, response.Status)
	}
	message := strings.TrimSpace(string(body))
	if message == "" {
		return fmt.Errorf("%s returned %s", operation, response.Status)
	}
	return fmt.Errorf("%s returned %s: %s", operation, response.Status, message)
}

func readBounded(reader io.Reader) ([]byte, error) {
	limited := io.LimitReader(reader, maxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	return body, nil
}
