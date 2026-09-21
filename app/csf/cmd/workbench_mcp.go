package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/candacelabs/csf/csf"
	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	"github.com/candacelabs/csf/services/copilot-adapter/copilotbridge"
	copilotv1 "github.com/candacelabs/csf/services/copilot-adapter/proto/candace/copilot/v1"
)

const (
	workbenchCSFServer     = "csf"
	workbenchTraceServer   = "brain-traces"
	workbenchMCPPath       = "/mcp"
	workbenchAgentMCPPath  = "/mcp/agent"
	workbenchTraceMCPPath  = "/api/public/mcp"
	workbenchAllTools      = "*"
	workbenchAuthorization = "Authorization"
	workbenchBasicAuth     = "Basic "
	workbenchSchemeHTTP    = "http"
	workbenchSchemeHTTPS   = "https"
)

// The same project credentials configure native Langfuse access and trace export.
// The upstream Copilot SDK owns MCP transport and permission requests.
func workbenchMCPServers(origin string, config *copilotv1.TraceExportConfig) (map[string]copilot.MCPServerConfig, error) {
	servers := map[string]copilot.MCPServerConfig{
		workbenchCSFServer: copilot.MCPHTTPServerConfig{
			URL: strings.TrimRight(origin, "/") + workbenchMCPPath, Tools: []string{workbenchAllTools},
		},
	}
	if config == nil {
		return servers, nil
	}
	if err := copilotv1.ValidateTraceExportConfig(config); err != nil {
		return nil, fmt.Errorf("invalid Workbench trace configuration")
	}
	endpoint, err := url.Parse(config.GetEndpointUrl())
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != workbenchSchemeHTTP && endpoint.Scheme != workbenchSchemeHTTPS) || endpoint.User != nil {
		return nil, fmt.Errorf("Workbench trace endpoint must be an HTTP URL without embedded credentials")
	}
	endpoint.Path, endpoint.RawPath, endpoint.RawQuery, endpoint.Fragment, endpoint.RawFragment = workbenchTraceMCPPath, "", "", "", ""
	endpoint.ForceQuery = false
	servers[workbenchTraceServer] = copilot.MCPHTTPServerConfig{
		URL: endpoint.String(), Tools: []string{workbenchAllTools},
		Headers: map[string]string{
			workbenchAuthorization: workbenchBasicAuth + base64.StdEncoding.EncodeToString([]byte(config.GetPublicKey()+":"+config.GetSecretKey())),
		},
	}
	return servers, nil
}

// workbenchMCPServerResolver supplies the callback the generic bridge invokes
// for each create or resume operation. It must be a separate function because
// the static Workbench settings (origin and trace configuration) are known when
// the CSF application starts, while the durable session identity is known only
// when the bridge opens that particular session.
//
// Human-operated sessions keep the ordinary /mcp catalog. Agent-owned sessions
// replace that catalog entry with /mcp/agent and the headers bound to the
// stored agent and session IDs. The bridge remains transport-generic: this
// application owns the choice of CSF endpoint and its bearer-key policy.
func workbenchMCPServerResolver(origin string, config *copilotv1.TraceExportConfig, authenticator *csf.AgentMCPAuthenticator) copilotbridge.MCPServerResolver {
	return func(_ context.Context, spec copilotadapter.BridgeSessionSpec) (map[string]copilot.MCPServerConfig, error) {
		servers, err := workbenchMCPServers(origin, config)
		if err != nil {
			return nil, err
		}
		if spec.AgentID == "" {
			return servers, nil
		}
		if authenticator == nil {
			return nil, fmt.Errorf("agent MCP authentication unavailable")
		}
		headers, err := authenticator.AgentMCPHeaders(spec.AgentID, spec.SessionID.String())
		if err != nil {
			return nil, fmt.Errorf("agent MCP headers: %w", err)
		}
		requestHeaders := map[string]string{
			csf.AgentMCPAuthorizationHeader: headers.Get(csf.AgentMCPAuthorizationHeader),
			csf.AgentMCPAgentIDHeader:       headers.Get(csf.AgentMCPAgentIDHeader),
			csf.AgentMCPSessionIDHeader:     headers.Get(csf.AgentMCPSessionIDHeader),
		}
		servers[workbenchCSFServer] = copilot.MCPHTTPServerConfig{
			URL: strings.TrimRight(origin, "/") + workbenchAgentMCPPath, Tools: []string{workbenchAllTools}, Headers: requestHeaders,
		}
		return servers, nil
	}
}
