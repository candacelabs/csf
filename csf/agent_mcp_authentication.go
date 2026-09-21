package csf

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

const (
	// AgentMCPAuthorizationHeader carries the bearer key for an agent MCP request.
	AgentMCPAuthorizationHeader = "Authorization"
	// AgentMCPAgentIDHeader identifies the agent making an authenticated MCP request.
	AgentMCPAgentIDHeader = "X-CSF-Agent-ID"
	// AgentMCPSessionIDHeader identifies the agent session making an authenticated MCP request.
	AgentMCPSessionIDHeader = "X-CSF-Session-ID"

	agentMCPBearerPrefix = "Bearer "
)

var agentMCPAgentIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// AgentMCPAuthenticator verifies the one bearer key accepted by CSF's
// protected streamable MCP route.
type AgentMCPAuthenticator struct {
	key []byte
}

// NewAgentMCPAuthenticator creates an authenticator from one caller-owned
// bearer key.
func NewAgentMCPAuthenticator(key []byte) (*AgentMCPAuthenticator, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("agent MCP bearer key is required")
	}
	return &AgentMCPAuthenticator{key: append([]byte(nil), key...)}, nil
}

// AgentMCPHeaders returns the complete bearer and identity headers for one
// validated agent session. It is intended for the local in-process bridge.
func (authenticator *AgentMCPAuthenticator) AgentMCPHeaders(agentID string, sessionID string) (http.Header, error) {
	if authenticator == nil {
		return nil, fmt.Errorf("agent MCP authenticator is required")
	}
	if err := validateAgentMCPIdentity(agentID, sessionID); err != nil {
		return nil, err
	}
	headers := make(http.Header, 3)
	headers.Set(AgentMCPAuthorizationHeader, agentMCPBearerPrefix+string(authenticator.key))
	headers.Set(AgentMCPAgentIDHeader, agentID)
	headers.Set(AgentMCPSessionIDHeader, sessionID)
	return headers, nil
}

// AgentMCPHandler returns a streamable MCP HTTP handler that checks the bearer
// key and identity headers before exposing CSF's private request identity.
func (service *Service) AgentMCPHandler(authenticator *AgentMCPAuthenticator) http.Handler {
	handler := service.MCPHandler()
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authenticated, err := authenticator.authenticatedContext(request)
		if err != nil {
			http.Error(response, ErrUnauthorized.Error(), http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(response, request.WithContext(authenticated))
	})
}

func (authenticator *AgentMCPAuthenticator) authenticatedContext(request *http.Request) (context.Context, error) {
	if authenticator == nil || request == nil {
		return nil, ErrUnauthorized
	}
	authorization, ok := agentMCPHeader(request, AgentMCPAuthorizationHeader)
	if !ok || !strings.HasPrefix(authorization, agentMCPBearerPrefix) {
		return nil, ErrUnauthorized
	}
	providedKey := []byte(strings.TrimPrefix(authorization, agentMCPBearerPrefix))
	if subtle.ConstantTimeCompare(authenticator.key, providedKey) != 1 {
		return nil, ErrUnauthorized
	}
	agentID, ok := agentMCPHeader(request, AgentMCPAgentIDHeader)
	if !ok {
		return nil, ErrUnauthorized
	}
	sessionID, ok := agentMCPHeader(request, AgentMCPSessionIDHeader)
	if !ok {
		return nil, ErrUnauthorized
	}
	if err := validateAgentMCPIdentity(agentID, sessionID); err != nil {
		return nil, ErrUnauthorized
	}
	return withVerifiedAgentIdentity(request.Context(), agentID, sessionID), nil
}

func agentMCPHeader(request *http.Request, name string) (string, bool) {
	values := request.Header.Values(name)
	if len(values) != 1 || values[0] == "" {
		return "", false
	}
	return values[0], true
}

func validateAgentMCPIdentity(agentID string, sessionID string) error {
	if !agentMCPAgentIDPattern.MatchString(agentID) {
		return fmt.Errorf("agent ID must match %s", agentMCPAgentIDPattern)
	}
	identifier, err := uuid.Parse(sessionID)
	if err != nil || identifier == uuid.Nil || identifier.String() != sessionID {
		return fmt.Errorf("session ID must be a nonzero canonical UUID")
	}
	return nil
}
