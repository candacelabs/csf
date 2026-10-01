package notify

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
	sharedemail "github.com/candacelabs/csf/services/email"
	"github.com/candacelabs/csf/services/warden"
)

func notifierConfig() SMTPConfig {
	return SMTPConfig{
		Host: "smtp.example.invalid", Port: 587,
		Username: "warden", Password: "secret",
		From: "warden@example.invalid", To: []string{"operator@example.invalid"},
	}
}

var _ = Describe("SMTPNotifier shared adapter", func() {
	var (
		controller *gomock.Controller
		transport  *MockITransport
		sink       *MockIReceiptSink
	)

	BeforeEach(func() {
		controller = gomock.NewController(GinkgoT())
		transport = NewMockITransport(controller)
		sink = NewMockIReceiptSink(controller)
	})

	DescribeTable("delivers long peer identities with a bounded UTF-8 subject and complete body", func(nodeID string, incidentType warden.IncidentType) {
		incident := deadIncident(nodeID)
		incident.Type = incidentType
		sink.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil).Times(2)
		transport.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, raw []byte) (emailv1.DeliveryOutcome, error) {
			message, err := mail.ReadMessage(bytes.NewReader(raw))
			Expect(err).NotTo(HaveOccurred())
			subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
			Expect(err).NotTo(HaveOccurred())
			Expect(len(subject)).To(BeNumerically("<=", 256))
			Expect(utf8.ValidString(subject)).To(BeTrue())
			Expect(subject).To(HaveSuffix("..."))
			_, parameters, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
			Expect(err).NotTo(HaveOccurred())
			part, err := multipart.NewReader(message.Body, parameters["boundary"]).NextPart()
			Expect(err).NotTo(HaveOccurred())
			body, err := io.ReadAll(part)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring(nodeID))
			return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED, nil
		})
		notifier := NewSMTPNotifier(notifierConfig(), withTransport(transport), WithReceiptSink(sink))
		Expect(notifier.Notify(context.Background(), incident)).To(Succeed())
	},
		Entry("dead ASCII node", strings.Repeat("node-", 70), warden.IncidentPeerDead),
		Entry("recovered multibyte node", strings.Repeat("節点", 60), warden.IncidentPeerRecovered),
		Entry("other incident", strings.Repeat("node-", 70), warden.IncidentType("custom")),
	)

	It("uses incident.ReportedBy provenance by default and removes the incorrect leader label", func() {
		sink.EXPECT().Record(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, receipt *emailv1.EmailReceipt) error {
			Expect(receipt.GetProvenance().GetReportingNode().GetNodeId()).To(Equal("node-c"))
			return nil
		}).Times(2)
		transport.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, message []byte) (emailv1.DeliveryOutcome, error) {
			wire := string(message)
			Expect(wire).To(ContainSubstring("Reported by:  node-c"))
			Expect(wire).NotTo(ContainSubstring("(leader)"))
			Expect(wire).To(ContainSubstring("Spine provenance"))
			return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED, nil
		})
		notifier := NewSMTPNotifier(
			notifierConfig(),
			withTransport(transport),
			WithReceiptSink(sink),
			withClock(func() time.Time { return testTime }),
		)
		Expect(notifier.Notify(context.Background(), deadIncident("node-a"))).To(Succeed())
	})

	It("returns a non-retryable terminal error for ambiguous SMTP acceptance", func() {
		sink.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil).Times(2)
		transport.EXPECT().Send(gomock.Any(), gomock.Any()).Return(
			emailv1.DeliveryOutcome_DELIVERY_OUTCOME_UNKNOWN,
			errors.New("DATA reply lost"),
		)
		notifier := NewSMTPNotifier(notifierConfig(), withTransport(transport), WithReceiptSink(sink))
		err := notifier.Notify(context.Background(), deadIncident("node-a"))
		var terminal *TerminalDeliveryError
		Expect(errors.As(err, &terminal)).To(BeTrue())
		Expect(terminal.Outcome).To(Equal(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_UNKNOWN))
		Expect(terminal.Retryable()).To(BeFalse())
		Expect(errors.Is(err, sharedemail.ErrDeliveryUnknown)).To(BeTrue())
	})

	It("returns a non-retryable terminal error when accepted delivery cannot record its final receipt", func() {
		gomock.InOrder(
			sink.EXPECT().Record(gomock.Any(), gomock.Any()).Return(nil),
			transport.EXPECT().Send(gomock.Any(), gomock.Any()).Return(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED, nil),
			sink.EXPECT().Record(gomock.Any(), gomock.Any()).Return(errors.New("disk full")),
		)
		notifier := NewSMTPNotifier(notifierConfig(), withTransport(transport), WithReceiptSink(sink))
		err := notifier.Notify(context.Background(), deadIncident("node-a"))
		var terminal *TerminalDeliveryError
		Expect(errors.As(err, &terminal)).To(BeTrue())
		Expect(terminal.Outcome).To(Equal(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED))
		Expect(terminal.Retryable()).To(BeFalse())
	})

	It("keeps a pre-send receipt failure retryable because transport was never called", func() {
		sink.EXPECT().Record(gomock.Any(), gomock.Any()).Return(errors.New("disk full"))
		notifier := NewSMTPNotifier(notifierConfig(), withTransport(transport), WithReceiptSink(sink))
		err := notifier.Notify(context.Background(), deadIncident("node-a"))
		var terminal *TerminalDeliveryError
		Expect(errors.As(err, &terminal)).To(BeFalse())
		Expect(err).To(HaveOccurred())
	})

	It("discards partial host provenance on observer failure but retains incident reporter", func() {
		source := NewMockIProvenanceSource(controller)
		source.EXPECT().Snapshot(gomock.Any()).Return(&provenancev1.ReceiptMetadata{
			CsfVersion: "stale-partial",
		}, errors.New("observer failed"))
		sink.EXPECT().Record(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, receipt *emailv1.EmailReceipt) error {
			metadata := receipt.GetProvenance()
			Expect(metadata.GetReportingNode().GetNodeId()).To(Equal("node-c"))
			Expect(metadata.GetCsfVersion()).To(BeEmpty())
			Expect(strings.Join(metadata.GetUnavailable(), " ")).To(ContainSubstring(hostProvenanceUnavailable))
			return nil
		}).Times(2)
		transport.EXPECT().Send(gomock.Any(), gomock.Any()).Return(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED, nil)
		notifier := NewSMTPNotifier(
			notifierConfig(),
			withTransport(transport),
			WithProvenance(source),
			WithReceiptSink(sink),
		)
		Expect(notifier.Notify(context.Background(), deadIncident("node-a"))).To(Succeed())
	})

	It("rejects header injection before transport", func() {
		config := notifierConfig()
		config.From = "warden@example.invalid\r\nBcc: attacker@example.invalid"
		notifier := NewSMTPNotifier(config, withTransport(transport), WithReceiptSink(sink))
		Expect(errors.Is(notifier.Notify(context.Background(), deadIncident("node-a")), ErrHeaderInjection)).To(BeTrue())
	})
})
