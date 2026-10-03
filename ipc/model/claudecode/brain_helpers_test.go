// Copyright 2026 Candace Labs

package claudecode_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/google/uuid"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/ipc/model/claudecode"
	"github.com/candacelabs/csf/ipc/proc"
)

// sessionID is the session every spec gives its brain.
var sessionID = uuid.MustParse("3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a01")

// Stream-json lines as Claude Code writes them, trimmed to the fields the
// specs care about.
const (
	initLine      = `{"type":"system","subtype":"init","session_id":"3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a01","tools":[]}`
	assistantLine = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"pong"}]},"session_id":"3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a01"}`
	resultLine    = `{"type":"result","subtype":"success","is_error":false,"result":"pong","session_id":"3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a01"}`
	userInput     = `{"type":"user","message":{"role":"user","content":"Reply with pong"}}`
)

// ownedPrefix is the argument vector every turn starts with.
var ownedPrefix = []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}

// claudeRun stands in for one Claude Code process: it records the standard
// input the brain wrote and writes lines to the standard output it was given.
type claudeRun struct {
	stdin   []byte
	command proc.Command
}

func (run *claudeRun) answer(lines ...string) func(ctx context.Context, command proc.Command) (proc.Result, error) {
	return func(ctx context.Context, command proc.Command) (proc.Result, error) {
		run.command = command
		stdin, err := io.ReadAll(command.Stdin)
		Expect(err).NotTo(HaveOccurred())
		run.stdin = stdin
		for _, line := range lines {
			_, err := io.WriteString(command.Stdout, line+"\n")
			Expect(err).NotTo(HaveOccurred())
		}
		return proc.Result{}, nil
	}
}

// logRecords decodes every JSON slog record written to buffer.
func logRecords(buffer *bytes.Buffer) []map[string]any {
	var records []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(buffer.Bytes()))
	for scanner.Scan() {
		// A JSON log record is an object of unknown shape: slog chooses it.
		var record map[string]any
		Expect(json.Unmarshal(scanner.Bytes(), &record)).To(Succeed())
		records = append(records, record)
	}
	return records
}

// messages are the records whose slog message is message.
func messages(records []map[string]any, message string) []map[string]any {
	var matched []map[string]any
	for _, record := range records {
		if record["msg"] == message {
			matched = append(matched, record)
		}
	}
	return matched
}

// rawLines are the events exactly as Claude Code wrote them.
func rawLines(events []claudecode.Event) []string {
	lines := make([]string, 0, len(events))
	for _, event := range events {
		lines = append(lines, string(event.Raw))
	}
	return lines
}
