package email

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
)

var (
	fixedTime = time.Date(2026, 9, 21, 12, 34, 56, 123456789, time.UTC)
	fixedID   = "8c0f0a90-b7cb-4d75-a29f-fb0d70b2f6c2"
)

func configuredMailer(
	transport ITransport,
	provenance IProvenanceSource,
	sink IReceiptSink,
) *Mailer {
	GinkgoHelper()
	mailer, err := NewMailer(
		WithTransport(transport),
		WithProvenance(provenance),
		WithReceiptSink(sink),
		WithAddresses("Spine <spine@example.invalid>", []string{"Operator <operator@example.invalid>"}),
		WithClock(func() time.Time { return fixedTime }),
	)
	Expect(err).NotTo(HaveOccurred())
	mailer.newReceiptID = func() string { return fixedID }
	return mailer
}

func validMetadata() *provenancev1.ReceiptMetadata {
	return &provenancev1.ReceiptMetadata{
		ReportingNode:  &provenancev1.NodeIdentity{NodeId: "node-a", Hostname: "host-a"},
		CsfVersion:     "v1.2.3",
		SourceRevision: "abc123",
		Sessions: []*provenancev1.SessionReference{{
			Provider: "codex", SessionId: "session-7", AgentId: "agent-2",
			Url: "https://workbench.example.invalid/session/7?view=trace",
		}},
		Containers: []*provenancev1.ContainerObservation{{
			Name: "worker", Image: "worker@sha256:abc",
			State: provenancev1.ContainerState_CONTAINER_STATE_RUNNING,
			Links: []*provenancev1.EvidenceLink{{
				Kind:  provenancev1.EvidenceLinkKind_EVIDENCE_LINK_KIND_LOGS,
				Label: "worker logs", Url: "https://grafana.example.invalid/explore?orgId=1&var-node=node-a",
			}},
		}},
		Links: []*provenancev1.EvidenceLink{{
			Kind:  provenancev1.EvidenceLinkKind_EVIDENCE_LINK_KIND_GRAFANA,
			Label: "fleet dashboard", Url: "https://grafana.example.invalid/d/fleet?var-node=node-a",
		}},
	}
}

var _ = Describe("Mailer", func() {
	var (
		controller *gomock.Controller
		transport  *MockITransport
		provenance *MockIProvenanceSource
		sink       *MockIReceiptSink
	)

	BeforeEach(func() {
		controller = gomock.NewController(GinkgoT())
		transport = NewMockITransport(controller)
		provenance = NewMockIProvenanceSource(controller)
		sink = NewMockIReceiptSink(controller)
	})

	It("records UNKNOWN before network and replaces it with ACCEPTED", func() {
		metadata := validMetadata()
		provenance.EXPECT().Snapshot(gomock.Any()).Return(metadata, nil)
		gomock.InOrder(
			sink.EXPECT().Record(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, receipt *emailv1.EmailReceipt) error {
				Expect(receipt.GetOutcome()).To(Equal(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_UNKNOWN))
				Expect(receipt.GetCompletedAt()).To(BeNil())
				return nil
			}),
			transport.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, raw []byte) (emailv1.DeliveryOutcome, error) {
				plain, html := decodedMIMEBodies(raw)
				Expect(plain).To(ContainSubstring("Spine provenance"))
				Expect(html).To(ContainSubstring("Spine provenance"))
				return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED, nil
			}),
			sink.EXPECT().Record(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, receipt *emailv1.EmailReceipt) error {
				Expect(receipt.GetOutcome()).To(Equal(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED))
				Expect(receipt.GetCompletedAt()).NotTo(BeNil())
				return nil
			}),
		)

		mailer := configuredMailer(transport, provenance, sink)
		receipt, err := mailer.Send(context.Background(), &emailv1.EmailMessage{Subject: "Status", Text: "All systems nominal."})
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.GetProvenance().GetReceiptId()).To(Equal(fixedID))
		Expect(receipt.GetProvenance().GetSchemaVersion()).To(Equal(uint32(1)))
		Expect(receipt.GetMessageSha256()).To(MatchRegexp("^[0-9a-f]{64}$"))
		Expect(metadata.GetReceiptId()).To(BeEmpty(), "provider metadata was mutated")
	})

	DescribeTable("returns the receipt for failed or ambiguous transport outcomes",
		func(outcome emailv1.DeliveryOutcome, wantCode string) {
			provenance.EXPECT().Snapshot(gomock.Any()).Return(validMetadata(), nil)
			sink.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil).Times(2)
			transport.EXPECT().Send(gomock.Any(), gomock.Any()).Return(outcome, errors.New("smtp detail"))
			mailer := configuredMailer(transport, provenance, sink)
			receipt, err := mailer.Send(context.Background(), &emailv1.EmailMessage{Subject: "Status", Text: "body"})
			Expect(err).To(HaveOccurred())
			Expect(receipt).NotTo(BeNil())
			Expect(receipt.GetOutcome()).To(Equal(outcome))
			Expect(receipt.GetErrorCode()).To(Equal(wantCode))
		},
		Entry("failed", emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED, errorCodeFailed),
		Entry("unknown", emailv1.DeliveryOutcome_DELIVERY_OUTCOME_UNKNOWN, errorCodeUnknown),
	)

	It("does not call the transport when pre-send receipt persistence fails", func() {
		provenance.EXPECT().Snapshot(gomock.Any()).Return(validMetadata(), nil)
		sink.EXPECT().Record(gomock.Any(), gomock.Any()).Return(errors.New("disk unavailable"))
		mailer := configuredMailer(transport, provenance, sink)
		receipt, err := mailer.Send(context.Background(), &emailv1.EmailMessage{Subject: "Status", Text: "body"})
		Expect(err).To(MatchError(ContainSubstring("recording pre-send receipt")))
		Expect(receipt.GetErrorCode()).To(Equal(errorCodeSink))
		Expect(receipt.GetOutcome()).To(Equal(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED))
		Expect(receipt.GetCompletedAt()).NotTo(BeNil())
	})

	It("collects provenance failure as unavailable without inventing facts", func() {
		provenance.EXPECT().Snapshot(gomock.Any()).Return(nil, errors.New("observer down"))
		sink.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil).Times(2)
		transport.EXPECT().Send(gomock.Any(), gomock.Any()).Return(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED, nil)
		mailer := configuredMailer(transport, provenance, sink)
		receipt, err := mailer.Send(context.Background(), &emailv1.EmailMessage{Subject: "Status", Text: "body"})
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.GetProvenance().GetReportingNode()).To(BeNil())
		Expect(receipt.GetProvenance().GetCsfVersion()).To(BeEmpty())
		Expect(receipt.GetProvenance().GetUnavailable()).To(ContainElement(provenanceUnavailable))
	})

	DescribeTable("removes credential-bearing URLs before receipt retention and delivery", func(unsafeURL string) {
		metadata := validMetadata()
		metadata.Sessions[0].Url = "https://user:password@workbench.example.invalid/session/7"
		metadata.Links = append(metadata.Links, &provenancev1.EvidenceLink{
			Kind:  provenancev1.EvidenceLinkKind_EVIDENCE_LINK_KIND_TRACE,
			Label: "secret trace", Url: unsafeURL,
		})
		provenance.EXPECT().Snapshot(gomock.Any()).Return(metadata, nil)
		sink.EXPECT().Record(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, receipt *emailv1.EmailReceipt) error {
			serialized := receipt.String()
			Expect(serialized).NotTo(ContainSubstring("password"))
			Expect(serialized).NotTo(ContainSubstring("do-not-retain"))
			Expect(receipt.GetProvenance().GetSessions()[0].GetUrl()).To(BeEmpty())
			Expect(receipt.GetProvenance().GetUnavailable()).To(ContainElement(unsafeURLUnavailable))
			return nil
		}).Times(2)
		transport.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, raw []byte) (emailv1.DeliveryOutcome, error) {
			plain, html := decodedMIMEBodies(raw)
			Expect(plain).NotTo(ContainSubstring("do-not-retain"))
			Expect(html).NotTo(ContainSubstring("do-not-retain"))
			return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED, nil
		})
		mailer := configuredMailer(transport, provenance, sink)
		receipt, err := mailer.Send(context.Background(), &emailv1.EmailMessage{Subject: "Status", Text: "body"})
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.String()).NotTo(ContainSubstring("do-not-retain"))
		Expect(metadata.GetSessions()[0].GetUrl()).To(ContainSubstring("password"), "provider metadata was mutated")
	},
		Entry("query", "https://trace.example.invalid/open?access_token=do-not-retain"),
		Entry("fragment", "https://trace.example.invalid/open#access_token=do-not-retain"),
		Entry("fragment route query", "https://trace.example.invalid/#/open?access_token=do-not-retain"),
		Entry("encoded fragment key", "https://trace.example.invalid/open#access%5Ftoken=do-not-retain"),
		Entry("malformed query", "https://trace.example.invalid/open?access_token=do-not-retain;other=value"),
	)

	It("preserves a noncredential fragment in delivered and retained evidence links", func() {
		metadata := validMetadata()
		const evidenceURL = "https://docs.example.invalid/csf#overview"
		metadata.Links[0].Url = evidenceURL
		provenance.EXPECT().Snapshot(gomock.Any()).Return(metadata, nil)
		sink.EXPECT().Record(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, receipt *emailv1.EmailReceipt) error {
			Expect(receipt.GetProvenance().GetLinks()[0].GetUrl()).To(Equal(evidenceURL))
			return nil
		}).Times(2)
		transport.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, raw []byte) (emailv1.DeliveryOutcome, error) {
			plain, html := decodedMIMEBodies(raw)
			Expect(plain).To(ContainSubstring(evidenceURL))
			Expect(html).To(ContainSubstring(evidenceURL))
			return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED, nil
		})
		_, err := configuredMailer(transport, provenance, sink).Send(context.Background(), &emailv1.EmailMessage{Subject: "Status", Text: "body"})
		Expect(err).NotTo(HaveOccurred())
	})

	It("validates message bounds before transport", func() {
		provenance.EXPECT().Snapshot(gomock.Any()).Return(validMetadata(), nil)
		sink.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil)
		mailer := configuredMailer(transport, provenance, sink)
		receipt, err := mailer.Send(context.Background(), &emailv1.EmailMessage{Subject: "", Text: "body"})
		Expect(err).To(HaveOccurred())
		Expect(receipt.GetOutcome()).To(Equal(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED))
		Expect(receipt.GetErrorCode()).To(Equal(errorCodeValidation))
	})

	It("returns a failed receipt without transport when the context is cancelled", func() {
		provenance.EXPECT().Snapshot(gomock.Any()).Return(validMetadata(), nil)
		sink.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil).Times(2)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		mailer := configuredMailer(transport, provenance, sink)
		receipt, err := mailer.Send(ctx, &emailv1.EmailMessage{Subject: "Status", Text: "body"})
		Expect(err).To(MatchError(context.Canceled))
		Expect(receipt.GetOutcome()).To(Equal(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED))
	})

	It("emits only the bounded outcome label on both named metrics", func() {
		registry := prometheus.NewRegistry()
		provenance.EXPECT().Snapshot(gomock.Any()).Return(validMetadata(), nil)
		sink.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil).Times(2)
		transport.EXPECT().Send(gomock.Any(), gomock.Any()).Return(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED, nil)
		mailer, err := NewMailer(
			WithTransport(transport),
			WithProvenance(provenance),
			WithReceiptSink(sink),
			WithAddresses("spine@example.invalid", []string{"operator@example.invalid"}),
			WithMetrics(registry),
		)
		Expect(err).NotTo(HaveOccurred())
		_, err = mailer.Send(context.Background(), &emailv1.EmailMessage{Subject: "Status", Text: "body"})
		Expect(err).NotTo(HaveOccurred())
		families, err := registry.Gather()
		Expect(err).NotTo(HaveOccurred())
		seen := make(map[string]bool)
		for _, family := range families {
			name := family.GetName()
			if name != "csf_email_send_total" && name != "csf_email_send_duration_seconds" {
				continue
			}
			seen[name] = true
			Expect(family.GetMetric()).To(HaveLen(1))
			labels := family.GetMetric()[0].GetLabel()
			Expect(labels).To(HaveLen(1))
			Expect(labels[0].GetName()).To(Equal("outcome"))
			Expect(labels[0].GetValue()).To(Equal(metricOutcomeAccepted))
		}
		Expect(seen).To(HaveKeyWithValue("csf_email_send_total", true))
		Expect(seen).To(HaveKeyWithValue("csf_email_send_duration_seconds", true))
	})
})

var _ = Describe("deterministic MIME", func() {
	It("renders exact stable bytes and safely filters provenance hyperlinks", func() {
		metadata := validMetadata()
		metadata.SchemaVersion = 1
		metadata.ReceiptId = fixedID
		metadata.RecordedAt = timestamppb.New(fixedTime)
		metadata.Links = append(metadata.Links, &provenancev1.EvidenceLink{
			Kind:  provenancev1.EvidenceLinkKind_EVIDENCE_LINK_KIND_TRACE,
			Label: "unsafe <trace>", Url: "https://trace.example.invalid/open?token=secret",
		})
		message := &emailv1.EmailMessage{Subject: "Status\r\nBcc: bad@example.invalid", Text: "<script>alert(1)</script>"}
		from := address{header: "Spine <spine@example.invalid>", envelope: "spine@example.invalid"}
		to := []address{{header: "Operator <operator@example.invalid>", envelope: "operator@example.invalid"}}

		first, err := renderMessage(from, to, message, metadata)
		Expect(err).NotTo(HaveOccurred())
		second, err := renderMessage(from, to, message, proto.Clone(metadata).(*provenancev1.ReceiptMetadata))
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(Equal(first))
		Expect(string(first)).NotTo(ContainSubstring("\r\nBcc:"))
		Expect(string(first)).To(ContainSubstring("boundary=\"csf-8c0f0a90b7cb4d75a29ffb0d70b2f6c2\""))
		for _, line := range strings.Split(string(first), "\r\n") {
			Expect(len(line)).To(BeNumerically("<=", 998), "wire line exceeded RFC 5322 limit")
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(first))
		Expect(digest).To(Equal("d8b99d159ab335019e8e9b32b9c04fb287c1f8c1ddc906d5b1399c4396242c87"))

		plain, html := decodedMIMEBodies(first)
		Expect(plain).To(ContainSubstring("Reporting node: node-a / host-a"))
		Expect(plain).To(ContainSubstring("CSF version: v1.2.3"))
		Expect(plain).To(ContainSubstring("URL omitted"))
		Expect(html).To(ContainSubstring("&lt;script&gt;alert(1)&lt;/script&gt;"))
		Expect(html).To(ContainSubstring("orgId=1&amp;var-node=node-a"))
		Expect(html).NotTo(ContainSubstring("token=secret"))
	})

	It("keeps a maximum-size message and folded recipient headers within the wire line limit", func() {
		metadata := validMetadata()
		metadata.SchemaVersion = 1
		metadata.ReceiptId = fixedID
		metadata.RecordedAt = timestamppb.New(fixedTime)
		recipients := make([]address, 0, maxRecipients)
		for index := 0; index < maxRecipients; index++ {
			recipients = append(recipients, address{
				header:   fmt.Sprintf("Operator %02d <operator-%02d@example.invalid>", index, index),
				envelope: fmt.Sprintf("operator-%02d@example.invalid", index),
			})
		}
		raw, err := renderMessage(
			address{header: "Spine <spine@example.invalid>", envelope: "spine@example.invalid"},
			recipients,
			&emailv1.EmailMessage{Subject: strings.Repeat("s", 256), Text: strings.Repeat("x", 65536)},
			metadata,
		)
		Expect(err).NotTo(HaveOccurred())
		for _, line := range strings.Split(string(raw), "\r\n") {
			Expect(len(line)).To(BeNumerically("<=", 998), "wire line exceeded RFC 5322 limit")
		}
		plain, _ := decodedMIMEBodies(raw)
		Expect(plain).To(HavePrefix(strings.Repeat("x", 65536)))
	})
})

func decodedMIMEBodies(raw []byte) (string, string) {
	GinkgoHelper()
	message, err := mail.ReadMessage(strings.NewReader(string(raw)))
	Expect(err).NotTo(HaveOccurred())
	mediaType, parameters, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	Expect(err).NotTo(HaveOccurred())
	Expect(mediaType).To(Equal("multipart/alternative"))
	reader := multipart.NewReader(message.Body, parameters["boundary"])
	bodies := make(map[string]string)
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		Expect(nextErr).NotTo(HaveOccurred())
		partType, _, parseErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
		Expect(parseErr).NotTo(HaveOccurred())
		content, readErr := io.ReadAll(part)
		Expect(readErr).NotTo(HaveOccurred())
		bodies[partType] = string(content)
	}
	return bodies["text/plain"], bodies["text/html"]
}
