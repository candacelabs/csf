// Copyright 2026 Candace Labs

package csf_test

import (
	"context"
	"encoding/json"
	stdnet "net"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/goleak"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/io"
	"github.com/candacelabs/csf/io/net/model/copilot"
	ionet "github.com/candacelabs/csf/io/net"
	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/pkg/eventually"
	agentv1 "github.com/candacelabs/csf/proto/candace/agent/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/relay"
	"github.com/candacelabs/csf/services/relay/relaytest"
)

const (
	messagingSigningKey      = "agent-messaging-test-key"
	messagingHostAgent       = "reviewer"
	messagingNetworkAgent    = "remote"
	messagingResidentAgent   = "resident"
	messagingHostSession     = "6f1d0c3a-1b2c-4d5e-8f90-a1b2c3d4e5f6"
	messagingNetworkSession  = "7a2e1d4b-2c3d-4e5f-9a01-b2c3d4e5f6a7"
	messagingResidentSession = "8b3f2e5c-3d4e-4f60-8b12-c3d4e5f6a7b8"
	messagingSocketName      = "csf-agents.sock"
	messagingUnixHost        = "http://csf-agents/"
	messagingLoopback        = "127.0.0.1:0"
	messagingTCP             = "tcp"
	messagingUnix            = "unix"
	messagingClaudeCode      = "claudecode"
	messagingCopilot         = "copilot"
	messagingQuestion        = "does the scope join every inbox?"
	messagingAnswer          = "yes, on cancel"
	messagingRelayed         = "relayed to the resident"
	messagingReferenceLabel  = "the shutdown-order change"
	messagingReferenceURI    = "https://example.invalid/pulls/1"
	conformancePrefix        = "agent message "
)

func question() *agentv1.AgentMessage {
	return &agentv1.AgentMessage{Kind: agentv1.AgentMessageKind_AGENT_MESSAGE_KIND_REQUEST, Text: messagingQuestion}
}

func relayedNote() *agentv1.AgentMessage {
	return &agentv1.AgentMessage{Kind: agentv1.AgentMessageKind_AGENT_MESSAGE_KIND_NOTE, Text: messagingRelayed}
}

func answer() *agentv1.AgentMessage {
	return &agentv1.AgentMessage{
		Kind:      agentv1.AgentMessageKind_AGENT_MESSAGE_KIND_REPLY,
		Text:      messagingAnswer,
		Reference: &agentv1.AgentReference{Label: messagingReferenceLabel, Uri: messagingReferenceURI},
	}
}

// The agent message contract runs the relay's generic messaging conformance
// specs, the same ones a relay of plain strings runs.
var _ = relaytest.DescribeRelayConformance("Relay[*agentv1.AgentMessage]", relay.NewMemoryRegistry,
	func(sequence int) *agentv1.AgentMessage {
		return &agentv1.AgentMessage{Kind: agentv1.AgentMessageKind_AGENT_MESSAGE_KIND_NOTE, Text: conformancePrefix + strconv.Itoa(sequence)}
	})

// messagingReadyBudget bounds the wait for both listeners to bind.
var messagingReadyBudget = eventually.Budget{Within: 10 * time.Second}

// signedTransport adds one agent session's credential to every request, the
// way an agent's MCP client configuration carries it.
type signedTransport struct {
	base    http.RoundTripper
	headers http.Header
}

func (transport signedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	signed := request.Clone(request.Context())
	for name, values := range transport.headers {
		signed.Header[name] = values
	}
	return transport.base.RoundTrip(signed)
}

// agentClient is one host or network agent's MCP session with CSF.
type agentClient struct {
	session *mcp.ClientSession
}

func connectAgent(ctx context.Context, authenticator *csf.AgentMCPAuthenticator, agentID string, sessionID string, endpoint string, dial func(ctx context.Context) (stdnet.Conn, error)) *agentClient {
	headers, err := authenticator.AgentMCPHeaders(agentID, sessionID)
	Expect(err).NotTo(HaveOccurred())
	transport := &http.Transport{DialContext: func(ctx context.Context, _ string, _ string) (stdnet.Conn, error) { return dial(ctx) }}
	DeferCleanup(transport.CloseIdleConnections)
	client := mcp.NewClient(&mcp.Implementation{Name: agentID, Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           &http.Client{Transport: signedTransport{base: transport, headers: headers}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(session.Close)
	return &agentClient{session: session}
}

// call invokes one tool and decodes its structured result.
func call[Output any](ctx context.Context, agent *agentClient, tool string, input any) (Output, error) {
	var output Output
	result, err := agent.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: input})
	if err != nil {
		return output, err
	}
	if result.IsError {
		return output, &toolError{result: result}
	}
	encoded, err := json.Marshal(result.StructuredContent)
	Expect(err).NotTo(HaveOccurred())
	Expect(json.Unmarshal(encoded, &output)).To(Succeed())
	return output, nil
}

type toolError struct{ result *mcp.CallToolResult }

func (failure *toolError) Error() string {
	text := ""
	for _, content := range failure.result.Content {
		if typed, ok := content.(*mcp.TextContent); ok {
			text += typed.Text
		}
	}
	return text
}

func tierCount(registry *prometheus.Registry, name string, tier io.Tier) float64 {
	families, err := registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == relay.MetricTierLabel && label.GetValue() == tier.String() {
					return metric.GetCounter().GetValue()
				}
			}
		}
	}
	Fail(name + " has no " + tier.String() + " series")
	return 0
}

var _ = Describe("agent messaging over MCP", func() {
	BeforeEach(func() {
		baseline := goleak.IgnoreCurrent()
		DeferCleanup(func() {
			Expect(goleak.Find(baseline)).To(Succeed(), "the host runtime must join the relay and both listeners")
		})
	})

	It("relays between a host agent on a unix socket, a network agent on TCP and a resident agent", func(ctx SpecContext) {
		registry := prometheus.NewRegistry()
		core, err := relay.NewRelay[*agentv1.AgentMessage](relay.WithMetrics(registry))
		Expect(err).NotTo(HaveOccurred())
		service, err := csf.New(csf.WithAgentMessaging(core))
		Expect(err).NotTo(HaveOccurred())
		authenticator, err := csf.NewAgentMCPAuthenticator([]byte(messagingSigningKey))
		Expect(err).NotTo(HaveOccurred())
		handler := service.AgentMCPHandler(authenticator)

		// The binary grants the kernel network stack once; both listeners
		// and both test clients cross the boundary only through it. The
		// network listener is bound on loopback because a spec has no
		// second machine; its agent is network-tier because of the address
		// it registers, not because of where its packets come from.
		network := ionet.NewHostNetwork()
		socket := filepath.Join(GinkgoT().TempDir(), messagingSocketName)
		hostListener, err := iohttp.NewUnixSocketHTTPListener(network, socket, handler)
		Expect(err).NotTo(HaveOccurred())
		networkListener, err := iohttp.NewHTTPListener(network, messagingLoopback, handler)
		Expect(err).NotTo(HaveOccurred())

		resident, err := copilot.NewInProcessAddress(uuid.MustParse(messagingResidentSession))
		Expect(err).NotTo(HaveOccurred())
		residentMessenger, err := relay.NewMessenger(core)
		Expect(err).NotTo(HaveOccurred())
		host, err := runtime.NewHostRuntime(runtime.WithHostName("agent messaging spec"))
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Mount("relay", core)).To(Succeed())
		Expect(host.Mount("resident", runtime.ServiceFunc(func(scope *runtime.Scope) error {
			return core.Register(scope.Context(), relay.Registration{Agent: messagingResidentAgent, Address: resident})
		}))).To(Succeed())
		Expect(host.Mount("host listener", hostListener)).To(Succeed())
		Expect(host.Mount("network listener", networkListener)).To(Succeed())
		owner := runtime.NewScope(ctx, "spec")
		Expect(owner.Go(host.Run)).To(Succeed())
		DeferCleanup(func() { Expect(owner.Close()).To(Succeed()) })
		eventually.Await(GinkgoT(), "the relay and both listeners to start", messagingReadyBudget, host.Ready, func(ready bool) bool { return ready })

		reviewer := connectAgent(ctx, authenticator, messagingHostAgent, messagingHostSession, messagingUnixHost,
			func(ctx context.Context) (stdnet.Conn, error) { return network.DialContext(ctx, messagingUnix, socket) })
		networkAddress := networkListener.Addr().String()
		remote := connectAgent(ctx, authenticator, messagingNetworkAgent, messagingNetworkSession, "http://"+networkAddress+"/",
			func(ctx context.Context) (stdnet.Conn, error) {
				return network.DialContext(ctx, messagingTCP, networkAddress)
			})

		registered, err := call[csf.AgentRegistrationView](ctx, reviewer, csf.RegisterAgentAddressTool,
			csf.RegisterAgentAddressInput{Provider: messagingClaudeCode, Endpoint: "unix:" + socket})
		Expect(err).NotTo(HaveOccurred())
		Expect(registered.Tier).To(Equal(io.TierIpc.String()))
		Expect(registered.Agent).To(Equal(messagingHostAgent))
		registered, err = call[csf.AgentRegistrationView](ctx, remote, csf.RegisterAgentAddressTool,
			csf.RegisterAgentAddressInput{Provider: messagingCopilot, Endpoint: networkAddress})
		Expect(err).NotTo(HaveOccurred())
		Expect(registered.Tier).To(Equal(io.TierNet.String()))

		// host → in_process: the resident receives by a direct call.
		sent, err := call[csf.AgentEnvelopeView](ctx, reviewer, csf.SendAgentMessageTool,
			csf.SendAgentMessageInput{To: messagingResidentAgent, Message: question()})
		Expect(err).NotTo(HaveOccurred())
		Expect(sent.Tier).To(Equal(io.TierIpc.String()))
		received, err := residentMessenger.Receive(ctx, messagingResidentAgent)
		Expect(err).NotTo(HaveOccurred())
		Expect(received.Body).To(BeComparableTo(question(), protocmp.Transform()))
		Expect(received.Tier).To(Equal(io.TierIpc))

		// in_process → network, then network → host.
		relayed, err := residentMessenger.Send(ctx, resident, mustResolve(ctx, core, messagingNetworkAgent).Address, relayedNote())
		Expect(err).NotTo(HaveOccurred())
		Expect(relayed.Tier).To(Equal(io.TierNet))
		sent, err = call[csf.AgentEnvelopeView](ctx, remote, csf.SendAgentMessageTool,
			csf.SendAgentMessageInput{To: messagingHostAgent, Message: answer()})
		Expect(err).NotTo(HaveOccurred())
		Expect(sent.Tier).To(Equal(io.TierNet.String()))

		remoteInbox, err := call[csf.FetchAgentInboxOutput](ctx, remote, csf.FetchAgentInboxTool, csf.FetchAgentInboxInput{})
		Expect(err).NotTo(HaveOccurred())
		Expect(remoteInbox.Envelopes).To(HaveLen(1))
		Expect(remoteInbox.Envelopes[0].Message).To(BeComparableTo(relayedNote(), protocmp.Transform()))
		Expect(remoteInbox.Envelopes[0].From).To(Equal(messagingResidentAgent))
		reviewerInbox, err := call[csf.FetchAgentInboxOutput](ctx, reviewer, csf.FetchAgentInboxTool, csf.FetchAgentInboxInput{Limit: 5})
		Expect(err).NotTo(HaveOccurred())
		Expect(reviewerInbox.Envelopes).To(HaveLen(1))
		Expect(reviewerInbox.Envelopes[0].Tier).To(Equal(io.TierNet.String()))
		Expect(reviewerInbox.Envelopes[0].Message).To(BeComparableTo(answer(), protocmp.Transform()), "the reference survives the MCP round trip")

		for _, acknowledgement := range []struct {
			agent *agentClient
			id    string
		}{{remote, remoteInbox.Envelopes[0].ID}, {reviewer, reviewerInbox.Envelopes[0].ID}} {
			acknowledged, err := call[csf.AcknowledgeAgentInboxOutput](ctx, acknowledgement.agent, csf.AcknowledgeAgentInboxTool,
				csf.AcknowledgeAgentInboxInput{IDs: []string{acknowledgement.id}})
			Expect(err).NotTo(HaveOccurred())
			Expect(acknowledged.Acknowledged).To(Equal(1))
		}
		emptied, err := call[csf.FetchAgentInboxOutput](ctx, reviewer, csf.FetchAgentInboxTool, csf.FetchAgentInboxInput{})
		Expect(err).NotTo(HaveOccurred())
		Expect(emptied.Envelopes).To(BeEmpty())

		Expect(tierCount(registry, relay.MetricEnvelopesSent, io.TierInProcess)).To(BeZero())
		Expect(tierCount(registry, relay.MetricEnvelopesSent, io.TierIpc)).To(Equal(1.0))
		Expect(tierCount(registry, relay.MetricEnvelopesSent, io.TierNet)).To(Equal(2.0))
		Expect(tierCount(registry, relay.MetricEnvelopesDelivered, io.TierIpc)).To(Equal(1.0))
		Expect(tierCount(registry, relay.MetricEnvelopesDelivered, io.TierNet)).To(Equal(2.0))
	})

	It("refuses a send from an agent that never registered, and an unknown provider", func(ctx SpecContext) {
		core, err := relay.NewRelay[*agentv1.AgentMessage]()
		Expect(err).NotTo(HaveOccurred())
		scope := runtime.NewScope(ctx, "spec")
		Expect(core.Start(scope)).To(Succeed())
		DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })
		service, err := csf.New(csf.WithAgentMessaging(core))
		Expect(err).NotTo(HaveOccurred())
		authenticator, err := csf.NewAgentMCPAuthenticator([]byte(messagingSigningKey))
		Expect(err).NotTo(HaveOccurred())
		network := ionet.NewHostNetwork()
		listener, err := iohttp.NewHTTPListener(network, messagingLoopback, service.AgentMCPHandler(authenticator))
		Expect(err).NotTo(HaveOccurred())
		Expect(listener.Start(scope)).To(Succeed())
		address := listener.Addr().String()
		stranger := connectAgent(ctx, authenticator, messagingNetworkAgent, messagingNetworkSession, "http://"+address+"/",
			func(ctx context.Context) (stdnet.Conn, error) { return network.DialContext(ctx, messagingTCP, address) })

		_, err = call[csf.AgentEnvelopeView](ctx, stranger, csf.SendAgentMessageTool, csf.SendAgentMessageInput{To: messagingHostAgent, Message: question()})
		Expect(err).To(MatchError(ContainSubstring("register your address before sending")))
		_, err = call[csf.AgentRegistrationView](ctx, stranger, csf.RegisterAgentAddressTool, csf.RegisterAgentAddressInput{Provider: messagingCopilot, Endpoint: address})
		Expect(err).NotTo(HaveOccurred())
		_, err = call[csf.AgentEnvelopeView](ctx, stranger, csf.SendAgentMessageTool,
			csf.SendAgentMessageInput{To: messagingNetworkAgent, Message: &agentv1.AgentMessage{Kind: agentv1.AgentMessageKind_AGENT_MESSAGE_KIND_NOTE}})
		Expect(err).To(MatchError(ContainSubstring("text")), "a message that breaks its contract is refused before it is relayed")
		_, err = call[csf.AgentRegistrationView](ctx, stranger, csf.RegisterAgentAddressTool, csf.RegisterAgentAddressInput{Provider: "codex", Endpoint: address})
		Expect(err).To(MatchError(ContainSubstring(`provider "codex" cannot register over MCP`)))
		_, err = call[csf.AgentRegistrationView](ctx, stranger, csf.RegisterAgentAddressTool, csf.RegisterAgentAddressInput{Provider: messagingClaudeCode, Endpoint: "203.0.113.7:14111"})
		Expect(err).To(MatchError(ContainSubstring("unix socket or a loopback address")), "a host-tier address cannot name a socket another machine reaches")
	})
})

func mustResolve(ctx context.Context, core *relay.Relay[*agentv1.AgentMessage], agent relay.AgentID) relay.Registration {
	registration, err := core.Resolve(ctx, agent)
	Expect(err).NotTo(HaveOccurred())
	return registration
}
