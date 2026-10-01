package email

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
)

var _ = Describe("SMTPTransport", func() {
	var (
		controller *gomock.Controller
		client     *MockSMTPClient
		transport  *SMTPTransport
	)

	BeforeEach(func() {
		controller = gomock.NewController(GinkgoT())
		client = NewMockSMTPClient(controller)
		transport = NewSMTPTransport(SMTPConfig{
			Host: "smtp.example.invalid", Port: 587,
			Username: "warden", Password: "secret",
		})
	})

	It("fails closed without STARTTLS and never calls AUTH, MAIL, RCPT, or DATA", func() {
		gomock.InOrder(
			client.EXPECT().Hello(gomock.Any()).Return(nil),
			client.EXPECT().Extension("STARTTLS").Return(false, ""),
		)
		outcome, err := transport.converse(
			client,
			"spine@example.invalid",
			[]string{"operator@example.invalid"},
			[]byte("message"),
		)
		Expect(errors.Is(err, ErrSTARTTLSRequired)).To(BeTrue())
		Expect(outcome).To(Equal(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED))
	})

	It("keeps ACCEPTED when QUIT fails after DATA closes successfully", func() {
		writer := NewMockWriteCloser(controller)
		gomock.InOrder(
			client.EXPECT().Hello(gomock.Any()).Return(nil),
			client.EXPECT().Extension("STARTTLS").Return(true, ""),
			client.EXPECT().StartTLS(gomock.Any()).Return(nil),
			client.EXPECT().Extension("AUTH").Return(true, "PLAIN"),
			client.EXPECT().Auth(gomock.Any()).Return(nil),
			client.EXPECT().Mail("spine@example.invalid").Return(nil),
			client.EXPECT().Rcpt("operator@example.invalid").Return(nil),
			client.EXPECT().Data().Return(writer, nil),
			writer.EXPECT().Write([]byte("message")).Return(len("message"), nil),
			writer.EXPECT().Close().Return(nil),
			client.EXPECT().Quit().Return(errors.New("connection closed")),
		)
		outcome, err := transport.converse(
			client,
			"spine@example.invalid",
			[]string{"operator@example.invalid"},
			[]byte("message"),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED))
	})

	It("reports UNKNOWN when DATA close leaves server acceptance ambiguous", func() {
		transport.config.Username = ""
		writer := NewMockWriteCloser(controller)
		gomock.InOrder(
			client.EXPECT().Hello(gomock.Any()).Return(nil),
			client.EXPECT().Extension("STARTTLS").Return(true, ""),
			client.EXPECT().StartTLS(gomock.Any()).Return(nil),
			client.EXPECT().Mail("spine@example.invalid").Return(nil),
			client.EXPECT().Rcpt("operator@example.invalid").Return(nil),
			client.EXPECT().Data().Return(writer, nil),
			writer.EXPECT().Write([]byte("message")).Return(len("message"), nil),
			writer.EXPECT().Close().Return(errors.New("reply lost")),
		)
		outcome, err := transport.converse(
			client,
			"spine@example.invalid",
			[]string{"operator@example.invalid"},
			[]byte("message"),
		)
		Expect(err).To(HaveOccurred())
		Expect(outcome).To(Equal(emailv1.DeliveryOutcome_DELIVERY_OUTCOME_UNKNOWN))
	})
})
