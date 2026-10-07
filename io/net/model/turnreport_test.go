// Copyright 2026 Candace Labs

package model_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/net/model"
)

// loggedEvent is one "turn executor event" record as the report writes it.
type loggedEvent struct {
	Message   string          `json:"msg"`
	EventType string          `json:"event_type"`
	Sequence  int             `json:"sequence"`
	Event     json.RawMessage `json:"event"`
}

// loggedRecords decodes every record the report wrote.
func loggedRecords(output *bytes.Buffer) []loggedEvent {
	var records []loggedEvent
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var record loggedEvent
		Expect(json.Unmarshal([]byte(line), &record)).To(Succeed())
		records = append(records, record)
	}
	return records
}

// thinkingTick is a Claude Code thinking progress event carrying a running total.
func thinkingTick(tokens int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"system","subtype":"thinking_tokens","estimated_tokens":%d}`, tokens))
}

var _ = Describe("A turn report", func() {
	var (
		output *bytes.Buffer
		report *model.TurnReport
	)

	BeforeEach(func() {
		output = &bytes.Buffer{}
		var err error
		report, err = model.NewTurnReport(context.Background(), slog.New(slog.NewJSONHandler(output, nil)), "claudecode", uuid.New(), 1)
		Expect(err).NotTo(HaveOccurred())
	})

	It("thins a burst of thinking ticks to the first and the latest, written before the next event", func() {
		for tokens := 1; tokens <= 500; tokens++ {
			report.Event(model.DirectionOut, "system", thinkingTick(tokens))
		}
		report.Event(model.DirectionOut, "assistant", json.RawMessage(`{"type":"assistant"}`))

		records := loggedRecords(output)
		Expect(records).To(HaveLen(3))
		Expect(records[0].Event).To(MatchJSON(thinkingTick(1)))
		Expect(records[1].Event).To(MatchJSON(thinkingTick(500)))
		Expect(records[2].EventType).To(Equal("assistant"))
		Expect([]int{records[0].Sequence, records[1].Sequence, records[2].Sequence}).To(Equal([]int{1, 2, 3}))
	})

	It("writes the held running total before the turn's end", func() {
		report.Event(model.DirectionOut, "system", thinkingTick(10))
		report.Event(model.DirectionOut, "system", thinkingTick(20))
		report.Finished(2, nil)

		records := loggedRecords(output)
		Expect(records).To(HaveLen(3))
		Expect(records[1].Event).To(MatchJSON(thinkingTick(20)))
		Expect(records[2].Message).To(Equal("turn executor turn finished"))
	})

	It("writes every other system event as it comes", func() {
		for range 3 {
			report.Event(model.DirectionOut, "system", json.RawMessage(`{"type":"system","subtype":"init"}`))
		}
		Expect(loggedRecords(output)).To(HaveLen(3))
	})
})
