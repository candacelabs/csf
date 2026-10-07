// Copyright 2026 Candace Labs

package codes

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/affect"
)

// The transcript vocabulary the reader acts on, spelled as Claude Code
// writes its session transcripts (~/.claude/projects/<project>/<session>.jsonl).
const (
	entryUser      = "user"
	entryAssistant = "assistant"
	originHuman    = "human"
	blockText      = "text"
	// maxTranscriptLine bounds one transcript line: a tool result can carry
	// a whole file.
	maxTranscriptLine = 32 << 20
)

// transcriptEntry is the part of one transcript line the reader needs.
type transcriptEntry struct {
	Type      string    `json:"type"`
	UUID      string    `json:"uuid"`
	Timestamp time.Time `json:"timestamp"`
	Origin    *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// text is the entry's text: a string content, or its text blocks joined by
// a blank line.
func (entry *transcriptEntry) text() string {
	var whole string
	if json.Unmarshal(entry.Message.Content, &whole) == nil {
		return whole
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(entry.Message.Content, &blocks) != nil {
		return ""
	}
	parts := []string{}
	for _, block := range blocks {
		if block.Type == blockText && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// ReaderRecall is how many of a reference's labeled messages the corpus
// holds, and how many of those pkg/affect reads as corrections.
type ReaderRecall struct {
	Labeled     int `json:"labeled"`
	Corrections int `json:"corrections"`
}

// Select is the messages to code: every correction, and every message the
// reference labels, so a coding is measured on the reference's own messages
// even where the correction reader misses them; the reference's labels
// never reach the model. It returns the reader's recall on the reference.
func Select(messages []Complaint, reference map[string]string) ([]Complaint, ReaderRecall) {
	selected := []Complaint{}
	recall := ReaderRecall{}
	for _, message := range messages {
		_, labeled := reference[message.Ref]
		if labeled {
			recall.Labeled++
			if message.Correction {
				recall.Corrections++
			}
		}
		if labeled || message.Correction {
			selected = append(selected, message)
		}
	}
	return selected, recall
}

// ReadTranscript reads one Claude Code session transcript, named source, for
// the operator's own messages (origin human) sent in [since, until), each
// with the agent's last text before it as its context and, as Correction,
// whether pkg/affect reads it as an operator correction: a complaint. A zero
// until is open-ended.
func ReadTranscript(source string, transcript io.Reader, since time.Time, until time.Time) ([]Complaint, error) {
	scanner := bufio.NewScanner(transcript)
	scanner.Buffer(make([]byte, 0, 64<<10), maxTranscriptLine)
	complaints := []Complaint{}
	agentSaid := ""
	for scanner.Scan() {
		var entry transcriptEntry
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		switch {
		case entry.Type == entryAssistant:
			if text := entry.text(); text != "" {
				agentSaid = text
			}
		case entry.Type == entryUser && entry.Origin != nil && entry.Origin.Kind == originHuman:
			text := entry.text()
			inWindow := !entry.Timestamp.Before(since) && (until.IsZero() || entry.Timestamp.Before(until))
			if inWindow {
				complaints = append(complaints, Complaint{Source: source, Ref: entry.UUID, At: entry.Timestamp, Text: text,
					Context: agentSaid, Correction: affect.Read(text).Correction})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failure codes: read transcript %s: %w", source, err)
	}
	return complaints, nil
}
