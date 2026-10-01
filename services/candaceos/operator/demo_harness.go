package operator

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

type demoHarness struct {
	controller *Controller

	mu              sync.Mutex
	lifecycle       context.Context
	cancelLifecycle context.CancelFunc
	turnID          string
	cancelTurn      context.CancelFunc
	wg              sync.WaitGroup
}

func (h *demoHarness) Start(ctx context.Context) (harnessStart, error) {
	h.mu.Lock()
	if h.cancelLifecycle != nil {
		h.cancelLifecycle()
	}
	h.lifecycle, h.cancelLifecycle = context.WithCancel(ctx)
	h.turnID = ""
	h.cancelTurn = nil
	h.mu.Unlock()
	sessionID := "demo-" + uuid.NewString()
	return harnessStart{
		SessionID: sessionID,
		Activate: func() error {
			h.emit("", eventRecord{
				ID: uuid.NewString(), Type: eventKindSessionStart, Timestamp: time.Now().UTC(),
				Data: map[string]any{"message": "Safe demo mode: no real node changes are possible."},
			})
			return nil
		},
	}, nil
}

func (h *demoHarness) Send(_ context.Context, prompt *candaceosv1.HarnessPrompt) error {
	h.mu.Lock()
	if h.lifecycle == nil || h.lifecycle.Err() != nil {
		h.mu.Unlock()
		return errors.New("demo session is unavailable")
	}
	turnID := uuid.NewString()
	turnCtx, cancel := context.WithCancel(h.lifecycle)
	h.turnID = turnID
	h.cancelTurn = cancel
	h.wg.Add(1)
	h.mu.Unlock()
	if !h.emit(turnID, eventRecord{
		ID: uuid.NewString(), Type: eventKindUserMessage, Timestamp: time.Now().UTC(),
		Data: map[string]any{"content": prompt.GetContent()},
	}) {
		h.wg.Done()
		return errors.New("demo session is unavailable")
	}
	go h.completeTurn(turnCtx, turnID)
	return nil
}

func (h *demoHarness) completeTurn(ctx context.Context, turnID string) {
	defer h.wg.Done()
	if !waitDemo(ctx, 40*time.Millisecond) {
		return
	}
	messageID := uuid.NewString()
	if !h.emit(turnID, eventRecord{
		ID: uuid.NewString(), Type: eventKindAssistantDelta, Timestamp: time.Now().UTC(), Ephemeral: true,
		Data: map[string]any{"messageId": messageID, "deltaContent": "I’ll turn that into a small app revision, test it, and pause before deployment."},
	}) {
		return
	}
	if !waitDemo(ctx, 40*time.Millisecond) {
		return
	}
	if !h.emit(turnID, eventRecord{
		ID: uuid.NewString(), Type: eventKindAssistantMessage, Timestamp: time.Now().UTC(),
		Data: map[string]any{"messageId": messageID, "content": "Demo plan complete. A configured agent backend uses the same UI, but this safe demo cannot mutate a node."},
	}) {
		return
	}
	if !h.emit(turnID, eventRecord{
		ID: uuid.NewString(), Type: eventKindSessionIdle, Timestamp: time.Now().UTC(), Data: map[string]any{},
	}) {
		return
	}
	h.mu.Lock()
	if h.turnID == turnID {
		h.turnID = ""
		h.cancelTurn = nil
	}
	h.mu.Unlock()
}

func (h *demoHarness) Abort(ctx context.Context) error {
	h.mu.Lock()
	if h.cancelTurn != nil {
		h.cancelTurn()
	}
	h.turnID = ""
	h.cancelTurn = nil
	live := h.lifecycle != nil && h.lifecycle.Err() == nil
	lifecycle := h.lifecycle
	if live {
		h.wg.Add(1)
	}
	h.mu.Unlock()
	if live {
		// Model the runtime's separate abort acknowledgement without allowing the
		// canceled turn to publish its delayed completion. Close cancels this too.
		go func() {
			defer h.wg.Done()
			if waitDemo(lifecycle, 10*time.Millisecond) {
				h.emit("", eventRecord{
					ID: uuid.NewString(), Type: eventKindSessionIdle, Timestamp: time.Now().UTC(), Data: map[string]any{},
				})
			}
		}()
	}
	return nil
}

func (h *demoHarness) Close() error {
	h.mu.Lock()
	if h.cancelTurn != nil {
		h.cancelTurn()
	}
	if h.cancelLifecycle != nil {
		h.cancelLifecycle()
	}
	h.turnID = ""
	h.cancelTurn = nil
	h.lifecycle = nil
	h.cancelLifecycle = nil
	h.mu.Unlock()
	h.wg.Wait()
	return nil
}

func (h *demoHarness) emit(turnID string, event eventRecord) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lifecycle == nil || h.lifecycle.Err() != nil {
		return false
	}
	if turnID != "" && h.turnID != turnID {
		return false
	}
	h.controller.ingest(event)
	return true
}

func waitDemo(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
