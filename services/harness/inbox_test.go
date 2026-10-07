// Copyright 2026 Candace Labs

package harness

import (
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

func TestMessageStateEnum(t *testing.T) {
	tests := []struct {
		state    harnessv1.MessageState
		name     string
		expected string
	}{
		{harnessv1.MessageState_MESSAGE_STATE_QUEUED, "QUEUED", "MESSAGE_STATE_QUEUED"},
		{harnessv1.MessageState_MESSAGE_STATE_DELIVERED, "DELIVERED", "MESSAGE_STATE_DELIVERED"},
		{harnessv1.MessageState_MESSAGE_STATE_READ, "READ", "MESSAGE_STATE_READ"},
		{harnessv1.MessageState_MESSAGE_STATE_ANSWERED, "ANSWERED", "MESSAGE_STATE_ANSWERED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.state.String(); got != test.expected {
				t.Errorf("got %s, want %s", got, test.expected)
			}
		})
	}
}

func TestInboxMessage(t *testing.T) {
	msg := &harnessv1.InboxMessage{
		ReceiptId:     uuid.New().String(),
		Text:          "test message",
		State:         harnessv1.MessageState_MESSAGE_STATE_QUEUED,
		SentAt:        timestamppb.Now(),
		PriorityClass: harnessv1.MessagePriorityClass_MESSAGE_PRIORITY_CLASS_QUEUE,
	}
	if msg.ReceiptId == "" {
		t.Error("ReceiptId should not be empty")
	}
	if msg.Text != "test message" {
		t.Errorf("Text: got %s, want 'test message'", msg.Text)
	}
	if msg.State != harnessv1.MessageState_MESSAGE_STATE_QUEUED {
		t.Errorf("State: got %v, want MESSAGE_STATE_QUEUED", msg.State)
	}
	if msg.SentAt == nil {
		t.Error("SentAt should not be nil")
	}
}
