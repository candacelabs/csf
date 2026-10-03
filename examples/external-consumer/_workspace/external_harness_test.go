package externalconsumer_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	deployv1 "github.com/candacelabs/csf/proto/candace/deploy/v1"
	"github.com/candacelabs/csf/services/harness"

	"example.com/candace-external-consumer/customharness"
	"example.com/candace-external-consumer/steering"
)

func TestExternalConsumer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "External Deploy Consumer Suite")
}

var _ harness.IHost = (*MockIHost)(nil)

var _ = Describe("an external harness", func() {
	It("publishes through the SDK host", func() {
		controller := gomock.NewController(GinkgoT())
		host := NewMockIHost(controller)
		gomock.InOrder(
			host.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, event *deployv1.HarnessEvent) error {
					Expect(ctx).NotTo(BeNil())
					Expect(event.GetRunId()).To(Equal("external-run-1"))
					Expect(event.GetAssistantMessage()).NotTo(BeNil())
					Expect(event.GetAssistantMessage().GetContent()).To(Equal("external echo: hello from another repository"))
					return nil
				},
			),
			host.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, event *deployv1.HarnessEvent) error {
					Expect(ctx).NotTo(BeNil())
					Expect(event.GetRunId()).To(Equal("external-run-1"))
					Expect(event.GetIdle()).NotTo(BeNil())
					return nil
				},
			),
		)

		instance, err := customharness.NewFactory(steering.Instance()).New(&deployv1.HarnessContext{
			Workspace: "/external/workspace",
		}, host)
		Expect(err).NotTo(HaveOccurred())
		Expect(instance.Identity).To(Equal(&deployv1.HarnessRuntimeIdentity{
			Backend:        deployv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
			Implementation: "external-echo",
			Model:          "echo-v1",
		}))

		session, err := instance.Runtime.Start(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(session.GetId()).To(Equal("external-echo-session"))
		Expect(instance.Runtime.Activate(context.Background())).To(Succeed())
		Expect(instance.Runtime.Send(context.Background(), &deployv1.HarnessPrompt{
			RunId:    "external-run-1",
			Content:  "hello from another repository",
			Delivery: deployv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE,
		})).To(Succeed())
		Expect(instance.Runtime.Close()).To(Succeed())
	})
})
