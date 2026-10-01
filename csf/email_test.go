package csf

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ = Describe("operator email capability", func() {
	It("uses authenticated MCP identity and retains an uncertain outcome", func() {
		sender := NewMockIEmailSender(gomock.NewController(GinkgoT()))
		provenance := NewEmailProvenance(&emailv1.EmailHostConfiguration{SessionProvider: "copilot"}, nil)
		sender.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, message *emailv1.EmailMessage) (*emailv1.EmailReceipt, error) {
			Expect(message.Subject).To(Equal("Leader unreachable"))
			metadata, err := provenance.Snapshot(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(metadata.Sessions).To(HaveLen(1))
			Expect(metadata.Sessions[0].Provider).To(Equal("copilot"))
			Expect(metadata.Sessions[0].AgentId).To(Equal("reporter"))
			Expect(metadata.Sessions[0].SessionId).To(Equal("f385e291-aad4-41ee-8e12-97d92c29fb0d"))
			return &emailv1.EmailReceipt{Provenance: metadata, Outcome: emailv1.DeliveryOutcome_DELIVERY_OUTCOME_UNKNOWN}, errors.New("transport interrupted")
		})
		service, err := New(WithEmail(sender))
		Expect(err).NotTo(HaveOccurred())
		authenticator, err := NewAgentMCPAuthenticator([]byte("email-test-signing-key"))
		Expect(err).NotTo(HaveOccurred())
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"SendEmail","arguments":{"message":{"subject":"Leader unreachable","text":"Please investigate."}}}}`
		request := httptest.NewRequest(http.MethodPost, "/mcp/agent", strings.NewReader(body))
		request.Header, err = authenticator.AgentMCPHeaders("reporter", "f385e291-aad4-41ee-8e12-97d92c29fb0d")
		Expect(err).NotTo(HaveOccurred())
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", "2025-06-18")
		response := httptest.NewRecorder()
		service.AgentMCPHandler(authenticator).ServeHTTP(response, request)
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(response.Body.String()).To(ContainSubstring("DELIVERY_OUTCOME_UNKNOWN"))
		Expect(response.Body.String()).NotTo(ContainSubstring("transport interrupted"))
	})

	It("rejects unauthenticated calls, invalid content and cancellation before sending", func() {
		sender := NewMockIEmailSender(gomock.NewController(GinkgoT()))
		service, err := New(WithEmail(sender))
		Expect(err).NotTo(HaveOccurred())
		_, err = service.SendEmail(context.Background(), nil)
		Expect(err).To(MatchError(ErrUnauthorized))
		ctx := withVerifiedAgentIdentity(context.Background(), "reporter", "session")
		_, err = service.SendEmail(ctx, nil)
		Expect(errors.Is(err, ErrInvalidRequest)).To(BeTrue())
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err = service.SendEmail(ctx, nil)
		Expect(err).To(MatchError(context.Canceled))
	})

	It("reads only fresh container observations and cannot import a file's identity", func() {
		now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
		path := filepath.Join(GinkgoT().TempDir(), "containers.json")
		observations := &provenancev1.ReceiptMetadata{
			SchemaVersion: 1, RecordedAt: timestamppb.New(now),
			ReportingNode: &provenancev1.NodeIdentity{NodeId: "not-the-sender"},
			Sessions:      []*provenancev1.SessionReference{{Provider: "unrelated", SessionId: "not-the-caller"}},
			Containers:    []*provenancev1.ContainerObservation{{Name: "redis", State: provenancev1.ContainerState_CONTAINER_STATE_RUNNING, ObservedAt: timestamppb.New(now)}},
		}
		data, err := protojson.Marshal(observations)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, data, 0600)).To(Succeed())
		configuration := &emailv1.EmailHostConfiguration{ContainerObservationsFile: path, ReportingNode: &provenancev1.NodeIdentity{NodeId: "node-a"}}
		source := NewEmailProvenance(configuration, func() time.Time { return now })
		configuration.ReportingNode.NodeId = "later-mutation"
		metadata, err := source.Snapshot(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(metadata.ReportingNode.NodeId).To(Equal("node-a"))
		Expect(metadata.Sessions).To(BeEmpty())
		Expect(metadata.Containers).To(HaveLen(1))
		Expect(metadata.Unavailable).To(BeEmpty())
		now = now.Add(6 * time.Minute)
		metadata, err = source.Snapshot(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(metadata.Containers).To(BeEmpty())
		Expect(metadata.Unavailable).NotTo(BeEmpty())
	})
})
