package copilotadapter_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	copilotv1 "github.com/candacelabs/csf/services/copilot-adapter/proto/candace/copilot/v1"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

var _ = Describe("durable trace delivery", func() {
	var (
		queries   *MockIStore
		transport *MockClient
		exporter  *copilotadapter.TraceExporter
		claim     storedb.ClaimTraceDeliveryRow
		usage     storedb.ProviderUsageEvent
	)
	BeforeEach(func() {
		controller := gomock.NewController(GinkgoT())
		queries, transport = NewMockIStore(controller), NewMockClient(controller)
		config := &copilotv1.TraceExportConfig{}
		document, err := os.ReadFile("config/traces.defaults.json")
		Expect(err).NotTo(HaveOccurred())
		Expect(protojson.Unmarshal(document, config)).To(Succeed())
		config.EndpointUrl, config.PublicKey, config.SecretKey = "http://collector.invalid/api/public/otel/v1/traces", "public-fixture", "secret-fixture"
		exporter, err = copilotadapter.NewTraceExporter(queries, config, copilotadapter.WithTraceClient(transport))
		Expect(err).NotTo(HaveOccurred())
		session := uuid.New()
		claim = storedb.ClaimTraceDeliveryRow{DeliveryID: "usage:" + session.String() + ":event", SessionID: session, UsageEventID: null.StringFrom("event"), Generation: 3}
		usage = storedb.ProviderUsageEvent{SessionID: session, EventID: "event", Kind: "modelCall", OccurredAt: time.Now().UTC(), Model: null.StringFrom("fixture-model"), InputTokens: null.IntFrom(0), OutputTokens: null.IntFrom(8), ApiDurationMs: null.IntFrom(1234), ProviderEvent: []byte(`{"id":"event","data":{"inputTokens":0,"outputTokens":8}}`)}
		queries.EXPECT().ClaimTraceDelivery(gomock.Any(), gomock.Any()).Return(claim, nil)
		queries.EXPECT().GetProviderUsageEvent(gomock.Any(), storedb.GetProviderUsageEventParams{SessionID: session, EventID: "event"}).Return(usage, nil)
	})

	It("exports persisted values and deterministic identities before accepting the fenced delivery", func() {
		upload := transport.EXPECT().UploadTraces(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, payload []*tracepb.ResourceSpans) error {
			Expect(payload).To(HaveLen(1))
			span := payload[0].ScopeSpans[0].Spans[0]
			expectedTrace := sha256.Sum256([]byte("csf.session.v1\x00" + claim.SessionID.String()))
			expectedSpan := sha256.Sum256([]byte("csf.observation.v1\x00" + claim.DeliveryID))
			Expect(span.TraceId).To(Equal(expectedTrace[:16]))
			Expect(span.SpanId).To(Equal(expectedSpan[:8]))
			Expect(span.ParentSpanId).To(BeEmpty())
			Expect(span.EndTimeUnixNano - span.StartTimeUnixNano).To(Equal(uint64(1234 * time.Millisecond)))
			known := map[string]bool{}
			for _, attribute := range span.Attributes {
				known[attribute.Key] = true
				if attribute.Key == "gen_ai.usage.input_tokens" {
					Expect(attribute.Value.GetIntValue()).To(BeZero())
				}
				if attribute.Key == "langfuse.observation.metadata.provider_event" {
					Expect(attribute.Value.GetStringValue()).To(ContainSubstring(`"outputTokens":8`))
				}
			}
			Expect(known["gen_ai.usage.input_tokens"]).To(BeTrue())
			Expect(known["gen_ai.usage.reasoning.output_tokens"]).To(BeFalse())
			Expect(known["langfuse.observation.metadata.premium_requests"]).To(BeFalse())
			return nil
		})
		queries.EXPECT().CompleteTraceDelivery(gomock.Any(), storedb.CompleteTraceDeliveryParams{DeliveryID: claim.DeliveryID, Generation: 3}).After(upload).Return(storedb.TraceDelivery{}, nil)
		worked, err := exporter.DeliverNext(context.Background())
		Expect(worked).To(BeTrue())
		Expect(err).NotTo(HaveOccurred())
	})

	It("records a retryable failure instead of acknowledging a rejected export", func() {
		rejection := errors.New("collector refused the payload")
		transport.EXPECT().UploadTraces(gomock.Any(), gomock.Any()).Return(rejection)
		queries.EXPECT().FailTraceDelivery(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, failure storedb.FailTraceDeliveryParams) (storedb.TraceDelivery, error) {
			Expect(failure.DeliveryID).To(Equal(claim.DeliveryID))
			Expect(failure.Generation).To(Equal(int64(3)))
			Expect(failure.LastError).To(Equal(rejection.Error()))
			return storedb.TraceDelivery{}, nil
		})
		worked, err := exporter.DeliverNext(context.Background())
		Expect(worked).To(BeTrue())
		Expect(err).To(MatchError(rejection))
	})

	It("leaves the lease recoverable when shutdown interrupts an upload", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		transport.EXPECT().UploadTraces(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, payload []*tracepb.ResourceSpans) error { cancel(); return context.Canceled })
		worked, err := exporter.DeliverNext(ctx)
		Expect(worked).To(BeTrue())
		Expect(err).To(MatchError(context.Canceled))
	})
})
