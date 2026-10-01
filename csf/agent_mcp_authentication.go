package csf

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

const (
	// AgentMCPAuthorizationHeader carries a session-bound credential for an agent MCP request.
	AgentMCPAuthorizationHeader = "Authorization"
	// AgentMCPAgentIDHeader identifies the agent making an authenticated MCP request.
	AgentMCPAgentIDHeader = "X-CSF-Agent-ID"
	// AgentMCPSessionIDHeader identifies the agent session making an authenticated MCP request.
	AgentMCPSessionIDHeader = "X-CSF-Session-ID"

	agentMCPBearerPrefix     = "Bearer "
	agentMCPCredentialDomain = "csf.agent-mcp.v1"
	agentMCPCredentialPrefix = "v1."
)

var agentMCPAgentIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// AgentMCPAuthenticator signs and verifies identity-bound session credentials
// for CSF's protected streamable MCP route. Its signing key stays with the host.
type AgentMCPAuthenticator struct {
	key []byte
}

// NewAgentMCPAuthenticator creates an authenticator from one caller-owned
// signing key. Credentials remain valid for their identity tuple until key rotation.
func NewAgentMCPAuthenticator(key []byte) (*AgentMCPAuthenticator, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("agent MCP signing key is required")
	}
	return &AgentMCPAuthenticator{key: append([]byte(nil), key...)}, nil
}

// AgentMCPHeaders returns the complete bearer and identity headers for one
// validated agent session without disclosing the signing key. It is intended
// for the trusted local in-process bridge, not for untrusted callers to mint identities.
func (authenticator *AgentMCPAuthenticator) AgentMCPHeaders(agentID string, sessionID string) (http.Header, error) {
	if authenticator == nil {
		return nil, fmt.Errorf("agent MCP authenticator is required")
	}
	if err := validateAgentMCPIdentity(agentID, sessionID); err != nil {
		return nil, err
	}
	headers := make(http.Header, 3)
	headers.Set(AgentMCPAuthorizationHeader, agentMCPBearerPrefix+authenticator.sessionCredential(agentID, sessionID))
	headers.Set(AgentMCPAgentIDHeader, agentID)
	headers.Set(AgentMCPSessionIDHeader, sessionID)
	return headers, nil
}

// AgentMCPHandler returns a streamable MCP HTTP handler that checks the bearer
// credential against both identity headers before exposing CSF's private request identity.
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
	providedCredential := strings.TrimPrefix(authorization, agentMCPBearerPrefix)
	expectedCredential := authenticator.sessionCredential(agentID, sessionID)
	if !hmac.Equal([]byte(expectedCredential), []byte(providedCredential)) {
		return nil, ErrUnauthorized
	}
	return withVerifiedAgentIdentity(request.Context(), agentID, sessionID), nil
}

func (authenticator *AgentMCPAuthenticator) sessionCredential(agentID string, sessionID string) string {
	mac := hmac.New(sha256.New, authenticator.key)
	// Validated identities cannot contain NUL, so the domain and tuple are unambiguous.
	_, _ = mac.Write([]byte(agentMCPCredentialDomain + "\x00" + agentID + "\x00" + sessionID))
	return agentMCPCredentialPrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
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
