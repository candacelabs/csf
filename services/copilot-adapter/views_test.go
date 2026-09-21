package copilotadapter

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

var _ = Describe("generated API views", func() {
	It("normalizes valid nullable timestamps to UTC and preserves invalid values as nil", func() {
		local := time.Date(2026, time.September, 6, 14, 30, 0, 0, time.FixedZone("UTC+2", 2*60*60))
		view := views.Turn(storedb.Turn{CreatedAt: local, CompletedAt: null.TimeFrom(local)})

		Expect(view.CompletedAt).NotTo(BeNil())
		Expect(view.CompletedAt.Equal(local)).To(BeTrue())
		Expect(view.CompletedAt.Location()).To(Equal(time.UTC))
		body, err := json.Marshal(view)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring(`"completedAt":"2026-09-06T12:30:00Z"`))
		Expect(nullTimePointer(null.Time{})).To(BeNil())
	})
})

var _ = Describe("unusable live sessions", func() {
	It("drops registry ownership before a blocking Close call", func() {
		registry := newSessionRegistry(100 * time.Millisecond)
		DeferCleanup(registry.stop)
		identifier := uuid.New()
		liveDuringClose := make(chan bool, 1)
		closeContextErr := make(chan error, 1)
		Expect(registry.register(identifier, liveSession{handle: BridgeSession{Close: func(ctx context.Context) error {
			_, live := registry.lookup(identifier)
			liveDuringClose <- live
			_, hasDeadline := ctx.Deadline()
			Expect(hasDeadline).To(BeTrue())
			<-ctx.Done()
			closeContextErr <- ctx.Err()
			return ctx.Err()
		}}})).To(BeTrue())
		config := DefaultAdapterConfig()
		config.DurableTransitionTimeoutMillis = 100
		adapter := &CopilotAdapter{
			sessions: registry,
			logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
			config:   config,
		}

		adapter.detachUnusableLiveSession(identifier)
		Expect(<-liveDuringClose).To(BeFalse())
		Expect(<-closeContextErr).To(MatchError(context.DeadlineExceeded))
	})

	It("bounds registry shutdown with one owned deadline", func() {
		registry := newSessionRegistry(100 * time.Millisecond)
		identifier := uuid.New()
		type closeObservation struct {
			hasDeadline bool
			err         error
		}
		observed := make(chan closeObservation, 1)
		Expect(registry.register(identifier, liveSession{handle: BridgeSession{Close: func(ctx context.Context) error {
			_, hasDeadline := ctx.Deadline()
			<-ctx.Done()
			observed <- closeObservation{hasDeadline: hasDeadline, err: ctx.Err()}
			return ctx.Err()
		}}})).To(BeTrue())

		registry.stop()

		observation := <-observed
		Expect(observation.hasDeadline).To(BeTrue())
		Expect(observation.err).To(MatchError(context.DeadlineExceeded))
	})
})
